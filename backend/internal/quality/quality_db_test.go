package quality_test

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

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/quality"
)

// The operator quality record (CP63, §4.3).
//
// # Why this harness seeds rows rather than driving the correction workflow
//
// The four acceptance criteria are about *counting and scoping*, and the scenarios that make them
// interesting — three transcription errors spread across four weeks, a correction on a value
// recorded at half past five in the evening, an operator with forty entries beside one with four
// hundred — cannot be produced by writing observations through an HTTP API against a fixed clock
// without inventing a way to move time, which would be a test of the clock.
//
// So the corrections are seeded, and the end-to-end path (a correction answered → the detector
// asked to look) is proven in `clinical` by `TestAnsweringACorrectionAsksForAQualityReview`. The
// two together cover the checkpoint; either alone would not.

type harness struct {
	*testsupport.DB

	facility  uuid.UUID
	operator  uuid.UUID
	patient   uuid.UUID
	physician uuid.UUID

	clock *clock.Fixed
	store *quality.Store

	// The caller the next request is made as.
	user        uuid.UUID
	role        string
	permissions []string

	server *httptest.Server
}

// staff is the authenticator and authorizer. It reads through the harness pointer rather than
// copying, because half of these tests are a conversation between two people — an operator's own
// record and the supervisor's view of it, on the same server.
type staff struct{ h *harness }

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.h.user.String(), FacilityID: s.h.facility.String(),
		SessionID: uuid.NewSHA1(s.h.user, []byte("session")).String(),
		Code:      "S001", Permissions: s.h.permissions,
		Roles: []string{s.h.role}, ActiveRole: s.h.role,
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code, Role: s.h.role,
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func newHarness(t *testing.T, permissions ...string) *harness {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &harness{DB: base, operator: uuid.New(), physician: uuid.New(), role: "ANTHROPOMETRY"}
	// A Monday **morning in Faridpur**: 04:00 UTC is 10:00 in Asia/Dhaka. The hour matters to
	// this harness, because everything it seeds inherits it — a clock set to the afternoon would
	// make every seeded correction an end-of-shift one, and the end-of-shift test would pass
	// while proving nothing. That test sets its own hours explicitly.
	h.clock = clock.NewFixed(time.Date(2026, 9, 14, 4, 0, 0, 0, time.UTC))
	h.permissions = permissions
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	h.user = h.operator
	h.store = quality.NewStore(pool)
	h.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := quality.NewHandlers(quality.HandlersConfig{
		Store: h.store, Clock: h.clock, Logger: logger,
	})
	who := staff{h: h}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 16, RequestTimeout: 10 * time.Second,
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

func (h *harness) seed(t *testing.T) {
	t.Helper()
	for _, who := range []struct {
		id           uuid.UUID
		code, en, bn string
		status       string
	}{
		{h.operator, "A014", "Shirin Akter", "শিরীন আক্তার", "active"},
		{h.physician, "P014", "Dr Farhana Islam", "ডা. ফারহানা ইসলাম", "active"},
	} {
		if _, err := h.SQL.Exec(`
			INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			who.id, h.facility, who.code, who.en, who.bn, who.status); err != nil {
			t.Fatal(err)
		}
	}
	h.patient = uuid.New()
	if _, err := h.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000301', 'Md Rahim Uddin', 'male', DATE '1985-06-14',
		        'day', 'national_id', '+8801711111301', 'active', $3, now())`,
		h.patient, h.facility, h.operator); err != nil {
		t.Fatal(err)
	}
}

// becomes switches who the next request is made as.
func (h *harness) becomes(user uuid.UUID, role string, permissions ...string) {
	h.user = user
	h.role = role
	h.permissions = permissions
}

var seq int64

// observation seeds one recorded value. `at` is when it was recorded, and the hour matters: the
// end-of-shift pattern reads the hour the value was **taken**, not the hour it was flagged.
func (h *harness) observation(t *testing.T, by uuid.UUID, code string, at time.Time,
	category string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	seq++
	// The unit has to be the code's own: a trigger from CP42 refuses a mass recorded in
	// centimetres, and it is right to. A seed that worked around it would be seeding rows the
	// application could never have written.
	unit, value := "cm", 150.0
	if code == "BODY_WEIGHT" {
		unit, value = "kg", 62.0
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO read.observation
		  (id, facility_id, patient_id, code, category, value_type, value_num, unit,
		   entered_num, entered_unit,
		   effective_at, recorded_at, source, status, recorded_by, recorded_role,
		   event_id, global_seq)
		VALUES ($1, $2, $3, $4, $5, 'numeric', $10, $11, $10, $11, $6, $6, 'STATION', 'ACTIVE',
		        $7, 'ANTHROPOMETRY', $8, $9)`,
		id, h.facility, h.patient, code, category, at, by, uuid.New(), seq,
		value, unit); err != nil {
		t.Fatal(err)
	}
	return id
}

// entries seeds n values so a denominator exists. Without one the thresholds refuse to fire,
// which is deliberate — and a test that forgot it would look like a broken detector.
func (h *harness) entries(t *testing.T, by uuid.UUID, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		h.observation(t, by, "BODY_WEIGHT", h.clock.Now().Add(-time.Duration(i)*time.Hour), "ANTHRO")
	}
}

// correction seeds one correction request against a value recorded at `recordedAt`.
func (h *harness) correction(t *testing.T, by uuid.UUID, code, reason string,
	recordedAt, requestedAt time.Time, status string) uuid.UUID {
	t.Helper()
	observation := h.observation(t, by, code, recordedAt, "ANTHRO")
	id := uuid.New()
	seq++
	var resolvedAt any
	var resolvedBy any
	note := ""
	if status != "OPEN" {
		resolvedAt = requestedAt.Add(time.Minute)
		resolvedBy = by
		note = "corrected"
	}
	replacement := any(nil)
	if status == "APPLIED" || status == "OVERRIDDEN" {
		replacement = h.observation(t, by, code, recordedAt, "ANTHRO")
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO read.correction_request
		  (id, facility_id, patient_id, observation_id, code, requested_at, requested_by,
		   requested_role, reason_code, note, assigned_to, status, resolved_at, resolved_by,
		   resolution_note, replacement_id, event_id, global_seq)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'PHYSICIAN', $8, 'the tape was against the wall',
		        $9, $10, $11, $12, $13, $14, $15, $16)`,
		id, h.facility, h.patient, observation, code, requestedAt, h.physician,
		reason, by, status, resolvedAt, resolvedBy, note, replacement,
		uuid.New(), seq); err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *harness) call(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
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
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

func (h *harness) detector() *quality.Detector {
	return quality.NewDetector(h.store, h.clock, nil, nil)
}

// --- criterion 1: the counting ---

func TestARateIsNeverReturnedWithoutItsDenominator(t *testing.T) {
	// The plan's stated risk, as a test. Three corrections against four hundred entries and
	// against forty are different facts, and only one of them is a problem; a payload carrying
	// the numerator alone is the number that makes staff hide their mistakes.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 0; i < 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i-1), h.clock.Now().AddDate(0, 0, -i-1), "APPLIED")
	}

	resp, body := h.call(t, "GET", "/v1/quality/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an operator's own record answered %d: %v", resp.StatusCode, body)
	}
	record, _ := body["record"].(map[string]any)
	if record["entries"] == nil {
		t.Fatal("the record reported corrections with no denominator")
	}
	// 43 values recorded (40 plus the three that were corrected, plus their replacements).
	if entries := record["entries"].(float64); entries < 40 {
		t.Fatalf("the denominator counted %v entries, want at least the 40 seeded", entries)
	}
	if got := record["corrections"].(float64); got != 3 {
		t.Fatalf("the record counted %v corrections, want 3", got)
	}
	if record["rate"] == nil {
		t.Fatal("a rate was withheld from a record with more than twenty entries")
	}
}

func TestTooFewEntriesMeansNoRateAtAll(t *testing.T) {
	// A rate computed from four entries is noise, and rendering noise as a number invites
	// somebody to act on it. Null, not zero, and not a number.
	h := newHarness(t)
	h.entries(t, h.operator, 4)
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -1), h.clock.Now().AddDate(0, 0, -1), "APPLIED")

	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)
	if record["rate"] != nil {
		t.Fatalf("a rate of %v was computed from %v entries", record["rate"], record["entries"])
	}
}

