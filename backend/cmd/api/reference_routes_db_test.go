package main

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
)

// The clinic's dictionary, on the wire (CP85).
//
// # What was broken
//
// Thirteen routes serve reference data — units, foods, growth curves, plausibility bands,
// reference ranges, reaction types, observation codes and answers, assessment instruments,
// exercise contraindications, correction reasons, prescription statuses. Every one of them
// declared a *patient* permission, which reaches only the station being worked for the nine
// station roles. A list of units has no patient in it, so no handler could judge a resource,
// so the route guard refused rather than enter a handler nobody would check.
//
// The measured consequence was not subtle: nine stations' clinical forms could not load
// their pickers. The nutritionist's food search answered 403. The anthropometry officer's
// plausibility warnings never arrived. The examination station's coded answer buttons had
// nothing to draw.
//
// # What this test holds
//
// Each of the thirteen, driven through the real router, the real middleware chain, the real
// sign-in and the real route guard, by a station role that could not reach it before — and
// answering 200. Not by calling the handler: the handler was never the problem, and a test
// that called it would have passed on every day the clinic was broken.
//
// The second test is the route half of invariant 128. The database can assert that
// `reference.read` exists, is not sensitive, is held by every role but RESEARCHER, and
// carries no prefix `rbac.isClinical` reads — see migration 00067 for why the route half
// cannot live in SQL. This is that half: the thirteen declare `reference.read`, and they
// declare no patient permission beside it.

