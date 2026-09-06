package offline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/offline"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The offline sync protocol, server side (CP65).
//
// The five acceptance criteria, and which tests carry them:
//
//  1. **a bad event never blocks the rest of its batch** — one bad event among fifty;
//  2. duplicate submission produces no duplicate events;
//  3. `occurred_at` is preserved and `recorded_at` reflects arrival;
//  4. per-aggregate ordering is preserved;
//  5. **events from revoked devices are quarantined and surfaced, never silently dropped** — the
//     one that needed a table, and the one whose failure mode is a morning of measurements
//     disappearing with nobody noticing.

type api struct {
	*testsupport.DB
	pool *pgxpool.Pool

	facility  uuid.UUID
	patient   uuid.UUID
	operator  uuid.UUID
	physician uuid.UUID
	device    uuid.UUID
	other     uuid.UUID // a second enrolled device, for the revoked path

	user        uuid.UUID
	activeDev   uuid.UUID
	role        string
	permissions []string

	clock  *clock.Fixed
	store  *offline.Store
	events *eventstore.Store
	server *httptest.Server
}

type staff struct{ h *api }

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.h.user.String(), FacilityID: s.h.facility.String(),
		// Deliberately not setting Caller.DeviceID: a device-bound *session* makes the middleware
		// demand a signed request, which is correct in production and is CP18's business rather
		// than this suite's. The verified device arrives on the principal below, which is where
		// the handlers read it from.
		SessionID:   uuid.NewSHA1(s.h.user, []byte("session")).String(),
		Code:        "S001",
		Permissions: s.h.permissions, Roles: []string{s.h.role}, ActiveRole: s.h.role,
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code,
					DeviceID: s.h.activeDev.String(), Role: caller.ActiveRole,
					Station: "STN_VITALS",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func newAPI(t *testing.T) *api {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	cfg, err := pgxpool.ParseConfig(base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &api{
		DB: base, pool: pool,
		operator: uuid.New(), physician: uuid.New(),
		device: uuid.New(), other: uuid.New(),
		role: "CLINICAL_ASSISTANT",
		permissions: []string{
			"observation.write.vitals", "observation.read.values",
			"patient.read.demographics", "visit.read",
		},
	}
	h.user = h.operator
	h.activeDev = h.device
	h.clock = clock.NewFixed(time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC))
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}

	h.events = eventstore.New(eventstore.Config{
		Pool: pool, Clock: h.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, h.events).Register(ctx); err != nil {
		t.Fatal(err)
	}
	h.store = offline.NewStore(pool)
	h.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := offline.NewHandlers(offline.HandlersConfig{
		Service: offline.NewService(h.store, h.events, h.clock),
		Store:   h.store, Clock: h.clock, Logger: logger,
	})
	who := staff{h: h}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 22, RequestTimeout: 60 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) { handlers.Mount(r) },
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
	for _, who := range []struct {
		id           uuid.UUID
		code, en, bn string
	}{
		{h.operator, "S001", "Nasrin Sultana", "নাসরিন সুলতানা"},
		{h.physician, "P001", "Dr Nahid-Ul-Haque", "ডা. নাহিদ-উল-হক"},
	} {
		if _, err := h.SQL.Exec(`
			INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
			VALUES ($1, $2, $3, $4, $5, 'active')`,
			who.id, h.facility, who.code, who.en, who.bn); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []struct {
		id     uuid.UUID
		name   string
		status string
	}{
		{h.device, "Tablet 4", "active"},
		{h.other, "Tablet 9", "active"},
	} {
		if _, err := h.SQL.Exec(`
			INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
			VALUES ($1, $2, $3, 'tablet', $4, now())`,
			d.id, h.facility, d.name, d.status); err != nil {
			t.Fatal(err)
		}
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000701', 'Jahanara Khatun', 'female', DATE '1971-04-02',
		        'day', 'national_id', '+8801711111701', 'active', $3, now())`,
		h.patient, h.facility, h.operator); err != nil {
		t.Fatal(err)
	}
}

func (h *api) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("X-Active-Role", h.role)
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// vital builds one offline blood pressure.
func (h *api) vital(occurred time.Time, systolic float64) offline.Incoming {
	payload, _ := json.Marshal(map[string]any{
		"observation_id": uuid.New().String(),
		"facility_id":    h.facility.String(),
		"patient_id":     h.patient.String(),
		"code":           "BP_SYSTOLIC",
		"value":          systolic,
		"unit":           "mm[Hg]",
		"effective_at":   occurred.UTC(),
		"source":         "STATION",
	})
	patient := h.patient
	return offline.Incoming{
		EventID: uuid.New(), AggregateType: "PATIENT", AggregateID: h.patient,
		PatientID: &patient, EventType: "OBSERVATION_RECORDED", EventVersion: 1,
		OccurredAt: occurred.UTC(), Payload: payload,
	}
}

func (h *api) push(t *testing.T, events []offline.Incoming, clientClock *time.Time) (*http.Response, map[string]any, uuid.UUID) {
	t.Helper()
	batch := uuid.New()
	body := map[string]any{
		"batch_id": batch, "device_id": h.activeDev, "events": events,
	}
	if clientClock != nil {
		body["client_clock"] = clientClock.UTC()
	}
	resp, out := h.call(t, "POST", "/v1/sync/events", body)
	return resp, out, batch
}

func outcomes(body map[string]any) map[string]string {
	out := map[string]string{}
	results, _ := body["results"].([]any)
	for _, raw := range results {
		r, _ := raw.(map[string]any)
		out[r["event_id"].(string)] = r["outcome"].(string)
	}
	return out
}

func counts(body map[string]any, key string) int {
	value, _ := body[key].(float64)
	return int(value)
}

// --- criterion 1: a bad event never blocks the rest ---

// One bad event among fifty, which is the checkpoint's own test. The bad one is bad in the way a
// real one is: a payload the registry's validation refuses.
func TestOneBadEventAmongFiftyDoesNotBlockTheOtherFortyNine(t *testing.T) {
	h := newAPI(t)
	base := h.clock.Now().Add(-3 * time.Hour)

	events := make([]offline.Incoming, 0, 50)
	for i := 0; i < 50; i++ {
		events = append(events, h.vital(base.Add(time.Duration(i)*time.Minute), float64(110+i%20)))
	}
	// The bad one, in the middle so that a batch which stopped at the first failure would be
	// visibly half-done rather than empty.
	bad := 25
	events[bad].Payload = json.RawMessage(`{"observation_id":"not-a-uuid"}`)

	resp, body, _ := h.push(t, events, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the push answered %d: %v", resp.StatusCode, body)
	}
	if got := counts(body, "accepted"); got != 49 {
		t.Fatalf("%d events were accepted, want 49", got)
	}
	if got := counts(body, "rejected"); got != 1 {
		t.Fatalf("%d events were rejected, want 1", got)
	}

	got := outcomes(body)
	if got[events[bad].EventID.String()] != "REJECTED" {
		t.Fatalf("the bad event is %s", got[events[bad].EventID.String()])
	}
	// And the one immediately after it, on the same aggregate, still landed — because an
	// observation is a fact about a moment, not a mutation of a state, and blocking it would hold
	// a morning's work hostage to one typo.
	if got[events[bad+1].EventID.String()] != "ACCEPTED" {
		t.Fatalf("the event after the bad one is %s", got[events[bad+1].EventID.String()])
	}
}

// The refusal has to say which field, or a device's next version cannot stop sending it.
func TestARejectionSaysWhy(t *testing.T) {
	h := newAPI(t)
	bad := h.vital(h.clock.Now().Add(-time.Hour), 120)
	bad.Payload = json.RawMessage(`{"observation_id":"not-a-uuid"}`)

	_, body, _ := h.push(t, []offline.Incoming{bad}, nil)
	results, _ := body["results"].([]any)
	first, _ := results[0].(map[string]any)
	if first["reason_code"] == "" || first["reason"] == "" {
		t.Fatalf("the rejection says nothing: %v", first)
	}
}

// --- criterion 2: no duplicate events ---

func TestResubmittingAWholeBatchChangesNothing(t *testing.T) {
	h := newAPI(t)
	base := h.clock.Now().Add(-2 * time.Hour)
	events := []offline.Incoming{
		h.vital(base, 118), h.vital(base.Add(time.Minute), 122), h.vital(base.Add(2*time.Minute), 130),
	}

	_, first, batch := h.push(t, events, nil)
	if counts(first, "accepted") != 3 {
		t.Fatalf("the first push accepted %d", counts(first, "accepted"))
	}

	var after int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'OBSERVATION_RECORDED'`).Scan(&after); err != nil {
		t.Fatal(err)
	}

	// The same events under a **new** batch id: the ledger absorbs them by event id.
	resp, second := h.call(t, "POST", "/v1/sync/events", map[string]any{
		"batch_id": uuid.New(), "device_id": h.activeDev, "events": events,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the resend answered %d: %v", resp.StatusCode, second)
	}
	if counts(second, "duplicated") != 3 || counts(second, "accepted") != 0 {
		t.Fatalf("a resend produced %d accepted and %d duplicate",
			counts(second, "accepted"), counts(second, "duplicated"))
	}

	var now int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'OBSERVATION_RECORDED'`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	if now != after {
		t.Fatalf("the ledger grew from %d to %d on a resend", after, now)
	}

	// And the **same** batch id is answered from the receipt without touching the ledger. That is
	// the lost-response case, which is the one this endpoint exists to survive.
	resp, replay := h.call(t, "GET", "/v1/sync/batches/"+batch.String(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the receipt answered %d: %v", resp.StatusCode, replay)
	}
	if replayed, _ := replay["replayed"].(bool); !replayed {
		t.Fatalf("a re-read receipt is not marked replayed: %v", replay)
	}
	if counts(replay, "accepted") != 3 {
		t.Fatalf("the stored receipt says %d accepted", counts(replay, "accepted"))
	}
}

func TestASecondPushOfTheSameBatchIdIsAnsweredFromTheReceipt(t *testing.T) {
	h := newAPI(t)
	events := []offline.Incoming{h.vital(h.clock.Now().Add(-time.Hour), 124)}
	batch := uuid.New()
	body := map[string]any{"batch_id": batch, "device_id": h.activeDev, "events": events}

	if _, first := h.call(t, "POST", "/v1/sync/events", body); counts(first, "accepted") != 1 {
		t.Fatalf("the first push accepted %d", counts(first, "accepted"))
	}
	// A second push of the same batch id — a client that retried before the answer arrived.
	_, second := h.call(t, "POST", "/v1/sync/events", body)
	if replayed, _ := second["replayed"].(bool); !replayed {
		t.Fatalf("the same batch id was reprocessed rather than replayed: %v", second)
	}
}

// --- criterion 3: occurred_at preserved, recorded_at is arrival ---

func TestTheOriginalTimeIsKeptAndTheArrivalTimeIsTheServers(t *testing.T) {
	h := newAPI(t)
	// Taken at 08:40; it is now 11:00 and the tablet has just found signal.
	taken := time.Date(2026, 9, 14, 8, 40, 0, 0, time.UTC)
	event := h.vital(taken, 138)

	_, body, _ := h.push(t, []offline.Incoming{event}, nil)
	if counts(body, "accepted") != 1 {
		t.Fatalf("the push accepted %d: %v", counts(body, "accepted"), body)
	}

	var occurred, recorded time.Time
	var source string
	if err := h.SQL.QueryRow(
		`SELECT occurred_at, recorded_at, source FROM ledger.event WHERE event_id = $1`,
		event.EventID).Scan(&occurred, &recorded, &source); err != nil {
		t.Fatal(err)
	}
	if !occurred.UTC().Equal(taken) {
		t.Fatalf("occurred_at is %v, want %v", occurred.UTC(), taken)
	}
	if !recorded.UTC().Equal(h.clock.Now().UTC()) {
		t.Fatalf("recorded_at is %v, want the server's clock %v", recorded.UTC(), h.clock.Now().UTC())
	}
	// Not MOBILE_ONLINE whatever the client said. This is how a reader tells a value entered at
	// the bedside from one that arrived hours later through a queue.
	if source != "MOBILE_OFFLINE_SYNC" {
		t.Fatalf("the source is %s", source)
	}
}

// --- criterion 4: per-aggregate ordering ---

func TestPerAggregateOrderIsPreservedAndAggregatesDoNotInterfere(t *testing.T) {
	h := newAPI(t)
	// A second patient, so there are two aggregates in one batch.
	second := uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000702', 'Abdul Karim', 'male', DATE '1968-01-11',
		        'day', 'national_id', '+8801711111702', 'active', $3, now())`,
		second, h.facility, h.operator); err != nil {
		t.Fatal(err)
	}

	base := h.clock.Now().Add(-time.Hour)
	events := []offline.Incoming{}
	for i := 0; i < 3; i++ {
		events = append(events, h.vital(base.Add(time.Duration(i)*time.Minute), float64(120+i)))
	}
	// Interleaved with the other patient's, as a real outbox would be.
	for i := 0; i < 3; i++ {
		e := h.vital(base.Add(time.Duration(i)*time.Minute), float64(130+i))
		e.AggregateID = second
		e.PatientID = &second
		var payload map[string]any
		_ = json.Unmarshal(e.Payload, &payload)
		payload["patient_id"] = second.String()
		e.Payload, _ = json.Marshal(payload)
		events = append(events, e)
	}

	_, body, _ := h.push(t, events, nil)
	if counts(body, "accepted") != 6 {
		t.Fatalf("%d of 6 accepted: %v", counts(body, "accepted"), body)
	}

	// The per-aggregate sequence follows the order the client sent, for both patients.
	for _, patient := range []uuid.UUID{h.patient, second} {
		rows, err := h.SQL.Query(`
			SELECT event_id, sequence FROM ledger.event
			 WHERE aggregate_id = $1 AND event_type = 'OBSERVATION_RECORDED'
			 ORDER BY sequence`, patient)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var id string
			var seq int64
			if err := rows.Scan(&id, &seq); err != nil {
				t.Fatal(err)
			}
			got = append(got, id)
		}
		rows.Close()

		var want []string
		for _, e := range events {
			if e.AggregateID == patient {
				want = append(want, e.EventID.String())
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("patient %s: order is %v, want %v", patient, got, want)
		}
	}
}

