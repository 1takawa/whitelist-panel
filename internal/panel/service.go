// Package panel is the business logic: it validates inbounds and clients, persists them and keeps Xray in sync.
package panel

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"anypanel2/internal/agent"
	"anypanel2/internal/front"
	"anypanel2/internal/links"
	"anypanel2/internal/model"
	"anypanel2/internal/remark"
	"anypanel2/internal/routing"
	"anypanel2/internal/store"
	"anypanel2/internal/sub"
	"anypanel2/internal/sysinfo"
	"anypanel2/internal/telegram"
	"anypanel2/internal/xray"
)

// XrayController is what the service needs from the Xray supervisor (implemented by *xray.Manager).
type XrayController interface {
	Installed() bool
	Check(ctx context.Context, cfg []byte) error
	Apply(ctx context.Context, cfg []byte) error
	Restart(ctx context.Context) error
	Status(ctx context.Context) xray.Status
	Logs(n int) []string
	QueryStats(ctx context.Context, reset bool) (map[string]xray.Traffic, error)
}

// Front keeps the front proxy routes in sync (implemented by *front.Caddy); optional.
type Front interface {
	Apply(ctx context.Context, inbounds []model.Inbound) error
}

type Service struct {
	Store    store.Store
	Xray     XrayController
	Front    Front
	Opts     xray.Options
	Reserved []int // ports the panel itself uses
	Now      func() time.Time
	Log      *slog.Logger
	devMu    sync.Mutex // serialises changes of a client's device list (subscription requests come in parallel)

	// Optional parts, wired by the caller.
	Sampler      *sysinfo.Sampler
	Version      string
	TelegramBase string                     // Bot API address; empty means api.telegram.org (replaced in tests)
	NodeClient   func(model.Server) NodeAPI // agent connections; nil means the real agent client (replaced in tests)
	// GeoCountry finds the country of a server address; nil switches the automatic flag off (tests).
	GeoCountry func(ctx context.Context, host string) (string, error)
	Installer  NodeInstaller   // SSH installation of nodes; nil means the real one (replaced in tests)
	Routing    *routing.Source // RoscomVPN routing profiles; nil means the built-in copies only
	// SNI manages the extra REALITY domains of the main server (nginx map, decoy sites); nil = not available here.
	SNI SNIFront
	// LookupHost resolves a domain (nil = the system resolver; replaced in tests).
	LookupHost func(ctx context.Context, host string) ([]string, error)

	// RegRuBase is the REG.API address ("" = the real one; replaced in tests). PublicIP tells this machine's address
	// as the internet sees it (nil = ask api.ipify.org); it is only shown to the user, to be allowed in REG.RU.
	RegRuBase string
	PublicIP  func(ctx context.Context) (string, error)
	regruMu   sync.Mutex // one DNS change at a time, so two generated names never collide

	mu       sync.Mutex
	lastHash [32]byte
	applied  bool

	jobsMu         sync.Mutex // guards jobs (node installations and upgrades)
	jobs           map[string]*job
	srvMu          sync.Mutex // guards srv
	srv            map[string]*serverRuntime
	secretsMu      sync.Mutex // read-modify-write of the secrets document
	extMu          sync.Mutex // external node list edits (duplicate checks)
	lastBackupFail time.Time

	tgMu     sync.Mutex
	tgCtx    context.Context
	tgCancel context.CancelFunc
	bot      *telegram.Bot
	tgErr    string
	tgWG     sync.WaitGroup

	// Previous poll's view, used to detect changes worth a notification (guarded by mu).
	evSeeded   bool
	evStatus   map[string]string
	evXrayUp   bool
	evServerUp map[string]bool
}

func New(st store.Store, xr XrayController, opts xray.Options, reserved []int, log *slog.Logger) *Service {
	return &Service{Store: st, Xray: xr, Opts: opts, Reserved: reserved, Now: time.Now, Log: log}
}

// ApplyError means the change was saved but could not be applied to the running Xray.
type ApplyError struct{ Err error }

func (e *ApplyError) Error() string { return "saved, but applying to xray failed: " + e.Err.Error() }
func (e *ApplyError) Unwrap() error { return e.Err }

func wrapApply(err error) error {
	if err == nil {
		return nil
	}
	return &ApplyError{Err: err}
}

// ---- config sync ----

