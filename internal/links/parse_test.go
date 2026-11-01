package links

import (
	"strings"
	"testing"

	"anypanel2/internal/model"
)

func TestParseRoundTripsEveryInboundShape(t *testing.T) {
	set := model.Settings{PanelDomain: "panel.example.com", RealityDomain: "reality.example.com"}
	cl := model.Client{Email: "a", UUID: "11111111-2222-4333-8444-555555555555", Password: "pw0rd", Flow: "xtls-rprx-vision"}
	cases := map[string]model.Inbound{
		"reality tcp": {Protocol: "vless", Network: "tcp", Security: "reality", Port: 443,
			Reality: model.Reality{ServerNames: []string{"www.microsoft.com"}, PublicKey: "PUBKEY", ShortIDs: []string{"ab12"}, SpiderX: "/x"}},
		"vless ws tls": {Protocol: "vless", Network: "ws", Security: "tls", Port: 8443,
			WS: model.PathHost{Path: "/ws", Host: "cdn.example.com"}, TLS: model.TLS{ServerName: "cdn.example.com", ALPN: []string{"h2", "http/1.1"}}},
		"trojan grpc tls": {Protocol: "trojan", Network: "grpc", Security: "tls", Port: 443,
			GRPC: model.GRPC{ServiceName: "svc", Authority: "a.example.com", MultiMode: true}},
		"vless xhttp none":   {Protocol: "vless", Network: "xhttp", Security: "none", Port: 8080, XHTTP: model.XHTTP{Path: "/x", Mode: "packet-up"}},
		"trojan httpupgrade": {Protocol: "trojan", Network: "httpupgrade", Security: "tls", Port: 443, HTTPUpgrade: model.PathHost{Path: "/h"}},
	}
	for name, in := range cases {
		link, err := BuildNamed(in, cl, set, "Some Name")
		if err != nil {
			t.Fatalf("%s: build: %v", name, err)
		}
		e, err := ParseURI(link)
		if err != nil {
			t.Fatalf("%s: parse %s: %v", name, link, err)
		}
		if e.Name != "Some Name" {
			t.Errorf("%s: name %q", name, e.Name)
		}
		want, err := NewEndpoint(in, cl, set, "Some Name")
		if err != nil {
			t.Fatalf("%s: endpoint: %v", name, err)
		}
		if got := e.URI(); got != link {
			t.Errorf("%s: URI() is not the inverse of parsing\n got %s\nwant %s", name, got, link)
		}
		if e.Host != want.Host || e.Port != want.Port || e.Network != want.Network || e.Security != want.Security ||
			e.Path != want.Path || e.ServiceName != want.ServiceName || e.PublicKey != want.PublicKey || e.MultiMode != want.MultiMode {
			t.Errorf("%s: parsed endpoint differs\n got %+v\nwant %+v", name, e, want)
		}
	}
}

func TestParseRenamesViaURI(t *testing.T) {
	e, err := ParseURI("trojan://secret@h.example.com:443?security=tls&sni=h.example.com#old")
	if err != nil {
		t.Fatal(err)
	}
	e.Name = "Нидерланды 🇳🇱 #1"
	back, err := ParseURI(e.URI())
	if err != nil || back.Name != "Нидерланды 🇳🇱 #1" || back.Password != "secret" || back.Host != "h.example.com" {
		t.Fatalf("%+v %v", back, err)
	}
}

func TestParseRejects(t *testing.T) {
	uuid := "11111111-2222-4333-8444-555555555555"
	for want, raw := range map[string]string{
		"empty":               "",
		"scheme":              "vmess://abc@h:1",
		"hysteria":            "hysteria2://pw@h:443",
		"user":                "vless://h:443?type=tcp",
		"uuid":                "vless://not-a-uuid@h:443",
		"port":                "vless://" + uuid + "@h:99999",
		"no port":             "vless://" + uuid + "@h",
		"transport":           "vless://" + uuid + "@h:443?type=kcp",
		"security":            "vless://" + uuid + "@h:443?security=xtls",
		"reality without pbk": "vless://" + uuid + "@h:443?security=reality&sni=x.com",
		"trojan reality":      "trojan://pw@h:443?security=reality&pbk=k",
		"control":             "vless://" + uuid + "@h:443\x00",
		"too long":            "trojan://pw@h:443?x=" + strings.Repeat("a", MaxURILength),
	} {
		if _, err := ParseURI(raw); err == nil {
			t.Errorf("%s must be rejected: %q", want, raw)
		}
	}
}

func TestParseDefaults(t *testing.T) {
	e, err := ParseURI("  VLESS://11111111-2222-4333-8444-555555555555@[2001:db8::1]:443  ")
	if err != nil {
		t.Fatal(err)
	}
	if e.Network != "tcp" || e.Security != "none" || e.Host != "2001:db8::1" || e.Port != 443 || e.Protocol != "vless" {
		t.Fatalf("%+v", e)
	}
	if e, _ := ParseURI("trojan://p@h:1?type=raw"); e.Network != "tcp" {
		t.Fatal("raw is the newer name for tcp")
	}
}
