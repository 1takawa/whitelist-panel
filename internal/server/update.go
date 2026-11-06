package server

import (
	"net/http"
)

// The panel updates itself from the releases of its repository (session only: it replaces the program that serves it).

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.CheckUpdate(r.Context(), r.URL.Query().Get("refresh") != "")
	s.respond(w, v, err)
}

func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Version string `json:"version"`
	}](w, r)
	if !ok {
		return
	}
	if err := s.svc.ApplyUpdate(r.Context(), in.Version); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) updateStatus(w http.ResponseWriter, _ *http.Request) {
	st, log, err := s.svc.UpdateStatus()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": st.State, "message": st.Message, "version": st.Version, "log": log})
}
