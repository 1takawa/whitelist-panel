// Package formats renders a subscriber's endpoints as Clash/Mihomo YAML and as Xray client JSON.
package formats

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"anypanel2/internal/links"
	"anypanel2/internal/model"
)

// ClashOptions control the generated Clash profile.
type ClashOptions struct {
	Rules     []string     // custom rules, already validated by ParseClashRules
	RemoteDNS bool         // add a dns section with DoH resolvers
	Info      []string     // names of info nodes: dummy servers that only carry a name
	Auto      *AutoOptions // the automatic node group; nil keeps the plain latency group
}

const (
	proxyGroup = "Proxy"
	autoGroup  = "Auto"
	probeURL   = "https://www.gstatic.com/generate_204"
)

type clashConfig struct {
	MixedPort   int          `yaml:"mixed-port"`
	AllowLAN    bool         `yaml:"allow-lan"`
	Mode        string       `yaml:"mode"`
	LogLevel    string       `yaml:"log-level"`
	IPv6        bool         `yaml:"ipv6"`
	DNS         *clashDNS    `yaml:"dns,omitempty"`
	Proxies     []clashProxy `yaml:"proxies"`
	ProxyGroups []clashGroup `yaml:"proxy-groups"`
	Rules       []string     `yaml:"rules"`
}

type clashDNS struct {
	Enable            bool     `yaml:"enable"`
	IPv6              bool     `yaml:"ipv6"`
	EnhancedMode      string   `yaml:"enhanced-mode"`
	FakeIPRange       string   `yaml:"fake-ip-range"`
	DefaultNameserver []string `yaml:"default-nameserver"`
	Nameserver        []string `yaml:"nameserver"`
}

type clashGroup struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Proxies   []string `yaml:"proxies"`
	URL       string   `yaml:"url,omitempty"`
	Interval  int      `yaml:"interval,omitempty"`
	Tolerance int      `yaml:"tolerance,omitempty"`
	Timeout   int      `yaml:"timeout,omitempty"` // milliseconds
	Lazy      *bool    `yaml:"lazy,omitempty"`
}

type clashProxy struct {
	Name              string      `yaml:"name"`
	Type              string      `yaml:"type"`
	Server            string      `yaml:"server"`
	Port              int         `yaml:"port"`
	UUID              string      `yaml:"uuid,omitempty"`
	Password          string      `yaml:"password,omitempty"`
	UDP               bool        `yaml:"udp"`
	Network           string      `yaml:"network,omitempty"`
	TLS               bool        `yaml:"tls,omitempty"`
	Flow              string      `yaml:"flow,omitempty"`
	Encryption        string      `yaml:"encryption,omitempty"`
	ServerName        string      `yaml:"servername,omitempty"` // vless
	SNI               string      `yaml:"sni,omitempty"`        // trojan
	ClientFingerprint string      `yaml:"client-fingerprint,omitempty"`
	ALPN              []string    `yaml:"alpn,omitempty"`
	Reality           *clashReal  `yaml:"reality-opts,omitempty"`
	WS                *clashWS    `yaml:"ws-opts,omitempty"`
	GRPC              *clashGRPC  `yaml:"grpc-opts,omitempty"`
	XHTTP             *clashXHTTP `yaml:"xhttp-opts,omitempty"`
}

type clashReal struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

type clashWS struct {
	Path        string            `yaml:"path"`
	Headers     map[string]string `yaml:"headers,omitempty"`
	HTTPUpgrade bool              `yaml:"v2ray-http-upgrade,omitempty"`
}

type clashGRPC struct {
	ServiceName string `yaml:"grpc-service-name"`
}

type clashXHTTP struct {
	Path string `yaml:"path"`
	Host string `yaml:"host,omitempty"`
	Mode string `yaml:"mode,omitempty"`
}

// ParseClashRules validates custom rules typed one per line. Blank lines and lines starting with # are skipped.
// Targets other than DIRECT, REJECT and the "Proxy" group are the author's responsibility.
func ParseClashRules(text string) ([]string, error) {
	var out []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 300 || strings.ContainsAny(line, "\x00\r") || strings.Count(line, ",") < 1 {
			return nil, fmt.Errorf("rule on line %d is not valid: use TYPE,VALUE,TARGET such as DOMAIN-SUFFIX,example.com,DIRECT", i+1)
		}
		if len(out) >= 500 {
			return nil, errors.New("too many rules (max 500)")
		}
		out = append(out, line)
	}
	return out, nil
}