// buildConfig renders the Xray config for the given state. Client expiry honours the "last day is valid" option,
// so what Xray enforces matches what subscribers are shown.
func (s *Service) buildConfig(ctx context.Context, ins []model.Inbound, cls []model.Client) ([]byte, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	return s.buildConfigFor(ctx, ins, cls, s.Opts.APIPort, set.XrayProfile)
}

// buildConfigFor is buildConfig with the stats API port of the machine the config is for.
func (s *Service) buildConfigFor(ctx context.Context, ins []model.Inbound, cls []model.Client, apiPort int, prof model.XrayProfile) ([]byte, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	eff := make([]model.Client, len(cls), len(cls)+4)
	for i, c := range cls {
		eff[i] = set.Subscription.EffectiveClient(c)
	}
	opts := s.Opts
	opts.APIPort = apiPort
	opts.Profile = prof

	// Cascades: the relay users the exit servers need, the routing the entry servers need, and the entry inbounds that
	// must not run because their exit is gone (they would otherwise send clients out directly).
	casc, relays, off, err := s.cascadePlan(ctx, set)
	if err != nil {
		return nil, err
	}
	if len(casc) > 0 || len(off) > 0 {
		eff = append(eff, relays...)
		opts.Cascades = casc
		kept := make([]model.Inbound, 0, len(ins))
		for _, in := range ins {
			if !off[in.ID] {
				kept = append(kept, in)
			}
		}
		ins = kept
	}
	return xray.BuildConfig(ins, eff, opts, s.Now())
}

// check asks Xray to validate the configs that would result from a change, before anything is saved: the one for
// this machine and one for every other server involved (the local Xray validates those too, same version of the syntax).
func (s *Service) check(ctx context.Context, ins []model.Inbound, cls []model.Client) error {
	if s.Xray == nil || !s.Xray.Installed() { // no local Xray (a command-line action): nothing to validate here
		return nil
	}
	groups := map[string][]model.Inbound{}
	for _, in := range ins {
		groups[in.ServerID] = append(groups[in.ServerID], in)
	}
	if _, ok := groups[""]; !ok {
		groups[""] = nil
	}
	for id, g := range groups {
		port := s.Opts.APIPort
		if id != "" {
			port = agent.APIPort
		}
		prof, err := s.profileFor(ctx, id)
		if err != nil {
			return err
		}
		cfg, err := s.buildConfigFor(ctx, g, cls, port, prof)
		if err != nil {
			return err
		}
		if err := s.Xray.Check(ctx, cfg); err != nil {
			return invalid("%v", err)
		}
	}
	return nil
}

// applyLocked pushes the stored state to this machine's Xray and to every server. Unless force is set it does
// nothing for a target whose rendered config is unchanged. Only a problem on this machine is reported as an error:
// servers that cannot be reached are shown as offline and retried.
func (s *Service) applyLocked(ctx context.Context, force bool) error {
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return err
	}
	local := make([]model.Inbound, 0, len(ins))
	for _, in := range ins {
		if in.ServerID == "" {
			local = append(local, in)
		}
	}
	err = s.applyLocalLocked(ctx, local, cls, force)
	if serr := s.syncServersLocked(ctx, force); serr != nil && err == nil {
		err = serr
	}
	return err
}

func (s *Service) applyLocalLocked(ctx context.Context, ins []model.Inbound, cls []model.Client, force bool) error {
	// The front proxy is updated independently: a Caddy problem must not stop Xray from getting its config.
	// It is idempotent (only reloads when the generated routes changed) and is retried on the next sync.
	var frontErr error
	if s.Front != nil {
		if err := s.Front.Apply(ctx, ins); err != nil {
			frontErr = fmt.Errorf("front proxy: %w", err)
		}
	}
	cfg, err := s.buildConfig(ctx, ins, cls)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(cfg)
	if !force && s.applied && hash == s.lastHash {
		return frontErr
	}
	if s.Xray == nil || !s.Xray.Installed() {
		return frontErr
	}
	if err := s.Xray.Apply(ctx, cfg); err != nil {
		return err
	}
	s.lastHash, s.applied = hash, true
	return frontErr
}

// Sync (re)applies the stored state to Xray, restarting it.
func (s *Service) Sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ctx, true)
}

// ---- inbounds ----

type InboundView struct {
	model.Inbound
	Clients int `json:"clients"`
}

