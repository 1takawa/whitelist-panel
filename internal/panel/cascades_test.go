package panel

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"anypanel2/internal/front"
	"anypanel2/internal/model"
	"anypanel2/internal/nodeinstall"
	"anypanel2/internal/store"
	"anypanel2/internal/xray"
)

type fakeSNI struct {
	sites []front.Site
	calls int
	fail  error
}

func (f *fakeSNI) Apply(_ context.Context, sites []front.Site) error {
	f.calls++
	if f.fail != nil {
		return f.fail
	}
	f.sites = append([]front.Site(nil), sites...)
	return nil
}

// cascadeEnv: the main server is the entry, an installed node (with a REALITY connection) is the exit. The main server
// can manage extra domains (fake nginx/Caddy) and DNS is a map the tests edit: the panel and REALITY domains point at the
// main server (203.0.113.10), the node's own name and n1/n2 at the node (203.0.113.20), c1/c2 at the main server.
func cascadeEnv(t *testing.T) (svc *Service, fx *fakeXray, node *fakeNode, serverID string) {
	t.Helper()
	svc, fx, node, serverID, _, _ = cascadeEnvFull(t)
	return
}

func cascadeEnvFull(t *testing.T) (svc *Service, fx *fakeXray, node *fakeNode, serverID string, sni *fakeSNI, dns map[string][]string) {
	t.Helper()
	ctx := context.Background()
	svc, _, node = installEnv(t)
	fx = svc.Xray.(*fakeXray)
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain, set.RealityDomain = "panel.example.com", "reality.example.com"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	j, err := svc.StartNodeInstall(ctx, goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, svc, j.ID)
	if done.Status != "done" {
		t.Fatalf("%+v", done)
	}
	sni = &fakeSNI{}
	svc.SNI = sni
	dns = map[string][]string{
		"panel.example.com":     {"203.0.113.10"},
		"reality.example.com":   {"203.0.113.10"},
		"c1.example.com":        {"203.0.113.10"},
		"c2.example.com":        {"203.0.113.10"},
		"n1.example.com":        {"203.0.113.20"},
		"n2.example.com":        {"203.0.113.20"},
		"elsewhere.example.com": {"198.51.100.7"},
	}
	svc.LookupHost = func(_ context.Context, host string) ([]string, error) {
		if a, ok := dns[host]; ok {
			return a, nil
		}
		return nil, errors.New("no such host")
	}
	return svc, fx, node, done.ServerID, sni, dns
}

func cfgOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	return m
}

func outboundByTag(cfg map[string]any, tag string) map[string]any {
	outs, _ := cfg["outbounds"].([]any)
	for _, o := range outs {
		if m, ok := o.(map[string]any); ok && m["tag"] == tag {
			return m
		}
	}
	return nil
}

