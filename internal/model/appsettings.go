package model

import "strings"

// FormatSettings enable the alternative subscription formats: Clash/Mihomo YAML and Xray client JSON. Each is served
// at its own path prefix and, optionally, on the main path to apps whose User-Agent matches a pattern.
type FormatSettings struct {
	ClashEnable        bool   `json:"clashEnable" bson:"clashEnable"`
	ClashPath          string `json:"clashPath" bson:"clashPath"`
	ClashAutoDetect    bool   `json:"clashAutoDetect" bson:"clashAutoDetect"`
	ClashUARegex       string `json:"clashUaRegex" bson:"clashUaRegex"`
	ClashEnableRouting bool   `json:"clashEnableRouting" bson:"clashEnableRouting"`
	ClashRules         string `json:"clashRules" bson:"clashRules"` // one Clash rule per line, e.g. DOMAIN-SUFFIX,ru,DIRECT
	ClashRemoteDNS     bool   `json:"clashRemoteDns" bson:"clashRemoteDns"`

	JSONEnable      bool   `json:"jsonEnable" bson:"jsonEnable"`
	JSONPath        string `json:"jsonPath" bson:"jsonPath"`
	JSONAutoDetect  bool   `json:"jsonAutoDetect" bson:"jsonAutoDetect"`
	JSONUARegex     string `json:"jsonUaRegex" bson:"jsonUaRegex"`
	JSONAlwaysArray bool   `json:"jsonAlwaysArray" bson:"jsonAlwaysArray"` // an array even for a single config
	JSONMux         string `json:"jsonMux" bson:"jsonMux"`                 // JSON object for the outbound "mux"
	JSONFragment    string `json:"jsonFragment" bson:"jsonFragment"`       // JSON object: packets, length, interval
	JSONNoises      string `json:"jsonNoises" bson:"jsonNoises"`           // JSON array of UDP noises
	JSONRules       string `json:"jsonRules" bson:"jsonRules"`             // JSON array of extra routing rules
	JSONDNS         string `json:"jsonDns" bson:"jsonDns"`                 // JSON object replacing the default dns section
}

const (
	DefaultClashPath    = "/clash/"
	DefaultJSONPath     = "/json/"
	DefaultClashUARegex = `(?i)(clash|mihomo|stash|flclash|verge|koala)`
)

// HappSettings customise the Happ client (https://happ.su) through subscription response headers. The headers are
// sent only to Happ itself (User-Agent contains "Happ") and only when AutoDetect is on, so other apps never see them.
type HappSettings struct {
	AutoDetect bool `json:"autoDetect" bson:"autoDetect"`

	ProviderID  string `json:"providerId" bson:"providerId"`
	NewURL      string `json:"newUrl" bson:"newUrl"`           // New-Url: the app moves to this subscription address
	FallbackURL string `json:"fallbackUrl" bson:"fallbackUrl"` // Fallback-Url

	// Information banner on the profile.
	SubInfoColor      string `json:"subInfoColor" bson:"subInfoColor"` // blue | green | red
	SubInfoText       string `json:"subInfoText" bson:"subInfoText"`
	SubInfoButtonText string `json:"subInfoButtonText" bson:"subInfoButtonText"`
	SubInfoButtonLink string `json:"subInfoButtonLink" bson:"subInfoButtonLink"`
	// Banners override the banner above for subscribers in one state; a state whose text is empty keeps the common one.
	Banners HappBanners `json:"banners" bson:"banners"`

	SubExpire           bool   `json:"subExpire" bson:"subExpire"` // show an expiry notice
	SubExpireButtonLink string `json:"subExpireButtonLink" bson:"subExpireButtonLink"`
	NotificationExpire  bool   `json:"notificationExpire" bson:"notificationExpire"`
	NoLimit             bool   `json:"noLimit" bson:"noLimit"`
	AlwaysHwid          bool   `json:"alwaysHwid" bson:"alwaysHwid"`

	TunMode       string `json:"tunMode" bson:"tunMode"`
	TunType       string `json:"tunType" bson:"tunType"`
	ExcludeRoutes string `json:"excludeRoutes" bson:"excludeRoutes"`
	ExcludeApns   bool   `json:"excludeApns" bson:"excludeApns"`
	ColorProfile  string `json:"colorProfile" bson:"colorProfile"`
	PingType      string `json:"pingType" bson:"pingType"`

	AutoConnect     bool   `json:"autoConnect" bson:"autoConnect"`
	AutoConnectType string `json:"autoConnectType" bson:"autoConnectType"` // lowestdelay | lastused | ...

	PerAppMode string `json:"perAppMode" bson:"perAppMode"` // off | on | bypass
	PerAppList string `json:"perAppList" bson:"perAppList"`

	LocalProxyAuth string `json:"localProxyAuth" bson:"localProxyAuth"` // Socks-Auth-Mode / Http-Auth-Mode

	// Routing is a Happ routing profile: a happ://routing/... link, happ://routing/off, or a JSON profile that is
	// converted to a link when served.
	EnableRouting bool   `json:"enableRouting" bson:"enableRouting"`
	RoutingRules  string `json:"routingRules" bson:"routingRules"`
}

