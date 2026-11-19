// Package auth handles password hashing, session tokens and login rate limiting.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLen is enforced for dashboard accounts, which may be exposed to the internet.
const MinPasswordLen = 10

// Cost is the bcrypt work factor. Tests may lower it to run faster.
var Cost = 12

// ErrWeakPassword is returned for passwords shorter than MinPasswordLen.
var ErrWeakPassword = errors.New("password must be at least 10 characters")

// HashPassword returns a bcrypt hash.
func HashPassword(pw string) (string, error) {
	if len(pw) < MinPasswordLen {
		return "", ErrWeakPassword
	}
	if len(pw) > 72 { // bcrypt limit
		return "", errors.New("password must be at most 72 bytes")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), Cost)
	return string(h), err
}

// CheckPassword compares a password with its hash.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// dummyHash is compared against when the user does not exist, to keep timing uniform.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("lps-dummy-password"), 12)

// CheckPasswordOrDummy runs a bcrypt comparison even when hash is empty.
func CheckPasswordOrDummy(hash, pw string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw))
		return false
	}
	return CheckPassword(hash, pw)
}

// NewToken returns a random session token (sent to the client) and its SHA-256 hash (stored).
func NewToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken hashes a session token for storage/lookup.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Limiter is a simple fixed-window failed-login limiter keyed by client IP.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
	now    func() time.Time
}

type window struct {
	start time.Time
	count int
}

// NewLimiter allows max failures per key per window.
func NewLimiter(max int, w time.Duration) *Limiter {
	return &Limiter{max: max, window: w, hits: map[string]*window{}, now: time.Now}
}

// Allowed reports whether key may attempt a login now.
func (l *Limiter) Allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.hits[key]
	if !ok || l.now().Sub(w.start) > l.window {
		return true
	}
	return w.count < l.max
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) > l.window {
		l.hits[key] = &window{start: now, count: 1}
		return
	}
	w.count++
}

// Reset clears a key after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
