package panel

import (
	"context"
	"testing"

	"anypanel2/internal/model"
)

func TestAutoNodeSettingsDefaultsAndValidation(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	set, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := set.Subscription.AutoNode
	if a.Enable || a.IntervalSec != 10 || a.TimeoutSec != 3 || a.Name == "" || a.ProbeURL == "" {
		t.Fatalf("a never-saved block gets usable defaults and stays off: %+v", a)
	}

	set.Subscription.AutoNode.Enable = true
	set.Subscription.AutoNode.IntervalSec, set.Subscription.AutoNode.TimeoutSec = 5, 2
	saved, err := svc.PutSettings(ctx, *set)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Subscription.AutoNode; !got.Enable || got.IntervalSec != 5 || got.TimeoutSec != 2 {
		t.Fatalf("saved: %+v", got)
	}

	bad := map[string]func(*model.AutoNodeSettings){
		"interval too short": func(a *model.AutoNodeSettings) { a.IntervalSec = 1 },
		"interval too long":  func(a *model.AutoNodeSettings) { a.IntervalSec = 4000 },
		"timeout zero":       func(a *model.AutoNodeSettings) { a.TimeoutSec = 0 },
		"timeout over":       func(a *model.AutoNodeSettings) { a.IntervalSec, a.TimeoutSec = 5, 6 },
		"tolerance":          func(a *model.AutoNodeSettings) { a.ToleranceMs = -1 },
		"probe address":      func(a *model.AutoNodeSettings) { a.ProbeURL = "ftp://x/" },
		"multi-line name":    func(a *model.AutoNodeSettings) { a.Name = "a\nb" },
		"name too long":      func(a *model.AutoNodeSettings) { a.Name = string(make([]rune, 61)) + "x" },
	}
	for name, mut := range bad {
		s, _ := svc.GetSettings(ctx)
		s.Subscription.AutoNode = model.AutoNodeSettings{Enable: true, Name: "x", ProbeURL: "https://x/", IntervalSec: 10, TimeoutSec: 3}
		mut(&s.Subscription.AutoNode)
		if _, err := svc.PutSettings(ctx, *s); !isInvalid(err) {
			t.Errorf("%s: want a validation error, got %v", name, err)
		}
	}

	// An empty name and address fall back to the defaults instead of failing.
	s, _ := svc.GetSettings(ctx)
	s.Subscription.AutoNode = model.AutoNodeSettings{Enable: true, IntervalSec: 10, TimeoutSec: 3}
	got, err := svc.PutSettings(ctx, *s)
	if err != nil || got.Subscription.AutoNode.Name != model.DefaultAutoNodeName || got.Subscription.AutoNode.ProbeURL != model.DefaultAutoNodeProbeURL {
		t.Fatalf("defaults for empty text: %+v %v", got.Subscription.AutoNode, err)
	}
}

func TestExcludeFromAutoReachesTheEndpoint(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.com"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	a, b := wsInbound(10001), wsInbound(10002)
	b.Remark, b.ExcludeFromAuto = "b", true
	ia, err := svc.CreateInbound(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	ib, err := svc.CreateInbound(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := svc.CreateClient(ctx, model.Client{Email: "ann", Enable: true, InboundIDs: []string{ia.ID, ib.ID}})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := svc.PublicSubscription(ctx, cl.SubID)
	if err != nil || len(sub.Endpoints) != 2 {
		t.Fatalf("%v %d", err, len(sub.Endpoints))
	}
	n := 0
	for _, e := range sub.Endpoints {
		if e.NoAuto {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the flag must travel from exactly one inbound to its endpoint, got %d", n)
	}
}

func TestAppPreviewListsTheAutoNode(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	set, _ := svc.GetSettings(ctx)
	subs := set.Subscription

	off, err := svc.PreviewApp(ctx, subs, "active")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range off.Servers {
		if s.Protocol == "auto" {
			t.Fatal("no automatic node while the feature is off")
		}
	}

	subs.AutoNode.Enable = true
	first, _ := svc.PreviewApp(ctx, subs, "active")
	if len(first.Servers) != len(off.Servers)+1 || first.Servers[0].Protocol != "auto" || first.Servers[0].Name != model.DefaultAutoNodeName {
		t.Fatalf("the automatic node leads the list: %+v", first.Servers)
	}
	subs.AutoNode.Last, subs.AutoNode.Name = true, "Auto"
	last, _ := svc.PreviewApp(ctx, subs, "active")
	if got := last.Servers[len(last.Servers)-1]; got.Protocol != "auto" || got.Name != "Auto" {
		t.Fatalf("Last puts it after the servers: %+v", last.Servers)
	}
	subs.InfoNode = true // only then does an expired subscriber see nothing but the info node
	exp, _ := svc.PreviewApp(ctx, subs, "expired")
	for _, s := range exp.Servers {
		if s.Protocol == "auto" {
			t.Fatal("an expired subscriber sees only the info node")
		}
	}
}
