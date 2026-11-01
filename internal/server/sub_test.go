package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// subFixture creates domains, an inbound and a client through the API and returns the client's subscription id.
func subFixture(t *testing.T, h http.Handler, extraClient string) (subID, uuid string) {
	t.Helper()
	c := loggedIn(t, h)
	if rec := do(h, http.MethodPut, "/api/settings", `{"panelDomain":"panel.example.com","realityDomain":"reality.example.com"}`, c, nil); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	in := `{"remark":"DE","enable":true,"protocol":"vless","listen":"127.0.0.1","port":8443,"network":"tcp","security":"reality",
		"reality":{"dest":"127.0.0.1:7443","serverNames":["reality.example.com"]},"public":{"port":443}}`
	rec := do(h, http.MethodPost, "/api/inbounds", in, c, nil)
	var created struct{ Item struct{ ID string } }
	decodeBody(t, rec, &created)
	body := `{"email":"alice","enable":true,"totalBytes":5000,"inboundIds":["` + created.Item.ID + `"]` + extraClient + `}`
	rec = do(h, http.MethodPost, "/api/clients", body, c, nil)
	if rec.Code != 200 {
		t.Fatalf("client: %d %s", rec.Code, rec.Body)
	}
	var cl struct {
		Item struct {
			UUID  string `json:"uuid"`
			SubID string `json:"subId"`
		}
	}
	decodeBody(t, rec, &cl)
	return cl.Item.SubID, cl.Item.UUID
}

func TestSubscriptionForApps(t *testing.T) {
	h := newHandler(t)
	subID, uuid := subFixture(t, h, "")

	// No cookie: the endpoint is public.
	rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*"})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil {
		t.Fatalf("body must be base64: %v", err)
	}
	if !strings.HasPrefix(string(raw), "vless://"+uuid+"@reality.example.com:443?") || strings.Contains(string(raw), "\n") {
		t.Fatalf("decoded body: %q", raw)
	}
	if got := rec.Header().Get("Subscription-Userinfo"); got != "upload=0; download=0; total=5000" {
		t.Fatalf("userinfo: %q", got)
	}
	if rec.Header().Get("Profile-Title") != "whitelist" || rec.Header().Get("Profile-Update-Interval") != "12" {
		t.Fatalf("default profile headers missing: %v", rec.Header())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("content headers: %v", rec.Header())
	}

	if rec := do(h, http.MethodGet, "/sub/does-not-exist", "", nil, map[string]string{"Accept": "*/*"}); rec.Code != 404 {
		t.Fatalf("unknown id: %d", rec.Code)
	}
}

func TestSubscriptionBrowserGetsTheUserPage(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "text/html,application/xhtml+xml"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Whitelist Panel") || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("browser must get the SPA: %d %s", rec.Code, rec.Body)
	}
	// ?raw=1 forces the machine-readable form even for a browser-like Accept header.
	raw := do(h, http.MethodGet, "/sub/"+subID+"?raw=1", "", nil, map[string]string{"Accept": "text/html"})
	if _, err := base64.StdEncoding.DecodeString(raw.Body.String()); err != nil || strings.Contains(raw.Body.String(), "<") {
		t.Fatalf("raw=1 must return the subscription: %q", raw.Body)
	}
}

