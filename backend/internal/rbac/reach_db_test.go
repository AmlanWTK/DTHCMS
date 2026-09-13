package rbac_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The escalation these tests exist against (ADR-0036 §1).
//
// A station role must not reach a patient who is not on its queue. That sentence is the
// whole of the clinical access policy, and until this checkpoint nothing in the repository
// held it: the engine compared `resource.StationID` to `subject.StationID`, both of which
// were nil on every HTTP request, so the comparison could not fail and could not pass.
//
// What is proved here, in order of how badly it would hurt to get wrong:
//
//  1. a station does not reach a patient standing at another station — read or write;
//  2. the write reach is *strictly* narrower than the read reach, demonstrated on the one
//     case where they differ and where a single mistaken `||` would erase the difference:
//     a patient this station has finished with is readable and is not writable;
//  3. a patient with no open visit is reached by nobody but the facility-wide roles;
//  4. the refusal says the same thing to the client whichever of these it was.
//
// Each assertion is made against the real tables through the real query. A fake Reacher
// would let the SQL be wrong in exactly the direction these tests exist to catch.

// --- the clinic, as rows ---

// clinic builds the minimum real world a reach question can be asked about: a patient, a
// visit, and whatever queue entries and encounters the case under test needs.
type clinic struct {
	st      *stack
	reach   *rbac.PostgresReach
	patient uuid.UUID
	visit   uuid.UUID
	user    uuid.UUID
}

func newClinic(t *testing.T) *clinic {
	t.Helper()
	st := newStack(t)
	c := &clinic{st: st, reach: rbac.NewPostgresReach(st.pool)}
	c.user = st.user(t, "REACH-OP")
	c.patient = c.newPatient(t, "Reach Test")
	return c
}

var clinicalCounter int

func (c *clinic) newPatient(t *testing.T, name string) uuid.UUID {
	t.Helper()
	clinicalCounter++
	var id uuid.UUID
	if err := c.st.db.SQL.QueryRow(`
		INSERT INTO core.patient
		  (facility_id, clinical_id, name_en, sex, birth_date, dob_verified_by,
		   phone_primary, registered_by)
		VALUES ($1, $2, $3, 'female', '1990-01-01', 'patient_stated', '+8801712345678', $4)
		RETURNING id`,
		c.st.facility, fmt.Sprintf("DTH-TEST-%04d", clinicalCounter), name, c.user).Scan(&id); err != nil {
		t.Fatalf("creating a patient: %v", err)
	}
	// The allergy gate (CP54) refuses a queue entry past station 4 for a patient whose
	// allergy status is NONE_RECORDED. That gate is not what these tests are about, and
	// leaving it to fire would mean the twelve-station test silently exercised four
	// stations. An assertion of "no known allergy" clears it the way the history desk does.
	if _, err := c.st.db.SQL.Exec(`
		INSERT INTO read.allergy_assertion
		  (id, event_id, facility_id, patient_id, kind, asserted_at, asserted_by, global_seq)
		VALUES (gen_random_uuid(), gen_random_uuid(), $1, $2, 'NO_KNOWN_ALLERGY', now(), $3, $4)`,
		c.st.facility, id, c.user, clinicalCounter); err != nil {
		t.Fatalf("clearing the allergy gate: %v", err)
	}
	return id
}

// openVisit puts the patient in the building. Returns the visit id.
func (c *clinic) openVisit(t *testing.T, patient uuid.UUID) uuid.UUID {
	t.Helper()
	clinicalCounter++
	var id uuid.UUID
	if err := c.st.db.SQL.QueryRow(`
		INSERT INTO core.visit (facility_id, patient_id, visit_code, visit_type, clinic_day, opened_by)
		VALUES ($1, $2, $3, 'new', current_date, $4) RETURNING id`,
		c.st.facility, patient, fmt.Sprintf("V-TEST-%04d", clinicalCounter), c.user).Scan(&id); err != nil {
		t.Fatalf("opening a visit: %v", err)
	}
	return id
}

