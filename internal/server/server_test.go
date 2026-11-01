package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"anypanel2/internal/auth"
	"anypanel2/internal/config"
	"anypanel2/internal/panel"
	"anypanel2/internal/store"
	"anypanel2/internal/store/memstore"
	"anypanel2/internal/xray"
)

const testPass = "s3cret-pass-123"

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	st := memstore.New()
	hash, err := auth.HashPassword(testPass)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.PutAdmin(context.Background(), store.Admin{Username: "admin", PasswordHash: hash})
	am, err := auth.NewManager(st, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>Whitelist Panel</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	xr := xray.NewManager(filepath.Join(t.TempDir(), "no-xray"), filepath.Join(t.TempDir(), "c.json"), "127.0.0.1:1", log)
	svc := panel.New(st, xr, xray.Options{}, []int{28270}, log)
	return New(config.Config{CookieSecure: false}, st, am, svc, ui, log).Handler()
}

func do(h http.Handler, method, path, body string, cookie *http.Cookie, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if method != http.MethodGet {
		req.Header.Set(csrfHeader, csrfValue)
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func loginBody(user, pass string) string {
	b, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	return string(b)
}

func sessionCookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func TestHealth(t *testing.T) {
	rec := do(newHandler(t), http.MethodGet, "/api/health", "", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("health: %d %s", rec.Code, rec.Body)
	}
}

func TestLoginMeLogout(t *testing.T) {
	h := newHandler(t)

	if rec := do(h, http.MethodGet, "/api/auth/me", "", nil, nil); rec.Code != 401 {
		t.Fatalf("me without session: %d", rec.Code)
	}
	rec := do(h, http.MethodPost, "/api/auth/login", loginBody("admin", testPass), nil, nil)
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	c := sessionCookieFrom(t, rec)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie must be HttpOnly + SameSite=Strict: %+v", c)
	}

	me := do(h, http.MethodGet, "/api/auth/me", "", c, nil)
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"admin"`) {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}
	if out := do(h, http.MethodPost, "/api/auth/logout", "", c, nil); out.Code != 200 {
		t.Fatalf("logout: %d", out.Code)
	}
	if again := do(h, http.MethodGet, "/api/auth/me", "", c, nil); again.Code != 401 {
		t.Fatalf("session must be revoked after logout, got %d", again.Code)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	h := newHandler(t)
	for _, tc := range []struct{ user, pass string }{{"admin", "wrong"}, {"nobody", testPass}} {
		rec := do(h, http.MethodPost, "/api/auth/login", loginBody(tc.user, tc.pass), nil, nil)
		if rec.Code != 401 {
			t.Fatalf("%s: want 401, got %d", tc.user, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("no cookie may be set on failed login")
		}
	}
}

func TestCSRFHeaderRequired(t *testing.T) {
	h := newHandler(t)
	rec := do(h, http.MethodPost, "/api/auth/login", loginBody("admin", testPass), nil, map[string]string{csrfHeader: ""})
	if rec.Code != 403 {
		t.Fatalf("want 403 without %s, got %d", csrfHeader, rec.Code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	h := newHandler(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = do(h, http.MethodPost, "/api/auth/login", loginBody("admin", "wrong"), nil, nil)
	}
	if last.Code != 429 || last.Header().Get("Retry-After") == "" {
		t.Fatalf("6th attempt: want 429 + Retry-After, got %d", last.Code)
	}
}

func TestLimiterKeyedByForwardedIPOnlyFromLoopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("XFF from a non-loopback peer must be ignored, got %s", got)
	}
	r.RemoteAddr = "127.0.0.1:5555"
	if got := clientIP(r); got != "198.51.100.1" {
		t.Fatalf("XFF from loopback proxy must be used, got %s", got)
	}
}

func TestStaticAndSPAFallback(t *testing.T) {
	h := newHandler(t)

	asset := do(h, http.MethodGet, "/assets/app.js", "", nil, nil)
	if asset.Code != 200 || !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d cache=%q", asset.Code, asset.Header().Get("Cache-Control"))
	}
	page := do(h, http.MethodGet, "/inbounds/42", "", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Whitelist Panel") {
		t.Fatalf("SPA fallback: %d %s", page.Code, page.Body)
	}
	if page.Header().Get("Content-Security-Policy") == "" || page.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers missing")
	}
	api := do(h, http.MethodGet, "/api/nope", "", nil, nil)
	if api.Code != 404 || !strings.Contains(api.Header().Get("Content-Type"), "json") {
		t.Fatalf("unknown API path must be JSON 404, got %d %s", api.Code, api.Header().Get("Content-Type"))
	}
}
