package formats

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"anypanel2/internal/links"
	"anypanel2/internal/model"
)

// JSONOptions control the generated Xray client configs. The string options hold JSON typed in the settings.
type JSONOptions struct {
	AlwaysArray bool
	Mux         string         // JSON object, the outbound "mux"
	Fragment    string         // JSON object {packets, length, interval}: TCP fragmentation of the TLS handshake
	Noises      string         // JSON array of UDP noises
	Rules       string         // JSON array of routing rules put before the built-in ones
	DNS         string         // JSON object replacing the default dns section
	Info        []string       // names of info nodes: dummy configs that only carry a name
	Auto        *AutoOptions   // the automatic node entry; nil = none
	MoreAuto    []*AutoOptions // more automatic nodes, each over its own connections
	Profile     string         // the app's routing profile (a happ:// or incy:// link, or JSON), written into the configs as rules
	// NodeProfiles are the profiles a connection may ask for by id (Endpoint.Routing), as links; a connection that asks for
	// "none" gets no profile at all. A connection that asks for nothing gets Profile.
	NodeProfiles map[string]string
}

type obj = map[string]any

// ValidateJSONOptions checks that every JSON field has the right shape.
func ValidateJSONOptions(o JSONOptions) error {
	for _, f := range []struct {
		name, v string
		array   bool
	}{{"mux", o.Mux, false}, {"fragment", o.Fragment, false}, {"DNS", o.DNS, false}, {"noises", o.Noises, true}, {"routing rules", o.Rules, true}} {
		if strings.TrimSpace(f.v) == "" {
			continue
		}
		if len(f.v) > 20000 {
			return fmt.Errorf("%s JSON is too large", f.name)
		}
		var v any
		if err := json.Unmarshal([]byte(f.v), &v); err != nil {
			return fmt.Errorf("%s is not valid JSON: %v", f.name, err)
		}
		switch v.(type) {
		case map[string]any:
			if f.array {
				return fmt.Errorf("%s must be a JSON array", f.name)
			}
		case []any:
			if !f.array {
				return fmt.Errorf("%s must be a JSON object", f.name)
			}
		default:
			return fmt.Errorf("%s must be a JSON %s", f.name, map[bool]string{true: "array", false: "object"}[f.array])
		}
	}
	return nil
}

func parse(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return nil
	}
	return v
}

// XrayJSON renders one Xray client config per endpoint. With a single config the output is an object, unless
// AlwaysArray is set.
func XrayJSON(eps []links.Endpoint, opt JSONOptions) ([]byte, error) {
	if err := ValidateJSONOptions(opt); err != nil {
		return nil, err
	}
	var cfgs []any
	for _, n := range opt.Info {
		cfgs = append(cfgs, dummyConfig(n))
	}
	for _, e := range eps {
		c, err := clientConfig(e, opt)
		if err != nil {
			return nil, err
		}
		cfgs = append(cfgs, c)
	}
	var autos []any
	var at, pos []int
	for _, a := range autoList(opt.Auto, opt.MoreAuto) {
		members := autoMembers(eps, a)
		if len(members) < 2 {
			continue
		}
		ac, err := autoConfig(members, opt, a)
		if err != nil {
			return nil, err
		}
		autos, at, pos = append(autos, ac), append(at, autoAt(eps, a)), append(pos, a.Position)
	}
	cfgs = insertAutos(cfgs, len(opt.Info), at, pos, autos)
	if len(cfgs) == 0 {
		cfgs = append(cfgs, dummyConfig("No servers available"))
	}
	if len(cfgs) == 1 && !opt.AlwaysArray {
		return json.MarshalIndent(cfgs[0], "", "  ")
	}
	return json.MarshalIndent(cfgs, "", "  ")
}

func dummyConfig(name string) obj {
	return obj{
		"remarks":   name,
		"log":       obj{"loglevel": "warning"},
		"inbounds":  []any{socksIn()},
		"outbounds": []any{obj{"tag": "proxy", "protocol": "socks", "settings": obj{"servers": []any{obj{"address": "127.0.0.1", "port": 1080}}}}},
	}
}

// clientDNS is the dns section of a client config: the custom one, else public resolvers.
func clientDNS(opt JSONOptions) any {
	if v := parse(opt.DNS); v != nil {
		return v
	}
	return obj{"servers": []string{"1.1.1.1", "8.8.8.8"}, "queryStrategy": "UseIP"}
}

// baseRules are the routing rules every client config starts with: the custom ones, then private addresses direct.
func baseRules(opt JSONOptions, balancer bool) ([]any, string) {
	var rules []any
	if v, ok := parse(opt.Rules).([]any); ok {
		rules = append(rules, v...)
	}
	profile, strategy := profileRules(opt.Profile, balancer)
	if strategy == "" {
		strategy = "AsIs"
	}
	rules = append(rules, profile...)
	return append(rules, obj{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "direct"}), strategy
}

func httpIn() obj {
	return obj{"tag": "http", "listen": "127.0.0.1", "port": 10809, "protocol": "http", "settings": obj{},
		"sniffing": obj{"enabled": true, "destOverride": []string{"http", "tls", "quic", "fakedns"}}}
}

func socksIn() obj {
	return obj{"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks",
		"settings": obj{"auth": "noauth", "udp": true},
		"sniffing": obj{"enabled": true, "destOverride": []string{"http", "tls", "quic", "fakedns"}}}
}

