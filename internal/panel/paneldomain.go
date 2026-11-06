package panel

import (
	"context"
	"net/url"
	"strings"
)

// HostOfURL is the domain name in an address such as https://panel.example.com, "" for an address without a domain name
// (an IP address, localhost, an empty value).
func HostOfURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if h == "" || !strings.Contains(h, ".") || !isHost(h) {
		return ""
	}
	for _, r := range h {
		if r != '.' && r != '-' && (r < '0' || r > '9') {
			return h // at least one letter: a name, not an IPv4 address
		}
	}
	return ""
}

// SyncPanelDomain makes the panel domain of the settings the domain the panel is served on. It used to be typed in by the
// admin, and an imported database brought the domain of the server it was made on, so links could name a machine that
// was no longer the panel.
func (s *Service) SyncPanelDomain(ctx context.Context) error {
	if s.PanelHost == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if set.PanelDomain == s.PanelHost {
		return nil
	}
	if s.Log != nil {
		s.Log.Info("panel domain set to the address the panel is served on", "was", set.PanelDomain, "now", s.PanelHost)
	}
	set.PanelDomain = s.PanelHost
	return s.Store.PutSettings(ctx, *set)
}
