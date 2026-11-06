// Package telegram is a small Telegram Bot API client and an admin bot built on it. It uses long polling, so the
// panel needs no public webhook address.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBase = "https://api.telegram.org"

// Client talks to the Bot API.
type Client struct {
	Token   string
	BaseURL string // default https://api.telegram.org (replaced in tests)
	HTTP    *http.Client
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultBase
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 70 * time.Second}
}

// scrub removes the bot token from text that may end up in logs or API responses.
func (c *Client) scrub(s string) string {
	if c.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.Token, "***")
}

func (c *Client) do(ctx context.Context, method string, req *http.Request, out any) error {
	res, err := c.http().Do(req)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	var r apiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("telegram %s: unexpected response (HTTP %d)", method, res.StatusCode)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %s (code %d)", method, c.scrub(r.Description), r.ErrorCode)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (c *Client) endpoint(method string) string { return c.base() + "/bot" + c.Token + "/" + method }

func (c *Client) post(ctx context.Context, method string, params url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), strings.NewReader(params.Encode()))
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(ctx, method, req, out)
}

// GetMe returns the bot's own account; it is also the cheapest way to validate a token.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.post(ctx, "getMe", url.Values{}, &u)
	return u, err
}

// SendMessage sends plain text (no parse mode, so user-provided names can never break the message).
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	if len([]rune(text)) > 4000 {
		text = string([]rune(text)[:4000]) + "…"
	}
	return c.post(ctx, "sendMessage", url.Values{"chat_id": {strconv.FormatInt(chatID, 10)}, "text": {text}, "disable_web_page_preview": {"true"}}, nil)
}

// SendDocument uploads a file with an optional caption.
func (c *Client) SendDocument(ctx context.Context, chatID int64, filename string, r io.Reader, caption string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	part, err := w.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, r); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("sendDocument"), &buf)
	if err != nil {
		return errors.New(c.scrub(err.Error()))
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(ctx, "sendDocument", req, nil)
}

// GetUpdates long-polls for new messages after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var ups []Update
	err := c.post(ctx, "getUpdates", url.Values{
		"offset": {strconv.FormatInt(offset, 10)}, "timeout": {strconv.Itoa(timeoutSec)}, "allowed_updates": {`["message"]`},
	}, &ups)
	return ups, err
}

// Handler answers the bot's commands. Every method returns the text to send back.
type Handler interface {
	Stats(ctx context.Context) string
	Online(ctx context.Context) string
	ClientInfo(ctx context.Context, name string) string
	Users(ctx context.Context) string
	// AddClient creates a client attached to every enabled inbound; gb and days of 0 mean unlimited.
	AddClient(ctx context.Context, name string, gb float64, days int) string
	SetEnabled(ctx context.Context, name string, enable bool) string
	DeleteClient(ctx context.Context, name string) string
	ResetTraffic(ctx context.Context, name string) string
	// Extend prolongs the subscription by days (counted from now when it has already ended or has no end).
	Extend(ctx context.Context, name string, days int) string
	RestartXray(ctx context.Context) string
	// Backup exports the database: kind is "full" or "users".
	Backup(ctx context.Context, kind string) (filename string, data []byte, caption string, err error)
}

// Bot answers commands from administrators and sends them notifications.
type Bot struct {
	Client  *Client
	Admins  func() []int64
	Handler Handler
	Log     *slog.Logger
	// PollSeconds is the long-poll timeout (shortened in tests).
	PollSeconds int
}

const help = `Команды администратора:
/stats — состояние сервера и клиентов
/online — кто сейчас онлайн
/users — список клиентов
/client имя — данные клиента и ссылка подписки
/add имя [ГБ] [дней] — создать клиента (без чисел — без лимитов)
/on имя, /off имя — включить, выключить
/extend имя дней — продлить подписку
/reset имя — обнулить трафик
/del имя yes — удалить клиента
/restart — перезапустить Xray
/backup — полная копия базы файлом
/backup users — только пользователи
/help — эта справка`

func (b *Bot) isAdmin(id int64) bool {
	for _, a := range b.Admins() {
		if a == id {
			return true
		}
	}
	return false
}

// Run polls for updates until ctx is cancelled, backing off after errors.
func (b *Bot) Run(ctx context.Context) {
	poll := b.PollSeconds
	if poll == 0 {
		poll = 30
	}
	var offset int64
	delay := time.Second
	for ctx.Err() == nil {
		ups, err := b.Client.GetUpdates(ctx, offset, poll)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.Log.Warn("telegram poll failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			if delay < time.Minute {
				delay *= 2
			}
			continue
		}
		delay = time.Second
		for _, u := range ups {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message != nil {
				b.handle(ctx, *u.Message)
			}
		}
	}
}

