// Package allergy is §3 step 4's checkpoint: the hard stop (CP54).
//
// # What makes this different from every other record in the system
//
// Everything else here is a fact somebody may record or not. An allergy is a **gate**, and the
// difference is not the content but what the rest of the system is allowed to do when the
// answer is missing: nothing. A patient cannot advance past the history station without one.
//
// That is why this is its own module rather than a seventh kind of medical history (ADR-0028).
// A module boundary is what stops a later change to history's write path quietly weakening a
// prescribing block — and a prescribing block that has been quietly weakened looks exactly
// like one that works, until it does not.
//
// # Where the gate actually lives
//
// Not here. Criterion 4 says the gate cannot be bypassed by any client, and a check in Go
// holds for the paths somebody remembered. The gate is a trigger on `core.queue_entry`, so it
// is met by the support script, the second client, and the one written after everybody who
// read the plan has left. What this package does is give an operator a sentence instead of a
// database error, and make satisfying the gate a five-second act.
//
// # Three ways to satisfy it, and no override
//
//	ALLERGIES_RECORDED  one or more, coded where the catalogue has the substance
//	NO_KNOWN_ALLERGY    asked, and the patient knows of none
//	UNABLE_TO_ASSESS    asked, and the answer could not be got — with the reason
//
// The third exists so there is no fourth. The unconscious patient and the child with no
// attendant are real, and the usual answer is an override button with a reason attached — but
// a gate with a way past it is a gate people learn the shape of, and the plan already names
// the risk: operators asserting NKA reflexively to clear it. `UNABLE_TO_ASSESS` is allergy
// status — somebody looked, somebody is named — and it is emphatically not a claim that there
// are none. The medication safety engine will treat the two very differently, and it can only
// do that because they are different rows rather than one row and a missing one.
package allergy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The four answers `core.allergy_status` can give. Constants rather than bare strings because
// a typo in this comparison fails open — the gate would pass — and a gate that fails open is
// worse than no gate, because somebody is relying on it.
const (
	StatusNone     = "NONE_RECORDED"
	StatusRecorded = "ALLERGIES_RECORDED"
	StatusNoKnown  = "NO_KNOWN_ALLERGY"
	StatusUnable   = "UNABLE_TO_ASSESS"
)

