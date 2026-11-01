package model

import (
	"slices"
	"strings"
)

// The documents Inbound.Settings and Inbound.StreamSettings use the layout of an Xray inbound (as in Xray-панелей):
//
//	streamSettings: network, security, tcpSettings, wsSettings, grpcSettings, xhttpSettings, httpupgradeSettings,
//	                kcpSettings, tlsSettings, realitySettings, sockopt, finalmask
//
// Three directions exist, all idempotent:
//   - EnsureRaw builds the documents from the typed fields (inbounds that have none yet),
//   - Derive reads the typed fields from the documents (the documents win),
//   - Sync writes the typed fields back after they were validated and normalised.

// NormalizeNetwork maps the transport names Xray accepts onto the panel's.
func NormalizeNetwork(n string) string {
	switch strings.ToLower(strings.TrimSpace(n)) {
	case "", "tcp", "raw":
		return NetTCP
	case "mkcp":
		return NetKCP
	}
	return strings.ToLower(strings.TrimSpace(n))
}

// EnsureRaw fills Settings and StreamSettings from the typed fields when they are missing.
func (in *Inbound) EnsureRaw() {
	if in.Settings == nil {
		in.Settings = Doc{}
		if in.Protocol == ProtoVLESS {
			in.Settings["decryption"] = "none"
		}
	}
	if in.StreamSettings == nil {
		in.StreamSettings = Doc{}
		if in.Network == "" {
			in.Network = NetTCP
		}
		if in.Security == "" {
			in.Security = SecNone
		}
		in.Sync()
	}
}

// HasRaw reports whether the inbound carries its Xray documents.
func (in Inbound) HasRaw() bool { return in.StreamSettings != nil }

// Derive reads the typed view from StreamSettings. It does nothing when the inbound has no document.
func (in *Inbound) Derive() {
	ss := in.StreamSettings
	if ss == nil {
		return
	}
	in.Network = NormalizeNetwork(Str(ss, "network"))
	if in.Security = strings.ToLower(Str(ss, "security")); in.Security == "" {
		in.Security = SecNone
	}

	in.WS = PathHost{Path: Str(ss.Sub("wsSettings"), "path"), Host: hostOf(ss.Sub("wsSettings"))}
	in.HTTPUpgrade = PathHost{Path: Str(ss.Sub("httpupgradeSettings"), "path"), Host: hostOf(ss.Sub("httpupgradeSettings"))}
	x := ss.Sub("xhttpSettings")
	in.XHTTP = XHTTP{Path: Str(x, "path"), Host: hostOf(x), Mode: Str(x, "mode")}
	g := ss.Sub("grpcSettings")
	in.GRPC = GRPC{ServiceName: Str(g, "serviceName"), Authority: Str(g, "authority"), MultiMode: Bool(g, "multiMode")}

	t := ss.Sub("tlsSettings")
	in.TLS = TLS{ServerName: Str(t, "serverName"), ALPN: Strs(t, "alpn")}
	if certs, ok := t["certificates"].([]any); ok && len(certs) > 0 {
		if c, ok := certs[0].(map[string]any); ok {
			in.TLS.CertFile, in.TLS.KeyFile = Str(c, "certificateFile"), Str(c, "keyFile")
		}
	}

	r := ss.Sub("realitySettings")
	rs, _ := r["settings"].(map[string]any)
	dest := Str(r, "target")
	if dest == "" {
		dest = Str(r, "dest")
	}
	in.Reality = Reality{
		Dest: dest, ServerNames: Strs(r, "serverNames"), PrivateKey: Str(r, "privateKey"),
		PublicKey: Str(rs, "publicKey"), ShortIDs: Strs(r, "shortIds"), SpiderX: Str(rs, "spiderX"),
	}

	in.AcceptProxyProtocol = false
	for _, k := range []string{"tcpSettings", "rawSettings", "wsSettings", "httpupgradeSettings", "sockopt"} {
		if Bool(ss.Sub(k), "acceptProxyProtocol") {
			in.AcceptProxyProtocol = true
		}
	}
}

