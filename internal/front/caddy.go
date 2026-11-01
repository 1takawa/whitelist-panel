// Package front keeps the front proxy (Caddy) routes in sync with inbounds that Xray serves without TLS.
//
// Such inbounds (ws, httpupgrade, xhttp, gRPC with security none and public security tls) are reached through the
// panel domain: Caddy terminates TLS and forwards a path to the local Xray port. REALITY inbounds are not handled
// here because nginx routes them by SNI before Caddy sees the connection.
package front

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"anypanel2/internal/model"
)

// Caddy writes a routes file that the panel's site block imports, and reloads Caddy when it changes.
type Caddy struct {
	Path      string   // e.g. /etc/anypanel/routes/inbounds.caddy
	ReloadCmd []string // e.g. {"systemctl", "reload", "caddy"}

	mu   sync.Mutex
	last string
	run  func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func NewCaddy(path string, reloadCmd []string) *Caddy {
	return &Caddy{Path: path, ReloadCmd: reloadCmd, run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}}
}

// Fronted reports whether the inbound is reached through Caddy (plain on Xray, TLS added by the front proxy).
func Fronted(in model.Inbound) bool {
	if !in.Enable || in.Security != model.SecNone || in.Public.Security != model.SecTLS {
		return false
	}
	switch in.Network {
	case model.NetWS, model.NetHTTPUpgrade, model.NetXHTTP, model.NetGRPC:
		return true
	}
	return false
}

// BuildRoutes renders Caddy directives for all fronted inbounds, ordered by path for stable output.
func BuildRoutes(inbounds []model.Inbound) string {
	type route struct{ match, body string }
	var routes []route
	for _, in := range inbounds {
		if !Fronted(in) {
			continue
		}
		upstream := fmt.Sprintf("127.0.0.1:%d", in.Port)
		switch in.Network {
		case model.NetWS:
			routes = append(routes, route{path(in.WS.Path), "reverse_proxy " + upstream})
		case model.NetHTTPUpgrade:
			routes = append(routes, route{path(in.HTTPUpgrade.Path), "reverse_proxy " + upstream})
		case model.NetXHTTP:
			routes = append(routes, route{path(in.XHTTP.Path) + "*", "reverse_proxy h2c://" + upstream + " {\n\t\t\tflush_interval -1\n\t\t}"})
		case model.NetGRPC:
			routes = append(routes, route{"/" + in.GRPC.ServiceName + "/*", "reverse_proxy h2c://" + upstream + " {\n\t\t\tflush_interval -1\n\t\t}"})
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].match < routes[j].match })

	var b strings.Builder
	b.WriteString("# Managed by Whitelist Panel. Do not edit: this file is regenerated on every change.\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "handle %s {\n\t\t%s\n\t}\n", r.match, r.body)
	}
	return b.String()
}

func path(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// Apply writes the routes for the given inbounds and reloads Caddy if the content changed.
func (c *Caddy) Apply(ctx context.Context, inbounds []model.Inbound) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	content := BuildRoutes(inbounds)
	if content == c.last {
		return nil
	}
	if err := os.MkdirAll(dirOf(c.Path), 0o755); err != nil {
		return err
	}
	tmp := c.Path + ".new"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	// The panel runs with a restrictive umask, but Caddy (its own user) must be able to read the file.
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.Path); err != nil {
		return err
	}
	if len(c.ReloadCmd) > 0 {
		rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if out, err := c.run(rctx, c.ReloadCmd[0], c.ReloadCmd[1:]...); err != nil {
			return fmt.Errorf("caddy reload: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	c.last = content
	return nil
}

func dirOf(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[:i]
	}
	return "."
}
