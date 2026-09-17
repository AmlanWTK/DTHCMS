package signing

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
)

// The signer seam (`docs/signing.md` §2).
//
// # The problem this exists to be honest about
//
// The implementation plan says *"signing via Cloud KMS with a non-exportable key"*. There is no
// cloud: D-01 is open, CP03 was deferred, and every environment is docker-compose on a laptop. A
// non-exportable key requires hardware or a managed service and DTHCMS has neither. Three ways
// to handle that and only one is honest:
//
//   - **Pretend.** Put an Ed25519 private key in a config file, call it non-exportable in the
//     commit message, and meet the criterion on paper. This is the option that gets discovered
//     during the CP94 penetration test, or later.
//   - **Wait for CP03.** Correct, and it stops CP84, CP85 and CP89 — the whole prescription
//     path — behind a decision that is with a lawyer.
//   - **Build the seam and be explicit about which side of it we are on.**
//
// This file is the third. The interface is shaped by **what a KMS can do**, not by what a file
// can do, which is the difference between a seam and a placeholder: [Signer] never returns a
// private key, never accepts one, and signs by being asked rather than by being read. Adopting
// the real thing is a configuration change and this package does not change at all.
//
// # The property that makes it safe is stated, not assumed
//
// The acceptance criterion *"the signing key is non-exportable"* is **not met by [LocalSigner]
// and cannot be.** Its key is a file. Anybody who can read the configuration can copy it, and a
// copied key signs. That is why [NewSigner] refuses it outside development, why every signature
// records [SignerKind], and why `core.signer_kind.non_exportable` is a column that says `false`
// for LOCAL in the database where an auditor will find it.
//
// # Why the refusal is at boot and not a flag
//
// `internal/ai/ai.go`'s tier guard is the precedent and the shape is copied deliberately. A flag
// that turns a safety property off is a flag somebody sets on a bad afternoon; a check the
// service makes about itself at start-up, against the environment it is deployed into, cannot be
// set. `NewSigner` returns an error and the composition root refuses to start — the same failure
// a misconfigured database URL produces, which is the failure a deployment mistake should have.

// Signer turns canonical bytes into a signature.
//
// # Shaped by what a key management service can do
//
// Three methods, and none of them is `PrivateKey()`. A KMS signs on request, tells you which key
// it used, and hands out the public half; it does not hand out the private half, and an interface
// with a method that returned one could not be implemented by the thing this seam exists for.
// That is the test of whether a seam is real: the *managed* implementation is the constraint and
// the local one is what has to fit.
//
// `Sign` takes a context because a managed signer is a network call — one that can be slow, be
// cancelled, or time out while a physician is looking at a spinner.
type Signer interface {
	// Kind says which implementation this is. Recorded on every signature.
	Kind() SignerKind
	// KeyID names the key. Opaque: a KMS resource name, or a local key's label. Recorded so
	// that "which key signed this" is answerable after the key is gone.
	KeyID() string
	// PublicKey is the 32-byte Ed25519 verifying key. Stored beside each signature so that
	// verification does not depend on this interface still being reachable, or on the key
	// register's shape a decade from now.
	PublicKey() ed25519.PublicKey
	// Sign produces a 64-byte signature over exactly the bytes given. It does not hash first,
	// does not canonicalise, and does not know what a prescription is — Ed25519 signs a
	// message, and a signer that also decided what the message was would be two decisions in
	// one place.
	Sign(ctx context.Context, message []byte) ([]byte, error)
}

// ---------------------------------------------------------------------------
// The local signer
// ---------------------------------------------------------------------------

// LocalSigner holds an Ed25519 key in this process's memory, loaded from configuration.
//
// # What it protects against, precisely
//
// It protects against **alteration of a stored prescription by anybody who does not hold the
// key**. That is not nothing: it is the entire threat the tamper-detection criterion names — a
// row edited in `psql`, a projection rebuilt from a doctored ledger, a backup restored with one
// dose changed. None of those produce bytes that verify.
//
// # What it does not protect against
//
// **Anybody who can read the configuration.** The key is a file, or an environment variable, or
// a Kubernetes secret; every one of those is readable by the process, by anything that can read
// the process's memory, by whoever deploys it, and by whoever holds a backup of it. Such a
// person can sign an arbitrary prescription and it will verify perfectly, forever, and nothing
// in this system will say otherwise.
//
// So the guarantee is: *this prescription has not been altered since something holding the
// signing key signed it*. With a managed signer, "something holding the key" is a service that
// logs every use and never released the key. With this one, it is "this deployment, or anybody
// who has ever had a copy of its configuration". The difference is the whole of CP03.
//
// There is no `String` method and no exported field that holds the private key, so no `%v`, no
// `slog` attribute and no error wrapper can print it.
type LocalSigner struct {
	keyID   string
	private ed25519.PrivateKey
}

