package sub

import (
	"encoding/base64"
	"strings"
	"testing"

	"anypanel2/internal/model"
)

const (
	uaHapp  = "Happ/1.5.2 (Android)"
	uaIncy  = "INCY/2.0/ios"
	uaOther = "v2rayNG/1.8.5"
)

func TestUserAgentDetection(t *testing.T) {
	if !IsHapp(uaHapp) || !IsHapp("happ") || IsHapp("Mozilla (Happy)") || IsHapp(uaOther) {
		t.Fatal("Happ must be matched as a word")
	}
	if !IsIncy(uaIncy) || IsIncy("incyclopedia") || IsIncy(uaHapp) {
		t.Fatal("INCY must be matched as a word")
	}
}

func TestNothingConfiguredChangesNothing(t *testing.T) {
	for _, ua := range []string{uaHapp, uaIncy, uaOther, ""} {
		if h := ExtraHeaders(model.SubSettings{}, ua); len(h) != 0 {
			t.Fatalf("an untouched panel must not add headers for %q: %v", ua, h)
		}
	}
}

func TestHappHeadersOnlyForHappWithAutoDetect(t *testing.T) {
	cfg := model.SubSettings{Happ: model.HappSettings{AutoDetect: true, ProviderID: " abc ", SubInfoText: "Support: @bot", SubInfoColor: "success",
		SubInfoButtonText: "Open", SubInfoButtonLink: "https://t.me/bot", SubExpire: true, SubExpireButtonLink: "https://pay.example.com",
		NotificationExpire: true, NoLimit: true, AlwaysHwid: true, TunMode: "tun", TunType: "system", ExcludeRoutes: "10.0.0.0/8",
		ExcludeApns: true, ColorProfile: "blue", PingType: "http", AutoConnect: true, AutoConnectType: "fastest",
		PerAppMode: "include", PerAppList: "com.a\ncom.b, com.c", LocalProxyAuth: "auto", NewURL: "https://new.example.com/sub/x"}}
	h := ExtraHeaders(cfg, uaHapp)
	want := map[string]string{
		"ProviderID": "abc", "New-Url": "https://new.example.com/sub/x", "Sub-Info-Color": "green", "Sub-Info-Text": "Support: @bot",
		"Sub-Info-Button-Text": "Open", "Sub-Info-Button-Link": "https://t.me/bot", "Sub-Expire": "1", "Sub-Expire-Button-Link": "https://pay.example.com",
		"Notification-Subs-Expire": "1", "No-Limit-Enabled": "1", "Subscription-Always-Hwid-Enable": "1", "Tun-Mode": "tun", "Tun-Type": "system",
		"Exclude-Routes": "10.0.0.0/8", "Exclude-Apns-Enable": "true", "Color-Profile": "blue", "Ping-Type": "proxy",
		"Subscription-Autoconnect": "1", "Subscription-Autoconnect-Type": "lowestdelay", "Per-App-Proxy-Mode": "on",
		"Per-App-Proxy-List": "com.a,com.b,com.c", "Socks-Auth-Mode": "auto", "Http-Auth-Mode": "auto",
		"Routing-Enable": "0", "Hide-Settings": "0",
	}
	for k, v := range want {
		if h[k] != v {
			t.Errorf("%s = %q, want %q", k, h[k], v)
		}
	}
	if len(ExtraHeaders(cfg, uaOther)) != 0 {
		t.Fatal("other apps must never receive Happ headers")
	}
	cfg.Happ.AutoDetect = false
	if len(ExtraHeaders(cfg, uaHapp)) != 0 {
		t.Fatal("without auto-detect Happ headers are not sent")
	}
}

func TestHappColorAliasesAndBannerNeedsText(t *testing.T) {
	for in, want := range map[string]string{"": "blue", "info": "blue", "primary": "blue", "warning": "red", "danger": "red", "green": "green"} {
		h := ExtraHeaders(model.SubSettings{Happ: model.HappSettings{AutoDetect: true, SubInfoText: "x", SubInfoColor: in}}, uaHapp)
		if h["Sub-Info-Color"] != want {
			t.Errorf("color %q -> %q, want %q", in, h["Sub-Info-Color"], want)
		}
	}
	h := ExtraHeaders(model.SubSettings{Happ: model.HappSettings{AutoDetect: true, SubInfoColor: "red", SubInfoButtonText: "Go"}}, uaHapp)
	if _, ok := h["Sub-Info-Color"]; ok || h["Sub-Info-Button-Text"] != "" {
		t.Fatal("the banner is only sent when it has text")
	}
	h = ExtraHeaders(model.SubSettings{Happ: model.HappSettings{AutoDetect: true, PerAppMode: "off", PerAppList: "x"}}, uaHapp)
	if _, ok := h["Per-App-Proxy-Mode"]; ok {
		t.Fatal("per-app off sends nothing")
	}
}

