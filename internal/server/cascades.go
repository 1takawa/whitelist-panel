package server

import (
	"errors"
	"net/http"

	"anypanel2/internal/panel"
)

// ---- cascades (two servers chained: clients enter on A, leave from B). Changes are session only: they edit the
// configuration of other machines.

func (s *Server) listCascades(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ListCascades(r.Context())
	s.respond(w, v, err)
}

func (s *Server) createCascade(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[panel.CascadeInput](w, r)
	if !ok {
		return
	}
	v, warning, err := s.svc.CreateCascade(r.Context(), in)
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
	}
	in, ok := decode[req](w, r)
	if !ok {
		return
	}
	v, err := s.svc.CheckCascadeDomain(r.Context(), in.Domain, in.ExceptID, in.EntryID)
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
	note, err := s.svc.DeleteCascadeNote(r.Context(), r.PathValue("id"))
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