func (s *Service) ListInbounds(ctx context.Context) ([]InboundView, error) {
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InboundView, 0, len(ins))
	for _, in := range ins {
		n := 0
		for _, c := range cls {
			if c.Uses(in.ID) {
				n++
			}
		}
		in.EnsureRaw() // inbounds saved by older releases get their Xray documents on the fly
		out = append(out, InboundView{Inbound: in, Clients: n})
	}
	return out, nil
}

func (s *Service) GetInbound(ctx context.Context, id string) (*model.Inbound, error) {
	in, err := s.Store.GetInbound(ctx, id)
	if err != nil {
		return nil, err
	}
	in.EnsureRaw()
	return in, nil
}

// MigrateInbounds stores the Xray documents of inbounds saved before they existed (release 0.9).
func (s *Service) MigrateInbounds(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	for _, in := range ins {
		if in.HasRaw() && in.Settings != nil {
			continue
		}
		in.EnsureRaw()
		if in.TrafficReset == "" {
			in.TrafficReset = model.ResetNever
		}
		if err := s.Store.PutInbound(ctx, in); err != nil {
			return err
		}
	}
	return nil
}

// CreateInbound saves a new connection and puts it into the subscription of every existing client: a connection is for
// everybody, and its on/off switch takes it in or out of all subscriptions at once.
func (s *Service) CreateInbound(ctx context.Context, in model.Inbound) (*model.Inbound, error) {
	return s.CreateInboundFor(ctx, in, true)
}

// CreateInboundFor is CreateInbound with the choice of attaching the connection to the existing clients.
func (s *Service) CreateInboundFor(ctx context.Context, in model.Inbound, toAllClients bool) (*model.Inbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reserved, err := s.checkServerRef(ctx, &in)
	if err != nil {
		return nil, err
	}
	if err := normalizeInbound(&in, reserved); err != nil {
		return nil, err
	}
	in.ID, in.CreatedAt = newID(), s.Now().UTC()
	in.Up, in.Down = 0, 0
	if in.SortOrder == 0 {
		// A new connection goes to the end of the list, whatever order the rows were dragged into.
		if all, err := s.Store.ListInbounds(ctx); err == nil {
			for _, o := range all {
				in.SortOrder = max(in.SortOrder, o.SortOrder+1)
			}
		}
	}
	if in.TrafficReset != model.ResetNever {
		now := s.Now()
		in.LastReset = &now
	}
	if err := s.persistInbound(ctx, in); err != nil {
		return nil, err
	}
	if toAllClients {
		if _, err := s.attachLocked(ctx, in.ID, func(model.Client) bool { return true }); err != nil {
			return nil, err
		}
	}
	return &in, wrapApply(s.applyLocked(ctx, false))
}

func (s *Service) UpdateInbound(ctx context.Context, id string, in model.Inbound) (*model.Inbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.Store.GetInbound(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.HasRaw() {
		in.Derive()
	}
	if in.Security == model.SecReality && in.Reality.PrivateKey == "" {
		in.Reality.PrivateKey = old.Reality.PrivateKey // keep the existing key pair when the form omits it
		if in.HasRaw() {
			in.Sync()
		}
	}
	reserved, err := s.checkServerRef(ctx, &in)
	if err != nil {
		return nil, err
	}
	if err := normalizeInbound(&in, reserved); err != nil {
		return nil, err
	}
	in.ID, in.CreatedAt, in.CascadeID = old.ID, old.CreatedAt, old.CascadeID // a form cannot turn a connection into (or out of) a cascade
	in.NodeDomain = old.NodeDomain                                           // nor into (or out of) a node of the main server
	in.Up, in.Down, in.LastReset = old.Up, old.Down, old.LastReset           // counters belong to the server, not to the form
	if in.TrafficReset != model.ResetNever && in.LastReset == nil {
		now := s.Now()
		in.LastReset = &now
	}
	if err := s.persistInbound(ctx, in); err != nil {
		return nil, err
	}
	return &in, wrapApply(s.applyLocked(ctx, false))
}

// persistInbound validates the resulting config with Xray, then saves the inbound.
func (s *Service) persistInbound(ctx context.Context, in model.Inbound) error {
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return err
	}
	candidate := make([]model.Inbound, 0, len(ins)+1)
	for _, o := range ins {
		if o.ID != in.ID {
			candidate = append(candidate, o)
		}
	}
	for _, o := range candidate {
		if o.Port == in.Port && o.ServerID == in.ServerID {
			return invalid("port %d is already used by inbound %q", in.Port, o.Remark)
		}
	}
	candidate = append(candidate, in)
	if err := s.check(ctx, candidate, cls); err != nil {
		return err
	}
	if err := s.Store.PutInbound(ctx, in); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return invalid("port %d is already used", in.Port)
		}
		return err
	}
	return nil
}

