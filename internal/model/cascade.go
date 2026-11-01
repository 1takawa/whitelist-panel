package model

import "time"

// Cascade chains two servers: clients connect to the entry server (A) and everything they send leaves the internet from
// the exit server (B). The cascade is shown to clients as a node of its own (name and flag), which is an inbound on
// the entry server (Inbound.CascadeID points back here); its traffic is routed to an outbound towards a VLESS inbound of
// the exit server, where the panel keeps a relay user.
type Cascade struct {
	ID      string `json:"id" bson:"_id"`
	Name    string `json:"name" bson:"name"`       // shown to clients next to the flag
	Country string `json:"country" bson:"country"` // two-letter code of the flag; empty when unknown

	EntryServerID string `json:"entryServerId" bson:"entryServerId"` // "" = the main server
	ExitServerID  string `json:"exitServerId" bson:"exitServerId"`   // "" = the main server
	ExitInboundID string `json:"exitInboundId" bson:"exitInboundId"` // the VLESS inbound of the exit server the relay user is added to
	InboundID     string `json:"inboundId" bson:"inboundId"`         // the inbound created on the entry server
	// Domain is the REALITY domain of this cascade on the main server (its own name behind nginx on 443, with a decoy
	// site); empty for a cascade that uses the main REALITY domain or a port of its own.
	Domain string `json:"domain" bson:"domain"`
	// Adopted is set when the entry is the connection a node already had (its nginx-SNI REALITY connection on 443),
	// not one made for the cascade: deleting the cascade gives that connection back under its old name.
	Adopted    bool   `json:"adopted,omitempty" bson:"adopted,omitempty"`
	OrigRemark string `json:"origRemark,omitempty" bson:"origRemark,omitempty"`

	// RelayUUID is the identity the entry server presents to the exit server. It is never shown to clients.
	RelayUUID string    `json:"-" bson:"relayUuid"`
	CreatedAt time.Time `json:"createdAt" bson:"createdAt"`
}

// RelayEmail is the user name of the relay on the exit server (it is not a client of the panel: its traffic is the sum
// of the cascade's clients and is not accounted per client).
func (c Cascade) RelayEmail() string { return "cascade-" + c.ID }

// RelayClient is the synthetic client that lets the entry server in on the exit server's inbound.
func (c Cascade) RelayClient() Client {
	return Client{ID: "relay-" + c.ID, Email: c.RelayEmail(), UUID: c.RelayUUID, Flow: FlowVision, Enable: true, InboundIDs: []string{c.ExitInboundID}}
}