func TestARejectedRequestIsNotCountedAsAnError(t *testing.T) {
	// An operator who defends a correct reading is doing the job. A metric that punished it
	// would teach everyone to accept every flag without looking, which is the opposite of what
	// this whole mechanism is for.
	h := newHarness(t)
	h.entries(t, h.operator, 30)
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -1), h.clock.Now().AddDate(0, 0, -1), "APPLIED")
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -2), h.clock.Now().AddDate(0, 0, -2), "REJECTED")

	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)
	if got := record["upheld"].(float64); got != 1 {
		t.Fatalf("%v corrections were upheld, want 1", got)
	}
	if got := record["rejected"].(float64); got != 1 {
		t.Fatalf("%v were rejected, want 1", got)
	}
	// The rate is upheld over entries, so the rejected one must not move it.
	rate := record["rate"].(float64)
	if rate <= 0 || rate > 5 {
		t.Fatalf("the rate reads %v, which is not one upheld correction in thirty-odd entries", rate)
	}
}

func TestTheCategoriesAddUp(t *testing.T) {
	// Criterion 1's "by category": the breakdown must be the same set of rows as the total.
	h := newHarness(t)
	h.entries(t, h.operator, 30)
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -1), h.clock.Now().AddDate(0, 0, -1), "APPLIED")
	h.correction(t, h.operator, "BODY_WEIGHT", "WRONG_UNIT",
		h.clock.Now().AddDate(0, 0, -2), h.clock.Now().AddDate(0, 0, -2), "APPLIED")
	h.correction(t, h.operator, "BODY_WEIGHT", "WRONG_UNIT",
		h.clock.Now().AddDate(0, 0, -3), h.clock.Now().AddDate(0, 0, -3), "OVERRIDDEN")

	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)
	total := int(record["corrections"].(float64))

	byReason := record["by_reason"].([]any)
	sum := 0
	for _, raw := range byReason {
		row := raw.(map[string]any)
		sum += int(row["corrections"].(float64))
		if row["display_en"] == "" || row["display_bn"] == "" {
			t.Fatalf("a reason rendered in only one language: %v", row)
		}
	}
	if sum != total {
		t.Fatalf("the reason breakdown sums to %d against a total of %d", sum, total)
	}

	sum = 0
	for _, raw := range record["by_code"].([]any) {
		sum += int(raw.(map[string]any)["corrections"].(float64))
	}
	if sum != total {
		t.Fatalf("the code breakdown sums to %d against a total of %d", sum, total)
	}
}