// queue puts the patient in a station's queue at a status.
func (c *clinic) queue(t *testing.T, visit, patient uuid.UUID, station, status string) {
	t.Helper()
	// `called` and anything downstream of it must name who called, which is the constraint
	// that makes "who claimed this patient" answerable and which the reach relies on.
	called := "NULL::timestamptz, NULL::uuid"
	if status != "waiting" {
		called = "now(), '" + c.user.String() + "'::uuid"
	}
	outcome := ""
	if status == "rerouted" {
		outcome = ", outcome_reason = 'test', rerouted_to = 'STN_QA'"
	}
	sql := fmt.Sprintf(`
		INSERT INTO core.queue_entry
		  (facility_id, visit_id, patient_id, station_code, status, clinic_day, called_at, called_by)
		VALUES ($1, $2, $3, $4, $5, current_date, %s)`, called)
	if _, err := c.st.db.SQL.Exec(sql, c.st.facility, visit, patient, station, status); err != nil {
		t.Fatalf("queueing at %s as %s: %v", station, status, err)
	}
	if outcome != "" {
		if _, err := c.st.db.SQL.Exec(`UPDATE core.queue_entry SET outcome_reason = 'test', rerouted_to = 'STN_QA'
			WHERE visit_id = $1 AND station_code = $2`, visit, station); err != nil {
			t.Fatal(err)
		}
	}
}

// encounter opens or closes a station touch.
func (c *clinic) encounter(t *testing.T, visit, patient uuid.UUID, station, status string) {
	t.Helper()
	ended := "NULL::timestamptz, NULL::uuid"
	if status != "in_progress" {
		ended = "now(), '" + c.user.String() + "'::uuid"
	}
	sql := fmt.Sprintf(`
		INSERT INTO core.encounter
		  (facility_id, visit_id, patient_id, station_code, status, started_by, ended_at, ended_by)
		VALUES ($1, $2, $3, $4, $5, $6, %s)`, ended)
	if _, err := c.st.db.SQL.Exec(sql, c.st.facility, visit, patient, station, status, c.user); err != nil {
		t.Fatalf("opening an encounter at %s: %v", station, err)
	}
}

// standing is a context carrying a subject wearing one hat, and the real reach store —
// exactly what the route guard leaves behind on a live request.
func (c *clinic) standing(role auth.RoleCode) context.Context {
	subject := rbac.Subject{
		UserID: c.user, FacilityID: c.st.facility,
		Roles: []auth.RoleCode{role}, ActiveRole: role,
		StationCode: auth.StationOf(role),
		Permissions: rbac.RolePermissions[role],
	}
	return rbac.WithReacher(rbac.WithSubject(context.Background(), subject), c.reach)
}

// --- (1) a station does not reach a patient at another station ---

func TestAStationDoesNotReachAPatientOnAnotherStationsQueue(t *testing.T) {
	c := newClinic(t)
	visit := c.openVisit(t, c.patient)
	// The patient is with anthropometry. Nobody else has had them.
	c.queue(t, visit, c.patient, string(auth.StationAnthropometry), "in_service")
	c.encounter(t, visit, c.patient, string(auth.StationAnthropometry), "in_progress")

	// The nutritionist, four stations downstream, holds the permission and is standing in
	// the same facility on the same day.
	nutritionist := c.standing(auth.RoleNutritionist)

	if err := rbac.AuthorizeStationRead(nutritionist, auth.PermObservationReadValues, "patient", c.patient); err == nil {
		t.Fatal("the nutritionist READ a patient who has never been in their queue; " +
			"this is the escalation ADR-0036 exists to prevent")
	}
	if err := rbac.AuthorizeStationWrite(nutritionist, auth.PermObservationReadValues, "patient", c.patient); err == nil {
		t.Fatal("the nutritionist WROTE against a patient who has never been in their queue")
	}

	// And the station that actually has them is allowed, in both directions — a rule that
	// refused everybody would pass the two assertions above and close the clinic.
	anthro := c.standing(auth.RoleAnthropometry)
	if err := rbac.AuthorizeStationRead(anthro, auth.PermObservationReadValues, "patient", c.patient); err != nil {
		t.Fatalf("anthropometry cannot read the patient it is measuring: %v", err)
	}
	if err := rbac.AuthorizeStationWrite(anthro, auth.PermObservationWriteAnthro, "patient", c.patient); err != nil {
		t.Fatalf("anthropometry cannot record against the patient it is measuring: %v", err)
	}
}

// --- (2) the write reach is strictly narrower than the read reach ---

