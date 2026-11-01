// Package agent lets a panel drive Xray on another machine. The agent is a small HTTPS service that runs next to
// Xray there (`anypanel agent`); the panel pushes the generated Xray config to it and pulls traffic counters.
// Trust is established with a shared token and by pinning the agent's self-signed certificate.
package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"anypanel2/internal/auth"
	"anypanel2/internal/sysinfo"
	"anypanel2/internal/xray"
)

// DefaultPort is the agent's HTTPS port unless configured otherwise.
const DefaultPort = 8765

// APIPort is the loopback port of Xray's stats API on every agent machine. The panel builds node configs with it,
// so it is fixed rather than configurable.
const APIPort = 62789

// Runner is the part of Xray's supervisor the agent needs (implemented by *xray.Manager).
type Runner interface {
	Installed() bool
	Check(ctx context.Context, cfg []byte) error
	Apply(ctx context.Context, cfg []byte) error
	Unchanged(ctx context.Context, cfg []byte) bool
	Restart(ctx context.Context) error
	Status(ctx context.Context) xray.Status
	QueryStats(ctx context.Context, reset bool) (map[string]xray.Traffic, error)
}

// Status is what the agent reports about its machine.
type Status struct {
	Version string       `json:"version"` // the agent's (panel binary) version
	Xray    xray.Status  `json:"xray"`
	System  sysinfo.Info `json:"system"`
}

// Server is the agent's HTTP side.
type Server struct {
	Token   string
	Xray    Runner
	Sampler *sysinfo.Sampler
	Version string
	Log     *slog.Logger

	limiter *auth.Limiter
}

const maxConfig = 8 << 20

func (s *Server) Handler() http.Handler {
	if s.limiter == nil {
		s.limiter = auth.NewLimiter(120, time.Minute)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.guard(s.status))
	mux.HandleFunc("POST /v1/check", s.guard(s.check))
	mux.HandleFunc("PUT /v1/config", s.guard(s.config))
	mux.HandleFunc("POST /v1/stats", s.guard(s.stats))
	mux.HandleFunc("POST /v1/restart", s.guard(s.restart))
	return mux
}

func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if !s.limiter.Allow(host) {
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		h := r.Header.Get("Authorization")
		tok := ""
		if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
			tok = strings.TrimSpace(h[7:])
		}
		if s.Token == "" || !tokenEqual(tok, s.Token) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func tokenEqual(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st := Status{Version: s.Version, Xray: s.Xray.Status(r.Context())}
	if s.Sampler != nil {
		st.System = s.Sampler.Get()
	}
	writeJSON(w, http.StatusOK, st)
}

func readConfig(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfig))
	if err != nil || !json.Valid(body) {
		writeErr(w, http.StatusBadRequest, "the body must be a JSON Xray config")
		return nil, false
	}
	return body, true
}

func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	cfg, ok := readConfig(w, r)
	if !ok {
		return
	}
	if !s.Xray.Installed() {
		writeErr(w, http.StatusConflict, "xray is not installed on this server")
		return
	}
	if err := s.Xray.Check(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	cfg, ok := readConfig(w, r)
	if !ok {
		return
	}
	if !s.Xray.Installed() {
		writeErr(w, http.StatusConflict, "xray is not installed on this server")
		return
	}
	if s.Xray.Unchanged(r.Context(), cfg) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "unchanged"})
		return
	}
	if err := s.Xray.Apply(r.Context(), cfg); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	reset := r.URL.Query().Get("reset") == "1"
	m, err := s.Xray.QueryStats(r.Context(), reset)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if m == nil {
		m = map[string]xray.Traffic{}
	}
	out := make(map[string]trafficJSON, len(m))
	for k, v := range m {
		out[k] = trafficJSON{Up: v.Up, Down: v.Down}
	}
	writeJSON(w, http.StatusOK, out)
}

type trafficJSON struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	if err := s.Xray.Restart(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Fingerprint is the hex SHA-256 of a DER certificate — the value the panel pins.
func Fingerprint(der []byte) string {
	h := sha256.Sum256(der)
	return hex.EncodeToString(h[:])
}

// errUnauthorized lets callers tell a wrong token from a transport problem.
var errUnauthorized = errors.New("the agent rejected the token")

// IsUnauthorized reports whether err came from a wrong token.
func IsUnauthorized(err error) bool { return errors.Is(err, errUnauthorized) }