func TestAValueOutsideTheWindowIsNotCounted(t *testing.T) {
	h := newHarness(t)
	h.entries(t, h.operator, 30)
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -60), h.clock.Now().AddDate(0, 0, -60), "APPLIED")

	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)
	if got := record["corrections"].(float64); got != 0 {
		t.Fatalf("a correction from sixty days ago was counted in a thirty-day window (%v)", got)
	}

	// And it is there when the window is asked to reach it.
	_, wider := h.call(t, "GET", "/v1/quality/me?days=90", nil)
	record, _ = wider["record"].(map[string]any)
	if got := record["corrections"].(float64); got != 1 {
		t.Fatalf("a ninety-day window found %v of the one correction sixty days ago", got)
	}
}

// --- criterion 2: the patterns ---

func TestThreeTranscriptionErrorsInThirtyDaysRaisesAFlag(t *testing.T) {
	// The plan's own manual verification, automated: *"generate three transcription errors for
	// one operator within 30 days and confirm the retraining flag appears on the supervisor view
	// with the supporting detail."*
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i*5), h.clock.Now().AddDate(0, 0, -i*5), "APPLIED")
	}

	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) != 1 {
		t.Fatalf("%d flags were raised, want the one transcription pattern", len(raised))
	}
	flag := raised[0]
	if flag.ThresholdCode != "TRANSCRIPTION_3_IN_30" {
		t.Fatalf("the flag names %q", flag.ThresholdCode)
	}
	if flag.ObservedCount != 3 {
		t.Fatalf("the flag counted %d", flag.ObservedCount)
	}
	// The supporting detail the manual verification asks for.
	if len(flag.Evidence) != 3 {
		t.Fatalf("the flag shows %d supporting corrections, want 3", len(flag.Evidence))
	}
	// And the denominator, so the supervisor is not reading a numerator alone.
	if flag.EntriesCount < 40 {
		t.Fatalf("the flag froze a denominator of %d", flag.EntriesCount)
	}
	// The numbers behind it are a proposal until somebody approves them, and the flag says so.
	if flag.ThresholdApproved {
		t.Fatal("a threshold nobody has approved was reported as approved")
	}
	if flag.ActionEN == "" || flag.ActionBN == "" {
		t.Fatal("the flag suggests nothing to do; a flag with no suggested action is a complaint")
	}
}

func TestOneSetOfCorrectionsRaisesOneFlag(t *testing.T) {
	// Three mistyped heights are both "repeated transcription errors" and "the same measurement
	// going wrong", and both are true. Raising both hands a supervisor two flags about one
	// conversation, and a supervisor asked to have three conversations about three corrections
	// will have none of them.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) != 1 {
		codes := make([]string, 0, len(raised))
		for _, flag := range raised {
			codes = append(codes, flag.ThresholdCode)
		}
		t.Fatalf("one set of corrections raised %v", codes)
	}
	// And the one it raises is the more actionable reading — "she is mistyping" — with the
	// instrument question left as the second thing to check rather than a second conversation.
	if raised[0].ThresholdCode != "TRANSCRIPTION_3_IN_30" {
		t.Fatalf("the flag raised was %s", raised[0].ThresholdCode)
	}
}

