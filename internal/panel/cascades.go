package panel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"

	"anypanel2/internal/agent"
	"anypanel2/internal/formats"
	"anypanel2/internal/front"
	"anypanel2/internal/links"
	"anypanel2/internal/model"
	"anypanel2/internal/nodeinstall"
	"anypanel2/internal/store"
	"anypanel2/internal/xray"
)

// A cascade chains two servers: clients connect to the entry server A and leave to the internet from the exit server B.
// For clients it is a node of its own (name and flag): an inbound on A, which every client gets like any other inbound.
// On A the traffic of that inbound is routed to an outbound towards a VLESS inbound of B; B knows A by a relay user the
// panel creates for the cascade (it is not a client and is never shown in the client list).

// cascadePlan works out what the machines' configs need for the cascades: the routing on the entry server, the relay
// user on the exit server, and the entry inbounds that have to stay off because their exit cannot be reached (they are
// left out of the config: traffic must never leave the entry server directly).
func (s *Service) cascadePlan(ctx context.Context, set *model.Settings) (casc []xray.Cascade, relays []model.Client, off map[string]bool, err error) {
	list, err := s.Store.ListCascades(ctx)
	if err != nil || len(list) == 0 {
		return nil, nil, nil, err
	}
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	servers, err := s.serverMap(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	off = map[string]bool{}
	now := s.Now()
	for _, c := range list {
		entry, exit := cascadeInbounds(c, all)
		if entry == nil {
			continue
		}
		out, relay, why := cascadeOutbound(c, exit, servers, set, now)
		if why != "" {
			off[entry.ID] = true
			continue
		}
		casc = append(casc, xray.Cascade{InboundID: entry.ID, Tag: xray.CascadeTag(c.ID), Outbound: out})
		relays = append(relays, relay)
	}
	return casc, relays, off, nil
}

// cascadeInbounds finds the entry inbound (the one that points back at the cascade) and the exit inbound.
func cascadeInbounds(c model.Cascade, all []model.Inbound) (entry, exit *model.Inbound) {
	for i := range all {
		switch {
		case all[i].CascadeID == c.ID:
			entry = &all[i]
		case all[i].ID == c.ExitInboundID:
			exit = &all[i]
		}
	}
	return entry, exit
}

// cascadeOutbound builds the entry server's outbound towards the exit inbound and the relay user the exit must know.
// why is not empty (and says what is wrong) when the cascade cannot work right now.
func cascadeOutbound(c model.Cascade, exit *model.Inbound, servers map[string]model.Server, set *model.Settings, now time.Time) (map[string]any, model.Client, string) {
	switch {
	case exit == nil:
		return nil, model.Client{}, "подключение выхода удалено"
	case exit.Protocol != model.ProtoVLESS:
		return nil, model.Client{}, "подключение выхода должно быть VLESS"
	case !exit.Running(now):
		return nil, model.Client{}, "подключение выхода выключено"
	}
	in, ok := withServerHost(*exit, servers)
	if !ok {
		return nil, model.Client{}, "сервер выхода выключен или удалён"
	}
	relay := c.RelayClient()
	ep, err := links.NewEndpoint(in, relay, *set, "cascade")
	if err != nil {
		return nil, model.Client{}, "не удалось собрать подключение к выходу: " + err.Error()
	}
	out, err := formats.Outbound(ep)
	if err != nil {
		return nil, model.Client{}, "не удалось собрать подключение к выходу: " + err.Error()
	}
	return out, relay, ""
}

// ---- views ----

// CascadeView is a cascade as the admin sees it.
type CascadeView struct {
	model.Cascade
	Flag         string `json:"flag"`
	Mode         string `json:"mode"` // merge | split
	EntryName    string `json:"entryName"`
	ExitName     string `json:"exitName"`
	ExitRemark   string `json:"exitRemark"`   // the exit connection's name
	EntryAddress string `json:"entryAddress"` // where clients connect
	Port         int    `json:"port"`
	State        string `json:"state"` // running | off | broken
	Problem      string `json:"problem,omitempty"`
}

func serverLabel(servers map[string]model.Server, id string) string {
	if id == "" {
		return "Основной сервер"
	}
	if sv, ok := servers[id]; ok {
		return sv.Name
	}
	return "удалённый сервер"
}

func (s *Service) cascadeView(c model.Cascade, all []model.Inbound, servers map[string]model.Server, set *model.Settings) CascadeView {
	v := CascadeView{Cascade: c, Mode: CascadeSplit, Flag: model.FlagEmoji(c.Country), EntryName: serverLabel(servers, c.EntryServerID), ExitName: serverLabel(servers, c.ExitServerID), State: "running"}
	if c.Adopted {
		v.Mode = CascadeMerge
	}
	entry, exit := cascadeInbounds(c, all)
	if exit != nil {
		v.ExitRemark = exit.Remark
	}
	if entry == nil {
		v.State, v.Problem = "broken", "подключение каскада на входном сервере удалено"
		return v
	}
	v.Port = entry.Public.Port // what clients connect to (nginx on 443 or the cascade's own port)
	if v.Port == 0 {
		v.Port = entry.Port
	}
	if entry.Public.Host != "" {
		v.EntryAddress = entry.Public.Host // a split cascade is reached by its own domain
	} else if sv, ok := servers[c.EntryServerID]; ok {
		v.EntryAddress = sv.Address
	} else if h, err := links.Host(*entry, *set); err == nil {
		v.EntryAddress = h
	}
	now := s.Now()
	if _, _, why := cascadeOutbound(c, exit, servers, set, now); why != "" {
		v.State, v.Problem = "broken", why
		return v
	}
	if sv, ok := servers[c.EntryServerID]; c.EntryServerID != "" && (!ok || !sv.Enable) {
		v.State, v.Problem = "broken", "входной сервер выключен или удалён"
		return v
	}
	if !entry.Running(now) {
		v.State = "off"
	}
	return v
}

func (s *Service) ListCascades(ctx context.Context) ([]CascadeView, error) {
	list, err := s.Store.ListCascades(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := s.serverMap(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CascadeView, 0, len(list))
	for _, c := range list {
		out = append(out, s.cascadeView(c, all, servers, set))
	}
	return out, nil
}

// ---- create, rename, delete ----

// The two ways to make a cascade out of the entry server A and the exit server B. Both work on port 443 only.
const (
	// CascadeMerge: A and B become one node. The connection A already has on 443 is the cascade: everything that comes in
	// through it leaves from B, and A has no direct exit any more.
	CascadeMerge = "merge"
	// CascadeSplit: A keeps its own node and the cascade is a second node on the same 443 under a domain of its own.
	CascadeSplit = "split"
)

// CascadeInput is the cascade form.
type CascadeInput struct {
	Name          string `json:"name"`
	Country       string `json:"country"`
	EntryServerID string `json:"entryServerId"` // "" = the main server
	ExitServerID  string `json:"exitServerId"`  // "" = the main server
	ExitInboundID string `json:"exitInboundId"` // optional: by default the first suitable connection of the exit server
	Mode          string `json:"mode"`          // CascadeMerge or CascadeSplit
	Domain        string `json:"domain"`        // split: the REALITY domain of the new node (an A record to the entry server)
	DNSPending    bool   `json:"dnsPending"`    // the domain's A record was only just created: do not wait for DNS to show it
}

var countryRe = regexp.MustCompile(`^[A-Za-z]{2}$`)

func normalizeCascadeName(name, country string) (string, string, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n < 1 || n > 64 {
		return "", "", invalid("name must be 1–64 characters")
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	if country != "" && (!countryRe.MatchString(country) || model.FlagEmoji(country) == "") {
		return "", "", invalid("country must be a two-letter code such as FI")
	}
	return name, country, nil
}

func cascadeRemark(name, country string) string {
	r := strings.TrimSpace(model.FlagEmoji(country) + " " + name)
	if rs := []rune(r); len(rs) > 64 {
		r = string(rs[:64])
	}
	return r
}

// pickExit chooses the exit server's connection the relay user is added to.
func pickExit(all []model.Inbound, serverID, wanted string) (*model.Inbound, error) {
	var best *model.Inbound
	for i := range all {
		in := &all[i]
		if in.ServerID != serverID || in.Protocol != model.ProtoVLESS || in.CascadeID != "" || !in.Enable {
			continue
		}
		if wanted != "" {
			if in.ID == wanted {
				return in, nil
			}
			continue
		}
		if best == nil || (in.Security == model.SecReality && in.Network == model.NetTCP && !(best.Security == model.SecReality && best.Network == model.NetTCP)) {
			best = in
		}
	}
	if wanted != "" {
		return nil, invalid("the chosen exit connection is not an enabled VLESS connection of the exit server")
	}
	if best == nil {
		return nil, invalid("the exit server has no enabled VLESS connection: create one first")
	}
	return best, nil
}

// reservedFor lists the ports a machine keeps for itself.
func (s *Service) reservedFor(ctx context.Context, serverID string) ([]int, error) {
	if serverID == "" {
		return s.Reserved, nil
	}
	sv, err := s.Store.GetServer(ctx, serverID)
	if err != nil {
		return nil, invalid("server %q does not exist", serverID)
	}
	return []int{sv.Port, agent.APIPort}, nil
}

// CreateCascade creates the cascade and its node (the inbound on the entry server) and puts it into every client's
// subscription like any new inbound. The returned warning says when the change is saved but not yet applied.
func (s *Service) CreateCascade(ctx context.Context, in CascadeInput) (*CascadeView, string, error) {
	name, country, err := normalizeCascadeName(in.Name, in.Country)
	if err != nil {
		return nil, "", err
	}
	if in.Mode != CascadeMerge && in.Mode != CascadeSplit {
		return nil, "", invalid("choose how to build the cascade: merge or split")
	}
	if in.EntryServerID == in.ExitServerID {
		return nil, "", invalid("the entry and the exit server must be different")
	}
	var entrySrv model.Server
	for _, id := range []string{in.EntryServerID, in.ExitServerID} {
		if id == "" {
			continue
		}
		sv, err := s.Store.GetServer(ctx, id)
		if err != nil {
			return nil, "", invalid("server %q does not exist", id)
		}
		if !sv.Enable {
			return nil, "", invalid("server %q is switched off", sv.Name)
		}
		if id == in.EntryServerID {
			entrySrv = *sv
		}
	}
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, "", err
	}
	exit, err := pickExit(all, in.ExitServerID, in.ExitInboundID)
	if err != nil {
		return nil, "", err
	}
	c := model.Cascade{ID: newID(), Name: name, Country: country, EntryServerID: in.EntryServerID, ExitServerID: in.ExitServerID,
		ExitInboundID: exit.ID, RelayUUID: newUUID(), CreatedAt: s.Now().UTC()}

	if in.Mode == CascadeMerge {
		own := ownConnection(all, in.EntryServerID)
		if own == nil {
			return nil, "", invalid("the entry server has no REALITY connection on port 443 (behind nginx) to merge with: create it first, or choose split")
		}
		return s.adoptCascade(ctx, c, *own)
	}

	// Split: a second REALITY name on the entry server's 443. nginx there tells the names apart (SNI) and hands each to
	// its own loopback port of Xray; the decoy site for REALITY to imitate is the one the server already has.
	domain := strings.ToLower(strings.TrimSpace(in.Domain))
	if domain == "" {
		return nil, "", invalid("split needs a domain of its own for the new node (an A record to the entry server)")
	}
	target := mainDecoyTarget
	if in.EntryServerID == "" {
		if s.SNI == nil {
			return nil, "", invalid("this server is not set up to manage nginx and Caddy for extra domains (NGINX_SNI_DIR is not set)")
		}
	} else {
		if entrySrv.SSHKey == "" {
			return nil, "", invalid("the panel has no SSH access to the entry server: add it with the node wizard, or merge instead")
		}
		target = nodeDecoyTarget
	}
	chk, err := s.CheckCascadeDomainWith(ctx, domain, "", in.EntryServerID, in.DNSPending)
	if err != nil {
		return nil, "", err
	}
	if !chk.OK {
		return nil, "", invalid("%s", chk.Problem)
	}
	reserved, err := s.reservedFor(ctx, in.EntryServerID)
	if err != nil {
		return nil, "", err
	}
	port := internalPort(all, reserved, in.EntryServerID)
	if port == 0 {
		return nil, "", errors.New("no free internal port found")
	}
	c.Domain = domain
	entry, err := newRealityInbound(cascadeRemark(name, country), in.EntryServerID, "127.0.0.1", port, target, domain, true, 443)
	if err != nil {
		return nil, "", err
	}
	entry.Public.Host = domain // clients connect to the domain itself
	entry.CascadeID = c.ID
	// The cascade record goes first: building the config of the entry inbound looks it up.
	if err := s.Store.PutCascade(ctx, c); err != nil {
		return nil, "", err
	}
	created, err := s.CreateInboundFor(ctx, entry, true)
	warning := ""
	var ae *ApplyError
	switch {
	case errors.As(err, &ae):
		warning = ae.Error()
	case err != nil:
		_ = s.Store.DeleteCascade(ctx, c.ID)
		return nil, "", err
	}
	c.InboundID = created.ID
	if err := s.Store.PutCascade(ctx, c); err != nil {
		return nil, warning, err
	}
	// nginx has to send the domain to this inbound. If that fails the cascade would be a node that never answers, so it
	// is taken back.
	if err := s.syncSNI(ctx, c.EntryServerID); err != nil {
		_ = s.DeleteInbound(ctx, created.ID)
		_ = s.Store.DeleteCascade(ctx, c.ID)
		_ = s.syncSNI(ctx, c.EntryServerID)
		return nil, "", fmt.Errorf("не удалось настроить nginx для домена %s: %w", domain, err)
	}
	s.regruAssign(ctx, domain, "", c.ID) // a domain the panel made goes away with the cascade
	return s.cascadeViewByID(ctx, c.ID, warning)
}

func (s *Service) cascadeViewByID(ctx context.Context, id, warning string) (*CascadeView, string, error) {
	views, err := s.ListCascades(ctx)
	if err != nil {
		return nil, warning, err
	}
	for i := range views {
		if views[i].ID == id {
			return &views[i], warning, nil
		}
	}
	return nil, warning, store.ErrNotFound
}

// ownConnection is the REALITY connection a server got from the installer (behind the node's nginx, on 443), if it
// is still plain: not a cascade and not used for anything else.
func ownConnection(all []model.Inbound, serverID string) *model.Inbound {
	for i := range all {
		in := &all[i]
		if in.ServerID == serverID && in.CascadeID == "" && in.Protocol == model.ProtoVLESS && in.Security == model.SecReality &&
			in.Port == nodeRealityPort && in.Listen == "127.0.0.1" && in.AcceptProxyProtocol && in.Public.Port == 443 {
			return in
		}
	}
	return nil
}

// adoptCascade turns the node's own connection into the cascade's entry: the same address, port 443 and domain, but all
// traffic of this connection now leaves through the exit server.
func (s *Service) adoptCascade(ctx context.Context, c model.Cascade, entry model.Inbound) (*CascadeView, string, error) {
	c.Adopted, c.OrigRemark, c.InboundID = true, entry.Remark, entry.ID
	if err := s.Store.PutCascade(ctx, c); err != nil {
		return nil, "", err
	}
	warning, err := s.setInboundCascade(ctx, entry.ID, c.ID, cascadeRemark(c.Name, c.Country))
	if err != nil {
		_ = s.Store.DeleteCascade(ctx, c.ID)
		return nil, "", err
	}
	return s.cascadeViewByID(ctx, c.ID, warning)
}

// setInboundCascade points a connection at a cascade (or away from it with an empty id) and renames it; the returned
// string is a warning when the change is saved but could not be applied everywhere.
func (s *Service) setInboundCascade(ctx context.Context, inboundID, cascadeID, remark string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := s.Store.GetInbound(ctx, inboundID)
	if err != nil {
		return "", err
	}
	in.CascadeID, in.Remark = cascadeID, remark
	if err := s.Store.PutInbound(ctx, *in); err != nil {
		return "", err
	}
	if err := s.applyLocked(ctx, false); err != nil {
		return wrapApply(err).Error(), nil
	}
	return "", nil
}

// RenameCascade changes the name and the flag the clients see.
func (s *Service) RenameCascade(ctx context.Context, id, name, country string) (*CascadeView, error) {
	name, country, err := normalizeCascadeName(name, country)
	if err != nil {
		return nil, err
	}
	c, err := s.Store.GetCascade(ctx, id)
	if err != nil {
		return nil, err
	}
	c.Name, c.Country = name, country
	if err := s.Store.PutCascade(ctx, *c); err != nil {
		return nil, err
	}
	if in, err := s.Store.GetInbound(ctx, c.InboundID); err == nil {
		upd := *in
		upd.Remark = cascadeRemark(name, country)
		if _, err := s.UpdateInbound(ctx, in.ID, upd); err != nil {
			var ae *ApplyError
			if !errors.As(err, &ae) {
				return nil, err
			}
		}
	}
	views, err := s.ListCascades(ctx)
	if err != nil {
		return nil, err
	}
	for i := range views {
		if views[i].ID == id {
			return &views[i], nil
		}
	}
	return nil, store.ErrNotFound
}

// DeleteCascade removes the cascade, its node and the relay user on the exit server.
func (s *Service) DeleteCascade(ctx context.Context, id string) error {
	var note string
	return s.deleteCascade(ctx, id, &note)
}

// DeleteCascadeNote is DeleteCascade that also reports, as a warning text, a DNS record that could not be removed.
func (s *Service) DeleteCascadeNote(ctx context.Context, id string) (string, error) {
	var note string
	err := s.deleteCascade(ctx, id, &note)
	return note, err
}

func (s *Service) deleteCascade(ctx context.Context, id string, note *string) error {
	c, err := s.Store.GetCascade(ctx, id)
	if err != nil {
		return err
	}
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	var applyErr error
	if entry, _ := cascadeInbounds(*c, all); entry != nil && c.Adopted {
		// The connection was the node's own: it stays, as it was before the cascade.
		if w, err := s.setInboundCascade(ctx, entry.ID, "", c.OrigRemark); err != nil {
			return err
		} else if w != "" {
			applyErr = errors.New(w)
		}
	} else if entry != nil {
		if err := s.DeleteInbound(ctx, entry.ID); err != nil { // also removes the cascade record
			var ae *ApplyError
			if !errors.As(err, &ae) {
				return err
			}
			applyErr = err
		}
	}
	if err := s.Store.DeleteCascade(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if c.Domain != "" {
		if err := s.syncSNI(ctx, c.EntryServerID); err != nil && applyErr == nil { // the domain leaves nginx
			applyErr = wrapApply(err)
		}
		*note = s.regruReleaseOwned(ctx, "", id) // and, if the panel made it, the zone
	}
	if applyErr != nil {
		return applyErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return wrapApply(s.applyLocked(ctx, false)) // the relay user leaves the exit server
}

// The installer's layout of the main server: nginx owns 443 and hands the REALITY domain to Xray on 8443, whose
// decoy site is Caddy on 7443.
const (
	mainRealityPort = 8443
	mainDecoyTarget = "127.0.0.1:7443"
)

// ---- the cascade's own REALITY domain ----

// DomainCheck is the answer to "can this domain be the REALITY domain of a new cascade on the main server".
type DomainCheck struct {
	OK        bool     `json:"ok"`
	Problem   string   `json:"problem,omitempty"` // why not
	Pending   bool     `json:"pending,omitempty"` // fine, but DNS does not show the record yet: the node may be unreachable for a few minutes
	Message   string   `json:"message,omitempty"` // what was found when it is fine
	Available bool     `json:"available"`         // the panel can set up nginx and Caddy for extra domains on this machine
	Addresses []string `json:"addresses"`         // where the domain points
	Main      []string `json:"mainAddresses"`     // the main server's addresses (from the panel and REALITY domains)
	UsedBy    string   `json:"usedBy,omitempty"`  // who already has the domain
}

func (s *Service) lookup(ctx context.Context, host string) ([]string, error) {
	if s.LookupHost != nil {
		return s.LookupHost(ctx, host)
	}
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupHost(cctx, host)
}

func sharesAny(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

// CheckCascadeDomain says whether domain is free and points at the entry server of a cascade (entryServerID, "" = the
// main server). It looks at what the panel already knows (its own domains, servers, connections, other cascades) and at
// DNS. exceptCascade is a cascade being edited.
func (s *Service) CheckCascadeDomain(ctx context.Context, domain, exceptCascade, entryServerID string) (*DomainCheck, error) {
	return s.CheckCascadeDomainWith(ctx, domain, exceptCascade, entryServerID, false)
}

// CheckCascadeDomainWith is CheckCascadeDomain that, with dnsPending (the A record was just created by the panel), accepts a
// domain DNS does not know yet. A record that already points somewhere else is still refused.
func (s *Service) CheckCascadeDomainWith(ctx context.Context, domain, exceptCascade, entryServerID string, dnsPending bool) (*DomainCheck, error) {
	d := strings.ToLower(strings.TrimSpace(domain))
	res := &DomainCheck{Available: entryServerID != "" || s.SNI != nil, Addresses: []string{}, Main: []string{}}
	fail := func(format string, a ...any) (*DomainCheck, error) {
		res.Problem = fmt.Sprintf(format, a...)
		return res, nil
	}
	if !nodeinstall.ValidDomain(d) {
		return fail("это не доменное имя: нужен вид reality2.example.com")
	}
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	servers, err := s.Store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	casc, err := s.Store.ListCascades(ctx)
	if err != nil {
		return nil, err
	}

	// 1. Names the panel already uses.
	for label, v := range map[string]string{"домен панели": set.PanelDomain, "домен REALITY основного сервера": set.RealityDomain, "домен подписок": set.Subscription.Domain} {
		if v != "" && strings.EqualFold(v, d) {
			res.UsedBy = label
			return fail("домен уже занят: это %s", label)
		}
	}
	for _, c := range casc {
		if c.ID != exceptCascade && strings.EqualFold(c.Domain, d) {
			res.UsedBy = "каскад «" + c.Name + "»"
			return fail("домен уже занят: это домен каскада «%s»", c.Name)
		}
	}
	for _, sv := range servers {
		if strings.EqualFold(sv.Address, d) {
			res.UsedBy = "сервер " + sv.Name
			return fail("домен уже занят: это адрес сервера «%s»", sv.Name)
		}
	}
	for _, in := range all {
		if in.CascadeID != "" && in.CascadeID == exceptCascade {
			continue
		}
		names := append(slices.Clone(in.Reality.ServerNames), in.Public.Host, in.Public.SNI)
		for _, n := range names {
			if n != "" && strings.EqualFold(n, d) {
				where := "основном сервере"
				if sv, ok := serverByID(servers, in.ServerID); ok {
					where = "сервере «" + sv.Name + "»"
				}
				res.UsedBy = "подключение «" + in.Remark + "»"
				return fail("домен уже занят: его использует подключение «%s» на %s", in.Remark, where)
			}
		}
	}

	// 2. DNS: it must point at the entry server and at nobody else.
	entryName := "основного сервера"
	var want []string
	if entryServerID == "" {
		// This machine's own address, as the internet sees it, is the truth. The panel's domains only stand in for it
		// when it cannot be found out: after a move or an import they may still name another server.
		if s.PublicIP != nil {
			if ip, err := s.PublicIP(ctx); err == nil && ip != "" {
				want = []string{ip}
			}
		}
		if len(want) == 0 {
			for _, host := range []string{set.PanelDomain, set.RealityDomain} {
				if host == "" {
					continue
				}
				if a, err := s.lookup(ctx, host); err == nil {
					want = append(want, a...)
				}
			}
		}
	} else {
		sv, ok := serverByID(servers, entryServerID)
		if !ok {
			return fail("сервер входа не найден")
		}
		entryName = "сервера «" + sv.Name + "»"
		if net.ParseIP(sv.Address) != nil {
			want = []string{sv.Address}
		} else if a, err := s.lookup(ctx, sv.Address); err == nil {
			want = a
		}
	}
	addrs, err := s.lookup(ctx, d)
	if (err != nil || len(addrs) == 0) && dnsPending {
		res.OK = true
		res.Pending = true
		res.Message = "запись только что создана, DNS её ещё не показывает. Ноду можно ставить сразу, но первые минуты она может быть недоступна, пока обновится DNS и выпустится сертификат"
		return res, nil
	}
	if err != nil || len(addrs) == 0 {
		return fail("у домена нет A-записи: создайте запись типа A на адрес %s и повторите проверку (после смены DNS может пройти несколько минут)", entryName)
	}
	res.Addresses = append([]string{}, addrs...) // never null in JSON: the page reads .length
	res.Main = append([]string{}, want...)
	if len(want) == 0 {
		res.OK = true
		res.Message = "домен свободен, но адрес " + entryName + " определить не удалось, DNS не сверен"
		return res, nil
	}
	if !sharesAny(addrs, want) {
		for _, sv := range servers {
			if sv.ID == entryServerID {
				continue
			}
			other := []string{sv.Address}
			if net.ParseIP(sv.Address) == nil {
				other, _ = s.lookup(ctx, sv.Address)
			}
			if sv.Address != "" && sharesAny(addrs, other) {
				res.UsedBy = "сервер " + sv.Name
				return fail("домен указывает на другой сервер — «%s» (%s), а нужен адрес %s (%s): исправьте A-запись", sv.Name, strings.Join(addrs, ", "), entryName, strings.Join(want, ", "))
			}
		}
		return fail("домен указывает на %s, а адрес %s — %s: исправьте A-запись", strings.Join(addrs, ", "), entryName, strings.Join(want, ", "))
	}
	res.OK = true
	res.Message = fmt.Sprintf("домен свободен и указывает на %s (%s)", entryName, strings.Join(addrs, ", "))
	return res, nil
}

func serverByID(list []model.Server, id string) (model.Server, bool) {
	for _, sv := range list {
		if sv.ID == id {
			return sv, true
		}
	}
	return model.Server{}, false
}

// internalPort picks the loopback port of a new domain's Xray inbound on a server (8444 and up).
func internalPort(all []model.Inbound, reserved []int, serverID string) int {
	used := map[int]bool{mainRealityPort: true}
	for _, p := range reserved {
		used[p] = true
	}
	for _, in := range all {
		if in.ServerID == serverID {
			used[in.Port] = true
		}
	}
	for p := mainRealityPort + 1; p < mainRealityPort+500; p++ {
		if !used[p] {
			return p
		}
	}
	return 0
}

// syncSNI makes the entry server's nginx (and, on the main server, Caddy) serve exactly the domains of the cascades that
// enter there.
func (s *Service) syncSNI(ctx context.Context, entryServerID string) error {
	list, err := s.Store.ListCascades(ctx)
	if err != nil {
		return err
	}
	return s.syncSNIWith(ctx, entryServerID, list)
}

// syncSNILocked is syncSNI of the main server.
func (s *Service) syncSNILocked(ctx context.Context) error { return s.syncSNI(ctx, "") }

func (s *Service) syncSNIWith(ctx context.Context, entryServerID string, list []model.Cascade) error {
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	var sites []front.Site
	for _, c := range list {
		if c.Domain == "" || c.EntryServerID != entryServerID {
			continue
		}
		if entry, _ := cascadeInbounds(c, all); entry != nil {
			sites = append(sites, front.Site{Domain: c.Domain, Port: entry.Port})
		}
	}
	for _, in := range all { // nodes of the main server on a domain of their own
		if in.NodeDomain != "" && in.ServerID == entryServerID {
			sites = append(sites, front.Site{Domain: in.NodeDomain, Port: in.Port})
		}
	}
	if entryServerID == "" {
		if s.SNI == nil {
			return nil
		}
		// The domain of the subscriptions, when it is not the panel's own, is a web site of Caddy that leads to the panel.
		if set, err := s.settings(ctx); err == nil && s.PanelListen != "" {
			if d := set.Subscription.Domain; d != "" && d != set.PanelDomain {
				sites = append(sites, front.Site{Domain: d, Proxy: s.PanelListen})
			}
		}
		return s.SNI.Apply(ctx, sites)
	}
	sv, err := s.Store.GetServer(ctx, entryServerID)
	if err != nil {
		return nil // the server is gone, so are its names
	}
	var ns []nodeinstall.SNISite
	for _, st := range sites {
		ns = append(ns, nodeinstall.SNISite{Domain: st.Domain, Port: st.Port})
	}
	var lines []string
	err = s.installer().SyncSNI(ctx, nodeTarget(*sv), ns, func(l string) { lines = append(lines, l) })
	if err != nil {
		if tail := strings.TrimSpace(strings.Join(lines, "\n")); tail != "" {
			return fmt.Errorf("%w: %s", err, tail)
		}
		return err
	}
	return nil
}

// RepairCascades finishes what an interrupted creation left half done, at start-up. A cascade is saved before its entry
// connection and linked to it after, so a request that died in between (the browser went away) leaves a cascade that
// points at nothing. When its connection was made the link is restored and the entry server's nginx told about the
// domain; when nothing was built the record is a leftover and goes.
func (s *Service) RepairCascades(ctx context.Context) error {
	list, err := s.Store.ListCascades(ctx)
	if err != nil {
		return err
	}
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.InboundID != "" {
			continue
		}
		var entry *model.Inbound
		for i := range all {
			if all[i].CascadeID == c.ID {
				entry = &all[i]
				break
			}
		}
		if entry == nil {
			if err := s.Store.DeleteCascade(ctx, c.ID); err == nil && s.Log != nil {
				s.Log.Warn("cascade: removed the leftover of an interrupted creation", "cascade", c.Name)
			}
			continue
		}
		c.InboundID = entry.ID
		if err := s.Store.PutCascade(ctx, c); err != nil {
			return err
		}
		if s.Log != nil {
			s.Log.Warn("cascade: finished an interrupted creation", "cascade", c.Name, "domain", c.Domain)
		}
		if err := s.syncSNI(ctx, c.EntryServerID); err != nil && s.Log != nil {
			s.Log.Warn("cascade: the domain could not be added to nginx", "cascade", c.Name, "err", err)
		}
		s.regruAssign(ctx, c.Domain, "", c.ID)
	}
	return nil
}

// SyncSNI is syncSNI for start-up: it restores the main server's files if they were lost. Without cascades that have a
// domain of their own there it leaves nginx and Caddy untouched. A failure is only logged by the caller.
func (s *Service) SyncSNI(ctx context.Context) error {
	list, err := s.Store.ListCascades(ctx)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Domain != "" && c.EntryServerID == "" {
			return s.syncSNI(ctx, "")
		}
	}
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	for _, in := range all {
		if in.NodeDomain != "" && in.ServerID == "" {
			return s.syncSNI(ctx, "")
		}
	}
	return nil
}
