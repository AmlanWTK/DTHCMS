package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/synthetic"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// SeedDevice is the device id every event this command writes is attributed to.
//
// A fixed uuid, and deliberately not a row in core.device. The attribution envelope [R-03]
// requires a device on every clinical event, and there are only two honest answers for a
// command line: invent an enrolment — a device row, a live Ed25519 key, a status of
// `active` — or name something that is visibly not a tablet. The first is worse than it
// looks: a key pair nobody holds the private half of is still a credential-shaped row that
// somebody will one day treat as evidence a real device was present.
//
// So this is the second answer, and it follows the precedent eventstore.SystemUserID set for
// the escalation sweep: a reserved v4 uuid with a legible tail, belonging to no device, which
// reads in a query and an audit export as exactly what it is. Every value in the register
// that was typed by nobody carries it, and one `WHERE actor_device_id` finds all of them.
var SeedDevice = uuid.MustParse("00000000-0000-4000-8000-00005EED0AD0")

// Override is how somebody who means it gets past the guards below.
const Override = "DTHCMS_SYNTHLOAD_ANYWAY"

// operator is one of devseed's accounts, doing the part of the morning that is theirs.
//
// Six rather than one, and it costs almost nothing: every screen in this system shows who
// entered a value, and a register in which one account entered all of it exercises none of
// the attribution the clinic will actually read. It also makes the role-scoped read paths
// meaningful — an observation recorded under CLINICAL_ASSISTANT and one recorded under
// PHYSICIAN are different rows to CP61's directory and to the audit trail.
type operator struct {
	Code   string
	Role   string
	UserID uuid.UUID
}

// The staff this command needs, by the role they act in. The codes are devseed's, because
// the whole point is that the names on the screens are names somebody can also sign in as.
var staffWanted = []struct{ code, role string }{
	{"REG01", "REGISTRATION"},
	{"CA01", "CLINICAL_ASSISTANT"},
	{"NUT01", "NUTRITIONIST"},
	{"EXE01", "EXERCISE"},
	{"DOC01", "PHYSICIAN"},
}

type loader struct {
	log      *slog.Logger
	pool     *pgxpool.Pool
	clock    *clock.Fixed
	rng      *rand.Rand
	facility uuid.UUID

	patients      *patient.Service
	visits        *visit.Service
	clinical      *clinical.Service
	allergies     *allergy.Service
	exercise      *exercise.Service
	exerciseStore *exercise.Store
	nutrition     *nutrition.Service
	// ai is the AI gateway's store, and the only thing this command uses it for is the
	// synthetic-subject register. See the note at the call site in load.go.
	ai *ai.Store

	staff map[string]operator
	tally tally
	// alreadyHere is how many patients the register held before this run, counted by the
	// guard. It is the offset for the invented telephone numbers and identity numbers, and
	// it exists because deriving those from a per-run counter made the second cohort collide
	// with the first: two unrelated people with one national identity number, refused by the
	// duplicate matcher, which is exactly right and not what anybody wanted to discover.
	alreadyHere int
}

// tally is what the closing report counts. Kept as it goes rather than queried at the end,
// because "how many did it write" and "how many are in the database" are different numbers
// and conflating them is how a loader that silently skipped a third of its work looks fine.
type tally struct {
	registered   int
	skipped      int
	visitsClosed int
	visitsOpen   int
	encounters   int
	observations int
	queued       int
	alerts       int
	allergies    int
	exercise     int
	nutrition    int
	// registeredSynthetic counts the entries in `core.ai_synthetic_subject`. Counted separately
	// from `registered` because they can differ — a register insert may fail without failing the
	// registration — and a load that quietly produced sixty patients the free tier refuses to
	// summarise should say so in its own report rather than at the first synthesis.
	registeredSynthetic int
}

