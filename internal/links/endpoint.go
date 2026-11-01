package links

import (
	"fmt"
	"strings"

	"anypanel2/internal/model"
)

// Endpoint is one client-side server entry: everything an app needs to connect, independent of the file format.
// Share links, Clash profiles and Xray client configs are all rendered from it.
type Endpoint struct {
	Name     string
	Protocol string // vless | trojan
	Host     string
	Port     int
	UUID     string // vless
	Password string // trojan
	Flow     string

	Network     string // tcp | ws | grpc | xhttp | httpupgrade
	Security    string // none | tls | reality, as advertised to clients
	SNI         string
	Fingerprint string
	ALPN        []string

	Path        string // ws, httpupgrade, xhttp
	HostHeader  string // ws, httpupgrade, xhttp
	Mode        string // xhttp mode
	ServiceName string // grpc
	Authority   string // grpc
	MultiMode   bool   // grpc

	Encryption    string         // vless: "none" or the VLESS encryption string
	HeaderType    string         // tcp "http" obfuscation, kcp header
	TCPHost       string         // tcp http obfuscation
	TCPPath       string         // tcp http obfuscation
	Seed          string         // kcp
	Extra         map[string]any // xhttp options clients need
	FinalMask     map[string]any // transport masks
	NoAuto        bool           // the inbound asked to stay out of the automatic node
	AllowInsecure bool
	ECH           string
	PQV           string // reality: ML-DSA-65 verification key

	PublicKey string // reality
	ShortID   string // reality
	SpiderX   string // reality
}

// NewEndpoint derives the entry a client uses to reach the inbound, with the same rules as BuildNamed.
func NewEndpoint(in model.Inbound, c model.Client, s model.Settings, name string) (Endpoint, error) {
	host, err := Host(in, s)
	if err != nil {
		return Endpoint{}, err
	}
	port := in.Public.Port
	if port == 0 {
		port = in.Port
	}
	e := Endpoint{Name: name, Protocol: in.Protocol, Host: host, Port: port, Network: in.Network, Security: EffectiveSecurity(in)}
	switch in.Protocol {
	case model.ProtoVLESS:
		e.UUID, e.Encryption = c.UUID, in.Encryption()
		if c.Flow != "" && flowApplies(in) {
			e.Flow = c.Flow
		}
	case model.ProtoTrojan:
		e.Password = c.Password
	default:
		return Endpoint{}, fmt.Errorf("unsupported protocol %q", in.Protocol)
	}
	if e.UUID == "" && e.Password == "" {
		return Endpoint{}, fmt.Errorf("client %q has no %s credential", c.Email, in.Protocol)
	}

	e.FinalMask = in.FinalMask()
	e.NoAuto = in.ExcludeFromAuto
	switch in.Network {
	case model.NetWS:
		e.Path, e.HostHeader = orDefault(in.WS.Path, "/"), in.WS.Host
	case model.NetHTTPUpgrade:
		e.Path, e.HostHeader = orDefault(in.HTTPUpgrade.Path, "/"), in.HTTPUpgrade.Host
	case model.NetXHTTP:
		e.Path, e.HostHeader, e.Mode = orDefault(in.XHTTP.Path, "/"), in.XHTTP.Host, orDefault(in.XHTTP.Mode, "auto")
		e.Extra = in.XHTTPExtra()
	case model.NetGRPC:
		e.ServiceName, e.Authority, e.MultiMode = in.GRPC.ServiceName, in.GRPC.Authority, in.GRPC.MultiMode
	case model.NetTCP:
		if typ, host, path := in.TCPHeader(); typ == "http" {
			e.HeaderType, e.TCPHost, e.TCPPath = typ, host, path
		}
	case model.NetKCP:
		e.HeaderType, e.Seed = in.KCPHeader()
	}

	switch e.Security {
	case model.SecTLS:
		e.SNI = firstNonEmpty(in.Public.SNI, in.TLS.ServerName, host)
		e.Fingerprint = firstNonEmpty(in.Public.Fingerprint, in.Fingerprint(), "firefox")
		e.AllowInsecure, e.ECH = in.AllowInsecure(), in.ECHConfigList()
		alpn := in.Public.ALPN
		if alpn == "" {
			alpn = strings.Join(in.TLS.ALPN, ",")
		}
		for _, a := range strings.Split(alpn, ",") {
			if a = strings.TrimSpace(a); a != "" {
				e.ALPN = append(e.ALPN, a)
			}
		}
	case model.SecReality:
		e.SNI = in.Public.SNI
		if e.SNI == "" && len(in.Reality.ServerNames) > 0 {
			e.SNI = in.Reality.ServerNames[0]
		}
		e.Fingerprint = firstNonEmpty(in.Public.Fingerprint, in.Fingerprint(), "firefox")
		e.PublicKey, e.SpiderX, e.PQV = in.Reality.PublicKey, in.Reality.SpiderX, in.MLDSAVerify()
		if len(in.Reality.ShortIDs) > 0 {
			e.ShortID = in.Reality.ShortIDs[0]
		}
	}
	return e, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
