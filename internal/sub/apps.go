package sub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"anypanel2/internal/model"
)

var (
	happUA = regexp.MustCompile(`(?i)\bhapp\b`)
	incyUA = regexp.MustCompile(`(?i)\bincy\b`)

	happRouteRe = regexp.MustCompile(`^happ://routing/(onadd|add)/[A-Za-z0-9+/_-]+={0,2}$`)

	incyRangeRe   = regexp.MustCompile(`^\d+(-\d+)?$`)
	incyHexRe     = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)
	incyPacketsRe = regexp.MustCompile(`^\d+-\d+$`)
	routeLinkRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://\S+$`)
)

// IsHapp and IsIncy recognise the client apps by User-Agent.
func IsHapp(userAgent string) bool { return happUA.MatchString(userAgent) }
func IsIncy(userAgent string) bool { return incyUA.MatchString(userAgent) }

// HappRoutingLink turns the stored Happ routing profile into the value of the Routing header. It accepts a
// happ://routing/onadd|add/<base64> link, happ://routing/off, or a JSON profile (encoded here).
func HappRoutingLink(rules string) (string, error) {
	rules = strings.TrimSpace(rules)
	switch {
	case rules == "":
		return "", nil
	case rules == "happ://routing/off":
		return rules, nil
	case strings.HasPrefix(rules, "{"):
		var probe map[string]any
		if err := json.Unmarshal([]byte(rules), &probe); err != nil {
			return "", errors.New("the Happ routing profile is not valid JSON")
		}
		return "happ://routing/onadd/" + base64.StdEncoding.EncodeToString([]byte(rules)), nil
	case happRouteRe.MatchString(rules):
		return rules, nil
	}
	return "", errors.New("Happ routing must be a happ://routing/onadd|add/… link, happ://routing/off or a JSON profile")
}

// IncyRoutingLine validates the single-line routing link that INCY reads from the subscription body.
func IncyRoutingLine(rules string) (string, error) {
	rules = strings.TrimSpace(rules)
	if rules == "" {
		return "", nil
	}
	if strings.ContainsAny(rules, "\r\n") || !routeLinkRe.MatchString(rules) {
		return "", errors.New("INCY routing must be one link such as incy://routing/… on a single line")
	}
	return rules, nil
}

// ExtraHeaders returns the headers beyond the common ones: routing, Hide-Settings and the app-management headers of
// the app that is asking. Apps are told apart by User-Agent, and an app's own headers are sent only when its
// auto-detect switch is on, so a panel with nothing configured never changes how any app behaves.
func ExtraHeaders(s model.SubSettings, userAgent string) map[string]string {
	return ExtraHeadersFor(s, userAgent, s.Happ.BannerFor(""))
}

// ExtraHeadersFor is ExtraHeaders with the Happ banner chosen (and its placeholders expanded) by the caller for the
// subscriber's state.
func ExtraHeadersFor(s model.SubSettings, userAgent string, banner model.HappBanner) map[string]string {
	h := map[string]string{}
	incy := IsIncy(userAgent)
	happManaged := s.Happ.AutoDetect && IsHapp(userAgent)

	// INCY takes its routing from the body, so the Happ routing header is withheld from it.
	link := ""
	if s.Happ.EnableRouting && !incy {
		link, _ = HappRoutingLink(s.Happ.RoutingRules)
	}
	switch {
	case link != "":
		h["Routing-Enable"] = "true"
		h["Routing"] = link
	case happManaged:
		h["Routing-Enable"] = "0" // undo a profile pushed earlier
	}

	switch {
	case s.HideSettings:
		h["Hide-Settings"] = "1"
	case happManaged:
		h["Hide-Settings"] = "0"
	}

	if happManaged {
		happHeaders(h, s.Happ, banner)
	}
	if s.Incy.AutoDetect && incy {
		incyHeaders(h, s.Incy)
	}
	return h
}

// ExtraBodyLines returns extra lines for the subscription body. INCY reads its routing profile from one.
func ExtraBodyLines(s model.SubSettings, userAgent string) []string {
	if !s.Incy.EnableRouting || !IsIncy(userAgent) {
		return nil
	}
	if line, err := IncyRoutingLine(s.Incy.RoutingRules); err == nil && line != "" {
		return []string{line}
	}
	return nil
}

func clean(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(v), "\r", ""), "\n", "")
}

