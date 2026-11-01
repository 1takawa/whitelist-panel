package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func loggedIn(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec := do(h, http.MethodPost, "/api/auth/login", loginBody("admin", testPass), nil, nil)
	if rec.Code != 200 {
		t.Fatalf("login: %d", rec.Code)
	}
	return sessionCookieFrom(t, rec)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body)
	}
}

func TestAPIRequiresAuth(t *testing.T) {
	h := newHandler(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/inbounds"}, {"POST", "/api/inbounds"}, {"GET", "/api/clients"},
		{"GET", "/api/xray/status"}, {"GET", "/api/settings"}, {"PUT", "/api/settings"},
		{"GET", "/api/reality/keys"}, {"DELETE", "/api/inbounds/x"}, {"GET", "/api/clients/x/links"},
	} {
		if rec := do(h, tc.method, tc.path, "{}", nil, nil); rec.Code != 401 {
			t.Errorf("%s %s without a session: want 401, got %d", tc.method, tc.path, rec.Code)
		}
	}
}

func TestAPIMutationsNeedCSRFHeader(t *testing.T) {
	h := newHandler(t)
	c := loggedIn(t, h)
	rec := do(h, http.MethodPost, "/api/inbounds", `{}`, c, map[string]string{csrfHeader: ""})
	if rec.Code != 403 {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestInboundClientLinkFlow(t *testing.T) {
	h := newHandler(t)
	c := loggedIn(t, h)

	if rec := do(h, http.MethodPut, "/api/settings", `{"panelDomain":"panel.example.com","realityDomain":"reality.example.com"}`, c, nil); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}

	keys := do(h, http.MethodGet, "/api/reality/keys", "", c, nil)
	var k map[string]string
	decodeBody(t, keys, &k)
	if len(k["privateKey"]) != 43 || len(k["publicKey"]) != 43 || len(k["shortId"]) != 16 {
		t.Fatalf("reality keys: %v", k)
	}

	in := `{"remark":"DE","enable":true,"protocol":"vless","listen":"127.0.0.1","port":8443,"network":"tcp","security":"reality",
		"reality":{"dest":"yandex.ru:443","serverNames":["yandex.ru"]},"public":{"port":443}}`
	rec := do(h, http.MethodPost, "/api/inbounds", in, c, nil)
	if rec.Code != 200 {
		t.Fatalf("create inbound: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Item struct {
			ID      string `json:"id"`
			Reality struct {
				PublicKey string `json:"publicKey"`
			} `json:"reality"`
		} `json:"item"`
		Warning string `json:"warning"`
	}
	decodeBody(t, rec, &created)
	if created.Item.ID == "" || created.Item.Reality.PublicKey == "" || created.Warning != "" {
		t.Fatalf("created: %+v", created)
	}

	if dup := do(h, http.MethodPost, "/api/inbounds", strings.Replace(in, `"remark":"DE"`, `"remark":"dup"`, 1), c, nil); dup.Code != 400 {
		t.Fatalf("duplicate port must be 400, got %d %s", dup.Code, dup.Body)
	}

	cl := `{"email":"alice","enable":true,"flow":"xtls-rprx-vision","inboundIds":["` + created.Item.ID + `"]}`
	rec = do(h, http.MethodPost, "/api/clients", cl, c, nil)
	if rec.Code != 200 {
		t.Fatalf("create client: %d %s", rec.Code, rec.Body)
	}
	var client struct {
		Item struct {
			ID, UUID string
			SubID    string `json:"subId"`
		} `json:"item"`
	}
	decodeBody(t, rec, &client)
	if client.Item.UUID == "" || client.Item.SubID == "" {
		t.Fatalf("client: %+v", client)
	}

	lrec := do(h, http.MethodGet, "/api/clients/"+client.Item.ID+"/links", "", c, nil)
	var lv struct {
		SubscriptionURL string `json:"subscriptionUrl"`
		Links           []struct{ Link, Error string }
	}
	decodeBody(t, lrec, &lv)
	if len(lv.Links) != 1 || lv.Links[0].Error != "" || !strings.HasPrefix(lv.Links[0].Link, "vless://"+client.Item.UUID+"@reality.example.com:443?") {
		t.Fatalf("links: %+v", lv)
	}
	if lv.SubscriptionURL != "https://panel.example.com/sub/"+client.Item.SubID {
		t.Fatalf("subscription url: %q", lv.SubscriptionURL)
	}

	list := do(h, http.MethodGet, "/api/inbounds", "", c, nil)
	if !strings.Contains(list.Body.String(), `"clients":1`) {
		t.Fatalf("inbound list must report the client count: %s", list.Body)
	}

	if del := do(h, http.MethodDelete, "/api/clients/"+client.Item.ID, "", c, nil); del.Code != 200 {
		t.Fatalf("delete client: %d", del.Code)
	}
	if del := do(h, http.MethodDelete, "/api/inbounds/"+created.Item.ID, "", c, nil); del.Code != 200 {
		t.Fatalf("delete inbound: %d", del.Code)
	}
	if gone := do(h, http.MethodGet, "/api/inbounds/"+created.Item.ID, "", c, nil); gone.Code != 404 {
		t.Fatalf("deleted inbound must be 404, got %d", gone.Code)
	}
}

func TestAPIErrors(t *testing.T) {
	h := newHandler(t)
	c := loggedIn(t, h)
	if rec := do(h, http.MethodPost, "/api/inbounds", `not json`, c, nil); rec.Code != 400 {
		t.Fatalf("bad JSON: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/api/inbounds", `{"remark":"x","protocol":"socks"}`, c, nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "protocol") {
		t.Fatalf("validation error must be 400 with a message: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/clients/nope", "", c, nil); rec.Code != 404 {
		t.Fatalf("unknown client: %d", rec.Code)
	}
	if rec := do(h, http.MethodPut, "/api/settings", `{"panelDomain":"bad domain"}`, c, nil); rec.Code != 400 {
		t.Fatalf("bad domain: %d", rec.Code)
	}
}

func TestXrayStatusWithoutBinary(t *testing.T) {
	h := newHandler(t)
	c := loggedIn(t, h)
	rec := do(h, http.MethodGet, "/api/xray/status", "", c, nil)
	var st struct{ Installed, Running bool }
	decodeBody(t, rec, &st)
	if rec.Code != 200 || st.Installed || st.Running {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	if logs := do(h, http.MethodGet, "/api/xray/logs", "", c, nil); logs.Code != 200 {
		t.Fatalf("logs: %d", logs.Code)
	}
}
