package totp

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B: the reference vectors for HMAC-SHA1, six digits, thirty seconds,
// with the ASCII secret "12345678901234567890". Any implementation that disagrees with
// these does not interoperate with authenticator apps.
var rfc6238Secret = base32.StdEncoding.WithPadding(base32.NoPadding).
	EncodeToString([]byte("12345678901234567890"))

func TestRFC6238ReferenceVectors(t *testing.T) {
	// The RFC lists eight-digit codes; the last six digits are the six-digit codes.
	cases := []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, err := Code(rfc6238Secret, Step(time.Unix(c.unix, 0)))
		if err != nil {
			t.Fatal(err)
		}
		if got != c.code {
			t.Errorf("t=%d: got %s, want %s", c.unix, got, c.code)
		}
	}
}

func TestRFC4226HOTPVectors(t *testing.T) {
	// RFC 4226 Appendix D, counters 0–9, same secret. Code() is HOTP with the step as the
	// counter, so these pin the truncation independently of any clock arithmetic.
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for counter, code := range want {
		got, err := Code(rfc6238Secret, int64(counter))
		if err != nil {
			t.Fatal(err)
		}
		if got != code {
			t.Errorf("counter %d: got %s, want %s", counter, got, code)
		}
	}
}

func TestVerifyAcceptsTheCurrentStepAndTheDriftWindow(t *testing.T) {
	now := time.Unix(1111111111, 0)
	current := Step(now)

	for _, delta := range []int64{0, -1, 1} {
		code, _ := Code(rfc6238Secret, current+delta)
		matched, ok := Verify(rfc6238Secret, code, now, 1)
		if !ok {
			t.Errorf("code for step %+d refused with window 1", delta)
		}
		if matched != current+delta {
			t.Errorf("matched step %d, want %d", matched, current+delta)
		}
	}

	// Two steps out is beyond a window of one.
	code, _ := Code(rfc6238Secret, current+2)
	if _, ok := Verify(rfc6238Secret, code, now, 1); ok {
		t.Error("a code two steps ahead was accepted with window 1")
	}
	// And a window of zero is exactly the current step.
	code, _ = Code(rfc6238Secret, current-1)
	if _, ok := Verify(rfc6238Secret, code, now, 0); ok {
		t.Error("a code one step behind was accepted with window 0")
	}
}

func TestVerifyRefusesTheObviousWrongThings(t *testing.T) {
	now := time.Unix(1111111111, 0)
	for _, code := range []string{"", "12345", "1234567", "abcdef", "000000 ", "050470"} {
		if _, ok := Verify(rfc6238Secret, code, now, 1); ok {
			t.Errorf("%q was accepted", code)
		}
	}
	// Whitespace around a right code is forgiven — people paste.
	if _, ok := Verify(rfc6238Secret, " 050471 ", now, 1); !ok {
		t.Error("a padded correct code was refused")
	}
}

func TestVerifyMatchesTheNearestStepFirst(t *testing.T) {
	// If the same six digits happened to be valid at two steps in the window, the nearer
	// one is what gets reported — the replay guard keys on it.
	now := time.Unix(1111111111, 0)
	code, _ := Code(rfc6238Secret, Step(now))
	matched, _ := Verify(rfc6238Secret, code, now, 5)
	if matched != Step(now) {
		t.Errorf("matched %d, want the current step %d", matched, Step(now))
	}
}

func TestNewSecretIsBase32AndLongEnough(t *testing.T) {
	a, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewSecret()
	if a == b {
		t.Fatal("two secrets are identical")
	}
	raw, err := Encoding.DecodeString(a)
	if err != nil {
		t.Fatalf("not base32: %v", err)
	}
	if len(raw) != SecretBytes {
		t.Errorf("%d bytes, want %d", len(raw), SecretBytes)
	}
	if strings.ContainsAny(a, "=") {
		t.Error("padded; authenticator apps expect unpadded base32")
	}
}

func TestProvisioningURIIsWhatAuthenticatorAppsRead(t *testing.T) {
	uri := ProvisioningURI("DTHCMS", "E001", "JBSWY3DPEHPK3PXP")
	for _, want := range []string{
		"otpauth://totp/DTHCMS:E001?",
		"secret=JBSWY3DPEHPK3PXP",
		"issuer=DTHCMS",
		"algorithm=SHA1",
		"digits=6",
		"period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("%s\n  missing %q", uri, want)
		}
	}
	// A space in the label must be escaped, not left to the app to guess at.
	if u := ProvisioningURI("DTHC Faridpur", "E001", "X"); strings.Contains(u, " ") {
		t.Errorf("unescaped space in %s", u)
	}
}

func TestCodeRefusesANonBase32Secret(t *testing.T) {
	if _, err := Code("not base32!", 0); err == nil {
		t.Error("a malformed secret produced a code")
	}
}
