package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"anypanel2/internal/xray"
)

// Client is the panel's side: it talks to one agent.
type Client struct {
	Address     string
	Port        int
	Token       string
	Fingerprint string // pinned certificate (hex SHA-256); empty means "trust the first certificate seen"

	mu       sync.Mutex
	observed string
	http     *http.Client
}

// NewClient prepares a client; no connection is made until a method is called.
func NewClient(address string, port int, token, fingerprint string) *Client {
	c := &Client{Address: address, Port: port, Token: token, Fingerprint: strings.ToLower(strings.TrimSpace(fingerprint))}
	c.http = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				// The certificate is self-signed: it is checked by its fingerprint instead of a chain.
				InsecureSkipVerify: true,
				VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
					if len(raw) == 0 {
						return errors.New("the agent presented no certificate")
					}
					fp := Fingerprint(raw[0])
					c.mu.Lock()
					c.observed = fp
					c.mu.Unlock()
					if c.Fingerprint != "" && c.Fingerprint != fp {
						return fmt.Errorf("the agent's certificate does not match the pinned fingerprint (got %s…)", fp[:16])
					}
					return nil
				},
			},
			DisableKeepAlives: true, // calls are rare (every 10 s); no idle connections to clean up
		},
	}
	return c
}

// ObservedFingerprint is the certificate fingerprint seen on the latest connection.
func (c *Client) ObservedFingerprint() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observed
}

func (c *Client) url(path string) string {
	return "https://" + net.JoinHostPort(c.Address, strconv.Itoa(c.Port)) + path
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode == http.StatusUnauthorized {
		return errUnauthorized
	}
	if res.StatusCode >= 300 {
		var e struct{ Error string }
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("HTTP %d", res.StatusCode)
		}
		return errors.New(e.Error)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	return st, c.do(ctx, http.MethodGet, "/v1/status", nil, &st)
}

func (c *Client) Check(ctx context.Context, cfg []byte) error {
	return c.do(ctx, http.MethodPost, "/v1/check", cfg, nil)
}

func (c *Client) Apply(ctx context.Context, cfg []byte) error {
	return c.do(ctx, http.MethodPut, "/v1/config", cfg, nil)
}

func (c *Client) Restart(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/restart", []byte("{}"), nil)
}

// Stats returns the traffic counters per client; with reset the agent zeroes them, so every byte is read once.
func (c *Client) Stats(ctx context.Context, reset bool) (map[string]xray.Traffic, error) {
	path := "/v1/stats"
	if reset {
		path += "?reset=1"
	}
	var raw map[string]trafficJSON
	if err := c.do(ctx, http.MethodPost, path, []byte("{}"), &raw); err != nil {
		return nil, err
	}
	out := make(map[string]xray.Traffic, len(raw))
	for k, v := range raw {
		out[k] = xray.Traffic{Up: v.Up, Down: v.Down}
	}
	return out, nil
}
