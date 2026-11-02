package server

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"anypanel2/internal/formats"
	"anypanel2/internal/links"
	"anypanel2/internal/model"
	"anypanel2/internal/panel"
	"anypanel2/internal/store"
	"anypanel2/internal/sub"
)

// Public subscription endpoints. They need no login: the subscription id is the secret (about 80 bits of entropy),
// so they are rate limited per client address to make guessing impractical.
func (s *Server) registerSub(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sub/{subId}", s.publicSub)
}

type kind string

const (
	kindRaw   kind = "raw"   // the base64 (or plain) list of links
	kindClash kind = "clash" // Clash / Mihomo YAML
	kindJSON  kind = "json"  // Xray client JSON
)

// prefixes are the configured URL prefixes; they are cached for a few seconds so ordinary requests do not hit the
// database, and reset by the settings handler.
type prefixes struct {
	raw, clash, json string // clash and json are empty when that format is switched off
}

type subPathCache struct {
	mu sync.Mutex
	p  prefixes
	at time.Time
	ok bool
}

func (s *Server) prefixes(r *http.Request) prefixes {
	c := &s.subPaths
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && time.Since(c.at) < 5*time.Second {
		return c.p
	}
	set, err := s.svc.GetSettings(r.Context())
	if err != nil {
		if c.ok {
			return c.p // keep serving with the last known prefixes through a database hiccup
		}
		return prefixes{raw: "/sub/"}
	}
	sb := set.Subscription
	p := prefixes{raw: sb.Path}
	if sb.Formats.ClashEnable {
		p.clash = sb.Formats.ClashPath
	}
	if sb.Formats.JSONEnable {
		p.json = sb.Formats.JSONPath
	}
	c.p, c.at, c.ok = p, time.Now(), true
	return p
}

func (s *Server) invalidateSubPath() {
	s.subPaths.mu.Lock()
	s.subPaths.ok = false
	s.subPaths.mu.Unlock()
}

