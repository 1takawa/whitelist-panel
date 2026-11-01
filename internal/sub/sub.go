// Package sub renders the machine-readable subscription: response headers, the link list and the public address.
package sub

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"anypanel2/internal/model"
)

// Meta is the profile metadata after placeholder expansion.
type Meta struct {
	Title      string
	SupportURL string
	ProfileURL string
	Announce   string
}

// UserInfo is the Subscription-Userinfo header value understood by Happ, Clash, v2rayNG and others.
// total=0 means unlimited; expire is omitted for unlimited time.
func UserInfo(c model.Client) string {
	s := fmt.Sprintf("upload=%d; download=%d; total=%d", c.Up, c.Down, c.TotalBytes)
	if c.ExpiryAt != nil {
		s += fmt.Sprintf("; expire=%d", c.ExpiryAt.Unix())
	}
	return s
}

// HeaderValue returns v as a safe HTTP header value: plain when it is printable ASCII, otherwise
// "base64:" + standard base64 of the UTF-8 text (the convention used by Happ and other clients).
func HeaderValue(v string) string {
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return "base64:" + base64.StdEncoding.EncodeToString([]byte(v))
		}
	}
	return v
}

// Headers returns the subscription response headers for a client.
func Headers(c model.Client, m Meta, updateHours int) map[string]string {
	h := map[string]string{
		"Subscription-Userinfo":   UserInfo(c),
		"Profile-Update-Interval": strconv.Itoa(max(updateHours, 1)),
	}
	set := func(k, v string) {
		if v != "" {
			h[k] = HeaderValue(v)
		}
	}
	set("Profile-Title", m.Title)
	set("Support-Url", m.SupportURL)
	set("Profile-Web-Page-Url", m.ProfileURL)
	set("Announce", m.Announce)
	return h
}

// Body is the subscription payload: the links joined by newlines, base64-encoded unless encode is false.
func Body(links []string, encode bool) string {
	joined := strings.Join(links, "\n")
	if !encode {
		return joined
	}
	return base64.StdEncoding.EncodeToString([]byte(joined))
}

// InfoLink builds the pseudo-link of the info node. Its only purpose is to carry a name that apps list as a server.
func InfoLink(name string) string {
	return model.InfoNodeLinkPrefix + strings.ReplaceAll(url.QueryEscape(name), "+", "%20")
}

// URL returns the public subscription address, or "" when no host is known. A complete URI override wins;
// otherwise the address is https://<Domain or panel domain><Path><subId>.
func URL(s model.SubSettings, panelDomain, subID string) string {
	if subID == "" {
		return ""
	}
	if s.URI != "" {
		return strings.TrimRight(s.URI, "/") + "/" + subID
	}
	host := s.Domain
	if host == "" {
		host = panelDomain
	}
	if host == "" {
		return ""
	}
	path := s.Path
	if path == "" {
		path = model.DefaultSubPath
	}
	return "https://" + host + path + subID
}
