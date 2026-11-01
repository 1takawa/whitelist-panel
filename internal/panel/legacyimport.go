package panel

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"anypanel2/internal/legacy"
	"anypanel2/internal/model"
)

// LegacyOptions control how legacy users are turned into clients.
type LegacyOptions struct {
	// InboundIDs are the inbounds every imported client is attached to; empty means all enabled inbounds.
	InboundIDs []string `json:"inboundIds"`
	// ExpiryFrom says what "expiration_days" counts from: "creation" (the account's creation date, the default)
	// or "now" (a fresh period starting today, useful when the legacy dates look wrong).
	ExpiryFrom string `json:"expiryFrom"`
	// DryRun reports what would happen without saving anything.
	DryRun bool `json:"dryRun"`
}

// LegacyItem is the outcome for one user.
type LegacyItem struct {
	Name     string     `json:"name"`
	Action   string     `json:"action"` // create | skip
	Reason   string     `json:"reason,omitempty"`
	Enable   bool       `json:"enable"`
	Total    int64      `json:"totalBytes"`
	Used     int64      `json:"used"`
	ExpiryAt *time.Time `json:"expiryAt"`
	Notes    []string   `json:"notes,omitempty"`
}

type LegacyReport struct {
	DryRun  bool         `json:"dryRun"`
	Created int          `json:"created"`
	Skipped int          `json:"skipped"`
	Items   []LegacyItem `json:"items"`
	// NotMigrated counts what the legacy records had that this panel has no equivalent for.
	NotMigrated map[string]int `json:"notMigrated"`
}

// ImportLegacy turns legacy users into clients. It is safe to run again: users whose name already exists are
// skipped, never overwritten.
func (s *Service) ImportLegacy(ctx context.Context, users []legacy.User, opts LegacyOptions) (*LegacyReport, error) {
	if len(users) == 0 {
		return nil, invalid("the file contains no users")
	}
	if len(users) > 5000 {
		return nil, invalid("at most 5000 users per import")
	}
	switch opts.ExpiryFrom {
	case "":
		opts.ExpiryFrom = "creation"
	case "creation", "now":
	default:
		return nil, invalid("expiryFrom must be creation or now")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	target := dedupe(opts.InboundIDs)
	if len(target) == 0 {
		for _, in := range ins {
			target = append(target, in.ID)
		}
	}
	if err := s.checkInboundIDs(ctx, target); err != nil {
		return nil, err
	}
	existing, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	names, tokens := map[string]bool{}, map[string]bool{}
	for _, c := range existing {
		names[strings.ToLower(c.Email)], tokens[c.SubID] = true, true
	}

	now := s.Now().UTC()
	rep := &LegacyReport{DryRun: opts.DryRun, Items: []LegacyItem{}, NotMigrated: map[string]int{}}
	for _, u := range users {
		it := LegacyItem{Name: u.Name}
		c := model.Client{
			Email: model.SanitizeName(u.Name), Enable: !u.Blocked, Flow: model.FlowVision, TotalBytes: u.MaxBytes, Up: u.Up, Down: u.Down,
			Comment: strings.TrimSpace(u.Note), TelegramID: u.TelegramID, InboundIDs: target, SubID: u.Token, CreatedAt: now,
		}
		if u.Unlimited {
			c.TotalBytes = 0
		}
		if !u.Created.IsZero() {
			c.CreatedAt = u.Created
		}
		if u.ExpirationDays > 0 {
			base := now
			if opts.ExpiryFrom == "creation" && !u.Created.IsZero() {
				base = u.Created
			}
			t := base.AddDate(0, 0, u.ExpirationDays)
			c.ExpiryAt = &t
		}

		if c.Email != u.Name && c.Email != "" {
			it.Notes = append(it.Notes, fmt.Sprintf("имя изменено на %q: допустимы только буквы, цифры и _ . @ + -", c.Email))
		}
		if !strings.ContainsFunc(c.Email, func(r rune) bool { return r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }) {
			it.Action, it.Reason = "skip", "в имени нет ни одной латинской буквы или цифры: создайте клиента вручную"
			rep.add(it, c)
			continue
		}
		if c.SubID != "" && !subIDRe.MatchString(c.SubID) {
			it.Notes = append(it.Notes, "токен подписки в нестандартном формате, выдан новый")
			c.SubID = ""
		}
		if err := normalizeClient(&c); err != nil {
			it.Action, it.Reason = "skip", err.Error()
			rep.add(it, c)
			continue
		}
		if names[strings.ToLower(c.Email)] {
			it.Action, it.Reason = "skip", "клиент с таким именем уже есть"
			rep.add(it, c)
			continue
		}
		if c.SubID == "" || tokens[c.SubID] {
			if c.SubID != "" {
				it.Notes = append(it.Notes, "прежний токен подписки уже занят, выдан новый")
			}
			c.SubID = newSubID()
		}
		if u.Devices > 0 {
			rep.NotMigrated["привязанные устройства (HWID)"]++
		}
		if u.Blocked {
			it.Notes = append(it.Notes, "заблокирован в прежней панели: импортирован выключенным")
		}
		if c.ExpiryAt != nil && !c.ExpiryAt.After(now) {
			it.Notes = append(it.Notes, "срок уже истёк")
		}

		it.Action = "create"
		if !opts.DryRun {
			c.ID, c.UUID, c.Password = newID(), newUUID(), newPassword()
			if err := s.putClient(ctx, c); err != nil {
				it.Action, it.Reason = "skip", err.Error()
				rep.add(it, c)
				continue
			}
		}
		names[strings.ToLower(c.Email)], tokens[c.SubID] = true, true
		rep.add(it, c)
	}
	if !opts.DryRun && rep.Created > 0 && s.Xray != nil {
		if err := s.applyLocked(ctx, false); err != nil {
			return rep, wrapApply(err)
		}
	}
	return rep, nil
}

func (r *LegacyReport) add(it LegacyItem, c model.Client) {
	it.Enable, it.Total, it.Used, it.ExpiryAt = c.Enable, c.TotalBytes, c.Up+c.Down, c.ExpiryAt
	if it.Action == "create" {
		r.Created++
	} else {
		r.Skipped++
	}
	r.Items = append(r.Items, it)
}
