package remark

import (
	"strings"
	"testing"
	"time"

	"anypanel2/internal/model"
)

var now = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

func client() model.Client {
	exp := now.Add(3*24*time.Hour + 5*time.Hour + 6*time.Minute)
	return model.Client{
		ID: "abcdef0123456789", Email: "john", SubID: "sub123", Comment: "vip", Enable: true,
		Up: 1 << 30, Down: 1 << 30, TotalBytes: 10 << 30, ExpiryAt: &exp, CreatedAt: now.Add(-24 * time.Hour),
	}
}

var inbound = &model.Inbound{Remark: "DE", Protocol: "vless", Network: "tcp", Security: "reality"}

func TestDefaultTemplate(t *testing.T) {
	got := Expand("{{INBOUND}}-{{EMAIL}}|📊{{TRAFFIC_LEFT}}|⏳{{DAYS_LEFT}}D", Context{Client: client(), Inbound: inbound, Now: now})
	if got != "DE-john|📊8.00GB|⏳3D" {
		t.Fatalf("got %q", got)
	}
}

func TestUnlimitedSegmentsAreDropped(t *testing.T) {
	c := client()
	c.TotalBytes, c.ExpiryAt = 0, nil
	got := Expand("{{INBOUND}}-{{EMAIL}}|📊{{TRAFFIC_LEFT}}|⏳{{DAYS_LEFT}}D", Context{Client: c, Inbound: inbound, Now: now})
	if got != "DE-john" {
		t.Fatalf("an unlimited client must not show ∞ segments, got %q", got)
	}
	// A segment that mixes a real value with an unlimited one is kept.
	if got := Expand("{{EMAIL}} {{TRAFFIC_LEFT}}", Context{Client: c, Now: now}); got != "john ∞" {
		t.Fatalf("mixed segment: %q", got)
	}
}

func TestHyphenSeparatorIsElided(t *testing.T) {
	c := client()
	if got := Expand("{{INBOUND}}-{{EMAIL}}", Context{Client: c, Now: now}); got != "john" {
		t.Fatalf("no inbound: %q", got)
	}
	if got := Expand("🌐{{INBOUND}}-{{EMAIL}}", Context{Client: c, Now: now}); got != "🌐john" {
		t.Fatalf("leading decoration must not keep the separator: %q", got)
	}
	if got := Expand("{{EMAIL}}-{{INBOUND}}-{{EMAIL}}", Context{Client: c, Now: now}); got != "john-john" {
		t.Fatalf("a single separator survives an empty middle token: %q", got)
	}
	if got := Expand("a-b {{EMAIL}}", Context{Client: c, Now: now}); got != "a-b john" {
		t.Fatalf("a hyphen in plain text is literal: %q", got)
	}
}

func TestTokens(t *testing.T) {
	ctx := Context{Client: client(), Inbound: inbound, Now: now, Security: "tls", Loc: time.UTC}
	for tpl, want := range map[string]string{
		"{{PROTOCOL}}":           "VLESS",
		"{{TRANSPORT}}":          "tcp",
		"{{SECURITY}}":           "TLS", // the advertised security wins over the inbound's own
		"{{TRAFFIC_USED}}":       "2.00GB",
		"{{TRAFFIC_TOTAL}}":      "10.00GB",
		"{{TRAFFIC_USED_BYTES}}": "2147483648",
		"{{TRAFFIC_LEFT_BYTES}}": "8589934592",
		"{{USAGE_PERCENTAGE}}":   "20.0％",
		"{{EXPIRE_DATE}}":        "2026-01-13",
		"{{TIME_LEFT}}":          "3d 5h 6m",
		"{{STATUS}}":             "active",
		"{{STATUS_EMOJI}}":       "✅",
		"{{SHORT_ID}}":           "abcdef01",
		"{{SUB_ID}}":             "sub123",
		"{{COMMENT}}":            "vip",
		"{{NOPE}}":               "",
	} {
		if got := Expand(tpl, ctx); got != want {
			t.Errorf("%s = %q, want %q", tpl, got, want)
		}
	}
	if got := Expand("{single} {{ lower }} {{EMAIL}", ctx); got != "{single} {{ lower }} {{EMAIL}" {
		t.Fatalf("anything that is not a {{TOKEN}} stays literal: %q", got)
	}
}

