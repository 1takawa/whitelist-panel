// Package xray builds Xray-core configuration from panel entities and supervises the Xray process.
package xray

import (
	"encoding/json"
	"fmt"
	"time"

	"anypanel2/internal/model"
)

// Options are server-wide settings for the generated config.
type Options struct {
	APIPort         int    // local gRPC API port used for statistics
	LogLevel        string // debug | info | warning | error | none
	BlockBittorrent bool   // the default for machines whose profile does not say otherwise
	// Cascades send the traffic of some inbounds through an outbound to another server (see Cascade).
	Cascades []Cascade
	// Profile is the machine's own Xray profile (log level, routing, template); it overrides the fields above.
	Profile model.XrayProfile
}

type obj = map[string]any

// privateRanges are the networks the "block private addresses" option refuses to connect to.
var privateRanges = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
}

// InboundTag is the Xray tag of a panel inbound.
func InboundTag(id string) string { return "in-" + id }

// vlessEncrypted reports whether the inbound uses VLESS encryption (decryption other than "none").
func vlessEncrypted(in model.Inbound) bool {
	d := model.Str(in.Settings, "decryption")
	return in.Protocol == model.ProtoVLESS && d != "" && d != "none"
}

// FlowAllowed reports whether a client flow applies to the inbound (xtls-rprx-vision needs VLESS over raw TCP with
// TLS, REALITY or VLESS encryption).
func FlowAllowed(in model.Inbound) bool {
	if in.DisableFlow || in.Protocol != model.ProtoVLESS || in.Network != model.NetTCP {
		return false
	}
	return in.Security == model.SecTLS || in.Security == model.SecReality || vlessEncrypted(in)
}

// BuildConfig renders the Xray config. Disabled, used-up and expired inbounds and inactive clients (disabled,
// expired, over quota) are omitted.
func BuildConfig(inbounds []model.Inbound, clients []model.Client, opt Options, now time.Time) ([]byte, error) {
	if opt.APIPort == 0 {
		opt.APIPort = 62789
	}
	prof := opt.Profile
	logLevel := firstNonEmpty(prof.LogLevel, opt.LogLevel, "warning")
	blockBT := opt.BlockBittorrent
	switch prof.BitTorrent {
	case "block":
		blockBT = true
	case "allow":
		blockBT = false
	}

	ins := []any{obj{
		"tag": "api", "listen": "127.0.0.1", "port": opt.APIPort, "protocol": "dokodemo-door",
		"settings": obj{"address": "127.0.0.1"},
	}}
	present := map[string]bool{}
	for _, in := range inbounds {
		if !in.Running(now) {
			continue
		}
		ins = append(ins, buildInbound(in, clients, now))
		present[in.ID] = true
	}

	rules := []any{obj{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"}}
	if blockBT {
		rules = append(rules, obj{"type": "field", "protocol": []string{"bittorrent"}, "outboundTag": "blocked"})
	}
	if prof.BlockPrivate {
		rules = append(rules, obj{"type": "field", "ip": privateRanges, "outboundTag": "blocked"})
	}
	outs := []any{
		obj{"tag": "direct", "protocol": "freedom"},
		obj{"tag": "blocked", "protocol": "blackhole"},
	}
	// A cascade is a rule plus an outbound, after the blocking rules so a cascaded inbound is still filtered by them.
	// There is no fallback: if the exit cannot be reached the connection fails, it never leaves from this machine.
	for _, c := range opt.Cascades {
		if !present[c.InboundID] {
			continue
		}
		o := obj{}
		for k, v := range c.Outbound {
			o[k] = v
		}
		o["tag"] = c.Tag
		outs = append(outs, o)
		rules = append(rules, obj{"type": "field", "inboundTag": []string{InboundTag(c.InboundID)}, "outboundTag": c.Tag})
	}

	cfg := obj{
		"log":   obj{"loglevel": logLevel},
		"api":   obj{"tag": "api", "services": []string{"StatsService"}},
		"stats": obj{},
		"policy": obj{
			"levels": obj{"0": obj{"statsUserUplink": true, "statsUserDownlink": true}},
			"system": obj{"statsInboundUplink": true, "statsInboundDownlink": true},
		},
		"inbounds":  ins,
		"outbounds": outs,
		"routing":   obj{"domainStrategy": firstNonEmpty(prof.DomainStrategy, "AsIs"), "rules": rules},
	}
	applyTemplate(cfg, prof.Template)
	return json.MarshalIndent(cfg, "", "  ")
}

// applyTemplate merges a profile template over the generated config (see model.XrayProfile.Template).
func applyTemplate(cfg obj, tpl map[string]any) {
	for k, v := range tpl {
		switch k {
		case "inbounds":
			continue // owned by the panel
		case "outbounds":
			cfg["outbounds"] = mergeOutbounds(asList(cfg["outbounds"]), asList(v))
		case "routing":
			rm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			routing := cfg["routing"].(obj)
			for rk, rv := range rm {
				if rk != "rules" {
					routing[rk] = rv
					continue
				}
				extra := asList(rv)
				cur := asList(routing["rules"])
				if len(cur) == 0 {
					routing["rules"] = extra
					continue
				}
				merged := append([]any{cur[0]}, extra...) // the API rule stays first
				routing["rules"] = append(merged, cur[1:]...)
			}
		default:
			if sm, ok := v.(map[string]any); ok {
				if dm, ok := cfg[k].(obj); ok {
					model.MergeDoc(dm, sm)
					continue
				}
			}
			cfg[k] = v
		}
	}
}

func asList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []obj:
		out := make([]any, len(x))
		for i := range x {
			out[i] = x[i]
		}
		return out
	}
	return nil
}

