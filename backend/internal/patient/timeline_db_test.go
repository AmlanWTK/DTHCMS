package patient_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The patient timeline (CP37, §8).
//
// Four acceptance criteria, and the two that matter most are the ones a screenshot cannot
// show: that a fact appears exactly once however many times its event is delivered, and that
// a rebuild from the ledger produces the same table.

func (h *api) timeline(t *testing.T, patientID, query string) map[string]any {
	t.Helper()
	path := "/v1/patients/" + patientID + "/timeline"
	if query != "" {
		path += "?" + query
	}
	resp, body := h.call(t, http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("timeline returned %d: %v", resp.StatusCode, body)
	}
	return body
}

func entries(body map[string]any) []map[string]any {
	raw, _ := body["entries"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(map[string]any))
	}
	return out
}

func TestEveryClinicalFactAppearsExactlyOnce(t *testing.T) {
	// Acceptance criterion 1.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	// A correction of two fields is two rows, and a photograph is one. "The record was
	// corrected" is not what somebody scrolls a timeline looking for; "the date of birth was
	// corrected" is.
	if resp, body := h.correct(t, id, map[string]any{
		"birth_date": "1985-06-14", "postcode": "7801",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("correcting: %d %v", resp.StatusCode, body)
	}

	rows := entries(h.timeline(t, id, ""))
	if len(rows) != 3 {
		for _, row := range rows {
			t.Logf("%s %s %s", row["category"], row["kind"], row["label_en"])
		}
		t.Fatalf("got %d timeline rows, want registration + two corrections", len(rows))
	}

	seen := map[string]int{}
	for _, row := range rows {
		seen[row["kind"].(string)+"/"+fmt.Sprint(row["item"])]++
	}
	for key, count := range seen {
		if count != 1 {
			t.Errorf("%s appears %d times", key, count)
		}
	}

	// Newest first, and the registration is last.
	if rows[len(rows)-1]["kind"] != "patient.registered" {
		t.Errorf("the oldest row is %v", rows[len(rows)-1]["kind"])
	}
}

func TestARedeliveredEventDoesNotDoubleAnEntry(t *testing.T) {
	// The same criterion, from the direction that actually happens: a tablet that sent an
	// event, lost the reply and sent it again.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	eventID := uuid.Must(uuid.NewV7()).String()
	for range 2 {
		if resp, body := h.correct(t, id, map[string]any{
			"event_id": eventID, "postcode": "7801",
		}); resp.StatusCode != http.StatusOK {
			t.Fatalf("correcting: %d %v", resp.StatusCode, body)
		}
	}

	var rows int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM read.patient_timeline WHERE kind = 'patient.corrected'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("a retried correction produced %d timeline rows", rows)
	}
}

func TestEveryRowCarriesAttribution(t *testing.T) {
	// Acceptance criterion 3, and §8's hover-to-see-who. Attribution resolved by a join is
	// attribution that disappears when the person who recorded it has left.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)
	if resp, _ := h.correct(t, id, map[string]any{"postcode": "7801"}); resp.StatusCode != http.StatusOK {
		t.Fatal("correcting failed")
	}

	for _, row := range entries(h.timeline(t, id, "")) {
		if row["actor_code"] != "R001" {
			t.Errorf("%v has actor_code %v", row["kind"], row["actor_code"])
		}
		if row["actor_role"] != "REGISTRATION" {
			t.Errorf("%v has actor_role %v", row["kind"], row["actor_role"])
		}
		if row["label_en"] == "" || row["label_bn"] == "" {
			t.Errorf("%v has no label in one of the two languages: %v", row["kind"], row)
		}
	}

	// And the invariant says so too, so a future row type that forgets is caught by the
	// migration suite rather than by somebody hovering.
	if _, err := h.SQL.Exec(`SELECT core.assert_timeline_rows_are_attributed()`); err != nil {
		t.Fatalf("the attribution invariant does not hold: %v", err)
	}
}

