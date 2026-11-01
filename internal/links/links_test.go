package links

import (
	"net/url"
	"strings"
	"testing"

	"anypanel2/internal/model"
)

var settings = model.Settings{PanelDomain: "panel.example.com", RealityDomain: "reality.example.com"}

var client = model.Client{Email: "alice", UUID: "11111111-2222-4333-8444-555555555555", Password: "s3cr/et pw", Flow: model.FlowVision}

func parse(t *testing.T, link string) *url.URL {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link must parse: %v (%s)", err, link)
	}
	return u
}

func TestVLESSReality(t *testing.T) {
	in := model.Inbound{
		Remark: "DE", Protocol: model.ProtoVLESS, Port: 8443, Network: model.NetTCP, Security: model.SecReality,
		Public:  model.Public{Port: 443},
		Reality: model.Reality{ServerNames: []string{"yandex.ru"}, PublicKey: "PUB", ShortIDs: []string{"ab12", "cd34"}, SpiderX: "/"},
	}
	link, err := Build(in, client, settings)
	if err != nil {
		t.Fatal(err)
	}
	u := parse(t, link)
	q := u.Query()
	if u.Scheme != "vless" || u.User.Username() != client.UUID || u.Host != "reality.example.com:443" || u.Fragment != "DE-alice" {
		t.Fatalf("link: %s", link)
	}
	for k, want := range map[string]string{
		"type": "tcp", "security": "reality", "encryption": "none", "flow": model.FlowVision,
		"pbk": "PUB", "sid": "ab12", "sni": "yandex.ru", "fp": "firefox", "spx": "/",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestVLESSWebSocketBehindFrontProxy(t *testing.T) {
	// Xray serves plain ws on a local port; the front proxy adds TLS on 443 for the panel domain.
	in := model.Inbound{
		Remark: "ws", Protocol: model.ProtoVLESS, Port: 10001, Network: model.NetWS, Security: model.SecNone,
		WS:     model.PathHost{Path: "/a b", Host: "panel.example.com"},
		Public: model.Public{Port: 443, Security: model.SecTLS},
	}
	c := client
	link, err := Build(in, c, settings)
	if err != nil {
		t.Fatal(err)
	}
	q := parse(t, link).Query()
	if q.Get("security") != "tls" || q.Get("type") != "ws" || q.Get("path") != "/a b" || q.Get("host") != "panel.example.com" {
		t.Fatalf("unexpected query: %v", q)
	}
	if q.Get("sni") != "panel.example.com" {
		t.Fatalf("sni must default to the public host, got %q", q.Get("sni"))
	}
	if q.Has("flow") {
		t.Fatal("flow must not be set for non-tcp transports")
	}
	if !strings.Contains(link, "panel.example.com:443") {
		t.Fatalf("must advertise the public port: %s", link)
	}
}

func TestTrojanGRPCEscapesPassword(t *testing.T) {
	in := model.Inbound{
		Remark: "tj", Protocol: model.ProtoTrojan, Port: 10002, Network: model.NetGRPC, Security: model.SecNone,
		GRPC:   model.GRPC{ServiceName: "svc", MultiMode: true},
		Public: model.Public{Port: 443, Security: model.SecTLS, ALPN: "h2"},
	}
	link, err := Build(in, client, settings)
	if err != nil {
		t.Fatal(err)
	}
	u := parse(t, link)
	if u.Scheme != "trojan" {
		t.Fatalf("scheme: %s", u.Scheme)
	}
	if pw, _ := u.User.Password(); pw != "" || u.User.Username() != client.Password {
		t.Fatalf("password must round-trip through escaping, got %q", u.User.Username())
	}
	q := u.Query()
	if q.Get("serviceName") != "svc" || q.Get("mode") != "multi" || q.Get("alpn") != "h2" || q.Get("security") != "tls" {
		t.Fatalf("query: %v", q)
	}
	if q.Has("encryption") {
		t.Fatal("trojan links have no encryption parameter")
	}
}

func TestXHTTPAndDefaults(t *testing.T) {
	in := model.Inbound{Protocol: model.ProtoVLESS, Port: 9000, Network: model.NetXHTTP, Security: model.SecNone, Public: model.Public{Port: 443, Security: model.SecTLS}}
	c := client
	c.Flow = ""
	link, err := Build(in, c, settings)
	if err != nil {
		t.Fatal(err)
	}
	u := parse(t, link)
	q := u.Query()
	if q.Get("type") != "xhttp" || q.Get("path") != "/" || q.Get("mode") != "auto" {
		t.Fatalf("query: %v", q)
	}
	if u.Fragment != "alice" {
		t.Fatalf("remark falls back to the client email, got %q", u.Fragment)
	}
}

func TestHostResolutionAndErrors(t *testing.T) {
	in := model.Inbound{Protocol: model.ProtoVLESS, Port: 1, Network: model.NetTCP, Security: model.SecNone}
	if _, err := Build(in, client, model.Settings{}); err == nil {
		t.Fatal("must fail without any public host")
	}
	in.Public.Host = "203.0.113.5"
	link, err := Build(in, client, model.Settings{})
	if err != nil || !strings.Contains(link, "203.0.113.5:1") {
		t.Fatalf("explicit host must win: %s %v", link, err)
	}
	in.Public.Host = "2001:db8::1"
	if link, _ := Build(in, client, model.Settings{}); !strings.Contains(link, "[2001:db8::1]:1") {
		t.Fatalf("IPv6 must be bracketed: %s", link)
	}
	if _, err := Build(model.Inbound{Protocol: "socks", Public: model.Public{Host: "h"}}, client, settings); err == nil {
		t.Fatal("unsupported protocol must fail")
	}
	noSecret := client
	noSecret.UUID = ""
	vl := model.Inbound{Protocol: model.ProtoVLESS, Port: 1, Network: model.NetTCP, Security: model.SecNone, Public: model.Public{Host: "h"}}
	if _, err := Build(vl, noSecret, settings); err == nil {
		t.Fatal("a client without the credential must fail")
	}
}

func TestRealityFallsBackToPanelDomain(t *testing.T) {
	in := model.Inbound{Protocol: model.ProtoVLESS, Port: 1, Network: model.NetTCP, Security: model.SecReality}
	h, err := Host(in, model.Settings{PanelDomain: "only.example.com"})
	if err != nil || h != "only.example.com" {
		t.Fatalf("got %q %v", h, err)
	}
}
