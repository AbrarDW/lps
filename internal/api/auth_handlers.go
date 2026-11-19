package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"time"

	"github.com/abrardw/lps/internal/auth"
	"github.com/abrardw/lps/internal/platform"
	"github.com/abrardw/lps/internal/store"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"needsSetup": n == 0})
}

// handleSetup creates the first admin account. It only works while no user exists.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	if !usernameRe.MatchString(c.Username) {
		writeError(w, 400, "username must be 3-32 characters: letters, digits, _ . -")
		return
	}
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	u, err := s.store.CreateFirstUser(r.Context(), c.Username, hash)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "setup already completed")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		s.internalError(w, err)
		return
	}
	s.addEvent(&store.Event{Kind: "auth.setup", Subject: subjAuth, Actor: u.Username, IP: clientIP(r),
		Message: fmt.Sprintf("Admin account %s created", u.Username)})
	writeJSON(w, 201, u)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed logins; try again later")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	u, err := s.store.UserByUsername(r.Context(), c.Username)
	hash := ""
	if err == nil {
		hash = u.PasswordHash
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, err)
		return
	}
	if !auth.CheckPasswordOrDummy(hash, c.Password) {
		s.limiter.Fail(ip)
		s.log.Warn("failed login", "user", c.Username, "ip", ip)
		name := c.Username
		if len(name) > 64 {
			name = name[:64]
		}
		s.addEvent(&store.Event{Level: store.LevelWarning, Kind: "auth.login_failed", Subject: subjAuth, IP: ip,
			Message: fmt.Sprintf("Failed sign-in as %q", name)})
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.limiter.Reset(ip)
	if err := s.startSession(w, r, u.ID); err != nil {
		s.internalError(w, err)
		return
	}
	s.addEvent(&store.Event{Kind: "auth.login", Subject: subjAuth, Actor: u.Username, IP: ip, Message: u.Username + " signed in"})
	writeJSON(w, 200, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, hash, err := auth.NewToken()
	if err != nil {
		return err
	}
	exp := time.Now().Add(s.cfg.SessionTTL)
	if err := s.store.CreateSession(r.Context(), hash, userID, exp); err != nil {
		return err
	}
	_ = s.store.DeleteExpiredSessions(r.Context())
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: exp,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.cfg.SecureCookies || isHTTPS(r),
	})
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, r.Context().Value(userKey))
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"version": s.cfg.Version,
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
		"bindIp":  s.cfg.BindIP,
	}
	if v, err := s.engine.Ping(r.Context()); err != nil {
		resp["docker"] = map[string]any{"ok": false, "error": err.Error(), "hint": platform.DockerHint()}
	} else {
		resp["docker"] = map[string]any{"ok": true, "version": v}
	}
	writeJSON(w, 200, resp)
}
