package panel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/xray"
)

// uiInbound is what the form sends: the Xray documents are the source of truth, the typed fields are ignored.
func uiInbound() model.Inbound {
	return model.Inbound{
		Remark: "ui reality", Enable: true, Protocol: model.ProtoVLESS, Listen: "0.0.0.0", Port: 8443,
		Settings: model.Doc{
			"decryption": "none",
			"testseed":   []any{900.0, 500.0, 900.0, 256.0},
			"fallbacks":  []any{map[string]any{"dest": 8080.0, "xver": 1.0}},
		},
		StreamSettings: model.Doc{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"show": false, "xver": 0.0, "target": "127.0.0.1:9443", "serverNames": []any{"a.example.com"},
				"maxTimediff": 5000.0, "minClient": "1.8.0",
				"limitFallbackUpload": map[string]any{"afterBytes": 1000.0, "bytesPerSec": 100.0, "burstBytesPerSec": 200.0},
				"settings":            map[string]any{"fingerprint": "firefox", "spiderX": "/"},
			},
			"sockopt":   map[string]any{"tcpFastOpen": true, "acceptProxyProtocol": true, "tcpKeepAliveInterval": 30.0},
			"finalmask": map[string]any{"tcp": []any{map[string]any{"type": "header-custom", "settings": map[string]any{}}}},
		},
		Sniffing: model.Sniffing{Enabled: true, MetadataOnly: true, DomainsExcluded: []string{"a.test"}},
	}
}

func lastConfig(t *testing.T, fx *fakeXray) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal([]byte(fx.last()), &cfg); err != nil {
		t.Fatalf("last applied config is not JSON: %v", err)
	}
	return cfg
}

func inboundByTag(t *testing.T, cfg map[string]any, tag string) map[string]any {
	t.Helper()
	for _, x := range cfg["inbounds"].([]any) {
		if m := x.(map[string]any); m["tag"] == tag {
			return m
		}
	}
	t.Fatalf("inbound %s not in config", tag)
	return nil
}

func TestXrayDocumentsAreKeptAndReachTheConfig(t *testing.T) {
	svc, fx, _ := newService(t)
	in, err := svc.CreateInbound(context.Background(), uiInbound())
	if err != nil {
		t.Fatal(err)
	}
	if in.Network != "tcp" || in.Security != "reality" || in.Reality.Dest != "127.0.0.1:9443" || !in.AcceptProxyProtocol {
		t.Fatalf("typed view must be derived from the documents: %+v", in)
	}
	if in.Reality.PrivateKey == "" || in.Reality.PublicKey == "" || len(in.Reality.ShortIDs) == 0 {
		t.Fatalf("keys must be generated into the document: %+v", in.Reality)
	}
	rs := in.StreamSettings.Sub("realitySettings")
	if rs["privateKey"] != in.Reality.PrivateKey || model.Str(rs["settings"].(map[string]any), "publicKey") != in.Reality.PublicKey {
		t.Fatalf("generated keys must be written back to the document: %+v", rs)
	}
	if model.Str(rs["settings"].(map[string]any), "fingerprint") != "firefox" || rs["maxTimediff"] != 5000.0 {
		t.Fatalf("options the typed view does not know must survive: %+v", rs)
	}

	cfg := lastConfig(t, fx)
	x := inboundByTag(t, cfg, xray.InboundTag(in.ID))
	settings := x["settings"].(map[string]any)
	if settings["decryption"] != "none" || len(settings["testseed"].([]any)) != 4 || len(settings["fallbacks"].([]any)) != 1 {
		t.Fatalf("protocol settings: %+v", settings)
	}
	stream := x["streamSettings"].(map[string]any)
	real := stream["realitySettings"].(map[string]any)
	if real["dest"] != "127.0.0.1:9443" || real["limitFallbackUpload"] == nil || real["minClientVer"] != "1.8.0" {
		t.Fatalf("reality settings in the config: %+v", real)
	}
	if _, ok := real["target"]; ok {
		t.Fatal("the config uses the spelling every Xray version understands (dest)")
	}
	so := stream["sockopt"].(map[string]any)
	if so["tcpFastOpen"] != true || so["acceptProxyProtocol"] != true || so["tcpKeepAliveInterval"] != 30.0 {
		t.Fatalf("sockopt: %+v", so)
	}
	if stream["finalmask"] == nil {
		t.Fatal("finalmask lost")
	}
	sn := x["sniffing"].(map[string]any)
	if sn["metadataOnly"] != true || len(sn["domainsExcluded"].([]any)) != 1 {
		t.Fatalf("sniffing: %+v", sn)
	}
}