func (s *LocalSigner) Kind() SignerKind             { return SignerLocal }
func (s *LocalSigner) KeyID() string                { return s.keyID }
func (s *LocalSigner) PublicKey() ed25519.PublicKey { return s.private.Public().(ed25519.PublicKey) }

func (s *LocalSigner) Sign(_ context.Context, message []byte) ([]byte, error) {
	if len(message) == 0 {
		// Signing nothing produces a valid signature over nothing, which would verify. Refused
		// here rather than treated as an edge case, because the only way to reach it is a
		// canonicalisation that returned empty, and that is a bug worth a loud failure.
		return nil, errors.New("signing: refusing to sign an empty message")
	}
	return ed25519.Sign(s.private, message), nil
}

// ---------------------------------------------------------------------------
// The managed signer
// ---------------------------------------------------------------------------

// ManagedSigner is the key management service (CP03, D-01).
//
// **Stubbed, and stubbed loudly.** Every method answers as it will, except that [Sign] refuses
// with [ErrManagedSignerUnavailable] rather than doing something plausible. That is deliberate:
// a stub that returned a fake signature would let a deployment configured for MANAGED sign
// prescriptions with nothing behind them, which is worse than the local signer in every way,
// including that nobody would notice.
//
// What arrives with CP03 is an HTTP client in this struct and a `Sign` that calls it. The
// signature shape, the storage, the verification path and every test in this package stay as
// they are, which is the seam paying for itself.
type ManagedSigner struct {
	keyID string
}

func (s *ManagedSigner) Kind() SignerKind             { return SignerManaged }
func (s *ManagedSigner) KeyID() string                { return s.keyID }
func (s *ManagedSigner) PublicKey() ed25519.PublicKey { return nil }

func (s *ManagedSigner) Sign(context.Context, []byte) ([]byte, error) {
	return nil, fmt.Errorf("%w (key %q)", ErrManagedSignerUnavailable, s.keyID)
}

// ---------------------------------------------------------------------------
// Choosing one, at boot
// ---------------------------------------------------------------------------

// SignerConfig is what the composition root knows about signing.
type SignerConfig struct {
	// Kind is which signer this deployment wants. Empty means LOCAL, which is what a developer
	// on a laptop gets with nothing configured.
	Kind SignerKind
	// KeyID names the key. Empty defaults to a recognisable local label.
	KeyID string
	// Seed is the local signer's 32-byte Ed25519 seed, base64. Empty means a development seed
	// is derived — see [LocalDevelopmentSeed], which is refused outside development by the same
	// guard as everything else here.
	Seed string
}

// LocalKeyID is the label a development key carries when nothing configured one.
//
// Recognisable on purpose. A prescription in a database somewhere whose signature names
// `local-dev-key` is a prescription somebody can identify at a glance as carrying the weaker
// guarantee, without reading a column they did not know to look at.
const LocalKeyID = "local-dev-key"

// LocalDevelopmentSeed is the seed a local stack uses when nothing set one.
//
// Committed, and committed on purpose: this is not a secret and pretending otherwise by hiding
// it would be theatre, in exactly the way `docker-compose.yml`'s password is not hidden. It is
// refused outside local, test and dev, along with every other local key, by [NewSigner].
//
// Thirty-two bytes, base64, and the bytes spell out what it is if anybody decodes them — which
// is deliberate: a key that announces itself in a hex dump is one nobody mistakes for a real one.
//
// It was thirty-one for an hour. Nothing about the constant looked wrong and every local process
// would have refused to start with "the local signing seed is 31 bytes and must be 32", which is
// the same afternoon `config.LocalSecretKey` records losing. What caught it here was
// `TestTheLocalSignerIsRefusedOutsideDevelopment` asserting the *positive* half — that the
// development environments accept it — rather than only the refusal.
const LocalDevelopmentSeed = "RFRIQ01TIGxvY2FsIGRldmVsb3BtZW50IHNlZWQgISE="

