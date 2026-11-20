package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B test vectors (SHA-1, 8 digits; we use the last 6).
func TestTOTPVectors(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		got, err := totpCode(secret, ts/30)
		if err != nil || got != want {
			t.Errorf("t=%d: got %s, want %s", ts, got, want)
		}
	}
}

func TestVerifyTOTP(t *testing.T) {
	s, _ := NewTOTPSecret()
	now := time.Unix(1_700_000_000, 0)
	cur := TOTPCounter(now)
	code, _ := totpCode(s, cur)
	if c, ok := VerifyTOTP(s, code[:3]+" "+code[3:], now, 0); !ok || c != cur {
		t.Fatal("current code with a space should verify")
	}
	prev, _ := totpCode(s, cur-1)
	if _, ok := VerifyTOTP(s, prev, now, 0); !ok {
		t.Fatal("previous step should be accepted (clock drift)")
	}
	old, _ := totpCode(s, cur-3)
	if _, ok := VerifyTOTP(s, old, now, 0); ok {
		t.Fatal("old code accepted")
	}
	if _, ok := VerifyTOTP(s, code, now, cur); ok {
		t.Fatal("a used step must not verify again")
	}
	if _, ok := VerifyTOTP(s, "12345", now, 0); ok {
		t.Fatal("short code accepted")
	}
}

func TestRecoveryCodesAndURI(t *testing.T) {
	codes, hashes, err := NewRecoveryCodes()
	if err != nil || len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatal(err)
	}
	if len(codes[0]) != 9 || codes[0][4] != '-' || HashRecoveryCode(strings.ToUpper(codes[0])) != hashes[0] {
		t.Fatalf("code %q", codes[0])
	}
	if IsTOTPFormat(codes[0]) || !IsTOTPFormat("123 456") {
		t.Fatal("format detection")
	}
	uri := TOTPURI("LPS (home-pc)", "admin", "ABC")
	if !strings.HasPrefix(uri, "otpauth://totp/LPS%20%28home-pc%29:admin?") || !strings.Contains(uri, "secret=ABC") {
		t.Fatalf("uri %s", uri)
	}
	if d, err := QRDataURL(uri); err != nil || !strings.HasPrefix(d, "data:image/png;base64,") {
		t.Fatal("qr", err)
	}
}
