package clinical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Station 5's structured examination (CP51, §3 step 5).
//
// # Why the answers are a table and not an enum in Go
//
// Criterion 2 asks that every finding be coded and queryable for research. Until this
// checkpoint a `coded` observation meant only that `value_code` was not empty — so `absent`,
// `Absent` and `not felt` were three findings as far as any query was concerned, and the
// research extract would have held all three with no way to tell they were the same one.
//
// The vocabulary is therefore data (`core.observation_answer`), a database trigger enforces
// it, and this file is how a station app gets hold of it: one fetch, rendered as buttons,
// which is also the only way a ten-site monofilament test plus pulses plus deformity fits into
// the two minutes criterion 1 asks for.
//
// # Laterality
//
// In the code, never in a column: `DP_PULSE_LEFT` and `DP_PULSE_RIGHT` are two codes. A `side`
// column would have been tidier and would have made `WHERE code = 'DP_PULSE'` silently mean
// "either foot" — which, for a diabetic foot, is the one question nobody may be vague about.

// Answer is one option a coded observation may take.
type Answer struct {
	Code      string `json:"code"`
	ValueCode string `json:"value_code"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	Ordering  int    `json:"ordering"`
	// Normal marks the answer that means nothing abnormal. A screen can pre-select it and a
	// report can count abnormal findings without a list of magic strings in three places.
	Normal bool `json:"is_normal"`
}

// Answers is every vocabulary, in clinical order, for a station app to render as buttons.
//
// Whole rather than per code, and fetched once: the examination screen needs eleven of these
// at the moment the patient sits down, and eleven round trips on a clinic connection is the
// difference between a two-minute examination and a five-minute one.
func (s *Store) Answers(ctx context.Context) ([]Answer, error) {
	rows, err := s.q.ObservationAnswers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Answer, 0, len(rows))
	for _, row := range rows {
		out = append(out, Answer{
			Code: row.Code, ValueCode: row.ValueCode,
			DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			Ordering: int(row.Ordering), Normal: row.IsNormal,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// The monofilament
// ---------------------------------------------------------------------------

// MonofilamentSites are the ten places a 10 g monofilament is applied, in the order an
// examiner works: the toes, then across the metatarsal heads, then the arch and the heel.
//
// Ten rather than the four some protocols use. Four is faster and misses the early loss that
// is the whole reason for screening — and the order matters because an examiner who has to
// hunt for the next site on the screen looks at the screen instead of the foot.
var MonofilamentSites = []string{
	"hallux", "toe_3", "toe_5",
	"mth_1", "mth_3", "mth_5",
	"medial_arch", "lateral_arch",
	"heel", "dorsum",
}

// Monofilament is what one foot's test records.
//
// Which sites were not felt, not merely how many: early neuropathy at the hallux and a
// forefoot that has lost protective sensation are different appointments, and a single
// "abnormal" loses that difference permanently.
type Monofilament struct {
	// Felt maps a site to whether the filament was felt there. Every site must be present —
	// a missing one is an examiner who was interrupted, and recording it as "not felt" would
	// invent a finding while recording it as "felt" would hide one.
	Felt map[string]bool `json:"felt"`
	// NotTested marks a foot that could not be examined, with the reason in `note` on the
	// observation. An amputation, a dressing, a patient who would not take their sock off.
	NotTested bool `json:"not_tested,omitempty"`
}

var (
	// ErrExamShape is a structured finding whose payload is not the shape its code requires.
	ErrExamShape = errors.New("clinical: that is not the shape this finding takes")
)

// LostProtectiveSensation is the finding the whole test exists to produce.
//
// One site missed is within the noise of a hurried examination; two is the threshold every
// published protocol uses, and it is the line the risk category is drawn from.
const LostProtectiveSensation = 2

// ParseMonofilament reads and checks one foot's test.
func ParseMonofilament(raw json.RawMessage) (Monofilament, error) {
	var out Monofilament
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return Monofilament{}, fmt.Errorf("%w: %v", ErrExamShape, err)
	}
	if out.NotTested {
		if len(out.Felt) != 0 {
			return Monofilament{}, fmt.Errorf(
				"%w: a foot that was not tested has no sites", ErrExamShape)
		}
		return out, nil
	}
	if len(out.Felt) != len(MonofilamentSites) {
		return Monofilament{}, fmt.Errorf("%w: %d sites of %d",
			ErrExamShape, len(out.Felt), len(MonofilamentSites))
	}
	for _, site := range MonofilamentSites {
		if _, present := out.Felt[site]; !present {
			return Monofilament{}, fmt.Errorf("%w: %s is missing", ErrExamShape, site)
		}
	}
	return out, nil
}

// Missed counts the sites where the filament was not felt.
func (m Monofilament) Missed() int {
	missed := 0
	for _, felt := range m.Felt {
		if !felt {
			missed++
		}
	}
	return missed
}

// LostSensation reports loss of protective sensation. A foot that was not tested is not a
// foot with sensation: the honest answer is "we do not know", which the caller distinguishes
// by checking NotTested first.
func (m Monofilament) LostSensation() bool {
	return !m.NotTested && m.Missed() >= LostProtectiveSensation
}

// ---------------------------------------------------------------------------
// The risk category
// ---------------------------------------------------------------------------

// FootRisk is the IWGDF category for one foot, derived from the findings.
//
// Derived rather than typed, and that is the point of a structured examination: the category
// falls out of the findings, so two examiners who record the same foot the same way cannot
// disagree about its risk. An examiner who could type it directly would be back to an opinion
// with a dropdown in front of it.
type FootRisk string

const (
	FootRiskVeryLow  FootRisk = "very_low"
	FootRiskLow      FootRisk = "low"
	FootRiskModerate FootRisk = "moderate"
	FootRiskHigh     FootRisk = "high"
)

// FootRiskVersion is the version of the categorisation, stored with every derived value.
//
// Not decoration. The categories were renumbered between IWGDF's 2015 and 2019 guidance, and a
// stored risk with no version cannot afterwards be told apart from one computed under the
// other.
const FootRiskVersion = "iwgdf-2019.1"

// FootFindings is what the category is computed from: the four facts, and nothing else.
type FootFindings struct {
	// LostSensation is the monofilament, or vibration sense, or an absent ankle reflex.
	LostSensation bool
	// PoorCirculation is either pedal pulse absent.
	PoorCirculation bool
	// Deformity is any deformity other than none — including a previous amputation, which is
	// also counted below.
	Deformity bool
	// PriorUlcerOrAmputation is a history of either, on this foot.
	PriorUlcerOrAmputation bool
}

// Categorise is IWGDF's 2019 stratification, written out.
//
// The order is the point: a history of ulceration or amputation is category 3 whatever else
// the foot looks like today, because the foot that ulcerated once is the foot that ulcerates
// again — and a well-healed foot examines normally.
func Categorise(f FootFindings) FootRisk {
	switch {
	case f.PriorUlcerOrAmputation:
		return FootRiskHigh
	case f.LostSensation && f.PoorCirculation,
		f.LostSensation && f.Deformity,
		f.PoorCirculation && f.Deformity:
		return FootRiskModerate
	case f.LostSensation || f.PoorCirculation:
		return FootRiskLow
	default:
		return FootRiskVeryLow
	}
}

// inputs renders the four facts as the numeric inputs the ledger stores beside a derived
// value. Zeroes and ones rather than prose, because the question a reviewer asks six months
// later is "what did the rule actually see", and four flags answer it exactly.
func (f FootFindings) inputs() map[string]float64 {
	flag := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	return map[string]float64{
		"lost_sensation":            flag(f.LostSensation),
		"poor_circulation":          flag(f.PoorCirculation),
		"deformity":                 flag(f.Deformity),
		"prior_ulcer_or_amputation": flag(f.PriorUlcerOrAmputation),
	}
}

// Side is which foot, eye or artery a lateral finding belongs to.
type Side string

const (
	Left  Side = "LEFT"
	Right Side = "RIGHT"
)

// footFindingsFor reads one foot's current findings out of the record.
//
// Reads what is *current* rather than what this form just wrote, because a foot examination
// is often finished across two encounters — the pulses at the station, the ulcer grade when
// the dressing comes off — and a category computed from half of it would be wrong in the
// direction that matters.
func footFindingsFor(coded map[string]string, structured map[string]json.RawMessage, side Side) (FootFindings, bool) {
	suffix := "_" + string(side)
	found := false
	var f FootFindings

	if raw, ok := structured["MONOFILAMENT"+suffix]; ok {
		if test, err := ParseMonofilament(raw); err == nil {
			found = true
			f.LostSensation = f.LostSensation || test.LostSensation()
		}
	}
	if value, ok := coded["VIBRATION"+suffix]; ok {
		found = true
		f.LostSensation = f.LostSensation || value == "absent" || value == "reduced"
	}
	if value, ok := coded["ANKLE_REFLEX"+suffix]; ok {
		found = true
		f.LostSensation = f.LostSensation || value == "absent"
	}
	for _, code := range []string{"DP_PULSE" + suffix, "PT_PULSE" + suffix} {
		if value, ok := coded[code]; ok {
			found = true
			f.PoorCirculation = f.PoorCirculation || value == "absent"
		}
	}
	if value, ok := coded["FOOT_DEFORMITY"+suffix]; ok {
		found = true
		f.Deformity = f.Deformity || value != "none"
		f.PriorUlcerOrAmputation = f.PriorUlcerOrAmputation || value == "amputation"
	}
	if value, ok := coded["FOOT_ULCER"+suffix]; ok {
		found = true
		f.PriorUlcerOrAmputation = f.PriorUlcerOrAmputation || value != "grade_0"
	}
	return f, found
}

// currentCodedTx is every code's current coded and structured value for one patient, read on
// the caller's transaction so a derivation sees what the batch just wrote.
func (s *Store) currentCodedTx(ctx context.Context, q *dbgen.Queries, patientID, facility uuid.UUID) (
	map[string]string, map[string]json.RawMessage, error) {

	rows, err := s.forPatientTx(ctx, q, patientID, facility, 0)
	if err != nil {
		return nil, nil, err
	}
	coded := map[string]string{}
	structured := map[string]json.RawMessage{}
	// The rows arrive newest first, so the first sighting of a code is its current value.
	for _, row := range rows {
		if row.Status != Active {
			continue
		}
		if row.ValueCode != "" {
			if _, seen := coded[row.Code]; !seen {
				coded[row.Code] = row.ValueCode
			}
		}
		if len(row.ValueJSON) > 0 {
			if _, seen := structured[row.Code]; !seen {
				structured[row.Code] = row.ValueJSON
			}
		}
	}
	return coded, structured, nil
}

// AnswerSets groups the vocabulary by code, for a caller that wants one code's options.
func AnswerSets(answers []Answer) map[string][]Answer {
	out := map[string][]Answer{}
	for _, answer := range answers {
		out[answer.Code] = append(out[answer.Code], answer)
	}
	for code := range out {
		sort.SliceStable(out[code], func(i, j int) bool {
			return out[code][i].Ordering < out[code][j].Ordering
		})
	}
	return out
}

// checkStructured validates a structured finding against the shape its code requires.
//
// A switch rather than a registry column, and deliberately: there is one structured code
// today, its shape is a clinical statement rather than a data type, and a JSON Schema in a
// column would put that statement somewhere nobody reviewing the examination would look.
// When there is a third, this becomes a map and the comment moves with it.
func checkStructured(code string, raw []byte) error {
	switch code {
	case "MONOFILAMENT_LEFT", "MONOFILAMENT_RIGHT":
		if len(raw) == 0 {
			return fmt.Errorf("%w: a monofilament test records its sites", ErrExamShape)
		}
		_, err := ParseMonofilament(raw)
		return err
	default:
		return nil
	}
}