// referenceRoutes are the thirteen, each with the query string that makes it a legal
// request and a station role for which the route was refused before CP85.
//
// The role matters. Driving all thirteen as a physician would pass whatever the permission
// said, because the physician reaches the facility for every one of the old permissions —
// which is exactly how this stayed hidden.
var referenceRoutes = []struct {
	path string
	// role is a role the route used to refuse. For the eleven that declared
	// `observation.read.values` or `patient.read.allergies` the nutritionist held the
	// permission with a station reach and was refused by the guard; for the prescription
	// state machine the pharmacist was refused the same way.
	role auth.RoleCode
	// station is where that role stands, for the workstation the session binds to.
	station auth.StationCode
}{
	{"/v1/allergies/reactions", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/assessments/instruments", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/corrections/reasons", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/exercise/contraindications", auth.RoleExercise, auth.StationExercise},
	{"/v1/foods?q=rice", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/foods/measures", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/observations/answers", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/observations/codes", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/observations/growth-curves?indicator=HFA&sex=female", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/observations/plausibility", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/observations/reference-ranges", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/observations/units", auth.RoleNutritionist, auth.StationNutrition},
	{"/v1/prescriptions/statuses", auth.RolePharmacist, ""},
}

// mountReferenceModules wires the six modules that own the thirteen routes, with real
// stores. Real stores because the point is a 200 with a body in it: a handler mounted over
// a nil store would answer 500, which is not a permission refusal but is also not proof that
// the route works.
func mountReferenceModules(pool *pgxpool.Pool, logger *slog.Logger, r chi.Router) {
	events := eventstore.New(eventstore.Config{Pool: pool, Clock: clock.Real{}})

	clinicalStore := clinical.NewStore(pool)
	clinicalHandlers := clinical.NewHandlers(clinical.HandlersConfig{
		Service: clinical.NewService(clinicalStore, events, clock.Real{}),
		Store:   clinicalStore, Clock: clock.Real{}, Logger: logger,
	})
	clinicalHandlers.Mount(r)
	clinicalHandlers.MountCorrections(r)

	allergyStore := allergy.NewStore(pool)
	allergy.NewHandlers(allergy.HandlersConfig{
		Service: allergy.NewService(allergyStore, events, clock.Real{}),
		Store:   allergyStore, Clock: clock.Real{}, Logger: logger,
	}).Mount(r)

	exerciseStore := exercise.NewStore(pool)
	exercise.NewHandlers(exercise.HandlersConfig{
		Service: exercise.NewService(exerciseStore, events, clock.Real{}),
		Store:   exerciseStore, Clock: clock.Real{}, Logger: logger,
	}).Mount(r)

	assessmentStore := assessment.NewStore(pool)
	assessment.NewHandlers(assessment.HandlersConfig{
		Service: assessment.NewService(assessmentStore, events, clock.Real{}),
		Store:   assessmentStore, Clock: clock.Real{}, Logger: logger,
	}).Mount(r)

	nutritionStore := nutrition.NewStore(pool)
	nutrition.NewHandlers(nutrition.HandlersConfig{
		Service: nutrition.NewService(nutritionStore, events, clock.Real{}),
		Store:   nutritionStore, Clock: clock.Real{}, Logger: logger,
	}).Mount(r)

	prescriptionStore := prescription.NewStore(pool)
	// The state machine comes out of `core.prescription_transition`, exactly as the
	// composition root reads it at start-up: the statuses route renders the matrix, and a
	// machine built from a literal here would be a second copy of the thing the route exists
	// to stop anybody keeping.
	machine, err := prescriptionStore.Machine(context.Background())
	if err != nil {
		panic("reading the prescription state machine: " + err.Error())
	}
	prescription.NewHandlers(prescription.HandlersConfig{
		Service: prescription.NewService(prescriptionStore, events, machine, nil, clock.Real{}),
		Store:   prescriptionStore, Clock: clock.Real{}, Logger: logger,
	}).Mount(r)
}

// The test this checkpoint exists for.
func TestEveryReferenceRouteOpensForAStationRoleThatCouldNotReachItBefore(t *testing.T) {
	for _, route := range referenceRoutes {
		t.Run(strings.SplitN(strings.TrimPrefix(route.path, "/v1/"), "?", 2)[0]+"/"+string(route.role), func(t *testing.T) {
			s := newRegistrationStack(t, mountReferenceModules)
			code := "REF-" + strings.ToUpper(string(route.role))[:3]
			s.seedClerk(t, code, route.role)
			// A role that works no station signs in without one; the pharmacist is the one
			// such role here, and `reference.read` reaching the facility is precisely why
			// that no longer matters.
			workstation := ""
			if route.station != "" {
				workstation = s.enrol(t, "Reference desk", string(route.station))
			}
			token := s.signIn(t, code, workstation)

			status, body := s.get(t, token, string(route.role), route.path)
			if status != http.StatusOK {
				t.Fatalf("GET %s answered %d for %s: %s\n\n"+
					"This route serves the clinic's dictionary and has no patient in it. A 403 here "+
					"means it is back on a patient permission, whose reach is the station being "+
					"worked — which is the defect CP85 fixed, and it presents as a clinical form "+
					"with empty pickers rather than as an error anybody reports.",
					route.path, status, route.role, body)
			}
			if strings.TrimSpace(body) == "" {
				t.Errorf("GET %s answered 200 with an empty body", route.path)
			}
		})
	}
}

// The route half of invariant 128 (migration 00067 explains why it is not in SQL).
func TestEveryReferenceRouteDeclaresTheReferencePermission(t *testing.T) {
	declarations, err := httpx.Declarations(contractRouter(t))
	if err != nil {
		t.Fatalf("reading the route declarations: %v", err)
	}

	// The prefixes rbac.isClinical reads as "this permission is about a patient". A
	// reference route declaring one of these acquires station scope and is refused for the
	// nine station roles, which is the whole defect.
	patientPrefixes := []string{
		"patient.", "observation.", "counseling.tick", "records.", "lab.", "diagnosis.",
		"prescription.", "ai.", "education.", "qa.",
	}

	for _, route := range referenceRoutes {
		pattern := strings.SplitN(route.path, "?", 2)[0]
		key := http.MethodGet + " " + pattern
		requirement, ok := declarations[key]
		if !ok {
			t.Errorf("%s is not declared; the route table moved and this test stopped meaning "+
				"anything. Fix the list in this file rather than deleting the assertion.", key)
			continue
		}
		permissions := requirement.Permissions()
		sort.Strings(permissions)
		if len(permissions) != 1 || permissions[0] != auth.PermReferenceRead {
			t.Errorf("%s declares %v, want exactly [%s]", key, permissions, auth.PermReferenceRead)
		}
		for _, permission := range permissions {
			for _, prefix := range patientPrefixes {
				if strings.HasPrefix(permission, prefix) {
					t.Errorf("%s declares %q, which rbac.isClinical reads as a patient permission: "+
						"it acquires station scope, and a route with no patient in it can never "+
						"satisfy that", key, permission)
				}
			}
		}
		if requirement.EnforcesResourceScope() {
			t.Errorf("%s declares httpx.PermissionScoped. `reference.read` reaches the whole "+
				"facility for every role that holds it, so there is no resource anybody owes a "+
				"check on and the declaration only invites one nobody can write.", key)
		}
	}
}
