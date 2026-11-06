package model

import (
	"strings"
	"time"
)

// ExternalNode is a server run by someone else (a third-party VLESS/Trojan/… link) that is added to every
// client's subscription next to the panel's own inbounds.
type ExternalNode struct {
	ID        string    `json:"id" bson:"_id"`
	Name      string    `json:"name" bson:"name"`
	URI       string    `json:"uri" bson:"uri"`
	Enable    bool      `json:"enable" bson:"enable"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
}

// Sample is one hour of total traffic, used for the dashboard chart. Hour is the Unix hour (seconds / 3600).
type Sample struct {
	Hour int64 `json:"hour" bson:"_id"`
	Up   int64 `json:"up" bson:"up"`
	Down int64 `json:"down" bson:"down"`
	// Nodes is the same hour split by inbound (inbound id → bytes), for the chart's "busiest nodes" hint.
	Nodes map[string]int64 `json:"nodes,omitempty" bson:"nodes,omitempty"`
	// At lets MongoDB expire old samples with a TTL index.
	At time.Time `json:"-" bson:"at"`
}

// Secrets are values that must never be returned by the settings API.
type Secrets struct {
	APITokenHash      string     `bson:"apiTokenHash"` // sha256 of the token; the token itself is shown once
	APITokenPrefix    string     `bson:"apiTokenPrefix"`
	APITokenCreatedAt *time.Time `bson:"apiTokenCreatedAt"`
	TelegramToken     string     `bson:"telegramToken"`
	// REG.RU API access (see RegRuSettings): the password, and the client certificate with its key as PEM text.
	RegRuPassword string `bson:"regruPassword"`
	RegRuCert     string `bson:"regruCert"`
	RegRuKey      string `bson:"regruKey"`
	// BackupSentAt is when the daily database copy last reached the administrators in Telegram.
	BackupSentAt *time.Time `bson:"backupSentAt"`
	// UpdateCheckedAt is when the panel last asked GitHub for a newer release (every 12 hours), UpdateNotified the newest
	// version the administrators were already told about in Telegram.
	UpdateCheckedAt *time.Time `bson:"updateCheckedAt"`
	UpdateNotified  string     `bson:"updateNotified"`
}

// TelegramSettings configure the administrators' bot. The bot token itself lives in Secrets. A running bot always
// reports expired subscriptions, used-up traffic, Xray and server outages, and sends the daily database copy.
type TelegramSettings struct {
	Enable   bool    `json:"enable" bson:"enable"`
	AdminIDs []int64 `json:"adminIds" bson:"adminIds"`
}

// RegRuSettings connect the panel to the REG.RU account that holds the domain, so it can create the A record of a
// new node's domain. The password, certificate and key live in Secrets. The record is empty until it is connected.
type RegRuSettings struct {
	Username string `json:"username" bson:"username"`
	Zone     string `json:"zone" bson:"zone"` // the domain whose DNS zone the panel manages, for example example.com
	// ExtraZones are further domains of the same REG.RU account that new names can be created in; Zone is the default.
	ExtraZones []string `json:"extraZones" bson:"extraZones"`
	// Created lists the names the panel made itself (and only those): their A records are removed again when the node or
	// cascade they were made for is deleted. A name picked from the zone's existing ones is never in this list.
	Created []RegRuCreated `json:"created" bson:"created"`
}

// RegRuCreated is a name the panel created in the zone. ServerID or CascadeID says which node or cascade it was made for;
// both are empty until the installer or the cascade form uses the name.
type RegRuCreated struct {
	Domain    string    `json:"domain" bson:"domain"`
	IP        string    `json:"ip" bson:"ip"`
	At        time.Time `json:"at" bson:"at"`
	ServerID  string    `json:"serverId,omitempty" bson:"serverId,omitempty"`
	CascadeID string    `json:"cascadeId,omitempty" bson:"cascadeId,omitempty"`
}

// applyStage5Defaults makes sure the administrator list is never null in JSON.
func (s *Settings) applyStage5Defaults() {
	if s.RegRu.ExtraZones == nil {
		s.RegRu.ExtraZones = []string{}
	}
	if s.RegRu.Created == nil {
		s.RegRu.Created = []RegRuCreated{}
	}
	if s.Telegram.AdminIDs == nil {
		s.Telegram.AdminIDs = []int64{}
	}
}

// Online reports whether the client moved traffic within the window.
func (c Client) Online(now time.Time, window time.Duration) bool {
	return c.LastActiveAt != nil && now.Sub(*c.LastActiveAt) <= window
}

// SanitizeName turns an arbitrary label into a valid client name (letters, digits and _ . @ + -).
func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("_.@+-", r):
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
