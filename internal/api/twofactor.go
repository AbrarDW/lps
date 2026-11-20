package api

// Two-factor sign-in (TOTP) and recovery codes.
//
// Sign-in with 2FA on is two requests: the password returns a short-lived
// ticket instead of a session, and the ticket plus a code (from the
// authenticator app, or a recovery code) creates the session.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abrardw/lps/internal/auth"
	"github.com/abrardw/lps/internal/store"
)

const (
	ticketTTL      = 5 * time.Minute
	ticketAttempts = 5
)

// loginTicket is a password check waiting for its second factor.
type loginTicket struct {
	userID   int64
	expires  time.Time
	attempts int
}

// issueTicket returns a ticket token for a user whose password was correct.
func (s *Server) issueTicket(userID int64) (string, error) {
	token, hash, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, t := range s.tickets { // prune
		if now.After(t.expires) {
			delete(s.tickets, k)
		}
	}
	s.tickets[hash] = &loginTicket{userID: userID, expires: now.Add(ticketTTL)}
	return token, nil
}

// checkSecondFactor verifies a TOTP code or recovery code for u. It records
// the use (so it can't be replayed) and reports how it was satisfied.
func (s *Server) checkSecondFactor(ctx context.Context, u *store.User, code string) (how string, ok bool, err error) {
	if auth.IsTOTPFormat(code) {
		step, valid := auth.VerifyTOTP(u.TOTPSecret, code, time.Now(), u.TOTPLast)
		if !valid {
			return "", false, nil
		}
		fresh, err := s.store.UseTOTPStep(ctx, u.ID, step)
		return "app", fresh, err
	}
	used, err := s.store.UseRecoveryCode(ctx, u.ID, auth.HashRecoveryCode(code))
	return "recovery", used, err
}

// handleLogin2FA completes a sign-in with the ticket from handleLogin.
func (s *Server) handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.Allowed(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed sign-ins; try again later")
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	hash := auth.HashToken(req.Ticket)
	s.mu.Lock()
	t := s.tickets[hash]
	if t != nil && time.Now().After(t.expires) {
		delete(s.tickets, hash)
		t = nil
	}
	s.mu.Unlock()
	if t == nil {
		writeError(w, http.StatusUnauthorized, "this sign-in expired; enter your password again")
		return
	}
	u, err := s.store.UserByID(r.Context(), t.userID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	how, ok, err := s.checkSecondFactor(r.Context(), u, req.Code)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if !ok {
		s.limiter.Fail(ip)
		s.mu.Lock()
		t.attempts++
		left := ticketAttempts - t.attempts
		if left <= 0 {
			delete(s.tickets, hash)
		}
		s.mu.Unlock()
		s.addEvent(&store.Event{Level: store.LevelWarning, Kind: "auth.2fa_failed", Subject: subjAuth, IP: ip,
			Message: fmt.Sprintf("Wrong two-factor code for %s", u.Username)})
		if left <= 0 {
			writeError(w, http.StatusUnauthorized, "too many wrong codes; enter your password again")
			return
		}
		writeError(w, http.StatusUnauthorized, "that code didn't work; codes change every 30 seconds and each works once")
		return
	}
	s.mu.Lock()
	delete(s.tickets, hash)
	s.mu.Unlock()
	s.limiter.Reset(ip)
	if err := s.startSession(w, r, u.ID); err != nil {
		s.internalError(w, err)
		return
	}
	msg := u.Username + " signed in (two-factor)"
	level := store.LevelInfo
	if how == "recovery" {
		left, _ := s.store.RecoveryCodesLeft(r.Context(), u.ID)
		msg = fmt.Sprintf("%s signed in with a recovery code (%d left)", u.Username, left)
		level = store.LevelWarning
	}
	s.addEvent(&store.Event{Level: level, Kind: "auth.login", Subject: subjAuth, Actor: u.Username, IP: ip, Message: msg})
	writeJSON(w, 200, u)
}

// currentUser is the signed-in user, reloaded so 2FA fields are fresh.
func (s *Server) currentUser(r *http.Request) (*store.User, error) {
	u, ok := r.Context().Value(userKey).(*store.User)
	if !ok || u == nil {
		return nil, errors.New("not signed in")
	}
	return s.store.UserByID(r.Context(), u.ID)
}

func (s *Server) handleGet2FA(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		s.internalError(w, err)
		return
	}
	left, err := s.store.RecoveryCodesLeft(r.Context(), u.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": u.TwoFactor, "recoveryCodesLeft": left})
}

// issuer names this PC in the authenticator app ("LPS (home-pc)").
func issuer() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return "LPS (" + strings.ToLower(h) + ")"
	}
	return "LPS"
}

