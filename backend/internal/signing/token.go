package signing

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"
)

// The verification token (CP85).
//
// # Three requirements, and the third is the one that gets forgotten
//
//  1. **Opaque.** It carries no patient data and nothing derived from any: not the prescription
//     id, not a hash of the patient, not a counter. There is no function in this file that takes
//     anything about a prescription, which is the strongest available statement that it cannot.
//  2. **Non-enumerable.** 160 bits from `crypto/rand`. Enumerating a space of 2^160 at a
//     thousand guesses a second — which the rate limiter allows nowhere near — takes longer than
//     the universe has existed. The requirement is met by entropy, not by rate limiting; the
//     rate limiting is there for everything else.
//  3. **Not stored.** What the database keeps is the SHA-256 digest, exactly as
//     `core.short_token` keeps a session challenge. The token itself exists in the response that
//     mints it and on the printed page, and nowhere else. Somebody who reads the whole database
//     cannot produce a working QR code for a prescription.
//
// # Why base32 and not base64
//
// The token is printed in a QR code and, when the code will not scan, read aloud or typed off a
// piece of paper by somebody at a pharmacy counter in Faridpur. Base32's alphabet has no
// lower-case, no `+`, no `/` and no padding to explain, so the same string survives a phone
// keypad, a fax and a photocopier. QR encoders also have a dedicated alphanumeric mode covering
// exactly upper-case letters and digits, which makes the printed symbol meaningfully smaller and
// therefore more likely to scan at the size a prescription sheet can spare.

const (
	// tokenBytes is 20 bytes — 160 bits, 32 base32 characters.
	//
	// Not 16. A 128-bit token is beyond enumeration too, and the extra four bytes cost four
	// characters in a QR code whose alphanumeric mode is cheap. The margin is for the thing
	// nobody plans for: a future where a token is also used as a lookup key somewhere with a
	// weaker rate limit.
	tokenBytes = 20
	// tokenLength is what a well-formed token looks like, for the cheap reject below.
	tokenLength = 32
)

// tokenEncoding is unpadded upper-case base32.
var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Token is a minted verification token: the string that goes in the QR, and the digest that
// goes in the database. Never both in the same place afterwards.
type Token struct {
	// Plaintext is what is printed. Returned to the signing physician's screen once and never
	// retrievable from the database.
	Plaintext string
	// Digest is the 32 bytes stored.
	Digest []byte
}

// NewToken mints one.
//
// It takes no arguments, and that is the point: there is nothing about a prescription, a patient
// or a facility that could find its way into a token minted by this function, so the "never
// patient data" requirement is a property of the signature rather than of anybody's discipline.
func NewToken() (Token, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, fmt.Errorf("generating a verification token: %w", err)
	}
	// Digest the encoded form, not the raw bytes: the client presents the string, so the string
	// is what the lookup hashes. `internal/auth`'s token helper records the afternoon that
	// inconsistency cost, and this is the same shape for the same reason.
	plaintext := tokenEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(plaintext))
	return Token{Plaintext: plaintext, Digest: digest[:]}, nil
}

// TokenDigest turns a token as presented into the digest to look up.
//
// It does not report whether the token is well-formed, deliberately: a malformed token hashes to
// a digest matching nothing and fails exactly as a wrong one does. One path, one outcome, and
// nothing to learn from the difference — which is what "no enumeration" means at the level of an
// individual request rather than at the level of the key space.
func TokenDigest(presented string) []byte {
	digest := sha256.Sum256([]byte(normaliseToken(presented)))
	return digest[:]
}

// normaliseToken undoes what a piece of paper and a human do to a token: lower case from a
// phone's autocorrect, spaces from reading it aloud in groups, a stray dash.
//
// **Not** a validity check. Normalising and rejecting are different jobs and mixing them is how
// a "helpful" error message becomes an oracle telling a prober that one string was closer than
// another.
func normaliseToken(presented string) string {
	presented = strings.ToUpper(strings.TrimSpace(presented))
	return strings.NewReplacer(" ", "", "-", "", "‑", "").Replace(presented)
}

// WellFormedToken reports whether a presented string could possibly be a token.
//
// Used only to decide whether to spend a database round trip, never to decide what to answer:
// a malformed token and an unknown one produce the same [VerdictNotVerified] response with the
// same shape. Its only job is to stop a flood of 4KB strings from becoming a flood of queries.
func WellFormedToken(presented string) bool {
	normalised := normaliseToken(presented)
	if len(normalised) != tokenLength {
		return false
	}
	_, err := tokenEncoding.DecodeString(normalised)
	return err == nil
}

// clientDigest fingerprints a caller's address so that abuse can be grouped without keeping
// addresses.
//
// Honest about what it is: an IPv4 address has 2^32 possibilities, and a SHA-256 of one is
// reversible by anybody willing to spend a minute on it. It is not a privacy control; it is a
// convention that stops an address appearing in clear in a table a support engineer opens. The
// address is not PHI and the row it lands on carries no patient, so the exposure is bounded by
// design rather than by this function.
func clientDigest(address string) []byte {
	digest := sha256.Sum256([]byte(address))
	return digest[:]
}
