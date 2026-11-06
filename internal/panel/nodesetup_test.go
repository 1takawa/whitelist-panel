package panel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/nodeinstall"
	"anypanel2/internal/xray"
)

// fakeInstaller stands in for SSH: it records what it was asked and returns canned results.
type fakeInstaller struct {
	mu          sync.Mutex
	report      *nodeinstall.Report
	checkErr    error
	installErr  error
	result      nodeinstall.Result
	opts        []nodeinstall.Options
	upgraded    []nodeinstall.Target
	gate        chan struct{} // when set, Install waits for it to be closed
	firewall    []fwCall      // OpenFirewall calls
	firewallFn  func()        // runs inside OpenFirewall (a test opens the "port" here)
	firewallErr error
	reality     []realityCall
	realityErr  error
	sni         map[string][]nodeinstall.SNISite // host -> the sites last synced there
	sniErr      error
	sniCalls    int
}

func (f *fakeInstaller) SyncSNI(_ context.Context, t nodeinstall.Target, sites []nodeinstall.SNISite, log func(string)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sniCalls++
	if f.sniErr != nil {
		log("nginx отклонил настройки")
		return f.sniErr
	}
	if f.sni == nil {
		f.sni = map[string][]nodeinstall.SNISite{}
	}
	f.sni[t.Host] = append([]nodeinstall.SNISite(nil), sites...)
	return nil
}

func (f *fakeInstaller) Check(_ context.Context, t nodeinstall.Target, _, _ int, _ string) (*nodeinstall.Report, error) {
	if f.checkErr != nil {
		return nil, f.checkErr
	}
	r := *f.report
	return &r, nil
}

func (f *fakeInstaller) Install(_ context.Context, o nodeinstall.Options, log func(string)) (*nodeinstall.Result, error) {
	f.mu.Lock()
	f.opts = append(f.opts, o)
	gate := f.gate
	f.mu.Unlock()
	log("загружаю агент")
	if gate != nil {
		<-gate
	}
	if f.installErr != nil {
		return nil, f.installErr
	}
	r := f.result
	return &r, nil
}

func (f *fakeInstaller) Upgrade(_ context.Context, t nodeinstall.Target, log func(string)) error {
	f.mu.Lock()
	f.upgraded = append(f.upgraded, t)
	f.mu.Unlock()
	log("обновляю")
	return nil
}

func waitJob(t *testing.T, svc *Service, id string) *JobView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := svc.NodeJob(id); ok && v.Status != "running" {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the job did not finish")
	return nil
}

func installEnv(t *testing.T) (*Service, *fakeInstaller, *fakeNode) {
	t.Helper()
	svc, _, _ := newService(t)
	fi := &fakeInstaller{
		report: &nodeinstall.Report{OK: true, HostKey: strings.Repeat("cd", 32), OS: "Ubuntu 24.04", Arch: "x86_64", Root: true},
		result: nodeinstall.Result{Fingerprint: strings.Repeat("ab", 32), HostKey: strings.Repeat("cd", 32), PrivateKey: "-----BEGIN KEY-----"},
	}
	node := &fakeNode{version: "0.8.0", fp: strings.Repeat("ab", 32)}
	svc.Installer = fi
	svc.NodeClient = func(model.Server) NodeAPI { return node }
	return svc, fi, node
}

func goodRequest() NodeInstallRequest {
	return NodeInstallRequest{
		SSHAccess:     SSHAccess{Host: "203.0.113.20", User: "root", Password: "s3cret-pass", HostKey: strings.Repeat("cd", 32)},
		Name:          "Frankfurt",
		Country:       "de",
		RealityDomain: "node.example.com",
		CreateInbound: true,
		AddClients:    true,
	}
}

