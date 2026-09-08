package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The provider seam (criterion 3: *"provider is swappable by configuration"*).
//
// # What is on which side of it
//
// A provider does exactly one thing: it turns a prompt into text and reports what that cost in
// tokens. It does not minimise, validate, cache, meter, retry, record or decide anything about
// tiers. All of that is the gateway's, and it is the gateway's precisely so that adding an eleventh
// provider cannot accidentally ship a path that skips one of them — which is the failure this
// checkpoint exists a year early to prevent.
//
// Two implementations ship: [Gemini] (real, HTTP, D-07) and [Mock] (in-process, deterministic,
// free). A third exists in the repository already and is not a provider: `tools/mockai` is a
// *server* that speaks the Gemini wire protocol, and the Gemini adapter pointed at it is how the
// local stack and the compose service exercise the real HTTP path with no network and no key. So
// there are two mocks doing different jobs on purpose — one proves the adapter, one makes unit
// tests instant — and neither replaces the other.

// Provider is the single thing the gateway knows how to talk to.
type Provider interface {
	// Name identifies the implementation in a log line and in a metric label. Not the model:
	// "gemini", "mock".
	Name() string
	// Generate sends one prompt. It reports failure as one of the sentinel errors below, wrapped,
	// so the gateway can tell a rate limit from a refusal from a network fault without parsing
	// anybody's message text.
	Generate(ctx context.Context, req ProviderRequest) (ProviderResponse, error)
}

// ProviderRequest is one call to a model.
type ProviderRequest struct {
	// ModelVersion is the pinned version, never an alias (D-13).
	ModelVersion string
	// System and User are the two halves of the prompt. Separate because Gemini and every
	// comparable API treat them differently, and flattening them here would make that difference
	// each adapter's guess.
	System string
	User   string

	Temperature     float64
	MaxOutputTokens int

	// OutputSchema is what the answer must satisfy. The gateway validates against it regardless;
	// it is handed to the provider as well because a provider that can constrain decoding to a
	// schema produces a valid answer far more often, which turns retries from routine into rare.
	OutputSchema *Schema
}

// ProviderResponse is what came back.
type ProviderResponse struct {
	Text string
	// ModelVersion is what actually answered, which need not be what was asked for: the mock
	// answers as itself, and a provider that silently served a different version is a thing the
	// record must be able to show.
	ModelVersion string

	InputTokens  int
	OutputTokens int

	// FinishReason is the provider's own word: STOP, MAX_TOKENS, SAFETY. Recorded rather than
	// interpreted, because the vocabulary is the provider's and will change.
	FinishReason string
}

// Errors a provider reports. Wrapped, never returned bare, so the message says which model and
// what happened while errors.Is still works.
var (
	// ErrProviderUnavailable is a fault worth retrying: a connection reset, a 5xx, a timeout
	// inside the provider.
	ErrProviderUnavailable = errors.New("ai: the provider is unavailable")
	// ErrProviderRateLimited is a 429. Retryable, and the only error carrying the provider's own
	// opinion about when — see [RetryAfter].
	ErrProviderRateLimited = errors.New("ai: the provider is rate limiting")
	// ErrProviderRefused is the model declining to answer on safety grounds. **Not retryable**:
	// asking the same question again is the one thing guaranteed not to help, and a retry loop
	// against a refusal is three times the cost for the same answer.
	ErrProviderRefused = errors.New("ai: the model declined to answer")
	// ErrProviderRejected is the provider saying the request itself is wrong — a bad model
	// version, a malformed body, a rejected key. Not retryable: it is a deployment fault, and
	// retrying hides it behind a slow failure instead of a fast one.
	ErrProviderRejected = errors.New("ai: the provider rejected the request")
)

// RetryAfter reports how long a provider asked us to wait, if it did.
type retryAfter struct {
	error
	after time.Duration
}

func (r retryAfter) Unwrap() error { return r.error }

// RetryAfter extracts a provider's own backoff advice from an error chain. Honouring it beats
// guessing: a 429 answered with our exponential backoff instead of the provider's Retry-After is a
// second 429, and then a third.
func RetryAfter(err error) (time.Duration, bool) {
	var advice retryAfter
	if errors.As(err, &advice) && advice.after > 0 {
		return advice.after, true
	}
	return 0, false
}

// Retryable reports whether trying the same call again could plausibly work.
func Retryable(err error) bool {
	return errors.Is(err, ErrProviderUnavailable) || errors.Is(err, ErrProviderRateLimited)
}

// --- the mock ---

// Mock is the in-process provider: it contacts nothing, costs nothing, and answers the same way
// every time.
//
// Determinism is not a nicety. The response cache is keyed on the hash of the input, and a test
// that asserts a second identical call was served from the cache is asserting that the first answer
// and the second are the same object — which is only meaningful if a provider that *was* called
// twice would have said the same thing. A mock with any randomness in it would make that test pass
// for the wrong reason.
//
// The answer is built from the request's own output schema, so an agent that changes its output
// shape gets a mock that follows. The alternative — a fixture per agent — is a file somebody
// forgets, and the symptom of forgetting is a green suite exercising the retry path while claiming
// to exercise the happy one.
type Mock struct {
	// Respond overrides the default answer. Tests use it to produce the malformed, the refused and
	// the slow; nothing in production sets it.
	Respond func(ProviderRequest) (ProviderResponse, error)
	// ModelVersion is what the mock reports as having answered. `mock-000` is registered in
	// core.ai_model with a price of zero, so a mock interaction costs nothing by arithmetic rather
	// than by a special case in the metering.
	ModelVersion string
}

// NewMock builds one.
func NewMock() *Mock { return &Mock{ModelVersion: MockModelVersion} }

// MockModelVersion is the pinned "version" of the mock. It is a version so that the no-aliases rule
// (D-13) has nothing to make an exception for.
const MockModelVersion = "mock-000"

// Name identifies the provider.
func (m *Mock) Name() string { return "mock" }

// Generate answers.
func (m *Mock) Generate(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	if m.Respond != nil {
		return m.Respond(req)
	}
	// The context is checked even though nothing here blocks, because a gateway timeout test that
	// passed only against a real provider would be testing the network rather than the timeout.
	if err := ctx.Err(); err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}

	var text string
	if req.OutputSchema != nil {
		encoded, err := json.Marshal(req.OutputSchema.Example())
		if err != nil {
			return ProviderResponse{}, fmt.Errorf("%w: building a mock answer: %v", ErrProviderRejected, err)
		}
		text = string(encoded)
	} else {
		text = "MOCK RESPONSE — no model was contacted. Prompt fingerprint " + fingerprint(req.User)
	}

	version := m.ModelVersion
	if version == "" {
		version = MockModelVersion
	}
	return ProviderResponse{
		Text:         text,
		ModelVersion: version,
		// Four characters to a token is the usual rough heuristic. It only has to be stable and
		// plausible: what is being exercised is that the metering adds up, not what Gemini's
		// tokeniser does.
		InputTokens:  estimateTokens(req.System) + estimateTokens(req.User),
		OutputTokens: estimateTokens(text),
		FinishReason: "STOP",
	}, nil
}

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
