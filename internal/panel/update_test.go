package panel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/update"
)

func releaseServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `","body":"что нового","html_url":"https://example.com/r","published_at":"2026-11-02T15:00:00Z"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckUpdateComparesWithTheRunningVersion(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	if _, err := svc.CheckUpdate(ctx, false); err == nil {
		t.Fatal("without an updater the feature is off")
	}
	srv := releaseServer(t, "v0.33.0")
	svc.Update = &update.Updater{Repo: "o/r", APIBase: srv.URL, Dir: t.TempDir()}

	svc.Version = "0.32.1"
	info, err := svc.CheckUpdate(ctx, true)
	if err != nil || !info.Newer || !info.CanApply || info.Latest != "0.33.0" || info.Current != "0.32.1" || info.Notes != "что нового" || info.Published == nil {
		t.Fatalf("%+v %v", info, err)
	}
	svc.Version = "0.33.0"
	if info, _ := svc.CheckUpdate(ctx, false); info.Newer || info.CanApply {
		t.Fatalf("the same version is not an update: %+v", info)
	}
	svc.Version = "dev"
	if info, _ := svc.CheckUpdate(ctx, false); info.CanApply || info.Reason == "" {
		t.Fatalf("a development build cannot compare: %+v", info)
	}
}

func TestApplyUpdateOnlyInstallsTheVersionTheAdminSaw(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	srv := releaseServer(t, "v0.33.0")
	svc.Update = &update.Updater{Repo: "o/r", APIBase: srv.URL, Dir: t.TempDir()}
	svc.Version = "0.33.0"
	if err := svc.ApplyUpdate(ctx, "0.33.0"); !isInvalid(err) {
		t.Fatalf("nothing newer: %v", err)
	}
	svc.Version = "0.32.1"
	if err := svc.ApplyUpdate(ctx, "0.34.0"); !isInvalid(err) {
		t.Fatalf("a version other than the latest one: %v", err)
	}
	if st := svc.Update.Status(); st.State != "idle" {
		t.Fatalf("nothing started: %+v", st)
	}
}

func TestGeneratedRegRuCertificateIsAUsableClientPair(t *testing.T) {
	svc, _, _ := newService(t)
	c, err := svc.GenerateRegRuCertificate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Certificate, "-----BEGIN CERTIFICATE-----") || !strings.HasPrefix(c.Key, "-----BEGIN PRIVATE KEY-----") {
		t.Fatalf("PEM, as the settings and REG.RU expect:\n%.60s\n%.60s", c.Certificate, c.Key)
	}
	if strings.Contains(c.Certificate, "PRIVATE") {
		t.Fatal("the certificate part must not carry the key")
	}
	pair, err := tls.X509KeyPair([]byte(c.Certificate), []byte(c.Key))
	if err != nil {
		t.Fatalf("the pair must load the way the REG.RU client loads it: %v", err)
	}
	block, _ := pem.Decode([]byte(c.Certificate))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	hasClient := false
	for _, u := range cert.ExtKeyUsage {
		hasClient = hasClient || u == x509.ExtKeyUsageClientAuth
	}
	if !hasClient || cert.Subject.CommonName == "" || cert.NotAfter.Sub(cert.NotBefore) < 9*365*24*time.Hour || len(pair.Certificate) != 1 {
		t.Fatalf("a client certificate valid for years: %+v", cert)
	}
	again, _ := svc.GenerateRegRuCertificate()
	if again.Key == c.Key {
		t.Fatal("every call makes a new key")
	}
}
