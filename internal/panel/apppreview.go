package panel

import (
	"context"
	"strings"
	"time"

	"anypanel2/internal/links"
	"anypanel2/internal/model"
	"anypanel2/internal/remark"
	"anypanel2/internal/sub"
)

// AppPreview is what a subscriber's app would receive for the settings being edited, rendered with the same code
// that builds real subscriptions. The settings come from the (unsaved) form, the client is a sample one.
type AppPreview struct {
	State      string           `json:"state"`  // active | trial | expired | depleted
	Banner     model.HappBanner `json:"banner"` // the Happ information banner this subscriber would see
	Email      string           `json:"email"`
	Title      string           `json:"title"`
	Announce   string           `json:"announce"`
	SupportURL string           `json:"supportUrl"`
	ProfileURL string           `json:"profileUrl"`
	Servers    []PreviewServer  `json:"servers"`  // the real servers the app lists (empty when only the info node is shown)
	InfoName   string           `json:"infoName"` // the info node's name, "" when there is none
	OnlyInfo   bool             `json:"onlyInfo"`
	Used       int64            `json:"used"`
	Total      int64            `json:"total"` // 0 = unlimited
	ExpireDate string           `json:"expireDate"`
	DaysLeft   int              `json:"daysLeft"` // -1 = no expiry
}

// PreviewServer is one server row of the app's list.
type PreviewServer struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // vless | trojan | auto (the automatic node)
	Network  string `json:"network"`  // tcp | ws | grpc …
	Security string `json:"security"` // reality | tls | none
	Country  string `json:"country"`  // two-letter code the app would show a flag for
}

// previewServers are the sample connections the link-name template is shown on, with the country of each.
func previewServers() []struct {
	In      model.Inbound
	Country string
} {
	return []struct {
		In      model.Inbound
		Country string
	}{
		{model.Inbound{Remark: "DE Reality", Protocol: model.ProtoVLESS, Network: model.NetTCP, Security: model.SecReality}, "DE"},
		{model.Inbound{Remark: "NL WebSocket", Protocol: model.ProtoVLESS, Network: model.NetWS, Security: model.SecTLS}, "NL"},
		{model.Inbound{Remark: "FI gRPC", Protocol: model.ProtoTrojan, Network: model.NetGRPC, Security: model.SecTLS}, "FI"},
	}
}

// PreviewApp renders the subscription for a sample client in the given state.
func (s *Service) PreviewApp(ctx context.Context, subs model.SubSettings, state string) (*AppPreview, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	c := sampleClient(now)
	switch state {
	case "", "active":
		state = "active"
	case "expired":
		past := now.Add(-26 * time.Hour)
		c.ExpiryAt = &past
		c.Up, c.Down = 1<<30, 3<<30
	case "depleted":
		c.Up, c.Down = 4<<30, c.TotalBytes-4<<30
	case "trial":
		c.Trial = true
	default:
		return nil, invalid("state must be active, trial, expired or depleted")
	}
	eff := subs.EffectiveClient(c)

	rc := remark.Context{Client: eff, Now: now, Loc: subs.Location()}
	var rows []PreviewServer
	for _, ps := range previewServers() {
		in := ps.In
		rc.Inbound, rc.Security = &in, links.EffectiveSecurity(in)
		name := strings.TrimSpace(remark.Expand(subs.RemarkTemplate, rc))
		if name == "" {
			name = c.Email
		}
		rows = append(rows, PreviewServer{Name: name, Protocol: in.Protocol, Network: in.Network, Security: rc.Security, Country: ps.Country})
	}
	if a := subs.AutoNode; len(rows) >= 2 {
		var front, back []PreviewServer
		put := func(p PreviewServer, last bool) {
			if last {
				back = append(back, p)
			} else {
				front = append(front, p)
			}
		}
		if a.Enable {
			auto := PreviewServer{Name: strings.TrimSpace(a.Name), Protocol: "auto"}
			if auto.Name == "" {
				auto.Name = model.DefaultAutoNodeName
			}
			put(auto, a.Last)
		}
		for _, g := range a.Groups {
			if g.Enable && len(g.Members) >= 2 {
				put(PreviewServer{Name: g.Name, Protocol: "auto", Country: g.Country}, g.Last)
			}
		}
		rows = append(append(front, rows...), back...)
	}
	info, only := infoNode(subs, eff, true, now)
	if only {
		rows = []PreviewServer{}
	}
	meta := profileMeta(subs, eff, sub.URL(subs, set.PanelDomain, c.SubID))
	_, banner := happBanner(subs, eff, now)

	v := &AppPreview{
		State: state, Banner: banner, Email: c.Email, Title: meta.Title, Announce: meta.Announce, SupportURL: meta.SupportURL, ProfileURL: meta.ProfileURL,
		Servers: rows, InfoName: info, OnlyInfo: only, Used: c.Up + c.Down, Total: c.TotalBytes, DaysLeft: -1,
	}
	if eff.ExpiryAt != nil {
		v.ExpireDate = eff.ExpiryAt.In(subs.Location()).Format("2006-01-02")
		if d := eff.ExpiryAt.Sub(now); d > 0 {
			v.DaysLeft = int(d / (24 * time.Hour))
		} else {
			v.DaysLeft = 0
		}
	}
	return v, nil
}