func TestEditingKeepsKeysWhenTheFormOmitsThem(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	in, err := svc.CreateInbound(ctx, uiInbound())
	if err != nil {
		t.Fatal(err)
	}
	edit := uiInbound()
	edit.Remark = "renamed"
	edit.StreamSettings["realitySettings"].(map[string]any)["serverNames"] = []any{"b.example.com"}
	got, err := svc.UpdateInbound(ctx, in.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reality.PrivateKey != in.Reality.PrivateKey || got.Reality.PublicKey != in.Reality.PublicKey {
		t.Fatal("the key pair must be kept when the form sends no private key")
	}
	if len(got.Reality.ServerNames) != 1 || got.Reality.ServerNames[0] != "b.example.com" {
		t.Fatalf("edit not applied: %+v", got.Reality)
	}
}

func TestXrayDocumentValidation(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	cases := map[string]func(in *model.Inbound){
		"bad decryption":     func(in *model.Inbound) { in.Settings["decryption"] = "aes" },
		"short testseed":     func(in *model.Inbound) { in.Settings["testseed"] = []any{1.0} },
		"fallback no dest":   func(in *model.Inbound) { in.Settings["fallbacks"] = []any{map[string]any{"xver": 0.0}} },
		"fallback bad xver":  func(in *model.Inbound) { in.Settings["fallbacks"] = []any{map[string]any{"dest": 80.0, "xver": 9.0}} },
		"fallback bad path":  func(in *model.Inbound) { in.Settings["fallbacks"] = []any{map[string]any{"dest": 80.0, "path": "x"}} },
		"reality xver 5":     func(in *model.Inbound) { in.StreamSettings["realitySettings"].(map[string]any)["xver"] = 5.0 },
		"reality bad client": func(in *model.Inbound) { in.StreamSettings["realitySettings"].(map[string]any)["minClient"] = "v1" },
		"mask without type": func(in *model.Inbound) {
			in.StreamSettings["finalmask"] = map[string]any{"tcp": []any{map[string]any{}}}
		},
		"sockopt not object": func(in *model.Inbound) { in.StreamSettings["sockopt"] = "yes" },
		"fragment mask on an inbound": func(in *model.Inbound) {
			in.StreamSettings["finalmask"] = map[string]any{"tcp": []any{map[string]any{"type": "fragment"}}}
		},
		"negative limit":    func(in *model.Inbound) { in.TotalBytes = -1 },
		"bad reset":         func(in *model.Inbound) { in.TrafficReset = "yearly" },
		"bad share address": func(in *model.Inbound) { in.ShareAddress = "request" },
		"tls without certs": func(in *model.Inbound) {
			in.StreamSettings["security"] = "tls"
			in.StreamSettings["tlsSettings"] = map[string]any{"serverName": "x.example.com"}
		},
	}
	for name, mut := range cases {
		in := uiInbound()
		mut(&in)
		if _, err := svc.CreateInbound(ctx, in); !isInvalid(err) {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}
}

func TestTLSWithPastedCertificateAndKCP(t *testing.T) {
	svc, fx, _ := newService(t)
	ctx := context.Background()
	in := uiInbound()
	in.Protocol = model.ProtoTrojan
	delete(in.Settings, "testseed")
	in.Port = 9001
	in.StreamSettings = model.Doc{
		"network": "tcp", "security": "tls",
		"tlsSettings": map[string]any{
			"serverName": "t.example.com", "minVersion": "1.2", "alpn": []any{"h2", "http/1.1"},
			"certificates": []any{map[string]any{"certificate": []any{"-----BEGIN CERTIFICATE-----"}, "key": []any{"-----BEGIN PRIVATE KEY-----"}}},
			"settings":     map[string]any{"fingerprint": "chrome"},
		},
	}
	got, err := svc.CreateInbound(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.TLS.ServerName != "t.example.com" || len(got.TLS.ALPN) != 2 {
		t.Fatalf("typed view: %+v", got.TLS)
	}
	if !strings.Contains(fx.last(), "-----BEGIN CERTIFICATE-----") {
		t.Fatal("pasted certificate must reach the config")
	}

	k := uiInbound()
	k.Port, k.Remark = 9002, "kcp"
	k.StreamSettings = model.Doc{"network": "mkcp", "security": "none", "kcpSettings": map[string]any{"mtu": 1350.0, "tti": 50.0}}
	kcp, err := svc.CreateInbound(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if kcp.Network != model.NetKCP {
		t.Fatalf("mkcp must be normalised to kcp, got %q", kcp.Network)
	}
}

func TestSwitchingTransportLeavesNoStaleSettings(t *testing.T) {
	svc, fx, _ := newService(t)
	ctx := context.Background()
	in := uiInbound()
	in.StreamSettings["wsSettings"] = map[string]any{"path": "/old"} // left over from an earlier transport
	got, err := svc.CreateInbound(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	x := inboundByTag(t, lastConfig(t, fx), xray.InboundTag(got.ID))
	if _, ok := x["streamSettings"].(map[string]any)["wsSettings"]; ok {
		t.Fatal("settings of an unselected transport must not be written to the config")
	}
}

func TestInboundLimitExhaustsAndResets(t *testing.T) {
	svc, fx, st := newService(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	in := uiInbound()
	in.TotalBytes = 100
	in.TrafficReset = model.ResetDaily
	got, err := svc.CreateInbound(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	tag := xray.InboundTag(got.ID)
	if got.LastReset == nil {
		t.Fatal("a periodic reset starts counting from creation")
	}

	fx.stats = map[string]xray.Traffic{xray.InboundStatPrefix + tag: {Up: 60, Down: 50}}
	if err := svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	cur, _ := st.GetInbound(ctx, got.ID)
	if cur.Up != 60 || cur.Down != 50 {
		t.Fatalf("inbound counters: %d/%d", cur.Up, cur.Down)
	}
	for _, x := range lastConfig(t, fx)["inbounds"].([]any) {
		if x.(map[string]any)["tag"] == tag {
			t.Fatal("an inbound over its limit must leave the config")
		}
	}

	now = now.Add(24 * time.Hour) // the next day
	if err := svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	cur, _ = st.GetInbound(ctx, got.ID)
	if cur.Up != 0 || cur.Down != 0 || cur.LastReset == nil || !cur.LastReset.Equal(now) {
		t.Fatalf("the daily reset must zero the counters: %+v", cur)
	}
	inboundByTag(t, lastConfig(t, fx), tag) // it is served again

	// A manual reset and an expiry date work as well.
	cur.Up = 500
	_ = st.PutInbound(ctx, *cur)
	if _, err := svc.ResetInboundTraffic(ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	past := now.Add(-time.Hour)
	edit := uiInbound()
	edit.ExpiryAt = &past
	if _, err := svc.UpdateInbound(ctx, got.ID, edit); err != nil {
		t.Fatal(err)
	}
	for _, x := range lastConfig(t, fx)["inbounds"].([]any) {
		if x.(map[string]any)["tag"] == tag {
			t.Fatal("an expired inbound must leave the config")
		}
	}
}

func TestResetPeriods(t *testing.T) {
	loc := time.UTC
	last := time.Date(2026, 10, 31, 12, 0, 0, 0, loc) // a Saturday
	cases := []struct {
		kind string
		now  time.Time
		due  bool
	}{
		{model.ResetNever, last.Add(1000 * time.Hour), false},
		{model.ResetHourly, last.Add(30 * time.Minute), false},
		{model.ResetHourly, last.Add(61 * time.Minute), true},
		{model.ResetDaily, time.Date(2026, 10, 31, 23, 59, 0, 0, loc), false},
		{model.ResetDaily, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), true},
		{model.ResetWeekly, time.Date(2026, 11, 1, 12, 0, 0, 0, loc), false}, // Sunday still the same week
		{model.ResetWeekly, time.Date(2026, 11, 2, 0, 0, 0, 0, loc), true},   // Monday
		{model.ResetMonthly, time.Date(2026, 10, 31, 23, 0, 0, 0, loc), false},
		{model.ResetMonthly, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), true},
	}
	for _, c := range cases {
		in := model.Inbound{TrafficReset: c.kind, LastReset: &last}
		if got := resetDue(in, c.now); got != c.due {
			t.Errorf("%s at %v: due=%v, want %v", c.kind, c.now, got, c.due)
		}
	}
}

func TestSubscriptionOrderAndExclusion(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain, set.RealityDomain = "panel.example.com", "reality.example.com"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	mk := func(port, order int, remark string, exclude bool) model.Inbound {
		in := wsInbound(port)
		in.Remark, in.SortOrder, in.ExcludeFromSub = remark, order, exclude
		return in
	}
	var ids []string
	for _, in := range []model.Inbound{mk(10001, 3, "third", false), mk(10002, 1, "first", false), mk(10003, 2, "hidden", true)} {
		c, err := svc.CreateInbound(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	cl, err := svc.CreateClient(ctx, model.Client{Email: "ann", Enable: true, InboundIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := svc.PublicSubscription(ctx, cl.SubID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Links) != 2 || sub.Links[0].Remark != "first" || sub.Links[1].Remark != "third" {
		t.Fatalf("links must follow the sort order and skip excluded inbounds: %+v", sub.Links)
	}
	all, _ := svc.ClientLinks(ctx, cl.ID)
	if len(all.Links) != 3 {
		t.Fatalf("the admin still sees every link: %d", len(all.Links))
	}
}

func TestXrayProfileTemplateAndOptions(t *testing.T) {
	svc, fx, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.CreateInbound(ctx, uiInbound()); err != nil {
		t.Fatal(err)
	}
	p := model.XrayProfile{
		LogLevel: "debug", BitTorrent: "allow", BlockPrivate: true, DomainStrategy: "IPIfNonMatch",
		Template: model.Doc{
			"outbounds": []any{map[string]any{"tag": "warp", "protocol": "socks"}, map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{"domainStrategy": "UseIPv4"}}},
			"routing":   map[string]any{"rules": []any{map[string]any{"type": "field", "domain": []any{"geosite:netflix"}, "outboundTag": "warp"}}},
			"dns":       map[string]any{"servers": []any{"1.1.1.1"}},
		},
	}
	if _, err := svc.PutXrayProfile(ctx, LocalServer, p); err != nil {
		t.Fatal(err)
	}
	cfg := lastConfig(t, fx)
	if cfg["log"].(map[string]any)["loglevel"] != "debug" || cfg["dns"] == nil {
		t.Fatalf("log level / dns not applied: %v", cfg["log"])
	}
	outs := cfg["outbounds"].([]any)
	if len(outs) != 3 { // direct (replaced), blocked, warp
		t.Fatalf("outbounds: %+v", outs)
	}
	if outs[0].(map[string]any)["settings"] == nil {
		t.Fatal("an outbound with a generated tag replaces it")
	}
	rules := cfg["routing"].(map[string]any)["rules"].([]any)
	// api rule first, then the template rule, then the generated private-address block (no BitTorrent rule: allowed).
	if len(rules) != 3 || rules[0].(map[string]any)["outboundTag"] != "api" || rules[1].(map[string]any)["outboundTag"] != "warp" || rules[2].(map[string]any)["outboundTag"] != "blocked" {
		t.Fatalf("rules: %+v", rules)
	}
	if cfg["routing"].(map[string]any)["domainStrategy"] != "IPIfNonMatch" {
		t.Fatal("domain strategy")
	}
	got, err := svc.GetXrayProfile(ctx, LocalServer)
	if err != nil || got.LogLevel != "debug" {
		t.Fatalf("profile not stored: %+v %v", got, err)
	}
	// The settings form must not wipe it.
	set, _ := svc.GetSettings(ctx)
	set.XrayProfile = model.XrayProfile{}
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetXrayProfile(ctx, LocalServer); got.LogLevel != "debug" {
		t.Fatal("saving settings must keep the Xray profile")
	}

	for name, bad := range map[string]model.XrayProfile{
		"inbounds in template": {Template: model.Doc{"inbounds": []any{}}},
		"log level":            {LogLevel: "loud"},
		"bittorrent":           {BitTorrent: "maybe"},
		"domain strategy":      {DomainStrategy: "x"},
	} {
		if _, err := svc.PutXrayProfile(ctx, LocalServer, bad); !isInvalid(err) {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}

	prev, err := svc.PreviewConfig(ctx, LocalServer)
	if err != nil || !strings.Contains(string(prev), "warp") {
		t.Fatalf("preview: %v", err)
	}
}

func TestXrayProfileOfAServerIsItsOwn(t *testing.T) {
	svc, _, st := newService(t)
	ctx := context.Background()
	sv, _, err := svc.CreateServer(ctx, model.Server{Name: "DE", Address: "de.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PutXrayProfile(ctx, sv.ID, model.XrayProfile{LogLevel: "error", BlockPrivate: true}); err != nil {
		t.Fatal(err)
	}
	stored, _ := st.GetServer(ctx, sv.ID)
	if stored.XrayProfile.LogLevel != "error" || !stored.XrayProfile.BlockPrivate {
		t.Fatalf("server profile: %+v", stored.XrayProfile)
	}
	if local, _ := svc.GetXrayProfile(ctx, LocalServer); local.LogLevel != "" {
		t.Fatal("the main server profile must be untouched")
	}
	// Editing the server's address must keep its profile.
	if _, err := svc.UpdateServer(ctx, sv.ID, model.Server{Name: "DE 2", Address: "de2.example.com", Port: 8765, Enable: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetXrayProfile(ctx, sv.ID); got.LogLevel != "error" {
		t.Fatal("updating a server must keep its Xray profile")
	}
	cfg, err := svc.PreviewConfig(ctx, sv.ID)
	if err != nil || !strings.Contains(string(cfg), `"loglevel": "error"`) {
		t.Fatalf("server preview: %v", err)
	}
}

func TestMigrationGivesLegacyInboundsTheirDocuments(t *testing.T) {
	svc, fx, st := newService(t)
	ctx := context.Background()
	legacy := realityInbound()
	legacy.ID, legacy.CreatedAt = "old1", time.Now()
	legacy.Reality.PrivateKey, legacy.Reality.PublicKey, legacy.Reality.ShortIDs = "", "", nil
	priv, pub, _ := xray.GenerateRealityKeys()
	legacy.Reality.PrivateKey, legacy.Reality.PublicKey, legacy.Reality.ShortIDs = priv, pub, []string{"ab12"}
	if err := st.PutInbound(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := svc.MigrateInbounds(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetInbound(ctx, "old1")
	if !got.HasRaw() || got.Settings == nil || got.TrafficReset != model.ResetNever {
		t.Fatalf("migration must store the documents: %+v", got)
	}
	if err := svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	x := inboundByTag(t, lastConfig(t, fx), "in-old1")
	stream := x["streamSettings"].(map[string]any)
	if stream["realitySettings"].(map[string]any)["dest"] != "yandex.ru:443" || stream["sockopt"].(map[string]any)["acceptProxyProtocol"] != true {
		t.Fatalf("migrated inbound must produce the same config as before: %+v", stream)
	}
}

type execXray struct {
	fakeXray
	out map[string]string
}

func (e *execXray) Exec(_ context.Context, args ...string) (string, error) {
	return e.out[args[0]], nil
}

func TestGenerators(t *testing.T) {
	svc, _, _ := newService(t)
	fx := &execXray{out: map[string]string{
		"vlessenc": `Choose one Authentication to use, do not mix them.

Authentication: X25519, not Post-Quantum
"decryption": "mlkem768x25519plus.native.600s.AAA"
"encryption": "mlkem768x25519plus.native.0rtt.BBB"

Authentication: ML-KEM-768, Post-Quantum
"decryption": "mlkem768x25519plus.native.600s.CCC"
"encryption": "mlkem768x25519plus.native.0rtt.DDD"
`,
		"mldsa65": "Seed: SEEDVALUE\nVerify: VERIFYVALUE\n",
		"tls":     "ECH config list: LISTVALUE\nECH server keys: KEYSVALUE\n",
	}}
	svc.Xray = fx
	ctx := context.Background()
	v, err := svc.GenerateVlessEnc(ctx)
	if err != nil || v.X25519.Decryption != "mlkem768x25519plus.native.600s.AAA" || v.MLKEM.Encryption != "mlkem768x25519plus.native.0rtt.DDD" {
		t.Fatalf("vlessenc: %+v %v", v, err)
	}
	seed, verify, err := svc.GenerateMLDSA65(ctx)
	if err != nil || seed != "SEEDVALUE" || verify != "VERIFYVALUE" {
		t.Fatalf("mldsa65: %q %q %v", seed, verify, err)
	}
	keys, list, err := svc.GenerateECH(ctx, "example.com")
	if err != nil || keys != "KEYSVALUE" || list != "LISTVALUE" {
		t.Fatalf("ech: %q %q %v", keys, list, err)
	}
	if _, _, err := svc.GenerateECH(ctx, "bad host!"); !isInvalid(err) {
		t.Fatal("ech server name must be validated")
	}
	if got := xray.VlessEncMode("mlkem768x25519plus.native.0rtt.KEY", "xorpub"); got != "mlkem768x25519plus.xorpub.0rtt.KEY" {
		t.Fatalf("mode switch: %s", got)
	}

	// Without the Xray binary the generators explain themselves.
	svc.Xray = &fakeXray{notReady: true}
	if _, err := svc.GenerateVlessEnc(ctx); !isInvalid(err) {
		t.Fatalf("no xray: %v", err)
	}
}
