package model

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Doc must survive MongoDB untouched: numbers stay numbers, empty arrays stay arrays, nested objects stay objects.
func TestDocBSONRoundTrip(t *testing.T) {
	in := Inbound{
		ID: "x", Remark: "r", Protocol: ProtoVLESS, Port: 443,
		Settings: Doc{"decryption": "none", "testseed": []any{900.0, 500.0, 900.0, 256.0}, "fallbacks": []any{}},
		StreamSettings: Doc{
			"network": "tcp", "security": "reality",
			"realitySettings": map[string]any{"serverNames": []any{"a.example.com"}, "xver": 0.0, "settings": map[string]any{"spiderX": "/"}},
		},
	}
	data, err := bson.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Inbound
	if err := bson.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.Settings, out.Settings) || !reflect.DeepEqual(in.StreamSettings, out.StreamSettings) {
		t.Fatalf("documents changed in MongoDB:\n%v\n%v", out.Settings, out.StreamSettings)
	}

	// A document stored before the fields existed reads back as nil, not as an error.
	data, _ = bson.Marshal(bson.M{"_id": "old", "remark": "legacy", "port": 1})
	var old Inbound
	if err := bson.Unmarshal(data, &old); err != nil || old.Settings != nil || old.StreamSettings != nil || old.HasRaw() {
		t.Fatalf("legacy document: %+v %v", old, err)
	}
	// A nil document is stored as null and stays nil.
	data, _ = bson.Marshal(Inbound{ID: "n"})
	var n Inbound
	if err := bson.Unmarshal(data, &n); err != nil || n.Settings != nil {
		t.Fatalf("nil document: %v %v", n.Settings, err)
	}
}

func TestDeriveAndSync(t *testing.T) {
	in := Inbound{
		Protocol: ProtoTrojan, Network: NetWS, Security: SecTLS,
		WS: PathHost{Path: "/ws", Host: "h.example.com"}, TLS: TLS{ServerName: "t.example.com", CertFile: "/c", KeyFile: "/k", ALPN: []string{"h2"}},
		AcceptProxyProtocol: true,
	}
	in.EnsureRaw()
	b, _ := json.Marshal(in.StreamSettings)
	var back Inbound
	back.StreamSettings = in.StreamSettings
	back.Derive()
	if back.Network != NetWS || back.Security != SecTLS || back.WS != in.WS || back.TLS.ServerName != "t.example.com" ||
		back.TLS.CertFile != "/c" || !back.AcceptProxyProtocol {
		t.Fatalf("derive does not invert sync: %+v\n%s", back, b)
	}
	if h, _ := in.StreamSettings.Sub("wsSettings")["headers"].(map[string]any); h == nil || h["Host"] != "h.example.com" {
		t.Fatalf("a WebSocket host is written as the Host header like Xray-панелей does: %v", in.StreamSettings)
	}

	// Xray-панелей spells the transport "raw" and keeps the proxy-protocol flag inside the transport settings.
	x := Inbound{StreamSettings: Doc{"network": "raw", "security": "none", "tcpSettings": map[string]any{"acceptProxyProtocol": true}}}
	x.Derive()
	if x.Network != NetTCP || !x.AcceptProxyProtocol {
		t.Fatalf("raw/tcpSettings: %+v", x)
	}
	x.AcceptProxyProtocol = false
	x.Sync()
	if x.StreamSettings.Sub("tcpSettings")["acceptProxyProtocol"] != nil {
		t.Fatal("switching the flag off must clear it everywhere")
	}

	// Sync keeps every option it does not model.
	k := Inbound{Network: NetXHTTP, Security: SecNone, XHTTP: XHTTP{Path: "/x", Mode: "auto"},
		StreamSettings: Doc{"xhttpSettings": map[string]any{"xPaddingBytes": "100-1000", "extra": map[string]any{"a": 1.0}}}}
	k.Sync()
	xs := k.StreamSettings.Sub("xhttpSettings")
	if xs["xPaddingBytes"] != "100-1000" || xs["path"] != "/x" || xs["extra"] == nil {
		t.Fatalf("unknown options must survive: %v", xs)
	}
	if extra := k.XHTTPExtra(); extra["xPaddingBytes"] != "100-1000" {
		t.Fatalf("link extras: %v", extra)
	}
}

func TestInboundLimits(t *testing.T) {
	in := Inbound{Enable: true, TotalBytes: 100, Up: 60, Down: 40}
	if !in.Exhausted() || in.Running(time.Time{}) {
		t.Fatal("an inbound at its limit is exhausted and does not run")
	}
	in.TotalBytes = 0
	if in.Exhausted() || !in.Running(time.Time{}) {
		t.Fatal("limit 0 means unlimited")
	}
}