func TestTheTimelineIsFilteredByTypeAndRange(t *testing.T) {
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)
	if resp, _ := h.correct(t, id, map[string]any{"postcode": "7801"}); resp.StatusCode != http.StatusOK {
		t.Fatal("correcting failed")
	}

	only := entries(h.timeline(t, id, "types=administrative"))
	if len(only) != 1 || only[0]["category"] != "administrative" {
		t.Errorf("filtering by category returned %v", only)
	}

	// An unknown category is refused rather than ignored: silently returning everything is
	// how a "medication only" screen shows a diagnosis to somebody who filtered it out.
	resp, _ := h.call(t, http.MethodGet, "/v1/patients/"+id+"/timeline?types=prescriptions", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("an unknown category returned %d", resp.StatusCode)
	}

	// A window that contains nothing still reports the whole span, so a screen can tell
	// "nothing this month" from "nothing at all".
	empty := h.timeline(t, id, "from=2020-01-01&to=2020-12-31")
	if len(entries(empty)) != 0 {
		t.Errorf("a window in 2020 returned %d rows", len(entries(empty)))
	}
	if empty["earliest"] == nil {
		t.Error("an empty window reported no span; a screen cannot tell it from an empty record")
	}

	// A date-only `to` means the whole of that day.
	today := h.clock.Now().In(patient.Dhaka).Format("2006-01-02")
	whole := h.timeline(t, id, "from="+today+"&to="+today)
	if len(entries(whole)) != 2 {
		t.Errorf("asking for today returned %d rows; the last day was dropped", len(entries(whole)))
	}
}