func mustInbounds(t *testing.T, svc *Service) []model.Inbound {
	t.Helper()
	list, err := svc.Store.ListInbounds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// split makes a cascade out of the main server (entry) and the node (exit) with a domain of its own.
func split(name, domain, exit string) CascadeInput {
	return CascadeInput{Name: name, Country: "FI", ExitServerID: exit, Mode: CascadeSplit, Domain: domain}
}

// mainVless gives the main server a VLESS connection a cascade can exit through.
func mainVless(t *testing.T, svc *Service) {
	t.Helper()
	in, err := newRealityInbound("main exit", "", "0.0.0.0", 24443, "example.com:443", "example.com", false, 24443)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateInbound(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func TestCascadeCreatesANodeForEveryClientAndRoutesItThroughTheExit(t *testing.T) {
	ctx := context.Background()
	svc, fx, node, exitServer := cascadeEnv(t)
	alice, _ := svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true})

	v, warning, err := svc.CreateCascade(ctx, CascadeInput{Name: "Финляндия", Country: "fi", ExitServerID: exitServer, Mode: CascadeSplit, Domain: "c1.example.com"})
	if err != nil || warning != "" {
		t.Fatalf("%v %q", err, warning)
	}
	if v.Flag != "🇫🇮" || v.Name != "Финляндия" || v.EntryName != "Основной сервер" || v.ExitName != "Frankfurt" || v.State != "running" || v.Port != 443 || v.Mode != CascadeSplit {
		t.Fatalf("%+v", v)
	}

	// The node of the cascade is an ordinary inbound on the entry server that every client already has.
	entry, err := svc.Store.GetInbound(ctx, v.InboundID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.CascadeID != v.ID || entry.ServerID != "" || entry.Remark != "🇫🇮 Финляндия" || entry.Security != model.SecReality || entry.Reality.Dest != mainDecoyTarget ||
		entry.Reality.ServerNames[0] != "c1.example.com" || len(entry.Reality.ShortIDs) != 8 || !entry.AcceptProxyProtocol || entry.Port < 8444 || entry.Listen != "127.0.0.1" ||
		entry.Public.Port != 443 || entry.Public.Host != "c1.example.com" {
		t.Fatalf("%+v", entry)
	}
	if got, _ := svc.Store.GetClient(ctx, alice.ID); !got.Uses(entry.ID) {
		t.Fatal("existing clients get the cascade's node like any new inbound")
	}
	if lv, _ := svc.ClientLinks(ctx, alice.ID); !strings.Contains(strings.Join(func() []string {
		var names []string
		for _, l := range lv.Links {
			names = append(names, l.Link)
		}
		return names
	}(), "\n"), "%F0%9F%87%AB%F0%9F%87%AE") {
		t.Fatal("the link carries the flag and name chosen for the cascade")
	}

	// Entry (main) config: the outbound towards the exit and the rule that sends the cascade's inbound there.
	cfg := cfgOf(t, fx.last())
	out := outboundByTag(cfg, xray.CascadeTag(v.ID))
	if out == nil || out["protocol"] != "vless" {
		t.Fatalf("outbound to the exit server: %s", fx.last())
	}
	raw, _ := json.Marshal(out)
	exitIn := func() model.Inbound {
		for _, in := range mustInbounds(t, svc) {
			if in.ServerID == exitServer {
				return in
			}
		}
		t.Fatal("no exit inbound")
		return model.Inbound{}
	}()
	for _, want := range []string{"203.0.113.20", `"port":443`, "node.example.com", exitIn.Reality.PublicKey, "xtls-rprx-vision", relayOf(svc, v.ID)} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the outbound lacks %q: %s", want, raw)
		}
	}
	rules, _ := cfg["routing"].(map[string]any)["rules"].([]any)
	found := false
	for _, r := range rules {
		m := r.(map[string]any)
		if m["outboundTag"] == xray.CascadeTag(v.ID) {
			tags, _ := m["inboundTag"].([]any)
			found = len(tags) == 1 && tags[0] == xray.InboundTag(entry.ID)
		}
	}
	if !found {
		t.Fatalf("a rule sends the cascade's inbound to the exit: %v", rules)
	}

	// Exit (node) config: the relay user is a user of the exit connection, not a client of the panel.
	nodeCfg := node.lastConfig()
	if !strings.Contains(nodeCfg, relayOf(svc, v.ID)) || !strings.Contains(nodeCfg, "cascade-"+v.ID) {
		t.Fatalf("the exit server knows the relay user:\n%s", nodeCfg)
	}
	if cls, _ := svc.ListClients(ctx); len(cls) != 1 {
		t.Fatalf("the relay is not a client: %+v", cls)
	}
}

func relayOf(svc *Service, id string) string {
	c, err := svc.Store.GetCascade(context.Background(), id)
	if err != nil {
		return "no-such-cascade"
	}
	return c.RelayUUID
}

func TestCascadeValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, _, exit := cascadeEnv(t)
	bad := map[string]CascadeInput{
		"no mode":        {Name: "x", ExitServerID: exit, Domain: "c1.example.com"},
		"unknown mode":   {Name: "x", ExitServerID: exit, Mode: "mix", Domain: "c1.example.com"},
		"no name":        split(" ", "c1.example.com", exit),
		"bad country":    {Name: "x", Country: "Finland", ExitServerID: exit, Mode: CascadeSplit, Domain: "c1.example.com"},
		"same server":    {Name: "x", Mode: CascadeSplit, Domain: "c1.example.com"},
		"unknown exit":   split("x", "c1.example.com", "nope"),
		"unknown entry":  {Name: "x", EntryServerID: "nope", Mode: CascadeSplit, Domain: "c1.example.com"},
		"wrong exit in":  {Name: "x", ExitServerID: exit, ExitInboundID: "nope", Mode: CascadeSplit, Domain: "c1.example.com"},
		"exit has none":  {Name: "x", EntryServerID: exit, Mode: CascadeSplit, Domain: "n1.example.com"}, // the main server has no connection to exit through
		"split no name":  split("x", "", exit),
		"split bad name": split("x", "not a domain", exit),
		"merge no own":   {Name: "x", ExitServerID: exit, Mode: CascadeMerge}, // the main server has no REALITY connection on 443
	}
	for name, in := range bad {
		if _, _, err := svc.CreateCascade(ctx, in); !isInvalid(err) {
			t.Errorf("%s must be refused, got %v", name, err)
		}
	}
	if list, _ := svc.ListCascades(ctx); len(list) != 0 {
		t.Fatalf("a refused cascade leaves nothing behind: %+v", list)
	}
}

