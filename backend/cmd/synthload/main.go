// Command synthload appends a synthetic clinic into a running local database.
//
// # Why this exists
//
// cmd/synthgen produces a clinically-reviewed cohort and writes it to a file. cmd/devseed
// produces the people who can sign in. Between the two there was nothing: a developer who
// ran both still met an empty register, an empty traffic board and an empty alert list, and
// every screen in the system reads as though it works because there is nothing in it to be
// wrong about. An empty system is not a system anybody has tested; it is a system whose
// defects are all still ahead of it.
//
// So this walks a cohort through the clinic the way a morning does: registrations spread
// over the past year or two, follow-up visits with measurements that form a series rather
// than a point, and a clinic that is half-way through today — some patients waiting, some at
// a station, a few with a value that should make somebody's phone ring.
//
// # Everything goes through the ledger
//
// Not one row is written to read.* here. Every fact this command creates is an event
// appended by the same domain service the API calls — patient.Service.Register,
// visit.Service.Open/Arrive/Depart/Close, clinical.Service.RecordBatch — with the same
// synchronous projections attached inside the same transaction. That is not fastidiousness.
// A loader that reached into the read models would produce a database whose screens and
// whose ledger disagree, and the disagreement would surface either as `migrate verify`
// failing or, far worse, as a projection rebuild silently deleting the data somebody has
// been demonstrating from for a month. The ledger is the record; a read model is an opinion
// about it, and this command is not entitled to one.
//
// The cost is real and worth stating: sixty patients is a few thousand transactions and
// takes tens of seconds rather than the second a COPY would take. That is the price of the
// data being true.
//
// # It is not idempotent, and it says so
//
// Running it twice appends a second cohort rather than reconciling with the first. Making it
// idempotent would mean deriving every event id from the seed so that a re-run replayed —
// and half the write paths here (an encounter start, a queue entry) create a row *before*
// the ledger absorbs the duplicate event, so a replay would leave rows with no events behind
// them. That is precisely the divergence the previous section exists to prevent, so the
// honest answer is to refuse the second run instead: `loader.guard` stops when the database
// already holds a cohort, and DTHCMS_SYNTHLOAD_ANYWAY=1 is how somebody who wants two says so.
//
// # What it will not do
//
// It refuses when the environment is production, and it refuses when the database holds
// patients it did not register. The second is the load-bearing one, and it is the same
// failure devseed guards: not somebody running this against production on purpose, but a
// local DTHCMS_POSTGRES_URL still pointing at a shared database from yesterday's debugging
// session. Sixty invented patients in a real register is a data-quality incident that takes
// a week to unpick.
//
// # What it does not load, and why
//
// Coded medical history, counselling sessions, consent records and prescriptions. The first
// two go together: a coded diabetes diagnosis puts the patient behind CP57's counselling
// gate, and satisfying that would mean inventing seven conversations nobody had. Consent has
// no template in a fresh database and nothing in the system authors one, so the registrations
// carry a paper reference instead. Prescriptions are simply not something to invent: a
// register full of them is a register somebody will one day quote a dose from.
//
// # It is also the first thing to run the write path as dthcms_app
//
// Every database test in this repository connects as the schema owner; the API connects as
// `dthcms_app`. Driving the domain services from a command found two privileges that role had
// never been given, either of which stopped the API dead — see migration 00051, which is where
// the argument about that class of defect is written down.
//
// Usage:
//
//	synthload -n 60                    generate sixty patients and load them
//	synthload -in cohort.ndjson        load a cohort synthgen already wrote
//	synthload -seed 42                 the same seed always gives the same people
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/synthetic"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "synthload:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		count    = flag.Int("n", 60, "how many patients to generate; ignored with -in")
		seed     = flag.Int64("seed", 1, "seed; the same seed and profile always give the same people")
		profile  = flag.String("profile", "internal/testdata/profile.v1.json", "path to the case-mix profile")
		in       = flag.String("in", "", "load this NDJSON cohort instead of generating one")
		asOfFlag = flag.String("as-of", "", "anchor date for the generated cohort, YYYY-MM-DD (default: today, UTC)")
		today    = flag.Int("today", 16, "how many patients are part-way through today's clinic")
	)
	flag.Parse()

	ctx := context.Background()

	rt, err := platform.Boot(ctx, platform.Options{Service: "synthload", NeedsDB: true, NoTelemetry: true})
	if err != nil {
		return err
	}
	defer rt.Close()

	if rt.Config.Env.IsProduction() {
		return errors.New("refused: this invents patients, and the environment is production")
	}

	// The guards run before the cohort is generated, not after. Generating is cheap, but the
	// message somebody needs when they have pointed this at the wrong database is the refusal
	// — and a refusal printed under an unrelated complaint about a flag is a refusal nobody
	// reads.
	loader, err := newLoader(ctx, rt, *seed)
	if err != nil {
		return err
	}
	if err := loader.guard(ctx); err != nil {
		return err
	}

	cohort, err := cohort(*in, *profile, *count, *seed, *asOfFlag)
	if err != nil {
		return err
	}
	if *today < 0 || *today > len(cohort) {
		return fmt.Errorf("-today must be between 0 and the cohort size (%d), got %d", len(cohort), *today)
	}

	started := time.Now()
	if err := loader.load(ctx, cohort, *today); err != nil {
		return err
	}
	loader.report(time.Since(started))
	return nil
}

// cohort is the population to load: generated here, or read from what synthgen wrote.
//
// Both paths exist because they answer different questions. Generating is what somebody
// wanting a working local stack does and needs no intermediate file; reading a file is what
// somebody investigating a defect does, because "patient 4,183 of seed 42" is a citation
// only if the cohort on disk is the cohort that was loaded.
func cohort(path, profilePath string, count int, seed int64, asOfFlag string) ([]synthetic.Patient, error) {
	if path != "" {
		return readNDJSON(path)
	}
	if count < 1 {
		return nil, fmt.Errorf("-n must be at least 1, got %d", count)
	}

	asOf := time.Now().UTC().Truncate(24 * time.Hour)
	if asOfFlag != "" {
		parsed, err := time.Parse(time.DateOnly, asOfFlag)
		if err != nil {
			return nil, fmt.Errorf("-as-of must be YYYY-MM-DD: %w", err)
		}
		asOf = parsed
	}

	p, err := synthetic.LoadProfile(profilePath)
	if err != nil {
		return nil, err
	}
	return synthetic.New(p, seed, asOf).Generate(count), nil
}
