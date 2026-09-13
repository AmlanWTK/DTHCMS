package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/education"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// CP88 §1, through the real thing.
//
// # Why this test exists rather than a comment
//
// The decision that carries CP88 is that **the improvement score is asked by the prescription
// education officer and not by the physician**. The reason is demand characteristics: a patient
// asked by the consultant who has just changed their treatment how much better they feel is
// being asked by the person whose work they are grading, and the answer drifts upward — most for
// the patients who most want to please, which in this clinic means the elderly, the poor, and
// the ones who travelled furthest. Every point of that drift is a point of false reassurance in
// the one number that is supposed to say the treatment is working.
//
// A rule of that shape written down and not enforced is a rule that lasts until the first busy
// clinic. So it is enforced in three independent places, and this drives the outermost one: a
// real sign-in, a real session at a real enrolled workstation, the real middleware chain, the
// real route guard, the real RBAC engine and the real handler.
//
// # Why it cannot pass vacuously
//
// The obvious way for a test like this to prove nothing is for the physician to be refused
// *before* the interesting check — a 401 for a bad token, a 403 from the route guard because
// they hold none of the route's permissions, a 404 because the fixture is wrong. Each of those
// would be a green run about nothing.
//
// So the same request, from the same physician, with the same body except for the observation
// code, must **succeed**. The physician records a foot-examination finding through this exact
// endpoint and it lands. That is what makes the refusal of `IMPROVEMENT_SCORE` a statement about
// the code rather than about the physician's session, their workstation, their reach or the
// route's declaration.

// educationStack is the registration stack with the observation endpoint and station 11 mounted.
func mountObservationAndEducation(pool *pgxpool.Pool, logger *slog.Logger, r chi.Router) {
	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: clock.Real{},
		Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).
		Register(context.Background()); err != nil {
		panic("registering the projections: " + err.Error())
	}

	clinicalStore := clinical.NewStore(pool)
	clinicalService := clinical.NewService(clinicalStore, events, clock.Real{})
	clinical.NewHandlers(clinical.HandlersConfig{
		Service: clinicalService, Store: clinicalStore, Clock: clock.Real{}, Logger: logger,
	}).Mount(r)

	educationRoutes(pool, logger).Mount(r)
}

// educationRoutes builds station 11's handlers against a real service.
//
// Separate from the mount above because its two halves go to two different routers: the
// reference data sits at `/v1/education`, and the session and the assessment sit inside
// `/v1/patients`, where `patient.Handlers` owns the path.
func educationRoutes(pool *pgxpool.Pool, logger *slog.Logger) *education.Handlers {
	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: clock.Real{},
		Synchronous: projection.NewSyncSet(projection.Default),
	})
	clinicalStore := clinical.NewStore(pool)
	prescriptionStore := prescription.NewStore(pool)
	educationStore := education.NewStore(pool)
	return education.NewHandlers(education.HandlersConfig{
		Service: education.NewService(educationStore,
			clinical.NewService(clinicalStore, events, clock.Real{}),
			// The real composition-root bridge, so the reach from a prescription to a checklist
			// is exercised here rather than stubbed. Nothing in this file prescribes anything,
			// so it returns no lines — which is the honest answer for these patients and is not
			// what is under test.
			&prescribedDevices{store: prescriptionStore}, clock.Real{}),
		Store: educationStore, Clock: clock.Real{}, Logger: logger,
	})
}

// educationPatientRoutes is the half that lives under /v1/patients/{id}.
func educationPatientRoutes(pool *pgxpool.Pool, logger *slog.Logger) func(chi.Router) {
	return educationRoutes(pool, logger).MountPatient
}

// scoreSubject puts a patient in front of one station with an open visit, and returns both ids.
func (s *registrationStack) scoreSubject(t *testing.T, station string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	patient := s.reachFixture(t, station, "in_service")
	var visit uuid.UUID
	if err := s.db.SQL.QueryRow(`
		SELECT id FROM core.visit WHERE patient_id = $1 ORDER BY opened_at DESC LIMIT 1`,
		patient).Scan(&visit); err != nil {
		t.Fatalf("finding the visit: %v", err)
	}
	return patient, visit
}

