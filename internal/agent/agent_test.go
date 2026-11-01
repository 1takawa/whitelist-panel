package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"anypanel2/internal/xray"
)

type fakeRunner struct {
	mu        sync.Mutex
	applied   [][]byte
	checkErr  error
	notReady  bool
	restarted int
	stats     map[string]xray.Traffic
	resets    []bool
}

func (f *fakeRunner) Installed() bool { return !f.notReady }
func (f *fakeRunner) Check(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkErr
}
func (f *fakeRunner) Apply(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checkErr != nil {
		return f.checkErr
	}
	f.applied = append(f.applied, cfg)
	return nil
}
func (f *fakeRunner) Unchanged(_ context.Context, cfg []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applied) > 0 && string(f.applied[len(f.applied)-1]) == string(cfg)
}
func (f *fakeRunner) Restart(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted++
	return nil
}
func (f *fakeRunner) Status(context.Context) xray.Status {
	return xray.Status{Installed: !f.notReady, Running: true, Version: "26.1.1"}
}
func (f *fakeRunner) QueryStats(_ context.Context, reset bool) (map[string]xray.Traffic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets = append(f.resets, reset)
	out := f.stats
	if reset {
		f.stats = nil
	}
	return out, nil
}

const token = "node-token-0123456789abcdef0123456789abcdef"

// start runs a real TLS agent on a loopback port with a generated certificate.
func start(t *testing.T) (*Server, *fakeRunner, int, string) {
	t.Helper()
	r := &fakeRunner{}
	s := &Server{Token: token, Xray: r, Version: "9.9.9"}
	cert, err := LoadOrCreateCert(t.TempDir(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.Handler(), TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	go srv.ServeTLS(ln, "", "")
	t.Cleanup(func() { _ = srv.Close() })
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return s, r, p, CertFingerprint(cert)
}

func TestApplyStatsRestartRoundTrip(t *testing.T) {
	_, r, port, fp := start(t)
	c := NewClient("127.0.0.1", port, token, fp)
	ctx := context.Background()

	st, err := c.Status(ctx)
	if err != nil || st.Version != "9.9.9" || !st.Xray.Running || st.Xray.Version != "26.1.1" {
		t.Fatalf("%+v %v", st, err)
	}
	if err := c.Check(ctx, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, []byte(`{"inbounds":[]}`)); err != nil {
		t.Fatal(err)
	}
	if len(r.applied) != 1 || string(r.applied[0]) != `{"inbounds":[]}` {
		t.Fatalf("the config arrives unchanged: %q", r.applied)
	}
	r.stats = map[string]xray.Traffic{"alice": {Up: 5, Down: 7}}
	got, err := c.Stats(ctx, true)
	if err != nil || got["alice"] != (xray.Traffic{Up: 5, Down: 7}) {
		t.Fatalf("%+v %v", got, err)
	}
	if again, _ := c.Stats(ctx, true); len(again) != 0 {
		t.Fatalf("counters were reset by the first read: %+v", again)
	}
	if len(r.resets) != 2 || !r.resets[0] {
		t.Fatalf("reset flag is passed through: %v", r.resets)
	}
	if err := c.Restart(ctx); err != nil || r.restarted != 1 {
		t.Fatalf("%v %d", err, r.restarted)
	}
}

func TestWrongTokenAndMissingToken(t *testing.T) {
	_, r, port, fp := start(t)
	ctx := context.Background()
	bad := NewClient("127.0.0.1", port, token+"x", fp)
	if _, err := bad.Status(ctx); !IsUnauthorized(err) {
		t.Fatalf("wrong token: %v", err)
	}
	if err := bad.Apply(ctx, []byte(`{}`)); !IsUnauthorized(err) || len(r.applied) != 0 {
		t.Fatalf("a wrong token must not apply anything: %v %d", err, len(r.applied))
	}
	if _, err := NewClient("127.0.0.1", port, "", fp).Status(ctx); !IsUnauthorized(err) {
		t.Fatalf("empty token: %v", err)
	}
}

func TestEmptyServerTokenRejectsEverything(t *testing.T) {
	s, _, port, fp := start(t)
	s.Token = ""
	if _, err := NewClient("127.0.0.1", port, "", fp).Status(context.Background()); !IsUnauthorized(err) {
		t.Fatalf("an agent without a configured token must be closed, not open: %v", err)
	}
}

func TestCertificatePinning(t *testing.T) {
	_, _, port, fp := start(t)
	ctx := context.Background()

	wrong := NewClient("127.0.0.1", port, token, strings.Repeat("ab", 32))
	if _, err := wrong.Status(ctx); err == nil || !strings.Contains(err.Error(), "pinned fingerprint") {
		t.Fatalf("a different certificate must be refused: %v", err)
	}

	// Trust on first use: no pin yet, the observed value is available to be saved.
	first := NewClient("127.0.0.1", port, token, "")
	if _, err := first.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if first.ObservedFingerprint() != fp {
		t.Fatalf("observed %q, want %q", first.ObservedFingerprint(), fp)
	}
	// The pin is case-insensitive.
	if _, err := NewClient("127.0.0.1", port, token, strings.ToUpper(fp)).Status(ctx); err != nil {
		t.Fatalf("upper-case pin: %v", err)
	}
}

func TestErrorsFromXrayReachThePanel(t *testing.T) {
	_, r, port, fp := start(t)
	c := NewClient("127.0.0.1", port, token, fp)
	ctx := context.Background()

	r.checkErr = errors.New("unknown field: boom")
	if err := c.Apply(ctx, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("%v", err)
	}
	if err := c.Check(ctx, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("%v", err)
	}
	r.checkErr = nil
	r.notReady = true
	if err := c.Apply(ctx, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("%v", err)
	}
	r.notReady = false
	if err := c.Apply(ctx, []byte(`not json`)); err == nil {
		t.Fatal("a body that is not JSON is refused before Xray sees it")
	}
}

func TestUnreachableAgent(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	port, _ := strconv.Atoi(p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := NewClient("127.0.0.1", port, token, "").Status(ctx); err == nil {
		t.Fatal("connection refused must be an error")
	}
}

func TestCertIsReusedAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreateCert(dir, "node.example.com", "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if CertFingerprint(a) != CertFingerprint(b) || CertFingerprint(a) == "" {
		t.Fatal("the pinned certificate must survive an agent restart")
	}
}

func TestResendingTheSameConfigDoesNotRestartXray(t *testing.T) {
	_, r, port, fp := start(t)
	c := NewClient("127.0.0.1", port, token, fp)
	cfg := []byte(`{"inbounds":[1]}`)
	if err := c.Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(r.applied) != 1 {
		t.Fatalf("an identical config is acknowledged without being applied again: %d applies", len(r.applied))
	}
	if err := c.Apply(context.Background(), []byte(`{"inbounds":[2]}`)); err != nil || len(r.applied) != 2 {
		t.Fatalf("a changed config is applied: %v %d", err, len(r.applied))
	}
}
