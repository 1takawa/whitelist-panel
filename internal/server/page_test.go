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
