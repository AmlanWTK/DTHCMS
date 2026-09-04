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

// Version 1. The derivation covers what exists at CP37: registration, corrections, merges,
// photographs and consent. Visits, observations, diagnoses and prescriptions arrive with the
// checkpoints that create them, and each will raise this number — which is what forces a
// rebuild rather than leaving a decade of history missing the new kind.
func (PatientTimeline) Version() int { return 1 }
func (PatientTimeline) Mode() Mode   { return Synchronous }

func (PatientTimeline) Handles(eventType string) bool {
	switch eventType {
	case "PATIENT_REGISTERED", "PATIENT_DEMOGRAPHICS_CORRECTED", "PATIENT_MERGED",
		"PATIENT_PHOTO_CAPTURED", "CONSENT_GRANTED", "CONSENT_REVOKED":
		return true
	}
	return false
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
		Permission: "patient.read.demographics",
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
