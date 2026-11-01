package panel

import (
	"context"
	"strconv"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/sysinfo"
	"anypanel2/internal/xray"
)

func TestDashboardCountsOnlineAndTraffic(t *testing.T) {
	ctx := context.Background()
	svc, fx, _ := newService(t)
	now := time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	in, _ := svc.CreateInbound(ctx, realityInbound())
	_, _ = svc.CreateInbound(ctx, func() model.Inbound { w := wsInbound(10001); w.Enable = false; return w }())
	past := now.Add(-time.Hour)
	mk := func(email string, c model.Client) *model.Client {
		c.Email, c.InboundIDs = email, []string{in.ID}
		out, err := svc.CreateClient(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	mk("busy", model.Client{Enable: true, TotalBytes: 1000})
	mk("idle", model.Client{Enable: true})
	mk("old", model.Client{Enable: true, ExpiryAt: &past})
	mk("off", model.Client{Enable: false})
	mk("full", model.Client{Enable: true, TotalBytes: 100})

	fx.stats = map[string]xray.Traffic{"busy": {Up: 100, Down: 300}, "full": {Up: 80, Down: 80}}
	if err := svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}

	d, err := svc.Dashboard(ctx, sysinfo.Info{Hostname: "h"}, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "9.9.9" || d.System.Hostname != "h" {
		t.Fatalf("version and system pass through: %+v", d)
	}
	if d.Inbounds.Total != 2 || d.Inbounds.Enabled != 1 {
		t.Fatalf("inbounds: %+v", d.Inbounds)
	}
	c := d.Clients
	if c.Total != 5 || c.Active != 2 || c.Expired != 1 || c.Depleted != 1 || c.Disabled != 1 {
		t.Fatalf("client states: %+v", c)
	}
	if c.Online != 2 || len(d.Online) != 2 || d.Online[0] != "busy" || d.Online[1] != "full" {
		t.Fatalf("clients that moved traffic in the last poll are online: %+v %v", c, d.Online)
	}
	if d.Traffic.Up != 180 || d.Traffic.Down != 380 {
		t.Fatalf("totals: %+v", d.Traffic)
	}
	if len(d.Top) != 2 || d.Top[0].Name != "busy" || d.Top[0].Used != 400 || d.Top[0].Limit != 1000 {
		t.Fatalf("top consumers, idle clients omitted: %+v", d.Top)
	}
	if len(d.History) != 24 {
		t.Fatalf("history has a fixed 24 points, got %d", len(d.History))
	}
	last := d.History[23]
	if last.Time != now.Unix()/3600*3600 || last.Up != 180 || last.Down != 380 {
		t.Fatalf("the current hour holds this poll's traffic: %+v", last)
	}
	if d.History[0].Up != 0 || d.History[0].Time >= last.Time {
		t.Fatalf("older hours are empty and ordered: %+v", d.History[0])
	}

	// Later: the window passes and nobody is online any more.
	svc.Now = func() time.Time { return now.Add(3 * time.Minute) }
	d, _ = svc.Dashboard(ctx, sysinfo.Info{}, "x")
	if d.Clients.Online != 0 {
		t.Fatalf("online means recent: %+v", d.Clients)
	}
}

func TestDashboardHistoryNamesTheBusiestNodes(t *testing.T) {
	ctx := context.Background()
	svc, fx, _ := newService(t)
	now := time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	a, _ := svc.CreateInbound(ctx, realityInbound())
	w := wsInbound(10001)
	w.Remark = "second"
	b, _ := svc.CreateInbound(ctx, w)
	_, _ = svc.CreateClient(ctx, model.Client{Email: "u", Enable: true, Trial: true, InboundIDs: []string{a.ID, b.ID}})

	fx.stats = map[string]xray.Traffic{
		"u": {Up: 10, Down: 90},
		xray.InboundStatPrefix + xray.InboundTag(a.ID): {Up: 5, Down: 15},
		xray.InboundStatPrefix + xray.InboundTag(b.ID): {Up: 20, Down: 60},
		xray.InboundStatPrefix + "in-gone":             {Up: 1, Down: 1},
	}
	if err := svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := svc.Dashboard(ctx, sysinfo.Info{}, "x")
	if err != nil {
		t.Fatal(err)
	}
	last := d.History[23]
	if len(last.Nodes) != 2 || last.Nodes[0].Name != "second" || last.Nodes[0].Bytes != 80 || last.Nodes[1].Bytes != 20 {
		t.Fatalf("the hour lists its inbounds busiest first, deleted ones left out: %+v", last.Nodes)
	}
	if d.History[0].Nodes == nil || len(d.History[0].Nodes) != 0 {
		t.Fatalf("an empty hour has an empty list, not null: %#v", d.History[0].Nodes)
	}
	if d.Clients.Trial != 1 || d.Clients.Active != 1 {
		t.Fatalf("a running test subscription is counted: %+v", d.Clients)
	}
}

func TestSamplesAccumulateWithinTheHour(t *testing.T) {
	ctx := context.Background()
	svc, fx, _ := newService(t)
	now := time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	in, _ := svc.CreateInbound(ctx, realityInbound())
	_, _ = svc.CreateClient(ctx, model.Client{Email: "a", Enable: true, InboundIDs: []string{in.ID}})

	for i := 0; i < 3; i++ {
		fx.stats = map[string]xray.Traffic{"a": {Up: 10, Down: 20}}
		_ = svc.PollTraffic(ctx)
		now = now.Add(10 * time.Second)
	}
	d, _ := svc.Dashboard(ctx, sysinfo.Info{}, "x")
	if got := d.History[23]; got.Up != 30 || got.Down != 60 {
		t.Fatalf("three polls in one hour add up: %+v", got)
	}
}

func TestDashboardCountsOnlineOnTheMainServerAlone(t *testing.T) {
	ctx := context.Background()
	svc, fx, _ := newService(t)
	now := time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	in, _ := svc.CreateInbound(ctx, realityInbound())
	_, _ = svc.CreateClient(ctx, model.Client{Email: "a", Enable: true, InboundIDs: []string{in.ID}})
	_, _ = svc.CreateClient(ctx, model.Client{Email: "b", Enable: true, InboundIDs: []string{in.ID}})

	fx.stats = map[string]xray.Traffic{"a": {Up: 1, Down: 2}}
	if err := svc.PollTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	// "b" is online through another server only: the global count has it, the main server's own count does not.
	if err := svc.Store.MarkActive(ctx, []string{"b"}, now); err != nil {
		t.Fatal(err)
	}
	d, _ := svc.Dashboard(ctx, sysinfo.Info{}, "x")
	if d.Clients.Online != 2 || d.Clients.OnlineHere != 1 {
		t.Fatalf("online overall and on this machine: %+v", d.Clients)
	}
	svc.Now = func() time.Time { return now.Add(3 * time.Minute) }
	d, _ = svc.Dashboard(ctx, sysinfo.Info{}, "x")
	if d.Clients.OnlineHere != 0 {
		t.Fatalf("online on this machine expires like the overall count: %+v", d.Clients)
	}
}

func TestDashboardTopOverDayWeekAndLifetime(t *testing.T) {
	ctx := context.Background()
	svc, fx, _ := newService(t)
	now := time.Date(2026, 5, 20, 12, 5, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	base := now
	in, _ := svc.CreateInbound(ctx, realityInbound())
	for _, e := range []string{"old", "week", "today"} {
		_, _ = svc.CreateClient(ctx, model.Client{Email: e, Enable: true, TotalBytes: 1000, InboundIDs: []string{in.ID}})
	}
	poll := func(at time.Time, stats map[string]xray.Traffic) {
		now = at
		fx.stats = stats
		if err := svc.PollTraffic(ctx); err != nil {
			t.Fatal(err)
		}
	}
	poll(base.AddDate(0, 0, -10), map[string]xray.Traffic{"old": {Down: 900}})
	poll(base.AddDate(0, 0, -3), map[string]xray.Traffic{"week": {Down: 400}, "today": {Down: 5}})
	poll(base, map[string]xray.Traffic{"today": {Up: 20, Down: 30}})

	d, err := svc.Dashboard(ctx, sysinfo.Info{}, "x")
	if err != nil {
		t.Fatal(err)
	}
	names := func(l []TopClient) string {
		s := ""
		for _, c := range l {
			s += c.Name + ":" + strconv.FormatInt(c.Used, 10) + " "
		}
		return s
	}
	if got := names(d.TopPeriods.Day); got != "today:50 " {
		t.Fatalf("day: %q", got)
	}
	if got := names(d.TopPeriods.Week); got != "week:400 today:55 " {
		t.Fatalf("week: %q", got)
	}
	if got := names(d.TopPeriods.All); got != "old:900 week:400 today:55 " {
		t.Fatalf("lifetime: %q", got)
	}
	if d.TopPeriods.Day[0].Limit != 1000 {
		t.Fatalf("the quota rides along: %+v", d.TopPeriods.Day[0])
	}
}
