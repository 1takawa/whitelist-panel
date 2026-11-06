// Package server wires the HTTP API and the embedded single-page frontend.
package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"anypanel2/internal/auth"
	"anypanel2/internal/config"
	"anypanel2/internal/panel"
	"anypanel2/internal/store"
)

const (
	sessionCookie = "ap_session"
	// csrfHeader must be present on every state-changing API request. Browsers cannot add a custom
	// header cross-origin without a CORS preflight, which this server never approves.
	csrfHeader = "X-Requested-With"
	csrfValue  = "anypanel"
)

func init() {
	// The flag font of the frontend; a minimal server image may have no system MIME table that knows it.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

type Server struct {
	cfg        config.Config
	store      store.Store
	auth       *auth.Manager
	svc        *panel.Service
	limiter    *auth.Limiter
	subLimiter *auth.Limiter
	// tokenLimiter bounds API-token requests per client IP (a burst allowance for scripts, a wall for guessing).
	tokenLimiter *auth.Limiter
	subPaths     subPathCache
	pageViews    pagePreviews // pages the settings editor is previewing
	ui           fs.FS
	log          *slog.Logger
}

type ctxKey struct{}

func New(cfg config.Config, st store.Store, am *auth.Manager, svc *panel.Service, ui fs.FS, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, store: st, auth: am, svc: svc, ui: ui, log: log,
		limiter:    auth.NewLimiter(5, time.Minute),   // login attempts per client IP
		subLimiter: auth.NewLimiter(120, time.Minute), // public subscription requests per client IP

		tokenLimiter: auth.NewLimiter(600, time.Minute),
	}
}

// Handler returns the full HTTP handler with security middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.requireAuth(s.logout))
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.me))
	s.registerAPI(mux)
	s.registerSub(mux)
	mux.Handle("/", s.spa())
	return s.securityHeaders(s.csrf(s.withSubRoute(mux)))
}

// ---- middleware ----

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			// A bearer token is not sent automatically by browsers, so requests carrying one need no CSRF header.
			// Such a request is only accepted by requireAuth if the token is valid; cookies are ignored for it.
			if _, bearer := bearerToken(r); !bearer && strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get(csrfHeader) != csrfValue {
				writeError(w, http.StatusForbidden, "missing "+csrfHeader+" header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken returns the API token from an "Authorization: Bearer" header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "Bearer ") {
		return "", false
	}
	return strings.TrimSpace(h[7:]), true
}

// requireAuth accepts a panel session or the API token. When an Authorization header is present only the token
// counts, so a stray cookie can never turn a bad token into a session.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tok, ok := bearerToken(r); ok {
			if !s.tokenLimiter.Allow(clientIP(r)) {
				w.Header().Set("Retry-After", "60")
				writeError(w, http.StatusTooManyRequests, "too many requests")
				return
			}
			if !s.svc.CheckAPIToken(r.Context(), tok) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, "api-token")))
			return
		}
		s.sessionAuth(next)(w, r)
	}
}

// requireSession is for actions that must never be reachable with the API token (managing the token itself,
// the Telegram bot token): a leaked token must not be able to extend or hide its own access.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc { return s.sessionAuth(next) }

// sessionUser returns the administrator owning the request's session cookie.
func (s *Server) sessionUser(r *http.Request) (string, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", auth.ErrNoSession
	}
	return s.auth.Authenticate(r.Context(), c.Value)
}

func (s *Server) sessionAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.sessionUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	}
}

// ---- handlers ----

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.log.Error("health: database ping failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.Allow(ip) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	token, expires, err := s.auth.Login(r.Context(), req.Username, req.Password, ip)
	switch {
	case err == auth.ErrInvalidCredentials:
		s.log.Warn("login failed", "ip", ip, "user", req.Username)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	case err != nil:
		s.log.Error("login error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(ctxKey{}).(string)
	writeJSON(w, http.StatusOK, map[string]string{"username": user})
}

// spa serves the embedded frontend; unknown non-API paths fall back to index.html for client-side routing.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" {
			if f, err := s.ui.Open(name); err == nil {
				st, serr := f.Stat()
				_ = f.Close()
				if serr == nil && !st.IsDir() {
					if strings.HasPrefix(name, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, s.ui, "index.html")
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// clientIP returns the peer address; behind the local reverse proxy it uses the first X-Forwarded-For entry.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	return host
}
