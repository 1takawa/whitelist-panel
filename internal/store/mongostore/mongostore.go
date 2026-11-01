// Package mongostore implements store.Store on MongoDB.
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"anypanel2/internal/model"
	"anypanel2/internal/store"
)

type Store struct {
	client   *mongo.Client
	admins   *mongo.Collection
	sessions *mongo.Collection
	inbounds *mongo.Collection
	clients  *mongo.Collection
	settings *mongo.Collection
	nodes    *mongo.Collection
	samples  *mongo.Collection
	servers  *mongo.Collection

	srvTraffic *mongo.Collection
	cascades   *mongo.Collection

	// userSamples holds each client's traffic per hour, for the dashboard's top lists over a day and a week.
	userSamples *mongo.Collection
}

type adminDoc struct {
	Username     string    `bson:"_id"`
	PasswordHash string    `bson:"passwordHash"`
	CreatedAt    time.Time `bson:"createdAt"`
}

type sessionDoc struct {
	TokenHash string    `bson:"_id"`
	Username  string    `bson:"username"`
	IP        string    `bson:"ip"`
	CreatedAt time.Time `bson:"createdAt"`
	ExpiresAt time.Time `bson:"expiresAt"`
}

const settingsID = "global"

type settingsDoc struct {
	ID             string `bson:"_id"`
	model.Settings `bson:",inline"`
}

// Open connects to MongoDB, verifies the connection and ensures indexes exist.
func Open(ctx context.Context, uri, dbName string) (*Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	db := client.Database(dbName)
	s := &Store{
		client: client, admins: db.Collection("admins"), sessions: db.Collection("sessions"),
		inbounds: db.Collection("inbounds"), clients: db.Collection("clients"), settings: db.Collection("settings"),
		nodes: db.Collection("external_nodes"), samples: db.Collection("traffic_samples"), servers: db.Collection("servers"),
		srvTraffic: db.Collection("server_traffic"),
		cascades:   db.Collection("cascades"),

		userSamples: db.Collection("user_samples"),
	}
	if err := s.ensureIndexes(ctx); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("mongo ensure indexes: %w", err)
	}
	return s, nil
}

func (s *Store) ensureIndexes(ctx context.Context) error {
	idx := func(c *mongo.Collection, key string, unique bool, ttl *int32) error {
		opts := options.Index().SetUnique(unique)
		if ttl != nil {
			opts = options.Index().SetExpireAfterSeconds(*ttl) // expired sessions are removed by MongoDB itself
		}
		_, err := c.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: key, Value: 1}}, Options: opts})
		return err
	}
	zero := int32(0)
	month := int32(30 * 24 * 3600)
	for _, f := range []func() error{
		func() error { return idx(s.sessions, "expiresAt", false, &zero) },
		func() error { return idx(s.samples, "at", false, &month) }, // hourly samples are kept for 30 days
		func() error { return idx(s.userSamples, "at", false, &month) },
		func() error { return idx(s.userSamples, "hour", false, nil) },
		func() error { return s.ensureInboundPortIndex(ctx) },
		func() error { return idx(s.clients, "email", true, nil) },
		func() error { return idx(s.clients, "subId", true, nil) },
	} {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error  { return s.client.Ping(ctx, nil) }
func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

func notFound(err error) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return store.ErrNotFound
	}
	return err
}

func conflict(err error) error {
	if mongo.IsDuplicateKeyError(err) {
		return store.ErrConflict
	}
	return err
}

// ---- admin / sessions ----

func (s *Store) GetAdmin(ctx context.Context, username string) (*store.Admin, error) {
	var d adminDoc
	if err := s.admins.FindOne(ctx, bson.M{"_id": username}).Decode(&d); err != nil {
		return nil, notFound(err)
	}
	return &store.Admin{Username: d.Username, PasswordHash: d.PasswordHash, CreatedAt: d.CreatedAt}, nil
}

