package api

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/abrardw/lps/internal/auth"
	"github.com/abrardw/lps/internal/store"
)

func code(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := auth.GenerateTOTP(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTwoFactorFlow(t *testing.T) {
	h := setup(t)
	h.login()

	// Setup needs the password; nothing changes until confirmed.
	if code := h.do("POST", "/api/auth/2fa/setup", map[string]string{"password": "wrong-password"}, nil); code != 401 {
		t.Fatalf("setup with wrong password: %d", code)
	}
	var setupResp struct{ Secret, URI, QR string }
	if code := h.do("POST", "/api/auth/2fa/setup", map[string]string{"password": "supersecret1"}, &setupResp); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	if !strings.Contains(setupResp.URI, "secret="+setupResp.Secret) || !strings.HasPrefix(setupResp.QR, "data:image/png;base64,") {
		t.Fatalf("setup resp = %+v", setupResp)
	}
	var st struct {
		Enabled           bool
		RecoveryCodesLeft int
	}
	h.do("GET", "/api/auth/2fa", nil, &st)
	if st.Enabled {
		t.Fatal("2FA on before confirming")
	}
	if code := h.do("POST", "/api/auth/2fa/enable", map[string]string{"code": "000000"}, nil); code != 400 {
		t.Fatalf("enable with a wrong code: %d", code)
	}
	now := time.Now()
	var enabled struct{ RecoveryCodes []string }
	if code := h.do("POST", "/api/auth/2fa/enable", map[string]string{"code": code(t, setupResp.Secret, now)}, &enabled); code != 200 {
		t.Fatalf("enable: %d", code)
	}
	if len(enabled.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes = %v", enabled.RecoveryCodes)
	}
	var me store.User
	h.do("GET", "/api/auth/me", nil, &me)
	if !me.TwoFactor {
		t.Fatal("me.twoFactor should be true")
	}

	// A new browser: the password alone no longer signs in.
	b := setupClientFor(t, h)
	var first struct {
		TwoFactorRequired bool
		Ticket            string
	}
	if code := b.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, &first); code != 200 || !first.TwoFactorRequired || first.Ticket == "" {
		t.Fatalf("login step 1: %d %+v", code, first)
	}
	if code := b.do("GET", "/api/vps", nil, nil); code != 401 {
		t.Fatalf("password alone gave a session: %d", code)
	}
	// The code that enabled 2FA can't be replayed in the same 30 seconds.
	if code := b.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": first.Ticket, "code": code(t, setupResp.Secret, now)}, nil); code != 401 {
		t.Fatalf("replayed code accepted: %d", code)
	}
	// The next step's code works (accepted for clock drift), once.
	next := code(t, setupResp.Secret, now.Add(30*time.Second))
	if code := b.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": first.Ticket, "code": next[:3] + " " + next[3:]}, nil); code != 200 {
		t.Fatalf("login step 2: %d", code)
	}
	if code := b.do("GET", "/api/vps", nil, nil); code != 200 {
		t.Fatalf("no session after 2FA: %d", code)
	}
	// The ticket is single-use.
	if code := b.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": first.Ticket, "code": next}, nil); code != 401 {
		t.Fatalf("ticket reused: %d", code)
	}

	// A recovery code works once.
	c := setupClientFor(t, h)
	c.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, &first)
	rc := strings.ToUpper(enabled.RecoveryCodes[0])
	if code := c.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": first.Ticket, "code": rc}, nil); code != 200 {
		t.Fatalf("recovery code: %d", code)
	}
	c2 := setupClientFor(t, h)
	c2.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, &first)
	if code := c2.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": first.Ticket, "code": rc}, nil); code != 401 {
		t.Fatalf("recovery code reused: %d", code)
	}
	h.do("GET", "/api/auth/2fa", nil, &st)
	if st.RecoveryCodesLeft != 9 {
		t.Fatalf("codes left = %d", st.RecoveryCodesLeft)
	}

	// Disabling needs the password and a code.
	if code := h.do("POST", "/api/auth/2fa/disable", map[string]string{"password": "supersecret1", "code": "111111"}, nil); code != 401 {
		t.Fatalf("disable with wrong code: %d", code)
	}
	if code := h.do("POST", "/api/auth/2fa/disable", map[string]string{"password": "supersecret1", "code": enabled.RecoveryCodes[1]}, nil); code != 204 {
		t.Fatalf("disable: %d", code)
	}
	d := setupClientFor(t, h)
	var plain store.User
	if code := d.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, &plain); code != 200 || plain.Username != "admin" {
		t.Fatalf("plain login after disable: %d %+v", code, plain)
	}

	// The activity feed recorded it all, without codes.
	var evs []store.Event
	h.do("GET", "/api/events?subject=auth&limit=50", nil, &evs)
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		for _, rc := range enabled.RecoveryCodes {
			if strings.Contains(strings.ToLower(e.Message), rc) {
				t.Fatalf("recovery code leaked: %s", e.Message)
			}
		}
	}
	got := strings.Join(kinds, ",")
	for _, k := range []string{"auth.2fa_on", "auth.2fa_failed", "auth.2fa_off"} {
		if !strings.Contains(got, k) {
			t.Fatalf("missing %s in %s", k, got)
		}
	}
}

func TestTwoFactorTicketLimits(t *testing.T) {
	h := setup(t)
	h.login()
	var s struct{ Secret string }
	h.do("POST", "/api/auth/2fa/setup", map[string]string{"password": "supersecret1"}, &s)
	h.do("POST", "/api/auth/2fa/enable", map[string]string{"code": code(t, s.Secret, time.Now())}, nil)

	b := setupClientFor(t, h)
	var first struct{ Ticket string }
	b.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, &first)
	for i := 0; i < ticketAttempts; i++ {
		b.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": first.Ticket, "code": "999999"}, nil)
	}
	// After too many wrong codes the ticket is gone, even with a right code.
	good := code(t, s.Secret, time.Now().Add(30*time.Second))
	if code := b.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": first.Ticket, "code": good}, nil); code != 401 && code != 429 {
		t.Fatalf("ticket survived %d wrong codes: %d", ticketAttempts, code)
	}
	// Expired tickets are refused.
	b2 := setupClientFor(t, h)
	b2.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, &first)
	h.srv.mu.Lock()
	for _, tk := range h.srv.tickets {
		tk.expires = time.Now().Add(-time.Second)
	}
	h.srv.mu.Unlock()
	if code := b2.do("POST", "/api/auth/login/2fa", map[string]string{"ticket": first.Ticket, "code": good}, nil); code != 401 && code != 429 {
		t.Fatalf("expired ticket accepted: %d", code)
	}
}

// setupClientFor is a second browser (its own cookie jar) on h's server.
func setupClientFor(t *testing.T, h *harness) *harness {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, ts: h.ts, c: &http.Client{Jar: jar}, eng: h.eng, srv: h.srv}
}