func clientConfig(e links.Endpoint, opt JSONOptions) (obj, error) {
	proxy, err := outbound(e)
	if err != nil {
		return nil, err
	}
	// Mux and the Vision flow are mutually exclusive in Xray, so Vision links never get a mux block.
	if m := parse(opt.Mux); m != nil && e.Flow == "" {
		proxy["mux"] = m
	}
	outbounds := []any{proxy}

	frag, noises := parse(opt.Fragment), parse(opt.Noises)
	if (frag != nil || noises != nil) && e.Security != model.SecNone {
		settings := obj{}
		if frag != nil {
			settings["fragment"] = frag
		}
		if noises != nil {
			settings["noises"] = noises
		}
		outbounds = append(outbounds, obj{"tag": "fragment", "protocol": "freedom", "settings": settings})
		ss := proxy["streamSettings"].(obj)
		ss["sockopt"] = obj{"dialerProxy": "fragment"}
	}
	outbounds = append(outbounds,
		obj{"tag": "direct", "protocol": "freedom", "settings": obj{"domainStrategy": "UseIP"}},
		obj{"tag": "block", "protocol": "blackhole"})

	dns := clientDNS(opt)
	own := opt // the connection's own routing, when it asked for one
	switch e.Routing {
	case "":
	case "none":
		own.Profile = ""
	default:
		if p, ok := opt.NodeProfiles[e.Routing]; ok {
			own.Profile = p
		}
	}
	rules, strategy := baseRules(own, false)

	return obj{
		"remarks":   e.Name,
		"log":       obj{"loglevel": "warning"},
		"dns":       dns,
		"inbounds":  []any{socksIn(), httpIn()},
		"outbounds": outbounds,
		"routing":   obj{"domainStrategy": strategy, "rules": rules},
	}, nil
}

func outbound(e links.Endpoint) (obj, error) {
	ss := obj{"network": e.Network, "security": e.Security}
	switch e.Network {
	case model.NetWS:
		ws := obj{"path": e.Path}
		if e.HostHeader != "" {
			ws["headers"] = obj{"Host": e.HostHeader}
		}
		ss["wsSettings"] = ws
	case model.NetHTTPUpgrade:
		ss["httpupgradeSettings"] = obj{"path": e.Path, "host": e.HostHeader}
	case model.NetXHTTP:
		x := obj{"path": e.Path, "host": e.HostHeader, "mode": e.Mode}
		for k, v := range e.Extra {
			x[k] = v
		}
		ss["xhttpSettings"] = x
	case model.NetKCP:
		k := obj{}
		if e.HeaderType != "" {
			k["header"] = obj{"type": e.HeaderType}
		}
		if e.Seed != "" {
			k["seed"] = e.Seed
		}
		ss["kcpSettings"] = k
	case model.NetTCP:
		if e.HeaderType == "http" {
			req := obj{"path": strings.Split(orSlash(e.TCPPath), ",")}
			if e.TCPHost != "" {
				req["headers"] = obj{"Host": strings.Split(e.TCPHost, ",")}
			}
			ss["tcpSettings"] = obj{"header": obj{"type": "http", "request": req}}
		}
	case model.NetGRPC:
		g := obj{"serviceName": e.ServiceName, "multiMode": e.MultiMode}
		if e.Authority != "" {
			g["authority"] = e.Authority
		}
		ss["grpcSettings"] = g
	}
	switch e.Security {
	case model.SecTLS:
		t := obj{"serverName": e.SNI, "fingerprint": e.Fingerprint, "allowInsecure": e.AllowInsecure}
		if e.ECH != "" {
			t["echConfigList"] = e.ECH
		}
		if len(e.ALPN) > 0 {
			t["alpn"] = e.ALPN
		}
		ss["tlsSettings"] = t
	case model.SecReality:
		r := obj{"serverName": e.SNI, "fingerprint": e.Fingerprint, "publicKey": e.PublicKey, "shortId": e.ShortID, "spiderX": e.SpiderX}
		if e.PQV != "" {
			r["mldsa65Verify"] = e.PQV
		}
		ss["realitySettings"] = r
	}

	if len(e.FinalMask) > 0 {
		ss["finalmask"] = e.FinalMask
	}
	out := obj{"tag": "proxy", "streamSettings": ss}
	switch e.Protocol {
	case model.ProtoVLESS:
		user := obj{"id": e.UUID, "encryption": orNone(e.Encryption)}
		if e.Flow != "" {
			user["flow"] = e.Flow
		}
		out["protocol"] = "vless"
		out["settings"] = obj{"vnext": []any{obj{"address": e.Host, "port": e.Port, "users": []any{user}}}}
	case model.ProtoTrojan:
		out["protocol"] = "trojan"
		out["settings"] = obj{"servers": []any{obj{"address": e.Host, "port": e.Port, "password": e.Password}}}
	default:
		return nil, errors.New("unsupported protocol " + e.Protocol)
	}
	return out, nil
}

func orSlash(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

// Outbound is the Xray outbound that connects to the endpoint (tag "proxy"; callers rename it). The panel uses it to
// chain one server to another.
func Outbound(e links.Endpoint) (map[string]any, error) { return outbound(e) }
