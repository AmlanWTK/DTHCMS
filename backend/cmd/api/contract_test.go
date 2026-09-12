package main

import (
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/assessment"
	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/consent"
	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/dashboard"
	"github.com/AmlanWTK/DTHCMS/backend/internal/exercise"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/jobs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/nutrition"
	"github.com/AmlanWTK/DTHCMS/backend/internal/offline"
	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/apispec"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/quality"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
	"github.com/AmlanWTK/DTHCMS/backend/internal/terminology"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// The route half of the contract test.
//
// api/openapi.yaml is the contract of record, which is a claim worth exactly as much as
// the mechanism that enforces it. This is that mechanism: it walks the router this binary
// assembles and fails when the router and the document disagree in either direction — an
// undocumented route, or a documented route nobody implemented.
//
// Both directions matter, and the second is the one people forget. A path left in the
// document after the endpoint was renamed generates a client method that 404s, and the
// generated client compiles perfectly while doing it.
//
// It lives here rather than in httpx because the full surface only exists once the auth
// endpoints are mounted beside the operational ones, and httpx may not import a module.
// The composition root is the only place the whole surface is assembled, so it is the only
// place the whole surface can honestly be checked.

const specRelativePath = "../../../api/openapi.yaml"

func loadSpec(t *testing.T) apispec.Document {
	t.Helper()
	doc, err := apispec.Load(specRelativePath)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return doc
}

func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	operations, err := loadSpec(t).Operations()
	if err != nil {
		t.Fatalf("%s: %v", specRelativePath, err)
	}
	return operations
}

// contractRouter assembles the surface the binary serves.
//
// The dependencies below the routing layer are absent on purpose: mounting a route does
// not touch a database or a session store, and requiring one here would make the contract
// check something that only runs when infrastructure is up. What is real is the route
// table — every handler is registered by the same code run() uses.
func contractRouter(t *testing.T) *chi.Mux {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	consentHandlers := consent.NewHandlers(consent.HandlersConfig{Logger: logger})
	visitHandlers := visit.NewHandlers(visit.HandlersConfig{Logger: logger})
	clinicalHandlers := clinical.NewHandlers(clinical.HandlersConfig{Logger: logger})
	historyHandlers := history.NewHandlers(history.HandlersConfig{Logger: logger})
	allergyHandlers := allergy.NewHandlers(allergy.HandlersConfig{
		Clock: clock.Real{}, Logger: logger,
	})
	counselingHandlers := counseling.NewHandlers(counseling.HandlersConfig{Logger: logger})
	qualityHandlers := quality.NewHandlers(quality.HandlersConfig{Clock: clock.Real{}, Logger: logger})
	assessmentHandlers := assessment.NewHandlers(assessment.HandlersConfig{Clock: clock.Real{}, Logger: logger})
	nutritionHandlers := nutrition.NewHandlers(nutrition.HandlersConfig{Clock: clock.Real{}, Logger: logger})
	exerciseHandlers := exercise.NewHandlers(exercise.HandlersConfig{Clock: clock.Real{}, Logger: logger})
	jobHandlers := jobs.NewHandlers(jobs.HandlersConfig{Clock: clock.Real{}, Logger: logger})
	offlineHandlers := offline.NewHandlers(offline.HandlersConfig{Clock: clock.Real{}, Logger: logger})
	aiHandlers := ai.NewHandlers(ai.HandlersConfig{Clock: clock.Real{}, Logger: logger})
	synthesisHandlers := synthesis.NewHandlers(synthesis.HandlersConfig{
		Clock: clock.Real{}, Logger: logger,
	})
	// The dashboard's handlers mount with no service behind them, like every other module
	// here: what is being checked is the route table, and requiring a database for that would
	// make the contract check something that only runs when infrastructure is up.
	dashboardHandlers := dashboard.NewHandlers(dashboard.HandlersConfig{Logger: logger})
	// CP78's safety check, mounted with no engine behind it like every other module here:
	// what is being checked is the route table and its declared permission.
	safetyCheckHandlers := medsafety.NewCheckHandlers(medsafety.CheckHandlersConfig{
		Clock: clock.Real{}, Logger: logger,
	})
	// CP80's prescription surface, with no service behind it like every other module here.
	// The state machine is nil, which is fine: what is being walked is the route table and
	// its declared permission, and no handler runs.
	prescriptionHandlers := prescription.NewHandlers(prescription.HandlersConfig{
		Clock: clock.Real{}, Logger: logger,
	})

	router, err := surface{
		Logger:         logger,
		IDs:            &ids.Sequential{Prefix: "req"},
		AllowedOrigins: []string{"http://localhost:3000"},
		MaxBodyBytes:   1024,
		RequestTimeout: 5 * time.Second,
		Health:         &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Auth:           auth.NewHandlers(auth.HandlersConfig{Logger: logger}),
		Devices:        auth.NewDeviceHandlers(auth.DeviceHandlersConfig{Logger: logger}),
		Admin:          auth.NewAdminHandlers(auth.AdminHandlersConfig{Logger: logger}),
		Audit:          audit.NewHandlers(audit.HandlersConfig{Logger: logger}),
		Patients: patient.NewHandlers(patient.HandlersConfig{
			Logger: logger,
			Sub: []func(chi.Router){
				consentHandlers.Mount, visitHandlers.MountPatient, clinicalHandlers.MountPatient,
				clinicalHandlers.MountPatientAlerts, clinicalHandlers.MountPatientCorrections,
				historyHandlers.MountPatient, allergyHandlers.MountPatient,
				assessmentHandlers.MountPatient, nutritionHandlers.MountPatient,
				exerciseHandlers.MountPatient, dashboardHandlers.MountPatient,
				safetyCheckHandlers.MountPatient, prescriptionHandlers.MountPatient,
			},
		}),
		Consent:     consentHandlers,
		Visits:      visitHandlers,
		Clinical:    clinicalHandlers,
		Terminology: terminology.NewHandlers(terminology.HandlersConfig{Logger: logger}),
		Formulary:   formulary.NewHandlers(formulary.HandlersConfig{Clock: clock.Real{}, Logger: logger}),
		MedicationRules: medsafety.NewHandlers(medsafety.HandlersConfig{
			Clock: clock.Real{}, Logger: logger,
		}),
		Prescriptions: prescriptionHandlers,
		History:       historyHandlers,
		Allergies:     allergyHandlers,
		Counseling:    counselingHandlers,
		Quality:       qualityHandlers,
		Assessments:   assessmentHandlers,
		Nutrition:     nutritionHandlers,
		Exercise:      exerciseHandlers,
		Jobs:          jobHandlers,
		AI:            aiHandlers,
		Synthesis:     synthesisHandlers,
		Offline:       offlineHandlers,
		Directory:     auth.NewDirectoryHandlers(auth.DirectoryHandlersConfig{Logger: logger}),
	}.router()
	if err != nil {
		t.Fatalf("the surface does not build: %v", err)
	}
	return router
}

