package panel

import (
	"context"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/xray"
)

// profileFor returns the Xray profile of a machine: the main server's ("") lives in the settings, an extra
// server's on the server itself.
func (s *Service) profileFor(ctx context.Context, serverID string) (model.XrayProfile, error) {
	if serverID == "" {
		set, err := s.settings(ctx)
		if err != nil {
			return model.XrayProfile{}, err
		}
		return set.XrayProfile, nil
	}
	sv, err := s.Store.GetServer(ctx, serverID)
	if err != nil {
		return model.XrayProfile{}, err
	}
	return sv.XrayProfile, nil
}

// periodStart is the beginning of the reset period that contains now: the top of the hour, midnight, Monday
// midnight or the first of the month, in now's time zone.
func periodStart(kind string, now time.Time) time.Time {
	y, m, d := now.Date()
	switch kind {
	case model.ResetHourly:
		return time.Date(y, m, d, now.Hour(), 0, 0, 0, now.Location())
	case model.ResetDaily:
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	case model.ResetWeekly:
		back := (int(now.Weekday()) + 6) % 7 // Monday = 0
		return time.Date(y, m, d-back, 0, 0, 0, 0, now.Location())
	case model.ResetMonthly:
		return time.Date(y, m, 1, 0, 0, 0, 0, now.Location())
	}
	return time.Time{}
}

// resetDue reports whether a period boundary passed since the inbound's counters were last zeroed.
func resetDue(in model.Inbound, now time.Time) bool {
	start := periodStart(in.TrafficReset, now)
	if start.IsZero() {
		return false
	}
	last := in.CreatedAt
	if in.LastReset != nil {
		last = *in.LastReset
	}
	return start.After(last)
}

// accountInbounds adds the counters read from Xray to the inbounds they belong to (by tag) and zeroes the counters
// whose reset period ended. It returns whether anything that decides who is served changed, so the caller re-applies
// the config: an inbound becomes inactive when its limit is used up and active again after a reset.
func (s *Service) accountInbounds(ctx context.Context, deltas map[string]xray.Traffic) {
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		s.Log.Warn("reading inbounds for traffic accounting failed", "err", err)
		return
	}
	now := s.Now()
	for _, in := range ins {
		t := deltas[xray.InboundTag(in.ID)]
		changed := t.Up != 0 || t.Down != 0
		in.Up, in.Down = in.Up+t.Up, in.Down+t.Down
		if resetDue(in, now) {
			in.Up, in.Down = 0, 0
			n := now
			in.LastReset = &n
			changed = true
		}
		if !changed {
			continue
		}
		if err := s.Store.PutInbound(ctx, in); err != nil {
			s.Log.Warn("saving inbound traffic failed", "inbound", in.Remark, "err", err)
		}
	}
}

// ResetInboundTraffic zeroes an inbound's counters by hand (which re-activates an inbound that hit its limit).
func (s *Service) ResetInboundTraffic(ctx context.Context, id string) (*model.Inbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := s.Store.GetInbound(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	in.Up, in.Down, in.LastReset = 0, 0, &now
	if err := s.Store.PutInbound(ctx, *in); err != nil {
		return nil, err
	}
	return in, wrapApply(s.applyLocked(ctx, false))
}
