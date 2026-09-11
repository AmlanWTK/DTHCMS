package projection_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The clinical kinds on the patient timeline (CP37 v2, feeding CP74).
//
// The derivation is the whole of this file's subject, so the harness registers only
// `PatientTimeline`: what is being asserted is which row a given event puts on a timeline, and
// running the other twelve projections beside it would only add ways for the test to fail for
// reasons that are not about the timeline.
//
// Every case asserts the four things a blank timeline screen is made of — the category a
// filter offers, the kind a lane is drawn from, a label in **both** languages, and the
// permission a reader genuinely needs — plus `value_num` wherever CP74 has a number to plot.

type timelineFixture struct {
	*harness
	patient  uuid.UUID
	visit    uuid.UUID
	occurred time.Time
}

// newTimelineFixture registers the timeline projection alone and gives it a real operator to
// attribute rows to. The operator has to exist: `read.apply_timeline` resolves the employee
// code from `core.app_user`, and `core.assert_timeline_rows_are_attributed()` refuses a row
// whose code came back empty.
func newTimelineFixture(t *testing.T) *timelineFixture {
	t.Helper()
	h := newHarness(t, projection.PatientTimeline{})
	if _, err := h.db.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'A007', 'Anthropometry Officer', 'দেহমাপ কর্মকর্তা', 'active')`,
		h.user, h.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Tablet 7', 'tablet', 'active', now())`,
		h.device, h.facility); err != nil {
		t.Fatal(err)
	}
	return &timelineFixture{
		harness: h, patient: uuid.New(), visit: uuid.New(),
		occurred: time.Date(2026, 3, 11, 4, 5, 0, 0, time.UTC),
	}
}

