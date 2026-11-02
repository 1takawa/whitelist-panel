package front

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// SNI lets the main server serve several REALITY domains on port 443. nginx (stream) takes 443 and picks the Xray port by
// the name the client asks for; every extra domain therefore needs a line in nginx's map, an upstream towards its own
// Xray port and a decoy site (Caddy, with a real certificate) for REALITY to show to strangers.
//
// The panel owns three small places and leaves everything else of both programs alone:
//
//	<NginxDir>/sni-map.conf        lines "domain  cPORT;" included into the installer's map
//	<NginxDir>/sni-upstreams.conf  "upstream cPORT { server 127.0.0.1:PORT; }" included next to the installer's upstreams
//	<SitesDir>/<domain>.caddy      one decoy site per domain, imported by the Caddyfile
//
// On first use it adds the three include lines to nginx.conf and the Caddyfile (keeping a backup of each). A change is
// written, checked with `nginx -t` and `caddy validate`, and only then reloaded; if a check fails the previous files are
// put back, so a bad domain can never take the panel's own site down.
type SNI struct {
	NginxConf   string   // /etc/nginx/nginx.conf
	Caddyfile   string   // /etc/caddy/Caddyfile
	NginxDir    string   // directory for the two nginx include files
	SitesDir    string   // directory for the Caddy site files
	DecoyDir    string   // document root of the decoy sites
	NginxReload []string // e.g. systemctl reload nginx
	CaddyReload []string // e.g. systemctl reload caddy

	mu   sync.Mutex
	last [32]byte
	run  func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Site is one REALITY domain served behind nginx: Port is the loopback port of its Xray inbound.
type Site struct {
	Domain string
	Port   int
}

// isManaged reports whether a file starts with the marker the panel writes into the files it owns. Files written by older
// releases carry another product name in the same marker, so only the shape of the line is checked.
func isManaged(content string) bool {
	first, _, _ := strings.Cut(content, "\n")
	return strings.HasPrefix(first, "# Managed by ") && strings.HasSuffix(first, "Do not edit: this file is regenerated on every change.")
}

const sniHeader = "# Managed by Whitelist Panel. Do not edit: this file is regenerated on every change.\n"

func NewSNI(nginxConf, caddyfile, nginxDir, sitesDir, decoyDir string, nginxReload, caddyReload []string) *SNI {
	return &SNI{NginxConf: nginxConf, Caddyfile: caddyfile, NginxDir: nginxDir, SitesDir: sitesDir, DecoyDir: decoyDir,
		NginxReload: nginxReload, CaddyReload: caddyReload,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}}
}

func (s *SNI) mapFile() string       { return filepath.Join(s.NginxDir, "sni-map.conf") }
func (s *SNI) upstreamsFile() string { return filepath.Join(s.NginxDir, "sni-upstreams.conf") }

func upstreamName(port int) string { return fmt.Sprintf("c%d", port) }

// Render returns the contents of the three kinds of files for the sites.
func (s *SNI) Render(sites []Site) (mapConf, upstreams string, caddy map[string]string) {
	sorted := append([]Site(nil), sites...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Domain < sorted[j].Domain })
	var m, u strings.Builder
	m.WriteString(sniHeader)
	u.WriteString(sniHeader)
	caddy = map[string]string{}
	seen := map[int]bool{}
	for _, st := range sorted {
		fmt.Fprintf(&m, "    %s  %s;\n", st.Domain, upstreamName(st.Port))
		if !seen[st.Port] {
			seen[st.Port] = true
			fmt.Fprintf(&u, "    upstream %s { server 127.0.0.1:%d; }\n", upstreamName(st.Port), st.Port)
		}
		caddy[st.Domain+".caddy"] = fmt.Sprintf("%s%s {\n    root * %s\n    file_server\n}\n", sniHeader, st.Domain, s.DecoyDir)
	}
	return m.String(), u.String(), caddy
}

// patch adds a line after the first line that starts with marker (once; a second call finds it there already).
func patch(content, marker, line string) (string, bool, error) {
	if strings.Contains(content, line) {
		return content, false, nil
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), marker) {
			out := append(append(append([]string{}, lines[:i+1]...), line), lines[i+1:]...)
			return strings.Join(out, "\n"), true, nil
		}
	}
	return content, false, fmt.Errorf("the layout of the file is not the installer's: no line starting with %q", marker)
}

