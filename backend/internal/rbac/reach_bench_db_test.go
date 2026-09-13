package rbac_test

import (
	"context"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
)

// What the reach costs, measured rather than assumed.
//
// ADR-0036 accepts a query per request on the scoped routes, on the explicit condition that
// CP93's load testing measures it. This is not that load test — it is one connection against
// a local Postgres with a handful of rows — but it is the number that says whether the
// design is in the right order of magnitude, and it is here rather than in a spreadsheet so
// that the next person can re-run it.
//
// Run it with:
//
//	go test ./internal/rbac/ -run TestTheReachQueryCosts -v
func TestTheReachQueryCosts(t *testing.T) {
	c := newClinic(t)
	visit := c.openVisit(t, c.patient)
	c.queue(t, visit, c.patient, string(auth.StationNutrition), "in_service")
	ctx := context.Background()
	station := string(auth.StationNutrition)

	const runs = 200
	for _, probe := range []struct {
		name string
		call func() error
	}{
		{"write reach, hit", func() error {
			_, err := c.reach.WriteReachAt(ctx, c.st.facility, station, c.patient)
			return err
		}},
		{"read reach, hit", func() error {
			_, err := c.reach.ReadReachAt(ctx, c.st.facility, station, c.patient)
			return err
		}},
		{"read reach, miss (another station)", func() error {
			_, err := c.reach.ReadReachAt(ctx, c.st.facility, string(auth.StationExercise), c.patient)
			return err
		}},
	} {
		// One warm run so the first measurement is not the statement being prepared.
		if err := probe.call(); err != nil {
			t.Fatalf("%s: %v", probe.name, err)
		}
		start := time.Now()
		for i := 0; i < runs; i++ {
			if err := probe.call(); err != nil {
				t.Fatalf("%s: %v", probe.name, err)
			}
		}
		each := time.Since(start) / runs
		t.Logf("%-36s %v per call over %d calls", probe.name, each.Round(time.Microsecond), runs)
	}
}
