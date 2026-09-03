// Package devicesig defines how an enrolled device proves a request is its own.
//
// The scheme is deliberately small. A device holds an Ed25519 private key that never leaves
// it; the server holds the public key. Every request the device makes carries four headers:
//
//	X-Device-Id:         the device's id
//	X-Device-Timestamp:  seconds since the epoch, by the device's clock
//	X-Device-Nonce:      16 random bytes, base64url, fresh per request
//	X-Device-Signature:  Ed25519 over the canonical string, base64
//
// and the canonical string is
//
//	method "\n" path "\n" timestamp "\n" nonce "\n" hex(sha256(body)) "\n" device-id
//
// Signing the method and path binds the signature to this request rather than to any
// request; the body digest binds it to these bytes; the timestamp bounds how long a captured
// request can be replayed, and the nonce closes the rest of that window; the device id in
// the string means a signature cannot be lifted from one device's request and presented
// under another's id — it would not verify under that id's key anyway, but belt and braces
// cost one line here.
//
// Not signed: query strings (a path with a query is signed as the path alone — clinical
// writes are POSTs with bodies, and a GET's query is not something worth binding), headers
// other than these (the access token is verified separately and binds the session to the
// device by a different route — see docs/identity.md §9), and the scheme or host (the
// server knows which host it is).
//
// This package is pure: no HTTP, no store, no clock. The middleware in httpx assembles a
// Proof from a request and asks a verifier; the client, in whatever language, builds the
// same canonical string. The test vectors in devicesig_test.go are the contract with the
// mobile implementation.
package devicesig

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Header names. Exported so that the middleware, the tests and the CORS allowlist agree.
const (
	HeaderID        = "X-Device-Id"
	HeaderTimestamp = "X-Device-Timestamp"
	HeaderNonce     = "X-Device-Nonce"
	HeaderSignature = "X-Device-Signature"
	// HeaderAppVersion is informational: the device says which build it is running, and
	// the admin screen shows it. Not signed, not trusted for anything but display.
	HeaderAppVersion = "X-Device-App-Version"
)

// Headers lists every header the scheme uses, for the CORS allowlist.
var Headers = []string{HeaderID, HeaderTimestamp, HeaderNonce, HeaderSignature, HeaderAppVersion}

// MaxSkew is how far the device's clock may be from the server's. Five minutes: enough for
// a tablet whose clock is set by hand, small enough that a captured request is worthless
// by the time anyone could use it.
const MaxSkew = 5 * time.Minute

// NonceLength is the number of random bytes in a nonce.
const NonceLength = 16

// Proof is what a request presented.
type Proof struct {
	DeviceID  string
	Timestamp int64
	Nonce     string
	Signature []byte

	Method     string
	Path       string
	BodyDigest [32]byte
}

var (
	ErrMalformed = errors.New("device proof is malformed")
	ErrSkew      = errors.New("device timestamp is outside the allowed skew")
	ErrSignature = errors.New("device signature does not verify")
)

// Canonical builds the string that is signed.
func Canonical(method, path string, timestamp int64, nonce string, bodyDigest [32]byte, deviceID string) []byte {
	var b strings.Builder
	b.WriteString(strings.ToUpper(method))
	b.WriteByte('\n')
	b.WriteString(path)
	b.WriteByte('\n')
	b.WriteString(strconv.FormatInt(timestamp, 10))
	b.WriteByte('\n')
	b.WriteString(nonce)
	b.WriteByte('\n')
	b.WriteString(hex.EncodeToString(bodyDigest[:]))
	b.WriteByte('\n')
	b.WriteString(deviceID)
	return []byte(b.String())
}

// DigestBody is the body half of the canonical string. An empty body digests to the
// well-known SHA-256 of nothing, so a GET and a POST with no body sign the same way.
func DigestBody(body []byte) [32]byte { return sha256.Sum256(body) }

// NewNonce returns a fresh nonce, base64url without padding.
func NewNonce() (string, error) {
	buf := make([]byte, NonceLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Sign produces the signature a device attaches. Used by the tests and by any Go client;
// the tablet does the same thing in TypeScript.
func Sign(priv ed25519.PrivateKey, p Proof) []byte {
	return ed25519.Sign(priv, Canonical(p.Method, p.Path, p.Timestamp, p.Nonce, p.BodyDigest, p.DeviceID))
}

// Verify checks a proof against a public key and the server's clock.
//
// Skew is checked before the signature so that a replayed request from last week is refused
// without spending a signature verification on it — and so that the error says which.
func Verify(pub ed25519.PublicKey, p Proof, now time.Time) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrMalformed
	}
	if p.DeviceID == "" || p.Nonce == "" || len(p.Signature) != ed25519.SignatureSize {
		return ErrMalformed
	}
	if raw, err := base64.RawURLEncoding.DecodeString(p.Nonce); err != nil || len(raw) < NonceLength {
		return ErrMalformed
	}
	at := time.Unix(p.Timestamp, 0)
	if d := now.Sub(at); d > MaxSkew || d < -MaxSkew {
		return ErrSkew
	}
	if !ed25519.Verify(pub, Canonical(p.Method, p.Path, p.Timestamp, p.Nonce, p.BodyDigest, p.DeviceID), p.Signature) {
		return ErrSignature
	}
	return nil
}

// EncodeSignature and DecodeSignature are the header encoding: standard base64.
func EncodeSignature(sig []byte) string { return base64.StdEncoding.EncodeToString(sig) }

func DecodeSignature(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, ErrMalformed
	}
	return raw, nil
}

// EncodePublicKey and DecodePublicKey are the wire encoding of a public key: standard
// base64 of the 32 raw bytes.
func EncodePublicKey(pub ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(pub) }

func DecodePublicKey(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("public key must be 32 bytes of Ed25519, base64")
	}
	return ed25519.PublicKey(raw), nil
}

// ParseTimestamp reads the header form.
func ParseTimestamp(s string) (int64, error) {
	ts, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || ts <= 0 {
		return 0, ErrMalformed
	}
	return ts, nil
}