func TestPublicSubJSON(t *testing.T) {
	h := newHandler(t)
	subID, uuid := subFixture(t, h, "")
	rec := do(h, http.MethodGet, "/api/sub/"+subID, "", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var v struct {
		Name, Title, Status, URL string
		Total                    int64
		Links                    []struct{ Remark, Link string }
	}
	decodeBody(t, rec, &v)
	if v.Name != "alice" || v.Status != "active" || v.Total != 5000 || v.URL != "https://panel.example.com/sub/"+subID || v.Title != "whitelist" {
		t.Fatalf("view: %+v", v)
	}
	if len(v.Links) != 1 || !strings.Contains(v.Links[0].Link, uuid) {
		t.Fatalf("links: %+v", v.Links)
	}
	for _, leak := range []string{"password", "uuid", "inboundIds", "id", "subId", "createdAt", "comment"} {
		if strings.Contains(rec.Body.String(), `"`+leak) {
			t.Errorf("public view must not expose %q: %s", leak, rec.Body)
		}
	}
	if do(h, http.MethodGet, "/api/sub/nope", "", nil, nil).Code != 404 {
		t.Fatal("unknown id must be 404")
	}
}

func TestSubscriptionSwitchedOffAndDisabledInbound(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)

	// Disabled inbounds drop out of the subscription.
	var list []struct{ ID string }
	decodeBody(t, do(h, http.MethodGet, "/api/inbounds", "", c, nil), &list)
	cur := do(h, http.MethodGet, "/api/inbounds/"+list[0].ID, "", c, nil)
	var in map[string]any
	_ = json.Unmarshal(cur.Body.Bytes(), &in)
	in["enable"] = false
	b, _ := json.Marshal(in)
	if rec := do(h, http.MethodPut, "/api/inbounds/"+list[0].ID, string(b), c, nil); rec.Code != 200 {
		t.Fatalf("disable inbound: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*"}); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("a subscription without enabled inbounds is empty: %d %q", rec.Code, rec.Body)
	}

	// Switching subscriptions off hides every id.
	off := `{"panelDomain":"panel.example.com","subscription":{"enable":false,"title":"x","updateHours":12}}`
	if rec := do(h, http.MethodPut, "/api/settings", off, c, nil); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*"}); rec.Code != 404 {
		t.Fatalf("disabled subscriptions must be 404, got %d", rec.Code)
	}
}

