package sub

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"anypanel2/internal/model"
)

func TestUserInfo(t *testing.T) {
	c := model.Client{Up: 100, Down: 2000, TotalBytes: 5000}
	if got := UserInfo(c); got != "upload=100; download=2000; total=5000" {
		t.Fatalf("no expiry: %q", got)
	}
	exp := time.Unix(1893456000, 0)
	c.ExpiryAt = &exp
	if got := UserInfo(c); got != "upload=100; download=2000; total=5000; expire=1893456000" {
		t.Fatalf("with expiry: %q", got)
	}
	if got := UserInfo(model.Client{}); got != "upload=0; download=0; total=0" {
		t.Fatalf("unlimited client: %q", got)
	}
}

func TestHeaderValue(t *testing.T) {
	if got := HeaderValue("My VPN"); got != "My VPN" {
		t.Fatalf("plain ASCII must stay as is: %q", got)
	}
	got := HeaderValue("Мой VPN")
	if !strings.HasPrefix(got, "base64:") {
		t.Fatalf("non-ASCII must be base64: %q", got)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, "base64:"))
	if err != nil || string(raw) != "Мой VPN" {
		t.Fatalf("round trip failed: %q %v", raw, err)
	}
	// Line breaks would allow header injection and must never reach the wire raw.
	if got := HeaderValue("a\r\nSet-Cookie: x=1"); strings.ContainsAny(got, "\r\n") {
		t.Fatalf("CR/LF must be encoded: %q", got)
	}
}

func TestHeaders(t *testing.T) {
	h := Headers(model.Client{Up: 1, Down: 2, TotalBytes: 3}, Meta{Title: "ANY", SupportURL: "https://t.me/x"}, 6)
	if h["Subscription-Userinfo"] != "upload=1; download=2; total=3" || h["Profile-Title"] != "ANY" ||
		h["Support-Url"] != "https://t.me/x" || h["Profile-Update-Interval"] != "6" {
		t.Fatalf("headers: %v", h)
	}
	for _, k := range []string{"Profile-Web-Page-Url", "Announce"} {
		if _, ok := h[k]; ok {
			t.Errorf("empty %s must be omitted", k)
		}
	}
	if Headers(model.Client{}, Meta{}, 0)["Profile-Update-Interval"] != "1" {
		t.Fatal("update interval is at least 1 hour")
	}
}

func TestBody(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(Body([]string{"vless://a", "trojan://b"}, true))
	if err != nil || string(raw) != "vless://a\ntrojan://b" {
		t.Fatalf("encoded body: %q %v", raw, err)
	}
	if Body([]string{"vless://a", "trojan://b"}, false) != "vless://a\ntrojan://b" {
		t.Fatal("with encode off the body is plain text")
	}
	if Body(nil, true) != "" || Body(nil, false) != "" {
		t.Fatal("an empty subscription is an empty body")
	}
}

func TestInfoLink(t *testing.T) {
	got := InfoLink("📊 5GB|⏳ 3D")
	if !strings.HasPrefix(got, "socks://127.0.0.1:1080#") || strings.Contains(got, "+") || strings.ContainsAny(got[len("socks://127.0.0.1:1080#"):], " |") {
		t.Fatalf("the name must be fully escaped with %%20 for spaces: %q", got)
	}
}

func TestURL(t *testing.T) {
	def := model.SubSettings{}
	if URL(def, "panel.example.com", "abc") != "https://panel.example.com/sub/abc" || URL(def, "", "abc") != "" || URL(def, "p", "") != "" {
		t.Fatal("default address")
	}
	if got := URL(model.SubSettings{Path: "/s/xyz/", Domain: "cdn.example.com"}, "panel.example.com", "abc"); got != "https://cdn.example.com/s/xyz/abc" {
		t.Fatalf("custom path and domain: %q", got)
	}
	if got := URL(model.SubSettings{URI: "https://edge.example.com/prefix/", Domain: "ignored"}, "p", "abc"); got != "https://edge.example.com/prefix/abc" {
		t.Fatalf("a complete URI wins: %q", got)
	}
}