func (s *Service) DeleteInbound(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, oldErr := s.Store.GetInbound(ctx, id)
	if err := s.Store.DeleteInbound(ctx, id); err != nil {
		return err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return err
	}
	for _, c := range cls {
		if c.Uses(id) {
			c.InboundIDs = slices.DeleteFunc(slices.Clone(c.InboundIDs), func(x string) bool { return x == id })
			if err := s.Store.PutClient(ctx, c); err != nil {
				return err
			}
		}
	}
	if oldErr == nil && old.CascadeID != "" {
		_ = s.Store.DeleteCascade(ctx, old.CascadeID) // the cascade lives and dies with its node
	}
	applyErr := wrapApply(s.applyLocked(ctx, false))
	if oldErr == nil && old.NodeDomain != "" && old.ServerID == "" {
		// The domain goes away from nginx and Caddy with its node.
		if err := s.syncSNI(ctx, ""); err != nil && s.Log != nil {
			s.Log.Warn("removing the domain of a deleted node failed", "domain", old.NodeDomain, "err", err)
		}
	}
	return applyErr
}

// NewRealityKeys generates a key pair and short id for the inbound form.
func (s *Service) NewRealityKeys() (priv, pub, shortID string, err error) {
	if priv, pub, err = xray.GenerateRealityKeys(); err != nil {
		return
	}
	shortID, err = xray.NewShortID()
	return
}

// ---- clients ----

func (s *Service) ListClients(ctx context.Context) ([]model.Client, error) {
	return s.Store.ListClients(ctx)
}

func (s *Service) GetClient(ctx context.Context, id string) (*model.Client, error) {
	return s.Store.GetClient(ctx, id)
}

func (s *Service) checkInboundIDs(ctx context.Context, ids []string) error {
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !slices.ContainsFunc(ins, func(in model.Inbound) bool { return in.ID == id }) {
			return invalid("inbound %q does not exist", id)
		}
	}
	return nil
}

func (s *Service) CreateClient(ctx context.Context, c model.Client) (*model.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := normalizeClient(&c); err != nil {
		return nil, err
	}
	if err := s.checkInboundIDs(ctx, c.InboundIDs); err != nil {
		return nil, err
	}
	if c.UUID == "" {
		c.UUID = newUUID()
	}
	if c.Password == "" {
		c.Password = newPassword()
	}
	if c.SubID == "" {
		c.SubID = newSubID()
	}
	c.ID, c.CreatedAt, c.Up, c.Down = newID(), s.Now().UTC(), 0, 0
	if err := s.putClient(ctx, c); err != nil {
		return nil, err
	}
	return &c, wrapApply(s.applyLocked(ctx, false))
}

func (s *Service) UpdateClient(ctx context.Context, id string, c model.Client) (*model.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, err := s.Store.GetClient(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := normalizeClient(&c); err != nil {
		return nil, err
	}
	if err := s.checkInboundIDs(ctx, c.InboundIDs); err != nil {
		return nil, err
	}
	// Credentials and counters are kept unless explicitly replaced.
	if c.UUID == "" {
		c.UUID = old.UUID
	}
	if c.Password == "" {
		c.Password = old.Password
	}
	if c.SubID == "" {
		c.SubID = old.SubID
	}
	c.ID, c.CreatedAt, c.Up, c.Down = old.ID, old.CreatedAt, old.Up, old.Down
	c.Devices = old.Devices // changed only through the device endpoints
	if err := s.putClient(ctx, c); err != nil {
		return nil, err
	}
	return &c, wrapApply(s.applyLocked(ctx, false))
}

func (s *Service) putClient(ctx context.Context, c model.Client) error {
	if err := s.Store.PutClient(ctx, c); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return invalid("a client with this name or subscription id already exists")
		}
		return err
	}
	return nil
}

