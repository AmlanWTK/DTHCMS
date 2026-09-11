package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// PatientTimeline is everything known about a patient, in one chronological shape (CP37, §8).
//
// The point of building it once is that four screens do not each write their own query over
// the ledger. The physician dashboard, the timeline visualisation, the AI synthesis and the
// records chronology all read this table, so a fact is either in all four or in none — and
// "missing from one of them" is the bug nobody finds, because the one it is missing from is
// always the one somebody is looking at.
//
// The row shape is deliberately uniform and deliberately extensible: `occurred_at, category,
// kind, label, value, unit, attribution, flags`, the same for an observation, a diagnosis, a
// prescription and a document. The plan's risk note asks for exactly this — new kinds are
// rows, not columns.
//
// **Synchronous**, like the patient projection and for the same reason: a physician opens a
// record seconds after a nurse saved a vital, and a timeline a second stale is a timeline that
// makes somebody ask the patient again.
type PatientTimeline struct{}

var _ Projection = PatientTimeline{}

func (PatientTimeline) Name() string { return "patient_timeline" }

// Version 2. CP37 shipped version 1, which knew registration, corrections, merges,
// photographs and consent — five administrative things. Everything a physician opens a
// timeline *for* landed in the checkpoints since (visits at CP38, observations at CP42,
// allergy status at CP54, critical values at CP50, diet at CP59, exercise at CP60, the
// pre-consultation synthesis at CP71) and none of it reached this table: on the loaded
// synthetic cohort, 62 rows out of 6,760 events.
//
// Raising this number is the mechanism that makes that a repair rather than a gap. The runner
// refuses to advance a model built by the old derivation — `ErrStaleVersion`, naming the
// rebuild command — so a decade of history cannot quietly stay missing the new kinds.
func (PatientTimeline) Version() int { return 2 }
func (PatientTimeline) Mode() Mode   { return Synchronous }

// Handles is the list of event types that put something on a *clinical* timeline.
//
// The QUEUE_* family is deliberately absent, and the omission is a decision rather than an
// oversight — see `docs/timeline.md` §6 and the note on `queueIsNotClinical` below.
func (PatientTimeline) Handles(eventType string) bool {
	switch eventType {
	case "PATIENT_REGISTERED", "PATIENT_DEMOGRAPHICS_CORRECTED", "PATIENT_MERGED",
		"PATIENT_PHOTO_CAPTURED", "CONSENT_GRANTED", "CONSENT_REVOKED",
		// CP38: the visit and the station encounters inside it.
		"VISIT_OPENED", "VISIT_CLOSED", "ENCOUNTER_STARTED", "ENCOUNTER_FINISHED",
		// CP42: every measured value.
		"OBSERVATION_RECORDED",
		// CP54, CP50: the two safety facts.
		"ALLERGY_STATUS_ASSERTED", "CRITICAL_VALUE_ALERTED",
		"CRITICAL_VALUE_DELIVERY_ATTEMPTED",
		// CP59, CP60: lifestyle.
		"DIET_ENTRY_RECORDED", "EXERCISE_ASSESSMENT_RECORDED",
		// CP71: the pre-consultation summary, and its absence.
		"AI_SYNTHESIS_COMPLETED", "AI_SYNTHESIS_FAILED":
		return true
	}
	return false
}

// Why QUEUE_ENTERED, QUEUE_CALLED and QUEUE_LEFT are not handled.
//
// A queue entry is the clinic's own logistics: which line a patient stood in, in what
// position, for how many seconds. Nothing about the patient is asserted by it — and on this
// deployment's own data every queue event is shadowed by an ENCOUNTER_* event naming the same
// visit and the same station, seconds apart. Deriving both would put every station transition
// on the timeline twice, and the second copy would be the one that says nothing.
//
// The queue *is* worth reading. `read.station_activity` and the traffic board (CP40) are
// where it is read, by the floor supervisor whose question it answers. A physician scrubbing
// a decade of a diabetic patient's record is asking a different question, and CP74's lanes
// are the answer to that one.
//
// The waiting time is the one thing here that is arguably clinical — a patient who waited
// ninety minutes is a fact about their day. It survives: ENCOUNTER_FINISHED carries
// `seconds_at_station`, which is on the timeline, and the waiting that preceded it is a
// property of the clinic rather than of the patient.
//
// `TestTheQueueIsDeliberatelyNotOnTheClinicalTimeline` holds the decision, so that adding the
// family later is a change somebody makes on purpose rather than one that arrives with a
// copy-pasted case label.

