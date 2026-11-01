// Package memstore is an in-memory store.Store for tests and local development.
package memstore

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/store"
)

type Store struct {
	mu       sync.Mutex
	admins   map[string]store.Admin
	sessions map[string]store.Session
	inbounds map[string]model.Inbound
	clients  map[string]model.Client
	nodes    map[string]model.ExternalNode
	servers  map[string]model.Server
	cascades map[string]model.Cascade
	samples  map[int64]model.Sample
	userSamp map[int64]map[string]int64 // hour → client → bytes
	secrets  model.Secrets
	settings model.Settings

	srvTraffic map[string]model.ServerTraffic // serverID + NUL + email
}

func New() *Store {
	return &Store{
		admins:   map[string]store.Admin{},
		sessions: map[string]store.Session{},
		inbounds: map[string]model.Inbound{},
		clients:  map[string]model.Client{},
		nodes:    map[string]model.ExternalNode{},
		servers:  map[string]model.Server{},
		cascades: map[string]model.Cascade{},
		samples:  map[int64]model.Sample{},
		userSamp: map[int64]map[string]int64{},
	}
}

func (s *Store) Ping(context.Context) error  { return nil }
func (s *Store) Close(context.Context) error { return nil }

func (s *Store) GetAdmin(_ context.Context, username string) (*store.Admin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.admins[username]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &a, nil
}

func (s *Store) PutAdmin(_ context.Context, a store.Admin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admins[a.Username] = a
	return nil
}

func (s *Store) ListAdmins(context.Context) ([]store.Admin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Admin, 0, len(s.admins))
	for _, a := range s.admins {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
}

func (s *Store) DeleteAdmin(_ context.Context, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admins[username]; !ok {
		return store.ErrNotFound
	}
	delete(s.admins, username)
	return nil
}

func (s *Store) DeleteAllSessions(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]store.Session{}
	return nil
}

func (s *Store) Snapshot(ctx context.Context) (*store.Snapshot, error) {
	admins, _ := s.ListAdmins(ctx)
	ins, _ := s.ListInbounds(ctx)
	cls, _ := s.ListClients(ctx)
	srvs, _ := s.ListServers(ctx)
	cascades, _ := s.ListCascades(ctx)
	nodes, _ := s.ListExternalNodes(ctx)
	samples, _ := s.ListSamples(ctx, 0)
	sec, _ := s.GetSecrets(ctx)
	set, _ := s.GetSettings(ctx)
	snap := &store.Snapshot{
		Admins: admins, Inbounds: ins, Clients: cls, Servers: srvs, ExternalNodes: nodes, Samples: samples,
		ServerTraffic: []model.ServerTraffic{}, Settings: *set, Secrets: *sec, Cascades: cascades,
	}
	s.mu.Lock()
	for _, v := range s.srvTraffic {
		snap.ServerTraffic = append(snap.ServerTraffic, v)
	}
	s.mu.Unlock()
	sort.Slice(snap.ServerTraffic, func(i, j int) bool {
		a, b := snap.ServerTraffic[i], snap.ServerTraffic[j]
		return a.ServerID+"\x00"+a.Email < b.ServerID+"\x00"+b.Email
	})
	return snap, nil
}

func (s *Store) ReplaceAll(_ context.Context, snap *store.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admins = map[string]store.Admin{}
	for _, a := range snap.Admins {
		s.admins[a.Username] = a
	}
	s.sessions = map[string]store.Session{}
	s.inbounds = map[string]model.Inbound{}
	for _, v := range snap.Inbounds {
		s.inbounds[v.ID] = v
	}
	s.clients = map[string]model.Client{}
	for _, v := range snap.Clients {
		s.clients[v.ID] = v
	}
	s.nodes = map[string]model.ExternalNode{}
	for _, v := range snap.ExternalNodes {
		s.nodes[v.ID] = v
	}
	s.servers = map[string]model.Server{}
	for _, v := range snap.Servers {
		s.servers[v.ID] = v
	}
	s.cascades = map[string]model.Cascade{}
	for _, v := range snap.Cascades {
		s.cascades[v.ID] = v
	}
	s.samples = map[int64]model.Sample{}
	for _, v := range snap.Samples {
		s.samples[v.Hour] = v
	}
	s.srvTraffic = map[string]model.ServerTraffic{}
	for _, v := range snap.ServerTraffic {
		s.srvTraffic[v.ServerID+"\x00"+v.Email] = v
	}
	s.settings, s.secrets = snap.Settings, snap.Secrets
	return nil
}

func (s *Store) CreateSession(_ context.Context, sess store.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.TokenHash] = sess
	return nil
}

func (s *Store) GetSession(_ context.Context, tokenHash string) (*store.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[tokenHash]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &sess, nil
}

func (s *Store) DeleteSession(_ context.Context, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, tokenHash)
	return nil
}

// ---- inbounds ----

