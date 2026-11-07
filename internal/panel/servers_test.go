package panel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"anypanel2/internal/agent"
	"anypanel2/internal/model"
	"anypanel2/internal/store"
	"anypanel2/internal/xray"
)

// fakeNode is one agent: it records configs and serves counters, and can be switched off.
type fakeNode struct {
	mu      sync.Mutex
	down    bool
	badCfg  error
	applied [][]byte
	stats   map[string]xray.Traffic
	fp      string
	version string
}

func (n *fakeNode) Status(context.Context) (agent.Status, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.down {
		return agent.Status{}, errors.New("connection refused")
	}
	return agent.Status{Version: n.version, Xray: xray.Status{Installed: true, Running: true, Version: "26.1.1"}}, nil
}
func (n *fakeNode) Check(context.Context, []byte) error { return nil }
func (n *fakeNode) Apply(_ context.Context, cfg []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.down {
		return errors.New("connection refused")
	}
	if n.badCfg != nil {
		return n.badCfg
	}
	n.applied = append(n.applied, cfg)
	return nil
}
func (n *fakeNode) Stats(_ context.Context, reset bool) (map[string]xray.Traffic, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.down {
		return nil, errors.New("connection refused")
	}
	out := n.stats
	if reset {
		n.stats = nil
	}
	return out, nil
}
func (n *fakeNode) Restart(context.Context) error { return nil }
func (n *fakeNode) ObservedFingerprint() string   { return n.fp }
func (n *fakeNode) lastConfig() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.applied) == 0 {
		return ""
	}
	return string(n.applied[len(n.applied)-1])
}
func (n *fakeNode) applies() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.applied)
}
func (n *fakeNode) set(f func(*fakeNode)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	f(n)
}

type serversEnv struct {
	svc  *Service
	fx   *fakeXray
	node *fakeNode
	srv  *ServerView
	now  *time.Time
}

func newServersEnv(t *testing.T) serversEnv {
	t.Helper()
	svc, fx, _ := newService(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	node := &fakeNode{version: "0.7.0", fp: strings.Repeat("ab", 32)}
	svc.NodeClient = func(model.Server) NodeAPI { return node }
	ctx := context.Background()
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain, set.RealityDomain = "panel.example.com", "reality.example.com"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	sv, _, err := svc.CreateServer(ctx, model.Server{Name: "Frankfurt", Address: "de1.example.net"})
	if err != nil {
		t.Fatal(err)
	}
	return serversEnv{svc: svc, fx: fx, node: node, srv: sv, now: &now}
}

func nodeInbound(serverID string, port int) model.Inbound {
	in := realityInbound()
	in.Port, in.Listen, in.ServerID = port, "0.0.0.0", serverID
	in.Reality.Dest, in.Reality.ServerNames = "www.microsoft.com:443", []string{"www.microsoft.com"}
	in.Public = model.Public{}
	return in
}

func TestServerRegistration(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)

	sv, token, err := svc.CreateServer(ctx, model.Server{Name: "  Berlin ", Address: "DE2.Example.net"})
	if err != nil {
		t.Fatal(err)
	}
	if sv.Name != "Berlin" || sv.Address != "de2.example.net" || sv.Port != agent.DefaultPort || !sv.Enable || len(token) != 48 {
		t.Fatalf("%+v %q", sv, token)
	}
	raw, _ := json.Marshal(sv)
	if strings.Contains(string(raw), token) || strings.Contains(strings.ToLower(string(raw)), "token") {
		t.Fatalf("the token is never part of the API view: %s", raw)
	}
	list, _ := svc.ListServers(ctx)
	if raw, _ := json.Marshal(list); strings.Contains(string(raw), token) {
		t.Fatal("not in the list either")
	}
	if _, _, err := svc.CreateServer(ctx, model.Server{Name: "dup", Address: "de2.example.net"}); !isInvalid(err) {
		t.Fatalf("same address and port twice: %v", err)
	}
	if _, _, err := svc.CreateServer(ctx, model.Server{Name: "other port", Address: "de2.example.net", Port: 9000}); err != nil {
		t.Fatalf("another agent port on the same address is a different server: %v", err)
	}
	for name, in := range map[string]model.Server{
		"name":        {Name: "", Address: "h.example.net"},
		"address":     {Name: "x", Address: "not a host!"},
		"empty addr":  {Name: "x"},
		"port":        {Name: "x", Address: "h.example.net", Port: 70000},
		"fingerprint": {Name: "x", Address: "h.example.net", Fingerprint: "xyz"},
	} {
		if _, _, err := svc.CreateServer(ctx, in); !isInvalid(err) {
			t.Errorf("%s must be rejected: %v", name, err)
		}
	}
	// Fingerprints are accepted in the colon-separated form tools print them in.
	fp := strings.Repeat("AB:", 31) + "AB"
	if v, _, err := svc.CreateServer(ctx, model.Server{Name: "pinned", Address: "h2.example.net", Fingerprint: fp}); err != nil || v.Fingerprint != strings.Repeat("ab", 32) {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestInboundOnAServerGoesToThatServerOnly(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	local, err := e.svc.CreateInbound(ctx, wsInbound(10001))
	if err != nil {
		t.Fatal(err)
	}
	remote, err := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	if err != nil {
		t.Fatal(err)
	}
	if remote.ServerID != e.srv.ID {
		t.Fatalf("%+v", remote)
	}

	localCfg, nodeCfg := e.fx.last(), e.node.lastConfig()
	if !strings.Contains(localCfg, `"in-`+local.ID+`"`) || strings.Contains(localCfg, `"in-`+remote.ID+`"`) {
		t.Fatalf("the local Xray serves only local inbounds:\n%s", localCfg)
	}
	if !strings.Contains(nodeCfg, `"in-`+remote.ID+`"`) || strings.Contains(nodeCfg, `"in-`+local.ID+`"`) {
		t.Fatalf("the server gets only its own inbounds:\n%s", nodeCfg)
	}
	if !strings.Contains(nodeCfg, `"port": 62789`) {
		t.Fatalf("a server's stats API uses the fixed agent port:\n%s", nodeCfg)
	}
}

func TestSamePortOnDifferentMachines(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	// 443 is not reserved locally here, so the same port can be used on this machine and on the server.
	if _, err := e.svc.CreateInbound(ctx, nodeInbound("", 443)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443)); err != nil {
		t.Fatalf("the same port on another machine is fine: %v", err)
	}
	if _, err := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443)); !isInvalid(err) {
		t.Fatalf("but not twice on the same machine: %v", err)
	}
	// The agent's own ports are reserved on its machine.
	if _, err := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, agent.DefaultPort)); !isInvalid(err) {
		t.Fatalf("agent port: %v", err)
	}
	if _, err := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, agent.APIPort)); !isInvalid(err) {
		t.Fatalf("stats API port: %v", err)
	}
	if _, err := e.svc.CreateInbound(ctx, nodeInbound("nope", 8443)); !isInvalid(err) {
		t.Fatalf("unknown server: %v", err)
	}
}

