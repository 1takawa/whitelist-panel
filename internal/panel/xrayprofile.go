package panel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"anypanel2/internal/agent"
	"anypanel2/internal/model"
	"anypanel2/internal/xray"
)

// LocalServer is the id the API uses for the main server (the one the panel runs on).
const LocalServer = "local"

func machineID(id string) string {
	if id == LocalServer {
		return ""
	}
	return id
}

var (
	logLevels      = []string{"", "debug", "info", "warning", "error", "none"}
	domainStrategy = []string{"", "AsIs", "IPIfNonMatch", "IPOnDemand"}
)

func normalizeProfile(p *model.XrayProfile) error {
	if !slices.Contains(logLevels, p.LogLevel) {
		return invalid("log level must be debug, info, warning, error or none")
	}
	switch p.BitTorrent {
	case "", "block", "allow":
	default:
		return invalid("BitTorrent must be empty, block or allow")
	}
	if !slices.Contains(domainStrategy, p.DomainStrategy) {
		return invalid("routing domain strategy must be AsIs, IPIfNonMatch or IPOnDemand")
	}
	if len(p.Template) == 0 {
		p.Template = nil
		return nil
	}
	if _, ok := p.Template["inbounds"]; ok {
		return invalid("inbounds are managed by the panel and cannot be set in the template")
	}
	b, err := json.Marshal(map[string]any(p.Template))
	if err != nil {
		return invalid("template is not valid JSON")
	}
	if len(b) > maxDocBytes {
		return invalid("template is too large")
	}
	return nil
}

// machineInbounds returns the inbounds that run on one machine.
func (s *Service) machineInbounds(ctx context.Context, serverID string) ([]model.Inbound, error) {
	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Inbound
	for _, in := range all {
		if in.ServerID == serverID {
			out = append(out, in)
		}
	}
	return out, nil
}

func apiPortFor(s *Service, serverID string) int {
	if serverID == "" {
		return s.Opts.APIPort
	}
	return agent.APIPort
}

