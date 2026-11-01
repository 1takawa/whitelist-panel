package panel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/remark"
	"anypanel2/internal/sub"
	"anypanel2/internal/sysinfo"
	"anypanel2/internal/telegram"
)

var botTokenRe = regexp.MustCompile(`^\d{6,12}:[A-Za-z0-9_-]{30,50}$`)

// notifyTimeout bounds one batch of Telegram messages so a slow API never stalls the panel.
const notifyTimeout = 20 * time.Second

// TelegramStatus is what the settings page shows about the bot.
type TelegramStatus struct {
	Enable   bool    `json:"enable"`
	AdminIDs []int64 `json:"adminIds"`
	TokenSet bool    `json:"tokenSet"`
	Running  bool    `json:"running"`
	Username string  `json:"username,omitempty"`
	Error    string  `json:"error,omitempty"`
}

func (s *Service) tgClient(token string) *telegram.Client {
	return &telegram.Client{Token: token, BaseURL: s.TelegramBase}
}

// StartTelegram records the long-lived context the bot runs in and starts the bot if it is configured.
func (s *Service) StartTelegram(ctx context.Context) {
	s.tgMu.Lock()
	s.tgCtx = ctx
	s.tgMu.Unlock()
	s.ReloadTelegram(ctx)
	go func() { // stop the bot when the panel shuts down
		<-ctx.Done()
		s.stopTelegram()
	}()
}

func (s *Service) stopTelegram() {
	s.tgMu.Lock()
	defer s.tgMu.Unlock()
	if s.tgCancel != nil {
		s.tgCancel()
		s.tgCancel = nil
	}
	s.bot = nil
}

// ReloadTelegram applies the current settings and token: it stops the running bot and starts a new one when
// Telegram is enabled and has a token and at least one administrator.
func (s *Service) ReloadTelegram(ctx context.Context) {
	set, err := s.settings(ctx)
	if err != nil {
		return
	}
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return
	}
	s.tgMu.Lock()
	defer s.tgMu.Unlock()
	if s.tgCancel != nil {
		s.tgCancel()
		s.tgCancel = nil
	}
	s.bot, s.tgErr = nil, ""
	if s.tgCtx == nil || !set.Telegram.Enable || sec.TelegramToken == "" || len(set.Telegram.AdminIDs) == 0 {
		return
	}
	bctx, cancel := context.WithCancel(s.tgCtx)
	s.tgCancel = cancel
	log := s.Log
	bot := &telegram.Bot{
		Client:  s.tgClient(sec.TelegramToken),
		Admins:  s.adminIDs,
		Handler: tgHandler{s},
		Log:     log,
	}
	s.bot = bot
	go bot.Run(bctx)
}

// adminIDs is read on every message so changes to the administrator list apply without restarting the bot.
func (s *Service) adminIDs() []int64 {
	set, err := s.settings(context.Background())
	if err != nil {
		return nil
	}
	return set.Telegram.AdminIDs
}

func (s *Service) currentBot() *telegram.Bot {
	s.tgMu.Lock()
	defer s.tgMu.Unlock()
	return s.bot
}

func (s *Service) TelegramStatus(ctx context.Context) (TelegramStatus, error) {
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return TelegramStatus{}, err
	}
	set, err := s.settings(ctx)
	if err != nil {
		return TelegramStatus{}, err
	}
	st := TelegramStatus{Enable: set.Telegram.Enable, AdminIDs: set.Telegram.AdminIDs, TokenSet: sec.TelegramToken != ""}
	bot := s.currentBot()
	st.Running = bot != nil
	if st.TokenSet {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if me, err := s.tgClient(sec.TelegramToken).GetMe(cctx); err == nil {
			st.Username = me.Username
		} else {
			st.Error = err.Error()
		}
	}
	return st, nil
}

// TelegramSave is the bot form: on/off, the administrators' Telegram IDs and, optionally, a new bot token.
type TelegramSave struct {
	Enable   bool    `json:"enable"`
	AdminIDs []int64 `json:"adminIds"`
	// Token is checked with Telegram and stored; empty keeps the stored one.
	Token string `json:"token"`
}