// The case the whole two-type arrangement exists for.
//
// A patient the counsellor finished with an hour ago: `done` on the queue, a `finished`
// encounter, nothing open. The counsellor must be able to re-open what they recorded, and
// must not be able to amend it — that is the correction workflow's job and it is a flagged,
// reviewed path on purpose.
//
// If somebody ever "simplifies" the two queries into one, this is the test that fails.
func TestWriteReachIsStrictlyNarrowerThanReadReach(t *testing.T) {
	c := newClinic(t)
	visit := c.openVisit(t, c.patient)
	c.queue(t, visit, c.patient, string(auth.StationCounseling), "done")
	c.encounter(t, visit, c.patient, string(auth.StationCounseling), "finished")

	counsellor := c.standing(auth.RoleCounselor)

	if err := rbac.AuthorizeStationRead(counsellor, auth.PermObservationReadValues, "patient", c.patient); err != nil {
		t.Fatalf("the counsellor cannot re-open what they just recorded: %v\n"+
			"A read reach that stops at the door of `done` makes the software slower than the "+
			"paper it replaces (ADR-0036 §1).", err)
	}
	if err := rbac.AuthorizeStationWrite(counsellor, auth.PermObservationWriteLifestyle, "patient", c.patient); err == nil {
		t.Fatal("the counsellor amended a patient they had finished with.\n" +
			"The write reach must stop at `done`: amending a finished record is the correction " +
			"workflow's job, and it is a different, flagged path precisely so that somebody sees it.")
	}

	// `skipped` is the other half of the same sentence: read, never write.
	other := c.newPatient(t, "Skipped")
	visit2 := c.openVisit(t, other)
	c.queue(t, visit2, other, string(auth.StationCounseling), "skipped")
	if err := rbac.AuthorizeStationRead(counsellor, auth.PermObservationReadValues, "patient", other); err != nil {
		t.Errorf("a skipped patient is not readable by the station that skipped them: %v", err)
	}
	if err := rbac.AuthorizeStationWrite(counsellor, auth.PermObservationWriteLifestyle, "patient", other); err == nil {
		t.Error("a skipped patient was writable")
	}
}

// `waiting` is the third boundary, and the one a careless status list gets wrong: a patient
// sitting in your queue is yours to see and not yet yours to record against. Claiming them
// is a recorded act (queue_entry.called_by) and it is what moves the boundary.
func TestWaitingIsReadableAndNotWritable(t *testing.T) {
	c := newClinic(t)
	visit := c.openVisit(t, c.patient)
	c.queue(t, visit, c.patient, string(auth.StationNutrition), "waiting")

	nutritionist := c.standing(auth.RoleNutritionist)
	if err := rbac.AuthorizeStationRead(nutritionist, auth.PermObservationReadValues, "patient", c.patient); err != nil {
		t.Errorf("a waiting patient is invisible to the station waiting for them: %v", err)
	}
	if err := rbac.AuthorizeStationWrite(nutritionist, auth.PermObservationWriteNutrition, "patient", c.patient); err == nil {
		t.Error("a station recorded against a patient it has not yet called")
	}

	// Calling them moves the boundary, which is what makes the rule usable rather than
	// merely strict.
	if _, err := c.st.db.SQL.Exec(`UPDATE core.queue_entry
		SET status = 'called', called_at = now(), called_by = $3
		WHERE visit_id = $1 AND station_code = $2`, visit, string(auth.StationNutrition), c.user); err != nil {
		t.Fatal(err)
	}
	if err := rbac.AuthorizeStationWrite(nutritionist, auth.PermObservationWriteNutrition, "patient", c.patient); err != nil {
		t.Errorf("a called patient is still not writable, so the station can never work: %v", err)
	}
}

// --- (3) a patient with no open visit is nobody's but the facility's ---