func TestLinksUseTheServersAddress(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	in, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	c, err := e.svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true, InboundIDs: []string{in.ID}})
	if err != nil {
		t.Fatal(err)
	}
	lv, err := e.svc.ClientLinks(ctx, c.ID)
	if err != nil || len(lv.Links) != 1 {
		t.Fatalf("%v %+v", err, lv)
	}
	if l := lv.Links[0].Link; !strings.Contains(l, "@de1.example.net:443?") || strings.Contains(l, "reality.example.com") {
		t.Fatalf("the client connects to the server directly: %s", l)
	}
	if !strings.Contains(e.node.lastConfig(), c.UUID) {
		t.Fatal("the client is in the server's config")
	}

	// An explicit public host on the inbound wins.
	upd := nodeInbound(e.srv.ID, 443)
	upd.Public.Host = "cdn.example.org"
	_, _ = e.svc.UpdateInbound(ctx, in.ID, upd)
	lv, _ = e.svc.ClientLinks(ctx, c.ID)
	if !strings.Contains(lv.Links[0].Link, "@cdn.example.org:443?") {
		t.Fatalf("%s", lv.Links[0].Link)
	}

	// A disabled server's links are not handed out.
	e.srv.Enable = false
	if _, err := e.svc.UpdateServer(ctx, e.srv.ID, e.srv.Server); err != nil {
		t.Fatal(err)
	}
	sb, _ := e.svc.PublicSubscription(ctx, c.SubID)
	if len(sb.Links) != 0 {
		t.Fatalf("a disabled server is left out of subscriptions: %+v", sb.Links)
	}
}

