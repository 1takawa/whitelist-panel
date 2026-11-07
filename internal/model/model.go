// Package model holds the persisted entities. The same structs are used for MongoDB (bson) and the REST API (json).
package model

import (
	"slices"
	"time"
)

const (
	ProtoVLESS  = "vless"
	ProtoTrojan = "trojan"

	NetTCP         = "tcp"
	NetWS          = "ws"
	NetGRPC        = "grpc"
	NetXHTTP       = "xhttp"
	NetHTTPUpgrade = "httpupgrade"
	NetKCP         = "kcp"

	SecNone    = "none"
	SecTLS     = "tls"
	SecReality = "reality"

	FlowVision = "xtls-rprx-vision"
)

// Inbound is one Xray listener. Settings and StreamSettings hold the protocol settings and the transport/security
// settings in the shape of an Xray inbound (what Xray-панелей stores), so every Xray option can be kept and edited. The typed
// fields next to them (Network, Security, Reality, …) are a validated view of those documents, derived whenever an
// inbound is saved (see rawinbound.go); the panel's own logic (links, front proxy) reads them.
// Security/Network describe what Xray itself serves; Public describes what the client link advertises, which differs
// when a front proxy (nginx/Caddy) terminates TLS or routes by SNI.
type Inbound struct {
	ID       string `json:"id" bson:"_id"`
	Remark   string `json:"remark" bson:"remark"`
	Enable   bool   `json:"enable" bson:"enable"`
	Protocol string `json:"protocol" bson:"protocol"`
	Listen   string `json:"listen" bson:"listen"`
	Port     int    `json:"port" bson:"port"`
	Network  string `json:"network" bson:"network"`
	Security string `json:"security" bson:"security"`

	// Settings is the protocol "settings" object without the clients (the panel manages those): decryption,
	// encryption, testseed and fallbacks for VLESS, fallbacks for Trojan. StreamSettings is the full Xray
	// "streamSettings" object (network, security, per-transport settings, sockopt, finalmask).
	Settings       Doc `json:"settings" bson:"settings"`
	StreamSettings Doc `json:"streamSettings" bson:"streamSettings"`

	WS          PathHost `json:"ws" bson:"ws"`
	HTTPUpgrade PathHost `json:"httpupgrade" bson:"httpupgrade"`
	XHTTP       XHTTP    `json:"xhttp" bson:"xhttp"`
	GRPC        GRPC     `json:"grpc" bson:"grpc"`
	TLS         TLS      `json:"tls" bson:"tls"`
	Reality     Reality  `json:"reality" bson:"reality"`
	Sniffing    Sniffing `json:"sniffing" bson:"sniffing"`
	Public      Public   `json:"public" bson:"public"`

	// ServerID is the extra server (see Server) that runs this inbound; empty means this machine.
	ServerID string `json:"serverId" bson:"serverId,omitempty"`

	// CascadeID is set on the inbound a cascade created on its entry server: its traffic leaves through the exit server.
	CascadeID string `json:"cascadeId" bson:"cascadeId,omitempty"`
	// NodeDomain marks a connection that is a node of the main server on a REALITY domain of its own: nginx routes the
	// domain to it (see Service.SetupMainNode). Empty for every other connection.
	NodeDomain string `json:"nodeDomain,omitempty" bson:"nodeDomain,omitempty"`

	// AcceptProxyProtocol must be true when nginx forwards to this inbound with proxy_protocol on.
	AcceptProxyProtocol bool `json:"acceptProxyProtocol" bson:"acceptProxyProtocol"`

	// Subscription behaviour (Xray-панелей "Basics").
	SortOrder       int    `json:"sortOrder" bson:"sortOrder"`             // position of the inbound's links in a subscription
	ExcludeFromSub  bool   `json:"excludeFromSub" bson:"excludeFromSub"`   // keep the inbound out of subscriptions
	DisableFlow     bool   `json:"disableFlow" bson:"disableFlow"`         // never send a client's XTLS flow for this inbound
	ShareAddress    string `json:"shareAddress" bson:"shareAddress"`       // "" = automatic, "listen" = the listen address
	ExcludeFromAuto bool   `json:"excludeFromAuto" bson:"excludeFromAuto"` // keep the inbound out of the automatic node
	// ClientRouting is the routing profile of the client app for this connection alone: "" = the one of the subscription,
	// "none" = no rules (everything goes through the server), or the id of a profile (default, whitelist, whitelist_ru).
	// It is written into the connection's own config, so it reaches the apps that read the Xray JSON format.
	ClientRouting string `json:"clientRouting" bson:"clientRouting"`

	// Limits of the whole inbound. TotalBytes 0 = unlimited; ExpiryAt nil = never. A used-up or expired inbound is
	// left out of the Xray config until the counters are reset (TrafficReset) or the limit is raised.
	TotalBytes   int64      `json:"totalBytes" bson:"totalBytes"`
	ExpiryAt     *time.Time `json:"expiryAt" bson:"expiryAt"`
	TrafficReset string     `json:"trafficReset" bson:"trafficReset"` // never | hourly | daily | weekly | monthly
	Up           int64      `json:"up" bson:"up"`
	Down         int64      `json:"down" bson:"down"`
	LastReset    *time.Time `json:"lastReset" bson:"lastReset"`

	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
}

