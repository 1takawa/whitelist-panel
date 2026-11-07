package panel

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// SyncSubscriptionDomain makes Caddy serve the subscription domain at start-up: the files live on the server, not in the
// database, so a restored database or a reinstalled web server must get them back. Without such a domain it does nothing.
func (s *Service) SyncSubscriptionDomain(ctx context.Context) error {
	set, err := s.settings(ctx)
	if err != nil || set.Subscription.Domain == "" || set.Subscription.Domain == set.PanelDomain {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncSNI(ctx, "")
}

// SubDomainCheck is the answer to "does this domain serve subscriptions".
type SubDomainCheck struct {
	OK        bool     `json:"ok"`
	Message   string   `json:"message,omitempty"`
	Problem   string   `json:"problem,omitempty"`
	Addresses []string `json:"addresses"` // where the domain points
	Main      []string `json:"mainAddresses"`
}

// CheckSubDomain looks whether the domain points at this server and answers over HTTPS with a certificate the apps will
// trust, as the panel on it. A domain that does not answer yet must not be given to the apps.
func (s *Service) CheckSubDomain(ctx context.Context, domain string) (*SubDomainCheck, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || !isHost(domain) {
		return nil, invalid("введите домен, например subs.example.com")
	}
	out := &SubDomainCheck{Addresses: []string{}, Main: []string{}}
	addrs, err := s.lookup(ctx, domain)
	if err != nil || len(addrs) == 0 {
		out.Problem = "у домена нет A-записи: создайте её у провайдера DNS и подождите несколько минут"
		return out, nil
	}
	out.Addresses = addrs
	if ip, err := s.publicIP(ctx); err == nil && ip != "" {
		out.Main = []string{ip}
		if !slices.Contains(addrs, ip) {
			out.Problem = "домен указывает на " + strings.Join(addrs, ", ") + ", а этот сервер — " + ip + ". Исправьте A-запись"
			return out, nil
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, "https://"+domain+"/api/health", nil)
	if err != nil {
		return nil, invalid("%v", err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		out.Problem = "домен пока не отвечает по HTTPS (" + shortErr(err) + "). Сертификат выпускается после сохранения домена, обычно за минуту: проверьте ещё раз"
		return out, nil
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<10))
	if res.StatusCode != http.StatusOK {
		out.Problem = "домен отвечает, но не как панель (код " + http.StatusText(res.StatusCode) + "). Сохраните домен: тогда панель подключит его к веб-серверу"
		return out, nil
	}
	out.OK = true
	out.Message = "домен указывает на этот сервер и отвечает по HTTPS"
	return out, nil
}

func shortErr(err error) string {
	m := err.Error()
	if i := strings.LastIndex(m, ": "); i >= 0 && i+2 < len(m) {
		m = m[i+2:]
	}
	return m
}
