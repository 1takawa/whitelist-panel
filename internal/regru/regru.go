// Package regru manages the DNS zone of one domain through the REG.RU API 2 (https://www.reg.ru/reseller/api2doc).
// Only what the panel needs is implemented: list records, add or remove A, AAAA, CNAME and TXT records.
package regru

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// DefaultBase is the REG.API 2 address; Config.BaseURL replaces it in tests.
const DefaultBase = "https://api.reg.ru/api/regru2"

// Config is how to reach the API and which zone to manage.
type Config struct {
	Username string
	// Password is the main password or, better, the "alternative API password" from the REG.RU API settings.
	Password string
	// CertPEM and KeyPEM are the client certificate that is uploaded in the REG.RU API settings. Once one is uploaded,
	// REG.RU wants it on every request, so both must be given then; leave both empty otherwise.
	CertPEM, KeyPEM string
	Zone            string // the domain, for example example.com
	BaseURL         string
}

// Client talks to REG.API for one zone. It is cheap: the panel builds one per request.
type Client struct {
	user, pass string
	zone       string
	base       string
	http       *http.Client
}

// New validates the configuration and prepares the HTTP client (with the client certificate, if any).
func New(cfg Config) (*Client, error) {
	zone := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(cfg.Zone), "."))
	switch {
	case strings.TrimSpace(cfg.Username) == "" || cfg.Password == "":
		return nil, errors.New("укажите логин и пароль API reg.ru")
	case zone == "" || !strings.Contains(zone, "."):
		return nil, errors.New("укажите домен, зоной которого управляет панель (например example.com)")
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true}
	if cfg.CertPEM != "" || cfg.KeyPEM != "" {
		pair, err := tls.X509KeyPair([]byte(cfg.CertPEM), []byte(cfg.KeyPEM))
		if err != nil {
			return nil, fmt.Errorf("сертификат или ключ не читаются (нужны PEM-тексты, ключ без пароля): %w", err)
		}
		tr.TLSClientConfig = &tls.Config{Certificates: []tls.Certificate{pair}}
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBase
	}
	return &Client{
		user: strings.TrimSpace(cfg.Username), pass: cfg.Password, zone: zone, base: strings.TrimSuffix(base, "/"),
		http: &http.Client{Timeout: 30 * time.Second, Transport: tr},
	}, nil
}

// Zone is the managed domain in lower case.
func (c *Client) Zone() string { return c.zone }

// Error is a failure reported by REG.API itself, as opposed to a network problem.
type Error struct{ Code, Text string }

func (e *Error) Error() string { return fmt.Sprintf("reg.ru: %s: %s", e.Code, e.Text) }

// ConnError is a failure to reach REG.RU at all: the server the panel runs on cannot connect, the answer never comes or
// the connection is cut. It is the server's network (or a block of its address at REG.RU), not the account's settings.
type ConnError struct{ Err error }

func (e *ConnError) Unwrap() error { return e.Err }

// Timeout reports that REG.RU did not answer in time.
func (e *ConnError) Timeout() bool {
	var ne net.Error
	return errors.Is(e.Err, context.DeadlineExceeded) || (errors.As(e.Err, &ne) && ne.Timeout())
}

func (e *ConnError) Error() string {
	if e.Timeout() {
		return "этот сервер не может подключиться к reg.ru: соединение не отвечает (возможно, reg.ru блокирует адрес этого хостинга)"
	}
	reason := e.Err
	var ue *url.Error
	if errors.As(e.Err, &ue) {
		reason = ue.Err // without the address of the request: it only repeats what is already known
	}
	return fmt.Sprintf("этот сервер не может подключиться к reg.ru: %v", reason)
}

