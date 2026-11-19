package auth

import (
	"testing"
	"time"
)

func TestPasswords(t *testing.T) {
	if _, err := HashPassword("short"); err != ErrWeakPassword {
		t.Fatalf("want ErrWeakPassword, got %v", err)
	}
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse battery") || CheckPassword(h, "wrong password!") {
		t.Fatal("CheckPassword mismatch")
	}
	if CheckPasswordOrDummy("", "anything") {
		t.Fatal("empty hash must never match")
	}
}

func TestTokens(t *testing.T) {
	a, ha, _ := NewToken()
	b, _, _ := NewToken()
	if a == b || HashToken(a) != ha || ha == a {
		t.Fatal("token generation/hashing broken")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	l.Fail("ip")
	l.Fail("ip")
	if l.Allowed("ip") {
		t.Fatal("should be blocked after 2 failures")
	}
	if !l.Allowed("other") {
		t.Fatal("other key should be allowed")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allowed("ip") {
		t.Fatal("window should have expired")
	}
	l.Fail("ip")
	l.Reset("ip")
	if !l.Allowed("ip") {
		t.Fatal("reset should clear")
	}
}