// withSubRoute serves <prefix><subId> before the regular routes, so the prefixes can be changed in the settings.
func (s *Server) withSubRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			p := s.prefixes(r)
			for _, c := range []struct {
				prefix string
				kind   kind
			}{{p.raw, kindRaw}, {p.clash, kindClash}, {p.json, kindJSON}} {
				if c.prefix == "" || !strings.HasPrefix(r.URL.Path, c.prefix) {
					continue
				}
				if id := r.URL.Path[len(c.prefix):]; id != "" && !strings.Contains(id, "/") {
					r.SetPathValue("subId", id)
					s.subscription(w, r, c.kind)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) subRateLimited(w http.ResponseWriter, r *http.Request) bool {
	if s.subLimiter.Allow(clientIP(r)) {
		return false
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, "too many requests", http.StatusTooManyRequests)
	return true
}

// detectFormat picks Clash or JSON for an app on the main path when auto-detection is on and its User-Agent matches.
func detectFormat(set model.SubSettings, ua string) kind {
	f := set.Formats
	if f.ClashEnable && f.ClashAutoDetect && matches(f.ClashUARegex, ua) {
		return kindClash
	}
	if f.JSONEnable && f.JSONAutoDetect && matches(f.JSONUARegex, ua) {
		return kindJSON
	}
	return kindRaw
}

// wantsAutoNodeJSON reports whether an app on the main address should get the Xray JSON format: Happ and INCY read a
// group only from it, so with the automatic node on (and at least two servers to put in it) they get it there, whether or
// not the separate JSON address is switched on.
func wantsAutoNodeJSON(set model.SubSettings, ua string, eps []links.Endpoint) bool {
	a := set.AutoNode
	if !a.Enable || a.KeepLinks || !matches(autoNodeAppsRe, ua) {
		return false
	}
	n := 0
	for _, e := range eps {
		if !e.NoAuto {
			n++
		}
	}
	return n >= 2
}

// autoNodeAppsRe matches the User-Agent of the apps that take the automatic node from the JSON format.
const autoNodeAppsRe = `(?i)(happ|incy)`

func matches(pattern, ua string) bool {
	if pattern == "" {
		return false
	}
	re, err := regexp.Compile(pattern)
	return err == nil && re.MatchString(ua)
}

// subscription serves the subscription to apps in the requested format, and the user page (the SPA) to browsers.
func (s *Server) subscription(w http.ResponseWriter, r *http.Request, k kind) {
	if s.subRateLimited(w, r) {
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") && r.URL.Query().Get("raw") == "" {
		s.servePage(w, r)
		return
	}
	sb, err := s.svc.PublicSubscription(r.Context(), r.PathValue("subId"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.log.Error("subscription failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// An app that sends its device id (HWID) gets the device remembered; a blocked device is served no servers.
	if d, ok := deviceFromRequest(r); ok {
		blocked, err := s.svc.TouchDevice(r.Context(), sb.Client.ID, d)
		if err != nil {
			s.log.Warn("remembering the device failed", "err", err)
		}
		if blocked {
			panel.BlockedSubscription(sb)
		}
	}
	set := sb.Settings.Subscription
	ua := r.UserAgent()
	if k == kindRaw {
		k = detectFormat(set, ua)
		if k == kindRaw && wantsAutoNodeJSON(set, ua, sb.Endpoints) {
			k = kindJSON
		}
	}

	h := w.Header()
	for key, v := range sub.Headers(sb.Client, sb.Meta, set.UpdateHours) {
		h.Set(key, v)
	}
	// Routing, Hide-Settings and the headers of the app that is asking (Happ, INCY); see sub.ExtraHeaders.
	for key, v := range sub.ExtraHeadersFor(set, ua, sb.Banner) {
		h.Set(key, v)
	}
	h.Set("Cache-Control", "no-store")

	switch k {
	case kindClash:
		body, err := s.clash(sb)
		if err != nil {
			s.log.Error("clash profile failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.Set("Content-Type", "text/yaml; charset=utf-8")
		_, _ = w.Write(body)
	case kindJSON:
		body, err := s.xrayJSON(sb, ua)
		if err != nil {
			s.log.Error("xray json failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(body)
	default:
		lines := append(append([]string{}, sb.Lines...), sub.ExtraBodyLines(set, ua)...)
		h.Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(sub.Body(lines, set.Encode)))
	}
}

// autoOptions turns the settings into the options of the formats; nil when the automatic node is off.
func autoOptions(a model.AutoNodeSettings) *formats.AutoOptions {
	if !a.Enable {
		return nil
	}
	return &formats.AutoOptions{Name: a.Name, Last: a.Last, Position: a.Position, URL: a.ProbeURL, IntervalSec: a.IntervalSec, TimeoutSec: a.TimeoutSec, ToleranceMs: a.ToleranceMs}
}

func (s *Server) clash(sb *panel.Subscription) ([]byte, error) {
	f := sb.Settings.Subscription.Formats
	opt := formats.ClashOptions{RemoteDNS: f.ClashRemoteDNS, Info: sb.InfoNames, Auto: autoOptions(sb.Settings.Subscription.AutoNode)}
	if f.ClashEnableRouting {
		rules, err := formats.ParseClashRules(f.ClashRules)
		if err != nil {
			return nil, err
		}
		opt.Rules = rules
	}
	return formats.Clash(sb.Endpoints, opt)
}

func (s *Server) xrayJSON(sb *panel.Subscription, ua string) ([]byte, error) {
	f := sb.Settings.Subscription.Formats
	return formats.XrayJSON(sb.Endpoints, formats.JSONOptions{
		AlwaysArray: f.JSONAlwaysArray, Mux: f.JSONMux, Fragment: f.JSONFragment, Noises: f.JSONNoises,
		Rules: f.JSONRules, DNS: f.JSONDNS, Info: sb.InfoNames, Auto: autoOptions(sb.Settings.Subscription.AutoNode),
		Profile: appRoutingProfile(sb.Settings.Subscription, ua),
	})
}

// appRoutingProfile is the routing profile the asking app would get in its header (Happ) or in the body (INCY); it is also
// written into the JSON configs, because an app may ignore the header. Other apps have no such profile.
func appRoutingProfile(set model.SubSettings, ua string) string {
	switch {
	case sub.IsIncy(ua) && set.Incy.EnableRouting:
		return set.Incy.RoutingRules
	case sub.IsHapp(ua) && set.Happ.EnableRouting:
		return set.Happ.RoutingRules
	}
	return ""
}

type publicLink struct {
	Remark   string `json:"remark"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Network  string `json:"network"`
	Security string `json:"security"`
	Link     string `json:"link"`
}

type publicSubView struct {
	Name       string       `json:"name"`
	Title      string       `json:"title"`
	Status     string       `json:"status"`
	Up         int64        `json:"up"`
	Down       int64        `json:"down"`
	Total      int64        `json:"total"`
	ExpiryAt   *time.Time   `json:"expiryAt"`
	URL        string       `json:"url"`
	SupportURL string       `json:"supportUrl"`
	ProfileURL string       `json:"profileUrl"`
	Announce   string       `json:"announce"`
	Links      []publicLink `json:"links"`
}

// publicSub feeds the user page. It exposes only what the subscriber needs to see about their own subscription.
func (s *Server) publicSub(w http.ResponseWriter, r *http.Request) {
	if s.subRateLimited(w, r) {
		return
	}
	sb, err := s.svc.PublicSubscription(r.Context(), r.PathValue("subId"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "subscription not found")
		return
	}
	if err != nil {
		s.log.Error("public subscription failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	v := publicSubView{
		Name: sb.Client.Email, Title: sb.Meta.Title, Status: sb.Client.Status(s.now()),
		Up: sb.Client.Up, Down: sb.Client.Down, Total: sb.Client.TotalBytes, ExpiryAt: sb.Client.ExpiryAt,
		URL: sb.URL, SupportURL: sb.Meta.SupportURL, ProfileURL: sb.Meta.ProfileURL, Announce: sb.Meta.Announce,
		Links: []publicLink{},
	}
	for _, l := range sb.Links {
		v.Links = append(v.Links, publicLink{Remark: l.Remark, Name: l.Name, Protocol: l.Protocol, Network: l.Network, Security: l.Security, Link: l.Link})
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) now() time.Time { return time.Now() }

// deviceFromRequest reads the device the app introduces in its request headers (Happ: X-HWID, X-Device-OS, X-Ver-OS,
// X-Device-Model). Without an HWID there is no device.
func deviceFromRequest(r *http.Request) (panel.DeviceInfo, bool) {
	h := r.Header
	d := panel.DeviceInfo{HWID: strings.TrimSpace(h.Get("X-Hwid")), OS: h.Get("X-Device-Os"), OSVersion: h.Get("X-Ver-Os"),
		Model: h.Get("X-Device-Model"), App: r.UserAgent(), IP: clientIP(r)}
	return d, d.HWID != ""
}