func (s *Store) PutAdmin(ctx context.Context, a store.Admin) error {
	d := adminDoc{Username: a.Username, PasswordHash: a.PasswordHash, CreatedAt: a.CreatedAt}
	_, err := s.admins.ReplaceOne(ctx, bson.M{"_id": a.Username}, d, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) ListAdmins(ctx context.Context) ([]store.Admin, error) {
	cur, err := s.admins.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []adminDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]store.Admin, 0, len(docs))
	for _, d := range docs {
		out = append(out, store.Admin{Username: d.Username, PasswordHash: d.PasswordHash, CreatedAt: d.CreatedAt})
	}
	return out, nil
}

func (s *Store) DeleteAdmin(ctx context.Context, username string) error {
	res, err := s.admins.DeleteOne(ctx, bson.M{"_id": username})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAllSessions(ctx context.Context) error {
	_, err := s.sessions.DeleteMany(ctx, bson.M{})
	return err
}

func (s *Store) Snapshot(ctx context.Context) (*store.Snapshot, error) {
	snap := &store.Snapshot{}
	var err error
	if snap.Admins, err = s.ListAdmins(ctx); err != nil {
		return nil, err
	}
	if snap.Inbounds, err = s.ListInbounds(ctx); err != nil {
		return nil, err
	}
	if snap.Clients, err = s.ListClients(ctx); err != nil {
		return nil, err
	}
	if snap.Servers, err = s.ListServers(ctx); err != nil {
		return nil, err
	}
	if snap.Cascades, err = s.ListCascades(ctx); err != nil {
		return nil, err
	}
	if snap.ExternalNodes, err = s.ListExternalNodes(ctx); err != nil {
		return nil, err
	}
	if snap.Samples, err = s.ListSamples(ctx, 0); err != nil {
		return nil, err
	}
	cur, err := s.srvTraffic.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []srvTrafficDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	snap.ServerTraffic = make([]model.ServerTraffic, 0, len(docs))
	for _, d := range docs {
		snap.ServerTraffic = append(snap.ServerTraffic, d.ServerTraffic)
	}
	set, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	sec, err := s.GetSecrets(ctx)
	if err != nil {
		return nil, err
	}
	snap.Settings, snap.Secrets = *set, *sec
	return snap, nil
}

func (s *Store) ReplaceAll(ctx context.Context, snap *store.Snapshot) error {
	for _, c := range []*mongo.Collection{s.admins, s.sessions, s.inbounds, s.clients, s.nodes, s.samples, s.servers, s.srvTraffic, s.cascades, s.userSamples, s.settings} {
		if _, err := c.DeleteMany(ctx, bson.M{}); err != nil {
			return err
		}
	}
	insert := func(c *mongo.Collection, docs []any) error {
		if len(docs) == 0 {
			return nil
		}
		_, err := c.InsertMany(ctx, docs)
		return err
	}
	var docs []any
	for _, v := range snap.Admins {
		docs = append(docs, adminDoc{Username: v.Username, PasswordHash: v.PasswordHash, CreatedAt: v.CreatedAt})
	}
	if err := insert(s.admins, docs); err != nil {
		return err
	}
	docs = nil
	for _, v := range snap.Inbounds {
		docs = append(docs, v)
	}
	if err := insert(s.inbounds, docs); err != nil {
		return err
	}
	docs = nil
	for _, v := range snap.Clients {
		docs = append(docs, v)
	}
	if err := insert(s.clients, docs); err != nil {
		return err
	}
	docs = nil
	for _, v := range snap.ExternalNodes {
		docs = append(docs, v)
	}
	if err := insert(s.nodes, docs); err != nil {
		return err
	}
	docs = nil
	for _, v := range snap.Servers {
		docs = append(docs, v)
	}
	if err := insert(s.servers, docs); err != nil {
		return err
	}
	docs = nil
	for _, v := range snap.Samples {
		v.At = time.Unix(v.Hour*3600, 0).UTC()
		docs = append(docs, v)
	}
	if err := insert(s.samples, docs); err != nil {
		return err
	}
	docs = nil
	for _, v := range snap.ServerTraffic {
		docs = append(docs, srvTrafficDoc{ID: v.ServerID + "\x00" + v.Email, ServerTraffic: v})
	}
	if err := insert(s.srvTraffic, docs); err != nil {
		return err
	}
	docs = nil
	for _, v := range snap.Cascades {
		docs = append(docs, v)
	}
	if err := insert(s.cascades, docs); err != nil {
		return err
	}
	if err := s.PutSettings(ctx, snap.Settings); err != nil {
		return err
	}
	return s.PutSecrets(ctx, snap.Secrets)
}

func (s *Store) CreateSession(ctx context.Context, sess store.Session) error {
	_, err := s.sessions.InsertOne(ctx, sessionDoc{
		TokenHash: sess.TokenHash, Username: sess.Username, IP: sess.IP,
		CreatedAt: sess.CreatedAt, ExpiresAt: sess.ExpiresAt,
	})
	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (*store.Session, error) {
	var d sessionDoc
	if err := s.sessions.FindOne(ctx, bson.M{"_id": tokenHash}).Decode(&d); err != nil {
		return nil, notFound(err)
	}
	return &store.Session{TokenHash: d.TokenHash, Username: d.Username, IP: d.IP, CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt}, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.sessions.DeleteOne(ctx, bson.M{"_id": tokenHash})
	return err
}

// ---- inbounds ----

func (s *Store) ListInbounds(ctx context.Context) ([]model.Inbound, error) {
	cur, err := s.inbounds.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []model.Inbound{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetInbound(ctx context.Context, id string) (*model.Inbound, error) {
	var in model.Inbound
	if err := s.inbounds.FindOne(ctx, bson.M{"_id": id}).Decode(&in); err != nil {
		return nil, notFound(err)
	}
	return &in, nil
}

func (s *Store) PutInbound(ctx context.Context, in model.Inbound) error {
	_, err := s.inbounds.ReplaceOne(ctx, bson.M{"_id": in.ID}, in, options.Replace().SetUpsert(true))
	return conflict(err)
}

func (s *Store) DeleteInbound(ctx context.Context, id string) error {
	res, err := s.inbounds.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ---- clients ----

func (s *Store) ListClients(ctx context.Context) ([]model.Client, error) {
	cur, err := s.clients.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []model.Client{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetClient(ctx context.Context, id string) (*model.Client, error) {
	var c model.Client
	if err := s.clients.FindOne(ctx, bson.M{"_id": id}).Decode(&c); err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (s *Store) GetClientBySubID(ctx context.Context, subID string) (*model.Client, error) {
	var c model.Client
	if err := s.clients.FindOne(ctx, bson.M{"subId": subID}).Decode(&c); err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (s *Store) PutClient(ctx context.Context, c model.Client) error {
	_, err := s.clients.ReplaceOne(ctx, bson.M{"_id": c.ID}, c, options.Replace().SetUpsert(true))
	return conflict(err)
}

func (s *Store) DeleteClient(ctx context.Context, id string) error {
	res, err := s.clients.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) AddTraffic(ctx context.Context, deltas map[string]store.Traffic) error {
	if len(deltas) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(deltas))
	for email, d := range deltas {
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"email": email}).
			SetUpdate(bson.M{"$inc": bson.M{"up": d.Up, "down": d.Down}}))
	}
	_, err := s.clients.BulkWrite(ctx, models)
	return err
}

// ---- activity, external nodes, samples, secrets ----

func (s *Store) MarkActive(ctx context.Context, emails []string, at time.Time) error {
	if len(emails) == 0 {
		return nil
	}
	_, err := s.clients.UpdateMany(ctx, bson.M{"email": bson.M{"$in": emails}}, bson.M{"$set": bson.M{"lastActiveAt": at}})
	return err
}

func (s *Store) ListExternalNodes(ctx context.Context) ([]model.ExternalNode, error) {
	cur, err := s.nodes.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []model.ExternalNode{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetExternalNode(ctx context.Context, id string) (*model.ExternalNode, error) {
	var n model.ExternalNode
	if err := s.nodes.FindOne(ctx, bson.M{"_id": id}).Decode(&n); err != nil {
		return nil, notFound(err)
	}
	return &n, nil
}

func (s *Store) PutExternalNode(ctx context.Context, n model.ExternalNode) error {
	_, err := s.nodes.ReplaceOne(ctx, bson.M{"_id": n.ID}, n, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) DeleteExternalNode(ctx context.Context, id string) error {
	res, err := s.nodes.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) AddSample(ctx context.Context, hour, up, down int64, nodes map[string]int64) error {
	inc := bson.M{"up": up, "down": down}
	for id, n := range nodes {
		if id != "" && n != 0 && !strings.ContainsAny(id, ".$") { // ids become field names
			inc["nodes."+id] = n
		}
	}
	_, err := s.samples.UpdateOne(ctx, bson.M{"_id": hour},
		bson.M{"$inc": inc, "$set": bson.M{"at": time.Unix(hour*3600, 0).UTC()}},
		options.UpdateOne().SetUpsert(true))
	return err
}

func (s *Store) AddUserSamples(ctx context.Context, hour int64, bytes map[string]int64) error {
	models := make([]mongo.WriteModel, 0, len(bytes))
	at := time.Unix(hour*3600, 0).UTC()
	for email, n := range bytes {
		if n == 0 {
			continue
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": userSampleID(hour, email)}).
			SetUpdate(bson.M{"$inc": bson.M{"bytes": n}, "$set": bson.M{"hour": hour, "email": email, "at": at}}).
			SetUpsert(true))
	}
	if len(models) == 0 {
		return nil
	}
	_, err := s.userSamples.BulkWrite(ctx, models)
	return err
}

func (s *Store) UserTrafficSince(ctx context.Context, fromHour int64) (map[string]int64, error) {
	cur, err := s.userSamples.Aggregate(ctx, bson.A{
		bson.M{"$match": bson.M{"hour": bson.M{"$gte": fromHour}}},
		bson.M{"$group": bson.M{"_id": "$email", "sum": bson.M{"$sum": "$bytes"}}},
	})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID  string `bson:"_id"`
		Sum int64  `bson:"sum"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Sum
	}
	return out, nil
}

func (s *Store) ListSamples(ctx context.Context, fromHour int64) ([]model.Sample, error) {
	cur, err := s.samples.Find(ctx, bson.M{"_id": bson.M{"$gte": fromHour}}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []model.Sample{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

const secretsID = "secrets"

type secretsDoc struct {
	ID            string `bson:"_id"`
	model.Secrets `bson:",inline"`
}

func (s *Store) GetSecrets(ctx context.Context) (*model.Secrets, error) {
	var d secretsDoc
	if err := s.settings.FindOne(ctx, bson.M{"_id": secretsID}).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return &model.Secrets{}, nil
		}
		return nil, err
	}
	return &d.Secrets, nil
}

func (s *Store) PutSecrets(ctx context.Context, v model.Secrets) error {
	_, err := s.settings.ReplaceOne(ctx, bson.M{"_id": secretsID}, secretsDoc{ID: secretsID, Secrets: v}, options.Replace().SetUpsert(true))
	return err
}

// ---- settings ----

func (s *Store) GetSettings(ctx context.Context) (*model.Settings, error) {
	var d settingsDoc
	if err := s.settings.FindOne(ctx, bson.M{"_id": settingsID}).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return &model.Settings{}, nil
		}
		return nil, err
	}
	return &d.Settings, nil
}

func (s *Store) PutSettings(ctx context.Context, v model.Settings) error {
	_, err := s.settings.ReplaceOne(ctx, bson.M{"_id": settingsID}, settingsDoc{ID: settingsID, Settings: v}, options.Replace().SetUpsert(true))
	return err
}

// ensureInboundPortIndex makes ports unique per server. Releases before 0.7 had a unique index on the port alone,
// which would forbid the same port on two machines, so it is replaced. Local inbounds have no serverId.
func (s *Store) ensureInboundPortIndex(ctx context.Context) error {
	if err := s.inbounds.Indexes().DropOne(ctx, "port_1"); err != nil {
		var ce mongo.CommandError
		if !errors.As(err, &ce) || (ce.Code != 27 && ce.Code != 26) { // IndexNotFound / NamespaceNotFound
			return err
		}
	}
	_, err := s.inbounds.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "serverId", Value: 1}, {Key: "port", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return err
}

// ---- servers ----

func (s *Store) ListServers(ctx context.Context) ([]model.Server, error) {
	cur, err := s.servers.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []model.Server{}
	return out, cur.All(ctx, &out)
}

func (s *Store) GetServer(ctx context.Context, id string) (*model.Server, error) {
	var v model.Server
	if err := s.servers.FindOne(ctx, bson.M{"_id": id}).Decode(&v); err != nil {
		return nil, notFound(err)
	}
	return &v, nil
}

func (s *Store) PutServer(ctx context.Context, v model.Server) error {
	_, err := s.servers.ReplaceOne(ctx, bson.M{"_id": v.ID}, v, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) DeleteServer(ctx context.Context, id string) error {
	res, err := s.servers.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

// srvTrafficDoc keeps one row per (server, client); the id is the pair, so a bulk upsert needs no read first.
type srvTrafficDoc struct {
	ID                  string `bson:"_id"`
	model.ServerTraffic `bson:",inline"`
}

func (s *Store) AddServerTraffic(ctx context.Context, serverID string, deltas map[string]store.Traffic, at time.Time) error {
	if len(deltas) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(deltas))
	for email, d := range deltas {
		upd := bson.M{
			"$inc":         bson.M{"up": d.Up, "down": d.Down},
			"$setOnInsert": bson.M{"serverId": serverID, "email": email},
		}
		if d.Up+d.Down > 0 {
			upd["$set"] = bson.M{"lastActiveAt": at}
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": serverID + "\x00" + email}).SetUpdate(upd).SetUpsert(true))
	}
	_, err := s.srvTraffic.BulkWrite(ctx, models)
	return err
}

func (s *Store) ListServerTraffic(ctx context.Context, serverID string) ([]model.ServerTraffic, error) {
	cur, err := s.srvTraffic.Find(ctx, bson.M{"serverId": serverID}, options.Find().SetSort(bson.D{{Key: "email", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []srvTrafficDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]model.ServerTraffic, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.ServerTraffic)
	}
	return out, nil
}

func (s *Store) DeleteServerTraffic(ctx context.Context, serverID string) error {
	_, err := s.srvTraffic.DeleteMany(ctx, bson.M{"serverId": serverID})
	return err
}

// ---- cascades ----

func (s *Store) ListCascades(ctx context.Context) ([]model.Cascade, error) {
	cur, err := s.cascades.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []model.Cascade{}
	return out, cur.All(ctx, &out)
}

func (s *Store) GetCascade(ctx context.Context, id string) (*model.Cascade, error) {
	var v model.Cascade
	if err := s.cascades.FindOne(ctx, bson.M{"_id": id}).Decode(&v); err != nil {
		return nil, notFound(err)
	}
	return &v, nil
}

func (s *Store) PutCascade(ctx context.Context, v model.Cascade) error {
	_, err := s.cascades.ReplaceOne(ctx, bson.M{"_id": v.ID}, v, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) DeleteCascade(ctx context.Context, id string) error {
	res, err := s.cascades.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

// userSampleID is the document id of one client's bucket for one hour.
func userSampleID(hour int64, email string) string { return fmt.Sprintf("%d\x00%s", hour, email) }
