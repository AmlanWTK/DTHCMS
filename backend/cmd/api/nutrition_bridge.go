package main

import (
	"context"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
)

// nutritionDeriver lets station 7 write the day's intake as ordinary clinical derived values
// (CP59).
//
// The same shape as `assessmentDeriver`, and for the same reason: `nutrition` holds exactly one
// opinion about clinical writes, which is that it does not perform them itself.
//
// The alerts a recording can raise are dropped here. A critical-value alert is about a measurement
// outside a clinical band; a day's calorie total computed against a food table nobody has approved
// is not a number to shout at a consultant about.
type nutritionDeriver struct {
	clinical *clinical.Service
}

var _ nutrition.Deriver = (*nutritionDeriver)(nil)

func (d *nutritionDeriver) RecordDerived(ctx context.Context,
	in clinical.Recording) (clinical.Observation, error) {

	observation, _, err := d.clinical.Record(ctx, in)
	return observation, err
}
