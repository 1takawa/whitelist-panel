package panel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"anypanel2/internal/update"
)

// Every 12 hours the panel asks GitHub whether a newer release came out and, if one did, tells the administrators in
// Telegram once: the version they have, the new one, when it came out and where the history of changes is.

const (
	updateCheckEvery = 12 * time.Hour
	updateRetry      = 30 * time.Minute // after a failed request or delivery
)

// UpdateNoticeTick does the check when it is due and the bot is running. It reports whether a notice was sent.
func (s *Service) UpdateNoticeTick(ctx context.Context) (bool, error) {
	bot := s.currentBot()
	if bot == nil || s.Update == nil {
		return false, nil
	}
	if _, ok := update.ParseVersion(s.Version); !ok {
		return false, nil // a development build has nothing to compare with
	}
	now := s.Now()
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return false, err
	}
	if sec.UpdateCheckedAt != nil && now.Sub(*sec.UpdateCheckedAt) < updateCheckEvery {
		return false, nil
	}
	if !s.lastUpdateFail.IsZero() && now.Sub(s.lastUpdateFail) < updateRetry {
		return false, nil
	}

	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	rel, err := s.Update.Latest(cctx, true)
	if err != nil {
		s.lastUpdateFail = now
		return false, err
	}
	sent := false
	if update.Newer(s.Version, rel.Version) && sec.UpdateNotified != rel.Version {
		when := ""
		if !rel.Published.IsZero() {
			when = ", вышла " + rel.Published.In(moscow).Format("02.01.2006")
		}
		text := fmt.Sprintf("Доступно обновление панели.\nВаша версия: %s\nНовая версия: %s%s\nИстория изменений: %s\nУстановить можно в «Настройках» панели.",
			s.Version, rel.Version, when, s.Update.ChangelogURL())
		n, err := bot.NotifyCount(cctx, text)
		if n == 0 {
			s.lastUpdateFail = now
			if err == nil {
				err = errors.New("no administrators to send to")
			}
			return false, err
		}
		sent = true
	}
	s.lastUpdateFail = time.Time{}
	return sent, s.markUpdateChecked(ctx, now, rel.Version, sent)
}

func (s *Service) markUpdateChecked(ctx context.Context, at time.Time, notified string, sent bool) error {
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return err
	}
	at = at.UTC()
	sec.UpdateCheckedAt = &at
	if sent {
		sec.UpdateNotified = notified
	}
	return s.Store.PutSecrets(ctx, *sec)
}

// RunUpdateNotices looks at the schedule every 5 minutes until ctx is cancelled.
func (s *Service) RunUpdateNotices(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.UpdateNoticeTick(ctx); err != nil && s.Log != nil {
				s.Log.Warn("checking for a new release failed", "err", err)
			}
		}
	}
}
