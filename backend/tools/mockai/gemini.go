package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// generateRequest is the subset of the Gemini generateContent request we care about.
type generateRequest struct {
	Contents []struct {
		Role  string `json:"role"`
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
	GenerationConfig struct {
		ResponseMimeType string   `json:"responseMimeType"`
		Temperature      *float64 `json:"temperature"`
	} `json:"generationConfig"`
}

func handleGenerate(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scenario := scenarioOf(r)
		if applyScenario(w, scenario) {
			return
		}

		model := strings.TrimSuffix(r.PathValue("model"), ":generateContent")

		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mockai: unreadable body"})
			return
		}

		var req generateRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"code":    400,
					"message": "mockai: request is not valid generateContent JSON",
					"status":  "INVALID_ARGUMENT",
				},
			})
			return
		}

		prompt := promptText(req)

		if scenario == ScenarioRefusal {
			writeJSON(w, http.StatusOK, geminiResponse(model, "", "SAFETY", prompt))
			return
		}

		writeJSON(w, http.StatusOK, geminiResponse(model, mockText(prompt, req), "STOP", prompt))
	}
}

func promptText(req generateRequest) string {
	var b strings.Builder
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			b.WriteString(p.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// mockText returns a deterministic, obviously-fake response.
//
// Determinism matters: a test that asserts on model output must not flake, and a cached
// response keyed on input hash must be reproducible. The same prompt always yields the
// same text here.
func mockText(prompt string, req generateRequest) string {
	if strings.Contains(strings.ToLower(req.GenerationConfig.ResponseMimeType), "json") {
		// Schema-constrained call: return valid JSON so the caller's validator has
		// something real to check, with values marked as mock.
		return `{"summary":"MOCK RESPONSE — no model was contacted.","confidence":0.5,"items":[],"generated_by":"mockai"}`
	}

	return "MOCK RESPONSE — no model was contacted.\n\n" +
		"This text comes from the local mock AI service (backend/tools/mockai). It is " +
		"deterministic: the same prompt always produces the same output.\n\n" +
		"Prompt fingerprint: " + fingerprint(prompt) + "\n" +
		"Prompt length: " + itoa(len(prompt)) + " characters"
}

// geminiResponse mirrors the shape of a real generateContent response closely enough
// that client code, token accounting and cost metering can all be exercised.
func geminiResponse(model, text, finishReason, prompt string) map[string]any {
	promptTokens := estimateTokens(prompt)
	outputTokens := estimateTokens(text)

	candidate := map[string]any{
		"finishReason": finishReason,
		"index":        0,
	}
	if text != "" {
		candidate["content"] = map[string]any{
			"role":  "model",
			"parts": []map[string]any{{"text": text}},
		}
	}

	return map[string]any{
		"candidates": []map[string]any{candidate},
		"usageMetadata": map[string]any{
			"promptTokenCount":     promptTokens,
			"candidatesTokenCount": outputTokens,
			"totalTokenCount":      promptTokens + outputTokens,
		},
		"modelVersion": model + "-mock",
	}
}

// estimateTokens is the usual rough heuristic: about four characters per token.
// It only has to be stable and plausible, so cost-metering code has something to add up.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

func fingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex12(binary.BigEndian.Uint64(sum[:8]))
}

const hexdigits = "0123456789abcdef"

func hex12(v uint64) string {
	buf := make([]byte, 12)
	for i := 11; i >= 0; i-- {
		buf[i] = hexdigits[v&0xf]
		v >>= 4
	}
	return string(buf)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
