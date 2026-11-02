package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

type env struct {
	h      http.Handler
	svc    *panel.Service
	cookie *http.Cookie
}

func newEnv(t *testing.T, _ bool) env {
	t.Helper()
	st := memstore.New()
	hash, _ := auth.HashPassword(testPass)
	_ = st.PutAdmin(context.Background(), store.Admin{Username: "admin", PasswordHash: hash})
	am, _ := auth.NewManager(st, time.Hour)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	xr := xray.NewManager(filepath.Join(t.TempDir(), "no-xray"), filepath.Join(t.TempDir(), "c.json"), "127.0.0.1:1", log)
	svc := panel.New(st, xr, xray.Options{}, []int{28270}, log)
	svc.Version = "test-1"
	ui := fstest.MapFS{"index.html": {Data: []byte("<title>x</title>")}}
	h := New(config.Config{}, st, am, svc, ui, log).Handler()
	rec := do(h, http.MethodPost, "/api/auth/login", loginBody("admin", testPass), nil, nil)
	return env{h: h, svc: svc, cookie: sessionCookieFrom(t, rec)}
}

func bearer(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok, csrfHeader: ""}
}

func (e env) newToken(t *testing.T) string {
	t.Helper()
	rec := do(e.h, http.MethodPost, "/api/api-token", "", e.cookie, nil)
	if rec.Code != 200 {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body)
	}
	var out struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Token
}

func TestAPITokenAuthenticatesWithoutCSRFHeader(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newToken(t)

	if rec := do(e.h, http.MethodGet, "/api/clients", "", nil, bearer(tok)); rec.Code != 200 {
		t.Fatalf("GET with token: %d %s", rec.Code, rec.Body)
	}
	// A mutating call with a token needs no X-Requested-With: scripts are not browsers.
	body := `{"remark":"x"}`
	if rec := do(e.h, http.MethodPost, "/api/inbounds", body, nil, bearer(tok)); rec.Code == 401 || rec.Code == 403 {
		t.Fatalf("POST with token must pass authentication and CSRF, got %d", rec.Code)
	}
	// Without a token the CSRF rule still applies.
	if rec := do(e.h, http.MethodPost, "/api/inbounds", body, e.cookie, map[string]string{csrfHeader: ""}); rec.Code != 403 {
		t.Fatalf("session without CSRF header: %d", rec.Code)
	}
}

func TestBadOrMissingTokenIsRejected(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newToken(t)
	for name, hdr := range map[string]map[string]string{
		"wrong token":  bearer(tok + "0"),
		"empty bearer": {"Authorization": "Bearer ", csrfHeader: ""},
		"basic scheme": {"Authorization": "Basic " + tok},
	} {
		if rec := do(e.h, http.MethodGet, "/api/clients", "", nil, hdr); rec.Code != 401 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// A valid session cookie must not rescue a request that presents a wrong token.
	if rec := do(e.h, http.MethodGet, "/api/clients", "", e.cookie, bearer("anyp_nonsense")); rec.Code != 401 {
		t.Errorf("a bad token next to a good cookie: %d", rec.Code)
	}
	// After revocation the old token stops working at once.
	if rec := do(e.h, http.MethodDelete, "/api/api-token", "", e.cookie, nil); rec.Code != 200 {
		t.Fatalf("revoke: %d", rec.Code)
	}
	if rec := do(e.h, http.MethodGet, "/api/clients", "", nil, bearer(tok)); rec.Code != 401 {
		t.Errorf("revoked token: %d", rec.Code)
	}
}

func TestTokenCannotManageAccess(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newToken(t)
	for _, r := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/api-token", ""},
		{http.MethodPost, "/api/api-token", ""},
		{http.MethodDelete, "/api/api-token", ""},
		{http.MethodGet, "/api/telegram", ""},
		{http.MethodPut, "/api/telegram", `{"enable":false}`},
		{http.MethodPut, "/api/account", `{"username":"x"}`},
		{http.MethodPost, "/api/db/import", `{}`},
		{http.MethodPost, "/api/telegram/test", ""},
	} {
		if rec := do(e.h, r.method, r.path, r.body, nil, bearer(tok)); rec.Code != 401 {
			t.Errorf("%s %s with a token: want 401, got %d", r.method, r.path, rec.Code)
		}
	}
	if !e.svc.CheckAPIToken(context.Background(), tok) {
		t.Fatal("the token survived the attempts")
	}
}

