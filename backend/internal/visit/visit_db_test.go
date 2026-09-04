package visit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// Visits and encounters, end to end and against a real database (CP38).

type api struct {
	*testsupport.DB
	pool     *pgxpool.Pool
	store    *visit.Store
	service  *visit.Service
	server   *httptest.Server
	clock    *clock.Fixed
	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	notices  *recordingNotifier
}

// recordingNotifier stands in for the realtime gateway (CP40). The production adapter lives
// in cmd/api, because `visit` may not import `realtime`; what this module can test is that
// it announces the right thing at the right moment, which is what the board's two-second
// criterion actually rests on.
type recordingNotifier struct {
	mu   sync.Mutex
	seen []visit.QueueChange
}

func (n *recordingNotifier) QueueChanged(_ context.Context, change visit.QueueChange) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = append(n.seen, change)
}

func (n *recordingNotifier) kinds() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, 0, len(n.seen))
	for _, change := range n.seen {
		out = append(out, change.Kind)
	}
	return out
}

func (n *recordingNotifier) reset() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = nil
}

type staff struct {
	facility, user, device uuid.UUID
	permissions            []string
	role                   string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      "R001", Permissions: s.permissions, Roles: []string{s.role},
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID, SessionID: caller.SessionID,
					Code: caller.Code, DeviceID: s.device.String(),
					Role: s.role, Station: "STN_REGISTRATION",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func newAPI(t *testing.T, permissions ...string) *api {
	t.Helper()
	if len(permissions) == 0 {
		permissions = []string{"visit.open", "visit.close", "visit.read", "visit.attend",
			"board.read", "visit.reroute"}
	}
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &api{DB: base, pool: pool, user: uuid.New(), device: uuid.New()}
	h.clock = clock.NewFixed(time.Date(2026, 9, 14, 4, 42, 0, 0, time.UTC))
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: h.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).Register(ctx); err != nil {
		t.Fatal(err)
	}
	h.store = visit.NewStore(pool)
	h.notices = &recordingNotifier{}
	h.service = visit.NewService(h.store, events, h.clock).Notify(h.notices)

	h.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := visit.NewHandlers(visit.HandlersConfig{
		Service: h.service, Store: h.store, Clock: h.clock, Logger: logger,
	})
	who := staff{facility: h.facility, user: h.user, device: h.device,
		permissions: permissions, role: "REGISTRATION"}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 16, RequestTimeout: 10 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) {
			handlers.Mount(r)
			handlers.MountStations(r)
			handlers.MountBoard(r)
			r.Route("/patients", handlers.MountPatient)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(router)
	t.Cleanup(h.server.Close)
	return h
}

func (h *api) seed(t *testing.T) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'R001', 'Registration Officer', 'নিবন্ধন কর্মকর্তা', 'active')`,
		h.user, h.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 1', 'tablet', 'active', now())`, h.device, h.facility); err != nil {
		t.Fatal(err)
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000137', 'Md Rahim Uddin', 'male', DATE '1985-06-14',
		        'day', 'national_id', '+8801711111101', 'active', $3, now())`,
		h.patient, h.facility, h.user); err != nil {
		t.Fatal(err)
	}
	h.asked(t, h.patient)
}

// asked records that somebody asked this patient about allergies.
//
// Not decoration. From CP54 the queue is gated: a patient with no allergy status cannot be
// put in the queue for any station after the history station, and the gate is a trigger on
// `core.queue_entry` rather than a check in a handler — so these tests meet it too, exactly as
// a support script would.
//
// Written straight into the read model rather than through the allergy service on purpose:
// `visit` may not import `allergy`, and a queue test should be a test of the queue. What it
// stands in for is the ordinary case, which is that somebody at station 4 asked.
func (h *api) asked(t *testing.T, patient uuid.UUID) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		INSERT INTO read.allergy_assertion (id, facility_id, patient_id, kind,
		                                    asserted_at, asserted_by, asserted_role,
		                                    event_id, global_seq)
		VALUES ($1, $2, $3, 'NO_KNOWN_ALLERGY', now(), $4, 'HISTORY', $5,
		        (SELECT coalesce(max(global_seq), 0) + 1 FROM read.allergy_assertion))`,
		uuid.New(), h.facility, patient, h.user, uuid.New()); err != nil {
		t.Fatalf("recording that the allergy question was asked: %v", err)
	}
}

func (h *api) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("X-Active-Role", "REGISTRATION")
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return resp, decoded
}

// --- helpers the tests read as sentences ---

func (h *api) openVisit(t *testing.T, edit func(map[string]any)) map[string]any {
	t.Helper()
	body := map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "patient_id": h.patient.String(),
		"visit_type": "new", "chief_complaint": "Sugar problem for three months.",
	}
	if edit != nil {
		edit(body)
	}
	resp, decoded := h.call(t, http.MethodPost, "/v1/visits", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("opening a visit returned %d: %v", resp.StatusCode, decoded)
	}
	return decoded["visit"].(map[string]any)
}

func (h *api) arrive(t *testing.T, visitID, station string) (*http.Response, map[string]any) {
	t.Helper()
	return h.call(t, http.MethodPost, "/v1/visits/"+visitID+"/encounters", map[string]any{
		"event_id": uuid.Must(uuid.NewV7()).String(), "station_code": station,
	})
}

func (h *api) depart(t *testing.T, visitID, encounterID, outcome string) (*http.Response, map[string]any) {
	t.Helper()
	return h.call(t, http.MethodPost,
		"/v1/visits/"+visitID+"/encounters/"+encounterID+"/finish",
		map[string]any{"event_id": uuid.Must(uuid.NewV7()).String(), "outcome": outcome})
}

func (h *api) closeVisit(t *testing.T, visitID string, edit func(map[string]any)) (*http.Response, map[string]any) {
	t.Helper()
	body := map[string]any{
		"event_id":         uuid.Must(uuid.NewV7()).String(),
		"diagnoses":        "Type 2 diabetes mellitus, newly diagnosed.",
		"plan":             "Metformin 500 mg twice daily; diet counselling; review in three months.",
		"next_review_days": 90,
	}
	if edit != nil {
		edit(body)
	}
	return h.call(t, http.MethodPost, "/v1/visits/"+visitID+"/close", body)
}
