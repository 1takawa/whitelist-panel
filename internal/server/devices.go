package server

import "net/http"

// blockDevice blocks or unblocks one device of a client: {"blocked": true|false}.
func (s *Server) blockDevice(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Blocked bool `json:"blocked"`
	}](w, r)
	if !ok {
		return
	}
	v, err := s.svc.SetDeviceBlocked(r.Context(), r.PathValue("id"), r.PathValue("hwid"), in.Blocked)
	s.respond(w, v, err)
}

// deleteDevice forgets a device of a client.
func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.DeleteDevice(r.Context(), r.PathValue("id"), r.PathValue("hwid"))
	s.respond(w, v, err)
}
