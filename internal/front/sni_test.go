package front

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const installerNginx = `user www-data;
events { worker_connections 4096; }

stream {
    map $ssl_preread_server_name $anypanel_upstream {
        reality.example.com  reality;
        default            web;
    }
    upstream reality { server 127.0.0.1:8443; }
    upstream web     { server 127.0.0.1:7443; }

    server {
        listen 443 reuseport;
        ssl_preread on;
        proxy_pass $anypanel_upstream;
        proxy_protocol on;
    }
}
`

const installerCaddy = `{
    https_port 7443
}

panel.example.com {
    reverse_proxy 127.0.0.1:28270
}

reality.example.com {
    root * /var/www/anypanel-decoy
    file_server
}
`

type sniEnv struct {
	s    *SNI
	dir  string
	cmds *[]string
	fail map[string]bool
}

func newSNIEnv(t *testing.T) sniEnv {
	t.Helper()
	dir := t.TempDir()
	must := func(p, data string) string {
		p = filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	s := NewSNI(must("nginx.conf", installerNginx), must("Caddyfile", installerCaddy), filepath.Join(dir, "anypanel-nginx"), filepath.Join(dir, "anypanel-sites"), "/var/www/decoy",
		[]string{"systemctl", "reload", "nginx"}, []string{"systemctl", "reload", "caddy"})
	cmds := &[]string{}
	fail := map[string]bool{}
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		line := name + " " + strings.Join(args, " ")
		*cmds = append(*cmds, line)
		for k := range fail {
			if strings.Contains(line, k) {
				return []byte("boom: " + k), errors.New("exit 1")
			}
		}
		return nil, nil
	}
	return sniEnv{s: s, dir: dir, cmds: cmds, fail: fail}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSNIWritesIncludesFilesAndReloadsAfterChecking(t *testing.T) {
	e := newSNIEnv(t)
	sites := []Site{{Domain: "b.example.com", Port: 8445}, {Domain: "a.example.com", Port: 8444}}
	if err := e.s.Apply(context.Background(), sites); err != nil {
		t.Fatal(err)
	}

	m := read(t, e.s.mapFile())
	if !strings.Contains(m, "a.example.com  c8444;") || !strings.Contains(m, "b.example.com  c8445;") || strings.Index(m, "a.example.com") > strings.Index(m, "b.example.com") {
		t.Fatalf("map lines, sorted:\n%s", m)
	}
	if u := read(t, e.s.upstreamsFile()); !strings.Contains(u, "upstream c8444 { server 127.0.0.1:8444; }") || !strings.Contains(u, "upstream c8445 { server 127.0.0.1:8445; }") {
		t.Fatalf("upstreams:\n%s", u)
	}
	site := read(t, filepath.Join(e.s.SitesDir, "a.example.com.caddy"))
	if !strings.Contains(site, "a.example.com {") || !strings.Contains(site, "root * /var/www/decoy") || !strings.Contains(site, "file_server") {
		t.Fatalf("decoy site:\n%s", site)
	}

	// The installer's files keep everything they had and gain only the include lines.
	ng := read(t, e.s.NginxConf)
	if !strings.Contains(ng, "reality.example.com  reality;") || !strings.Contains(ng, "default            web;") || !strings.Contains(ng, "upstream web     { server 127.0.0.1:7443; }") {
		t.Fatalf("the installer's lines survive:\n%s", ng)
	}
	if strings.Count(ng, e.s.mapFile()) != 1 || strings.Count(ng, e.s.upstreamsFile()) != 1 {
		t.Fatalf("one include of each:\n%s", ng)
	}
	if mapAt, openAt := strings.Index(ng, e.s.mapFile()), strings.Index(ng, "map $ssl_preread_server_name"); mapAt < openAt || mapAt > strings.Index(ng, "default            web;") {
		t.Fatalf("the map include sits inside the map block:\n%s", ng)
	}
	if cd := read(t, e.s.Caddyfile); !strings.Contains(cd, "import "+filepath.Join(e.s.SitesDir, "*.caddy")) || !strings.Contains(cd, "reality.example.com {") {
		t.Fatalf("Caddyfile:\n%s", cd)
	}
	for _, p := range []string{e.s.NginxConf + ".anypanel-sni-bak", e.s.Caddyfile + ".anypanel-sni-bak"} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("the original is kept: %v", err)
		}
	}
	if bak := read(t, e.s.NginxConf+".anypanel-sni-bak"); bak != installerNginx {
		t.Fatal("the backup is the installer's original")
	}

	// Checked first, then reloaded, Caddy before nginx.
	got := strings.Join(*e.cmds, " | ")
	if !strings.Contains(got, "nginx -t") || !strings.Contains(got, "caddy validate") || strings.Index(got, "nginx -t") > strings.Index(got, "systemctl reload caddy") ||
		strings.Index(got, "systemctl reload caddy") > strings.Index(got, "systemctl reload nginx") {
		t.Fatalf("order of commands: %s", got)
	}
	if runtime.GOOS == "windows" {
		return // Windows has no Unix permission bits
	}
	info, _ := os.Stat(e.s.mapFile())
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("nginx and Caddy run as other users, the files must be readable: %v", info.Mode())
	}
}

