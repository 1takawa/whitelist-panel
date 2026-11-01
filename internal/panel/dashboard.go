package panel

import (
	"context"
	"sort"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/remark"
	"anypanel2/internal/sysinfo"
	"anypanel2/internal/xray"
)

// LocalServerID keys the per-server traffic rows of the machine the panel itself runs on.
const LocalServerID = "local"

// OnlineWindow is how recently a client must have moved traffic to count as online.
const OnlineWindow = 2 * time.Minute

type Dashboard struct {
	Version string       `json:"version"`
	System  sysinfo.Info `json:"system"`
	Xray    xray.Status  `json:"xray"`

	Inbounds struct {
		Total   int `json:"total"`
		Enabled int `json:"enabled"`
	} `json:"inbounds"`

	Clients struct {
		Total      int `json:"total"`
		Active     int `json:"active"`     // may connect right now
		Online     int `json:"online"`     // moved traffic within OnlineWindow (on any server)
		OnlineHere int `json:"onlineHere"` // the same, through the main server only
		Expired    int `json:"expired"`    // period ended
		Depleted   int `json:"depleted"`   // quota used up
		Disabled   int `json:"disabled"`
		Trial      int `json:"trial"` // test subscriptions that may connect right now (a subset of Active)
	} `json:"clients"`

	Traffic struct {
		Up   int64 `json:"up"`
		Down int64 `json:"down"`
	} `json:"traffic"`

	Top []TopClient `json:"top"` // by traffic over the clients' whole life
	// TopPeriods are the same top five over the last day, the last week and the whole life. Day and week are counted from the
	// hourly samples, so they fill up from the day the panel started keeping them.
	TopPeriods struct {
		Day  []TopClient `json:"day"`
		Week []TopClient `json:"week"`
		All  []TopClient `json:"all"`
	} `json:"topPeriods"`
	History []HistoryPoint `json:"history"` // the last 24 hours, one point per hour, oldest first
	Online  []string       `json:"onlineNames"`
}

type TopClient struct {
	Name  string `json:"name"`
	Used  int64  `json:"used"`
	Limit int64  `json:"limit"`
}

type HistoryPoint struct {
	Time  int64       `json:"time"` // Unix seconds at the start of the hour
	Up    int64       `json:"up"`
	Down  int64       `json:"down"`
	Nodes []NodeShare `json:"nodes"` // the busiest inbounds of that hour, busiest first
}

// NodeShare is the traffic one inbound carried in an hour.
type NodeShare struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// busiestNodes is how many inbounds the chart's hint lists per hour.
const busiestNodes = 3

// topOf lists the five clients with the most traffic in a period; names that are not clients (any more) are left out.
// Never nil, so the JSON is always a list.
func topOf(used map[string]int64, limits map[string]int64) []TopClient {
	out := []TopClient{}
	for name, n := range used {
		if lim, ok := limits[name]; ok && n > 0 {
			out = append(out, TopClient{Name: name, Used: n, Limit: lim})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Used != out[j].Used {
			return out[i].Used > out[j].Used
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// busiest names the inbounds that carried the most in an hour; inbounds deleted since are left out. Never nil, so the
// JSON is always a list.
func busiest(nodes map[string]int64, remarks map[string]string) []NodeShare {
	out := []NodeShare{}
	for id, n := range nodes {
		if name, ok := remarks[id]; ok && n > 0 {
			out = append(out, NodeShare{Name: name, Bytes: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > busiestNodes {
		out = out[:busiestNodes]
	}
	return out
}

// Dashboard gathers the overview figures. System is supplied by the caller (it comes from a background sampler).
func (s *Service) Dashboard(ctx context.Context, sys sysinfo.Info, version string) (*Dashboard, error) {
	ins, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, err
	}
	cls, err := s.Store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	d := &Dashboard{Version: version, System: sys, Xray: s.Xray.Status(ctx), Top: []TopClient{}, Online: []string{}}

	d.Inbounds.Total = len(ins)
	for _, in := range ins {
		if in.Enable {
			d.Inbounds.Enabled++
		}
	}
	for _, c := range cls {
		eff := set.Subscription.EffectiveClient(c)
		d.Clients.Total++
		switch remark.Status(eff, now) {
		case "active":
			d.Clients.Active++
			if c.Trial {
				d.Clients.Trial++
			}
		case "expired":
			d.Clients.Expired++
		case "depleted":
			d.Clients.Depleted++
		case "disabled":
			d.Clients.Disabled++
		}
		if c.Online(now, OnlineWindow) {
			d.Clients.Online++
			d.Online = append(d.Online, c.Email)
		}
		d.Traffic.Up += c.Up
		d.Traffic.Down += c.Down
	}

	byUse := append([]model.Client(nil), cls...)
	sort.Slice(byUse, func(i, j int) bool { return byUse[i].Up+byUse[i].Down > byUse[j].Up+byUse[j].Down })
	for _, c := range byUse {
		if len(d.Top) == 5 || c.Up+c.Down == 0 {
			break
		}
		d.Top = append(d.Top, TopClient{Name: c.Email, Used: c.Up + c.Down, Limit: c.TotalBytes})
	}
	d.TopPeriods.All = d.Top
	sort.Strings(d.Online)
	if rows, err := s.Store.ListServerTraffic(ctx, LocalServerID); err == nil {
		for _, r := range rows {
			if r.LastActiveAt != nil && now.Sub(*r.LastActiveAt) <= OnlineWindow {
				d.Clients.OnlineHere++
			}
		}
	}

	// Top clients over the last day and week.
	nowH := now.Unix() / 3600
	limits := make(map[string]int64, len(cls))
	for _, c := range cls {
		limits[c.Email] = c.TotalBytes
	}
	for _, p := range []struct {
		hours int64
		out   *[]TopClient
	}{{24, &d.TopPeriods.Day}, {7 * 24, &d.TopPeriods.Week}} {
		sums, err := s.Store.UserTrafficSince(ctx, nowH-p.hours+1)
		if err != nil {
			return nil, err
		}
		*p.out = topOf(sums, limits)
	}

	// History: always 24 points so the chart has a stable shape; hours without samples are zero.
	nowHour := now.Unix() / 3600
	samples, err := s.Store.ListSamples(ctx, nowHour-23)
	if err != nil {
		return nil, err
	}
	byHour := map[int64]model.Sample{}
	for _, sm := range samples {
		byHour[sm.Hour] = sm
	}
	remarks := make(map[string]string, len(ins))
	for _, in := range ins {
		remarks[in.ID] = in.Remark
	}
	for h := nowHour - 23; h <= nowHour; h++ {
		sm := byHour[h]
		d.History = append(d.History, HistoryPoint{Time: h * 3600, Up: sm.Up, Down: sm.Down, Nodes: busiest(sm.Nodes, remarks)})
	}
	return d, nil
}
