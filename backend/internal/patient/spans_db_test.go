package patient_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
)

// The scrubbable timeline's API (CP74, §8).
//
// # What these tests are for, and what they are deliberately not
//
// The screen's acceptance criteria are about a picture — smooth interaction, a correlation
// that is visually apparent — and no Go test can see a picture. What *can* be proven here is
// everything the picture is built out of, and every one of these is a place where a
// plausible-looking wrong answer would reach a physician:
//
//   - a medication that ran for two years is **one bar**, not forty dots. That is what makes
//     the correlation legible at all, and it is the checkpoint's whole reason for a second
//     endpoint;
//   - a bar nobody closed is **open-ended**, and does not end at its last refill;
//   - a caller without `observation.read.values` gets **no points at all**, and is told so
//     rather than shown an empty chart;
//   - attribution is on **every** mark and **every** point, because a tooltip that cannot say
//     who recorded a value is the criterion silently not holding.
//
// The tests write timeline rows directly. That is not laziness: this module has no way to
// create a medication — CP81 owns prescriptions and the projection that will put them on the
// timeline is another checkpoint's — and the derivation being tested here is the **fold**,
// which is a function of the row shape `docs/timeline.md` documents and is stable.

// seriesFixture is the observation read model as the handler tests need it: it always has
// points, so that a test asserting the points are absent is asserting about the permission
// rather than about an empty database.
var seriesFixture = &fakeSeries{}

type fakeSeries struct{}

func (f *fakeSeries) Series(_ context.Context, _, _ uuid.UUID, codes []string,
	from, to time.Time, _ int) (map[string][]patient.SeriesPoint, map[string]string, error) {

	points := map[string][]patient.SeriesPoint{}
	units := map[string]string{}
	for _, code := range codes {
		if code != "HBA1C" {
			continue
		}
		at := time.Date(2022, 3, 1, 9, 5, 0, 0, time.UTC)
		if at.Before(from) || !at.Before(to) {
			continue
		}
		units[code] = "%"
		points[code] = []patient.SeriesPoint{{
			ObservationID: uuid.MustParse("0190a8f2-0000-7000-8000-0000000000f1"),
			At:            at,
			ValueNum:      9.4,
			Unit:          "%",
			Flags:         []string{},
			SpanAttribution: patient.SpanAttribution{
				ActorID:      uuid.MustParse("0190a8f2-0000-7000-8000-0000000000e1"),
				ActorRole:    "LAB_TECH",
				ActorStation: "STN_LAB",
				Source:       "STATION",
				RecordedAt:   at.Add(20 * time.Minute),
			},
		}}
	}
	return points, units, nil
}

// --- the harness ---

func (h *api) spans(t *testing.T, patientID, query string) map[string]any {
	t.Helper()
	path := "/v1/patients/" + patientID + "/timeline/spans"
	if query != "" {
		path += "?" + query
	}
	resp, body := h.call(t, http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spans returned %d: %v", resp.StatusCode, body)
	}
	return body
}

// timelineRow writes one row of the projection's own shape.
//
// `read.apply_timeline` is the derivation's own entry point, so a row written here is a row
// written the way the projection writes one — including the invariant that refuses a row
// with no actor and no label, which is what would otherwise let this test build a fixture
// the real system cannot produce.
func (h *api) timelineRow(t *testing.T, patientID string, occurred time.Time,
	category, kind, labelEN, labelBN string) {
	t.Helper()

	if _, err := h.SQL.Exec(`SELECT read.apply_timeline($1::jsonb)`, fmt.Sprintf(`[{
		"patient_id": %q, "facility_id": %q,
		"occurred_at": %q, "recorded_at": %q,
		"category": %q, "kind": %q, "label_en": %q, "label_bn": %q,
		"actor_id": %q, "actor_role": "PHYSICIAN", "actor_station": "STN_CONSULT",
		"source": "STATION", "flags": [],
		"event_id": %q, "event_type": "TEST_EVENT", "global_seq": %d,
		"needs_permission": "patient.read.demographics"
	}]`,
		patientID, h.facility,
		occurred.Format(time.RFC3339Nano), occurred.Format(time.RFC3339Nano),
		category, kind, labelEN, labelBN,
		h.user,
		uuid.Must(uuid.NewV7()).String(), time.Now().UnixNano(),
	)); err != nil {
		t.Fatal(err)
	}
}

func lanes(body map[string]any) map[string]map[string]any {
	raw, _ := body["lanes"].([]any)
	out := map[string]map[string]any{}
	for _, item := range raw {
		lane := item.(map[string]any)
		out[lane["key"].(string)] = lane
	}
	return out
}

func marks(lane map[string]any) []map[string]any {
	raw, _ := lane["marks"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(map[string]any))
	}
	return out
}

