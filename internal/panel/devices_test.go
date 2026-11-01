package panel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"anypanel2/internal/model"
)

func TestDeviceListIsBoundedAndBlockedDevicesAreKept(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	t0 := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	svc.Now = func() time.Time { return now }
	c, err := svc.CreateClient(ctx, model.Client{Email: "alice", Enable: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"", "has space", "semi;colon", string(make([]byte, 200))} {
		if blocked, err := svc.TouchDevice(ctx, c.ID, DeviceInfo{HWID: bad}); err != nil || blocked {
			t.Fatalf("%q: %v %v", bad, blocked, err)
		}
	}
	if got, _ := svc.Store.GetClient(ctx, c.ID); len(got.Devices) != 0 {
		t.Fatalf("a header that is not an HWID is not a device: %+v", got.Devices)
	}

	if _, err := svc.TouchDevice(ctx, c.ID, DeviceInfo{HWID: "dev-0", Model: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetDeviceBlocked(ctx, c.ID, "dev-0", true); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= maxDevices+5; i++ {
		now = t0.Add(time.Duration(i) * time.Minute)
		if _, err := svc.TouchDevice(ctx, c.ID, DeviceInfo{HWID: fmt.Sprintf("dev-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := svc.Store.GetClient(ctx, c.ID)
	if len(got.Devices) != maxDevices {
		t.Fatalf("at most %d devices: %d", maxDevices, len(got.Devices))
	}
	if !got.Devices[0].Blocked || got.Devices[0].HWID != "dev-0" {
		t.Fatalf("a blocked device is never pushed out (that would unblock it): %+v", got.Devices[0])
	}
	if blocked, _ := svc.TouchDevice(ctx, c.ID, DeviceInfo{HWID: "dev-0"}); !blocked {
		t.Fatal("still blocked")
	}

	// A known device is written again only after a while, so a busy app does not hammer the database.
	last := got.Devices[len(got.Devices)-1]
	now = now.Add(time.Minute)
	_, _ = svc.TouchDevice(ctx, c.ID, DeviceInfo{HWID: last.HWID})
	if again, _ := svc.Store.GetClient(ctx, c.ID); !again.Devices[len(again.Devices)-1].LastSeen.Equal(last.LastSeen) {
		t.Fatal("a repeat within the period changes nothing")
	}
	now = now.Add(deviceTouchPeriod)
	_, _ = svc.TouchDevice(ctx, c.ID, DeviceInfo{HWID: last.HWID, Model: "renamed"})
	if again, _ := svc.Store.GetClient(ctx, c.ID); again.Devices[len(again.Devices)-1].Model != "renamed" {
		t.Fatal("new details are recorded")
	}
}