// NewSigner builds the signer this deployment is allowed to have, and refuses at boot otherwise.
//
// # The guard, and why it is shaped like the AI tier guard
//
// `internal/ai/ai.go`'s `tierRefusal` refuses a free-tier Gemini credential outside local, test
// and dev — *whatever the request says* — because those are the only environments where the
// deployment as a whole is not allowed to hold real patient data. This is the same argument
// about a different secret: the local signer's key is a file, a prescription signed with a file
// is a prescription anybody with the file can forge, and the only deployments where that is
// acceptable are the ones with no real patient in them.
//
// Two properties this shares with that guard and would lose if it were a flag:
//
//   - It is **about the deployment**, not about the request. There is no call site that can pass
//     something making it allow more.
//   - It fails at **start-up**. A production deployment misconfigured for the local signer does
//     not serve a single request and then get discovered at an audit; it does not start, and the
//     message says why.
//
// The error names the environment and the criterion, so that whoever meets it at 2am knows
// whether they have a configuration problem or a CP03 problem.
func NewSigner(env config.Environment, cfg SignerConfig) (Signer, error) {
	kind := cfg.Kind
	if kind == "" {
		kind = SignerLocal
	}

	switch kind {
	case SignerManaged:
		keyID := strings.TrimSpace(cfg.KeyID)
		if keyID == "" {
			return nil, fmt.Errorf("%w: the managed signer needs a key id", ErrSignerRefused)
		}
		return &ManagedSigner{keyID: keyID}, nil

	case SignerLocal:
		if refusal := localSignerRefusal(env); refusal != "" {
			return nil, fmt.Errorf("%w: %s", ErrSignerRefused, refusal)
		}
		seed := strings.TrimSpace(cfg.Seed)
		if seed == "" {
			seed = LocalDevelopmentSeed
		}
		raw, err := base64.StdEncoding.DecodeString(seed)
		if err != nil {
			// The seed is not echoed, not in this error and not anywhere else. A malformed
			// secret in a log line is still a secret in a log line.
			return nil, fmt.Errorf("%w: the local signing seed is not valid base64", ErrSignerRefused)
		}
		if len(raw) != ed25519.SeedSize {
			return nil, fmt.Errorf("%w: the local signing seed is %d bytes and must be %d",
				ErrSignerRefused, len(raw), ed25519.SeedSize)
		}
		keyID := strings.TrimSpace(cfg.KeyID)
		if keyID == "" {
			keyID = LocalKeyID
		}
		return &LocalSigner{keyID: keyID, private: ed25519.NewKeyFromSeed(raw)}, nil

	default:
		return nil, fmt.Errorf("%w: %q is not a signer this build implements", ErrSignerRefused, kind)
	}
}

// localSignerRefusal returns why the local signer may not be used here, or "".
//
// Split out from [NewSigner] so that the environment rule is one expression somebody can read,
// and so that the test asserting *every* non-development environment is refused can call it
// directly for each value of `config.Environment` rather than trusting a list.
func localSignerRefusal(env config.Environment) string {
	switch env {
	case config.EnvLocal, config.EnvTest, config.EnvDev:
		return ""
	default:
		return fmt.Sprintf(
			"the local signer holds its Ed25519 key in this deployment's configuration, so the "+
				"key is a file and anybody who can read the configuration can forge a "+
				"signature; acceptance criterion 4 (\"the signing key is non-exportable\") is "+
				"not met by it and cannot be, and %s is not an environment that may sign "+
				"prescriptions with it. Configure the managed signer (CP03, D-01) "+
				"(docs/signing.md §2)", env)
	}
}

// VerifyDetached checks a signature against a message and a public key.
//
// A free function rather than a method on [Signer], because **verification must not need the
// signer**. A prescription signed by a managed service, a decade from now, with the service
// retired and the key deleted, still verifies from the 32 bytes stored beside it — and a
// verifier written by somebody auditing this clinic needs nothing from this package but the
// canonical form and this line of Ed25519.
func VerifyDetached(publicKey, message, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), message, signature)
}
