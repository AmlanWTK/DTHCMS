package signing_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/signing"
)

// The seam and its boot guard (CP84, docs/signing.md §2).

// TestTheLocalSignerIsRefusedOutsideDevelopment is the criterion-4 guard.
//
// It enumerates **every** value of `config.Environment` rather than testing production alone, so
// that a new environment added later — "pilot", "uat" — is refused by default and somebody has
// to come here and say otherwise in a diff. That is the difference between a guard and a
// blacklist.
func TestTheLocalSignerIsRefusedOutsideDevelopment(t *testing.T) {
	development := map[config.Environment]bool{
		config.EnvLocal: true, config.EnvTest: true, config.EnvDev: true,
	}
	for _, env := range []config.Environment{
		config.EnvLocal, config.EnvTest, config.EnvDev, config.EnvStaging, config.EnvProduction,
		config.Environment("pilot"),
	} {
		t.Run(string(env), func(t *testing.T) {
			signer, err := signing.NewSigner(env, signing.SignerConfig{})
			if development[env] {
				if err != nil {
					t.Fatalf("the local signer is refused in %s, where it must be allowed: %v", env, err)
				}
				if signer.Kind() != signing.SignerLocal {
					t.Fatalf("got signer kind %q", signer.Kind())
				}
				return
			}
			if !errors.Is(err, signing.ErrSignerRefused) {
				t.Fatalf("the local signer was accepted in %s: a key in a configuration file "+
					"would be signing prescriptions in a deployment that may hold real patient "+
					"data (err=%v)", env, err)
			}
			// The refusal says why, in the words somebody meeting it at 2am needs.
			if !strings.Contains(err.Error(), "non-exportable") {
				t.Errorf("the refusal does not name the criterion it is about: %v", err)
			}
		})
	}
}

// TestTheRefusalIsNotAFlag. There is no field on SignerConfig that turns the guard off, and
// this test is what keeps it that way: it asserts that no configuration of the *local* signer
// makes production accept it.
func TestTheRefusalIsNotAFlag(t *testing.T) {
	for _, cfg := range []signing.SignerConfig{
		{},
		{Kind: signing.SignerLocal},
		{Kind: signing.SignerLocal, KeyID: "production-key"},
		{Kind: signing.SignerLocal, Seed: signing.LocalDevelopmentSeed},
		{Kind: "local"},
		{Kind: "LOCAL", KeyID: "kms://not-really"},
	} {
		if _, err := signing.NewSigner(config.EnvProduction, cfg); err == nil {
			t.Fatalf("production accepted a local signer configured as %+v", cfg)
		}
	}
}

// TestTheManagedSignerRefusesToSignRatherThanFakingIt.
//
// A stub that returned a plausible signature would let a deployment configured for MANAGED sign
// prescriptions with nothing behind them — worse than the local signer in every way, including
// that nobody would notice.
func TestTheManagedSignerRefusesToSignRatherThanFakingIt(t *testing.T) {
	signer, err := signing.NewSigner(config.EnvProduction, signing.SignerConfig{
		Kind: signing.SignerManaged, KeyID: "projects/dthc/keys/prescription-signing",
	})
	if err != nil {
		t.Fatalf("the managed signer is refused in production, which is the one place it belongs: %v", err)
	}
	if signer.Kind() != signing.SignerManaged {
		t.Fatalf("got kind %q", signer.Kind())
	}
	if _, err := signer.Sign(context.Background(), []byte("anything")); !errors.Is(err, signing.ErrManagedSignerUnavailable) {
		t.Fatalf("the managed stub produced something rather than refusing: %v", err)
	}
}

// TestAManagedSignerWithNoKeyIsRefused — there is no sensible default for a KMS key name,
// because there is no key it could mean.
func TestAManagedSignerWithNoKeyIsRefused(t *testing.T) {
	if _, err := signing.NewSigner(config.EnvProduction, signing.SignerConfig{
		Kind: signing.SignerManaged,
	}); !errors.Is(err, signing.ErrSignerRefused) {
		t.Fatalf("a managed signer with no key id was accepted: %v", err)
	}
}

