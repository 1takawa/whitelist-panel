package panel

import (
	"context"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/legacy"
	"anypanel2/internal/model"
)

func lu(name string) legacy.User {
	return legacy.User{Name: name, Token: "tok-" + name + "-0123456789", Created: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
}

func importEnv(t *testing.T) (*Service, *fakeXray, string) {
	t.Helper()
	svc, fx, _ := newService(t)
	svc.Now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	in, err := svc.CreateInbound(context.Background(), realityInbound())
	if err != nil {
		t.Fatal(err)
	}
	return svc, fx, in.ID
}

func TestLegacyFieldMapping(t *testing.T) {
	ctx := context.Background()
	svc, fx, inID := importEnv(t)
	before := fx.count()

	alice := lu("alice")
	alice.MaxBytes, alice.Up, alice.Down, alice.ExpirationDays, alice.Note, alice.TelegramID, alice.Devices = 5<<30, 100, 200, 30, "friend", 777, 2
	bob := lu("bob")
	bob.Blocked = true
	carol := lu("carol")
	carol.MaxBytes, carol.Unlimited = 9<<30, true

	rep, err := svc.ImportLegacy(ctx, []legacy.User{alice, bob, carol}, LegacyOptions{})
	if err != nil || rep.Created != 3 || rep.Skipped != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	cls, _ := svc.ListClients(ctx)
	by := map[string]model.Client{}
	for _, c := range cls {
		by[c.Email] = c
	}

	a := by["alice"]
	wantExp := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC) // created 2026-10-08 + 30 days
	if !a.Enable || a.TotalBytes != 5<<30 || a.Up != 100 || a.Down != 200 || a.Comment != "friend" || a.TelegramID != 777 ||
		a.SubID != "tok-alice-0123456789" || a.ExpiryAt == nil || !a.ExpiryAt.Equal(wantExp) ||
		!a.CreatedAt.Equal(alice.Created) || len(a.InboundIDs) != 1 || a.InboundIDs[0] != inID {
		t.Fatalf("alice: %+v", a)
	}
	if a.UUID == "" || a.Password == "" || a.ID == "" {
		t.Fatalf("fresh credentials are generated, the legacy password is not reused: %+v", a)
	}
	if by["bob"].Enable || by["bob"].ExpiryAt != nil {
		t.Fatalf("blocked users arrive disabled, no expiry without days: %+v", by["bob"])
	}
	if by["carol"].TotalBytes != 0 {
		t.Fatalf("unlimited_user wins over a stored quota: %+v", by["carol"])
	}
	if rep.NotMigrated["привязанные устройства (HWID)"] != 1 {
		t.Fatalf("what is not migrated is reported: %+v", rep.NotMigrated)
	}
	if fx.count() == before {
		t.Fatal("the new clients are applied to Xray")
	}
	if !strings.Contains(fx.last(), a.UUID) {
		t.Fatal("alice's new credentials are in the running config")
	}
}

func TestLegacyExpiryFromNowAndExpiredFlag(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := importEnv(t)
	u := lu("old")
	u.ExpirationDays = 13 // 2026-10-08 + 13 = 2026-10-21; "now" in the test is 10-20 12:00
	u2 := lu("older")
	u2.ExpirationDays = 5 // 10-13: already over

	rep, _ := svc.ImportLegacy(ctx, []legacy.User{u, u2}, LegacyOptions{DryRun: true})
	if !rep.DryRun || rep.Created != 2 {
		t.Fatalf("%+v", rep)
	}
	if got := rep.Items[1].Notes; len(got) != 1 || got[0] != "срок уже истёк" {
		t.Fatalf("an already ended period is flagged: %+v", rep.Items[1])
	}
	if cls, _ := svc.ListClients(ctx); len(cls) != 0 {
		t.Fatal("a dry run saves nothing")
	}

	rep, _ = svc.ImportLegacy(ctx, []legacy.User{u2}, LegacyOptions{ExpiryFrom: "now"})
	cls, _ := svc.ListClients(ctx)
	if want := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC); cls[0].ExpiryAt == nil || !cls[0].ExpiryAt.Equal(want) || len(rep.Items[0].Notes) != 0 {
		t.Fatalf("a fresh period counted from now: %+v %+v", cls[0].ExpiryAt, rep.Items[0])
	}
}

