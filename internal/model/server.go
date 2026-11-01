package model

import (
	"strings"
	"time"
)

// Server is an additional machine that runs only Xray, driven by this panel through the agent
// (`anypanel agent`). Inbounds with a ServerID are served from there; clients, links and statistics stay here.
type Server struct {
	ID      string `json:"id" bson:"_id"`
	Name    string `json:"name" bson:"name"`       // shown in the panel and, through {{SERVER}}, in subscription link names
	Address string `json:"address" bson:"address"` // host that clients connect to and the panel reaches the agent at
	Port    int    `json:"port" bson:"port"`       // the agent's HTTPS port
	Enable  bool   `json:"enable" bson:"enable"`
	Country string `json:"country" bson:"country"` // ISO 3166-1 alpha-2 code ("DE"); empty when unknown
	// Token authenticates the panel to the agent. It is stored in clear (the panel needs it) and never returned
	// by the API; Fingerprint is the SHA-256 of the agent's certificate (hex), pinned on first contact if empty.
	Token       string    `json:"-" bson:"token"`
	Fingerprint string    `json:"fingerprint" bson:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt" bson:"createdAt"`

	// SSH access, present only for nodes the panel installed itself. SSHKey is the private key the panel generated
	// for this node (the password used during installation is never stored); it never leaves the panel.
	SSHPort    int    `json:"sshPort" bson:"sshPort,omitempty"`
	SSHUser    string `json:"sshUser" bson:"sshUser,omitempty"`
	SSHHostKey string `json:"sshHostKey" bson:"sshHostKey,omitempty"` // SHA-256 of the SSH host key, as seen at installation
	SSHKey     string `json:"-" bson:"sshKey,omitempty"`

	// Xray holds the node's own Xray settings (logging, routing, template), editable at any time.
	XrayProfile XrayProfile `json:"xrayProfile" bson:"xrayProfile"`
}

// Flag is the country's flag emoji, or "" when the country is unknown.
func (s Server) Flag() string { return FlagEmoji(s.Country) }

// FlagEmoji turns a two-letter country code into the flag emoji (two regional indicator symbols).
func FlagEmoji(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return ""
	}
	return string([]rune{0x1F1E6 + rune(code[0]-'A'), 0x1F1E6 + rune(code[1]-'A')})
}

// ServerTraffic is what one client moved through one extra server (counted since the server was added).
type ServerTraffic struct {
	ServerID     string     `json:"serverId" bson:"serverId"`
	Email        string     `json:"email" bson:"email"`
	Up           int64      `json:"up" bson:"up"`
	Down         int64      `json:"down" bson:"down"`
	LastActiveAt *time.Time `json:"lastActiveAt" bson:"lastActiveAt"`
}

// XrayProfile is everything about one machine's Xray that is not an inbound: logging, basic routing rules and a
// free-form template merged into the generated config. The main server's profile is Settings.Xray, an extra
// server's is Server.Xray, so each machine can be tuned on its own and at any time.
type XrayProfile struct {
	LogLevel       string `json:"logLevel" bson:"logLevel"`             // "" = warning; debug | info | warning | error | none
	BitTorrent     string `json:"bittorrent" bson:"bittorrent"`         // "" = the default (block), "block" or "allow"
	BlockPrivate   bool   `json:"blockPrivate" bson:"blockPrivate"`     // refuse connections to private and loopback addresses
	DomainStrategy string `json:"domainStrategy" bson:"domainStrategy"` // routing domainStrategy: AsIs | IPIfNonMatch | IPOnDemand
	// Template is merged over the generated config: objects merge field by field, "outbounds" are added (an outbound
	// with the same tag replaces the generated one), "routing.rules" are placed before the generated rules and any
	// other array replaces the generated one. Inbounds are managed by the panel and cannot be set here.
	Template Doc `json:"template" bson:"template"`
}
