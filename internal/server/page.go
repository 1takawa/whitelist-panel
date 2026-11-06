package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"anypanel2/internal/panel"
	"anypanel2/internal/subpage"
)

// pageSandbox is the policy the administrator's page is served with. The page is code written by whoever runs the panel
// and it is served from the panel's own address, so it must not be able to act as the panel: the sandbox without
// allow-same-origin gives it an empty origin, with no access to the panel's cookies or API. Scripts, links to the apps
// (happ://…) and loading fonts or pictures from anywhere stay allowed.
const pageSandbox = "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation"

// pagePreviews keeps the pages the settings editor is showing: the editor sends the code, gets an id, and a frame opens
// the page by that id (a frame cannot take the code in a request body).
type pagePreviews struct {
	mu    sync.Mutex
	items map[string]pagePreview
}

type pagePreview struct {
	html []byte
	at   time.Time
}

const (
	pagePreviewTTL = 10 * time.Minute
	pagePreviewMax = 20
)

func (p *pagePreviews) put(html []byte) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.items == nil {
		p.items = map[string]pagePreview{}
	}
	now := time.Now()
	oldest, oldestID := now, ""
	for id, it := range p.items {
		if now.Sub(it.at) > pagePreviewTTL {
			delete(p.items, id)
		} else if it.at.Before(oldest) {
			oldest, oldestID = it.at, id
		}
	}
	if len(p.items) >= pagePreviewMax && oldestID != "" {
		delete(p.items, oldestID)
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	p.items[id] = pagePreview{html: html, at: now}
	return id
}

func (p *pagePreviews) get(id string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	it, ok := p.items[id]
	if !ok || time.Since(it.at) > pagePreviewTTL {
		return nil, false
	}
	return it.html, true
}

// sendPage writes the administrator's page with the sandbox policy. Framing is allowed only for the editor's preview.
func sendPage(w http.ResponseWriter, body []byte, framed bool) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	if framed {
		h.Set("Content-Security-Policy", pageSandbox+"; frame-ancestors 'self'")
		h.Set("X-Frame-Options", "SAMEORIGIN")
	} else {
		h.Set("Content-Security-Policy", pageSandbox+"; frame-ancestors 'none'")
	}
	_, _ = w.Write(body)
}

// customPage renders the administrator's page for a subscription; ok is false when there is none (or it does not render,
// so the standard page is shown instead).
func (s *Server) customPage(sb *panel.Subscription) ([]byte, bool) {
	page := sb.Settings.Subscription.Page
	var code subpage.Page
	var fields map[string]string
	if id, ok := strings.CutPrefix(page.Selected, subpage.PresetPrefix); ok {
		pr, found := subpage.FindPreset(id)
		if !found {
			return nil, false
		}
		code, fields = pr.Page(), pr.Values(page.Presets[id])
	} else {
		t := page.Theme(page.Selected)
		if page.Selected == "" || t == nil || t.HTML == "" {
			return nil, false
		}
		code = subpage.Page{HTML: t.HTML, CSS: t.CSS, JS: t.JS}
	}
	in := subpage.Input{
		Name: sb.Client.Email, Title: sb.Meta.Title, Status: sb.Client.Status(s.now()),
		Up: sb.Client.Up, Down: sb.Client.Down, Total: sb.Client.TotalBytes, ExpiryAt: sb.Client.ExpiryAt,
		URL: sb.URL, SupportURL: sb.Meta.SupportURL, ProfileURL: sb.Meta.ProfileURL, Announce: sb.Meta.Announce, Now: s.now(), Fields: fields,
	}
	for _, l := range sb.Links {
		in.Links = append(in.Links, subpage.Link{Remark: l.Remark, Name: l.Name, Protocol: l.Protocol, Network: l.Network, Security: l.Security, Link: l.Link})
	}
	out, err := subpage.Render(code, subpage.Data(in))
	if err != nil {
		s.log.Error("custom subscription page failed", "err", err)
		return nil, false
	}
	return out, true
}

// pageExample gives the starting page for the editor.
func (s *Server) pageExample(w http.ResponseWriter, r *http.Request) {
	e := subpage.Example()
	writeJSON(w, http.StatusOK, map[string]string{"html": e.HTML, "css": e.CSS, "js": e.JS})
}

// pagePresets lists the built-in pages for the settings.
func (s *Server) pagePresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, subpage.Presets())
}

// themeAsset serves a picture of a built-in page. It is public: the subscriber's browser loads it.
func (s *Server) themeAsset(w http.ResponseWriter, r *http.Request) {
	b, ct, ok := subpage.Asset(r.PathValue("preset"), r.PathValue("file"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	// The browser may keep the picture but asks again each time whether it changed (a 304 costs nothing), so a replaced
	// picture shows up at once instead of after the day the old one was kept.
	sum := sha256.Sum256(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "public, no-cache")
	h.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(b)
}

// pagePreviewCreate checks the code the editor sends, renders it with made-up data and remembers the result.
func (s *Server) pagePreviewCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		HTML   string            `json:"html"`
		CSS    string            `json:"css"`
		JS     string            `json:"js"`
		Preset string            `json:"preset"` // a built-in page instead of the code above
		Fields map[string]string `json:"fields"`
	}](w, r)
	if !ok {
		return
	}
	code, data := subpage.Page{HTML: req.HTML, CSS: req.CSS, JS: req.JS}, subpage.Sample()
	if req.Preset != "" {
		pr, found := subpage.FindPreset(req.Preset)
		if !found {
			writeError(w, http.StatusBadRequest, "unknown built-in page")
			return
		}
		vals := map[string]string{}
		for k, v := range req.Fields {
			n, err := pr.NormalizeField(k, v)
			if err != nil {
				writeError(w, http.StatusBadRequest, k+": "+err.Error())
				return
			}
			vals[k] = n
		}
		code, data = pr.Page(), subpage.SampleWith(pr.Values(vals))
	}
	out, err := subpage.Render(code, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": s.pageViews.put(out)})
}

func (s *Server) pagePreviewShow(w http.ResponseWriter, r *http.Request) {
	body, ok := s.pageViews.get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	sendPage(w, body, true)
}
