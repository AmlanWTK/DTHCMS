package devicesig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func proof(t *testing.T, priv ed25519.PrivateKey) Proof {
	t.Helper()
	nonce, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	p := Proof{
		DeviceID:   "5c1d2f9e-0a41-4b8c-9d3e-2f6a7b8c9d0e",
		Timestamp:  fixedNow.Unix(),
		Nonce:      nonce,
		Method:     "post",
		Path:       "/v1/observations",
		BodyDigest: DigestBody([]byte(`{"value":72}`)),
	}
	p.Signature = Sign(priv, p)
	return p
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	pub, priv := keypair(t)
	if err := Verify(pub, proof(t, priv), fixedNow); err != nil {
		t.Fatalf("a signature by the right key must verify: %v", err)
	}
}

// The canonical string is the contract with the mobile client. This vector is duplicated
// in mobile/test/device-signing.test.ts; changing one without the other fails there.
func TestCanonicalVector(t *testing.T) {
	digest := DigestBody([]byte(`{"a":1}`))
	got := string(Canonical("POST", "/v1/x", 1_700_000_000, "AAAAAAAAAAAAAAAAAAAAAA", digest, "dev-1"))
	const bodyHex = "015abd7f5cc57a2dd94b7590f04ad8084273905ee33ec5cebeae62276a97f862"
	if hex.EncodeToString(digest[:]) != bodyHex {
		t.Fatalf("body digest drifted: %s", hex.EncodeToString(digest[:]))
	}
	want := "POST\n/v1/x\n1700000000\nAAAAAAAAAAAAAAAAAAAAAA\n" + bodyHex + "\ndev-1"
	if got != want {
		t.Fatalf("canonical string drifted:\n got %q\nwant %q", got, want)
	}
}

func TestEveryFieldIsBound(t *testing.T) {
	pub, priv := keypair(t)
	base := proof(t, priv)

	mutations := map[string]func(p *Proof){
		"method":    func(p *Proof) { p.Method = "PUT" },
		"path":      func(p *Proof) { p.Path = "/v1/other" },
		"timestamp": func(p *Proof) { p.Timestamp++ },
		"nonce":     func(p *Proof) { p.Nonce = "BBBBBBBBBBBBBBBBBBBBBB" },
		"body":      func(p *Proof) { p.BodyDigest = DigestBody([]byte(`{"value":73}`)) },
		"device id": func(p *Proof) { p.DeviceID = "7c1d2f9e-0a41-4b8c-9d3e-2f6a7b8c9d0e" },
	}
	for name, mutate := range mutations {
		p := base
		mutate(&p)
		if err := Verify(pub, p, fixedNow); !errors.Is(err, ErrSignature) {
			t.Errorf("changing the %s must break the signature; got %v", name, err)
		}
	}
}

func TestForgedDeviceIDFailsUnderTheRealKey(t *testing.T) {
	// Acceptance criterion 2: a forged device id fails signature verification. An attacker
	// with their own key signs a request under a victim's id; the server verifies under
	// the victim's key and refuses.
	victimPub, _ := keypair(t)
	_, attackerPriv := keypair(t)
	p := proof(t, attackerPriv)
	if err := Verify(victimPub, p, fixedNow); !errors.Is(err, ErrSignature) {
		t.Fatalf("a signature by another key must not verify; got %v", err)
	}
}

func TestSkew(t *testing.T) {
	pub, priv := keypair(t)
	p := proof(t, priv)
	if err := Verify(pub, p, fixedNow.Add(MaxSkew-time.Second)); err != nil {
		t.Fatalf("inside the skew: %v", err)
	}
	if err := Verify(pub, p, fixedNow.Add(MaxSkew+time.Second)); !errors.Is(err, ErrSkew) {
		t.Fatalf("a stale request must be refused as skew; got %v", err)
	}
	if err := Verify(pub, p, fixedNow.Add(-MaxSkew-time.Second)); !errors.Is(err, ErrSkew) {
		t.Fatalf("a request from the future must be refused as skew; got %v", err)
	}
}

func TestMalformed(t *testing.T) {
	pub, priv := keypair(t)
	cases := map[string]func(p *Proof){
		"short nonce":     func(p *Proof) { p.Nonce = "abc" },
		"no nonce":        func(p *Proof) { p.Nonce = "" },
		"no device":       func(p *Proof) { p.DeviceID = "" },
		"short signature": func(p *Proof) { p.Signature = p.Signature[:10] },
	}
	for name, mutate := range cases {
		p := proof(t, priv)
		mutate(&p)
		if err := Verify(pub, p, fixedNow); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: want ErrMalformed, got %v", name, err)
		}
	}
	if err := Verify(ed25519.PublicKey("short"), proof(t, priv), fixedNow); !errors.Is(err, ErrMalformed) {
		t.Errorf("bad key: want ErrMalformed, got %v", err)
	}
}

func TestEncodings(t *testing.T) {
	pub, _ := keypair(t)
	back, err := DecodePublicKey(EncodePublicKey(pub))
	if err != nil || !back.Equal(pub) {
		t.Fatal("public key does not round-trip")
	}
	if _, err := DecodePublicKey("bm90IGEga2V5"); err == nil {
		t.Fatal("a 9-byte key must be refused")
	}
	if _, err := ParseTimestamp("0"); err == nil {
		t.Fatal("zero timestamp must be refused")
	}
	if _, err := ParseTimestamp("x"); err == nil {
		t.Fatal("non-numeric timestamp must be refused")
	}
	if _, err := DecodeSignature("!!"); err == nil {
		t.Fatal("bad base64 must be refused")
	}
}