// HappBanner is one information banner. The text may have several lines and use the placeholders of the link names.
type HappBanner struct {
	Color      string `json:"color" bson:"color"` // blue | green | red
	Text       string `json:"text" bson:"text"`
	ButtonText string `json:"buttonText" bson:"buttonText"`
	ButtonLink string `json:"buttonLink" bson:"buttonLink"`
}

// HappBanners are the per-state banners: a subscriber sees the one for their state (see Client.BannerState).
type HappBanners struct {
	Active   HappBanner `json:"active" bson:"active"`
	Trial    HappBanner `json:"trial" bson:"trial"`
	Expired  HappBanner `json:"expired" bson:"expired"`
	Depleted HappBanner `json:"depleted" bson:"depleted"`
}

// BannerFor returns the banner for a subscriber state: the state's own when it has a text, else the common one.
func (h HappSettings) BannerFor(state string) HappBanner {
	var b HappBanner
	switch state {
	case "active":
		b = h.Banners.Active
	case "trial":
		b = h.Banners.Trial
	case "expired":
		b = h.Banners.Expired
	case "depleted":
		b = h.Banners.Depleted
	}
	if strings.TrimSpace(b.Text) != "" {
		return b
	}
	return HappBanner{Color: h.SubInfoColor, Text: h.SubInfoText, ButtonText: h.SubInfoButtonText, ButtonLink: h.SubInfoButtonLink}
}