// Reaction is one thing an allergy did.
type Reaction struct {
	Reaction  string `json:"reaction"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	// IsEmergency is a property of the reaction, not of the severity somebody ticked.
	// Anaphylaxis is an emergency whatever the operator chose from the list beside it.
	IsEmergency bool `json:"is_emergency"`
	Ordering    int  `json:"ordering"`
}

// Allergy is one recorded reaction to one substance.
type Allergy struct {
	ID        uuid.UUID `json:"id"`
	PatientID uuid.UUID `json:"patient_id"`

	CodeSystem  string `json:"code_system,omitempty"`
	CodeVersion string `json:"code_version,omitempty"`
	Code        string `json:"code,omitempty"`
	DisplayEN   string `json:"display_en,omitempty"`
	DisplayBN   string `json:"display_bn,omitempty"`

	// Said is the substance in the patient's own words, and the only field on an uncoded
	// allergy. Kept on coded ones too: "the yellow tablet from the pharmacy near the bridge"
	// is sometimes the only thing that identifies what actually happened.
	Said string `json:"said,omitempty"`

	Reaction    string `json:"reaction"`
	ReactionEN  string `json:"reaction_en"`
	ReactionBN  string `json:"reaction_bn"`
	IsEmergency bool   `json:"is_emergency"`

	Severity  string `json:"severity"`
	Certainty string `json:"certainty"`
	Note      string `json:"note,omitempty"`

	RecordedAt    time.Time `json:"recorded_at"`
	RecordedBy    uuid.UUID `json:"recorded_by"`
	RecordedRole  string    `json:"recorded_role,omitempty"`
	RecordedVisit string    `json:"recorded_visit,omitempty"`
}

// Coded says whether the catalogue had the substance. Uncoded allergies are legitimate and
// countable, for the same reason uncoded history items are — and here the count matters more,
// because a substance the safety engine cannot match is a warning a human has to catch.
func (a Allergy) Coded() bool { return a.Code != "" }

// Assertion is a statement about allergy status made by a named person. Criterion 2.
type Assertion struct {
	ID        uuid.UUID `json:"id"`
	PatientID uuid.UUID `json:"patient_id"`
	Kind      string    `json:"kind"`
	Reason    string    `json:"reason,omitempty"`

	AssertedAt    time.Time `json:"asserted_at"`
	AssertedBy    uuid.UUID `json:"asserted_by"`
	AssertedRole  string    `json:"asserted_role,omitempty"`
	AssertedVisit string    `json:"asserted_visit,omitempty"`
}

// State is the whole answer for one patient: the status, what is recorded, and who said so.
type State struct {
	Status    string     `json:"status"`
	Allergies []Allergy  `json:"allergies"`
	Assertion *Assertion `json:"assertion,omitempty"`
}

// Satisfied reports whether this patient may advance past the history station.
//
// The same question the database trigger asks, phrased once here so a screen can grey out a
// button for the right reason rather than discovering the refusal on submit. It is not the
// enforcement — see the package comment.
func (s State) Satisfied() bool { return s.Status != StatusNone }

// Emergency is the allergies a header must lead with. Empty is not the same as safe: a patient
// with no allergy status at all has an empty list, which is why a header renders the status
// and not only this.
func (s State) Emergency() []Allergy {
	out := make([]Allergy, 0, len(s.Allergies))
	for _, item := range s.Allergies {
		if item.IsEmergency || item.Severity == "life_threatening" {
			out = append(out, item)
		}
	}
	return out
}

var (
	// ErrNotFound is an allergy or an assertion that is not in the record.
	ErrNotFound = errors.New("allergy: no such record")

	// ErrAlreadyWithdrawn is an act on something somebody has already taken back.
	ErrAlreadyWithdrawn = errors.New("allergy: that was already withdrawn")

	// ErrPartialCoding is two of the three coding fields.
	ErrPartialCoding = errors.New("allergy: a coding is a system, a version and a code, or none")

	// ErrNothingNamed is an allergy that names no substance. It would produce a warning
	// nobody can act on, and warnings nobody can act on are how a clinic learns to click past
	// the ones that matter.
	ErrNothingNamed = errors.New("allergy: name the substance, in the catalogue or in words")

	// ErrUnknownReaction is a reaction outside the vocabulary.
	ErrUnknownReaction = errors.New("allergy: that is not one of the reactions")

	// ErrReasonRequired is UNABLE_TO_ASSESS with no reason. The third state exists because it
	// is reviewable; without a reason it is a silent gap wearing a label.
	ErrReasonRequired = errors.New("allergy: say why the allergy status could not be assessed")

	// ErrReasonNotWanted is a reason attached to NO_KNOWN_ALLERGY. Refused rather than
	// ignored: an operator writing an explanation there is answering a question nobody asked,
	// and the text would never be read.
	ErrReasonNotWanted = errors.New("allergy: no known allergies needs no reason")
)

// Store reads allergies, assertions and the status.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

// InTransaction runs work against one transaction, so an event and its projection commit
// together or not at all.
func (s *Store) InTransaction(ctx context.Context,
	fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Reactions is the vocabulary a screen renders as buttons.
func (s *Store) Reactions(ctx context.Context) ([]Reaction, error) {
	rows, err := s.q.AllergyReactions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Reaction, 0, len(rows))
	for _, row := range rows {
		out = append(out, Reaction{
			Reaction: row.Reaction, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			IsEmergency: row.IsEmergency, Ordering: int(row.Ordering),
		})
	}
	return out, nil
}

// Status is the gate's question, answered by the same function the gate calls.
func (s *Store) Status(ctx context.Context, patient uuid.UUID) (string, error) {
	status, err := s.q.AllergyStatus(ctx, patient)
	if err != nil {
		return "", err
	}
	return status, nil
}

// For is everything a header and a station need about one patient.
func (s *Store) For(ctx context.Context, patient uuid.UUID) (State, error) {
	status, err := s.Status(ctx, patient)
	if err != nil {
		return State{}, err
	}
	rows, err := s.q.AllergiesForPatient(ctx, patient)
	if err != nil {
		return State{}, err
	}
	state := State{Status: status, Allergies: make([]Allergy, 0, len(rows))}
	for _, row := range rows {
		item := Allergy{
			ID: row.ID, PatientID: row.PatientID,
			DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn, Said: row.Said,
			Reaction: row.Reaction, ReactionEN: row.ReactionEn, ReactionBN: row.ReactionBn,
			IsEmergency: row.IsEmergency,
			Severity:    row.Severity, Certainty: row.Certainty, Note: row.Note,
			RecordedAt: row.RecordedAt, RecordedBy: row.RecordedBy,
			RecordedRole: row.RecordedRole,
		}
		if row.CodeSystem != nil {
			item.CodeSystem = *row.CodeSystem
		}
		if row.CodeVersion != nil {
			item.CodeVersion = *row.CodeVersion
		}
		if row.Code != nil {
			item.Code = *row.Code
		}
		if row.RecordedVisit.Valid {
			item.RecordedVisit = row.RecordedVisit.UUID.String()
		}
		state.Allergies = append(state.Allergies, item)
	}

	assertion, err := s.q.LiveAssertionForPatient(ctx, patient)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No assertion is a real answer, and not an error: the patient has allergies
		// recorded, or nobody has been asked yet.
	case err != nil:
		return State{}, err
	default:
		live := Assertion{
			ID: assertion.ID, PatientID: assertion.PatientID, Kind: assertion.Kind,
			Reason: assertion.Reason, AssertedAt: assertion.AssertedAt,
			AssertedBy: assertion.AssertedBy, AssertedRole: assertion.AssertedRole,
		}
		if assertion.AssertedVisit.Valid {
			live.AssertedVisit = assertion.AssertedVisit.UUID.String()
		}
		state.Assertion = &live
	}
	return state, nil
}

// Change is one line of the allergy change history the plan asks for.
type Change struct {
	Kind      string     `json:"kind"`
	ID        uuid.UUID  `json:"id"`
	Said      string     `json:"said,omitempty"`
	Code      string     `json:"code,omitempty"`
	Reaction  string     `json:"reaction,omitempty"`
	Severity  string     `json:"severity,omitempty"`
	Detail    string     `json:"detail,omitempty"`
	At        time.Time  `json:"at"`
	By        uuid.UUID  `json:"by"`
	ByRole    string     `json:"by_role,omitempty"`
	UndoneAt  *time.Time `json:"undone_at,omitempty"`
	UndoneBy  string     `json:"undone_by,omitempty"`
	UndoneWhy string     `json:"undone_why,omitempty"`
}

// History is everything ever said about this patient's allergies, withdrawn entries included.
//
// The withdrawn ones are the reason it exists. An allergy that was recorded and then taken
// back is a clinical event — somebody believed it and somebody else disagreed — and both halves
// are worth reading before writing a prescription.
func (s *Store) History(ctx context.Context, patient uuid.UUID) ([]Change, error) {
	rows, err := s.q.AllergyHistoryForPatient(ctx, patient)
	if err != nil {
		return nil, err
	}
	out := make([]Change, 0, len(rows))
	for _, row := range rows {
		change := Change{
			Kind: row.Kind, ID: row.ID, Said: row.Said, Reaction: row.Reaction,
			Severity: row.Severity, Detail: row.Certainty,
			At: row.At, By: row.ByUser, ByRole: row.ByRole,
			UndoneAt: row.UndoneAt, UndoneWhy: row.UndoneReason,
		}
		if row.Code != nil {
			change.Code = *row.Code
		}
		if row.UndoneBy.Valid {
			change.UndoneBy = row.UndoneBy.UUID.String()
		}
		out = append(out, change)
	}
	return out, nil
}

// OperatorRate is one operator's assertions over a window.
type OperatorRate struct {
	AssertedBy uuid.UUID `json:"asserted_by"`
	NoKnown    int       `json:"no_known"`
	Unable     int       `json:"unable"`
	Asserted   int       `json:"asserted"`
}

// Rates is the plan's own mitigation for the risk it names: operators asserting NKA reflexively
// to clear the gate.
//
// Deliberately not a rule. An officer whose patients genuinely have no allergies sits near the
// top of this list, and so does one who taps the button without asking — which is exactly why
// it belongs in front of a QA officer rather than in an automatic threshold.
func (s *Store) Rates(ctx context.Context, facility uuid.UUID, from, to time.Time) ([]OperatorRate, error) {
	rows, err := s.q.NoKnownAllergyRateByOperator(ctx, dbgen.NoKnownAllergyRateByOperatorParams{
		FacilityID: facility, AssertedAt: from, AssertedAt_2: to,
	})
	if err != nil {
		return nil, err
	}
	out := make([]OperatorRate, 0, len(rows))
	for _, row := range rows {
		out = append(out, OperatorRate{
			AssertedBy: row.AssertedBy, NoKnown: int(row.NoKnown),
			Unable: int(row.Unable), Asserted: int(row.Asserted),
		})
	}
	return out, nil
}

func encode(payload any) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding allergy event: %w", err)
	}
	return raw, nil
}

func trimmed(values ...*string) {
	for _, v := range values {
		*v = strings.TrimSpace(*v)
	}
}
