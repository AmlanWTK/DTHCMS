package clinical

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Impossible inputs, refused while the patient is still in the room (CP46, §3 step 2).
//
// # Three kinds of wrong
//
// A height of 15 cm is **impossible** and no client may store it. A height of 205 cm is
// **implausible but possible** — rare, real, and a typing error far more often than not — so
// it is accepted with an explicit confirmation that is recorded. And 12 cm of height gained
// since March in an adult is neither: the value is ordinary and the **change** is not.
//
// Conflating the second with the first is the classic failure of validation in clinical
// software. Rules tuned to catch typing errors end up refusing exactly the patients who most
// need recording, and then staff learn to work around the system — which costs more than the
// typing errors did.
//
// # Where this runs
//
// Here, authoritatively, on every write including the batch. The station app runs the same
// rules locally so the warning appears as the operator types, and the rules reach it through
// `GET /v1/observations/plausibility`. A client that skipped the check, or an old build, or a
// request assembled by hand, all still hit this one.
//
// # Why a rule can be missing
//
// It cannot, for a measured code — an invariant refuses that. But a LAB code has no rule and
// is checked only against the registry's own band, which is deliberate: reference intervals
// for a lab analyte are CP5x's problem, and a plausibility rule pretending to be one would be
// worse than none.

// Rule is one plausibility rule as the database holds it.
type Rule struct {
	Code string `json:"code"`
	// Sex, MinAgeYears and MaxAgeYears narrow the rule. Empty and nil mean "anyone".
	Sex         string   `json:"sex,omitempty"`
	MinAgeYears *float64 `json:"min_age_years,omitempty"`
	MaxAgeYears *float64 `json:"max_age_years,omitempty"`

	AbsoluteMin *float64 `json:"absolute_min,omitempty"`
	AbsoluteMax *float64 `json:"absolute_max,omitempty"`

	PlausibleMin *float64 `json:"plausible_min,omitempty"`
	PlausibleMax *float64 `json:"plausible_max,omitempty"`

	MaxIncrease *float64 `json:"max_increase,omitempty"`
	MaxDecrease *float64 `json:"max_decrease,omitempty"`

	MaxIncreasePerDay *float64 `json:"max_increase_per_day,omitempty"`
	MaxDecreasePerDay *float64 `json:"max_decrease_per_day,omitempty"`

	NoteEN string `json:"note_en,omitempty"`
	NoteBN string `json:"note_bn,omitempty"`
	// Approved says whether a clinician has signed off on these numbers. The seeded bands
	// are proposals, and an interface that showed them as settled would be overstating what
	// anybody has agreed to.
	Approved bool `json:"approved"`
}

// The two refusals this file raises. Distinct, because they send an operator to two
// different places: one to the field, one to a confirmation.
var (
	// ErrImpossible is a value outside the absolute band. Nothing stores it.
	ErrImpossible = errors.New("clinical: that value is outside the possible range")
	// ErrNeedsConfirmation is a value inside the absolute band and outside the plausible
	// one, or a change larger than the rule allows. Storable, once somebody says it is real.
	ErrNeedsConfirmation = errors.New("clinical: that value is unusual and needs confirming")
)

// Breach is what a rule refused and why, in the numbers the operator needs to see.
//
// Carried on the error rather than pre-composed into a sentence, for the same reason CP40's
// board sends facts: the person reading it may be reading Bangla, and "check the number" is
// not a message — "height is usually between 135 and 200 cm" is.
type Breach struct {
	Code string
	// Kind is `low`, `high`, `rose` or `fell`.
	Kind string
	// Value is what was entered, canonical.
	Value float64
	// Limit is the band edge or the change limit it crossed.
	Limit float64
	// Unit is the canonical unit, so the numbers can be read.
	Unit string
	// Previous and Since describe the earlier value, for a delta breach.
	Previous *float64
	Since    *time.Time
	// Hard says whether this is impossible (no confirmation will store it) or merely
	// implausible.
	Hard bool
	// NoteEN and NoteBN are the rule's own explanation, when it has one.
	NoteEN string
	NoteBN string
}