// --- the fold ---

func TestATwoYearMedicationIsOneBarAndNotFortyDots(t *testing.T) {
	// Acceptance criterion 4, and the reason this endpoint exists at all. A physician looking
	// for "the HbA1c fell after we started metformin" needs the drug to have a *beginning*.
	// Forty identical dots have no beginning; a bar does.
	h := newAPI(t)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)

	start := time.Date(2022, 1, 10, 10, 0, 0, 0, time.UTC)
	for month := range 24 {
		h.timelineRow(t, id, start.AddDate(0, month, 0),
			"medication", "medication.prescribed", "Metformin 500mg", "মেটফরমিন ৫০০ মি.গ্রা.")
	}

	lane := lanes(h.spans(t, id, ""))["medications"]
	if lane == nil {
		t.Fatal("no medications lane")
	}
	if lane["durative"] != true {
		t.Errorf("the medications lane is not durative: %v", lane["durative"])
	}
	bars := marks(lane)
	if len(bars) != 1 {
		t.Fatalf("24 prescriptions of one drug produced %d marks, want one bar", len(bars))
	}
	if got := bars[0]["count"]; got != float64(24) {
		t.Errorf("the bar folds %v rows, want 24", got)
	}
	if got := bars[0]["occurred_at"]; got != start.Format(time.RFC3339Nano) {
		t.Errorf("the bar begins at %v, want the first prescription at %v", got, start)
	}
	if bars[0]["open_ended"] != true {
		t.Error("nothing stopped this drug, so the bar must be open-ended")
	}
	if _, ended := bars[0]["ended_at"]; ended {
		// The failure this guards is the tempting one: ending the bar at the last refill.
		// That draws a patient as having stopped a drug they are still on.
		t.Error("a bar nobody closed must not claim an end date")
	}
	if got := bars[0]["last_seen_at"]; got != start.AddDate(0, 23, 0).Format(time.RFC3339Nano) {
		t.Errorf("last_seen_at is %v, want the newest refill", got)
	}
}

func TestStoppingADrugClosesItsBar(t *testing.T) {
	h := newAPI(t)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)

	start := time.Date(2023, 2, 1, 9, 0, 0, 0, time.UTC)
	stop := time.Date(2024, 2, 1, 9, 0, 0, 0, time.UTC)
	h.timelineRow(t, id, start, "medication", "medication.prescribed", "Gliclazide 80mg", "গ্লিক্লাজাইড ৮০ মি.গ্রা.")
	h.timelineRow(t, id, start.AddDate(0, 6, 0), "medication", "medication.prescribed",
		"Gliclazide 80mg", "গ্লিক্লাজাইড ৮০ মি.গ্রা.")
	h.timelineRow(t, id, stop, "medication", "medication.stopped", "Gliclazide 80mg", "গ্লিক্লাজাইড ৮০ মি.গ্রা.")

	bars := marks(lanes(h.spans(t, id, ""))["medications"])
	if len(bars) != 1 {
		t.Fatalf("start, refill and stop produced %d marks, want one closed bar", len(bars))
	}
	if bars[0]["open_ended"] != false {
		t.Error("a drug that was stopped must not be drawn as still running")
	}
	if got := bars[0]["ended_at"]; got != stop.Format(time.RFC3339Nano) {
		t.Errorf("the bar ends at %v, want %v", got, stop)
	}
}

func TestTwoDifferentDrugsAreTwoBars(t *testing.T) {
	// The fold's other direction. A rule that collapsed everything on a lane into one bar
	// would pass the test above and be useless, so this is the assertion that says the
	// grouping is by *what the drug is* rather than by *which lane it is on*.
	h := newAPI(t)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)

	at := time.Date(2023, 5, 1, 9, 0, 0, 0, time.UTC)
	h.timelineRow(t, id, at, "medication", "medication.prescribed", "Metformin 500mg", "মেটফরমিন")
	h.timelineRow(t, id, at.AddDate(0, 1, 0), "medication", "medication.prescribed", "Empagliflozin 10mg", "এমপাগ্লিফ্লোজিন")
	h.timelineRow(t, id, at.AddDate(0, 2, 0), "medication", "medication.prescribed", "Metformin 500mg", "মেটফরমিন")

	bars := marks(lanes(h.spans(t, id, ""))["medications"])
	if len(bars) != 2 {
		for _, bar := range bars {
			t.Logf("%v ×%v", bar["label_en"], bar["count"])
		}
		t.Fatalf("two drugs produced %d bars", len(bars))
	}
}