// An event that declares a dependency on an aggregate whose earlier event failed is not attempted.
// Attempting it would produce a sequence conflict, which says something misleading about why it did
// not land — the client would fix the wrong thing.
func TestAnEventDeclaringADependencyIsBlockedRatherThanMisreported(t *testing.T) {
	h := newAPI(t)
	base := h.clock.Now().Add(-time.Hour)

	bad := h.vital(base, 120)
	bad.Payload = json.RawMessage(`{"observation_id":"not-a-uuid"}`)
	dependent := h.vital(base.Add(time.Minute), 124)
	dependent.ExpectedSequence = 7 // it thinks it knows where the aggregate is
	independent := h.vital(base.Add(2*time.Minute), 126)

	_, body, _ := h.push(t, []offline.Incoming{bad, dependent, independent}, nil)
	got := outcomes(body)

	if got[bad.EventID.String()] != "REJECTED" {
		t.Fatalf("the bad event is %s", got[bad.EventID.String()])
	}
	if got[dependent.EventID.String()] != "BLOCKED" {
		t.Fatalf("the dependent event is %s, want BLOCKED", got[dependent.EventID.String()])
	}
	// And the one that declared nothing carried on.
	if got[independent.EventID.String()] != "ACCEPTED" {
		t.Fatalf("the independent event is %s", got[independent.EventID.String()])
	}
	// A blocked event is not in the ledger and not in the quarantine: the client still has it.
	var found int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ledger.event WHERE event_id = $1`,
		dependent.EventID).Scan(&found); err != nil {
		t.Fatal(err)
	}
	if found != 0 {
		t.Fatal("a blocked event reached the ledger")
	}
}

// --- criterion 5: the revoked device ---

// The whole reason this checkpoint has tables. Forty real measurements on a device somebody
// revoked while it was out of signal: accepting them defeats the revocation, dropping them loses a
// morning silently, so they are held and a person decides.
func TestEventsFromARevokedDeviceAreHeldWholeAndSurfaced(t *testing.T) {
	h := newAPI(t)
	base := h.clock.Now().Add(-3 * time.Hour)

	if _, err := h.SQL.Exec(`
		UPDATE core.device SET status = 'revoked', status_reason = 'reported stolen from station 4'
		 WHERE id = $1`, h.device); err != nil {
		t.Fatal(err)
	}

	events := []offline.Incoming{h.vital(base, 142), h.vital(base.Add(time.Minute), 138)}
	resp, body, _ := h.push(t, events, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the push answered %d: %v", resp.StatusCode, body)
	}
	// Not accepted...
	if counts(body, "accepted") != 0 {
		t.Fatalf("%d events from a revoked device were accepted", counts(body, "accepted"))
	}
	// ...and not lost.
	if counts(body, "quarantined") != 2 {
		t.Fatalf("%d of 2 were held: %v", counts(body, "quarantined"), body)
	}
	var inLedger int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ledger.event WHERE event_id = $1`,
		events[0].EventID).Scan(&inLedger); err != nil {
		t.Fatal(err)
	}
	if inLedger != 0 {
		t.Fatal("an event from a revoked device reached the ledger")
	}

	// The client is told, per event, with a code it can branch on and a sentence a person reads.
	results, _ := body["results"].([]any)
	first, _ := results[0].(map[string]any)
	if first["reason_code"] != "DEVICE_REVOKED" {
		t.Fatalf("the hold reason is %v", first["reason_code"])
	}
	if !strings.Contains(first["reason"].(string), "stolen") {
		t.Fatalf("the reason does not say what happened: %v", first["reason"])
	}

	// And a supervisor can see them. The list carries no clinical value — that is what makes it
	// safe to open on a screen that shows a count.
	h.user = h.physician
	h.permissions = []string{"sync.quarantine.read", "sync.quarantine.release"}
	resp, list := h.call(t, "GET", "/v1/sync/quarantine", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the quarantine list answered %d: %v", resp.StatusCode, list)
	}
	held, _ := list["held"].([]any)
	if len(held) != 2 {
		t.Fatalf("%d held events are visible, want 2", len(held))
	}
	item, _ := held[0].(map[string]any)
	if _, hasEnvelope := item["envelope"]; hasEnvelope {
		t.Fatal("the triage list carries the envelope; a list that shows a count should not show a blood pressure")
	}
	if item["event_type"] != "OBSERVATION_RECORDED" || item["device_name"] != "Tablet 4" {
		t.Fatalf("the triage row cannot be read without opening it: %v", item)
	}
	if item["operator_name_bn"] != "নাসরিন সুলতানা" {
		t.Fatalf("the held event does not name the operator: %v", item)
	}
}

