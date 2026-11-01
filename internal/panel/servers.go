package panel

import (
	"context"
	"crypto/sha256"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"anypanel2/internal/agent"
	"anypanel2/internal/model"
	"anypanel2/internal/remark"
	"anypanel2/internal/store"
	"anypanel2/internal/sysinfo"
	"anypanel2/internal/xray"
)

// NodeAPI is one agent as the service sees it (implemented by *agent.Client; replaced in tests).
type NodeAPI interface {
	Status(ctx context.Context) (agent.Status, error)
	Check(ctx context.Context, cfg []byte) error
	Apply(ctx context.Context, cfg []byte) error
	Stats(ctx context.Context, reset bool) (map[string]xray.Traffic, error)
	Restart(ctx context.Context) error
	ObservedFingerprint() string
}

// serverRetry is how long an unreachable server is left alone before the next attempt.
const serverRetry = 30 * time.Second

// serverTimeout bounds one round of talking to an agent so a dead machine cannot stall the panel for long.
const serverTimeout = 10 * time.Second

// ServerState is what the panel last learned about an agent.
type ServerState struct {
	Online       bool         `json:"online"`
	Error        string       `json:"error,omitempty"`
	LastSeen     *time.Time   `json:"lastSeen"`
	AgentVersion string       `json:"agentVersion,omitempty"`
	Xray         xray.Status  `json:"xray"`
	System       sysinfo.Info `json:"system"`
	PingMs       int64        `json:"pingMs"` // how long the agent took to answer the last status request
}

// ServerView is a server as the admin sees it.
type ServerView struct {
	model.Server
	ServerState
	Flag          string `json:"flag"`
	Managed       bool   `json:"managed"` // installed by the panel over SSH: it holds a key and can upgrade the agent
	Inbounds      int    `json:"inbounds"`
	Clients       int    `json:"clients"`       // clients attached to inbounds on this server
	OnlineClients int    `json:"onlineClients"` // moved traffic through this server within OnlineWindow
	Up            int64  `json:"up"`            // traffic through this server since it was added
	Down          int64  `json:"down"`
}

type serverRuntime struct {
	state   ServerState
	applied [32]byte // hash of the config last accepted by the agent
	haveCfg bool
	retryAt time.Time
	geoAt   time.Time // when the country was last looked up, so a failed lookup is retried only now and then
}

func (s *Service) nodeAPI(srv model.Server) NodeAPI {
	if s.NodeClient != nil {
		return s.NodeClient(srv)
	}
	return agent.NewClient(srv.Address, srv.Port, srv.Token, srv.Fingerprint)
}

func (s *Service) runtimeFor(id string) *serverRuntime {
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	if s.srv == nil {
		s.srv = map[string]*serverRuntime{}
	}
	rt := s.srv[id]
	if rt == nil {
		rt = &serverRuntime{}
		s.srv[id] = rt
	}
	return rt
}

func (s *Service) forgetRuntime(id string) {
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	delete(s.srv, id)
}

// ---- CRUD ----

