package calc

import "math"

// The composite lifestyle risk score (CP58, §3 step 3, §12).
//
// # What this is, and what it is not
//
// §12's cohorting is done on behaviour, and a single number lets a clinic ask "did the people who
// scored badly in March move" without re-deriving four separate things every time. That is the
// whole use, and it is a research and triage convenience rather than a clinical instrument.
//
// **This formula is not validated and is not published anywhere.** D-26 lists the instruments and
// the composite formula as an open decision requiring clinical approval, and the version string
// says so — `0.1.0-proposed` — so that a score computed today is identifiable forever as one
// computed before anybody agreed to the arithmetic. Nothing here should be reported to a patient
// or used in a study without that agreement.
//
// # Why missing domains are not zero
//
// The obvious implementation scores four domains out of four and treats an unanswered one as
// nothing. It is wrong in the worst direction: the patient who answered nothing scores best, and
// the operator who ran the whole questionnaire has produced a worse-looking record than the one
// who ran none of it. So the score is the **mean of the domains that were actually assessed**, and
// it reports how many those were — a score from three domains is a different number from a score
// from four and must never be compared with one as though it were the same measurement.
//
// Below three domains it refuses. A "lifestyle risk" computed from one answer is a number with a
// name that promises far more than it holds.

// LifestyleRiskVersion is the composite's version. The `-proposed` suffix is load-bearing: it is
// what makes a score computed before D-26 is answered identifiable afterwards.
const LifestyleRiskVersion = "0.1.0-proposed"

// MinimumLifestyleDomains is how many of the four have to be present. Three, because two domains
// averaged is not a composite of anything.
//
// Exported so the API can report it: a client told only that there is no score has to guess how
// many more questions to ask, and guessing means re-implementing a decision that lives here.
const MinimumLifestyleDomains = 3

// LifestyleInputs is what the score is computed from. Every field is optional; the absent ones are
// left out of the mean rather than counted as good news.
type LifestyleInputs struct {
	// PackYears is the smoking burden, already derived (CP43's `pack_years`).
	PackYears *float64
	// AuditC is the AUDIT-C total, 0–12. The one validated instrument this clinic may run today,
	// because the WHO publishes it for free use.
	AuditC *float64
	// SleepHours is a usual night.
	SleepHours *float64
	// ActiveMinutesWeek is a usual week.
	ActiveMinutesWeek *float64
}

// LifestyleRisk is the composite, 0 (nothing to act on) to 100.
//
// Every band below is a proposal. They are written as named constants rather than inline numbers
// so that the day a clinician changes one, the diff says which judgement changed.
func LifestyleRisk(in LifestyleInputs) (Result, int, error) {
	var parts []float64

	if in.PackYears != nil {
		if *in.PackYears < 0 || *in.PackYears > 300 {
			return Result{}, 0, ErrOutOfRange
		}
		// Thirty pack-years is the burden at which the major cohort studies stop distinguishing
		// degrees of harm. Above it the score is already at its maximum, which is honest: the
		// difference between forty and sixty pack-years is not a difference this number can
		// usefully express.
		parts = append(parts, clamp01(*in.PackYears/packYearsAtFullRisk))
	}

	if in.AuditC != nil {
		if *in.AuditC < 0 || *in.AuditC > auditCMaximum {
			return Result{}, 0, ErrOutOfRange
		}
		parts = append(parts, *in.AuditC/auditCMaximum)
	}

	if in.SleepHours != nil {
		if *in.SleepHours < 0 || *in.SleepHours > 24 {
			return Result{}, 0, ErrOutOfRange
		}
		// Distance from the band, not distance from a point. Seven hours and nine hours are both
		// fine, and a formula that punished either for not being eight would be measuring
		// tidiness rather than sleep.
		var away float64
		switch {
		case *in.SleepHours < sleepGoodLow:
			away = sleepGoodLow - *in.SleepHours
		case *in.SleepHours > sleepGoodHigh:
			away = *in.SleepHours - sleepGoodHigh
		}
		parts = append(parts, clamp01(away/sleepHoursAtFullRisk))
	}

	if in.ActiveMinutesWeek != nil {
		if *in.ActiveMinutesWeek < 0 || *in.ActiveMinutesWeek > 5000 {
			return Result{}, 0, ErrOutOfRange
		}
		// The WHO's 150 minutes a week of moderate activity. Meeting it scores zero; none of it
		// scores one; in between is linear, which is a simplification the guideline does not make
		// and this formula does.
		parts = append(parts, clamp01(1-*in.ActiveMinutesWeek/activeMinutesAtNoRisk))
	}

	if len(parts) < MinimumLifestyleDomains {
		return Result{}, len(parts), ErrInputsIncomplete
	}

	var sum float64
	for _, part := range parts {
		sum += part
	}
	return Result{
		Value:   math.Round(sum/float64(len(parts))*1000) / 10,
		Unit:    "1",
		Formula: "lifestyle_risk",
		Version: LifestyleRiskVersion,
	}, len(parts), nil
}

// The bands. Every one is a proposal (D-26), and they are named so that a change to one reads as a
// change to a judgement rather than as a change to a number.
const (
	packYearsAtFullRisk   = 30.0
	auditCMaximum         = 12.0
	sleepGoodLow          = 7.0
	sleepGoodHigh         = 9.0
	sleepHoursAtFullRisk  = 4.0
	activeMinutesAtNoRisk = 150.0
)

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
