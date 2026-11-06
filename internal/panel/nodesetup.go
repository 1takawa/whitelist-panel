package panel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"anypanel2/internal/agent"
	"anypanel2/internal/model"
	"anypanel2/internal/nodeinstall"
	"anypanel2/internal/xray"
)

// NodeInstaller is the SSH side of node setup (implemented by realInstaller; replaced in tests).
type NodeInstaller interface {
	Check(ctx context.Context, t nodeinstall.Target, agentPort, connectPort int, domain string) (*nodeinstall.Report, error)
	SetupReality(ctx context.Context, t nodeinstall.Target, domain string, freePorts []int, log func(string)) error
	Install(ctx context.Context, o nodeinstall.Options, log func(string)) (*nodeinstall.Result, error)
	Upgrade(ctx context.Context, t nodeinstall.Target, log func(string)) error
	OpenFirewall(ctx context.Context, t nodeinstall.Target, agentPort int, ports []int, log func(string)) error
	// SyncSNI makes the node's nginx send exactly these extra REALITY domains to their own Xray ports.
	SyncSNI(ctx context.Context, t nodeinstall.Target, sites []nodeinstall.SNISite, log func(string)) error
}

type realInstaller struct{ binary func() (string, error) }

func (r realInstaller) servable() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("панель запущена не на Linux x86_64 (%s/%s): агент для нового сервера берётся из самого бинарника панели", runtime.GOOS, runtime.GOARCH)
	}
	return nil
}

func (r realInstaller) Check(ctx context.Context, t nodeinstall.Target, agentPort, connectPort int, domain string) (*nodeinstall.Report, error) {
	rep, err := nodeinstall.Check(ctx, t, agentPort, connectPort, domain)
	if err != nil {
		return nil, err
	}
	if err := r.servable(); err != nil {
		rep.Problems = append(rep.Problems, err.Error())
		rep.OK = false
	}
	return rep, nil
}

func (r realInstaller) Install(ctx context.Context, o nodeinstall.Options, log func(string)) (*nodeinstall.Result, error) {
	if err := r.servable(); err != nil {
		return nil, err
	}
	bin, err := r.binary()
	if err != nil {
		return nil, err
	}
	o.Binary = bin
	return nodeinstall.Install(ctx, o, log)
}

func (r realInstaller) Upgrade(ctx context.Context, t nodeinstall.Target, log func(string)) error {
	if err := r.servable(); err != nil {
		return err
	}
	bin, err := r.binary()
	if err != nil {
		return err
	}
	return nodeinstall.Upgrade(ctx, t, bin, log)
}

func (s *Service) installer() NodeInstaller {
	if s.Installer != nil {
		return s.Installer
	}
	return realInstaller{binary: os.Executable}
}

// ---- requests ----

// SSHAccess is how the admin reaches a fresh server. The password is used for the installation only.
type SSHAccess struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password"`
	PrivateKey string `json:"privateKey"` // PEM, instead of a password; used for the installation only
	HostKey    string `json:"hostKey"`    // host key fingerprint from the check; a different key is refused
}

// NodeInstallRequest is the wizard's form.
type NodeInstallRequest struct {
	SSHAccess
	Name          string `json:"name"`          // the node's name in the panel and in subscription link names
	Country       string `json:"country"`       // two-letter code
	RealityDomain string `json:"realityDomain"` // the node's own domain (an A record to its address): REALITY site, certificate and SNI
	AgentPort     int    `json:"agentPort"`
	ConnectPort   int    `json:"connectPort"`   // port of the VLESS REALITY connection created on the node
	CreateInbound bool   `json:"createInbound"` // create that connection
	AddClients    bool   `json:"addClients"`    // attach every existing client to it
	ForcePort     bool   `json:"forcePort"`     // stop whatever listens on ports 80 and 443 before installing (and disable its service)
	SkipDNSCheck  bool   `json:"skipDnsCheck"`  // the A record of the domain is not there yet: the installation waits for it instead of refusing
}

var sshUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (a *SSHAccess) normalize(needAuth bool) error {
	a.Host = strings.ToLower(strings.TrimSpace(a.Host))
	if a.Host == "" || !isHost(a.Host) {
		return invalid("SSH address must be a domain name or an IP address")
	}
	if a.Port == 0 {
		a.Port = 22
	}
	if a.Port < 1 || a.Port > 65535 {
		return invalid("SSH port must be 1–65535")
	}
	a.User = strings.TrimSpace(a.User)
	if a.User == "" {
		a.User = "root"
	}
	if !sshUserRe.MatchString(a.User) {
		return invalid("SSH user name is not valid")
	}
	a.PrivateKey = strings.TrimSpace(a.PrivateKey)
	if needAuth && a.Password == "" && a.PrivateKey == "" {
		return invalid("enter the SSH password or a private key")
	}
	if len(a.Password) > 256 {
		return invalid("password is too long")
	}
	if len(a.PrivateKey) > 16<<10 || (a.PrivateKey != "" && !strings.HasPrefix(a.PrivateKey, "-----BEGIN ")) {
		return invalid("the private key must be a PEM file (-----BEGIN … PRIVATE KEY-----)")
	}
	a.HostKey = strings.ToLower(strings.TrimSpace(a.HostKey))
	if a.HostKey != "" && !fingerprintRe.MatchString(a.HostKey) {
		return invalid("host key fingerprint must be 64 hexadecimal characters")
	}
	return nil
}

func (a SSHAccess) target() nodeinstall.Target {
	return nodeinstall.Target{Host: a.Host, Port: a.Port, User: a.User, Password: a.Password, PrivateKey: a.PrivateKey, HostKey: a.HostKey}
}

func defaultPorts(agentPort, connectPort int) (int, int, error) {
	if agentPort == 0 {
		agentPort = agent.DefaultPort
	}
	if connectPort == 0 {
		connectPort = 443
	}
	if agentPort < 1 || agentPort > 65535 || connectPort < 1 || connectPort > 65535 {
		return 0, 0, invalid("ports must be 1–65535")
	}
	if agentPort == connectPort || connectPort == agent.APIPort {
		return 0, 0, invalid("the connection port must differ from the agent port and from %d", agent.APIPort)
	}
	return agentPort, connectPort, nil
}

// SSHCheck logs in and inspects the machine without changing anything.
func (s *Service) SSHCheck(ctx context.Context, a SSHAccess, agentPort, connectPort int, domain string) (*nodeinstall.Report, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain != "" && !nodeinstall.ValidDomain(domain) {
		return nil, invalid("REALITY domain must be a domain name such as reality.example.com")
	}
	if err := a.normalize(true); err != nil {
		return nil, err
	}
	agentPort, connectPort, err := defaultPorts(agentPort, connectPort)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	rep, err := s.installer().Check(cctx, a.target(), agentPort, connectPort, domain)
	if err != nil {
		return nil, invalid("%v", err)
	}
	return rep, nil
}

// ---- jobs ----