// TestThePhysicianCannotRecordAnImprovementScore is CP88's design, asserted on the wire.
func TestThePhysicianCannotRecordAnImprovementScore(t *testing.T) {
	s := newStackWithPatientRoutes(t, []patientSub{educationPatientRoutes}, mountObservationAndEducation)
	s.seedClerk(t, "DOC-CP88", auth.RolePhysician)
	workstation := s.enrol(t, "Consulting room 1", "STN_CONSULTATION")
	token := s.signIn(t, "DOC-CP88", workstation)
	hat := string(auth.RolePhysician)

	// The physician's own patient, at the physician's own station. Nothing about reach can
	// explain a refusal here.
	patient, visit := s.scoreSubject(t, string(auth.StationConsultation))

	// 1. The control. The same endpoint, the same session, the same patient, a code the
	//    physician's permissions do cover. If this is not a 201 the rest of the test proves
	//    nothing, because the refusal below would be about something other than the code.
	status, _, body := s.post(t, token, hat, "/v1/observations", map[string]any{
		"event_id":   uuid.Must(uuid.NewV7()).String(),
		"patient_id": patient.String(),
		"visit_id":   visit.String(),
		"code":       "FOOT_ULCER_PRESENT",
		"value_bool": false,
	})
	if status != http.StatusCreated {
		t.Fatalf("the physician could not record a foot examination finding: %d %s\n"+
			"Without this the refusal below is not evidence of anything: it could be the "+
			"session, the workstation, the reach or the route's declaration.", status, body)
	}

	// 2. The rule. The same request, one field different.
	status, _, body = s.post(t, token, hat, "/v1/observations", map[string]any{
		"event_id":   uuid.Must(uuid.NewV7()).String(),
		"patient_id": patient.String(),
		"visit_id":   visit.String(),
		"code":       "IMPROVEMENT_SCORE",
		"value":      9,
		"unit":       "1",
	})
	if status != http.StatusForbidden {
		t.Fatalf("the physician recorded an improvement score: %d %s\n"+
			"CP88 §1: the score is asked at the education station by somebody with no stake in "+
			"the answer, once the prescription is written and cannot be changed by what the "+
			"patient says. A consultant who can record it will record it, and every point of "+
			"the resulting drift is a point of false reassurance.", status, body)
	}

	// 3. The refusal says nothing about the patient. A 403 that leaked "no such patient" or
	//    "that patient is not yours" would be a different sentence and a different disclosure.
	if strings.Contains(strings.ToLower(body), "patient") {
		t.Errorf("the refusal mentions the patient: %s", body)
	}

	// 4. And nothing landed. A 403 with a row behind it is the worst of both.
	var rows int
	if err := s.db.SQL.QueryRow(`
		SELECT count(*) FROM read.observation
		 WHERE patient_id = $1 AND code IN ('IMPROVEMENT_SCORE', 'IMPROVEMENT_SCORE_NA')`,
		patient).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d improvement rows exist after a refused write", rows)
	}

	// 5. The station route is closed to them too, so the rule is not one endpoint's habit.
	//    The physician holds `education.read` — criterion 2 — and not `education.record`.
	status, _, body = s.post(t, token, hat,
		"/v1/patients/"+patient.String()+"/education", map[string]any{
			"event_id":    uuid.Must(uuid.NewV7()).String(),
			"visit_id":    visit.String(),
			"improvement": map[string]any{"score": 9},
		})
	if status != http.StatusForbidden {
		t.Fatalf("the physician reached the education station's write: %d %s", status, body)
	}
}