// Sync writes the typed fields into StreamSettings, leaving every option the typed view does not know untouched.
func (in *Inbound) Sync() {
	if in.StreamSettings == nil {
		in.StreamSettings = Doc{}
	}
	ss := in.StreamSettings
	ss["network"], ss["security"] = in.Network, in.Security

	switch in.Network {
	case NetWS:
		w := ss.Ensure("wsSettings")
		w["path"] = in.WS.Path
		setHost(w, in.WS.Host, true)
	case NetHTTPUpgrade:
		w := ss.Ensure("httpupgradeSettings")
		w["path"] = in.HTTPUpgrade.Path
		setHost(w, in.HTTPUpgrade.Host, false)
	case NetXHTTP:
		w := ss.Ensure("xhttpSettings")
		w["path"] = in.XHTTP.Path
		w["mode"] = in.XHTTP.Mode
		setHost(w, in.XHTTP.Host, false)
	case NetGRPC:
		g := ss.Ensure("grpcSettings")
		g["serviceName"], g["multiMode"] = in.GRPC.ServiceName, in.GRPC.MultiMode
		if in.GRPC.Authority != "" {
			g["authority"] = in.GRPC.Authority
		} else {
			delete(g, "authority")
		}
	}

	switch in.Security {
	case SecTLS:
		t := ss.Ensure("tlsSettings")
		if in.TLS.ServerName != "" {
			t["serverName"] = in.TLS.ServerName
		} else {
			delete(t, "serverName")
		}
		if len(in.TLS.ALPN) > 0 {
			t["alpn"] = toAny(in.TLS.ALPN)
		} else {
			delete(t, "alpn")
		}
		certs, _ := t["certificates"].([]any)
		if in.TLS.CertFile != "" || in.TLS.KeyFile != "" {
			var first map[string]any
			if len(certs) > 0 {
				first, _ = certs[0].(map[string]any)
			}
			if first == nil {
				first = map[string]any{}
				certs = append([]any{first}, certs...)
			}
			first["certificateFile"], first["keyFile"] = in.TLS.CertFile, in.TLS.KeyFile
			t["certificates"] = certs
		}
	case SecReality:
		r := ss.Ensure("realitySettings")
		if _, ok := r["show"]; !ok {
			r["show"] = false
		}
		if _, ok := r["xver"]; !ok {
			r["xver"] = float64(0)
		}
		delete(r, "dest") // the current name is "target"
		r["target"] = in.Reality.Dest
		r["serverNames"] = toAny(in.Reality.ServerNames)
		r["privateKey"] = in.Reality.PrivateKey
		r["shortIds"] = toAny(in.Reality.ShortIDs)
		rs := Doc(r).Ensure("settings")
		rs["publicKey"] = in.Reality.PublicKey
		if in.Reality.SpiderX != "" {
			rs["spiderX"] = in.Reality.SpiderX
		} else {
			delete(rs, "spiderX")
		}
	}

	if !in.AcceptProxyProtocol {
		for _, k := range []string{"tcpSettings", "rawSettings", "wsSettings", "httpupgradeSettings", "sockopt"} {
			delete(ss.Sub(k), "acceptProxyProtocol")
		}
		if so := ss.Sub("sockopt"); so != nil && len(so) == 0 {
			delete(ss, "sockopt")
		}
	} else if !anyProxyProtocol(ss) {
		ss.Ensure("sockopt")["acceptProxyProtocol"] = true
	}
}

func anyProxyProtocol(ss Doc) bool {
	for _, k := range []string{"tcpSettings", "rawSettings", "wsSettings", "httpupgradeSettings", "sockopt"} {
		if Bool(ss.Sub(k), "acceptProxyProtocol") {
			return true
		}
	}
	return false
}

// hostOf reads the Host of a transport: the "host" field, else the Host header (any capitalisation).
func hostOf(m map[string]any) string {
	if h := Str(m, "host"); h != "" {
		return h
	}
	if hd, ok := m["headers"].(map[string]any); ok {
		for k, v := range hd {
			if strings.EqualFold(k, "host") {
				switch x := v.(type) {
				case string:
					return x
				case []any:
					if len(x) > 0 {
						s, _ := x[0].(string)
						return s
					}
				}
			}
		}
	}
	return ""
}

// setHost stores the Host where the document already keeps it (the Host header, the "host" field, or both).
func setHost(m map[string]any, host string, viaHeader bool) {
	hd, _ := m["headers"].(map[string]any)
	hk := ""
	for k := range hd {
		if strings.EqualFold(k, "host") {
			hk = k
		}
	}
	if host == "" {
		delete(m, "host")
		if hk != "" {
			delete(hd, hk)
		}
		return
	}
	if hk != "" {
		hd[hk] = host
	}
	if hk == "" && viaHeader {
		if hd == nil {
			hd = map[string]any{}
			m["headers"] = hd
		}
		hd["Host"] = host
		return
	}
	if _, ok := m["host"]; ok || hk == "" {
		m["host"] = host
	}
}