func TestOfflineServerDoesNotBlockChangesAndCatchesUp(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	in, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	before := e.node.applies()

	e.node.set(func(n *fakeNode) { n.down = true })
	*e.now = e.now.Add(time.Minute)
	c, err := e.svc.CreateClient(ctx, model.Client{Email: "bob", Enable: true, InboundIDs: []string{in.ID}})
	if err != nil {
		t.Fatalf("an unreachable server must not make the change fail: %v", err)
	}
	list, _ := e.svc.ListServers(ctx)
	if list[0].Online || list[0].Error == "" || list[0].Inbounds != 1 {
		t.Fatalf("it is shown as offline with the reason: %+v", list[0])
	}

	// While it is down and inside the retry pause no more attempts are made.
	if _, err := e.svc.CreateClient(ctx, model.Client{Email: "carol", Enable: true, InboundIDs: []string{in.ID}}); err != nil {
		t.Fatal(err)
	}
	if e.node.applies() != before {
		t.Fatal("nothing can be applied to a machine that is down")
	}

	// It comes back: the next sync after the pause delivers everything it missed.
	e.node.set(func(n *fakeNode) { n.down = false })
	*e.now = e.now.Add(serverRetry + time.Second)
	if err := e.svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := e.node.lastConfig()
	if !strings.Contains(cfg, c.UUID) || !strings.Contains(cfg, "carol") {
		t.Fatalf("the returning server gets the current config:\n%s", cfg)
	}
	list, _ = e.svc.ListServers(ctx)
	if !list[0].Online || list[0].Error != "" || list[0].AgentVersion != "0.7.0" || !list[0].Xray.Running || list[0].LastSeen == nil {
		t.Fatalf("%+v", list[0])
	}
}

func TestRejectedConfigIsReportedOnTheServer(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	e.node.set(func(n *fakeNode) { n.badCfg = errors.New("xray rejected the configuration: boom") })
	if _, err := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443)); err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.ListServers(ctx)
	if !strings.Contains(list[0].Error, "boom") {
		t.Fatalf("%+v", list[0])
	}
}

func TestTrafficFromServersAddsUp(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	local, _ := e.svc.CreateInbound(ctx, wsInbound(10001))
	remote, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	c, _ := e.svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true, InboundIDs: []string{local.ID, remote.ID}, TotalBytes: 1000})
	other, _ := e.svc.CreateClient(ctx, model.Client{Email: "only-remote", Enable: true, InboundIDs: []string{remote.ID}})

	e.fx.stats = map[string]xray.Traffic{"alice": {Up: 10, Down: 20}}
	e.node.set(func(n *fakeNode) {
		n.stats = map[string]xray.Traffic{"alice": {Up: 1, Down: 2}, "only-remote": {Up: 5, Down: 5}}
	})
	if err := e.svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := e.svc.GetClient(ctx, c.ID)
	o, _ := e.svc.GetClient(ctx, other.ID)
	if a.Up != 11 || a.Down != 22 || o.Up != 5 || o.Down != 5 {
		t.Fatalf("a client's use on every machine is summed: %+v %+v", a, o)
	}
	if a.LastActiveAt == nil || o.LastActiveAt == nil {
		t.Fatal("clients that only used the server are online too")
	}
	e.fx.stats = nil
	if err := e.svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.svc.GetClient(ctx, c.ID); again.Up != 11 {
		t.Fatal("the agent's counters were reset: nothing is counted twice")
	}

	// An offline server loses nothing: its counters keep growing on the machine until the panel can read them.
	e.node.set(func(n *fakeNode) { n.down = true; n.stats = map[string]xray.Traffic{"alice": {Up: 100, Down: 100}} })
	*e.now = e.now.Add(time.Minute)
	_ = e.svc.PollTraffic(ctx)
	if v, _ := e.svc.GetClient(ctx, c.ID); v.Up != 11 {
		t.Fatalf("%+v", v)
	}
	e.node.set(func(n *fakeNode) { n.down = false })
	*e.now = e.now.Add(serverRetry + time.Second)
	_ = e.svc.PollTraffic(ctx)
	if v, _ := e.svc.GetClient(ctx, c.ID); v.Up != 111 || v.Down != 122 {
		t.Fatalf("what accumulated while it was unreachable is collected afterwards: %+v", v)
	}
}

func TestExhaustedClientIsRemovedFromServersToo(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	remote, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	c, _ := e.svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true, InboundIDs: []string{remote.ID}, TotalBytes: 100})
	if !strings.Contains(e.node.lastConfig(), c.UUID) {
		t.Fatal("setup")
	}
	e.node.set(func(n *fakeNode) { n.stats = map[string]xray.Traffic{"alice": {Up: 60, Down: 60}} })
	_ = e.svc.PollTraffic(ctx)
	if strings.Contains(e.node.lastConfig(), c.UUID) {
		t.Fatalf("traffic used on the server counts against the quota, and the client is cut off there as well:\n%s", e.node.lastConfig())
	}
}

