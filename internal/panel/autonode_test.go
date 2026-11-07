package panel

import (
	"context"
	"strings"
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

func TestDraggedOrderSetsTheSubscriptionOrderAndTheAutoNodePlace(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.com"
	set.Subscription.AutoNode = model.AutoNodeSettings{Enable: true, Name: "A", ProbeURL: "https://x/", IntervalSec: 5, TimeoutSec: 2}
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i, remark := range []string{"one", "two", "three"} {
		in := wsInbound(10001 + i)
		in.Remark = remark
		v, err := svc.CreateInbound(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	one, two, three := ids[0], ids[1], ids[2]
	remarks := func() []string {
		cl, err := svc.CreateClient(ctx, model.Client{Email: "u" + t.Name() + string(rune('a'+len(ids))), Enable: true, InboundIDs: ids})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = svc.DeleteClient(ctx, cl.ID) }()
		sub, err := svc.PublicSubscription(ctx, cl.SubID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range sub.Links {
			out = append(out, l.Remark)
		}
		return out
	}
	if got := strings.Join(remarks(), ","); got != "one,two,three" {
		t.Fatalf("new connections go to the end of the list: %s", got)
	}

	if err := svc.ReorderInbounds(ctx, []string{three, AutoNodeSlot, one, two}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(remarks(), ","); got != "three,one,two" {
		t.Fatalf("the dragged order is the subscription order: %s", got)
	}
	got, _ := svc.GetSettings(ctx)
	if a := got.Subscription.AutoNode; a.Position != 1 || a.Last {
		t.Fatalf("the automatic node sits between the first and the second: %+v", a)
	}
	if err := svc.ReorderInbounds(ctx, []string{two, one, three, AutoNodeSlot}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetSettings(ctx); !got.Subscription.AutoNode.Last || got.Subscription.AutoNode.Position != 3 {
		t.Fatalf("dragged to the end: %+v", got.Subscription.AutoNode)
	}

	// A new connection still goes to the end of the list.
	in := wsInbound(10009)
	in.Remark = "four"
	v, err := svc.CreateInbound(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, v.ID)
	if got := strings.Join(remarks(), ","); got != "two,one,three,four" {
		t.Fatalf("%s", got)
	}

	for name, bad := range map[string][]string{
		"missing":   {two, one},
		"unknown":   {two, one, three, ids[3], "nope"},
		"duplicate": {two, two, one, three, ids[3]},
	} {
		if err := svc.ReorderInbounds(ctx, bad); !isInvalid(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestAutoNodeGroupsAreValidatedAndOrdered(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.com"
	var ids []string
	for i, remark := range []string{"one", "two", "three"} {
		in := wsInbound(10101 + i)
		in.Remark = remark
		v, err := svc.CreateInbound(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	bad := func(g model.AutoGroup, why string) {
		t.Helper()
		s := *set
		s.Subscription.AutoNode.Groups = []model.AutoGroup{g}
		if _, err := svc.PutSettings(ctx, s); err == nil {
			t.Errorf("%s must be refused", why)
		}
	}
	bad(model.AutoGroup{Name: "x", Country: "RUS"}, "a flag that is not a two-letter code")
	bad(model.AutoGroup{Name: "a\nb"}, "a name on two lines")
	bad(model.AutoGroup{Name: strings.Repeat("я", 61)}, "a long name")
	s := *set
	s.Subscription.AutoNode.Groups = make([]model.AutoGroup, 11)
	if _, err := svc.PutSettings(ctx, s); err == nil {
		t.Error("more than ten groups must be refused")
	}

	s = *set
	s.Subscription.AutoNode.Groups = []model.AutoGroup{{Name: "  Авто РУ ", Country: "ru", Enable: true, Members: []string{ids[0], ids[2], ids[0], " "}}, {Name: "", Enable: true}}
	saved, err := svc.PutSettings(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	gs := saved.Subscription.AutoNode.Groups
	if len(gs) != 2 || gs[0].ID == "" || gs[0].ID == gs[1].ID || gs[0].Name != "Авто РУ" || gs[0].Country != "RU" || len(gs[0].Members) != 2 || gs[1].Name == "" || !gs[0].Last {
		t.Fatalf("%+v", gs)
	}
	if gs[0].DisplayName() != "🇷🇺 Авто РУ" {
		t.Fatalf("flag then name: %q", gs[0].DisplayName())
	}

	g0, g1 := AutoNodeSlot+":"+gs[0].ID, AutoNodeSlot+":"+gs[1].ID
	if err := svc.ReorderInbounds(ctx, []string{ids[0], g1, ids[1], g0, ids[2]}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetSettings(ctx)
	a, b := got.Subscription.AutoNode.Groups[0], got.Subscription.AutoNode.Groups[1]
	if a.Position != 3 || a.Last || b.Position != 1 || b.Last {
		t.Fatalf("the groups take the places they were dragged to: %+v %+v", a, b)
	}
	if err := svc.ReorderInbounds(ctx, []string{g1, ids[0], ids[1], ids[2], g0}); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.GetSettings(ctx)
	if a, b := got.Subscription.AutoNode.Groups[0], got.Subscription.AutoNode.Groups[1]; !a.Last || b.Last || b.Position != 0 {
		t.Fatalf("%+v %+v", a, b)
	}
	if err := svc.ReorderInbounds(ctx, []string{AutoNodeSlot + ":nope", ids[0], ids[1], ids[2]}); err == nil {
		t.Error("an unknown group must be refused")
	}
}

func TestConnectionRoutingIsCheckedAndReachesTheEndpoint(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	set, _ := svc.GetSettings(ctx)
	set.PanelDomain = "panel.example.com"
	if _, err := svc.PutSettings(ctx, *set); err != nil {
		t.Fatal(err)
	}
	bad := wsInbound(10201)
	bad.ClientRouting = "everything"
	if _, err := svc.CreateInbound(ctx, bad); !isInvalid(err) {
		t.Fatalf("an unknown routing is refused: %v", err)
	}
	for i, id := range []string{"", "none", "default", "whitelist", "whitelist_ru"} {
		in := wsInbound(10210 + i)
		in.Remark = "r" + id
		in.ClientRouting = id
		if _, err := svc.CreateInbound(ctx, in); err != nil {
			t.Fatalf("%q: %v", id, err)
		}
	}
	list, _ := svc.ListInbounds(ctx)
	cl, err := svc.CreateClient(ctx, model.Client{Email: "routing", Enable: true, InboundIDs: func() []string {
		var ids []string
		for _, in := range list {
			ids = append(ids, in.ID)
		}
		return ids
	}()})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := svc.PublicSubscription(ctx, cl.SubID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range sub.Endpoints {
		got[strings.TrimSuffix(e.Name, "-routing")] = e.Routing
	}
	if len(got) != 5 || got["rwhitelist"] != "whitelist" || got["rnone"] != "none" || got["r"] != "" {
		t.Fatalf("the endpoints carry the routing of their connection: %v", got)
	}
}
