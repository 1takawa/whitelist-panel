package routing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"anypanel2/internal/sub"
)

func TestBuiltInCopiesAreValidForBothAppsAndDoNotNeedTheNetwork(t *testing.T) {
	var s Source // zero value: no network
	for _, app := range []string{"happ", "incy"} {
		ps, err := s.Presets(context.Background(), app, false)
		if err != nil || len(ps) != 3 || ps[0].ID != "default" || ps[1].ID != "whitelist" || ps[2].ID != RussianID {
			t.Fatalf("%s: %+v %v", app, ps, err)
		}
		for _, p := range ps {
			if p.Source != "embedded" || !strings.HasPrefix(p.Link, app+"://routing/onadd/") || p.Name == "" || p.Updated == nil {
				t.Fatalf("%s/%s: %+v", app, p.ID, p)
			}
			if prof, err := decode(p.Link, app); err != nil || prof.Geoipurl == "" || prof.Geositeurl == "" {
				t.Fatalf("%s/%s: %v", app, p.ID, err)
			}
		}
	}
	if _, err := s.Presets(context.Background(), "clash", false); err == nil {
		t.Fatal("unknown app")
	}
}

// upstream serves the built-in copy under a name, as GitHub would, and counts the requests.
func upstream(t *testing.T, mutate func(path, body string) (int, string)) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		app := strings.ToLower(strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0])
		id := strings.ToLower(strings.TrimSuffix(strings.Split(r.URL.Path, "/")[2], ".DEEPLINK"))
		code, body := 200, embedded(app, id)
		if mutate != nil {
			code, body = mutate(r.URL.Path, body)
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body + "\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestFreshProfilesComeFromGitHubAndAreCachedForAnHour(t *testing.T) {
	srv, n := upstream(t, nil)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	s := &Source{Base: srv.URL, Now: func() time.Time { return now }}
	ps, _ := s.Presets(context.Background(), "happ", false)
	if ps[0].Source != "github" || n.Load() != 2 {
		t.Fatalf("both profiles are read: %+v requests=%d", ps, n.Load())
	}
	_, _ = s.Presets(context.Background(), "happ", false)
	if n.Load() != 2 {
		t.Fatalf("a repeat within the hour is served from memory: %d", n.Load())
	}
	now = now.Add(61 * time.Minute)
	_, _ = s.Presets(context.Background(), "happ", false)
	if n.Load() != 4 {
		t.Fatalf("after an hour it is read again: %d", n.Load())
	}
}

func TestBrokenOrUnreachableUpstreamFallsBackWithoutBreakingThePage(t *testing.T) {
	cases := map[string]func(path, body string) (int, string){
		"404":            func(_, b string) (int, string) { return 404, "not found" },
		"garbage":        func(_, b string) (int, string) { return 200, "hello" },
		"wrong app":      func(_, b string) (int, string) { return 200, strings.Replace(b, "happ://", "incy://", 1) },
		"no geo https":   func(_, b string) (int, string) { return 200, "happ://routing/onadd/e30=" },
		"too large":      func(_, b string) (int, string) { return 200, b + strings.Repeat("A", 70<<10) },
		"bad base64":     func(_, b string) (int, string) { return 200, "happ://routing/onadd/@@@" },
		"html error doc": func(_, b string) (int, string) { return 200, "<html>blocked</html>" },
	}
	for name, mut := range cases {
		srv, _ := upstream(t, mut)
		s := &Source{Base: srv.URL}
		ps, err := s.Presets(context.Background(), "happ", false)
		if err != nil || ps[0].Source != "embedded" || ps[0].Link != embedded("happ", "default") {
			t.Errorf("%s: the built-in copy must be used: %+v %v", name, ps[0], err)
		}
	}
	dead := &Source{Base: "http://127.0.0.1:1"}
	if ps, _ := dead.Presets(context.Background(), "incy", false); ps[0].Source != "embedded" {
		t.Fatalf("unreachable: %+v", ps[0])
	}
}