func TestCascadeStopsInsteadOfLeakingDirectlyWhenTheExitGoesAway(t *testing.T) {
	ctx := context.Background()
	svc, fx, _, exitServer := cascadeEnv(t)
	v, _, err := svc.CreateCascade(ctx, split("FI", "c1.example.com", exitServer))
	if err != nil {
		t.Fatal(err)
	}
	entryTag := xray.InboundTag(v.InboundID)

	// Switch the exit connection off: the cascade's inbound disappears from the entry config altogether.
	var exit model.Inbound
	for _, in := range mustInbounds(t, svc) {
		if in.ServerID == exitServer {
			exit = in
		}
	}
	off := exit
	off.Enable = false
	if _, err := svc.UpdateInbound(ctx, exit.ID, off); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fx.last(), entryTag) || strings.Contains(fx.last(), xray.CascadeTag(v.ID)) {
		t.Fatalf("with the exit off nothing may let clients out directly:\n%s", fx.last())
	}
	list, _ := svc.ListCascades(ctx)
	if len(list) != 1 || list[0].State != "broken" || !strings.Contains(list[0].Problem, "выключено") {
		t.Fatalf("the admin is told why: %+v", list)
	}

	// Back on: the node returns.
	off.Enable = true
	if _, err := svc.UpdateInbound(ctx, exit.ID, off); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fx.last(), entryTag) {
		t.Fatal("the cascade runs again")
	}
}