func TestSettingsDefaultsAndValidation(t *testing.T) {
	h := newHandler(t)
	c := loggedIn(t, h)

	got := do(h, http.MethodGet, "/api/settings", "", c, nil)
	if !strings.Contains(got.Body.String(), `"enable":true`) || !strings.Contains(got.Body.String(), `"title":"whitelist"`) || !strings.Contains(got.Body.String(), `"updateHours":12`) {
		t.Fatalf("unsaved settings must come with subscription defaults: %s", got.Body)
	}
	// A body that only carries domains must not switch subscriptions off.
	if rec := do(h, http.MethodPut, "/api/settings", `{"panelDomain":"panel.example.com"}`, c, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"enable":true`) {
		t.Fatalf("domains-only update: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"bad support url": `{"subscription":{"enable":true,"updateHours":12,"supportUrl":"javascript:alert(1)"}}`,
		"bad interval":    `{"subscription":{"enable":true,"updateHours":99999}}`,
		"long title":      `{"subscription":{"enable":true,"updateHours":12,"title":"` + strings.Repeat("я", 101) + `"}}`,
	} {
		if rec := do(h, http.MethodPut, "/api/settings", body, c, nil); rec.Code != 400 {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body)
		}
	}
	ok := `{"panelDomain":"panel.example.com","subscription":{"enable":true,"updateHours":6,"title":"Мой VPN","supportUrl":"https://t.me/support","announce":"Привет"}}`
	if rec := do(h, http.MethodPut, "/api/settings", ok, c, nil); rec.Code != 200 {
		t.Fatalf("valid settings: %d %s", rec.Code, rec.Body)
	}
}

func TestSubscriptionPathIsConfigurable(t *testing.T) {
	h := newHandler(t)
	subID, uuid := subFixture(t, h, "")
	c := loggedIn(t, h)

	set := `{"panelDomain":"panel.example.com","subscription":{"enable":true,"updateHours":12,"path":"/s/abc123/"}}`
	if rec := do(h, http.MethodPut, "/api/settings", set, c, nil); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	app := map[string]string{"Accept": "*/*"}

	rec := do(h, http.MethodGet, "/s/abc123/"+subID, "", nil, app)
	raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if rec.Code != 200 || err != nil || !strings.Contains(string(raw), uuid) {
		t.Fatalf("the new prefix must serve the subscription: %d %v %q", rec.Code, err, rec.Body)
	}
	// The old prefix stops serving subscriptions at once (the settings handler resets the cache).
	old := do(h, http.MethodGet, "/sub/"+subID, "", nil, app)
	if _, err := base64.StdEncoding.DecodeString(old.Body.String()); err == nil && strings.Contains(old.Body.String(), "vless") {
		t.Fatalf("the old prefix must not serve the subscription any more: %q", old.Body)
	}
	if strings.Contains(old.Header().Get("Subscription-Userinfo"), "upload") {
		t.Fatal("old prefix still sends subscription headers")
	}
	// A browser on the new prefix gets the user page.
	page := do(h, http.MethodGet, "/s/abc123/"+subID, "", nil, map[string]string{"Accept": "text/html"})
	if !strings.Contains(page.Body.String(), "Whitelist Panel") {
		t.Fatalf("user page on the new prefix: %q", page.Body)
	}
	// Nested paths are not subscription ids.
	if rec := do(h, http.MethodGet, "/s/abc123/"+subID+"/extra", "", nil, app); strings.Contains(rec.Header().Get("Subscription-Userinfo"), "upload") {
		t.Fatal("only <prefix><id> is a subscription")
	}
}

func TestPlainTextSubscription(t *testing.T) {
	h := newHandler(t)
	subID, uuid := subFixture(t, h, "")
	c := loggedIn(t, h)
	set := `{"panelDomain":"panel.example.com","subscription":{"enable":true,"updateHours":12,"encode":false,"version":3}}`
	if rec := do(h, http.MethodPut, "/api/settings", set, c, nil); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*"})
	if !strings.HasPrefix(rec.Body.String(), "vless://"+uuid) {
		t.Fatalf("with encode off the body is the links themselves: %q", rec.Body)
	}
}

func TestInfoNodeInTheSubscriptionBody(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)
	set := `{"panelDomain":"panel.example.com","subscription":{"enable":true,"updateHours":12,"encode":false,"infoNode":true,"version":3}}`
	if rec := do(h, http.MethodPut, "/api/settings", set, c, nil); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	lines := strings.Split(do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*"}).Body.String(), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "socks://127.0.0.1:1080#") || !strings.HasPrefix(lines[1], "vless://") {
		t.Fatalf("info node first, then the real link: %q", lines)
	}
}

func putSettings(t *testing.T, h http.Handler, c *http.Cookie, subscription string) {
	t.Helper()
	body := `{"panelDomain":"panel.example.com","subscription":` + subscription + `}`
	if rec := do(h, http.MethodPut, "/api/settings", body, c, nil); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
}

func TestClashAndJSONFormats(t *testing.T) {
	h := newHandler(t)
	subID, uuid := subFixture(t, h, "")
	c := loggedIn(t, h)
	app := map[string]string{"Accept": "*/*"}

	// Off by default: the format paths do not exist.
	if rec := do(h, http.MethodGet, "/clash/"+subID, "", nil, app); strings.Contains(rec.Body.String(), "proxy-groups") {
		t.Fatal("Clash must be off until it is enabled")
	}

	putSettings(t, h, c, `{"enable":true,"updateHours":12,"encode":true,"version":4,"title":"VPN",
	  "formats":{"clashEnable":true,"clashEnableRouting":true,"clashRules":"DOMAIN-SUFFIX,ru,DIRECT","clashRemoteDns":true,"jsonEnable":true,"jsonAlwaysArray":true,
	             "jsonRules":"[{\"type\":\"field\",\"domain\":[\"domain:ru\"],\"outboundTag\":\"direct\"}]"}}`)

	clash := do(h, http.MethodGet, "/clash/"+subID, "", nil, app)
	if clash.Code != 200 || !strings.HasPrefix(clash.Header().Get("Content-Type"), "text/yaml") {
		t.Fatalf("clash: %d %s", clash.Code, clash.Header().Get("Content-Type"))
	}
	for _, want := range []string{"proxy-groups:", "type: vless", "uuid: " + uuid, "DOMAIN-SUFFIX,ru,DIRECT", "MATCH,Proxy", "reality-opts:", "enable: true"} {
		if !strings.Contains(clash.Body.String(), want) {
			t.Errorf("clash profile lacks %q:\n%s", want, clash.Body)
		}
	}
	if clash.Header().Get("Subscription-Userinfo") == "" || clash.Header().Get("Profile-Title") != "VPN" {
		t.Fatalf("the common subscription headers accompany every format: %v", clash.Header())
	}

	js := do(h, http.MethodGet, "/json/"+subID, "", nil, app)
	var configs []map[string]any
	if js.Code != 200 || json.Unmarshal(js.Body.Bytes(), &configs) != nil || len(configs) != 1 {
		t.Fatalf("json (always-array): %d %s", js.Code, js.Body)
	}
	if !strings.HasPrefix(js.Header().Get("Content-Type"), "application/json") || !strings.Contains(js.Body.String(), uuid) || !strings.Contains(js.Body.String(), "domain:ru") {
		t.Fatalf("json config: %s", js.Body)
	}

	// The main path stays the plain subscription for ordinary apps...
	raw := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*", "User-Agent": "Clash.Meta/1.18"})
	if _, err := base64.StdEncoding.DecodeString(raw.Body.String()); err != nil || strings.Contains(raw.Body.String(), "proxies:") {
		t.Fatal("without auto-detect the main path is never Clash")
	}
	// ...until auto-detection is switched on.
	putSettings(t, h, c, `{"enable":true,"updateHours":12,"encode":true,"version":4,"formats":{"clashEnable":true,"clashAutoDetect":true,"jsonEnable":true,"jsonAutoDetect":true,"jsonUaRegex":"(?i)streisand"}}`)
	for ua, want := range map[string]string{"Clash.Meta/1.18": "proxy-groups:", "FlClash/0.8": "proxy-groups:", "Streisand/1.6": `"outbounds"`} {
		rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*", "User-Agent": ua})
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s on the main path: want %q, got %.80s", ua, want, rec.Body)
		}
	}
	v2 := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*", "User-Agent": "v2rayNG/1.8"})
	if _, err := base64.StdEncoding.DecodeString(v2.Body.String()); err != nil {
		t.Fatalf("an app that matches nothing still gets the base64 list: %q", v2.Body)
	}
}

func TestFormatsShowTheInfoNodeAndExplainExpiry(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, `,"expiryAt":"2020-01-01T00:00:00Z"`)
	c := loggedIn(t, h)
	putSettings(t, h, c, `{"enable":true,"updateHours":12,"encode":true,"version":4,"infoNode":true,"formats":{"clashEnable":true,"jsonEnable":true}}`)
	app := map[string]string{"Accept": "*/*"}

	clash := do(h, http.MethodGet, "/clash/"+subID, "", nil, app).Body.String()
	if !strings.Contains(clash, "type: socks5") || strings.Contains(clash, "type: vless") || !strings.Contains(clash, "Срок истёк") {
		t.Fatalf("an expired client gets only the explanation in Clash:\n%s", clash)
	}
	js := do(h, http.MethodGet, "/json/"+subID, "", nil, app).Body.String()
	if !strings.Contains(js, `"protocol": "socks"`) || strings.Contains(js, `"vless"`) || !strings.Contains(js, "Срок истёк") {
		t.Fatalf("an expired client gets only the explanation in JSON:\n%s", js)
	}
}

func TestFormatSettingsValidation(t *testing.T) {
	h := newHandler(t)
	c := loggedIn(t, h)
	for name, sub := range map[string]string{
		"overlapping paths":   `{"enable":true,"updateHours":12,"path":"/s/","formats":{"clashPath":"/s/c/"}}`,
		"same path":           `{"enable":true,"updateHours":12,"formats":{"clashPath":"/sub/"}}`,
		"reserved path":       `{"enable":true,"updateHours":12,"formats":{"jsonPath":"/api/j/"}}`,
		"bad regex":           `{"enable":true,"updateHours":12,"formats":{"clashUaRegex":"(unclosed"}}`,
		"autodetect no regex": `{"enable":true,"updateHours":12,"formats":{"jsonAutoDetect":true}}`,
		"bad clash rule":      `{"enable":true,"updateHours":12,"formats":{"clashRules":"nonsense"}}`,
		"routing without":     `{"enable":true,"updateHours":12,"formats":{"clashEnableRouting":true}}`,
		"bad mux json":        `{"enable":true,"updateHours":12,"formats":{"jsonMux":"{oops"}}`,
		"rules not array":     `{"enable":true,"updateHours":12,"formats":{"jsonRules":"{}"}}`,
	} {
		body := `{"panelDomain":"panel.example.com","subscription":` + sub + `}`
		if rec := do(h, http.MethodPut, "/api/settings", body, c, nil); rec.Code != 400 {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestAppSpecificHeadersAndRouting(t *testing.T) {
	h := newHandler(t)
	subID, _ := subFixture(t, h, "")
	c := loggedIn(t, h)

	set := `{"panelDomain":"panel.example.com","subscription":{"enable":true,"updateHours":12,"encode":false,"version":4,"hideSettings":true,
	  "happ":{"autoDetect":true,"providerId":"prov1","subInfoText":"Hello","enableRouting":true,"routingRules":"happ://routing/onadd/e30="},
	  "incy":{"autoDetect":true,"sortOrder":"ping","enableRouting":true,"routingRules":"incy://routing/abc"}}}`
	if rec := do(h, http.MethodPut, "/api/settings", set, c, nil); rec.Code != 200 {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	get := func(ua string) (map[string]string, []string) {
		rec := do(h, http.MethodGet, "/sub/"+subID, "", nil, map[string]string{"Accept": "*/*", "User-Agent": ua})
		hdr := map[string]string{}
		for k := range rec.Header() {
			hdr[k] = rec.Header().Get(k)
		}
		return hdr, strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	}

	happ, body := get("Happ/1.5 (Android)")
	if happ["Providerid"] != "prov1" || happ["Sub-Info-Text"] != "Hello" || happ["Routing"] != "happ://routing/onadd/e30=" || happ["Routing-Enable"] != "true" || happ["Hide-Settings"] != "1" {
		t.Fatalf("Happ headers: %v", happ)
	}
	if len(body) != 1 || !strings.HasPrefix(body[0], "vless://") {
		t.Fatalf("Happ gets only the real link: %v", body)
	}

	incy, body := get("INCY/2.0/ios")
	if incy["Sort-Order"] != "ping" || incy["Routing"] != "" || incy["Providerid"] != "" || incy["Hide-Settings"] != "1" {
		t.Fatalf("INCY headers: %v", incy)
	}
	if len(body) != 2 || body[1] != "incy://routing/abc" {
		t.Fatalf("INCY gets its routing line after the links: %v", body)
	}

	other, body := get("v2rayNG/1.8.5")
	if other["Providerid"] != "" || other["Sort-Order"] != "" || other["Sub-Info-Text"] != "" {
		t.Fatalf("other apps must not receive app headers: %v", other)
	}
	if other["Routing"] != "happ://routing/onadd/e30=" || len(body) != 1 {
		t.Fatalf("routing is offered to ordinary clients, the INCY line is not: %v %v", other, body)
	}
}

func TestPreviewAndTokensEndpoints(t *testing.T) {
	h := newHandler(t)
	if do(h, http.MethodPost, "/api/settings/preview", `{}`, nil, nil).Code != 401 || do(h, http.MethodGet, "/api/settings/tokens", "", nil, nil).Code != 401 {
		t.Fatal("both need a session")
	}
	c := loggedIn(t, h)
	rec := do(h, http.MethodPost, "/api/settings/preview", `{"kind":"remark","template":"{{EMAIL}}|📊{{TRAFFIC_LEFT}}"}`, c, nil)
	var out struct{ Result string }
	decodeBody(t, rec, &out)
	if rec.Code != 200 || out.Result != "alice|📊7.50GB" {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	if do(h, http.MethodPost, "/api/settings/preview", `{"kind":"bogus","template":"x"}`, c, nil).Code != 400 {
		t.Fatal("unknown kind must be 400")
	}
	toks := do(h, http.MethodGet, "/api/settings/tokens", "", c, nil)
	if toks.Code != 200 || !strings.Contains(toks.Body.String(), `"TRAFFIC_LEFT"`) {
		t.Fatalf("tokens: %d %s", toks.Code, toks.Body)
	}
}

func TestSubscriptionRateLimited(t *testing.T) {
	h := newHandler(t)
	var last int
	for i := 0; i < 130; i++ {
		last = do(h, http.MethodGet, "/sub/guess-"+strings.Repeat("x", i%5), "", nil, map[string]string{"Accept": "*/*"}).Code
	}
	if last != 429 {
		t.Fatalf("after 120 requests a client must be throttled, got %d", last)
	}
}