func (s *Service) DeleteClient(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Store.DeleteClient(ctx, id); err != nil {
		return err
	}
	return wrapApply(s.applyLocked(ctx, false))
}

// ResetTraffic zeroes a client's counters (which may re-activate an exhausted client).
func (s *Service) ResetTraffic(ctx context.Context, id string) (*model.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.Store.GetClient(ctx, id)
	if err != nil {
		return nil, err
	}
	c.Up, c.Down = 0, 0
	if err := s.Store.PutClient(ctx, *c); err != nil {
		return nil, err
	}
	return c, wrapApply(s.applyLocked(ctx, false))
}

// ---- links ----

type LinkView struct {
	ep        links.Endpoint // the same entry as a structure, for the Clash and JSON formats
	InboundID string         `json:"inboundId"`
	Remark    string         `json:"remark"` // the inbound's name
	Name      string         `json:"name"`   // the link's name after template expansion
	Protocol  string         `json:"protocol"`
	Network   string         `json:"network"`
	Security  string         `json:"security"`
	Enabled   bool           `json:"enabled"`
	excluded  bool           // the inbound asked to stay out of subscriptions
	Link      string         `json:"link"`
	Error     string         `json:"error,omitempty"`
}

// ClientLinksView is what the admin sees for one client: every link plus the subscription address.
type ClientLinksView struct {
	SubscriptionURL string     `json:"subscriptionUrl"`
	Links           []LinkView `json:"links"`
}