// SaveTelegram stores the whole bot setup in one step and restarts the bot with it.
func (s *Service) SaveTelegram(ctx context.Context, in TelegramSave) (TelegramStatus, error) {
	tg := model.TelegramSettings{Enable: in.Enable, AdminIDs: in.AdminIDs}
	if err := normalizeTelegram(&tg); err != nil {
		return TelegramStatus{}, err
	}
	token := strings.TrimSpace(in.Token)
	if token != "" {
		if !botTokenRe.MatchString(token) {
			return TelegramStatus{}, invalid("это не похоже на токен бота (123456789:AA…, его выдаёт @BotFather)")
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if _, err := s.tgClient(token).GetMe(cctx); err != nil {
			return TelegramStatus{}, invalid("Telegram не принял токен: %v", err)
		}
	}
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return TelegramStatus{}, err
	}
	if tg.Enable {
		if token == "" && sec.TelegramToken == "" {
			return TelegramStatus{}, invalid("укажите токен бота от @BotFather")
		}
		if len(tg.AdminIDs) == 0 {
			return TelegramStatus{}, invalid("укажите Telegram ID хотя бы одного администратора")
		}
	}
	if token != "" {
		s.secretsMu.Lock()
		sec, err = s.Store.GetSecrets(ctx)
		if err == nil {
			sec.TelegramToken = token
			err = s.Store.PutSecrets(ctx, *sec)
		}
		s.secretsMu.Unlock()
		if err != nil {
			return TelegramStatus{}, err
		}
	}
	set, err := s.settings(ctx)
	if err != nil {
		return TelegramStatus{}, err
	}
	set.Telegram = tg
	if _, err := s.PutSettings(ctx, *set); err != nil { // restarts the bot
		return TelegramStatus{}, err
	}
	return s.TelegramStatus(ctx)
}

// TelegramTest sends a test message to every administrator so the setup can be verified.
func (s *Service) TelegramTest(ctx context.Context) error {
	bot := s.currentBot()
	if bot == nil {
		return invalid("the bot is not running: enable it, save a token and add at least one administrator ID")
	}
	cctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	if err := bot.Notify(cctx, "✅ Whitelist Panel: связь с ботом работает."); err != nil {
		return invalid("%v", err)
	}
	return nil
}

// ---- notifications ----

// notify sends text to the administrators in the background; it does nothing when the bot is not running.
func (s *Service) notify(text string) {
	bot := s.currentBot()
	if bot == nil {
		return
	}
	s.tgWG.Add(1)
	go func() {
		defer s.tgWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		defer cancel()
		if err := bot.Notify(ctx, text); err != nil && s.Log != nil {
			s.Log.Warn("telegram notification failed", "err", err)
		}
	}()
}

// WaitNotifications blocks until queued notifications were attempted (used by tests and shutdown).
func (s *Service) WaitNotifications() { s.tgWG.Wait() }

// checkEvents compares the current client and Xray states with the previous poll and reports changes.
// The first call only records the state, so a restart of the panel never repeats old notifications.
func (s *Service) checkEvents(ctx context.Context) {
	set, err := s.settings(ctx)
	if err != nil {
		return
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return
	}
	now := s.Now()
	seeded := s.evSeeded
	cur := make(map[string]string, len(cls))
	for _, c := range cls {
		st := remark.Status(set.Subscription.EffectiveClient(c), now)
		cur[c.ID] = st
		if !seeded || st == s.evStatus[c.ID] {
			continue
		}
		switch {
		case st == "expired":
			s.notify(fmt.Sprintf("⏰ Подписка клиента %s истекла.", c.Email))
		case st == "depleted":
			s.notify(fmt.Sprintf("📉 У клиента %s закончился трафик.", c.Email))
		}
	}
	s.evStatus = cur

	if s.Xray != nil && s.Xray.Installed() {
		running := s.Xray.Status(ctx).Running
		if seeded && running != s.evXrayUp {
			if running {
				s.notify("✅ Xray снова работает.")
			} else {
				s.notify("🛑 Xray остановлен.")
			}
		}
		s.evXrayUp = running
	}
	s.checkServerEvents(ctx, set, seeded)
	s.evSeeded = true
}