// Friendly explains the error in Russian, with what to do about it.
func (e *Error) Friendly() string {
	switch e.Code {
	case "ACCESS_DENIED_FROM_IP":
		return "reg.ru не пускает с адреса этого сервера: добавьте его в разрешённые IP в настройках API аккаунта reg.ru"
	case "NO_CERTIFICATE":
		return "в аккаунте reg.ru загружен сертификат, и он нужен в каждом запросе: вставьте сертификат и ключ ниже"
	case "PASSWORD_AUTH_FAILED", "USER_AUTHENTICATION_FAILED", "NO_AUTH", "NO_USERNAME":
		return "reg.ru не принял логин или пароль (для API нужен «альтернативный пароль» из настроек API)"
	case "ACCOUNT_BLOCKED", "ACCESS_DENIED":
		return "доступ к API reg.ru закрыт для этого аккаунта: " + e.Text
	case "DOMAIN_NOT_FOUND", "SERVICE_NOT_FOUND", "SERVICE_ID_NOT_FOUND":
		return "домена нет в этом аккаунте reg.ru или DNS-зоной управляет другой сервис"
	case "SERVICE_UNAVAILABLE":
		return "API reg.ru временно недоступен, повторите позже"
	case "DOMAIN_PROTECTION":
		return "для домена в reg.ru включена защита: операции через API запрещены"
	}
	return e.Error()
}

// Describe turns any error from this package into a message for the user.
func Describe(err error) string {
	var ae *Error
	if errors.As(err, &ae) {
		return ae.Friendly()
	}
	return err.Error()
}

type domainResult struct {
	Result    json.RawMessage `json:"result"`
	ErrorCode string          `json:"error_code"`
	ErrorText string          `json:"error_text"`
	Records   []struct {
		Sub     flex `json:"subname"`
		Type    flex `json:"rectype"`
		Content flex `json:"content"`
		Prio    flex `json:"prio"`
	} `json:"rrs"`
}

// flex is a string that also accepts a JSON number (the API mixes both).
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flex(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flex(n.String())
	return nil
}

