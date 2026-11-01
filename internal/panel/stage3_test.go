package panel

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/model"
	"anypanel2/internal/store"
)

// stage3Fixture creates domains, a REALITY inbound and a client attached to it.
func stage3Fixture(t *testing.T, mutate func(*model.Settings), client model.Client) (*Service, *fakeXray, *model.Client) {
	t.Helper()
	svc, fx, _ := newService(t)
	ctx := context.Background()
	in, err := svc.CreateInbound(ctx, realityInbound())
	if err != nil {
		t.Fatal(err)
	}
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain, set.RealityDomain = "panel.example.com", "reality.example.com"
	if mutate != nil {
		mutate(set)
	}
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatalf("settings: %v", err)
	}
	client.Enable = client.Enable || client.Email == ""
	if client.Email == "" {
		client.Email = "alice"
	}
	client.InboundIDs = []string{in.ID}
	c, err := svc.CreateClient(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	return svc, fx, c
}

func fragment(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Fragment
}

func TestLinkNamesFollowTheTemplate(t *testing.T) {
	svc, _, c := stage3Fixture(t, nil, model.Client{Enable: true, TotalBytes: 10 << 30})
	lv, err := svc.ClientLinks(context.Background(), c.ID)
	if err != nil || len(lv.Links) != 1 {
		t.Fatalf("%v %+v", err, lv)
	}
	// Default template; the unlimited expiry segment is dropped.
	if got := fragment(t, lv.Links[0].Link); got != "DE reality-alice|📊10.00GB" || lv.Links[0].Name != got {
		t.Fatalf("name %q / %q", got, lv.Links[0].Name)
	}

	set, _ := svc.GetSettings(context.Background())
	set.Subscription.RemarkTemplate = "{{EMAIL}}@{{PROTOCOL}}/{{TRANSPORT}}/{{SECURITY}}"
	if _, err := svc.PutSettings(context.Background(), *set); err != nil {
		t.Fatal(err)
	}
	lv, _ = svc.ClientLinks(context.Background(), c.ID)
	if got := fragment(t, lv.Links[0].Link); got != "alice@VLESS/tcp/REALITY" {
		t.Fatalf("custom template: %q", got)
	}
}

func TestInfoNode(t *testing.T) {
	ctx := context.Background()
	on := func(s *model.Settings) { s.Subscription.InfoNode = true }

	// Active client: the info node is added in front of the real links.
	svc, _, c := stage3Fixture(t, on, model.Client{Enable: true, TotalBytes: 10 << 30})
	sb, err := svc.PublicSubscription(ctx, c.SubID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sb.Lines) != 2 || !strings.HasPrefix(sb.Lines[0], model.InfoNodeLinkPrefix) || !strings.HasPrefix(sb.Lines[1], "vless://") {
		t.Fatalf("active: %q", sb.Lines)
	}
	if len(sb.Links) != 1 {
		t.Fatal("the user page lists real links only, not the info node")
	}
	if name := fragment(t, sb.Lines[0]); !strings.Contains(name, "📊10.00GB") {
		t.Fatalf("info node name: %q", name)
	}

	// Expired: the info node is the only entry and uses the expired template.
	past := time.Now().Add(-48 * time.Hour)
	svc, _, c = stage3Fixture(t, on, model.Client{Email: "old", Enable: true, ExpiryAt: &past})
	sb, _ = svc.PublicSubscription(ctx, c.SubID)
	if len(sb.Lines) != 1 || !strings.HasPrefix(sb.Lines[0], model.InfoNodeLinkPrefix) {
		t.Fatalf("expired: %q", sb.Lines)
	}
	if name := fragment(t, sb.Lines[0]); !strings.Contains(name, "⛔ old") || !strings.Contains(name, "Срок истёк") {
		t.Fatalf("expired name: %q", name)
	}

	// Depleted quota.
	svc, _, c = stage3Fixture(t, on, model.Client{Email: "big", Enable: true, TotalBytes: 100})
	svc.Store.AddTraffic(ctx, map[string]store.Traffic{"big": {Up: 60, Down: 60}})
	sb, _ = svc.PublicSubscription(ctx, c.SubID)
	if len(sb.Lines) != 1 || !strings.Contains(fragment(t, sb.Lines[0]), "Трафик исчерпан") {
		t.Fatalf("depleted: %q", sb.Lines)
	}

	// Off by default: nothing is added.
	svc, _, c = stage3Fixture(t, nil, model.Client{Enable: true})
	sb, _ = svc.PublicSubscription(ctx, c.SubID)
	if len(sb.Lines) != 1 || !strings.HasPrefix(sb.Lines[0], "vless://") {
		t.Fatalf("info node off: %q", sb.Lines)
	}
}