func (b *Bot) handle(ctx context.Context, m Message) {
	// Only private chats with a listed administrator are answered; everyone else is silently ignored.
	if m.From == nil || m.Chat.Type != "private" || m.Chat.ID != m.From.ID || !b.isAdmin(m.From.ID) {
		return
	}
	cmd, arg, _ := strings.Cut(strings.TrimSpace(m.Text), " ")
	cmd, _, _ = strings.Cut(cmd, "@") // /stats@MyBot
	arg = strings.TrimSpace(arg)

	var reply string
	switch cmd {
	case "/start", "/help":
		reply = help
	case "/stats":
		reply = b.Handler.Stats(ctx)
	case "/online":
		reply = b.Handler.Online(ctx)
	case "/client":
		if arg == "" {
			reply = "Укажите имя клиента: /client имя"
		} else {
			reply = b.Handler.ClientInfo(ctx, arg)
		}
	case "/users":
		reply = b.Handler.Users(ctx)
	case "/add":
		reply = b.add(ctx, arg)
	case "/on", "/off":
		if arg == "" {
			reply = "Укажите имя клиента: " + cmd + " имя"
		} else {
			reply = b.Handler.SetEnabled(ctx, arg, cmd == "/on")
		}
	case "/reset":
		if arg == "" {
			reply = "Укажите имя клиента: /reset имя"
		} else {
			reply = b.Handler.ResetTraffic(ctx, arg)
		}
	case "/extend":
		reply = b.extend(ctx, arg)
	case "/del":
		reply = b.del(ctx, arg)
	case "/restart":
		reply = b.Handler.RestartXray(ctx)
	case "/backup":
		kind := "full"
		switch strings.ToLower(arg) {
		case "", "full":
		case "users":
			kind = "users"
		default:
			reply = "Используйте /backup или /backup users"
		}
		if reply != "" {
			break
		}
		name, data, caption, err := b.Handler.Backup(ctx, kind)
		if err != nil {
			reply = "Не удалось сделать резервную копию: " + err.Error()
			break
		}
		if err := b.Client.SendDocument(ctx, m.From.ID, name, bytes.NewReader(data), caption); err != nil {
			reply = "Копия создана, но отправить файл не удалось: " + err.Error()
			break
		}
		return
	default:
		reply = "Неизвестная команда. /help — список команд."
	}
	if err := b.Client.SendMessage(ctx, m.Chat.ID, reply); err != nil {
		b.Log.Warn("telegram reply failed", "err", err)
	}
}

// Notify sends text to every administrator and reports the first error.
func (b *Bot) Notify(ctx context.Context, text string) error {
	_, err := b.NotifyCount(ctx, text)
	return err
}

// NotifyCount is Notify that also says how many administrators received the text.
func (b *Bot) NotifyCount(ctx context.Context, text string) (int, error) {
	var first error
	sent := 0
	for _, id := range b.Admins() {
		if err := b.Client.SendMessage(ctx, id, text); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		sent++
	}
	return sent, first
}

// maxUpload is what the Bot API accepts for one file.
const maxUpload = 49 << 20

// NotifyFile sends a file to every administrator. It returns how many received it and the first error, so the
// caller can tell "nobody got it" from "one administrator never pressed /start".
func (b *Bot) NotifyFile(ctx context.Context, filename string, data []byte, caption string) (int, error) {
	if len(data) > maxUpload {
		return 0, fmt.Errorf("the file is %d MB, more than Telegram accepts (50 MB)", len(data)>>20)
	}
	var first error
	sent := 0
	for _, id := range b.Admins() {
		if err := b.Client.SendDocument(ctx, id, filename, bytes.NewReader(data), caption); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		sent++
	}
	return sent, first
}

func (b *Bot) add(ctx context.Context, arg string) string {
	f := strings.Fields(arg)
	if len(f) == 0 || len(f) > 3 {
		return "Формат: /add имя [ГБ] [дней]\nНапример: /add alice 50 30"
	}
	var gb float64
	var days int
	if len(f) > 1 {
		v, err := strconv.ParseFloat(strings.Replace(f[1], ",", ".", 1), 64)
		if err != nil || v < 0 || v > 1e6 {
			return "Объём в ГБ — число от 0 (без лимита)."
		}
		gb = v
	}
	if len(f) > 2 {
		v, err := strconv.Atoi(f[2])
		if err != nil || v < 0 || v > 36500 {
			return "Срок в днях — целое число от 0 (без срока)."
		}
		days = v
	}
	return b.Handler.AddClient(ctx, f[0], gb, days)
}

func (b *Bot) extend(ctx context.Context, arg string) string {
	f := strings.Fields(arg)
	if len(f) != 2 {
		return "Формат: /extend имя дней"
	}
	days, err := strconv.Atoi(f[1])
	if err != nil || days < 1 || days > 36500 {
		return "Число дней — целое от 1 до 36500."
	}
	return b.Handler.Extend(ctx, f[0], days)
}

// del asks for an explicit "yes" because a chat has no confirmation dialog and a deleted client cannot be restored.
func (b *Bot) del(ctx context.Context, arg string) string {
	f := strings.Fields(arg)
	if len(f) == 0 {
		return "Формат: /del имя yes"
	}
	if len(f) != 2 || !strings.EqualFold(f[1], "yes") {
		return "Удаление необратимо. Чтобы подтвердить, отправьте: /del " + f[0] + " yes"
	}
	return b.Handler.DeleteClient(ctx, f[0])
}