// buildLinks renders the links of every inbound the client is attached to.
func (s *Service) buildLinks(ctx context.Context, c model.Client, set model.Settings) ([]LinkView, error) {
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := s.serverMap(ctx)
	if err != nil {
		return nil, err
	}
	eff := set.Subscription.EffectiveClient(c)
	slices.SortStableFunc(ins, func(a, b model.Inbound) int { return a.SortOrder - b.SortOrder })
	rc := remark.Context{Client: eff, Now: s.Now(), Loc: set.Subscription.Location()}
	out := []LinkView{}
	for _, in := range ins {
		if !c.Uses(in.ID) {
			continue
		}
		in, serverOK := withServerHost(in, servers)
		rc.Inbound, rc.Security, rc.Server = &in, links.EffectiveSecurity(in), nil
		if sv, ok := servers[in.ServerID]; ok {
			rc.Server = &sv
		}
		name := strings.TrimSpace(remark.Expand(set.Subscription.RemarkTemplate, rc))
		if name == "" {
			name = c.Email
		}
		v := LinkView{InboundID: in.ID, Remark: in.Remark, Name: name, Protocol: in.Protocol, Network: in.Network,
			Security: rc.Security, Enabled: in.Running(s.Now()) && serverOK, excluded: in.ExcludeFromSub}
		if link, err := links.BuildNamed(in, c, set, name); err != nil {
			v.Error = err.Error()
		} else {
			v.Link = link
			v.ep, _ = links.NewEndpoint(in, c, set, name)
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) ClientLinks(ctx context.Context, id string) (*ClientLinksView, error) {
	c, err := s.Store.GetClient(ctx, id)
	if err != nil {
		return nil, err
	}
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	lv, err := s.buildLinks(ctx, *c, *set)
	if err != nil {
		return nil, err
	}
	return &ClientLinksView{SubscriptionURL: sub.URL(set.Subscription, set.PanelDomain, c.SubID), Links: lv}, nil
}

// ---- public subscription ----

// Subscription is everything the public endpoints need for one subscription id.
type Subscription struct {
	Client   model.Client // with the effective expiry applied
	Settings model.Settings
	Links    []LinkView // enabled inbounds only, links that could be built (what the user page lists)
	Lines    []string   // the machine-readable body: links, plus or replaced by the info node
	Meta     sub.Meta   // profile metadata after placeholder expansion
	URL      string     // the public subscription address, "" when no host is configured

	// For the Clash and JSON formats: the usable servers, and the info node (if any) with whether it replaces them.
	Endpoints []links.Endpoint
	InfoNames []string
	OnlyInfo  bool

	// The Happ information banner for this subscriber's state, placeholders expanded.
	Banner model.HappBanner
}

// happBanner picks the Happ banner for the client's state and fills in its placeholders (the same ones the link
// names use, so a banner can say "до {{EXPIRE_DATE}}"; in the button link only the identity ones, URL-escaped).
func happBanner(set model.SubSettings, c model.Client, now time.Time) (state string, b model.HappBanner) {
	state = c.BannerState(now)
	b = set.Happ.BannerFor(state)
	b.Text = strings.TrimSpace(remark.Expand(b.Text, remark.Context{Client: c, Now: now, Loc: set.Location()}))
	b.ButtonText = strings.TrimSpace(remark.Expand(b.ButtonText, remark.Context{Client: c, Now: now, Loc: set.Location()}))
	b.ButtonLink = remark.ExpandMeta(b.ButtonLink, c, true)
	return state, b
}

// infoNode decides whether the pseudo-node showing traffic and expiry is added, and what it is called. Once the
// period has ended or the quota is used up it replaces every real server (only = true), so the app shows the reason
// instead of dead servers.
func infoNode(set model.SubSettings, c model.Client, haveLinks bool, now time.Time) (name string, only bool) {
	if !set.InfoNode || !c.Enable {
		return "", false
	}
	rc := remark.Context{Client: c, Now: now, Loc: set.Location()}
	switch {
	case c.Expired(now):
		name := strings.TrimSpace(remark.Expand(set.ExpiredTemplate, rc))
		if name == "" {
			name = "Expired"
		}
		return name, true
	case c.Exhausted():
		name := strings.TrimSpace(remark.Expand(set.DepletedTemplate, rc))
		if name == "" {
			name = "Traffic depleted"
		}
		return name, true
	case haveLinks:
		return strings.TrimSpace(remark.Expand(set.RemarkTemplate, rc)), false
	}
	return "", false
}

// infoNodeLines is the base64-subscription form of infoNode: the pseudo-link in front of (or instead of) the links.
func infoNodeLines(set model.SubSettings, c model.Client, lines []string, now time.Time) []string {
	name, only := infoNode(set, c, len(lines) > 0, now)
	switch {
	case name == "":
		return lines
	case only:
		return []string{sub.InfoLink(name)}
	}
	return append([]string{sub.InfoLink(name)}, lines...)
}

// profileMeta renders the profile metadata for a subscriber.
func profileMeta(set model.SubSettings, c model.Client, subURL string) sub.Meta {
	profileURL := ""
	switch set.ProfileMode {
	case model.ProfileModeBuiltin:
		profileURL = subURL
	case model.ProfileModeCustom:
		profileURL = set.ProfileURL
	}
	return sub.Meta{
		Title:      remark.ExpandMeta(set.Title, c, false),
		SupportURL: remark.ExpandMeta(set.SupportURL, c, true),
		ProfileURL: remark.ExpandMeta(profileURL, c, true),
		Announce:   remark.ExpandMeta(set.Announce, c, false),
	}
}

// PublicSubscription resolves a subscription id. It returns store.ErrNotFound for unknown ids and when
// subscriptions are switched off, so the two cases are indistinguishable from outside.
func (s *Service) PublicSubscription(ctx context.Context, subID string) (*Subscription, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	if !set.Subscription.Enable {
		return nil, store.ErrNotFound
	}
	c, err := s.Store.GetClientBySubID(ctx, subID)
	if err != nil {
		return nil, err
	}
	all, err := s.buildLinks(ctx, *c, *set)
	if err != nil {
		return nil, err
	}
	var usable []LinkView
	var eps []links.Endpoint
	lines := []string{}
	for _, l := range all {
		if l.Enabled && l.Error == "" && !l.excluded {
			usable = append(usable, l)
			lines = append(lines, l.Link)
			eps = append(eps, l.ep)
		}
	}
	eff := set.Subscription.EffectiveClient(*c)
	// Third-party servers are only for subscribers in good standing: an expired or exhausted client would
	// otherwise keep using servers the panel cannot cut off.
	if remark.Status(eff, s.Now()) == "active" {
		ext, err := s.externalLinks(ctx)
		if err != nil {
			return nil, err
		}
		for _, l := range ext {
			usable = append(usable, l)
			lines = append(lines, l.Link)
			eps = append(eps, l.ep)
		}
	}
	subURL := sub.URL(set.Subscription, set.PanelDomain, c.SubID)
	sb := &Subscription{
		Client: eff, Settings: *set, Links: usable, URL: subURL, Endpoints: eps,
		Lines: infoNodeLines(set.Subscription, eff, lines, s.Now()),
		Meta:  profileMeta(set.Subscription, eff, subURL),
	}
	_, sb.Banner = happBanner(set.Subscription, eff, s.Now())
	if name, only := infoNode(set.Subscription, eff, len(lines) > 0, s.Now()); name != "" {
		sb.InfoNames, sb.OnlyInfo = []string{name}, only
		if only {
			sb.Endpoints = nil
		}
	}
	return sb, nil
}

// ---- settings ----

// settings loads the stored settings with defaults applied.
func (s *Service) settings(ctx context.Context) (*model.Settings, error) {
	v, err := s.Store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	d := v.WithDefaults()
	return &d, nil
}

func (s *Service) GetSettings(ctx context.Context) (*model.Settings, error) { return s.settings(ctx) }

func (s *Service) PutSettings(ctx context.Context, v model.Settings) (*model.Settings, error) {
	if err := normalizeSettings(&v); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, err := s.settings(ctx); err == nil {
		v.RegRu = cur.RegRu                                           // saved through /api/regru together with its secrets
		v.XrayProfile = cur.XrayProfile                               // saved on its own page, never by the settings form
		v.Subscription.Preview.Image = cur.Subscription.Preview.Image // uploaded through its own endpoint
	}
	if err := s.Store.PutSettings(ctx, v); err != nil {
		return nil, err
	}
	// Calendar expiry and the time zone change who may connect, so Xray is re-synced (a no-op when nothing changed).
	if s.Xray != nil {
		if err := s.applyLocked(ctx, false); err != nil && s.Log != nil {
			s.Log.Warn("sync after settings change failed", "err", err)
		}
	}
	s.ReloadTelegram(ctx)
	return &v, nil
}

// ---- template preview ----

// PreviewTemplate renders a template for the settings UI. kind is "remark" (a link name), "meta" (a text such as
// the title) or "url" (a link field, placeholder values escaped). With clientID empty a sample client is used.
func (s *Service) PreviewTemplate(ctx context.Context, kind, template, clientID, timeZone string) (string, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return "", err
	}
	sub := set.Subscription
	if timeZone != "" {
		sub.TimeZone = timeZone
	}
	now := s.Now()
	c, in := sampleClient(now), sampleInbound()
	if clientID != "" {
		real, err := s.Store.GetClient(ctx, clientID)
		if err != nil {
			return "", err
		}
		c = *real
		if ins, err := s.Store.ListInbounds(ctx); err == nil {
			for _, i := range ins {
				if real.Uses(i.ID) {
					in = i
					break
				}
			}
		}
	}
	c = sub.EffectiveClient(c)
	switch kind {
	case "remark":
		return remark.Expand(template, remark.Context{Client: c, Inbound: &in, Security: links.EffectiveSecurity(in), Now: now, Loc: sub.Location()}), nil
	case "meta":
		return remark.ExpandMeta(template, c, false), nil
	case "url":
		return remark.ExpandMeta(template, c, true), nil
	}
	return "", invalid("kind must be remark, meta or url")
}

func sampleClient(now time.Time) model.Client {
	exp := now.Add(12*24*time.Hour + 3*time.Hour)
	return model.Client{
		ID: "a1b2c3d4e5f60718", Email: "alice", SubID: "k3j9x0q2m7p5v8ab", Comment: "пример", Enable: true,
		Up: 1 << 30, Down: 3 << 29, TotalBytes: 10 << 30, ExpiryAt: &exp, CreatedAt: now.Add(-48 * time.Hour),
	}
}

func sampleInbound() model.Inbound {
	return model.Inbound{Remark: "DE Reality", Protocol: model.ProtoVLESS, Network: model.NetTCP, Security: model.SecReality}
}

// TemplateTokens lists the placeholders for the settings UI.
func (s *Service) TemplateTokens() []remark.Token { return remark.Tokens }

// ---- xray ----

func (s *Service) XrayStatus(ctx context.Context) xray.Status { return s.Xray.Status(ctx) }
func (s *Service) XrayLogs(n int) []string                    { return s.Xray.Logs(n) }

func (s *Service) XrayRestart(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ctx, true)
}