func TestCalendarExpiryIsEnforcedByXrayToo(t *testing.T) {
	ctx := context.Background()
	exp := time.Date(2026, 1, 10, 8, 0, 0, 0, time.UTC) // morning of the last day
	now := time.Date(2026, 1, 10, 20, 0, 0, 0, time.UTC)

	for _, inclusive := range []bool{false, true} {
		svc, fx, c := stage3Fixture(t, func(s *model.Settings) {
			s.Subscription.CalendarExpire, s.Subscription.TimeZone = inclusive, "UTC"
		}, model.Client{Enable: true, ExpiryAt: &exp})
		svc.Now = func() time.Time { return now }
		if err := svc.PollTraffic(ctx); err != nil {
			t.Fatal(err)
		}
		inConfig := strings.Contains(fx.last(), c.UUID)
		sb, _ := svc.PublicSubscription(ctx, c.SubID)
		if inclusive {
			if !inConfig || sb.Client.Status(now) != "active" {
				t.Fatalf("with the last day counted the client must still connect (config=%v status=%s)", inConfig, sb.Client.Status(now))
			}
			if got := sb.Client.ExpiryAt.UTC(); !got.Equal(time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("the reported expiry moves to the end of the day: %v", got)
			}
		} else if inConfig || sb.Client.Status(now) != "expired" {
			t.Fatalf("without the option the client expired at 08:00 (config=%v status=%s)", inConfig, sb.Client.Status(now))
		}
	}
}

func TestProfileMetadata(t *testing.T) {
	ctx := context.Background()
	svc, _, c := stage3Fixture(t, func(s *model.Settings) {
		s.Subscription.Title = "VPN {{EMAIL}}"
		s.Subscription.SupportURL = "https://t.me/support?u={{EMAIL}}&s={{SUB_ID}}"
		s.Subscription.Announce = "Привет, {{EMAIL}}"
		s.Subscription.ProfileMode = model.ProfileModeBuiltin
	}, model.Client{Email: "a.b+c", Enable: true})
	sb, _ := svc.PublicSubscription(ctx, c.SubID)
	if sb.Meta.Title != "VPN a.b+c" || sb.Meta.Announce != "Привет, a.b+c" {
		t.Fatalf("text placeholders: %+v", sb.Meta)
	}
	if sb.Meta.SupportURL != "https://t.me/support?u=a.b%2Bc&s="+c.SubID {
		t.Fatalf("link placeholders must be escaped: %q", sb.Meta.SupportURL)
	}
	if sb.Meta.ProfileURL != "https://panel.example.com/sub/"+c.SubID || sb.URL != sb.Meta.ProfileURL {
		t.Fatalf("builtin mode points at the subscription itself: %q", sb.Meta.ProfileURL)
	}

	set, _ := svc.GetSettings(ctx)
	set.Subscription.ProfileMode, set.Subscription.ProfileURL = model.ProfileModeCustom, "https://example.com/u/{{SUB_ID}}"
	svc.PutSettings(ctx, *set)
	if sb, _ = svc.PublicSubscription(ctx, c.SubID); sb.Meta.ProfileURL != "https://example.com/u/"+c.SubID {
		t.Fatalf("custom mode: %q", sb.Meta.ProfileURL)
	}
	set.Subscription.ProfileMode = model.ProfileModeNone
	svc.PutSettings(ctx, *set)
	if sb, _ = svc.PublicSubscription(ctx, c.SubID); sb.Meta.ProfileURL != "" {
		t.Fatalf("none mode must omit the profile page even though a URL is saved: %q", sb.Meta.ProfileURL)
	}
}

func TestSubscriptionURLOverrides(t *testing.T) {
	ctx := context.Background()
	svc, _, c := stage3Fixture(t, func(s *model.Settings) {
		s.Subscription.Path, s.Subscription.Domain = "s/xyz", "CDN.Example.com"
	}, model.Client{Enable: true})
	lv, _ := svc.ClientLinks(ctx, c.ID)
	if lv.SubscriptionURL != "https://cdn.example.com/s/xyz/"+c.SubID {
		t.Fatalf("path and domain overrides: %q", lv.SubscriptionURL)
	}
	set, _ := svc.GetSettings(ctx)
	set.Subscription.URI = "https://edge.example.com/prefix/"
	svc.PutSettings(ctx, *set)
	if lv, _ = svc.ClientLinks(ctx, c.ID); lv.SubscriptionURL != "https://edge.example.com/prefix/"+c.SubID {
		t.Fatalf("a full URI wins: %q", lv.SubscriptionURL)
	}
}