func TestNodeInstallRegistersServerCreatesConnectionAndAttachesClients(t *testing.T) {
	ctx := context.Background()
	svc, fi, node := installEnv(t)
	local, _ := svc.CreateInbound(ctx, wsInbound(10001))
	alice, _ := svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true, InboundIDs: []string{local.ID}})
	bob, _ := svc.CreateClient(ctx, model.Client{Email: "bob", Enable: true})

	j, err := svc.StartNodeInstall(ctx, goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != "running" || j.ID == "" {
		t.Fatalf("%+v", j)
	}
	done := waitJob(t, svc, j.ID)
	if done.Status != "done" || done.Error != "" || done.ServerID == "" {
		t.Fatalf("%+v", done)
	}
	if raw, _ := json.Marshal(done); strings.Contains(string(raw), "s3cret-pass") {
		t.Fatalf("the SSH password must never appear in the job: %s", raw)
	}

	fi.mu.Lock()
	o := fi.opts[0]
	fi.mu.Unlock()
	if len(o.Token) != 48 || o.AgentPort != 8765 || o.CertHost != "203.0.113.20" || o.Password != "s3cret-pass" {
		t.Fatalf("%+v", o)
	}

	sv, err := svc.Store.GetServer(ctx, done.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Name != "Frankfurt" || sv.Country != "DE" || sv.Address != "203.0.113.20" || sv.Port != 8765 || !sv.Enable ||
		sv.Token != o.Token || sv.Fingerprint != strings.Repeat("ab", 32) || sv.SSHUser != "root" || sv.SSHPort != 22 ||
		sv.SSHHostKey != strings.Repeat("cd", 32) || sv.SSHKey != "-----BEGIN KEY-----" {
		t.Fatalf("%+v", sv)
	}
	list, _ := svc.ListServers(ctx)
	if len(list) != 1 || !list[0].Managed || list[0].Flag != "🇩🇪" || !list[0].Online || list[0].Inbounds != 1 {
		t.Fatalf("%+v", list)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "BEGIN KEY") || strings.Contains(string(raw), sv.Token) {
		t.Fatalf("neither the private key nor the token is exposed: %s", raw)
	}

	ins, _ := svc.ListInbounds(ctx)
	var remote *InboundView
	for i := range ins {
		if ins[i].ServerID == sv.ID {
			remote = &ins[i]
		}
	}
	if remote == nil || remote.Remark != "🇩🇪 Frankfurt" || remote.Port != 8443 || remote.Security != model.SecReality ||
		remote.Reality.Dest != "127.0.0.1:9443" || remote.Reality.ServerNames[0] != "node.example.com" || remote.Clients != 2 {
		t.Fatalf("%+v", remote)
	}
	cfg := node.lastConfig()
	for _, c := range []*model.Client{alice, bob} {
		if !strings.Contains(cfg, c.UUID) {
			t.Fatalf("%s is on the new node:\n%s", c.Email, cfg)
		}
	}
	if a, _ := svc.Store.GetClient(ctx, alice.ID); len(a.InboundIDs) != 2 {
		t.Fatalf("existing connections are kept: %+v", a)
	}
}

func TestNodeInstallWithoutConnectionOrClients(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := installEnv(t)
	req := goodRequest()
	req.CreateInbound, req.AddClients = false, false
	req.RealityDomain = ""
	j, err := svc.StartNodeInstall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitJob(t, svc, j.ID); done.Status != "done" {
		t.Fatalf("%+v", done)
	}
	ins, _ := svc.ListInbounds(ctx)
	if len(ins) != 0 {
		t.Fatalf("no connection was asked for: %+v", ins)
	}
}

func TestNodeInstallFailureLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	svc, fi, _ := installEnv(t)
	fi.installErr = errors.New("установщик завершился с ошибкой (код 1)")
	j, err := svc.StartNodeInstall(ctx, goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, svc, j.ID)
	if done.Status != "failed" || !strings.Contains(done.Error, "код 1") || done.ServerID != "" {
		t.Fatalf("%+v", done)
	}
	if list, _ := svc.ListServers(ctx); len(list) != 0 {
		t.Fatalf("a failed installation must not register a server: %+v", list)
	}
	// And the host can be tried again right away.
	fi.installErr = nil
	if _, err := svc.StartNodeInstall(ctx, goodRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestNodeInstallWhenTheAgentIsUnreachableStillSucceedsWithAWarning(t *testing.T) {
	ctx := context.Background()
	svc, _, node := installEnv(t)
	node.set(func(n *fakeNode) { n.down = true })
	j, err := svc.StartNodeInstall(ctx, goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, svc, j.ID)
	if done.Status != "done" || len(done.Warnings) == 0 || !strings.Contains(done.Warnings[0], "8765") {
		t.Fatalf("a closed firewall port is a warning, the node is installed: %+v", done)
	}
	list, _ := svc.ListServers(ctx)
	if len(list) != 1 || list[0].Online || list[0].Inbounds != 1 {
		t.Fatalf("%+v", list)
	}
}

func TestNodeInstallValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := installEnv(t)
	cases := map[string]func(*NodeInstallRequest){
		"host":           func(r *NodeInstallRequest) { r.Host = "bad host!" },
		"empty host":     func(r *NodeInstallRequest) { r.Host = "" },
		"password":       func(r *NodeInstallRequest) { r.Password = "" },
		"ssh port":       func(r *NodeInstallRequest) { r.SSHAccess.Port = 70000 },
		"user":           func(r *NodeInstallRequest) { r.User = "ro ot;" },
		"name":           func(r *NodeInstallRequest) { r.Name = "" },
		"country":        func(r *NodeInstallRequest) { r.Country = "Germany" },
		"reality domain": func(r *NodeInstallRequest) { r.RealityDomain = "not a domain" },
		"agent port":     func(r *NodeInstallRequest) { r.AgentPort = 99999 },
		"same ports":     func(r *NodeInstallRequest) { r.AgentPort, r.ConnectPort = 9000, 9000 },
		"stats port":     func(r *NodeInstallRequest) { r.ConnectPort = 62789 },
		"other port":     func(r *NodeInstallRequest) { r.ConnectPort = 8443 },
		"host key":       func(r *NodeInstallRequest) { r.HostKey = "xyz" },
	}
	for name, mod := range cases {
		r := goodRequest()
		mod(&r)
		if _, err := svc.StartNodeInstall(ctx, r); !isInvalid(err) {
			t.Errorf("%s must be rejected: %v", name, err)
		}
	}
	if list, _ := svc.ListServers(ctx); len(list) != 0 {
		t.Fatal("nothing was installed")
	}

	if _, _, err := svc.CreateServer(ctx, model.Server{Name: "taken", Address: "203.0.113.20"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartNodeInstall(ctx, goodRequest()); !isInvalid(err) {
		t.Fatalf("an address that is already registered: %v", err)
	}
}

func TestOnlyOneInstallationPerHostAtATime(t *testing.T) {
	ctx := context.Background()
	svc, fi, _ := installEnv(t)
	fi.gate = make(chan struct{})
	j, err := svc.StartNodeInstall(ctx, goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartNodeInstall(ctx, goodRequest()); !isInvalid(err) {
		t.Fatalf("a second installation on the same host while one runs: %v", err)
	}
	close(fi.gate)
	waitJob(t, svc, j.ID)
}

func TestSSHCheckPassesTheReportThrough(t *testing.T) {
	ctx := context.Background()
	svc, fi, _ := installEnv(t)
	rep, err := svc.SSHCheck(ctx, SSHAccess{Host: "203.0.113.20", Password: "x"}, 0, 0, "")
	if err != nil || !rep.OK || rep.HostKey == "" {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := svc.SSHCheck(ctx, SSHAccess{Host: "203.0.113.20"}, 0, 0, ""); !isInvalid(err) {
		t.Fatalf("the password is required: %v", err)
	}
	fi.checkErr = errors.New("неверный логин или пароль")
	if _, err := svc.SSHCheck(ctx, SSHAccess{Host: "203.0.113.20", Password: "x"}, 0, 0, ""); !isInvalid(err) || !strings.Contains(err.Error(), "неверный логин") {
		t.Fatalf("a failed login is reported to the admin as is: %v", err)
	}
}

func TestUpgradeUsesTheSavedKeyAndNeedsOne(t *testing.T) {
	ctx := context.Background()
	svc, fi, _ := installEnv(t)

	manual, _, _ := svc.CreateServer(ctx, model.Server{Name: "manual", Address: "m.example.net"})
	if _, err := svc.StartNodeUpgrade(ctx, manual.ID); !isInvalid(err) {
		t.Fatalf("a server added by hand has no SSH access: %v", err)
	}

	j, _ := svc.StartNodeInstall(ctx, goodRequest())
	done := waitJob(t, svc, j.ID)
	up, err := svc.StartNodeUpgrade(ctx, done.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	if fin := waitJob(t, svc, up.ID); fin.Status != "done" {
		t.Fatalf("%+v", fin)
	}
	fi.mu.Lock()
	defer fi.mu.Unlock()
	if len(fi.upgraded) != 1 {
		t.Fatalf("%+v", fi.upgraded)
	}
	got := fi.upgraded[0]
	if got.PrivateKey != "-----BEGIN KEY-----" || got.Password != "" || got.User != "root" || got.Port != 22 || got.HostKey != strings.Repeat("cd", 32) {
		t.Fatalf("upgrades log in with the key the panel created, not a password: %+v", got)
	}
}

func TestEditingAServerKeepsItsSSHAccess(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := installEnv(t)
	j, _ := svc.StartNodeInstall(ctx, goodRequest())
	done := waitJob(t, svc, j.ID)
	cur, _ := svc.Store.GetServer(ctx, done.ServerID)
	edit := *cur
	edit.Name, edit.Country = "Frankfurt 2", "nl"
	edit.SSHKey, edit.SSHUser = "tampered", "evil" // the API never decodes these, but the service must not trust the caller either
	if _, err := svc.UpdateServer(ctx, cur.ID, edit); err != nil {
		t.Fatal(err)
	}
	now, _ := svc.Store.GetServer(ctx, cur.ID)
	if now.Name != "Frankfurt 2" || now.Country != "NL" || now.SSHKey != cur.SSHKey || now.SSHUser != "root" || now.Token != cur.Token {
		t.Fatalf("%+v", now)
	}
}

func TestPerServerPingClientsAndTraffic(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	remote, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	local, _ := e.svc.CreateInbound(ctx, wsInbound(10001))
	_, _ = e.svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true, InboundIDs: []string{remote.ID}})
	_, _ = e.svc.CreateClient(ctx, model.Client{Email: "bob", Enable: true, InboundIDs: []string{remote.ID, local.ID}})
	_, _ = e.svc.CreateClient(ctx, model.Client{Email: "local-only", Enable: true, InboundIDs: []string{local.ID}})

	e.node.set(func(n *fakeNode) {
		n.stats = map[string]xray.Traffic{"alice": {Up: 10, Down: 90}, "bob": {Up: 1, Down: 2}}
	})
	if err := e.svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.ListServers(ctx)
	v := list[0]
	if v.Clients != 2 || v.OnlineClients != 2 || v.Up != 11 || v.Down != 92 || !v.Online {
		t.Fatalf("the server counts its own clients and traffic: %+v", v)
	}

	rows, err := e.svc.ServerClients(ctx, e.srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Email != "alice" || rows[0].Up != 10 || rows[0].Down != 90 || !rows[0].Online || rows[1].Email != "bob" {
		t.Fatalf("busiest first, only clients of this server: %+v", rows)
	}

	// Later nothing moves: they stop being online, the totals stay.
	*e.now = e.now.Add(OnlineWindow + time.Second)
	e.node.set(func(n *fakeNode) { n.stats = map[string]xray.Traffic{"bob": {Up: 0, Down: 0}} })
	_ = e.svc.PollTraffic(ctx)
	list, _ = e.svc.ListServers(ctx)
	if list[0].OnlineClients != 0 || list[0].Down != 92 {
		t.Fatalf("%+v", list[0])
	}
	if _, err := e.svc.ServerClients(ctx, "nope"); err == nil {
		t.Fatal("unknown server")
	}

	// Deleting the server forgets its numbers.
	_ = e.svc.DeleteInbound(ctx, remote.ID)
	if err := e.svc.DeleteServer(ctx, e.srv.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := e.svc.Store.ListServerTraffic(ctx, e.srv.ID); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}

func TestLinkNamesCanUseServerCountryAndFlag(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	sv := e.srv.Server
	sv.Country, sv.Name = "de", "Frankfurt"
	if _, err := e.svc.UpdateServer(ctx, sv.ID, sv); err != nil {
		t.Fatal(err)
	}
	set, _ := e.svc.GetSettings(ctx)
	set.Subscription.RemarkTemplate = "{{FLAG}} {{SERVER}} ({{COUNTRY}})-{{EMAIL}}"
	if _, err := e.svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	remote, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	local, _ := e.svc.CreateInbound(ctx, wsInbound(10001))
	c, _ := e.svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true, InboundIDs: []string{remote.ID, local.ID}})
	lv, err := e.svc.ClientLinks(ctx, c.ID)
	if err != nil || len(lv.Links) != 2 {
		t.Fatalf("%v %+v", err, lv)
	}
	names := map[string]bool{}
	for _, l := range lv.Links {
		names[l.Name] = true
	}
	if !names["🇩🇪 Frankfurt (DE)-alice"] {
		t.Fatalf("the server's name, country and flag are available to templates: %v", names)
	}
	if len(names) != 2 {
		t.Fatalf("a local inbound has no server tokens, so its name differs: %v", names)
	}
}

func TestSSHKeyCanReplaceThePassword(t *testing.T) {
	ctx := context.Background()
	svc, fi, _ := installEnv(t)
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"
	if _, err := svc.SSHCheck(ctx, SSHAccess{Host: "203.0.113.20", PrivateKey: key}, 0, 0, ""); err != nil {
		t.Fatalf("a key alone is enough: %v", err)
	}
	for name, k := range map[string]string{"not pem": "ssh-rsa AAAA", "huge": "-----BEGIN " + strings.Repeat("x", 17<<10)} {
		if _, err := svc.SSHCheck(ctx, SSHAccess{Host: "203.0.113.20", PrivateKey: k}, 0, 0, ""); !isInvalid(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	req := goodRequest()
	req.Password, req.PrivateKey = "", key
	j, err := svc.StartNodeInstall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, svc, j.ID)
	if done.Status != "done" {
		t.Fatalf("%+v", done)
	}
	fi.mu.Lock()
	defer fi.mu.Unlock()
	if fi.opts[0].PrivateKey != key || fi.opts[0].Password != "" {
		t.Fatalf("%+v", fi.opts[0])
	}
	if raw, _ := json.Marshal(done); strings.Contains(string(raw), "abc") {
		t.Fatalf("the key never appears in the job: %s", raw)
	}
}

func TestForcePortIsPassedOnlyWhenAConnectionIsCreated(t *testing.T) {
	ctx := context.Background()
	svc, fi, _ := installEnv(t)
	req := goodRequest()
	req.ForcePort = true
	j, err := svc.StartNodeInstall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, svc, j.ID)
	fi.mu.Lock()
	got := fi.opts[len(fi.opts)-1].FreePorts
	fi.mu.Unlock()
	if len(got) != 2 || got[0] != 80 || got[1] != 443 {
		t.Fatalf("nginx needs 80 (certificate) and 443 (clients), got %v", got)
	}

	req = goodRequest()
	req.Host = "203.0.113.21"
	req.ForcePort, req.CreateInbound = true, false
	j, err = svc.StartNodeInstall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, svc, j.ID)
	fi.mu.Lock()
	got = fi.opts[len(fi.opts)-1].FreePorts
	fi.mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("without a connection nothing needs the ports, got %v", got)
	}
}

type fwCall struct {
	target    nodeinstall.Target
	agentPort int
	ports     []int
}

func (f *fakeInstaller) OpenFirewall(_ context.Context, t nodeinstall.Target, agentPort int, ports []int, log func(string)) error {
	f.mu.Lock()
	f.firewall = append(f.firewall, fwCall{t, agentPort, ports})
	fn, err := f.firewallFn, f.firewallErr
	f.mu.Unlock()
	log("ufw: открываю порты")
	if fn != nil {
		fn()
	}
	return err
}

func TestInstallOpensTheFirewallByItselfWhenTheAgentIsUnreachable(t *testing.T) {
	ctx := context.Background()
	svc, fi, node := installEnv(t)
	node.set(func(n *fakeNode) { n.down = true })
	fi.firewallFn = func() { node.set(func(n *fakeNode) { n.down = false }) } // "ufw allow" makes the agent reachable
	j, err := svc.StartNodeInstall(ctx, goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, svc, j.ID)
	if done.Status != "done" || len(done.Warnings) != 0 {
		t.Fatalf("after opening the ports there is nothing to warn about: %+v", done)
	}
	if len(fi.firewall) != 1 || fi.firewall[0].agentPort != 8765 || len(fi.firewall[0].ports) != 2 || fi.firewall[0].ports[1] != 443 || fi.firewall[0].target.PrivateKey == "" {
		t.Fatalf("the panel logs in with its own key and opens the agent and connection ports: %+v", fi.firewall)
	}
	if list, _ := svc.ListServers(ctx); len(list) != 1 || !list[0].Online {
		t.Fatalf("%+v", list)
	}
}

func TestInstallDoesNotTouchTheFirewallWhenTheAgentAnswers(t *testing.T) {
	ctx := context.Background()
	svc, fi, _ := installEnv(t)
	j, _ := svc.StartNodeInstall(ctx, goodRequest())
	waitJob(t, svc, j.ID)
	if len(fi.firewall) != 0 {
		t.Fatalf("no firewall work for a reachable agent: %+v", fi.firewall)
	}
}

func TestOpenPortsActionForAManagedNode(t *testing.T) {
	ctx := context.Background()
	svc, fi, node := installEnv(t)
	j, _ := svc.StartNodeInstall(ctx, goodRequest())
	done := waitJob(t, svc, j.ID)

	node.set(func(n *fakeNode) { n.down = true })
	fi.firewallFn = func() { node.set(func(n *fakeNode) { n.down = false }) }
	fj, err := svc.StartNodeFirewall(ctx, done.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	fd := waitJob(t, svc, fj.ID)
	if fd.Status != "done" || fd.Kind != "firewall" || len(fd.Warnings) != 0 {
		t.Fatalf("%+v", fd)
	}
	last := fi.firewall[len(fi.firewall)-1]
	if last.agentPort != 8765 || len(last.ports) != 2 || last.ports[0] != 80 || last.ports[1] != 443 {
		t.Fatalf("the ports of the node's enabled connections are opened: %+v", last)
	}

	// A node added by hand has no SSH key: the panel cannot do it and says so.
	sv, _, err := svc.CreateServer(ctx, model.Server{Name: "manual", Address: "203.0.113.50", Port: 8765, Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartNodeFirewall(ctx, sv.ID); !isInvalid(err) {
		t.Fatalf("manual server: %v", err)
	}
	if _, err := svc.StartNodeFirewall(ctx, "nope"); err == nil {
		t.Fatal("unknown server")
	}
}

func (f *fakeInstaller) SetupReality(_ context.Context, t nodeinstall.Target, domain string, freePorts []int, log func(string)) error {
	f.mu.Lock()
	f.reality = append(f.reality, realityCall{t, domain, freePorts})
	err := f.realityErr
	f.mu.Unlock()
	log("nginx, сертификат, заглушка для " + domain)
	return err
}

type realityCall struct {
	target    nodeinstall.Target
	domain    string
	freePorts []int
}

// The node's connection must match what the nginx-SNI installer writes into its database.
func TestNodeRealityConnectionMatchesThreeXUIPro(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := installEnv(t)
	j, err := svc.StartNodeInstall(ctx, goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, svc, j.ID)
	if done.Status != "done" {
		t.Fatalf("%+v", done)
	}
	ins, _ := svc.Store.ListInbounds(ctx)
	var in model.Inbound
	for _, i := range ins {
		if i.ServerID == done.ServerID {
			in = i
		}
	}
	rs := model.Doc(in.StreamSettings.Sub("realitySettings"))
	if in.Port != 8443 || in.Listen != "127.0.0.1" || in.Protocol != model.ProtoVLESS || in.Security != model.SecReality || in.Network != model.NetTCP {
		t.Fatalf("Xray listens behind nginx on 8443: %+v", in)
	}
	if model.Str(rs, "target") != "127.0.0.1:9443" || in.Reality.Dest != "127.0.0.1:9443" {
		t.Fatalf("target is the node's own decoy site: %v", rs)
	}
	if names := model.Strs(rs, "serverNames"); len(names) != 1 || names[0] != "node.example.com" {
		t.Fatalf("server name is the node's domain: %v", names)
	}
	if got := model.Str(rs.Sub("settings"), "fingerprint"); got != "firefox" {
		t.Fatalf("uTLS fingerprint: %q", got)
	}
	if got := model.Str(rs.Sub("settings"), "spiderX"); got != "/" {
		t.Fatalf("spiderX: %q", got)
	}
	ids := model.Strs(rs, "shortIds")
	if len(ids) != 8 || len(in.Reality.ShortIDs) != 8 {
		t.Fatalf("eight short ids: %v", ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if len(id) != 16 || seen[id] {
			t.Fatalf("short ids are 16 hex characters and differ: %v", ids)
		}
		seen[id] = true
	}
	if !in.AcceptProxyProtocol || !model.Bool(in.StreamSettings.Sub("sockopt"), "acceptProxyProtocol") {
		t.Fatalf("the PROXY protocol from nginx is accepted: %+v", in.StreamSettings)
	}
	if in.Reality.PrivateKey == "" || in.Reality.PublicKey == "" || model.Str(rs.Sub("settings"), "publicKey") == "" {
		t.Fatal("a key pair is generated")
	}
	if in.Sniffing.Enabled { // the panel keeps no destOverride list for a disabled sniffer; Xray ignores it then anyway
		t.Fatalf("sniffing: %+v", in.Sniffing)
	}
	if in.Public.Port != 443 {
		t.Fatalf("clients connect to nginx on 443: %+v", in.Public)
	}
}

func TestRealityActionReplacesTheOldConnectionAndMovesItsClients(t *testing.T) {
	ctx := context.Background()
	svc, fi, node := installEnv(t)
	req := goodRequest()
	j, _ := svc.StartNodeInstall(ctx, req)
	done := waitJob(t, svc, j.ID)

	// An old-style connection straight on 443 (what the first installer made), with one client on it.
	old := model.Inbound{Remark: "old", Enable: true, Protocol: model.ProtoVLESS, Listen: "0.0.0.0", Port: 443, ServerID: done.ServerID,
		Network: model.NetTCP, Security: model.SecReality, Reality: model.Reality{Dest: "www.example.com:443", ServerNames: []string{"www.example.com"}}}
	oldIn, err := svc.CreateInbound(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	cl, _ := svc.CreateClient(ctx, model.Client{Email: "carol", Enable: true, InboundIDs: []string{oldIn.ID}})
	other, _ := svc.CreateClient(ctx, model.Client{Email: "dave", Enable: true})
	_ = node

	if _, err := svc.StartNodeReality(ctx, done.ServerID, NodeRealityRequest{Domain: "not a domain"}); !isInvalid(err) {
		t.Fatalf("domain: %v", err)
	}
	rj, err := svc.StartNodeReality(ctx, done.ServerID, NodeRealityRequest{Domain: "Node2.Example.com", ForcePort: true})
	if err != nil {
		t.Fatal(err)
	}
	rd := waitJob(t, svc, rj.ID)
	if rd.Status != "done" || rd.Kind != "reality" {
		t.Fatalf("%+v", rd)
	}
	fi.mu.Lock()
	call := fi.reality[len(fi.reality)-1]
	fi.mu.Unlock()
	if call.domain != "node2.example.com" || len(call.freePorts) != 2 || call.target.PrivateKey == "" {
		t.Fatalf("the panel logs in with its key, asks for ports 80 and 443 to be freed: %+v", call)
	}
	if _, err := svc.Store.GetInbound(ctx, oldIn.ID); err == nil {
		t.Fatal("the old connection on 443 is gone: it would hold the port nginx needs")
	}
	ins, _ := svc.Store.ListInbounds(ctx)
	var fresh []model.Inbound
	for _, i := range ins {
		if i.ServerID == done.ServerID && i.Port == 8443 && model.Strs(i.StreamSettings.Sub("realitySettings"), "serverNames")[0] == "node2.example.com" {
			fresh = append(fresh, i)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("one new connection for the new domain: %+v", ins)
	}
	c, _ := svc.Store.GetClient(ctx, cl.ID)
	d, _ := svc.Store.GetClient(ctx, other.ID)
	if !c.Uses(fresh[0].ID) {
		t.Fatalf("the clients of the old connection move to the new one: %+v", c)
	}
	if !d.Uses(fresh[0].ID) {
		t.Fatalf("like any new connection it goes to every client, not only those of the old one: %+v", d)
	}

	// A manually added server has no SSH key: the panel cannot do this.
	sv, _, _ := svc.CreateServer(ctx, model.Server{Name: "manual", Address: "203.0.113.60", Port: 8765, Enable: true})
	if _, err := svc.StartNodeReality(ctx, sv.ID, NodeRealityRequest{Domain: "a.example.com"}); !isInvalid(err) {
		t.Fatalf("no SSH access: %v", err)
	}
}

// The command-line actions run a Service that has no local Xray; changing connections there must not panic.
func TestServiceWithoutLocalXrayCanChangeConnections(t *testing.T) {
	ctx := context.Background()
	svc, _, st := newService(t)
	cli := &Service{Store: st, Now: svc.Now, Log: svc.Log}
	in, err := cli.CreateInbound(ctx, wsInbound(10101))
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.DeleteInbound(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNodeInstallPassesTheSkippedDNSCheckOnAndGivesTheRecordTimeToAppear(t *testing.T) {
	ctx := context.Background()
	svc, fi, _ := installEnv(t)
	req := goodRequest()
	req.SkipDNSCheck = true
	j, err := svc.StartNodeInstall(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitJob(t, svc, j.ID); done.Status != "done" {
		t.Fatalf("%+v", done)
	}
	fi.mu.Lock()
	defer fi.mu.Unlock()
	if len(fi.opts) != 1 || !fi.opts[0].SkipDNSCheck {
		t.Fatalf("the installer is told to wait for the record: %+v", fi.opts)
	}
}