// Apply makes the three places match the sites. It returns nil without doing anything when nothing changed.
func (s *SNI) Apply(ctx context.Context, sites []Site) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mapConf, upstreams, caddy := s.Render(sites)
	h := sha256.New()
	h.Write([]byte(mapConf + "\x00" + upstreams))
	keys := make([]string, 0, len(caddy))
	for k := range caddy {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte("\x00" + k + "\x00" + caddy[k]))
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	if sum == s.last && s.includesPresent() {
		return nil
	}

	// Remember everything to put back if a check fails.
	type saved struct {
		path string
		data []byte
		had  bool
	}
	var undo []saved
	remember := func(path string) {
		b, err := os.ReadFile(path)
		undo = append(undo, saved{path, b, err == nil})
	}
	restore := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			u := undo[i]
			if u.had {
				_ = writeShared(u.path, u.data)
			} else {
				_ = os.Remove(u.path)
			}
		}
	}

	if err := mkShared(s.NginxDir); err != nil {
		return err
	}
	if err := mkShared(s.SitesDir); err != nil {
		return err
	}
	write := func(path, data string) error {
		remember(path)
		return writeShared(path, []byte(data))
	}

	// 1. The include lines in the installer's own files (once, with a backup).
	if err := s.patchFile(s.NginxConf, []struct{ marker, line string }{
		{"map $ssl_preread_server_name", "        include " + s.mapFile() + ";"},
		{"upstream web", "    include " + s.upstreamsFile() + ";"},
	}, remember); err != nil {
		restore()
		return fmt.Errorf("nginx.conf: %w", err)
	}
	if err := s.appendOnce(s.Caddyfile, "import "+filepath.Join(s.SitesDir, "*.caddy"), remember); err != nil {
		restore()
		return fmt.Errorf("Caddyfile: %w", err)
	}

	// 2. Our files.
	if err := write(s.mapFile(), mapConf); err != nil {
		restore()
		return err
	}
	if err := write(s.upstreamsFile(), upstreams); err != nil {
		restore()
		return err
	}
	keep := map[string]bool{"00-anypanel.caddy": true}
	if err := write(filepath.Join(s.SitesDir, "00-anypanel.caddy"), sniHeader); err != nil { // the import glob always matches something
		restore()
		return err
	}
	for name, data := range caddy {
		keep[name] = true
		if err := write(filepath.Join(s.SitesDir, name), data); err != nil {
			restore()
			return err
		}
	}
	if entries, err := os.ReadDir(s.SitesDir); err == nil { // sites of cascades that are gone
		for _, e := range entries {
			if p := filepath.Join(s.SitesDir, e.Name()); !keep[e.Name()] && strings.HasSuffix(e.Name(), ".caddy") {
				if b, err := os.ReadFile(p); err == nil && isManaged(string(b)) {
					remember(p)
					_ = os.Remove(p)
				}
			}
		}
	}

	// 3. Check both programs on the new files; only then touch the running ones.
	if out, err := s.run(ctx, "nginx", "-t"); err != nil {
		restore()
		return fmt.Errorf("nginx rejected the configuration, nothing was changed: %s", strings.TrimSpace(string(out)))
	}
	if out, err := s.run(ctx, "caddy", "validate", "--config", s.Caddyfile, "--adapter", "caddyfile"); err != nil {
		restore()
		return fmt.Errorf("Caddy rejected the configuration, nothing was changed: %s", lastLines(string(out), 6))
	}
	for _, cmd := range [][]string{s.CaddyReload, s.NginxReload} {
		if len(cmd) == 0 {
			continue
		}
		if out, err := s.run(ctx, cmd[0], cmd[1:]...); err != nil {
			return fmt.Errorf("%s failed: %v: %s", strings.Join(cmd, " "), err, strings.TrimSpace(string(out)))
		}
	}
	s.last = sum
	return nil
}

func (s *SNI) includesPresent() bool {
	n, err1 := os.ReadFile(s.NginxConf)
	c, err2 := os.ReadFile(s.Caddyfile)
	return err1 == nil && err2 == nil && strings.Contains(string(n), s.mapFile()) && strings.Contains(string(n), s.upstreamsFile()) &&
		strings.Contains(string(c), filepath.Join(s.SitesDir, "*.caddy"))
}

func (s *SNI) patchFile(path string, edits []struct{ marker, line string }, remember func(string)) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content, changed := string(b), false
	for _, e := range edits {
		var did bool
		if content, did, err = patch(content, e.marker, e.line); err != nil {
			return err
		}
		changed = changed || did
	}
	if !changed {
		return nil
	}
	if _, err := os.Stat(path + ".anypanel-sni-bak"); errors.Is(err, os.ErrNotExist) {
		_ = writeShared(path+".anypanel-sni-bak", b)
	}
	remember(path)
	return writeShared(path, []byte(content))
}

func (s *SNI) appendOnce(path, line string, remember func(string)) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), line) {
		return nil
	}
	if _, err := os.Stat(path + ".anypanel-sni-bak"); errors.Is(err, os.ErrNotExist) {
		_ = writeShared(path+".anypanel-sni-bak", b)
	}
	remember(path)
	return writeShared(path, []byte(strings.TrimRight(string(b), "\n")+"\n\n# Decoy sites of the cascades (managed by Whitelist Panel)\n"+line+"\n"))
}

// writeShared writes a file that nginx and Caddy read. They run as other users than the panel, and the panel's service
// runs with a umask of 0077, which would turn the 0644 of os.WriteFile into 0600: Caddy then silently loaded no decoy
// sites at all. So the mode is set explicitly.
func writeShared(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return os.Chmod(path, 0o644)
}

// mkShared makes a directory those programs can enter, for the same reason (and fixes one an older version made private).
func mkShared(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.Chmod(dir, 0o755)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
