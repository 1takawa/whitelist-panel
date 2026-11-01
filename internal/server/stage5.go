package server

import (
	"errors"
	"net/http"
	"time"

	"anypanel2/internal/auth"
	"anypanel2/internal/model"
	"anypanel2/internal/panel"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.DashboardNow(r.Context())
	s.respond(w, v, err)
}

// ---- administrator account ----

// updateAccount changes the administrator's login and password. Every session ends, so the cookie is cleared too.
func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](w, r)
	if !ok {
		return
	}
	current, _ := r.Context().Value(ctxKey{}).(string)
	switch err := s.auth.UpdateCredentials(r.Context(), current, req.Username, req.Password); {
	case errors.Is(err, auth.ErrInvalidUsername), errors.Is(err, auth.ErrPasswordLength), errors.Is(err, auth.ErrUsernameTaken):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.log.Info("administrator credentials changed", "user", req.Username)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- database export and import ----

// exportDB downloads the database. The users file is also available to scripts with the API token; the full one
// carries the administrator's password hash and the bot token, so it needs a signed-in session.
func (s *Server) exportDB(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == panel.ExportFull {
		_, viaToken := bearerToken(r)
		if _, err := s.sessionUser(r); viaToken || err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	}
	name, data, err := s.svc.ExportDB(r.Context(), kind)
	if err != nil {
		s.fail(w, err)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(data)
}

// importDB reads the uploaded file (the request body itself). A full file replaces everything and signs everybody
// out, so the cookie is cleared in that case.
func (s *Server) importDB(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(5 * time.Minute))
	_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, panel.ReadLimit+1)
	data, err := panel.ReadAll(r.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	q := r.URL.Query()
	rep, err := s.svc.ImportDB(r.Context(), data, panel.ImportOptions{DryRun: q.Get("dryRun") == "1", ExpiryFrom: q.Get("expiryFrom")})
	if err != nil {
		s.fail(w, err)
		return
	}
	if rep.Kind == panel.ExportFull && !rep.DryRun {
		s.invalidateSubPath()
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteStrictMode,
		})
		s.log.Warn("database replaced by a full import")
	}
	writeJSON(w, http.StatusOK, rep)
}

// ---- API token ----

func (s *Server) apiTokenInfo(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.APITokenInfo(r.Context())
	s.respond(w, v, err)
}

// apiTokenGenerate returns the new token. It is the only time the plain value exists.
func (s *Server) apiTokenGenerate(w http.ResponseWriter, r *http.Request) {
	tok, info, err := s.svc.GenerateAPIToken(r.Context())
	s.respond(w, map[string]any{"token": tok, "info": info}, err)
}

func (s *Server) apiTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RevokeAPIToken(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- Telegram ----

func (s *Server) telegramStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.TelegramStatus(r.Context())
	s.respond(w, v, err)
}

func (s *Server) telegramSave(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[panel.TelegramSave](w, r)
	if !ok {
		return
	}
	v, err := s.svc.SaveTelegram(r.Context(), req)
	s.respond(w, v, err)
}

func (s *Server) telegramTest(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.TelegramTest(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- extra servers ----

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.ListServers(r.Context())
	s.respond(w, v, err)
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[model.Server](w, r)
	if !ok {
		return
	}
	v, token, err := s.svc.CreateServer(r.Context(), in)
	s.respond(w, map[string]any{"item": v, "token": token}, err)
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[model.Server](w, r)
	if !ok {
		return
	}
	v, err := s.svc.UpdateServer(r.Context(), r.PathValue("id"), in)
	s.respond(w, v, err)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	note, err := s.svc.DeleteServerNote(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "warning": note})
}

func (s *Server) testServer(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.TestServer(r.Context(), r.PathValue("id"))
	s.respond(w, v, err)
}

func (s *Server) restartServer(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RestartServer(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// serverToken issues a new token; like the API token it is shown once.
func (s *Server) serverToken(w http.ResponseWriter, r *http.Request) {
	tok, err := s.svc.RegenerateServerToken(r.Context(), r.PathValue("id"))
	s.respond(w, map[string]string{"token": tok}, err)
}