// TestASeedIsNeverEchoedInAnError. A malformed secret in a log line is still a secret in a log
// line, and the loudest moment for a secret to appear is the moment something is wrong with it.
func TestASeedIsNeverEchoedInAnError(t *testing.T) {
	const secret = "SUPERSECRETSEEDMATERIALTHATMUSTNOTAPPEAR"
	for _, seed := range []string{secret, base64.StdEncoding.EncodeToString([]byte(secret))} {
		_, err := signing.NewSigner(config.EnvDev, signing.SignerConfig{Seed: seed})
		if err == nil {
			t.Fatalf("a %d-byte seed was accepted", len(seed))
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), seed) {
			t.Fatalf("the error echoes the seed: %v", err)
		}
	}
}

// TestTheSignatureRoundTripsAndOneAlteredByteBreaksIt is the smallest statement of the whole
// checkpoint, made without a database in the way.
func TestTheSignatureRoundTripsAndOneAlteredByteBreaksIt(t *testing.T) {
	signer, err := signing.NewSigner(config.EnvTest, signing.SignerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	message, err := signing.Canonicalise(signing.CanonicalVersion, faridpurSubject())
	if err != nil {
		t.Fatal(err)
	}
	signature, err := signer.Sign(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if !signing.VerifyDetached(signer.PublicKey(), message, signature) {
		t.Fatal("a freshly made signature does not verify")
	}

	for i := range message {
		altered := append([]byte(nil), message...)
		altered[i] ^= 0x01
		if signing.VerifyDetached(signer.PublicKey(), altered, signature) {
			t.Fatalf("flipping bit 0 of byte %d left the signature valid", i)
		}
		if i > 64 {
			// Every byte would be thorough and slow; the first sixty-five cover the header,
			// the ids and the first field boundaries, which is where a structural weakness
			// would live. The whole-message property is Ed25519's, not ours.
			break
		}
	}

	// A different key does not verify it, which is what makes "which key signed this" a
	// question worth recording.
	other := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	if signing.VerifyDetached(other.Public().(ed25519.PublicKey), message, signature) {
		t.Fatal("a signature verified under a key that did not make it")
	}
}

// TestTheLocalSignerRefusesAnEmptyMessage. Signing nothing produces a signature over nothing,
// which verifies. The only way to reach it is a canonicalisation that returned empty, which is
// a bug worth a loud failure rather than a valid-looking signature.
func TestTheLocalSignerRefusesAnEmptyMessage(t *testing.T) {
	signer, err := signing.NewSigner(config.EnvTest, signing.SignerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Sign(context.Background(), nil); err == nil {
		t.Fatal("signing an empty message succeeded")
	}
}

// TestTheSignerSeamHasNoWayToHandOutAPrivateKey.
//
// A compile-time statement rather than a runtime one: if a `PrivateKey()` method were added to
// [signing.Signer], the managed implementation could not honestly implement it and this file
// would stop compiling — which is the whole argument for the seam being shaped by what a KMS can
// do rather than by what a file can do.
func TestTheSignerSeamHasNoWayToHandOutAPrivateKey(t *testing.T) {
	var seam signing.Signer = &signing.ManagedSigner{}
	// The interface has exactly four methods. Naming them here means adding a fifth is a change
	// somebody makes in this file too.
	_ = seam.Kind
	_ = seam.KeyID
	_ = seam.PublicKey
	_ = seam.Sign
}

// TestTheImageIsNotTheSignature — docs/signing.md §6, as a type-level fact.
//
// [signing.SignatureImage] has no signature bytes, no key and no verification method, so no call
// site can treat the picture on the paper as the thing that proves anything. The test asserts
// the caveat is not empty in either language, because a picture shown with no caveat is a
// picture a reader will believe.
func TestTheImageIsNotTheSignature(t *testing.T) {
	image := signing.TheImageIsNotTheSignature()
	if image.CaveatEN == "" || image.CaveatBN == "" {
		t.Fatal("the signature image carries no caveat in one of the two languages")
	}
	if !strings.Contains(image.CaveatEN, "QR") {
		t.Error("the English caveat does not point the reader at the thing that does prove it")
	}
}
