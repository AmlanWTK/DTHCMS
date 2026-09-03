package audit

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// Signing (criterion 4): an exported trail carries a signature that anyone holding the
// clinic's public key can check, so a PDF produced for a court or a regulator can be shown
// to be the one the system produced, byte for byte.
//
// Ed25519 over the SHA-256 of the file. The seed is configuration
// (DTHCMS_AUDIT_SIGNING_SEED); the public key is published by the API and printed in the
// operations guide, so verification does not need the server — `go run ./tools/auditverify`
// does it offline from the PDF, the signature and the key.

// Signer holds the clinic's export key.
type Signer struct {
	keyID string
	priv  ed25519.PrivateKey
}

// Signature is what travels with a file.
type Signature struct {
	// KeyID names the key, so a rotated key can still verify old exports.
	KeyID string `json:"key_id"`
	// Algorithm is always "ed25519-sha256": the signature is over the SHA-256 digest.
	Algorithm string `json:"algorithm"`
	// Digest is the SHA-256 of the file, base64, so a reader can see what was signed.
	Digest string `json:"sha256"`
	// Value is the signature, base64.
	Value string `json:"signature"`
}

const Algorithm = "ed25519-sha256"

// NewSigner takes a 32-byte seed. A seed of the wrong size is a configuration error.
func NewSigner(keyID string, seed []byte) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("audit signing seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	if keyID == "" {
		return nil, errors.New("audit signing key needs an id")
	}
	return &Signer{keyID: keyID, priv: ed25519.NewKeyFromSeed(seed)}, nil
}

func (s *Signer) KeyID() string { return s.keyID }

// PublicKey is the verification half, base64.
func (s *Signer) PublicKey() string {
	return base64.StdEncoding.EncodeToString(s.priv.Public().(ed25519.PublicKey))
}

// Sign signs a file.
func (s *Signer) Sign(file []byte) Signature {
	digest := sha256.Sum256(file)
	return Signature{
		KeyID: s.keyID, Algorithm: Algorithm,
		Digest: base64.StdEncoding.EncodeToString(digest[:]),
		Value:  base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, digest[:])),
	}
}

// Verify checks a file against a signature and a public key. It is a package function,
// not a method, because the verifier does not have the private key and must not need it.
func Verify(publicKey string, file []byte, sig Signature) error {
	if sig.Algorithm != Algorithm {
		return fmt.Errorf("unknown signature algorithm %q", sig.Algorithm)
	}
	pub, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("the public key is not a valid ed25519 key")
	}
	value, err := base64.StdEncoding.DecodeString(sig.Value)
	if err != nil {
		return errors.New("the signature is not valid base64")
	}
	digest := sha256.Sum256(file)
	if sig.Digest != "" && sig.Digest != base64.StdEncoding.EncodeToString(digest[:]) {
		return errors.New("the file's digest does not match the signature's")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), digest[:], value) {
		return errors.New("the signature does not verify: the file is not the one that was signed, or the key is wrong")
	}
	return nil
}
