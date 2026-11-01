//go:build !windows

package front

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"anypanel2/internal/model"
)

func TestRoutesFileIsReadableByCaddyDespiteUmask(t *testing.T) {
	old := syscall.Umask(0o077) // what the systemd unit of the panel sets
	defer syscall.Umask(old)

	path := filepath.Join(t.TempDir(), "r.caddy")
	c := NewCaddy(path, nil)
	if err := c.Apply(context.Background(), []model.Inbound{ws("/a", 1)}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("routes file must be world-readable for the caddy user, got %v", st.Mode().Perm())
	}
}