func TestCascadeRenameAndDelete(t *testing.T) {
	ctx := context.Background()
	svc, fx, node, exitServer := cascadeEnv(t)
	v, _, _ := svc.CreateCascade(ctx, split("FI", "c1.example.com", exitServer))
	entryID := v.InboundID

	r, err := svc.RenameCascade(ctx, v.ID, "Хельсинки", "FI")
	if err != nil || r.Name != "Хельсинки" {
		t.Fatalf("%+v %v", r, err)
	}
	if in, _ := svc.Store.GetInbound(ctx, entryID); in.Remark != "🇫🇮 Хельсинки" || in.CascadeID != v.ID {
		t.Fatalf("the node follows the name and keeps its link to the cascade: %+v", in)
	}

	// A form cannot cut the node loose from its cascade.
	in, _ := svc.Store.GetInbound(ctx, entryID)
	edit := *in
	edit.CascadeID = ""
	if got, err := svc.UpdateInbound(ctx, entryID, edit); err != nil || got.CascadeID != v.ID {
		t.Fatalf("%+v %v", got, err)
	}

	relay := relayOf(svc, v.ID)
	if err := svc.DeleteCascade(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.GetInbound(ctx, entryID); err == nil {
		t.Fatal("the node is gone")
	}
	if list, _ := svc.ListCascades(ctx); len(list) != 0 {
		t.Fatalf("%+v", list)
	}
	if strings.Contains(node.lastConfig(), relay) || strings.Contains(fx.last(), xray.CascadeTag(v.ID)) {
		t.Fatal("the relay user leaves the exit server and the routing the entry server")
	}
	if err := svc.DeleteCascade(ctx, v.ID); err == nil {
		t.Fatal("deleting twice")
	}

	// Deleting the node itself from the connections page takes the cascade with it.
	v2, _, _ := svc.CreateCascade(ctx, split("DE", "c1.example.com", exitServer))
	if err := svc.DeleteInbound(ctx, v2.InboundID); err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.ListCascades(ctx); len(list) != 0 {
		t.Fatalf("no cascade without its node: %+v", list)
	}
}

func TestCascadeSurvivesFullExportAndImport(t *testing.T) {
	ctx := context.Background()
	svc, _, _, exitServer := cascadeEnv(t)
	_ = svc.Store.PutAdmin(ctx, store.Admin{Username: "boss", PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"})
	v, _, _ := svc.CreateCascade(ctx, split("FI", "c1.example.com", exitServer))
	relay := relayOf(svc, v.ID)

	_, data, err := svc.ExportDB(ctx, ExportFull)
	if err != nil || !strings.Contains(string(data), relay) {
		t.Fatalf("the full export carries the relay identity: %v", err)
	}
	other, _, _ := newService(t)
	other.NodeClient = svc.NodeClient // no real agent to dial
	if _, err := other.ImportDB(ctx, data, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	c, err := other.Store.GetCascade(ctx, v.ID)
	if err != nil || c.RelayUUID != relay || c.ExitInboundID != v.ExitInboundID || c.Domain != "c1.example.com" {
		t.Fatalf("%+v %v", c, err)
	}
	// A damaged file is refused.
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	m["cascades"].([]any)[0].(map[string]any)["exitInboundId"] = "gone"
	bad, _ := json.Marshal(m)
	if _, err := other.ImportDB(ctx, bad, ImportOptions{DryRun: true}); err == nil {
		t.Fatal("a cascade whose exit connection is not in the file is refused")
	}
}

func TestCascadeDomainCheck(t *testing.T) {
	ctx := context.Background()
	svc, _, _, exitServer, _, dns := cascadeEnvFull(t)
	dns["exit-alias.example.com"] = []string{"203.0.113.20"}

	cases := []struct {
		domain, entry, want string
		ok                  bool
	}{
		{"c1.example.com", "", "свободен", true},
		{"C1.Example.com ", "", "свободен", true},
		{"not a domain", "", "не доменное имя", false},
		{"panel.example.com", "", "домен панели", false},
		{"reality.example.com", "", "REALITY основного", false},
		{"nowhere.example.com", "", "нет A-записи", false},
		{"elsewhere.example.com", "", "исправьте A-запись", false},
		{"exit-alias.example.com", "", "другой сервер", false},      // points at the node, but the entry is the main server
		{"n1.example.com", exitServer, "Frankfurt", true},           // points at the node, and the node is the entry
		{"c1.example.com", exitServer, "исправьте A-запись", false}, // points at the main server, but the entry is the node
		{"node.example.com", exitServer, "уже занят", false},        // the node's own REALITY name
	}
	for _, c := range cases {
		got, err := svc.CheckCascadeDomain(ctx, c.domain, "", c.entry)
		if err != nil {
			t.Fatal(err)
		}
		if got.OK != c.ok || !strings.Contains(got.Message+got.Problem, c.want) {
			t.Fatalf("%q entry %q: %+v (want ok=%v, %q)", c.domain, c.entry, got, c.ok, c.want)
		}
	}
	// A domain already used by another cascade is busy, but not for the cascade that owns it.
	v, _, err := svc.CreateCascade(ctx, split("one", "c1.example.com", exitServer))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.CheckCascadeDomain(ctx, "c1.example.com", "", ""); got.OK || !strings.Contains(got.Problem, "«one»") {
		t.Fatalf("%+v", got)
	}
	if got, _ := svc.CheckCascadeDomain(ctx, "c1.example.com", v.ID, ""); !got.OK {
		t.Fatalf("the owner may keep its domain: %+v", got)
	}
}

func TestCascadesEachGetTheirOwnDomainOn443(t *testing.T) {
	ctx := context.Background()
	svc, _, _, exitServer, sni, _ := cascadeEnvFull(t)

	a, _, err := svc.CreateCascade(ctx, CascadeInput{Name: "A", Country: "DE", ExitServerID: exitServer, Mode: CascadeSplit, Domain: "c1.example.com"})
	if err != nil || a.Port != 443 || a.Domain != "c1.example.com" || a.EntryAddress != "c1.example.com" {
		t.Fatalf("%+v %v", a, err)
	}
	b, _, err := svc.CreateCascade(ctx, CascadeInput{Name: "B", Country: "PL", ExitServerID: exitServer, Mode: CascadeSplit, Domain: "c2.example.com"})
	if err != nil || b.Port != 443 {
		t.Fatalf("a second cascade on 443 needs only another domain: %+v %v", b, err)
	}
	ina, _ := svc.Store.GetInbound(ctx, a.InboundID)
	inb, _ := svc.Store.GetInbound(ctx, b.InboundID)
	if ina.Port == inb.Port || len(sni.sites) != 2 {
		t.Fatalf("nginx and Caddy know both, each on its own internal port: %+v", sni.sites)
	}
	want := map[string]int{"c1.example.com": ina.Port, "c2.example.com": inb.Port}
	for _, st := range sni.sites {
		if want[st.Domain] != st.Port {
			t.Fatalf("%+v", sni.sites)
		}
	}
	// Same domain twice, a domain that is not ready: refused before anything is created.
	n := len(mustInbounds(t, svc))
	for name, in := range map[string]CascadeInput{
		"busy":   split("x", "c1.example.com", exitServer),
		"no dns": split("x", "nowhere.example.com", exitServer),
	} {
		if _, _, err := svc.CreateCascade(ctx, in); !isInvalid(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(mustInbounds(t, svc)) != n {
		t.Fatal("a refused cascade leaves nothing behind")
	}

	// Deleting one takes only its domain away.
	if err := svc.DeleteCascade(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if len(sni.sites) != 1 || sni.sites[0].Domain != "c2.example.com" {
		t.Fatalf("%+v", sni.sites)
	}
	if err := svc.DeleteCascade(ctx, b.ID); err != nil || len(sni.sites) != 0 {
		t.Fatalf("%v %+v", err, sni.sites)
	}
}

func TestCascadeDomainIsTakenBackWhenNginxOrCaddyRefuseIt(t *testing.T) {
	ctx := context.Background()
	svc, _, _, exitServer, sni, _ := cascadeEnvFull(t)
	sni.fail = errors.New("nginx rejected the configuration")
	n := len(mustInbounds(t, svc))
	_, _, err := svc.CreateCascade(ctx, split("A", "c1.example.com", exitServer))
	if err == nil || !strings.Contains(err.Error(), "nginx rejected") {
		t.Fatalf("%v", err)
	}
	if list, _ := svc.Store.ListCascades(ctx); len(list) != 0 || len(mustInbounds(t, svc)) != n {
		t.Fatalf("no half-made cascade: %+v", list)
	}
	// Without the SNI manager the feature says so instead of pretending.
	sni.fail = nil
	svc.SNI = nil
	if _, _, err := svc.CreateCascade(ctx, split("A", "c1.example.com", exitServer)); !isInvalid(err) {
		t.Fatalf("%v", err)
	}
}

func TestSyncSNIAtStartOnlyWhenCascadesHaveDomains(t *testing.T) {
	ctx := context.Background()
	svc, _, _, exitServer, sni, _ := cascadeEnvFull(t)
	if err := svc.SyncSNI(ctx); err != nil || sni.calls != 0 {
		t.Fatalf("nothing to serve, nothing touched: %v %d", err, sni.calls)
	}
	if _, _, err := svc.CreateCascade(ctx, split("A", "c1.example.com", exitServer)); err != nil {
		t.Fatal(err)
	}
	before := sni.calls
	sni.sites = nil // the files were lost
	if err := svc.SyncSNI(ctx); err != nil || sni.calls != before+1 || len(sni.sites) != 1 {
		t.Fatalf("%v %d %+v", err, sni.calls, sni.sites)
	}
}

// Split with a node as the entry: the node keeps its own connection and gets a second REALITY name on 443, set up in
// the node's nginx over SSH.
func TestSplitCascadeOnANode(t *testing.T) {
	ctx := context.Background()
	svc, _, node, nodeID, _, _ := cascadeEnvFull(t)
	fi := svc.Installer.(*fakeInstaller)
	mainVless(t, svc)
	var own model.Inbound
	for _, in := range mustInbounds(t, svc) {
		if in.ServerID == nodeID {
			own = in
		}
	}
	n := len(mustInbounds(t, svc))

	v, _, err := svc.CreateCascade(ctx, CascadeInput{Name: "Нидерланды-Германия", Country: "DE", EntryServerID: nodeID, ExitServerID: "", Mode: CascadeSplit, Domain: "n1.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Port != 443 || v.EntryAddress != "n1.example.com" || v.Mode != CascadeSplit || len(mustInbounds(t, svc)) != n+1 {
		t.Fatalf("%+v", v)
	}
	entry, _ := svc.Store.GetInbound(ctx, v.InboundID)
	if entry.ServerID != nodeID || entry.Listen != "127.0.0.1" || entry.Port != 8444 || !entry.AcceptProxyProtocol || entry.Public.Port != 443 ||
		entry.Reality.ServerNames[0] != "n1.example.com" || entry.Reality.Dest != nodeDecoyTarget || entry.Reality.PrivateKey == own.Reality.PrivateKey {
		t.Fatalf("a second REALITY inbound behind the node's nginx: %+v", entry)
	}
	if sites := fi.sni["203.0.113.20"]; len(sites) != 1 || sites[0] != (nodeinstall.SNISite{Domain: "n1.example.com", Port: 8444}) {
		t.Fatalf("the node's nginx was told: %+v", fi.sni)
	}
	if cfg := node.lastConfig(); !strings.Contains(cfg, "cascade-"+v.ID) || !strings.Contains(cfg, own.Reality.PrivateKey) {
		t.Fatalf("the node keeps its own connection and routes the new one:\n%s", cfg)
	}
	if got, _ := svc.Store.GetInbound(ctx, own.ID); got.CascadeID != "" || got.Remark != own.Remark {
		t.Fatalf("the node's own connection is untouched: %+v", got)
	}

	// Taking the name away again.
	if err := svc.DeleteCascade(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if sites := fi.sni["203.0.113.20"]; len(sites) != 0 {
		t.Fatalf("%+v", fi.sni)
	}

	// nginx on the node says no: nothing is left behind.
	fi.sniErr = errors.New("nginx -t failed")
	n = len(mustInbounds(t, svc))
	if _, _, err := svc.CreateCascade(ctx, CascadeInput{Name: "x", EntryServerID: nodeID, Mode: CascadeSplit, Domain: "n2.example.com"}); err == nil || !strings.Contains(err.Error(), "nginx") {
		t.Fatalf("%v", err)
	}
	if list, _ := svc.Store.ListCascades(ctx); len(list) != 0 || len(mustInbounds(t, svc)) != n {
		t.Fatalf("no half-made cascade: %+v", list)
	}
}

// Merge: the entry server's own REALITY connection on 443 becomes the cascade; nothing new is created.
func TestMergeCascadeUsesTheEntryServersOwnConnection(t *testing.T) {
	ctx := context.Background()
	svc, _, node, nodeID := cascadeEnv(t)
	mainVless(t, svc)
	var own model.Inbound
	for _, in := range mustInbounds(t, svc) {
		if in.ServerID == nodeID {
			own = in
		}
	}
	n := len(mustInbounds(t, svc))

	v, _, err := svc.CreateCascade(ctx, CascadeInput{Name: "Польша-Германия", Country: "PL", EntryServerID: nodeID, Mode: CascadeMerge})
	if err != nil {
		t.Fatal(err)
	}
	if v.Port != 443 || v.InboundID != own.ID || v.State != "running" || v.Mode != CascadeMerge || len(mustInbounds(t, svc)) != n {
		t.Fatalf("the same connection, no new one: %+v", v)
	}
	got, _ := svc.Store.GetInbound(ctx, own.ID)
	if got.CascadeID != v.ID || got.Remark != "🇵🇱 Польша-Германия" || got.Reality.PrivateKey != own.Reality.PrivateKey || got.Public.Port != 443 {
		t.Fatalf("%+v", got)
	}
	if cfg := node.lastConfig(); !strings.Contains(cfg, "cascade-"+v.ID) {
		t.Fatalf("the node routes the connection to the exit: %s", cfg)
	}
	// A second cascade cannot take the connection again, and a domain does not belong to a merge.
	if _, _, err := svc.CreateCascade(ctx, CascadeInput{Name: "y", EntryServerID: nodeID, Mode: CascadeMerge}); !isInvalid(err) {
		t.Fatalf("%v", err)
	}

	// Deleting the cascade gives the node its connection back as it was.
	if err := svc.DeleteCascade(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	back, err := svc.Store.GetInbound(ctx, own.ID)
	if err != nil || back.CascadeID != "" || back.Remark != own.Remark || len(mustInbounds(t, svc)) != n {
		t.Fatalf("%+v %v", back, err)
	}
	if cfg := node.lastConfig(); strings.Contains(cfg, "cascade-") {
		t.Fatalf("no routing to the exit is left: %s", cfg)
	}
}

func TestMergeCascadeOnTheMainServer(t *testing.T) {
	ctx := context.Background()
	svc, fx, _, exitServer := cascadeEnv(t)
	own, err := newRealityInbound("main reality", "", "127.0.0.1", mainRealityPort, mainDecoyTarget, "reality.example.com", true, 443)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateInbound(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := svc.CreateCascade(ctx, CascadeInput{Name: "Финляндия", Country: "FI", ExitServerID: exitServer, Mode: CascadeMerge})
	if err != nil || v.InboundID != created.ID || v.Port != 443 {
		t.Fatalf("%+v %v", v, err)
	}
	if !strings.Contains(fx.last(), xray.CascadeTag(v.ID)) {
		t.Fatalf("the main server routes the merged connection to the exit:\n%s", fx.last())
	}
}

// The exit connection can be hidden from subscriptions and still work as the cascade's exit.
func TestHiddenExitConnectionStillServesTheCascade(t *testing.T) {
	ctx := context.Background()
	svc, _, node, exitServer := cascadeEnv(t)
	alice, _ := svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true})
	v, _, err := svc.CreateCascade(ctx, split("FI", "c1.example.com", exitServer))
	if err != nil {
		t.Fatal(err)
	}
	var exit model.Inbound
	for _, in := range mustInbounds(t, svc) {
		if in.ServerID == exitServer {
			exit = in
		}
	}
	hidden := exit
	hidden.ExcludeFromSub = true
	if _, err := svc.UpdateInbound(ctx, exit.ID, hidden); err != nil {
		t.Fatal(err)
	}
	subscription, err := svc.PublicSubscription(ctx, alice.SubID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range subscription.Links {
		names = append(names, l.Remark)
	}
	if slices.Contains(names, exit.Remark) || !slices.Contains(names, "🇫🇮 FI") {
		t.Fatalf("the hidden connection is out of the subscription, the cascade is in: %v", names)
	}
	if list, _ := svc.ListCascades(ctx); len(list) != 1 || list[0].State != "running" {
		t.Fatalf("the cascade keeps working through the hidden exit: %+v", list)
	}
	if cfg := node.lastConfig(); !strings.Contains(cfg, relayOf(svc, v.ID)) {
		t.Fatalf("the hidden exit still serves the relay user:\n%s", cfg)
	}
}

func TestMainServerGetsANodeOnADomainOfItsOwn(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, sni, dns := cascadeEnvFull(t)
	dns["fresh.example.com"] = []string{"203.0.113.10"}
	svc.GeoCountry = func(context.Context, string) (string, error) { return "DE", nil }
	c, _ := svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true})

	node, warning, err := svc.SetupMainNode(ctx, MainNodeInput{Domain: " Fresh.Example.com "})
	if err != nil {
		t.Fatal(err)
	}
	_ = warning
	if node.NodeDomain != "fresh.example.com" || node.ServerID != "" || node.Public.Host != "fresh.example.com" || node.Public.Port != 443 ||
		node.Security != model.SecReality || !node.AcceptProxyProtocol || node.Port < 8444 || node.Reality.PrivateKey == "" {
		t.Fatalf("a REALITY connection behind nginx on its own domain: %+v", node)
	}
	if !strings.Contains(node.Remark, "fresh.example.com") || !strings.HasPrefix(node.Remark, "🇩🇪") {
		t.Fatalf("named after the domain, with the flag of its country: %q", node.Remark)
	}
	if len(sni.sites) != 1 || sni.sites[0] != (front.Site{Domain: "fresh.example.com", Port: node.Port}) {
		t.Fatalf("nginx and Caddy are told about the domain: %+v", sni.sites)
	}
	got, _ := svc.GetClient(ctx, c.ID)
	if !got.Uses(node.ID) {
		t.Fatal("the node is in every client's subscription")
	}

	// Editing the connection cannot take the marker away, and deleting it takes the domain away from nginx.
	upd := *node
	upd.NodeDomain = ""
	if after, err := svc.UpdateInbound(ctx, node.ID, upd); err != nil || after.NodeDomain != "fresh.example.com" {
		t.Fatalf("%+v %v", after, err)
	}
	if err := svc.DeleteInbound(ctx, node.ID); err != nil || len(sni.sites) != 0 {
		t.Fatalf("%v %+v", err, sni.sites)
	}
}

func TestMainServerNodeIsRefusedWhenTheDomainIsNotReady(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, sni, dns := cascadeEnvFull(t)
	dns["fresh.example.com"] = []string{"203.0.113.10"}
	n := len(mustInbounds(t, svc))
	for name, in := range map[string]MainNodeInput{
		"empty":      {},
		"no dns":     {Domain: "nowhere.example.com"},
		"elsewhere":  {Domain: "elsewhere.example.com"},
		"the panel":  {Domain: "panel.example.com"},
		"not a name": {Domain: "not a domain"},
	} {
		if _, _, err := svc.SetupMainNode(ctx, in); !isInvalid(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(mustInbounds(t, svc)) != n || sni.calls != 0 {
		t.Fatal("a refused node leaves nothing behind")
	}
	// nginx refusing the change takes the node back.
	sni.fail = errors.New("nginx: boom")
	if _, _, err := svc.SetupMainNode(ctx, MainNodeInput{Domain: "fresh.example.com"}); err == nil || len(mustInbounds(t, svc)) != n {
		t.Fatalf("%v", err)
	}
	// Without the SNI manager the feature says so.
	svc.SNI = nil
	if _, _, err := svc.SetupMainNode(ctx, MainNodeInput{Domain: "fresh.example.com"}); !isInvalid(err) {
		t.Fatalf("%v", err)
	}
}

func TestSyncSNIAtStartKnowsMainNodes(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, sni, dns := cascadeEnvFull(t)
	dns["fresh.example.com"] = []string{"203.0.113.10"}
	if _, _, err := svc.SetupMainNode(ctx, MainNodeInput{Domain: "fresh.example.com"}); err != nil {
		t.Fatal(err)
	}
	before := sni.calls
	if err := svc.SyncSNI(ctx); err != nil || sni.calls != before+1 || len(sni.sites) != 1 {
		t.Fatalf("a restart restores the files of a main node: %v %d %+v", err, sni.calls, sni.sites)
	}
}

func TestFreshDomainIsAcceptedBeforeDNSShowsIt(t *testing.T) {
	ctx := context.Background()
	svc, _, _, exitServer, _, _ := cascadeEnvFull(t)

	// Without the flag a domain DNS does not know is refused; with it (the panel just made the record) it is fine, marked pending.
	if got, _ := svc.CheckCascadeDomain(ctx, "later.example.com", "", ""); got.OK {
		t.Fatalf("%+v", got)
	}
	got, err := svc.CheckCascadeDomainWith(ctx, "later.example.com", "", "", true)
	if err != nil || !got.OK || !got.Pending {
		t.Fatalf("%+v %v", got, err)
	}
	// A record that already points elsewhere, or a name that is taken, is still refused.
	if got, _ := svc.CheckCascadeDomainWith(ctx, "elsewhere.example.com", "", "", true); got.OK {
		t.Fatalf("a wrong record is not pending: %+v", got)
	}
	if got, _ := svc.CheckCascadeDomainWith(ctx, "panel.example.com", "", "", true); got.OK {
		t.Fatalf("a taken name is not pending: %+v", got)
	}

	in := split("later", "later.example.com", exitServer)
	if _, _, err := svc.CreateCascade(ctx, in); !isInvalid(err) {
		t.Fatalf("without the flag: %v", err)
	}
	in.DNSPending = true
	v, _, err := svc.CreateCascade(ctx, in)
	if err != nil || v == nil || v.Domain != "later.example.com" {
		t.Fatalf("with the flag: %+v %v", v, err)
	}
}

// The page reads .length of both address lists: a null in the JSON (nil slice) would blank the whole page.
func TestDomainCheckListsAreNeverNull(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _, _, dns := cascadeEnvFull(t)
	// The main server's own names do not resolve, so there is nothing to compare the domain with.
	delete(dns, "panel.example.com")
	delete(dns, "reality.example.com")
	dns["c1.example.com"] = []string{"203.0.113.10"}
	got, err := svc.CheckCascadeDomain(ctx, "c1.example.com", "", "")
	if err != nil || !got.OK {
		t.Fatalf("%+v %v", got, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "null") {
		t.Fatalf("lists must be [] and not null: %s", raw)
	}
}

func TestInterruptedCascadeCreationIsRepairedAtStart(t *testing.T) {
	ctx := context.Background()
	svc, _, _, exitServer, sni, _ := cascadeEnvFull(t)
	v, _, err := svc.CreateCascade(ctx, split("half", "c1.example.com", exitServer))
	if err != nil {
		t.Fatal(err)
	}
	// The request died after the entry connection was made: the cascade is saved without a link to it.
	c, _ := svc.Store.GetCascade(ctx, v.ID)
	entryID := c.InboundID
	c.InboundID = ""
	if err := svc.Store.PutCascade(ctx, *c); err != nil {
		t.Fatal(err)
	}
	// A second one died before anything was built.
	ghost := model.Cascade{ID: "ghost", Name: "ghost", Domain: "c2.example.com", ExitServerID: exitServer}
	if err := svc.Store.PutCascade(ctx, ghost); err != nil {
		t.Fatal(err)
	}
	before := sni.calls
	if err := svc.RepairCascades(ctx); err != nil {
		t.Fatal(err)
	}
	fixed, err := svc.Store.GetCascade(ctx, v.ID)
	if err != nil || fixed.InboundID != entryID {
		t.Fatalf("the link to the entry connection is restored: %+v %v", fixed, err)
	}
	if sni.calls != before+1 {
		t.Fatal("nginx is told about the domain again")
	}
	if _, err := svc.Store.GetCascade(ctx, "ghost"); err == nil {
		t.Fatal("a cascade with nothing built is a leftover and goes")
	}
	// Nothing left to repair: a second pass does nothing.
	if err := svc.RepairCascades(ctx); err != nil || sni.calls != before+1 {
		t.Fatalf("%v %d", err, sni.calls)
	}
}
