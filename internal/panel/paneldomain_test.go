package panel

import (
	"context"
	"testing"
)

func TestHostOfURLIsADomainNameOrNothing(t *testing.T) {
	for in, want := range map[string]string{
		"https://panel.example.com":       "panel.example.com",
		"https://Panel.Example.com/":      "panel.example.com",
		"https://panel.example.com:8443/": "panel.example.com",
		"http://localhost:28270":          "",
		"https://203.0.113.9":             "",
		"":                                "",
		"not a url at all":                "",
		"https://":                        "",
	} {
		if got := HostOfURL(in); got != want {
			t.Errorf("HostOfURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPanelDomainFollowsTheAddressThePanelIsServedOn(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)

	// Without PANEL_PUBLIC_URL nothing is imposed: the settings keep what they hold.
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "typed.example.com"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	if err := svc.SyncPanelDomain(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetSettings(ctx); got.PanelDomain != "typed.example.com" {
		t.Fatalf("%q", got.PanelDomain)
	}

	// With it, the stale value (an imported or typed one) is replaced at start, and a form cannot bring it back.
	svc.PanelHost = "mod.example.org"
	if err := svc.SyncPanelDomain(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetSettings(ctx)
	if got.PanelDomain != "mod.example.org" {
		t.Fatalf("set at start: %q", got.PanelDomain)
	}
	got.PanelDomain = "testtest.example.com"
	saved, err := svc.PutSettings(ctx, *got)
	if err != nil || saved.PanelDomain != "mod.example.org" {
		t.Fatalf("a saved form cannot change it: %+v %v", saved, err)
	}
	if again, _ := svc.GetSettings(ctx); again.PanelDomain != "mod.example.org" {
		t.Fatalf("%q", again.PanelDomain)
	}
}

func TestFullImportBringsNoPanelDomainWhenThePanelKnowsItsOwn(t *testing.T) {
	ctx := context.Background()
	src, _ := seeded(t) // panel.example.com
	_, data, err := src.ExportDB(ctx, ExportFull)
	if err != nil {
		t.Fatal(err)
	}
	dst, _, dstStore := newService(t)
	dst.PanelHost = "mod.example.org" // the settings of this fresh panel are still empty
	if _, err := dst.ImportDB(ctx, data, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	after, _ := dstStore.Snapshot(ctx)
	if after.Settings.PanelDomain != "mod.example.org" {
		t.Fatalf("the imported domain is not taken over: %q", after.Settings.PanelDomain)
	}
}
