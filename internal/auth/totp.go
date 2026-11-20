package auth

// Two-factor sign-in with time-based one-time passwords (RFC 6238), the
// 6-digit codes authenticator apps show, plus single-use recovery codes.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"rsc.io/qr"
)

const (
	totpPeriod = 30 // seconds per code
	totpDigits = 6
	// totpSkew accepts the previous and next code too, for clock drift.
	totpSkew = 1
	// RecoveryCodeCount is how many recovery codes a user gets.
	RecoveryCodeCount = 10
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret, base32-encoded as
// authenticator apps expect.
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// totpCode computes the code for one 30-second step.
func totpCode(secret string, counter int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secret, " ", "")))
	if err != nil {
		return "", fmt.Errorf("bad TOTP secret: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000), nil
}

// TOTPCounter is the time step a moment falls in.
func TOTPCounter(t time.Time) int64 { return t.Unix() / totpPeriod }

// VerifyTOTP checks a code against the steps around now. It returns the
// matching step, which the caller must record so a code can't be used twice.
// Steps at or below lastUsed are refused.
func VerifyTOTP(secret, code string, now time.Time, lastUsed int64) (int64, bool) {
	code = NormalizeCode(code)
	if len(code) != totpDigits {
		return 0, false
	}
	cur := TOTPCounter(now)
	for c := cur - totpSkew; c <= cur+totpSkew; c++ {
		if c <= lastUsed {
			continue
		}
		want, err := totpCode(secret, c)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return c, true
		}
	}
	return 0, false
}

// NormalizeCode drops spaces and dashes people type or paste with codes.
func NormalizeCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(code)))
}

// IsTOTPFormat reports whether a code looks like a 6-digit app code (as
// opposed to a recovery code).
func IsTOTPFormat(code string) bool {
	c := NormalizeCode(code)
	if len(c) != totpDigits {
		return false
	}
	for _, r := range c {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// TOTPURI is the otpauth:// link authenticator apps import (also as a QR code).
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// QRDataURL renders text as a QR code PNG data: URL for an <img>.
func QRDataURL(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	c.Scale = 6
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(c.PNG()), nil
}

// recoveryAlphabet has no look-alike characters (no 0/o, 1/l/i).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewRecoveryCodes returns codes like "k7m2-xq9p" and their hashes to store.
func NewRecoveryCodes() (codes, hashes []string, err error) {
	for range RecoveryCodeCount {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		var sb strings.Builder
		for i, x := range b {
			if i == 4 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryAlphabet[int(x)%len(recoveryAlphabet)])
		}
		codes = append(codes, sb.String())
		hashes = append(hashes, HashRecoveryCode(sb.String()))
	}
	return codes, hashes, nil
}

// HashRecoveryCode hashes a recovery code for storage. Codes are random
// (~40 bits) and single-use behind a rate-limited login, so a fast hash is
// enough.
func HashRecoveryCode(code string) string {
	s := sha256.Sum256([]byte("lps-recovery:" + NormalizeCode(code)))
	return hex.EncodeToString(s[:])
}

// GenerateTOTP returns the code for time t (what an authenticator app shows).
func GenerateTOTP(secret string, t time.Time) (string, error) {
	return totpCode(secret, TOTPCounter(t))
}
