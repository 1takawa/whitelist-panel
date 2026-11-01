package links

import (
	"net/url"
	"strings"
	"testing"

	"anypanel2/internal/model"
)

// The endpoint and the share link are derived by two code paths; they must never disagree.
func TestEndpointAgreesWithTheShareLink(t *testing.T) {
	cases := map[string]model.Inbound{
		"reality": {Protocol: model.ProtoVLESS, Port: 8443, Network: model.NetTCP, Security: model.SecReality, Public: model.Public{Port: 443},
			Reality: model.Reality{ServerNames: []string{"yandex.ru"}, PublicKey: "PUB", ShortIDs: []string{"ab12"}, SpiderX: "/"}},
		"ws":          {Protocol: model.ProtoVLESS, Port: 1, Network: model.NetWS, Security: model.SecNone, WS: model.PathHost{Path: "/a", Host: "h.example.com"}, Public: model.Public{Port: 443, Security: model.SecTLS, ALPN: "h2,http/1.1"}},
		"grpc":        {Protocol: model.ProtoTrojan, Port: 2, Network: model.NetGRPC, Security: model.SecNone, GRPC: model.GRPC{ServiceName: "svc", MultiMode: true}, Public: model.Public{Port: 443, Security: model.SecTLS}},
		"xhttp":       {Protocol: model.ProtoVLESS, Port: 3, Network: model.NetXHTTP, Security: model.SecNone, XHTTP: model.XHTTP{Path: "/x", Mode: "packet-up"}, Public: model.Public{Port: 443, Security: model.SecTLS}},
		"httpupgrade": {Protocol: model.ProtoTrojan, Port: 4, Network: model.NetHTTPUpgrade, Security: model.SecNone, HTTPUpgrade: model.PathHost{Path: "/u"}, Public: model.Public{Port: 443, Security: model.SecTLS}},
		"plain tcp":   {Protocol: model.ProtoVLESS, Port: 5, Network: model.NetTCP, Security: model.SecNone, Public: model.Public{Host: "203.0.113.7"}},
	}
	c := model.Client{Email: "alice", UUID: "11111111-2222-4333-8444-555555555555", Password: "pw", Flow: model.FlowVision}
	set := model.Settings{PanelDomain: "panel.example.com", RealityDomain: "reality.example.com"}

	for name, in := range cases {
		link, err := BuildNamed(in, c, set, "n")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		e, err := NewEndpoint(in, c, set, "n")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		u, _ := url.Parse(link)
		q := u.Query()
		if u.Hostname() != e.Host || u.Port() == "" || q.Get("type") != e.Network || q.Get("security") != e.Security {
			t.Errorf("%s: link %s vs endpoint %+v", name, link, e)
		}
		for key, got := range map[string]string{"path": e.Path, "host": e.HostHeader, "serviceName": e.ServiceName, "sni": e.SNI, "fp": e.Fingerprint,
			"pbk": e.PublicKey, "sid": e.ShortID, "flow": e.Flow, "alpn": strings.Join(e.ALPN, ","), "spx": e.SpiderX, "authority": e.Authority} {
			if q.Get(key) != got {
				t.Errorf("%s: %s: link has %q, endpoint has %q", name, key, q.Get(key), got)
			}
		}
		if e.Protocol == model.ProtoVLESS && e.UUID != c.UUID || e.Protocol == model.ProtoTrojan && e.Password != "pw" {
			t.Errorf("%s: credential: %+v", name, e)
		}
	}
}

func TestEndpointErrors(t *testing.T) {
	in := model.Inbound{Protocol: model.ProtoVLESS, Network: model.NetTCP, Security: model.SecNone}
	if _, err := NewEndpoint(in, model.Client{UUID: "x"}, model.Settings{}, "n"); err == nil {
		t.Fatal("no public host must fail")
	}
	in.Public.Host = "h"
	if _, err := NewEndpoint(in, model.Client{}, model.Settings{}, "n"); err == nil {
		t.Fatal("a client without the credential must fail")
	}
	in.Protocol = "socks"
	if _, err := NewEndpoint(in, model.Client{UUID: "x"}, model.Settings{}, "n"); err == nil {
		t.Fatal("unsupported protocol must fail")
	}
}