// The permissions a row can need.
//
// Written out rather than taken from `auth`'s catalogue, and the reason is a boundary rather
// than laziness: `architecture.json` lets a projection import `platform` and `eventstore` and
// nothing else. A derivation that depended on the authorisation module would be a read model
// that could not be rebuilt without it, and the direction of that dependency is the wrong way
// round — the projection *labels* rows with the permission a reader will need; it does not ask
// whether anybody has it.
//
// A string constant that drifts from the catalogue is the obvious risk, and it is a real one:
// a permission nobody holds fails closed, so the row would simply never be readable and nobody
// would know why. `TestEveryPermissionTheTimelineNamesIsARealOne` compares these against
// `core.permission` — the catalogue itself, not a second copy of it.
const (
	permPatientReadDemographics = "patient.read.demographics"
	permPatientReadAllergies    = "patient.read.allergies"
	permObservationReadValues   = "observation.read.values"
	permVisitRead               = "visit.read"
	permAlertRead               = "alert.read"
	permAISynthesisRead         = "ai.synthesis.read"
)

// TimelinePermissions is every permission this derivation can put on a row, for the test that
// checks them against the database's catalogue.
func TimelinePermissions() []string {
	return []string{
		permPatientReadDemographics, permPatientReadAllergies, permObservationReadValues,
		permVisitRead, permAlertRead, permAISynthesisRead,
	}
}

// row is one line of the timeline. The JSON names are the derivation's parameter names.
type row struct {
	PatientID  string    `json:"patient_id"`
	FacilityID string    `json:"facility_id"`
	OccurredAt time.Time `json:"occurred_at"`
	RecordedAt time.Time `json:"recorded_at"`

	Category string `json:"category"`
	Kind     string `json:"kind"`
	LabelEN  string `json:"label_en"`
	LabelBN  string `json:"label_bn"`
	Value    string `json:"value,omitempty"`
	Unit     string `json:"unit,omitempty"`
	ValueNum string `json:"value_num,omitempty"`

	ActorID string `json:"actor_id"`
	// No actor_code: the ledger holds the user id, and the derivation resolves the employee
	// code from it. Replaying a string the ledger never held would be replaying a rendering.
	ActorRole    string `json:"actor_role"`
	ActorStation string `json:"actor_station"`
	DeviceID     string `json:"device_id,omitempty"`
	Source       string `json:"source"`

	Flags []string `json:"flags"`

	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	GlobalSeq  int64  `json:"global_seq"`
	Item       string `json:"item,omitempty"`
	Permission string `json:"needs_permission"`

	// What the derivation asks the database to look up on its way in. The bilingual names of
	// observation codes, stations, foods, measures and meals are already in `core`, reviewed
	// in one place; a second copy in Go would be a Bangla label that is right in the picker
	// and stale on the timeline, which nobody notices because the two are never open together.
	//
	// So a label ships with holes in it — "{1} started" — and `Lookups` says what fills them.
	// `read.apply_timeline` substitutes `{1}`, `{2}`, … in both languages.
	Lookups []lookup `json:"lookups,omitempty"`

	// A measured value, as the operator entered it. The conversion to the code's canonical
	// unit happens in `core.to_canonical` — the same function the write path uses (ADR-0017) —
	// so `value_num` on a rebuilt row and on a live one cannot drift apart.
	ObsCode     string `json:"obs_code,omitempty"`
	EnteredNum  string `json:"entered_num,omitempty"`
	EnteredUnit string `json:"entered_unit,omitempty"`

	// A row that carries its own number and names the registry its unit lives in.
	UnitCode string `json:"unit_code,omitempty"`
	UnitKind string `json:"unit_kind,omitempty"`
}