// event writes one envelope through the timeline projection. `aggregate` matters: the registry
// refuses an event filed under the wrong one, which is a real check and not worth defeating.
func (f *timelineFixture) event(t *testing.T, eventType, aggregate string, payload map[string]any) eventstore.Event {
	t.Helper()
	f.clock.Advance(time.Second)
	id := f.patient
	if aggregate == "VISIT" {
		id = f.visit
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return f.append(t, eventstore.Envelope{
		EventID: uuid.Must(uuid.NewV7()), AggregateType: aggregate, AggregateID: id,
		PatientID: &f.patient, VisitID: &f.visit,
		EventType: eventType, EventVersion: 1, OccurredAt: f.occurred,
		Actor:   eventstore.ActorForTest(f.user, f.device, f.facility, "ANTHROPOMETRY", "STN_ANTHROPOMETRY"),
		Source:  eventstore.SourceMobileOnline,
		Payload: raw,
	})
}

// timelineRow is a row as the database holds it, which is the only copy that matters: the Go
// struct is an instruction to `read.apply_timeline`, not the result.
type timelineRow struct {
	Category, Kind, LabelEN, LabelBN string
	Value, Unit, Permission          string
	ValueNum                         *float64
	ActorCode                        string
	// Joined rather than scanned as an array: database/sql has no text[] decoder, and the
	// order is the derivation's own, which is what the assertion is about.
	Flags      string
	OccurredAt time.Time
}

// rowFor is the event's principal row: the one with the empty `item`, or its only row when
// the derivation gave it a discriminator of its own (a consent type, an allergy assertion).
func (f *timelineFixture) rowFor(t *testing.T, e eventstore.Event) timelineRow {
	t.Helper()
	var got timelineRow
	if err := f.db.SQL.QueryRow(`
		SELECT category, kind, label_en, label_bn, value, unit, needs_permission,
		       value_num, actor_code, array_to_string(flags, ','), occurred_at
		  FROM read.patient_timeline WHERE event_id = $1
		 ORDER BY item LIMIT 1`, e.EventID).
		Scan(&got.Category, &got.Kind, &got.LabelEN, &got.LabelBN, &got.Value, &got.Unit,
			&got.Permission, &got.ValueNum, &got.ActorCode, &got.Flags, &got.OccurredAt); err != nil {
		t.Fatalf("no timeline row for %s: %v", e.EventType, err)
	}
	return got
}

// --- the derivation, kind by kind ---

func TestEveryClinicalEventPutsTheRowItPromisesOnTheTimeline(t *testing.T) {
	f := newTimelineFixture(t)
	encounter := uuid.New().String()

	cases := []struct {
		name       string
		eventType  string
		aggregate  string
		payload    map[string]any
		category   string
		kind       string
		labelEN    string
		labelBN    string
		permission string
		// value_num and unit, when this kind has a number CP74 can plot. A `nil` numeric
		// here is an assertion too: the row must NOT carry one.
		valueNum *float64
		unit     string
		// value, when the shown string is worth pinning: the rounded rendering of a
		// converted measurement is the thing a clinician actually reads.
		value string
		flags string
	}{
		{
			name: "a weight", eventType: "OBSERVATION_RECORDED", aggregate: "PATIENT",
			payload: map[string]any{
				"observation_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "code": "BODY_WEIGHT",
				"value": 69.85, "unit": "kg", "source": "STATION",
				"effective_at": "2026-03-11T03:05:00Z",
			},
			category: "observation", kind: "observation.body_weight",
			// From `core.observation_code`, both languages, never a string invented in Go.
			labelEN: "Weight", labelBN: "ওজন",
			permission: "observation.read.values",
			valueNum:   ptr(69.85), unit: "kg",
		},
		{
			name:      "a weight entered in pounds is stored in kilograms",
			eventType: "OBSERVATION_RECORDED", aggregate: "PATIENT",
			payload: map[string]any{
				"observation_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "code": "BODY_WEIGHT",
				"value": 154.0, "unit": "[lb_av]", "source": "STATION",
				"effective_at": "2026-03-11T03:06:00Z",
			},
			category: "observation", kind: "observation.body_weight",
			labelEN: "Weight", labelBN: "ওজন",
			permission: "observation.read.values",
			// 154 lb is 69.85322498 kg, exactly, by `core.to_canonical`. A chart that
			// plotted 154 beside a colleague's 70 would draw a patient who doubled in
			// weight between two stations.
			//
			// `value_num` is **not** rounded and `value` is: the number is for arithmetic
			// and the string is for a cell, and rounding the one a chart reads would put a
			// rounding step between the ledger and every trend drawn from it.
			valueNum: ptr(69.85322498), unit: "kg", value: "69.9",
		},
		{
			name: "a blood pressure", eventType: "OBSERVATION_RECORDED", aggregate: "PATIENT",
			payload: map[string]any{
				"observation_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "code": "BP_SYSTOLIC",
				"value": 148.0, "unit": "mm[Hg]", "source": "STATION",
				"effective_at": "2026-03-11T03:07:00Z",
			},
			category: "observation", kind: "observation.bp_systolic",
			labelEN: "Systolic blood pressure", labelBN: "সিস্টোলিক রক্তচাপ",
			permission: "observation.read.values",
			valueNum:   ptr(148), unit: "mmHg",
		},
		{
			name: "an HbA1c entered as NGSP percent", eventType: "OBSERVATION_RECORDED", aggregate: "PATIENT",
			payload: map[string]any{
				"observation_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "code": "HBA1C",
				"value": 8.2, "unit": "%#ngsp", "source": "STATION",
				"effective_at": "2026-03-11T03:08:00Z",
			},
			category: "observation", kind: "observation.hba1c",
			labelEN: "HbA1c", labelBN: "এইচবিএ১সি",
			permission: "observation.read.values",
			// IFCC = (NGSP − 2.15) × 10.929, which is 66.12045 for 8.2 %. The conversion is
			// the database's — the same `core.to_canonical` the write path uses — and not a
			// second copy of the unit table in Go.
			valueNum: ptr(66.12045), unit: "mmol/mol", value: "66",
		},
		{
			name: "a visit opening", eventType: "VISIT_OPENED", aggregate: "VISIT",
			payload: map[string]any{
				"facility_id": f.facility.String(), "patient_id": f.patient.String(),
				"visit_code": "V-2026-0311-004", "visit_type": "follow_up",
				"clinic_day": "2026-03-11", "chief_complaint": "Tiredness",
			},
			category: "visit", kind: "visit.opened",
			labelEN: "Visit opened — follow-up", labelBN: "ভিজিট শুরু — ফলো-আপ",
			permission: "visit.read",
		},
		{
			name: "a visit closing", eventType: "VISIT_CLOSED", aggregate: "VISIT",
			payload: map[string]any{
				"facility_id": f.facility.String(), "patient_id": f.patient.String(),
				"visit_code": "V-2026-0311-004", "chief_complaint": "Tiredness",
				"diagnoses": "type 2 diabetes", "plan": "Continue metformin",
				"next_review_days": 90,
			},
			category: "visit", kind: "visit.closed",
			labelEN: "Visit closed", labelBN: "ভিজিট শেষ",
			permission: "visit.read",
			// The review interval is its own row — see `visit.review_due` below.
			value: "V-2026-0311-004",
		},
		{
			name: "arriving at a station", eventType: "ENCOUNTER_STARTED", aggregate: "VISIT",
			payload: map[string]any{
				"facility_id": f.facility.String(), "patient_id": f.patient.String(),
				"visit_id": f.visit.String(), "encounter_id": encounter,
				"station_code": "STN_ANTHROPOMETRY",
			},
			category: "visit", kind: "encounter.started",
			// The station's name, from `core.station`, in both languages.
			labelEN: "Anthropometry & Screening started", labelBN: "দেহমাপ ও স্ক্রিনিং শুরু হয়েছে",
			permission: "visit.read",
		},
		{
			name: "leaving a station", eventType: "ENCOUNTER_FINISHED", aggregate: "VISIT",
			payload: map[string]any{
				"facility_id": f.facility.String(), "patient_id": f.patient.String(),
				"visit_id": f.visit.String(), "encounter_id": encounter,
				"station_code": "STN_ANTHROPOMETRY", "outcome": "completed",
				"seconds_at_station": 606,
			},
			category: "visit", kind: "encounter.finished",
			labelEN: "Anthropometry & Screening finished", labelBN: "দেহমাপ ও স্ক্রিনিং শেষ হয়েছে",
			permission: "visit.read",
			valueNum:   ptr(606), unit: "s", value: "606",
		},
		{
			name: "a bounced encounter reads as a bounce", eventType: "ENCOUNTER_FINISHED", aggregate: "VISIT",
			payload: map[string]any{
				"facility_id": f.facility.String(), "patient_id": f.patient.String(),
				"visit_id": f.visit.String(), "encounter_id": uuid.New().String(),
				"station_code": "STN_ANTHROPOMETRY", "outcome": "bounced",
				"seconds_at_station": 44,
			},
			category: "visit", kind: "encounter.finished",
			// §14.2 counts rework. A bounce that reads as a completion at a glance makes
			// rework invisible, which is the one number a quality gate exists to produce.
			labelEN:    "Anthropometry & Screening sent back for rework",
			labelBN:    "দেহমাপ ও স্ক্রিনিং থেকে সংশোধনের জন্য ফেরত",
			permission: "visit.read",
			valueNum:   ptr(44), unit: "s", value: "44",
		},
		{
			name: "no known allergies", eventType: "ALLERGY_STATUS_ASSERTED", aggregate: "PATIENT",
			payload: map[string]any{
				"assertion_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "kind": "NO_KNOWN_ALLERGY",
				"asserted_at": "2026-03-11T03:20:00Z",
			},
			category: "observation", kind: "allergy.status",
			labelEN: "No known allergies", labelBN: "কোনো জানা অ্যালার্জি নেই",
			// CP54's asymmetry: an allergy reaches the pharmacist, who is blind to values.
			permission: "patient.read.allergies",
		},
		{
			name: "allergy status could not be assessed", eventType: "ALLERGY_STATUS_ASSERTED", aggregate: "PATIENT",
			payload: map[string]any{
				"assertion_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "kind": "UNABLE_TO_ASSESS",
				"reason": "Patient unconscious, no attendant", "asserted_at": "2026-03-11T03:21:00Z",
			},
			category: "observation", kind: "allergy.status",
			// Not "no allergies recorded". The third state says the opposite of "none",
			// which is exactly the reading a blank field invites.
			labelEN: "Allergy status could not be assessed", labelBN: "অ্যালার্জির তথ্য জানা যায়নি",
			permission: "patient.read.allergies",
		},
		{
			name: "a critical value", eventType: "CRITICAL_VALUE_ALERTED", aggregate: "PATIENT",
			payload: map[string]any{
				"alert_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "observation_id": uuid.New().String(),
				"code": "GLUCOSE_RANDOM", "value_num": 2.6, "unit": "mmol/L",
				"breached": "low", "threshold": 3.3, "raised_at": "2026-03-11T03:25:00Z",
			},
			category: "alert", kind: "alert.critical_value",
			labelEN:    "Critical value: Random plasma glucose",
			labelBN:    "জরুরি মান: যেকোনো সময়ের গ্লুকোজ",
			permission: "alert.read",
			valueNum:   ptr(2.6), unit: "mmol/L",
			// 3.0 is as urgent as 25.0 and the two mean opposite things.
			flags: "critical,low",
		},
		{
			name:      "an alert nobody could be reached about",
			eventType: "CRITICAL_VALUE_DELIVERY_ATTEMPTED", aggregate: "PATIENT",
			payload: map[string]any{
				"alert_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "recipients": 0,
				"attempted_at": "2026-03-11T03:26:00Z",
			},
			category: "communication", kind: "alert.delivery_attempted",
			labelEN: "Critical value alert sent", labelBN: "জরুরি মান জানানো হয়েছে",
			permission: "alert.read",
			// Zero is the number that matters: an attempt that reached nobody is the case
			// the escalation exists for, and it is only visible if the count is on the row.
			valueNum: ptr(0), unit: "recipients",
		},
		{
			name: "a diet recall", eventType: "DIET_ENTRY_RECORDED", aggregate: "PATIENT",
			payload: map[string]any{
				"entry_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "recall_date": "2026-03-10",
				"meal": "BREAKFAST", "eaten_at_hour": 8,
				"food_code": "RUTI_ATTA", "measure_code": "PIECE", "quantity": 2.0,
				"recorded_at": "2026-03-11T03:30:00Z",
			},
			category: "observation", kind: "diet.entry",
			// Meal and food both from the registries that own their Bangla names.
			labelEN: "Breakfast: Ruti (wholemeal)", labelBN: "সকালের নাশতা: আটার রুটি",
			permission: "observation.read.values",
			valueNum:   ptr(2), unit: "piece",
		},
		{
			name: "an exercise assessment", eventType: "EXERCISE_ASSESSMENT_RECORDED", aggregate: "PATIENT",
			payload: map[string]any{
				"assessment_id": uuid.New().String(), "facility_id": f.facility.String(),
				"patient_id": f.patient.String(), "walk_minutes": 41,
				"walks_unaided": true, "contraindications": []string{},
				"asked":       []string{"SEVERE_NEUROPATHY", "ACTIVE_FOOT_ULCER"},
				"recorded_at": "2026-03-11T03:35:00Z",
			},
			category: "observation", kind: "exercise.assessment",
			labelEN: "Exercise assessment", labelBN: "ব্যায়াম মূল্যায়ন",
			permission: "observation.read.values",
			valueNum:   ptr(41), unit: "min",
		},
		{
			name: "a synthesis that is ready", eventType: "AI_SYNTHESIS_COMPLETED", aggregate: "VISIT",
			payload: map[string]any{
				"facility_id": f.facility.String(), "patient_id": f.patient.String(),
				"visit_id": f.visit.String(), "synthesis_id": uuid.New().String(),
				"agent_code": "clinical.synthesis", "prompt_version": "1.0.0",
				"model_version": "mock-000", "generation": 1, "state": "READY",
				"met_sla": true, "completed_at": "2026-03-11T03:40:00Z",
			},
			category: "document", kind: "ai.synthesis.completed",
			labelEN:    "Pre-consultation summary ready",
			labelBN:    "পরামর্শপূর্ব সারসংক্ষেপ প্রস্তুত",
			permission: "ai.synthesis.read",
		},
		{
			name: "a synthesis that failed", eventType: "AI_SYNTHESIS_FAILED", aggregate: "VISIT",
			payload: map[string]any{
				"facility_id": f.facility.String(), "patient_id": f.patient.String(),
				"visit_id": f.visit.String(), "synthesis_id": uuid.New().String(),
				"agent_code": "clinical.synthesis", "generation": 1,
				"failure_kind": "PROVIDER", "failed_at": "2026-03-11T03:41:00Z",
			},
			category: "document", kind: "ai.synthesis.failed",
			// The physician who opened a record expecting a summary has to see that there
			// is not one, rather than an empty panel that reads as a patient with no history.
			labelEN:    "Pre-consultation summary could not be produced",
			labelBN:    "পরামর্শপূর্ব সারসংক্ষেপ তৈরি করা যায়নি",
			permission: "ai.synthesis.read",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := f.event(t, tc.eventType, tc.aggregate, tc.payload)
			got := f.rowFor(t, e)

			if got.Category != tc.category {
				t.Errorf("category = %q, want %q", got.Category, tc.category)
			}
			if got.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.kind)
			}
			if got.LabelEN != tc.labelEN {
				t.Errorf("label_en = %q, want %q", got.LabelEN, tc.labelEN)
			}
			if got.LabelBN != tc.labelBN {
				t.Errorf("label_bn = %q, want %q", got.LabelBN, tc.labelBN)
			}
			// Not just "non-empty": a Bangla label that came back as the English one is a
			// timeline that is silently monolingual for the codes nobody checked.
			if got.LabelBN == got.LabelEN {
				t.Errorf("label_bn is the English label; the floor staff read Bangla")
			}
			if got.Permission != tc.permission {
				t.Errorf("needs_permission = %q, want %q", got.Permission, tc.permission)
			}
			if got.ActorCode != "A007" {
				t.Errorf("actor_code = %q; §8's hover-to-see-who needs one on every row", got.ActorCode)
			}

			switch {
			case tc.valueNum == nil && got.ValueNum != nil:
				t.Errorf("value_num = %v; this kind has no number to plot", *got.ValueNum)
			case tc.valueNum != nil && got.ValueNum == nil:
				t.Errorf("value_num is null; CP74 draws its overlays from it")
			case tc.valueNum != nil && *got.ValueNum != *tc.valueNum:
				t.Errorf("value_num = %v, want %v", *got.ValueNum, *tc.valueNum)
			}
			if got.Unit != tc.unit {
				t.Errorf("unit = %q, want %q", got.Unit, tc.unit)
			}
			if tc.value != "" && got.Value != tc.value {
				t.Errorf("value = %q, want %q", got.Value, tc.value)
			}
			if tc.flags != "" && got.Flags != tc.flags {
				t.Errorf("flags = %q, want %q", got.Flags, tc.flags)
			}
		})
	}

	// And the invariant agrees about all of them at once, which is what `migrate verify`
	// will run against the real ledger.
	if _, err := f.db.SQL.Exec(`SELECT core.assert_timeline_rows_are_attributed()`); err != nil {
		t.Fatalf("the attribution invariant does not hold: %v", err)
	}
}

