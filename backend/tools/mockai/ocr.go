package main

import (
	"io"
	"log/slog"
	"net/http"
)

// handleOCR stands in for the Python ML service that arrives at CP99–CP101.
//
// The response shape is intentionally close to what the real pipeline will return —
// text with per-field confidence and bounding boxes — so that the validation queue and
// confidence gating can be built and tested before the OCR engine is chosen (D-16).
func handleOCR(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if applyScenario(w, scenarioOf(r)) {
			return
		}

		// Drain the upload so the client sees a normal completion.
		n, _ := io.Copy(io.Discard, io.LimitReader(r.Body, 32<<20))

		writeJSON(w, http.StatusOK, map[string]any{
			"engine":         "mockai",
			"engine_version": "0.0.0-mock",
			"bytes_received": n,
			"pages": []map[string]any{
				{
					"page":            1,
					"script":          "mixed",
					"mean_confidence": 0.5,
					"text":            "MOCK OCR OUTPUT — no OCR engine was contacted.",
					"blocks": []map[string]any{
						{
							"text":       "HbA1c",
							"confidence": 0.5,
							"bbox":       []int{100, 200, 180, 220},
						},
						{
							"text":       "8.2 %",
							"confidence": 0.5,
							"bbox":       []int{300, 200, 360, 220},
						},
					},
				},
			},
			"extracted": []map[string]any{
				{
					"analyte":    "HbA1c",
					"value":      8.2,
					"unit":       "%",
					"confidence": 0.5,
					"source":     "mockai",
				},
			},
			"note": "Deterministic canned output. Real extraction arrives at CP101–CP104.",
		})
	}
}