func TestLegacyImportIsIdempotentAndHandlesConflicts(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := importEnv(t)
	first, _ := svc.ImportLegacy(ctx, []legacy.User{lu("alice"), lu("bob")}, LegacyOptions{})
	if first.Created != 2 {
		t.Fatalf("%+v", first)
	}
	again, _ := svc.ImportLegacy(ctx, []legacy.User{lu("alice"), lu("bob"), lu("dave")}, LegacyOptions{})
	if again.Created != 1 || again.Skipped != 2 || !strings.Contains(again.Items[0].Reason, "уже есть") {
		t.Fatalf("running it twice must not duplicate or overwrite: %+v", again)
	}

	// A name that only differs by case is the same client; a taken token is replaced with a fresh one.
	clash := lu("Eve")
	clash.Token = lu("alice").Token
	dupe := lu("ALICE")
	rep, _ := svc.ImportLegacy(ctx, []legacy.User{clash, dupe}, LegacyOptions{})
	if rep.Created != 1 || rep.Skipped != 1 || len(rep.Items[0].Notes) != 1 {
		t.Fatalf("%+v", rep)
	}
	cls, _ := svc.ListClients(ctx)
	seen := map[string]bool{}
	for _, c := range cls {
		if seen[c.SubID] {
			t.Fatalf("subscription ids must stay unique: %+v", cls)
		}
		seen[c.SubID] = true
	}
}

func TestLegacyNamesAreSanitisedAndBadOnesSkipped(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := importEnv(t)
	rep, _ := svc.ImportLegacy(ctx, []legacy.User{lu("Иван Петров"), lu("john doe"), lu("!!!")}, LegacyOptions{})
	cls, _ := svc.ListClients(ctx)
	if rep.Created != 1 || len(cls) != 1 || cls[0].Email != "john_doe" || len(rep.Items[1].Notes) != 2 {
		t.Fatalf("a space becomes _ and the change is reported: %+v / %+v", rep, cls)
	}
	if rep.Skipped != 2 || rep.Items[0].Action != "skip" || rep.Items[2].Action != "skip" {
		t.Fatalf("names that sanitise to nothing are skipped with a reason: %+v", rep.Items)
	}
}

func TestLegacyTargetInbounds(t *testing.T) {
	ctx := context.Background()
	svc, _, first := importEnv(t)
	off := wsInbound(10001)
	off.Enable = false
	disabled, _ := svc.CreateInbound(ctx, off)

	_, _ = svc.ImportLegacy(ctx, []legacy.User{lu("a")}, LegacyOptions{})
	cls, _ := svc.ListClients(ctx)
	if len(cls[0].InboundIDs) != 2 || !cls[0].Uses(first) || !cls[0].Uses(disabled.ID) {
		t.Fatalf("by default users join every inbound (a switched-off one is shown to nobody until it is switched on): %v", cls[0].InboundIDs)
	}
	_, _ = svc.ImportLegacy(ctx, []legacy.User{lu("b")}, LegacyOptions{InboundIDs: []string{disabled.ID}})
	if _, err := svc.ImportLegacy(ctx, []legacy.User{lu("c")}, LegacyOptions{InboundIDs: []string{"nope"}}); !isInvalid(err) {
		t.Fatalf("unknown inbound: %v", err)
	}
	if _, err := svc.ImportLegacy(ctx, nil, LegacyOptions{}); !isInvalid(err) {
		t.Fatalf("empty import: %v", err)
	}
	if _, err := svc.ImportLegacy(ctx, []legacy.User{lu("d")}, LegacyOptions{ExpiryFrom: "later"}); !isInvalid(err) {
		t.Fatalf("bad expiryFrom: %v", err)
	}
}