func TestTwoDistinctPatternsBothRaise(t *testing.T) {
	// The deduplication above must not swallow a second, genuinely different pattern. Three
	// mistyped heights and three misread weights are two conversations, not one.
	h := newHarness(t)
	h.entries(t, h.operator, 60)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
		h.correction(t, h.operator, "BODY_WEIGHT", "MISREAD_INSTRUMENT",
			h.clock.Now().AddDate(0, 0, -i-10), h.clock.Now().AddDate(0, 0, -i-10), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]bool{}
	for _, flag := range raised {
		byCode[flag.ThresholdCode] = true
	}
	if !byCode["TRANSCRIPTION_3_IN_30"] || !byCode["SAME_CODE_3_IN_30"] {
		t.Fatalf("two distinct patterns raised %v", byCode)
	}
}

func TestTwoErrorsAreNotAPattern(t *testing.T) {
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 2; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) != 0 {
		t.Fatalf("%d flags were raised on two corrections", len(raised))
	}
}

func TestANewOperatorIsNotFlaggedOnTheirFirstMorning(t *testing.T) {
	// Three corrections out of five entries. The arithmetic says 60%; the truth says somebody
	// started this morning. A system that flags them for retraining teaches a clinic's staff to
	// stop asking for help.
	h := newHarness(t)
	h.entries(t, h.operator, 2)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) != 0 {
		t.Fatalf("a new operator was flagged after five entries: %v", raised)
	}
}

func TestThreeDifferentMeasurementsAreNotOneMeasurementGoingWrong(t *testing.T) {
	// SAME_CODE is a question about an instrument. Three different measurements corrected once
	// each is a busy month, and conflating the two would send somebody to check a scale that is
	// working perfectly.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i, code := range []string{"BODY_HEIGHT", "BODY_WEIGHT", "WAIST_CIRC"} {
		h.correction(t, h.operator, code, "MISREAD_INSTRUMENT",
			h.clock.Now().AddDate(0, 0, -i-1), h.clock.Now().AddDate(0, 0, -i-1), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range raised {
		if flag.ThresholdCode == "SAME_CODE_3_IN_30" {
			t.Fatalf("three different measurements were read as one going wrong: %v", flag.Evidence)
		}
	}
}

func TestTheSameMeasurementGoingWrongThreeTimesIsAPattern(t *testing.T) {
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_WEIGHT", "MISREAD_INSTRUMENT",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, flag := range raised {
		if flag.ThresholdCode != "SAME_CODE_3_IN_30" {
			continue
		}
		found = true
		for _, item := range flag.Evidence {
			if item.Code != "BODY_WEIGHT" {
				t.Fatalf("the flag's evidence includes %s, which is not the measurement it is about", item.Code)
			}
		}
	}
	if !found {
		t.Fatalf("the same measurement corrected three times raised no flag: %v", raised)
	}
}

func TestEndOfShiftIsTheHourTheValueWasTakenNotTheHourItWasFlagged(t *testing.T) {
	// A physician reviewing yesterday's file at nine in the morning would otherwise make every
	// operator look like a morning problem.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	dhaka, err := time.LoadLocation(quality.Zone)
	if err != nil {
		t.Skipf("no tzdata in this environment: %v", err)
	}
	// Three *different* measurements, so that the only shape these share is the hour. Three of
	// the same one would be a SAME_CODE pattern too, and the detector — rightly — raises that
	// one conversation rather than two.
	for i, code := range []string{"BODY_HEIGHT", "BODY_WEIGHT", "WAIST_CIRC"} {
		// Recorded at half past five in the evening, Faridpur time; flagged the next morning.
		day := h.clock.Now().In(dhaka).AddDate(0, 0, -i-1)
		recorded := time.Date(day.Year(), day.Month(), day.Day(), 17, 30, 0, 0, dhaka)
		h.correction(t, h.operator, code, "MISREAD_INSTRUMENT",
			recorded.UTC(), recorded.Add(15*time.Hour).UTC(), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, flag := range raised {
		if flag.ThresholdCode == "END_OF_SHIFT_3_IN_14" {
			found = true
			for _, item := range flag.Evidence {
				if item.Hour < 16 {
					t.Fatalf("a value recorded at %02d:00 was counted as end-of-shift", item.Hour)
				}
			}
		}
	}
	if !found {
		t.Fatalf("three late-afternoon corrections raised no end-of-shift flag: %v", raised)
	}
}

func TestAPatternIsFlaggedOnceWhileNobodyHasActedOnIt(t *testing.T) {
	// A second row would mean a supervisor acknowledging the same conversation twice. The
	// database refuses it; the detector avoids asking.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 4; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	detector := h.detector()
	first, err := detector.Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("nothing was flagged")
	}
	second, err := detector.Review(context.Background(), h.facility, h.operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("the same pattern was flagged twice: %v", second)
	}
}

// --- criteria 3 and 4: who reads whose ---

func TestAnOperatorSeesTheirOwnRecordWithNoPermissionAtAll(t *testing.T) {
	// Criterion 3. And the reason it needs no permission: an operator who has to be granted
	// something before they may see their own error count will assume the count is being kept
	// from them.
	h := newHarness(t) // no permissions at all
	h.entries(t, h.operator, 30)
	resp, body := h.call(t, "GET", "/v1/quality/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an operator's own record answered %d: %v", resp.StatusCode, body)
	}
	record, _ := body["record"].(map[string]any)
	if record["operator_id"] != h.operator.String() {
		t.Fatalf("the record is about %v, not the caller", record["operator_id"])
	}
}

