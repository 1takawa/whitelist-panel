package xray

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/model"
)

// ---- fake xray: the test binary re-executes itself as "xray" ----

func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_FAKE_XRAY") == "1" {
		fakeXray()
		return
	}
	os.Exit(m.Run())
}

func fakeXray() {
	args := os.Args[1:]
	switch {
	case len(args) > 0 && args[0] == "version":
		fmt.Println("Xray 25.1.1 (Xray, Penetrates Everything.) fake")
	case len(args) > 2 && args[0] == "run" && args[1] == "-test":
		data, _ := os.ReadFile(args[len(args)-1])
		if strings.Contains(string(data), "INVALID") {
			fmt.Fprintln(os.Stderr, "Failed to start: bad config")
			os.Exit(23)
		}
	case len(args) > 0 && args[0] == "run":
		fmt.Println("fake xray started")
		time.Sleep(time.Hour)
	case len(args) > 1 && args[0] == "api" && args[1] == "statsquery":
		fmt.Println(`{"stat":[{"name":"user>>>alice>>>traffic>>>uplink","value":"100"},{"name":"user>>>alice>>>traffic>>>downlink","value":2048}]}`)
	default:
		fmt.Fprintln(os.Stderr, "unexpected args", args)
		os.Exit(2)
	}
	os.Exit(0)
}

func newFakeManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("GO_WANT_FAKE_XRAY", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(exe, filepath.Join(t.TempDir(), "xray", "config.json"), "127.0.0.1:62789", slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(m.Stop)
	return m
}

func TestManagerApplyRestartAndReject(t *testing.T) {
	m := newFakeManager(t)
	ctx := context.Background()

	if st := m.Status(ctx); !st.Installed || st.Running || st.Version != "25.1.1" {
		t.Fatalf("initial status: %+v", st)
	}
	if err := m.Apply(ctx, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("apply valid: %v", err)
	}
	st := m.Status(ctx)
	if !st.Running || st.PID == 0 {
		t.Fatalf("should be running: %+v", st)
	}
	firstPID := st.PID

	err := m.Apply(ctx, []byte(`{"INVALID":true}`))
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("invalid config must be rejected, got %v", err)
	}
	if got := m.Status(ctx); !got.Running || got.PID != firstPID {
		t.Fatalf("a rejected config must not disturb the running process: %+v", got)
	}
	saved, _ := os.ReadFile(m.ConfigPath)
	if !strings.Contains(string(saved), `"ok"`) {
		t.Fatalf("config file must keep the last valid config, got %s", saved)
	}

	if err := m.Apply(ctx, []byte(`{"ok":2}`)); err != nil {
		t.Fatal(err)
	}
	if got := m.Status(ctx); !got.Running || got.PID == firstPID {
		t.Fatalf("apply must restart the process: %+v", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(m.Logs(10)) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if logs := m.Logs(10); len(logs) == 0 || !strings.Contains(logs[0], "fake xray started") {
		t.Fatalf("process output must be captured, got %v", logs)
	}

	m.Stop()
	if m.Status(ctx).Running {
		t.Fatal("Stop must stop the process")
	}
}

func TestManagerQueryStats(t *testing.T) {
	m := newFakeManager(t)
	stats, err := m.QueryStats(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats["alice"]; got.Up != 100 || got.Down != 2048 {
		t.Fatalf("stats: %+v", stats)
	}
}

func TestManagerNotInstalled(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "c.json"), "x", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if st := m.Status(context.Background()); st.Installed {
		t.Fatal("must report not installed")
	}
	if err := m.Apply(context.Background(), []byte("{}")); err != ErrNotInstalled {
		t.Fatalf("got %v", err)
	}
}

// ---- stats parsing ----

func TestParseStats(t *testing.T) {
	got, err := ParseStats([]byte(`{"stat":[
		{"name":"user>>>bob>>>traffic>>>uplink","value":"5"},
		{"name":"user>>>bob>>>traffic>>>downlink","value":7},
		{"name":"user>>>carol>>>traffic>>>downlink"},
		{"name":"inbound>>>in-x>>>traffic>>>uplink","value":999},
		{"name":"garbage","value":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got["bob"] != (Traffic{Up: 5, Down: 7}) {
		t.Fatalf("bob: %+v", got["bob"])
	}
	if _, ok := got["carol"]; !ok {
		t.Fatal("a user with only zero counters should still appear")
	}
	if len(got) != 2 {
		t.Fatalf("inbound/garbage entries must be ignored: %+v", got)
	}
	if empty, err := ParseStats([]byte(`{}`)); err != nil || len(empty) != 0 {
		t.Fatalf("empty: %v %v", empty, err)
	}
	if _, err := ParseStats([]byte(`nope`)); err == nil {
		t.Fatal("garbage must error")
	}
}

// ---- REALITY keys (RFC 7748 §6.1 test vector) ----

func TestPublicFromPrivateRFC7748(t *testing.T) {
	priv, _ := hex.DecodeString("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
	wantPub, _ := hex.DecodeString("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	got, err := PublicFromPrivate(base64.RawURLEncoding.EncodeToString(priv))
	if err != nil {
		t.Fatal(err)
	}
	if got != base64.RawURLEncoding.EncodeToString(wantPub) {
		t.Fatalf("public key mismatch: %s", got)
	}
}

func TestGenerateRealityKeys(t *testing.T) {
	priv, pub, err := GenerateRealityKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(priv) != 43 || len(pub) != 43 { // 32 bytes, unpadded base64url
		t.Fatalf("unexpected key lengths %d/%d", len(priv), len(pub))
	}
	if derived, _ := PublicFromPrivate(priv); derived != pub {
		t.Fatal("public key must match the private key")
	}
	if _, err := PublicFromPrivate("short"); err == nil {
		t.Fatal("bad private key must error")
	}
	sid, _ := NewShortID()
	if len(sid) != 16 {
		t.Fatalf("short id: %q", sid)
	}
}

// ---- config generation ----

func fixture() ([]model.Inbound, []model.Client) {
	ins := []model.Inbound{
		{
			ID: "r1", Remark: "reality", Enable: true, Protocol: model.ProtoVLESS, Listen: "127.0.0.1", Port: 8443,
			Network: model.NetTCP, Security: model.SecReality, AcceptProxyProtocol: true,
			Reality:  model.Reality{Dest: "example.com:443", ServerNames: []string{"example.com"}, PrivateKey: "PRIV", ShortIDs: []string{"abcd"}},
			Sniffing: model.Sniffing{Enabled: true},
		},
		{
			ID: "w1", Remark: "ws", Enable: true, Protocol: model.ProtoTrojan, Port: 10001,
			Network: model.NetWS, Security: model.SecNone, WS: model.PathHost{Path: "/ws", Host: "h.example.com"},
		},
		{ID: "off", Enable: false, Protocol: model.ProtoVLESS, Port: 10002, Network: model.NetTCP, Security: model.SecNone},
	}
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	cls := []model.Client{
		{ID: "c1", Email: "alice", UUID: "u-alice", Password: "p-alice", Flow: model.FlowVision, Enable: true, InboundIDs: []string{"r1", "w1"}, ExpiryAt: &future},
		{ID: "c2", Email: "bob", UUID: "u-bob", Password: "p-bob", Enable: false, InboundIDs: []string{"r1"}},
		{ID: "c3", Email: "carol", UUID: "u-carol", Enable: true, InboundIDs: []string{"r1"}, ExpiryAt: &past},
		{ID: "c4", Email: "dave", UUID: "u-dave", Enable: true, InboundIDs: []string{"r1"}, TotalBytes: 100, Up: 60, Down: 40},
	}
	return ins, cls
}

func TestBuildConfig(t *testing.T) {
	ins, cls := fixture()
	raw, err := BuildConfig(ins, cls, Options{BlockBittorrent: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		API      struct{ Tag string }
		Inbounds []struct {
			Tag, Listen, Protocol string
			Port                  int
			Settings              struct {
				Clients    []map[string]any
				Decryption string
			}
			StreamSettings map[string]any
			Sniffing       map[string]any
		}
		Routing struct{ Rules []map[string]any }
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("output must be valid JSON: %v\n%s", err, raw)
	}

	if cfg.API.Tag != "api" || len(cfg.Inbounds) != 3 { // api + reality + trojan; the disabled inbound is omitted
		t.Fatalf("api=%q inbounds=%d\n%s", cfg.API.Tag, len(cfg.Inbounds), raw)
	}
	if cfg.Inbounds[0].Tag != "api" || cfg.Inbounds[0].Listen != "127.0.0.1" {
		t.Fatal("api inbound must be first and local-only")
	}

	vless := cfg.Inbounds[1]
	if vless.Tag != "in-r1" || vless.Protocol != "vless" || vless.Port != 8443 || vless.Settings.Decryption != "none" {
		t.Fatalf("vless inbound: %+v", vless)
	}
	if len(vless.Settings.Clients) != 1 || vless.Settings.Clients[0]["email"] != "alice" ||
		vless.Settings.Clients[0]["id"] != "u-alice" || vless.Settings.Clients[0]["flow"] != model.FlowVision {
		t.Fatalf("only the active client may be present (disabled/expired/over-quota excluded): %+v", vless.Settings.Clients)
	}
	rs := vless.StreamSettings["realitySettings"].(map[string]any)
	if rs["privateKey"] != "PRIV" || rs["dest"] != "example.com:443" {
		t.Fatalf("reality settings: %+v", rs)
	}
	if vless.StreamSettings["sockopt"].(map[string]any)["acceptProxyProtocol"] != true {
		t.Fatal("acceptProxyProtocol must be set")
	}
	if vless.Sniffing["enabled"] != true {
		t.Fatal("sniffing must be enabled")
	}

	trojan := cfg.Inbounds[2]
	if trojan.Listen != "0.0.0.0" || trojan.Protocol != "trojan" {
		t.Fatalf("trojan inbound: %+v", trojan)
	}
	if len(trojan.Settings.Clients) != 1 || trojan.Settings.Clients[0]["password"] != "p-alice" {
		t.Fatalf("trojan clients: %+v", trojan.Settings.Clients)
	}
	ws := trojan.StreamSettings["wsSettings"].(map[string]any)
	if ws["path"] != "/ws" || ws["headers"].(map[string]any)["Host"] != "h.example.com" {
		t.Fatalf("ws settings: %+v", ws)
	}
	if _, ok := trojan.StreamSettings["sockopt"]; ok {
		t.Fatal("sockopt must be absent without proxy protocol")
	}

	if len(cfg.Routing.Rules) != 2 {
		t.Fatalf("api + bittorrent rules expected: %+v", cfg.Routing.Rules)
	}
}

func TestFlowDroppedWhenNotApplicable(t *testing.T) {
	ins, cls := fixture()
	ins[0].Network, ins[0].Security = model.NetWS, model.SecNone // vision flow is invalid over ws
	raw, _ := BuildConfig(ins[:1], cls[:1], Options{}, time.Now())
	if strings.Contains(string(raw), "xtls-rprx-vision") {
		t.Fatal("flow must be dropped when the inbound cannot use it")
	}
}

func TestBuildConfigEmptyClientsIsAnArray(t *testing.T) {
	ins, _ := fixture()
	raw, _ := BuildConfig(ins[:1], nil, Options{}, time.Now())
	if !strings.Contains(string(raw), `"clients": []`) {
		t.Fatalf("clients must serialize as [] not null:\n%s", raw)
	}
}

func TestStreamSettingsPerNetwork(t *testing.T) {
	cases := map[string]model.Inbound{
		"grpcSettings":        {Network: model.NetGRPC, GRPC: model.GRPC{ServiceName: "svc", MultiMode: true}},
		"xhttpSettings":       {Network: model.NetXHTTP, XHTTP: model.XHTTP{Path: "/x"}},
		"httpupgradeSettings": {Network: model.NetHTTPUpgrade, HTTPUpgrade: model.PathHost{Path: "/h"}},
	}
	for key, in := range cases {
		in.ID, in.Enable, in.Protocol, in.Port, in.Security = "n", true, model.ProtoVLESS, 1000, model.SecNone
		raw, _ := BuildConfig([]model.Inbound{in}, nil, Options{}, time.Now())
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("%s missing in config for %s", key, in.Network)
		}
	}
	tls := model.Inbound{ID: "t", Enable: true, Protocol: model.ProtoTrojan, Port: 1001, Network: model.NetTCP, Security: model.SecTLS,
		TLS: model.TLS{CertFile: "/c.pem", KeyFile: "/k.pem", ServerName: "x.example.com", ALPN: []string{"h2"}}}
	raw, _ := BuildConfig([]model.Inbound{tls}, nil, Options{}, time.Now())
	for _, want := range []string{`"certificateFile": "/c.pem"`, `"keyFile": "/k.pem"`, `"serverName": "x.example.com"`, `"h2"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("tls config missing %s", want)
		}
	}
}