// routerOperations returns the "METHOD /path" set the router actually serves.
func routerOperations(t *testing.T, router chi.Routes) map[string]bool {
	t.Helper()

	operations := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler) error {

		// A mounted subrouter with nothing in it leaves a wildcard behind. It is a
		// mount point rather than an endpoint, and documenting it would be a lie.
		route = strings.TrimSuffix(route, "/*")
		if route != "/" {
			route = strings.TrimSuffix(route, "/")
		}
		if route == "" || route == "/" || strings.Contains(route, "*") {
			return nil
		}

		operations[strings.ToUpper(method)+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking the router: %v", err)
	}
	return operations
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func TestEveryImplementedRouteIsDocumented(t *testing.T) {
	// Acceptance criterion 2. Add an endpoint without documenting it and this fails,
	// which is the entire reason the check exists — a contract nobody enforces is a
	// comment.
	spec := specOperations(t)
	router := routerOperations(t, contractRouter(t))

	var undocumented []string
	for operation := range router {
		if !spec[operation] {
			undocumented = append(undocumented, operation)
		}
	}
	sort.Strings(undocumented)

	if len(undocumented) > 0 {
		t.Errorf("these routes are served but absent from api/openapi.yaml:\n  %s\n\n"+
			"Add them to the contract. Three surfaces consume this API; an endpoint that "+
			"exists only in Go is an endpoint no client can be generated for.",
			strings.Join(undocumented, "\n  "))
	}
}

func TestEveryDocumentedRouteIsImplemented(t *testing.T) {
	// The direction people forget. A path left in the document after the endpoint was
	// renamed generates a client method that 404s — and the generated client compiles
	// perfectly while doing it.
	spec := specOperations(t)
	router := routerOperations(t, contractRouter(t))

	var unimplemented []string
	for operation := range spec {
		if !router[operation] {
			unimplemented = append(unimplemented, operation)
		}
	}
	sort.Strings(unimplemented)

	if len(unimplemented) > 0 {
		t.Errorf("these routes are documented but not served:\n  %s\n\n"+
			"Either implement them or remove them from api/openapi.yaml. Documented routes "+
			"that do not exist are generated into every client.",
			strings.Join(unimplemented, "\n  "))
	}
}

func TestTheServedRoutesAreTheOnesWeExpect(t *testing.T) {
	// A guard on the two tests above rather than a duplicate of them: if the walk or the
	// scanner silently returned nothing, both would pass by agreeing about an empty set.
	//
	// It is written out by hand deliberately. Adding an endpoint should require saying so
	// here, in a list a reviewer can read in one glance and ask "should that exist?" — the
	// question the other two tests cannot ask, because they only check that two machines
	// agree with each other.
	want := []string{
		// CP80. Removing a line from a draft is a DELETE because it is a removal — the row
		// stays, with who removed it and why, but the line comes off the sheet, and a POST to
		// `/remove` would have made the one destructive-looking act on a prescription look
		// like every other write.
		"DELETE /v1/prescriptions/{id}/items/{itemId}",
		"GET /healthz",
		"GET /readyz",
		"GET /v1/admin/roles",
		"GET /v1/admin/users",
		"GET /v1/admin/users/{id}",
		"GET /v1/alerts",
		"GET /v1/alerts/escalation",
		"GET /v1/alerts/rules",
		"GET /v1/alerts/{id}",
		"GET /v1/allergies/assertion-rates",
		"GET /v1/allergies/reactions",
		"GET /v1/assessments/instruments",
		"GET /v1/audit/alerts",
		"GET /v1/audit/break-glass",
		"GET /v1/audit/break-glass/mine",
		"GET /v1/audit/chain",
		"GET /v1/audit/events",
		"GET /v1/audit/export",
		"GET /v1/audit/kinds",
		"GET /v1/audit/signing-key",
		"GET /v1/auth/me",
		"GET /v1/auth/second-factor",
		"GET /v1/auth/sessions",
		"GET /v1/board",
		"GET /v1/consent-templates",
		"GET /v1/corrections/mine",
		"GET /v1/corrections/reasons",
		"GET /v1/corrections/{id}",
		"GET /v1/counseling/assignments",
		"GET /v1/counseling/gate/overrides",
		"GET /v1/counseling/rooms",
		"GET /v1/counseling/sessions/{sessionId}",
		"GET /v1/counseling/templates",
		"GET /v1/counseling/templates/{templateId}",
		"GET /v1/counseling/templates/{templateId}/versions/{version}",
		"GET /v1/counseling/visits/{visitId}/checklists",
		"GET /v1/counseling/visits/{visitId}/gate",
		"GET /v1/counseling/visits/{visitId}/sessions",
		"GET /v1/devices",
		"GET /v1/devices/self",
		"GET /v1/devices/{id}",
		"GET /v1/devices/{id}/events",
		"GET /v1/directory",
		"GET /v1/exercise/contraindications",
		"GET /v1/foods",
		"GET /v1/foods/measures",
		"GET /v1/formulary/catalogue",
		"GET /v1/formulary/generics",
		"GET /v1/formulary/imports",
		"GET /v1/formulary/imports/{id}",
		"GET /v1/formulary/products",
		"GET /v1/formulary/products/{id}",
		// CP75 criterion 1, and the route CP127's affordability lens calls for every
		// prescription: the price that was in force on a named day, not today's.
		"GET /v1/formulary/products/{id}/price",
		"GET /v1/formulary/products/{id}/prices",
		"GET /v1/formulary/review",
		// CP76's two-letter prescribing autocomplete, served from the in-process formulary
		// cache. Its own route rather than a mode of /products: a different question, a
		// different shape and a different ranking.
		"GET /v1/formulary/search",
		"GET /v1/history/items/{itemId}",
		"GET /v1/history/kinds",
		"GET /v1/history/uncoded",
		// CP77's medication safety rule library: the physician's authoring screens, the
		// sandbox, and the import/export a rule review happens away from the screen with.
		// Publishing and withdrawing sit behind a step-up as well as a permission.
		"GET /v1/medication-rules",
		"GET /v1/medication-rules/allergens",
		"GET /v1/medication-rules/export",
		"GET /v1/medication-rules/vocabulary",
		"GET /v1/medication-rules/{ruleId}",
		"GET /v1/observations/answers",
		"GET /v1/observations/codes",
		"GET /v1/observations/growth-curves",
		"GET /v1/observations/plausibility",
		"GET /v1/observations/reference-ranges",
		"GET /v1/observations/units",
		"GET /v1/observations/{id}",
		"GET /v1/ops/ai/evaluation-runs",
		"GET /v1/ops/ai/grounding-defects",
		"GET /v1/ops/ai/interactions",
		"GET /v1/ops/ai/interactions/{id}",
		"GET /v1/ops/ai/prompts",
		"GET /v1/ops/ai/spend",
		"GET /v1/ops/ai/synthesis-sla",
		"GET /v1/ops/jobs",
		"GET /v1/ops/jobs/health",
		"GET /v1/ops/jobs/kinds",
		"GET /v1/ops/jobs/{id}",
		"GET /v1/patients",
		"GET /v1/patients/today",
		"GET /v1/patients/{id}",
		"GET /v1/patients/{id}/alerts",
		"GET /v1/patients/{id}/allergies",
		"GET /v1/patients/{id}/allergies/history",
		"GET /v1/patients/{id}/assessments",
		"GET /v1/patients/{id}/consents",
		"GET /v1/patients/{id}/consents/history",
		"GET /v1/patients/{id}/corrections",
		// CP73's whole screen, in one request. If a second dashboard route ever appears in
		// this list, that is the checkpoint's own acceptance criterion being walked back and
		// it should be argued for rather than added.
		"GET /v1/patients/{id}/dashboard",
		"GET /v1/patients/{id}/diet",
		"GET /v1/patients/{id}/diet/days",
		"GET /v1/patients/{id}/exercise",
		"GET /v1/patients/{id}/exercise/history",
		"GET /v1/patients/{id}/exercise/options",
		"GET /v1/patients/{id}/growth",
		"GET /v1/patients/{id}/history",
		"GET /v1/patients/{id}/lifestyle-scoring",
		"GET /v1/patients/{id}/medical-history",
		"GET /v1/patients/{id}/merges",
		"GET /v1/patients/{id}/observations",
		"GET /v1/patients/{id}/observations/{code}/history",
		"GET /v1/patients/{id}/photo",
		"GET /v1/patients/{id}/prescriptions",
		// CP79's renal indicator: the eGFR in use, its date, the CKD stage it implies, and
		// whether the facility's recency window has passed. One route so that the number the
		// screen shows and the number the rules ran against cannot drift apart.
		"GET /v1/patients/{id}/renal-status",
		"GET /v1/patients/{id}/summary",
		"GET /v1/patients/{id}/timeline",
		"GET /v1/patients/{id}/timeline/spans",
		"GET /v1/patients/{id}/visits",
		// CP80. `statuses` returns the seven states and the twelve legal transitions, so a
		// screen drawing which buttons are available reads the matrix rather than keeping
		// its own copy — the only arrangement in which the screen and the trigger cannot
		// drift apart.
		"GET /v1/prescriptions/statuses",
		"GET /v1/prescriptions/{id}",
		"GET /v1/quality/flags",
		"GET /v1/quality/flags/{id}",
		"GET /v1/quality/me",
		"GET /v1/quality/operators",
		"GET /v1/quality/operators/{id}",
		"GET /v1/quality/thresholds",
		"GET /v1/stations/board",
		"GET /v1/stations/{station}/queue",
		"GET /v1/sync/batches/{id}",
		"GET /v1/sync/events",
		"GET /v1/sync/quarantine",
		"GET /v1/sync/quarantine/{id}",
		"GET /v1/sync/reference",
		"GET /v1/sync/state",
		"GET /v1/terminology/concept",
		"GET /v1/terminology/favourites",
		"GET /v1/terminology/search",
		"GET /v1/terminology/systems",
		"GET /v1/visits/today",
		"GET /v1/visits/{id}",
		"GET /v1/visits/{id}/queue",
		// The pre-consultation summary (CP71). Reading it is sensitive; asking for one is not,
		// and the two are separate permissions on purpose.
		"GET /v1/visits/{id}/synthesis",
		"GET /v1/visits/{id}/synthesis/history",
		"GET /version",
		"PATCH /v1/history/items/{itemId}",
		"PATCH /v1/patients/{id}",
		// CP80. PATCH rather than PUT: a modification changes how a medicine is taken and
		// never which medicine it is, so the request is partial by construction. Changing the
		// drug is removing one line and adding another — two events, two rows, both visible —
		// rather than a silent substitution on a line that keeps its identity.
		"PATCH /v1/prescriptions/{id}/items/{itemId}",
		"POST /v1/admin/users",
		"POST /v1/admin/users/{id}/password",
		"POST /v1/admin/users/{id}/roles",
		"POST /v1/admin/users/{id}/roles/{role}/revoke",
		"POST /v1/admin/users/{id}/second-factor/reset",
		"POST /v1/admin/users/{id}/sessions/end",
		"POST /v1/admin/users/{id}/status",
		"POST /v1/alerts/{id}/acknowledge",
		"POST /v1/allergies/assertions/{assertionId}/withdraw",
		"POST /v1/allergies/{allergyId}/withdraw",
		"POST /v1/assessments",
		"POST /v1/assessments/score",
		"POST /v1/audit/alerts/{id}/acknowledge",
		"POST /v1/audit/break-glass",
		"POST /v1/audit/break-glass/{id}/acknowledge",
		"POST /v1/audit/break-glass/{id}/end",
		"POST /v1/auth/active-role",
		"POST /v1/auth/device/enrol",
		"POST /v1/auth/login",
		"POST /v1/auth/login/second-factor",
		"POST /v1/auth/logout",
		"POST /v1/auth/logout-all",
		"POST /v1/auth/refresh",
		"POST /v1/auth/second-factor/confirm",
		"POST /v1/auth/second-factor/disable",
		"POST /v1/auth/second-factor/enrol",
		"POST /v1/auth/second-factor/recovery-codes",
		"POST /v1/auth/step-up",
		"POST /v1/board/reroute/{entryId}",
		"POST /v1/corrections/{id}/apply",
		"POST /v1/corrections/{id}/reject",
		"POST /v1/counseling/sessions",
		"POST /v1/counseling/sessions/{sessionId}/complete",
		"POST /v1/counseling/sessions/{sessionId}/ticks",
		"POST /v1/counseling/sessions/{sessionId}/unticks",
		"POST /v1/counseling/templates",
		"POST /v1/counseling/templates/{templateId}/versions",
		"POST /v1/counseling/templates/{templateId}/versions/{version}/publish",
		"POST /v1/counseling/visits/{visitId}/gate/override",
		"POST /v1/devices",
		"POST /v1/devices/self/rotate-key",
		"POST /v1/devices/{id}/enrolments",
		"POST /v1/devices/{id}/lost",
		"POST /v1/devices/{id}/reinstate",
		"POST /v1/devices/{id}/revoke",
		"POST /v1/devices/{id}/suspend",
		"POST /v1/diet",
		"POST /v1/diet/{id}/withdraw",
		"POST /v1/exercise/assessments",
		"POST /v1/exercise/plans",
		"POST /v1/formulary/imports",
		"POST /v1/formulary/products",
		"POST /v1/formulary/products/{id}/prices",
		"POST /v1/formulary/products/{id}/reinstate",
		"POST /v1/formulary/products/{id}/withdraw",
		"POST /v1/formulary/review/{id}/complete",
		"POST /v1/history/items/{itemId}/confirm",
		"POST /v1/history/items/{itemId}/remove",
		"POST /v1/medication-rules",
		"POST /v1/medication-rules/allergens/cross-reactions/{id}/approve",
		"POST /v1/medication-rules/import",
		"POST /v1/medication-rules/preview",
		"POST /v1/medication-rules/sandbox",
		"POST /v1/medication-rules/{ruleId}/versions",
		"POST /v1/medication-rules/{ruleId}/versions/{versionId}/publish",
		"POST /v1/medication-rules/{ruleId}/withdraw",
		"POST /v1/observations",
		"POST /v1/observations/batch",
		"POST /v1/observations/derive",
		"POST /v1/observations/{id}/flag",
		"POST /v1/ops/ai/grounding-defects/{id}/review",
		"POST /v1/ops/jobs/kinds/{kind}/pause",
		"POST /v1/ops/jobs/kinds/{kind}/resume",
		"POST /v1/ops/jobs/{id}/cancel",
		"POST /v1/ops/jobs/{id}/retry",
		"POST /v1/patients",
		"POST /v1/patients/check-duplicates",
		"POST /v1/patients/{id}/allergies",
		"POST /v1/patients/{id}/allergies/assert",
		"POST /v1/patients/{id}/consents",
		"POST /v1/patients/{id}/consents/evidence-url",
		"POST /v1/patients/{id}/consents/{type}/revoke",
		// A physician answering one of §8's drafted suggestions (CP73). A write, and small,
		// and deliberately not folded into the read: a screen that had to re-read everything
		// to record a rejection would make the cheapest interaction the most expensive
		// request.
		"POST /v1/patients/{id}/dashboard/suggestions/{ref}/decision",
		"POST /v1/patients/{id}/medical-history",
		"POST /v1/patients/{id}/merge",
		"POST /v1/patients/{id}/photo",
		"POST /v1/patients/{id}/photo/upload-url",
		// CP78's deterministic safety engine. §7.2 names
		// `POST /prescriptions/{id}/safety-check`; CP80's prescription aggregate does not
		// exist, so the engine takes the proposed item list it actually reads and hangs off
		// the patient, whose clinical picture is the other half of the multiplication. When
		// CP80 lands, its route is a loader in front of the same evaluation and this one stays.
		"POST /v1/patients/{id}/safety-check",
		// CP80. The clinic's primary output artefact.
		//
		// Creation is `POST /v1/prescriptions` with the visit in the body rather than
		// `POST /v1/visits/{id}/prescriptions`: a prescription's identity is its own — it is
		// corrected, superseded and read years later without anybody caring which visit it
		// was written at — and nesting it would have meant a sub-router hook on the visit
		// module for one route.
		//
		// **There is no sign, print, dispense or QA-clear route here, and that is the point.**
		// Those four transitions exist in `core.prescription_transition` and in the service;
		// their screens and their guards belong to CP83, CP84, CP89 and CP118. A signing
		// endpoint without step-up 2FA would be a hole, not a head start.
		"POST /v1/prescriptions",
		"POST /v1/prescriptions/{id}/cancel",
		"POST /v1/prescriptions/{id}/corrections",
		"POST /v1/prescriptions/{id}/items",
		// §7.2's own route, at last. CP78 built the engine around a proposed item list and
		// said in as many words that CP80's route would be a loader in front of the same
		// `Engine.Check`. It is.
		"POST /v1/prescriptions/{id}/safety-check",
		"POST /v1/prescriptions/{id}/submit",
		"POST /v1/quality/flags/{id}/resolve",
		"POST /v1/stations/queue/{entryId}/leave",
		"POST /v1/stations/{station}/call-next",
		"POST /v1/sync/events",
		"POST /v1/sync/quarantine/{id}/discard",
		"POST /v1/sync/quarantine/{id}/release",
		"POST /v1/visits",
		"POST /v1/visits/{id}/abandon",
		"POST /v1/visits/{id}/close",
		"POST /v1/visits/{id}/encounters",
		"POST /v1/visits/{id}/encounters/{encounterId}/finish",
		"POST /v1/visits/{id}/queue",
		"POST /v1/visits/{id}/reopen",
		"POST /v1/visits/{id}/synthesis",
		"PUT /v1/counseling/templates/{templateId}/versions/{version}",
		"PUT /v1/formulary/products/{id}",
		"PUT /v1/formulary/review/owner",
		"PUT /v1/medication-rules/{ruleId}/versions/{versionId}",
	}

	if got := sorted(routerOperations(t, contractRouter(t))); !reflect.DeepEqual(got, want) {
		t.Errorf("router serves %v, want %v", got, want)
	}
	if got := sorted(specOperations(t)); !reflect.DeepEqual(got, want) {
		t.Errorf("contract declares %v, want %v", got, want)
	}
}

// TestEveryRouteDeclaresItsRequirement is the route-registry audit (CP20, criterion 4).
//
// NewRouter already refuses to build a surface with an undeclared route; this test is the
// reviewer's copy of the table — which routes are public, which need only a session, and
// which permission guards each of the rest — written out by hand so that adding a route
// means saying here what it takes to reach it, in a diff somebody reads.
func TestEveryRouteDeclaresItsRequirement(t *testing.T) {
	decls, err := httpx.Declarations(contractRouter(t))
	if err != nil {
		t.Fatal(err)
	}

	public := "public"
	session := "session"
	// The union the correction workflow's answering routes admit. Written out once because it
	// is the same list on five routes, and spelled in the order the guard declares it — the
	// test compares the joined string, and a re-ordering here would be a failure that says
	// nothing about what changed.
	// The formulary's read requirement, spelled in the order formulary.Mount declares it: the
	// test compares the joined string, so a re-ordering there would be a failure that says
	// nothing about what changed.
	formularyRead := strings.Join([]string{"formulary.read", "formulary.write", "formulary.price.review"}, "|")
	// CP77's read requirement, in the order medsafety.Mount declares it. Wide within the roles
	// that hold any of the three, because a physician reading his own library and a QA officer
	// reading it before a clearance are the same read.
	ruleRead := strings.Join([]string{
		"medication.rule.read", "medication.rule.write", "medication.rule.publish"}, "|")
	correctionAnswer := strings.Join(append(
		[]string{"observation.read.values", "observation.correct.approve"},
		"observation.write.anthro", "observation.write.vitals", "observation.write.lifestyle",
		"observation.write.history", "observation.write.nutrition", "observation.write.exercise",
		"observation.write.exam",
	), "|")
	want := map[string]string{
		"GET /healthz": public,
		"GET /readyz":  public,
		"GET /version": public,

		"POST /v1/auth/login":               public,
		"POST /v1/auth/login/second-factor": public,
		"POST /v1/auth/refresh":             public,
		"POST /v1/auth/device/enrol":        public,

		"GET /v1/auth/me":                            session,
		"GET /v1/auth/sessions":                      session,
		"POST /v1/auth/logout":                       session,
		"POST /v1/auth/logout-all":                   session,
		"POST /v1/auth/active-role":                  session,
		"GET /v1/auth/second-factor":                 session,
		"POST /v1/auth/second-factor/enrol":          session,
		"POST /v1/auth/second-factor/confirm":        session,
		"POST /v1/auth/step-up":                      session,
		"POST /v1/auth/second-factor/disable":        session, // plus a step-up
		"POST /v1/auth/second-factor/recovery-codes": session, // plus a step-up
		"GET /v1/devices/self":                       session, // plus a verified device
		"POST /v1/devices/self/rotate-key":           session, // plus a verified device

		"GET /v1/admin/roles":                           "user.read",
		"GET /v1/admin/users":                           "user.read",
		"GET /v1/admin/users/{id}":                      "user.read",
		"POST /v1/admin/users":                          "user.invite", // plus a step-up
		"POST /v1/admin/users/{id}/status":              "user.invite|user.suspend|user.deactivate",
		"POST /v1/admin/users/{id}/roles":               "role.grant",  // plus a step-up
		"POST /v1/admin/users/{id}/roles/{role}/revoke": "role.revoke", // plus a step-up
		"POST /v1/admin/users/{id}/sessions/end":        "user.credential.reset",
		"POST /v1/admin/users/{id}/password":            "user.credential.reset",
		"POST /v1/admin/users/{id}/second-factor/reset": "user.credential.reset",

		"GET /v1/audit/events":                        "audit.read",
		"GET /v1/audit/kinds":                         session,
		"GET /v1/audit/chain":                         "audit.read",
		"GET /v1/audit/export":                        "audit.read",
		"GET /v1/audit/signing-key":                   session,
		"GET /v1/audit/alerts":                        "audit.read",
		"POST /v1/audit/alerts/{id}/acknowledge":      "audit.read",
		"POST /v1/audit/break-glass":                  "patient.read.clinical|patient.read.demographics", // plus a step-up
		"GET /v1/audit/break-glass":                   "audit.read",
		"GET /v1/audit/break-glass/mine":              session,
		"POST /v1/audit/break-glass/{id}/end":         session, // one's own, or audit.read
		"POST /v1/audit/break-glass/{id}/acknowledge": "audit.read",

		// Registration. The plan names a patient.create permission; the catalogue has no
		// such code, and at this clinic's size registering and correcting are one
		// authority held by one desk. ADR-0020 records the deviation; splitting them is a
		// catalogue change and Dr Nahid's to make.
		"GET /v1/consent-templates":                     "patient.consent.record",
		"GET /v1/patients/{id}/consents":                "patient.read.demographics",
		"GET /v1/patients/{id}/consents/history":        "patient.read.demographics",
		"POST /v1/patients/{id}/consents":               "patient.consent.record",
		"POST /v1/patients/{id}/consents/evidence-url":  "patient.consent.record",
		"POST /v1/patients/{id}/consents/{type}/revoke": "patient.consent.revoke",
		"GET /v1/patients":                              "patient.read.demographics",
		"GET /v1/patients/today":                        "patient.read.demographics",
		"GET /v1/patients/{id}/summary":                 "patient.read.demographics",
		"GET /v1/patients/{id}/photo":                   "patient.read.demographics",
		"GET /v1/board":                                 "board.read",
		"GET /v1/observations/codes":                    "observation.read.values",
		"GET /v1/observations/units":                    "observation.read.values",
		// Reference data a station app fetches once and applies offline (CP46, CP47, CP49).
		// Read by every signed-in clinical role: none of it is about a patient.
		"GET /v1/observations/plausibility":     "observation.read.values",
		"GET /v1/observations/answers":          "observation.read.values",
		"GET /v1/observations/reference-ranges": "observation.read.values",

		// Critical values (CP50). Reading the board and acknowledging an alert are
		// separate permissions on purpose: the officer who typed the value already knows
		// about it, and a clinic where they can close their own alerts is a clinic that
		// can clear its board without a clinician ever seeing one.
		"GET /v1/alerts":                     "alert.read",
		"GET /v1/alerts/rules":               "alert.read",
		"GET /v1/alerts/escalation":          "alert.read",
		"GET /v1/alerts/{id}":                "alert.read",
		"GET /v1/patients/{id}/alerts":       "alert.read",
		"POST /v1/alerts/{id}/acknowledge":   "alert.acknowledge",
		"GET /v1/observations/growth-curves": "observation.read.values",
		// The physician's dashboard (CP73). `patient.read.clinical` and not a permission of
		// its own: the screen is the patient's whole clinical picture, which is exactly what
		// §4.4 blinds registration and the pharmacist from. Answering a drafted suggestion is
		// narrower — an act rather than a look — and has its own.
		"GET /v1/patients/{id}/dashboard":                             "patient.read.clinical",
		"POST /v1/patients/{id}/dashboard/suggestions/{ref}/decision": "ai.suggestion.approve",
		// This child's own percentiles, which are.
		"GET /v1/patients/{id}/growth":                      "observation.read.values",
		"GET /v1/observations/{id}":                         "observation.read.values",
		"GET /v1/patients/{id}/observations":                "observation.read.values",
		"GET /v1/patients/{id}/observations/{code}/history": "observation.read.values",
		// One endpoint for every station, so the route guard asks for the union: a caller
		// holding none of these has no business here at all. The permission the write
		// actually needs is the one the *code* declares, checked in the handler against
		// the active role — see internal/clinical/http.go (CP42).
		"POST /v1/observations": "observation.write.anthro|observation.write.vitals|" +
			"observation.write.lifestyle|observation.write.history|" +
			"observation.write.nutrition|observation.write.exercise|observation.write.exam",
		"POST /v1/observations/derive": "observation.write.anthro|observation.write.vitals|" +
			"observation.write.lifestyle|observation.write.history|" +
			"observation.write.nutrition|observation.write.exercise|observation.write.exam",
		// A whole station form in one transaction (CP45). The same union on the route; the
		// per-code permission is still checked per value against the active role, by the
		// same helper the single write uses — a batch is not a way around CP41's rule.
		"POST /v1/observations/batch": "observation.write.anthro|observation.write.vitals|" +
			"observation.write.lifestyle|observation.write.history|" +
			"observation.write.nutrition|observation.write.exercise|observation.write.exam",
		"POST /v1/board/reroute/{entryId}": "visit.reroute",
		"GET /v1/stations/board":           "visit.read",
		"GET /v1/stations/{station}/queue": "visit.read",
		"GET /v1/visits/{id}/queue":        "visit.read",
		// CP71. Reading a summary is `ai.synthesis.read`, which is sensitive: the narrative is the
		// patient's whole clinical picture in prose. Asking for one is narrower and separate — the
		// exercise specialist who finishes the last station presses the button and never reads the
		// answer. The SLA report is the queue's own permission, because it is a report about a job
		// kind's deadline with no patient in it.
		"GET /v1/visits/{id}/synthesis":           "ai.synthesis.read",
		"GET /v1/visits/{id}/synthesis/history":   "ai.synthesis.read",
		"POST /v1/visits/{id}/synthesis":          "ai.synthesis.request",
		"GET /v1/ops/ai/synthesis-sla":            "ops.jobs.read",
		"POST /v1/stations/queue/{entryId}/leave": "visit.attend",

		// The offline sync protocol (CP65). Pushing is guarded by the **union of every station
		// write permission** rather than by a new `sync.push`, because pushing is not a
		// privileged act: it is the same clinical write the station already had the right to
		// make, arriving later. A dedicated permission would have meant "may write clinical
		// data", held by everybody who has any of the others, and the first access review to
		// look at it would have granted it to somebody who should have had none of them. What
		// each event may actually do is judged event by event, where it always was.
		"POST /v1/sync/events":      "observation.write.anthro|observation.write.vitals|observation.write.lifestyle|observation.write.history|observation.write.nutrition|observation.write.exercise|observation.write.exam|counseling.tick|allergy.write|history.write|visit.attend",
		"GET /v1/sync/batches/{id}": "observation.write.anthro|observation.write.vitals|observation.write.lifestyle|observation.write.history|observation.write.nutrition|observation.write.exercise|observation.write.exam|counseling.tick|allergy.write|history.write|visit.attend",
		"GET /v1/sync/state":        "observation.write.anthro|observation.write.vitals|observation.write.lifestyle|observation.write.history|observation.write.nutrition|observation.write.exercise|observation.write.exam|counseling.tick|allergy.write|history.write|visit.attend",
		"GET /v1/sync/events":       "observation.read.values|patient.read.demographics|visit.read",
		"GET /v1/sync/reference":    "observation.read.values|patient.read.demographics|visit.read",

		// The quarantine holds whole envelopes, so reading it is the only permission in this
		// system that shows a clinical value from outside the ledger — and releasing is narrower
		// still, because admitting a measurement from a device somebody refused to trust is a
		// decision about trust rather than about data.
		"GET /v1/sync/quarantine":                              "sync.quarantine.read",
		"GET /v1/sync/quarantine/{id}":                         "sync.quarantine.read",
		"POST /v1/sync/quarantine/{id}/release":                "sync.quarantine.release",
		"POST /v1/sync/quarantine/{id}/discard":                "sync.quarantine.release",
		"POST /v1/stations/{station}/call-next":                "visit.attend",
		"POST /v1/visits/{id}/queue":                           "visit.attend",
		"GET /v1/patients/{id}/visits":                         "visit.read",
		"GET /v1/visits/today":                                 "visit.read",
		"GET /v1/visits/{id}":                                  "visit.read",
		"POST /v1/visits":                                      "visit.open",
		"POST /v1/visits/{id}/abandon":                         "visit.close",
		"POST /v1/visits/{id}/close":                           "visit.close",
		"POST /v1/visits/{id}/reopen":                          "visit.close",
		"POST /v1/visits/{id}/encounters":                      "visit.attend",
		"POST /v1/visits/{id}/encounters/{encounterId}/finish": "visit.attend",
		"GET /v1/patients/{id}/history":                        "patient.read.demographics",
		"GET /v1/patients/{id}/timeline":                       "patient.read.demographics",
		"GET /v1/patients/{id}/timeline/spans":                 "patient.read.demographics",
		// A high-impact field (date of birth, sex, English name) also needs a step-up,
		// demanded by the handler rather than the route: whether one is required depends on
		// what actually changed, which is only known once the body is read.
		"PATCH /v1/patients/{id}":      "patient.write.demographics",
		"POST /v1/patients/{id}/photo": "patient.write.demographics",
		// CP78. Its own permission rather than medication.rule.read: reading the rule library
		// is reading a drug label, and running a check is reading this patient's kidney
		// function, diagnoses and allergies. Granted to the prescribers and to QA, which is
		// CP83 re-running the checks as part of clearance.
		"POST /v1/patients/{id}/safety-check": "medication.safety.check",
		// CP79's renal indicator holds the safety-check permission rather than one of its
		// own. The object is the same patient's kidney function; a separate grant would have
		// meant "may read a patient's renal function but may not check a prescription against
		// it", which describes nobody in this clinic.
		"GET /v1/patients/{id}/renal-status": "medication.safety.check",
		// CP80. **No new permission.** CP15's catalogue already holds `prescription.draft`
		// and `prescription.read`, granted against §4.4's access matrix and enforced by
		// invariant 43. The first draft of the checkpoint added a `prescription.write` beside
		// them; it duplicated `draft`, and an ON CONFLICT clause next to it silently flipped
		// `prescription.read` to sensitive, which broke the access matrix on the next verify.
		// The invariant caught it.
		"POST /v1/prescriptions":                       "prescription.draft",
		"GET /v1/prescriptions/statuses":               "prescription.read",
		"GET /v1/prescriptions/{id}":                   "prescription.read",
		"GET /v1/patients/{id}/prescriptions":          "prescription.read",
		"POST /v1/prescriptions/{id}/items":            "prescription.draft",
		"PATCH /v1/prescriptions/{id}/items/{itemId}":  "prescription.draft",
		"DELETE /v1/prescriptions/{id}/items/{itemId}": "prescription.draft",
		"POST /v1/prescriptions/{id}/submit":           "prescription.draft",
		"POST /v1/prescriptions/{id}/cancel":           "prescription.draft",
		// Correcting is `prescription.draft` and not a permission of its own, because what a
		// correction *is* is writing a new prescription. A separate grant would have created
		// the role that may supersede a signed prescription without being able to write one,
		// which is not a person this clinic has.
		"POST /v1/prescriptions/{id}/corrections": "prescription.draft",
		// CP78's permission, reused rather than duplicated: the object is the same patient's
		// clinical picture whether the items come from a request body or from a saved draft.
		"POST /v1/prescriptions/{id}/safety-check": "medication.safety.check",
		"POST /v1/patients/{id}/photo/upload-url":  "patient.write.demographics",
		"POST /v1/patients":                        "patient.write.demographics",
		"POST /v1/patients/check-duplicates":       "patient.write.demographics",
		"GET /v1/patients/{id}":                    "patient.read.demographics",
		"GET /v1/patients/{id}/merges":             "patient.read.demographics",
		"POST /v1/patients/{id}/merge":             "patient.merge", // plus a step-up

		// Counselling templates (CP55). Publishing is separate from writing because saving
		// a draft is cheap and reversible, while publishing puts a checklist on every phone
		// on the floor and freezes it forever. It additionally carries a step-up, which the
		// route table cannot show.
		"GET /v1/counseling/rooms":                                              "counseling.template.read",
		"GET /v1/counseling/assignments":                                        "counseling.template.read",
		"GET /v1/counseling/templates":                                          "counseling.template.read",
		"POST /v1/counseling/templates":                                         "counseling.template.write",
		"GET /v1/counseling/templates/{templateId}":                             "counseling.template.read",
		"POST /v1/counseling/templates/{templateId}/versions":                   "counseling.template.write",
		"GET /v1/counseling/templates/{templateId}/versions/{version}":          "counseling.template.read",
		"PUT /v1/counseling/templates/{templateId}/versions/{version}":          "counseling.template.write",
		"POST /v1/counseling/templates/{templateId}/versions/{version}/publish": "counseling.template.publish",

		// Counselling on the floor (CP56). Reading a session is not ticking one: the
		// physician's panel and the traffic board read and never write, and a panel that
		// needed `counseling.tick` would be a physician's screen carrying the right to write
		// on somebody else's checklist. Starting a session *is* a tick permission — opening a
		// checklist for a patient is the first act of counselling them.
		"POST /v1/counseling/sessions":                      "counseling.tick",
		"GET /v1/counseling/sessions/{sessionId}":           "counseling.session.read",
		"POST /v1/counseling/sessions/{sessionId}/ticks":    "counseling.tick",
		"POST /v1/counseling/sessions/{sessionId}/unticks":  "counseling.tick",
		"POST /v1/counseling/sessions/{sessionId}/complete": "counseling.tick",
		"GET /v1/counseling/visits/{visitId}/sessions":      "counseling.session.read",

		// The correction workflow (CP62). Flagging is its own permission — saying "that number
		// looks wrong" is a clinical judgement and must not require the authority to change
		// somebody else's work.
		//
		// Answering is the long list, and the length is the point. A request is routed to
		// *whoever typed the value*, and asking that person to hold a permission before they may
		// fix their own mistake is how the mistakes stay. The first version guarded these on
		// `observation.read.values` as a stand-in for "a clinical user at all", which refused the
		// field worker — who records values and does not browse the record — from the one request
		// addressed to them. So the guard is every write permission plus the supervisor's: wide
		// enough to admit anybody a request can name, narrow enough to keep HR, the pharmacist
		// and registration out of the workflow entirely. Which act it was — an operator's own fix
		// or a supervisor's override — is decided by the handler from the caller's permissions,
		// and whether this request is theirs at all is decided by the service.
		// The vocabulary is reference data with no patient in it, so its guard is wider than the
		// answering routes': it also admits whoever may *flag*. Registration holds the flag
		// permission and none of the answering ones, so the narrower guard meant a clerk could
		// raise a flag and could not read the list of reasons the flag form requires — which
		// meant they could not flag at all.
		"GET /v1/corrections/reasons":       "observation.correct.request|" + correctionAnswer,
		"GET /v1/corrections/mine":          correctionAnswer,
		"GET /v1/corrections/{id}":          correctionAnswer,
		"POST /v1/corrections/{id}/apply":   correctionAnswer,
		"POST /v1/corrections/{id}/reject":  correctionAnswer,
		"POST /v1/observations/{id}/flag":   "observation.correct.request",
		"GET /v1/patients/{id}/corrections": "observation.read.values",

		// Station 7's 24-hour recall (CP59). The food table is reference data with no patient in
		// it, and the physician reading a recall at station 8 needs the names to render it — a
		// consultant seeing "RICE_BOILED, 2 CUP" and not what either word meant would be a table
		// gated for no reason. Writing is station 7's own permission.
		"GET /v1/foods":                   "observation.read.values|observation.write.nutrition",
		"GET /v1/foods/measures":          "observation.read.values|observation.write.nutrition",
		"POST /v1/diet":                   "observation.write.nutrition",
		"POST /v1/diet/{id}/withdraw":     "observation.write.nutrition",
		"GET /v1/patients/{id}/diet":      "observation.read.values|observation.write.nutrition",
		"GET /v1/patients/{id}/diet/days": "observation.read.values|observation.write.nutrition",

		// Station 8 (CP60). Note what is not in this table: there is no `GET /v1/exercises`.
		// The library is only reachable through the patient's own options route, which applies
		// the contraindication filter — a route that returned it whole would be the thing
		// criterion 1 forbids, whatever the screen did with the answer afterwards. The
		// *conditions* catalogue is unfiltered because it contains no exercises, and the
		// physician's view needs it to render a recorded contraindication by name.
		"GET /v1/exercise/contraindications":     "observation.read.values|observation.write.exercise",
		"POST /v1/exercise/assessments":          "observation.write.exercise",
		"POST /v1/exercise/plans":                "observation.write.exercise",
		"GET /v1/patients/{id}/exercise":         "observation.read.values|observation.write.exercise",
		"GET /v1/patients/{id}/exercise/options": "observation.read.values|observation.write.exercise",
		"GET /v1/patients/{id}/exercise/history": "observation.read.values|observation.write.exercise",

		// The AI gateway's outbound log (CP70). One permission rather than two, unlike the queue
		// below, because there is nothing here to manage: no pause, no retry, no knob. What there
		// is is the mitigation the checkpoint names for its own headline risk — "the scrubber plus
		// a human-reviewable outbound log" — and a log nobody can open is not reviewable.
		//
		// Sensitive, and this is the argument worth reading twice: the payload names no person, by
		// construction and by three separate checks, and it carries that person's clinical picture
		// in full. §4.4 blinds registration and the pharmacist from a patient's diagnoses; handing
		// them the same diagnoses with the name removed would be that rule defeated by a
		// technicality.
		"GET /v1/ops/ai/interactions":      "ai.gateway.read",
		"GET /v1/ops/ai/interactions/{id}": "ai.gateway.read",
		"GET /v1/ops/ai/spend":             "ai.gateway.read",
		"GET /v1/ops/ai/prompts":           "ai.gateway.read",
		// CP72. Reading a grounding defect is reading the outbound log's answer with one sentence
		// highlighted, so it is the same permission and the same three roles; a second read
		// permission over the same content would be an access rule kept in step with the one it
		// copies, and the day it drifted the drift would be permissive.
		"GET /v1/ops/ai/grounding-defects": "ai.gateway.read",
		"GET /v1/ops/ai/evaluation-runs":   "ai.gateway.read",
		// Pronouncing on one is a different act: it is the sole source of the false-positive rate
		// that any future argument for loosening the check will be made with, and a clinic should
		// be able to grant it separately from the ability to read the log.
		"POST /v1/ops/ai/grounding-defects/{id}/review": "ai.quality.review",

		// The background work queue (CP69). Reading it and touching it are separate permissions,
		// the same split CP50 made between reading the alert board and acknowledging an alert:
		// retrying a dead-lettered job runs code against a patient's record, and pausing a kind
		// stops the synthesis §7.1 promises will be ready before the consultation.
		"GET /v1/ops/jobs":                      "ops.jobs.read",
		"GET /v1/ops/jobs/health":               "ops.jobs.read",
		"GET /v1/ops/jobs/kinds":                "ops.jobs.read",
		"GET /v1/ops/jobs/{id}":                 "ops.jobs.read",
		"POST /v1/ops/jobs/{id}/retry":          "ops.jobs.manage",
		"POST /v1/ops/jobs/{id}/cancel":         "ops.jobs.manage",
		"POST /v1/ops/jobs/kinds/{kind}/pause":  "ops.jobs.manage",
		"POST /v1/ops/jobs/kinds/{kind}/resume": "ops.jobs.manage",

		// Station 3's questionnaires (CP58). Reading the catalogue is reading published
		// literature with no patient in it, so the guard is "anybody who works with values" —
		// a nutritionist who cannot see what station 3 asks cannot follow up on it. Writing is
		// station 3's own permission and deliberately not a new one: an assessment is lifestyle
		// data in another shape, and a second grant would be an access review with one more line
		// and no more meaning.
		"GET /v1/assessments/instruments":         "observation.read.values|observation.write.lifestyle",
		"POST /v1/assessments":                    "observation.write.lifestyle",
		"POST /v1/assessments/score":              "observation.write.lifestyle",
		"GET /v1/patients/{id}/assessments":       "observation.read.values|observation.write.lifestyle",
		"GET /v1/patients/{id}/lifestyle-scoring": "observation.read.values|observation.write.lifestyle",

		// The operator quality record (CP63). Two rules, and the gap between them is the point.
		//
		// **Your own record needs a session and nothing else.** The plan's stated risk here is
		// that a metric which feels punitive makes staff hide their errors; an operator who has
		// to be granted something before they may see their own error count is an operator who
		// will assume the count is being kept from them. The handler reads the caller's own id
		// from the session, so there is no version of it that returns somebody else's work, and
		// there is no patient in the response. The threshold list is a session for the same
		// reason: somebody who can see a flag on their own record should be able to read what
		// raised it without asking a supervisor to explain.
		//
		// **Somebody else's needs `quality.read.team`** — deliberately not `hr.performance.read`,
		// which already exists and which HR holds. ADR-0029 has the argument: the plan puts
		// performance-linked pay and discipline out of scope, and a permission handing an
		// operator's error history to the department that sets pay puts it back in whatever
		// anybody intends by it.
		"GET /v1/quality/me":                  session,
		"GET /v1/quality/thresholds":          session,
		"GET /v1/quality/operators":           "quality.read.team",
		"GET /v1/quality/operators/{id}":      "quality.read.team",
		"GET /v1/quality/flags":               "quality.read.team",
		"GET /v1/quality/flags/{id}":          "quality.read.team",
		"POST /v1/quality/flags/{id}/resolve": "quality.flag.resolve",

		// The attribution directory (CP61). A session and nothing more: every clinical screen
		// renders attribution, so every role that may see a value may see who entered it — and
		// there is no patient in the response.
		"GET /v1/directory": session,
		// Which checklists a visit calls for is asked by two people: a counsellor before
		// starting, and the physician's panel (CP57) explaining why a gate held a patient. A
		// panel that could see what a visit *was* walked through but not what it *should have
		// been* could not say why. So either permission answers it.
		"GET /v1/counseling/visits/{visitId}/checklists": "counseling.tick|counseling.session.read",

		// The gate and its valve (CP57). Reading the gate is the session-read permission — the
		// physician's panel, the counsellor's phone and the board all need to know whether a
		// patient is held and why, and none of them writes. Overriding is its own permission,
		// held by nobody who merely works at a station, and the rate view is quality's, because
		// the answer to a rising override rate is a person asking why rather than a rule.
		"GET /v1/counseling/visits/{visitId}/gate":           "counseling.session.read",
		"POST /v1/counseling/visits/{visitId}/gate/override": "counseling.gate.override",
		"GET /v1/counseling/gate/overrides":                  "qa.review",

		// The allergy hard stop (CP54). Reading is `patient.read.allergies`, which the
		// pharmacist and the prescription educator already hold — §4.4 blinds them to
		// diagnoses, and an allergy is not a diagnosis: it has to reach the person handing
		// over the medicine. The rate view is QA's, because the plan's mitigation for
		// reflexive NKA is a person looking, not a rule.
		"GET /v1/allergies/reactions":                          "patient.read.allergies",
		"GET /v1/allergies/assertion-rates":                    "qa.review",
		"POST /v1/allergies/{allergyId}/withdraw":              "allergy.write",
		"POST /v1/allergies/assertions/{assertionId}/withdraw": "allergy.write",
		"GET /v1/patients/{id}/allergies":                      "patient.read.allergies",
		"GET /v1/patients/{id}/allergies/history":              "patient.read.allergies",
		"POST /v1/patients/{id}/allergies":                     "allergy.write",
		"POST /v1/patients/{id}/allergies/assert":              "allergy.write",

		// Medical history (CP53). Three permissions, because confirming is not amending:
		// "is this still true" is a question any clinician taking a history may answer,
		// and rewriting one is station 4's job.
		"GET /v1/history/kinds":                   "history.read|history.write|history.confirm",
		"GET /v1/history/uncoded":                 "history.read",
		"GET /v1/history/items/{itemId}":          "history.read",
		"POST /v1/history/items/{itemId}/confirm": "history.confirm",
		"PATCH /v1/history/items/{itemId}":        "history.write",
		"POST /v1/history/items/{itemId}/remove":  "history.write",
		"GET /v1/patients/{id}/medical-history":   "history.read",
		"POST /v1/patients/{id}/medical-history":  "history.write",

		// The coded catalogue (CP52). One permission, granted to everyone who fills in a
		// coded field, because there is no patient in these tables — see the note on the
		// grant in migration 00034.
		// The formulary (CP75). Reading is wide — a physician prescribing, a pharmacist
		// dispensing, the education officer explaining a cost to a patient. Writing is the
		// pharmacist, the physician and the administrator, per §16.1. None of the three
		// permissions is sensitive: §4.4 blinds the pharmacist from diagnoses, and the
		// pharmacist is the person §16.1 puts in charge of this.
		"GET /v1/formulary/catalogue":                formularyRead,
		"GET /v1/formulary/generics":                 formularyRead,
		"GET /v1/formulary/products":                 formularyRead,
		"GET /v1/formulary/products/{id}":            formularyRead,
		"GET /v1/formulary/products/{id}/price":      formularyRead,
		"GET /v1/formulary/products/{id}/prices":     formularyRead,
		"GET /v1/formulary/imports":                  formularyRead,
		"GET /v1/formulary/imports/{id}":             formularyRead,
		"GET /v1/formulary/review":                   formularyRead,
		"GET /v1/formulary/search":                   formularyRead,
		"POST /v1/formulary/products":                "formulary.write",
		"PUT /v1/formulary/products/{id}":            "formulary.write",
		"POST /v1/formulary/products/{id}/withdraw":  "formulary.write",
		"POST /v1/formulary/products/{id}/reinstate": "formulary.write",
		"POST /v1/formulary/products/{id}/prices":    "formulary.write",
		"POST /v1/formulary/imports":                 "formulary.write",
		"PUT /v1/formulary/review/owner":             "formulary.price.review",
		"POST /v1/formulary/review/{id}/complete":    "formulary.price.review",

		// The medication safety rule library (CP77, D-22). Reading is the physician, the
		// junior doctor, QA and the administrator; **writing and publishing are the physician
		// alone**, which is D-22 stated as a grant. Publishing, withdrawing and approving a
		// cross-reactivity mapping additionally need a step-up — that is middleware in
		// medsafety.Mount rather than a permission, and this map records the permission.
		"GET /v1/medication-rules":                                         ruleRead,
		"GET /v1/medication-rules/vocabulary":                              ruleRead,
		"GET /v1/medication-rules/allergens":                               ruleRead,
		"GET /v1/medication-rules/export":                                  ruleRead,
		"GET /v1/medication-rules/{ruleId}":                                ruleRead,
		"POST /v1/medication-rules":                                        "medication.rule.write",
		"POST /v1/medication-rules/import":                                 "medication.rule.write",
		"POST /v1/medication-rules/preview":                                "medication.rule.write",
		"POST /v1/medication-rules/sandbox":                                "medication.rule.write",
		"POST /v1/medication-rules/{ruleId}/versions":                      "medication.rule.write",
		"PUT /v1/medication-rules/{ruleId}/versions/{versionId}":           "medication.rule.write",
		"POST /v1/medication-rules/{ruleId}/versions/{versionId}/publish":  "medication.rule.publish",
		"POST /v1/medication-rules/{ruleId}/withdraw":                      "medication.rule.publish",
		"POST /v1/medication-rules/allergens/cross-reactions/{id}/approve": "medication.rule.publish",

		"GET /v1/terminology/systems":    "terminology.read",
		"GET /v1/terminology/search":     "terminology.read",
		"GET /v1/terminology/favourites": "terminology.read",
		"GET /v1/terminology/concept":    "terminology.read",

		"GET /v1/devices":                  "device.enroll|device.revoke|audit.read",
		"GET /v1/devices/{id}":             "device.enroll|device.revoke|audit.read",
		"GET /v1/devices/{id}/events":      "device.enroll|device.revoke|audit.read",
		"POST /v1/devices":                 "device.enroll",
		"POST /v1/devices/{id}/enrolments": "device.enroll",
		"POST /v1/devices/{id}/suspend":    "device.revoke",
		"POST /v1/devices/{id}/reinstate":  "device.revoke",
		"POST /v1/devices/{id}/revoke":     "device.revoke",
		"POST /v1/devices/{id}/lost":       "device.revoke",
	}

	got := map[string]string{}
	for route, req := range decls {
		switch {
		case req.IsPublic():
			got[route] = public
		case len(req.Permissions()) == 0:
			got[route] = session
		default:
			got[route] = strings.Join(req.Permissions(), "|")
		}
	}
	if !reflect.DeepEqual(got, want) {
		for route, g := range got {
			if w, ok := want[route]; !ok || w != g {
				t.Errorf("%s: declared %q, table says %q", route, g, w)
			}
		}
		for route := range want {
			if _, ok := got[route]; !ok {
				t.Errorf("%s: in the table but not served", route)
			}
		}
	}
}

// CP24: the `Idempotency-Key` header is documented on every state-changing endpoint
// inside the authenticated chain, and nowhere it cannot work.
//
// The middleware refuses a mutating request without a key (server.go), so a contract that
// failed to document one would generate a client that cannot call the endpoint at all.
// This is the check that keeps the two in step as endpoints are added.
func TestEveryMutatingEndpointDocumentsItsIdempotencyKey(t *testing.T) {
	operations, err := apispec.Operations(specRelativePath)
	if err != nil {
		t.Fatalf("%v", err)
	}

	mutating := map[string]bool{http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true}
	var checked int
	for _, op := range operations {
		if !mutating[op.Method] {
			if has(op.Parameters, "IdempotencyKey") {
				t.Errorf("%s %s is not state-changing but documents Idempotency-Key", op.Method, op.Path)
			}
			continue
		}

		// The sign-in corner sits outside the authenticated chain: there is no caller yet
		// to scope a key to, so the middleware never sees these and the contract must not
		// claim otherwise.
		if strings.HasPrefix(op.Path, "/v1/auth/") {
			if has(op.Parameters, "IdempotencyKey") {
				t.Errorf("%s %s is outside the authenticated chain and cannot honour an "+
					"Idempotency-Key; documenting one promises something the server does not do",
					op.Method, op.Path)
			}
			continue
		}
		if !strings.HasPrefix(op.Path, "/v1/") {
			continue // the operational endpoints
		}

		checked++
		if !has(op.Parameters, "IdempotencyKey") {
			t.Errorf("%s %s changes state but does not document Idempotency-Key; the "+
				"middleware refuses it with 422 and the generated client cannot supply one",
				op.Method, op.Path)
		}
		if !has(op.Responses, "409") {
			t.Errorf("%s %s takes an Idempotency-Key but does not document 409, which is "+
				"what a reused key and an attempt still in flight both return", op.Method, op.Path)
		}
	}

	// A scanner that quietly stopped finding parameters would make every assertion above
	// vacuous, which is the failure mode a conformance test must never have.
	if checked < 15 {
		t.Fatalf("only %d state-changing endpoints were checked; the scanner has probably "+
			"stopped understanding the document", checked)
	}
}

func has(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// CP25: the API's clinical store carries its synchronous projections.
//
// No route appends yet — the first is patient registration at CP29 — so this is the only
// thing standing between "the vitals strip is maintained inside the write transaction" and
// a line of assembly somebody deletes because nothing referenced it.
func TestTheClinicalStoreCarriesItsSynchronousProjections(t *testing.T) {
	synchronous := projection.Default.InMode(projection.Synchronous)
	if len(synchronous) == 0 {
		t.Fatal("no synchronous projections are registered; if that is deliberate, this test " +
			"should go, and so should the wiring in run()")
	}
	names := synchronousNames()
	if len(names) != len(synchronous) {
		t.Fatalf("the start-up line reports %v for %d synchronous projections", names, len(synchronous))
	}

	// A nil pool is enough: assembling the store must not touch the database, and a
	// clinicalStore that panicked here would panic in run() too.
	store := clinicalStore(nil)
	if store == nil {
		t.Fatal("clinicalStore returned nothing")
	}

	// The asynchronous ones must NOT be here: their failure must never fail an append
	// (criterion 4), and they run as a role this process does not hold.
	for _, p := range projection.Default.InMode(projection.Asynchronous) {
		for _, name := range names {
			if name == p.Name() {
				t.Errorf("%s is asynchronous but is attached to the append transaction", name)
			}
		}
	}
}