func toAny(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}

// ---- values the client link needs that the typed view does not carry ----

func (in Inbound) stream(key string) map[string]any { return in.StreamSettings.Sub(key) }

// Encryption is the VLESS "encryption" string clients must use ("none" when the inbound does not encrypt).
func (in Inbound) Encryption() string {
	if in.Protocol != ProtoVLESS {
		return ""
	}
	if e := Str(in.Settings, "encryption"); e != "" {
		return e
	}
	return "none"
}

// TCPHeader returns the HTTP obfuscation of a raw/tcp transport: ("http", host, path) or ("none", "", "").
func (in Inbound) TCPHeader() (typ, host, path string) {
	t := in.stream("tcpSettings")
	if t == nil {
		t = in.stream("rawSettings")
	}
	h, _ := t["header"].(map[string]any)
	if Str(h, "type") != "http" {
		return "none", "", ""
	}
	req, _ := h["request"].(map[string]any)
	if hd, ok := req["headers"].(map[string]any); ok {
		for k, v := range hd {
			if strings.EqualFold(k, "host") {
				if l := anyStrings(v); len(l) > 0 {
					host = strings.Join(l, ",")
				}
			}
		}
	}
	if p := Strs(req, "path"); len(p) > 0 {
		path = strings.Join(p, ",")
	}
	return "http", host, path
}

// XHTTPExtra holds the XHTTP options a client has to know besides path, host and mode.
func (in Inbound) XHTTPExtra() map[string]any {
	x := in.stream("xhttpSettings")
	if x == nil {
		return nil
	}
	keys := []string{"headers", "xPaddingBytes", "xPaddingObfsMode", "xPaddingKey", "xPaddingHeader", "xPaddingPlacement",
		"xPaddingMethod", "noGRPCHeader", "scMaxEachPostBytes", "scMinPostsIntervalMs", "uplinkHTTPMethod",
		"sessionIDPlacement", "sessionIDKey", "sessionIDLength", "seqPlacement", "seqKey", "uplinkDataPlacement", "uplinkDataKey", "uplinkChunkSize"}
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := x[k]; ok && !isEmpty(v) {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FinalMask returns the transport masks, which clients have to mirror.
func (in Inbound) FinalMask() map[string]any {
	if m := in.stream("finalmask"); len(m) > 0 {
		return m
	}
	return nil
}

// KCPHeader is the mKCP header type and seed (empty when unused).
func (in Inbound) KCPHeader() (header, seed string) {
	k := in.stream("kcpSettings")
	h, _ := k["header"].(map[string]any)
	return Str(h, "type"), Str(k, "seed")
}

// Fingerprint is the uTLS fingerprint saved with the security settings.
func (in Inbound) Fingerprint() string {
	switch in.Security {
	case SecReality:
		rs, _ := in.stream("realitySettings")["settings"].(map[string]any)
		return Str(rs, "fingerprint")
	case SecTLS:
		ts, _ := in.stream("tlsSettings")["settings"].(map[string]any)
		return Str(ts, "fingerprint")
	}
	return ""
}

// MLDSAVerify is the public part of the REALITY post-quantum signature key (the "pqv" link parameter).
func (in Inbound) MLDSAVerify() string {
	rs, _ := in.stream("realitySettings")["settings"].(map[string]any)
	return Str(rs, "mldsa65Verify")
}

// ECHConfigList is the TLS Encrypted Client Hello configuration clients use.
func (in Inbound) ECHConfigList() string {
	ts, _ := in.stream("tlsSettings")["settings"].(map[string]any)
	return Str(ts, "echConfigList")
}

// AllowInsecure reports whether the saved TLS settings tell clients to skip certificate verification.
func (in Inbound) AllowInsecure() bool {
	ts, _ := in.stream("tlsSettings")["settings"].(map[string]any)
	return Bool(ts, "allowInsecure")
}

func anyStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// SanitizeStrings trims and drops empty entries, keeping order and removing duplicates.
func SanitizeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
