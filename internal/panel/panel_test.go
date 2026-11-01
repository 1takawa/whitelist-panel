package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/store"
	"anypanel2/internal/store/memstore"
	"anypanel2/internal/xray"
)

// fakeXray records applied configs and lets tests inject failures and traffic.
type fakeXray struct {
	mu       sync.Mutex
	notReady bool
	running  bool
	checkErr error
	applied  [][]byte
	stats    map[string]xray.Traffic
}

func (f *fakeXray) Installed() bool { return !f.notReady }
func (f *fakeXray) Check(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkErr
}
func (f *fakeXray) Apply(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, cfg)
	f.running = true
	return nil
}
func (f *fakeXray) Restart(context.Context) error { return nil }
func (f *fakeXray) Status(context.Context) xray.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return xray.Status{Installed: !f.notReady, Running: f.running}
}
func (f *fakeXray) Logs(int) []string { return nil }
func (f *fakeXray) QueryStats(context.Context, bool) (map[string]xray.Traffic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.stats
	f.stats = nil
	return out, nil
}
func (f *fakeXray) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.applied) == 0 {
		return ""
	}
	return string(f.applied[len(f.applied)-1])
}
func (f *fakeXray) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applied)
}

func newService(t *testing.T) (*Service, *fakeXray, *memstore.Store) {
	t.Helper()
	st := memstore.New()
	fx := &fakeXray{}
	svc := New(st, fx, xray.Options{}, []int{28270, 62789}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, fx, st
}

func realityInbound() model.Inbound {
	return model.Inbound{
		Remark: "DE reality", Enable: true, Protocol: model.ProtoVLESS, Listen: "127.0.0.1", Port: 8443,
		Network: model.NetTCP, Security: model.SecReality, AcceptProxyProtocol: true,
		Reality: model.Reality{Dest: "yandex.ru:443", ServerNames: []string{"yandex.ru"}},
		Public:  model.Public{Port: 443},
	}
}

func wsInbound(port int) model.Inbound {
	return model.Inbound{
		Remark: "ws", Enable: true, Protocol: model.ProtoTrojan, Listen: "127.0.0.1", Port: port,
		Network: model.NetWS, Security: model.SecNone, WS: model.PathHost{Path: "/ws"},
		Public: model.Public{Port: 443, Security: model.SecTLS},
	}
}

func isInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

func TestCreateRealityInboundGeneratesKeysAndApplies(t *testing.T) {
	svc, fx, _ := newService(t)
	ctx := context.Background()

	in, err := svc.CreateInbound(ctx, realityInbound())
	if err != nil {
		t.Fatal(err)
	}
	if in.ID == "" || in.Reality.PrivateKey == "" || in.Reality.PublicKey == "" || len(in.Reality.ShortIDs) != 1 {
		t.Fatalf("keys and short id must be generated: %+v", in.Reality)
	}
	if pub, _ := xray.PublicFromPrivate(in.Reality.PrivateKey); pub != in.Reality.PublicKey {
		t.Fatal("public key must be derived from the private key")
	}
	if !strings.Contains(fx.last(), `"tag": "in-`+in.ID+`"`) || !strings.Contains(fx.last(), "acceptProxyProtocol") {
		t.Fatalf("inbound must reach the xray config:\n%s", fx.last())
	}
}

func TestPublicKeyIsNeverTrustedFromInput(t *testing.T) {
	svc, _, _ := newService(t)
	in := realityInbound()
	priv, _, _ := xray.GenerateRealityKeys()
	in.Reality.PrivateKey, in.Reality.PublicKey = priv, "FORGED"
	got, err := svc.CreateInbound(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reality.PublicKey == "FORGED" {
		t.Fatal("public key must be re-derived")
	}
}

func TestInboundValidation(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	mutate := func(f func(*model.Inbound)) model.Inbound { in := realityInbound(); f(&in); return in }
	bad := map[string]model.Inbound{
		"empty remark":       mutate(func(i *model.Inbound) { i.Remark = "  " }),
		"bad protocol":       mutate(func(i *model.Inbound) { i.Protocol = "socks" }),
		"bad network":        mutate(func(i *model.Inbound) { i.Network = "kcp" }),
		"port zero":          mutate(func(i *model.Inbound) { i.Port = 0 }),
		"reserved port":      mutate(func(i *model.Inbound) { i.Port = 28270 }),
		"bad listen":         mutate(func(i *model.Inbound) { i.Listen = "not-an-ip" }),
		"reality on trojan":  mutate(func(i *model.Inbound) { i.Protocol = model.ProtoTrojan }),
		"reality on ws":      mutate(func(i *model.Inbound) { i.Network = model.NetWS }),
		"reality dest":       mutate(func(i *model.Inbound) { i.Reality.Dest = "yandex.ru" }),
		"reality no sni":     mutate(func(i *model.Inbound) { i.Reality.ServerNames = nil }),
		"bad private key":    mutate(func(i *model.Inbound) { i.Reality.PrivateKey = "short" }),
		"bad short id":       mutate(func(i *model.Inbound) { i.Reality.ShortIDs = []string{"xyz"} }),
		"tls without certs":  mutate(func(i *model.Inbound) { i.Protocol, i.Security = model.ProtoTrojan, model.SecTLS }),
		"grpc without name":  mutate(func(i *model.Inbound) { i.Network, i.Security = model.NetGRPC, model.SecNone }),
		"public tls+reality": mutate(func(i *model.Inbound) { i.Public.Security = model.SecTLS }),
		"bad public host":    mutate(func(i *model.Inbound) { i.Public.Host = "bad host!" }),
		"bad sniffing":       mutate(func(i *model.Inbound) { i.Sniffing = model.Sniffing{Enabled: true, DestOverride: []string{"ftp"}} }),
		"bad xhttp mode":     mutate(func(i *model.Inbound) { i.Network, i.Security, i.XHTTP.Mode = model.NetXHTTP, model.SecNone, "nope" }),
	}
	for name, in := range bad {
		if _, err := svc.CreateInbound(ctx, in); !isInvalid(err) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	if list, _ := svc.ListInbounds(ctx); len(list) != 0 {
		t.Fatalf("nothing may be saved after failed validation, got %d", len(list))
	}
}

func TestPortConflict(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.CreateInbound(ctx, realityInbound()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateInbound(ctx, wsInbound(8443)); !isInvalid(err) {
		t.Fatalf("duplicate port: want InvalidError, got %v", err)
	}
}

func TestXrayRejectionBlocksSaving(t *testing.T) {
	svc, fx, _ := newService(t)
	fx.checkErr = errors.New("boom: unknown field")
	_, err := svc.CreateInbound(context.Background(), realityInbound())
	if !isInvalid(err) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("xray error must surface as InvalidError, got %v", err)
	}
	if list, _ := svc.ListInbounds(context.Background()); len(list) != 0 {
		t.Fatal("a config xray rejects must not be saved")
	}
}

func TestWorksWithoutXrayInstalled(t *testing.T) {
	svc, fx, _ := newService(t)
	fx.notReady = true
	if _, err := svc.CreateInbound(context.Background(), realityInbound()); err != nil {
		t.Fatalf("dev mode without xray must still allow editing: %v", err)
	}
	if fx.count() != 0 {
		t.Fatal("nothing can be applied without xray")
	}
}

type failingFront struct{ calls int }

func (f *failingFront) Apply(context.Context, []model.Inbound) error {
	f.calls++
	return errors.New("caddy is not running")
}

func TestFrontProxyFailureDoesNotBlockXray(t *testing.T) {
	svc, fx, _ := newService(t)
	ff := &failingFront{}
	svc.Front = ff

	in, err := svc.CreateInbound(context.Background(), realityInbound())
	var ae *ApplyError
	if !errors.As(err, &ae) || !strings.Contains(err.Error(), "caddy is not running") {
		t.Fatalf("the front proxy failure must be reported as a warning, got %v", err)
	}
	if in == nil || in.ID == "" {
		t.Fatal("the inbound must still be saved and returned")
	}
	if !strings.Contains(fx.last(), `"tag": "in-`+in.ID+`"`) {
		t.Fatalf("xray must receive its config even when the front proxy failed:\n%s", fx.last())
	}
}

func TestUpdateInboundKeepsKeys(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	in, _ := svc.CreateInbound(ctx, realityInbound())

	edit := realityInbound()
	edit.Remark = "renamed"
	edit.Reality.PrivateKey = "" // the form did not send it
	out, err := svc.UpdateInbound(ctx, in.ID, edit)
	if err != nil {
		t.Fatal(err)
	}
	if out.Reality.PrivateKey != in.Reality.PrivateKey || out.Reality.PublicKey != in.Reality.PublicKey {
		t.Fatal("key pair must be preserved on update")
	}
	if out.CreatedAt != in.CreatedAt || out.Remark != "renamed" {
		t.Fatalf("update: %+v", out)
	}
	if _, err := svc.UpdateInbound(ctx, "nope", edit); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestClientLifecycleAndLinks(t *testing.T) {
	svc, fx, _ := newService(t)
	ctx := context.Background()
	r, _ := svc.CreateInbound(ctx, realityInbound())
	w, _ := svc.CreateInbound(ctx, wsInbound(10001))
	_, _ = svc.PutSettings(ctx, model.Settings{PanelDomain: "Panel.Example.com ", RealityDomain: "reality.example.com"})

	c, err := svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true, Flow: model.FlowVision, InboundIDs: []string{r.ID, w.ID, r.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if c.UUID == "" || c.Password == "" || len(c.SubID) != 16 || len(c.InboundIDs) != 2 {
		t.Fatalf("credentials generated and ids deduplicated: %+v", c)
	}
	cfg := fx.last()
	if !strings.Contains(cfg, c.UUID) || !strings.Contains(cfg, c.Password) {
		t.Fatalf("client must be in the xray config:\n%s", cfg)
	}

	list, _ := svc.ListInbounds(ctx)
	if len(list) != 2 || list[0].Clients != 1 || list[1].Clients != 1 {
		t.Fatalf("client counts: %+v", list)
	}

	lv, err := svc.ClientLinks(ctx, c.ID)
	if err != nil || len(lv.Links) != 2 {
		t.Fatalf("links: %v %+v", err, lv)
	}
	if lv.SubscriptionURL != "https://panel.example.com/sub/"+c.SubID {
		t.Fatalf("subscription url: %q", lv.SubscriptionURL)
	}
	byInbound := map[string]LinkView{}
	for _, l := range lv.Links {
		byInbound[l.InboundID] = l // created within the same clock tick, so match by id rather than by position
	}
	if l := byInbound[r.ID]; !strings.HasPrefix(l.Link, "vless://"+c.UUID+"@reality.example.com:443") {
		t.Fatalf("reality link: %+v", l)
	}
	if l := byInbound[w.ID]; !strings.HasPrefix(l.Link, "trojan://"+c.Password+"@panel.example.com:443") || l.Security != "tls" {
		t.Fatalf("trojan link: %+v", l)
	}

	if _, err := svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true}); !isInvalid(err) {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := svc.CreateClient(ctx, model.Client{Email: "bob", Enable: true, InboundIDs: []string{"ghost"}}); !isInvalid(err) {
		t.Fatalf("unknown inbound: %v", err)
	}
	if _, err := svc.CreateClient(ctx, model.Client{Email: "bad name!"}); !isInvalid(err) {
		t.Fatalf("bad name: %v", err)
	}

	// Updating without credentials keeps them.
	upd, err := svc.UpdateClient(ctx, c.ID, model.Client{Email: "alice", Enable: true, InboundIDs: []string{r.ID}})
	if err != nil || upd.UUID != c.UUID || upd.Password != c.Password || upd.SubID != c.SubID {
		t.Fatalf("credentials must be preserved: %+v %v", upd, err)
	}
	if strings.Contains(fx.last(), `"password": "`+c.Password+`"`) {
		t.Fatal("detached inbound must drop the client")
	}

	// Deleting an inbound detaches it from clients.
	if err := svc.DeleteInbound(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetClient(ctx, c.ID); len(got.InboundIDs) != 0 {
		t.Fatalf("inbound must be detached: %+v", got.InboundIDs)
	}
	if err := svc.DeleteClient(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fx.last(), c.UUID) {
		t.Fatal("deleted client must leave the config")
	}
}

func TestTrafficAccountingQuotaAndExpiry(t *testing.T) {
	svc, fx, st := newService(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	in, _ := svc.CreateInbound(ctx, realityInbound())
	quota, _ := svc.CreateClient(ctx, model.Client{Email: "quota", Enable: true, TotalBytes: 1000, InboundIDs: []string{in.ID}})
	exp := now.Add(time.Hour)
	timed, _ := svc.CreateClient(ctx, model.Client{Email: "timed", Enable: true, ExpiryAt: &exp, InboundIDs: []string{in.ID}})

	fx.stats = map[string]xray.Traffic{"quota": {Up: 100, Down: 200}, "ghost": {Up: 5}}
	if err := svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetClient(ctx, quota.ID)
	if got.Up != 100 || got.Down != 200 {
		t.Fatalf("traffic must accumulate: %+v", got)
	}
	if !strings.Contains(fx.last(), quota.UUID) {
		t.Fatal("client under quota stays in the config")
	}

	before := fx.count()
	if err := svc.PollTraffic(ctx); err != nil || fx.count() != before {
		t.Fatalf("an unchanged state must not restart xray (applies %d -> %d, err %v)", before, fx.count(), err)
	}

	fx.stats = map[string]xray.Traffic{"quota": {Down: 800}}
	_ = svc.PollTraffic(ctx)
	if strings.Contains(fx.last(), quota.UUID) {
		t.Fatal("client over quota must be removed from the config")
	}
	if !strings.Contains(fx.last(), timed.UUID) {
		t.Fatal("other clients stay")
	}

	if _, err := svc.ResetTraffic(ctx, quota.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fx.last(), quota.UUID) {
		t.Fatal("resetting traffic must re-activate the client")
	}

	now = now.Add(2 * time.Hour)
	_ = svc.PollTraffic(ctx)
	if strings.Contains(fx.last(), timed.UUID) {
		t.Fatal("expired client must be removed from the config")
	}
}

func TestSettingsValidation(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.PutSettings(context.Background(), model.Settings{PanelDomain: "bad domain"}); !isInvalid(err) {
		t.Fatalf("got %v", err)
	}
	got, err := svc.PutSettings(context.Background(), model.Settings{PanelDomain: " Panel.Example.COM "})
	if err != nil || got.PanelDomain != "panel.example.com" {
		t.Fatalf("domains are trimmed and lowercased: %+v %v", got, err)
	}
}

func TestUUIDGeneration(t *testing.T) {
	u := newUUID()
	if !uuidRe.MatchString(u) || u[14] != '4' {
		t.Fatalf("not a v4 uuid: %s", u)
	}
	b, _ := json.Marshal(newPassword())
	if len(b) != 26 { // 24 chars + quotes
		t.Fatalf("password length: %s", b)
	}
}