func TestNoOperatorReadsAnothersRecord(t *testing.T) {
	// Criterion 4.
	h := newHarness(t, "observation.read.values", "observation.write.anthro")
	resp, body := h.call(t, "GET", "/v1/quality/operators/"+h.physician.String(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reading a colleague's record answered %d, want 403: %v", resp.StatusCode, body)
	}
	if resp, _ := h.call(t, "GET", "/v1/quality/operators", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the team list answered %d to somebody without the permission", resp.StatusCode)
	}
	if resp, _ := h.call(t, "GET", "/v1/quality/flags", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the flag list answered %d to somebody without the permission", resp.StatusCode)
	}
}

func TestASupervisorReadsTheTeamAndEveryLineCarriesItsDenominator(t *testing.T) {
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 2; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	h.becomes(h.physician, "PHYSICIAN", "quality.read.team")

	resp, body := h.call(t, "GET", "/v1/quality/operators", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the supervisor's list answered %d: %v", resp.StatusCode, body)
	}
	operators, _ := body["operators"].([]any)
	if len(operators) != 1 {
		t.Fatalf("the list has %d lines, want the one operator with corrections", len(operators))
	}
	line := operators[0].(map[string]any)
	if line["entries"] == nil {
		t.Fatal("a line ranked by corrections carries no denominator")
	}
	if line["name_en"] == "" || line["name_bn"] == "" {
		t.Fatalf("the line names nobody: %v", line)
	}
}

func TestAFlagShowsItsWorkingAndNamesNoPatient(t *testing.T) {
	// The invariant, from the other side. A supervisor reading a patient's values through their
	// staff's error history would be reading clinical data through a side door.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil || len(raised) == 0 {
		t.Fatalf("nothing was flagged: %v %v", raised, err)
	}
	h.becomes(h.physician, "PHYSICIAN", "quality.read.team")

	resp, body := h.call(t, "GET", "/v1/quality/flags/"+raised[0].ID.String(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reading a flag answered %d: %v", resp.StatusCode, body)
	}
	raw, err := json.Marshal(body["flag"])
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(raw)
	if strings.Contains(rendered, h.patient.String()) {
		t.Fatalf("a quality flag names a patient:\n%s", rendered)
	}
	// The evidence is the corrections, and it says what a supervisor needs before speaking to
	// anybody: what was being measured, why it was wrong, and when.
	flag, _ := body["flag"].(map[string]any)
	evidence, _ := flag["evidence"].([]any)
	if len(evidence) != 3 {
		t.Fatalf("the flag shows %d corrections", len(evidence))
	}
	for _, item := range evidence {
		row := item.(map[string]any)
		for _, field := range []string{"request_id", "reason_code", "code", "at"} {
			if row[field] == nil || row[field] == "" {
				t.Fatalf("the evidence is missing %s: %v", field, row)
			}
		}
	}

	// And the database agrees, which is the version that survives a future writer.
	if _, err := h.SQL.Exec(`SELECT core.assert_no_quality_record_names_a_patient()`); err != nil {
		t.Fatalf("the invariant refuses the row this system wrote: %v", err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_every_quality_flag_shows_its_working()`); err != nil {
		t.Fatalf("the invariant refuses the row this system wrote: %v", err)
	}
}

func TestTheInvariantNoticesAPatientSmuggledIntoAQualityFlag(t *testing.T) {
	// A canary. An invariant that cannot fail is worse than none, because it reads as protection.
	h := newHarness(t)
	if _, err := h.SQL.Exec(`
		INSERT INTO core.quality_flag
		  (facility_id, operator_id, threshold_code, window_from, window_to,
		   observed_count, entries_count, evidence)
		VALUES ($1, $2, 'TRANSCRIPTION_3_IN_30', now() - interval '30 days', now(), 3, 40,
		        $3::jsonb)`,
		h.facility, h.operator,
		fmt.Sprintf(`[{"request_id":"%s","patient_id":"%s"}]`, uuid.New(), h.patient),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`SELECT core.assert_no_quality_record_names_a_patient()`); err == nil {
		t.Fatal("a patient id in a staff quality record passed the invariant")
	}
}

// --- answering a flag ---

func TestADismissalMustSayWhy(t *testing.T) {
	// The same rule a rejected correction follows, for the same reason: "no" with no reason is
	// how a flagging culture dies.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	raised, _ := h.detector().Review(context.Background(), h.facility, h.operator)
	if len(raised) == 0 {
		t.Fatal("nothing was flagged")
	}
	h.becomes(h.physician, "PHYSICIAN", "quality.read.team", "quality.flag.resolve")

	resp, body := h.call(t, "POST", "/v1/quality/flags/"+raised[0].ID.String()+"/resolve",
		map[string]any{"status": "DISMISSED"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a silent dismissal answered %d, want 422: %v", resp.StatusCode, body)
	}

	resp, answered := h.call(t, "POST", "/v1/quality/flags/"+raised[0].ID.String()+"/resolve",
		map[string]any{"status": "DISMISSED", "resolution": "New scale; the old one read 2kg light."})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a dismissal with a reason answered %d: %v", resp.StatusCode, answered)
	}
	flag, _ := answered["flag"].(map[string]any)
	if flag["status"] != "DISMISSED" {
		t.Fatalf("the flag reads %v", flag["status"])
	}

	// And it is answered once.
	resp, _ = h.call(t, "POST", "/v1/quality/flags/"+raised[0].ID.String()+"/resolve",
		map[string]any{"status": "ACKNOWLEDGED"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second answer returned %d, want 409", resp.StatusCode)
	}
}

// --- what the review round found, as tests ---

func TestTheListAndTheRecordAgreeAboutOnePerson(t *testing.T) {
	// The list used to compute corrections-over-entries and the record upheld-over-entries, so a
	// supervisor comparing a line against the record they opened in the next click saw two
	// different numbers for one person and one window — and would rightly conclude a screen was
	// broken.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -1), h.clock.Now().AddDate(0, 0, -1), "APPLIED")
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -2), h.clock.Now().AddDate(0, 0, -2), "REJECTED")

	_, mine := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := mine["record"].(map[string]any)

	h.becomes(h.physician, "PHYSICIAN", "quality.read.team")
	_, list := h.call(t, "GET", "/v1/quality/operators", nil)
	var line map[string]any
	for _, raw := range list["operators"].([]any) {
		row := raw.(map[string]any)
		if row["operator_id"] == h.operator.String() {
			line = row
		}
	}
	if line == nil {
		t.Fatal("the operator is not on the supervisor's list")
	}
	if line["rate"] != record["rate"] {
		t.Fatalf("the list says %v and the record says %v about the same person and window",
			line["rate"], record["rate"])
	}
	for _, field := range []string{"corrections", "upheld", "rejected", "entries"} {
		if line[field] != record[field] {
			t.Fatalf("%s reads %v on the list and %v on the record", field, line[field], record[field])
		}
	}
}

func TestASupervisorsFixIsNotCountedAsTheOperatorsOwn(t *testing.T) {
	// CP62 made a supervisor's fix a different event precisely so an operator's record would not
	// read it as their own. Folding them together here would throw that away at the one place it
	// was created for.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -1), h.clock.Now().AddDate(0, 0, -1), "APPLIED")
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -2), h.clock.Now().AddDate(0, 0, -2), "OVERRIDDEN")

	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)
	if got := record["upheld"].(float64); got != 1 {
		t.Fatalf("the operator is credited with %v of their own fixes, want 1", got)
	}
	if got := record["overridden"].(float64); got != 1 {
		t.Fatalf("%v corrections were recorded as somebody else's fix, want 1", got)
	}
}

func TestEveryBreakdownCarriesItsOwnDenominator(t *testing.T) {
	// "Three corrections at four in the afternoon" answers nothing on its own: an operator who
	// works only the late shift will always cluster late. Against "how many values did you enter
	// at four in the afternoon" it becomes a question somebody can answer.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -1), h.clock.Now().AddDate(0, 0, -1), "APPLIED")

	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)

	for _, raw := range record["by_code"].([]any) {
		row := raw.(map[string]any)
		if row["entries"] == nil {
			t.Fatalf("a measurement breakdown carries no denominator: %v", row)
		}
		if row["display_en"] == "" || row["display_bn"] == "" {
			t.Fatalf("a measurement rendered as a bare code: %v", row)
		}
	}
	for _, raw := range record["by_hour"].([]any) {
		row := raw.(map[string]any)
		if row["entries"] == nil {
			t.Fatalf("an hour breakdown carries no denominator: %v", row)
		}
		if row["entries"].(float64) < row["corrections"].(float64) {
			t.Fatalf("an hour reports more corrections than entries: %v", row)
		}
	}
	// And the floor is on the payload, so a screen can say how many more are needed.
	if record["rate_floor"] == nil {
		t.Fatal("the rate floor is not reported, so a screen can only say \"too few\"")
	}
}

func TestTheRateIsAlwaysAKeyEvenWhenItIsNull(t *testing.T) {
	// The contract says this field is null below the floor. It used to be `omitempty`, so a
	// client checking `rate === null` was reading a field the encoder had dropped.
	h := newHarness(t)
	h.entries(t, h.operator, 3)
	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)
	if _, present := record["rate"]; !present {
		t.Fatalf("the rate key is absent rather than null: %v", record)
	}
	if record["rate"] != nil {
		t.Fatalf("a rate of %v was computed from three entries", record["rate"])
	}
}

func TestTheSupervisorsListIsARosterByDefault(t *testing.T) {
	// A list every row of which has at least one correction is structurally an accusation,
	// whatever it is titled. The rows that make it a roster are the people with a large
	// denominator and nothing corrected.
	h := newHarness(t)
	h.entries(t, h.operator, 10)
	h.entries(t, h.physician, 40) // nothing corrected
	h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
		h.clock.Now().AddDate(0, 0, -1), h.clock.Now().AddDate(0, 0, -1), "APPLIED")

	h.becomes(h.physician, "PHYSICIAN", "quality.read.team")
	_, roster := h.call(t, "GET", "/v1/quality/operators", nil)
	if got := len(roster["operators"].([]any)); got != 2 {
		t.Fatalf("the roster has %d rows, want everybody who recorded anything", got)
	}

	_, narrowed := h.call(t, "GET", "/v1/quality/operators?only_corrected=1", nil)
	if got := len(narrowed["operators"].([]any)); got != 1 {
		t.Fatalf("the narrowed list has %d rows, want the one operator with a correction", got)
	}
}

func TestAWindowNobodyCanUseIsRefusedRatherThanReplaced(t *testing.T) {
	// A client asking for fourteen days and being given thirty has two windows pretending to be
	// one, and every count on the screen is then a numerator over somebody else's denominator.
	h := newHarness(t)
	for _, days := range []string{"0", "-5", "nonsense", "4000"} {
		resp, body := h.call(t, "GET", "/v1/quality/me?days="+days, nil)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("days=%s answered %d, want 422: %v", days, resp.StatusCode, body)
		}
	}
	if resp, _ := h.call(t, "GET", "/v1/quality/me?days=14", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("a usable window answered %d", resp.StatusCode)
	}
}

func TestAnAnsweredFlagDoesNotVanishFromTheRecordItIsAbout(t *testing.T) {
	// The ambush from the other end: a note appears on somebody's device, a supervisor closes it,
	// and it silently disappears with no resolution and no reason.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -i), h.clock.Now().AddDate(0, 0, -i), "APPLIED")
	}
	raised, _ := h.detector().Review(context.Background(), h.facility, h.operator)
	if len(raised) == 0 {
		t.Fatal("nothing was flagged")
	}

	h.becomes(h.physician, "PHYSICIAN", "quality.read.team", "quality.flag.resolve")
	if resp, body := h.call(t, "POST", "/v1/quality/flags/"+raised[0].ID.String()+"/resolve",
		map[string]any{"status": "ACKNOWLEDGED",
			"resolution": "Sat with her on Tuesday."}); resp.StatusCode != http.StatusOK {
		t.Fatalf("acknowledging answered %d: %v", resp.StatusCode, body)
	}

	h.becomes(h.operator, "ANTHROPOMETRY")
	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)
	flags, _ := record["flags"].([]any)
	if len(flags) != 1 {
		t.Fatalf("the answered flag vanished from the record it is about (%d flags)", len(flags))
	}
	flag := flags[0].(map[string]any)
	if flag["status"] != "ACKNOWLEDGED" {
		t.Fatalf("the flag reads %v", flag["status"])
	}
	if flag["resolution"] == "" || flag["resolved_by_name_en"] == "" {
		t.Fatalf("the operator cannot see who decided what, or why: %v", flag)
	}
}

func TestAnOpenFlagIsNeverOutsideAWindow(t *testing.T) {
	// Three bugs were one bug: the queue filtered on the raise date, the record did not filter at
	// all, and the roster's open count was all-time. At seven days a supervisor saw a flag on
	// somebody's record that was missing from the queue on the same screen.
	//
	// "What is still waiting" is not a question about a date range.
	h := newHarness(t)
	h.entries(t, h.operator, 40)
	for i := 1; i <= 3; i++ {
		h.correction(t, h.operator, "BODY_HEIGHT", "TRANSCRIPTION",
			h.clock.Now().AddDate(0, 0, -20-i), h.clock.Now().AddDate(0, 0, -20-i), "APPLIED")
	}
	raised, err := h.detector().Review(context.Background(), h.facility, h.operator)
	if err != nil || len(raised) == 0 {
		t.Fatalf("nothing was flagged: %v %v", raised, err)
	}

	h.becomes(h.physician, "PHYSICIAN", "quality.read.team")

	// A seven-day window. The flag was raised today (the detector stamps `now`), so shorten the
	// test by asking for a window that cannot contain the corrections behind it either.
	_, queue := h.call(t, "GET", "/v1/quality/flags?days=1", nil)
	if len(queue["flags"].([]any)) != 1 {
		t.Fatalf("an unanswered flag fell out of a one-day window: %v", queue["flags"])
	}
	_, roster := h.call(t, "GET", "/v1/quality/operators?days=1", nil)
	for _, raw := range roster["operators"].([]any) {
		row := raw.(map[string]any)
		if row["operator_id"] != h.operator.String() {
			continue
		}
		if row["open_flags"].(float64) != 1 {
			t.Fatalf("the roster says %v waiting where the queue shows one", row["open_flags"])
		}
	}
	_, record := h.call(t, "GET", "/v1/quality/operators/"+h.operator.String()+"?days=1", nil)
	flags := record["record"].(map[string]any)["flags"].([]any)
	if len(flags) != 1 {
		t.Fatalf("the record shows %d flags where the queue shows one", len(flags))
	}
}

func TestARecordSaysHowLongAnAnsweredFlagStays(t *testing.T) {
	// The same shape of problem as the rate floor: a screen that cannot name the number can only
	// say "it will disappear eventually".
	h := newHarness(t)
	_, body := h.call(t, "GET", "/v1/quality/me", nil)
	record, _ := body["record"].(map[string]any)
	if got := record["answered_flags_kept_days"]; got == nil || got.(float64) <= 0 {
		t.Fatalf("the record does not say how long an answered flag stays: %v", got)
	}
}

func TestEveryThresholdIsAProposalUntilSomebodyApprovesIt(t *testing.T) {
	// The plan lists the numbers as an open decision requiring approval. Rows rather than
	// constants, and the API reports the absence rather than hiding it.
	h := newHarness(t)
	resp, body := h.call(t, "GET", "/v1/quality/thresholds", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the thresholds answered %d: %v", resp.StatusCode, body)
	}
	thresholds, _ := body["thresholds"].([]any)
	if len(thresholds) != 3 {
		t.Fatalf("%d thresholds, want §4.3's three shapes", len(thresholds))
	}
	for _, raw := range thresholds {
		row := raw.(map[string]any)
		threshold := row["threshold"].(map[string]any)
		if threshold["approved"] != false {
			t.Fatalf("%v ships approved", threshold["code"])
		}
		// Both languages. This is the screen an operator reads to understand the rule that
		// measures them; an English-only sentence in front of a Bangla reader is not a choice.
		for _, field := range []string{"looks_for_en", "looks_for_bn"} {
			if row[field] == "" {
				t.Fatalf("%v says nothing about what it looks for in %s", threshold["code"], field)
			}
		}
		if row["looks_for_en"] == row["looks_for_bn"] {
			t.Fatalf("%v renders the same sentence in both languages", threshold["code"])
		}
		for _, field := range []string{"display_en", "display_bn", "action_en", "action_bn"} {
			if threshold[field] == "" {
				t.Fatalf("%v is missing %s", threshold["code"], field)
			}
		}
	}
}
