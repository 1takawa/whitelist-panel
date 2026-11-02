package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"anypanel2/internal/panel"
)

// ---- cascades (two servers chained: clients enter on A, leave from B). Changes are session only: they edit the
// configuration of other machines.

// detached is the context of a change that touches several machines and files: it must not be left half done because the
// browser went away, so it outlives the request (but not forever).
func detached(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
}

func (s *Server) listCascades(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ListCascades(r.Context())
	s.respond(w, v, err)
}

func (s *Server) createCascade(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[panel.CascadeInput](w, r)
	if !ok {
		return
	}
	ctx, cancel := detached(r)
	defer cancel()
	v, warning, err := s.svc.CreateCascade(ctx, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result{Item: v, Warning: warning})
}

// checkCascadeDomain says whether a domain is free and points at the main server (nothing is changed).
func (s *Server) checkCascadeDomain(w http.ResponseWriter, r *http.Request) {
	type req struct {
		Domain   string `json:"domain"`
		ExceptID string `json:"exceptId"`
		EntryID  string `json:"entryServerId"`
		Pending  bool   `json:"dnsPending"`
	}
	in, ok := decode[req](w, r)
	if !ok {
		return
	}
	v, err := s.svc.CheckCascadeDomainWith(r.Context(), in.Domain, in.ExceptID, in.EntryID, in.Pending)
	s.respond(w, v, err)
}

func (s *Server) renameCascade(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[panel.CascadeInput](w, r)
	if !ok {
		return
	}
	v, err := s.svc.RenameCascade(r.Context(), r.PathValue("id"), in.Name, in.Country)
	s.respond(w, v, err)
}

func (s *Server) deleteCascade(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := detached(r)
	defer cancel()
	note, err := s.svc.DeleteCascadeNote(ctx, r.PathValue("id"))
	var ae *panel.ApplyError
	if err == nil && note != "" {
		writeJSON(w, http.StatusOK, result{Warning: note})
		return
	}
	if err != nil && errors.As(err, &ae) && note != "" {
		err = &panel.ApplyError{Err: errors.New(ae.Err.Error() + ". " + note)}
	}
	s.finish(w, nil, err)
}

// setupMainNode puts a node with a domain of its own on the machine the panel runs on (session only: it edits nginx and Caddy).
func (s *Server) setupMainNode(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[panel.MainNodeInput](w, r)
	if !ok {
		return
	}
	ctx, cancel := detached(r)
	defer cancel()
	v, warning, err := s.svc.SetupMainNode(ctx, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result{Item: v, Warning: warning})
}
