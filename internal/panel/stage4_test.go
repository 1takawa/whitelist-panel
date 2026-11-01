package panel

import (
	"context"
	"strings"
	"testing"

	"anypanel2/internal/model"
)

func TestSettingsFromRelease03GetAppDefaults(t *testing.T) {
	ctx := context.Background()
	svc, _, st := newService(t)
	// Exactly what release 0.3 stored: schema version 3, an explicit encode=false, no Happ or INCY block.
	_ = st.PutSettings(ctx, model.Settings{PanelDomain: "p.example.com", Subscription: model.SubSettings{
		Version: 3, Enable: true, Encode: false, UpdateHours: 6, Path: "/s/x/", RemarkTemplate: "{{EMAIL}}",
		ExpiredTemplate: "e", DepletedTemplate: "d", ProfileMode: model.ProfileModeNone, Title: "T",
	}})
	got, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := got.Subscription
	if s.Version != model.SubSettingsVersion {
		t.Fatalf("version %d", s.Version)
	}
	if s.Encode || s.Path != "/s/x/" || s.RemarkTemplate != "{{EMAIL}}" || s.Title != "T" {
		t.Fatalf("release 0.3 choices must survive the upgrade: %+v", s)
	}
	if s.Happ.SubInfoColor != "blue" || s.Happ.AutoConnectType != "lowestdelay" || s.Happ.PerAppMode != "off" || s.Happ.LocalProxyAuth != "auto" {
		t.Fatalf("Happ defaults: %+v", s.Happ)
	}
	if s.Happ.AutoDetect || s.Incy.AutoDetect || s.Happ.EnableRouting || s.Incy.EnableRouting {
		t.Fatal("nothing is switched on by the upgrade: apps keep their own behaviour")
	}
}

func TestHappAndIncyValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	base, _ := svc.GetSettings(ctx)

	bad := map[string]func(*model.SubSettings){
		"happ new url scheme":     func(s *model.SubSettings) { s.Happ.NewURL = "javascript:alert(1)" },
		"happ fallback no scheme": func(s *model.SubSettings) { s.Happ.FallbackURL = "example.com/x" },
		"happ button link":        func(s *model.SubSettings) { s.Happ.SubInfoButtonLink = "ftp://x" },
		"happ color":              func(s *model.SubSettings) { s.Happ.SubInfoColor = "purple" },
		"happ token":              func(s *model.SubSettings) { s.Happ.TunMode = "tun mode!" },
		"happ banner too long":    func(s *model.SubSettings) { s.Happ.SubInfoText = strings.Repeat("я", 201) },
		"happ banner many lines":  func(s *model.SubSettings) { s.Happ.Banners.Trial.Text = strings.Repeat("a\n", 10) + "b" },
		"happ state banner color": func(s *model.SubSettings) { s.Happ.Banners.Expired = model.HappBanner{Text: "x", Color: "pink"} },
		"happ state banner link":  func(s *model.SubSettings) { s.Happ.Banners.Active = model.HappBanner{Text: "x", ButtonLink: "ftp://x"} },
		"happ routing garbage":    func(s *model.SubSettings) { s.Happ.RoutingRules = "just text" },
		"happ routing bad json":   func(s *model.SubSettings) { s.Happ.RoutingRules = "{nope" },
		"happ routing on, empty":  func(s *model.SubSettings) { s.Happ.EnableRouting = true },
		"incy email":              func(s *model.SubSettings) { s.Incy.SupportEmail = "not-an-email" },
		"incy sort":               func(s *model.SubSettings) { s.Incy.SortOrder = "random" },
		"incy switch":             func(s *model.SubSettings) { s.Incy.HideURL = "yes" },
		"incy color":              func(s *model.SubSettings) { s.Incy.BannerBgColor = "red" },
		"incy range":              func(s *model.SubSettings) { s.Incy.FragmentationLength = "ten" },
		"incy packets":            func(s *model.SubSettings) { s.Incy.FragmentationPackets = "some" },
		"incy routing multi-line": func(s *model.SubSettings) { s.Incy.RoutingRules = "incy://a\nincy://b" },
		"incy routing on, empty":  func(s *model.SubSettings) { s.Incy.EnableRouting = true },
		"incy link scheme":        func(s *model.SubSettings) { s.Incy.PremiumURL = "file:///etc/passwd" },
	}
	for name, mutate := range bad {
		s := *base
		mutate(&s.Subscription)
		if _, err := svc.PutSettings(ctx, s); !isInvalid(err) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}

	ok := *base
	ok.Subscription.Happ = model.HappSettings{AutoDetect: true, ProviderID: "abc", NewURL: "https://new.example.com/", SubInfoText: "Привет", SubInfoColor: "GREEN",
		SubInfoButtonLink: "tg://resolve?domain=bot", PingType: "proxy", AutoConnect: true, AutoConnectType: "lowestdelay", PerAppMode: "on",
		PerAppList: "com.a\ncom.b", EnableRouting: true, RoutingRules: `{"Name":"x","GlobalProxy":"true"}`}
	ok.Subscription.Incy = model.IncySettings{AutoDetect: true, SortOrder: "Ping", SupportEmail: "help@example.com", BannerBgColor: "#aabbcc", HideURL: "1",
		FragmentationLength: "10-20", FragmentationPackets: "tlshello", EnableRouting: true, RoutingRules: "incy://routing/abc"}
	got, err := svc.PutSettings(ctx, ok)
	if err != nil {
		t.Fatalf("valid app settings were rejected: %v", err)
	}
	if got.Subscription.Happ.SubInfoColor != "green" || got.Subscription.Incy.SortOrder != "ping" {
		t.Fatalf("enums are normalised: %+v %+v", got.Subscription.Happ, got.Subscription.Incy)
	}
	if !strings.HasPrefix(got.Subscription.Happ.RoutingRules, "{") {
		t.Fatal("the JSON profile is stored as typed and converted when served")
	}
}