func TestRouting(t *testing.T) {
	json := `{"Name":"Test","GlobalProxy":"true","DirectSites":["domain:ru"]}`
	cfg := model.SubSettings{Happ: model.HappSettings{EnableRouting: true, RoutingRules: json}}

	h := ExtraHeaders(cfg, uaOther)
	if h["Routing-Enable"] != "true" || !strings.HasPrefix(h["Routing"], "happ://routing/onadd/") {
		t.Fatalf("routing goes to every non-INCY client: %v", h)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h["Routing"], "happ://routing/onadd/"))
	if err != nil || string(raw) != json {
		t.Fatalf("a JSON profile is encoded into a link: %q %v", raw, err)
	}

	if h := ExtraHeaders(cfg, uaIncy); h["Routing"] != "" || h["Routing-Enable"] != "" {
		t.Fatalf("INCY must not get the Happ routing header: %v", h)
	}

	cfg.Happ.RoutingRules = "happ://routing/onadd/e30="
	if h := ExtraHeaders(cfg, ""); h["Routing"] != "happ://routing/onadd/e30=" {
		t.Fatalf("a ready link is passed through: %v", h)
	}
	cfg.Happ.RoutingRules = "happ://routing/off"
	if h := ExtraHeaders(cfg, ""); h["Routing"] != "happ://routing/off" {
		t.Fatalf("off: %v", h)
	}

	cfg.Happ.EnableRouting = false
	if h := ExtraHeaders(cfg, uaOther); len(h) != 0 {
		t.Fatalf("disabled routing sends nothing: %v", h)
	}
	cfg.Happ.AutoDetect = true
	if h := ExtraHeaders(cfg, uaHapp); h["Routing-Enable"] != "0" || h["Routing"] != "" {
		t.Fatalf("a managed Happ is told to switch routing off: %v", h)
	}
}