// ---- bot commands ----

type tgHandler struct{ s *Service }

func (h tgHandler) Stats(ctx context.Context) string {
	d, err := h.s.DashboardNow(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	xr := "остановлен"
	if d.Xray.Running {
		xr = "работает"
		if d.Xray.Version != "" {
			xr += " (" + d.Xray.Version + ")"
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Whitelist Panel %s — %s\n", d.Version, d.System.Hostname)
	fmt.Fprintf(&b, "Xray: %s\n", xr)
	if d.System.MemTotal > 0 {
		fmt.Fprintf(&b, "CPU %.0f%%, память %s из %s, диск %s из %s\n", d.System.CPUPercent,
			remark.FormatBytes(int64(d.System.MemUsed)), remark.FormatBytes(int64(d.System.MemTotal)),
			remark.FormatBytes(int64(d.System.DiskUsed)), remark.FormatBytes(int64(d.System.DiskTotal)))
	}
	fmt.Fprintf(&b, "Подключений: %d из %d включено\n", d.Inbounds.Enabled, d.Inbounds.Total)
	fmt.Fprintf(&b, "Клиенты: всего %d, активных %d, онлайн %d, истекло %d, без трафика %d, выключено %d\n",
		d.Clients.Total, d.Clients.Active, d.Clients.Online, d.Clients.Expired, d.Clients.Depleted, d.Clients.Disabled)
	fmt.Fprintf(&b, "Трафик всего: ↑ %s  ↓ %s", remark.FormatBytes(d.Traffic.Up), remark.FormatBytes(d.Traffic.Down))
	return b.String()
}

func (h tgHandler) Online(ctx context.Context) string {
	d, err := h.s.DashboardNow(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	if len(d.Online) == 0 {
		return "Сейчас никто не онлайн."
	}
	return fmt.Sprintf("Онлайн (%d):\n%s", len(d.Online), strings.Join(d.Online, "\n"))
}

func (h tgHandler) ClientInfo(ctx context.Context, name string) string {
	cls, err := h.s.Store.ListClients(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	var match []model.Client
	for _, c := range cls {
		if strings.EqualFold(c.Email, name) {
			match = []model.Client{c}
			break
		}
		if strings.HasPrefix(strings.ToLower(c.Email), strings.ToLower(name)) {
			match = append(match, c)
		}
	}
	switch {
	case len(match) == 0:
		return "Клиент не найден."
	case len(match) > 1:
		names := make([]string, 0, len(match))
		for _, c := range match {
			names = append(names, c.Email)
		}
		sort.Strings(names)
		if len(names) > 20 {
			names = append(names[:20], "…")
		}
		return "Подходит несколько клиентов, уточните имя:\n" + strings.Join(names, "\n")
	}
	set, err := h.s.settings(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	c := match[0]
	eff := set.Subscription.EffectiveClient(c)
	now := h.s.Now()
	statusRu := map[string]string{"active": "активен", "expired": "истёк", "depleted": "трафик исчерпан", "disabled": "выключен"}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", c.Email, statusRu[remark.Status(eff, now)])
	limit := "без лимита"
	if c.TotalBytes > 0 {
		limit = "из " + remark.FormatBytes(c.TotalBytes)
	}
	fmt.Fprintf(&b, "Трафик: %s %s (↑ %s ↓ %s)\n", remark.FormatBytes(c.Up+c.Down), limit, remark.FormatBytes(c.Up), remark.FormatBytes(c.Down))
	if c.ExpiryAt != nil {
		fmt.Fprintf(&b, "Действует до: %s\n", eff.ExpiryAt.In(set.Subscription.Location()).Format("02.01.2006 15:04"))
	} else {
		b.WriteString("Срок: без ограничения\n")
	}
	if c.Online(now, OnlineWindow) {
		b.WriteString("Сейчас онлайн\n")
	}
	if c.Comment != "" {
		fmt.Fprintf(&b, "Заметка: %s\n", c.Comment)
	}
	if set.PanelDomain != "" {
		fmt.Fprintf(&b, "Подписка: %s", sub.URL(set.Subscription, set.PanelDomain, c.SubID))
	}
	return strings.TrimSpace(b.String())
}

// ---- dashboard glue ----

// DashboardNow builds the dashboard with the service's own system sampler and version.
func (s *Service) DashboardNow(ctx context.Context) (*Dashboard, error) {
	var sys sysinfo.Info
	if s.Sampler != nil {
		sys = s.Sampler.Get()
	}
	return s.Dashboard(ctx, sys, s.Version)
}

// checkServerEvents reports servers that went offline or came back. Like the other events it stays silent on the
// first look, and only judges servers whose state is actually known.
func (s *Service) checkServerEvents(ctx context.Context, set *model.Settings, seeded bool) {
	srvs, err := s.Store.ListServers(ctx)
	if err != nil {
		return
	}
	if s.evServerUp == nil {
		s.evServerUp = map[string]bool{}
	}
	for _, sv := range srvs {
		if !sv.Enable {
			delete(s.evServerUp, sv.ID)
			continue
		}
		s.srvMu.Lock()
		rt := s.srv[sv.ID]
		known := rt != nil && (rt.state.LastSeen != nil || rt.state.Error != "")
		on := rt != nil && rt.state.Online
		s.srvMu.Unlock()
		if !known {
			continue
		}
		prev, had := s.evServerUp[sv.ID]
		if seeded && had && prev != on {
			if on {
				s.notify(fmt.Sprintf("✅ Сервер %s снова на связи.", sv.Name))
			} else {
				s.notify(fmt.Sprintf("🛑 Сервер %s недоступен.", sv.Name))
			}
		}
		s.evServerUp[sv.ID] = on
	}
}

// ---- client management from the bot ----

const tgListLimit = 60

// clientByName finds exactly one client by its full name (case-insensitive). Changing commands never guess.
func (h tgHandler) clientByName(ctx context.Context, name string) (*model.Client, string) {
	cls, err := h.s.Store.ListClients(ctx)
	if err != nil {
		return nil, "Ошибка: " + err.Error()
	}
	for _, c := range cls {
		if strings.EqualFold(c.Email, name) {
			c := c
			return &c, ""
		}
	}
	return nil, "Клиент «" + name + "» не найден. Для изменений нужно полное имя, список — /users."
}

func (h tgHandler) Users(ctx context.Context) string {
	cls, err := h.s.Store.ListClients(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	if len(cls) == 0 {
		return "Клиентов пока нет. Создать: /add имя [ГБ] [дней]"
	}
	set, err := h.s.settings(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	now := h.s.Now()
	mark := map[string]string{"active": "🟢", "expired": "⏰", "depleted": "📉", "disabled": "⚫"}
	sort.Slice(cls, func(i, j int) bool { return strings.ToLower(cls[i].Email) < strings.ToLower(cls[j].Email) })
	var b strings.Builder
	fmt.Fprintf(&b, "Клиенты (%d):\n", len(cls))
	for i, c := range cls {
		if i == tgListLimit {
			fmt.Fprintf(&b, "… и ещё %d. Найти одного: /client имя", len(cls)-tgListLimit)
			break
		}
		fmt.Fprintf(&b, "%s %s\n", mark[remark.Status(set.Subscription.EffectiveClient(c), now)], c.Email)
	}
	return strings.TrimSpace(b.String())
}

func (h tgHandler) AddClient(ctx context.Context, name string, gb float64, days int) string {
	ins, err := h.s.Store.ListInbounds(ctx)
	if err != nil {
		return "Ошибка: " + err.Error()
	}
	c := model.Client{Email: name, Enable: true, Flow: model.FlowVision, TotalBytes: int64(gb * (1 << 30))}
	for _, in := range ins {
		c.InboundIDs = append(c.InboundIDs, in.ID) // every connection: its switch decides who sees it
	}
	if len(c.InboundIDs) == 0 {
		return "В панели ещё нет подключений: сначала создайте подключение."
	}
	if days > 0 {
		t := h.s.Now().UTC().AddDate(0, 0, days)
		c.ExpiryAt = &t
	}
	made, err := h.s.CreateClient(ctx, c)
	var ie *InvalidError
	switch {
	case errors.As(err, &ie):
		return "Не получилось: " + ie.Msg
	case err != nil && made == nil:
		return "Ошибка: " + err.Error()
	}
	return "Клиент создан.\n" + h.ClientInfo(ctx, made.Email)
}

func (h tgHandler) SetEnabled(ctx context.Context, name string, enable bool) string {
	c, msg := h.clientByName(ctx, name)
	if c == nil {
		return msg
	}
	c.Enable = enable
	return h.save(ctx, *c, map[bool]string{true: "включён", false: "выключен"}[enable])
}

func (h tgHandler) ResetTraffic(ctx context.Context, name string) string {
	c, msg := h.clientByName(ctx, name)
	if c == nil {
		return msg
	}
	if _, err := h.s.ResetTraffic(ctx, c.ID); err != nil {
		var ae *ApplyError
		if !errors.As(err, &ae) {
			return "Ошибка: " + err.Error()
		}
	}
	return fmt.Sprintf("Трафик клиента %s обнулён.", c.Email)
}

func (h tgHandler) Extend(ctx context.Context, name string, days int) string {
	c, msg := h.clientByName(ctx, name)
	if c == nil {
		return msg
	}
	now := h.s.Now().UTC()
	base := now
	if c.ExpiryAt != nil && c.ExpiryAt.After(now) {
		base = *c.ExpiryAt
	}
	t := base.AddDate(0, 0, days)
	c.ExpiryAt = &t
	set, _ := h.s.settings(ctx)
	loc := time.Local
	if set != nil {
		loc = set.Subscription.Location()
	}
	return h.save(ctx, *c, "продлён до "+t.In(loc).Format("02.01.2006 15:04"))
}

// save stores an edited client through the service, so Xray is updated, and words the outcome.
func (h tgHandler) save(ctx context.Context, c model.Client, what string) string {
	_, err := h.s.UpdateClient(ctx, c.ID, c)
	var ie *InvalidError
	var ae *ApplyError
	switch {
	case errors.As(err, &ie):
		return "Не получилось: " + ie.Msg
	case err != nil && !errors.As(err, &ae):
		return "Ошибка: " + err.Error()
	}
	return fmt.Sprintf("Клиент %s %s.", c.Email, what)
}

func (h tgHandler) DeleteClient(ctx context.Context, name string) string {
	c, msg := h.clientByName(ctx, name)
	if c == nil {
		return msg
	}
	if err := h.s.DeleteClient(ctx, c.ID); err != nil {
		var ae *ApplyError
		if !errors.As(err, &ae) {
			return "Ошибка: " + err.Error()
		}
	}
	return fmt.Sprintf("Клиент %s удалён.", c.Email)
}

func (h tgHandler) RestartXray(ctx context.Context) string {
	if err := h.s.XrayRestart(ctx); err != nil {
		return "Xray не перезапущен: " + err.Error()
	}
	return "Xray перезапущен."
}

func (h tgHandler) Backup(ctx context.Context, kind string) (string, []byte, string, error) {
	name, data, err := h.s.ExportDB(ctx, kind)
	if err != nil {
		return "", nil, "", err
	}
	label := "Полная копия базы"
	if kind == ExportUsers {
		label = "Пользователи"
	}
	return name, data, fmt.Sprintf("%s, %s", label, remark.FormatBytes(int64(len(data)))), nil
}
