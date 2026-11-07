//go:build !windows

package front

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The panel's service runs with a umask of 0077. Caddy and nginx are other users and must still read what the panel writes:
// with private files Caddy loaded no decoy site and every REALITY node on the main server failed its handshake.
func TestSNIFilesStayReadableUnderAStrictUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	e := newSNIEnv(t)
	if err := e.s.Apply(context.Background(), []Site{{Domain: "a.example.com", Port: 8444}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{e.s.mapFile(), e.s.upstreamsFile(), filepath.Join(e.s.SitesDir, "a.example.com.caddy")} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o644 {
			t.Fatalf("%s must be 0644 for the other users: %v %v", p, fi, err)
		}
	}
	for _, d := range []string{e.s.NginxDir, e.s.SitesDir} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o755 {
			t.Fatalf("%s must be 0755: %v %v", d, fi, err)
		}
	}
}
