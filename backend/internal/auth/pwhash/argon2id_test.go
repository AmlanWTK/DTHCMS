package pwhash

import (
	"errors"
	"strings"
	"testing"
)

// cheap is argon2id at a cost that makes the suite fast. The parameters travel with the
// hash, so nothing about the format or the comparison changes with them.
var cheap = Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestHashRoundTrips(t *testing.T) {
	h := New(cheap)

	encoded, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}

	ok, rehash, err := h.Verify("correct horse battery", encoded)
	if err != nil || !ok {
		t.Fatalf("the right password did not verify: ok=%v err=%v", ok, err)
	}
	if rehash {
		t.Error("a hash made with the current parameters asked to be rehashed")
	}

	ok, _, err = h.Verify("correct horse batter", encoded)
	if err != nil || ok {
		t.Fatalf("a near-miss verified: ok=%v err=%v", ok, err)
	}
}

func TestHashIsSaltedAndPHCFormatted(t *testing.T) {
	h := New(cheap)

	a, _ := h.Hash("same password")
	b, _ := h.Hash("same password")
	if a == b {
		t.Fatal("two hashes of one password are identical; the salt is not random")
	}

	for _, encoded := range []string{a, b} {
		if !strings.HasPrefix(encoded, "$argon2id$v=19$m=8192,t=1,p=1$") {
			t.Errorf("not PHC-formatted with the parameters it was made with: %s", encoded)
		}
		if strings.Contains(encoded, "same password") {
			t.Error("the plaintext appears in the hash")
		}
	}
}

func TestVerifyAsksForARehashWhenTheStoredHashIsWeaker(t *testing.T) {
	// The upgrade path: a password hashed under yesterday's parameters still verifies
	// under today's hasher, and says so, so the login can quietly rehash it.
	old := New(cheap)
	encoded, _ := old.Hash("password")

	stronger := New(Params{Memory: 16 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	ok, rehash, err := stronger.Verify("password", encoded)
	if err != nil || !ok {
		t.Fatalf("an older hash did not verify under stronger parameters: ok=%v err=%v", ok, err)
	}
	if !rehash {
		t.Error("a hash weaker than current parameters was not flagged for rehash")
	}

	// And not the other way round: a hash *stronger* than current is left alone.
	strongHash, _ := stronger.Hash("password")
	if _, rehash, _ := old.Verify("password", strongHash); rehash {
		t.Error("a hash stronger than current parameters was flagged for rehash")
	}
}

func TestVerifyDoesNotFlagARehashForAWrongPassword(t *testing.T) {
	// The rehash signal must never fire on a failed verification: a caller acting on it
	// would rehash an attacker's guess over the real password.
	old := New(cheap)
	encoded, _ := old.Hash("password")
	stronger := New(Params{Memory: 16 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})

	ok, rehash, _ := stronger.Verify("wrong", encoded)
	if ok || rehash {
		t.Errorf("wrong password: ok=%v rehash=%v; both must be false", ok, rehash)
	}
}

func TestVerifyRefusesWhatItDidNotWrite(t *testing.T) {
	h := New(cheap)
	for _, bad := range []string{
		"",
		"plaintext",
		"$2b$12$bcrypt.hash.here",
		"$argon2id$unavailable",
		"$argon2id$v=19$m=8192,t=1,p=1$notbase64!$alsonot!",
		"$argon2i$v=19$m=8192,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$AAAA",
	} {
		ok, rehash, err := h.Verify("anything", bad)
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: err = %v, want ErrMalformed", bad, err)
		}
		if ok || rehash {
			t.Errorf("%q: ok=%v rehash=%v on a malformed hash", bad, ok, rehash)
		}
	}
}

func TestDummyVerifiesWithoutError(t *testing.T) {
	// Dummy exists to spend the same time on a login for an account that does not exist.
	// Its only requirement is that Verify runs the full argon2 computation against it —
	// which it cannot do if the string is malformed.
	h := New(cheap)
	ok, _, err := h.Verify("whatever was typed", h.Dummy())
	if err != nil {
		t.Fatalf("Verify against Dummy errored, so the timing path was skipped: %v", err)
	}
	if ok {
		t.Fatal("an arbitrary password verified against the dummy hash")
	}
}

func TestDefaultParamsAreInTheRecommendedBand(t *testing.T) {
	// Guards against a stray edit rather than proving the choice. OWASP's floor for
	// argon2id is 19 MiB / t=2 / p=1; below that the hash is not doing its job.
	p := DefaultParams()
	if p.Memory < 19*1024 {
		t.Errorf("memory %d KiB is below the OWASP floor of 19 MiB", p.Memory)
	}
	if p.Iterations < 2 {
		t.Errorf("iterations %d is below the OWASP floor of 2", p.Iterations)
	}
	if p.SaltLength < 16 || p.KeyLength < 32 {
		t.Errorf("salt %d / key %d bytes are shorter than 16 / 32", p.SaltLength, p.KeyLength)
	}
}