func (b Breach) Error() string {
	return fmt.Sprintf("%s %s: %g against %g %s", b.Code, b.Kind, b.Value, b.Limit, b.Unit)
}

// Unwrap lets a handler ask which of the two refusals this is.
func (b Breach) Unwrap() error {
	if b.Hard {
		return ErrImpossible
	}
	return ErrNeedsConfirmation
}

// checkPlausible is the whole of CP46 on the write path.
//
// Ordered so the first message an operator sees is the most actionable: an impossible value
// before an implausible one, and a value before a delta — because a value that is itself
// wrong makes its delta wrong too, and telling somebody their weight changed by 300 kg when
// they typed 372 instead of 72 sends them to the wrong question.
func (s *Service) checkPlausible(ctx context.Context, q *dbgen.Queries, spec Code,
	in Recording, canonical float64, facts patientFacts, patientID, facility uuid.UUID) error {

	if in.Value == nil {
		return nil
	}
	rule, found, err := s.store.ruleFor(ctx, spec.Code, string(facts.sex), facts.ageYears)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	breach := func(kind string, limit float64, hard bool) error {
		return Breach{
			Code: spec.Code, Kind: kind, Value: canonical, Limit: limit,
			Unit: spec.CanonicalUnit, Hard: hard,
			NoteEN: rule.NoteEN, NoteBN: rule.NoteBN,
		}
	}

	if rule.AbsoluteMin != nil && canonical < *rule.AbsoluteMin {
		return breach("low", *rule.AbsoluteMin, true)
	}
	if rule.AbsoluteMax != nil && canonical > *rule.AbsoluteMax {
		return breach("high", *rule.AbsoluteMax, true)
	}

	// Everything below is confirmable. A confirmed write skips it — but only after the
	// absolute band above, which no confirmation can pass.
	if in.Confirmed {
		return nil
	}

	if rule.PlausibleMin != nil && canonical < *rule.PlausibleMin {
		return breach("low", *rule.PlausibleMin, false)
	}
	if rule.PlausibleMax != nil && canonical > *rule.PlausibleMax {
		return breach("high", *rule.PlausibleMax, false)
	}

	return s.checkDelta(ctx, q, spec, in, canonical, rule, patientID, facility)
}

// checkDelta compares against the patient's own last value of this code.
//
// The comparison a band cannot make. A weight of 58 kg is ordinary; 58 kg in somebody who
// was 72 kg in March is either a serious clinical event or the wrong patient, and both are
// worth stopping for.
func (s *Service) checkDelta(ctx context.Context, q *dbgen.Queries, spec Code, in Recording,
	canonical float64, rule Rule, patientID, facility uuid.UUID) error {

	if rule.MaxIncrease == nil && rule.MaxDecrease == nil &&
		rule.MaxIncreasePerDay == nil && rule.MaxDecreasePerDay == nil {
		return nil
	}

	previous, at, found, err := s.store.lastValueTx(ctx, q, patientID, facility, spec.Code)
	if err != nil || !found {
		return err
	}
	// A correction replaces the value it is correcting, so comparing against it would refuse
	// every correction of a mistyped number — the exact case the operator is trying to fix.
	if in.Replaces != nil {
		return nil
	}

	change := canonical - previous
	days := in.EffectiveAt.Sub(at).Hours() / 24

	breach := func(kind string, limit float64) error {
		earlier, when := previous, at
		return Breach{
			Code: spec.Code, Kind: kind, Value: canonical, Limit: limit,
			Unit: spec.CanonicalUnit, Previous: &earlier, Since: &when, Hard: false,
			NoteEN: rule.NoteEN, NoteBN: rule.NoteBN,
		}
	}

	if change > 0 && rule.MaxIncrease != nil && change > *rule.MaxIncrease {
		return breach("rose", *rule.MaxIncrease)
	}
	if change < 0 && rule.MaxDecrease != nil && -change > *rule.MaxDecrease {
		return breach("fell", *rule.MaxDecrease)
	}

	// A rate needs a gap to be a rate. Under a day, two measurements are the same visit and
	// their difference is a re-measurement, not a trend.
	if days < 1 {
		return nil
	}
	if change > 0 && rule.MaxIncreasePerDay != nil && change/days > *rule.MaxIncreasePerDay {
		return breach("rose", *rule.MaxIncreasePerDay*days)
	}
	if change < 0 && rule.MaxDecreasePerDay != nil && -change/days > *rule.MaxDecreasePerDay {
		return breach("fell", *rule.MaxDecreasePerDay*days)
	}
	return nil
}

