package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const token = "123456:ABCdefGhIJKlmNoPQRsTUVwxyz0123456789"

// fakeAPI is a minimal Telegram Bot API.
type fakeAPI struct {
	mu       sync.Mutex
	updates  []Update
	messages []map[string]string // sendMessage calls
	docs     []map[string]string // sendDocument calls: chat_id, caption, filename, content
	fail     string              // when set, every call fails with this description
}

func (f *fakeAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Unauthorized", "error_code": 401})
			return
		}
		if f.fail != "" {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": f.fail + " " + token, "error_code": 400})
			return
		}
		method := strings.TrimPrefix(r.URL.Path, "/bot"+token+"/")
		ok := func(v any) { json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": v}) }
		switch method {
		case "getMe":
			ok(User{ID: 1, Username: "my_bot"})
		case "getUpdates":
			_ = r.ParseForm()
			ups := f.updates
			f.updates = nil
			if ups == nil {
				ups = []Update{}
			}
			ok(ups)
		case "sendMessage":
			_ = r.ParseForm()
			f.messages = append(f.messages, map[string]string{"chat_id": r.Form.Get("chat_id"), "text": r.Form.Get("text")})
			ok(map[string]any{"message_id": 1})
		case "sendDocument":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			file, hdr, err := r.FormFile("document")
			if err != nil {
				t.Errorf("document part: %v", err)
				return
			}
			content, _ := io.ReadAll(file)
			f.docs = append(f.docs, map[string]string{"chat_id": r.FormValue("chat_id"), "caption": r.FormValue("caption"), "filename": hdr.Filename, "content": string(content)})
			ok(map[string]any{"message_id": 2})
		default:
			t.Errorf("unexpected method %s", method)
		}
	})
}

func (f *fakeAPI) sent() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.messages...)
}

// fakeHandler records the calls of the management commands in calls.
type fakeHandler struct {
	backupData []byte
	calls      *[]string
}

func (h fakeHandler) rec(s string) string {
	if h.calls != nil {
		*h.calls = append(*h.calls, s)
	}
	return "OK " + s
}

func (fakeHandler) Stats(context.Context) string  { return "STATS" }
func (fakeHandler) Online(context.Context) string { return "ONLINE" }
func (fakeHandler) ClientInfo(_ context.Context, name string) string {
	return "INFO " + name
}
func (h fakeHandler) Users(context.Context) string { return h.rec("users") }
func (h fakeHandler) AddClient(_ context.Context, name string, gb float64, days int) string {
	return h.rec(fmt.Sprintf("add %s %g %d", name, gb, days))
}
func (h fakeHandler) SetEnabled(_ context.Context, name string, on bool) string {
	return h.rec(fmt.Sprintf("enable %s %v", name, on))
}
func (h fakeHandler) DeleteClient(_ context.Context, name string) string { return h.rec("del " + name) }
func (h fakeHandler) ResetTraffic(_ context.Context, name string) string {
	return h.rec("reset " + name)
}
func (h fakeHandler) Extend(_ context.Context, name string, days int) string {
	return h.rec(fmt.Sprintf("extend %s %d", name, days))
}
func (h fakeHandler) RestartXray(context.Context) string { return h.rec("restart") }
func (h fakeHandler) Backup(_ context.Context, kind string) (string, []byte, string, error) {
	if h.backupData == nil {
		return "", nil, "", errors.New("no data")
	}
	return "copy-" + kind + ".json", h.backupData, "caption", nil
}

