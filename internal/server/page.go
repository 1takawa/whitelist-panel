package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
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
	p := sb.Settings.Subscription.Page
	if !p.Enable || p.HTML == "" {
		return nil, false
	}
	in := subpage.Input{
		Name: sb.Client.Email, Title: sb.Meta.Title, Status: sb.Client.Status(s.now()),
		Up: sb.Client.Up, Down: sb.Client.Down, Total: sb.Client.TotalBytes, ExpiryAt: sb.Client.ExpiryAt,
		URL: sb.URL, SupportURL: sb.Meta.SupportURL, ProfileURL: sb.Meta.ProfileURL, Announce: sb.Meta.Announce, Now: s.now(),
	}
	for _, l := range sb.Links {
		in.Links = append(in.Links, subpage.Link{Remark: l.Remark, Name: l.Name, Protocol: l.Protocol, Network: l.Network, Security: l.Security, Link: l.Link})
	}
	out, err := subpage.Render(p.HTML, subpage.Data(in))
	if err != nil {
		s.log.Error("custom subscription page failed", "err", err)
		return nil, false
	}
	return out, true
}

// pageExample gives the starting page for the editor.
func (s *Server) pageExample(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"html": subpage.Example()})
}

// pagePreviewCreate checks the code the editor sends, renders it with made-up data and remembers the result.
func (s *Server) pagePreviewCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		HTML string `json:"html"`
	}](w, r)
	if !ok {
		return
	}
	out, err := subpage.Render(req.HTML, subpage.Sample())
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
