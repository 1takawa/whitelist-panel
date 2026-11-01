package panel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/xray"
)

// ---- API token ----

func TestAPITokenLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, _, st := newService(t)

	if info, _ := svc.APITokenInfo(ctx); info.Exists {
		t.Fatal("no token yet")
	}
	if svc.CheckAPIToken(ctx, "anyp_whatever") {
		t.Fatal("nothing is accepted before a token exists")
	}

	tok, info, err := svc.GenerateAPIToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "anyp_") || len(tok) != 45 || !info.Exists || info.Prefix != tok[:9] {
		t.Fatalf("token shape: %q %+v", tok, info)
	}
	sec, _ := st.GetSecrets(ctx)
	if strings.Contains(sec.APITokenHash, tok) || sec.APITokenHash == tok || len(sec.APITokenHash) != 64 {
		t.Fatalf("only a hash may be stored: %q", sec.APITokenHash)
	}
	if !svc.CheckAPIToken(ctx, tok) {
		t.Fatal("the issued token works")
	}
	for _, bad := range []string{"", tok + "x", tok[:len(tok)-1], strings.ToUpper(tok), "Bearer " + tok, strings.Repeat("a", 500)} {
		if svc.CheckAPIToken(ctx, bad) {
			t.Fatalf("%q must not pass", bad)
		}
	}

	tok2, _, _ := svc.GenerateAPIToken(ctx)
	if svc.CheckAPIToken(ctx, tok) || !svc.CheckAPIToken(ctx, tok2) {
		t.Fatal("generating a new token revokes the old one")
	}
	if err := svc.RevokeAPIToken(ctx); err != nil {
		t.Fatal(err)
	}
	if svc.CheckAPIToken(ctx, tok2) {
		t.Fatal("revoked")
	}
	if info, _ := svc.APITokenInfo(ctx); info.Exists || info.Prefix != "" {
		t.Fatalf("revoked token leaves no trace: %+v", info)
	}
}

func TestSettingsNeverExposeSecrets(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	tok, _, _ := svc.GenerateAPIToken(ctx)
	set, _ := svc.GetSettings(ctx)
	raw, _ := json.Marshal(set)
	if strings.Contains(string(raw), tok) || strings.Contains(strings.ToLower(string(raw)), "token") {
		t.Fatalf("settings JSON must not carry secrets: %s", raw)
	}
}

func TestSettingsValidationForTelegram(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	mut := func(f func(*model.Settings)) error {
		set, _ := svc.GetSettings(ctx)
		f(set)
		_, err := svc.PutSettings(ctx, *set)
		return err
	}
	for name, f := range map[string]func(*model.Settings){
		"admin id": func(s *model.Settings) { s.Telegram.AdminIDs = []int64{-5} },
	} {
		if err := mut(f); !isInvalid(err) {
			t.Errorf("%s must be rejected, got %v", name, err)
		}
	}
	if err := mut(func(s *model.Settings) { s.Telegram.AdminIDs = []int64{5, 5, 7} }); err != nil {
		t.Fatal(err)
	}
	set, _ := svc.GetSettings(ctx)
	if len(set.Telegram.AdminIDs) != 2 {
		t.Fatalf("duplicate IDs are dropped: %v", set.Telegram.AdminIDs)
	}
}

// ---- Telegram ----

const goodToken = "123456789:AAEhBOweik6ad7PNjkgsjkdl9aQ0123456789"

type fakeTG struct {
	mu         sync.Mutex
	messages   []string
	docs       int
	files      []string
	contents   [][]byte
	reject     bool
	rejectDocs bool
}

func (f *fakeTG) start(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": v}) }
		if f.reject || !strings.HasPrefix(r.URL.Path, "/bot"+goodToken+"/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Unauthorized", "error_code": 401})
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/bot"+goodToken+"/") {
		case "getMe":
			reply(map[string]any{"id": 1, "username": "panel_bot"})
		case "getUpdates":
			f.mu.Unlock()
			time.Sleep(30 * time.Millisecond)
			f.mu.Lock()
			reply([]any{})
		case "sendMessage":
			_ = r.ParseForm()
			f.messages = append(f.messages, r.Form.Get("chat_id")+"|"+r.Form.Get("text"))
			reply(map[string]any{"message_id": 1})
		case "sendDocument":
			if f.rejectDocs {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Forbidden", "error_code": 403})
				return
			}
			f.docs++
			_ = r.ParseMultipartForm(64 << 20)
			if file, hdr, err := r.FormFile("document"); err == nil {
				content, _ := io.ReadAll(file)
				f.files = append(f.files, hdr.Filename)
				f.contents = append(f.contents, content)
			}
			reply(map[string]any{"message_id": 2})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTG) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func tgSetup(t *testing.T) (*Service, *fakeXray, *fakeTG) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc, fx, _ := newService(t)
	fake := &fakeTG{}
	svc.TelegramBase = fake.start(t).URL
	svc.StartTelegram(ctx)
	return svc, fx, fake
}

