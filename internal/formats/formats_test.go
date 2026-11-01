package formats

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"anypanel2/internal/links"
	"anypanel2/internal/model"
)

func eps() []links.Endpoint {
	return []links.Endpoint{
		{Name: "DE Reality", Protocol: model.ProtoVLESS, Host: "reality.example.com", Port: 443, UUID: "u-1", Flow: model.FlowVision, Network: model.NetTCP,
			Security: model.SecReality, SNI: "reality.example.com", Fingerprint: "chrome", PublicKey: "PUB", ShortID: "ab12", SpiderX: "/"},
		{Name: "WS", Protocol: model.ProtoVLESS, Host: "panel.example.com", Port: 443, UUID: "u-1", Network: model.NetWS, Security: model.SecTLS,
			SNI: "panel.example.com", Fingerprint: "chrome", ALPN: []string{"h2", "http/1.1"}, Path: "/ws", HostHeader: "h.example.com"},
		{Name: "gRPC", Protocol: model.ProtoTrojan, Host: "panel.example.com", Port: 443, Password: "pw", Network: model.NetGRPC, Security: model.SecTLS,
			SNI: "panel.example.com", Fingerprint: "chrome", ServiceName: "svc", MultiMode: true},
		{Name: "XHTTP", Protocol: model.ProtoVLESS, Host: "panel.example.com", Port: 443, UUID: "u-1", Network: model.NetXHTTP, Security: model.SecTLS,
			SNI: "panel.example.com", Fingerprint: "chrome", Path: "/x", Mode: "auto"},
		{Name: "Upgrade", Protocol: model.ProtoTrojan, Host: "panel.example.com", Port: 443, Password: "pw", Network: model.NetHTTPUpgrade, Security: model.SecTLS,
			SNI: "panel.example.com", Fingerprint: "chrome", Path: "/u"},
		{Name: "Plain", Protocol: model.ProtoVLESS, Host: "203.0.113.7", Port: 80, UUID: "u-1", Network: model.NetTCP, Security: model.SecNone},
	}
}

func TestClashProfile(t *testing.T) {
	raw, err := Clash(eps(), ClashOptions{Info: []string{"📊 5GB"}, Rules: []string{"DOMAIN-SUFFIX,ru,DIRECT"}, RemoteDNS: true})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Mode    string
		DNS     map[string]any
		Proxies []map[string]any
		Groups  []struct {
			Name    string
			Type    string
			Proxies []string
		} `yaml:"proxy-groups"`
		Rules []string
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("output must be valid YAML: %v\n%s", err, raw)
	}
	if len(cfg.Proxies) != 7 || cfg.Proxies[0]["type"] != "socks5" || cfg.Proxies[0]["name"] != "📊 5GB" {
		t.Fatalf("the info node comes first as a dummy socks5 proxy: %v", cfg.Proxies[0])
	}
	byName := map[string]map[string]any{}
	for _, p := range cfg.Proxies {
		byName[p["name"].(string)] = p
	}

	r := byName["DE Reality"]
	ro, _ := r["reality-opts"].(map[string]any)
	if r["type"] != "vless" || r["flow"] != model.FlowVision || r["tls"] != true || r["servername"] != "reality.example.com" || ro["public-key"] != "PUB" || ro["short-id"] != "ab12" || r["client-fingerprint"] != "chrome" {
		t.Fatalf("reality proxy: %v", r)
	}
	ws := byName["WS"]
	wo, _ := ws["ws-opts"].(map[string]any)
	if ws["network"] != "ws" || wo["path"] != "/ws" || wo["headers"].(map[string]any)["Host"] != "h.example.com" || ws["tls"] != true {
		t.Fatalf("ws proxy: %v", ws)
	}
	g := byName["gRPC"]
	if g["type"] != "trojan" || g["password"] != "pw" || g["sni"] != "panel.example.com" || g["grpc-opts"].(map[string]any)["grpc-service-name"] != "svc" {
		t.Fatalf("trojan grpc proxy: %v", g)
	}
	if x := byName["XHTTP"]["xhttp-opts"].(map[string]any); byName["XHTTP"]["network"] != "xhttp" || x["path"] != "/x" || x["mode"] != "auto" {
		t.Fatalf("xhttp proxy: %v", byName["XHTTP"])
	}
	if u := byName["Upgrade"]; u["network"] != "ws" || u["ws-opts"].(map[string]any)["v2ray-http-upgrade"] != true {
		t.Fatalf("httpupgrade is ws with v2ray-http-upgrade: %v", u)
	}
	if p := byName["Plain"]; p["tls"] != nil || p["client-fingerprint"] != nil || p["alpn"] != nil {
		t.Fatalf("TLS-only fields must be absent without TLS: %v", p)
	}

	if len(cfg.Groups) != 2 || cfg.Groups[0].Name != "Proxy" || cfg.Groups[0].Type != "select" || cfg.Groups[1].Type != "url-test" {
		t.Fatalf("groups: %+v", cfg.Groups)
	}
	for _, name := range cfg.Groups[1].Proxies {
		if byName[name] == nil {
			t.Fatalf("the auto group references an unknown proxy %q", name)
		}
	}
	if cfg.Rules[0] != "DOMAIN-SUFFIX,ru,DIRECT" || cfg.Rules[len(cfg.Rules)-1] != "MATCH,Proxy" || cfg.Mode != "rule" {
		t.Fatalf("custom rules go first, MATCH goes last: %v", cfg.Rules)
	}
	if cfg.DNS["enable"] != true {
		t.Fatalf("dns: %v", cfg.DNS)
	}
}