// Clash renders a Mihomo-compatible profile: one proxy per endpoint, a selector and an automatic latency group.
func Clash(eps []links.Endpoint, opt ClashOptions) ([]byte, error) {
	used := map[string]bool{proxyGroup: true, autoGroup: true, "DIRECT": true, "REJECT": true}
	unique := func(name string) string {
		base, n := name, 1
		for used[name] || name == "" {
			n++
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		used[name] = true
		return name
	}

	groupName := autoGroup
	if opt.Auto != nil && opt.Auto.Name != "" {
		groupName = opt.Auto.Name
		used[groupName] = true
	}
	var proxies []clashProxy
	var names, autoNames []string
	for _, n := range opt.Info {
		p := clashProxy{Name: unique(n), Type: "socks5", Server: "127.0.0.1", Port: 1080}
		proxies, names = append(proxies, p), append(names, p.Name)
	}
	for _, e := range eps {
		p, err := clashProxyFor(e)
		if err != nil {
			return nil, err
		}
		p.Name = unique(e.Name)
		proxies, names = append(proxies, p), append(names, p.Name)
		if !e.NoAuto {
			autoNames = append(autoNames, p.Name)
		}
	}
	if len(proxies) == 0 {
		p := clashProxy{Name: unique("No servers available"), Type: "socks5", Server: "127.0.0.1", Port: 1080}
		proxies, names = append(proxies, p), append(names, p.Name)
	}

	// The group the app picks from: every server, probed every 5 minutes, unless the automatic node is configured.
	group := clashGroup{Name: groupName, Type: "url-test", Proxies: names, URL: probeURL, Interval: 300, Tolerance: 50}
	last := false
	if a := opt.Auto; a != nil && len(autoNames) >= 2 {
		lazy := false
		group.Proxies, group.URL, group.Interval, group.Tolerance = autoNames, a.URL, a.IntervalSec, a.ToleranceMs
		group.Timeout, group.Lazy = a.TimeoutSec*1000, &lazy
		last = a.Last
	}
	choices := append([]string{group.Name}, append(append([]string{}, names...), "DIRECT")...)
	if last {
		choices = append(append(append([]string{}, names...), group.Name), "DIRECT")
	}
	cfg := clashConfig{
		MixedPort: 7890, Mode: "rule", LogLevel: "info", Proxies: proxies,
		ProxyGroups: []clashGroup{{Name: proxyGroup, Type: "select", Proxies: choices}, group},
	}
	cfg.Rules = append(cfg.Rules, opt.Rules...)
	cfg.Rules = append(cfg.Rules, "GEOIP,PRIVATE,DIRECT,no-resolve", "MATCH,"+proxyGroup)
	if opt.RemoteDNS {
		cfg.DNS = &clashDNS{Enable: true, EnhancedMode: "fake-ip", FakeIPRange: "198.18.0.1/16",
			DefaultNameserver: []string{"1.1.1.1", "8.8.8.8"},
			Nameserver:        []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}}
	}
	return yaml.Marshal(cfg)
}

func clashProxyFor(e links.Endpoint) (clashProxy, error) {
	p := clashProxy{Server: e.Host, Port: e.Port, UDP: true, Network: e.Network, ClientFingerprint: e.Fingerprint, ALPN: e.ALPN}
	switch e.Protocol {
	case model.ProtoVLESS:
		p.Type, p.UUID, p.Flow = "vless", e.UUID, e.Flow
		if e.Encryption != "" && e.Encryption != "none" {
			p.Encryption = e.Encryption
		}
	case model.ProtoTrojan:
		p.Type, p.Password = "trojan", e.Password
	default:
		return p, fmt.Errorf("unsupported protocol %q", e.Protocol)
	}

	switch e.Security {
	case model.SecTLS:
		p.TLS = true
		if e.Protocol == model.ProtoTrojan {
			p.SNI = e.SNI
		} else {
			p.ServerName = e.SNI
		}
	case model.SecReality:
		p.TLS, p.ServerName = true, e.SNI
		p.Reality = &clashReal{PublicKey: e.PublicKey, ShortID: e.ShortID}
	default:
		p.ClientFingerprint, p.ALPN = "", nil // only meaningful with TLS
	}

	switch e.Network {
	case model.NetTCP:
		p.Network = "tcp"
	case model.NetWS:
		p.WS = &clashWS{Path: e.Path}
		if e.HostHeader != "" {
			p.WS.Headers = map[string]string{"Host": e.HostHeader}
		}
	case model.NetHTTPUpgrade:
		p.Network = "ws"
		p.WS = &clashWS{Path: e.Path, HTTPUpgrade: true}
		if e.HostHeader != "" {
			p.WS.Headers = map[string]string{"Host": e.HostHeader}
		}
	case model.NetGRPC:
		p.GRPC = &clashGRPC{ServiceName: e.ServiceName}
	case model.NetXHTTP:
		p.XHTTP = &clashXHTTP{Path: e.Path, Host: e.HostHeader, Mode: e.Mode}
	case model.NetKCP:
		return p, fmt.Errorf("transport mKCP is not supported by Clash")
	}
	return p, nil
}