// JobView is the progress of an installation or upgrade, polled by the UI.
type JobView struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`   // install | upgrade
	Status   string   `json:"status"` // running | done | failed
	Log      []string `json:"log"`
	Warnings []string `json:"warnings"`
	Error    string   `json:"error,omitempty"`
	ServerID string   `json:"serverId,omitempty"`
}

type job struct {
	mu      sync.Mutex
	v       JobView
	host    string
	started time.Time
}

func (j *job) logf(format string, a ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.v.Log) < 500 {
		j.v.Log = append(j.v.Log, fmt.Sprintf(format, a...))
	}
}

func (j *job) warn(msg string) {
	j.mu.Lock()
	j.v.Warnings = append(j.v.Warnings, msg)
	j.mu.Unlock()
	j.logf("!! %s", msg)
}

func (j *job) finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err != nil {
		j.v.Status, j.v.Error = "failed", err.Error()
		return
	}
	j.v.Status = "done"
}

func (j *job) view() JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := j.v
	v.Log = append([]string{}, j.v.Log...)
	v.Warnings = append([]string{}, j.v.Warnings...)
	return v
}

// NodeJob returns a job's progress.
func (s *Service) NodeJob(id string) (*JobView, bool) {
	s.jobsMu.Lock()
	j := s.jobs[id]
	s.jobsMu.Unlock()
	if j == nil {
		return nil, false
	}
	v := j.view()
	return &v, true
}

// startJob registers a job unless one is already running for the host.
func (s *Service) startJob(kind, host string) (*job, error) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.jobs == nil {
		s.jobs = map[string]*job{}
	}
	now := time.Now()
	for id, j := range s.jobs {
		v := j.view()
		if v.Status != "running" && now.Sub(j.started) > time.Hour {
			delete(s.jobs, id)
		} else if v.Status == "running" && j.host == host {
			return nil, invalid("an installation on %s is already running", host)
		}
	}
	j := &job{host: host, started: now, v: JobView{ID: newID(), Kind: kind, Status: "running", Log: []string{}, Warnings: []string{}}}
	s.jobs[j.v.ID] = j
	return j, nil
}

// run executes fn in the background; a panic or timeout ends the job as failed instead of the process.
func (s *Service) runJob(j *job, timeout time.Duration, fn func(ctx context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				j.finish(fmt.Errorf("внутренняя ошибка: %v", r))
			}
		}()
		j.finish(fn(ctx))
	}()
}

// ---- install ----

// StartNodeInstall validates the form and starts the installation in the background.
func (s *Service) StartNodeInstall(ctx context.Context, req NodeInstallRequest) (*JobView, error) {
	if err := req.SSHAccess.normalize(true); err != nil {
		return nil, err
	}
	var err error
	if req.AgentPort, req.ConnectPort, err = defaultPorts(req.AgentPort, req.ConnectPort); err != nil {
		return nil, err
	}
	sv := model.Server{Name: req.Name, Address: req.Host, Port: req.AgentPort, Country: req.Country}
	if err := normalizeServer(&sv); err != nil {
		return nil, err
	}
	req.Name, req.Country = sv.Name, sv.Country
	req.RealityDomain = strings.ToLower(strings.TrimSpace(req.RealityDomain))
	if req.CreateInbound {
		if !nodeinstall.ValidDomain(req.RealityDomain) {
			return nil, invalid("REALITY domain must be the node's own domain name such as reality.example.com (an A record pointing at the server)")
		}
		if req.ConnectPort != 443 {
			return nil, invalid("with the REALITY site nginx takes clients on port 443: the connection port is always 443")
		}
	}
	existing, err := s.Store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	if len(existing) >= 50 {
		return nil, invalid("at most 50 servers")
	}
	for _, e := range existing {
		if e.Address == sv.Address && e.Port == sv.Port {
			return nil, invalid("this server is already registered as %q", e.Name)
		}
	}

	j, err := s.startJob("install", req.Host)
	if err != nil {
		return nil, err
	}
	limit := 15 * time.Minute
	if req.SkipDNSCheck && req.RealityDomain != "" {
		limit = 30 * time.Minute // the installer may wait up to 12 minutes for the record
	}
	s.runJob(j, limit, func(ctx context.Context) error { return s.installNode(ctx, j, req) })
	v := j.view()
	return &v, nil
}

func (s *Service) installNode(ctx context.Context, j *job, req NodeInstallRequest) error {
	token := randString(alnum, 48)
	res, err := s.installer().Install(ctx, nodeinstall.Options{
		Target:        req.SSHAccess.target(),
		Token:         token,
		AgentPort:     req.AgentPort,
		CertHost:      req.Host,
		KeyName:       "anypanel-" + req.Host,
		FreePorts:     freePorts(req),
		RealityDomain: realityDomainFor(req),
		SkipDNSCheck:  req.SkipDNSCheck,
	}, func(line string) { j.logf("%s", line) })
	if err != nil {
		return err
	}

	sv := model.Server{
		ID: newID(), Name: req.Name, Address: req.Host, Port: req.AgentPort, Enable: true, Country: req.Country,
		Token: token, Fingerprint: res.Fingerprint, CreatedAt: s.Now().UTC(),
		SSHPort: req.Port, SSHUser: req.User, SSHHostKey: res.HostKey, SSHKey: res.PrivateKey,
	}
	if err := normalizeServer(&sv); err != nil {
		return err
	}
	if err := s.Store.PutServer(ctx, sv); err != nil {
		return fmt.Errorf("агент установлен, но сервер не сохранён в панели: %w", err)
	}
	j.mu.Lock()
	j.v.ServerID = sv.ID
	j.mu.Unlock()
	j.logf("Сервер добавлен в панель: %s", sv.Name)
	if req.RealityDomain != "" {
		s.regruAssign(ctx, req.RealityDomain, sv.ID, "") // a domain the panel made goes away with the node
	}

	j.logf("Проверяю связь панели с агентом…")
	if v, err := s.TestServer(ctx, sv.ID); err != nil {
		j.warn("проверить связь не удалось: " + err.Error())
	} else if v == nil || !v.Online {
		reason := ""
		if v != nil {
			reason = v.Error
		}
		j.logf("Панель не достучалась до агента (%s). Пробую открыть порты в файрволе сервера по SSH…", reason)
		var ports []int
		if req.CreateInbound {
			ports = []int{80, 443} // nginx: the certificate and the clients
		}
		if sv.SSHKey == "" {
			j.warn(fmt.Sprintf("откройте порт %d/tcp для главного сервера в файрволе, затем нажмите «Проверить связь»", sv.Port))
		} else if err := s.openNodePorts(ctx, j, sv, ports); err != nil {
			j.warn(fmt.Sprintf("открыть порты не получилось (%v). Откройте порт %d/tcp для главного сервера в файрволе, затем нажмите «Проверить связь»", err, sv.Port))
		}
	} else {
		j.logf("Агент отвечает: версия %s, пинг %d мс", v.AgentVersion, v.PingMs)
	}

	if !req.CreateInbound {
		return nil
	}
	in, err := nodeRealityInbound(sv, req.RealityDomain)
	if err != nil {
		return fmt.Errorf("агент установлен и добавлен, но подключение собрать не удалось: %w", err)
	}
	j.logf("Создаю подключение VLESS REALITY по схеме с nginx: порт %d за nginx, target %s, SNI %s…", in.Port, nodeDecoyTarget, req.RealityDomain)
	created, err := s.CreateInboundFor(ctx, in, false) // the wizard has its own "add all clients" switch
	var ae *ApplyError
	switch {
	case errors.As(err, &ae):
		j.warn("подключение сохранено, но пока не применено на сервере: " + ae.Err.Error())
	case err != nil:
		return fmt.Errorf("агент установлен и добавлен, но подключение создать не удалось: %w", err)
	}
	if created != nil && req.AddClients {
		n, err := s.attachAllClients(ctx, created.ID)
		if err != nil && !errors.As(err, &ae) {
			j.warn("клиентов подключить не удалось: " + err.Error())
		} else {
			j.logf("К подключению добавлено клиентов: %d", n)
		}
	}
	return nil
}

// nodeRemark is the inbound name of a freshly installed node: "🇩🇪 Frankfurt".
func nodeRemark(sv model.Server) string {
	r := strings.TrimSpace(sv.Flag() + " " + sv.Name)
	if rs := []rune(r); len(rs) > 64 {
		r = string(rs[:64])
	}
	return r
}

// attachAllClients adds the inbound to every client and returns how many were changed.
func (s *Service) attachAllClients(ctx context.Context, inboundID string) (int, error) {
	return s.attachClients(ctx, inboundID, func(model.Client) bool { return true })
}

// ---- upgrade ----

// StartNodeUpgrade replaces the agent on a node the panel installed, using the SSH key it created.
func (s *Service) StartNodeUpgrade(ctx context.Context, id string) (*JobView, error) {
	sv, err := s.Store.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	if sv.SSHKey == "" {
		return nil, invalid("this server was added manually: the panel has no SSH access to it. Upgrade the agent on the server with install-node.sh upgrade")
	}
	j, err := s.startJob("upgrade", sv.Address)
	if err != nil {
		return nil, err
	}
	t := nodeinstall.Target{Host: sv.Address, Port: sv.SSHPort, User: sv.SSHUser, PrivateKey: sv.SSHKey, HostKey: sv.SSHHostKey}
	j.v.ServerID = sv.ID
	s.runJob(j, 10*time.Minute, func(ctx context.Context) error {
		if err := s.installer().Upgrade(ctx, t, func(line string) { j.logf("%s", line) }); err != nil {
			return err
		}
		j.logf("Проверяю связь с агентом…")
		s.srvMu.Lock()
		if rt := s.srv[sv.ID]; rt != nil {
			rt.retryAt = time.Time{}
		}
		s.srvMu.Unlock()
		if v, err := s.TestServer(ctx, sv.ID); err == nil && v != nil && v.Online {
			j.logf("Агент работает, версия %s", v.AgentVersion)
		} else {
			j.warn("после обновления агент пока не отвечает: проверьте связь через минуту")
		}
		return nil
	})
	v := j.view()
	return &v, nil
}

func (r realInstaller) OpenFirewall(ctx context.Context, t nodeinstall.Target, agentPort int, ports []int, log func(string)) error {
	return nodeinstall.OpenFirewall(ctx, t, agentPort, ports, log)
}

func (r realInstaller) SyncSNI(ctx context.Context, t nodeinstall.Target, sites []nodeinstall.SNISite, log func(string)) error {
	return nodeinstall.SyncSNI(ctx, t, sites, log)
}

// ---- firewall ----

func nodeTarget(sv model.Server) nodeinstall.Target {
	return nodeinstall.Target{Host: sv.Address, Port: sv.SSHPort, User: sv.SSHUser, PrivateKey: sv.SSHKey, HostKey: sv.SSHHostKey}
}

// openNodePorts opens the agent port and the given connection ports in the node's firewall over SSH, then asks the
// agent for its status again so the server shows as online at once.
func (s *Service) openNodePorts(ctx context.Context, j *job, sv model.Server, ports []int) error {
	if err := s.installer().OpenFirewall(ctx, nodeTarget(sv), sv.Port, ports, func(line string) { j.logf("%s", line) }); err != nil {
		return err
	}
	j.logf("Проверяю связь с агентом…")
	if v, err := s.TestServer(ctx, sv.ID); err == nil && v != nil && v.Online {
		j.logf("Агент отвечает: версия %s, пинг %d мс", v.AgentVersion, v.PingMs)
	} else {
		reason := ""
		if v != nil {
			reason = v.Error
		}
		j.warn("агент по-прежнему не отвечает (" + reason + "). Скорее всего, порт закрыт в файрволе у провайдера (облачные правила в личном кабинете): откройте там " + fmt.Sprint(sv.Port) + "/tcp для адреса панели")
	}
	return nil
}

// StartNodeFirewall opens the node's ports in ufw using the SSH key the panel created for it.
func (s *Service) StartNodeFirewall(ctx context.Context, id string) (*JobView, error) {
	sv, err := s.Store.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	if sv.SSHKey == "" {
		return nil, invalid("this server was added manually: the panel has no SSH access to it. Open the ports on the server yourself (ufw allow from <panel address> to any port %d proto tcp)", sv.Port)
	}
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	var ports []int
	add := func(p int) {
		if !slices.Contains(ports, p) {
			ports = append(ports, p)
		}
	}
	for _, in := range ins {
		if in.ServerID != sv.ID || !in.Enable {
			continue
		}
		if ip := net.ParseIP(in.Listen); ip != nil && ip.IsLoopback() {
			add(80) // behind the node's nginx (the REALITY site): clients and the certificate come through 80 and 443
			add(443)
		} else {
			add(in.Port)
		}
	}
	j, err := s.startJob("firewall", sv.Address)
	if err != nil {
		return nil, err
	}
	j.v.ServerID = sv.ID
	s.runJob(j, 3*time.Minute, func(ctx context.Context) error { return s.openNodePorts(ctx, j, *sv, ports) })
	v := j.view()
	return &v, nil
}

// freePorts are the ports to clear on the node before installing: nginx needs 80 (the certificate) and 443 (clients).
// Only a node that gets the REALITY site needs them.
func freePorts(req NodeInstallRequest) []int {
	if req.ForcePort && req.CreateInbound {
		return []int{80, 443}
	}
	return nil
}

// realityDomainFor is the domain the installer builds the REALITY site for, "" when no connection is created.
func realityDomainFor(req NodeInstallRequest) string {
	if req.CreateInbound {
		return req.RealityDomain
	}
	return ""
}

// The REALITY connection of a node, built the way the nginx-SNI installer builds it:
//   - Xray listens on 8443 behind nginx (which takes 443 and routes by SNI) and accepts the PROXY protocol nginx sends;
//   - target is the node's own decoy site, nginx on 127.0.0.1:9443 with a real certificate for the domain;
//   - server name is the node's domain, uTLS fingerprint firefox, spiderX "/", eight random short ids;
//   - sniffing is off, like in their database record (the panel keeps no destOverride list for a disabled sniffer).
const (
	nodeRealityPort = 8443
	nodeDecoyTarget = "127.0.0.1:9443"
	nodeShortIDs    = 8
)

func (r realInstaller) SetupReality(ctx context.Context, t nodeinstall.Target, domain string, freePorts []int, log func(string)) error {
	return nodeinstall.SetupReality(ctx, t, domain, freePorts, log)
}

// ---- REALITY site on a node that is already installed ----

// NodeRealityRequest is what the admin gives to rebuild an installed node the nginx-SNI way.
type NodeRealityRequest struct {
	Domain    string `json:"domain"`    // the node's own domain (an A record pointing at the server)
	ForcePort bool   `json:"forcePort"` // stop whatever listens on ports 80 and 443 first
}

// StartNodeReality builds the nginx-SNI scheme on a node the panel installed: nginx on 443, a certificate and a decoy
// site for the domain, and a new REALITY connection made like the installer makes it. The node's old REALITY
// connections on port 443 would fight nginx for the port, so they are replaced; the new one goes to every client.
func (s *Service) StartNodeReality(ctx context.Context, id string, req NodeRealityRequest) (*JobView, error) {
	sv, err := s.Store.GetServer(ctx, id)
	if err != nil {
		return nil, err
	}
	if sv.SSHKey == "" {
		return nil, invalid("this server was added manually: the panel has no SSH access to it")
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !nodeinstall.ValidDomain(req.Domain) {
		return nil, invalid("REALITY domain must be the node's own domain name such as reality.example.com (an A record pointing at the server)")
	}
	j, err := s.startJob("reality", sv.Address)
	if err != nil {
		return nil, err
	}
	j.v.ServerID = sv.ID
	s.runJob(j, 15*time.Minute, func(ctx context.Context) error { return s.setupNodeReality(ctx, j, *sv, req) })
	v := j.view()
	return &v, nil
}

func (s *Service) setupNodeReality(ctx context.Context, j *job, sv model.Server, req NodeRealityRequest) error {
	// 1. Free port 443 on the node by removing the connections that hold it (Xray itself listens there).
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return err
	}
	var old []model.Inbound
	for _, in := range ins {
		if in.ServerID == sv.ID && (in.Port == 443 || in.Port == nodeRealityPort) { // 443 would fight nginx; 8443 is the previous run of this action
			old = append(old, in)
		}
	}
	for _, in := range old {
		j.logf("Убираю прежнее подключение %q (порт 443): оно занимает порт, который нужен nginx", in.Remark)
		if err := s.DeleteInbound(ctx, in.ID); err != nil {
			var ae *ApplyError
			if !errors.As(err, &ae) {
				return fmt.Errorf("не удалось убрать прежнее подключение: %w", err)
			}
		}
	}

	// 2. nginx, certificate, decoy site.
	var free []int
	if req.ForcePort {
		free = []int{80, 443}
	}
	if err := s.installer().SetupReality(ctx, nodeTarget(sv), req.Domain, free, func(line string) { j.logf("%s", line) }); err != nil {
		return err
	}

	s.regruAssign(ctx, req.Domain, sv.ID, "")

	// 3. The new connection: like any new connection it goes into every client's subscription.
	in, err := nodeRealityInbound(sv, req.Domain)
	if err != nil {
		return err
	}
	j.logf("Создаю подключение VLESS REALITY по схеме с nginx: порт %d за nginx, target %s, SNI %s…", in.Port, nodeDecoyTarget, req.Domain)
	created, err := s.CreateInbound(ctx, in)
	var ae *ApplyError
	switch {
	case errors.As(err, &ae):
		j.warn("подключение сохранено, но пока не применено на сервере: " + ae.Err.Error())
	case err != nil:
		return fmt.Errorf("REALITY-сайт готов, но подключение создать не удалось: %w", err)
	}
	if created == nil {
		return nil
	}
	if cls, err := s.Store.ListClients(ctx); err == nil {
		n := 0
		for _, c := range cls {
			if c.Uses(created.ID) {
				n++
			}
		}
		j.logf("Подключение добавлено в подписки клиентов: %d", n)
	}
	s.srvMu.Lock()
	if rt := s.srv[sv.ID]; rt != nil {
		rt.retryAt = time.Time{}
	}
	s.srvMu.Unlock()
	return nil
}

// attachLocked adds the inbound to every client the filter accepts (the caller holds s.mu) and returns how many were
// changed. It only saves the clients: applying the result to Xray is the caller's job.
func (s *Service) attachLocked(ctx context.Context, inboundID string, accept func(model.Client) bool) (int, error) {
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range cls {
		if c.Uses(inboundID) || !accept(c) {
			continue
		}
		c.InboundIDs = append(append([]string{}, c.InboundIDs...), inboundID)
		if err := s.putClient(ctx, c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// attachClients is attachLocked for callers that do not hold the lock; it also applies the change.
func (s *Service) attachClients(ctx context.Context, inboundID string, accept func(model.Client) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.attachLocked(ctx, inboundID, accept)
	if err != nil || n == 0 {
		return n, err
	}
	return n, wrapApply(s.applyLocked(ctx, false))
}

// nodeRealityInbound is the REALITY connection of a node installed with the nginx-SNI scheme: behind nginx on 8443.
func nodeRealityInbound(sv model.Server, domain string) (model.Inbound, error) {
	in, err := newRealityInbound(nodeRemark(sv), sv.ID, "127.0.0.1", nodeRealityPort, nodeDecoyTarget, domain, true, 443)
	return in, err
}

// newRealityInbound builds a VLESS REALITY connection the way nginx-SNI does: uTLS firefox, spiderX "/", eight random short
// ids, sniffing off. With proxy set it accepts the PROXY protocol (it sits behind nginx); publicPort is the port clients
// connect to.
func newRealityInbound(remark, serverID, listen string, port int, target, sni string, proxy bool, publicPort int) (model.Inbound, error) {
	ids := make([]any, 0, nodeShortIDs)
	for i := 0; i < nodeShortIDs; i++ {
		id, err := xray.NewShortID()
		if err != nil {
			return model.Inbound{}, err
		}
		ids = append(ids, id)
	}
	stream := model.Doc{
		"network":     "tcp",
		"security":    "reality",
		"tcpSettings": map[string]any{"header": map[string]any{"type": "none"}},
		"realitySettings": map[string]any{
			"show": false, "xver": float64(0), "target": target,
			"serverNames": []any{sni},
			"privateKey":  "", "minClientVer": "", "maxClientVer": "", "maxTimediff": float64(0),
			"shortIds": ids,
			"settings": map[string]any{"publicKey": "", "fingerprint": "firefox", "spiderX": "/"},
		},
	}
	if proxy {
		stream["sockopt"] = map[string]any{"acceptProxyProtocol": true}
	}
	return model.Inbound{
		Remark: remark, Enable: true, Protocol: model.ProtoVLESS, Listen: listen, Port: port,
		ServerID: serverID, AcceptProxyProtocol: proxy, TrafficReset: model.ResetNever,
		Settings:       model.Doc{"decryption": "none"},
		StreamSettings: stream,
		Sniffing:       model.Sniffing{Enabled: false},
		Public:         model.Public{Port: publicPort},
	}, nil
}