func TestRoutingValidation(t *testing.T) {
	for _, bad := range []string{"not a link", "happ://routing/onadd/", "happ://other/x", "{broken", "happ://routing/add/a b"} {
		if _, err := HappRoutingLink(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if l, err := HappRoutingLink("  "); err != nil || l != "" {
		t.Fatal("blank means no routing")
	}
	for _, bad := range []string{"incy routing", "incy://a\nincy://b", "://x"} {
		if _, err := IncyRoutingLine(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if l, err := IncyRoutingLine(" incy://routing/abc "); err != nil || l != "incy://routing/abc" {
		t.Fatalf("incy line: %q %v", l, err)
	}
}

func TestIncyRoutingRidesTheBody(t *testing.T) {
	cfg := model.SubSettings{Incy: model.IncySettings{EnableRouting: true, RoutingRules: "incy://routing/abc"}}
	if got := ExtraBodyLines(cfg, uaIncy); len(got) != 1 || got[0] != "incy://routing/abc" {
		t.Fatalf("INCY gets the line: %v", got)
	}
	if len(ExtraBodyLines(cfg, uaHapp)) != 0 || len(ExtraBodyLines(cfg, uaOther)) != 0 {
		t.Fatal("other apps must not see the INCY line in their subscription")
	}
	cfg.Incy.EnableRouting = false
	if len(ExtraBodyLines(cfg, uaIncy)) != 0 {
		t.Fatal("disabled")
	}
}

func TestIncyHeaders(t *testing.T) {
	cfg := model.SubSettings{Incy: model.IncySettings{AutoDetect: true,
		ProfileDescription: "Привет", SortOrder: "PING", SupportEmail: "a@b.c", BannerText: "Banner", BannerBgColor: "#112233", BannerButtonColor: "red",
		HideURL: "true", HideCheck: "0", NoLimitEnabled: "maybe", PerAppEnable: "1", PerAppMode: "proxy", PerAppList: "a\nb",
		FragmentationEnable: "on", FragmentationLength: "10-20", FragmentationInterval: "x", FragmentationPackets: "tlshello",
		NoisesEnable: "off", NoisesType: "hex", NoisesPacket: "abcd", NoisesDelay: "10-20",
		ResolveEnable: "1", ResolveDNSDomain: "dns.example.com", ResolveDNSIP: "1.1.1.1"}}
	h := ExtraHeaders(cfg, uaIncy)

	if !strings.HasPrefix(h["Profile-Description"], "base64:") {
		t.Fatalf("non-ASCII text must be base64: %q", h["Profile-Description"])
	}
	want := map[string]string{
		"Sort-Order": "ping", "Support-Email": "a@b.c", "Banner-Text": "Banner", "Banner-Bg-Color": "#112233", "Hide-Url": "1", "Hide-Check": "0",
		"Per-App-Proxy-Enable": "1", "Per-App-Proxy-Mode": "proxy", "Per-App-Proxy-List": "a,b", "Fragmentation-Enable": "1",
		"Fragmentation-Length": "10-20", "Fragmentation-Packets": "tlshello", "Noises-Enable": "0", "Noises-Type": "hex", "Noises-Packet": "abcd",
		"Noises-Delay": "10-20", "Server-Address-Resolve-Enable": "1", "Server-Address-Resolve-Dns-Domain": "dns.example.com",
		"Server-Address-Resolve-Dns-Ip": "1.1.1.1",
	}
	for k, v := range want {
		if h[k] != v {
			t.Errorf("%s = %q, want %q", k, h[k], v)
		}
	}
	for _, k := range []string{"Banner-Button-Color", "No-Limit-Enabled", "Fragmentation-Interval"} {
		if _, ok := h[k]; ok {
			t.Errorf("%s has an invalid value and must be omitted", k)
		}
	}
	if len(ExtraHeaders(cfg, uaHapp)) != 0 || len(ExtraHeaders(cfg, uaOther)) != 0 {
		t.Fatal("INCY headers go to INCY only")
	}
}

func TestHideSettings(t *testing.T) {
	if h := ExtraHeaders(model.SubSettings{HideSettings: true}, uaOther); h["Hide-Settings"] != "1" {
		t.Fatalf("hide: %v", h)
	}
	if h := ExtraHeaders(model.SubSettings{Happ: model.HappSettings{AutoDetect: true}}, uaHapp); h["Hide-Settings"] != "0" {
		t.Fatalf("a managed Happ is told to show settings again: %v", h)
	}
}

func TestHappBannerWithSeveralLinesGoesAsBase64(t *testing.T) {
	cfg := model.SubSettings{Happ: model.HappSettings{AutoDetect: true, SubInfoText: "ignored"}}
	banner := model.HappBanner{Color: "red", Text: "Первая\r\nВторая  \n\nТретья", ButtonText: "Купить", ButtonLink: "https://pay.example.com"}
	h := ExtraHeadersFor(cfg, uaHapp, banner)
	if !strings.HasPrefix(h["Sub-Info-Text"], "base64:") || h["Sub-Info-Color"] != "red" || h["Sub-Info-Button-Text"] != "Купить" {
		t.Fatalf("%v", h)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h["Sub-Info-Text"], "base64:"))
	if err != nil || string(raw) != "Первая\nВторая\n\nТретья" {
		t.Fatalf("lines must survive as \n: %q %v", raw, err)
	}
	if h := ExtraHeadersFor(cfg, uaOther, banner); len(h) != 0 {
		t.Fatal("other apps get no banner")
	}
	if h := ExtraHeadersFor(cfg, uaHapp, model.HappBanner{}); h["Sub-Info-Text"] != "" {
		t.Fatal("no text, no banner")
	}
}

func TestBannerForFallsBackToTheCommonBanner(t *testing.T) {
	h := model.HappSettings{SubInfoText: "common", SubInfoColor: "green"}
	h.Banners.Expired = model.HappBanner{Text: "gone", Color: "red"}
	h.Banners.Trial = model.HappBanner{Text: "  ", Color: "red"}
	if b := h.BannerFor("expired"); b.Text != "gone" || b.Color != "red" {
		t.Fatalf("%+v", b)
	}
	for _, st := range []string{"trial", "active", "depleted", ""} {
		if b := h.BannerFor(st); b.Text != "common" || b.Color != "green" {
			t.Errorf("%q: %+v", st, b)
		}
	}
}