func TestTokenInfoNeverRepeatsTheToken(t *testing.T) {
	e := newEnv(t, true)
	tok := e.newToken(t)
	rec := do(e.h, http.MethodGet, "/api/api-token", "", e.cookie, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), tok) || !strings.Contains(rec.Body.String(), tok[:9]) {
		t.Fatalf("info shows only the prefix: %s", rec.Body)
	}
}

func TestDashboardEndpoint(t *testing.T) {
	e := newEnv(t, false)
	if rec := do(e.h, http.MethodGet, "/api/dashboard", "", nil, nil); rec.Code != 401 {
		t.Fatalf("auth: %d", rec.Code)
	}
	rec := do(e.h, http.MethodGet, "/api/dashboard", "", e.cookie, nil)
	var d panel.Dashboard
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	if d.Version != "test-1" || len(d.History) != 24 || d.Top == nil || d.Online == nil {
		t.Fatalf("shape: %+v", d)
	}
}

func TestTelegramEndpointsValidateInput(t *testing.T) {
	e := newEnv(t, false)
	if rec := do(e.h, http.MethodPut, "/api/telegram", `{"token":"nope"}`, e.cookie, nil); rec.Code != 400 {
		t.Fatalf("bad token format: %d %s", rec.Code, rec.Body)
	}
	if rec := do(e.h, http.MethodPut, "/api/telegram", `{"enable":true,"adminIds":[1]}`, e.cookie, nil); rec.Code != 400 {
		t.Fatalf("enabling without a token: %d %s", rec.Code, rec.Body)
	}
	rec := do(e.h, http.MethodGet, "/api/telegram", "", e.cookie, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"tokenSet":false`) || !strings.Contains(rec.Body.String(), `"adminIds":[]`) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	if rec := do(e.h, http.MethodPost, "/api/telegram/test", "", e.cookie, nil); rec.Code != 400 {
		t.Fatalf("test without a bot: %d", rec.Code)
	}
}

func TestAccountChangeEndsEverySession(t *testing.T) {
	e := newEnv(t, false)
	for name, body := range map[string]string{
		"bad login":      `{"username":"a b","password":""}`,
		"short password": `{"username":"admin","password":"short"}`,
		"empty":          `{}`,
	} {
		if rec := do(e.h, http.MethodPut, "/api/account", body, e.cookie, nil); rec.Code != 400 {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := do(e.h, http.MethodGet, "/api/auth/me", "", e.cookie, nil); rec.Code != 200 {
		t.Fatal("rejected changes keep the session")
	}

	rec := do(e.h, http.MethodPut, "/api/account", `{"username":"newadmin","password":"a-brand-new-password"}`, e.cookie, nil)
	if rec.Code != 200 {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("the cookie is cleared: %q", rec.Header().Get("Set-Cookie"))
	}
	if rec := do(e.h, http.MethodGet, "/api/auth/me", "", e.cookie, nil); rec.Code != 401 {
		t.Fatalf("the old session is gone: %d", rec.Code)
	}
	if rec := do(e.h, http.MethodPost, "/api/auth/login", loginBody("admin", testPass), nil, nil); rec.Code != 401 {
		t.Fatalf("the old login no longer works: %d", rec.Code)
	}
	if rec := do(e.h, http.MethodPost, "/api/auth/login", loginBody("newadmin", "a-brand-new-password"), nil, nil); rec.Code != 200 {
		t.Fatalf("the new one does: %d", rec.Code)
	}
}

func TestPasswordIsKeptWhenOnlyTheLoginChanges(t *testing.T) {
	e := newEnv(t, false)
	if rec := do(e.h, http.MethodPut, "/api/account", `{"username":"renamed"}`, e.cookie, nil); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := do(e.h, http.MethodPost, "/api/auth/login", loginBody("renamed", testPass), nil, nil); rec.Code != 200 {
		t.Fatalf("an empty password means unchanged: %d", rec.Code)
	}
}

func TestDatabaseExportAndImportEndpoints(t *testing.T) {
	e := newEnv(t, false)
	tok := e.newToken(t)

	full := do(e.h, http.MethodGet, "/api/db/export?kind=full", "", e.cookie, nil)
	if full.Code != 200 || !strings.Contains(full.Header().Get("Content-Disposition"), "anypanel-full-") || !strings.Contains(full.Body.String(), `"kind":"full"`) {
		t.Fatalf("full export: %d %v", full.Code, full.Header())
	}
	if rec := do(e.h, http.MethodGet, "/api/db/export?kind=full", "", nil, bearer(tok)); rec.Code != 401 {
		t.Fatalf("the full file (password hash, bot token) is not for the API token: %d", rec.Code)
	}
	if rec := do(e.h, http.MethodGet, "/api/db/export?kind=full", "", nil, nil); rec.Code != 401 {
		t.Fatalf("auth: %d", rec.Code)
	}
	users := do(e.h, http.MethodGet, "/api/db/export?kind=users", "", nil, bearer(tok))
	if users.Code != 200 || !strings.Contains(users.Body.String(), `"kind":"users"`) || strings.Contains(users.Body.String(), "$argon2id$") {
		t.Fatalf("users export with a token: %d %s", users.Code, users.Body)
	}
	if rec := do(e.h, http.MethodGet, "/api/db/export?kind=nope", "", e.cookie, nil); rec.Code != 400 {
		t.Fatalf("unknown kind: %d", rec.Code)
	}

	// Import: the token may not, a bad file is refused, a dry run leaves the session alone, a real full import ends it.
	if rec := do(e.h, http.MethodPost, "/api/db/import", full.Body.String(), nil, bearer(tok)); rec.Code != 401 {
		t.Fatalf("import with a token: %d", rec.Code)
	}
	if rec := do(e.h, http.MethodPost, "/api/db/import", "hello", e.cookie, nil); rec.Code != 400 {
		t.Fatalf("garbage: %d %s", rec.Code, rec.Body)
	}
	dry := do(e.h, http.MethodPost, "/api/db/import?dryRun=1", full.Body.String(), e.cookie, nil)
	if dry.Code != 200 || !strings.Contains(dry.Body.String(), `"dryRun":true`) || strings.Contains(dry.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("dry run: %d %s", dry.Code, dry.Body)
	}
	rec := do(e.h, http.MethodPost, "/api/db/import", full.Body.String(), e.cookie, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"kind":"full"`) || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("full import: %d %s %v", rec.Code, rec.Body, rec.Header())
	}
	if rec := do(e.h, http.MethodGet, "/api/auth/me", "", e.cookie, nil); rec.Code != 401 {
		t.Fatalf("a full import signs everybody out: %d", rec.Code)
	}
	if rec := do(e.h, http.MethodPost, "/api/auth/login", loginBody("admin", testPass), nil, nil); rec.Code != 200 {
		t.Fatalf("the login from the file works: %d", rec.Code)
	}
}