func newLoader(ctx context.Context, rt *platform.Runtime, seed int64) (*loader, error) {
	pool := rt.DB.Pool

	var facility uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT core.default_facility()`).Scan(&facility); err != nil {
		return nil, fmt.Errorf("finding the facility; has migrate run? %w", err)
	}

	// The ledger, wired exactly as cmd/api wires it: the append path with CP25's synchronous
	// projections inside the transaction. Asynchronous projections are absent here as they are
	// there — they belong to cmd/projector, and read.station_activity therefore stays empty
	// until it runs, which is the same thing that happens to a real clinic day.
	//
	// Its clock is the real one while every *service* below shares a movable one. That split is
	// deliberate: occurred_at is when the clinic did the thing, and these events claim months
	// ago; recorded_at is when this server heard about it, and that is now. Backdating both
	// would be a lie about the second, and the ledger keeps them apart precisely so that a
	// value entered late is distinguishable from one entered at the bedside (§7.2).
	events := eventstore.New(eventstore.Config{
		Pool:        pool,
		Clock:       clock.Real{},
		Synchronous: projection.NewSyncSet(projection.Default),
	})

	// One clock for every service, moved forward as the loader walks the cohort's calendar.
	// Each service reads it for the event's occurred_at *and* for the row it writes, so moving
	// it is the only way to produce a visit that closed last March rather than a visit that
	// claims to have closed last March while its row says today.
	moment := clock.NewFixed(time.Now().UTC())

	pepper, err := base64.StdEncoding.DecodeString(rt.Config.Secrets.IdentifierPepper)
	if err != nil {
		return nil, fmt.Errorf("DTHCMS_IDENTIFIER_PEPPER is not valid base64: %w", err)
	}
	ring, err := secretRing(rt.Config.Secrets.KeyID, rt.Config.Secrets.Key)
	if err != nil {
		return nil, err
	}
	sealer, err := patient.NewIdentifierSealer(pepper, ring)
	if err != nil {
		return nil, fmt.Errorf("building the identifier sealer: %w", err)
	}

	patientStore := patient.NewStore(pool)
	// The duplicate matcher is attached for the same reason the API attaches it: a cohort
	// loaded past the check is a cohort that never exercised it. It blocks only at 0.95, so
	// what it will actually catch here is this command generating the same person twice —
	// which is worth knowing about rather than worth loading.
	matcher := patient.NewMatcher(patientStore, sealer)

	exerciseStore := exercise.NewStore(pool)
	clinicalService := clinical.NewService(clinical.NewStore(pool), events, moment)

	l := &loader{
		log:      rt.Logger,
		pool:     pool,
		clock:    moment,
		rng:      rand.New(rand.NewSource(seed)), //nolint:gosec // invented people, not cryptography
		facility: facility,
		patients: patient.NewService(patient.ServiceConfig{
			Store: patientStore, Events: events, Sealer: sealer, Clock: moment,
			Duplicates: matcher.AsCheck(),
		}),
		visits:        visit.NewService(visit.NewStore(pool), events, moment),
		clinical:      clinicalService,
		allergies:     allergy.NewService(allergy.NewStore(pool), events, moment),
		exercise:      exercise.NewService(exerciseStore, events, moment),
		exerciseStore: exerciseStore,
		ai:            ai.NewStore(pool),
		staff:         map[string]operator{},
	}

	// Station 7 writes the day's totals as clinical derived values through the same bridge
	// cmd/api builds, so a recall that lands here produces the same four ENERGY/PROTEIN/CARB/
	// FAT rows a real one would. Assembled after the struct rather than inside it because the
	// bridge counts into the loader's own tally.
	l.nutrition = nutrition.NewService(nutrition.NewStore(pool), events, moment).
		WithDeriver(&nutritionDeriver{
			clinical: clinicalService,
			counted:  func() { l.tally.observations++ },
		})

	// No realtime notifier and no board feed, unlike cmd/api. Nothing is listening: the alerts
	// this raises are recorded as delivered to nobody, which is the truth and is also what the
	// escalation sweep will find if cmd/worker is running — an alert nobody acknowledged is
	// exactly the state /alerts exists to show.

	if err := l.findStaff(ctx); err != nil {
		return nil, err
	}
	return l, nil
}

// secretRing builds the key ring the identifier sealer needs. The loader carries no previous
// keys: it is sealing identifiers it has just invented, so there is nothing old to open.
func secretRing(keyID, key string) (*secretbox.Ring, error) {
	current, err := secretbox.ParseKey(keyID, key)
	if err != nil {
		return nil, err
	}
	return secretbox.NewRing(current)
}

// findStaff resolves devseed's accounts, and refuses when one is missing.
//
// Refusing here rather than falling back to whoever is available is the point. A loader that
// quietly attributed the whole morning to one account because CA01 had been renamed would
// produce a database that looks right and teaches the wrong thing about every attribution
// screen in the system.
func (l *loader) findStaff(ctx context.Context) error {
	var missing []string
	for _, want := range staffWanted {
		var id uuid.UUID
		// The role grant is checked, not assumed. This is the same pair of facts the
		// authorisation engine confirms on a request — the person is active, and the hat
		// they are wearing is one they hold — and checking them is what makes the principal
		// built in `as` a verified one rather than an asserted one.
		err := l.pool.QueryRow(ctx, `
			SELECT u.id
			  FROM core.app_user u
			  JOIN core.user_role ur ON ur.user_id = u.id AND ur.revoked_at IS NULL
			  JOIN core.role r ON r.id = ur.role_id
			 WHERE u.facility_id = $1 AND u.employee_code = $2
			   AND u.status = 'active' AND r.code = $3`,
			l.facility, want.code, want.role).Scan(&id)
		if err != nil {
			missing = append(missing, want.code+" ("+want.role+")")
			continue
		}
		l.staff[want.role] = operator{Code: want.code, Role: want.role, UserID: id}
	}
	if len(missing) > 0 {
		return fmt.Errorf("refused: %s cannot be found holding those roles. Run devseed first",
			strings.Join(missing, ", "))
	}
	return nil
}

// as returns a context that will produce the actor for one person at one station.
//
// # Why this is not forging an actor
//
// eventstore.Actor has no exported fields and no public constructor, so nothing outside that
// package can write down who made an event — that is CP24's whole guarantee. What this does
// instead is what the authorisation engine does: it puts a **principal** on the context, and
// leaves eventstore.ActorFrom to build the actor from it. Every field of that principal is
// something this command checked against the same rows the middleware chain reads — the user
// exists, is active, is at this facility, and holds the role — in findStaff above.
//
// The alternative was a new door in eventstore of the shape ActorForService has. It was
// rejected because it would be a wider hole for a smaller reason: the worker needs one
// because an escalation has no person behind it and no request to read, whereas this has
// both a person and a verifiable grant, and can therefore go in through the front.
//
// The station is per-act rather than per-person: the same clinical assistant is at
// anthropometry at 09:10 and at examination at 09:40, and an observation that recorded the
// wrong one would misattribute a bottleneck in §14.2's analysis.
//
// It is also the one field here that the API does not fill in today. `rbac.HTTPAuthorizer`
// resolves every subject with a nil station, so `read.observation.station_code` is empty on
// every value the running system writes — a gap in the request path, not a rule. This fills it
// with the station's code, which is what the column is named for and what makes the loaded
// register useful for the screens that group by station. Worth knowing when comparing a loaded
// row with one an operator typed: the loaded one says where it was taken and the typed one
// does not, yet.
func (l *loader) as(ctx context.Context, role, station string) context.Context {
	who := l.staff[role]
	return httpx.WithPrincipal(ctx, httpx.Principal{
		UserID:     who.UserID.String(),
		FacilityID: l.facility.String(),
		Code:       who.Code,
		DeviceID:   SeedDevice.String(),
		Role:       who.Role,
		Station:    station,
	})
}

// at moves the shared clock to a moment in the clinic's own calendar.
func (l *loader) at(day time.Time, hour, minute int) {
	local := day.In(visit.Dhaka)
	l.clock.Current = time.Date(local.Year(), local.Month(), local.Day(),
		hour, minute, l.rng.Intn(60), 0, visit.Dhaka).UTC()
}

// guard is the two refusals, in the order that makes the message useful.
func (l *loader) guard(ctx context.Context) error {
	var foreign, ours int
	err := l.pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE NOT loaded),
		  count(*) FILTER (WHERE loaded)
		  FROM (
		    SELECT EXISTS (
		             SELECT 1 FROM ledger.event e
		              WHERE e.patient_id = p.id
		                AND e.event_type = 'PATIENT_REGISTERED'
		                AND e.actor_device_id = $1) AS loaded
		      FROM core.patient p) AS census`, SeedDevice).Scan(&foreign, &ours)
	if err != nil {
		return fmt.Errorf("counting the patients already here: %w", err)
	}
	l.alreadyHere = foreign + ours
	if os.Getenv(Override) != "" {
		return nil
	}
	// Asked first, because it is the dangerous one. A register with people in it that this
	// command did not invent is somebody's real register until proven otherwise, whatever
	// DTHCMS_ENV says.
	if foreign > 0 {
		return fmt.Errorf("refused: this database holds %d patient(s) this command did not "+
			"register, so it is probably not a local one. Set %s=1 if you are certain",
			foreign, Override)
	}
	if ours > 0 {
		return fmt.Errorf("refused: %d synthetic patient(s) are already loaded, and this "+
			"command is not idempotent — a second run would double the register rather than "+
			"reconcile with it. Reset the database, or set %s=1 to load a second cohort",
			ours, Override)
	}
	return nil
}

