package auth

import (
	"context"
	"testing"
	"time"

	"anypanel2/internal/store"
	"anypanel2/internal/store/memstore"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword("correct horse battery", h); err != nil || !ok {
		t.Fatalf("expected match, got ok=%v err=%v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong", h); ok {
		t.Fatal("wrong password must not verify")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
}

func TestVerifyRejectsMalformedAndHostileHashes(t *testing.T) {
	for _, bad := range []string{
		"", "plain", "$argon2id$v=19$m=1,t=1,p=1$AAAA$AAAA",
		"$argon2id$v=19$m=999999999,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAA", // memory bomb
		"$bcrypt$v=19$m=65536,t=3,p=2$AAAA$AAAA",
	} {
		if ok, err := VerifyPassword("x", bad); ok || err == nil {
			t.Errorf("hash %q: want error, got ok=%v err=%v", bad, ok, err)
		}
	}
}

func TestLimiterWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	if !l.Allow("a") || !l.Allow("a") {
		t.Fatal("first two attempts must pass")
	}
	if l.Allow("a") {
		t.Fatal("third attempt must be blocked")
	}
	if !l.Allow("b") {
		t.Fatal("other key must be independent")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("a") {
		t.Fatal("attempt after the window must pass")
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	hash, _ := HashPassword("s3cret-pass-123")
	_ = st.PutAdmin(ctx, store.Admin{Username: "admin", PasswordHash: hash})

	m, err := NewManager(st, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(5000, 0)
	m.Now = func() time.Time { return now }

	if _, _, err := m.Login(ctx, "admin", "nope", "1.1.1.1"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password: got %v", err)
	}
	if _, _, err := m.Login(ctx, "ghost", "s3cret-pass-123", "1.1.1.1"); err != ErrInvalidCredentials {
		t.Fatalf("unknown user: got %v", err)
	}
	tok, _, err := m.Login(ctx, "admin", "s3cret-pass-123", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if u, err := m.Authenticate(ctx, tok); err != nil || u != "admin" {
		t.Fatalf("authenticate: user=%q err=%v", u, err)
	}
	if _, err := m.Store.GetSession(ctx, tok); err == nil {
		t.Fatal("raw token must not be a storage key")
	}

	now = now.Add(2 * time.Hour)
	if _, err := m.Authenticate(ctx, tok); err != ErrNoSession {
		t.Fatalf("expired session: got %v", err)
	}

	now = time.Unix(5000, 0)
	tok2, _, _ := m.Login(ctx, "admin", "s3cret-pass-123", "1.1.1.1")
	_ = m.Logout(ctx, tok2)
	if _, err := m.Authenticate(ctx, tok2); err != ErrNoSession {
		t.Fatalf("after logout: got %v", err)
	}
}
