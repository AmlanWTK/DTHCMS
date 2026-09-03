// Package totp implements RFC 6238 time-based one-time passwords over RFC 4226 HOTP.
//
// Written here rather than imported. The algorithm is an HMAC, a truncation and a modulus —
// small enough that a dependency would be more code to audit than the implementation, and
// the RFC ships test vectors that make correctness a matter of running them. It also keeps
// the second factor free of any library whose release cadence the clinic does not control.
//
// What is fixed: SHA-1 (what every authenticator app speaks), six digits, thirty-second
// steps. What is a parameter: the drift window, because a phone's clock and a server's
// clock are never quite the same and the difference is a policy, not a constant.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 mandates HMAC-SHA1 for interoperability with authenticator apps; it is a MAC, not a hash of anything secret.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Digits in a code. Six is what every authenticator app shows.
	Digits = 6
	// Period is the length of one time step.
	Period = 30 * time.Second
	// SecretBytes is the length of a generated secret: 160 bits, RFC 4226's recommendation.
	SecretBytes = 20
)

// Encoding is the base32 alphabet authenticator apps expect, unpadded.
var Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a fresh random secret, base32-encoded for provisioning.
func NewSecret() (string, error) {
	raw := make([]byte, SecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("reading randomness for a TOTP secret: %w", err)
	}
	return Encoding.EncodeToString(raw), nil
}

// Step is the time-step counter for an instant.
func Step(at time.Time) int64 {
	return at.Unix() / int64(Period/time.Second)
}

// Code computes the code for a secret at a step. The secret is base32.
func Code(secret string, step int64) (string, error) {
	key, err := Encoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("the TOTP secret is not base32: %w", err)
	}

	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	// RFC 4226 §5.3: dynamic truncation.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	return fmt.Sprintf("%0*d", Digits, value%1_000_000), nil
}

// Verify reports whether a code is right for the secret at the instant, allowing the clock
// on either side to be off by up to `window` steps in either direction.
//
// It returns the step the code matched, so the caller can refuse a second use of the same
// step: a code that was right thirty seconds ago and has been seen already is a replay,
// not a login.
//
// The comparison is constant-time. Over a network the timing of a string compare is not
// realistically observable, but there is no reason to make the reviewer prove that.
func Verify(secret, code string, at time.Time, window int) (matched int64, ok bool) {
	code = strings.TrimSpace(code)
	if len(code) != Digits {
		return 0, false
	}
	if window < 0 {
		window = 0
	}

	current := Step(at)
	// Nearest first, so the common case — a correct code at the current step — is found
	// without evaluating the neighbours, and so the matched step is the closest one.
	for delta := 0; delta <= window; delta++ {
		for _, step := range []int64{current + int64(delta), current - int64(delta)} {
			want, err := Code(secret, step)
			if err != nil {
				return 0, false
			}
			if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
				return step, true
			}
			if delta == 0 {
				break // current + 0 == current - 0
			}
		}
	}
	return 0, false
}

// ProvisioningURI is the otpauth:// URI an authenticator app reads from a QR code.
//
// issuer names the system, account names the person, and both appear in the app's list —
// "DTHCMS (E001)" — which is what lets a physician with several codes pick the right one.
func ProvisioningURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(Digits))
	query.Set("period", fmt.Sprint(int(Period/time.Second)))
	return "otpauth://totp/" + label + "?" + query.Encode()
}
