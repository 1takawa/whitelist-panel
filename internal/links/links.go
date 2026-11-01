// Package links builds client share links (vless:// and trojan://) from an inbound and a client.
package links

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"anypanel2/internal/model"
)

// Build returns the share link for one client on one inbound, named "<inbound>-<client>".
func Build(in model.Inbound, c model.Client, s model.Settings) (string, error) {
	return BuildNamed(in, c, s, remark(in, c))
}

// BuildNamed is Build with an explicit link name (the fragment shown by client apps).
func BuildNamed(in model.Inbound, c model.Client, s model.Settings, name string) (string, error) {
	host, err := Host(in, s)
	if err != nil {
		return "", err
	}
	port := in.Public.Port
	if port == 0 {
		port = in.Port
	}

	q := url.Values{}
	switch in.Protocol {
	case model.ProtoVLESS:
		q.Set("encryption", in.Encryption())
	case model.ProtoTrojan:
	default:
		return "", fmt.Errorf("unsupported protocol %q", in.Protocol)
	}
	q.Set("type", in.Network)
	addTransport(q, in)
	addSecurity(q, in, host)
	if in.Protocol == model.ProtoVLESS && c.Flow != "" && flowApplies(in) {
		q.Set("flow", c.Flow)
	}
	if fm := in.FinalMask(); fm != nil {
		if b, err := json.Marshal(fm); err == nil {
			q.Set("fm", string(b))
		}
	}

	secret := c.UUID
	if in.Protocol == model.ProtoTrojan {
		secret = c.Password
	}
	if secret == "" {
		return "", fmt.Errorf("client %q has no %s credential", c.Email, in.Protocol)
	}
	u := url.URL{
		Scheme:   in.Protocol,
		User:     url.User(secret),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String(), nil
}

// Host is the address clients connect to: an explicit override, else the REALITY or panel domain from settings.
func Host(in model.Inbound, s model.Settings) (string, error) {
	if in.Public.Host != "" {
		return in.Public.Host, nil
	}
	if in.ShareAddress == "listen" {
		if ip := net.ParseIP(in.Listen); ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() {
			return in.Listen, nil
		}
	}
	h := s.PanelDomain
	if in.Security == model.SecReality && s.RealityDomain != "" {
		h = s.RealityDomain
	}
	if h == "" {
		return "", errors.New("no public host: set the panel/REALITY domain in settings or Public.Host on the inbound")
	}
	return h, nil
}

// EffectiveSecurity is the transport security advertised to clients (a front proxy may add TLS Xray does not serve).
func EffectiveSecurity(in model.Inbound) string {
	switch in.Public.Security {
	case model.SecTLS:
		return model.SecTLS
	case model.SecNone:
		return model.SecNone
	}
	return in.Security
}

func flowApplies(in model.Inbound) bool {
	if in.DisableFlow || in.Network != model.NetTCP {
		return false
	}
	sec := EffectiveSecurity(in)
	if sec == model.SecTLS || sec == model.SecReality {
		return true
	}
	return in.Encryption() != "none" && in.Encryption() != ""
}

func addTransport(q url.Values, in model.Inbound) {
	switch in.Network {
	case model.NetTCP:
		if typ, host, path := in.TCPHeader(); typ == "http" {
			q.Set("headerType", "http")
			setIf(q, "host", host)
			setIf(q, "path", path)
		}
	case model.NetKCP:
		header, seed := in.KCPHeader()
		setIf(q, "headerType", header)
		setIf(q, "seed", seed)
	case model.NetWS:
		setIf(q, "path", orDefault(in.WS.Path, "/"))
		setIf(q, "host", in.WS.Host)
	case model.NetHTTPUpgrade:
		setIf(q, "path", orDefault(in.HTTPUpgrade.Path, "/"))
		setIf(q, "host", in.HTTPUpgrade.Host)
	case model.NetXHTTP:
		setIf(q, "path", orDefault(in.XHTTP.Path, "/"))
		setIf(q, "host", in.XHTTP.Host)
		q.Set("mode", orDefault(in.XHTTP.Mode, "auto"))
		if extra := in.XHTTPExtra(); extra != nil {
			if b, err := json.Marshal(extra); err == nil {
				q.Set("extra", string(b))
			}
		}
	case model.NetGRPC:
		setIf(q, "serviceName", in.GRPC.ServiceName)
		setIf(q, "authority", in.GRPC.Authority)
		if in.GRPC.MultiMode {
			q.Set("mode", "multi")
		} else {
			q.Set("mode", "gun")
		}
	}
}

func addSecurity(q url.Values, in model.Inbound, host string) {
	sec := EffectiveSecurity(in)
	q.Set("security", sec)
	switch sec {
	case model.SecTLS:
		sni := in.Public.SNI
		if sni == "" {
			sni = in.TLS.ServerName
		}
		if sni == "" {
			sni = host
		}
		q.Set("sni", sni)
		q.Set("fp", firstNonEmpty(in.Public.Fingerprint, in.Fingerprint(), "firefox"))
		if in.AllowInsecure() {
			q.Set("allowInsecure", "1")
		}
		setIf(q, "ech", in.ECHConfigList())
		alpn := in.Public.ALPN
		if alpn == "" {
			alpn = strings.Join(in.TLS.ALPN, ",")
		}
		setIf(q, "alpn", alpn)
	case model.SecReality:
		sni := in.Public.SNI
		if sni == "" && len(in.Reality.ServerNames) > 0 {
			sni = in.Reality.ServerNames[0]
		}
		setIf(q, "sni", sni)
		q.Set("fp", firstNonEmpty(in.Public.Fingerprint, in.Fingerprint(), "firefox"))
		setIf(q, "pbk", in.Reality.PublicKey)
		setIf(q, "pqv", in.MLDSAVerify())
		if len(in.Reality.ShortIDs) > 0 {
			q.Set("sid", in.Reality.ShortIDs[0])
		}
		setIf(q, "spx", in.Reality.SpiderX)
	}
}

func remark(in model.Inbound, c model.Client) string {
	if in.Remark == "" {
		return c.Email
	}
	return in.Remark + "-" + c.Email
}

func setIf(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