// mergeOutbounds adds the template's outbounds; one with the tag of a generated outbound replaces it.
func mergeOutbounds(cur, extra []any) []any {
	out := append([]any(nil), cur...)
	for _, e := range extra {
		em, _ := e.(map[string]any)
		tag, _ := em["tag"].(string)
		replaced := false
		for i, c := range out {
			if cm, _ := c.(map[string]any); tag != "" && cm["tag"] == tag {
				out[i], replaced = e, true
				break
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out
}

func buildInbound(in model.Inbound, clients []model.Client, now time.Time) obj {
	in.EnsureRaw()
	listen := in.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}

	var users []any
	for _, c := range clients {
		if !c.Uses(in.ID) || !c.Active(now) {
			continue
		}
		switch in.Protocol {
		case model.ProtoVLESS:
			u := obj{"id": c.UUID, "email": c.Email, "level": 0}
			if c.Flow != "" && FlowAllowed(in) {
				u["flow"] = c.Flow
			}
			users = append(users, u)
		case model.ProtoTrojan:
			users = append(users, obj{"password": c.Password, "email": c.Email, "level": 0})
		}
	}
	if users == nil {
		users = []any{}
	}

	settings := in.Settings.Clone()
	if settings == nil {
		settings = model.Doc{}
	}
	delete(settings, "encryption") // the client's half of VLESS encryption; Xray's inbound only knows "decryption"
	settings["clients"] = users
	if in.Protocol == model.ProtoVLESS && model.Str(settings, "decryption") == "" {
		settings["decryption"] = "none"
	}

	out := obj{
		"tag": InboundTag(in.ID), "listen": listen, "port": in.Port, "protocol": in.Protocol,
		"settings": settings, "streamSettings": buildStream(in),
	}
	if in.Sniffing.Enabled {
		dest := in.Sniffing.DestOverride
		if len(dest) == 0 {
			dest = []string{"http", "tls", "quic"}
		}
		sn := obj{"enabled": true, "destOverride": dest, "routeOnly": in.Sniffing.RouteOnly}
		if in.Sniffing.MetadataOnly {
			sn["metadataOnly"] = true
		}
		if len(in.Sniffing.DomainsExcluded) > 0 {
			sn["domainsExcluded"] = in.Sniffing.DomainsExcluded
		}
		if len(in.Sniffing.IPsExcluded) > 0 {
			sn["ipsExcluded"] = in.Sniffing.IPsExcluded
		}
		out["sniffing"] = sn
	}
	return out
}

// buildStream returns the inbound's streamSettings without the sections of the transports and security modes that
// are not selected, so a switch of transport in the form leaves no stale settings in the running config.
func buildStream(in model.Inbound) model.Doc {
	s := in.StreamSettings.Clone()
	network := model.NormalizeNetwork(model.Str(s, "network"))
	s["network"] = network
	keep := map[string]string{
		model.NetTCP: "tcpSettings", model.NetWS: "wsSettings", model.NetGRPC: "grpcSettings",
		model.NetXHTTP: "xhttpSettings", model.NetHTTPUpgrade: "httpupgradeSettings", model.NetKCP: "kcpSettings",
	}
	for _, k := range []string{"tcpSettings", "rawSettings", "wsSettings", "grpcSettings", "xhttpSettings", "httpupgradeSettings", "kcpSettings"} {
		if k != keep[network] && !(network == model.NetTCP && k == "rawSettings") {
			delete(s, k)
		}
	}
	switch model.Str(s, "security") {
	case model.SecTLS:
		delete(s, "realitySettings")
	case model.SecReality:
		delete(s, "tlsSettings")
		if r := s.Sub("realitySettings"); r != nil {
			// Every Xray version understands "dest"; "target" is the newer spelling of the same field.
			if t := model.Str(r, "target"); t != "" {
				r["dest"] = t
				delete(r, "target")
			}
		}
	default:
		s["security"] = model.SecNone
		delete(s, "tlsSettings")
		delete(s, "realitySettings")
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ErrNotInstalled is returned when the Xray binary is missing.
var ErrNotInstalled = fmt.Errorf("xray is not installed")

// Cascade routes everything that enters one inbound through an outbound to another server. It takes effect on the
// machine that has the inbound; on every other machine it is ignored.
type Cascade struct {
	InboundID string         // the panel inbound (the entry of the chain)
	Tag       string         // outbound tag
	Outbound  map[string]any // a complete Xray outbound towards the exit server (its "tag" is replaced by Tag)
}

// CascadeTag is the outbound tag of a cascade.
func CascadeTag(id string) string { return "cascade-" + id }