// A measurement belongs at the moment it was true, not the moment somebody typed it.
func TestAnObservationSitsAtTheMomentItWasMeasured(t *testing.T) {
	f := newTimelineFixture(t)
	measured := time.Date(2026, 3, 11, 3, 5, 0, 0, time.UTC)

	e := f.event(t, "OBSERVATION_RECORDED", "PATIENT", map[string]any{
		"observation_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "code": "BODY_TEMP",
		"value": 38.4, "unit": "Cel", "source": "STATION",
		"effective_at": measured.Format(time.RFC3339),
	})
	// The envelope says 04:05; the reading was taken at 03:05. A timeline that used the
	// envelope would order it wrongly beside a promptly-entered one.
	if got := f.rowFor(t, e).OccurredAt; !got.Equal(measured) {
		t.Errorf("occurred_at = %s, want the effective time %s", got, measured)
	}
}

// A 24-hour recall taken on Wednesday is about Tuesday, in the clinic's calendar.
func TestADietRecallSitsOnTheDayItIsAbout(t *testing.T) {
	f := newTimelineFixture(t)
	e := f.event(t, "DIET_ENTRY_RECORDED", "PATIENT", map[string]any{
		"entry_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "recall_date": "2026-03-10",
		"meal": "LUNCH", "eaten_at_hour": 13,
		"food_code": "RUTI_ATTA", "measure_code": "PIECE", "quantity": 3.0,
		"recorded_at": "2026-03-11T03:30:00Z",
	})
	// 13:00 in Asia/Dhaka on the tenth, which is 07:00 UTC. Recorded a day later.
	want := time.Date(2026, 3, 10, 7, 0, 0, 0, time.UTC)
	if got := f.rowFor(t, e).OccurredAt; !got.Equal(want) {
		t.Errorf("occurred_at = %s, want %s — a recall placed on the day it was taken "+
			"makes every recall a day wrong", got.UTC(), want)
	}
}

