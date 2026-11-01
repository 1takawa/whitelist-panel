package panel

import (
	"context"
	"strings"
	"testing"

	"anypanel2/internal/model"
)

func previewSettings() model.SubSettings {
	set := model.Settings{PanelDomain: "panel.example.com"}.WithDefaults()
	return set.Subscription
}

func TestPreviewAppActiveShowsServersWithTheTemplate(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.com"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	s := previewSettings()
	s.RemarkTemplate = "{{INBOUND}} ({{PROTOCOL}})"
	s.Title = "{{EMAIL}} VPN"
	s.Announce = "Привет, {{EMAIL}}"
	s.SupportURL = "https://t.me/support?u={{EMAIL}}"
	s.ProfileMode = model.ProfileModeBuiltin
	s.InfoNode = true

	p, err := svc.PreviewApp(ctx, s, "active")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Servers) != 3 || p.Servers[0].Name != "DE Reality (VLESS)" || p.Servers[2].Name != "FI gRPC (TROJAN)" || p.Servers[0].Country != "DE" || p.Servers[1].Network != "ws" || p.Servers[0].Security != "reality" || p.Servers[2].Protocol != "trojan" {
		t.Fatalf("the template from the unsaved form is applied to each sample server: %v", p.Servers)
	}
	if p.Title != "alice VPN" || p.Announce != "Привет, alice" || p.SupportURL != "https://t.me/support?u=alice" {
		t.Fatalf("%+v", p)
	}
	if !strings.HasPrefix(p.ProfileURL, "https://panel.example.com/sub/") {
		t.Fatalf("the built-in profile page is the subscription address: %q", p.ProfileURL)
	}
	if p.State != "active" || p.OnlyInfo || p.Total == 0 || p.Used == 0 || p.Used >= p.Total || p.DaysLeft < 11 || p.ExpireDate == "" {
		t.Fatalf("%+v", p)
	}
}

func TestPreviewAppInfoNodeUsesTheLinkTemplateWithoutAnInbound(t *testing.T) {
	svc, _, _ := newService(t)
	s := previewSettings()
	s.InfoNode = true
	p, _ := svc.PreviewApp(context.Background(), s, "active")
	if p.InfoName != "alice|📊7.50GB|⏳12D" || len(p.Servers) != 3 || !strings.HasPrefix(p.Servers[0].Name, "DE Reality-alice|") {
		t.Fatalf("%+v", p)
	}
}

func TestPreviewAppExpiredAndDepletedShowOnlyTheInfoNode(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	s := previewSettings()
	s.InfoNode = true
	s.ExpiredTemplate = "⛔ Срок вышел {{EXPIRE_DATE}}"
	s.DepletedTemplate = "🚫 Трафик {{TRAFFIC_USED}}/{{TRAFFIC_TOTAL}}"

	exp, _ := svc.PreviewApp(ctx, s, "expired")
	if !exp.OnlyInfo || len(exp.Servers) != 0 || !strings.HasPrefix(exp.InfoName, "⛔ Срок вышел 20") || exp.DaysLeft != 0 {
		t.Fatalf("%+v", exp)
	}
	dep, _ := svc.PreviewApp(ctx, s, "depleted")
	if !dep.OnlyInfo || len(dep.Servers) != 0 || !strings.HasPrefix(dep.InfoName, "🚫 Трафик 10.00GB/10.00GB") || dep.Used != dep.Total {
		t.Fatalf("%+v", dep)
	}

	// Without the info node an expired client simply has the servers listed.
	s.InfoNode = false
	off, _ := svc.PreviewApp(ctx, s, "expired")
	if off.OnlyInfo || off.InfoName != "" || len(off.Servers) != 3 {
		t.Fatalf("%+v", off)
	}
}

func TestPreviewAppRejectsUnknownState(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.PreviewApp(context.Background(), previewSettings(), "banned"); !isInvalid(err) {
		t.Fatalf("%v", err)
	}
	if p, err := svc.PreviewApp(context.Background(), previewSettings(), ""); err != nil || p.State != "active" {
		t.Fatalf("an empty state means active: %+v %v", p, err)
	}
}

func TestPreviewAppPicksTheBannerOfTheState(t *testing.T) {
	svc, _, _ := newService(t)
	s := previewSettings()
	s.Happ.SubInfoText, s.Happ.SubInfoColor = "Общий", "blue"
	s.Happ.Banners.Trial = model.HappBanner{Color: "green", Text: "Тест для {{EMAIL}}\nещё {{DAYS_LEFT}} дн.", ButtonText: "Купить", ButtonLink: "https://pay.example.com/?u={{EMAIL}}"}
	s.Happ.Banners.Expired = model.HappBanner{Color: "red", Text: "Срок вышел {{EXPIRE_DATE}}"}

	for state, want := range map[string]string{"active": "Общий", "depleted": "Общий"} {
		if p, err := svc.PreviewApp(context.Background(), s, state); err != nil || p.Banner.Text != want {
			t.Errorf("%s: a state without its own banner keeps the common one: %+v %v", state, p, err)
		}
	}
	p, err := svc.PreviewApp(context.Background(), s, "trial")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != "trial" || p.Banner.Color != "green" || !strings.HasPrefix(p.Banner.Text, "Тест для alice\nещё 1") || p.Banner.ButtonLink != "https://pay.example.com/?u=alice" {
		t.Fatalf("%+v", p.Banner)
	}
	p, _ = svc.PreviewApp(context.Background(), s, "expired")
	if p.Banner.Color != "red" || !strings.HasPrefix(p.Banner.Text, "Срок вышел 20") {
		t.Fatalf("%+v", p.Banner)
	}
}
