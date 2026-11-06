package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestOwnSubscriptionPage(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)
	browser := map[string]string{"Accept": "text/html"}

	put := func(page string) int {
		body, _ := json.Marshal(map[string]any{"panelDomain": "panel.example.com", "subscription": map[string]any{"page": map[string]any{"enable": true, "html": page}}})
		return do(h, http.MethodPut, "/api/settings", string(body), c, nil).Code
	}

	// Pages that cannot work are refused when saved, not when a subscriber opens them.
	for name, page := range map[string]string{"empty": "  ", "broken": "<p>{{.name</p>", "unknown call": "<p>{{nosuchfunc .name}}</p>"} {
		if code := put(page); code != 400 {
			t.Errorf("%s page must be refused: %d", name, code)
		}
	}

	if code := put(`<!doctype html><title>{{.title}}</title><h1>{{.name}}</h1><b>{{.statusLabel}}</b><a href="{{.url}}">link</a><script>const sub = {{.json}};</script>`); code != 200 {
		t.Fatalf("a good page is saved: %d", code)
	}
	rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, browser)
	got := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(got, "<h1>alice</h1>") || !strings.Contains(got, "Активна") || !strings.Contains(got, "const sub = {") {
		t.Fatalf("the page is filled with the subscription: %d\n%s", rec.Code, got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || strings.Contains(csp, "allow-same-origin") || rec.Header().Get("X-Frame-Options") == "SAMEORIGIN" {
		t.Fatalf("the page must run in a sandbox without the panel's origin: %q", csp)
	}

	// Apps still get the subscription itself.
	if app := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*"}); strings.Contains(app.Body.String(), "<h1>") {
		t.Fatal("only browsers get the page")
	}

	// Turned off, the standard page comes back.
	body, _ := json.Marshal(map[string]any{"panelDomain": "panel.example.com", "subscription": map[string]any{"page": map[string]any{"enable": false, "html": "<p>x</p>"}}})
	if code := do(h, http.MethodPut, "/api/settings", string(body), c, nil).Code; code != 200 {
		t.Fatal(code)
	}
	if page := do(h, http.MethodGet, "/sub/"+subID, "", nil, browser); strings.Contains(page.Body.String(), "<h1>alice</h1>") || strings.Contains(page.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("the page is off:\n%s", page.Body)
	}
}

func TestSubscriptionPageEditorEndpoints(t *testing.T) {
	h := newHandler(t)
	c := loggedIn(t, h)

	if rec := do(h, http.MethodGet, "/api/settings/page-example", "", nil, nil); rec.Code != 401 {
		t.Fatalf("the editor is for the administrator: %d", rec.Code)
	}
	ex := do(h, http.MethodGet, "/api/settings/page-example", "", c, nil)
	var example struct{ HTML string }
	_ = json.Unmarshal(ex.Body.Bytes(), &example)
	if ex.Code != 200 || !strings.Contains(example.HTML, "{{.name}}") {
		t.Fatalf("the example: %d", ex.Code)
	}

	post := func(html string) (int, string) {
		b, _ := json.Marshal(map[string]string{"html": html})
		rec := do(h, http.MethodPost, "/api/settings/page-preview", string(b), c, nil)
		var out struct{ ID string }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.ID
	}
	if code, _ := post("<p>{{.name"); code != 400 {
		t.Fatalf("a broken page has no preview: %d", code)
	}
	code, id := post(example.HTML)
	if code != 200 || id == "" {
		t.Fatalf("preview of the example: %d", code)
	}
	view := do(h, http.MethodGet, "/api/settings/page-preview/"+id, "", nil, nil) // the sandboxed frame carries no cookie
	if view.Code != 200 || !strings.Contains(view.Body.String(), "demo@example.com") ||
		!strings.Contains(view.Header().Get("Content-Security-Policy"), "sandbox") || view.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("the preview is shown in a sandboxed frame: %d %v", view.Code, view.Header())
	}
	if rec := do(h, http.MethodGet, "/api/settings/page-preview/nope", "", nil, nil); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}
