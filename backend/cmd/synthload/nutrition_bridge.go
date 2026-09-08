package main

import (
	"context"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
)

// nutritionDeriver lets station 7 write the day's intake as ordinary clinical derived values
// (CP59).
//
// The same three lines as cmd/api's, and copied rather than shared on purpose: the bridge
// exists because `nutrition` may not import `clinical`, so it belongs to whichever binary
// composes the two. A package the two commands both imported would be a fifth place that
// knows about both, which is the coupling the module boundary was drawn to prevent.
type nutritionDeriver struct {
	clinical *clinical.Service
	// counted is called for each derived value that lands, so the closing report's count of
	// measurements is the number actually in the record. Without it the four intake values a
	// recall produces are invisible to the tally, and a report that undercounts is a report
	// somebody will one day use to conclude that a load went wrong.
	counted func()
}

var _ nutrition.Deriver = (*nutritionDeriver)(nil)

func (d *nutritionDeriver) RecordDerived(ctx context.Context,
	in clinical.Recording) (clinical.Observation, error) {

	observation, _, err := d.clinical.Record(ctx, in)
	if err == nil && d.counted != nil {
		d.counted()
	}
	return observation, err
}
