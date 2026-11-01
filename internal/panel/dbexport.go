package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"anypanel2/internal/legacy"
	"anypanel2/internal/model"
	"anypanel2/internal/store"
)

// Database export. Two kinds exist:
//
//	full  – the whole panel: administrator login and password hash, settings, inbounds, servers (with the keys the
//	        panel holds for them), external nodes, clients with their counters, API token hash and bot token.
//	users – only the clients, with the credentials they connect with, so their links keep working elsewhere.
//
// Both are one JSON document. ImportDB reads either, and also the users file of the previous panel (mongoexport).
const (
	ExportFormat  = "anypanel2-db"
	ExportVersion = 1
	ExportFull    = "full"
	ExportUsers   = "users"

	maxImportClients = 50000
)

// moscow is the time zone of the daily copy. Moscow has had no daylight saving since 2014, so a fixed offset is exact
// and does not depend on the time zone database being installed.
var moscow = time.FixedZone("MSK", 3*3600)

type exportAdmin struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"passwordHash"`
	CreatedAt    time.Time `json:"createdAt"`
}

// exportServer adds what model.Server hides from the API: the agent token and the SSH key.
type exportServer struct {
	model.Server
	AgentToken string `json:"agentToken"`
	SSHKey     string `json:"sshKey,omitempty"`
}

// exportCascade adds the relay identity, which model.Cascade keeps out of the API.
type exportCascade struct {
	model.Cascade
	RelayUUID string `json:"relayUuid"`
}

type exportSecrets struct {
	APITokenHash      string     `json:"apiTokenHash,omitempty"`
	APITokenPrefix    string     `json:"apiTokenPrefix,omitempty"`
	APITokenCreatedAt *time.Time `json:"apiTokenCreatedAt,omitempty"`
	TelegramToken     string     `json:"telegramToken,omitempty"`
	RegRuPassword     string     `json:"regruPassword,omitempty"`
	RegRuCert         string     `json:"regruCert,omitempty"`
	RegRuKey          string     `json:"regruKey,omitempty"`
}

// Export is the file format.
type Export struct {
	Format       string    `json:"format"`
	Version      int       `json:"version"`
	Kind         string    `json:"kind"`
	CreatedAt    time.Time `json:"createdAt"`
	PanelVersion string    `json:"panelVersion,omitempty"`

	Clients []model.Client `json:"clients"`

	// Full export only.
	Admins        []exportAdmin         `json:"admins,omitempty"`
	Inbounds      []model.Inbound       `json:"inbounds,omitempty"`
	Servers       []exportServer        `json:"servers,omitempty"`
	ServerTraffic []model.ServerTraffic `json:"serverTraffic,omitempty"`
	ExternalNodes []model.ExternalNode  `json:"externalNodes,omitempty"`
	Cascades      []exportCascade       `json:"cascades,omitempty"`
	Samples       []model.Sample        `json:"samples,omitempty"`
	Settings      *model.Settings       `json:"settings,omitempty"`
	Secrets       *exportSecrets        `json:"secrets,omitempty"`
}

// ExportDB serializes the database. It returns the suggested file name and the file's bytes.
func (s *Service) ExportDB(ctx context.Context, kind string) (string, []byte, error) {
	if kind != ExportFull && kind != ExportUsers {
		return "", nil, invalid("kind must be %s or %s", ExportFull, ExportUsers)
	}
	snap, err := s.Store.Snapshot(ctx)
	if err != nil {
		return "", nil, err
	}
	now := s.Now()
	exp := Export{Format: ExportFormat, Version: ExportVersion, Kind: kind, CreatedAt: now.UTC(), PanelVersion: s.Version, Clients: snap.Clients}
	if exp.Clients == nil {
		exp.Clients = []model.Client{}
	}
	if kind == ExportFull {
		for _, a := range snap.Admins {
			exp.Admins = append(exp.Admins, exportAdmin{Username: a.Username, PasswordHash: a.PasswordHash, CreatedAt: a.CreatedAt})
		}
		exp.Inbounds, exp.ServerTraffic, exp.ExternalNodes, exp.Samples = snap.Inbounds, snap.ServerTraffic, snap.ExternalNodes, snap.Samples
		for _, c := range snap.Cascades {
			exp.Cascades = append(exp.Cascades, exportCascade{Cascade: c, RelayUUID: c.RelayUUID})
		}
		for _, sv := range snap.Servers {
			exp.Servers = append(exp.Servers, exportServer{Server: sv, AgentToken: sv.Token, SSHKey: sv.SSHKey})
		}
		set := snap.Settings.WithDefaults()
		exp.Settings = &set
		sec := snap.Secrets
		exp.Secrets = &exportSecrets{APITokenHash: sec.APITokenHash, APITokenPrefix: sec.APITokenPrefix, APITokenCreatedAt: sec.APITokenCreatedAt, TelegramToken: sec.TelegramToken,
			RegRuPassword: sec.RegRuPassword, RegRuCert: sec.RegRuCert, RegRuKey: sec.RegRuKey}
	}
	data, err := json.Marshal(exp)
	if err != nil {
		return "", nil, err
	}
	name := fmt.Sprintf("anypanel-%s-%s.json", kind, now.In(moscow).Format("2006-01-02_15-04"))
	return name, data, nil
}

// ImportOptions control ImportDB.
type ImportOptions struct {
	// DryRun reports what would happen without changing anything.
	DryRun bool
	// ExpiryFrom is only for the previous panel's file: what "expiration_days" counts from, "creation" (default) or "now".
	ExpiryFrom string
}

// ImportItem is the outcome for one client of a users or legacy file.
type ImportItem struct {
	Name   string   `json:"name"`
	Action string   `json:"action"` // create | skip
	Reason string   `json:"reason,omitempty"`
	Notes  []string `json:"notes,omitempty"`
}

// ImportReport tells what a file contained and what was (or, for a dry run, would be) done with it.
type ImportReport struct {
	Kind       string         `json:"kind"` // full | users | legacy
	DryRun     bool           `json:"dryRun"`
	ExportedAt *time.Time     `json:"exportedAt,omitempty"`
	Created    int            `json:"created"`
	Skipped    int            `json:"skipped"`
	Counts     map[string]int `json:"counts,omitempty"` // full: what replaces the current data
	Items      []ImportItem   `json:"items"`
	// NotMigrated counts what the previous panel's records had that this panel has no equivalent for.
	NotMigrated map[string]int `json:"notMigrated,omitempty"`
	Warning     string         `json:"warning,omitempty"`
}

// ImportDB reads an export file (full, users) or the previous panel's users file and applies it.
// A full file replaces everything and signs every administrator out; a users file only adds clients that are missing.
func (s *Service) ImportDB(ctx context.Context, data []byte, opts ImportOptions) (*ImportReport, error) {
	data = bytes.TrimPrefix(bytes.TrimSpace(data), []byte("\xef\xbb\xbf"))
	if len(data) == 0 {
		return nil, invalid("the file is empty")
	}
	var head struct {
		Format string `json:"format"`
	}
	if data[0] == '{' {
		_ = json.NewDecoder(bytes.NewReader(data)).Decode(&head) // a JSON-lines file of the old panel has no "format"
	}
	if head.Format == "" {
		users, err := legacy.Parse(bytes.NewReader(data))
		if err != nil {
			return nil, invalid("не удалось прочитать файл: это не выгрузка панели и не users.json прежней панели (%v)", err)
		}
		return s.importLegacyReport(ctx, users, opts)
	}
	if head.Format != ExportFormat {
		return nil, invalid("unknown file format %q", head.Format)
	}
	var exp Export
	if err := json.Unmarshal(data, &exp); err != nil {
		return nil, invalid("the file is damaged: %v", err)
	}
	if exp.Version < 1 || exp.Version > ExportVersion {
		return nil, invalid("the file was made by a newer panel (format version %d); update this panel first", exp.Version)
	}
	switch exp.Kind {
	case ExportFull:
		return s.importFull(ctx, exp, opts)
	case ExportUsers:
		return s.importUsers(ctx, exp, opts)
	}
	return nil, invalid("unknown export kind %q", exp.Kind)
}

func (s *Service) importLegacyReport(ctx context.Context, users []legacy.User, opts ImportOptions) (*ImportReport, error) {
	rep, err := s.ImportLegacy(ctx, users, LegacyOptions{ExpiryFrom: opts.ExpiryFrom, DryRun: opts.DryRun})
	if rep == nil {
		return nil, err
	}
	out := &ImportReport{Kind: "legacy", DryRun: rep.DryRun, Created: rep.Created, Skipped: rep.Skipped, NotMigrated: rep.NotMigrated, Items: make([]ImportItem, 0, len(rep.Items))}
	for _, it := range rep.Items {
		out.Items = append(out.Items, ImportItem{Name: it.Name, Action: it.Action, Reason: it.Reason, Notes: it.Notes})
	}
	if err != nil { // saved, but applying to Xray failed: the report is still valid
		out.Warning = err.Error()
	}
	return out, nil
}

// ---- users ----

func (s *Service) importUsers(ctx context.Context, exp Export, opts ImportOptions) (*ImportReport, error) {
	if len(exp.Clients) == 0 {
		return nil, invalid("the file contains no users")
	}
	if len(exp.Clients) > maxImportClients {
		return nil, invalid("at most %d users per import", maxImportClients)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	known, fallback := map[string]bool{}, []string{}
	for _, in := range ins {
		known[in.ID] = true
		fallback = append(fallback, in.ID)
	}
	existing, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	names, subs, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range existing {
		names[strings.ToLower(c.Email)], subs[c.SubID], ids[c.ID] = true, true, true
	}

	now := s.Now().UTC()
	rep := &ImportReport{Kind: ExportUsers, DryRun: opts.DryRun, ExportedAt: &exp.CreatedAt, Items: []ImportItem{}}
	for _, c := range exp.Clients {
		it := ImportItem{Name: c.Email}
		skip := func(reason string) {
			it.Action, it.Reason = "skip", reason
			rep.Skipped++
			rep.Items = append(rep.Items, it)
		}
		if err := normalizeClient(&c); err != nil {
			skip(err.Error())
			continue
		}
		it.Name = c.Email
		if names[strings.ToLower(c.Email)] {
			skip("клиент с таким именем уже есть")
			continue
		}
		if c.ID == "" || ids[c.ID] {
			c.ID = newID()
		}
		if c.SubID == "" || subs[c.SubID] {
			if c.SubID != "" {
				it.Notes = append(it.Notes, "токен подписки уже занят другим клиентом, выдан новый")
			}
			c.SubID = newSubID()
		}
		if c.UUID == "" {
			c.UUID = newUUID()
		}
		if c.Password == "" {
			c.Password = newPassword()
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		kept := make([]string, 0, len(c.InboundIDs))
		for _, id := range c.InboundIDs {
			if known[id] {
				kept = append(kept, id)
			}
		}
		if len(kept) == 0 && len(fallback) > 0 {
			kept = append(kept, fallback...)
			it.Notes = append(it.Notes, "подключения из файла здесь не найдены: клиент привязан ко всем включённым подключениям")
		}
		c.InboundIDs = kept

		it.Action = "create"
		if !opts.DryRun {
			if err := s.putClient(ctx, c); err != nil {
				skip(err.Error())
				continue
			}
		}
		names[strings.ToLower(c.Email)], subs[c.SubID], ids[c.ID] = true, true, true
		rep.Created++
		rep.Items = append(rep.Items, it)
	}
	if !opts.DryRun && rep.Created > 0 && s.Xray != nil {
		if err := s.applyLocked(ctx, false); err != nil {
			rep.Warning = wrapApply(err).Error()
		}
	}
	return rep, nil
}

// ---- full ----

// snapshotFromExport validates a full export and turns it into what the store holds. Everything is checked before
// anything is replaced, because replacing is not atomic.
func snapshotFromExport(exp Export) (*store.Snapshot, error) {
	if exp.Settings == nil {
		return nil, invalid("the file has no settings: it is not a full export")
	}
	if len(exp.Admins) == 0 {
		return nil, invalid("the file has no administrator account: importing it would lock everybody out")
	}
	if len(exp.Clients) > maxImportClients {
		return nil, invalid("at most %d users per import", maxImportClients)
	}
	snap := &store.Snapshot{}
	for _, a := range exp.Admins {
		if a.Username == "" || !strings.HasPrefix(a.PasswordHash, "$argon2id$") {
			return nil, invalid("the administrator account in the file is damaged")
		}
		snap.Admins = append(snap.Admins, store.Admin{Username: a.Username, PasswordHash: a.PasswordHash, CreatedAt: a.CreatedAt})
	}

	servers := map[string]bool{}
	for _, sv := range exp.Servers {
		if sv.ID == "" || servers[sv.ID] {
			return nil, invalid("the file lists a server twice or without an id")
		}
		servers[sv.ID] = true
		v := sv.Server
		v.Token, v.SSHKey = sv.AgentToken, sv.SSHKey
		snap.Servers = append(snap.Servers, v)
	}

	inbounds, ports := map[string]bool{}, map[string]bool{}
	for _, in := range exp.Inbounds {
		key := fmt.Sprintf("%s/%d", in.ServerID, in.Port)
		switch {
		case in.ID == "" || inbounds[in.ID]:
			return nil, invalid("the file lists an inbound twice or without an id")
		case ports[key]:
			return nil, invalid("two inbounds use port %d", in.Port)
		case in.ServerID != "" && !servers[in.ServerID]:
			return nil, invalid("inbound %q belongs to a server that is not in the file", in.Remark)
		}
		inbounds[in.ID], ports[key] = true, true
		snap.Inbounds = append(snap.Inbounds, in)
	}

	ids, emails, subs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range exp.Clients {
		switch {
		case c.ID == "" || ids[c.ID]:
			return nil, invalid("the file lists a client twice or without an id")
		case c.Email == "" || emails[c.Email]:
			return nil, invalid("client name %q is empty or used twice", c.Email)
		case c.SubID == "" || subs[c.SubID]:
			return nil, invalid("client %q has an empty or repeated subscription id", c.Email)
		}
		for _, id := range c.InboundIDs {
			if !inbounds[id] {
				return nil, invalid("client %q is attached to an inbound that is not in the file", c.Email)
			}
		}
		ids[c.ID], emails[c.Email], subs[c.SubID] = true, true, true
		snap.Clients = append(snap.Clients, c)
	}

	cascades := map[string]bool{}
	for _, c := range exp.Cascades {
		if c.ID == "" || cascades[c.ID] || !inbounds[c.ExitInboundID] || c.RelayUUID == "" || (c.EntryServerID != "" && !servers[c.EntryServerID]) || (c.ExitServerID != "" && !servers[c.ExitServerID]) {
			return nil, invalid("cascade %q in the file is damaged or refers to something that is not in it", c.Name)
		}
		cascades[c.ID] = true
		v := c.Cascade
		v.RelayUUID = c.RelayUUID
		snap.Cascades = append(snap.Cascades, v)
	}

	nodes := map[string]bool{}
	for _, n := range exp.ExternalNodes {
		if n.ID == "" || nodes[n.ID] {
			return nil, invalid("the file lists an external node twice or without an id")
		}
		nodes[n.ID] = true
		snap.ExternalNodes = append(snap.ExternalNodes, n)
	}
	for _, t := range exp.ServerTraffic {
		if servers[t.ServerID] && emails[t.Email] {
			snap.ServerTraffic = append(snap.ServerTraffic, t)
		}
	}
	snap.Samples = exp.Samples

	set := *exp.Settings
	if err := normalizeSettings(&set); err != nil {
		return nil, err
	}
	snap.Settings = set
	if sec := exp.Secrets; sec != nil {
		snap.Secrets = model.Secrets{APITokenHash: sec.APITokenHash, APITokenPrefix: sec.APITokenPrefix, APITokenCreatedAt: sec.APITokenCreatedAt, TelegramToken: sec.TelegramToken,
			RegRuPassword: sec.RegRuPassword, RegRuCert: sec.RegRuCert, RegRuKey: sec.RegRuKey}
	}
	return snap, nil
}

func (s *Service) importFull(ctx context.Context, exp Export, opts ImportOptions) (*ImportReport, error) {
	snap, err := snapshotFromExport(exp)
	if err != nil {
		return nil, err
	}
	rep := &ImportReport{
		Kind: ExportFull, DryRun: opts.DryRun, ExportedAt: &exp.CreatedAt, Items: []ImportItem{},
		Counts: map[string]int{
			"clients": len(snap.Clients), "inbounds": len(snap.Inbounds), "servers": len(snap.Servers),
			"externalNodes": len(snap.ExternalNodes), "admins": len(snap.Admins), "cascades": len(snap.Cascades),
		},
	}
	if opts.DryRun {
		return rep, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.Store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Store.ReplaceAll(ctx, snap); err != nil {
		if rerr := s.Store.ReplaceAll(ctx, old); rerr != nil {
			if s.Log != nil {
				s.Log.Error("restoring the previous database after a failed import also failed", "err", rerr)
			}
			return nil, fmt.Errorf("импорт не удался (%v), и прежние данные не удалось вернуть: %v", err, rerr)
		}
		return nil, fmt.Errorf("импорт не удался, прежние данные возвращены: %w", err)
	}
	rep.Created = len(snap.Clients)

	// Runtime state belonged to the old data: forget it so nothing is reported or reused by mistake.
	s.srvMu.Lock()
	s.srv = map[string]*serverRuntime{}
	s.srvMu.Unlock()
	s.evSeeded, s.evStatus, s.evServerUp = false, nil, nil
	s.ReloadTelegram(ctx)
	if s.Xray != nil {
		if err := s.applyLocked(ctx, true); err != nil {
			rep.Warning = wrapApply(err).Error()
		}
	}
	return rep, nil
}

// ReadLimit is the largest file ImportDB is given.
const ReadLimit = 64 << 20

// ReadAll reads at most ReadLimit bytes.
func ReadAll(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, ReadLimit+1))
	if err == nil && len(data) > ReadLimit {
		return nil, invalid("the file is larger than %d MB", ReadLimit>>20)
	}
	return data, err
}
