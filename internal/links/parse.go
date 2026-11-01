package links

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// MaxURILength bounds a pasted link.
const MaxURILength = 2048

// ParseURI reads a vless:// or trojan:// share link written by another panel into an Endpoint. Only what the
// panel can render in every subscription format is accepted; anything else is an error that says why.
func ParseURI(raw string) (Endpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Endpoint{}, errors.New("empty link")
	}
	if len(raw) > MaxURILength {
		return Endpoint{}, errors.New("link is too long")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return Endpoint{}, errors.New("link contains control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Endpoint{}, fmt.Errorf("not a valid link: %v", err)
	}
	e := Endpoint{Protocol: strings.ToLower(u.Scheme), Name: u.Fragment}
	if e.Protocol != "vless" && e.Protocol != "trojan" {
		return Endpoint{}, fmt.Errorf("unsupported scheme %q (only vless:// and trojan:// links can be added)", u.Scheme)
	}
	if u.User == nil || u.User.Username() == "" {
		return Endpoint{}, errors.New("the link has no user id / password before the @")
	}
	secret := u.User.Username()
	if e.Protocol == "vless" {
		if !uuidRe.MatchString(secret) {
			return Endpoint{}, errors.New("a vless id must be a UUID")
		}
		e.UUID = strings.ToLower(secret)
	} else {
		e.Password = secret
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" {
		return Endpoint{}, errors.New("the link needs host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return Endpoint{}, errors.New("port must be 1–65535")
	}
	e.Host, e.Port = host, port

	q := u.Query()
	e.Network = strings.ToLower(q.Get("type"))
	switch e.Network {
	case "", "raw":
		e.Network = "tcp"
	case "tcp", "ws", "grpc", "xhttp", "httpupgrade":
	default:
		return Endpoint{}, fmt.Errorf("transport %q is not supported (tcp, ws, grpc, xhttp, httpupgrade)", e.Network)
	}
	e.Security = strings.ToLower(q.Get("security"))
	switch e.Security {
	case "":
		e.Security = "none"
	case "none", "tls", "reality":
	default:
		return Endpoint{}, fmt.Errorf("security %q is not supported (none, tls, reality)", e.Security)
	}

	e.Flow = q.Get("flow")
	e.SNI = q.Get("sni")
	e.Fingerprint = q.Get("fp")
	for _, a := range strings.Split(q.Get("alpn"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			e.ALPN = append(e.ALPN, a)
		}
	}
	e.Path, e.HostHeader, e.Mode = q.Get("path"), q.Get("host"), q.Get("mode")
	e.ServiceName, e.Authority = q.Get("serviceName"), q.Get("authority")
	if e.Network == "grpc" {
		e.MultiMode = e.Mode == "multi"
		e.Mode = ""
	}
	e.PublicKey, e.ShortID, e.SpiderX = q.Get("pbk"), q.Get("sid"), q.Get("spx")
	if e.Security == "reality" && e.PublicKey == "" {
		return Endpoint{}, errors.New("a reality link needs pbk (the public key)")
	}
	if e.Protocol == "trojan" && e.Security == "reality" {
		return Endpoint{}, errors.New("trojan over reality is not supported")
	}
	return e, nil
}

// URI renders the endpoint as a share link — the inverse of ParseURI, with the name as the fragment.
func (e Endpoint) URI() string {
	q := url.Values{}
	if e.Protocol == "vless" {
		q.Set("encryption", "none")
	}
	q.Set("type", e.Network)
	switch e.Network {
	case "ws", "httpupgrade":
		setIf(q, "path", e.Path)
		setIf(q, "host", e.HostHeader)
	case "xhttp":
		setIf(q, "path", e.Path)
		setIf(q, "host", e.HostHeader)
		setIf(q, "mode", e.Mode)
	case "grpc":
		setIf(q, "serviceName", e.ServiceName)
		setIf(q, "authority", e.Authority)
		if e.MultiMode {
			q.Set("mode", "multi")
		} else {
			q.Set("mode", "gun")
		}
	}
	q.Set("security", e.Security)
	switch e.Security {
	case "tls":
		setIf(q, "sni", e.SNI)
		setIf(q, "fp", e.Fingerprint)
		setIf(q, "alpn", strings.Join(e.ALPN, ","))
	case "reality":
		setIf(q, "sni", e.SNI)
		setIf(q, "fp", e.Fingerprint)
		setIf(q, "pbk", e.PublicKey)
		setIf(q, "sid", e.ShortID)
		setIf(q, "spx", e.SpiderX)
	}
	setIf(q, "flow", e.Flow)
	secret := e.UUID
	if e.Protocol == "trojan" {
		secret = e.Password
	}
	u := url.URL{
		Scheme:   e.Protocol,
		User:     url.User(secret),
		Host:     net.JoinHostPort(e.Host, strconv.Itoa(e.Port)),
		RawQuery: q.Encode(),
		Fragment: e.Name,
	}
	return u.String()
}