// ---- background work ----

// PollTraffic folds Xray's counters into client totals and re-applies the config if a client became inactive
// (quota used up or expired) or active again.
func (s *Service) PollTraffic(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	{
		stats := map[string]xray.Traffic{}
		inStats := map[string]xray.Traffic{}
		localUsers := map[string]xray.Traffic{} // what this machine alone moved, for its "online" count
		if s.Xray.Installed() && s.Xray.Status(ctx).Running {
			local, err := s.Xray.QueryStats(ctx, true)
			if err != nil {
				s.Log.Warn("traffic poll failed", "err", err)
			}
			users, byInbound := xray.SplitStats(local)
			for email, t := range users {
				stats[email] = t
				localUsers[email] = t
			}
			for tag, t := range byInbound {
				inStats[tag] = t
			}
		}
		// Servers report their own counters; a client used on several machines adds up.
		nodeUsers, nodeInbounds := s.pollServersLocked(ctx)
		for email, t := range nodeUsers {
			cur := stats[email]
			stats[email] = xray.Traffic{Up: cur.Up + t.Up, Down: cur.Down + t.Down}
		}
		for tag, t := range nodeInbounds {
			cur := inStats[tag]
			inStats[tag] = xray.Traffic{Up: cur.Up + t.Up, Down: cur.Down + t.Down}
		}
		s.accountInbounds(ctx, inStats)
		if len(stats) > 0 {
			deltas := make(map[string]store.Traffic, len(stats))
			for email, t := range stats {
				deltas[email] = store.Traffic{Up: t.Up, Down: t.Down}
			}
			if err := s.Store.AddTraffic(ctx, deltas); err != nil {
				return fmt.Errorf("save traffic: %w", err)
			}
			// Dashboard data: who moved traffic just now, and the hourly totals for the chart.
			var active []string
			var up, down int64
			for email, t := range stats {
				up, down = up+t.Up, down+t.Down
				if t.Up+t.Down > 0 {
					active = append(active, email)
				}
			}
			now := s.Now()
			if len(localUsers) > 0 {
				local := make(map[string]store.Traffic, len(localUsers))
				for email, t := range localUsers {
					local[email] = store.Traffic{Up: t.Up, Down: t.Down}
				}
				if err := s.Store.AddServerTraffic(ctx, LocalServerID, local, now); err != nil {
					s.Log.Warn("saving this machine's traffic failed", "err", err)
				}
			}
			if err := s.Store.MarkActive(ctx, active, now); err != nil {
				s.Log.Warn("mark active failed", "err", err)
			}
			if up+down > 0 {
				byUser := make(map[string]int64, len(stats))
				for email, t := range stats {
					if t.Up+t.Down > 0 {
						byUser[email] = t.Up + t.Down
					}
				}
				if err := s.Store.AddUserSamples(ctx, now.Unix()/3600, byUser); err != nil {
					s.Log.Warn("client traffic sample failed", "err", err)
				}
				byNode := make(map[string]int64, len(inStats))
				for tag, t := range inStats {
					if id := strings.TrimPrefix(tag, "in-"); id != tag && t.Up+t.Down > 0 {
						byNode[id] = t.Up + t.Down
					}
				}
				if err := s.Store.AddSample(ctx, now.Unix()/3600, up, down, byNode); err != nil {
					s.Log.Warn("traffic sample failed", "err", err)
				}
			}
		}
	}
	err := s.applyLocked(ctx, false)
	s.checkEvents(ctx)
	return err
}

// Run syncs Xray at startup and then polls traffic until ctx is cancelled.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if err := s.MigrateInbounds(ctx); err != nil {
		s.Log.Error("migrating inbounds failed", "err", err)
	}
	if err := s.Sync(ctx); err != nil {
		s.Log.Error("initial xray sync failed", "err", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.PollTraffic(ctx); err != nil {
				s.Log.Error("background sync failed", "err", err)
			}
		}
	}
}

// RoutingPresets lists the ready-made routing profiles (DEFAULT, WHITELIST) for "happ" or "incy".
func (s *Service) RoutingPresets(ctx context.Context, app string, refresh bool) ([]routing.Preset, error) {
	src := s.Routing
	if src == nil {
		src = &routing.Source{}
	}
	ps, err := src.Presets(ctx, app, refresh)
	if err != nil {
		return nil, invalid("%v", err)
	}
	return ps, nil
}

// SNIFront is what the service needs from front.SNI.
type SNIFront interface {
	Apply(ctx context.Context, sites []front.Site) error
}
