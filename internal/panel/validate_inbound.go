package panel

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"

	"anypanel2/internal/model"
)

// maxDocBytes bounds the Xray documents of one inbound (a certificate pasted inline is the largest thing in there).
const maxDocBytes = 256 << 10

var clientVerRe = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}$`)

func checkDocSize(in *model.Inbound) error {
	for name, d := range map[string]model.Doc{"settings": in.Settings, "streamSettings": in.StreamSettings} {
		b, err := json.Marshal(map[string]any(d))
		if err != nil {
			return invalid("%s is not valid JSON", name)
		}
		if len(b) > maxDocBytes {
			return invalid("%s is too large", name)
		}
	}
	return nil
}

// number reads a JSON number. ok is false when the key is absent; a value of another type is an error.
func number(m map[string]any, key, what string) (v float64, ok bool, err error) {
	raw, present := m[key]
	if !present || raw == nil {
		return 0, false, nil
	}
	switch x := raw.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false, invalid("%s must be a number", what)
		}
		return x, true, nil
	}
	return 0, false, invalid("%s must be a number", what)
}

func mustObject(m map[string]any, key, what string) error {
	if v, ok := m[key]; ok && v != nil {
		if _, isObj := v.(map[string]any); !isObj {
			return invalid("%s must be an object", what)
		}
	}
	return nil
}

// normalizeXrayDocs checks the parts of the Xray documents the panel does not model field by field. Anything Xray
// itself can judge is left to `xray run -test`, which runs before an inbound is saved.
func normalizeXrayDocs(in *model.Inbound) error {
	ss, s := in.StreamSettings, in.Settings

	for _, k := range []string{"tcpSettings", "rawSettings", "wsSettings", "grpcSettings", "xhttpSettings", "httpupgradeSettings",
		"kcpSettings", "tlsSettings", "realitySettings", "sockopt", "finalmask"} {
		if err := mustObject(ss, k, k); err != nil {
			return err
		}
	}

	switch in.Security {
	case model.SecReality:
		r := ss.Sub("realitySettings")
		if v, ok, err := number(r, "xver", "REALITY xver"); err != nil {
			return err
		} else if ok && (v < 0 || v > 2 || v != math.Trunc(v)) {
			return invalid("REALITY xver must be 0, 1 or 2")
		}
		if v, ok, err := number(r, "maxTimediff", "REALITY max time difference"); err != nil {
			return err
		} else if ok && (v < 0 || v != math.Trunc(v)) {
			return invalid("REALITY max time difference must be a non-negative number of milliseconds")
		}
		// Xray-панелей calls these minClient/maxClient; Xray reads minClientVer/maxClientVer, so the old spelling is moved.
		for _, p := range [][2]string{{"minClient", "minClientVer"}, {"maxClient", "maxClientVer"}} {
			if old, ok := r[p[0]]; ok {
				if _, has := r[p[1]]; !has {
					r[p[1]] = old
				}
				delete(r, p[0])
			}
			if v := strings.TrimSpace(model.Str(r, p[1])); v != "" && !clientVerRe.MatchString(v) {
				return invalid("REALITY %s must look like 1.8.0", p[1])
			}
		}
		for _, k := range []string{"limitFallbackUpload", "limitFallbackDownload"} {
			if err := mustObject(r, k, "REALITY "+k); err != nil {
				return err
			}
		}
	case model.SecTLS:
		if err := checkTLSCertificates(ss.Sub("tlsSettings")); err != nil {
			return err
		}
	}

	if err := checkFinalMask(ss.Sub("finalmask"), in.Security); err != nil {
		return err
	}
	return normalizeProtocolSettings(in, s)
}

func checkTLSCertificates(t map[string]any) error {
	certs, _ := t["certificates"].([]any)
	if len(certs) == 0 {
		return invalid("TLS needs at least one certificate (files or pasted content)")
	}
	for _, c := range certs {
		cm, ok := c.(map[string]any)
		if !ok {
			return invalid("every TLS certificate must be an object")
		}
		byFile := strings.TrimSpace(model.Str(cm, "certificateFile")) != "" && strings.TrimSpace(model.Str(cm, "keyFile")) != ""
		byContent := len(model.Strs(cm, "certificate")) > 0 && len(model.Strs(cm, "key")) > 0
		if !byFile && !byContent {
			return invalid("every TLS certificate needs both a certificate and a key, as files or as pasted content")
		}
	}
	return nil
}

// checkFinalMask validates the transport mask lists: every entry needs a type.
func checkFinalMask(fm map[string]any, security string) error {
	for _, k := range []string{"tcp", "udp"} {
		v, ok := fm[k]
		if !ok || v == nil {
			continue
		}
		list, isList := v.([]any)
		if !isList {
			return invalid("finalmask.%s must be a list", k)
		}
		for _, e := range list {
			m, ok := e.(map[string]any)
			if !ok || strings.TrimSpace(model.Str(m, "type")) == "" {
				return invalid("every finalmask.%s entry needs a type", k)
			}
			if err := mustObject(m, "settings", "finalmask settings"); err != nil {
				return err
			}
			// Found on Xray 26.10.29: a REALITY inbound with the fragment mask passes `xray run -test` and then panics
			// on the first connection, leaving the machine in a crash loop. Fragmenting is a client-side option anyway,
			// so it is refused on every inbound.
			if k == "tcp" && model.Str(m, "type") == "fragment" {
				return invalid("the fragment mask is a client-side option and cannot be used on an inbound: with REALITY Xray crashes on the first connection (set it in the client instead)")
			}
		}
	}
	return mustObject(fm, "quicParams", "finalmask.quicParams")
}

func normalizeProtocolSettings(in *model.Inbound, s model.Doc) error {
	if in.Protocol == model.ProtoVLESS {
		for _, k := range []string{"decryption", "encryption"} {
			v := model.Str(s, k)
			if k == "decryption" && v == "" {
				s[k] = "none"
				continue
			}
			if v != "" && v != "none" && !strings.HasPrefix(v, "mlkem768x25519plus.") {
				return invalid("VLESS %s must be \"none\" or a string made by the key generator (mlkem768x25519plus.…)", k)
			}
		}
		if v, ok := s["testseed"]; ok && v != nil {
			list, isList := v.([]any)
			if !isList || len(list) != 4 {
				return invalid("Vision testseed must be four numbers")
			}
			for _, e := range list {
				if n, isNum := e.(float64); !isNum || n < 0 || n != math.Trunc(n) {
					return invalid("Vision testseed must be four non-negative integers")
				}
			}
		}
	} else {
		delete(s, "decryption")
		delete(s, "encryption")
		delete(s, "testseed")
	}

	if v, ok := s["fallbacks"]; ok && v != nil {
		list, isList := v.([]any)
		if !isList {
			return invalid("fallbacks must be a list")
		}
		if len(list) > 64 {
			return invalid("at most 64 fallbacks")
		}
		for i, e := range list {
			f, ok := e.(map[string]any)
			if !ok {
				return invalid("fallback %d must be an object", i+1)
			}
			switch d := f["dest"].(type) {
			case string:
				if strings.TrimSpace(d) == "" {
					return invalid("fallback %d needs a destination", i+1)
				}
			case float64:
				if d < 1 || d > 65535 || d != math.Trunc(d) {
					return invalid("fallback %d: destination port must be 1–65535", i+1)
				}
			default:
				return invalid("fallback %d needs a destination (a port, host:port or a socket path)", i+1)
			}
			if p := model.Str(f, "path"); p != "" && !strings.HasPrefix(p, "/") {
				return invalid("fallback %d: path must start with /", i+1)
			}
			if v, ok, err := number(f, "xver", "fallback xver"); err != nil {
				return err
			} else if ok && (v < 0 || v > 2 || v != math.Trunc(v)) {
				return invalid("fallback %d: xver must be 0, 1 or 2", i+1)
			}
		}
	}
	return nil
}
