package panel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/store"
)

// seeded builds a service with an administrator, settings, an inbound, a server, an external node and two clients.
func seeded(t *testing.T) (*Service, *store.Snapshot) {
	t.Helper()
	ctx := context.Background()
	svc, _, st := newService(t)
	_ = st.PutAdmin(ctx, store.Admin{Username: "boss", PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA", CreatedAt: time.Now()})
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.com"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	in, err := svc.CreateInbound(ctx, realityInbound())
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	for _, c := range []model.Client{
		{Email: "alice", Enable: true, TotalBytes: 5 << 30, ExpiryAt: &exp, InboundIDs: []string{in.ID}},
		{Email: "bob", Enable: true, InboundIDs: []string{in.ID}},
	} {
		if _, err := svc.CreateClient(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.PutServer(ctx, model.Server{ID: "s1", Name: "DE", Address: "1.2.3.4", Port: 8765, Enable: false, Token: "agent-secret-token", SSHKey: "PRIVATE-KEY", CreatedAt: time.Now()})
	_ = st.PutExternalNode(ctx, model.ExternalNode{ID: "n1", Name: "ext", URI: "vless://x@h:1#n", Enable: true, CreatedAt: time.Now()})
	_ = st.PutSecrets(ctx, model.Secrets{APITokenHash: "hash", TelegramToken: goodToken})
	snap, _ := st.Snapshot(ctx)
	return svc, snap
}

func TestFullExportCarriesEverythingAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	svc, before := seeded(t)
	name, data, err := svc.ExportDB(ctx, ExportFull)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, "anypanel-full-") || !strings.HasSuffix(name, ".json") {
		t.Fatalf("file name: %s", name)
	}
	for _, want := range []string{"boss", "$argon2id$", "agent-secret-token", "PRIVATE-KEY", goodToken, "alice", "panel.example.com", "vless://x@h:1#n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("a full export must contain %q", want)
		}
	}

	// Wipe the data, then bring it back from the file.
	other, _, ost := newService(t)
	_ = ost.PutAdmin(ctx, store.Admin{Username: "someone-else", PasswordHash: "x"})
	_ = ost.PutClient(ctx, model.Client{ID: "zz", Email: "stale", SubID: "stalestale", Enable: true})
	rep, err := other.ImportDB(ctx, data, ImportOptions{DryRun: true})
	if err != nil || rep.Kind != ExportFull || rep.Counts["clients"] != 2 {
		t.Fatalf("dry run: %+v %v", rep, err)
	}
	if cls, _ := ost.ListClients(ctx); len(cls) != 1 {
		t.Fatal("a dry run changes nothing")
	}
	if _, err := other.ImportDB(ctx, data, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	after, _ := ost.Snapshot(ctx)
	if len(after.Admins) != 1 || after.Admins[0].Username != "boss" {
		t.Fatalf("login comes from the file: %+v", after.Admins)
	}
	if len(after.Clients) != 2 || len(after.Inbounds) != 1 || len(after.Servers) != 1 || len(after.ExternalNodes) != 1 {
		t.Fatalf("counts: %+v", after)
	}
	if after.Servers[0].Token != "agent-secret-token" || after.Servers[0].SSHKey != "PRIVATE-KEY" {
		t.Fatalf("server keys survive: %+v", after.Servers[0])
	}
	if after.Secrets.TelegramToken != goodToken || after.Secrets.APITokenHash != "hash" {
		t.Fatalf("secrets survive: %+v", after.Secrets)
	}
	if after.Settings.PanelDomain != "panel.example.com" {
		t.Fatalf("settings survive: %+v", after.Settings)
	}
	byName := func(cs []model.Client, n string) model.Client {
		for _, c := range cs {
			if c.Email == n {
				return c
			}
		}
		t.Fatalf("no client %s", n)
		return model.Client{}
	}
	a, b := byName(before.Clients, "alice"), byName(after.Clients, "alice")
	if a.ID != b.ID || a.UUID != b.UUID || a.SubID != b.SubID || a.Password != b.Password || !a.ExpiryAt.Equal(*b.ExpiryAt) || a.TotalBytes != b.TotalBytes {
		t.Fatalf("a client keeps its credentials and limits:\n%+v\n%+v", a, b)
	}
}

func TestUsersExportHasOnlyClients(t *testing.T) {
	ctx := context.Background()
	svc, _ := seeded(t)
	_, data, err := svc.ExportDB(ctx, ExportUsers)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(data, &m)
	for _, forbidden := range []string{"admins", "settings", "secrets", "servers", "inbounds", "externalNodes"} {
		if _, ok := m[forbidden]; ok {
			t.Errorf("users export must not contain %q", forbidden)
		}
	}
	for _, secret := range []string{"agent-secret-token", "PRIVATE-KEY", "$argon2id$", goodToken} {
		if strings.Contains(string(data), secret) {
			t.Errorf("users export leaks %q", secret)
		}
	}
	if _, _, err := svc.ExportDB(ctx, "everything"); !isInvalid(err) {
		t.Fatalf("unknown kind: %v", err)
	}
}

func TestUsersImportAddsMissingClientsOnly(t *testing.T) {
	ctx := context.Background()
	src, _ := seeded(t)
	_, data, _ := src.ExportDB(ctx, ExportUsers)
	srcCls, _ := src.Store.ListClients(ctx)
	srcBob := srcCls[0]
	for _, c := range srcCls {
		if c.Email == "bob" {
			srcBob = c
		}
	}

	dst, _, dstStore := newService(t)
	in, _ := dst.CreateInbound(ctx, realityInbound())
	_, _ = dst.CreateClient(ctx, model.Client{Email: "ALICE", Enable: true, InboundIDs: []string{in.ID}}) // the same name, any case

	rep, err := dst.ImportDB(ctx, data, ImportOptions{})
	if err != nil || rep.Kind != ExportUsers || rep.Created != 1 || rep.Skipped != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	items := map[string]ImportItem{}
	for _, it := range rep.Items {
		items[it.Name] = it
	}
	if len(rep.Items) != 2 || items["alice"].Action != "skip" || !strings.Contains(items["alice"].Reason, "уже есть") {
		t.Fatalf("report: %+v", rep.Items)
	}
	cls, _ := dstStore.ListClients(ctx)
	var bob *model.Client
	for i := range cls {
		if cls[i].Email == "bob" {
			bob = &cls[i]
		}
	}
	if bob == nil || bob.UUID != srcBob.UUID || bob.SubID != srcBob.SubID || bob.Password != srcBob.Password {
		t.Fatalf("the imported client keeps the credentials its links were made with: %+v", bob)
	}
	if len(bob.InboundIDs) != 1 || bob.InboundIDs[0] != in.ID {
		t.Fatalf("inbounds that do not exist here are replaced by the enabled ones: %v", bob.InboundIDs)
	}
	if len(items["bob"].Notes) == 0 {
		t.Fatal("the replacement is reported")
	}

	// Running it again adds nothing.
	rep, _ = dst.ImportDB(ctx, data, ImportOptions{})
	if rep.Created != 0 || rep.Skipped != 2 {
		t.Fatalf("second run: %+v", rep)
	}
}

func TestUsersImportDryRunDoesNotSave(t *testing.T) {
	ctx := context.Background()
	src, _ := seeded(t)
	_, data, _ := src.ExportDB(ctx, ExportUsers)
	dst, _, dstStore := newService(t)
	rep, err := dst.ImportDB(ctx, data, ImportOptions{DryRun: true})
	if err != nil || !rep.DryRun || rep.Created != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	if cls, _ := dstStore.ListClients(ctx); len(cls) != 0 {
		t.Fatal("nothing is saved")
	}
}

func TestLegacyFileIsRecognised(t *testing.T) {
	ctx := context.Background()
	dst, _, dstStore := newService(t)
	in, _ := dst.CreateInbound(ctx, realityInbound())
	_ = in
	legacy := `[{"_id":"olduser","password":"p","max_download_bytes":1073741824,"expiration_days":30,"account_creation_date":"2026-10-01","blocked":false,"unlimited_user":false,"upload_bytes":1,"download_bytes":2,"subscription_token":"` + strings.Repeat("a", 40) + `"}]`
	rep, err := dst.ImportDB(ctx, []byte(legacy), ImportOptions{})
	if err != nil || rep.Kind != "legacy" || rep.Created != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if cls, _ := dstStore.ListClients(ctx); len(cls) != 1 || cls[0].Email != "olduser" {
		t.Fatalf("%+v", cls)
	}
	// JSON lines (what mongoexport writes without --jsonArray) also work.
	if rep, err := dst.ImportDB(ctx, []byte(`{"_id":"second","upload_bytes":0,"download_bytes":0}`+"\n"), ImportOptions{}); err != nil || rep.Kind != "legacy" || rep.Created != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestImportRejectsBadFiles(t *testing.T) {
	ctx := context.Background()
	svc, _ := seeded(t)
	_, full, _ := svc.ExportDB(ctx, ExportFull)

	mutate := func(f func(m map[string]any)) []byte {
		var m map[string]any
		_ = json.Unmarshal(full, &m)
		f(m)
		out, _ := json.Marshal(m)
		return out
	}
	cases := map[string][]byte{
		"empty":            []byte("  "),
		"not json":         []byte("hello"),
		"unknown format":   []byte(`{"format":"other"}`),
		"newer version":    mutate(func(m map[string]any) { m["version"] = 99 }),
		"unknown kind":     mutate(func(m map[string]any) { m["kind"] = "half" }),
		"no admin":         mutate(func(m map[string]any) { delete(m, "admins") }),
		"broken hash":      mutate(func(m map[string]any) { m["admins"] = []any{map[string]any{"username": "x", "passwordHash": "plain"}} }),
		"no settings":      mutate(func(m map[string]any) { delete(m, "settings") }),
		"orphan client":    mutate(func(m map[string]any) { m["inbounds"] = []any{} }),
		"duplicate client": mutate(func(m map[string]any) { cl := m["clients"].([]any); m["clients"] = append(cl, cl[0]) }),
	}
	dst, _, dstStore := newService(t)
	_ = dstStore.PutAdmin(ctx, store.Admin{Username: "keep", PasswordHash: "x"})
	for name, data := range cases {
		if _, err := dst.ImportDB(ctx, data, ImportOptions{}); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
	if a, _ := dstStore.ListAdmins(ctx); len(a) != 1 || a[0].Username != "keep" {
		t.Fatal("a rejected file leaves the data untouched")
	}
}

func TestDailyCopyGoesToTelegramOncePerMoscowDay(t *testing.T) {
	ctx := context.Background()
	svc, _, fake := tgSetup(t)
	enableBot(t, svc)
	now := time.Date(2026, 10, 31, 20, 30, 0, 0, time.UTC) // 23:30 in Moscow
	svc.Now = func() time.Time { return now }

	if sent, err := svc.BackupTick(ctx); !sent || err != nil {
		t.Fatalf("the first tick sends the copy: %v %v", sent, err)
	}
	fake.mu.Lock()
	if len(fake.files) != 1 || !strings.HasPrefix(fake.files[0], "anypanel-full-") || !strings.Contains(string(fake.contents[0]), `"kind":"full"`) {
		t.Fatalf("a full export reaches the administrator: %v", fake.files)
	}
	fake.mu.Unlock()

	now = now.Add(20 * time.Minute) // 23:50, still the same Moscow day
	if sent, _ := svc.BackupTick(ctx); sent {
		t.Fatal("once per day")
	}
	now = now.Add(15 * time.Minute) // 00:05 of the next Moscow day (UTC is still the 31st)
	if sent, _ := svc.BackupTick(ctx); !sent {
		t.Fatal("the new Moscow day sends again")
	}
	if sent, _ := svc.BackupTick(ctx); sent {
		t.Fatal("and only once")
	}
	now = now.Add(5 * 24 * time.Hour) // the panel was away: one copy on return
	if sent, _ := svc.BackupTick(ctx); !sent {
		t.Fatal("catches up")
	}
	fake.mu.Lock()
	n := len(fake.files)
	fake.mu.Unlock()
	if n != 3 {
		t.Fatalf("three copies in total, got %d", n)
	}
}

func TestDailyCopyNeedsARunningBotAndRetriesAfterFailure(t *testing.T) {
	ctx := context.Background()
	svc, _, fake := tgSetup(t)
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	if sent, err := svc.BackupTick(ctx); sent || err != nil {
		t.Fatalf("no bot, nothing to do: %v %v", sent, err)
	}
	enableBot(t, svc)
	fake.mu.Lock()
	fake.rejectDocs = true
	fake.mu.Unlock()
	if sent, err := svc.BackupTick(ctx); sent || err == nil {
		t.Fatalf("a failed delivery is an error and is not marked as sent: %v %v", sent, err)
	}
	if sent, err := svc.BackupTick(ctx); sent || err != nil {
		t.Fatal("no retry before the pause is over")
	}
	fake.mu.Lock()
	fake.rejectDocs = false
	fake.mu.Unlock()
	now = now.Add(backupRetry + time.Minute)
	if sent, err := svc.BackupTick(ctx); !sent || err != nil {
		t.Fatalf("retry succeeds: %v %v", sent, err)
	}
}

func TestBotManagesClients(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	h := tgHandler{svc}

	if got := h.AddClient(ctx, "alice", 1, 0); !strings.Contains(got, "подключени") {
		t.Fatalf("no inbound yet: %s", got)
	}
	in, _ := svc.CreateInbound(ctx, realityInbound())
	if got := h.AddClient(ctx, "alice", 10, 30); !strings.Contains(got, "создан") || !strings.Contains(got, "alice") {
		t.Fatalf("add: %s", got)
	}
	c := mustClient(t, svc, "alice")
	if c.TotalBytes != 10<<30 || c.ExpiryAt == nil || !c.ExpiryAt.Equal(now.AddDate(0, 0, 30)) || len(c.InboundIDs) != 1 || c.InboundIDs[0] != in.ID || !c.Enable {
		t.Fatalf("a new client gets the limit, the term and every enabled inbound: %+v", c)
	}
	if got := h.AddClient(ctx, "alice", 0, 0); !strings.Contains(got, "Не получилось") {
		t.Fatalf("duplicate name: %s", got)
	}
	if got := h.AddClient(ctx, "bad name!", 0, 0); !strings.Contains(got, "Не получилось") {
		t.Fatalf("invalid name: %s", got)
	}

	h.SetEnabled(ctx, "ALICE", false)
	if mustClient(t, svc, "alice").Enable {
		t.Fatal("/off")
	}
	h.SetEnabled(ctx, "alice", true)
	if !mustClient(t, svc, "alice").Enable {
		t.Fatal("/on")
	}
	if got := h.SetEnabled(ctx, "ali", false); !strings.Contains(got, "не найден") {
		t.Fatalf("changing commands need the full name, not a prefix: %s", got)
	}

	_ = svc.Store.AddTraffic(ctx, map[string]store.Traffic{"alice": {Up: 5, Down: 6}})
	h.ResetTraffic(ctx, "alice")
	if c := mustClient(t, svc, "alice"); c.Flow != model.FlowVision {
		t.Fatalf("a client made from the bot gets xtls-rprx-vision: %+v", c)
	}
	if c := mustClient(t, svc, "alice"); c.Up != 0 || c.Down != 0 {
		t.Fatalf("/reset: %+v", c)
	}

	h.Extend(ctx, "alice", 10) // from the existing end
	if got := mustClient(t, svc, "alice").ExpiryAt; !got.Equal(now.AddDate(0, 0, 40)) {
		t.Fatalf("/extend adds to the end of a running subscription: %v", got)
	}
	now = now.AddDate(0, 0, 100) // the subscription is over: counted from today
	h.Extend(ctx, "alice", 10)
	if got := mustClient(t, svc, "alice").ExpiryAt; !got.Equal(now.AddDate(0, 0, 10)) {
		t.Fatalf("/extend of an ended subscription counts from now: %v", got)
	}

	if got := h.Users(ctx); !strings.Contains(got, "alice") {
		t.Fatalf("/users: %s", got)
	}
	if got := h.DeleteClient(ctx, "alice"); !strings.Contains(got, "удалён") {
		t.Fatal(got)
	}
	if cls, _ := svc.Store.ListClients(ctx); len(cls) != 0 {
		t.Fatal("/del")
	}
	if got := h.Users(ctx); !strings.Contains(got, "/add") {
		t.Fatalf("empty list hints at /add: %s", got)
	}
	name, data, caption, err := h.Backup(ctx, ExportUsers)
	if err != nil || !strings.Contains(name, "users") || len(data) == 0 || caption == "" {
		t.Fatalf("%s %d %q %v", name, len(data), caption, err)
	}
}

func mustClient(t *testing.T, svc *Service, name string) model.Client {
	t.Helper()
	cls, _ := svc.Store.ListClients(context.Background())
	for _, c := range cls {
		if c.Email == name {
			return c
		}
	}
	t.Fatalf("client %q not found", name)
	return model.Client{}
}
