package panel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"anypanel2/internal/update"
)

// githubWith answers the "latest release" request with the given tag and counts the requests.
func githubWith(t *testing.T, tag *atomic.Value, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example.test/r/%s","published_at":"2026-11-06T20:30:00Z","body":"notes"}`, tag.Load().(string), tag.Load().(string))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewReleaseIsReportedOnceEveryTwelveHours(t *testing.T) {
	ctx := context.Background()
	svc, _, fake := tgSetup(t)
	svc.Version = "0.40.0"
	var tag atomic.Value
	var calls atomic.Int32
	tag.Store("v0.40.0")
	gh := githubWith(t, &tag, &calls)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	svc.Update = &update.Updater{Repo: "owner/repo", APIBase: gh.URL, Dir: t.TempDir(), Now: svc.Now} // the release cache follows the same clock

	if sent, err := svc.UpdateNoticeTick(ctx); sent || err != nil || calls.Load() != 0 {
		t.Fatalf("no bot, no request to GitHub: %v %v %d", sent, err, calls.Load())
	}
	enableBot(t, svc)

	// The current version is the newest: GitHub is asked, nobody is told.
	if sent, err := svc.UpdateNoticeTick(ctx); sent || err != nil || calls.Load() != 1 {
		t.Fatalf("nothing new: %v %v %d", sent, err, calls.Load())
	}
	now = now.Add(11 * time.Hour)
	if _, _ = svc.UpdateNoticeTick(ctx); calls.Load() != 1 {
		t.Fatal("GitHub is asked once per 12 hours")
	}

	tag.Store("v0.41.0")
	now = now.Add(2 * time.Hour) // 13 hours since the first check
	sent, err := svc.UpdateNoticeTick(ctx)
	if !sent || err != nil || calls.Load() != 2 {
		t.Fatalf("a new release is reported: %v %v %d", sent, err, calls.Load())
	}
	texts := fake.texts()
	if len(texts) != 1 {
		t.Fatalf("%v", texts)
	}
	for _, want := range []string{"Доступно обновление панели", "Ваша версия: 0.40.0", "Новая версия: 0.41.0", "вышла 06.11.2026", "https://github.com/owner/repo/blob/main/CHANGELOG.md"} {
		if !strings.Contains(texts[0], want) {
			t.Errorf("the notice lacks %q:\n%s", want, texts[0])
		}
	}
	if strings.Contains(strings.ToLower(texts[0]), "anypanel") {
		t.Fatalf("the notice must not mention the old name:\n%s", texts[0])
	}

	// The same release is not announced again at the next check.
	now = now.Add(13 * time.Hour)
	if sent, _ := svc.UpdateNoticeTick(ctx); sent || len(fake.texts()) != 1 {
		t.Fatal("one notice per version")
	}
	tag.Store("v0.42.0")
	now = now.Add(13 * time.Hour)
	if sent, _ := svc.UpdateNoticeTick(ctx); !sent || len(fake.texts()) != 2 {
		t.Fatal("a still newer release is announced too")
	}
}

func TestUpdateCheckRetriesAfterAFailureAndSkipsDevBuilds(t *testing.T) {
	ctx := context.Background()
	svc, _, fake := tgSetup(t)
	enableBot(t, svc)
	var tag atomic.Value
	var calls atomic.Int32
	tag.Store("v9.0.0")
	gh := githubWith(t, &tag, &calls)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	svc.Update = &update.Updater{Repo: "owner/repo", APIBase: gh.URL, Dir: t.TempDir(), Now: svc.Now} // the release cache follows the same clock

	svc.Version = "dev"
	if sent, err := svc.UpdateNoticeTick(ctx); sent || err != nil || calls.Load() != 0 {
		t.Fatal("a development build is not compared with releases")
	}

	svc.Version = "0.40.0"
	fake.mu.Lock()
	fake.rejectMsgs = true
	fake.mu.Unlock()
	if sent, err := svc.UpdateNoticeTick(ctx); sent || err == nil {
		t.Fatalf("a failed delivery is an error and is not remembered: %v %v", sent, err)
	}
	if sent, err := svc.UpdateNoticeTick(ctx); sent || err != nil {
		t.Fatal("no retry before the pause is over")
	}
	fake.mu.Lock()
	fake.rejectMsgs = false
	fake.mu.Unlock()
	now = now.Add(updateRetry + time.Minute)
	if sent, err := svc.UpdateNoticeTick(ctx); !sent || err != nil {
		t.Fatalf("the retry delivers it: %v %v", sent, err)
	}
}
