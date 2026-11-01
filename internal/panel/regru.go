package panel

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/nodeinstall"
	"anypanel2/internal/regru"
)

// The panel keeps the DNS of its domain in REG.RU: with an account connected, a node's REALITY domain can be created
// (an A record to the node's address) from the installer instead of by hand.

const regruTimeout = 30 * time.Second

var subLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,40}[a-z0-9])?$`)

// RegRuStatus is what the settings page shows about the REG.RU connection.
type RegRuStatus struct {
	Configured bool   `json:"configured"` // a login and a zone are saved
	Connected  bool   `json:"connected"`  // and REG.RU accepted them just now
	Username   string `json:"username"`
	Zone       string `json:"zone"`
	// ExtraZones are the other domains names can be created in, and ZoneProblems the ones REG.RU does not let the
	// panel manage ("example.org: …").
	ExtraZones   []string `json:"extraZones"`
	ZoneProblems []string `json:"zoneProblems"`
	PasswordSet  bool     `json:"passwordSet"`
	CertSet      bool     `json:"certSet"`
	Error        string   `json:"error,omitempty"`
	// Unused are the names the panel created that nothing uses any more (left from an abandoned installation, or whose
	// record could not be removed together with the node): the cleanup button removes them.
	Unused []string `json:"unused"`
	// ServerIP is this machine's address as REG.RU sees it, shown when REG.RU refuses it: that is the address to allow
	// in the account's API settings.
	ServerIP string `json:"serverIp,omitempty"`
}

// regruClients builds one client per managed zone from the saved settings: the default zone first, then the extra ones.
func (s *Service) regruClients(ctx context.Context) ([]*regru.Client, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return nil, err
	}
	if set.RegRu.Username == "" || set.RegRu.Zone == "" || sec.RegRuPassword == "" {
		return nil, invalid("аккаунт reg.ru не подключён: укажите логин, пароль API и домен в Настройках")
	}
	var out []*regru.Client
	for _, zone := range append([]string{set.RegRu.Zone}, set.RegRu.ExtraZones...) {
		c, err := regru.New(regru.Config{
			Username: set.RegRu.Username, Password: sec.RegRuPassword, Zone: zone,
			CertPEM: sec.RegRuCert, KeyPEM: sec.RegRuKey, BaseURL: s.RegRuBase,
		})
		if err != nil {
			return nil, invalid("%v", err)
		}
		out = append(out, c)
	}
	return out, nil
}

// regruClient is the client of the default zone.
func (s *Service) regruClient(ctx context.Context) (*regru.Client, error) {
	cs, err := s.regruClients(ctx)
	if err != nil {
		return nil, err
	}
	return cs[0], nil
}

// regruClientFor is the client of one of the managed zones; "" means the default one.
func (s *Service) regruClientFor(ctx context.Context, zone string) (*regru.Client, error) {
	cs, err := s.regruClients(ctx)
	if err != nil {
		return nil, err
	}
	zone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
	if zone == "" {
		return cs[0], nil
	}
	for _, c := range cs {
		if c.Zone() == zone {
			return c, nil
		}
	}
	return nil, invalid("домен %s не подключён к панели: добавьте его в Настройках", zone)
}

// RegRuStatus reads the saved connection and tests it against REG.RU.
func (s *Service) RegRuStatus(ctx context.Context) (RegRuStatus, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return RegRuStatus{}, err
	}
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return RegRuStatus{}, err
	}
	st := RegRuStatus{
		Username: set.RegRu.Username, Zone: set.RegRu.Zone, ExtraZones: set.RegRu.ExtraZones, ZoneProblems: []string{}, PasswordSet: sec.RegRuPassword != "", CertSet: sec.RegRuCert != "" && sec.RegRuKey != "",
	}
	st.Configured = st.Username != "" && st.Zone != "" && st.PasswordSet
	st.Unused = []string{}
	if st.Configured {
		if orphans, err := s.regruOrphans(ctx); err == nil {
			for _, o := range orphans {
				st.Unused = append(st.Unused, o.Domain)
			}
		}
	}
	if !st.Configured {
		return st, nil
	}
	c, err := s.regruClient(ctx)
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	cctx, cancel := context.WithTimeout(ctx, regruTimeout)
	defer cancel()
	if err := c.Check(cctx); err != nil {
		st.Error = regru.Describe(err)
		var ae *regru.Error
		if errors.As(err, &ae) && ae.Code == "ACCESS_DENIED_FROM_IP" {
			st.ServerIP, _ = s.publicIP(ctx)
		}
		return st, nil
	}
	st.Connected = true
	// The other domains of the account must be manageable too; each that is not is reported next to the connection.
	if cs, err := s.regruClients(ctx); err == nil {
		for _, ec := range cs[1:] {
			if err := ec.Check(cctx); err != nil {
				st.ZoneProblems = append(st.ZoneProblems, ec.Zone()+": "+regru.Describe(err))
			}
		}
	}
	return st, nil
}

func (s *Service) publicIP(ctx context.Context) (string, error) {
	if s.PublicIP != nil {
		return s.PublicIP(ctx)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	ip := strings.TrimSpace(string(b))
	if _, perr := netip.ParseAddr(ip); err != nil || perr != nil {
		return "", errors.New("address not found")
	}
	return ip, nil
}

// RegRuSave is the connection form. Empty Password, Cert and Key keep what is stored; ClearCert drops the certificate.
type RegRuSave struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Zone     string `json:"zone"`
	// ExtraZones are more domains of the same account that names can be created in (the form takes them separated by
	// commas or spaces).
	ExtraZones []string `json:"extraZones"`
	Cert       string   `json:"cert"`
	Key        string   `json:"key"`
	ClearCert  bool     `json:"clearCert"`
}

// SaveRegRu stores the connection and reports whether REG.RU accepts it. It is saved even when REG.RU refuses it, so
// the address of this server can be allowed in the account first and the connection tested again.
func (s *Service) SaveRegRu(ctx context.Context, in RegRuSave) (RegRuStatus, error) {
	user := strings.TrimSpace(in.Username)
	zone := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Zone), "."))
	if user == "" {
		return RegRuStatus{}, invalid("укажите логин reg.ru")
	}
	if !nodeinstall.ValidDomain(zone) || !strings.Contains(zone, ".") {
		return RegRuStatus{}, invalid("укажите домен, чьей DNS-зоной управляет панель, например example.com")
	}
	extra := []string{}
	for _, z := range in.ExtraZones {
		z = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z), "."))
		switch {
		case z == "" || z == zone || slices.Contains(extra, z):
		case !nodeinstall.ValidDomain(z) || !strings.Contains(z, "."):
			return RegRuStatus{}, invalid("«%s» не похоже на домен, например example.org", z)
		default:
			extra = append(extra, z)
		}
	}
	if len(extra) > 10 {
		return RegRuStatus{}, invalid("не больше 10 дополнительных доменов")
	}
	cert, key := strings.TrimSpace(in.Cert), strings.TrimSpace(in.Key)
	if (cert == "") != (key == "") {
		return RegRuStatus{}, invalid("сертификат и ключ нужны вместе: вставьте оба или ни одного")
	}

	s.secretsMu.Lock()
	sec, err := s.Store.GetSecrets(ctx)
	if err == nil {
		if in.Password != "" {
			sec.RegRuPassword = in.Password
		}
		switch {
		case in.ClearCert:
			sec.RegRuCert, sec.RegRuKey = "", ""
		case cert != "":
			sec.RegRuCert, sec.RegRuKey = cert+"\n", key+"\n"
		}
		switch {
		case sec.RegRuPassword == "":
			err = invalid("укажите пароль API (альтернативный пароль из настроек API reg.ru)")
		default:
			// Only certificate and key that really form a pair are stored.
			if _, cerr := regru.New(regru.Config{Username: user, Password: sec.RegRuPassword, Zone: zone, CertPEM: sec.RegRuCert, KeyPEM: sec.RegRuKey}); cerr != nil {
				err = invalid("%v", cerr)
			}
		}
		if err == nil {
			err = s.Store.PutSecrets(ctx, *sec)
		}
	}
	s.secretsMu.Unlock()
	if err != nil {
		return RegRuStatus{}, err
	}

	s.mu.Lock()
	set, err := s.settings(ctx)
	if err == nil {
		set.RegRu.Username, set.RegRu.Zone, set.RegRu.ExtraZones = user, zone, extra // the list of created names stays
		err = s.Store.PutSettings(ctx, *set)
	}
	s.mu.Unlock()
	if err != nil {
		return RegRuStatus{}, err
	}
	return s.RegRuStatus(ctx)
}

// DisconnectRegRu forgets the login, password, certificate and zone.
func (s *Service) DisconnectRegRu(ctx context.Context) error {
	s.secretsMu.Lock()
	sec, err := s.Store.GetSecrets(ctx)
	if err == nil {
		sec.RegRuPassword, sec.RegRuCert, sec.RegRuKey = "", "", ""
		err = s.Store.PutSecrets(ctx, *sec)
	}
	s.secretsMu.Unlock()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	set, err := s.settings(ctx)
	if err != nil {
		return err
	}
	set.RegRu = model.RegRuSettings{}
	return s.Store.PutSettings(ctx, *set)
}

// RegRuDomain is an existing subdomain of the zone that points at an address.
type RegRuDomain struct {
	Name string `json:"name"` // the full name, sub.example.com
	IP   string `json:"ip"`
}

// RegRuDomains lists the zone's A records that can serve as a node's domain. The names the panel itself uses (its
// own domain, REALITY and subscription domains) are left out.
func (s *Service) RegRuDomains(ctx context.Context) ([]RegRuDomain, error) {
	cs, err := s.regruClients(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, regruTimeout)
	defer cancel()
	own := map[string]bool{}
	for _, d := range []string{set.PanelDomain, set.RealityDomain, set.Subscription.Domain} {
		own[strings.ToLower(d)] = true
	}
	out := []RegRuDomain{}
	var firstErr error
	failed := 0
	for _, c := range cs {
		rs, err := c.Records(cctx)
		if err != nil { // the settings page reports a zone that cannot be read; the others still work
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, r := range rs {
			if r.Type != "A" || r.Sub == "@" || r.Sub == "*" || strings.HasPrefix(r.Sub, "_") {
				continue
			}
			if name := c.FQDN(r.Sub); !own[name] {
				out = append(out, RegRuDomain{Name: name, IP: r.Content})
			}
		}
	}
	if failed == len(cs) {
		return nil, invalid("%s", regru.Describe(firstErr))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RegRuGenerateRequest says which address the new name must point at: Host (an IP or a name to resolve), or the
// server ServerID, or, with both empty, the main server. Label is an optional wish for the name's first part.
type RegRuGenerateRequest struct {
	Host     string `json:"host"`
	ServerID string `json:"serverId"`
	Label    string `json:"label"`
	Zone     string `json:"zone"` // the domain to create the name in; empty = the default one
}

// RegRuGenerated is the result: the name and the address it now points at.
type RegRuGenerated struct {
	Domain  string `json:"domain"`
	IP      string `json:"ip"`
	Created bool   `json:"created"` // false when the name already pointed at that address
}

var nameWords = []string{
	"alder", "amber", "aspen", "birch", "brook", "cedar", "cliff", "cloud", "coral", "creek", "dawn", "delta", "dune", "ember",
	"fern", "field", "frost", "glade", "grove", "harbor", "hazel", "heath", "iris", "island", "juniper", "lake", "larch",
	"maple", "meadow", "mist", "moss", "north", "oak", "ocean", "olive", "pine", "plain", "pond", "quartz", "reed", "ridge",
	"river", "sage", "shore", "slate", "spruce", "stone", "summit", "tide", "vale", "willow", "wind",
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

// regruTarget finds the IPv4 (or, failing that, IPv6) address the new name must point at.
func (s *Service) regruTarget(ctx context.Context, req RegRuGenerateRequest) (string, error) {
	host := strings.TrimSpace(req.Host)
	if host == "" {
		if req.ServerID != "" {
			sv, err := s.Store.GetServer(ctx, req.ServerID)
			if err != nil {
				return "", err
			}
			host = sv.Address
		} else {
			set, err := s.settings(ctx)
			if err != nil {
				return "", err
			}
			for _, h := range []string{set.PanelDomain, set.RealityDomain} {
				if h != "" {
					host = h
					break
				}
			}
			if host == "" {
				return "", invalid("адрес основного сервера неизвестен: задайте домен панели в Настройках")
			}
		}
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return a.String(), nil
	}
	addrs, err := s.lookup(ctx, host)
	if err != nil || len(addrs) == 0 {
		return "", invalid("не удалось узнать IP-адрес «%s»", host)
	}
	best := addrs[0]
	for _, a := range addrs {
		if p, err := netip.ParseAddr(a); err == nil && p.Is4() {
			best = a
			break
		}
	}
	return best, nil
}

// GenerateRegRuDomain creates a name in the zone with an A record to the target address and returns it. A random
// name like "cedar417" is chosen unless a label is given; an existing label is reused only if it already points
// at the address.
func (s *Service) GenerateRegRuDomain(ctx context.Context, req RegRuGenerateRequest) (*RegRuGenerated, error) {
	c, err := s.regruClientFor(ctx, req.Zone)
	if err != nil {
		return nil, err
	}
	ip, err := s.regruTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	typ := regru.Kind(ip)
	label := strings.ToLower(strings.TrimSpace(req.Label))
	if label != "" && !subLabelRe.MatchString(label) {
		return nil, invalid("имя поддомена: латинские буквы, цифры и дефис, например node1")
	}

	s.regruMu.Lock()
	defer s.regruMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, regruTimeout)
	defer cancel()
	rs, err := c.Records(cctx)
	if err != nil {
		return nil, invalid("%s", regru.Describe(err))
	}
	taken := map[string][]regru.Record{}
	for _, r := range rs {
		taken[strings.ToLower(r.Sub)] = append(taken[strings.ToLower(r.Sub)], r)
	}
	if label != "" {
		for _, r := range taken[label] {
			if r.Type == typ && r.Content == ip {
				return &RegRuGenerated{Domain: c.FQDN(label), IP: ip}, nil
			}
		}
		if len(taken[label]) > 0 {
			return nil, invalid("имя %s уже занято в зоне другой записью", c.FQDN(label))
		}
	} else {
		for range 20 {
			cand := fmt.Sprintf("%s%d", nameWords[randInt(len(nameWords))], 10+randInt(990))
			if len(taken[cand]) == 0 {
				label = cand
				break
			}
		}
		if label == "" {
			return nil, errors.New("не удалось подобрать свободное имя, попробуйте ещё раз")
		}
	}
	if err := c.Add(cctx, typ, label, ip); err != nil {
		return nil, invalid("reg.ru не создал запись: %s", regru.Describe(err))
	}
	if s.Log != nil {
		s.Log.Info("reg.ru: created a domain for a node", "domain", c.FQDN(label), "ip", ip)
	}
	if err := s.regruTrack(ctx, c.FQDN(label), ip); err != nil && s.Log != nil {
		s.Log.Warn("reg.ru: the new domain could not be remembered, its record will not be removed automatically", "domain", c.FQDN(label), "err", err)
	}
	return &RegRuGenerated{Domain: c.FQDN(label), IP: ip, Created: true}, nil
}