// lookup is one hole in a label and the registry that fills it.
type lookup struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

func (p PatientTimeline) Apply(ctx context.Context, tx pgx.Tx, e eventstore.Event) error {
	if e.PatientID == nil {
		return fmt.Errorf("timeline: %s has no patient_id", e.EventType)
	}
	var payload map[string]any
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		return fmt.Errorf("timeline: decoding %s: %w", e.EventType, err)
	}

	rows := p.rowsFor(e, payload)
	if len(rows) == 0 {
		return nil
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT read.apply_timeline($1::jsonb)`, encoded); err != nil {
		return fmt.Errorf("read.apply_timeline for %s: %w", e.EventType, err)
	}
	return nil
}

// rowsFor turns one event into the lines it puts on the timeline.
//
// One event can be several rows — a blood pressure is two numbers, a prescription is several
// drugs — and `item` distinguishes them so the unique index still holds.
func (p PatientTimeline) rowsFor(e eventstore.Event, payload map[string]any) []row {
	base := row{
		PatientID: e.PatientID.String(), FacilityID: e.Actor.FacilityID().String(),
		OccurredAt: e.OccurredAt, RecordedAt: e.RecordedAt,
		ActorID:   e.Actor.UserID().String(),
		ActorRole: e.Actor.Role(), ActorStation: e.Actor.Station(),
		Source:     string(e.Source),
		Flags:      []string{},
		EventID:    e.EventID.String(),
		EventType:  e.EventType,
		GlobalSeq:  e.GlobalSeq,
		Permission: permPatientReadDemographics,
	}
	if device := e.Actor.DeviceID(); device.String() != "00000000-0000-0000-0000-000000000000" {
		base.DeviceID = device.String()
	}

	switch e.EventType {
	case "PATIENT_REGISTERED":
		entry := base
		entry.Category = "registration"
		entry.Kind = "patient.registered"
		entry.LabelEN = "Registered at the clinic"
		entry.LabelBN = "ক্লিনিকে নিবন্ধিত"
		entry.Value = text(payload["clinical_id"])
		return []row{entry}

	case "PATIENT_DEMOGRAPHICS_CORRECTED":
		// One row per changed field. A correction that changed three things is three lines,
		// because "the date of birth was corrected" is what somebody scrolls the timeline
		// looking for and "the record was corrected" is not.
		changes, _ := payload["changes"].([]any)
		out := make([]row, 0, len(changes))
		for _, item := range changes {
			change, ok := item.(map[string]any)
			if !ok {
				continue
			}
			field := text(change["field"])
			entry := base
			entry.Category = "administrative"
			entry.Kind = "patient.corrected"
			entry.Item = field
			entry.LabelEN = "Corrected " + humanise(field)
			entry.LabelBN = humaniseBN(field) + " সংশোধন"
			entry.Value = text(change["previous"]) + " → " + text(change["current"])
			entry.Flags = []string{"corrected"}
			if truthy(payload["high_impact"]) {
				entry.Flags = append(entry.Flags, "high")
			}
			out = append(out, entry)
		}
		return out

	case "PATIENT_MERGED":
		entry := base
		entry.Category = "administrative"
		entry.Kind = "patient.merged"
		entry.LabelEN = "Merged into another record"
		entry.LabelBN = "অন্য রেকর্ডে একীভূত"
		entry.Value = text(payload["survivor_clinical_id"])
		entry.Flags = []string{"amended"}
		return []row{entry}

	case "PATIENT_PHOTO_CAPTURED":
		entry := base
		entry.Category = "document"
		entry.Kind = "patient.photo"
		entry.LabelEN = "Photograph taken"
		entry.LabelBN = "ছবি তোলা হয়েছে"
		// Deliberately no key and no URL. A timeline row is read by everyone who may read
		// the record; the image is fetched from its own endpoint, which mints a signed URL
		// per request and audits the read.
		return []row{entry}

	case "CONSENT_GRANTED", "CONSENT_REVOKED":
		kind := text(payload["consent_type"])
		entry := base
		entry.Category = "consent"
		entry.Item = kind
		if e.EventType == "CONSENT_GRANTED" {
			entry.Kind = "consent.granted"
			entry.LabelEN = "Consent given: " + humanise(kind)
			entry.LabelBN = "সম্মতি দেওয়া: " + consentBN(kind)
			entry.Value = text(payload["capture_method"])
		} else {
			entry.Kind = "consent.revoked"
			entry.LabelEN = "Consent withdrawn: " + humanise(kind)
			entry.LabelBN = "সম্মতি প্রত্যাহার: " + consentBN(kind)
			entry.Value = text(payload["reason"])
			entry.Flags = []string{"amended"}
		}
		return []row{entry}

	// -----------------------------------------------------------------------
	// CP38 — the visit, and the station encounters inside it
	// -----------------------------------------------------------------------

	case "VISIT_OPENED":
		visitType := text(payload["visit_type"])
		entry := base
		entry.Category = "visit"
		entry.Kind = "visit.opened"
		entry.LabelEN = "Visit opened — " + visitTypeEN(visitType)
		entry.LabelBN = "ভিজিট শুরু — " + visitTypeBN(visitType)
		// The visit code, not the chief complaint. The complaint is free text in the
		// patient's own words and belongs behind the clinical permissions the consultation
		// screen already holds; the code is what links this row to the rest of the visit.
		entry.Value = text(payload["visit_code"])
		entry.Permission = permVisitRead
		return []row{entry}

	case "VISIT_CLOSED":
		entry := base
		entry.Category = "visit"
		entry.Kind = "visit.closed"
		entry.LabelEN = "Visit closed"
		entry.LabelBN = "ভিজিট শেষ"
		entry.Value = text(payload["visit_code"])
		entry.Permission = permVisitRead
		out := []row{entry}

		// The review interval is its **own row** rather than a number bolted onto the one
		// above, and the reason is that `value`, `unit` and `value_num` have to agree. A row
		// reading `V-2025-0118-001 days = 90` is three columns that contradict each other,
		// and a chart reading the third while a person reads the first is how a screen comes
		// to say two things at once. `item` is what the schema gives for exactly this.
		if days, ok := number(payload["next_review_days"]); ok && days > 0 {
			review := base
			review.Category = "visit"
			review.Kind = "visit.review_due"
			review.Item = "review"
			review.LabelEN = "Next review due"
			review.LabelBN = "পরবর্তী সাক্ষাতের সময়"
			review.Value = formatNumber(days)
			review.ValueNum = review.Value
			review.Unit = "days"
			review.Permission = permVisitRead
			out = append(out, review)
		}
		return out

	case "ENCOUNTER_STARTED":
		entry := base
		entry.Category = "visit"
		entry.Kind = "encounter.started"
		entry.LabelEN = "{1} started"
		entry.LabelBN = "{1} শুরু হয়েছে"
		entry.Lookups = []lookup{{Kind: "station", Code: text(payload["station_code"])}}
		entry.Permission = permVisitRead
		return []row{entry}

	case "ENCOUNTER_FINISHED":
		outcome := text(payload["outcome"])
		entry := base
		entry.Category = "visit"
		entry.Kind = "encounter.finished"
		// The outcome is in the label rather than only in `value`, because "sent back" and
		// "finished" are what somebody scrolling for a bounced encounter is looking for, and
		// a row that reads "Vitals finished" with the word `bounced` in a value column is a
		// row that reads as its opposite at a glance.
		entry.LabelEN, entry.LabelBN = encounterOutcomeLabels(outcome)
		entry.Lookups = []lookup{{Kind: "station", Code: text(payload["station_code"])}}
		// The outcome is *not* also in `value`. `value`, `unit` and `value_num` describe one
		// quantity between them, and "completed s 606" is three columns disagreeing.
		if seconds, ok := number(payload["seconds_at_station"]); ok && seconds > 0 {
			entry.Value = formatNumber(seconds)
			entry.ValueNum = entry.Value
			entry.Unit = "s"
		}
		entry.Permission = permVisitRead
		return []row{entry}

	// -----------------------------------------------------------------------
	// CP42 — every measured value
	// -----------------------------------------------------------------------

	case "OBSERVATION_RECORDED":
		code := text(payload["code"])
		entry := base
		entry.Category = "observation"
		// The code is in the kind, lower-cased, so CP74 can give HbA1c its own lane without
		// a migration. `category` is closed and `kind` is open, and this is what that is for.
		entry.Kind = "observation." + strings.ToLower(code)
		// The name comes from the registry that owns it, in both languages. Never a string
		// invented here: `core.observation_code` is the copy Dr. Nahid reviews.
		entry.LabelEN, entry.LabelBN = "{1}", "{1}"
		entry.Lookups = []lookup{{Kind: "observation_code", Code: code}}
		// **`observation.read.values`, not `patient.read.demographics`.** §4.4 blinds
		// registration and the pharmacist to clinical values, and a timeline row is the one
		// place a value could leak past that without anybody writing a query for it.
		entry.Permission = permObservationReadValues

		// When it was true, not when it was written down. A blood pressure taken at 09:05 and
		// entered at 09:20 orders wrongly beside a promptly-entered one if the second is used.
		if when := timestamp(payload["effective_at"]); !when.IsZero() {
			entry.OccurredAt = when
		}

		if value, ok := number(payload["value"]); ok {
			entry.ObsCode = code
			entry.EnteredNum = formatNumber(value)
			entry.EnteredUnit = text(payload["unit"])
		} else {
			// text, boolean, coded and structured codes. `value_json` is deliberately not
			// rendered: a structured finding is a screen of its own, and flattening one into
			// a timeline cell produces a string nobody can read and nobody can query.
			entry.Value = firstNonEmpty(
				text(payload["value_text"]), text(payload["value_code"]),
				text(payload["value_bool"]))
		}

		// A value that replaces an earlier one. `amended` rather than `corrected`: the ledger
		// distinguishes "it was wrong" from "it was re-measured", and only the first is an
		// error rate.
		if text(payload["replaces"]) != "" {
			if text(payload["replaced_status"]) == "CORRECTED" {
				entry.Flags = []string{"corrected"}
			} else {
				entry.Flags = []string{"amended"}
			}
		}
		return []row{entry}

	// -----------------------------------------------------------------------
	// CP54, CP50 — the two safety facts
	// -----------------------------------------------------------------------

	case "ALLERGY_STATUS_ASSERTED":
		kind := text(payload["kind"])
		entry := base
		// `observation`, not `alert`. This is a finding somebody looked for and recorded in
		// their own name; `alert` in this system is the critical-value board, and a category
		// that meant two things would make its filter useless for both.
		entry.Category = "observation"
		entry.Kind = "allergy.status"
		entry.Item = kind
		entry.LabelEN, entry.LabelBN = allergyStatusLabels(kind)
		entry.Value = text(payload["reason"])
		// Deliberately *not* observation.read.values: CP54's asymmetry is the point. An
		// allergy has to reach the pharmacist and the prescription educator — the roles §4.4
		// blinds to diagnoses — because they are the last people who could catch the mistake.
		entry.Permission = permPatientReadAllergies
		if when := timestamp(payload["asserted_at"]); !when.IsZero() {
			entry.OccurredAt = when
		}
		return []row{entry}

	case "CRITICAL_VALUE_ALERTED":
		breached := text(payload["breached"])
		entry := base
		entry.Category = "alert"
		entry.Kind = "alert.critical_value"
		entry.LabelEN = "Critical value: {1}"
		entry.LabelBN = "জরুরি মান: {1}"
		entry.Lookups = []lookup{{Kind: "observation_code", Code: text(payload["code"])}}
		if value, ok := number(payload["value_num"]); ok {
			entry.Value = formatNumber(value)
			entry.ValueNum = entry.Value
		}
		// The unit as the alert recorded it, rendered from `core.unit`. The alert carries a
		// value already in the code's canonical unit — it was read from the read model, not
		// from an operator — so there is nothing to convert.
		entry.UnitCode = text(payload["unit"])
		// `critical`, and which end. 3.0 is as urgent as 25.0 and the two mean opposite
		// things, so a screen that showed only `critical` would show them identically.
		entry.Flags = []string{"critical"}
		if breached == "high" || breached == "low" {
			entry.Flags = append(entry.Flags, breached)
		}
		entry.Permission = permAlertRead
		if when := timestamp(payload["raised_at"]); !when.IsZero() {
			entry.OccurredAt = when
		}
		return []row{entry}

	case "CRITICAL_VALUE_DELIVERY_ATTEMPTED":
		entry := base
		// The one thing on this timeline that is a *message*, which is what `communication`
		// is for. An acknowledgement screen that could not say "we tried twice and nobody
		// answered" would be asking somebody to explain a delay they have no record of.
		entry.Category = "communication"
		entry.Kind = "alert.delivery_attempted"
		entry.LabelEN = "Critical value alert sent"
		entry.LabelBN = "জরুরি মান জানানো হয়েছে"
		if recipients, ok := number(payload["recipients"]); ok {
			entry.Value = formatNumber(recipients)
			// Zero is the number that matters. A delivery attempt that reached nobody is the
			// case the escalation exists for, and it is only visible if the count is on the
			// row rather than implied by its absence.
			entry.ValueNum = entry.Value
			entry.Unit = "recipients"
		}
		entry.Permission = permAlertRead
		if when := timestamp(payload["attempted_at"]); !when.IsZero() {
			entry.OccurredAt = when
		}
		return []row{entry}

	// -----------------------------------------------------------------------
	// CP59, CP60 — lifestyle
	// -----------------------------------------------------------------------

	case "DIET_ENTRY_RECORDED":
		entry := base
		entry.Category = "observation"
		entry.Kind = "diet.entry"
		entry.LabelEN = "{1}: {2}"
		entry.LabelBN = "{1}: {2}"
		entry.Lookups = []lookup{
			{Kind: "meal", Code: text(payload["meal"])},
			{Kind: "food", Code: text(payload["food_code"])},
		}
		if quantity, ok := number(payload["quantity"]); ok {
			entry.Value = formatNumber(quantity)
			entry.ValueNum = entry.Value
		}
		// The household measure, from the registry that owns its Bangla name. "two piece" is
		// the unit a 24-hour recall is actually recorded in.
		entry.UnitKind, entry.UnitCode = "food_measure", text(payload["measure_code"])
		entry.Permission = permObservationReadValues
		// The day being recalled, which is usually **yesterday**. A recall taken on Tuesday
		// about Monday's food and placed on Tuesday would make every recall a day wrong.
		if when := recallMoment(text(payload["recall_date"]), payload["eaten_at_hour"]); !when.IsZero() {
			entry.OccurredAt = when
		}
		return []row{entry}

	case "EXERCISE_ASSESSMENT_RECORDED":
		entry := base
		entry.Category = "observation"
		entry.Kind = "exercise.assessment"
		entry.LabelEN = "Exercise assessment"
		entry.LabelBN = "ব্যায়াম মূল্যায়ন"
		if minutes, ok := number(payload["walk_minutes"]); ok {
			entry.Value = formatNumber(minutes)
			entry.ValueNum = entry.Value
			entry.Unit = "min"
		}
		entry.Permission = permObservationReadValues
		if when := timestamp(payload["recorded_at"]); !when.IsZero() {
			entry.OccurredAt = when
		}
		return []row{entry}

	// -----------------------------------------------------------------------
	// CP71 — the pre-consultation summary, and its absence
	// -----------------------------------------------------------------------

	case "AI_SYNTHESIS_COMPLETED":
		entry := base
		// A generated summary is a document. It is read like one, it is superseded like one,
		// and CP110's records lane is where it will sit beside the scanned reports.
		entry.Category = "document"
		entry.Kind = "ai.synthesis.completed"
		if text(payload["state"]) == "UNCHANGED" {
			// A completion, not work skipped: the record was checked and was already current.
			entry.LabelEN = "Pre-consultation summary checked, unchanged"
			entry.LabelBN = "পরামর্শপূর্ব সারসংক্ষেপ যাচাই করা হয়েছে, অপরিবর্তিত"
		} else {
			entry.LabelEN = "Pre-consultation summary ready"
			entry.LabelBN = "পরামর্শপূর্ব সারসংক্ষেপ প্রস্তুত"
		}
		entry.Value = text(payload["agent_code"])
		entry.Permission = permAISynthesisRead
		if when := timestamp(payload["completed_at"]); !when.IsZero() {
			entry.OccurredAt = when
		}
		return []row{entry}

	case "AI_SYNTHESIS_FAILED":
		entry := base
		entry.Category = "document"
		entry.Kind = "ai.synthesis.failed"
		// On the timeline for the same reason the delivery attempt is: the physician who
		// opened a record expecting a summary needs to see that there is not one, rather
		// than an empty panel that looks like a patient with no history.
		entry.LabelEN = "Pre-consultation summary could not be produced"
		entry.LabelBN = "পরামর্শপূর্ব সারসংক্ষেপ তৈরি করা যায়নি"
		entry.Value = text(payload["failure_kind"])
		entry.Permission = permAISynthesisRead
		if when := timestamp(payload["failed_at"]); !when.IsZero() {
			entry.OccurredAt = when
		}
		return []row{entry}
	}
	return nil
}

func (PatientTimeline) Reset(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT read.reset_patient_timeline()`)
	return err
}

