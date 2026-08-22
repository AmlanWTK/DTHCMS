// Command mockai is a local stand-in for the Gemini API and the OCR service.
//
// It exists so that development, tests and demos never call a real model. That matters
// for three reasons:
//
//   - ADR-0007: the Gemini free tier may not receive patient data, and development data,
//     while synthetic, should never establish the habit of calling a real endpoint.
//   - Tests must be deterministic. The same request always produces the same response here.
//   - Development must work with no internet connection and no API key.
//
// Responses are deliberately, visibly fake. Scenarios can be forced with the
// X-Mock-Scenario header (or ?scenario=) to exercise failure paths:
//
//	default   a normal response
//	slow      responds after a delay, for timeout handling
//	error     HTTP 500, for retry and circuit-breaker behaviour
//	overload  HTTP 429 with Retry-After, for rate-limit handling
//	invalid   valid HTTP, malformed body, for schema-validation failure
//	refusal   the model declines, for safety-refusal handling
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	// The container image is distroless: no shell, no curl, no wget. The health check
	// is therefore the binary asking itself whether it is alive.
	healthcheck := flag.Bool("healthcheck", false, "probe a running instance and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(probe())
	}

	// A mock that reaches production would silently replace clinical AI with canned
	// text. Refuse to start anywhere that calls itself production.
	if env := strings.ToLower(os.Getenv("DTHCMS_ENV")); env == "production" || env == "prod" {
		fmt.Fprintln(os.Stderr, "mockai: refusing to start with DTHCMS_ENV=production")
		os.Exit(1)
	}

	addr := os.Getenv("MOCKAI_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	server := &http.Server{
		Addr:              addr,
		Handler:           NewRouter(logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("mockai listening",
		"addr", addr,
		"note", "all responses are canned; no model is contacted")

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("mockai stopped", "error", err)
		os.Exit(1)
	}
}

// probe performs the container health check against a locally running instance.
func probe() int {
	addr := os.Getenv("MOCKAI_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mockai: healthcheck failed: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "mockai: healthcheck status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