func (s *Store) ListInbounds(context.Context) ([]model.Inbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Inbound, 0, len(s.inbounds))
	for _, in := range s.inbounds {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (s *Store) GetInbound(_ context.Context, id string) (*model.Inbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.inbounds[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &in, nil
}

func (s *Store) PutInbound(_ context.Context, in model.Inbound) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.inbounds {
		if o.ID != in.ID && o.Port == in.Port && o.ServerID == in.ServerID {
			return store.ErrConflict
		}
	}
	s.inbounds[in.ID] = in
	return nil
}

func (s *Store) DeleteInbound(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inbounds[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.inbounds, id)
	return nil
}

// ---- clients ----

func (s *Store) ListClients(context.Context) ([]model.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Client, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (s *Store) GetClient(_ context.Context, id string) (*model.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &c, nil
}

func (s *Store) GetClientBySubID(_ context.Context, subID string) (*model.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clients {
		if c.SubID == subID {
			c := c
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Store) PutClient(_ context.Context, c model.Client) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.clients {
		if o.ID != c.ID && (o.Email == c.Email || o.SubID == c.SubID) {
			return store.ErrConflict
		}
	}
	s.clients[c.ID] = c
	return nil
}

func (s *Store) DeleteClient(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.clients, id)
	return nil
}

func (s *Store) AddTraffic(_ context.Context, deltas map[string]store.Traffic) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.clients {
		if d, ok := deltas[c.Email]; ok {
			c.Up += d.Up
			c.Down += d.Down
			s.clients[id] = c
		}
	}
	return nil
}

// ---- activity, external nodes, samples, secrets ----

func (s *Store) MarkActive(_ context.Context, emails []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.clients {
		if slices.Contains(emails, c.Email) {
			t := at
			c.LastActiveAt = &t
			s.clients[id] = c
		}
	}
	return nil
}

func (s *Store) ListExternalNodes(context.Context) ([]model.ExternalNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.ExternalNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (s *Store) GetExternalNode(_ context.Context, id string) (*model.ExternalNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &n, nil
}

func (s *Store) PutExternalNode(_ context.Context, n model.ExternalNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[n.ID] = n
	return nil
}

func (s *Store) DeleteExternalNode(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.nodes, id)
	return nil
}

func (s *Store) AddSample(_ context.Context, hour, up, down int64, nodes map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.samples[hour]
	v.Hour, v.Up, v.Down = hour, v.Up+up, v.Down+down
	merged := make(map[string]int64, len(v.Nodes)+len(nodes))
	for id, n := range v.Nodes {
		merged[id] = n
	}
	for id, n := range nodes {
		merged[id] += n
	}
	if len(merged) > 0 {
		v.Nodes = merged
	}
	s.samples[hour] = v
	return nil
}

func (s *Store) AddUserSamples(_ context.Context, hour int64, bytes map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userSamp[hour] == nil {
		s.userSamp[hour] = map[string]int64{}
	}
	for email, n := range bytes {
		s.userSamp[hour][email] += n
	}
	return nil
}

func (s *Store) UserTrafficSince(_ context.Context, fromHour int64) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{}
	for h, m := range s.userSamp {
		if h < fromHour {
			continue
		}
		for email, n := range m {
			out[email] += n
		}
	}
	return out, nil
}

func (s *Store) ListSamples(_ context.Context, fromHour int64) ([]model.Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Sample
	for h, v := range s.samples {
		if h >= fromHour {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hour < out[j].Hour })
	return out, nil
}

func (s *Store) GetSecrets(context.Context) (*model.Secrets, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.secrets
	return &v, nil
}

func (s *Store) PutSecrets(_ context.Context, v model.Secrets) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets = v
	return nil
}

// ---- settings ----

func (s *Store) GetSettings(context.Context) (*model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.settings
	return &v, nil
}

func (s *Store) PutSettings(_ context.Context, v model.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = v
	return nil
}

// ---- servers ----

func (s *Store) ListServers(context.Context) ([]model.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Server, 0, len(s.servers))
	for _, v := range s.servers {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *Store) GetServer(_ context.Context, id string) (*model.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.servers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &v, nil
}

func (s *Store) PutServer(_ context.Context, v model.Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers[v.ID] = v
	return nil
}

func (s *Store) DeleteServer(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.servers, id)
	return nil
}

func (s *Store) AddServerTraffic(_ context.Context, serverID string, deltas map[string]store.Traffic, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srvTraffic == nil {
		s.srvTraffic = map[string]model.ServerTraffic{}
	}
	for email, d := range deltas {
		key := serverID + "\x00" + email
		v := s.srvTraffic[key]
		v.ServerID, v.Email = serverID, email
		v.Up, v.Down = v.Up+d.Up, v.Down+d.Down
		if d.Up+d.Down > 0 {
			t := at
			v.LastActiveAt = &t
		}
		s.srvTraffic[key] = v
	}
	return nil
}

func (s *Store) ListServerTraffic(_ context.Context, serverID string) ([]model.ServerTraffic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []model.ServerTraffic{}
	for _, v := range s.srvTraffic {
		if v.ServerID == serverID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

func (s *Store) DeleteServerTraffic(_ context.Context, serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.srvTraffic {
		if v.ServerID == serverID {
			delete(s.srvTraffic, k)
		}
	}
	return nil
}

// ---- cascades ----

func (s *Store) ListCascades(context.Context) ([]model.Cascade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Cascade, 0, len(s.cascades))
	for _, c := range s.cascades {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return out, nil
}

func (s *Store) GetCascade(_ context.Context, id string) (*model.Cascade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cascades[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &c, nil
}

func (s *Store) PutCascade(_ context.Context, c model.Cascade) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cascades == nil {
		s.cascades = map[string]model.Cascade{}
	}
	s.cascades[c.ID] = c
	return nil
}

func (s *Store) DeleteCascade(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cascades[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.cascades, id)
	return nil
}