func TestSettingsWrittenByTheEarlierReleaseAreUpgraded(t *testing.T) {
	ctx := context.Background()
	svc, _, st := newService(t)
	// Exactly what release 0.2 stored: no version, no new fields.
	_ = st.PutSettings(ctx, model.Settings{PanelDomain: "p.example.com", Subscription: model.SubSettings{
		Enable: true, Title: "Old title", SupportURL: "https://t.me/x", ProfileURL: "https://example.com", UpdateHours: 6,
	}})
	got, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := got.Subscription
	if s.Version != model.SubSettingsVersion || !s.Encode || s.Path != "/sub/" || s.RemarkTemplate != model.DefaultRemarkTemplate ||
		s.ExpiredTemplate != model.DefaultExpiredTemplate || s.DepletedTemplate != model.DefaultDepletedTemplate {
		t.Fatalf("defaults for the new fields: %+v", s)
	}
	if s.Title != "Old title" || s.UpdateHours != 6 || s.SupportURL != "https://t.me/x" || !s.Enable {
		t.Fatalf("existing values must be kept: %+v", s)
	}
	if s.ProfileMode != model.ProfileModeCustom {
		t.Fatalf("a profile link saved earlier keeps working: %q", s.ProfileMode)
	}
	if got.PanelDomain != "p.example.com" {
		t.Fatal("domains are kept")
	}
}

func TestExplicitEncodeOffSurvivesSaving(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	set, _ := svc.GetSettings(ctx)
	set.Subscription.Encode = false
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetSettings(ctx)
	if got.Subscription.Encode {
		t.Fatal("encode=false chosen by the user must not be reset to the default")
	}
}

func TestSubSettingsValidation(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	base, _ := svc.GetSettings(ctx)
	bad := map[string]func(*model.SubSettings){
		"reserved path":        func(s *model.SubSettings) { s.Path = "/api/" },
		"login path":           func(s *model.SubSettings) { s.Path = "/login/x/" },
		"path with spaces":     func(s *model.SubSettings) { s.Path = "/a b/" },
		"path with dot dot":    func(s *model.SubSettings) { s.Path = "/a//b/" },
		"bad domain":           func(s *model.SubSettings) { s.Domain = "not a host" },
		"uri without scheme":   func(s *model.SubSettings) { s.URI = "edge.example.com/x" },
		"uri with query":       func(s *model.SubSettings) { s.URI = "https://e.example.com/?a=1" },
		"unknown time zone":    func(s *model.SubSettings) { s.TimeZone = "Mars/Olympus" },
		"custom without url":   func(s *model.SubSettings) { s.ProfileMode, s.ProfileURL = model.ProfileModeCustom, "" },
		"unknown profile mode": func(s *model.SubSettings) { s.ProfileMode = "weird" },
		"huge template":        func(s *model.SubSettings) { s.RemarkTemplate = strings.Repeat("x", 300) },
	}
	for name, mutate := range bad {
		s := *base
		mutate(&s.Subscription)
		if _, err := svc.PutSettings(ctx, s); !isInvalid(err) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	ok := *base
	ok.Subscription.Path, ok.Subscription.TimeZone, ok.Subscription.URI = "/s/abc-123/", "Europe/Moscow", "https://edge.example.com/p"
	got, err := svc.PutSettings(ctx, ok)
	if err != nil || got.Subscription.Path != "/s/abc-123/" {
		t.Fatalf("valid settings: %v %+v", err, got)
	}
}

func TestPreviewTemplate(t *testing.T) {
	ctx := context.Background()
	svc, _, c := stage3Fixture(t, nil, model.Client{Enable: true, TotalBytes: 10 << 30})

	got, err := svc.PreviewTemplate(ctx, "remark", "{{INBOUND}}-{{EMAIL}}|📊{{TRAFFIC_LEFT}}|⏳{{DAYS_LEFT}}D", "", "")
	if err != nil || got != "DE Reality-alice|📊7.50GB|⏳12D" { // 10 GB limit, 1 GB up + 1.5 GB down used
		t.Fatalf("sample client: %q %v", got, err)
	}
	got, _ = svc.PreviewTemplate(ctx, "remark", "{{EMAIL}} {{STATUS_EMOJI}}", c.ID, "")
	if got != "alice ✅" {
		t.Fatalf("real client: %q", got)
	}
	if got, _ := svc.PreviewTemplate(ctx, "meta", "Hi {{EMAIL}} {{TRAFFIC_LEFT}}", "", ""); got != "Hi alice {{TRAFFIC_LEFT}}" {
		t.Fatalf("meta preview uses identity tokens only: %q", got)
	}
	if got, _ := svc.PreviewTemplate(ctx, "url", "https://x/?u={{SUB_ID}}", "", ""); got != "https://x/?u=k3j9x0q2m7p5v8ab" {
		t.Fatalf("url preview: %q", got)
	}
	if _, err := svc.PreviewTemplate(ctx, "nope", "x", "", ""); !isInvalid(err) {
		t.Fatalf("unknown kind: %v", err)
	}
	if got, _ := svc.PreviewTemplate(ctx, "remark", "{{EXPIRE_DATE}}", "", "Pacific/Kiritimati"); got == "" {
		t.Fatal("a time zone override must be accepted")
	}
}
