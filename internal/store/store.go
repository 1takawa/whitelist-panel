// Package store defines the persistence interface used by the panel.
// Implementations: mongostore (production) and memstore (tests, local dev).
package store

import (
	"context"
	"errors"
	"time"

	"anypanel2/internal/model"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict") // unique constraint violated (port, email, subId)
)

type Admin struct {
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

type Session struct {
	TokenHash string // sha256 of the cookie token; the raw token is never stored
	Username  string
	IP        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Traffic is a byte delta for one client.
type Traffic struct{ Up, Down int64 }

// Snapshot is everything the panel keeps, except login sessions. It is what the full database export carries.
type Snapshot struct {
	Admins        []Admin
	Inbounds      []model.Inbound
	Clients       []model.Client
	Servers       []model.Server
	ServerTraffic []model.ServerTraffic
	Cascades      []model.Cascade
	ExternalNodes []model.ExternalNode
	Samples       []model.Sample
	Settings      model.Settings
	Secrets       model.Secrets
}

type Store interface {
	Ping(ctx context.Context) error
	Close(ctx context.Context) error

	GetAdmin(ctx context.Context, username string) (*Admin, error)
	PutAdmin(ctx context.Context, a Admin) error
	ListAdmins(ctx context.Context) ([]Admin, error)
	DeleteAdmin(ctx context.Context, username string) error

	// Snapshot reads the whole database. ReplaceAll wipes it and writes the snapshot instead (sessions included:
	// everybody is signed out). It is not atomic, so callers keep the previous snapshot to roll back with.
	Snapshot(ctx context.Context) (*Snapshot, error)
	ReplaceAll(ctx context.Context, snap *Snapshot) error
	DeleteAllSessions(ctx context.Context) error

	CreateSession(ctx context.Context, s Session) error
	GetSession(ctx context.Context, tokenHash string) (*Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error

	// Inbounds are returned oldest first. PutInbound is an upsert and returns ErrConflict if the port is taken.
	ListInbounds(ctx context.Context) ([]model.Inbound, error)
	GetInbound(ctx context.Context, id string) (*model.Inbound, error)
	PutInbound(ctx context.Context, in model.Inbound) error
	DeleteInbound(ctx context.Context, id string) error

	// Clients are returned oldest first. PutClient is an upsert and returns ErrConflict if email or subId is taken.
	ListClients(ctx context.Context) ([]model.Client, error)
	GetClient(ctx context.Context, id string) (*model.Client, error)
	GetClientBySubID(ctx context.Context, subID string) (*model.Client, error)
	PutClient(ctx context.Context, c model.Client) error
	DeleteClient(ctx context.Context, id string) error
	// AddTraffic atomically adds byte deltas keyed by client email; unknown emails are ignored.
	AddTraffic(ctx context.Context, deltas map[string]Traffic) error

	// MarkActive records that the clients (by email) moved traffic at the given time.
	MarkActive(ctx context.Context, emails []string, at time.Time) error

	// Servers (extra machines running the agent) are returned oldest first.
	ListServers(ctx context.Context) ([]model.Server, error)
	GetServer(ctx context.Context, id string) (*model.Server, error)
	PutServer(ctx context.Context, s model.Server) error
	DeleteServer(ctx context.Context, id string) error
	// Per-server client traffic: AddServerTraffic adds deltas (keyed by client email) and marks the clients that moved
	// bytes as active at the given time; DeleteServerTraffic forgets everything about the server.
	AddServerTraffic(ctx context.Context, serverID string, deltas map[string]Traffic, at time.Time) error
	ListServerTraffic(ctx context.Context, serverID string) ([]model.ServerTraffic, error)
	DeleteServerTraffic(ctx context.Context, serverID string) error

	// Cascades (entry server -> exit server chains) are returned oldest first.
	ListCascades(ctx context.Context) ([]model.Cascade, error)
	GetCascade(ctx context.Context, id string) (*model.Cascade, error)
	PutCascade(ctx context.Context, c model.Cascade) error
	DeleteCascade(ctx context.Context, id string) error

	// External nodes are returned oldest first.
	ListExternalNodes(ctx context.Context) ([]model.ExternalNode, error)
	GetExternalNode(ctx context.Context, id string) (*model.ExternalNode, error)
	PutExternalNode(ctx context.Context, n model.ExternalNode) error
	DeleteExternalNode(ctx context.Context, id string) error

	// Hourly traffic totals for the dashboard chart. AddSample adds to the bucket of the given Unix hour.
	// nodes adds the traffic of each inbound (by inbound id) to the same bucket; it may be nil.
	AddSample(ctx context.Context, hour, up, down int64, nodes map[string]int64) error
	ListSamples(ctx context.Context, fromHour int64) ([]model.Sample, error)
	// AddUserSamples adds each client's traffic (client email → bytes) to its bucket of the given Unix hour; UserTrafficSince
	// sums those buckets from the given hour on. They feed the dashboard's top lists over a day and a week.
	AddUserSamples(ctx context.Context, hour int64, bytes map[string]int64) error
	UserTrafficSince(ctx context.Context, fromHour int64) (map[string]int64, error)

	// Secrets (API token hash, Telegram token) are kept apart from the settings the UI can read.
	GetSecrets(ctx context.Context) (*model.Secrets, error)
	PutSecrets(ctx context.Context, s model.Secrets) error

	// GetSettings returns zero-value settings if none were saved yet.
	GetSettings(ctx context.Context) (*model.Settings, error)
	PutSettings(ctx context.Context, s model.Settings) error
}