// --- the decision about the queue ---

// TestTheQueueIsDeliberatelyNotOnTheClinicalTimeline holds a decision, so that adding the
// family later is something somebody does on purpose.
//
// A queue entry is the clinic's logistics: which line, what position, how many seconds. It is
// also shadowed — every QUEUE_* event names the same visit and the same station as an
// ENCOUNTER_* event seconds away, so deriving both would draw every station transition twice
// and the second copy would be the one that says nothing about the patient.
//
// The traffic board (CP40) and `read.station_activity` are where the queue is read, by the
// floor supervisor whose question it answers.
func TestTheQueueIsDeliberatelyNotOnTheClinicalTimeline(t *testing.T) {
	timeline := projection.PatientTimeline{}
	for _, eventType := range []string{"QUEUE_ENTERED", "QUEUE_CALLED", "QUEUE_LEFT"} {
		if timeline.Handles(eventType) {
			t.Errorf("%s reaches the clinical timeline; it is operational, it is shadowed by "+
				"ENCOUNTER_*, and it is read on the traffic board (CP40). If this is now "+
				"wanted, change the decision here as well as the code", eventType)
		}
	}
	// And the encounter, which is the clinical half of the same journey, does.
	for _, eventType := range []string{"ENCOUNTER_STARTED", "ENCOUNTER_FINISHED"} {
		if !timeline.Handles(eventType) {
			t.Errorf("%s does not reach the timeline; the station journey would be missing", eventType)
		}
	}
}