const (
	ResetNever   = "never"
	ResetHourly  = "hourly"
	ResetDaily   = "daily"
	ResetWeekly  = "weekly"
	ResetMonthly = "monthly"
)

// Exhausted reports whether the inbound's traffic limit is used up.
func (in Inbound) Exhausted() bool { return in.TotalBytes > 0 && in.Up+in.Down >= in.TotalBytes }

// Expired reports whether the inbound's period has ended.
func (in Inbound) Expired(now time.Time) bool { return in.ExpiryAt != nil && !now.Before(*in.ExpiryAt) }

// Running reports whether Xray should serve the inbound right now.
func (in Inbound) Running(now time.Time) bool {
	return in.Enable && !in.Exhausted() && !in.Expired(now)
}

type PathHost struct {
	Path string `json:"path" bson:"path"`
	Host string `json:"host" bson:"host"`
}

type XHTTP struct {
	Path string `json:"path" bson:"path"`
	Host string `json:"host" bson:"host"`
	Mode string `json:"mode" bson:"mode"` // auto | packet-up | stream-up | stream-one
}

type GRPC struct {
	ServiceName string `json:"serviceName" bson:"serviceName"`
	Authority   string `json:"authority" bson:"authority"`
	MultiMode   bool   `json:"multiMode" bson:"multiMode"`
}

type TLS struct {
	ServerName string   `json:"serverName" bson:"serverName"`
	CertFile   string   `json:"certFile" bson:"certFile"`
	KeyFile    string   `json:"keyFile" bson:"keyFile"`
	ALPN       []string `json:"alpn" bson:"alpn"`
}

type Reality struct {
	Dest        string   `json:"dest" bson:"dest"`
	ServerNames []string `json:"serverNames" bson:"serverNames"`
	PrivateKey  string   `json:"privateKey" bson:"privateKey"`
	PublicKey   string   `json:"publicKey" bson:"publicKey"`
	ShortIDs    []string `json:"shortIds" bson:"shortIds"`
	SpiderX     string   `json:"spiderX" bson:"spiderX"`
}

type Sniffing struct {
	Enabled         bool     `json:"enabled" bson:"enabled"`
	DestOverride    []string `json:"destOverride" bson:"destOverride"`
	MetadataOnly    bool     `json:"metadataOnly" bson:"metadataOnly"`
	RouteOnly       bool     `json:"routeOnly" bson:"routeOnly"`
	DomainsExcluded []string `json:"domainsExcluded" bson:"domainsExcluded"`
	IPsExcluded     []string `json:"ipsExcluded" bson:"ipsExcluded"`
}

// Public controls the client-facing side of the link.
type Public struct {
	Host        string `json:"host" bson:"host"`               // empty: derived from Settings
	Port        int    `json:"port" bson:"port"`               // 0: same as Inbound.Port
	Security    string `json:"security" bson:"security"`       // "" or "same" | "tls" | "none"
	SNI         string `json:"sni" bson:"sni"`                 // empty: derived
	Fingerprint string `json:"fingerprint" bson:"fingerprint"` // uTLS fingerprint, default firefox, as in nginx-SNI
	ALPN        string `json:"alpn" bson:"alpn"`               // comma-separated
}

