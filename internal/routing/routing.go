// Package routing serves the ready-made routing profiles of github.com/hydraponique/roscomvpn-routing (RoscomVPN) for
// Happ and INCY: DEFAULT (Russia and Belarus direct, YouTube/Telegram/GitHub through the proxy, ads blocked) and
// WHITELIST (direct only for the Russian whitelists). The profiles bring their own geo files and DNS settings, which is
// what makes them work: a profile that only lists rules depends on whatever geo data the app happens to have.
//
// The current profiles are read from the repository (the upstream updates them whenever new geo files are released);
// a copy compiled into the binary is used when GitHub cannot be reached.
package routing

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed snapshot/*.deeplink
var snapshot embed.FS

// UpstreamBase is where the profiles live: <base>/HAPP/DEFAULT.DEEPLINK and so on.
const UpstreamBase = "https://raw.githubusercontent.com/hydraponique/roscomvpn-routing/main"

const (
	maxLink  = 64 << 10
	cacheTTL = time.Hour
	// minRefresh is how soon a forced refresh may ask GitHub again: a button pressed repeatedly must not hammer it.
	minRefresh = 30 * time.Second
)

// Apps the profiles exist for, with the scheme of their deep links.
var apps = map[string]struct{ dir, scheme string }{
	"happ": {"HAPP", "happ"},
	"incy": {"INCY", "incy"},
}

// Preset describes one profile for the settings page.
type Preset struct {
	ID    string `json:"id"` // default | whitelist
	Label string `json:"label"`
	Hint  string `json:"hint"`
	// Link is the deep link to store as the routing value (<scheme>://routing/onadd/<base64>).
	Link string `json:"link"`
	// Name is the profile's own name (what the app shows), Updated its LastUpdated time.
	Name    string     `json:"name"`
	Updated *time.Time `json:"updated"`
	// Source is "github" for a profile read from the repository now (or a moment ago), "embedded" for the copy
	// built into the panel.
	Source string `json:"source"`
}

var meta = []struct{ id, label, hint string }{
	{"default", "DEFAULT — Россия и Беларусь напрямую",
		"Российские и белорусские сайты, банки, Яндекс, VK, обновления Apple и Microsoft, игровые платформы — напрямую. YouTube, Telegram, GitHub и Google Play — через прокси. Реклама, телеметрия Windows и торренты блокируются."},
	{"whitelist", "WHITELIST — только белые списки РФ напрямую",
		"Напрямую идут только сервисы и IP из белых списков РФ, всё остальное — через прокси. Реклама, телеметрия Windows и торренты блокируются."},
}

type entry struct {
	link string
	at   time.Time
}

// Source reads profiles from the repository and remembers them for an hour. The zero value never touches the network
// and serves the built-in copy.
type Source struct {
	Base string // "" = built-in copies only
	HTTP *http.Client
	Now  func() time.Time

	mu    sync.Mutex
	cache map[string]entry
}

// NewGitHub returns a Source that reads the upstream repository.
func NewGitHub() *Source { return &Source{Base: UpstreamBase} }

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Source) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 8 * time.Second}
}

// Presets returns DEFAULT and WHITELIST for the app ("happ" or "incy"). refresh skips the hour of memory (not the 30-second
// minimum) so the admin can ask whether the upstream has a newer version.
func (s *Source) Presets(ctx context.Context, app string, refresh bool) ([]Preset, error) {
	a, ok := apps[app]
	if !ok {
		return nil, fmt.Errorf("unknown app %q (happ or incy)", app)
	}
	out := make([]Preset, 0, len(meta))
	for _, m := range meta {
		link, source := s.link(ctx, app, a.dir, a.scheme, m.id, refresh)
		p := Preset{ID: m.id, Label: m.label, Hint: m.hint, Link: link, Source: source}
		if prof, err := decode(link, a.scheme); err == nil {
			p.Name = prof.Name
			if n, err := strconv.ParseInt(prof.LastUpdated, 10, 64); err == nil && n > 0 {
				t := time.Unix(n, 0).UTC()
				p.Updated = &t
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// link returns the best available deep link: a fresh cached one, a freshly downloaded one, an older cached one, the
// built-in copy.
func (s *Source) link(ctx context.Context, app, dir, scheme, id string, refresh bool) (link, source string) {
	key := app + "/" + id
	s.mu.Lock()
	defer s.mu.Unlock()
	ttl := cacheTTL
	if refresh {
		ttl = minRefresh
	}
	if e, ok := s.cache[key]; ok && s.now().Sub(e.at) < ttl {
		return e.link, "github"
	}
	if s.Base != "" {
		if l, err := s.fetch(ctx, dir, scheme, id); err == nil {
			if s.cache == nil {
				s.cache = map[string]entry{}
			}
			s.cache[key] = entry{link: l, at: s.now()}
			return l, "github"
		}
		if e, ok := s.cache[key]; ok {
			return e.link, "github"
		}
	}
	return embedded(app, id), "embedded"
}

func embedded(app, id string) string {
	b, _ := snapshot.ReadFile("snapshot/" + app + "_" + id + ".deeplink")
	return strings.TrimSpace(string(b))
}

func (s *Source) fetch(ctx context.Context, dir, scheme, id string) (string, error) {
	url := s.Base + "/" + dir + "/" + strings.ToUpper(id) + ".DEEPLINK"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxLink+1))
	if err != nil {
		return "", err
	}
	link := strings.TrimSpace(string(b))
	if len(link) > maxLink {
		return "", errors.New("the profile is too large")
	}
	if _, err := decode(link, scheme); err != nil {
		return "", err // a changed or damaged upstream file never reaches the admin: the built-in copy is used instead
	}
	return link, nil
}

// profile is the part of a routing profile the panel looks at.
type profile struct {
	Name        string   `json:"Name"`
	Geoipurl    string   `json:"Geoipurl"`
	Geositeurl  string   `json:"Geositeurl"`
	LastUpdated string   `json:"LastUpdated"`
	DirectSites []string `json:"DirectSites"`
	DirectIP    []string `json:"DirectIp"`
}

// decode checks that link is a deep link of the right app holding a plausible profile: a name, https geo file
// addresses and rule lists. Anything else is refused.
func decode(link, scheme string) (*profile, error) {
	prefix := scheme + "://routing/onadd/"
	if !strings.HasPrefix(link, prefix) {
		return nil, fmt.Errorf("not a %s routing link", scheme)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, prefix))
	if err != nil {
		return nil, fmt.Errorf("bad base64: %w", err)
	}
	var p profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("bad profile: %w", err)
	}
	switch {
	case strings.TrimSpace(p.Name) == "" || len(p.Name) > 100:
		return nil, errors.New("the profile has no usable name")
	case !strings.HasPrefix(p.Geoipurl, "https://") || !strings.HasPrefix(p.Geositeurl, "https://") || len(p.Geoipurl) > 300 || len(p.Geositeurl) > 300:
		return nil, errors.New("the profile has no https geo file addresses")
	case len(p.DirectSites)+len(p.DirectIP) == 0:
		return nil, errors.New("the profile has no direct rules")
	}
	return &p, nil
}