func TestAnOlderDownloadIsKeptWhenTheNextOneFails(t *testing.T) {
	fail := false
	srv, _ := upstream(t, func(_, b string) (int, string) {
		if fail {
			return 500, "boom"
		}
		return 200, b
	})
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	s := &Source{Base: srv.URL, Now: func() time.Time { return now }}
	_, _ = s.Presets(context.Background(), "happ", false)
	fail, now = true, now.Add(2*time.Hour)
	ps, _ := s.Presets(context.Background(), "happ", false)
	if ps[0].Source != "github" {
		t.Fatalf("the last good download is better than the built-in copy: %+v", ps[0])
	}
}

// What the panel stores and serves must accept the published links as they are.
func TestPublishedLinksPassThePanelsOwnChecks(t *testing.T) {
	var s Source
	happ, _ := s.Presets(context.Background(), "happ", false)
	incy, _ := s.Presets(context.Background(), "incy", false)
	for _, p := range happ {
		out, err := sub.HappRoutingLink(p.Link)
		if err != nil || out != p.Link {
			t.Errorf("Happ %s: %v (the Routing header must carry the link unchanged)", p.ID, err)
		}
	}
	for _, p := range incy {
		line, err := sub.IncyRoutingLine(p.Link)
		if err != nil || line != p.Link {
			t.Errorf("INCY %s: %v", p.ID, err)
		}
	}
}

func TestRefreshAsksGitHubAgainButNotMoreOftenThanEvery30Seconds(t *testing.T) {
	srv, n := upstream(t, nil)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	s := &Source{Base: srv.URL, Now: func() time.Time { return now }}
	_, _ = s.Presets(context.Background(), "happ", false)
	if n.Load() != 2 {
		t.Fatalf("%d", n.Load())
	}
	now = now.Add(10 * time.Second)
	_, _ = s.Presets(context.Background(), "happ", true)
	if n.Load() != 2 {
		t.Fatalf("a button pressed again at once does not hammer GitHub: %d", n.Load())
	}
	now = now.Add(25 * time.Second)
	_, _ = s.Presets(context.Background(), "happ", true)
	if n.Load() != 4 {
		t.Fatalf("a refresh after the pause reads the repository again: %d", n.Load())
	}
	now = now.Add(5 * time.Minute)
	_, _ = s.Presets(context.Background(), "happ", false)
	if n.Load() != 4 {
		t.Fatalf("an ordinary load still trusts the hour of memory: %d", n.Load())
	}
}

func TestWhitelistRUIsDefaultUnderItsOwnNameWithTheRussianNamesAdded(t *testing.T) {
	var s Source
	for _, app := range []string{"happ", "incy"} {
		ps, _ := s.Presets(context.Background(), app, false)
		def, ru := ps[0], ps[2]
		if ru.Name != RussianName || ru.Name == def.Name {
			t.Fatalf("%s: the apps must show whitelist_RU, got %q", app, ru.Name)
		}
		d, err := decode(def.Link, app)
		if err != nil {
			t.Fatal(err)
		}
		r, err := decode(ru.Link, app)
		if err != nil {
			t.Fatal(err)
		}
		if r.Geoipurl != d.Geoipurl || r.Geositeurl != d.Geositeurl || r.LastUpdated != d.LastUpdated {
			t.Fatalf("%s: same geo files and date as DEFAULT: %+v %+v", app, r, d)
		}
		direct := strings.Join(r.DirectSites, ",")
		for _, want := range append([]string{"geosite:category-ru"}, RussianDirect...) {
			if !strings.Contains(direct, want) {
				t.Fatalf("%s: %s is not sent direct: %s", app, want, direct)
			}
		}
		if len(r.DirectSites) != len(d.DirectSites)+len(RussianDirect) {
			t.Fatalf("%s: exactly the Russian names are added", app)
		}
		// Asking again must not add them twice, and the link must pass the panel's own checks.
		again, err := whitelistRU(ru.Link, app)
		if err != nil || again != ru.Link {
			t.Fatalf("%s: idempotent: %v", app, err)
		}
		if app == "happ" {
			if out, err := sub.HappRoutingLink(ru.Link); err != nil || out != ru.Link {
				t.Fatalf("%v", err)
			}
		} else if line, err := sub.IncyRoutingLine(ru.Link); err != nil || line != ru.Link {
			t.Fatalf("%v", err)
		}
	}
}