func TestStatusAndExpiry(t *testing.T) {
	c := client()
	past := now.Add(-time.Hour)
	c.ExpiryAt = &past
	ctx := Context{Client: c, Now: now}
	if Expand("{{STATUS}}", ctx) != "expired" || Expand("{{DAYS_LEFT}}", ctx) != "0" || Expand("{{TIME_LEFT}}", ctx) != "0" {
		t.Fatal("an expired client reports expired and zero time left")
	}
	c = client()
	c.Up = 20 << 30
	if got := Expand("{{STATUS}} {{TRAFFIC_LEFT}} {{USAGE_PERCENTAGE}}", Context{Client: c, Now: now}); got != "depleted 0B 100.0％" {
		t.Fatalf("depleted: %q", got)
	}
	c.Enable = false
	if Expand("{{STATUS}}", Context{Client: c, Now: now}) != "disabled" {
		t.Fatal("disabled wins")
	}
}

func TestExpireDateUsesTimeZone(t *testing.T) {
	exp := time.Date(2026, 1, 13, 22, 30, 0, 0, time.UTC)
	c := model.Client{ExpiryAt: &exp}
	msk := time.FixedZone("MSK", 3*3600)
	if got := Expand("{{EXPIRE_DATE}}", Context{Client: c, Now: now, Loc: msk}); got != "2026-01-14" {
		t.Fatalf("22:30 UTC is already the next day in Moscow, got %q", got)
	}
}

func TestExpandMeta(t *testing.T) {
	c := model.Client{ID: "abcdef0123456789", Email: "a b@x", SubID: "s1"}
	if got := ExpandMeta("VPN {{EMAIL}} ({{SUB_ID}}) {{TRAFFIC_LEFT}}", c, false); got != "VPN a b@x (s1) {{TRAFFIC_LEFT}}" {
		t.Fatalf("only identity tokens are replaced in metadata: %q", got)
	}
	if got := ExpandMeta("https://t.me/support?u={{EMAIL}}", c, true); got != "https://t.me/support?u=a+b%40x" {
		t.Fatalf("link values must be escaped: %q", got)
	}
	if ExpandMeta("  plain  ", c, false) != "plain" {
		t.Fatal("whitespace is trimmed")
	}
}

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0B", 1023: "1023B", 1024: "1.00KB", 1536: "1.50KB", 5 << 30: "5.00GB", 3 << 40: "3.00TB"} {
		if got := FormatBytes(n); got != want {
			t.Errorf("%d = %q, want %q", n, got, want)
		}
	}
}

func TestEveryDocumentedTokenIsHandled(t *testing.T) {
	ctx := Context{Client: client(), Inbound: inbound, Now: now, Server: &model.Server{Name: "Frankfurt", Country: "DE"}}
	for _, tk := range Tokens {
		if tk.Help == "" || strings.ContainsAny(tk.Name, "{} ") {
			t.Errorf("bad token entry %+v", tk)
		}
		// A documented token must render something for a fully populated client.
		if Expand("{{"+tk.Name+"}}", ctx) == "" {
			t.Errorf("token %s is documented but renders nothing", tk.Name)
		}
	}
}

func TestServerTokens(t *testing.T) {
	sv := model.Server{Name: "Frankfurt", Country: "de"}
	ctx := Context{Client: client(), Inbound: inbound, Now: now, Server: &sv}
	if got := Expand("{{FLAG}} {{SERVER}} ({{COUNTRY}})", ctx); got != "🇩🇪 Frankfurt (de)" {
		t.Fatalf("%q", got)
	}
	// On the main server (no Server) the tokens are empty and the segments that only held them vanish.
	ctx.Server = nil
	if got := Expand("{{FLAG}}{{SERVER}}|{{EMAIL}}", ctx); got != client().Email {
		t.Fatalf("%q", got)
	}
}