func (s *Service) ListServers(ctx context.Context) ([]ServerView, error) {
	srvs, err := s.Store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	known := make(map[string]bool, len(cls))
	for _, c := range cls {
		known[c.Email] = true
	}
	out := make([]ServerView, 0, len(srvs))
	for _, sv := range srvs {
		v := ServerView{Server: sv, Flag: sv.Flag(), Managed: sv.SSHKey != ""}
		s.srvMu.Lock()
		if rt := s.srv[sv.ID]; rt != nil {
			v.ServerState = rt.state
		}
		s.srvMu.Unlock()
		here := map[string]bool{}
		for _, in := range ins {
			if in.ServerID == sv.ID {
				v.Inbounds++
				here[in.ID] = true
			}
		}
		for _, c := range cls {
			if slices.ContainsFunc(c.InboundIDs, func(id string) bool { return here[id] }) {
				v.Clients++
			}
		}
		rows, err := s.Store.ListServerTraffic(ctx, sv.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			// Rows of names that are not (or no longer) clients are leftovers, for example per-inbound counters that
			// an agent returned next to the clients' and older versions stored as if they were clients.
			if !known[r.Email] {
				continue
			}
			v.Up, v.Down = v.Up+r.Up, v.Down+r.Down
			if r.LastActiveAt != nil && now.Sub(*r.LastActiveAt) <= OnlineWindow {
				v.OnlineClients++
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// ServerClient is one client of an extra server with what it moved through that server.
type ServerClient struct {
	Email        string     `json:"email"`
	Up           int64      `json:"up"`
	Down         int64      `json:"down"`
	Online       bool       `json:"online"`
	LastActiveAt *time.Time `json:"lastActiveAt"`
	Status       string     `json:"status"`
}

// ServerClients lists the clients that can use the server (attached to its inbounds) or have used it, with their
// traffic through it, busiest first.
func (s *Service) ServerClients(ctx context.Context, id string) ([]ServerClient, error) {
	if _, err := s.Store.GetServer(ctx, id); err != nil {
		return nil, err
	}
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.Store.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.ListServerTraffic(ctx, id)
	if err != nil {
		return nil, err
	}
	traffic := map[string]model.ServerTraffic{}
	for _, r := range rows {
		traffic[r.Email] = r
	}
	here := map[string]bool{}
	for _, in := range ins {
		if in.ServerID == id {
			here[in.ID] = true
		}
	}
	now := s.Now()
	out := []ServerClient{}
	for _, c := range cls {
		attached := slices.ContainsFunc(c.InboundIDs, func(x string) bool { return here[x] })
		t, used := traffic[c.Email]
		if !attached && !used {
			continue
		}
		sc := ServerClient{Email: c.Email, Up: t.Up, Down: t.Down, LastActiveAt: t.LastActiveAt,
			Status: remark.Status(set.Subscription.EffectiveClient(c), now)}
		sc.Online = t.LastActiveAt != nil && now.Sub(*t.LastActiveAt) <= OnlineWindow
		out = append(out, sc)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := out[i].Up+out[i].Down, out[j].Up+out[j].Down; a != b {
			return a > b
		}
		return out[i].Email < out[j].Email
	})
	return out, nil
}

func normalizeServer(sv *model.Server) error {
	sv.Name = strings.TrimSpace(sv.Name)
	if n := len([]rune(sv.Name)); n < 1 || n > 60 {
		return invalid("name must be 1–60 characters")
	}
	sv.Address = strings.ToLower(strings.TrimSpace(sv.Address))
	if sv.Address == "" || !isHost(sv.Address) {
		return invalid("address must be a domain name or an IP address")
	}
	if sv.Port == 0 {
		sv.Port = agent.DefaultPort
	}
	if sv.Port < 1 || sv.Port > 65535 {
		return invalid("agent port must be 1–65535")
	}
	sv.Country = strings.ToUpper(strings.TrimSpace(sv.Country))
	if sv.Country != "" && model.FlagEmoji(sv.Country) == "" {
		return invalid("country must be a two-letter code such as DE")
	}
	sv.Fingerprint = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(sv.Fingerprint), ":", ""))
	if sv.Fingerprint != "" && !fingerprintRe.MatchString(sv.Fingerprint) {
		return invalid("fingerprint must be 64 hexadecimal characters (SHA-256), as printed by the installer")
	}
	return nil
}

// CreateServer registers a server and returns it with its token. The token is shown to the admin once; the
// installer on the new machine needs it.
func (s *Service) CreateServer(ctx context.Context, sv model.Server) (*ServerView, string, error) {
	if err := normalizeServer(&sv); err != nil {
		return nil, "", err
	}
	existing, err := s.Store.ListServers(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(existing) >= 50 {
		return nil, "", invalid("at most 50 servers")
	}
	for _, e := range existing {
		if e.Address == sv.Address && e.Port == sv.Port {
			return nil, "", invalid("this server is already registered as %q", e.Name)
		}
	}
	sv.ID, sv.Token, sv.Enable, sv.CreatedAt = newID(), randString(alnum, 48), true, s.Now().UTC()
	if err := s.Store.PutServer(ctx, sv); err != nil {
		return nil, "", err
	}
	return &ServerView{Server: sv}, sv.Token, nil
}

func (s *Service) UpdateServer(ctx context.Context, id string, in model.Server) (*ServerView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.Store.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	in.ID, in.Token, in.CreatedAt = cur.ID, cur.Token, cur.CreatedAt
	in.XrayProfile = cur.XrayProfile // edited on its own page
	in.SSHPort, in.SSHUser, in.SSHHostKey, in.SSHKey = cur.SSHPort, cur.SSHUser, cur.SSHHostKey, cur.SSHKey
	if err := normalizeServer(&in); err != nil {
		return nil, err
	}
	if err := s.Store.PutServer(ctx, in); err != nil {
		return nil, err
	}
	if in.Address != cur.Address || in.Port != cur.Port || in.Fingerprint != cur.Fingerprint || in.Enable != cur.Enable {
		s.forgetRuntime(id) // new address, pin or state: start from scratch
	}
	_ = s.syncServersLocked(ctx, true)
	list, _ := s.ListServers(ctx)
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return &ServerView{Server: in}, nil
}

// RegenerateServerToken issues a new token; the agent must be reinstalled or reconfigured with it.
func (s *Service) RegenerateServerToken(ctx context.Context, id string) (string, error) {
	cur, err := s.Store.GetServer(ctx, id)
	if err != nil {
		return "", err
	}
	cur.Token = randString(alnum, 48)
	if err := s.Store.PutServer(ctx, *cur); err != nil {
		return "", err
	}
	s.forgetRuntime(id)
	return cur.Token, nil
}

func (s *Service) DeleteServer(ctx context.Context, id string) error {
	_, err := s.DeleteServerNote(ctx, id)
	return err
}

// DeleteServerNote deletes the server and, with it, the domain the panel made for it in the REG.RU zone. The text is a
// warning when that record could not be removed.
func (s *Service) DeleteServerNote(ctx context.Context, id string) (string, error) {
	if err := s.deleteServer(ctx, id); err != nil {
		return "", err
	}
	return s.regruReleaseOwned(ctx, id, ""), nil
}

func (s *Service) deleteServer(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Store.GetServer(ctx, id); err != nil {
		return err
	}
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	for _, in := range ins {
		if in.ServerID == id {
			return invalid("inbound %q still runs on this server: move or delete it first", in.Remark)
		}
	}
	if err := s.Store.DeleteServer(ctx, id); err != nil {
		return err
	}
	if err := s.Store.DeleteServerTraffic(ctx, id); err != nil && s.Log != nil {
		s.Log.Warn("removing server traffic failed", "server", id, "err", err)
	}
	s.forgetRuntime(id)
	return nil
}

// TestServer contacts the agent right now and reports the result. On first contact without a pinned fingerprint
// the one the agent presents is pinned.
func (s *Service) TestServer(ctx context.Context, id string) (*ServerView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sv, err := s.Store.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	s.runtimeFor(id).retryAt = time.Time{}
	s.refreshServer(ctx, *sv)
	_ = s.syncServersLocked(ctx, false)
	list, err := s.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, nil
}

func (s *Service) RestartServer(ctx context.Context, id string) error {
	sv, err := s.Store.GetServer(ctx, id)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, serverTimeout)
	defer cancel()
	if err := s.nodeAPI(*sv).Restart(cctx); err != nil {
		return invalid("restart failed: %v", err)
	}
	return nil
}

// ---- state tracking ----

// refreshServer asks the agent for its status and records the outcome. It returns whether the server is online.
func (s *Service) refreshServer(ctx context.Context, sv model.Server) bool {
	rt := s.runtimeFor(sv.ID)
	cctx, cancel := context.WithTimeout(ctx, serverTimeout)
	defer cancel()
	api := s.nodeAPI(sv)
	began := time.Now()
	st, err := api.Status(cctx)
	ping := time.Since(began).Milliseconds()
	now := s.Now()

	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	if err != nil {
		rt.state.Online = false
		switch {
		case agent.IsUnauthorized(err):
			rt.state.Error = "агент отклонил токен: переустановите агент или обновите токен"
		default:
			rt.state.Error = err.Error()
		}
		rt.retryAt = now.Add(serverRetry)
		return false
	}
	if !rt.state.Online {
		rt.haveCfg = false // it may have restarted or been reinstalled: push the config again
	}
	t := now
	rt.state = ServerState{Online: true, LastSeen: &t, AgentVersion: st.Version, Xray: st.Xray, System: st.System, PingMs: ping}
	rt.retryAt = time.Time{}
	// A server without a country gets it from its address once it has answered: the flag then needs no picking.
	if sv.Country == "" && s.GeoCountry != nil && now.Sub(rt.geoAt) > geoRetry {
		rt.geoAt = now
		go s.detectCountry(sv.ID, sv.Address)
	}
	// Trust on first use: remember the certificate seen when nothing was pinned yet.
	if sv.Fingerprint == "" {
		if fp := api.ObservedFingerprint(); fp != "" {
			sv.Fingerprint = fp
			if err := s.Store.PutServer(ctx, sv); err != nil && s.Log != nil {
				s.Log.Warn("saving the agent fingerprint failed", "server", sv.Name, "err", err)
			}
		}
	}
	return true
}

// geoRetry is how long to wait before asking again after a lookup that found nothing.
const geoRetry = time.Hour

// detectCountry looks up the country of a server's address and stores it, unless someone set one in the meantime. It runs
// in the background and under the service lock, so it never races a settings change.
func (s *Service) detectCountry(id, host string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code, err := s.GeoCountry(ctx, host)
	if err != nil {
		if s.Log != nil {
			s.Log.Info("country of a server not found", "server", host, "err", err)
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.Store.GetServer(ctx, id)
	if err != nil || cur.Country != "" || cur.Address != host {
		return
	}
	cur.Country = code
	if err := s.Store.PutServer(ctx, *cur); err != nil && s.Log != nil {
		s.Log.Warn("saving the country of a server failed", "server", host, "err", err)
	}
}

func (s *Service) serverGroups(ctx context.Context) (map[string][]model.Inbound, error) {
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	g := map[string][]model.Inbound{}
	for _, in := range ins {
		g[in.ServerID] = append(g[in.ServerID], in)
	}
	return g, nil
}

// syncServersLocked pushes each enabled server's config when it changed (or force is set). A server that cannot be
// reached does not fail the change: it is marked offline and retried every serverRetry.
func (s *Service) syncServersLocked(ctx context.Context, force bool) error {
	srvs, err := s.Store.ListServers(ctx)
	if err != nil {
		return err
	}
	if len(srvs) == 0 {
		return nil
	}
	groups, err := s.serverGroups(ctx)
	if err != nil {
		return err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, sv := range srvs {
		if !sv.Enable {
			continue
		}
		sv := sv
		cfg, err := s.buildConfigFor(ctx, groups[sv.ID], cls, agent.APIPort, sv.XrayProfile)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(cfg)
		rt := s.runtimeFor(sv.ID)
		s.srvMu.Lock()
		skip := !force && rt.haveCfg && rt.applied == hash
		wait := !force && s.Now().Before(rt.retryAt)
		s.srvMu.Unlock()
		if skip || wait {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.pushConfig(ctx, sv, rt, cfg, hash)
		}()
	}
	wg.Wait()
	return nil
}

func (s *Service) pushConfig(ctx context.Context, sv model.Server, rt *serverRuntime, cfg []byte, hash [32]byte) {
	if !s.refreshServer(ctx, sv) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, serverTimeout)
	defer cancel()
	err := s.nodeAPI(sv).Apply(cctx, cfg)
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	if err != nil {
		rt.state.Error = "конфигурация не применена: " + err.Error()
		rt.retryAt = s.Now().Add(serverRetry)
		return
	}
	rt.applied, rt.haveCfg, rt.state.Error = hash, true, ""
}

// pollServersLocked reads status and traffic from every reachable server. The counters are reset on the agent, so
// each byte is returned exactly once.
func (s *Service) pollServersLocked(ctx context.Context) (map[string]xray.Traffic, map[string]xray.Traffic) {
	srvs, err := s.Store.ListServers(ctx)
	if err != nil || len(srvs) == 0 {
		return nil, nil
	}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		sum    = map[string]xray.Traffic{}
		sumIns = map[string]xray.Traffic{}
	)
	for _, sv := range srvs {
		if !sv.Enable {
			continue
		}
		sv := sv
		rt := s.runtimeFor(sv.ID)
		s.srvMu.Lock()
		wait := s.Now().Before(rt.retryAt)
		s.srvMu.Unlock()
		if wait {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !s.refreshServer(ctx, sv) {
				return
			}
			cctx, cancel := context.WithTimeout(ctx, serverTimeout)
			defer cancel()
			m, err := s.nodeAPI(sv).Stats(cctx, true)
			if err != nil {
				if s.Log != nil {
					s.Log.Warn("server traffic poll failed", "server", sv.Name, "err", err)
				}
				return
			}
			// An agent returns the per-inbound counters next to the clients'; they are not clients.
			users, byInbound := xray.SplitStats(m)
			deltas := make(map[string]store.Traffic, len(users))
			for email, t := range users {
				deltas[email] = store.Traffic{Up: t.Up, Down: t.Down}
			}
			if err := s.Store.AddServerTraffic(ctx, sv.ID, deltas, s.Now()); err != nil && s.Log != nil {
				s.Log.Warn("saving per-server traffic failed", "server", sv.Name, "err", err)
			}
			mu.Lock()
			for email, t := range users {
				cur := sum[email]
				sum[email] = xray.Traffic{Up: cur.Up + t.Up, Down: cur.Down + t.Down}
			}
			for tag, t := range byInbound {
				cur := sumIns[tag]
				sumIns[tag] = xray.Traffic{Up: cur.Up + t.Up, Down: cur.Down + t.Down}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return sum, sumIns
}

// serverOf returns the registered server an inbound runs on, or nil for this machine.
func (s *Service) serverMap(ctx context.Context) (map[string]model.Server, error) {
	srvs, err := s.Store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]model.Server, len(srvs))
	for _, sv := range srvs {
		m[sv.ID] = sv
	}
	return m, nil
}

// withServerHost gives an inbound that runs elsewhere the address of its server, unless it has its own.
func withServerHost(in model.Inbound, servers map[string]model.Server) (model.Inbound, bool) {
	if in.ServerID == "" {
		return in, true
	}
	sv, ok := servers[in.ServerID]
	if !ok {
		return in, false
	}
	if in.Public.Host == "" {
		in.Public.Host = sv.Address
	}
	return in, sv.Enable
}

// checkServerRef validates the server an inbound is placed on and returns the ports that machine reserves.
func (s *Service) checkServerRef(ctx context.Context, in *model.Inbound) ([]int, error) {
	if in.ServerID == "" {
		return s.Reserved, nil
	}
	sv, err := s.Store.GetServer(ctx, in.ServerID)
	if err != nil {
		return nil, invalid("server %q does not exist", in.ServerID)
	}
	return []int{sv.Port, agent.APIPort}, nil
}

var fingerprintRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
