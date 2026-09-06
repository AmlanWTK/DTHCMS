package main

import (
	"context"

	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
)

// assessmentDeriver lets the assessment module write the composite lifestyle score as an ordinary
// clinical derived value (CP58).
//
// `assessment` may import `clinical` (architecture.json), so this bridge is not there to cross a
// wall. It is there to keep the assessment module holding exactly one opinion about clinical
// writes — that it does not perform them itself — and to make that testable: a test can watch what
// was derived without a clinical service behind it.
//
// The alerts a recording can raise are dropped here, and that is a decision rather than an
// oversight. A critical-value alert is raised on a *measurement* outside a clinical band; the
// composite is a research and triage number on a formula nobody has approved yet (D-26), and
// shouting at a consultant about it would be shouting about arithmetic.
type assessmentDeriver struct {
	clinical *clinical.Service
}

var _ assessment.Deriver = (*assessmentDeriver)(nil)

func (d *assessmentDeriver) RecordDerived(ctx context.Context,
	in clinical.Recording) (clinical.Observation, error) {

	observation, _, err := d.clinical.Record(ctx, in)
	return observation, err
}