func TestClashNamesAreUniqueAndNeverEmpty(t *testing.T) {
	e := eps()[5]
	list := []links.Endpoint{e, e, {Name: "", Protocol: model.ProtoVLESS, Host: "h", Port: 1, UUID: "u", Network: model.NetTCP, Security: model.SecNone}, {Name: "Proxy", Protocol: model.ProtoVLESS, Host: "h", Port: 1, UUID: "u", Network: model.NetTCP, Security: model.SecNone}}
	raw, err := Clash(list, ClashOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct{ Proxies []struct{ Name string } }
	_ = yaml.Unmarshal(raw, &cfg)
	seen := map[string]bool{}
	for _, p := range cfg.Proxies {
		if p.Name == "" || seen[p.Name] || p.Name == "Proxy" || p.Name == "Auto" {
			t.Fatalf("duplicate, empty or reserved proxy name %q in %v", p.Name, cfg.Proxies)
		}
		seen[p.Name] = true
	}
}

func TestClashWithoutEndpointsStillHasAProxy(t *testing.T) {
	raw, _ := Clash(nil, ClashOptions{})
	if !strings.Contains(string(raw), "No servers available") {
		t.Fatalf("an empty subscription must still be a loadable profile:\n%s", raw)
	}
	raw, _ = Clash(nil, ClashOptions{Info: []string{"⛔ expired"}})
	if strings.Contains(string(raw), "No servers available") || !strings.Contains(string(raw), "expired") {
		t.Fatalf("the info node is the only entry when the quota is gone:\n%s", raw)
	}
}

func TestParseClashRules(t *testing.T) {
	rules, err := ParseClashRules("# comment\n\n DOMAIN-SUFFIX,ru,DIRECT \nGEOIP,RU,DIRECT\n")
	if err != nil || len(rules) != 2 || rules[0] != "DOMAIN-SUFFIX,ru,DIRECT" {
		t.Fatalf("%v %v", rules, err)
	}
	for _, bad := range []string{"justtext", strings.Repeat("a,", 200) + "x"} {
		if _, err := ParseClashRules(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if _, err := ParseClashRules(strings.Repeat("A,b,DIRECT\n", 501)); err == nil {
		t.Error("more than 500 rules must be rejected")
	}
}

func decodeConfigs(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var one map[string]any
	if err := json.Unmarshal(raw, &one); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	return []map[string]any{one}
}

func TestXrayJSONConfigs(t *testing.T) {
	raw, err := XrayJSON(eps(), JSONOptions{Info: []string{"📊 5GB"}, Rules: `[{"type":"field","domain":["domain:ru"],"outboundTag":"direct"}]`})
	if err != nil {
		t.Fatal(err)
	}
	cfgs := decodeConfigs(t, raw)
	if len(cfgs) != 7 || cfgs[0]["remarks"] != "📊 5GB" {
		t.Fatalf("one config per endpoint after the info node: %d", len(cfgs))
	}
	if ob := cfgs[0]["outbounds"].([]any)[0].(map[string]any); ob["protocol"] != "socks" {
		t.Fatalf("the info node is a dummy socks outbound: %v", ob)
	}

	real := cfgs[1]
	if real["remarks"] != "DE Reality" {
		t.Fatalf("remarks: %v", real["remarks"])
	}
	proxy := real["outbounds"].([]any)[0].(map[string]any)
	ss := proxy["streamSettings"].(map[string]any)
	rs := ss["realitySettings"].(map[string]any)
	user := proxy["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)
	if proxy["protocol"] != "vless" || proxy["tag"] != "proxy" || ss["security"] != "reality" || rs["publicKey"] != "PUB" || rs["shortId"] != "ab12" || user["flow"] != model.FlowVision || user["encryption"] != "none" {
		t.Fatalf("reality outbound: %v", proxy)
	}
	if _, ok := proxy["mux"]; ok {
		t.Fatal("no mux unless configured")
	}
	inbounds := real["inbounds"].([]any)
	if len(inbounds) != 2 || inbounds[0].(map[string]any)["protocol"] != "socks" || inbounds[1].(map[string]any)["protocol"] != "http" {
		t.Fatalf("local inbounds: %v", inbounds)
	}
	rules := real["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 || rules[0].(map[string]any)["outboundTag"] != "direct" || rules[1].(map[string]any)["ip"].([]any)[0] != "geoip:private" {
		t.Fatalf("custom rules first, private-direct last: %v", rules)
	}

	for i, e := range eps() {
		o := decodeConfigs(t, mustJSON(t, []links.Endpoint{e}, JSONOptions{}))[0]["outbounds"].([]any)[0].(map[string]any)
		s := o["streamSettings"].(map[string]any)
		switch e.Network {
		case model.NetWS:
			if s["wsSettings"].(map[string]any)["path"] != "/ws" || s["tlsSettings"].(map[string]any)["serverName"] != "panel.example.com" {
				t.Errorf("%d ws: %v", i, s)
			}
		case model.NetGRPC:
			if o["protocol"] != "trojan" || s["grpcSettings"].(map[string]any)["serviceName"] != "svc" {
				t.Errorf("%d grpc: %v", i, o)
			}
		case model.NetXHTTP:
			if s["xhttpSettings"].(map[string]any)["path"] != "/x" {
				t.Errorf("%d xhttp: %v", i, s)
			}
		case model.NetHTTPUpgrade:
			if s["httpupgradeSettings"].(map[string]any)["path"] != "/u" {
				t.Errorf("%d httpupgrade: %v", i, s)
			}
		}
	}
}

func mustJSON(t *testing.T, e []links.Endpoint, o JSONOptions) []byte {
	t.Helper()
	raw, err := XrayJSON(e, o)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestXrayJSONSingleObjectUnlessAlwaysArray(t *testing.T) {
	one := eps()[1:2]
	if raw := mustJSON(t, one, JSONOptions{}); !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		t.Fatalf("a single config is an object:\n%s", raw)
	}
	if raw := mustJSON(t, one, JSONOptions{AlwaysArray: true}); !strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		t.Fatalf("AlwaysArray forces an array:\n%s", raw)
	}
	if raw := mustJSON(t, nil, JSONOptions{}); !strings.Contains(string(raw), "No servers available") {
		t.Fatalf("an empty subscription is still a valid config:\n%s", raw)
	}
}

func TestXrayJSONMuxFragmentNoisesAndDNS(t *testing.T) {
	opt := JSONOptions{
		Mux:      `{"enabled":true,"concurrency":8}`,
		Fragment: `{"packets":"tlshello","length":"100-200","interval":"10-20"}`,
		Noises:   `[{"type":"rand","packet":"10-20","delay":"10-16"}]`,
		DNS:      `{"servers":["https://1.1.1.1/dns-query"]}`,
	}
	cfgs := decodeConfigs(t, mustJSON(t, []links.Endpoint{eps()[1], eps()[0]}, opt)) // WS (tls, no flow) and Reality (vision flow)
	ws, vision := cfgs[0], cfgs[1]

	wsOut := ws["outbounds"].([]any)
	proxy := wsOut[0].(map[string]any)
	if proxy["mux"].(map[string]any)["concurrency"] != float64(8) {
		t.Fatal("mux is applied to ordinary outbounds")
	}
	if proxy["streamSettings"].(map[string]any)["sockopt"].(map[string]any)["dialerProxy"] != "fragment" {
		t.Fatal("TLS outbounds are routed through the fragment outbound")
	}
	frag := wsOut[1].(map[string]any)
	if frag["tag"] != "fragment" || frag["settings"].(map[string]any)["fragment"].(map[string]any)["packets"] != "tlshello" || frag["settings"].(map[string]any)["noises"] == nil {
		t.Fatalf("fragment outbound: %v", frag)
	}
	if ws["dns"].(map[string]any)["servers"].([]any)[0] != "https://1.1.1.1/dns-query" {
		t.Fatalf("custom dns: %v", ws["dns"])
	}
	if _, ok := vision["outbounds"].([]any)[0].(map[string]any)["mux"]; ok {
		t.Fatal("a Vision flow link must not get mux")
	}

	plain := decodeConfigs(t, mustJSON(t, []links.Endpoint{eps()[5]}, opt))[0]
	for _, o := range plain["outbounds"].([]any) {
		if o.(map[string]any)["tag"] == "fragment" {
			t.Fatal("fragmentation only makes sense with TLS or REALITY")
		}
	}
}

func TestValidateJSONOptions(t *testing.T) {
	for name, o := range map[string]JSONOptions{
		"mux not json":      {Mux: "{oops"},
		"mux is an array":   {Mux: "[1]"},
		"rules is a object": {Rules: `{"a":1}`},
		"noises scalar":     {Noises: `"x"`},
		"dns number":        {DNS: "5"},
		"huge":              {Rules: "[" + strings.Repeat("1,", 20000) + "1]"},
	} {
		if err := ValidateJSONOptions(o); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if err := ValidateJSONOptions(JSONOptions{Mux: " ", Rules: "[]", DNS: "{}", Noises: "[]", Fragment: "{}"}); err != nil {
		t.Fatalf("valid options: %v", err)
	}
	if _, err := XrayJSON(eps(), JSONOptions{Mux: "{oops"}); err == nil {
		t.Fatal("XrayJSON must refuse invalid options")
	}
}