// --- version and rebuild ---

// A derivation that grew new kinds and kept its version is a decade of history quietly
// missing them. The runner refuses to advance a model built by an older one, and that refusal
// is only armed if the number moved.
func TestGrowingTheDerivationRaisedItsVersion(t *testing.T) {
	if got := (projection.PatientTimeline{}).Version(); got < 2 {
		t.Fatalf("PatientTimeline.Version() = %d; CP37 shipped 1 and the clinical kinds are new", got)
	}
}

// A rebuild of the clinical kinds produces the same table, field by field. The patient-facing
// twin of this lives in internal/patient; this one runs against the derivation alone, with
// every kind present.
func TestRebuildingTheClinicalKindsReproducesThem(t *testing.T) {
	f := newTimelineFixture(t)
	f.everyKind(t)

	before := f.fingerprint(t)
	if len(before) < 10 {
		t.Fatalf("the fixture produced %d rows; the test would prove nothing", len(before))
	}

	if _, err := f.engine.Rebuild(context.Background(), "patient_timeline",
		projection.RebuildOptions{BatchSize: 3, Logger: testLogger()}); err != nil {
		t.Fatal(err)
	}

	after := f.fingerprint(t)
	if len(after) != len(before) {
		t.Fatalf("the rebuild produced %d rows, was %d", len(after), len(before))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("row %d differs:\n before %s\n  after %s", i, before[i], after[i])
		}
	}

	// Twice, because that is what a nervous operator actually does.
	if _, err := f.engine.Rebuild(context.Background(), "patient_timeline",
		projection.RebuildOptions{BatchSize: 3, Logger: testLogger()}); err != nil {
		t.Fatal(err)
	}
	for i, line := range f.fingerprint(t) {
		if line != before[i] {
			t.Errorf("row %d differs after a second rebuild:\n once %s\n twice %s", i, before[i], line)
		}
	}
}

