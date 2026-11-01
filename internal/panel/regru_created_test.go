package panel

import (
	"context"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/model"
)

func (f *fakeRegRu) has(sub string) (content string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rrs {
		if r["subname"] == sub {
			return r["content"], true
		}
	}
	return "", false
}

// generated makes a name through the panel, as the "Создать" button does, and returns it with its subdomain part.
func generated(t *testing.T, svc *Service, ip string) (domain, sub string) {
	t.Helper()
	g, err := svc.GenerateRegRuDomain(context.Background(), RegRuGenerateRequest{Host: ip})
	if err != nil {
		t.Fatal(err)
	}
	return g.Domain, strings.TrimSuffix(g.Domain, ".example.test")
}

func createdCount(t *testing.T, svc *Service) int {
	t.Helper()
	set, err := svc.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(set.RegRu.Created)
}

func TestDeletingANodeRemovesTheDomainThePanelMadeForIt(t *testing.T) {
	ctx := context.Background()
	svc, f := regruSetup(t)
	connect(t, svc)

	domain, sub := generated(t, svc, "198.51.100.7")
	if createdCount(t, svc) != 1 {
		t.Fatal("a created name is remembered")
	}
	if err := svc.Store.PutServer(ctx, model.Server{ID: "node-1", Name: "Node", Address: "198.51.100.7", Port: 8765, Enable: true}); err != nil {
		t.Fatal(err)
	}
	svc.regruAssign(ctx, domain, "node-1", "")
	svc.regruAssign(ctx, "old.example.test", "node-1", "") // not created by the panel: nothing is recorded for it

	if err := svc.DeleteServer(ctx, "node-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.has(sub); ok {
		t.Fatalf("the record of %s must go with the node", domain)
	}
	if _, ok := f.has("old"); !ok {
		t.Fatal("a name the panel did not create must stay")
	}
	if createdCount(t, svc) != 0 {
		t.Fatal("a removed name is forgotten")
	}
}