// readNDJSON reads a cohort a line at a time, as synthgen writes it.
func readNDJSON(path string) ([]synthetic.Patient, error) {
	f, err := os.Open(path) //nolint:gosec // the operator names the input file
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var people []synthetic.Patient
	// A generated cohort holds long Bengali names and a dozen visits; the default 64 KiB
	// token limit is ample, but a scanner that silently stops at a long line would load
	// half a cohort and report success.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var q synthetic.Patient
		if err := json.Unmarshal([]byte(text), &q); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}
		people = append(people, q)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(people) == 0 {
		return nil, fmt.Errorf("%s holds no patients", path)
	}
	return people, nil
}

// report is the closing summary, printed rather than logged.
//
// Printed for the same reason devseed prints: the person reading this is at a terminal
// wanting to know whether their stack now has anything in it, and a json log line is a worse
// answer to that question than four aligned columns.
func (l *loader) report(elapsed time.Duration) {
	fmt.Println()
	// What *this run* wrote, not what the register holds. The two differ the moment somebody
	// loads a second cohort with the override set, and a summary that quietly reported the
	// wrong one would be the least trustworthy line in the whole command.
	fmt.Printf("Loaded in %s. This run appended:\n\n", elapsed.Round(time.Second))
	for _, row := range []struct {
		label string
		n     int
	}{
		{"patients registered", l.tally.registered},
		{"visits closed (history)", l.tally.visitsClosed},
		{"visits open (today)", l.tally.visitsOpen},
		{"station encounters", l.tally.encounters},
		{"queue entries today", l.tally.queued},
		{"observations recorded", l.tally.observations},
		{"critical-value alerts", l.tally.alerts},
		{"allergy assertions", l.tally.allergies},
		{"exercise assessments", l.tally.exercise},
		{"diet recall entries", l.tally.nutrition},
		{"AI provenance entries", l.tally.registeredSynthetic},
	} {
		fmt.Printf("  %-26s %d\n", row.label, row.n)
	}
	if l.tally.skipped > 0 {
		fmt.Printf("\n  %d patient(s) were skipped; the reasons are in the log above.\n", l.tally.skipped)
	}
	fmt.Println()
	fmt.Println("Everything above went through the event ledger, so `migrate verify` and a")
	fmt.Println("projection rebuild both still hold. Nothing was written to read.* directly.")
	fmt.Println()
	fmt.Println("Not loaded, and deliberately: coded medical history (it would put every patient")
	fmt.Println("behind the counselling gate), consent records (a fresh database has no template),")
	fmt.Println("and prescriptions. read.station_activity fills when cmd/projector runs.")
	fmt.Println()
	fmt.Println("Sign in at http://localhost:3100 as DOC01 to see the alerts, or REG01 for the board.")
	fmt.Println()
}
