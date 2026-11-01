package server

import (
	"net/http"

	"anypanel2/internal/panel"
)

// ---- node installation over SSH (session only: these take an SSH password) ----

func (s *Server) sshCheck(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		panel.SSHAccess
		AgentPort   int    `json:"agentPort"`
		ConnectPort int    `json:"connectPort"`
		Domain      string `json:"realityDomain"`
	}](w, r)
	if !ok {
		return
	}
	rep, err := s.svc.SSHCheck(r.Context(), in.SSHAccess, in.AgentPort, in.ConnectPort, in.Domain)
	s.respond(w, rep, err)
}

func (s *Server) installNode(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[panel.NodeInstallRequest](w, r)
	if !ok {
		return
	}
	j, err := s.svc.StartNodeInstall(r.Context(), in)
	s.respond(w, j, err)
}

func (s *Server) upgradeNode(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.StartNodeUpgrade(r.Context(), r.PathValue("id"))
	s.respond(w, j, err)
}

func (s *Server) nodeJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.svc.NodeJob(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) serverClients(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ServerClients(r.Context(), r.PathValue("id"))
	s.respond(w, v, err)
}

// openNodePorts opens the node's ports in its firewall over SSH (session only: it uses the node's SSH key).
func (s *Server) openNodePorts(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.StartNodeFirewall(r.Context(), r.PathValue("id"))
	s.respond(w, j, err)
}

// setupNodeReality rebuilds an installed node the nginx-SNI way (session only: it logs in to the node over SSH).
func (s *Server) setupNodeReality(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[panel.NodeRealityRequest](w, r)
	if !ok {
		return
	}
	j, err := s.svc.StartNodeReality(r.Context(), r.PathValue("id"), in)
	s.respond(w, j, err)
}
