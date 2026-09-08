package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The Gemini adapter (D-07, ADR-0007).
//
// # What it talks to
//
// `generateContent` on the Gemini REST API, or anything that speaks it. That "anything" is not
// hypothetical: `tools/mockai` is a server in this repository speaking exactly this protocol, and
// `DTHCMS_AI_BASE_URL` pointing at it is how the local stack and CI exercise this adapter — the
// real request assembly, the real response parsing, the real error classification — with no network
// and no key. Vertex AI is the same protocol behind a different base URL and a different credential,
// which is what makes D-07's option B a configuration change rather than a second adapter.
//
// # What it deliberately does not do
//
// No retry, no backoff, no circuit breaker, no caching, no metering, no PHI check. Every one of
// those is the gateway's, and an adapter that did any of them would be a second place where a rule
// could be subtly different — which is the thing this whole checkpoint exists to prevent before
// there are ten agents relying on it.
//
// The one thing it does that looks like policy is **classifying failures**, and that is not policy:
// only the adapter knows that this provider says 429 with a Retry-After header and puts a refusal
// in `finishReason` rather than in a status code. Turning that into the sentinel errors in
// provider.go is translation, and it is the whole reason the gateway can have one retry policy
// rather than one per provider.

// Gemini is the real provider.
type Gemini struct {
	// BaseURL is where generateContent lives: the Google endpoint, a Vertex endpoint, or the local
	// mockai service.
	BaseURL string
	// APIKey goes in the `x-goog-api-key` header rather than the query string. A key in a URL is a
	// key in every proxy log and every trace between here and Google.
	APIKey string
	// HTTP is the client. Its timeout is a backstop; the real deadline is the context the gateway
	// passes, which comes from the agent's own budget.
	HTTP *http.Client
}

// NewGemini builds the adapter.
func NewGemini(baseURL, apiKey string, timeout time.Duration) *Gemini {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Gemini{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// Name identifies the provider.
func (g *Gemini) Name() string { return "gemini" }

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  struct {
		Temperature      *float64 `json:"temperature,omitempty"`
		MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
		ResponseMimeType string   `json:"responseMimeType,omitempty"`
	} `json:"generationConfig"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
	Error        *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// Generate sends one prompt.
func (g *Gemini) Generate(ctx context.Context, req ProviderRequest) (ProviderResponse, error) {
	body := geminiRequest{
		Contents: []geminiContent{{Role: "user", Parts: []geminiPart{{Text: req.User}}}},
	}
	if strings.TrimSpace(req.System) != "" {
		body.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: req.System}}}
	}
	temperature := req.Temperature
	body.GenerationConfig.Temperature = &temperature
	body.GenerationConfig.MaxOutputTokens = req.MaxOutputTokens
	if req.OutputSchema != nil {
		// Asking the provider to emit JSON does not remove the need to validate it — the gateway
		// validates regardless, and criterion 4 is about what happens when the answer is wrong.
		// What it does is make "wrong" rare, which is the difference between a retry budget that
		// is a safety net and one that is the normal path.
		body.GenerationConfig.ResponseMimeType = "application/json"
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: encoding the request: %v", ErrProviderRejected, err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", g.BaseURL, req.ModelVersion)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: building the request: %v", ErrProviderRejected, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if g.APIKey != "" {
		httpReq.Header.Set("x-goog-api-key", g.APIKey)
	}

	resp, err := g.HTTP.Do(httpReq)
	if err != nil {
		// Everything the transport reports is retryable: a reset, a refused connection, a deadline.
		// The gateway decides whether there is budget left to retry with; the adapter only says
		// what kind of failure this was.
		return ProviderResponse{}, fmt.Errorf("%w: %v", ErrProviderUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded. A provider that streamed indefinitely would otherwise fill this process's memory,
	// and eight megabytes is far more than any agent's output budget allows.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return ProviderResponse{}, fmt.Errorf("%w: reading the answer: %v", ErrProviderUnavailable, err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		wait := parseRetryAfter(resp.Header.Get("Retry-After"))
		return ProviderResponse{}, retryAfter{
			error: fmt.Errorf("%w: %s said %s", ErrProviderRateLimited, req.ModelVersion, resp.Status),
			after: wait,
		}
	}
	if resp.StatusCode >= 500 {
		return ProviderResponse{}, fmt.Errorf("%w: %s said %s", ErrProviderUnavailable, req.ModelVersion, resp.Status)
	}
	if resp.StatusCode >= 400 {
		// A 4xx is our fault: a retired model version, a rejected key, a malformed body. Retrying
		// would turn a fast, obvious deployment error into a slow one, three times over.
		return ProviderResponse{}, fmt.Errorf("%w: %s said %s: %s",
			ErrProviderRejected, req.ModelVersion, resp.Status, summarise(raw))
	}

	var parsed geminiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// A 200 whose body is not the shape we know. Retryable, because the commonest cause is a
		// truncated response rather than a changed contract — and if the contract really has
		// changed, the retries fail identically and the recorded error says so.
		return ProviderResponse{}, fmt.Errorf("%w: the answer is not a generateContent response: %v",
			ErrProviderUnavailable, err)
	}
	if parsed.Error != nil {
		return ProviderResponse{}, fmt.Errorf("%w: %s", ErrProviderRejected, parsed.Error.Message)
	}
	if len(parsed.Candidates) == 0 {
		return ProviderResponse{}, fmt.Errorf("%w: the answer carries no candidate", ErrProviderRefused)
	}

	candidate := parsed.Candidates[0]
	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		text.WriteString(part.Text)
	}

	// A refusal is a 200 with a finish reason and no text. It is *not* retryable: asking the same
	// question again is the one thing guaranteed not to change the answer, and three attempts at a
	// safety refusal is three times the cost for the same silence. D-15 then applies — the screen
	// says the AI is unavailable rather than inventing something.
	if text.Len() == 0 {
		return ProviderResponse{}, fmt.Errorf("%w: finish reason %s",
			ErrProviderRefused, candidate.FinishReason)
	}

	version := parsed.ModelVersion
	if version == "" {
		version = req.ModelVersion
	}
	return ProviderResponse{
		Text:         text.String(),
		ModelVersion: version,
		InputTokens:  parsed.UsageMetadata.PromptTokenCount,
		OutputTokens: parsed.UsageMetadata.CandidatesTokenCount,
		FinishReason: candidate.FinishReason,
	}, nil
}

// parseRetryAfter reads the header in the only form Google sends it: whole seconds.
func parseRetryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return 0
	}
	// Capped. A provider asking us to wait an hour is a provider we would rather fail against and
	// let D-15's degraded mode take over, than one we hold a clinical job open for.
	if seconds > 120 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

// summarise trims a provider's error body for a log line.
//
// Truncated rather than passed through whole because a provider that echoes the request in its
// error message would otherwise write the prompt into our logs — and the prompt, even minimised,
// is clinical content that `logging.PHIKeys` exists to keep out of them.
func summarise(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if len(text) > 200 {
		return text[:200] + "…"
	}
	return text
}