// Client is one subscriber. It can be attached to several inbounds; its Email is the traffic-accounting key.
type Client struct {
	ID         string     `json:"id" bson:"_id"`
	Email      string     `json:"email" bson:"email"`
	SubID      string     `json:"subId" bson:"subId"`
	UUID       string     `json:"uuid" bson:"uuid"`         // VLESS
	Password   string     `json:"password" bson:"password"` // Trojan
	Flow       string     `json:"flow" bson:"flow"`
	Enable     bool       `json:"enable" bson:"enable"`
	TotalBytes int64      `json:"totalBytes" bson:"totalBytes"` // 0 = unlimited
	ExpiryAt   *time.Time `json:"expiryAt" bson:"expiryAt"`
	InboundIDs []string   `json:"inboundIds" bson:"inboundIds"`
	Up         int64      `json:"up" bson:"up"`
	Down       int64      `json:"down" bson:"down"`
	Comment    string     `json:"comment" bson:"comment"`
	Trial      bool       `json:"trial" bson:"trial"` // a test subscription: only changes which app banner it sees
	CreatedAt  time.Time  `json:"createdAt" bson:"createdAt"`
	// Num is the client's own number in the order they were made: 1, 2, 3… It is given once and never reused, so a number
	// kept in a conversation with a subscriber stays theirs after others are deleted.
	Num int `json:"num" bson:"num"`

	// LastActiveAt is the last time the client moved traffic (set by the traffic poll); "online" means recent.
	LastActiveAt *time.Time `json:"lastActiveAt" bson:"lastActiveAt"`
	// TelegramID links the client to a Telegram account (used by the bot and by migrated users).
	TelegramID int64 `json:"telegramId" bson:"telegramId"`

	// Devices are the devices (HWID) that asked for this subscription. The apps send the HWID in a header when the
	// provider asks for it (Happ: "always pass the HWID"). Managed through the device endpoints, never by the client form.
	Devices []Device `json:"devices" bson:"devices,omitempty"`

	// LastNode is the server the client last moved traffic through. It is worked out when the list is read, never stored.
	LastNode *ClientNode `json:"lastNode,omitempty" bson:"-"`
}

// ClientNode names the server a client was last seen on.
type ClientNode struct {
	ID   string    `json:"id"` // "local" for the main server
	Name string    `json:"name"`
	Flag string    `json:"flag"`
	At   time.Time `json:"at"`
}

// Device is one device of a subscriber, as it introduced itself in the subscription request.
type Device struct {
	HWID      string    `json:"hwid" bson:"hwid"`
	OS        string    `json:"os" bson:"os"` // Android, iOS, Windows…
	OSVersion string    `json:"osVersion" bson:"osVersion"`
	Model     string    `json:"model" bson:"model"`
	App       string    `json:"app" bson:"app"` // the app's User-Agent
	IP        string    `json:"ip" bson:"ip"`   // where it last came from
	FirstSeen time.Time `json:"firstSeen" bson:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen" bson:"lastSeen"`
	// Blocked devices get no servers when they refresh the subscription. A config the device already has keeps working
	// until the client's own keys are changed: Xray cannot tell two devices of one client apart.
	Blocked bool `json:"blocked" bson:"blocked"`
}

// Exhausted reports whether the traffic quota is used up.
func (c Client) Exhausted() bool { return c.TotalBytes > 0 && c.Up+c.Down >= c.TotalBytes }

// Expired reports whether the subscription period has ended.
func (c Client) Expired(now time.Time) bool { return c.ExpiryAt != nil && !now.Before(*c.ExpiryAt) }

// Active reports whether the client may connect right now.
func (c Client) Active(now time.Time) bool { return c.Enable && !c.Exhausted() && !c.Expired(now) }

// BannerState is the state that picks the app banner: expired, depleted, trial or active ("" for a disabled client).
func (c Client) BannerState(now time.Time) string {
	switch {
	case !c.Enable:
		return ""
	case c.Expired(now):
		return "expired"
	case c.Exhausted():
		return "depleted"
	case c.Trial:
		return "trial"
	}
	return "active"
}