func TestAPatientWithNoOpenVisitIsReachedByNobodyButFacilityWideRoles(t *testing.T) {
	c := newClinic(t)
	// Registered and never routed: the case ADR-0036 warns will feel wrong the first time
	// somebody hits it. No visit at all.
	for _, role := range []auth.RoleCode{
		auth.RoleAnthropometry, auth.RoleCounselor, auth.RoleHistory,
		auth.RoleClinicalAssistant, auth.RoleNutritionist, auth.RoleExercise, auth.RoleRxEducator,
	} {
		ctx := c.standing(role)
		if err := rbac.AuthorizeStationRead(ctx, auth.PermObservationReadValues, "patient", c.patient); err == nil {
			t.Errorf("%s reached an unrouted patient", role)
		}
	}

	// A visit that has been closed is the same answer, and it is the one that matters for
	// the long run: last month's nutritionist must not still hold a reach.
	visit := c.openVisit(t, c.patient)
	c.queue(t, visit, c.patient, string(auth.StationNutrition), "done")
	nutritionist := c.standing(auth.RoleNutritionist)
	if err := rbac.AuthorizeStationRead(nutritionist, auth.PermObservationReadValues, "patient", c.patient); err != nil {
		t.Fatalf("with the visit open, the nutritionist who saw them cannot read them: %v", err)
	}
	if _, err := c.st.db.SQL.Exec(`UPDATE core.visit SET status = 'closed', closed_at = now(), closed_by = $2
		WHERE id = $1`, visit, c.user); err != nil {
		t.Fatal(err)
	}
	if err := rbac.AuthorizeStationRead(nutritionist, auth.PermObservationReadValues, "patient", c.patient); err == nil {
		t.Fatal("the visit closed and the nutritionist kept their reach.\n" +
			"Reach is evaluated against the *current* visit; a reach that outlives the visit is a " +
			"reach that never expires.")
	}

	// The facility-wide roles are unaffected throughout, which is what makes this a policy
	// and not an outage: the physician, QA and the records office can always open the file.
	for _, role := range []auth.RoleCode{auth.RolePhysician, auth.RoleQa, auth.RoleRecords} {
		ctx := c.standing(role)
		if err := rbac.AuthorizeStationRead(ctx, auth.PermPatientReadDemographics, "patient", c.patient); err != nil {
			t.Errorf("%s cannot open the record of a patient who is not queued anywhere: %v\n"+
				"ADR-0036 §2 makes these roles facility-wide precisely so that somebody can.", role, err)
		}
	}
}

// --- (4) one refusal, whatever the cause ---

// A 403 that varies with the state of a record is a way to ask questions about records you
// may not read: "not in the building" and "somebody else's patient" must be indistinguishable
// to the client, however different they are in the log.
func TestTheRefusalIsTheSameWhateverTheCause(t *testing.T) {
	c := newClinic(t)
	unrouted := c.patient
	elsewhere := c.newPatient(t, "Elsewhere")
	visit := c.openVisit(t, elsewhere)
	c.queue(t, visit, elsewhere, string(auth.StationAnthropometry), "in_service")

	nutritionist := c.standing(auth.RoleNutritionist)
	var messages []string
	for _, id := range []uuid.UUID{unrouted, elsewhere, uuid.New()} {
		err := rbac.AuthorizeStationRead(nutritionist, auth.PermObservationReadValues, "patient", id)
		if err == nil {
			t.Fatal("one of the three refusals was an allow")
		}
		messages = append(messages, clientMessage(err))
	}
	for i := 1; i < len(messages); i++ {
		if messages[i] != messages[0] {
			t.Errorf("the refusals differ: %q vs %q.\nA client that can tell them apart can "+
				"enumerate which patients exist and which are in the building today.",
				messages[0], messages[i])
		}
	}
	// And nothing in any of them names a patient.
	for _, m := range messages {
		for _, id := range []uuid.UUID{unrouted, elsewhere} {
			if strings.Contains(m, id.String()) {
				t.Errorf("the refusal names a patient id: %q", m)
			}
		}
	}
}

// clientMessage is what the caller would actually see.
//
// Not err.Error(), which deliberately carries the detail — that sentence is the log's, and
// it is where the two causes are told apart on purpose so that somebody can explain a
// refusal to the desk. What crosses the wire is the errs envelope's code and message, which
// is what this reads and what must be identical.
func clientMessage(err error) string {
	var e *errs.Error
	if !errors.As(err, &e) {
		return "not an errs.Error: " + err.Error()
	}
	return e.Code + "|" + e.MessageEN + "|" + e.MessageBN
}

// --- the twelve stations, each doing its own job ---