// --- rendering helpers ---

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func truthy(value any) bool {
	flag, ok := value.(bool)
	return ok && flag
}

// humanise turns a field or type code into something a person reads. Deliberately a plain
// transformation rather than a lookup table: a table would need an entry per new kind and
// the failure would be a blank label on a screen, which is worse than an imperfect one.
func humanise(code string) string {
	if code == "" {
		return ""
	}
	words := strings.FieldsFunc(code, func(r rune) bool { return r == '_' || r == '.' })
	for i, word := range words {
		switch word {
		case "en":
			words[i] = "(English)"
		case "bn":
			words[i] = "(Bangla)"
		case "dob":
			words[i] = "date of birth"
		case "ai":
			words[i] = "AI"
		}
	}
	joined := strings.Join(words, " ")
	return strings.ToUpper(joined[:1]) + joined[1:]
}

// The Bangla labels for the handful of codes that reach a timeline today. Dr. Nahid's review
// is D-24's; an unknown code falls back to the code itself rather than to an empty cell.
var fieldBN = map[string]string{
	"name_en": "নাম (ইংরেজি)", "name_bn": "নাম (বাংলা)", "sex": "লিঙ্গ",
	"birth_date": "জন্ম তারিখ", "dob_precision": "তারিখের নির্ভুলতা", "dob_source": "তারিখের উৎস",
	"phone_primary": "মোবাইল নম্বর", "phone_secondary": "অন্য নম্বর",
	"division": "বিভাগ", "district": "জেলা", "upazila": "উপজেলা",
	"address_line": "ঠিকানা", "postcode": "পোস্ট কোড",
}

