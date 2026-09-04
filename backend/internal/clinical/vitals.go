package clinical

import "context"

// What is normal, as opposed to what is possible (CP49).
//
// Three different things a number can be outside, and they are not the same:
//
//   - **plausibility** (CP42/CP46) — outside it, the number is a typing error;
//   - **the reference range** — outside it, the number is worth a second look;
//   - **critical** (CP50) — outside it, somebody has to act now.
//
// Conflating the second with the third is how a clinic ends up ignoring both. This file is
// only the second: it says what is ordinary for a patient of this age, and the station app
// turns the field amber. Nothing here alerts anybody.

// ReferenceRange is what is normal for one code and one kind of patient.
type ReferenceRange struct {
	Code        string   `json:"code"`
	Sex         string   `json:"sex,omitempty"`
	MinAgeYears *float64 `json:"min_age_years,omitempty"`
	MaxAgeYears *float64 `json:"max_age_years,omitempty"`
	// Low and High are canonical. Either may be absent: a pulse oximeter reading has a floor
	// and no ceiling worth naming, and an invented upper bound is a bound that flags healthy
	// patients until staff stop reading the flag.
	Low    *float64 `json:"low,omitempty"`
	High   *float64 `json:"high,omitempty"`
	NoteEN string   `json:"note_en,omitempty"`
	NoteBN string   `json:"note_bn,omitempty"`
	// Approved says whether a clinician has signed off. The seeded ranges are proposals, and
	// the plan says so: "normal ranges per age band — clinical confirmation".
	Approved bool `json:"approved"`
}

// Ranges is every reference range, most specific first, for the station app.
func (s *Store) Ranges(ctx context.Context) ([]ReferenceRange, error) {
	rows, err := s.q.ReferenceRanges(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ReferenceRange, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReferenceRange{
			Code:        row.Code,
			Sex:         stringOf(row.Sex),
			MinAgeYears: optionalNumeric(row.MinAgeYears),
			MaxAgeYears: optionalNumeric(row.MaxAgeYears),
			Low:         optionalNumeric(row.Low),
			High:        optionalNumeric(row.High),
			NoteEN:      row.NoteEn,
			NoteBN:      row.NoteBn,
			Approved:    row.ApprovedAt != nil,
		})
	}
	return out, nil
}