// Uses reports whether the client is attached to the inbound.
func (c Client) Uses(inboundID string) bool { return slices.Contains(c.InboundIDs, inboundID) }

// Settings are global panel settings (one document).
type Settings struct {
	PanelDomain   string `json:"panelDomain" bson:"panelDomain"`
	RealityDomain string `json:"realityDomain" bson:"realityDomain"`

	Subscription SubSettings      `json:"subscription" bson:"subscription"`
	Telegram     TelegramSettings `json:"telegram" bson:"telegram"`
	// RegRu is managed through /api/regru (its secrets must not pass through the settings form).
	RegRu RegRuSettings `json:"regru" bson:"regru"`

	// Xray is the main server's Xray profile (managed through /api/xray-profile, not through the settings form).
	XrayProfile XrayProfile `json:"xrayProfile" bson:"xrayProfile"`
}

// SubSettings configure the public subscription endpoint (<Path><subId>).
type SubSettings struct {
	// Version of the settings schema; documents written by older releases are upgraded in WithDefaults.
	Version int `json:"version" bson:"version"`

	// General.
	Enable bool   `json:"enable" bson:"enable"`
	Path   string `json:"path" bson:"path"`     // URL prefix of the subscription, default /sub/
	Domain string `json:"domain" bson:"domain"` // host used in subscription URLs instead of the panel domain
	// MoveToDomain makes the panel tell Happ (the new-domain header) to switch its saved subscription address to Domain the
	// next time it updates. Turn it on only when the domain already answers: the app would move to an address that is dead.
	MoveToDomain bool   `json:"moveToDomain" bson:"moveToDomain"`
	URI          string `json:"uri" bson:"uri"` // complete base URL override (for example behind a CDN or path prefix)

	// Information.
	Encode           bool   `json:"encode" bson:"encode"`                     // base64-encode the response body
	RemarkTemplate   string `json:"remarkTemplate" bson:"remarkTemplate"`     // name of every link
	InfoNode         bool   `json:"infoNode" bson:"infoNode"`                 // add a pseudo-node showing traffic and expiry
	CalendarExpire   bool   `json:"calendarExpire" bson:"calendarExpire"`     // the expiry date itself is still valid
	TimeZone         string `json:"timeZone" bson:"timeZone"`                 // IANA zone for dates; empty = server local time
	ExpiredTemplate  string `json:"expiredTemplate" bson:"expiredTemplate"`   // info node name once the period ended
	DepletedTemplate string `json:"depletedTemplate" bson:"depletedTemplate"` // info node name once the quota is used up
	UpdateHours      int    `json:"updateHours" bson:"updateHours"`

	// Profile (all text fields accept the {{EMAIL}}, {{ID}}, {{SHORT_ID}}, {{SUB_ID}} placeholders).
	Title       string `json:"title" bson:"title"`             // Profile-Title shown by client apps
	SupportURL  string `json:"supportUrl" bson:"supportUrl"`   // Support-Url
	ProfileMode string `json:"profileMode" bson:"profileMode"` // none | builtin (the subscription URL) | custom (ProfileURL)
	ProfileURL  string `json:"profileUrl" bson:"profileUrl"`   // Profile-Web-Page-Url when ProfileMode is custom
	Announce    string `json:"announce" bson:"announce"`       // Announce banner text

	// Client apps.
	HideSettings bool         `json:"hideSettings" bson:"hideSettings"` // Hide-Settings: apps hide their settings screen
	Happ         HappSettings `json:"happ" bson:"happ"`
	Incy         IncySettings `json:"incy" bson:"incy"`

	Formats FormatSettings `json:"formats" bson:"formats"`

	AutoNode AutoNodeSettings `json:"autoNode" bson:"autoNode"`

	// Page holds the saved page themes and says which one the subscribers see; none selected, the panel's own page.
	Page PageSettings `json:"page" bson:"page"`

	// Preview is how the subscription link looks when it is pasted into a messenger.
	Preview LinkPreview `json:"preview" bson:"preview"`
}