// The test that proves the clinic can run.
//
// Each station role, standing at its own station, with the patient correctly queued to it,
// must be able to do the thing its blueprint job requires. Nine of these twelve could not
// perform a single clinical action before ADR-0036, and the reason the number was nine
// rather than twelve is that three of the roles happen to hold facility-wide scope — which
// is to say the clinic worked for exactly the people who were not the point.
func TestEveryStationCanWorkThePatientInFrontOfIt(t *testing.T) {
	type job struct {
		role  auth.RoleCode
		write string
		read  string
	}
	jobs := []job{
		{auth.RoleRegistration, auth.PermPatientWriteDemographics, auth.PermPatientReadDemographics},
		{auth.RoleAnthropometry, auth.PermObservationWriteAnthro, auth.PermObservationReadValues},
		{auth.RoleCounselor, auth.PermObservationWriteLifestyle, auth.PermObservationReadValues},
		{auth.RoleHistory, auth.PermObservationWriteHistory, auth.PermObservationReadValues},
		{auth.RoleClinicalAssistant, auth.PermObservationWriteVitals, auth.PermObservationReadValues},
		{auth.RoleJuniorDoctor, auth.PermObservationWriteVitals, auth.PermObservationReadValues},
		{auth.RoleRecords, auth.PermRecordsUpload, auth.PermRecordsRead},
		{auth.RoleNutritionist, auth.PermObservationWriteNutrition, auth.PermObservationReadValues},
		{auth.RoleExercise, auth.PermObservationWriteExercise, auth.PermObservationReadValues},
		{auth.RolePhysician, auth.PermPrescriptionSign, auth.PermPatientReadClinical},
		{auth.RoleQa, auth.PermQaReview, auth.PermPatientReadClinical},
		{auth.RoleRxEducator, auth.PermEducationRecord, auth.PermPrescriptionRead},
	}
	if len(jobs) != 12 {
		t.Fatalf("blueprint §3 has twelve stations; this test drives %d", len(jobs))
	}

	c := newClinic(t)
	for _, j := range jobs {
		t.Run(string(j.role), func(t *testing.T) {
			patient := c.newPatient(t, "Station "+string(j.role))
			visit := c.openVisit(t, patient)
			station := auth.StationOf(j.role)
			if station == "" {
				t.Fatalf("%s works no station; the table in auth.RoleStations disagrees with this test", j.role)
			}
			// Correctly queued to this station and claimed, which is what the clinic does
			// before an operator touches anything.
			c.queue(t, visit, patient, station, "in_service")
			c.encounter(t, visit, patient, station, "in_progress")

			ctx := c.standing(j.role)
			if err := rbac.AuthorizeStationRead(ctx, j.read, "patient", patient); err != nil {
				t.Errorf("%s cannot read the patient in front of it (%s): %v", j.role, j.read, err)
			}
			if err := rbac.AuthorizeStationWrite(ctx, j.write, "patient", patient); err != nil {
				t.Errorf("%s cannot do its job on the patient in front of it (%s): %v", j.role, j.write, err)
			}

			// And the same role, on the patient at the next desk, cannot.
			stranger := c.newPatient(t, "Stranger "+string(j.role))
			strangerVisit := c.openVisit(t, stranger)
			c.queue(t, strangerVisit, stranger, string(auth.StationRegistration), "in_service")
			err := rbac.AuthorizeStationWrite(ctx, j.write, "patient", stranger)
			if err == nil && rbac.ReachOf(j.role, j.write) == rbac.ScopeOwnStation {
				t.Errorf("%s reached a patient queued only to registration", j.role)
			}
		})
	}
}

// A guard against this file going quiet. Every station role must actually be station-scoped
// for the permission the test above drives it with, or the test proves nothing about that
// role — it would pass because the reach was facility-wide all along.
func TestTheTwelveStationTestIsActuallyTestingStationScope(t *testing.T) {
	stationScoped := 0
	for _, role := range auth.AllRoles {
		if auth.StationOf(role) == "" {
			continue
		}
		for _, perm := range auth.AllPermissions {
			if rbac.ReachOf(role, perm) == rbac.ScopeOwnStation {
				stationScoped++
				break
			}
		}
	}
	// Registration and records are facility-wide for their own work (ADR-0036 §2) but both
	// still hold station-scoped permissions elsewhere; physician, QA and CRM are wholly
	// facility-wide. Eight is the count that makes the escalation test meaningful, and it
	// is asserted rather than described so that a scope change has to come past this line.
	const want = 8
	if stationScoped != want {
		t.Errorf("%d station roles hold a station-scoped permission, want %d.\n"+
			"If this went up, a role was narrowed and somebody should say so. If it went down, "+
			"a role was widened and the escalation tests above have quietly stopped covering it.",
			stationScoped, want)
	}
}

