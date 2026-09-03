package secretbox

import (
	"bytes"
	"errors"
	"testing"
)

func key(id string, fill byte) Key {
	return Key{ID: id, Material: bytes.Repeat([]byte{fill}, KeySize)}
}

func TestSealAndOpenRoundTrip(t *testing.T) {
	ring, err := NewRing(key("k1", 1))
	if err != nil {
		t.Fatal(err)
	}
	sealed, id, err := ring.Seal([]byte("JBSWY3DPEHPK3PXP"), []byte("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if id != "k1" {
		t.Errorf("sealed under %q, want k1", id)
	}
	if bytes.Contains(sealed, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("the plaintext is visible in the ciphertext")
	}
	opened, err := ring.Open(sealed, id, []byte("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != "JBSWY3DPEHPK3PXP" {
		t.Errorf("opened %q", opened)
	}
}

func TestEveryNonceIsFresh(t *testing.T) {
	ring, _ := NewRing(key("k1", 1))
	a, _, _ := ring.Seal([]byte("same"), nil)
	b, _, _ := ring.Seal([]byte("same"), nil)
	if bytes.Equal(a, b) {
		t.Fatal("two seals of one plaintext are identical; the nonce is not random")
	}
}

func TestTheAssociatedDataBindsTheCiphertextToItsOwner(t *testing.T) {
	// A TOTP row copied from one user onto another must not decrypt there.
	ring, _ := NewRing(key("k1", 1))
	sealed, id, _ := ring.Seal([]byte("secret"), []byte("user-1"))
	if _, err := ring.Open(sealed, id, []byte("user-2")); !errors.Is(err, ErrTampered) {
		t.Errorf("opened under another user's aad: err=%v", err)
	}
}

func TestTamperingIsDetected(t *testing.T) {
	ring, _ := NewRing(key("k1", 1))
	sealed, id, _ := ring.Seal([]byte("secret"), nil)
	sealed[len(sealed)-1] ^= 0x01
	if _, err := ring.Open(sealed, id, nil); !errors.Is(err, ErrTampered) {
		t.Errorf("a flipped bit opened: err=%v", err)
	}
	if _, err := ring.Open([]byte("short"), id, nil); !errors.Is(err, ErrTampered) {
		t.Errorf("a truncated ciphertext opened: err=%v", err)
	}
}

func TestRotation(t *testing.T) {
	// Sealed under k1 yesterday; today k2 is current and k1 is still in the ring.
	old, _ := NewRing(key("k1", 1))
	sealed, id, _ := old.Seal([]byte("secret"), nil)

	rotated, _ := NewRing(key("k2", 2), key("k1", 1))
	if rotated.CurrentKeyID() != "k2" {
		t.Fatal("the first key is not the current one")
	}
	opened, err := rotated.Open(sealed, id, nil)
	if err != nil || string(opened) != "secret" {
		t.Fatalf("the rotated ring cannot open the old key's ciphertext: %v", err)
	}
	if !rotated.NeedsResealing(id) {
		t.Error("an old-key ciphertext is not flagged for resealing")
	}
	resealed, newID, _ := rotated.Seal(opened, nil)
	if newID != "k2" || rotated.NeedsResealing(newID) {
		t.Errorf("resealed under %q", newID)
	}
	if _, err := rotated.Open(resealed, newID, nil); err != nil {
		t.Error(err)
	}

	// And a ring that has dropped k1 says so, distinctly from tampering.
	dropped, _ := NewRing(key("k2", 2))
	if _, err := dropped.Open(sealed, id, nil); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("err=%v, want ErrUnknownKey", err)
	}
}

func TestRingRefusesBadKeys(t *testing.T) {
	for name, keys := range map[string][]Key{
		"none":      {},
		"no id":     {{ID: "", Material: bytes.Repeat([]byte{1}, KeySize)}},
		"short":     {{ID: "k", Material: []byte("short")}},
		"duplicate": {key("k", 1), key("k", 2)},
	} {
		if _, err := NewRing(keys...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseKey(t *testing.T) {
	k, err := ParseKey("k1", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil || len(k.Material) != KeySize {
		t.Fatalf("err=%v len=%d", err, len(k.Material))
	}
	if _, err := ParseKey("k1", "not base64!"); err == nil {
		t.Error("accepted non-base64")
	}
}
