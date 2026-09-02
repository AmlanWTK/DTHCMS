package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// Tokens are 32 bytes of randomness, and the server keeps only their digest.
//
// ADR-0011 has the reasoning. The short version: the session registry is consulted on every
// request anyway, because revocation has to take effect within one request, so a signature
// would verify a token that is about to be checked statefully regardless. What is left is a
// random string and a lookup — which cannot suffer algorithm confusion, key rotation, clock
// skew, or claims that were true when they were signed.

// TokenBytes is the length of a raw token. Thirty-two bytes is 256 bits, which is past the
// point where the number of guesses matters.
const TokenBytes = 32

// Token is a credential as the client holds it, and as the server stores it.
//
// The two halves are kept together in one type so that the plaintext cannot be persisted by
// accident: everything that writes to the database takes Digest, and Plaintext exists only
// long enough to be put in a response.
type Token struct {
	// Plaintext goes to the client, once, and is never recoverable afterwards.
	Plaintext string
	// Digest is what the database holds. Thirty-two bytes, so a leak of the table yields
	// nothing presentable.
	Digest []byte
}

// NewToken mints a token from the system's cryptographic randomness.
//
// An error here is not something to retry or degrade around: it means the operating system
// could not produce randomness, and every credential issued after that point would be
// suspect. It is returned so the caller refuses the login.
func NewToken() (Token, error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, fmt.Errorf("generating a token: %w", err)
	}

	// Digest the encoded form, not the raw bytes. The client sends the string, so the
	// string is what the server hashes to find the row — and the two must agree or every
	// login mints a session that can never be found again.
	//
	// The first draft hashed `raw` here and the encoded form in DigestOf. Nothing about the
	// code looked wrong; both lines are the obvious thing to write in isolation.
	plaintext := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(plaintext))

	return Token{Plaintext: plaintext, Digest: digest[:]}, nil
}

// DigestOf turns a token as presented by a client into the digest to look up.
//
// It does not report whether the token is well-formed, deliberately. A malformed token
// hashes to a digest that matches nothing, so the lookup fails exactly as a wrong token
// does — one path, one outcome, and no way to learn from the difference.
func DigestOf(plaintext string) []byte {
	digest := sha256.Sum256([]byte(plaintext))
	return digest[:]
}

// DigestOfRaw is DigestOf for bytes that are already decoded, used where a client address
// or another non-secret is being fingerprinted rather than a credential.
func DigestOfRaw(value []byte) []byte {
	digest := sha256.Sum256(value)
	return digest[:]
}