func TestDeleteServerRules(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	in, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	if err := e.svc.DeleteServer(ctx, e.srv.ID); !isInvalid(err) {
		t.Fatalf("a server that still runs inbounds cannot be deleted: %v", err)
	}
	if err := e.svc.DeleteInbound(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteServer(ctx, e.srv.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.ListServers(ctx); len(list) != 0 {
		t.Fatalf("%+v", list)
	}
}

func TestFirstContactPinsTheCertificate(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	got, err := e.svc.TestServer(ctx, e.srv.ID)
	if err != nil || !got.Online {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Fingerprint != strings.Repeat("ab", 32) {
		t.Fatalf("the certificate seen on first contact is remembered: %q", got.Fingerprint)
	}
	// A new token drops the connection state; the pin stays.
	tok, err := e.svc.RegenerateServerToken(ctx, e.srv.ID)
	if err != nil || len(tok) != 48 {
		t.Fatal(err)
	}
	if sv, _ := e.svc.Store.GetServer(ctx, e.srv.ID); sv.Token != tok || sv.Fingerprint == "" {
		t.Fatalf("%+v", sv)
	}
}

func TestServerOfflineAndBackNotifications(t *testing.T) {
	ctx := context.Background()
	svc, _, fake := tgSetup(t)
	enableBot(t, svc)
	node := &fakeNode{version: "0.7.0"}
	svc.NodeClient = func(model.Server) NodeAPI { return node }
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	if _, _, err := svc.CreateServer(ctx, model.Server{Name: "Frankfurt", Address: "de1.example.net"}); err != nil {
		t.Fatal(err)
	}

	_ = svc.PollTraffic(ctx) // first look: online, silent
	svc.WaitNotifications()
	if n := len(fake.texts()); n != 0 {
		t.Fatalf("%v", fake.texts())
	}
	node.set(func(n *fakeNode) { n.down = true })
	now = now.Add(time.Minute)
	_ = svc.PollTraffic(ctx)
	svc.WaitNotifications()
	if got := fake.texts(); len(got) != 1 || !strings.Contains(got[0], "Frankfurt") || !strings.Contains(got[0], "недоступен") {
		t.Fatalf("%v", got)
	}
	node.set(func(n *fakeNode) { n.down = false })
	now = now.Add(serverRetry + time.Second)
	_ = svc.PollTraffic(ctx)
	svc.WaitNotifications()
	if got := fake.texts(); len(got) != 2 || !strings.Contains(got[1], "снова на связи") {
		t.Fatalf("%v", got)
	}
}

func TestServerOnlineCountsClientsNotInboundCounters(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	remote, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	_, _ = e.svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true, InboundIDs: []string{remote.ID}})

	// The agent returns the inbound's own counter next to the client's; only the client is a client.
	e.node.set(func(n *fakeNode) {
		n.stats = map[string]xray.Traffic{
			"alice": {Up: 1, Down: 2},
			xray.InboundStatPrefix + xray.InboundTag(remote.ID): {Up: 1, Down: 2},
		}
	})
	if err := e.svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ := e.svc.ListServers(ctx)
	if list[0].OnlineClients != 1 || list[0].Up != 1 || list[0].Down != 2 {
		t.Fatalf("one client online, its traffic counted once: %+v", list[0])
	}
	in, _ := e.svc.Store.GetInbound(ctx, remote.ID)
	if in.Up != 1 || in.Down != 2 {
		t.Fatalf("the inbound counter of a server is accounted to the inbound: %+v", in)
	}
}

func TestServerCountryIsFoundFromItsAddress(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	sv, _ := e.svc.Store.GetServer(ctx, e.srv.ID)
	sv.Country = ""
	if err := e.svc.Store.PutServer(ctx, *sv); err != nil {
		t.Fatal(err)
	}
	asked := make(chan string, 4)
	e.svc.GeoCountry = func(_ context.Context, host string) (string, error) {
		asked <- host
		return "NL", nil
	}
	if err := e.svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		cur, _ := e.svc.Store.GetServer(ctx, e.srv.ID)
		if cur.Country == "NL" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the country was not stored: %+v", cur)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if host := <-asked; host != sv.Address {
		t.Fatalf("the server's address is what is looked up: %q", host)
	}
	// A country somebody set is never replaced.
	cur, _ := e.svc.Store.GetServer(ctx, e.srv.ID)
	cur.Country = "DE"
	_ = e.svc.Store.PutServer(ctx, *cur)
	*e.now = e.now.Add(2 * time.Hour)
	_ = e.svc.PollTraffic(ctx)
	time.Sleep(100 * time.Millisecond)
	if again, _ := e.svc.Store.GetServer(ctx, e.srv.ID); again.Country != "DE" {
		t.Fatalf("a chosen country stays: %+v", again)
	}
}

