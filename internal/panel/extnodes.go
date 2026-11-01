package panel

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"anypanel2/internal/links"
	"anypanel2/internal/model"
)

const maxExternalNodes = 200

// ExternalNodeView is a node as the admin sees it: the stored record plus what the link says.
type ExternalNodeView struct {
	model.ExternalNode
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Network  string `json:"network"`
	Security string `json:"security"`
}

func viewNode(n model.ExternalNode) ExternalNodeView {
	v := ExternalNodeView{ExternalNode: n}
	if e, err := links.ParseURI(n.URI); err == nil {
		v.Protocol, v.Host, v.Port, v.Network, v.Security = e.Protocol, e.Host, e.Port, e.Network, e.Security
	}
	return v
}

func (s *Service) ListExternalNodes(ctx context.Context) ([]ExternalNodeView, error) {
	ns, err := s.Store.ListExternalNodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ExternalNodeView, 0, len(ns))
	for _, n := range ns {
		out = append(out, viewNode(n))
	}
	return out, nil
}

// normalizeNode parses the link and fixes the stored form: the canonical link carries the node's name.
func normalizeNode(n *model.ExternalNode) error {
	e, err := links.ParseURI(n.URI)
	if err != nil {
		return invalid("%v", err)
	}
	n.Name = strings.TrimSpace(n.Name)
	if n.Name == "" {
		n.Name = strings.TrimSpace(e.Name)
	}
	if n.Name == "" {
		n.Name = fmt.Sprintf("%s:%d", e.Host, e.Port)
	}
	if len([]rune(n.Name)) > 100 {
		return invalid("name is too long (max 100 characters)")
	}
	e.Name = n.Name
	n.URI = e.URI()
	return nil
}

// nodeKey identifies the server behind a link regardless of its display name.
func nodeKey(uri string) string {
	e, err := links.ParseURI(uri)
	if err != nil {
		return uri
	}
	e.Name = ""
	return e.URI()
}

func (s *Service) CreateExternalNode(ctx context.Context, n model.ExternalNode) (*ExternalNodeView, error) {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	if err := normalizeNode(&n); err != nil {
		return nil, err
	}
	existing, err := s.Store.ListExternalNodes(ctx)
	if err != nil {
		return nil, err
	}
	if len(existing) >= maxExternalNodes {
		return nil, invalid("at most %d external nodes", maxExternalNodes)
	}
	key := nodeKey(n.URI)
	for _, x := range existing {
		if nodeKey(x.URI) == key {
			return nil, invalid("this server is already in the list as %q", x.Name)
		}
	}
	n.ID, n.CreatedAt = newID(), s.Now().UTC()
	if err := s.Store.PutExternalNode(ctx, n); err != nil {
		return nil, err
	}
	v := viewNode(n)
	return &v, nil
}

func (s *Service) UpdateExternalNode(ctx context.Context, id string, in model.ExternalNode) (*ExternalNodeView, error) {
	s.extMu.Lock()
	defer s.extMu.Unlock()
	cur, err := s.Store.GetExternalNode(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.URI == "" {
		in.URI = cur.URI
	}
	in.ID, in.CreatedAt = cur.ID, cur.CreatedAt
	if err := normalizeNode(&in); err != nil {
		return nil, err
	}
	existing, err := s.Store.ListExternalNodes(ctx)
	if err != nil {
		return nil, err
	}
	key := nodeKey(in.URI)
	for _, x := range existing {
		if x.ID != id && nodeKey(x.URI) == key {
			return nil, invalid("this server is already in the list as %q", x.Name)
		}
	}
	if err := s.Store.PutExternalNode(ctx, in); err != nil {
		return nil, err
	}
	v := viewNode(in)
	return &v, nil
}

func (s *Service) DeleteExternalNode(ctx context.Context, id string) error {
	return s.Store.DeleteExternalNode(ctx, id)
}

// ImportProblem is one line that could not be added.
type ImportProblem struct {
	Line  int    `json:"line"`
	Error string `json:"error"`
}

type ImportResult struct {
	Added    int             `json:"added"`
	Problems []ImportProblem `json:"problems"`
}

// ImportExternalNodes adds many links at once: one per line, or the body of a base64 subscription.
func (s *Service) ImportExternalNodes(ctx context.Context, text string) (*ImportResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, invalid("paste at least one link")
	}
	if len(text) > 512<<10 {
		return nil, invalid("the pasted text is too large")
	}
	if !strings.Contains(text, "://") { // a base64 subscription body
		raw := strings.Join(strings.Fields(text), "")
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, err := enc.DecodeString(raw); err == nil && strings.Contains(string(b), "://") {
				text = string(b)
				break
			}
		}
	}
	res := &ImportResult{Problems: []ImportProblem{}}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := s.CreateExternalNode(ctx, model.ExternalNode{URI: line, Enable: true}); err != nil {
			var ie *InvalidError
			msg := err.Error()
			if !asInvalid(err, &ie) {
				return res, err // a storage failure stops the import
			}
			res.Problems = append(res.Problems, ImportProblem{Line: i + 1, Error: msg})
			continue
		}
		res.Added++
	}
	return res, nil
}

// externalLinks returns the enabled external nodes as subscription entries.
func (s *Service) externalLinks(ctx context.Context) ([]LinkView, error) {
	ns, err := s.Store.ListExternalNodes(ctx)
	if err != nil {
		return nil, err
	}
	var out []LinkView
	for _, n := range ns {
		if !n.Enable {
			continue
		}
		e, err := links.ParseURI(n.URI)
		if err != nil {
			continue // a record that no longer parses is skipped rather than breaking every subscription
		}
		e.Name = n.Name
		out = append(out, LinkView{
			ep: e, InboundID: "ext:" + n.ID, Remark: n.Name, Name: n.Name, Protocol: e.Protocol,
			Network: e.Network, Security: e.Security, Enabled: true, Link: e.URI(),
		})
	}
	return out, nil
}
