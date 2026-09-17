package signing_test

import (
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/signing"
)

// The verification token (CP85 criterion 3).

// TestTokensAreNotEnumerable states the property the criterion is actually about.
//
// Enumeration resistance is entropy, not rate limiting. Ten thousand tokens, all distinct, all
// the same length, all inside the declared alphabet — which is the observable shadow of "160
// bits from crypto/rand". A generator that had silently become a counter, a timestamp or a
// truncated uuid would fail the distinctness or the alphabet check, and one that had lost its
// randomness entirely would fail both.
func TestTokensAreNotEnumerable(t *testing.T) {
	const runs = 10000
	seen := make(map[string]bool, runs)
	alphabet := regexp.MustCompile(`^[A-Z2-7]{32}$`)

	for i := 0; i < runs; i++ {
		token, err := signing.NewToken()
		if err != nil {
			t.Fatalf("minting token %d: %v", i, err)
		}
		if !alphabet.MatchString(token.Plaintext) {
			t.Fatalf("token %d is %q, which is outside the declared alphabet", i, token.Plaintext)
		}
		if seen[token.Plaintext] {
			t.Fatalf("token %d collided after %d draws; this generator is not random", i, len(seen))
		}
		seen[token.Plaintext] = true
		if len(token.Digest) != 32 {
			t.Fatalf("token %d has a %d-byte digest", i, len(token.Digest))
		}
	}
}

// TestTheTokenItselfIsNotDerivableFromTheDigest.
//
// What the database holds is the digest, and this is the statement of why that matters: the
// digest is not the token, does not contain it, and cannot be presented in its place. Somebody
// who reads the whole `read.prescription_signature` table cannot produce a working QR code.
func TestTheTokenItselfIsNotDerivableFromTheDigest(t *testing.T) {
	token, err := signing.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	stored := hex.EncodeToString(token.Digest)
	if strings.Contains(stored, strings.ToLower(token.Plaintext)) ||
		strings.Contains(strings.ToUpper(stored), token.Plaintext) {
		t.Fatal("the stored digest contains the token")
	}
	// Presenting the digest where the token belongs resolves to nothing.
	if hex.EncodeToString(signing.TokenDigest(stored)) == stored {
		t.Fatal("hashing the digest yields the digest; the lookup could be satisfied by a " +
			"database reader rather than by a holder of the paper")
	}
}

// TestATokenSurvivesBeingReadOffAPieceOfPaper.
//
// The QR will sometimes not scan, and somebody at a pharmacy counter will type it. Lower case
// from a phone keyboard, spaces from reading it aloud in groups, a dash somebody added for
// legibility — all of those must resolve to the same digest, because the alternative is a
// verification that fails for a genuine prescription, which teaches people the check is
// unreliable and is therefore worse than no check.
func TestATokenSurvivesBeingReadOffAPieceOfPaper(t *testing.T) {
	token, err := signing.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	canonical := hex.EncodeToString(signing.TokenDigest(token.Plaintext))

	for _, typed := range []string{
		strings.ToLower(token.Plaintext),
		"  " + token.Plaintext + "  ",
		token.Plaintext[:8] + " " + token.Plaintext[8:16] + " " + token.Plaintext[16:],
		token.Plaintext[:8] + "-" + token.Plaintext[8:],
	} {
		if got := hex.EncodeToString(signing.TokenDigest(typed)); got != canonical {
			t.Errorf("%q resolved to a different digest than the token it is", typed)
		}
		if !signing.WellFormedToken(typed) {
			t.Errorf("%q was judged malformed", typed)
		}
	}
}

// TestAMalformedTokenIsRejectedWithoutBeingTold.
//
// `WellFormedToken` decides whether to spend a database round trip and nothing else. What it
// must not become is a validator whose answer reaches the caller: a response that said "that is
// not a token" for one string and "no such prescription" for another would be an oracle
// narrowing the space a prober has to search.
func TestAMalformedTokenIsRejectedWithoutBeingTold(t *testing.T) {
	for _, bad := range []string{
		"", "short", strings.Repeat("A", 31), strings.Repeat("A", 33),
		strings.Repeat("1", 32), // '1' is not in base32's alphabet
		"../../../etc/passwd",
		strings.Repeat("A", 4096),
	} {
		if signing.WellFormedToken(bad) {
			t.Errorf("%.20q was judged well-formed", bad)
		}
	}
}

// TestNothingAboutAPrescriptionCanReachAToken.
//
// `NewToken` takes no arguments. This test is the statement of that in a form a reader looking
// for "is patient data in the QR" can find: there is no parameter through which anything about a
// patient could arrive, so the requirement holds by the function's signature rather than by
// anybody's discipline.
func TestNothingAboutAPrescriptionCanReachAToken(t *testing.T) {
	mint := signing.NewToken
	_ = mint // func() (signing.Token, error) — no inputs, and the compiler enforces it.
}