func TestExternalNodesAPIAndSubscriptionFormats(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)

	vless := "vless://aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee@nl.example.net:443?type=tcp&security=reality&sni=www.apple.com&fp=chrome&pbk=PUB&sid=ab#NL"
	rec := do(h, http.MethodPost, "/api/external-nodes", `{"uri":"`+vless+`","enable":true}`, c, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"host":"nl.example.net"`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/api/external-nodes", `{"uri":"`+vless+`"}`, c, nil); rec.Code != 400 {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/external-nodes", `{"uri":"ss://x@h:1"}`, c, nil); rec.Code != 400 {
		t.Fatalf("unsupported scheme: %d", rec.Code)
	}
	imp := do(h, http.MethodPost, "/api/external-nodes/import", `{"text":"trojan://p@de.example.net:443?security=tls\nbroken"}`, c, nil)
	if imp.Code != 200 || !strings.Contains(imp.Body.String(), `"added":1`) || !strings.Contains(imp.Body.String(), `"line":2`) {
		t.Fatalf("import: %d %s", imp.Code, imp.Body)
	}
	if rec := do(h, http.MethodGet, "/api/external-nodes", "", nil, nil); rec.Code != 401 {
		t.Fatalf("auth: %d", rec.Code)
	}

	// The panel's own server plus both external ones reach every format.
	raw := do(h, http.MethodGet, "/sub/"+subID+"?raw=1", "", nil, nil)
	dec, _ := base64.StdEncoding.DecodeString(raw.Body.String())
	if n := strings.Count(string(dec), "://"); n != 3 || !strings.Contains(string(dec), "nl.example.net") {
		t.Fatalf("base64 body: %q", dec)
	}
	putSettings(t, h, c, `{"enable":true,"updateHours":12,"encode":true,"version":4,"formats":{"clashEnable":true}}`)
	clash := do(h, http.MethodGet, "/clash/"+subID, "", nil, map[string]string{"Accept": "*/*"})
	if clash.Code != 200 || !strings.Contains(clash.Body.String(), "de.example.net") || !strings.Contains(clash.Body.String(), "nl.example.net") {
		t.Fatalf("clash: %d %s", clash.Code, clash.Body)
	}

	var list []struct{ ID, Name string }
	decodeBody(t, do(h, http.MethodGet, "/api/external-nodes", "", c, nil), &list)
	if len(list) != 2 {
		t.Fatalf("list: %+v", list)
	}
	if rec := do(h, http.MethodPut, "/api/external-nodes/"+list[0].ID, `{"enable":false}`, c, nil); rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	raw = do(h, http.MethodGet, "/sub/"+subID+"?raw=1", "", nil, nil)
	dec, _ = base64.StdEncoding.DecodeString(raw.Body.String())
	if n := strings.Count(string(dec), "://"); n != 2 {
		t.Fatalf("a disabled node leaves subscriptions at once: %q", dec)
	}
	if rec := do(h, http.MethodDelete, "/api/external-nodes/"+list[1].ID, "", c, nil); rec.Code != 200 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do(h, http.MethodDelete, "/api/external-nodes/"+list[1].ID, "", c, nil); rec.Code != 404 {
		t.Fatalf("delete twice: %d", rec.Code)
	}
}

func TestXrayLogsAreNeverJSONNull(t *testing.T) {
	e := newEnv(t, false)
	rec := do(e.h, http.MethodGet, "/api/xray/logs", "", e.cookie, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"lines":[]`) {
		t.Fatalf("an empty log must be [], the UI calls .length on it: %s", rec.Body)
	}
}

