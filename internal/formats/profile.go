package formats

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// The routing profile of Happ and INCY normally reaches the app in a header. A subscription in the Xray JSON format may
// be read by an app that ignores the header (the mobile Happ does not apply it for sure), and then every address goes
// through the proxy, Russian banks and Yandex included. So the profile is also written into each JSON config as ordinary
// Xray routing rules, which every app honours.
//
// Only what any geo file has is written. The profiles of RoscomVPN name tags that exist only in their own geo files
// (geosite:whitelist, geoip:direct…); a tag missing from the app's files makes Xray refuse the whole config, which is
// worse than a missing rule. Those tags are left to the header, and the .ru, .su and .рф names stand in for the Russian
// lists when the profile sends them direct.

type happProfile struct {
	RouteOrder string   `json:"RouteOrder"`
	DirectSite []string `json:"DirectSites"`
	DirectIP   []string `json:"DirectIp"`
	ProxySite  []string `json:"ProxySites"`
	ProxyIP    []string `json:"ProxyIp"`
	BlockSite  []string `json:"BlockSites"`
	BlockIP    []string `json:"BlockIp"`
	Strategy   string   `json:"DomainStrategy"`
}

// safeGeo are the geo tags that standard geo files contain, so a config with them loads whichever files the app has.
var safeGeo = map[string]bool{
	"geosite:private": true, "geosite:category-ru": true, "geosite:microsoft": true, "geosite:apple": true,
	"geosite:youtube": true, "geosite:telegram": true, "geosite:github": true, "geosite:google-play": true,
	"geoip:private": true,
}

// russianNames stand in for the Russian lists (category-ru, whitelist) of a profile that sends them direct: the Russian
// zones, and the big Russian services whose names are not in them (Yandex lives on yandex.net and yastatic.net, VK on
// vk.com and userapi.com…). Nothing here depends on a geo file.
var russianNames = []string{
	"domain:ru", "domain:su", "domain:xn--p1ai", "domain:xn--80asehdb", "domain:xn--c1avg",
	"domain:yandex.com", "domain:yandex.net", "domain:yandex.by", "domain:yandex.kz", "domain:yandex.uz", "domain:yandex.cloud",
	"domain:yandexcloud.net", "domain:yastatic.net", "domain:yango.com", "domain:ya.cc", "domain:yadi.sk",
	"domain:vk.com", "domain:vk.me", "domain:vkuser.net", "domain:userapi.com", "domain:vk-cdn.net", "domain:mycdn.me",
	"domain:2gis.com", "domain:wbstatic.net", "domain:avito.st", "domain:ozone.ru",
}

func decodeProfile(raw string) (*happProfile, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasSuffix(raw, "/off") {
		return nil, false
	}
	if !strings.HasPrefix(raw, "{") {
		i := strings.LastIndex(raw, "/")
		if i < 0 {
			return nil, false
		}
		payload := strings.TrimRight(raw[i+1:], "=")
		b, err := base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			if b, err = base64.RawURLEncoding.DecodeString(payload); err != nil {
				return nil, false
			}
		}
		raw = string(b)
	}
	var p happProfile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, false
	}
	return &p, true
}

// keep drops the tags a geo file may not have and any entry that is not a plain name or address.
func keep(list []string) []string {
	var out []string
	for _, v := range list {
		v = strings.TrimSpace(v)
		switch {
		case v == "" || strings.HasPrefix(v, "ext:"):
		case strings.HasPrefix(v, "geosite:") || strings.HasPrefix(v, "geoip:"):
			if safeGeo[v] {
				out = append(out, v)
			}
		default:
			out = append(out, v)
		}
	}
	return out
}

// profileRules turns the profile (a happ:// or incy:// link, or its JSON) into Xray routing rules in the profile's own
// order of block, proxy and direct, and the domain strategy they need. It returns nothing for a profile that is off,
// unreadable or leaves nothing safe to write.
func profileRules(raw string, balancer bool) ([]any, string) {
	p, ok := decodeProfile(raw)
	if !ok {
		return nil, ""
	}
	direct := keep(p.DirectSite)
	for _, v := range p.DirectSite {
		if v == "geosite:category-ru" || v == "geosite:whitelist" {
			direct = append(direct, russianNames...)
			break
		}
	}
	groups := map[string]struct{ sites, ips []string }{
		"block":  {keep(p.BlockSite), keep(p.BlockIP)},
		"proxy":  {keep(p.ProxySite), keep(p.ProxyIP)},
		"direct": {direct, keep(p.DirectIP)},
	}
	order := strings.Split(strings.ToLower(strings.TrimSpace(p.RouteOrder)), "-")
	if len(order) != 3 {
		order = []string{"block", "proxy", "direct"}
	}
	// The automatic node has no outbound called proxy: its balancer takes that place.
	send := func(name string) obj {
		if name == "proxy" && balancer {
			return obj{"balancerTag": "auto"}
		}
		return obj{"outboundTag": name}
	}
	var rules []any
	byIP := false
	for _, name := range order {
		g, ok := groups[name]
		if !ok {
			continue
		}
		if len(g.sites) > 0 {
			rules = append(rules, withTarget(obj{"type": "field", "domain": g.sites}, send(name)))
		}
		if len(g.ips) > 0 {
			rules = append(rules, withTarget(obj{"type": "field", "ip": g.ips}, send(name)))
			for _, ip := range g.ips {
				if ip != "geoip:private" {
					byIP = true
				}
			}
		}
	}
	strategy := "AsIs"
	if byIP && strings.EqualFold(p.Strategy, "IPIfNonMatch") {
		strategy = "IPIfNonMatch"
	}
	return rules, strategy
}

func withTarget(rule, target obj) obj {
	for k, v := range target {
		rule[k] = v
	}
	return rule
}
