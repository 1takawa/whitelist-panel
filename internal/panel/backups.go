package panel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"anypanel2/internal/remark"
)

// The daily copy of the database goes to the administrators in Telegram and is not kept on the server.
// It is sent once per Moscow day, as soon as the day starts; a panel that was off at midnight sends it on return.

// backupRetry is how long to wait after a failed delivery before trying again.
const backupRetry = 15 * time.Minute

// backupSlot is the latest scheduled moment that is not in the future: today's 00:00 in Moscow.
func backupSlot(now time.Time) time.Time {
	t := now.In(moscow)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, moscow)
}

// BackupTick sends the daily copy if it is due and the bot is running. It reports whether a copy was sent.
func (s *Service) BackupTick(ctx context.Context) (bool, error) {
	bot := s.currentBot()
	if bot == nil {
		return false, nil
	}
	now := s.Now()
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return false, err
	}
	if sec.BackupSentAt != nil && !sec.BackupSentAt.Before(backupSlot(now)) {
		return false, nil
	}
	if !s.lastBackupFail.IsZero() && now.Sub(s.lastBackupFail) < backupRetry {
		return false, nil
	}

	name, data, err := s.ExportDB(ctx, ExportFull)
	if err == nil {
		caption := fmt.Sprintf("Ежедневная копия базы, %s", remark.FormatBytes(int64(len(data))))
		var sent int
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		sent, err = bot.NotifyFile(cctx, name, data, caption)
		cancel()
		if sent > 0 {
			if err != nil && s.Log != nil { // some administrators could not be reached; the others have the copy
				s.Log.Warn("daily copy was not delivered to every administrator", "err", err)
			}
			s.lastBackupFail = time.Time{}
			return true, s.markBackupSent(ctx, now)
		}
		if err == nil {
			err = errors.New("no administrators to send to")
		}
	}
	s.lastBackupFail = now
	return false, err
}

func (s *Service) markBackupSent(ctx context.Context, at time.Time) error {
	s.secretsMu.Lock()
	defer s.secretsMu.Unlock()
	sec, err := s.Store.GetSecrets(ctx)
	if err != nil {
		return err
	}
	at = at.UTC()
	sec.BackupSentAt = &at
	return s.Store.PutSecrets(ctx, *sec)
}

// RunBackups checks the schedule every 30 seconds until ctx is cancelled.
func (s *Service) RunBackups(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.BackupTick(ctx); err != nil && s.Log != nil {
				s.Log.Error("daily database copy failed", "err", err)
			}
		}
	}
}