func TestADomainStaysWhileSomethingStillUsesItOrSomeoneRepointedIt(t *testing.T) {
	ctx := context.Background()
	svc, f := regruSetup(t)
	connect(t, svc)

	// Repointed by hand: the record is not the panel's any more, it is left alone and forgotten.
	d1, sub1 := generated(t, svc, "198.51.100.7")
	f.mu.Lock()
	for _, r := range f.rrs {
		if r["subname"] == sub1 {
			r["content"] = "203.0.113.99"
		}
	}
	f.mu.Unlock()
	_ = svc.Store.PutServer(ctx, model.Server{ID: "n1", Name: "N1", Address: "198.51.100.7", Port: 8765, Enable: true})
	svc.regruAssign(ctx, d1, "n1", "")
	if err := svc.DeleteServer(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	if c, ok := f.has(sub1); !ok || c != "203.0.113.99" {
		t.Fatalf("a repointed record must stay: %q %v", c, ok)
	}
	if createdCount(t, svc) != 0 {
		t.Fatal("and it is forgotten")
	}

	// Another connection still uses the name: nothing is removed.
	d2, sub2 := generated(t, svc, "198.51.100.8")
	_ = svc.Store.PutServer(ctx, model.Server{ID: "n2", Name: "N2", Address: "198.51.100.8", Port: 8765, Enable: true})
	svc.regruAssign(ctx, d2, "n2", "")
	in := model.Inbound{ID: "i1", Remark: "uses it", ServerID: "elsewhere"}
	in.Reality.ServerNames = []string{strings.ToUpper(d2)}
	if err := svc.Store.PutInbound(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteServer(ctx, "n2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.has(sub2); !ok {
		t.Fatal("a name that a connection still uses must stay")
	}
}

func TestADeletedCascadeTakesItsDomainAlong(t *testing.T) {
	ctx := context.Background()
	svc, f := regruSetup(t)
	connect(t, svc)
	domain, sub := generated(t, svc, "198.51.100.7")
	svc.regruAssign(ctx, domain, "", "cascade-1") // the cascade does not exist (any more)
	svc.regruReleaseOwned(ctx, "", "cascade-1")
	if _, ok := f.has(sub); ok || createdCount(t, svc) != 0 {
		t.Fatal("the cascade's name leaves the zone")
	}
}

func TestAbandonedDomainsAreOfferedForCleanup(t *testing.T) {
	ctx := context.Background()
	svc, f := regruSetup(t)
	connect(t, svc)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	fresh, freshSub := generated(t, svc, "198.51.100.7")
	st, _ := svc.RegRuStatus(ctx)
	if len(st.Unused) != 0 {
		t.Fatalf("a name made a minute ago belongs to the wizard that is still open: %v", st.Unused)
	}
	if res, err := svc.CleanupRegRu(ctx); err != nil || len(res.Removed) != 0 {
		t.Fatalf("cleanup must not touch it: %+v %v", res, err)
	}

	now = now.Add(time.Hour)
	st, _ = svc.RegRuStatus(ctx)
	if len(st.Unused) != 1 || st.Unused[0] != fresh {
		t.Fatalf("an hour later it is abandoned: %v", st.Unused)
	}
	res, err := svc.CleanupRegRu(ctx)
	if err != nil || len(res.Removed) != 1 || len(res.Problems) != 0 {
		t.Fatalf("cleanup: %+v %v", res, err)
	}
	if _, ok := f.has(freshSub); ok {
		t.Fatal("the abandoned record is removed")
	}
	if st, _ = svc.RegRuStatus(ctx); len(st.Unused) != 0 {
		t.Fatalf("nothing left: %v", st.Unused)
	}

	// Without a connection there is nothing to clean with.
	_ = svc.DisconnectRegRu(ctx)
	if _, err := svc.CleanupRegRu(ctx); !isInvalid(err) {
		t.Fatalf("cleanup needs the connection: %v", err)
	}
}

func TestSeveralZones(t *testing.T) {
	ctx := context.Background()
	svc, f := regruSetup(t)
	f.other = map[string][]map[string]string{"example.org": {{"subname": "web", "rectype": "A", "content": "198.51.100.40"}}}

	if _, err := svc.SaveRegRu(ctx, RegRuSave{Username: "me", Password: "secret", Zone: "example.test", ExtraZones: []string{"not a domain"}}); !isInvalid(err) {
		t.Fatalf("a bad extra domain is refused: %v", err)
	}
	st, err := svc.SaveRegRu(ctx, RegRuSave{Username: "me", Password: "secret", Zone: "example.test", ExtraZones: []string{"Example.Org", "example.test", "example.org", "broken.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected || len(st.ExtraZones) != 2 || st.ExtraZones[0] != "example.org" || st.ExtraZones[1] != "broken.test" {
		t.Fatalf("extra zones are normalized and de-duplicated: %+v", st)
	}
	if len(st.ZoneProblems) != 1 || !strings.HasPrefix(st.ZoneProblems[0], "broken.test: ") {
		t.Fatalf("the zone REG.RU refuses is reported next to the connection: %v", st.ZoneProblems)
	}

	// The list covers every zone that can be read; the broken one does not hide the others.
	doms, err := svc.RegRuDomains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range doms {
		names = append(names, d.Name)
	}
	if got := strings.Join(names, " "); got != "old.example.test panel.example.test web.example.org" {
		t.Fatalf("domains of all zones: %q", got)
	}

	// A name is created in the chosen zone and goes away with its node from that zone.
	g, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "198.51.100.7", Zone: "example.org"})
	if err != nil || !strings.HasSuffix(g.Domain, ".example.org") {
		t.Fatalf("generate in the second zone: %+v %v", g, err)
	}
	sub := strings.TrimSuffix(g.Domain, ".example.org")
	if _, ok := f.other["example.org"][len(f.other["example.org"])-1]["subname"]; !ok || f.other["example.org"][len(f.other["example.org"])-1]["subname"] != sub {
		t.Fatalf("the record is in the second zone: %v", f.other)
	}
	if _, err := svc.GenerateRegRuDomain(ctx, RegRuGenerateRequest{Host: "198.51.100.7", Zone: "unknown.test"}); !isInvalid(err) {
		t.Fatalf("a zone that is not connected: %v", err)
	}
	d1, sub1 := generated(t, svc, "198.51.100.8") // the default zone
	_ = svc.Store.PutServer(ctx, model.Server{ID: "a", Name: "A", Address: "198.51.100.7", Port: 8765, Enable: true})
	_ = svc.Store.PutServer(ctx, model.Server{ID: "b", Name: "B", Address: "198.51.100.8", Port: 8765, Enable: true})
	svc.regruAssign(ctx, g.Domain, "a", "")
	svc.regruAssign(ctx, d1, "b", "")
	if note, err := svc.DeleteServerNote(ctx, "a"); err != nil || note != "" {
		t.Fatalf("delete a: %q %v", note, err)
	}
	for _, r := range f.other["example.org"] {
		if r["subname"] == sub {
			t.Fatal("the record leaves the second zone")
		}
	}
	if _, ok := f.has(sub1); !ok {
		t.Fatal("the other zone's record is untouched until its own node goes")
	}
	if note, err := svc.DeleteServerNote(ctx, "b"); err != nil || note != "" {
		t.Fatalf("delete b: %q %v", note, err)
	}
	if _, ok := f.has(sub1); ok {
		t.Fatal("and then it goes too")
	}
}

func TestDeleteReportsARecordThatCouldNotBeRemoved(t *testing.T) {
	ctx := context.Background()
	svc, f := regruSetup(t)
	connect(t, svc)
	domain, sub := generated(t, svc, "198.51.100.7")
	_ = svc.Store.PutServer(ctx, model.Server{ID: "n", Name: "N", Address: "198.51.100.7", Port: 8765, Enable: true})
	svc.regruAssign(ctx, domain, "n", "")

	f.mu.Lock()
	f.deny = true // REG.RU refuses this server right when the node is deleted
	f.mu.Unlock()
	note, err := svc.DeleteServerNote(ctx, "n")
	if err != nil {
		t.Fatalf("the node is deleted anyway: %v", err)
	}
	if !strings.Contains(note, "не удалена") || !strings.Contains(note, "разрешённые IP") {
		t.Fatalf("the user is told what was left and why: %q", note)
	}
	if _, ok := f.has(sub); !ok || createdCount(t, svc) != 1 {
		t.Fatal("the record stays, and the name stays remembered for the cleanup button")
	}

	f.mu.Lock()
	f.deny = false
	f.mu.Unlock()
	st, _ := svc.RegRuStatus(ctx)
	if len(st.Unused) != 1 || st.Unused[0] != domain {
		t.Fatalf("it is offered for cleanup: %v", st.Unused)
	}
	if res, err := svc.CleanupRegRu(ctx); err != nil || len(res.Removed) != 1 {
		t.Fatalf("cleanup: %+v %v", res, err)
	}
	if _, ok := f.has(sub); ok {
		t.Fatal("removed")
	}
}