func TestAnInvestigationIsAPointAndNotABar(t *testing.T) {
	// §8's lanes are not all the same kind of thing. A lab test *happens*; a drug *runs*.
	// Folding two lab tests of the same name into one bar would draw a patient as having
	// been continuously under investigation between two blood draws.
	h := newAPI(t)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)

	at := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)
	h.timelineRow(t, id, at, "observation", "investigation.ordered", "HbA1c", "এইচবিএ১সি")
	h.timelineRow(t, id, at.AddDate(0, 3, 0), "observation", "investigation.ordered", "HbA1c", "এইচবিএ১সি")

	all := lanes(h.spans(t, id, ""))
	lane := all["investigations"]
	if lane == nil {
		t.Fatalf("an investigation.* kind did not reach the investigations lane: %v", keysOf(all))
	}
	if lane["durative"] != false {
		t.Error("investigations must not be drawn as bars")
	}
	if got := len(marks(lane)); got != 2 {
		t.Errorf("two lab orders produced %d marks, want two points", got)
	}
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

// --- attribution ---

func TestEveryMarkAndEveryPointCarriesAttribution(t *testing.T) {
	// Acceptance criterion 3. The tooltip is the client's; what the server owes is that
	// there is something for it to show, on every mark and on every point rather than per
	// lane or per series.
	h := newAPI(t, "patient.read.demographics", "patient.write.demographics",
		patient.PermObservationValues)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)
	h.timelineRow(t, id, time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC),
		"diagnosis", "diagnosis.recorded", "Type 2 diabetes mellitus", "টাইপ ২ ডায়াবেটিস")

	body := h.spans(t, id, "series=HBA1C")

	found := 0
	for _, lane := range lanes(body) {
		for _, mark := range marks(lane) {
			found++
			for _, field := range []string{"actor_id", "actor_role", "recorded_at"} {
				if value, ok := mark[field]; !ok || value == "" {
					t.Errorf("a %v mark has no %s", lane["key"], field)
				}
			}
			// The zero uuid is not an author. It is what an unset field serialises to, it
			// renders as a plausible-looking id, and the directory answers nothing for it — so
			// the panel would say "we could not name them" about a value that has an author.
			// A blank would at least have looked broken.
			if mark["actor_id"] == "00000000-0000-0000-0000-000000000000" {
				t.Errorf("a %v mark's actor is the zero uuid", lane["key"])
			}
			if _, ok := mark["source"]; !ok {
				// Empty is a fine answer and absent is not: "the record has the field and
				// nobody filled it" has to be distinguishable from a station entry.
				t.Errorf("a %v mark carries no source field at all", lane["key"])
			}
		}
	}
	if found == 0 {
		t.Fatal("no marks were checked, so this test asserted nothing")
	}

	series, _ := body["series"].([]any)
	points := 0
	for _, item := range series {
		raw, _ := item.(map[string]any)["points"].([]any)
		for _, entry := range raw {
			points++
			point := entry.(map[string]any)
			for _, field := range []string{"actor_id", "actor_role", "recorded_at", "observation_id"} {
				if value, ok := point[field]; !ok || value == "" {
					t.Errorf("a series point has no %s", field)
				}
			}
			if point["actor_id"] == "00000000-0000-0000-0000-000000000000" {
				t.Error("a series point's actor is the zero uuid")
			}
		}
	}
	if points == 0 {
		t.Fatal("no series points were checked, so this test asserted nothing")
	}
}

// --- the permission ---

func TestACallerWhoMayNotReadValuesGetsNoPoints(t *testing.T) {
	/*
	 * The one that has to bite. `observation.read.values` is narrower than the route's own
	 * guard: §4.4 blinds several roles from a patient's numbers while letting them see that
	 * the patient attended.
	 *
	 * Written so that deleting the filter fails it: the fake series reader **always** has a
	 * point, so a response with no points is a response the permission emptied and not a
	 * database that had nothing. And the omission is asserted as well as the emptiness,
	 * because a chart that is silently empty is the failure the omission list exists to
	 * prevent — "this patient has no HbA1c" and "you may not see it" are opposite facts.
	 */
	blind := newAPI(t, "patient.read.demographics", "patient.write.demographics")
	id := blind.registerAs(t, func(map[string]any) {})["id"].(string)

	body := blind.spans(t, id, "series=HBA1C")
	for _, item := range body["series"].([]any) {
		if points := item.(map[string]any)["points"].([]any); len(points) != 0 {
			t.Fatalf("a caller without %s received %d values",
				patient.PermObservationValues, len(points))
		}
	}

	withheld := false
	for _, item := range body["omitted"].([]any) {
		omission := item.(map[string]any)
		if omission["part"] == "series" && omission["withheld"] == true {
			withheld = true
			if omission["needs"] != patient.PermObservationValues {
				t.Errorf("the omission names %v as the permission needed", omission["needs"])
			}
			// Both languages, because the person who most needs to know a panel was
			// withheld may not be reading the interface in English.
			for _, field := range []string{"reason_en", "reason_bn"} {
				if omission[field] == "" {
					t.Errorf("the omission has no %s", field)
				}
			}
		}
	}
	if !withheld {
		t.Error("the values were withheld and the response did not say so")
	}
}

