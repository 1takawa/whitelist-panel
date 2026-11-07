package server

import "net/http"

// checkSubDomain tells whether a domain already serves subscriptions from this server (for the settings page).
func (s *Server) checkSubDomain(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		Domain string `json:"domain"`
	}](w, r)
	if !ok {
		return
	}
	v, err := s.svc.CheckSubDomain(r.Context(), req.Domain)
	s.respond(w, v, err)
}
