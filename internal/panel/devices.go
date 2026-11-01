package panel

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/store"
	"anypanel2/internal/sub"
)

// Devices of a subscriber. The apps send the HWID with the subscription request (Happ does when the provider asks it to
// with Subscription-Always-Hwid-Enable); the panel remembers each device, shows it next to the client and can forget or
// block it. A blocked device is served a subscription without servers.

const (
	maxDevices        = 50               // per client: more are dropped, oldest first
	deviceTouchPeriod = 10 * time.Minute // how often the "last seen" time is written for a known device
)

var hwidRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// DeviceBlockedName is the name of the only entry a blocked device sees in its server list.
const DeviceBlockedName = "⛔ Устройство заблокировано"

// DeviceInfo is what a subscription request says about its device.
type DeviceInfo struct {
	HWID, OS, OSVersion, Model, App, IP string
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

// TouchDevice records that the device asked for the client's subscription and says whether it is blocked. A request
// without a usable HWID is not a device and is never blocked.
func (s *Service) TouchDevice(ctx context.Context, clientID string, d DeviceInfo) (blocked bool, err error) {
	if !hwidRe.MatchString(d.HWID) {
		return false, nil
	}
	s.devMu.Lock()
	defer s.devMu.Unlock()
	c, err := s.Store.GetClient(ctx, clientID)
	if err != nil {
		return false, err
	}
	now := s.Now().UTC()
	i := slices.IndexFunc(c.Devices, func(x model.Device) bool { return x.HWID == d.HWID })
	if i < 0 {
		if len(c.Devices) >= maxDevices {
			// Make room by forgetting the device that has been silent longest (never a blocked one: that would unblock it).
			old := -1
			for j, x := range c.Devices {
				if !x.Blocked && (old < 0 || x.LastSeen.Before(c.Devices[old].LastSeen)) {
					old = j
				}
			}
			if old < 0 {
				return false, nil
			}
			c.Devices = slices.Delete(c.Devices, old, old+1)
		}
		c.Devices = append(c.Devices, model.Device{HWID: d.HWID, FirstSeen: now})
		i = len(c.Devices) - 1
	}
	dev := &c.Devices[i]
	changed := dev.LastSeen.IsZero() || now.Sub(dev.LastSeen) >= deviceTouchPeriod
	for _, f := range []struct {
		dst *string
		src string
		max int
	}{{&dev.OS, d.OS, 40}, {&dev.OSVersion, d.OSVersion, 40}, {&dev.Model, d.Model, 80}, {&dev.App, d.App, 120}, {&dev.IP, d.IP, 64}} {
		if v := clip(f.src, f.max); v != "" && v != *f.dst {
			*f.dst, changed = v, true
		}
	}
	if changed {
		dev.LastSeen = now
		if err := s.Store.PutClient(ctx, *c); err != nil {
			return dev.Blocked, err
		}
	}
	return dev.Blocked, nil
}

func (s *Service) editDevices(ctx context.Context, clientID string, edit func(*model.Client) error) (*model.Client, error) {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	c, err := s.Store.GetClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if err := edit(c); err != nil {
		return nil, err
	}
	if err := s.Store.PutClient(ctx, *c); err != nil {
		return nil, err
	}
	return c, nil
}

// SetDeviceBlocked blocks or unblocks one device of a client.
func (s *Service) SetDeviceBlocked(ctx context.Context, clientID, hwid string, blocked bool) (*model.Client, error) {
	return s.editDevices(ctx, clientID, func(c *model.Client) error {
		i := slices.IndexFunc(c.Devices, func(x model.Device) bool { return x.HWID == hwid })
		if i < 0 {
			return store.ErrNotFound
		}
		c.Devices[i].Blocked = blocked
		return nil
	})
}

// DeleteDevice forgets a device. If it asks for the subscription again it is recorded as new (and no longer blocked).
func (s *Service) DeleteDevice(ctx context.Context, clientID, hwid string) (*model.Client, error) {
	return s.editDevices(ctx, clientID, func(c *model.Client) error {
		i := slices.IndexFunc(c.Devices, func(x model.Device) bool { return x.HWID == hwid })
		if i < 0 {
			return store.ErrNotFound
		}
		c.Devices = slices.Delete(c.Devices, i, i+1)
		return nil
	})
}

// BlockedSubscription turns a subscription into what a blocked device gets: a single entry that says so, and no servers.
func BlockedSubscription(sb *Subscription) {
	sb.Links, sb.Endpoints = nil, nil
	sb.Lines = []string{sub.InfoLink(DeviceBlockedName)}
	sb.InfoNames, sb.OnlyInfo = []string{DeviceBlockedName}, true
}