var consentTypeBN = map[string]string{
	"care": "চিকিৎসা", "communication": "কল ও এসএমএস", "research": "নামহীন গবেষণা",
	"ai_processing": "এআই সহায়তা", "outreach": "কমিউনিটি ফলো-আপ",
}

func humaniseBN(field string) string {
	if label, ok := fieldBN[field]; ok {
		return label
	}
	return field
}

func consentBN(kind string) string {
	if label, ok := consentTypeBN[kind]; ok {
		return label
	}
	return kind
}

// --- values from a payload ---

// number reads a numeric payload field. JSON has one number type, so this is float64 or
// nothing; "or nothing" is the case that matters, because an absent `walk_minutes` and a
// recorded zero are different answers and a default of 0 conflates them.
func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	}
	return 0, false
}

// formatNumber writes a float back as the shortest string that reads back identically. The
// derivation must be deterministic: a rebuild that formatted 69.9 as 69.900000 the second time
// would produce a table that differs from the one it replaced, field by field.
func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// timestamp reads an RFC 3339 field. Zero when it is absent or unparseable, and the caller
// keeps the envelope's own time rather than substituting one — a row placed at the zero
// instant is a row at the start of the timeline, which is worse than a row placed by the
// envelope.
func timestamp(value any) time.Time {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return time.Time{}
	}
	when, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return when
}

