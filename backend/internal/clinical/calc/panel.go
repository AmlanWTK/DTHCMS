package calc

import "errors"

// The anthropometry panel (CP45, P-4).
//
// # Why a panel and not four calls
//
// The station screen shows BMI, its class, BMR and ideal weight *while the operator is still
// typing*, and it has to show them in a half-typed state: a height and no weight yet, a
// weight the operator is in the middle of correcting. Four separate calls, each with its own
// idea of what "not yet" means, is four places for the screen to disagree with itself — and
// the screen that matters is the phone, where four half-answers is what an operator actually
// sees for the first fifteen seconds of every entry.
//
// So the composition itself is a unit: one input, one answer, one rule about missing values.
// And because the composition is a unit, it is a unit the parity harness can hold — the
// phone and the server compute the same panel from the same numbers, which is criterion (2)
// of this checkpoint.
//
// # What "missing" means here
//
// Nothing. Not an error, not a zero, not a NaN. A value the panel cannot compute is absent
// from the result and named in Needs, because "we have not measured their height" is the
// sentence the operator needs, and it is a different sentence from "that height cannot be
// right" — which is what the refusals in this package are for.

// PanelInput is what an anthropometry screen has as the operator types. Every measurement is
// a pointer because "not typed yet" is a state the screen spends most of its life in, and a
// zero would be a measurement.
type PanelInput struct {
	WeightKg *float64
	HeightCm *float64
	WaistCm  *float64
	HipCm    *float64
	// AgeYears and Sex come from the patient record, never from the screen. BMR moves with
	// both, and a field an operator could edit is a field that changes a clinical number.
	AgeYears float64
	Sex      Sex
	// Asian picks the obesity cut-offs. True for this clinic; see Classify.
	Asian bool
}

// Panel is everything the screen shows. Absent values are absent, not zero.
type Panel struct {
	BMI          *Result      `json:"bmi,omitempty"`
	Class        ObesityClass `json:"obesity_class,omitempty"`
	ClassVersion string       `json:"obesity_class_version,omitempty"`
	BMR          *Result      `json:"bmr,omitempty"`
	IBW          *Result      `json:"ideal_body_weight,omitempty"`
	WHR          *Result      `json:"whr,omitempty"`

	// Needs names the measurements each absent value is waiting for, keyed by the value.
	// The screen turns it into a sentence; this library does not compose sentences, for the
	// same reason the traffic board does not (CP40) — the reader may be reading Bangla.
	Needs map[string][]string `json:"needs,omitempty"`

	// Refused names the values an equation would not produce even though its inputs were
	// present — a height below the range Devine covers, a sex the equation has no
	// coefficient for. Distinct from Needs: one sends the operator to a tape measure, the
	// other tells them the number does not exist for this patient.
	//
	// The value is a reason *code*, not a message. The phone renders it in the language it
	// is being read in, and the four codes are the same four the TypeScript library returns
	// — which is what lets the parity fixtures compare the two panels field by field.
	Refused map[string]string `json:"refused,omitempty"`
}

// PanelVersion is bumped when the *composition* changes — which formula feeds which slot,
// or what counts as missing. Not when one of the underlying formulas is revised: those carry
// their own versions, and a stored value names the one it used.
const PanelVersion = "1.0.0"

// AnthroPanel computes everything derivable from a partly-filled anthropometry form.
func AnthroPanel(in PanelInput) Panel {
	out := Panel{Needs: map[string][]string{}, Refused: map[string]string{}}

	missing := func(key string, need ...string) {
		out.Needs[key] = need
	}
	refused := func(key string, err error) {
		if err != nil {
			out.Refused[key] = ReasonOf(err)
		}
	}

	switch {
	case in.WeightKg == nil && in.HeightCm == nil:
		missing("bmi", "weight", "height")
	case in.WeightKg == nil:
		missing("bmi", "weight")
	case in.HeightCm == nil:
		missing("bmi", "height")
	default:
		result, err := BMI(*in.WeightKg, *in.HeightCm)
		if err != nil {
			refused("bmi", err)
			break
		}
		out.BMI = &result
		class, version, err := Classify(result.Value, in.Asian)
		if err != nil {
			refused("obesity_class", err)
			break
		}
		out.Class, out.ClassVersion = class, version
	}

	switch {
	case in.WeightKg == nil && in.HeightCm == nil:
		missing("bmr", "weight", "height")
	case in.WeightKg == nil:
		missing("bmr", "weight")
	case in.HeightCm == nil:
		missing("bmr", "height")
	default:
		result, err := BMRMifflin(*in.WeightKg, *in.HeightCm, in.AgeYears, in.Sex)
		if err != nil {
			refused("bmr", err)
			break
		}
		out.BMR = &result
	}

	if in.HeightCm == nil {
		missing("ideal_body_weight", "height")
	} else if result, err := IdealBodyWeight(*in.HeightCm, in.Sex); err != nil {
		refused("ideal_body_weight", err)
	} else {
		out.IBW = &result
	}

	switch {
	case in.WaistCm == nil && in.HipCm == nil:
		missing("whr", "waist", "hip")
	case in.WaistCm == nil:
		missing("whr", "waist")
	case in.HipCm == nil:
		missing("whr", "hip")
	default:
		result, err := WHR(*in.WaistCm, *in.HipCm)
		if err != nil {
			refused("whr", err)
			break
		}
		out.WHR = &result
	}

	if len(out.Needs) == 0 {
		out.Needs = nil
	}
	if len(out.Refused) == 0 {
		out.Refused = nil
	}
	return out
}

// ReasonOf names why an equation refused, in the same four words the TypeScript library
// uses. A code rather than a sentence: the sentence belongs to whoever knows what language
// the operator reads.
func ReasonOf(err error) string {
	switch {
	case errors.Is(err, ErrNotPositive):
		return "not_positive"
	case errors.Is(err, ErrOutOfRange):
		return "out_of_range"
	case errors.Is(err, ErrSexUnsupported):
		return "sex_unsupported"
	case errors.Is(err, ErrMissingInput):
		return "missing_input"
	default:
		return "missing_input"
	}
}
