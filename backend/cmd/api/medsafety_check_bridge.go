package main

import (
	"context"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
)

// A prescription safety check, on its way to the audit trail (CP78, `SAFETY_CHECK_RUN`).
//
// `medsafety` may not import `audit` (architecture.json), for the reason every bridge in this
// binary exists: a module able to write audit entries directly grows a second, differently-shaped
// way of describing what happened, and the trail's value is that there is exactly one.
//
// # Why the rule versions are in the entry
//
// **Criterion 5: historical checks must be reproducible against the rule versions used at the
// time.** `POST /v1/patients/{id}/safety-check` takes an `at` and re-runs against that instant's
// ruleset, which answers the question only as long as the version rows are still there and still
// say what they said. They will be — CP77 keeps every version and freezes published ones — but
// "the table still has it" is a weaker guarantee than "the hash chain has it", and the
// prescription being questioned months later is exactly the case where the weaker one is not
// enough. So the exact `rule@version` list travels into the chain.
//
// # Why there is no clinical content in it
//
// This entry says a check happened, on how many medicines, and what it concluded. It does not say
// which medicines, which diagnoses, which allergens, or what the eGFR was. Two reasons, and the
// second is the one that decided it:
//
//  1. the audit trail has different access controls from the clinical record, and a drug list in
//     it is a prescription readable by everyone who may read the trail;
//  2. the prescription is where that belongs, and CP80 records it there with attribution. A
//     second copy here would be a second truth about what was prescribed, and the two would
//     eventually disagree.
//
// The patient is named in the entry's subject, like every clinical entry, and nowhere in its
// details.
type medsafetyCheckAuditBridge struct {
	recorder *audit.Recorder
}

var _ medsafety.CheckAuditor = (*medsafetyCheckAuditBridge)(nil)

func (b *medsafetyCheckAuditBridge) SafetyCheckRun(ctx context.Context,
	run medsafety.SafetyCheckRun) error {

	// "MET-RENAL-30@1" for each. A flat list of strings rather than a list of objects, because
	// the thing a person reads this row for is "was it checked against version 1 or version 2",
	// and a rendered audit line has to be able to print it.
	versions := make([]string, 0, len(run.Evaluated))
	for _, v := range run.Evaluated {
		versions = append(versions, v.Rule+"@"+itoa(v.Version))
	}

	actor := run.ActorID
	patient := run.PatientID
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       "medication_safety.check_run",
		FacilityID: run.FacilityID,
		ActorID:    &actor,
		ActorCode:  run.ActorCode,
		ActorRole:  run.ActorRole,
		PatientID:  &patient,
		At:         run.At,
		Details: map[string]any{
			"verdict":    string(run.Verdict),
			"items":      run.ItemCount,
			"findings":   run.FindingCount,
			"blocks":     run.BlockCount,
			"unverified": run.UnverifiedCount,
			"uncovered":  run.UncoveredCount,
			"rules":      run.RulesLive,
			// Criterion 5. The whole point of the entry.
			"versions": versions,
		},
	})
	return err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
