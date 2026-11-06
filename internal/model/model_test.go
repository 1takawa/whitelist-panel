package model

import (
	"testing"
	"time"
)

func TestBannerState(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for name, tc := range map[string]struct {
		c    Client
		want string
	}{
		"active":            {Client{Enable: true}, "active"},
		"trial":             {Client{Enable: true, Trial: true}, "trial"},
		"expired trial":     {Client{Enable: true, Trial: true, ExpiryAt: &past}, "expired"},
		"running trial":     {Client{Enable: true, Trial: true, ExpiryAt: &future}, "trial"},
		"depleted":          {Client{Enable: true, TotalBytes: 10, Up: 10}, "depleted"},
		"depleted and over": {Client{Enable: true, TotalBytes: 10, Down: 11, ExpiryAt: &past}, "expired"},
		"disabled":          {Client{Trial: true}, ""},
	} {
		if got := tc.c.BannerState(now); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestOldDefaultSubscriptionTitleIsReplaced(t *testing.T) {
	s := Settings{Subscription: SubSettings{Version: SubSettingsVersion, Enable: true, Title: "ANY Panel"}}
	if got := s.WithDefaults().Subscription.Title; got != DefaultSubTitle {
		t.Fatalf("the old default title becomes the new one: %q", got)
	}
	s.Subscription.Title = "My VPN"
	if got := s.WithDefaults().Subscription.Title; got != "My VPN" {
		t.Fatalf("a title somebody chose stays: %q", got)
	}
}

func TestSinglePageOfEarlierReleasesBecomesATheme(t *testing.T) {
	on := PageSettings{OldEnable: true, OldHTML: "<p>x</p>", OldCSS: "p{}", OldJS: "1"}
	on.migrate()
	if len(on.Themes) != 1 || on.Selected != on.Themes[0].ID || on.Themes[0].HTML != "<p>x</p>" || on.Themes[0].CSS != "p{}" || on.Themes[0].JS != "1" || on.OldHTML != "" {
		t.Fatalf("%+v", on)
	}
	off := PageSettings{OldHTML: "<p>x</p>"}
	off.migrate()
	if len(off.Themes) != 1 || off.Selected != "" {
		t.Fatalf("a page that was off stays off: %+v", off)
	}
	none := PageSettings{}
	none.migrate()
	if none.Themes == nil || len(none.Themes) != 0 || none.Theme("x") != nil {
		t.Fatalf("%+v", none)
	}
	kept := PageSettings{Selected: "a", Themes: []PageTheme{{ID: "a", Name: "A", HTML: "<b/>"}}, OldHTML: "<p>old</p>"}
	kept.migrate()
	if len(kept.Themes) != 1 || kept.Themes[0].HTML != "<b/>" {
		t.Fatalf("saved themes win over the old page: %+v", kept)
	}
}