// PageSettings hold the pages of the administrator's own design, as saved themes. The theme that is selected is shown to
// a browser instead of the standard page; the apps get the subscription itself as before.
type PageSettings struct {
	Selected string      `json:"selected" bson:"selected"` // id of a theme, "preset:" + id of a built-in page; "" = the standard page
	Themes   []PageTheme `json:"themes" bson:"themes"`
	// Presets are the values the administrator typed into the fields of the built-in pages: page id -> field -> value.
	Presets map[string]map[string]string `json:"presets" bson:"presets"`

	// A page saved by releases 0.39 and 0.40, when there was only one: moved into a theme when the settings are loaded.
	OldEnable bool   `json:"-" bson:"enable,omitempty"`
	OldHTML   string `json:"-" bson:"html,omitempty"`
	OldCSS    string `json:"-" bson:"css,omitempty"`
	OldJS     string `json:"-" bson:"js,omitempty"`
}

// PageTheme is one saved page: an HTML document (a Go html/template), its styles and its script.
type PageTheme struct {
	ID   string `json:"id" bson:"id"`
	Name string `json:"name" bson:"name"`
	HTML string `json:"html" bson:"html"`
	CSS  string `json:"css" bson:"css"` // put into the page as <style>
	JS   string `json:"js" bson:"js"`   // put into the page as <script>; the subscription is in window.sub
}

// Theme is the saved theme with the id, nil when there is none.
func (p PageSettings) Theme(id string) *PageTheme {
	for i := range p.Themes {
		if p.Themes[i].ID == id {
			return &p.Themes[i]
		}
	}
	return nil
}

// migrate turns the single page of releases 0.39 and 0.40 into a theme (selected if the page was on).
func (p *PageSettings) migrate() {
	if p.OldHTML != "" && len(p.Themes) == 0 {
		p.Themes = []PageTheme{{ID: "mypage", Name: "Моя страница", HTML: p.OldHTML, CSS: p.OldCSS, JS: p.OldJS}}
		if p.OldEnable {
			p.Selected = "mypage"
		}
	}
	p.OldEnable, p.OldHTML, p.OldCSS, p.OldJS = false, "", "", ""
	if p.Themes == nil {
		p.Themes = []PageTheme{}
	}
	if p.Presets == nil {
		p.Presets = map[string]map[string]string{}
	}
}

// Link preview modes.
const (
	PreviewDefault = "default" // the panel's own title and description
	PreviewCustom  = "custom"  // the fields below; an empty field is left out of the preview
)

// LinkPreview is the card messengers (Telegram, WhatsApp, Slack…) draw under a pasted subscription link: they read the
// title, description and picture from the page the link opens. The text fields accept the same placeholders as the
// profile ({{EMAIL}}, {{ID}}, {{SHORT_ID}}, {{SUB_ID}}).
type LinkPreview struct {
	Mode        string `json:"mode" bson:"mode"`
	SiteName    string `json:"siteName" bson:"siteName"` // the small line above the title (by default the domain)
	Title       string `json:"title" bson:"title"`
	Description string `json:"description" bson:"description"`
	ImageURL    string `json:"imageUrl" bson:"imageUrl"` // a picture shown on the card (https address)

	// Image is a picture uploaded to the panel; it is used instead of ImageURL when there is one. It is managed through
	// its own endpoints, never by the settings form.
	Image PreviewImage `json:"image" bson:"image"`
}

// PreviewImage is the uploaded picture of the link card. The bytes stay out of the settings JSON.
type PreviewImage struct {
	Type   string `json:"type" bson:"type"` // image/png, image/jpeg, image/gif or image/webp; empty = no picture
	Data   []byte `json:"-" bson:"data"`
	Width  int    `json:"width" bson:"width"`
	Height int    `json:"height" bson:"height"`
	Sum    string `json:"sum" bson:"sum"` // short hash of the bytes: changes with the picture so messengers refetch it
}

// legacySubTitle is the old default profile title; settings that still hold it get the new default.
const legacySubTitle = "ANY Panel"

