package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVersionsAreComparedByNumbers(t *testing.T) {
	for _, c := range []struct {
		cur, lat string
		want     bool
	}{
		{"0.32.1", "0.32.2", true}, {"0.32.1", "0.33.0", true}, {"0.9.0", "0.10.0", true}, {"0.32.1", "1.0.0", true},
		{"0.32.1", "0.32.1", false}, {"0.32.2", "0.32.1", false}, {"0.10.0", "0.9.9", false},
		{"v0.32.1", "v0.32.2", true}, {"0.32.1-3-gabc", "0.32.2", true},
		{"dev", "0.32.2", false}, {"", "0.32.2", false}, {"0.32.1", "latest", false},
	} {
		if got := Newer(c.cur, c.lat); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.lat, got, c.want)
		}
	}
}

type fakeGitHub struct {
	srv     *httptest.Server
	mu      sync.Mutex
	api     int
	tag     string
	archive []byte
	sum     string
	noSum   bool
}

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func newGitHub(t *testing.T, tag string, files map[string]string) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{tag: tag, archive: tgz(t, files)}
	h := sha256.Sum256(g.archive)
	g.sum = hex.EncodeToString(h[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		g.api++
		g.mu.Unlock()
		_, _ = w.Write([]byte(`{"tag_name":"` + g.tag + `","body":"notes","html_url":"https://example.com/r","published_at":"2026-11-02T15:00:00Z","draft":false,"prerelease":false}`))
	})
	mux.HandleFunc("/o/r/releases/download/"+tag+"/"+asset, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(g.archive) })
	mux.HandleFunc("/o/r/releases/download/"+tag+"/"+asset+".sha256", func(w http.ResponseWriter, _ *http.Request) {
		if g.noSum {
			http.NotFound(w, nil)
			return
		}
		_, _ = w.Write([]byte(g.sum + "  " + asset + "\n"))
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

type runs struct {
	mu   sync.Mutex
	cmds []string
	ver  string
	fail map[string]error
}

func (r *runs) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := name + " " + strings.Join(args, " ")
	r.cmds = append(r.cmds, line)
	for k, err := range r.fail {
		if strings.Contains(line, k) {
			return []byte("boom"), err
		}
	}
	if strings.HasSuffix(name, "anypanel") {
		return []byte(r.ver + "\n"), nil
	}
	return nil, nil
}

func env(t *testing.T, files map[string]string, ver string) (*Updater, *fakeGitHub, *runs) {
	t.Helper()
	g := newGitHub(t, "v0.33.0", files)
	rs := &runs{ver: ver, fail: map[string]error{}}
	u := &Updater{Repo: "o/r", APIBase: g.srv.URL, DownloadBase: g.srv.URL, Dir: filepath.ToSlash(t.TempDir()), Run: rs.run}
	return u, g, rs
}

var good = map[string]string{"anypanel": "bin", "install.sh": "#!/bin/bash", "install-node.sh": "#!/bin/bash"}

func waitState(t *testing.T, u *Updater, want ...string) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := u.Status()
		for _, w := range want {
			if st.State == w {
				return st
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("state %+v, wanted one of %v", u.Status(), want)
	return State{}
}

func TestLatestReadsTheReleaseAndRemembersIt(t *testing.T) {
	u, g, _ := env(t, good, "0.33.0")
	r, err := u.Latest(context.Background(), false)
	if err != nil || r.Version != "0.33.0" || r.Tag != "v0.33.0" || r.Notes != "notes" || r.Published.IsZero() {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := u.Latest(context.Background(), false); err != nil || g.api != 1 {
		t.Fatalf("answered from memory: %d requests, %v", g.api, err)
	}
	g.tag = "v0.34.0"
	if r, _ := u.Latest(context.Background(), false); r.Tag != "v0.33.0" {
		t.Fatal("a plain check keeps the remembered answer")
	}
	now := time.Now()
	u.Now = func() time.Time { return now.Add(time.Minute) }
	if r, _ := u.Latest(context.Background(), true); r.Tag != "v0.34.0" {
		t.Fatalf("a check on request asks again after 20 seconds: %+v", r)
	}

	g.tag = "latest"
	u2 := &Updater{Repo: "o/r", APIBase: g.srv.URL}
	if _, err := u2.Latest(context.Background(), true); err == nil {
		t.Fatal("a tag that is not a version is refused")
	}
}

func TestApplyDownloadsChecksAndHandsOverToTheInstaller(t *testing.T) {
	u, _, rs := env(t, good, "0.33.0")
	rel, _ := u.Latest(context.Background(), false)
	if err := u.Apply(rel); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, u, "installing", "failed")
	if st.State != "installing" || st.Version != "0.33.0" {
		t.Fatalf("%+v", st)
	}
	var launched string
	for _, c := range rs.cmds {
		if strings.HasPrefix(c, "systemd-run") {
			launched = strings.ReplaceAll(c, "\\", "/") // paths use backslashes on Windows
		}
	}
	if !strings.Contains(launched, "--unit=wl-selfupdate") || !strings.Contains(launched, "install.sh upgrade") || !strings.Contains(launched, "v0.33.0/pkg") {
		t.Fatalf("the installer runs in a unit of its own, from the unpacked release: %q", launched)
	}
	for _, f := range wanted {
		fi, err := os.Stat(filepath.Join(u.Dir, "v0.33.0", "pkg", f))
		if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm()&0o100 == 0) { // Windows has no execute bit
			t.Fatalf("%s unpacked and executable: %v", f, err)
		}
	}
	if err := u.Apply(rel); err == nil {
		t.Fatal("one update at a time")
	}
}

func TestApplyRefusesWhatCannotBeTrusted(t *testing.T) {
	cases := map[string]func(t *testing.T) (*Updater, *runs){
		"wrong checksum": func(t *testing.T) (*Updater, *runs) {
			u, g, rs := env(t, good, "0.33.0")
			g.sum = strings.Repeat("0", 64)
			return u, rs
		},
		"no checksum file": func(t *testing.T) (*Updater, *runs) {
			u, g, rs := env(t, good, "0.33.0")
			g.noSum = true
			return u, rs
		},
		"a stray file in the archive": func(t *testing.T) (*Updater, *runs) {
			u, _, rs := env(t, map[string]string{"anypanel": "b", "install.sh": "s", "install-node.sh": "s", "../evil": "x"}, "0.33.0")
			return u, rs
		},
		"a missing file": func(t *testing.T) (*Updater, *runs) {
			u, _, rs := env(t, map[string]string{"anypanel": "b", "install.sh": "s"}, "0.33.0")
			return u, rs
		},
		"another version inside": func(t *testing.T) (*Updater, *runs) {
			u, _, rs := env(t, good, "0.31.0")
			return u, rs
		},
		"a binary that does not run": func(t *testing.T) (*Updater, *runs) {
			u, _, rs := env(t, good, "0.33.0")
			rs.fail["anypanel version"] = os.ErrPermission
			return u, rs
		},
		"no systemd": func(t *testing.T) (*Updater, *runs) {
			u, _, rs := env(t, good, "0.33.0")
			rs.fail["systemd-run"] = os.ErrNotExist
			return u, rs
		},
	}
	for name, mk := range cases {
		u, rs := mk(t)
		rel, err := u.Latest(context.Background(), false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := u.Apply(rel); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		st := waitState(t, u, "failed", "installing")
		if st.State != "failed" || st.Message == "" {
			t.Errorf("%s: %+v", name, st)
		}
		for _, c := range rs.cmds {
			if strings.HasPrefix(c, "systemd-run") && name != "no systemd" {
				t.Errorf("%s: the installer must not start: %s", name, c)
			}
		}
	}
}

func TestApplyRefusesBadInput(t *testing.T) {
	u, _, _ := env(t, good, "0.33.0")
	if err := u.Apply(&Release{Tag: "v0.33.0; rm -rf /"}); err == nil {
		t.Fatal("a tag that is not a version")
	}
	u.Dir = "/var/lib/x'y"
	if err := u.Apply(&Release{Tag: "v0.33.0"}); err == nil {
		t.Fatal("a directory with a quote in it would break out of the installer's command line")
	}
}

func TestLogTailKeepsTheLastLines(t *testing.T) {
	u := &Updater{Dir: t.TempDir()}
	if u.LogTail(5) != "" {
		t.Fatal("no log yet")
	}
	if err := os.WriteFile(filepath.Join(u.Dir, "last.log"), []byte("a\nb\nc\nd\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := u.LogTail(2); got != "c\nd" {
		t.Fatalf("%q", got)
	}
}