// handleSetup2FA starts setup: it checks the password and returns a new
// secret as a QR code. Nothing changes at sign-in until it is confirmed.
func (s *Server) handleSetup2FA(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "wrong password")
		return
	}
	if u.TwoFactor {
		writeError(w, http.StatusConflict, "two-factor sign-in is already on; turn it off first to switch apps")
		return
	}
	secret, err := auth.NewTOTPSecret()
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.store.SetTOTPPending(r.Context(), u.ID, secret); err != nil {
		s.internalError(w, err)
		return
	}
	uri := auth.TOTPURI(issuer(), u.Username, secret)
	qr, err := auth.QRDataURL(uri)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"secret": secret, "uri": uri, "qr": qr})
}

// handleEnable2FA confirms setup with a code from the app and returns the
// recovery codes (shown once).
func (s *Server) handleEnable2FA(w http.ResponseWriter, r *http.Request) {
	u, err := s.currentUser(r)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	if u.TOTPPending == "" {
		writeError(w, http.StatusConflict, "start the setup first")
		return
	}
	step, ok := auth.VerifyTOTP(u.TOTPPending, req.Code, time.Now(), 0)
	if !ok {
		writeError(w, 400, "that code didn't match; check the time on your phone and try the next code")
		return
	}
	codes, hashes, err := auth.NewRecoveryCodes()
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.store.EnableTOTP(r.Context(), u.ID, u.TOTPPending, step, hashes); err != nil {
		s.internalError(w, err)
		return
	}
	s.info(r, "auth.2fa_on", subjAuth, 0, "", "Two-factor sign-in turned on for %s", u.Username)
	writeJSON(w, 200, map[string]any{"recoveryCodes": codes})
}

// handleDisable2FA turns 2FA off; it needs the password and a current code.
func (s *Server) handleDisable2FA(w http.ResponseWriter, r *http.Request) {
	u, ok := s.verifiedUser(w, r)
	if !ok {
		return
	}
	if err := s.store.DisableTOTP(r.Context(), u.ID); err != nil {
		s.internalError(w, err)
		return
	}
	s.warn(r, "auth.2fa_off", subjAuth, 0, "", "Two-factor sign-in turned off for %s", u.Username)
	w.WriteHeader(http.StatusNoContent)
}

// handleNewRecoveryCodes replaces the recovery codes; the old ones stop working.
func (s *Server) handleNewRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	u, ok := s.verifiedUser(w, r)
	if !ok {
		return
	}
	codes, hashes, err := auth.NewRecoveryCodes()
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.store.ReplaceRecoveryCodes(r.Context(), u.ID, hashes); err != nil {
		s.internalError(w, err)
		return
	}
	s.info(r, "auth.recovery_codes", subjAuth, 0, "", "New recovery codes made for %s", u.Username)
	writeJSON(w, 200, map[string]any{"recoveryCodes": codes})
}

// verifiedUser re-checks the password and a second-factor code before a
// sensitive 2FA change.
func (s *Server) verifiedUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	u, err := s.currentUser(r)
	if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &req) {
		return nil, false
	}
	if !u.TwoFactor {
		writeError(w, http.StatusConflict, "two-factor sign-in is off")
		return nil, false
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "wrong password")
		return nil, false
	}
	if _, ok, err := s.checkSecondFactor(r.Context(), u, req.Code); err != nil {
		s.internalError(w, err)
		return nil, false
	} else if !ok {
		writeError(w, http.StatusUnauthorized, "that code didn't work")
		return nil, false
	}
	return u, true
}
