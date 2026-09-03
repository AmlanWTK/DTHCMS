package realtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

// A real browser against the gateway.
//
// This is the test that makes writing RFC 6455 by hand defensible (ADR-0018). The frame
// layer is checked against hand-written bytes, and the gateway against this repository's
// own client — but both were written by the same person from the same reading of the
// document, so they would agree about a misreading. Chromium would not, and Chromium is the
// client the web application actually has.
//
// It skips when Playwright is not installed, because a developer without it should not have
// a red build; CI has it, and `make verify` runs there.

func TestChromiumSpeaksToTheGateway(t *testing.T) {
	if testing.Short() {
		t.Skip("browser test")
	}
	script, err := filepath.Abs("testdata/browser_client.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}

	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	facility := uuid.New()
	hub := realtime.NewHub(realtime.HubConfig{Filter: realtime.RBACFilter{}, Logger: testLogger()})
	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub: hub, Resolver: resolver, Clock: clock.Real{}, Logger: testLogger(),
		Heartbeat: time.Second,
		// No OriginPatterns: same-origin only. The page below is served by this same
		// server, so the browser's Origin matches its Host. A page from anywhere else —
		// including `about:blank`, whose Origin is the string "null" — is refused, which
		// is what stops any site the user is reading from opening a socket here.
	})

	// The credential comes from the query string here and only here: a browser cannot set
	// a header on `new WebSocket`, and the production path is the session cookie, which
	// the browser attaches by itself.
	mux := http.NewServeMux()
	// A page to load, so the browser has a real origin to connect from.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>gateway</title>"))
	})
	mux.HandleFunc("/v1/realtime", func(w http.ResponseWriter, r *http.Request) {
		caller := httpx.Caller{
			UserID:     r.URL.Query().Get("user"),
			FacilityID: facility.String(),
			ActiveRole: r.URL.Query().Get("role"),
		}
		httpx.CallerForTest(caller, handler).ServeHTTP(w, r)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	userID := uuid.New()
	resolver.set(userID, staffed(auth.RolePhysician, facility, nil))
	patient := uuid.New()
	topic := realtime.PatientTopic(patient)

	// Publish once the browser has subscribed. The gateway does not replay, so a message
	// sent before the subscription lands would simply not arrive.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				hub.Deliver(realtime.Message{
					Seq: time.Now().UnixNano(), Topic: topic, Kind: "measurement.recorded",
					Requires: auth.PermObservationReadValues, PatientID: patient.String(),
					FacilityID: facility.String(), At: time.Now(),
				})
			}
		}
	}()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/realtime"
	cmd := exec.CommandContext(ctx, "node", script, server.URL+"/", wsURL,
		userID.String(), string(auth.RolePhysician), string(topic))
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		t.Skipf("playwright is not available in this environment: %v", err)
	}

	line := lastJSONLine(string(output))
	if line == "" {
		t.Skipf("the browser harness produced no result; playwright is probably missing:\n%s", output)
	}

	var result struct {
		OK        bool   `json:"ok"`
		Error     string `json:"error"`
		Envelopes []struct {
			Type    string            `json:"type"`
			Error   string            `json:"error"`
			Message *realtime.Message `json:"message"`
		} `json:"envelopes"`
	}
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("the browser harness said: %s", output)
	}
	if !result.OK {
		t.Fatalf("Chromium could not talk to the gateway: %s\n%s", result.Error, output)
	}

	// What the browser saw, in order: the welcome, the subscription acknowledgement, the
	// pong, an error for the deliberately unknown command, and a message.
	seen := map[string]bool{}
	for _, envelope := range result.Envelopes {
		seen[envelope.Type] = true
		if envelope.Type == "message" && envelope.Message.PatientID != patient.String() {
			t.Errorf("the browser received another patient's message")
		}
	}
	for _, want := range []string{"welcome", "subscribed", "pong", "error", "message"} {
		if !seen[want] {
			t.Errorf("Chromium never saw a %q envelope; it saw %v", want, seen)
		}
	}
}

func lastJSONLine(output string) string {
	var last string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			last = strings.TrimSpace(line)
		}
	}
	return last
}

// The other half: a page on another origin must not be able to open a socket here.
//
// A WebSocket handshake is exempt from the same-origin policy and carries the browser's
// cookies, so without this check any site the user happens to be reading could open a
// connection as them and watch a clinic's traffic. It is the CSRF hole with a long-lived
// connection attached, and the only way to be sure of it is to try it from a browser.
func TestChromiumOnAnotherOriginIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("browser test")
	}
	script, err := filepath.Abs("testdata/browser_client.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}

	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	facility := uuid.New()
	hub := realtime.NewHub(realtime.HubConfig{Filter: realtime.RBACFilter{}, Logger: testLogger()})
	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub: hub, Resolver: resolver, Clock: clock.Real{}, Logger: testLogger(),
	})

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := httpx.Caller{
			UserID: r.URL.Query().Get("user"), FacilityID: facility.String(),
			ActiveRole: r.URL.Query().Get("role"),
		}
		httpx.CallerForTest(caller, handler).ServeHTTP(w, r)
	}))
	defer gateway.Close()

	// Another site entirely, serving a page that tries to connect to the gateway.
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>somewhere else</title>"))
	}))
	defer elsewhere.Close()

	userID := uuid.New()
	resolver.set(userID, staffed(auth.RolePhysician, facility, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(gateway.URL, "http") + "/v1/realtime"
	cmd := exec.CommandContext(ctx, "node", script, elsewhere.URL+"/", wsURL,
		userID.String(), string(auth.RolePhysician), string(realtime.PatientTopic(uuid.New())))
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		t.Skipf("playwright is not available: %v", err)
	}
	line := lastJSONLine(string(output))
	if line == "" {
		t.Skipf("no result from the browser harness:\n%s", output)
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		t.Fatalf("the browser harness said: %s", output)
	}
	if result.OK {
		t.Fatal("a page on another origin opened a connection to the gateway")
	}
	if hub.Count() != 0 {
		t.Errorf("%d connections are open after a cross-origin attempt", hub.Count())
	}
}
