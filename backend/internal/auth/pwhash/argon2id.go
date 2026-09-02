// Package pwhash implements argon2id password hashing.
//
// It is a package of its own, rather than a file in auth, for one practical reason: it is
// the only part of authentication that needs a cryptography library, and keeping it
// separate means every rule *around* hashing — the throttle, the session lifecycle, the
// refresh rotation — stays testable without one. That separation was worth having anyway;
// it also happens to be what lets the logic be verified where it is written.
//
// Nothing here is invented. Argon2id comes from golang.org/x/crypto; this package chooses
// parameters, encodes the result, and parses it back.
package pwhash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the cost of a hash.
//
// The defaults sit in the band OWASP recommends for argon2id and are deliberately on the
// memory-heavy side, because memory is what makes a GPU attack expensive and a clinic
// server has memory to spare at three in the morning when nobody is logging in.
//
// THEY SHOULD BE RE-BENCHMARKED ON THE REAL SERVER. The target is 250–500 ms per hash: fast
// enough that a nurse at the start of a clinic does not notice, slow enough that an offline
// attacker with the table gets very little for their electricity. What that costs depends
// on hardware nobody has bought yet (D-30 is contingent on D-01), so these are a starting
// point with a number to aim at rather than a finished answer.
type Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams: 46 MiB, one pass, one lane.
func DefaultParams() Params {
	lanes := uint8(1)
	if n := runtime.NumCPU(); n > 1 {
		lanes = 2
	}
	return Params{
		Memory:      47104, // 46 MiB
		Iterations:  2,
		Parallelism: lanes,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// Hasher hashes and verifies passwords.
type Hasher struct{ params Params }

func New(p Params) *Hasher { return &Hasher{params: p} }

// Hash returns a PHC-format string: the algorithm, its version, its parameters, the salt
// and the digest.
//
// The parameters travel with the hash rather than living in configuration, so raising the
// cost later does not invalidate every existing password: an old hash still verifies under
// the parameters it was made with, and is rehashed on the next successful login.
func (h *Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("reading salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt,
		h.params.Iterations, h.params.Memory, h.params.Parallelism, h.params.KeyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.Memory, h.params.Iterations, h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// ErrMalformed means the stored string is not an argon2id hash this package wrote.
var ErrMalformed = errors.New("the stored password hash is not in the expected format")

// Verify reports whether the password matches, and whether the hash should be upgraded.
//
// The comparison is constant-time. That matters less here than it would for a token —
// an attacker who can measure it already has the hash — but it costs nothing and removes a
// question nobody should have to re-answer during a review.
func (h *Hasher) Verify(password, encoded string) (ok bool, needsRehash bool, err error) {
	stored, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}

	candidate := argon2.IDKey([]byte(password), salt,
		stored.Iterations, stored.Memory, stored.Parallelism, uint32(len(key)))

	if subtle.ConstantTimeCompare(key, candidate) != 1 {
		return false, false, nil
	}
	return true, h.weakerThanCurrent(stored), nil
}

// weakerThanCurrent reports whether a verified hash was made with less work than we now
// require, so a successful login can quietly upgrade it.
func (h *Hasher) weakerThanCurrent(stored Params) bool {
	return stored.Memory < h.params.Memory ||
		stored.Iterations < h.params.Iterations ||
		stored.Parallelism < h.params.Parallelism
}

func decode(encoded string) (p Params, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrMalformed
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, ErrMalformed
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("%w: argon2 version %d, this build speaks %d",
			ErrMalformed, version, argon2.Version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return p, nil, nil, ErrMalformed
	}

	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return p, nil, nil, ErrMalformed
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return p, nil, nil, ErrMalformed
	}
	return p, salt, key, nil
}

// Dummy is a hash of nothing in particular, used to spend the same time on a login for an
// account that does not exist as one that does.
//
// Without it, the server answers "is there a user with this code" by how quickly it says
// no — which is the whole point of returning an identical error either way, undone by a
// stopwatch. The auth service verifies against this when no user is found, and throws the
// result away.
func (h *Hasher) Dummy() string {
	// Computed once at construction would be better still; it is computed on demand
	// because a package-level init that spends 300 ms of CPU surprises whoever imports it.
	encoded, err := h.Hash("dummy password for constant-time refusal")
	if err != nil {
		// rand.Read failing is not a condition this can paper over, but a login path must
		// not panic. An unparseable string makes Verify fail closed, which is the right
		// answer to "the system cannot generate randomness".
		return "$argon2id$unavailable"
	}
	return encoded
}
