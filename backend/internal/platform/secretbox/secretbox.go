// Package secretbox encrypts small secrets for storage — a TOTP seed, later a device key.
//
// AES-256-GCM under a key the process is configured with, with the key's id stored beside
// every ciphertext. That id is what makes this a stepping stone rather than a dead end: when
// hosting exists (D-01) and a KMS with it, a new key gets a new id, new secrets use it, old
// ones are re-encrypted on their next successful use, and nothing has to happen all at once.
//
// This is not a substitute for a KMS. A key in an environment variable is a key on the same
// machine as the ciphertexts. What it buys today is narrower and still worth having: a
// database backup, a `pg_dump` on a laptop, a stray copy of the table — none of them contain
// a usable TOTP seed. The threat it does not address, a compromise of the running server, is
// the same threat under a KMS, which would happily decrypt for the compromised process too.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize is AES-256.
const KeySize = 32

// ErrUnknownKey means a ciphertext names a key this process was not given. The secret is
// not lost — it is on a key that has been rotated out of this deployment's configuration.
var ErrUnknownKey = errors.New("secretbox: the ciphertext was sealed under a key this process does not have")

// ErrTampered means the ciphertext or its associated data does not authenticate.
var ErrTampered = errors.New("secretbox: the ciphertext does not authenticate")

// Ring holds the keys a process may open with and the one it seals with.
type Ring struct {
	current string
	keys    map[string]cipher.AEAD
}

// Key is a named key as configured.
type Key struct {
	ID string
	// Material is the raw 32 bytes, base64 in configuration.
	Material []byte
}

// NewRing builds a ring. The first key seals; every key opens.
func NewRing(keys ...Key) (*Ring, error) {
	if len(keys) == 0 {
		return nil, errors.New("secretbox: at least one key is required")
	}
	ring := &Ring{current: keys[0].ID, keys: make(map[string]cipher.AEAD, len(keys))}
	for _, k := range keys {
		if k.ID == "" {
			return nil, errors.New("secretbox: a key must have an id")
		}
		if len(k.Material) != KeySize {
			return nil, fmt.Errorf("secretbox: key %q is %d bytes, want %d", k.ID, len(k.Material), KeySize)
		}
		if _, dup := ring.keys[k.ID]; dup {
			return nil, fmt.Errorf("secretbox: key id %q appears twice", k.ID)
		}
		block, err := aes.NewCipher(k.Material)
		if err != nil {
			return nil, fmt.Errorf("secretbox: key %q: %w", k.ID, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("secretbox: key %q: %w", k.ID, err)
		}
		ring.keys[k.ID] = aead
	}
	return ring, nil
}

// ParseKey decodes a base64 key as it appears in configuration.
func ParseKey(id, encoded string) (Key, error) {
	material, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Key{}, fmt.Errorf("secretbox: key %q is not base64: %w", id, err)
	}
	return Key{ID: id, Material: material}, nil
}

// CurrentKeyID is the key new secrets are sealed under.
func (r *Ring) CurrentKeyID() string { return r.current }

// Seal encrypts plaintext. The nonce is prepended to the ciphertext.
//
// aad binds the ciphertext to its context — the user it belongs to — so a row copied onto
// another user's record does not decrypt there. It is authenticated, not encrypted, and must
// be supplied identically to Open.
func (r *Ring) Seal(plaintext, aad []byte) (ciphertext []byte, keyID string, err error) {
	aead := r.keys[r.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", fmt.Errorf("secretbox: reading a nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, aad), r.current, nil
}

// Open decrypts a ciphertext sealed by Seal under the named key.
func (r *Ring) Open(ciphertext []byte, keyID string, aad []byte) ([]byte, error) {
	aead, ok := r.keys[keyID]
	if !ok {
		return nil, ErrUnknownKey
	}
	if len(ciphertext) < aead.NonceSize() {
		return nil, ErrTampered
	}
	nonce, sealed := ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		return nil, ErrTampered
	}
	return plaintext, nil
}

// NeedsResealing reports whether a ciphertext is on an older key than the current one, so a
// caller that has just decrypted it can write it back under the current key.
func (r *Ring) NeedsResealing(keyID string) bool { return keyID != r.current }