// The content is there, whole, for the person who has to decide.
func TestAHeldEventCanBeReadInFullAndReleasedWithItsOriginalTime(t *testing.T) {
	h := newAPI(t)
	taken := time.Date(2026, 9, 14, 8, 40, 0, 0, time.UTC)

	if _, err := h.SQL.Exec(`
		UPDATE core.device SET status = 'revoked', status_reason = 'the operator left the clinic'
		 WHERE id = $1`, h.device); err != nil {
		t.Fatal(err)
	}
	event := h.vital(taken, 144)
	_, body, _ := h.push(t, []offline.Incoming{event}, nil)
	if counts(body, "quarantined") != 1 {
		t.Fatalf("the event was not held: %v", body)
	}

	h.user = h.physician
	h.permissions = []string{"sync.quarantine.read", "sync.quarantine.release"}
	_, list := h.call(t, "GET", "/v1/sync/quarantine", nil)
	held, _ := list["held"].([]any)
	item, _ := held[0].(map[string]any)
	id := item["id"].(string)

	resp, one := h.call(t, "GET", "/v1/sync/quarantine/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading one answered %d: %v", resp.StatusCode, one)
	}
	full, _ := one["held"].(map[string]any)
	envelope, ok := full["envelope"].(map[string]any)
	if !ok {
		t.Fatalf("the single read carries no envelope: %v", full)
	}
	if envelope["event_type"] != "OBSERVATION_RECORDED" {
		t.Fatalf("the envelope is not the event: %v", envelope)
	}

	// A decision needs a reason, for a release as much as for a discard — more, since it is the
	// one that is harder to undo.
	resp, refused := h.call(t, "POST", "/v1/sync/quarantine/"+id+"/release", map[string]any{})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a release with no reason answered %d: %v", resp.StatusCode, refused)
	}

	resp, released := h.call(t, "POST", "/v1/sync/quarantine/"+id+"/release", map[string]any{
		"note": "the operator confirmed she took these readings before the tablet was handed in",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the release answered %d: %v", resp.StatusCode, released)
	}
	out, _ := released["held"].(map[string]any)
	if out["status"] != "RELEASED" || out["released_event_id"] == nil {
		t.Fatalf("the release did not name what it became: %v", out)
	}

	// In the ledger, at the time it was taken — not at the time it was released.
	var occurred time.Time
	var actor uuid.UUID
	var metadata []byte
	if err := h.SQL.QueryRow(
		`SELECT occurred_at, actor_user_id, metadata FROM ledger.event WHERE event_id = $1`,
		event.EventID).Scan(&occurred, &actor, &metadata); err != nil {
		t.Fatalf("the released event is not in the ledger: %v", err)
	}
	if !occurred.UTC().Equal(taken) {
		t.Fatalf("the released event is dated %v, want %v", occurred.UTC(), taken)
	}
	// The physician's name, because the reason it is in the record is that they decided it should
	// be — and the operator carried in the metadata, so the whole truth is there rather than half.
	if actor != h.physician {
		t.Fatalf("the released event is attributed to %v, want the releasing physician", actor)
	}
	var meta map[string]any
	if err := json.Unmarshal(metadata, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["original_operator_id"] != h.operator.String() {
		t.Fatalf("the metadata does not carry the original operator: %v", meta)
	}
	if meta["held_reason_code"] != "DEVICE_REVOKED" {
		t.Fatalf("the metadata does not say why it was held: %v", meta)
	}
}

func TestADiscardIsAttributedAndTheContentIsKept(t *testing.T) {
	h := newAPI(t)
	if _, err := h.SQL.Exec(`
		UPDATE core.device SET status = 'lost', status_reason = 'not seen since Friday'
		 WHERE id = $1`, h.device); err != nil {
		t.Fatal(err)
	}
	event := h.vital(h.clock.Now().Add(-2*time.Hour), 150)
	h.push(t, []offline.Incoming{event}, nil)

	h.user = h.physician
	h.permissions = []string{"sync.quarantine.read", "sync.quarantine.release"}
	_, list := h.call(t, "GET", "/v1/sync/quarantine", nil)
	held, _ := list["held"].([]any)
	item, _ := held[0].(map[string]any)
	id := item["id"].(string)

	resp, discarded := h.call(t, "POST", "/v1/sync/quarantine/"+id+"/discard", map[string]any{
		"note": "nobody can say who was holding the tablet on Friday afternoon",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the discard answered %d: %v", resp.StatusCode, discarded)
	}

	// Kept, not deleted. A discarded measurement with no trace is exactly the silent loss this
	// table exists to prevent, one step later.
	var status, note string
	var by uuid.UUID
	var envelope []byte
	if err := h.SQL.QueryRow(
		`SELECT status, resolution_note, resolved_by, envelope FROM ops.sync_quarantine
		  WHERE event_id = $1`, event.EventID).Scan(&status, &note, &by, &envelope); err != nil {
		t.Fatal(err)
	}
	if status != "DISCARDED" || by != h.physician || note == "" || len(envelope) < 10 {
		t.Fatalf("the discard lost something: status=%s by=%v note=%q envelope=%d bytes",
			status, by, note, len(envelope))
	}

	// And deciding twice is refused rather than silently repeated.
	resp, _ = h.call(t, "POST", "/v1/sync/quarantine/"+id+"/discard", map[string]any{"note": "again"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second decision answered %d, want 409", resp.StatusCode)
	}
}

func TestTheInvariantNoticesAQuarantineDecisionWithNoAuthor(t *testing.T) {
	h := newAPI(t)
	if _, err := h.SQL.Exec(`
		UPDATE core.device SET status = 'revoked', status_reason = 'stolen' WHERE id = $1`,
		h.device); err != nil {
		t.Fatal(err)
	}
	h.push(t, []offline.Incoming{h.vital(h.clock.Now().Add(-time.Hour), 132)}, nil)

	// Past the constraint, the way a restored dump would arrive.
	if _, err := h.SQL.Exec(
		`ALTER TABLE ops.sync_quarantine DROP CONSTRAINT sync_quarantine_resolution_is_whole`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(
		`UPDATE ops.sync_quarantine SET status = 'DISCARDED', resolution_note = 'x'`); err != nil {
		t.Fatal(err)
	}
	var ignored string
	err := h.SQL.QueryRow(
		`SELECT core.assert_every_quarantine_decision_has_an_author()::text`).Scan(&ignored)
	if err == nil {
		t.Fatal("the invariant passed with a decision nobody made")
	}
	if !strings.Contains(err.Error(), "resolved by nobody") {
		t.Fatalf("the invariant failed for the wrong reason: %v", err)
	}
}

// --- the clock ---

func TestAClockFarInTheFutureIsHeldAndSkewIsAlwaysReported(t *testing.T) {
	h := newAPI(t)
	// Backwards is fine and unbounded — a device offline for a week is the entire point.
	old := h.vital(h.clock.Now().Add(-7*24*time.Hour), 128)
	// Forwards past the tolerance is not: an observation dated next week sorts above every real
	// measurement forever, and nothing downstream can tell it from a genuine future record.
	future := h.vital(h.clock.Now().Add(48*time.Hour), 126)

	deviceClock := h.clock.Now().Add(90 * time.Second)
	_, body, _ := h.push(t, []offline.Incoming{old, future}, &deviceClock)
	got := outcomes(body)
	if got[old.EventID.String()] != "ACCEPTED" {
		t.Fatalf("a week-old event is %s", got[old.EventID.String()])
	}
	if got[future.EventID.String()] != "QUARANTINED" {
		t.Fatalf("an event dated two days ahead is %s", got[future.EventID.String()])
	}

	// Skew reported on every receipt, not only when it is large: a client that only learns about
	// skew once it is a problem cannot correct for it gradually.
	skew, ok := body["clock_skew_ms"].(float64)
	if !ok {
		t.Fatalf("no skew was reported: %v", body)
	}
	if skew < 89000 || skew > 91000 {
		t.Fatalf("the skew is %v ms, want about 90000", skew)
	}
	if body["server_time"] == nil {
		t.Fatal("the receipt carries no server time")
	}
}

func TestAPushWithNoClockReportsSkewAsUnknownRatherThanZero(t *testing.T) {
	h := newAPI(t)
	_, body, _ := h.push(t, []offline.Incoming{h.vital(h.clock.Now().Add(-time.Hour), 118)}, nil)
	if _, present := body["clock_skew_ms"]; present {
		t.Fatalf("a push with no clock reported a skew: %v", body["clock_skew_ms"])
	}
}

// --- the batch itself ---

func TestABatchAboveTheLimitIsRefusedRatherThanTruncated(t *testing.T) {
	h := newAPI(t)
	events := make([]offline.Incoming, 0, offline.MaxBatch+1)
	for i := 0; i <= offline.MaxBatch; i++ {
		events = append(events, h.vital(h.clock.Now().Add(-time.Duration(i)*time.Second), 120))
	}
	resp, body, _ := h.push(t, events, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an oversized batch answered %d: %v", resp.StatusCode, body)
	}
	// Nothing landed. A client told "accepted" for a prefix and left to work out which is a
	// client that drops the rest.
	var landed int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'OBSERVATION_RECORDED'`).Scan(&landed); err != nil {
		t.Fatal(err)
	}
	if landed != 0 {
		t.Fatalf("%d events from a refused batch landed", landed)
	}
}

func TestALargeBatchIsProcessedWhole(t *testing.T) {
	h := newAPI(t)
	base := h.clock.Now().Add(-8 * time.Hour)
	events := make([]offline.Incoming, 0, offline.MaxBatch)
	for i := 0; i < offline.MaxBatch; i++ {
		events = append(events, h.vital(base.Add(time.Duration(i)*time.Second), float64(110+i%30)))
	}
	resp, body, _ := h.push(t, events, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a full batch answered %d: %v", resp.StatusCode, body)
	}
	if counts(body, "accepted") != offline.MaxBatch {
		t.Fatalf("%d of %d accepted", counts(body, "accepted"), offline.MaxBatch)
	}
}

func TestAnEmptyBatchIsRefused(t *testing.T) {
	h := newAPI(t)
	resp, _ := h.call(t, "POST", "/v1/sync/events", map[string]any{
		"batch_id": uuid.New(), "device_id": h.activeDev, "events": []offline.Incoming{},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an empty batch answered %d", resp.StatusCode)
	}
}

// --- the pull ---

func TestThePullIsScopedToWhatTheCallerMayRead(t *testing.T) {
	h := newAPI(t)
	base := h.clock.Now().Add(-time.Hour)
	h.push(t, []offline.Incoming{h.vital(base, 120), h.vital(base.Add(time.Minute), 124)}, nil)

	resp, body := h.call(t, "GET", "/v1/sync/events?since=0", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the pull answered %d: %v", resp.StatusCode, body)
	}
	events, _ := body["events"].([]any)
	if len(events) < 2 {
		t.Fatalf("%d events came back", len(events))
	}
	// The cursor is present even on a page that ends, so a client always has somewhere to ask
	// from next.
	if body["cursor"] == nil || body["latest"] == nil {
		t.Fatalf("the page carries no cursor: %v", body)
	}

	// A caller with no reading permission at all receives nothing rather than everything.
	h.permissions = []string{"observation.write.vitals"}
	resp, _ = h.call(t, "GET", "/v1/sync/events?since=0", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a caller with no read permission answered %d, want 403", resp.StatusCode)
	}
}

// The allowlist is the security property. A type nobody decided about must not be pullable, and
// the build must fail rather than the default being whatever the query happens to do.
func TestEveryEventTypeIsDecidedAbout(t *testing.T) {
	pull := offline.Pullable()
	no := offline.NotPullable()
	for _, name := range eventstore.Default.Names() {
		_, isPullable := pull[name]
		_, isNot := no[name]
		if !isPullable && !isNot {
			t.Fatalf("%s is registered and neither pullable nor listed as not pullable; "+
				"decide, in internal/offline/pullable.go, rather than letting the default happen", name)
		}
		if isPullable && isNot {
			t.Fatalf("%s is in both lists", name)
		}
	}
	for name := range pull {
		if _, known := eventstore.Default.Lookup(name, 1); !known {
			t.Fatalf("%s is pullable and not a registered event type", name)
		}
	}
	for name := range no {
		if _, known := eventstore.Default.Lookup(name, 1); !known {
			t.Fatalf("%s is listed as not pullable and not a registered event type", name)
		}
	}
	// And every permission named is one that exists — a typo here fails closed, silently, which
	// is the worst way for an allowlist to be wrong.
	for name, permission := range pull {
		if !strings.Contains(permission, ".") {
			t.Fatalf("%s names %q, which is not a permission code", name, permission)
		}
	}
}

// --- reference data and device state ---

func TestTheReferenceFingerprintMovesWhenACatalogueDoes(t *testing.T) {
	h := newAPI(t)
	resp, before := h.call(t, "GET", "/v1/sync/reference", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the reference answered %d: %v", resp.StatusCode, before)
	}
	fingerprint := func(body map[string]any, catalogue string) string {
		list, _ := body["catalogues"].([]any)
		for _, raw := range list {
			row, _ := raw.(map[string]any)
			if row["catalogue"] == catalogue {
				return row["fingerprint"].(string)
			}
		}
		return ""
	}
	was := fingerprint(before, "exercise")
	if was == "" {
		t.Fatalf("no exercise catalogue in the response: %v", before)
	}

	if _, err := h.SQL.Exec(`
		INSERT INTO core.exercise (code, name_en, name_bn, how_en, how_bn, kind, intensity, impact, ordering)
		VALUES ('TAI_CHI', 'Tai chi', 'তাই চি', 'Follow the slow movements.', 'ধীর নড়াচড়া অনুসরণ করুন।',
		        'BALANCE', 'LOW', 'NONE', 130)`); err != nil {
		t.Fatal(err)
	}
	_, after := h.call(t, "GET", "/v1/sync/reference", nil)
	if fingerprint(after, "exercise") == was {
		t.Fatal("the fingerprint did not move when the catalogue did")
	}
	// And an untouched catalogue did not move, or the fingerprint would be useless — a client
	// would re-fetch everything whenever anything changed.
	if fingerprint(after, "food") != fingerprint(before, "food") {
		t.Fatal("an untouched catalogue's fingerprint moved")
	}
}

func TestTheDeviceStateAnswersHasThatTabletSyncedToday(t *testing.T) {
	h := newAPI(t)
	resp, before := h.call(t, "GET", "/v1/sync/state", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the state answered %d: %v", resp.StatusCode, before)
	}
	// A device that has never synced is not an error. It is every device's first morning.
	state, _ := before["state"].(map[string]any)
	if state["last_pushed_at"] != nil {
		t.Fatalf("a fresh device reports a push: %v", state)
	}

	h.push(t, []offline.Incoming{h.vital(h.clock.Now().Add(-time.Hour), 122)}, nil)
	_, after := h.call(t, "GET", "/v1/sync/state", nil)
	state, _ = after["state"].(map[string]any)
	if state["last_pushed_at"] == nil {
		t.Fatalf("the state does not record the push: %v", state)
	}
	if pushed, _ := state["pushed_total"].(float64); pushed != 1 {
		t.Fatalf("pushed_total is %v, want 1", pushed)
	}
}

// A device that is not enrolled here answers not-found, with one answer for "does not exist" and
// "belongs to another clinic" — a distinguishing error would disclose which.
//
// Reached by way of the *verified* device now, since the body's is only checked against it: this
// is the case where a session's device was deleted between sign-in and the push.
func TestADeviceThisFacilityDoesNotHaveIsNotFound(t *testing.T) {
	h := newAPI(t)
	stranger := uuid.New()
	h.activeDev = stranger

	resp, _ := h.call(t, "POST", "/v1/sync/events", map[string]any{
		"batch_id": uuid.New(),
		"events":   []offline.Incoming{h.vital(h.clock.Now().Add(-time.Hour), 120)},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown device answered %d, want 404", resp.StatusCode)
	}
}

func TestReadingTheQuarantineNeedsItsOwnPermission(t *testing.T) {
	h := newAPI(t)
	// The station's own write permissions do not reach it: the quarantine holds whole envelopes,
	// and reading it is the only way to see a clinical value from outside the ledger.
	resp, _ := h.call(t, "GET", "/v1/sync/quarantine", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a station operator could read the quarantine: %d", resp.StatusCode)
	}
}

func TestReleasingNeedsMoreThanReading(t *testing.T) {
	h := newAPI(t)
	h.permissions = []string{"sync.quarantine.read"}
	resp, _ := h.call(t, "POST", "/v1/sync/quarantine/"+uuid.New().String()+"/release",
		map[string]any{"note": "x"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a reader could release: %d", resp.StatusCode)
	}
}

// --- the four protocol defects the client review found ---

// The quarantine's whole reason for existing is a device revoked *while it was offline*, and that
// scenario was unreachable: the device middleware refused a non-active device before any handler
// ran, so the push met a 401 indistinguishable from an expired token — and a client following
// §13.8's "wipe on revocation" would then destroy exactly the data the quarantine preserves.
//
// This test is the middleware chain, not the service: it asserts that a correctly-signed request
// from a revoked device *reaches* the sync push, and only there.
func TestARevokedDeviceReachesTheSyncPushAndNothingElse(t *testing.T) {
	h := newAPI(t)

	// Revoked in the database — this is the scenario, not a stub of it — and the verifier reports
	// "signed correctly, no longer active" rather than refusing.
	if _, err := h.SQL.Exec(`
		UPDATE core.device SET status = 'revoked', status_reason = 'reported stolen from station 4'
		 WHERE id = $1`, h.device); err != nil {
		t.Fatal(err)
	}
	verifier := &fakeVerifier{device: h.device, facility: h.facility, active: false}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := offline.NewHandlers(offline.HandlersConfig{
		Service: offline.NewService(h.store, h.events, h.clock),
		Store:   h.store, Clock: h.clock, Logger: logger,
	})
	who := staff{h: h}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 22, RequestTimeout: 30 * time.Second,
		Health:         &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator:  who,
		Authorizer:     who,
		DeviceVerifier: verifier,
		// The one exemption, exactly as the API wires it.
		QuarantineRoutes: map[string]bool{"POST /v1/sync/events": true},
		Routes:           func(r chi.Router) { handlers.Mount(r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	send := func(method, path string, body any) int {
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = strings.NewReader(string(raw))
		}
		req, err := http.NewRequest(method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test")
		req.Header.Set("X-Requested-With", "DTHCMS")
		req.Header.Set("X-Active-Role", h.role)
		// Enough to make the middleware ask the verifier; the fake answers without checking.
		req.Header.Set("X-Device-Id", h.device.String())
		req.Header.Set("X-Device-Timestamp", fmt.Sprint(h.clock.Now().Unix()))
		req.Header.Set("X-Device-Nonce", uuid.New().String())
		req.Header.Set("X-Device-Signature", strings.Repeat("A", 86)+"==")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp.StatusCode
	}

	// The push gets through, and quarantines.
	if got := send("POST", "/v1/sync/events", map[string]any{
		"batch_id": uuid.New(),
		"events":   []offline.Incoming{h.vital(h.clock.Now().Add(-2*time.Hour), 146)},
	}); got != http.StatusOK {
		t.Fatalf("a revoked device's push answered %d; the quarantine is unreachable", got)
	}
	var held int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ops.sync_quarantine WHERE reason_code = 'DEVICE_REVOKED'`).
		Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 1 {
		t.Fatalf("%d events were held, want 1", held)
	}

	// And nothing else. A revoked device may hand over what it has; it may not read a patient,
	// pull events, or ask for reference data.
	for _, path := range []string{"/v1/sync/events?since=0", "/v1/sync/reference", "/v1/sync/state"} {
		if got := send("GET", path, nil); got != http.StatusUnauthorized {
			t.Fatalf("a revoked device reached %s: %d", path, got)
		}
	}
}

// fakeVerifier stands in for the signature check, which is CP18's and is tested there. What this
// suite needs is the *status* half: a device that signed correctly and is no longer active.
type fakeVerifier struct {
	device   uuid.UUID
	facility uuid.UUID
	active   bool
}

func (f *fakeVerifier) VerifyDevice(context.Context, httpx.DeviceProof) (httpx.DeviceIdentity, error) {
	return httpx.DeviceIdentity{
		DeviceID: f.device.String(), FacilityID: f.facility.String(),
		Name: "Tablet 4", KeyID: uuid.New().String(), Active: f.active,
	}, nil
}

// The body used to decide which device a batch came from. Any caller holding a station write
// permission could therefore attribute a batch — and its quarantine rows, and another tablet's
// sync state — to a device that did not send it.
func TestTheSendingDeviceIsTheOneThatSignedNotTheOneInTheBody(t *testing.T) {
	h := newAPI(t)
	resp, body := h.call(t, "POST", "/v1/sync/events", map[string]any{
		"batch_id": uuid.New(),
		// A different enrolled device at the same facility: the old code would have believed it.
		"device_id": h.other,
		"events":    []offline.Incoming{h.vital(h.clock.Now().Add(-time.Hour), 120)},
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a batch naming another device answered %d: %v", resp.StatusCode, body)
	}
	var attributed int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ops.sync_batch WHERE device_id = $1`, h.other).Scan(&attributed); err != nil {
		t.Fatal(err)
	}
	if attributed != 0 {
		t.Fatal("a batch was attributed to a device that did not send it")
	}

	// Omitting it entirely is fine: the signature already said which device this is.
	resp, body = h.call(t, "POST", "/v1/sync/events", map[string]any{
		"batch_id": uuid.New(),
		"events":   []offline.Incoming{h.vital(h.clock.Now().Add(-time.Hour), 121)},
	})
	if resp.StatusCode != http.StatusOK || counts(body, "accepted") != 1 {
		t.Fatalf("a batch with no device_id answered %d: %v", resp.StatusCode, body)
	}
}

// The batch row is opened before the first event, so a server interrupted halfway leaves a receipt
// that exists and reports nothing. Answering from it would tell a client "your fifty events
// produced no results" for ever, while docs/sync.md promises the opposite.
func TestAnInterruptedBatchIsReprocessedRatherThanAnsweredAsEmpty(t *testing.T) {
	h := newAPI(t)
	batch := uuid.New()
	events := []offline.Incoming{
		h.vital(h.clock.Now().Add(-time.Hour), 118),
		h.vital(h.clock.Now().Add(-59*time.Minute), 124),
	}

	// The state a crash leaves: opened, nothing closed.
	if _, err := h.SQL.Exec(`
		INSERT INTO ops.sync_batch (id, device_id, user_id, facility_id, received_at)
		VALUES ($1, $2, $3, $4, $5::timestamptz)`,
		batch, h.device, h.operator, h.facility, h.clock.Now()); err != nil {
		t.Fatal(err)
	}

	resp, body := h.call(t, "POST", "/v1/sync/events", map[string]any{
		"batch_id": batch, "events": events,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resending an interrupted batch answered %d: %v", resp.StatusCode, body)
	}
	if counts(body, "accepted") != 2 {
		t.Fatalf("an interrupted batch was answered as empty: %v", body)
	}

	// And now it is closed, so a third ask is a replay rather than a third run.
	resp, receipt := h.call(t, "GET", "/v1/sync/batches/"+batch.String(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the receipt answered %d", resp.StatusCode)
	}
	if closed, _ := receipt["closed"].(bool); !closed {
		t.Fatalf("the receipt is not closed: %v", receipt)
	}
	if counts(receipt, "accepted") != 2 {
		t.Fatalf("the closed receipt says %d accepted", counts(receipt, "accepted"))
	}
}

// The receipt exists for the client that lost the response, and that client needs the cursor as
// much as the outcome — without it, it pulls its own writes back down.
func TestAReplayedReceiptStillCarriesWhereEachEventLanded(t *testing.T) {
	h := newAPI(t)
	_, first, batch := h.push(t, []offline.Incoming{
		h.vital(h.clock.Now().Add(-time.Hour), 130),
	}, nil)
	results, _ := first["results"].([]any)
	one, _ := results[0].(map[string]any)
	landed, _ := one["global_seq"].(float64)
	if landed == 0 {
		t.Fatalf("the live receipt carries no cursor: %v", one)
	}

	_, replay := h.call(t, "GET", "/v1/sync/batches/"+batch.String(), nil)
	results, _ = replay["results"].([]any)
	again, _ := results[0].(map[string]any)
	if got, _ := again["global_seq"].(float64); got != landed {
		t.Fatalf("the replayed receipt says %v, the live one said %v", got, landed)
	}
}

// --- the ceiling on what one device may leave behind (migration 00050) ---
//
// CP65 opened `POST /v1/sync/events` to a device the clinic has **refused**, so that a tablet
// revoked while it was offline can hand over what it is holding instead of destroying it. What
// arrives that way lands in `ops.sync_quarantine`, and `dthcms_app` has no DELETE there.
//
// Those two facts together are the hole. An honest client stops when its backlog is delivered; a
// stolen tablet with a live key has no reason to, and every row it writes is permanent and is one
// a supervisor has to read. These tests are about the ceiling that closes it, and about the two
// things the ceiling must not break: a held event is never turned into a lost one, and a *working*
// tablet is never locked out of recording clinical values.

// capAt lowers the per-device cap for this test's database.
//
// The cap lives in exactly one place — `ops.quarantine_cap()` — so that the trigger that enforces
// it and the service that explains it cannot disagree. Redefining that one function is therefore
// the whole of what a test needs to do, which is itself the design being checked: if the number
// were duplicated in Go, this helper would move the database's copy and prove nothing.
func (h *api) capAt(t *testing.T, n int) {
	t.Helper()
	if _, err := h.SQL.Exec(fmt.Sprintf(
		`CREATE OR REPLACE FUNCTION ops.quarantine_cap() RETURNS integer
		 LANGUAGE sql IMMUTABLE AS $$ SELECT %d $$`, n)); err != nil {
		t.Fatal(err)
	}
}

func (h *api) revoke(t *testing.T, reason string) {
	t.Helper()
	if _, err := h.SQL.Exec(`
		UPDATE core.device SET status = 'revoked', status_reason = $2 WHERE id = $1`,
		h.activeDev, reason); err != nil {
		t.Fatal(err)
	}
}

func (h *api) heldCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ops.sync_quarantine WHERE device_id = $1 AND status = 'HELD'`,
		h.activeDev).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The event is not lost. That is the whole point of the outcome chosen at the ceiling.
func TestAtTheCeilingEventsAreBlockedForResendingRatherThanRejected(t *testing.T) {
	h := newAPI(t)
	h.capAt(t, 3)
	h.revoke(t, "reported stolen from station 4")
	base := h.clock.Now().Add(-2 * time.Hour)

	events := make([]offline.Incoming, 0, 5)
	for i := 0; i < 5; i++ {
		events = append(events, h.vital(base.Add(time.Duration(i)*time.Minute), float64(120+i)))
	}

	resp, body, _ := h.push(t, events, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the push answered %d: %v", resp.StatusCode, body)
	}
	if got := counts(body, "quarantined"); got != 3 {
		t.Fatalf("%d events were held, want exactly the cap of 3", got)
	}
	if got := counts(body, "blocked"); got != 2 {
		t.Fatalf("%d events were blocked, want 2", got)
	}
	if got := counts(body, "rejected"); got != 0 {
		t.Fatalf("%d events were rejected: a conforming client drops a REJECTED event, so "+
			"rejecting here would lose a real blood pressure because a supervisor is behind on "+
			"their reading — the silent loss the quarantine exists to prevent, by a new door", got)
	}

	// And the reason says what would clear it, so a client can tell this apart from its own bug.
	var found bool
	for _, raw := range body["results"].([]any) {
		r := raw.(map[string]any)
		if r["outcome"] == "BLOCKED" {
			found = true
			if r["reason_code"] != "QUARANTINE_FULL" {
				t.Fatalf("reason_code = %v, want QUARANTINE_FULL", r["reason_code"])
			}
		}
	}
	if !found {
		t.Fatal("no blocked result carried a reason")
	}
	if got := h.heldCount(t); got != 3 {
		t.Fatalf("the quarantine holds %d rows for this device, want 3", got)
	}
}

// A device that has spent its allowance and is refused gets nothing at all — not even a receipt
// row. Otherwise the flood simply moves into `ops.sync_batch` and `ops.sync_result`.
func TestARefusedDeviceAtItsCeilingIsTurnedAwayWholeWithoutWritingAnything(t *testing.T) {
	h := newAPI(t)
	h.capAt(t, 2)
	h.revoke(t, "reported stolen from station 4")
	base := h.clock.Now().Add(-2 * time.Hour)

	// Fill the allowance.
	if resp, body, _ := h.push(t, []offline.Incoming{
		h.vital(base, 120), h.vital(base.Add(time.Minute), 122),
	}, nil); resp.StatusCode != http.StatusOK || counts(body, "quarantined") != 2 {
		t.Fatalf("filling the allowance: %d %v", resp.StatusCode, body)
	}

	var batchesBefore, resultsBefore int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ops.sync_batch`).Scan(&batchesBefore); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ops.sync_result`).Scan(&resultsBefore); err != nil {
		t.Fatal(err)
	}

	resp, body, _ := h.push(t, []offline.Incoming{h.vital(base.Add(2*time.Minute), 124)}, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the push answered %d, want 429: %v", resp.StatusCode, body)
	}
	if code := errorCode(body); code != "SYNC_QUARANTINE_FULL" {
		t.Fatalf("error code = %q, want SYNC_QUARANTINE_FULL", code)
	}

	var batchesAfter, resultsAfter int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ops.sync_batch`).Scan(&batchesAfter); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(`SELECT count(*) FROM ops.sync_result`).Scan(&resultsAfter); err != nil {
		t.Fatal(err)
	}
	if batchesAfter != batchesBefore || resultsAfter != resultsBefore {
		t.Fatalf("a turned-away batch still wrote %d batch rows and %d result rows: the flood "+
			"just moved into the receipt tables", batchesAfter-batchesBefore, resultsAfter-resultsBefore)
	}
	if got := h.heldCount(t); got != 2 {
		t.Fatalf("the quarantine holds %d rows, want 2", got)
	}
}

// The asymmetry that keeps the ceiling from being a denial of service against the clinic itself.
func TestAWorkingDeviceAtItsCeilingStillRecordsClinicalValues(t *testing.T) {
	h := newAPI(t)
	h.capAt(t, 1)
	base := h.clock.Now().Add(-2 * time.Hour)

	// This device is *active*. Its holds come from an event type this server does not know —
	// a newer app during a rolling deploy, which is an ordinary state and not misbehaviour.
	future := h.vital(base, 130)
	future.EventType = "SOMETHING_THE_SERVER_HAS_NOT_LEARNED_YET"
	second := h.vital(base.Add(time.Minute), 132)
	second.EventType = "SOMETHING_THE_SERVER_HAS_NOT_LEARNED_YET"

	events := []offline.Incoming{
		h.vital(base.Add(2*time.Minute), 118),
		future,
		h.vital(base.Add(3*time.Minute), 121),
		second,
		h.vital(base.Add(4*time.Minute), 119),
	}

	resp, body, _ := h.push(t, events, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the push answered %d: %v", resp.StatusCode, body)
	}
	if got := counts(body, "accepted"); got != 3 {
		t.Fatalf("%d ordinary blood pressures were accepted, want 3. A rolling deploy must not "+
			"lock a working tablet out of recording clinical values", got)
	}
	if got := counts(body, "quarantined"); got != 1 {
		t.Fatalf("%d events were held, want 1 (the cap)", got)
	}
	if got := counts(body, "blocked"); got != 1 {
		t.Fatalf("%d events were blocked, want 1", got)
	}
}

// Triage refills the allowance, which is the point of counting HELD rather than every row.
func TestWorkingThroughTheTriageListGivesTheAllowanceBack(t *testing.T) {
	h := newAPI(t)
	h.capAt(t, 1)
	h.revoke(t, "the operator left the clinic")
	base := h.clock.Now().Add(-2 * time.Hour)

	if _, body, _ := h.push(t, []offline.Incoming{h.vital(base, 120)}, nil); counts(body, "quarantined") != 1 {
		t.Fatalf("the first event was not held: %v", body)
	}
	// Refused *and* at the ceiling, so the whole batch is turned away rather than answered
	// per event — see TestARefusedDeviceAtItsCeilingIsTurnedAwayWholeWithoutWritingAnything.
	if resp, body, _ := h.push(t, []offline.Incoming{h.vital(base.Add(time.Minute), 121)}, nil); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the second push answered %d, want 429: %v", resp.StatusCode, body)
	}

	// A supervisor decides about the held one. Discarded rather than released so that this test
	// says nothing about the release path, which has its own.
	var held uuid.UUID
	if err := h.SQL.QueryRow(
		`SELECT id FROM ops.sync_quarantine WHERE device_id = $1 AND status = 'HELD'`,
		h.activeDev).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		UPDATE ops.sync_quarantine
		   SET status = 'DISCARDED', resolved_at = now(), resolved_by = $2,
		       resolution_note = 'the device was stolen; these are not trustworthy'
		 WHERE id = $1`, held, h.physician); err != nil {
		t.Fatal(err)
	}

	_, body, _ := h.push(t, []offline.Incoming{h.vital(base.Add(2*time.Minute), 122)}, nil)
	if counts(body, "quarantined") != 1 {
		t.Fatalf("the allowance did not come back after triage: %v body. The cap counts events "+
			"awaiting review because what it protects is somebody's attention, not disk", body)
	}
}

// The trigger, on its own, without the service in front of it. This is the half that is still
// there when Redis is down and the rate limiter is failing open — and the half that would still
// be there if a future code path forgot to ask.
func TestTheDatabaseRefusesToHoldMoreThanTheCapEvenWithoutTheService(t *testing.T) {
	h := newAPI(t)
	h.capAt(t, 1)
	h.revoke(t, "stolen")
	base := h.clock.Now().Add(-2 * time.Hour)

	if _, body, _ := h.push(t, []offline.Incoming{h.vital(base, 120)}, nil); counts(body, "quarantined") != 1 {
		t.Fatalf("the first event was not held: %v", body)
	}

	// A direct insert, bypassing every line of Go in this package.
	var batch uuid.UUID
	if err := h.SQL.QueryRow(`SELECT id FROM ops.sync_batch LIMIT 1`).Scan(&batch); err != nil {
		t.Fatal(err)
	}
	_, err := h.SQL.Exec(`
		INSERT INTO ops.sync_quarantine
		  (id, batch_id, event_id, device_id, user_id, facility_id, reason_code, reason,
		   envelope, event_type, occurred_at, held_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'DEVICE_REVOKED', 'straight past the service',
		        '{}'::jsonb, 'OBSERVATION_RECORDED', now(), now())`,
		uuid.New(), batch, uuid.New(), h.activeDev, h.operator, h.facility)
	if err == nil {
		t.Fatal("the database accepted a row past the cap: the ceiling is a promise one code " +
			"path keeps rather than a fact about the table, and the rate limiter it backs up " +
			"fails open by design")
	}
	if !strings.Contains(err.Error(), "awaiting review") {
		t.Fatalf("refused, but not by the cap: %v", err)
	}
}

// A second device is not charged for the first one's backlog.
func TestTheCeilingIsPerDeviceRatherThanPerClinic(t *testing.T) {
	h := newAPI(t)
	h.capAt(t, 1)
	h.revoke(t, "stolen")
	base := h.clock.Now().Add(-2 * time.Hour)

	if _, body, _ := h.push(t, []offline.Incoming{h.vital(base, 120)}, nil); counts(body, "quarantined") != 1 {
		t.Fatalf("the first device's event was not held: %v", body)
	}

	// The second tablet, also revoked, has its own allowance: one stolen device must not stop the
	// other eleven stations from handing over the work they are holding.
	h.activeDev = h.other
	if _, err := h.SQL.Exec(`
		UPDATE core.device SET status = 'revoked', status_reason = 'also stolen' WHERE id = $1`,
		h.other); err != nil {
		t.Fatal(err)
	}
	_, body, _ := h.push(t, []offline.Incoming{h.vital(base.Add(time.Minute), 121)}, nil)
	if counts(body, "quarantined") != 1 {
		t.Fatalf("a second device was refused because the first had filled its own allowance: %v", body)
	}
}

// errorCode reads the code out of the envelope every refusal is written in.
func errorCode(body map[string]any) string {
	envelope, _ := body["error"].(map[string]any)
	code, _ := envelope["code"].(string)
	return code
}
