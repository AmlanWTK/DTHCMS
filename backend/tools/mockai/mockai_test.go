package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func post(t *testing.T, h http.Handler, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const samplePrompt = `{"contents":[{"role":"user","parts":[{"text":"Summarise this visit."}]}]}`

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "mock") {
		t.Error("health response must make it obvious this is a mock")
	}
}

func TestGenerateReturnsGeminiShapedResponse(t *testing.T) {
	rec := post(t, testServer(t), "/v1beta/models/gemini-2.5-flash:generateContent", samplePrompt, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if len(body.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(body.Candidates))
	}
	if body.Candidates[0].FinishReason != "STOP" {
		t.Errorf("finishReason = %q, want STOP", body.Candidates[0].FinishReason)
	}
	if !strings.Contains(body.Candidates[0].Content.Parts[0].Text, "MOCK RESPONSE") {
		t.Error("mock output must be visibly labelled as mock")
	}
	if body.UsageMetadata.TotalTokenCount == 0 {
		t.Error("usage metadata must be populated so cost metering can be exercised")
	}
	if !strings.HasSuffix(body.ModelVersion, "-mock") {
		t.Errorf("modelVersion = %q, want a -mock suffix", body.ModelVersion)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	h := testServer(t)
	first := post(t, h, "/v1beta/models/gemini-2.5-flash:generateContent", samplePrompt, nil).Body.String()
	second := post(t, h, "/v1beta/models/gemini-2.5-flash:generateContent", samplePrompt, nil).Body.String()

	if first != second {
		t.Error("the same prompt must produce the same response; tests and response caching depend on it")
	}
}

func TestGenerateHonoursJSONResponseMimeType(t *testing.T) {
	req := `{"contents":[{"role":"user","parts":[{"text":"x"}]}],"generationConfig":{"responseMimeType":"application/json"}}`
	rec := post(t, testServer(t), "/v1beta/models/gemini-2.5-flash:generateContent", req, nil)

	var body struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("outer response invalid: %v", err)
	}

	inner := body.Candidates[0].Content.Parts[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(inner), &parsed); err != nil {
		t.Fatalf("a JSON-mime-type request must return parseable JSON, got %q", inner)
	}
}

func TestScenarios(t *testing.T) {
	h := testServer(t)
	path := "/v1beta/models/gemini-2.5-flash:generateContent"

	t.Run("error", func(t *testing.T) {
		rec := post(t, h, path, samplePrompt, map[string]string{"X-Mock-Scenario": "error"})
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})

	t.Run("overload sets Retry-After", func(t *testing.T) {
		rec := post(t, h, path, samplePrompt, map[string]string{"X-Mock-Scenario": "overload"})
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want 429", rec.Code)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Error("a 429 must carry Retry-After so backoff can honour it")
		}
	})

	t.Run("invalid body fails to parse", func(t *testing.T) {
		rec := post(t, h, path, samplePrompt, map[string]string{"X-Mock-Scenario": "invalid"})
		var anything map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &anything); err == nil {
			t.Error("the invalid scenario must return a body the client cannot parse")
		}
	})

	t.Run("refusal returns no content", func(t *testing.T) {
		rec := post(t, h, path, samplePrompt, map[string]string{"X-Mock-Scenario": "refusal"})
		if !strings.Contains(rec.Body.String(), "SAFETY") {
			t.Errorf("refusal must report a SAFETY finish reason: %s", rec.Body.String())
		}
	})

	t.Run("slow delays the response", func(t *testing.T) {
		original := SlowDelay
		SlowDelay = 40 * time.Millisecond
		defer func() { SlowDelay = original }()

		start := time.Now()
		post(t, h, path, samplePrompt, map[string]string{"X-Mock-Scenario": "slow"})
		if time.Since(start) < 40*time.Millisecond {
			t.Error("the slow scenario must actually delay, so timeout handling can be tested")
		}
	})
}

func TestGenerateRejectsMalformedRequest(t *testing.T) {
	rec := post(t, testServer(t), "/v1beta/models/gemini-2.5-flash:generateContent", "{not json", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestOCRReturnsConfidenceScoredOutput(t *testing.T) {
	rec := post(t, testServer(t), "/v1/ocr", `{"document_id":"doc_1"}`, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Engine    string `json:"engine"`
		Extracted []struct {
			Analyte    string  `json:"analyte"`
			Confidence float64 `json:"confidence"`
		} `json:"extracted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if body.Engine != "mockai" {
		t.Errorf("engine = %q, want mockai — the engine must always identify itself", body.Engine)
	}
	if len(body.Extracted) == 0 {
		t.Fatal("extracted values must be present so the validation queue has something to show")
	}
	if body.Extracted[0].Confidence == 0 {
		t.Error("every extracted value must carry a confidence score")
	}
}

func TestUnknownRouteExplainsItself(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "supported") {
		t.Error("a 404 should tell the developer which routes exist")
	}
}
