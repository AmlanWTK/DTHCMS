package main

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
)

// The value overlays behind CP74's chart, wired where knowing about every module is the job.
//
// `patient` may not import `clinical` — architecture.json, and the reason is written into
// `patient`'s own handler config: a patient is the thing other modules are *about*, and it
// must not know which of them exist. So `patient` declares [patient.SeriesReader] and this
// is the one place the two are introduced.
//
// The translation is not ceremony. It is where the observation model's vocabulary — a
// status, a source, a recorder — becomes the chart's: a flag, a provenance, an attribution.
// Doing it here rather than in either module is what keeps the shape of one from leaking
// into the other.
type timelineSeriesBridge struct {
	store *clinical.Store
}

var _ patient.SeriesReader = (*timelineSeriesBridge)(nil)

func (b *timelineSeriesBridge) Series(ctx context.Context, patientID, facility uuid.UUID,
	codes []string, from, to time.Time, limit int) (
	map[string][]patient.SeriesPoint, map[string]string, error) {

	points, err := b.store.SeriesForCodes(ctx, patientID, facility, codes, from, to, limit)
	if err != nil {
		return nil, nil, err
	}

	byCode := make(map[string][]patient.SeriesPoint, len(codes))
	units := make(map[string]string, len(codes))
	// A code whose points disagree about their unit gets no series unit. Empty rather than
	// the first one seen: labelling a mixed series with one unit is a chart stating
	// something no record says, and a patient weighed in kg at the clinic and in lb at home
	// is exactly the case that produces one.
	mixed := make(map[string]bool, len(codes))

	for _, point := range points {
		flags := []string{}
		switch point.Status {
		case clinical.Corrected:
			// The two are kept apart deliberately. "This was wrong and has been replaced"
			// and "this was right and has been re-measured" are different facts, and a
			// chart that conflated them would draw every follow-up reading as an error.
			flags = append(flags, "corrected")
		case clinical.Superseded:
			flags = append(flags, "superseded")
		}

		byCode[point.Code] = append(byCode[point.Code], patient.SeriesPoint{
			ObservationID: point.ID,
			At:            point.At,
			ValueNum:      point.Value,
			Unit:          point.Unit,
			Flags:         flags,
			// No employee code: the observation model holds the recorder's **user id**, which
			// is the durable fact, and CP61's directory is what turns it into a name. A code
			// invented here would be a second rendering of the same person that drifts from
			// the directory's the day somebody is re-coded.
			SpanAttribution: patient.SpanAttribution{
				ActorID:      point.RecordedBy,
				ActorRole:    point.RecordedRole,
				ActorStation: point.StationCode,
				Source:       string(point.Source),
				RecordedAt:   point.RecordedAt,
			},
		})

		if existing, seen := units[point.Code]; !seen {
			units[point.Code] = point.Unit
		} else if existing != point.Unit {
			mixed[point.Code] = true
		}
	}
	for code := range mixed {
		units[code] = ""
	}
	return byCode, units, nil
}