// IncySettings customise the INCY client (https://docs.incy.cc/en/app-management/). Empty values omit the header,
// so an untouched panel never overrides the subscriber's own app settings. On/off values are "1" and "0".
type IncySettings struct {
	AutoDetect bool `json:"autoDetect" bson:"autoDetect"`

	ProfileDescription string `json:"profileDescription" bson:"profileDescription"`
	SortOrder          string `json:"sortOrder" bson:"sortOrder"` // none | ping | name
	SupportEmail       string `json:"supportEmail" bson:"supportEmail"`
	AnnounceURL        string `json:"announceUrl" bson:"announceUrl"`
	PremiumURL         string `json:"premiumUrl" bson:"premiumUrl"`

	BannerText        string `json:"bannerText" bson:"bannerText"`
	BannerButtonText  string `json:"bannerButtonText" bson:"bannerButtonText"`
	BannerButtonURL   string `json:"bannerButtonUrl" bson:"bannerButtonUrl"`
	BannerBgColor     string `json:"bannerBgColor" bson:"bannerBgColor"`
	BannerButtonColor string `json:"bannerButtonColor" bson:"bannerButtonColor"`

	HideURL        string `json:"hideUrl" bson:"hideUrl"`
	HideCheck      string `json:"hideCheck" bson:"hideCheck"`
	NoLimitEnabled string `json:"noLimitEnabled" bson:"noLimitEnabled"`

	PerAppEnable string `json:"perAppEnable" bson:"perAppEnable"`
	PerAppMode   string `json:"perAppMode" bson:"perAppMode"` // bypass | proxy
	PerAppList   string `json:"perAppList" bson:"perAppList"`

	FragmentationEnable   string `json:"fragmentationEnable" bson:"fragmentationEnable"`
	FragmentationLength   string `json:"fragmentationLength" bson:"fragmentationLength"`
	FragmentationInterval string `json:"fragmentationInterval" bson:"fragmentationInterval"`
	FragmentationPackets  string `json:"fragmentationPackets" bson:"fragmentationPackets"`

	NoisesEnable string `json:"noisesEnable" bson:"noisesEnable"`
	NoisesType   string `json:"noisesType" bson:"noisesType"` // rand | str | hex
	NoisesPacket string `json:"noisesPacket" bson:"noisesPacket"`
	NoisesDelay  string `json:"noisesDelay" bson:"noisesDelay"`

	ResolveEnable    string `json:"resolveEnable" bson:"resolveEnable"`
	ResolveDNSDomain string `json:"resolveDnsDomain" bson:"resolveDnsDomain"`
	ResolveDNSIP     string `json:"resolveDnsIp" bson:"resolveDnsIp"`

	// Routing is a routing link of any scheme; INCY reads it from a line of the subscription body.
	EnableRouting bool   `json:"enableRouting" bson:"enableRouting"`
	RoutingRules  string `json:"routingRules" bson:"routingRules"`
}

// AutoNodeSettings configure the "automatic node": one entry in the subscription that holds every server of the
// subscriber and lets the client app pick the one with the lowest delay, moving to the next when it stops answering.
// The app does the choosing (only it knows what is fastest from where the subscriber is): the panel hands it a group.
// Xray client JSON (Happ, INCY, v2rayN…) gets a balancer with a ping observatory, Clash/Mihomo an url-test group.
// A plain list of links cannot express a group, so apps reading it do not see the entry.
type AutoNodeSettings struct {
	Enable bool   `json:"enable" bson:"enable"`
	Name   string `json:"name" bson:"name"`
	Last   bool   `json:"last" bson:"last"` // after the servers instead of before them
	// Position is the place of the entry in the list: before the first server whose connection has a SortOrder of at least
	// this (Last overrides it). Dragging the rows on the Connections page sets it together with the connections' SortOrder.
	Position int `json:"position" bson:"position"`
	// KeepLinks stops the automatic switch of Happ and INCY to the JSON format on the main subscription address. The switch
	// is what makes the entry appear for them (a plain list of links cannot carry a group), so it is on by default.
	KeepLinks bool `json:"keepLinks" bson:"keepLinks"`

	// Health check. Xray probes every server at once each IntervalSec; Clash also gives up on a probe after TimeoutSec.
	ProbeURL    string `json:"probeUrl" bson:"probeUrl"`
	IntervalSec int    `json:"intervalSec" bson:"intervalSec"` // how often every server is probed
	TimeoutSec  int    `json:"timeoutSec" bson:"timeoutSec"`   // a probe that takes longer counts as a failure
	ToleranceMs int    `json:"toleranceMs" bson:"toleranceMs"` // the chosen server is replaced only by one this much faster (Clash)
}

const (
	DefaultAutoNodeName     = "🌐 Авто"
	DefaultAutoNodeProbeURL = "https://www.gstatic.com/generate_204"
)

// applyDefaults fills a block that was never saved (IntervalSec is always >= 1 once saved).
func (a *AutoNodeSettings) applyDefaults() {
	if a.IntervalSec != 0 {
		return
	}
	a.Name, a.ProbeURL = DefaultAutoNodeName, DefaultAutoNodeProbeURL
	a.IntervalSec, a.TimeoutSec, a.ToleranceMs = 10, 3, 50
}