func setup(t *testing.T, admins ...int64) (*Bot, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{}
	srv := httptest.NewServer(api.handler(t))
	t.Cleanup(srv.Close)
	b := &Bot{
		Client:  &Client{Token: token, BaseURL: srv.URL},
		Admins:  func() []int64 { return admins },
		Handler: fakeHandler{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return b, api
}

func msg(from, chat int64, typ, text string) *Message {
	return &Message{From: &User{ID: from}, Chat: Chat{ID: chat, Type: typ}, Text: text}
}

func TestAdminCommands(t *testing.T) {
	b, api := setup(t, 42)
	ctx := context.Background()
	for text, want := range map[string]string{
		"/stats": "STATS", "/online": "ONLINE", "/client alice": "INFO alice", "/stats@my_bot": "STATS",
		"/start": "Команды администратора", "/help": "Команды администратора", "/client": "Укажите имя", "/nope": "Неизвестная команда",
	} {
		b.handle(ctx, *msg(42, 42, "private", text))
		got := api.sent()
		if len(got) == 0 || !strings.Contains(got[len(got)-1]["text"], want) || got[len(got)-1]["chat_id"] != "42" {
			t.Errorf("%s: want a reply containing %q, got %v", text, want, got)
		}
	}
}

func TestStrangersAreIgnored(t *testing.T) {
	b, api := setup(t, 42)
	ctx := context.Background()
	b.handle(ctx, *msg(7, 7, "private", "/stats"))                              // not an admin
	b.handle(ctx, *msg(42, -100, "group", "/stats"))                            // admin, but in a group chat
	b.handle(ctx, *msg(42, 7, "private", "/stats"))                             // chat id differs from the sender
	b.handle(ctx, Message{Chat: Chat{ID: 42, Type: "private"}, Text: "/stats"}) // no sender
	if n := len(api.sent()); n != 0 {
		t.Fatalf("only administrators in a private chat may use the bot, but %d replies were sent", n)
	}
}

func TestBackupCommandSendsTheFile(t *testing.T) {
	b, api := setup(t, 42)
	b.Handler = fakeHandler{backupData: []byte("DATA")}
	b.handle(context.Background(), *msg(42, 42, "private", "/backup users"))
	api.mu.Lock()
	if len(api.docs) != 1 || api.docs[0]["content"] != "DATA" || api.docs[0]["filename"] != "copy-users.json" || api.docs[0]["caption"] != "caption" || api.docs[0]["chat_id"] != "42" {
		t.Fatalf("the file must be uploaded to the requesting admin: %v", api.docs)
	}
	api.mu.Unlock()
	b.handle(context.Background(), *msg(42, 42, "private", "/backup"))
	api.mu.Lock()
	if len(api.docs) != 2 || api.docs[1]["filename"] != "copy-full.json" {
		t.Fatalf("a plain /backup is the full copy: %v", api.docs)
	}
	api.mu.Unlock()

	b.Handler = fakeHandler{}
	b.handle(context.Background(), *msg(42, 42, "private", "/backup"))
	if got := api.sent(); !strings.Contains(got[len(got)-1]["text"], "no data") {
		t.Fatalf("a failed backup is reported: %v", got)
	}
	b.handle(context.Background(), *msg(42, 42, "private", "/backup everything"))
	if got := api.sent(); !strings.Contains(got[len(got)-1]["text"], "/backup users") {
		t.Fatalf("an unknown backup kind gets the usage: %v", got)
	}
}

func TestNotifyFileCountsDeliveries(t *testing.T) {
	b, api := setup(t, 1, 2)
	sent, err := b.NotifyFile(context.Background(), "x.json", []byte("D"), "c")
	if err != nil || sent != 2 {
		t.Fatalf("sent=%d err=%v", sent, err)
	}
	api.mu.Lock()
	api.fail = "Forbidden: bot can't initiate conversation"
	api.mu.Unlock()
	if sent, err := b.NotifyFile(context.Background(), "x.json", []byte("D"), "c"); sent != 0 || err == nil {
		t.Fatalf("nobody reached must be an error: sent=%d err=%v", sent, err)
	}
}

func TestManagementCommands(t *testing.T) {
	b, api := setup(t, 42)
	var calls []string
	b.Handler = fakeHandler{calls: &calls}
	ctx := context.Background()
	for _, text := range []string{
		"/users", "/add alice", "/add bob 50 30", "/add carol 0,5", "/on alice", "/off alice", "/reset alice", "/extend alice 30",
		"/del alice yes", "/del alice", "/restart",
		"/add", "/add a b c d", "/add alice x", "/add alice 1 -3", "/on", "/extend alice", "/extend alice 0", "/del",
	} {
		b.handle(ctx, *msg(42, 42, "private", text))
	}
	want := []string{
		"users", "add alice 0 0", "add bob 50 30", "add carol 0.5 0", "enable alice true", "enable alice false", "reset alice", "extend alice 30",
		"del alice", "restart",
	}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("handler calls:\n got %v\nwant %v", calls, want)
	}
	if n := len(api.sent()); n != 19 {
		t.Fatalf("every command is answered, got %d replies", n)
	}
	last := api.sent()
	if got := last[9]["text"]; !strings.Contains(got, "/del alice yes") {
		t.Fatalf("deleting needs an explicit yes: %q", got)
	}
}

func TestNotifyReachesEveryAdminAndReportsErrors(t *testing.T) {
	b, api := setup(t, 1, 2, 3)
	if err := b.Notify(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	got := api.sent()
	if len(got) != 3 || got[0]["chat_id"] != "1" || got[2]["chat_id"] != "3" || got[1]["text"] != "hello" {
		t.Fatalf("one message per admin: %v", got)
	}
	api.mu.Lock()
	api.fail = "Forbidden: bot was blocked"
	api.mu.Unlock()
	err := b.Notify(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "bot was blocked") {
		t.Fatalf("API errors surface: %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("the bot token must never appear in an error: %v", err)
	}
}

func TestLongMessagesAreTrimmed(t *testing.T) {
	b, api := setup(t, 1)
	_ = b.Notify(context.Background(), strings.Repeat("я", 5000))
	if n := len([]rune(api.sent()[0]["text"])); n > 4001 {
		t.Fatalf("Telegram rejects messages over 4096 characters, sent %d", n)
	}
}

func TestGetMeValidatesTheToken(t *testing.T) {
	b, _ := setup(t, 1)
	u, err := b.Client.GetMe(context.Background())
	if err != nil || u.Username != "my_bot" {
		t.Fatalf("%v %+v", u, err)
	}
	bad := &Client{Token: "999:wrong", BaseURL: b.Client.BaseURL}
	if _, err := bad.GetMe(context.Background()); err == nil || strings.Contains(err.Error(), "999:wrong") {
		t.Fatalf("a wrong token fails without echoing it: %v", err)
	}
}

func TestRunPollsAnswersAndStops(t *testing.T) {
	b, api := setup(t, 42)
	b.PollSeconds = 1
	api.updates = []Update{{UpdateID: 10, Message: msg(42, 42, "private", "/stats")}, {UpdateID: 11, Message: msg(9, 9, "private", "/stats")}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for len(api.sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run must return when the context is cancelled")
	}
	got := api.sent()
	if len(got) != 1 || got[0]["chat_id"] != "42" || got[0]["text"] != "STATS" {
		t.Fatalf("the admin is answered once, the stranger not at all: %v", got)
	}
}
