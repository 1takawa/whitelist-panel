package panel

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"anypanel2/internal/model"
)

// MainNodeInput asks for a node on the machine the panel itself runs on: a VLESS + REALITY connection on a domain of its
// own, behind the same nginx (SNI) and Caddy decoy site the cascades use.
type MainNodeInput struct {
	Domain  string `json:"domain"`  // an A record to this server
	Name    string `json:"name"`    // optional: by default the domain
	Country string `json:"country"` // optional: by default looked up from the domain's address
}

// SetupMainNode creates the node and puts it into every client's subscription. Nginx is told to send the domain to the new
// connection and Caddy gets a decoy site (with a real certificate) for it; if either refuses, the node is taken back. The
// returned warning says when the change is saved but Xray did not take it yet.
func (s *Service) SetupMainNode(ctx context.Context, in MainNodeInput) (*model.Inbound, string, error) {
	if s.SNI == nil {
		return nil, "", invalid("this server is not set up to manage nginx and Caddy for extra domains (NGINX_SNI_DIR is not set)")
	}
	domain := strings.ToLower(strings.TrimSpace(in.Domain))
	if domain == "" {
		return nil, "", invalid("enter the domain of the node (an A record to this server)")
	}
	chk, err := s.CheckCascadeDomain(ctx, domain, "", "")
	if err != nil {
		return nil, "", err
	}
	if !chk.OK {
		return nil, "", invalid("%s", chk.Problem)
	}

	country := strings.TrimSpace(in.Country)
	if country == "" && s.GeoCountry != nil {
		if code, err := s.GeoCountry(ctx, domain); err == nil {
			country = code
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = domain
	}
	name, country, err = normalizeCascadeName(name, country)
	if err != nil {
		return nil, "", err
	}

	all, err := s.Store.ListInbounds(ctx)
	if err != nil {
		return nil, "", err
	}
	port := internalPort(all, s.Reserved, "")
	if port == 0 {
		return nil, "", errors.New("no free internal port found")
	}
	node, err := newRealityInbound(cascadeRemark(name, country), "", "127.0.0.1", port, mainDecoyTarget, domain, true, 443)
	if err != nil {
		return nil, "", err
	}
	node.Public.Host = domain // clients connect to the domain itself
	node.NodeDomain = domain

	created, err := s.CreateInboundFor(ctx, node, true)
	warning := ""
	var ae *ApplyError
	switch {
	case errors.As(err, &ae):
		warning = ae.Error()
	case err != nil:
		return nil, "", err
	}
	// nginx has to send the domain to this connection. If that fails the node would never answer, so it is taken back.
	if err := s.syncSNI(ctx, ""); err != nil {
		_ = s.DeleteInbound(ctx, created.ID)
		_ = s.syncSNI(ctx, "")
		return nil, "", fmt.Errorf("не удалось настроить nginx для домена %s: %w", domain, err)
	}
	return created, warning, nil
}