func TestACallerWhoMayReadValuesGetsThem(t *testing.T) {
	// The other half. Without this, the test above passes against a build that returns no
	// points to anybody — which is the shape a broken filter actually takes.
	h := newAPI(t, "patient.read.demographics", "patient.write.demographics",
		patient.PermObservationValues)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)

	body := h.spans(t, id, "series=HBA1C")
	total := 0
	for _, item := range body["series"].([]any) {
		total += len(item.(map[string]any)["points"].([]any))
	}
	if total == 0 {
		t.Fatal("a caller holding the permission received no values")
	}
	for _, item := range body["omitted"].([]any) {
		if item.(map[string]any)["part"] == "series" {
			t.Error("nothing was withheld and the response says something was")
		}
	}
}

// --- the query ---

func TestAnEmptySeriesIsNotAMissingOne(t *testing.T) {
	// "This patient has never had an HbA1c" and "you did not ask for HbA1c" are different
	// answers, and a client that could not tell them apart would draw three overlays where
	// four were asked for and say nothing about the fourth.
	h := newAPI(t, "patient.read.demographics", "patient.write.demographics",
		patient.PermObservationValues)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)

	body := h.spans(t, id, "series=HBA1C,BODY_WEIGHT")
	series := body["series"].([]any)
	if len(series) != 2 {
		t.Fatalf("two codes were asked for and %d came back", len(series))
	}
	for _, item := range series {
		entry := item.(map[string]any)
		if entry["code"] == "BODY_WEIGHT" && len(entry["points"].([]any)) != 0 {
			t.Error("BODY_WEIGHT has no values and came back with some")
		}
	}
}

func TestTurningEveryOverlayOffIsNotTheSameAsAskingForNothing(t *testing.T) {
	// `series=` present and empty is a reader who wants the lanes alone. Rounding it up to
	// the defaults would make the control on the screen not work.
	h := newAPI(t, "patient.read.demographics", "patient.write.demographics",
		patient.PermObservationValues)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)

	if got := len(h.spans(t, id, "series=")["series"].([]any)); got != 0 {
		t.Errorf("series= returned %d series, want none", got)
	}
	if got := len(h.spans(t, id, "")["series"].([]any)); got != len(patient.DefaultSeriesCodes) {
		t.Errorf("no series parameter returned %d series, want the %d defaults",
			got, len(patient.DefaultSeriesCodes))
	}
}

func TestAnUnknownSeriesCodeIsRefusedRatherThanReturnedEmpty(t *testing.T) {
	// The same reasoning CP37 applies to `types`: a silently empty series is
	// indistinguishable from a patient who has never had that test, and the person who
	// typed the code has no way to learn they typed it wrongly.
	h := newAPI(t)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)

	// A lower-case code, which is the typo that actually happens: the registry spells every
	// code in upper case and a client that sent `hba1c` would otherwise get a chart with a
	// silently empty overlay on it.
	resp, body := h.call(t, http.MethodGet,
		"/v1/patients/"+id+"/timeline/spans?series=hba1c", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a malformed code answered %d: %v", resp.StatusCode, body)
	}
	envelope := body["error"].(map[string]any)
	if envelope["code"] != "VALIDATION_FAILED" {
		t.Errorf("the error code is %v", envelope["code"])
	}
	if envelope["message_bn"] == "" {
		t.Error("the error has no Bengali message")
	}
}

func TestTheWholeSpanOnRecordIsReportedWhateverWindowWasAsked(t *testing.T) {
	// So the screen can offer a range rather than guessing one, and so "nothing in this
	// window" is distinguishable from "nothing at all".
	h := newAPI(t)
	id := h.registerAs(t, func(map[string]any) {})["id"].(string)
	old := time.Date(2016, 4, 2, 8, 0, 0, 0, time.UTC)
	h.timelineRow(t, id, old, "diagnosis", "diagnosis.recorded", "Hypertension", "উচ্চ রক্তচাপ")

	body := h.spans(t, id, "from=2026-01-01&to=2026-12-31")
	if len(marks(lanes(body)["diagnoses"])) != 0 {
		t.Error("a 2016 diagnosis was returned inside a 2026 window")
	}
	if body["earliest"] == nil {
		t.Fatal("no earliest was reported, so the screen cannot offer the range")
	}
	if got := body["earliest"].(string); got[:4] != "2016" {
		t.Errorf("earliest is %v, want the 2016 diagnosis", got)
	}
}
