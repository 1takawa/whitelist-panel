// Package geoip finds the country a server stands in from its address, so a new node gets its flag without anyone
// picking it. It asks a public lookup service about the server's own IP; nothing about clients or the panel is sent.
package geoip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// endpoint is the lookup service; the IP is appended. A variable so tests can point it elsewhere.
var endpoint = "https://ipwho.is/"

var client = &http.Client{Timeout: 8 * time.Second}

// Country returns the ISO 3166-1 alpha-2 code ("DE") of the host, a domain name or an IP address. Private and loopback
// addresses have no country and give an error.
func Country(ctx context.Context, host string) (string, error) {
	ip, err := resolve(ctx, host)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+ip.String()+"?fields=success,country_code", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("geo lookup: status %d", resp.StatusCode)
	}
	var out struct {
		Success     bool   `json:"success"`
		CountryCode string `json:"country_code"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil {
		return "", err
	}
	code := strings.ToUpper(strings.TrimSpace(out.CountryCode))
	if !out.Success || len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return "", errors.New("geo lookup: no country for " + ip.String())
	}
	return code, nil
}

func resolve(ctx context.Context, host string) (netip.Addr, error) {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if ip, err := netip.ParseAddr(host); err == nil {
		return check(ip)
	}
	addrs, err := (&net.Resolver{}).LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("geo lookup: cannot resolve %q", host)
	}
	return check(addrs[0].Unmap())
}

func check(ip netip.Addr) (netip.Addr, error) {
	if !ip.IsValid() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return netip.Addr{}, errors.New("geo lookup: " + ip.String() + " is not a public address")
	}
	return ip, nil
}