func (s *Service) GetXrayProfile(ctx context.Context, id string) (*model.XrayProfile, error) {
	p, err := s.profileFor(ctx, machineID(id))
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// PutXrayProfile validates the profile together with the machine's inbounds (Xray itself checks the result), saves it
// and applies it. The change reaches an extra server on the next sync, like any other.
func (s *Service) PutXrayProfile(ctx context.Context, id string, p model.XrayProfile) (*model.XrayProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mid := machineID(id)
	if err := normalizeProfile(&p); err != nil {
		return nil, err
	}
	ins, err := s.machineInbounds(ctx, mid)
	if err != nil {
		return nil, err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := s.buildConfigFor(ctx, ins, cls, apiPortFor(s, mid), p)
	if err != nil {
		return nil, err
	}
	if s.Xray.Installed() {
		if err := s.Xray.Check(ctx, cfg); err != nil {
			return nil, invalid("%v", err)
		}
	}
	if mid == "" {
		set, err := s.settings(ctx)
		if err != nil {
			return nil, err
		}
		set.XrayProfile = p
		if err := s.Store.PutSettings(ctx, *set); err != nil {
			return nil, err
		}
	} else {
		sv, err := s.Store.GetServer(ctx, mid)
		if err != nil {
			return nil, err
		}
		sv.XrayProfile = p
		if err := s.Store.PutServer(ctx, *sv); err != nil {
			return nil, err
		}
	}
	return &p, wrapApply(s.applyLocked(ctx, false))
}

// PreviewConfig renders the Xray config the machine runs (or would run) right now.
func (s *Service) PreviewConfig(ctx context.Context, id string) ([]byte, error) {
	mid := machineID(id)
	ins, err := s.machineInbounds(ctx, mid)
	if err != nil {
		return nil, err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	prof, err := s.profileFor(ctx, mid)
	if err != nil {
		return nil, err
	}
	return s.buildConfigFor(ctx, ins, cls, apiPortFor(s, mid), prof)
}

// ---- generators for the inbound form ----

type execer interface {
	Exec(ctx context.Context, args ...string) (string, error)
}

func (s *Service) xrayExec(ctx context.Context, args ...string) (string, error) {
	x, ok := s.Xray.(execer)
	if !ok || !s.Xray.Installed() {
		return "", invalid("this needs Xray on the panel server, and it is not installed")
	}
	out, err := x.Exec(ctx, args...)
	if err != nil {
		return "", invalid("%v", err)
	}
	return out, nil
}

// VlessEncKeys is the result of the VLESS encryption generator: one pair per authentication method.
type VlessEncKeys struct {
	X25519 xray.VlessEnc `json:"x25519"`
	MLKEM  xray.VlessEnc `json:"mlkem768"`
}

func (s *Service) GenerateVlessEnc(ctx context.Context) (*VlessEncKeys, error) {
	out, err := s.xrayExec(ctx, "vlessenc")
	if err != nil {
		return nil, err
	}
	x, m, err := xray.ParseVlessEnc(out)
	if err != nil {
		return nil, invalid("%v", err)
	}
	return &VlessEncKeys{X25519: x, MLKEM: m}, nil
}

func (s *Service) GenerateMLDSA65(ctx context.Context) (seed, verify string, err error) {
	out, err := s.xrayExec(ctx, "mldsa65")
	if err != nil {
		return "", "", err
	}
	seed, verify, err = xray.ParseMLDSA65(out)
	if err != nil {
		return "", "", invalid("%v", err)
	}
	return seed, verify, nil
}

func (s *Service) GenerateECH(ctx context.Context, serverName string) (keys, configList string, err error) {
	serverName = strings.TrimSpace(serverName)
	if serverName == "" || !isHost(serverName) {
		return "", "", invalid("a valid server name is required")
	}
	out, err := s.xrayExec(ctx, "tls", "ech", "--serverName", serverName)
	if err != nil {
		return "", "", err
	}
	keys, configList, err = xray.ParseECH(out)
	if err != nil {
		return "", "", invalid("%v", err)
	}
	return keys, configList, nil
}

// ---- REALITY target check ----

// ScanResult says whether a site is a good REALITY target: TLS 1.3 with HTTP/2 and a certificate that names it.
type ScanResult struct {
	Target     string   `json:"target"`
	OK         bool     `json:"ok"`
	TLS        string   `json:"tls"`
	ALPN       string   `json:"alpn"`
	LatencyMs  int64    `json:"latencyMs"`
	Names      []string `json:"names"`
	Issuer     string   `json:"issuer"`
	Expires    string   `json:"expires"`
	Warnings   []string `json:"warnings"`
	Suggestion string   `json:"suggestion"` // a server name to use
}

// ScanTarget connects to host:port from the panel machine and judges the site as a REALITY target.
func (s *Service) ScanTarget(ctx context.Context, target, serverName string) (*ScanResult, error) {
	target = strings.TrimSpace(target)
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return nil, invalid("the target must look like example.com:443")
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return nil, invalid("the target has an invalid port")
	}
	res := &ScanResult{Target: target, Names: []string{}, Warnings: []string{}}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	began := time.Now()
	raw, err := (&net.Dialer{}).DialContext(cctx, "tcp", target)
	if err != nil {
		return nil, invalid("cannot connect to %s: %v", target, err)
	}
	defer raw.Close()
	// The certificate is only inspected, never trusted, so verification is off.
	sni := strings.TrimSpace(serverName)
	if sni == "" && net.ParseIP(host) == nil {
		sni = host
	}
	conn := tls.Client(raw, &tls.Config{ServerName: sni, InsecureSkipVerify: true, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12}) //nolint:gosec
	if err := conn.HandshakeContext(cctx); err != nil {
		return nil, invalid("TLS handshake with %s failed: %v", target, err)
	}
	res.LatencyMs = time.Since(began).Milliseconds()
	st := conn.ConnectionState()
	res.TLS = tls.VersionName(st.Version)
	res.ALPN = st.NegotiatedProtocol
	if len(st.PeerCertificates) > 0 {
		c := st.PeerCertificates[0]
		res.Names = append(res.Names, c.DNSNames...)
		res.Issuer = c.Issuer.CommonName
		res.Expires = c.NotAfter.Format("2006-01-02")
		if time.Now().After(c.NotAfter) {
			res.Warnings = append(res.Warnings, "the certificate of the site has expired")
		}
	}
	if st.Version != tls.VersionTLS13 {
		res.Warnings = append(res.Warnings, "the site does not use TLS 1.3, which REALITY requires")
	}
	if res.ALPN != "h2" {
		res.Warnings = append(res.Warnings, "the site does not offer HTTP/2 (h2)")
	}
	if net.ParseIP(host) == nil {
		res.Suggestion = host
	} else if len(res.Names) > 0 {
		res.Suggestion = res.Names[0]
	}
	res.OK = len(res.Warnings) == 0
	return res, nil
}