// recallMoment places a 24-hour recall on the day it is *about*, in the clinic's calendar.
//
// The hour is optional by design — a patient who cannot remember when they ate still
// remembers the meal, and requiring it would produce invented hours — so a recall with no hour
// sits at the start of its day rather than at an hour nobody said.
func recallMoment(recallDate string, hour any) time.Time {
	if recallDate == "" {
		return time.Time{}
	}
	day, err := time.ParseInLocation("2006-01-02", recallDate, dhaka)
	if err != nil {
		return time.Time{}
	}
	if at, ok := number(hour); ok && at >= 0 && at <= 23 {
		day = day.Add(time.Duration(at) * time.Hour)
	}
	return day
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// --- the small closed enums ---
//
// These are the codes with no registry of their own: a handful of values fixed by a CHECK
// constraint or by a validator in the event registry, not a table somebody maintains. Anything
// that *is* a table — observation codes, stations, foods, measures, meals — is looked up at
// derivation time instead, so there is one copy of it (see `lookup`).
//
// Bangla throughout is the engineer's, flagged for Dr. Nahid's review like every other
// clinical label in the system. An unknown code falls back to something readable rather than
// to an empty cell, because the attribution invariant refuses a blank label and a blank label
// is a worse bug than an English one.

var visitTypeLabels = map[string][2]string{
	"new":               {"new patient", "নতুন রোগী"},
	"follow_up":         {"follow-up", "ফলো-আপ"},
	"outreach_referral": {"outreach referral", "কমিউনিটি রেফারেল"},
}

func visitTypeEN(code string) string {
	if label, ok := visitTypeLabels[code]; ok {
		return label[0]
	}
	return humanise(code)
}

func visitTypeBN(code string) string {
	if label, ok := visitTypeLabels[code]; ok {
		return label[1]
	}
	return humanise(code)
}

// encounterOutcomeLabels reads the station's name out of `{1}`.
//
// `bounced` is its own sentence rather than "finished" with a word in a value column, because
// §14.2 counts rework and a bounce that reads as a completion at a glance makes rework
// invisible — which is the one number a quality gate exists to produce.
func encounterOutcomeLabels(outcome string) (string, string) {
	switch outcome {
	case "skipped":
		return "{1} skipped", "{1} বাদ দেওয়া হয়েছে"
	case "bounced":
		return "{1} sent back for rework", "{1} থেকে সংশোধনের জন্য ফেরত"
	case "patient_left":
		return "Patient left during {1}", "{1} চলাকালে রোগী চলে গেছেন"
	default:
		return "{1} finished", "{1} শেষ হয়েছে"
	}
}

// allergyStatusLabels renders CP54's two assertions.
//
// Neither is "no allergies recorded". The whole point of the event is that somebody looked and
// said so in their own name — and `UNABLE_TO_ASSESS` says the opposite of "none", which is
// exactly the reading a blank field invites.
func allergyStatusLabels(kind string) (string, string) {
	switch kind {
	case "NO_KNOWN_ALLERGY":
		return "No known allergies", "কোনো জানা অ্যালার্জি নেই"
	case "UNABLE_TO_ASSESS":
		return "Allergy status could not be assessed", "অ্যালার্জির তথ্য জানা যায়নি"
	default:
		return "Allergy status: " + humanise(kind), "অ্যালার্জির অবস্থা: " + kind
	}
}
