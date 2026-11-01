package nodeinstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const nodeStream = `map $ssl_preread_server_name $sni_name {
    hostnames;
    node.example.com      xray;
    default   xray;
}

upstream xray {
    server 127.0.0.1:8443;
}

server {
    proxy_protocol on;
    listen          443;
    proxy_pass      $sni_name;
    ssl_preread     on;
}
`

func runSNI(t *testing.T, dir string, sites []SNISite, nginxFails bool) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	script, err := SNIScript(sites)
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "nginx")
	body := "#!/bin/sh\nexit 0\n"
	if nginxFails {
		body = "#!/bin/sh\necho 'unknown directive in test'\nexit 1\n"
	}
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(), "ANYPANEL_NGINX_STREAM="+filepath.ToSlash(filepath.Join(dir, "stream.conf")),
		"ANYPANEL_SNI_DIR="+filepath.ToSlash(filepath.Join(dir, "sni")), "ANYPANEL_NGINX_BIN="+filepath.ToSlash(fake))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSNIScriptAddsDomainsToTheNodesNginxAndUndoesARejectedChange(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "stream.conf")
	if err := os.WriteFile(conf, []byte(nodeStream), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	out, err := runSNI(t, dir, []SNISite{{"b.example.com", 8445}, {"a.example.com", 8444}}, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	m, u := read(filepath.Join(dir, "sni", "sni-map.conf")), read(filepath.Join(dir, "sni", "sni-upstreams.conf"))
	if !strings.Contains(m, "a.example.com  c8444;") || !strings.Contains(m, "b.example.com  c8445;") || strings.Index(m, "a.example.com") > strings.Index(m, "b.example.com") {
		t.Fatalf("map:\n%s", m)
	}
	if !strings.Contains(u, "upstream c8444 { server 127.0.0.1:8444; }") || !strings.Contains(u, "upstream c8445 { server 127.0.0.1:8445; }") {
		t.Fatalf("upstreams:\n%s", u)
	}
	c := read(conf)
	mapAt, openAt, defAt := strings.Index(c, "sni-map.conf"), strings.Index(c, "map $ssl_preread_server_name"), strings.Index(c, "default   xray;")
	if strings.Count(c, "sni-map.conf") != 1 || strings.Count(c, "sni-upstreams.conf") != 1 || mapAt < openAt || mapAt > defAt ||
		strings.Index(c, "sni-upstreams.conf") > strings.Index(c, "upstream xray") || !strings.Contains(c, "node.example.com      xray;") {
		t.Fatalf("the node's own lines stay and the includes sit in the right blocks:\n%s", c)
	}
	if read(conf+".anypanel-sni-bak") != nodeStream {
		t.Fatal("the original is kept")
	}

	// Again with one site fewer: still one include of each, the removed domain is gone.
	if out, err := runSNI(t, dir, []SNISite{{"b.example.com", 8445}}, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(read(filepath.Join(dir, "sni", "sni-map.conf")), "a.example.com") || strings.Count(read(conf), "sni-map.conf") != 1 {
		t.Fatal("idempotent")
	}

	// nginx says no: everything is as it was before this call.
	before := read(filepath.Join(dir, "sni", "sni-map.conf"))
	out, err = runSNI(t, dir, []SNISite{{"c.example.com", 8446}}, true)
	if err == nil || !strings.Contains(out, "nginx отклонил") || !strings.Contains(out, "unknown directive") {
		t.Fatalf("%v\n%s", err, out)
	}
	if read(filepath.Join(dir, "sni", "sni-map.conf")) != before || strings.Contains(read(filepath.Join(dir, "sni", "sni-map.conf")), "c.example.com") {
		t.Fatal("the rejected change is undone")
	}
}

func TestSNIScriptRefusesAnUnknownLayoutAndBadInput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stream.conf"), []byte("stream_stuff;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runSNI(t, dir, []SNISite{{"a.example.com", 8444}}, false)
	if err == nil || !strings.Contains(out, "не в формате установщика") {
		t.Fatalf("%v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "stream.conf")); string(b) != "stream_stuff;\n" {
		t.Fatal("a file in a layout we do not know is left alone")
	}
	if _, err := SNIScript([]SNISite{{"bad domain; rm -rf /", 8444}}); err == nil {
		t.Fatal("domains are validated before they get into a script")
	}
	if _, err := SNIScript([]SNISite{{"a.example.com", 0}}); err == nil {
		t.Fatal("ports are validated")
	}
}