func TestARowTheCallerMayNotSeeIsNotCountedEither(t *testing.T) {
	// The reason the permission filter is in SQL. A post-filter is how a total comes back
	// larger than the rows returned, and how paging skips what it hid — the second is worse,
	// because the user sees a short page and has no way to know why.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)
	patientID := uuid.MustParse(id)

	if _, err := h.SQL.Exec(
		`UPDATE read.patient_timeline SET needs_permission = 'clinical.read.notes'
		  WHERE patient_id = $1`, patientID); err != nil {
		t.Fatal(err)
	}

	body := h.timeline(t, id, "")
	if len(entries(body)) != 0 {
		t.Errorf("a row needing a permission the caller lacks was returned")
	}
	if body["total"] != float64(0) {
		t.Errorf("total = %v; a hidden row was still counted", body["total"])
	}

	// And with the permission, it comes back.
	page, err := h.store.Timeline(context.Background(), patientID, h.facility, patient.TimelineQuery{
		Permissions: []string{"clinical.read.notes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total == 0 {
		t.Error("a caller holding the permission still saw nothing")
	}
}

func TestARebuildReproducesTheTimelineIdentically(t *testing.T) {
	// Acceptance criterion 4. The read model is derived, and "derived" means throwing it
	// away and replaying the ledger must produce the same thing — otherwise a rebuild after
	// an incident silently changes a decade of somebody's history.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)
	if resp, _ := h.correct(t, id, map[string]any{
		"birth_date": "1985-06-14", "postcode": "7801",
	}); resp.StatusCode != http.StatusOK {
		t.Fatal("correcting failed")
	}

	before := h.timelineFingerprint(t)
	if len(before) == 0 {
		t.Fatal("nothing to rebuild")
	}

	// Replay: empty the table and apply every event through the same derivation, in order.
	if _, err := h.SQL.Exec(`SELECT read.reset_patient_timeline()`); err != nil {
		t.Fatal(err)
	}
	h.replayTimeline(t)

	after := h.timelineFingerprint(t)
	if len(after) != len(before) {
		t.Fatalf("rebuild produced %d rows, was %d", len(after), len(before))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("row %d differs:\n before %s\n  after %s", i, before[i], after[i])
		}
	}
}

// timelineFingerprint is every row's content, ordered, as text. Everything except the
// generated id and the timestamps the database writes.
func (h *api) timelineFingerprint(t *testing.T) []string {
	t.Helper()
	rows, err := h.SQL.Query(`
		SELECT patient_id || '|' || occurred_at || '|' || category || '|' || kind || '|' ||
		       label_en || '|' || label_bn || '|' || value || '|' || unit || '|' ||
		       coalesce(actor_code, '') || '|' || coalesce(actor_role, '') || '|' ||
		       array_to_string(flags, ',') || '|' || event_id || '|' || item
		  FROM read.patient_timeline
		 ORDER BY global_seq, item`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// replayTimeline runs every event in the ledger through the timeline projection, in order,
// exactly as `projector rebuild` does.
func (h *api) replayTimeline(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	events, err := h.events.FromGlobal(ctx, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	timeline := projection.PatientTimeline{}
	for _, e := range events {
		if !timeline.Handles(e.EventType) {
			continue
		}
		if err := timeline.Apply(ctx, tx, e); err != nil {
			t.Fatalf("replaying %s: %v", e.EventType, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// --- performance ---

// TestTheTimelineIsFastEnoughForATenYearPatient is acceptance criterion 2: p95 under 300 ms
// for a decade of history.
//
// Skipped by default because seeding takes time, and a suite a developer stops running is a
// suite that stops finding things. `-short=false` runs it, as CI does nightly.
func TestTheTimelineIsFastEnoughForATenYearPatient(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test: run with -short=false")
	}
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := uuid.MustParse(created["id"].(string))

	// Ten years of a diabetic patient at this clinic: quarterly visits, and at each one
	// roughly a dozen observations, a prescription and a counselling note. Forty rows a
	// quarter is a deliberately pessimistic reading of §2's station list.
	const rows = 10 * 4 * 40
	start := time.Now()
	h.seedTimeline(t, id, rows)
	t.Logf("seeded %d timeline rows in %s", rows, time.Since(start).Round(time.Millisecond))

	// And the rest of the clinic's history in the same table, because a query that is fast
	// against eight thousand rows says nothing about one running against a real register.
	// Three hundred patients with a decade each is roughly half a million rows — a few years
	// of DTHC at the caseload §2 describes.
	const neighbours = 300
	start = time.Now()
	for range neighbours {
		h.seedTimeline(t, uuid.New(), rows)
	}
	var total int64
	if err := h.SQL.QueryRow(`SELECT count(*) FROM read.patient_timeline`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	// ANALYZE, deliberately: the planner has never seen this table and an autovacuum has not
	// run. Measuring against stale statistics measures the wrong thing in both directions.
	if _, err := h.SQL.Exec(`ANALYZE read.patient_timeline`); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d rows across %d patients in %s", total, neighbours+1,
		time.Since(start).Round(time.Second))

	queries := []struct {
		name  string
		query patient.TimelineQuery
	}{
		{"whole history", patient.TimelineQuery{Limit: 100}},
		{"last year", patient.TimelineQuery{
			From: time.Now().AddDate(-1, 0, 0), Limit: 100,
		}},
		{"observations only", patient.TimelineQuery{
			Categories: []string{"observation"}, Limit: 100,
		}},
		{"deep page", patient.TimelineQuery{Limit: 100, Offset: 1000}},
	}

	var samples []time.Duration
	worst := map[string]time.Duration{}
	for range 12 {
		for _, tc := range queries {
			query := tc.query
			query.Permissions = []string{"patient.read.demographics", "clinical.read.notes"}
			began := time.Now()
			if _, err := h.store.Timeline(context.Background(), id, h.facility, query); err != nil {
				t.Fatal(err)
			}
			took := time.Since(began)
			samples = append(samples, took)
			if took > worst[tc.name] {
				worst[tc.name] = took
			}
		}
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50 := samples[len(samples)/2]
	p95 := samples[(len(samples)*95)/100]
	for name, took := range worst {
		t.Logf("worst %-18s %s", name, took.Round(time.Microsecond))
	}
	t.Logf("timeline: p50 %s, p95 %s over %d queries", p50.Round(time.Microsecond),
		p95.Round(time.Microsecond), len(samples))

	if p95 > 300*time.Millisecond {
		t.Errorf("p95 is %s, over the 300 ms budget", p95.Round(time.Millisecond))
	}
}

// seedTimeline writes rows directly, which is the point: the projection is tested elsewhere
// and what is being measured here is the query.
func (h *api) seedTimeline(t *testing.T, patientID uuid.UUID, count int) {
	t.Helper()
	// Everything as text, cast column by column: pgx will not encode time.Time into a text
	// VALUES parameter, and building one statement per row would dominate the seed time.
	const batch = 500
	for start := 0; start < count; start += batch {
		end := min(start+batch, count)
		values := make([]any, 0, (end-start)*6)
		sqlText := `INSERT INTO read.patient_timeline
			(patient_id, facility_id, occurred_at, recorded_at, category, kind,
			 label_en, label_bn, value, actor_id, actor_code, actor_role, actor_station,
			 source, flags, event_id, event_type, global_seq, item, needs_permission)
			VALUES `
		for i := start; i < end; i++ {
			when := time.Now().AddDate(0, 0, -i*9).UTC().Format(time.RFC3339)
			category := []string{"observation", "medication", "diagnosis", "visit"}[i%4]
			values = append(values, patientID.String(), h.facility.String(), when,
				category, uuid.New().String(), fmt.Sprintf("%d", i))
			n := len(values)
			sqlText += fmt.Sprintf(
				`($%d::uuid, $%d::uuid, $%d::timestamptz, $%d::timestamptz, $%d, `+
					`'measurement', 'Weight', 'ওজন', '78.4', %s, 'R001', 'NURSE', 'ANTHRO', `+
					`'mobile_online', '{}', $%d::uuid, 'WEIGHT_RECORDED', $%d::bigint, '', `+
					`'patient.read.demographics'),`,
				n-5, n-4, n-3, n-3, n-2, "'"+h.user.String()+"'::uuid", n-1, n)
		}
		sqlText = sqlText[:len(sqlText)-1]
		if _, err := h.SQL.Exec(sqlText, values...); err != nil {
			t.Fatal(err)
		}
	}
}
