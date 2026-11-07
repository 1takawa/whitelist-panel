package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOwnSubscriptionPage(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)
	browser := map[string]string{"Accept": "text/html"}

	putPage := func(page map[string]any) (int, string) {
		body, _ := json.Marshal(map[string]any{"panelDomain": "panel.example.com", "subscription": map[string]any{"page": page}})
		rec := do(h, http.MethodPut, "/api/settings", string(body), c, nil)
		return rec.Code, rec.Body.String()
	}
	theme := func(name, html string) map[string]any {
		return map[string]any{"id": "t1", "name": name, "html": html, "css": "h1{color:#123456}", "js": "document.title = sub.name;"}
	}
	good := `<!doctype html><title>{{.title}}</title><h1>{{.name}}</h1><b>{{.statusLabel}}</b><a href="{{.url}}">link</a><script>const sub = {{.json}};</script>`

	// Themes that cannot work are refused when saved, not when a subscriber opens them.
	for name, page := range map[string]map[string]any{
		"broken":         {"selected": "t1", "themes": []any{theme("A", "<p>{{.name</p>")}},
		"unknown call":   {"selected": "t1", "themes": []any{theme("A", "<p>{{nosuchfunc .name}}</p>")}},
		"empty selected": {"selected": "t1", "themes": []any{theme("A", "  ")}},
		"no such theme":  {"selected": "zzz", "themes": []any{theme("A", good)}},
		"no name":        {"themes": []any{theme(" ", good)}},
		"same names":     {"themes": []any{theme("A", good), map[string]any{"id": "t2", "name": "a", "html": good}}},
		"bad id":         {"themes": []any{map[string]any{"id": "../x", "name": "A", "html": good}}},
	} {
		if code, _ := putPage(page); code != 400 {
			t.Errorf("%s must be refused: %d", name, code)
		}
	}

	// A theme that is saved but not selected changes nothing for subscribers.
	if code, body := putPage(map[string]any{"themes": []any{theme("Hidden Pool", good)}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if page := do(h, http.MethodGet, "/sub/"+subID, "", nil, browser); strings.Contains(page.Body.String(), "<h1>alice</h1>") || strings.Contains(page.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("the standard page stays until a theme is chosen:\n%s", page.Body)
	}

	if code, body := putPage(map[string]any{"selected": "t1", "themes": []any{theme("Hidden Pool", good)}}); code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, browser)
	got := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(got, "<h1>alice</h1>") || !strings.Contains(got, "Активна") || !strings.Contains(got, "const sub = {") ||
		!strings.Contains(got, "h1{color:#123456}") || !strings.Contains(got, "document.title = sub.name;") || !strings.Contains(got, "window.sub = {") {
		t.Fatalf("the page is filled with the subscription: %d\n%s", rec.Code, got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || strings.Contains(csp, "allow-same-origin") || rec.Header().Get("X-Frame-Options") == "SAMEORIGIN" {
		t.Fatalf("the page must run in a sandbox without the panel's origin: %q", csp)
	}

	// Apps still get the subscription itself.
	if app := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*"}); strings.Contains(app.Body.String(), "<h1>") {
		t.Fatal("only browsers get the page")
	}

	// A theme without an id gets one; going back to the standard page keeps the theme saved.
	if code, body := putPage(map[string]any{"themes": []any{map[string]any{"name": "New", "html": good}}}); code != 200 || !strings.Contains(body, `"name":"New"`) || strings.Contains(body, `"id":""`) {
		t.Fatalf("%d %s", code, body)
	}
	if page := do(h, http.MethodGet, "/sub/"+subID, "", nil, browser); strings.Contains(page.Body.String(), "<h1>alice</h1>") {
		t.Fatalf("the standard page is back:\n%s", page.Body)
	}
}

func TestSubscriptionPageEditorEndpoints(t *testing.T) {
	h := newHandler(t)
	c := loggedIn(t, h)

	if rec := do(h, http.MethodGet, "/api/settings/page-example", "", nil, nil); rec.Code != 401 {
		t.Fatalf("the editor is for the administrator: %d", rec.Code)
	}
	ex := do(h, http.MethodGet, "/api/settings/page-example", "", c, nil)
	var example struct{ HTML, CSS, JS string }
	_ = json.Unmarshal(ex.Body.Bytes(), &example)
	if ex.Code != 200 || !strings.Contains(example.HTML, "{{.name}}") || example.CSS == "" || example.JS == "" {
		t.Fatalf("the example: %d", ex.Code)
	}

	post := func(html string) (int, string) {
		b, _ := json.Marshal(map[string]string{"html": html, "css": example.CSS, "js": example.JS})
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
		!strings.Contains(view.Body.String(), "<style>") || !strings.Contains(view.Body.String(), "window.sub = ") ||
		!strings.Contains(view.Header().Get("Content-Security-Policy"), "sandbox") || view.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("the preview is shown in a sandboxed frame: %d %v", view.Code, view.Header())
	}
	if rec := do(h, http.MethodGet, "/api/settings/page-preview/nope", "", nil, nil); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestBuiltInPageIsSelectedWithItsFields(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)
	browser := map[string]string{"Accept": "text/html"}
	put := func(page map[string]any) (int, string) {
		body, _ := json.Marshal(map[string]any{"panelDomain": "panel.example.com", "subscription": map[string]any{"page": page}})
		rec := do(h, http.MethodPut, "/api/settings", string(body), c, nil)
		return rec.Code, rec.Body.String()
	}

	list := do(h, http.MethodGet, "/api/settings/page-presets", "", c, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"name":"Hidden Pool"`) || !strings.Contains(list.Body.String(), `"tag":"Спонсор"`) || !strings.Contains(list.Body.String(), `"key":"telegram"`) {
		t.Fatalf("%d %s", list.Code, list.Body)
	}
	if rec := do(h, http.MethodGet, "/api/settings/page-presets", "", nil, nil); rec.Code != 401 {
		t.Fatalf("the list is for the administrator: %d", rec.Code)
	}

	for name, page := range map[string]map[string]any{
		"unknown page":  {"selected": "preset:nope"},
		"bad link":      {"selected": "preset:hidden-pool", "presets": map[string]any{"hidden-pool": map[string]any{"site": "javascript:alert(1)"}}},
		"unknown field": {"selected": "preset:hidden-pool", "presets": map[string]any{"hidden-pool": map[string]any{"nope": "https://a.example"}}},
	} {
		if code, _ := put(page); code != 400 {
			t.Errorf("%s must be refused: %d", name, code)
		}
	}

	if code, body := put(map[string]any{"selected": "preset:hidden-pool", "presets": map[string]any{"hidden-pool": map[string]any{"site": "example.org", "support": "@my_support"}}}); code != 200 || !strings.Contains(body, `"site":"https://example.org"`) || !strings.Contains(body, `"support":"https://t.me/my_support"`) {
		t.Fatalf("the fields are checked and written in full: %d %s", code, body)
	}
	rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, browser)
	got := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(got, "Hidden Pool") || !strings.Contains(got, `href="https://example.org"`) || !strings.Contains(got, `href="https://t.me/my_support"`) ||
		!strings.Contains(got, "https://t.me/HiddenPoolVPNBot") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("the built-in page is shown with the typed values and the preset's own for the rest: %d\n%s", rec.Code, got)
	}

	// The pictures are public, and only the registered ones.
	if img := do(h, http.MethodGet, "/api/theme-assets/hidden-pool/banner.jpg", "", nil, nil); img.Code != 200 || img.Header().Get("Content-Type") != "image/jpeg" || img.Body.Len() < 1000 {
		t.Fatalf("%d %s", img.Code, img.Header().Get("Content-Type"))
	}
	// A kept copy is asked about each time and answered with 304 while it is current.
	first := do(h, http.MethodGet, "/api/theme-assets/hidden-pool/banner.jpg", "", nil, nil)
	if etag := first.Header().Get("ETag"); etag == "" || first.Header().Get("Cache-Control") != "public, no-cache" ||
		do(h, http.MethodGet, "/api/theme-assets/hidden-pool/banner.jpg", "", nil, map[string]string{"If-None-Match": etag}).Code != 304 {
		t.Fatal("pictures are revalidated, not kept for a day")
	}
	if img := do(h, http.MethodGet, "/api/theme-assets/hidden-pool/page.html", "", nil, nil); img.Code != 404 {
		t.Fatalf("%d", img.Code)
	}

	// The editor previews the built-in page with the fields as they are being typed.
	b, _ := json.Marshal(map[string]any{"preset": "hidden-pool", "fields": map[string]string{"site": "https://typed.example"}})
	pv := do(h, http.MethodPost, "/api/settings/page-preview", string(b), c, nil)
	var id struct{ ID string }
	_ = json.Unmarshal(pv.Body.Bytes(), &id)
	view := do(h, http.MethodGet, "/api/settings/page-preview/"+id.ID, "", nil, nil)
	if pv.Code != 200 || !strings.Contains(view.Body.String(), "https://typed.example") || !strings.Contains(view.Body.String(), "demo@example.com") {
		t.Fatalf("%d %s", pv.Code, view.Body)
	}
	bad, _ := json.Marshal(map[string]any{"preset": "hidden-pool", "fields": map[string]string{"site": "javascript:1"}})
	if rec := do(h, http.MethodPost, "/api/settings/page-preview", string(bad), c, nil); rec.Code != 400 {
		t.Fatalf("%d", rec.Code)
	}
}

// doHost is do with the Host the request came to.
func doHost(h http.Handler, host, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSubscriptionDomainServesOnlySubscriptions(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)
	put := func(sub string) {
		body := `{"panelDomain":"panel.example.com","subscription":` + sub + `}`
		if rec := do(h, http.MethodPut, "/api/settings", body, c, nil); rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	put(`{"domain":"subs.example.com","moveToDomain":true}`)
	app := map[string]string{"Accept": "*/*", "User-Agent": "Happ/2.1.0"}

	// The subscription itself and what its page needs.
	if rec := doHost(h, "subs.example.com", http.MethodGet, "/sub/"+subID, app); rec.Code != 200 {
		t.Fatalf("the subscription: %d", rec.Code)
	}
	for _, path := range []string{"/api/health", "/assets/app.js", "/api/sub/" + subID} {
		if rec := doHost(h, "subs.example.com", http.MethodGet, path, nil); rec.Code != 200 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	// Nothing of the panel: not even its login page.
	for _, path := range []string{"/", "/login", "/inbounds", "/api/clients", "/api/auth/me", "/api/settings"} {
		if rec := doHost(h, "subs.example.com:443", http.MethodGet, path, nil); rec.Code != 404 {
			t.Errorf("%s must not be served on the subscription domain: %d", path, rec.Code)
		}
	}
	if rec := doHost(h, "subs.example.com", http.MethodPost, "/api/auth/login", map[string]string{csrfHeader: csrfValue}); rec.Code != 404 {
		t.Errorf("no login there: %d", rec.Code)
	}
	// The panel's own domain keeps everything.
	if rec := doHost(h, "panel.example.com", http.MethodGet, "/login", nil); rec.Code != 200 {
		t.Errorf("the panel domain: %d", rec.Code)
	}

	// Happ is told to move to the new domain when it came by the old one, and only then.
	if got := doHost(h, "panel.example.com", http.MethodGet, "/sub/"+subID, app).Header().Get("new-domain"); got != "subs.example.com" {
		t.Errorf("new-domain: %q", got)
	}
	if got := doHost(h, "subs.example.com", http.MethodGet, "/sub/"+subID, app).Header().Get("new-domain"); got != "" {
		t.Errorf("already there: %q", got)
	}
	if got := doHost(h, "panel.example.com", http.MethodGet, "/sub/"+subID, map[string]string{"Accept": "*/*", "User-Agent": "v2rayN/6.0"}).Header().Get("new-domain"); got != "" {
		t.Errorf("only Happ is told: %q", got)
	}
	put(`{"domain":"subs.example.com","moveToDomain":false}`)
	if got := doHost(h, "panel.example.com", http.MethodGet, "/sub/"+subID, app).Header().Get("new-domain"); got != "" {
		t.Errorf("switched off: %q", got)
	}
}