const (
	SubSettingsVersion = 4

	DefaultSubTitle         = "whitelist"
	DefaultSubUpdateHours   = 12
	DefaultSubPath          = "/sub/"
	DefaultRemarkTemplate   = "{{INBOUND}}-{{EMAIL}}|📊{{TRAFFIC_LEFT}}|⏳{{DAYS_LEFT}}D"
	DefaultExpiredTemplate  = "⛔ {{EMAIL}} | Срок истёк: {{EXPIRE_DATE}}"
	DefaultDepletedTemplate = "🚫 {{EMAIL}} | Трафик исчерпан | {{TRAFFIC_USED}}/{{TRAFFIC_TOTAL}}"
	ProfileModeNone         = "none"
	ProfileModeBuiltin      = "builtin"
	ProfileModeCustom       = "custom"
	// The info node is a pseudo-link: apps list it as a server whose name carries the information.
	InfoNodeLinkPrefix = "socks://127.0.0.1:1080#"
)

// WithDefaults fills settings that were never saved or were written by an older release. A valid UpdateHours is
// always >= 1, so zero means "never saved"; Version marks documents that predate a group of settings.
func (s Settings) WithDefaults() Settings {
	s.applyStage5Defaults()
	sub := &s.Subscription
	if sub.UpdateHours == 0 {
		sub.Enable = true
		sub.UpdateHours = DefaultSubUpdateHours
		if sub.Title == "" {
			sub.Title = DefaultSubTitle
		}
	}
	if sub.Title == legacySubTitle { // the title the product had before it was renamed
		sub.Title = DefaultSubTitle
	}
	if sub.Version < 3 { // release 0.3: information and profile settings
		sub.Encode = true
		if sub.ProfileMode == "" {
			sub.ProfileMode = ProfileModeNone
			if sub.ProfileURL != "" {
				sub.ProfileMode = ProfileModeCustom // a link saved by an earlier release keeps working
			}
		}
	}
	if sub.Version < 4 { // release 0.4: client apps (Happ, INCY) and the Clash/JSON formats
		sub.Happ.SubInfoColor = "blue"
		sub.Happ.AutoConnectType = "lowestdelay"
		sub.Happ.PerAppMode = "off"
		sub.Happ.LocalProxyAuth = "auto"
	}
	sub.AutoNode.applyDefaults()
	sub.Page.migrate()
	sub.Version = SubSettingsVersion
	if sub.Preview.Mode == "" {
		sub.Preview.Mode = PreviewDefault
	}
	if sub.Formats.ClashPath == "" {
		sub.Formats.ClashPath = DefaultClashPath
	}
	if sub.Formats.JSONPath == "" {
		sub.Formats.JSONPath = DefaultJSONPath
	}
	if sub.Formats.ClashUARegex == "" {
		sub.Formats.ClashUARegex = DefaultClashUARegex
	}
	if sub.Path == "" {
		sub.Path = DefaultSubPath
	}
	if sub.RemarkTemplate == "" {
		sub.RemarkTemplate = DefaultRemarkTemplate
	}
	if sub.ExpiredTemplate == "" {
		sub.ExpiredTemplate = DefaultExpiredTemplate
	}
	if sub.DepletedTemplate == "" {
		sub.DepletedTemplate = DefaultDepletedTemplate
	}
	if sub.ProfileMode == "" {
		sub.ProfileMode = ProfileModeNone
	}
	return s
}

// Location is the time zone for dates shown to subscribers (server local time when unset or unknown).
func (s SubSettings) Location() *time.Location {
	if s.TimeZone != "" {
		if loc, err := time.LoadLocation(s.TimeZone); err == nil {
			return loc
		}
	}
	return time.Local
}

// EffectiveClient applies the calendar-expiry option: when the last day counts as valid, the subscription ends at
// the start of the next day in the configured zone instead of at the exact timestamp.
func (s SubSettings) EffectiveClient(c Client) Client {
	if s.CalendarExpire && c.ExpiryAt != nil {
		loc := s.Location()
		t := c.ExpiryAt.In(loc)
		end := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		c.ExpiryAt = &end
	}
	return c
}

// Status is the human-readable state of a client for subscription pages.
func (c Client) Status(now time.Time) string {
	switch {
	case !c.Enable:
		return "disabled"
	case c.Exhausted():
		return "exhausted"
	case c.Expired(now):
		return "expired"
	}
	return "active"
}