// TestTheEducationOfficerCanRecordAnImprovementScore is the other half, and it is what stops the
// rule above being enforced by nobody being able to record one at all.
//
// A permission held by no role, or a station whose officer cannot reach the endpoint, would pass
// every assertion in the test above and would mean the score is never captured. That failure is
// invisible from the physician's side and would look, on a dashboard, exactly like patients who
// declined to answer.
func TestTheEducationOfficerCanRecordAnImprovementScore(t *testing.T) {
	s := newStackWithPatientRoutes(t, []patientSub{educationPatientRoutes}, mountObservationAndEducation)
	s.seedClerk(t, "EDU-CP88", auth.RoleRxEducator)
	workstation := s.enrol(t, "Education 1", "STN_RX_EDUCATION")
	token := s.signIn(t, "EDU-CP88", workstation)
	hat := string(auth.RoleRxEducator)

	patient, visit := s.scoreSubject(t, string(auth.StationRxEducation))
	// A second, earlier visit, so §2's question has the comparison point it is anchored to.
	if _, err := s.db.SQL.Exec(`
		INSERT INTO core.visit (facility_id, patient_id, visit_code, visit_type, status,
		                        clinic_day, opened_at, opened_by, closed_at, closed_by)
		VALUES ($1, $2, $3, 'new', 'closed', current_date - 90, now() - interval '90 days', $4,
		        now() - interval '90 days' + interval '2 hours', $4)`,
		s.facility, patient, "V-CP88-"+uuid.NewString()[:8], s.user); err != nil {
		t.Fatalf("giving the patient a last visit: %v", err)
	}

	status, decoded, body := s.post(t, token, hat,
		"/v1/patients/"+patient.String()+"/education", map[string]any{
			"event_id":    uuid.Must(uuid.NewV7()).String(),
			"visit_id":    visit.String(),
			"improvement": map[string]any{"score": 7},
		})
	if status != http.StatusCreated {
		t.Fatalf("the education officer could not record a score: %d %s\n"+
			"If this is a 403 the score is captured by nobody, which on a dashboard is "+
			"indistinguishable from patients who declined to answer.", status, body)
	}
	if decoded == nil {
		t.Fatalf("no body: %s", body)
	}

	// The capturing operator is on the record, which is what makes §12's question — does the
	// answer depend on who asked — answerable at all.
	var role string
	var recordedBy uuid.UUID
	if err := s.db.SQL.QueryRow(`
		SELECT recorded_role, recorded_by FROM read.observation
		 WHERE patient_id = $1 AND code = 'IMPROVEMENT_SCORE' AND status = 'ACTIVE'`,
		patient).Scan(&role, &recordedBy); err != nil {
		t.Fatalf("reading the score back: %v", err)
	}
	if role != string(auth.RoleRxEducator) {
		t.Errorf("the score is attributed to %s", role)
	}
	if recordedBy != s.user {
		t.Errorf("the score names %s as the asker, not the officer who asked", recordedBy)
	}
}

// TestThePhysicianSeesTheCompetencyTheyMayNotRecord is CP92 criterion 2, and the pair to the
// test above.
//
// The two rules pull in opposite directions and both matter: the consultant must see what the
// patient could and could not do at the last visit, and must not be the one who records it. A
// design that achieved the second by blinding them to the station's output entirely would have
// satisfied CP88 by breaking CP92.
func TestThePhysicianSeesTheCompetencyTheyMayNotRecord(t *testing.T) {
	s := newStackWithPatientRoutes(t, []patientSub{educationPatientRoutes}, mountObservationAndEducation)
	s.seedClerk(t, "DOC-CP92", auth.RolePhysician)
	workstation := s.enrol(t, "Consulting room 2", "STN_CONSULTATION")
	token := s.signIn(t, "DOC-CP92", workstation)
	hat := string(auth.RolePhysician)

	patient, visit := s.scoreSubject(t, string(auth.StationConsultation))

	status, body := s.get(t, token, hat,
		"/v1/patients/"+patient.String()+"/education?visit_id="+visit.String())
	if status != http.StatusOK {
		t.Fatalf("the physician cannot read the education station's output: %d %s\n"+
			"CP92 criterion 2 is that competency is visible to the physician at the next visit. "+
			"If this is a 403 the station records something nobody acts on.", status, body)
	}
	if !strings.Contains(body, "prior_competency") {
		t.Errorf("the session carries no competency: %s", body)
	}
}