var _ = time.Second

// --- the migration, held by its effects ---

// Migration 00066 is two indexes and an invariant, and all three are the kind of thing that
// can be written, reviewed, merged and then quietly not applied. This asserts what ran.
//
// The index check is not decoration: without those two indexes the reach query is a
// sequential scan of every queue entry in the clinic, on a decision made in front of every
// station-scoped request, and the failure mode is a clinic that gets slower every week
// rather than one that breaks.
func TestTheReachMigrationLanded(t *testing.T) {
	s := newStack(t)

	// Sequence 127 is *present*, rather than being the highest. It was written as the
	// highest, which read as a tighter assertion and was really an assertion about whichever
	// checkpoint happened to be last: CP85 added 128 and this failed without anything about
	// ADR-0036 having changed. What is worth holding is that this invariant exists and
	// carries its own number.
	var registered int
	if err := s.db.SQL.QueryRow(`SELECT count(*) FROM ops.invariant
		WHERE sequence = 127 AND function_name = 'assert_the_facility_wide_desks_stay_blinded'`).
		Scan(&registered); err != nil {
		t.Fatal(err)
	}
	if registered != 1 {
		t.Errorf("invariant 127 (ADR-0036's blueprint §4.4 assertion) is not registered")
	}

	var indexes int
	if err := s.db.SQL.QueryRow(`SELECT count(*) FROM pg_indexes
		WHERE indexname IN ('queue_entry_station_reach', 'encounter_station_reach')`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 2 {
		t.Errorf("%d of the 2 reach indexes exist; the reach query is scanning", indexes)
	}

	// And the invariant itself runs and passes over the seeded catalogue. A function that
	// raises on a clean database is an invariant that will be disabled the first time
	// somebody runs the verifier.
	if _, err := s.db.SQL.Exec(`SELECT core.assert_the_facility_wide_desks_stay_blinded()`); err != nil {
		t.Errorf("invariant 127 fails on a freshly migrated database: %v", err)
	}
}

// Migration 00067 is a permission, seventeen grants and an invariant, and all three are the
// kind of thing that can be written, reviewed, merged and then quietly not applied (CP85).
//
// An invariant that raises on a clean database is an invariant somebody disables the first
// time they run the verifier, so it is exercised here rather than trusted.
func TestTheReferenceMigrationLanded(t *testing.T) {
	s := newStack(t)

	var sensitive bool
	if err := s.db.SQL.QueryRow(
		`SELECT is_sensitive FROM core.permission WHERE code = 'reference.read'`).Scan(&sensitive); err != nil {
		t.Fatalf("reference.read is not in the catalogue: %v", err)
	}
	if sensitive {
		t.Error("reference.read is marked sensitive; §4.4's blinded roles would lose the food table")
	}

	// Every role but RESEARCHER, which D-48 keeps to the de-identified marts.
	var ungranted string
	if err := s.db.SQL.QueryRow(`
		SELECT coalesce(string_agg(r.code, ', ' ORDER BY r.code), '')
		  FROM core.role r
		 WHERE r.code <> 'RESEARCHER'
		   AND NOT EXISTS (SELECT 1 FROM core.role_permission rp
		                    WHERE rp.role_id = r.id AND rp.permission_code = 'reference.read')`).
		Scan(&ungranted); err != nil {
		t.Fatal(err)
	}
	if ungranted != "" {
		t.Errorf("these roles do not hold reference.read: %s; their clinical forms load no pickers", ungranted)
	}

	// And the name, from the engine's own rule rather than from a second copy of the prefix
	// list: a reference permission that reads as clinical acquires station scope, and all
	// thirteen routes go straight back to being refused.
	if rbac.ReachOf(auth.RoleNutritionist, auth.PermReferenceRead) != rbac.ScopeAny {
		t.Errorf("reference.read does not reach the facility for a station role; "+
			"rbac.isClinical is reading %q as a permission about a patient", auth.PermReferenceRead)
	}

	if _, err := s.db.SQL.Exec(`SELECT core.assert_reference_data_is_not_scoped_to_a_patient()`); err != nil {
		t.Errorf("invariant 128 fails on a freshly migrated database: %v", err)
	}
}
