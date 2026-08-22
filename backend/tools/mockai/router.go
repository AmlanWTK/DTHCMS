package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Scenario forces a particular response shape, so failure paths can be tested.
type Scenario string

const (
	ScenarioDefault  Scenario = "default"
	ScenarioSlow     Scenario = "slow"
	ScenarioError    Scenario = "error"
	ScenarioOverload Scenario = "overload"
	ScenarioInvalid  Scenario = "invalid"
	ScenarioRefusal  Scenario = "refusal"
)

// SlowDelay is how long the "slow" scenario waits. Short enough not to annoy, long
// enough to trip a tight timeout.
var SlowDelay = 3 * time.Second

// NewRouter builds the mock's HTTP handler.
func NewRouter(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
			"role":   "mock — responses are canned, no model is contacted",
		})
	})

	mux.HandleFunc("POST /v1beta/models/{model}", handleGenerate(logger))
	mux.HandleFunc("POST /v1/ocr", handleOCR(logger))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "mockai: no handler for " + r.Method + " " + r.URL.Path,
			"hint":  "supported: GET /healthz, POST /v1beta/models/{model}:generateContent, POST /v1/ocr",
		})
	})

	return logging(logger, mux)
}

func scenarioOf(r *http.Request) Scenario {
	value := r.Header.Get("X-Mock-Scenario")
	if value == "" {
		value = r.URL.Query().Get("scenario")
	}
	switch Scenario(strings.ToLower(value)) {
	case ScenarioSlow:
		return ScenarioSlow
	case ScenarioError:
		return ScenarioError
	case ScenarioOverload:
		return ScenarioOverload
	case ScenarioInvalid:
		return ScenarioInvalid
	case ScenarioRefusal:
		return ScenarioRefusal
	default:
		return ScenarioDefault
	}
}

// applyScenario handles the scenarios that short-circuit a normal response.
// It reports whether the request was fully handled.
func applyScenario(w http.ResponseWriter, s Scenario) bool {
	switch s {
	case ScenarioSlow:
		time.Sleep(SlowDelay)
		return false
	case ScenarioError:
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]any{
				"code":    500,
				"message": "mockai: simulated provider failure",
				"status":  "INTERNAL",
			},
		})
		return true
	case ScenarioOverload:
		w.Header().Set("Retry-After", "17")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": map[string]any{
				"code":    429,
				"message": "mockai: simulated rate limit",
				"status":  "RESOURCE_EXHAUSTED",
			},
		})
		return true
	case ScenarioInvalid:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Deliberately malformed: a valid HTTP response the client cannot parse.
		_, _ = w.Write([]byte(`{"candidates": [{"content": {"parts": [{"text": "unterminated`))
		return true
	default:
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Deliberately does not log request bodies: even synthetic prompts should not
		// establish the habit of logging model input.
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"scenario", string(scenarioOf(r)),
			"duration_ms", time.Since(start).Milliseconds())
	})
}