// fingerprint is every row's derived content, ordered. `value_num` and `needs_permission` are
// in it deliberately: a rebuild that reproduced the labels and lost the numbers, or one that
// reproduced the rows under CP37's permission, would pass a row count and fail a clinician.
func (f *timelineFixture) fingerprint(t *testing.T) []string {
	t.Helper()
	rows, err := f.db.SQL.Query(`
		SELECT patient_id || '|' || occurred_at || '|' || category || '|' || kind || '|' ||
		       label_en || '|' || label_bn || '|' || value || '|' || unit || '|' ||
		       coalesce(value_num::text, 'null') || '|' || needs_permission || '|' ||
		       coalesce(actor_code, '') || '|' || array_to_string(flags, ',') || '|' ||
		       event_id || '|' || item
		  FROM read.patient_timeline ORDER BY global_seq, item`)
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

// everyKind writes one event of every type the timeline handles, so a rebuild test covers the
// derivation rather than the two kinds somebody remembered.
func (f *timelineFixture) everyKind(t *testing.T) {
	t.Helper()
	encounter := uuid.New().String()
	f.event(t, "OBSERVATION_RECORDED", "PATIENT", map[string]any{
		"observation_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "code": "BODY_WEIGHT",
		"value": 71.2, "unit": "kg", "source": "STATION", "effective_at": "2026-03-11T03:05:00Z",
	})
	f.event(t, "OBSERVATION_RECORDED", "PATIENT", map[string]any{
		"observation_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "code": "HBA1C",
		"value": 7.6, "unit": "%#ngsp", "source": "STATION", "effective_at": "2026-03-11T03:06:00Z",
	})
	f.event(t, "VISIT_OPENED", "VISIT", map[string]any{
		"facility_id": f.facility.String(), "patient_id": f.patient.String(),
		"visit_code": "V-2026-0311-009", "visit_type": "new", "clinic_day": "2026-03-11",
	})
	f.event(t, "ENCOUNTER_STARTED", "VISIT", map[string]any{
		"facility_id": f.facility.String(), "patient_id": f.patient.String(),
		"visit_id": f.visit.String(), "encounter_id": encounter, "station_code": "STN_EXAMINATION",
	})
	f.event(t, "ENCOUNTER_FINISHED", "VISIT", map[string]any{
		"facility_id": f.facility.String(), "patient_id": f.patient.String(),
		"visit_id": f.visit.String(), "encounter_id": encounter,
		"station_code": "STN_EXAMINATION", "outcome": "completed", "seconds_at_station": 300,
	})
	f.event(t, "ALLERGY_STATUS_ASSERTED", "PATIENT", map[string]any{
		"assertion_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "kind": "NO_KNOWN_ALLERGY",
		"asserted_at": "2026-03-11T03:20:00Z",
	})
	f.event(t, "CRITICAL_VALUE_ALERTED", "PATIENT", map[string]any{
		"alert_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "observation_id": uuid.New().String(),
		"code": "GLUCOSE_FASTING", "value_num": 21.4, "unit": "mmol/L",
		"breached": "high", "threshold": 20, "raised_at": "2026-03-11T03:25:00Z",
	})
	f.event(t, "CRITICAL_VALUE_DELIVERY_ATTEMPTED", "PATIENT", map[string]any{
		"alert_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "recipients": 2,
		"attempted_at": "2026-03-11T03:26:00Z",
	})
	f.event(t, "DIET_ENTRY_RECORDED", "PATIENT", map[string]any{
		"entry_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "recall_date": "2026-03-10", "meal": "DINNER",
		"food_code": "RUTI_ATTA", "measure_code": "PIECE", "quantity": 2.0,
		"recorded_at": "2026-03-11T03:30:00Z",
	})
	f.event(t, "EXERCISE_ASSESSMENT_RECORDED", "PATIENT", map[string]any{
		"assessment_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "walk_minutes": 25, "walks_unaided": true,
		"contraindications": []string{}, "asked": []string{"CARDIAC_LIMITATION"},
		"recorded_at": "2026-03-11T03:35:00Z",
	})
	f.event(t, "AI_SYNTHESIS_COMPLETED", "VISIT", map[string]any{
		"facility_id": f.facility.String(), "patient_id": f.patient.String(),
		"visit_id": f.visit.String(), "synthesis_id": uuid.New().String(),
		"agent_code": "clinical.synthesis", "prompt_version": "1.0.0", "model_version": "mock-000",
		"generation": 1, "state": "READY", "met_sla": true, "completed_at": "2026-03-11T03:40:00Z",
	})
	f.event(t, "AI_SYNTHESIS_FAILED", "VISIT", map[string]any{
		"facility_id": f.facility.String(), "patient_id": f.patient.String(),
		"visit_id": f.visit.String(), "synthesis_id": uuid.New().String(),
		"agent_code": "clinical.synthesis", "generation": 1,
		"failure_kind": "TIMEOUT", "failed_at": "2026-03-11T03:41:00Z",
	})
	// And the queue, which must produce nothing.
	f.event(t, "QUEUE_ENTERED", "VISIT", map[string]any{
		"entry_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "visit_id": f.visit.String(),
		"station_code": "STN_EXAMINATION", "position": 2, "priority": 0,
	})
}

// A re-delivered clinical event updates its row rather than adding one — the tablet that sent,
// lost the reply and sent again.
func TestARedeliveredObservationDoesNotDoubleTheTimeline(t *testing.T) {
	f := newTimelineFixture(t)
	payload := map[string]any{
		"observation_id": uuid.New().String(), "facility_id": f.facility.String(),
		"patient_id": f.patient.String(), "code": "BODY_HEIGHT",
		"value": 162.0, "unit": "cm", "source": "STATION", "effective_at": "2026-03-11T03:05:00Z",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope := eventstore.Envelope{
		EventID: uuid.Must(uuid.NewV7()), AggregateType: "PATIENT", AggregateID: f.patient,
		PatientID: &f.patient, EventType: "OBSERVATION_RECORDED", EventVersion: 1,
		OccurredAt: f.occurred,
		Actor:      eventstore.ActorForTest(f.user, f.device, f.facility, "ANTHROPOMETRY", "STN_ANTHROPOMETRY"),
		Source:     eventstore.SourceMobileOnline, Payload: raw,
	}
	f.append(t, envelope)
	f.append(t, envelope)

	if got := f.count(t, `SELECT count(*) FROM read.patient_timeline WHERE kind = 'observation.body_height'`); got != 1 {
		t.Errorf("a retried observation produced %d timeline rows", got)
	}
}

func ptr(v float64) *float64 { return &v }

// A closed visit puts the review interval on a row of its own.
//
// The alternative — value_num on the `visit.closed` row — makes `value`, `unit` and
// `value_num` say three different things: the visit code, "days", and 90. A chart reading the
// third while a person reads the first is a screen saying two things at once, and `item` is
// what the schema provides so it does not have to.
func TestAClosedVisitPutsItsReviewIntervalOnItsOwnRow(t *testing.T) {
	f := newTimelineFixture(t)
	e := f.event(t, "VISIT_CLOSED", "VISIT", map[string]any{
		"facility_id": f.facility.String(), "patient_id": f.patient.String(),
		"visit_code": "V-2026-0311-011", "chief_complaint": "Tiredness",
		"diagnoses": "type 2 diabetes", "plan": "Continue metformin", "next_review_days": 90,
	})

	var kind, label, labelBN, value, unit, permission string
	var valueNum *float64
	if err := f.db.SQL.QueryRow(`
		SELECT kind, label_en, label_bn, value, unit, value_num, needs_permission
		  FROM read.patient_timeline WHERE event_id = $1 AND item = 'review'`, e.EventID).
		Scan(&kind, &label, &labelBN, &value, &unit, &valueNum, &permission); err != nil {
		t.Fatalf("a closed visit produced no review row: %v", err)
	}
	if kind != "visit.review_due" {
		t.Errorf("kind = %q", kind)
	}
	if label == "" || labelBN == "" || labelBN == label {
		t.Errorf("labels are %q / %q", label, labelBN)
	}
	if permission != "visit.read" {
		t.Errorf("needs_permission = %q", permission)
	}
	if valueNum == nil || *valueNum != 90 {
		t.Errorf("value_num = %v, want 90", valueNum)
	}
	if value != "90" || unit != "days" {
		t.Errorf("value/unit = %q %q; the three columns must agree", value, unit)
	}

	// And the visit row itself carries the code and no number, so nothing plots it.
	var closedValue, closedUnit string
	var closedNum *float64
	if err := f.db.SQL.QueryRow(`
		SELECT value, unit, value_num FROM read.patient_timeline
		 WHERE event_id = $1 AND item = ''`, e.EventID).
		Scan(&closedValue, &closedUnit, &closedNum); err != nil {
		t.Fatal(err)
	}
	if closedValue != "V-2026-0311-011" || closedUnit != "" || closedNum != nil {
		t.Errorf("the visit.closed row is %q %q %v", closedValue, closedUnit, closedNum)
	}
}

// TestEveryPermissionTheTimelineNamesIsARealOne closes the gap left by the architecture
// boundary.
//
// A projection may not import the authorisation module — a read model that could not be
// rebuilt without it would have the dependency the wrong way round — so the permissions the
// derivation writes onto rows are string constants. A constant that drifts from the catalogue
// fails *closed*: the row is simply never readable by anybody, and nobody finds out why.
//
// So they are compared against `core.permission`, which is the catalogue rather than a second
// copy of it.
func TestEveryPermissionTheTimelineNamesIsARealOne(t *testing.T) {
	h := newHarness(t, projection.PatientTimeline{})
	for _, permission := range projection.TimelinePermissions() {
		var exists bool
		if err := h.db.SQL.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM core.permission WHERE code = $1)`, permission).
			Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("the timeline labels rows %q, which is not in core.permission; a row "+
				"needing a permission nobody can hold is a row nobody can read", permission)
		}
	}

	// And every permission actually written onto a row is one of them, so a case added later
	// with a hand-typed string is caught here rather than by a clinician seeing nothing.
	f := newTimelineFixture(t)
	f.everyKind(t)
	rows, err := f.db.SQL.Query(`SELECT DISTINCT needs_permission FROM read.patient_timeline`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	declared := map[string]bool{}
	for _, permission := range projection.TimelinePermissions() {
		declared[permission] = true
	}
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			t.Fatal(err)
		}
		if !declared[permission] {
			t.Errorf("a row needs %q, which TimelinePermissions() does not declare", permission)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