func TestAClientIsOnlineOnceOnTheServerItWasLastSeenOn(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	remote, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	local, _ := e.svc.CreateInbound(ctx, wsInbound(10001))
	for _, name := range []string{"alice", "bob", "carol"} {
		_, _ = e.svc.CreateClient(ctx, model.Client{Email: name, Enable: true, InboundIDs: []string{remote.ID, local.ID}})
	}
	now := *e.now
	moved := map[string]store.Traffic{"alice": {Up: 1}, "bob": {Up: 1}, "carol": {Up: 1}}
	// All three moved traffic on the node a minute ago; alice and bob then moved to the main server.
	_ = e.svc.Store.AddServerTraffic(ctx, e.srv.ID, moved, now.Add(-60*time.Second))
	_ = e.svc.Store.AddServerTraffic(ctx, LocalServerID, map[string]store.Traffic{"alice": {Up: 1}, "bob": {Up: 1}}, now.Add(-10*time.Second))

	list, _ := e.svc.ListServers(ctx)
	if list[0].OnlineClients != 1 {
		t.Fatalf("only carol is still seen on the node: %d", list[0].OnlineClients)
	}
	d, err := e.svc.DashboardNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Clients.OnlineHere != 2 {
		t.Fatalf("alice and bob are online on the main server: %d", d.Clients.OnlineHere)
	}
	if list[0].OnlineClients+d.Clients.OnlineHere != 3 {
		t.Fatal("the servers' counts add up to the three clients online")
	}
	rows, _ := e.svc.ServerClients(ctx, e.srv.ID)
	online := map[string]bool{}
	for _, r := range rows {
		online[r.Email] = r.Online
	}
	if online["alice"] || online["bob"] || !online["carol"] {
		t.Fatalf("in the node's list a client that moved on is not online: %v", online)
	}

	// Later the node sees alice again: she is on the node now and no longer on the main server.
	_ = e.svc.Store.AddServerTraffic(ctx, e.srv.ID, map[string]store.Traffic{"alice": {Up: 1}}, now)
	list, _ = e.svc.ListServers(ctx)
	d, _ = e.svc.DashboardNow(ctx)
	if list[0].OnlineClients != 2 || d.Clients.OnlineHere != 1 {
		t.Fatalf("alice moved back: node %d, main %d", list[0].OnlineClients, d.Clients.OnlineHere)
	}

	// Past the window nobody is online anywhere.
	*e.now = now.Add(OnlineWindow + time.Minute)
	list, _ = e.svc.ListServers(ctx)
	d, _ = e.svc.DashboardNow(ctx)
	if list[0].OnlineClients != 0 || d.Clients.OnlineHere != 0 {
		t.Fatalf("%d %d", list[0].OnlineClients, d.Clients.OnlineHere)
	}
}

func TestClientListSaysWhichServerEachClientWasLastSeenOn(t *testing.T) {
	ctx := context.Background()
	e := newServersEnv(t)
	remote, _ := e.svc.CreateInbound(ctx, nodeInbound(e.srv.ID, 443))
	for _, name := range []string{"alice", "bob", "never"} {
		_, _ = e.svc.CreateClient(ctx, model.Client{Email: name, Enable: true, InboundIDs: []string{remote.ID}})
	}
	now := *e.now
	_ = e.svc.Store.AddServerTraffic(ctx, e.srv.ID, map[string]store.Traffic{"alice": {Up: 1}, "bob": {Up: 1}}, now.Add(-3*time.Hour))
	_ = e.svc.Store.AddServerTraffic(ctx, LocalServerID, map[string]store.Traffic{"alice": {Up: 1}}, now.Add(-2*time.Hour))

	list, err := e.svc.ListClients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*model.ClientNode{}
	for _, c := range list {
		got[c.Email] = c.LastNode
	}
	if got["alice"] == nil || got["alice"].ID != LocalServerID || got["alice"].Name != MainServerName {
		t.Fatalf("alice moved to the main server later: %+v", got["alice"])
	}
	if got["bob"] == nil || got["bob"].ID != e.srv.ID || got["bob"].Name == "" {
		t.Fatalf("bob was only on the node, however long ago: %+v", got["bob"])
	}
	if got["never"] != nil {
		t.Fatalf("a client that never connected has no node: %+v", got["never"])
	}
}
