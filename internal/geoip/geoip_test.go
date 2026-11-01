package geoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCountryReadsTheLookupAnswer(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		_, _ = w.Write([]byte(`{"success":true,"country_code":"nl"}`))
	}))
	defer srv.Close()
	old := endpoint
	endpoint = srv.URL + "/"
	defer func() { endpoint = old }()

	got, err := Country(context.Background(), "203.0.113.77")
	if err != nil || got != "NL" {
		t.Fatalf("got %q, %v", got, err)
	}
	if asked != "/203.0.113.77" {
		t.Fatalf("the server's IP is what is asked about: %q", asked)
	}
}

func TestCountryRefusesPrivateAddressesAndBadAnswers(t *testing.T) {
	if _, err := Country(context.Background(), "10.0.0.5"); err == nil {
		t.Fatal("a private address has no country")
	}
	if _, err := Country(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("loopback has no country")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false}`))
	}))
	defer srv.Close()
	old := endpoint
	endpoint = srv.URL + "/"
	defer func() { endpoint = old }()
	if _, err := Country(context.Background(), "8.8.8.8"); err == nil {
		t.Fatal("an unsuccessful answer is an error")
	}
}