// call invokes one REG.API function for the zone. Credentials go in the form body, never the query string.
func (c *Client) call(ctx context.Context, fn string, params map[string]any) (*domainResult, error) {
	if params == nil {
		params = map[string]any{}
	}
	params["domains"] = []any{map[string]any{"dname": c.zone}}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"username": {c.user}, "password": {c.pass},
		"input_format": {"json"}, "input_data": {string(data)}, "output_format": {"json"}, "lang": {"en"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+fn, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &ConnError{Err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var env struct {
		Result    string `json:"result"`
		ErrorCode string `json:"error_code"`
		ErrorText string `json:"error_text"`
		Answer    struct {
			Domains []domainResult `json:"domains"`
		} `json:"answer"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("reg.ru: HTTP %d, ответ не разобрать: %.200s", resp.StatusCode, body)
	}
	if env.Result != "success" {
		return nil, &Error{env.ErrorCode, env.ErrorText}
	}
	if len(env.Answer.Domains) == 0 {
		return nil, fmt.Errorf("reg.ru: %s не вернул данных по домену", fn)
	}
	d := env.Answer.Domains[0]
	// A failure of the zone itself is reported per domain, either as error_code or as a result other than "success".
	var res string
	_ = json.Unmarshal(d.Result, &res)
	if d.ErrorCode != "" || (res != "" && res != "success") {
		text := d.ErrorText
		if text == "" {
			text = res
		}
		return nil, &Error{d.ErrorCode, text}
	}
	return &d, nil
}

// Check proves that the credentials, the allowed IP and the certificate work and that the zone can be managed.
func (c *Client) Check(ctx context.Context) error {
	_, err := c.call(ctx, "zone/nop", nil)
	return err
}

// Record is one resource record of the zone. Sub is "@" for the domain itself.
type Record struct {
	Sub, Type, Content, Prio string
}

// Records returns every record of the zone.
func (c *Client) Records(ctx context.Context) ([]Record, error) {
	d, err := c.call(ctx, "zone/get_resource_records", nil)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(d.Records))
	for _, r := range d.Records {
		out = append(out, Record{string(r.Sub), string(r.Type), string(r.Content), string(r.Prio)})
	}
	return out, nil
}

// Add creates a record: A, AAAA, CNAME or TXT. The API has no per-record TTL, the zone's applies.
func (c *Client) Add(ctx context.Context, typ, sub, value string) error {
	params := map[string]any{"subdomain": sub}
	var fn string
	switch typ {
	case "A":
		fn, params["ipaddr"] = "zone/add_alias", value
	case "AAAA":
		fn, params["ipaddr"] = "zone/add_aaaa", value
	case "CNAME":
		fn, params["canonical_name"] = "zone/add_cname", value
	case "TXT":
		fn, params["text"] = "zone/add_txt", value
	default:
		return fmt.Errorf("неподдерживаемый тип записи %q (A, AAAA, CNAME или TXT)", typ)
	}
	_, err := c.call(ctx, fn, params)
	return err
}

// Remove deletes the records of this type on sub; with content set, only the one with that value.
func (c *Client) Remove(ctx context.Context, typ, sub, content string) error {
	params := map[string]any{"subdomain": sub, "record_type": typ}
	if content != "" {
		params["content"] = content
	}
	_, err := c.call(ctx, "zone/remove_record", params)
	return err
}

// Kind picks the record type for a target: IPv4 -> A, IPv6 -> AAAA, anything else -> CNAME.
func Kind(target string) string {
	if a, err := netip.ParseAddr(target); err == nil {
		if a.Is4() {
			return "A"
		}
		return "AAAA"
	}
	return "CNAME"
}

// ValidSub reports whether s is a usable subdomain: the part before the zone, "@" or "*".
func ValidSub(s string) error {
	if s == "" || strings.ContainsAny(s, " \t/") || strings.HasSuffix(s, ".") {
		return fmt.Errorf("плохое имя поддомена %q: нужна часть до домена, «@» для самого домена или «*»", s)
	}
	return nil
}

// FQDN is the full name of a subdomain in the zone.
func (c *Client) FQDN(sub string) string {
	if sub == "@" {
		return c.zone
	}
	return sub + "." + c.zone
}

// Sub is the subdomain part of a name inside the zone; ok is false for names of other zones.
func (c *Client) Sub(name string) (sub string, ok bool) {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name == c.zone {
		return "@", true
	}
	if s, found := strings.CutSuffix(name, "."+c.zone); found && s != "" {
		return s, true
	}
	return "", false
}

func norm(s string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".") }

// Action is one step of Ensure.
type Action struct {
	Op    string // "ok" (nothing to do), "remove" or "add"
	Type  string
	Sub   string
	Value string
	Done  bool // false in a dry run
}

// Ensure makes sub point to target as a record of type typ and reports what it did. A record that is already
// right is left alone; for A, AAAA and CNAME other values of the same name and type are removed first unless keep is
// set (TXT records are additive), so repeated runs never pile up duplicates. existing is the zone's current records.
func (c *Client) Ensure(ctx context.Context, existing []Record, sub, typ, target string, keep, dry bool) ([]Action, error) {
	if err := ValidSub(sub); err != nil {
		return nil, err
	}
	var same []string
	for _, r := range existing {
		if strings.EqualFold(r.Sub, sub) && strings.EqualFold(r.Type, typ) {
			same = append(same, r.Content)
		}
	}
	for _, v := range same {
		if norm(v) == norm(target) {
			return []Action{{Op: "ok", Type: typ, Sub: sub, Value: target, Done: true}}, nil
		}
	}
	var acts []Action
	if typ != "TXT" && !keep {
		for _, old := range same {
			a := Action{Op: "remove", Type: typ, Sub: sub, Value: old}
			if !dry {
				if err := c.Remove(ctx, typ, sub, old); err != nil {
					return acts, fmt.Errorf("remove %s %s -> %s: %w", typ, sub, old, err)
				}
				a.Done = true
			}
			acts = append(acts, a)
		}
	}
	a := Action{Op: "add", Type: typ, Sub: sub, Value: target}
	if !dry {
		if err := c.Add(ctx, typ, sub, target); err != nil {
			return acts, fmt.Errorf("add %s %s -> %s: %w", typ, sub, target, err)
		}
		a.Done = true
	}
	return append(acts, a), nil
}
