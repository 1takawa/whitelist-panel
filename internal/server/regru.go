package server

import (
	"net/http"

	"anypanel2/internal/panel"
)

// ---- REG.RU: the DNS of the panel's domain ----

func (s *Server) regruStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.RegRuStatus(r.Context(), r.URL.Query().Get("refresh") != "")
	s.respond(w, v, err)
}

func (s *Server) regruSave(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[panel.RegRuSave](w, r)
	if !ok {
		return
	}
	v, err := s.svc.SaveRegRu(r.Context(), req)
	s.respond(w, v, err)
}

// regruCertificate makes a key pair for the REG.RU API on this server (session only, nothing is stored yet).
func (s *Server) regruCertificate(w http.ResponseWriter, _ *http.Request) {
	v, err := s.svc.GenerateRegRuCertificate()
	s.respond(w, v, err)
}

func (s *Server) regruDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DisconnectRegRu(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) regruDomains(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.RegRuDomains(r.Context())
	s.respond(w, v, err)
}

func (s *Server) regruGenerate(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[panel.RegRuGenerateRequest](w, r)
	if !ok {
		return
	}
	v, err := s.svc.GenerateRegRuDomain(r.Context(), req)
	s.respond(w, v, err)
}

func (s *Server) regruCleanup(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.CleanupRegRu(r.Context())
	s.respond(w, v, err)
}