func enableBot(t *testing.T, svc *Service) {
	t.Helper()
	if _, err := svc.SaveTelegram(context.Background(), TelegramSave{Enable: true, AdminIDs: []int64{42}, Token: goodToken}); err != nil {
		t.Fatal(err)
	}
}

func TestSaveTelegramValidatesAndStores(t *testing.T) {
	ctx := context.Background()
	svc, _, fake := tgSetup(t)

	if _, err := svc.SaveTelegram(ctx, TelegramSave{Token: "not a token"}); !isInvalid(err) {
		t.Fatalf("format is checked before any network call: %v", err)
	}
	if _, err := svc.SaveTelegram(ctx, TelegramSave{Token: "123456789:BBEhBOweik6ad7PNjkgsjkdl9aQ0123456789"}); !isInvalid(err) {
		t.Fatalf("Telegram says no: %v", err)
	}
	if st, _ := svc.TelegramStatus(ctx); st.TokenSet {
		t.Fatal("a rejected token is not stored")
	}
	if _, err := svc.SaveTelegram(ctx, TelegramSave{Enable: true, AdminIDs: []int64{42}}); !isInvalid(err) {
		t.Fatalf("enabling without any token: %v", err)
	}
	if _, err := svc.SaveTelegram(ctx, TelegramSave{Enable: true, Token: goodToken}); !isInvalid(err) {
		t.Fatalf("enabling without administrators: %v", err)
	}
	if _, err := svc.SaveTelegram(ctx, TelegramSave{AdminIDs: []int64{-1}}); !isInvalid(err) {
		t.Fatalf("bad administrator id: %v", err)
	}

	// Stored but switched off: the bot does not start.
	st, err := svc.SaveTelegram(ctx, TelegramSave{Token: "  " + goodToken + " ", AdminIDs: []int64{42}})
	if err != nil || !st.TokenSet || st.Username != "panel_bot" || st.Running || st.Enable {
		t.Fatalf("%+v %v", st, err)
	}
	set, _ := svc.GetSettings(ctx)
	if raw, _ := json.Marshal(set); strings.Contains(string(raw), goodToken) {
		t.Fatal("the bot token must never appear in the settings")
	}
	if err := svc.TelegramTest(ctx); !isInvalid(err) {
		t.Fatalf("test message without a running bot: %v", err)
	}

	// An empty token keeps the stored one.
	st, err = svc.SaveTelegram(ctx, TelegramSave{Enable: true, AdminIDs: []int64{42, 7}})
	if err != nil || !st.Running || !st.Enable || len(st.AdminIDs) != 2 {
		t.Fatalf("enable + stored token + admins starts the bot: %+v %v", st, err)
	}
	if err := svc.TelegramTest(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fake.texts(); len(got) != 2 || !strings.HasPrefix(got[0], "42|") || !strings.HasPrefix(got[1], "7|") {
		t.Fatalf("test message goes to every admin: %v", got)
	}

	st, _ = svc.SaveTelegram(ctx, TelegramSave{Enable: false, AdminIDs: []int64{42}})
	if st.Running || !st.TokenSet {
		t.Fatalf("switching off stops the bot but keeps the token: %+v", st)
	}
}

func TestExpiryDepletionAndXrayNotifications(t *testing.T) {
	ctx := context.Background()
	svc, fx, fake := tgSetup(t)
	enableBot(t, svc)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	in, _ := svc.CreateInbound(ctx, realityInbound())
	soon := now.Add(time.Hour)
	mk := func(email string, c model.Client) {
		c.Email, c.InboundIDs, c.Enable = email, []string{in.ID}, true
		if _, err := svc.CreateClient(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	mk("shortlived", model.Client{ExpiryAt: &soon})
	mk("capped", model.Client{TotalBytes: 100})
	mk("normal", model.Client{})

	if err := svc.PollTraffic(ctx); err != nil { // first poll only records the state
		t.Fatal(err)
	}
	svc.WaitNotifications()
	if got := fake.texts(); len(got) != 0 {
		t.Fatalf("the first poll must stay silent, a restart cannot repeat old news: %v", got)
	}

	now = now.Add(2 * time.Hour)
	fx.stats = map[string]xray.Traffic{"capped": {Up: 80, Down: 80}}
	_ = svc.PollTraffic(ctx)
	svc.WaitNotifications()
	got := strings.Join(fake.texts(), "\n")
	if !strings.Contains(got, "shortlived") || !strings.Contains(got, "capped") || strings.Contains(got, "normal") || len(fake.texts()) != 2 {
		t.Fatalf("one message per client that expired or ran out: %v", fake.texts())
	}
	_ = svc.PollTraffic(ctx)
	svc.WaitNotifications()
	if len(fake.texts()) != 2 {
		t.Fatal("the same state is not announced twice")
	}

	fx.mu.Lock()
	fx.running = false
	fx.mu.Unlock()
	_ = svc.PollTraffic(ctx)
	svc.WaitNotifications()
	if all := fake.texts(); len(all) != 3 || !strings.Contains(all[2], "Xray") {
		t.Fatalf("Xray stopping is reported: %v", all)
	}
}

func TestBotCommandsReadFromTheService(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.com"
	_, _ = svc.PutSettings(ctx, *set)
	in, _ := svc.CreateInbound(ctx, realityInbound())
	exp := now.Add(48 * time.Hour)
	c, _ := svc.CreateClient(ctx, model.Client{Email: "alice", Comment: "friend", Enable: true, TotalBytes: 1 << 30, ExpiryAt: &exp, InboundIDs: []string{in.ID}})
	_, _ = svc.CreateClient(ctx, model.Client{Email: "alina", Enable: true, InboundIDs: []string{in.ID}})
	h := tgHandler{svc}

	got := h.ClientInfo(ctx, "alice")
	for _, want := range []string{"alice", "активен", "friend", "https://panel.example.com/", c.SubID} {
		if !strings.Contains(got, want) {
			t.Errorf("client info lacks %q:\n%s", want, got)
		}
	}
	if got := h.ClientInfo(ctx, "ali"); !strings.Contains(got, "alina") || !strings.Contains(got, "alice") || !strings.Contains(got, "уточните") {
		t.Errorf("an ambiguous prefix lists the candidates: %s", got)
	}
	if got := h.ClientInfo(ctx, "zed"); !strings.Contains(got, "не найден") {
		t.Errorf("unknown client: %s", got)
	}
	if got := h.Stats(ctx); !strings.Contains(got, "Клиенты: всего 2") {
		t.Errorf("stats: %s", got)
	}
	if got := h.Online(ctx); !strings.Contains(got, "никто") {
		t.Errorf("online: %s", got)
	}
}

func TestANewConnectionGoesToEveryExistingClientAndTheSwitchHidesItFromAll(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	in1, _ := svc.CreateInbound(ctx, wsInbound(10401))
	a, _ := svc.CreateClient(ctx, model.Client{Email: "a", Enable: true, InboundIDs: []string{in1.ID}})
	b, _ := svc.CreateClient(ctx, model.Client{Email: "b", Enable: true})

	in2, err := svc.CreateInbound(ctx, wsInbound(10402))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []*model.Client{a, b} {
		got, _ := svc.Store.GetClient(ctx, c.ID)
		if !got.Uses(in2.ID) {
			t.Fatalf("%s must get the new connection", c.Email)
		}
	}
	// One that is only for later: created for nobody on purpose.
	in3, err := svc.CreateInboundFor(ctx, wsInbound(10403), false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store.GetClient(ctx, a.ID); got.Uses(in3.ID) {
		t.Fatal("CreateInboundFor(false) leaves the clients alone")
	}

	// The master switch: off, the connection is in nobody's subscription; on, it is back for everybody.
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.com"
	_, _ = svc.PutSettings(ctx, *set)
	count := func(c *model.Client) int {
		v, err := svc.ClientLinks(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, l := range v.Links {
			if l.Enabled && l.InboundID == in2.ID {
				n++
			}
		}
		return n
	}
	if count(a) != 1 || count(b) != 1 {
		t.Fatalf("both subscriptions carry the connection: %d %d", count(a), count(b))
	}
	off := *in2
	off.Enable = false
	if _, err := svc.UpdateInbound(ctx, in2.ID, off); err != nil {
		t.Fatal(err)
	}
	if count(a) != 0 || count(b) != 0 {
		t.Fatalf("switched off: nobody has it (%d %d)", count(a), count(b))
	}
	off.Enable = true
	if _, err := svc.UpdateInbound(ctx, in2.ID, off); err != nil {
		t.Fatal(err)
	}
	if count(a) != 1 || count(b) != 1 {
		t.Fatalf("switched on again: everybody has it back (%d %d)", count(a), count(b))
	}
}