func TestNewClientsGetVisionFlowUnlessToldOtherwise(t *testing.T) {
	e := newEnv(t, false)
	tok := e.newToken(t)
	create := func(body string, hdr map[string]string, c *http.Cookie) string {
		rec := do(e.h, http.MethodPost, "/api/clients", body, c, hdr)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	if got := create(`{"email":"api-default"}`, bearer(tok), nil); !strings.Contains(got, `"flow":"xtls-rprx-vision"`) {
		t.Fatalf("a client made through the API without a flow gets xtls-rprx-vision: %s", got)
	}
	if got := create(`{"email":"api-off","flow":""}`, bearer(tok), nil); !strings.Contains(got, `"flow":""`) {
		t.Fatalf("an explicit empty flow stays empty: %s", got)
	}
	if got := create(`{"email":"form","flow":"xtls-rprx-vision"}`, nil, e.cookie); !strings.Contains(got, `"flow":"xtls-rprx-vision"`) {
		t.Fatalf("%s", got)
	}
}

func TestNodeActionsNeedASession(t *testing.T) {
	e := newEnv(t, false)
	tok := e.newToken(t)
	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/servers/x/reality", `{"domain":"a.example.com"}`},
		{http.MethodPost, "/api/servers/x/open-ports", ""},
	} {
		if rec := do(e.h, r.method, r.path, r.body, nil, bearer(tok)); rec.Code != 401 {
			t.Errorf("%s %s with a token: want 401, got %d", r.method, r.path, rec.Code)
		}
		if rec := do(e.h, r.method, r.path, r.body, e.cookie, nil); rec.Code != 404 {
			t.Errorf("%s %s for an unknown server: want 404, got %d %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
}

func TestRoutingPresetsEndpoint(t *testing.T) {
	e := newEnv(t, false)
	if rec := do(e.h, http.MethodGet, "/api/routing-presets?app=happ", "", nil, nil); rec.Code != 401 {
		t.Fatalf("auth: %d", rec.Code)
	}
	rec := do(e.h, http.MethodGet, "/api/routing-presets?app=happ", "", e.cookie, nil)
	var ps []struct{ ID, Link, Source, Name string }
	if err := json.Unmarshal(rec.Body.Bytes(), &ps); err != nil || rec.Code != 200 || len(ps) != 3 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	if ps[0].ID != "default" || ps[1].ID != "whitelist" || !strings.HasPrefix(ps[0].Link, "happ://routing/onadd/") || ps[0].Source != "embedded" || ps[0].Name != "RoscomVPN" {
		t.Fatalf("%+v", ps)
	}
	rec = do(e.h, http.MethodGet, "/api/routing-presets?app=incy", "", e.cookie, nil)
	if !strings.Contains(rec.Body.String(), "incy://routing/onadd/") {
		t.Fatalf("INCY has its own links: %s", rec.Body)
	}
	if rec := do(e.h, http.MethodGet, "/api/routing-presets?app=nope", "", e.cookie, nil); rec.Code != 400 {
		t.Fatalf("unknown app: %d", rec.Code)
	}
}

func TestNewClientsViaTheAPIJoinEveryConnectionUnlessToldOtherwise(t *testing.T) {
	e := newEnv(t, false)
	tok := e.newToken(t)
	mk := func(body string) string {
		rec := do(e.h, http.MethodPost, "/api/inbounds", body, e.cookie, nil)
		if rec.Code != 200 {
			t.Fatalf("inbound: %d %s", rec.Code, rec.Body)
		}
		var out struct{ Item struct{ ID string } }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Item.ID
	}
	on := mk(`{"remark":"on","enable":true,"protocol":"vless","listen":"127.0.0.1","port":10201,"network":"tcp","security":"none"}`)
	off := mk(`{"remark":"off","enable":false,"protocol":"vless","listen":"127.0.0.1","port":10202,"network":"tcp","security":"none"}`)

	create := func(body string) string {
		rec := do(e.h, http.MethodPost, "/api/clients", body, nil, bearer(tok))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	if got := create(`{"email":"auto"}`); !strings.Contains(got, on) || !strings.Contains(got, off) {
		t.Fatalf("without inboundIds the client joins every connection, switched on or off: %s", got)
	}
	if got := create(`{"email":"none","inboundIds":[]}`); strings.Contains(got, on) {
		t.Fatalf("an explicit empty list means none: %s", got)
	}
	if got := create(`{"email":"chosen","inboundIds":["` + off + `"]}`); !strings.Contains(got, off) || strings.Contains(got, on) {
		t.Fatalf("an explicit list is kept as given: %s", got)
	}
}

func TestNewConnectionViaTheAPIJoinsExistingClientsUnlessTold(t *testing.T) {
	e := newEnv(t, false)
	c := do(e.h, http.MethodPost, "/api/clients", `{"email":"early","enable":true}`, e.cookie, nil)
	if c.Code != 200 {
		t.Fatalf("%d %s", c.Code, c.Body)
	}
	mk := func(extra string, port int) string {
		body := fmt.Sprintf(`{"remark":"in%d","enable":true,"protocol":"vless","listen":"127.0.0.1","port":%d,"network":"tcp","security":"none"%s}`, port, port, extra)
		rec := do(e.h, http.MethodPost, "/api/inbounds", body, e.cookie, nil)
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		var out struct{ Item struct{ ID string } }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Item.ID
	}
	auto, quiet := mk("", 10501), mk(`,"addToClients":false`, 10502)
	list := do(e.h, http.MethodGet, "/api/clients", "", e.cookie, nil).Body.String()
	if !strings.Contains(list, auto) || strings.Contains(list, quiet) {
		t.Fatalf("the new connection joins the existing client, the one told not to does not: %s", list)
	}
}

func TestCascadeAPIRules(t *testing.T) {
	e := newEnv(t, false)
	tok := e.newToken(t)
	if rec := do(e.h, http.MethodGet, "/api/cascades", "", nil, nil); rec.Code != 401 {
		t.Fatalf("auth: %d", rec.Code)
	}
	if rec := do(e.h, http.MethodGet, "/api/cascades", "", nil, bearer(tok)); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("an empty list is [], and the token may read it: %d %s", rec.Code, rec.Body)
	}
	for _, r := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/cascades", `{"name":"x"}`},
		{http.MethodPut, "/api/cascades/x", `{"name":"x"}`},
		{http.MethodDelete, "/api/cascades/x", ""},
		{http.MethodPost, "/api/cascades/check-domain", `{"domain":"x.example.com"}`},
		{http.MethodPost, "/api/main-node", `{"domain":"x.example.com"}`},
	} {
		if rec := do(e.h, r.method, r.path, r.body, nil, bearer(tok)); rec.Code != 401 {
			t.Errorf("%s %s with a token must be refused (it rewrites other machines): %d", r.method, r.path, rec.Code)
		}
	}
	if rec := do(e.h, http.MethodPost, "/api/cascades", `{"name":"x"}`, e.cookie, nil); rec.Code != 400 {
		t.Fatalf("entry and exit are both the main server: %d %s", rec.Code, rec.Body)
	}
	if rec := do(e.h, http.MethodPost, "/api/main-node", `{"domain":"x.example.com"}`, e.cookie, nil); rec.Code != 400 {
		t.Fatalf("a server that does not manage nginx says so: %d %s", rec.Code, rec.Body)
	}
	if rec := do(e.h, http.MethodDelete, "/api/cascades/nope", "", e.cookie, nil); rec.Code != 404 {
		t.Fatalf("unknown cascade: %d", rec.Code)
	}
	rec := do(e.h, http.MethodPost, "/api/cascades/check-domain", `{"domain":"not a domain"}`, e.cookie, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "problem") {
		t.Fatalf("a bad name is an answer, not an error: %d %s", rec.Code, rec.Body)
	}
}