func TestSNIIsIdempotentAndRemovesSitesOfGoneCascades(t *testing.T) {
	e := newSNIEnv(t)
	ctx := context.Background()
	_ = e.s.Apply(ctx, []Site{{Domain: "a.example.com", Port: 8444}, {Domain: "b.example.com", Port: 8445}})
	n := len(*e.cmds)
	if err := e.s.Apply(ctx, []Site{{Domain: "b.example.com", Port: 8445}, {Domain: "a.example.com", Port: 8444}}); err != nil || len(*e.cmds) != n {
		t.Fatalf("the same sites change nothing and reload nothing: %v (%d new commands)", err, len(*e.cmds)-n)
	}
	if err := e.s.Apply(ctx, []Site{{Domain: "b.example.com", Port: 8445}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.s.SitesDir, "a.example.com.caddy")); err == nil {
		t.Fatal("the decoy site of a removed cascade goes away")
	}
	if strings.Contains(read(t, e.s.mapFile()), "a.example.com") || strings.Contains(read(t, e.s.upstreamsFile()), "c8444") {
		t.Fatal("and so do its map line and upstream")
	}
	// A file somebody else put there is not ours to delete.
	foreign := filepath.Join(e.s.SitesDir, "mine.caddy")
	_ = os.WriteFile(foreign, []byte("mine.example.com { respond 200 }\n"), 0o644)
	_ = e.s.Apply(ctx, nil)
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("only files the panel wrote are removed")
	}
	// Two cascades can never share an upstream name by accident.
	m, u, _ := e.s.Render([]Site{{Domain: "x.example.com", Port: 8444}, {Domain: "y.example.com", Port: 8444}})
	if strings.Count(u, "upstream c8444") != 1 || !strings.Contains(m, "x.example.com  c8444;") || !strings.Contains(m, "y.example.com  c8444;") {
		t.Fatalf("%s\n%s", m, u)
	}
}

func TestSNIPutsEverythingBackWhenACheckFails(t *testing.T) {
	for _, bad := range []string{"nginx -t", "caddy validate"} {
		e := newSNIEnv(t)
		e.fail[bad] = true
		before := [3]string{read(t, e.s.NginxConf), read(t, e.s.Caddyfile)}
		err := e.s.Apply(context.Background(), []Site{{Domain: "a.example.com", Port: 8444}})
		if err == nil || !strings.Contains(err.Error(), "nothing was changed") || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("%s: %v", bad, err)
		}
		if read(t, e.s.NginxConf) != before[0] || read(t, e.s.Caddyfile) != before[1] {
			t.Fatalf("%s: the installer's files are back as they were", bad)
		}
		for _, p := range []string{e.s.mapFile(), e.s.upstreamsFile(), filepath.Join(e.s.SitesDir, "a.example.com.caddy")} {
			if _, err := os.Stat(p); err == nil {
				t.Fatalf("%s: %s must not be left behind", bad, p)
			}
		}
		for _, c := range *e.cmds {
			if strings.Contains(c, "reload") {
				t.Fatalf("%s: a rejected configuration is never reloaded: %v", bad, *e.cmds)
			}
		}
		// And once the problem is gone the same change goes through.
		delete(e.fail, bad)
		if err := e.s.Apply(context.Background(), []Site{{Domain: "a.example.com", Port: 8444}}); err != nil {
			t.Fatalf("%s: %v", bad, err)
		}
	}
}

func TestSNIRefusesAnUnknownNginxLayoutInsteadOfGuessing(t *testing.T) {
	e := newSNIEnv(t)
	_ = os.WriteFile(e.s.NginxConf, []byte("events {}\nstream { server { listen 443; proxy_pass x; } }\n"), 0o644)
	err := e.s.Apply(context.Background(), []Site{{Domain: "a.example.com", Port: 8444}})
	if err == nil || !strings.Contains(err.Error(), "installer") {
		t.Fatalf("%v", err)
	}
	if strings.Contains(read(t, e.s.NginxConf), "include") {
		t.Fatal("a file in a layout we do not know is not edited")
	}
}

func TestSNIServesAWebDomainThroughCaddyOnly(t *testing.T) {
	e := newSNIEnv(t)
	if err := e.s.Apply(context.Background(), []Site{{Domain: "a.example.com", Port: 8444}, {Domain: "subs.example.com", Proxy: "127.0.0.1:28270"}}); err != nil {
		t.Fatal(err)
	}
	if m := read(t, e.s.mapFile()); strings.Contains(m, "subs.example.com") {
		t.Fatalf("nginx sends a web domain to Caddy like any unknown name, it has no line in the map:\n%s", m)
	}
	site := read(t, filepath.Join(e.s.SitesDir, "subs.example.com.caddy"))
	if !strings.Contains(site, "subs.example.com {") || !strings.Contains(site, "reverse_proxy 127.0.0.1:28270") || strings.Contains(site, "file_server") {
		t.Fatalf("a site that leads to the panel:\n%s", site)
	}
	// The domain removed: its file goes.
	if err := e.s.Apply(context.Background(), []Site{{Domain: "a.example.com", Port: 8444}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.s.SitesDir, "subs.example.com.caddy")); err == nil {
		t.Fatal("the site of a removed domain is deleted")
	}
}