// --- the store's half ---

// Rules is every plausibility rule, for the station app to evaluate locally.
//
// Whole rather than per code, for the same reason the registry is: a tablet fetches it once
// on start-up and warns the operator for the rest of the clinic session, offline.
func (s *Store) Rules(ctx context.Context) ([]Rule, error) {
	rows, err := s.q.PlausibilityRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(rows))
	for _, row := range rows {
		out = append(out, ruleOf(row))
	}
	return out, nil
}

// ruleFor is the most specific rule for one patient and one code, resolved by the database
// so that the client's copy of the resolution rule and the server's cannot disagree.
func (s *Store) ruleFor(ctx context.Context, code, sex string, ageYears float64) (Rule, bool, error) {
	row, err := s.q.PlausibilityRuleFor(ctx, dbgen.PlausibilityRuleForParams{
		PCode: code, PSex: sex, PAgeYears: numericOf(ageYears),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// No rule for this code. Real, and not an error: a lab analyte is checked against
		// the registry's own band and nothing narrower, because a plausibility rule
		// pretending to be a reference interval would be worse than none.
		return Rule{}, false, nil
	}
	if err != nil {
		return Rule{}, false, err
	}
	return ruleOf(row), true, nil
}

// lastValueTx is the patient's previous value of one code, through the caller's transaction
// so that a batch compares against the record rather than against a stale snapshot.
func (s *Store) lastValueTx(ctx context.Context, q *dbgen.Queries,
	patientID, facility uuid.UUID, code string) (float64, time.Time, bool, error) {

	rows, err := q.ObservationHistoryForCode(ctx, dbgen.ObservationHistoryForCodeParams{
		PatientID: patientID, FacilityID: facility, Code: code, Limit: 1,
	})
	if err != nil || len(rows) == 0 {
		return 0, time.Time{}, false, err
	}
	value, ok := numericValue(rows[0].ValueNum)
	if !ok {
		return 0, time.Time{}, false, nil
	}
	return value, rows[0].EffectiveAt, true, nil
}

func ruleOf(row dbgen.CorePlausibilityRule) Rule {
	out := Rule{
		Code:              row.Code,
		Sex:               stringOf(row.Sex),
		MinAgeYears:       optionalNumeric(row.MinAgeYears),
		MaxAgeYears:       optionalNumeric(row.MaxAgeYears),
		AbsoluteMin:       optionalNumeric(row.AbsoluteMin),
		AbsoluteMax:       optionalNumeric(row.AbsoluteMax),
		PlausibleMin:      optionalNumeric(row.PlausibleMin),
		PlausibleMax:      optionalNumeric(row.PlausibleMax),
		MaxIncrease:       optionalNumeric(row.MaxIncrease),
		MaxDecrease:       optionalNumeric(row.MaxDecrease),
		MaxIncreasePerDay: optionalNumeric(row.MaxIncreasePerDay),
		MaxDecreasePerDay: optionalNumeric(row.MaxDecreasePerDay),
		NoteEN:            row.NoteEn,
		NoteBN:            row.NoteBn,
		Approved:          row.ApprovedAt != nil,
	}
	return out
}

func stringOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optionalNumeric(n pgtype.Numeric) *float64 {
	value, ok := numericValue(n)
	if !ok || math.IsNaN(value) {
		return nil
	}
	return &value
}