// cleanLines is clean for text that may have several lines: line breaks stay, normalised to \n.
func cleanLines(v string) string {
	v = strings.ReplaceAll(strings.ReplaceAll(v, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(strings.TrimSpace(v), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func happHeaders(h map[string]string, c model.HappSettings, banner model.HappBanner) {
	set := func(k, v string) {
		if v != "" {
			h[k] = v
		}
	}
	set("ProviderID", strings.TrimSpace(c.ProviderID))
	set("New-Url", strings.TrimSpace(c.NewURL))
	set("Fallback-Url", strings.TrimSpace(c.FallbackURL))

	if text := cleanLines(banner.Text); text != "" {
		color := strings.ToLower(strings.TrimSpace(banner.Color))
		switch color {
		case "", "primary", "info":
			color = "blue"
		case "success":
			color = "green"
		case "warning", "danger":
			color = "red"
		}
		h["Sub-Info-Color"] = color
		// A header cannot hold a line break, so a multi-line text goes as base64 (the form Happ takes for announcements).
		if strings.Contains(text, "\n") {
			text = HeaderValue(text)
		}
		h["Sub-Info-Text"] = text
		set("Sub-Info-Button-Text", clean(banner.ButtonText))
		set("Sub-Info-Button-Link", clean(banner.ButtonLink))
	}
	if c.SubExpire {
		h["Sub-Expire"] = "1"
		set("Sub-Expire-Button-Link", strings.TrimSpace(c.SubExpireButtonLink))
	}
	if c.NotificationExpire {
		h["Notification-Subs-Expire"] = "1"
	}
	if c.NoLimit {
		h["No-Limit-Enabled"] = "1"
	}
	if c.AlwaysHwid {
		h["Subscription-Always-Hwid-Enable"] = "1"
	}
	set("Tun-Mode", strings.TrimSpace(c.TunMode))
	set("Tun-Type", strings.TrimSpace(c.TunType))
	set("Exclude-Routes", strings.TrimSpace(c.ExcludeRoutes))
	if c.ExcludeApns {
		h["Exclude-Apns-Enable"] = "true"
	}
	set("Color-Profile", clean(c.ColorProfile))
	if ping := strings.TrimSpace(c.PingType); ping != "" {
		if strings.EqualFold(ping, "http") {
			ping = "proxy"
		}
		h["Ping-Type"] = ping
	}
	if c.AutoConnect {
		h["Subscription-Autoconnect"] = "1"
		switch t := strings.TrimSpace(c.AutoConnectType); strings.ToLower(t) {
		case "fastest":
			h["Subscription-Autoconnect-Type"] = "lowestdelay"
		case "last":
			h["Subscription-Autoconnect-Type"] = "lastused"
		case "":
		default:
			h["Subscription-Autoconnect-Type"] = t
		}
	}
	// Happ's local SOCKS/HTTP inbounds have no authentication by default, so any app on the device could use them.
	if mode := strings.TrimSpace(c.LocalProxyAuth); mode != "" {
		h["Socks-Auth-Mode"] = mode
		h["Http-Auth-Mode"] = mode
	}
	if mode := strings.TrimSpace(c.PerAppMode); mode != "" && mode != "off" {
		switch strings.ToLower(mode) {
		case "include":
			mode = "on"
		case "exclude":
			mode = "bypass"
		}
		h["Per-App-Proxy-Mode"] = mode
		set("Per-App-Proxy-List", appList(c.PerAppList))
	}
}

func onOff(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return "1"
	case "0", "false", "no", "off":
		return "0"
	}
	return ""
}

func oneOf(v string, allowed ...string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return ""
}

func matching(v string, re *regexp.Regexp) string {
	if v = strings.TrimSpace(v); re.MatchString(v) {
		return v
	}
	return ""
}

// appList joins a comma- or line-separated package list into CSV: a header cannot carry newlines.
func appList(v string) string {
	var out []string
	for _, e := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return strings.Join(out, ",")
}

// text passes non-ASCII text as base64:<…>, as the INCY documentation requires.
func text(v string) string {
	v = clean(v)
	if v == "" {
		return ""
	}
	return HeaderValue(v)
}

func incyHeaders(h map[string]string, c model.IncySettings) {
	set := func(k, v string) {
		if v != "" {
			h[k] = v
		}
	}
	set("Profile-Description", text(c.ProfileDescription))
	set("Sort-Order", oneOf(c.SortOrder, "none", "ping", "name"))
	set("Support-Email", clean(c.SupportEmail))
	set("Announce-Url", clean(c.AnnounceURL))
	set("Premium-Url", clean(c.PremiumURL))

	set("Banner-Text", text(c.BannerText))
	set("Banner-Button-Text", text(c.BannerButtonText))
	set("Banner-Button-Url", clean(c.BannerButtonURL))
	set("Banner-Bg-Color", matching(c.BannerBgColor, incyHexRe))
	set("Banner-Button-Color", matching(c.BannerButtonColor, incyHexRe))

	set("Hide-Url", onOff(c.HideURL))
	set("Hide-Check", onOff(c.HideCheck))
	set("No-Limit-Enabled", onOff(c.NoLimitEnabled))

	set("Per-App-Proxy-Enable", onOff(c.PerAppEnable))
	set("Per-App-Proxy-Mode", oneOf(c.PerAppMode, "bypass", "proxy"))
	set("Per-App-Proxy-List", appList(c.PerAppList))

	set("Fragmentation-Enable", onOff(c.FragmentationEnable))
	set("Fragmentation-Length", matching(c.FragmentationLength, incyRangeRe))
	set("Fragmentation-Interval", matching(c.FragmentationInterval, incyRangeRe))
	if p := strings.ToLower(strings.TrimSpace(c.FragmentationPackets)); p == "tlshello" || p == "all" || p == "1" || incyPacketsRe.MatchString(p) {
		h["Fragmentation-Packets"] = p
	}

	set("Noises-Enable", onOff(c.NoisesEnable))
	set("Noises-Type", oneOf(c.NoisesType, "rand", "str", "hex"))
	set("Noises-Packet", clean(c.NoisesPacket))
	set("Noises-Delay", matching(c.NoisesDelay, incyRangeRe))

	set("Server-Address-Resolve-Enable", onOff(c.ResolveEnable))
	set("Server-Address-Resolve-Dns-Domain", clean(c.ResolveDNSDomain))
	set("Server-Address-Resolve-Dns-Ip", clean(c.ResolveDNSIP))
}
