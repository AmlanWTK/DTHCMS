package qa_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/clinicalterm"
	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/education"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/history"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/qa"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// Station 10 against a real database (CP83).
//
// # What each of these is for
//
// The pure tests in `rules_test.go` cover the eighteen rules. These cover the five things only a
// database can answer:
//
//  1. **A prescription cannot reach PRINTED without clearance** — structurally, through every
//     path including a direct UPDATE as the role that actually holds the grant, and then
//     mutation-tested by dropping the trigger.
//  2. **An empty rule table clears everything and still requires clearance** — the §5
//     distinction, asserted directly, because it is the one place where "safe" and "convenient"
//     look identical from a screen.
//  3. **The plan's named manual check**, automated: a diabetic with no HbA1c recorded or ordered
//     cannot clear; order the HbA1c; it clears.
//  4. **A bounce routes to the correct station with a specific reason, in both languages.**
//  5. **An override needs elevated permission, step-up and a reason**, each refused on its own.
//
// # The seed is a real Faridpur clinic morning
//
// Rahima Begum, 61, of Boalmari, type 2 diabetes for eleven years, on metformin. Not "Test
// Patient 1": a fixture whose name is a placeholder is a fixture nobody notices is wrong, and
// this record is also what the screenshots are taken against.

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

type qarig struct {
	*testsupport.DB
	pool   *pgxpool.Pool
	clock  *clock.Fixed
	events *eventstore.Store
	engine *projection.Engine

	sheets      *prescription.Store
	prescribing *prescription.Service
	values      *clinical.Store
	clinical    *clinical.Service
	store       *qa.Store
	service     *qa.Service
	counsel     *counseling.SessionService
	who         *stubWho
	server      *httptest.Server
	stepUp      *stubStepUp

	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	visit    uuid.UUID
	product  uuid.UUID

	role string
	held []string
}

// stubStepUp is the second factor an override needs.
//
// `ok` is what the test flips to prove criterion 4's second half independently: with it false the
// override is refused even when the permission is held and the reason is present.
type stubStepUp struct{ ok bool }

func (s *stubStepUp) ConsumeStepUp(context.Context, string, string, string) error {
	if s.ok {
		return nil
	}
	return errors.New("no second factor")
}

type qastaff struct {
	facility, user, device uuid.UUID
	permissions            *[]string
	role                   *string
}

func (s qastaff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      "QA01", Permissions: *s.permissions, Roles: []string{*s.role},
	}, nil
}

func (s qastaff) Authorize(ctx context.Context, caller httpx.Caller,
	anyOf []string) (context.Context, httpx.AuthzDecision) {

	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want != held {
				continue
			}
			granted := httpx.WithPrincipal(ctx, httpx.Principal{
				UserID: caller.UserID, FacilityID: caller.FacilityID,
				SessionID: caller.SessionID, Code: caller.Code,
				DeviceID: s.device.String(), Role: *s.role, Station: "STN_QA",
				DeviceAssurance: httpx.AssuranceProven,
			})
			granted = rbac.GrantedForTest(granted, caller, *s.role, "STN_QA")
			return granted, httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func (r *qarig) ctx() context.Context {
	return httpx.WithPrincipal(context.Background(), httpx.Principal{
		UserID: r.user.String(), FacilityID: r.facility.String(),
		SessionID: uuid.NewSHA1(r.user, []byte("session")).String(),
		Code:      "QA01", DeviceID: r.device.String(),
		Role: r.role, Station: "STN_QA", DeviceAssurance: httpx.AssuranceProven,
	})
}

func newQARig(t *testing.T) *qarig {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	r := &qarig{DB: base, pool: pool, user: uuid.New(), device: uuid.New(), role: "PHYSICIAN"}
	r.who = &stubWho{years: 61, sex: "female"}
	r.clock = clock.NewFixed(time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC))
	r.held = []string{qa.PermReview, qa.PermClear, qa.PermBounce, qa.PermOverride,
		qa.PermRuleWrite, prescription.PermRead, prescription.PermDraft, clinical.PermOrder,
		clinical.PermObservationRead}
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&r.facility); err != nil {
		t.Fatal(err)
	}

	r.events = eventstore.New(eventstore.Config{
		Pool: pool, Clock: r.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	r.engine = projection.NewEngineWithEvents(pool, projection.Default, r.events)
	if err := r.engine.Register(ctx); err != nil {
		t.Fatal(err)
	}

	catalogue := formulary.NewStore(pool)
	r.sheets = prescription.NewStore(pool)
	machine, err := r.sheets.Machine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.prescribing = prescription.NewService(r.sheets, r.events, machine, catalogue, r.clock)
	r.values = clinical.NewStore(pool)
	r.clinical = clinical.NewService(r.values, r.events, r.clock)

	medsafetyStore := medsafety.NewStore(pool, catalogue)
	counselingStore := counseling.NewStore(pool)
	r.counsel = counseling.NewSessionService(counselingStore, r.events, r.clock)
	historyStore := history.NewStore(pool)
	allergyStore := allergy.NewStore(pool)
	sources := qa.Sources{
		Sheets: r.sheets, Catalogue: catalogue, Values: r.values,
		Visits: visit.NewStore(pool), Histories: historyStore, Allergies: allergyStore,
		Counsel: counselingStore, Educate: education.NewStore(pool),
		Safety: medsafety.NewEngine(medsafetyStore, catalogue), Rules: medsafetyStore,
		Facts: stubFacts{},
		Who:   r.who,
		// The real observation catalogue. Wired here and not stubbed, because the thing these
		// tests most need to be true of the rendering is that it agrees with the clinic's own
		// display names rather than with a fixture that says what this test wants to read.
		Terms: clinicalterm.NewCache(pool),
	}
	r.store = qa.NewStore(pool)
	r.service = qa.NewService(r.store, r.events, sources, r.prescribing, r.clock)

	r.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r.stepUp = &stubStepUp{ok: true}
	handlers := qa.NewHandlers(qa.HandlersConfig{
		Service: r.service, Store: r.store, StepUp: r.stepUp,
		Clock: r.clock, Logger: logger,
	})
	who := qastaff{facility: r.facility, user: r.user, device: r.device,
		permissions: &r.held, role: &r.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 18, RequestTimeout: 30 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(rt chi.Router) { handlers.Mount(rt) },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.server = httptest.NewServer(router)
	t.Cleanup(r.server.Close)
	return r
}

// stubFacts is the clinical picture CP78 reads. Everything unknown, which is the fail-closed
// state and the one that makes the safety findings this station consumes appear at all.
type stubFacts struct{}

func (stubFacts) Age(context.Context, uuid.UUID, uuid.UUID) (*float64, error) {
	years := 61.0
	return &years, nil
}
func (stubFacts) Pregnancy(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "", nil
}
func (stubFacts) Renal(context.Context, uuid.UUID, uuid.UUID) (*float64, *time.Time, error) {
	return nil, nil, nil
}
func (stubFacts) Hepatic(context.Context, uuid.UUID, uuid.UUID) (string, error) { return "", nil }
func (stubFacts) Diagnoses(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
	return []string{"E11.9"}, nil
}
func (stubFacts) Allergies(context.Context, uuid.UUID, uuid.UUID) ([]medsafety.ReportedAllergy, error) {
	return []medsafety.ReportedAllergy{}, nil
}
func (stubFacts) CurrentMedications(context.Context, uuid.UUID, uuid.UUID) ([]medsafety.Item, error) {
	return []medsafety.Item{}, nil
}

// stubWho stands in for the register. A pointer so a test can move the patient's age without
// rebuilding the rig: rule 14's band is 15-49 and the default fixture is 61, so the tests that are
// about the teratogen flag say so by changing this rather than by seeding a second patient.
type stubWho struct {
	years float64
	sex   string
}

func (w *stubWho) AgeAndSex(context.Context, uuid.UUID, uuid.UUID) (*float64, string, error) {
	years := w.years
	return &years, w.sex, nil
}

func (r *qarig) seed(t *testing.T) {
	t.Helper()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'QA01', 'Shirin Akhter', 'শিরীন আক্তার', 'active')`,
		r.user, r.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'QA desk', 'tablet', 'active', now())`, r.device, r.facility); err != nil {
		t.Fatal(err)
	}
	// Rahima Begum of Boalmari, 61, eleven years of type 2 diabetes. A real Faridpur picture,
	// because a fixture whose name is a placeholder is a fixture nobody notices is wrong.
	r.patient = uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, name_bn, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          district, upazila, registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000412', 'Rahima Begum', 'রহিমা বেগম', 'female',
		        DATE '1965-03-02', 'day', 'national_id', '+8801711204412', 'active',
		        'Faridpur', 'Boalmari', $3, now())`,
		r.patient, r.facility, r.user); err != nil {
		t.Fatal(err)
	}
	r.visit = uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.visit (id, facility_id, patient_id, visit_code, visit_type, status,
		                        clinic_day, opened_at, opened_by)
		VALUES ($1, $2, $3, 'V-CP83-1', 'follow_up', 'open', current_date, now(), $4)`,
		r.visit, r.facility, r.patient, r.user); err != nil {
		t.Fatal(err)
	}
	if err := r.SQL.QueryRow(`
		SELECT p.id FROM core.medication_product p
		  JOIN core.generic g ON g.id = p.generic_id
		 WHERE p.facility_id = $1 AND lower(g.name) = 'metformin hydrochloride'
		 ORDER BY p.trade_name LIMIT 1`, r.facility).Scan(&r.product); err != nil {
		t.Fatalf("the formulary has no metformin product to prescribe: %v", err)
	}
	// The diagnosis, coded and recorded at this visit: without it rule 15 fires and every test
	// below would be testing rule 15 rather than what it means to test.
	if _, err := r.SQL.Exec(`
		INSERT INTO read.history_item (id, facility_id, patient_id, kind, code_system,
		         code_version, code, said, status, recorded_at, recorded_by, recorded_visit,
		         event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'COMORBIDITY', 'ICD10', '2019', 'E11.9',
		        'সুগার, এগারো বছর', 'ACTIVE', now(), $3, $4, gen_random_uuid(), 0)`,
		r.facility, r.patient, r.user, r.visit); err != nil {
		t.Fatalf("seeding the coded diagnosis: %v", err)
	}
	// Allergy status asserted — "no known allergy" is an answer, and rule 2 is satisfied by an
	// answer rather than by its content.
	if _, err := r.SQL.Exec(`
		INSERT INTO read.allergy_assertion (id, facility_id, patient_id, kind, reason,
		         asserted_at, asserted_by, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'NO_KNOWN_ALLERGY', '',
		        now(), $3, gen_random_uuid(), 0)`,
		r.facility, r.patient, r.user); err != nil {
		t.Fatalf("seeding the allergy assertion: %v", err)
	}
}

// draft opens a sheet with one metformin line and submits it to QA.
func (r *qarig) submitted(t *testing.T) prescription.Prescription {
	t.Helper()
	sheet, err := r.prescribing.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatalf("creating a draft: %v", err)
	}
	product := r.product
	if _, err := r.prescribing.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: sheet.ID, ProductID: &product,
		Dose: "1 tablet", DoseUnit: "mg", Frequency: "twice daily",
		DurationDays: intp(30), Route: "oral",
		InstructionsEN: "After food.", InstructionsBN: "খাবারের পরে।",
	}); err != nil {
		t.Fatalf("adding an item: %v", err)
	}
	out, err := r.prescribing.Submit(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb)
	if err != nil {
		t.Fatalf("submitting for QA: %v", err)
	}
	return out
}

func intp(n int) *int { return &n }

// productNamed finds a stocked product whose molecule matches, and fails loudly when there is
// none.
//
// Loudly rather than skipping: a test that quietly did nothing because the formulary moved is the
// green check this project's brief warns about, and invariant 131's lesson one level up.
func (r *qarig) productNamed(t *testing.T, generic string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := r.SQL.QueryRow(`
		SELECT p.id FROM core.medication_product p
		  JOIN core.generic g ON g.id = p.generic_id
		 WHERE p.facility_id = $1 AND p.is_active AND lower(g.name) LIKE lower($2) || '%'
		 ORDER BY p.trade_name LIMIT 1`, r.facility, generic).Scan(&id); err != nil {
		t.Fatalf("the formulary holds no %s to prescribe: %v", generic, err)
	}
	return id
}

// submittedWith opens a sheet carrying one named line and submits it to QA.
//
// `product` may be `uuid.Nil`, in which case the line is written by label — which is CP80's
// "a medicine this formulary does not hold" and is exactly how spironolactone is prescribed here.
func (r *qarig) submittedWith(t *testing.T, product uuid.UUID, label string) prescription.Prescription {
	t.Helper()
	sheet, err := r.prescribing.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatalf("creating a draft: %v", err)
	}
	addition := prescription.Addition{
		PrescriptionID: sheet.ID, Label: label,
		Dose: "1 tablet", DoseUnit: "mg", Frequency: "once daily",
		DurationDays: intp(30), Route: "oral",
		InstructionsEN: "After food.", InstructionsBN: "খাবারের পরে।",
	}
	if product != uuid.Nil {
		addition.ProductID = &product
	}
	if _, err := r.prescribing.AddItem(r.ctx(), addition); err != nil {
		t.Fatalf("adding %q: %v", label, err)
	}
	out, err := r.prescribing.Submit(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb)
	if err != nil {
		t.Fatalf("submitting for QA: %v", err)
	}
	return out
}

// recordCoded writes a coded observation on this visit, the way a station would.
func (r *qarig) recordCoded(t *testing.T, code, value string) {
	t.Helper()
	if _, err := r.SQL.Exec(`
		INSERT INTO read.observation (id, facility_id, patient_id, visit_id, code, category,
		         value_type, value_code, effective_at, recorded_at, source, status,
		         recorded_by, recorded_role, station_code, event_id, global_seq)
		SELECT gen_random_uuid(), $1, $2, $3, c.code, c.category, c.value_type, $5,
		       $6, $6, 'STATION', 'ACTIVE', $7, 'HISTORY', 'STN_HISTORY', gen_random_uuid(), 0
		  FROM core.observation_code c WHERE c.code = $4`,
		r.facility, r.patient, r.visit, code, value, r.clock.Now().UTC(), r.user); err != nil {
		t.Fatalf("recording %s: %v", code, err)
	}
}

// emptyRules retires every rule in this facility, which is the state §5 is about.
func (r *qarig) emptyRules(t *testing.T) {
	t.Helper()
	if _, err := r.SQL.Exec(
		`UPDATE core.qa_rule SET retired_at = now() WHERE facility_id = $1`, r.facility); err != nil {
		t.Fatal(err)
	}
}

// onlyRule retires everything except one code, so a test is about one rule.
func (r *qarig) onlyRule(t *testing.T, code string) {
	t.Helper()
	if _, err := r.SQL.Exec(
		`UPDATE core.qa_rule SET retired_at = now() WHERE facility_id = $1 AND code <> $2`,
		r.facility, code); err != nil {
		t.Fatal(err)
	}
}

// recordObservation writes a value straight into the read model.
//
// The clinical service would be the honest path, and it is used where the *act* is under test
// (the HbA1c order below goes through `clinical.Service.Order`). Here it is scaffolding for a
// measurement whose own write path CP42 already tests, and going through it would need a station
// reach, an encounter and a device for every fixture value.
func (r *qarig) recordObservation(t *testing.T, code string, value float64, visitScoped bool, daysAgo int) {
	t.Helper()
	var visitID any
	if visitScoped {
		visitID = r.visit
	}
	when := r.clock.Now().UTC().AddDate(0, 0, -daysAgo)
	if _, err := r.SQL.Exec(`
		INSERT INTO read.observation (id, facility_id, patient_id, visit_id, code, category,
		         value_type, value_num, unit, entered_num, entered_unit,
		         effective_at, recorded_at, source, status,
		         recorded_by, recorded_role, station_code, event_id, global_seq)
		SELECT gen_random_uuid(), $1, $2, $3, c.code, c.category, c.value_type, $5,
		       (SELECT u.code FROM core.unit u WHERE u.dimension = c.dimension AND u.is_canonical),
		       $5,
		       (SELECT u.code FROM core.unit u WHERE u.dimension = c.dimension AND u.is_canonical),
		       $6, $6, 'STATION', 'ACTIVE', $7, 'PHYSICIAN', 'STN_EXAMINATION',
		       gen_random_uuid(), 0
		  FROM core.observation_code c WHERE c.code = $4`,
		r.facility, r.patient, visitID, code, value, when, r.user); err != nil {
		t.Fatalf("recording %s: %v", code, err)
	}
}

// ---------------------------------------------------------------------------
// 1. Printing is impossible without clearance
// ---------------------------------------------------------------------------

// TestAPrescriptionCannotReachPrintedWithoutClearance is acceptance criterion 1.
//
// **Proved structurally, from four paths**, because a check in one of them is a check the other
// three do not make:
//
//  1. the service method CP84 will drive,
//  2. a direct UPDATE as `dthcms_app`, the role this process runs as,
//  3. a direct UPDATE as `dthcms_projector`, the role that actually holds the grant — this is
//     the one that matters, because a guarantee resting on a grant ends the day somebody widens
//     the grant, and
//  4. jumping straight to PRINTED, in case anybody ever adds an edge that skips SIGNED.
func TestAPrescriptionCannotReachPrintedWithoutClearance(t *testing.T) {
	r := newQARig(t)
	// The rule table is emptied so that this test is about the gate and nothing else. With the
	// eighteen seeded rules in play a clean clearance would need eleven fixtures, and a failure
	// would be ambiguous between "the gate held" and "a rule fired".
	r.emptyRules(t)
	sheet := r.submitted(t)

	// (1) The service.
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err == nil {
		t.Fatal("an uncleared prescription was signed through the service")
	} else if !strings.Contains(err.Error(), "no QA clearance") {
		t.Fatalf("it was refused, but not by the clearance gate: %v", err)
	}

	// (2) A plain UPDATE as the application role.
	if _, err := r.SQL.Exec(`
		UPDATE read.prescription SET status = 'SIGNED', signed_at = now(), signed_by = $2
		 WHERE id = $1`, sheet.ID, r.user); err == nil {
		t.Fatal("a direct UPDATE signed an uncleared prescription")
	}

	// (3) As the projector, which is the role that holds INSERT and UPDATE on this table.
	projector := r.OpenAs(t, "dthcms_projector_local", "dthcms_local_only")
	if _, err := projector.Exec(`
		UPDATE read.prescription SET status = 'SIGNED', signed_at = now(), signed_by = $2
		 WHERE id = $1`, sheet.ID, r.user); err == nil {
		t.Fatal("the projector role signed an uncleared prescription; the gate rests on a " +
			"grant rather than on a trigger")
	} else if !strings.Contains(err.Error(), "no QA clearance") {
		t.Fatalf("the projector was refused, but not by the clearance gate: %v", err)
	}

	// (4) Straight to PRINTED. Illegal as a transition today, and the gate is asserted anyway:
	// the guarantee has to survive a thirteenth row in `core.prescription_transition`.
	if _, err := projector.Exec(`
		UPDATE read.prescription SET status = 'PRINTED', signed_at = now(), signed_by = $2,
		       printed_at = now(), printed_by = $2 WHERE id = $1`, sheet.ID, r.user); err == nil {
		t.Fatal("a prescription reached PRINTED directly, uncleared")
	}

	// And with a clearance, it signs. A gate that refused everything would pass every assertion
	// above and would be a clinic that cannot prescribe.
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
		Acknowledged: acknowledgeAll(t, r, sheet.ID),
	}); err != nil {
		t.Fatalf("clearing the file: %v", err)
	}
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err != nil {
		t.Fatalf("a cleared prescription would not sign: %v", err)
	}
	if _, err := r.prescribing.Print(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err != nil {
		t.Fatalf("a signed prescription would not print: %v", err)
	}
}

// acknowledgeAll is every warning standing on a file, which a clearance has to cover.
func acknowledgeAll(t *testing.T, r *qarig, id uuid.UUID) []string {
	t.Helper()
	review, err := r.service.Review(r.ctx(), r.facility, id)
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{}
	for _, w := range review.Warnings() {
		codes = append(codes, w.RuleCode)
	}
	return codes
}

// TestRemovingTheGateLetsAnUnclearedPrescriptionSign is the mutation test.
//
// It drops the trigger inside the test's own database, re-runs the same assertion, and asserts
// that it now passes — which is to say, that the previous test is testing the trigger and not
// something else that happens to refuse. Then it puts the trigger back, so the rest of the suite
// runs against a whole system.
//
// A test that only asserts the refusal is a test that passes against a system whose refusal comes
// from somewhere nobody intended: a NOT NULL constraint, a foreign key, a typo in a status
// string. This is the one that says where the refusal comes from.
func TestRemovingTheGateLetsAnUnclearedPrescriptionSign(t *testing.T) {
	r := newQARig(t)
	sheet := r.submitted(t)

	if _, err := r.SQL.Exec(
		`DROP TRIGGER prescription_needs_qa_clearance_trigger ON read.prescription`); err != nil {
		t.Fatalf("dropping the gate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.SQL.Exec(`CREATE TRIGGER prescription_needs_qa_clearance_trigger
			BEFORE UPDATE ON read.prescription
			FOR EACH ROW EXECUTE FUNCTION core.prescription_needs_qa_clearance()`)
	})

	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err != nil {
		t.Fatalf("with the gate dropped the signature should succeed, which is what proves the "+
			"gate is what refuses it: %v", err)
	}
	stands, err := r.store.ClearanceStands(context.Background(), sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stands {
		t.Fatal("this prescription has a clearance, so the mutation proved nothing")
	}

	// And the invariant notices, which is the second half: a deployment that lost the trigger
	// fails `migrate verify` rather than quietly printing uncleared prescriptions.
	var message string
	err = r.SQL.QueryRow(`SELECT core.assert_the_qa_gate_is_wired()`).Scan(&message)
	if err == nil {
		t.Fatal("invariant 135 passed with the gate dropped")
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("invariant 135 failed for the wrong reason: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 2. The §5 distinction
// ---------------------------------------------------------------------------

// TestAnEmptyRuleTableClearsEverythingAndStillRequiresClearance is `docs/qa-rules.md` §5, which
// is the sentence this whole checkpoint is arranged around:
//
//	*"an empty rule table means every prescription clears, not that clearance is skipped."*
//
// Three assertions, and the third is the one that is easy to lose:
//
//  1. with no rules, the review finds nothing and would clear,
//  2. with no rules, the prescription **still cannot be signed** until somebody records a
//     clearance, and
//  3. `core.qa_clearance_stands()` has no path to `core.qa_rule` at all, so no rule row can ever
//     change the answer to (2).
func TestAnEmptyRuleTableClearsEverythingAndStillRequiresClearance(t *testing.T) {
	r := newQARig(t)
	r.emptyRules(t)
	sheet := r.submitted(t)

	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if review.RulesLive != 0 || len(review.Findings) != 0 {
		t.Fatalf("the rule table is empty and the review found something: %+v", review)
	}
	if !review.CanClear() {
		t.Fatal("an empty rule table refused a clearance")
	}
	if !strings.Contains(review.SummaryEN(), "nothing was checked") {
		t.Fatalf("the screen would not say that nothing was checked: %q", review.SummaryEN())
	}

	// (2) **The distinction.** No rules, nothing found, and the signature is still refused.
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err == nil {
		t.Fatal("an empty rule table made clearance optional, which is the exact failure §5 " +
			"names: 'every prescription clears' and 'clearance is skipped' are different, and " +
			"only one of them is safe")
	}

	// (3) The structural half: the gate function does not read the rule table, so there is no
	// row anybody could write that would turn it off. Asserted against the catalogue rather than
	// by reading the source, because the source is what a future edit changes.
	var body string
	if err := r.SQL.QueryRow(
		`SELECT prosrc FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		  WHERE n.nspname = 'core' AND p.proname = 'qa_clearance_stands'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "qa_rule") {
		t.Fatal("core.qa_clearance_stands() reads the rule table: a rule row could then make " +
			"clearance optional, which is precisely what must not be configurable")
	}

	// And once somebody records the clearance, it signs — on an empty table, with no findings,
	// because a clearance is a person having looked.
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
	}); err != nil {
		t.Fatalf("clearing on an empty rule table: %v", err)
	}
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err != nil {
		t.Fatalf("a cleared prescription on an empty rule table would not sign: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 3. The plan's named manual check, automated
// ---------------------------------------------------------------------------

// TestADiabeticWithNoHbA1cCannotClearUntilItIsOrdered is the plan's manual verification:
//
//	*"Attempt to print a diabetic patient's prescription with no HbA1c recorded or ordered;
//	confirm blocking; order the HbA1c; confirm clearance."*
func TestADiabeticWithNoHbA1cCannotClearUntilItIsOrdered(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "DIABETES_HBA1C")
	sheet := r.submitted(t)

	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Blocking()) != 1 || review.Blocking()[0].RuleCode != "DIABETES_HBA1C" {
		t.Fatalf("a diabetic with no HbA1c produced %+v", review.Findings)
	}
	if review.CanClear() {
		t.Fatal("a diabetic file with no HbA1c would have cleared")
	}
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
	}); !errors.Is(err, qa.ErrBlocked) {
		t.Fatalf("the clearance was not refused as blocked: %v", err)
	}
	// Criterion 1, on the same file: the signature is refused as well, by the database.
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err == nil {
		t.Fatal("an unblocked signature on a blocked file")
	}

	// **Order the HbA1c** — the real act, through `clinical.Service.Order`, which is the write
	// path a consultant's screen uses.
	visitID := r.visit
	if _, err := r.clinical.Order(r.ctx(), clinical.Ordering{
		EventID: uuid.New(), PatientID: r.patient, VisitID: &visitID, Code: "HBA1C",
		Note: "Bounced by QA: no result in six months.", Source: eventstore.SourceWeb,
	}); err != nil {
		t.Fatalf("ordering the HbA1c: %v", err)
	}

	review, err = r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Blocking()) != 0 {
		t.Fatalf("the HbA1c was ordered and the rule still blocks: %+v", review.Findings)
	}
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
	}); err != nil {
		t.Fatalf("clearing after the order: %v", err)
	}
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err != nil {
		t.Fatalf("a cleared file would not sign: %v", err)
	}

	// A recorded result satisfies it too, on a second file: the rule is "recorded **or**
	// ordered" and both halves have to work.
	second := r.submitted(t)
	r.recordObservation(t, "HBA1C", 74, true, 0)
	review, err = r.service.Review(r.ctx(), r.facility, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Blocking()) != 0 {
		t.Fatalf("a recorded HbA1c did not satisfy the rule: %+v", review.Findings)
	}
}

// ---------------------------------------------------------------------------
// 4. Bounce routing
// ---------------------------------------------------------------------------

// TestABounceNamesItsStationAndSaysWhyInBothLanguages is criterion 3.
func TestABounceNamesItsStationAndSaysWhyInBothLanguages(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "DIABETES_HBA1C")
	sheet := r.submitted(t)

	decision, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeBounced,
	})
	if err != nil {
		t.Fatalf("bouncing: %v", err)
	}
	if decision.BounceStation != "STN_CONSULTATION" {
		t.Fatalf("the bounce went to %q rather than to the station rule 4 names",
			decision.BounceStation)
	}
	if decision.StationEN == "" || decision.StationBN == "" {
		t.Fatalf("the bounce carries no room name in one of the two languages: %+v", decision)
	}
	for _, reason := range []string{decision.ReasonEN, decision.ReasonBN} {
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("a bounce with no reason in one language: %+v", decision)
		}
		if !strings.Contains(reason, "HBA1C") && !strings.Contains(reason, "এইচবিএ১সি") &&
			!strings.Contains(strings.ToLower(reason), "hba1c") {
			t.Errorf("the reason does not name what is missing, so the consultation room "+
				"cannot act on it: %q", reason)
		}
	}
	if decision.ReasonEN == decision.ReasonBN {
		t.Fatal("the two reasons are the same string: one of the languages is not a translation")
	}

	// The sheet went back to the prescriber, and its items are editable again — which is the
	// whole point of a bounce rather than a note on a screen.
	back, err := r.sheets.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if back.Status != prescription.StatusDraft {
		t.Fatalf("a bounced prescription is %s rather than DRAFT", back.Status)
	}
	if !back.Editable {
		t.Fatal("a bounced prescription is not editable, so nobody can fix what was wrong")
	}

	// And a bounce does not clear: the clearance gate still refuses the signature.
	stands, err := r.store.ClearanceStands(context.Background(), sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stands {
		t.Fatal("a bounce left a clearance standing")
	}

	// An officer's own words win over the finding's, in both languages.
	again := r.submitted(t)
	decision, err = r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: again.ID, Outcome: qa.OutcomeBounced,
		BounceStation: "STN_HISTORY",
		ReasonEN:      "Please confirm the insulin she is already on.",
		ReasonBN:      "তিনি যে ইনসুলিন নিচ্ছেন তা নিশ্চিত করুন।",
	})
	if err != nil {
		t.Fatalf("bouncing with the officer's own reason: %v", err)
	}
	if decision.BounceStation != "STN_HISTORY" || decision.StationBN == "" {
		t.Fatalf("the officer's station did not win: %+v", decision)
	}
	if !strings.Contains(decision.ReasonBN, "ইনসুলিন") {
		t.Fatalf("the officer's Bengali reason was replaced: %q", decision.ReasonBN)
	}
}

// TestAClearanceIsInvalidatedByALaterBounce is the clause that stops
// clear → bounce → edit → resubmit from leaving the first clearance standing over a sheet whose
// drugs have changed since. It is CP80's "reviewed and then altered" defect one step earlier.
func TestAClearanceIsInvalidatedByALaterBounce(t *testing.T) {
	r := newQARig(t)
	r.emptyRules(t)
	sheet := r.submitted(t)

	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
	}); err != nil {
		t.Fatal(err)
	}
	stands, err := r.store.ClearanceStands(context.Background(), sheet.ID)
	if err != nil || !stands {
		t.Fatalf("the clearance does not stand: %v", err)
	}

	// The officer changes their mind and bounces it. The clearance must not survive.
	r.clock.Advance(time.Minute)
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeBounced,
		BounceStation: "STN_CONSULTATION",
		ReasonEN:      "The dose is wrong.", ReasonBN: "মাত্রাটি ঠিক নেই।",
	}); err != nil {
		t.Fatal(err)
	}
	stands, err = r.store.ClearanceStands(context.Background(), sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stands {
		t.Fatal("a clearance survived a later bounce: the sheet could then be edited and signed " +
			"on a clearance that was given about different drugs")
	}

	// Resubmitted, it needs a new one.
	r.clock.Advance(time.Minute)
	if _, err := r.prescribing.Submit(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err != nil {
		t.Fatal(err)
	}
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err == nil {
		t.Fatal("a resubmitted prescription signed on the clearance given before the bounce")
	}
}

// ---------------------------------------------------------------------------
// 5. The override
// ---------------------------------------------------------------------------

// TestAnOverrideNeedsPermissionStepUpAndAReason is criterion 4, and each of the three is refused
// **on its own** — with the other two satisfied — because a test that removed all three at once
// would pass against a system enforcing any one of them.
func TestAnOverrideNeedsPermissionStepUpAndAReason(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "DIABETES_HBA1C")
	sheet := r.submitted(t)
	path := "/v1/prescriptions/" + sheet.ID.String() + "/qa/override"

	// (a) No permission. Step-up works, the reason is good.
	r.held = []string{qa.PermReview, qa.PermClear, qa.PermBounce, prescription.PermRead}
	r.stepUp.ok = true
	if status, _ := r.post(t, path, map[string]any{
		"event_id": uuid.NewString(), "reason": "Lab closed; patient travelled from Boalmari.",
	}, ""); status != http.StatusForbidden {
		t.Fatalf("an override without qa.override answered %d", status)
	}

	// (b) No step-up. The permission is held, the reason is good.
	r.held = append(r.held, qa.PermOverride)
	r.stepUp.ok = false
	if status, body := r.post(t, path, map[string]any{
		"event_id": uuid.NewString(), "reason": "Lab closed; patient travelled from Boalmari.",
	}, "no-token"); status == http.StatusCreated {
		t.Fatalf("an override was granted with no second factor: %s", body)
	}

	// (c) No reason. The permission is held and the step-up works.
	r.stepUp.ok = true
	if status, body := r.post(t, path, map[string]any{
		"event_id": uuid.NewString(), "reason": "   ",
	}, "token"); status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
		t.Fatalf("an override with no reason answered %d: %s", status, body)
	}

	// All three, and it is granted.
	status, body := r.post(t, path, map[string]any{
		"event_id": uuid.NewString(),
		"reason":   "Lab closed for Eid; the patient travelled from Boalmari and cannot return.",
	}, "token")
	if status != http.StatusCreated {
		t.Fatalf("a well-formed override answered %d: %s", status, body)
	}

	// The override records what was blocking **at the moment it was granted**, so that an HbA1c
	// ordered an hour later cannot make the record say it was for nothing.
	var blocking string
	if err := r.SQL.QueryRow(
		`SELECT array_to_string(blocking_at_grant, ',') FROM read.qa_override WHERE prescription_id = $1`,
		sheet.ID).Scan(&blocking); err != nil {
		t.Fatal(err)
	}
	if blocking != "DIABETES_HBA1C" {
		t.Fatalf("the override does not name what it overrode: %q", blocking)
	}

	// A second override is refused while the file is still with QA: it is the same act, not
	// another one, and stacking rows would make the rate view count one decision twice.
	if _, err := r.service.Override(r.ctx(), r.facility, uuid.New(), sheet.ID,
		"again", eventstore.SourceWeb); !errors.Is(err, qa.ErrAlreadyOverridden) {
		t.Fatalf("a second override was accepted: %v", err)
	}

	// And with it standing, the file clears and signs — which is what an override is for.
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
	}); err != nil {
		t.Fatalf("clearing on an override: %v", err)
	}
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err != nil {
		t.Fatalf("a file cleared on an override would not sign: %v", err)
	}
}

// TestAnOverrideOnACleanFileIsRefused: an override with nothing blocking is a row that makes the
// rate view lie, and the rate view is the only thing standing between a valve and a habit.
func TestAnOverrideOnACleanFileIsRefused(t *testing.T) {
	r := newQARig(t)
	r.emptyRules(t)
	sheet := r.submitted(t)

	if _, err := r.service.Override(r.ctx(), r.facility, uuid.New(), sheet.ID,
		"because", eventstore.SourceWeb); !errors.Is(err, qa.ErrNothingToOverride) {
		t.Fatalf("an override on a clean file was accepted: %v", err)
	}
}

// TestTheOverrideRateIsQualitysAndNotThePrescribers is `docs/qa-rules.md` §2's separation.
//
// A physician may grant an override and may not read how often they are granted; the QA officer
// watching the rate may not grant one. The answer to a rising override rate is a person asking
// why, and that person should not be the one granting them.
func TestTheOverrideRateIsQualitysAndNotThePrescribers(t *testing.T) {
	r := newQARig(t)

	// The prescriber's hat: qa.override, no qa.review.
	r.held = []string{qa.PermOverride, prescription.PermRead}
	if status, _ := r.get(t, "/v1/qa/overrides"); status != http.StatusForbidden {
		t.Fatalf("a holder of qa.override alone could read the override rate: %d", status)
	}

	// Quality's hat: qa.review, no qa.override.
	r.held = []string{qa.PermReview}
	if status, _ := r.get(t, "/v1/qa/overrides"); status != http.StatusOK {
		t.Fatalf("a holder of qa.review could not read the override rate: %d", status)
	}

	// And the permission catalogue says the same thing about the roles, which is where the
	// separation actually lives — a route is a check and a grant is a policy.
	var physicianHasReview, qaHasOverride bool
	if err := r.SQL.QueryRow(`
		SELECT EXISTS (SELECT 1 FROM core.role r JOIN core.role_permission rp ON rp.role_id = r.id
		                WHERE r.code = 'PHYSICIAN' AND rp.permission_code = 'qa.review'),
		       EXISTS (SELECT 1 FROM core.role r JOIN core.role_permission rp ON rp.role_id = r.id
		                WHERE r.code = 'QA' AND rp.permission_code = 'qa.override')`).
		Scan(&physicianHasReview, &qaHasOverride); err != nil {
		t.Fatal(err)
	}
	if physicianHasReview {
		t.Error("PHYSICIAN holds qa.review: the prescriber can watch their own override rate")
	}
	if qaHasOverride {
		t.Error("QA holds qa.override: the officer watching the rate can grant them")
	}
}

// ---------------------------------------------------------------------------
// Severity
// ---------------------------------------------------------------------------

// TestAWarnClearsWithAnAcknowledgementAndABlockDoesNot is §2's table against the database.
func TestAWarnClearsWithAnAcknowledgementAndABlockDoesNot(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "DIABETES_LIPIDS") // a WARN
	sheet := r.submitted(t)

	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Warnings()) != 1 || len(review.Blocking()) != 0 {
		t.Fatalf("expected one warning and no blocks: %+v", review.Findings)
	}

	// A clearance that does not acknowledge it is refused. Without this, WARN and "no rule" are
	// the same severity.
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
	}); !errors.Is(err, qa.ErrWarningsNotAcknowledged) {
		t.Fatalf("a clearance swallowed an unacknowledged warning: %v", err)
	}

	decision, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
		Acknowledged: []string{"DIABETES_LIPIDS"},
	})
	if err != nil {
		t.Fatalf("clearing with the warning acknowledged: %v", err)
	}
	if len(decision.Acknowledged) != 1 {
		t.Fatalf("the acknowledgement was not recorded: %+v", decision)
	}
	// **Recorded as codes, not as a boolean.** "Which ones did they wave through" is the
	// question somebody asks later.
	var stored string
	if err := r.SQL.QueryRow(
		`SELECT array_to_string(acknowledged, ',') FROM read.qa_review WHERE id = $1`,
		decision.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "DIABETES_LIPIDS" {
		t.Fatalf("the acknowledged warnings were not stored by code: %q", stored)
	}
	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err != nil {
		t.Fatalf("a file cleared over a warning would not sign: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Criterion 5 — the rule set is configurable
// ---------------------------------------------------------------------------

// TestARuleChangesWithoutARelease is acceptance criterion 5, asserted as behaviour rather than as
// the existence of a table.
//
// The same file, the same code, three different outcomes, produced by three UPDATEs:
//
//  1. as seeded, rule 4 blocks;
//  2. moved to WARN, it clears with an acknowledgement;
//  3. its window widened past the patient's last HbA1c, it does not fire at all.
//
// And then the line: **the kind is not configurable**, because a kind is a Go predicate.
func TestARuleChangesWithoutARelease(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "DIABETES_HBA1C")
	// An HbA1c from nine months ago: outside the seeded six-month window and inside a wider one.
	r.recordObservation(t, "HBA1C", 81, false, 270)
	sheet := r.submitted(t)

	var ruleID uuid.UUID
	if err := r.SQL.QueryRow(
		`SELECT id FROM core.qa_rule WHERE facility_id = $1 AND code = 'DIABETES_HBA1C'`,
		r.facility).Scan(&ruleID); err != nil {
		t.Fatal(err)
	}

	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Blocking()) != 1 {
		t.Fatalf("as seeded, rule 4 should block: %+v", review.Findings)
	}

	// (2) Severity is a row.
	warn := qa.Severity("WARN")
	if _, err := r.store.UpdateRule(context.Background(), r.facility, ruleID, r.user,
		qa.RuleChange{Severity: &warn}); err != nil {
		t.Fatalf("changing the severity: %v", err)
	}
	review, err = r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Blocking()) != 0 || len(review.Warnings()) != 1 {
		t.Fatalf("the severity change did not take effect: %+v", review.Findings)
	}

	// (3) The window is a row. Nothing recompiled, no process restarted.
	window := 365
	if _, err := r.store.UpdateRule(context.Background(), r.facility, ruleID, r.user,
		qa.RuleChange{WindowDays: &window}); err != nil {
		t.Fatalf("widening the window: %v", err)
	}
	review, err = r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Findings) != 0 {
		t.Fatalf("a nine-month-old HbA1c still fires a twelve-month rule: %+v", review.Findings)
	}

	// **The line.** A kind is a Go predicate; changing it would explain every past finding with
	// a question the rule never asked. Refused by the database, not by a Go check that a
	// refactor removes.
	if _, err := r.SQL.Exec(
		`UPDATE core.qa_rule SET kind = 'ALLERGY_STATUS_ASSERTED' WHERE id = $1`, ruleID); err == nil {
		t.Fatal("a rule changed kind: every review that cited it is now explained by a " +
			"different question")
	} else if !strings.Contains(err.Error(), "written once") {
		t.Fatalf("it was refused for the wrong reason: %v", err)
	}

	// A rule is retired, never deleted: `read.qa_review` names rules by code, and a review from
	// last year still has to be explicable.
	if _, err := r.SQL.Exec(`DELETE FROM core.qa_rule WHERE id = $1`, ruleID); err == nil {
		t.Fatal("a rule was deleted")
	}

	// And a brand-new rule of an existing shape is a row. No release.
	if _, err := r.SQL.Exec(`
		INSERT INTO core.qa_rule (facility_id, code, kind, severity, bounce_station_code,
		         window_days, params, title_en, title_bn, ordering)
		VALUES ($1, 'DIABETES_ACR', 'OBSERVATION_RECENT', 'WARN', 'STN_CONSULTATION', 365,
		        '{"codes":["CREATININE"],"accept_ordered":true,"when_diagnosis":["E11"]}'::jsonb,
		        'Diabetic with no renal function in twelve months',
		        'ডায়াবেটিস রোগী, বারো মাসে কিডনির কার্যক্ষমতা দেখা হয়নি', 95)`,
		r.facility); err != nil {
		t.Fatalf("adding a rule of an existing shape needed more than a row: %v", err)
	}
	review, err = r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Warnings()) != 1 || review.Warnings()[0].RuleCode != "DIABETES_ACR" {
		t.Fatalf("the new rule did not fire: %+v", review.Findings)
	}
}

// TestEveryKindInTheDatabaseHasAPredicate holds `core.qa_rule_kind` and `qa.AllKinds` together.
//
// A kind in the database that Go does not implement is a rule that can never fire; a kind in Go
// that the database does not know is a predicate no row can reach. Both are silent, and silence
// is the failure this station exists to prevent.
func TestEveryKindInTheDatabaseHasAPredicate(t *testing.T) {
	r := newQARig(t)

	rows, err := r.SQL.Query(`SELECT kind FROM core.qa_rule_kind ORDER BY kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	inDatabase := map[string]bool{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		inDatabase[kind] = true
	}
	inGo := map[string]bool{}
	for _, kind := range qa.AllKinds {
		inGo[string(kind)] = true
	}
	for kind := range inDatabase {
		if !inGo[kind] {
			t.Errorf("%s is a kind in the database with no predicate in Go: a rule naming it "+
				"would be present, enabled and permanently silent", kind)
		}
	}
	for kind := range inGo {
		if !inDatabase[kind] {
			t.Errorf("%s is a predicate no rule row can reach, because core.qa_rule_kind does "+
				"not have it", kind)
		}
	}

	// And every seeded rule loads, which is what says the eighteen rows of §3 are askable rather
	// than merely present.
	set, err := r.store.Ruleset(context.Background(), r.facility)
	if err != nil {
		t.Fatalf("the seeded rule set does not load: %v", err)
	}
	if len(set.Rules) < 15 {
		t.Fatalf("only %d of the eighteen seeded rules are live and valid", len(set.Rules))
	}
}

// TestTheApplicationCannotInventAQuestionShape is invariant 134, from the role that runs the
// application rather than from the migration role.
func TestTheApplicationCannotInventAQuestionShape(t *testing.T) {
	r := newQARig(t)
	app := r.OpenAs(t, "dthcms_app_local", "dthcms_local_only")

	if _, err := app.Exec(`
		INSERT INTO core.qa_rule_kind (kind, question_en, question_bn, params_doc, ordering)
		VALUES ('PATIENT_HAS_PAID', 'has the patient paid', 'রোগী টাকা দিয়েছেন কি', 'none', 999)`,
	); err == nil {
		t.Fatal("the application invented a question shape; a rule naming it would be enabled " +
			"and permanently silent")
	}
	if _, err := app.Exec(
		`DELETE FROM core.qa_rule WHERE facility_id = $1`, r.facility); err == nil {
		t.Fatal("the application deleted the rule table")
	}

	var message string
	if err := r.SQL.QueryRow(
		`SELECT core.assert_qa_rule_kinds_are_not_writable_by_the_application()`).Scan(&message); err != nil {
		t.Fatalf("invariant 134 fails against the shipped schema: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The refusals a screen depends on
// ---------------------------------------------------------------------------

// TestADecisionOnAPrescriptionThatIsNotWithQAIsRefused: a decision recorded against a signed
// prescription would be a row nothing else stops.
func TestADecisionOnAPrescriptionThatIsNotWithQAIsRefused(t *testing.T) {
	r := newQARig(t)
	r.emptyRules(t)

	sheet, err := r.prescribing.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
	}); !errors.Is(err, qa.ErrNotUnderReview) {
		t.Fatalf("a draft was cleared: %v", err)
	}
}

// TestAMissingPrescriptionAnswersTheSameWayAsOneYouMayNotSee: a 403 must not reveal whether a
// resource exists.
func TestAMissingPrescriptionAnswersTheSameWayAsOneYouMayNotSee(t *testing.T) {
	r := newQARig(t)

	missing := uuid.New()
	status, _ := r.get(t, "/v1/prescriptions/"+missing.String()+"/qa")
	if status != http.StatusNotFound {
		t.Fatalf("a prescription that does not exist answered %d", status)
	}

	// One that exists in another facility answers the same 404, not a 403 — otherwise a caller
	// enumerates prescriptions by watching which answer they get.
	var other uuid.UUID
	if err := r.SQL.QueryRow(`
		INSERT INTO core.facility (id, code, name_en, name_bn, timezone, status)
		VALUES (gen_random_uuid(), 'OTH', 'Other clinic', 'অন্য ক্লিনিক', 'Asia/Dhaka', 'active')
		RETURNING id`).Scan(&other); err != nil {
		t.Skipf("could not create a second facility: %v", err)
	}
	sheet := r.submitted(t)
	if _, err := r.SQL.Exec(
		`UPDATE read.prescription SET facility_id = $2 WHERE id = $1`, sheet.ID, other); err != nil {
		// The prescription is frozen against a facility change, which is itself the guarantee.
		// Nothing to assert here beyond the 404 above.
		t.Logf("read.prescription refuses a facility change, which is CP80's own guarantee: %v", err)
		return
	}
	status, _ = r.get(t, "/v1/prescriptions/"+sheet.ID.String()+"/qa")
	if status != http.StatusNotFound {
		t.Fatalf("a prescription in another facility answered %d rather than 404", status)
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func (r *qarig) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	resp, err := r.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func (r *qarig) post(t *testing.T, path string, body map[string]any, stepUp string) (int, string) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, r.server.URL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DTHCMS-Device", r.device.String())
	// CP12's CSRF header. Without it every POST here is refused for the same reason, which made
	// the first draft of this test pass all three of its refusals for the wrong one — the exact
	// shape of green check the brief warns about.
	req.Header.Set("X-Requested-With", "DTHCMS")
	if stepUp != "" {
		req.Header.Set(httpx.StepUpHeader, stepUp)
	}
	resp, err := r.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// ---------------------------------------------------------------------------
// The two rules that were inert until the appendix was written
// ---------------------------------------------------------------------------

// TestRule12FiresForCarbimazoleUntilTheWarningIsCounselled is `docs/qa-rules.md` A1, end to end.
//
// Before migration 00072 this rule named `AGRANULOCYTOSIS_WARNING` and no template defined it, so
// it fired for nothing and invariant 137 said so. The appendix wrote the item; this proves it
// switched the rule on **and that the way out of it exists**, which is the half that matters: a
// BLOCK whose only exit is the override is a valve becoming the process.
//
// The argument the rule is for, from §3.3: agranulocytosis is survivable only if the patient knows
// that a sore throat and fever means stop the drug and get a blood count today. A patient who was
// never told has no way to act on a symptom they will otherwise treat as flu.
func TestRule12FiresForCarbimazoleUntilTheWarningIsCounselled(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "ANTITHYROID_COUNSEL")

	sheet := r.submittedWith(t, r.productNamed(t, "carbimazole"), "")

	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Blocking()) != 1 || review.Blocking()[0].RuleCode != "ANTITHYROID_COUNSEL" {
		t.Fatalf("carbimazole with the warning uncounselled produced %+v", review.Findings)
	}
	// It bounces to counselling, which is where the item can be covered.
	if review.Blocking()[0].BounceStation != "STN_COUNSELING" {
		t.Fatalf("the bounce goes to %q rather than to counselling",
			review.Blocking()[0].BounceStation)
	}

	// **The remediation.** Note that the template is not started by the assignment: this visit is
	// coded E11, not E05, and a counsellor can still open the antithyroid checklist for it. That is
	// deliberate — a rule whose remediation depended on the visit's coding would be a dead end for
	// the patient whose thyrotoxicosis was coded somewhere else.
	var template uuid.UUID
	if err := r.SQL.QueryRow(
		`SELECT id FROM core.counseling_template WHERE code = 'ANTITHYROID'`).Scan(&template); err != nil {
		t.Fatalf("migration 00072 did not create the antithyroid checklist: %v", err)
	}
	session, err := r.counsel.Start(r.ctx(), counseling.StartSession{
		EventID: uuid.New(), PatientID: r.patient, VisitID: r.visit, TemplateID: template,
		LedgerSource: eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatalf("starting the antithyroid checklist: %v", err)
	}
	if _, err := r.counsel.Tick(r.ctx(), counseling.TickItem{
		EventID: uuid.New(), SessionID: session.ID, ItemCode: "AGRANULOCYTOSIS_WARNING",
		LedgerSource: eventstore.SourceWeb,
	}); err != nil {
		t.Fatalf("ticking the agranulocytosis warning: %v", err)
	}

	review, err = r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Findings) != 0 {
		t.Fatalf("the warning was counselled and the rule still fires: %+v", review.Findings)
	}
	if _, err := r.service.Decide(r.ctx(), r.facility, qa.Deciding{
		EventID: uuid.New(), PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared,
	}); err != nil {
		t.Fatalf("clearing after the item was covered: %v", err)
	}

	// And the counselling item carries A1's three load-bearing properties, word for word. This is
	// asserted against the database rather than against the migration's source, because what a
	// patient is told is the row and not the file that wrote it.
	var english string
	if err := r.SQL.QueryRow(
		`SELECT text_en FROM core.counseling_item WHERE item_code = 'AGRANULOCYTOSIS_WARNING'`).
		Scan(&english); err != nil {
		t.Fatal(err)
	}
	stop := strings.Index(english, "stop the medicine")
	count := strings.Index(english, "get a blood count")
	switch {
	case stop < 0 || count < 0:
		t.Fatalf("the item does not say both halves of the instruction: %q", english)
	case stop > count:
		t.Fatal("'get a blood count' comes before 'stop the medicine'. A1: a patient who waits " +
			"for the test while still taking the drug is the patient who dies of this")
	}
	if !strings.Contains(english, "Do not wait to see if it settles") {
		t.Errorf("the item drops 'do not wait to see if it settles', which exists because every "+
			"instinct says a sore throat is flu: %q", english)
	}
	for _, symptom := range []string{"sore throat", "mouth ulcers", "fever"} {
		if !strings.Contains(english, symptom) {
			t.Errorf("the item does not name %q. A1 names the three symptoms rather than saying "+
				"'signs of infection', which means nothing to somebody who is not a clinician",
				symptom)
		}
	}
}

// TestRule14AsksThePregnancyQuestionForAWomanOnSpironolactone is `docs/qa-rules.md` A2's sharpest
// row, and the one most likely to have been dropped as an oversight.
//
// A2: *"Spironolactone — anti-androgen: feminisation of a male fetus. **This clinic prescribes it
// for PCOS**, to exactly the women rule 14 exists for."*
//
// It is also the row the formulary cannot yet serve: no product is stocked, so migration 00072 had
// to add the molecule to the dictionary and the line is prescribed by label. Invariant 137 reports
// that the flag reaches no stocked product; this test proves the flag itself works.
func TestRule14AsksThePregnancyQuestionForAWomanOnSpironolactone(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "TERATOGEN_PREGNANCY")
	// Thirty-four: inside A2's 15-49 band, which is what makes this test about the flag.
	r.who.years = 34

	// Metformin first. The same woman, the same missing pregnancy status, and **nothing fires** —
	// because A2 deliberately does not flag metformin: it is used in pregnancy and is not
	// teratogenic, and flagging it would ask the question of nearly every woman in this clinic.
	metformin := r.submitted(t)
	review, err := r.service.Review(r.ctx(), r.facility, metformin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Findings) != 0 {
		t.Fatalf("metformin asked the pregnancy question, which A2 explicitly does not want: %+v",
			review.Findings)
	}

	// Spironolactone. Prescribed by label, because the pharmacy stocks none.
	spiro := r.submittedWith(t, uuid.Nil, "Spironolactone")
	review, err = r.service.Review(r.ctx(), r.facility, spiro.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Blocking()) != 1 || review.Blocking()[0].RuleCode != "TERATOGEN_PREGNANCY" {
		t.Fatalf("spironolactone for a woman of 34 with no pregnancy status produced %+v",
			review.Findings)
	}
	if !strings.Contains(review.Blocking()[0].SubjectEN, "Spironolactone") {
		t.Fatalf("the finding does not name the drug: %q", review.Blocking()[0].SubjectEN)
	}
	// It goes back to History, which is where the answer is recorded.
	if review.Blocking()[0].BounceStation != "STN_HISTORY" {
		t.Fatalf("the bounce goes to %q rather than to history",
			review.Blocking()[0].BounceStation)
	}

	// **The remediation, which did not exist before this checkpoint.** Recording a pregnancy
	// status at the history station satisfies the rule. Without somewhere for the answer to go,
	// rule 14 would have been a block whose only way out was the override.
	r.recordCoded(t, "PREGNANCY_STATUS", "not_pregnant")
	review, err = r.service.Review(r.ctx(), r.facility, spiro.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Findings) != 0 {
		t.Fatalf("a recorded pregnancy status did not satisfy rule 14: %+v", review.Findings)
	}

	// A man is not asked, and neither is a woman past the band.
	r.who.sex = "male"
	again := r.submittedWith(t, uuid.Nil, "Spironolactone")
	if review, err = r.service.Review(r.ctx(), r.facility, again.ID); err != nil {
		t.Fatal(err)
	} else if len(review.Findings) != 0 {
		t.Fatalf("a man was asked about pregnancy: %+v", review.Findings)
	}
}

// TestTheTeratogenicFlagReachesAFixedDoseCombination is the half of "ARBs (class)" that is easy to
// miss.
//
// A woman on telmisartan + amlodipine is on an ARB. Her prescription line resolves to the
// combination's own generic, whose class is `ARB_CCB_FDC` and not
// `ANGIOTENSIN_II_RECEPTOR_BLOCKER` — so a flag that reached only the single-molecule class would
// not ask her, and nothing would say so.
func TestTheTeratogenicFlagReachesAFixedDoseCombination(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "TERATOGEN_PREGNANCY")
	r.who.years = 34

	sheet := r.submittedWith(t, r.productNamed(t, "telmisartan + amlodipine"), "")
	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Blocking()) != 1 {
		t.Fatalf("a fixed-dose ARB combination did not reach the flag: %+v", review.Findings)
	}
}

// TestTheFlagDoesNotReachWhatA2DeliberatelyLeftOff, because a list like this grows by anxiety.
//
// Each of the four has its reason in A2 and each is repeated in migration 00072's commentary,
// because a later reader's instinct will be to add them. This is the test that notices if somebody
// does.
func TestTheFlagDoesNotReachWhatA2DeliberatelyLeftOff(t *testing.T) {
	r := newQARig(t)
	flagged, err := medsafety.NewStore(r.pool, formulary.NewStore(r.pool)).
		TeratogenicGenerics(context.Background(), r.facility, r.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(flagged) == 0 {
		t.Fatal("no molecule is flagged, so this test proves nothing — migration 00072 did not run")
	}
	for name, why := range map[string]string{
		"metformin hydrochloride": "used in pregnancy and not teratogenic; flagging it would ask the question of nearly every woman in this clinic",
		"levothyroxine sodium":    "essential, and the dose goes up in pregnancy — flagging it would teach the opposite of the right instinct",
		"cabergoline":             "stopped at conception in prolactinoma, but that is a management decision rather than a teratogenic one",
	} {
		if flagged[name] {
			t.Errorf("%s carries the teratogenic flag and A2 says it must not: %s", name, why)
		}
	}
	// Insulin, by class rather than by molecule name: there are seven of them.
	for name := range flagged {
		if strings.Contains(name, "insulin") {
			t.Errorf("%s carries the teratogenic flag. Insulin is the safest option in pregnancy "+
				"and the thing patients are switched *to*; a flag on it is a warning against the "+
				"answer", name)
		}
	}
}

// ---------------------------------------------------------------------------
// The screen is in English and Bangla, not in column names
// ---------------------------------------------------------------------------

// codeOnAScreen matches an internal handle: two or more upper-case runs joined by underscores,
// or a dotted fact reference. It is deliberately broad — it would match a drug label somebody
// wrote in shouty capitals — because the assertion below is "nothing that looks like a handle",
// and a rule whose product label genuinely looks like `CHOL_LDL` is a rule with a different
// problem.
var codeOnAScreen = regexp.MustCompile(`[A-Z][A-Z0-9]*_[A-Z0-9_]+`)

// A QA officer reads sentences. This is the regression test for the defect that made this
// package exist: the review screen was rendering `HBA1C`, `MONOFILAMENT_LEFT or
// MONOFILAMENT_RIGHT` and `CHOL_LDL or CHOL_TOTAL` to somebody standing in a clinic with a
// patient waiting.
//
// **Every rule in the seeded table is driven to fire at once**, rather than one rule per test,
// because the failure this guards against is a rule somebody adds later without a phrase — and a
// test that named the rules it checked would not cover it. The file below is a prescription with
// nothing done to it: no observations, no counselling, no allergy status, no diagnosis code.
func TestNoFindingShowsAnInternalCodeAsItsSentence(t *testing.T) {
	r := newQARig(t)
	sheet := r.submittedWith(t, r.productNamed(t, "carbimazole"), "")

	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Findings) == 0 {
		t.Fatal("an untouched file found nothing, so this test asserts nothing")
	}

	for _, f := range review.Findings {
		for label, text := range map[string]string{
			"title_en":   f.TitleEN,
			"title_bn":   f.TitleBN,
			"detail_en":  f.DetailEN,
			"detail_bn":  f.DetailBN,
			"subject_en": f.SubjectEN,
			"subject_bn": f.SubjectBN,
			"reason_en":  f.ReasonEN(),
			"reason_bn":  f.ReasonBN(),
		} {
			if hit := codeOnAScreen.FindString(text); hit != "" {
				t.Errorf("rule %s, %s: %q contains the internal code %q. "+
					"A QA officer reads this mid-clinic; set looks_for_en/looks_for_bn on the "+
					"rule, or add the observation code to core.observation_code",
					f.RuleCode, label, text, hit)
			}
		}
		// The code has not been thrown away, though — somebody debugging a rule at eight in the
		// evening still needs it, and the client renders it as a detail.
		if f.RuleCode == "DIABETES_LIPIDS" && len(f.SubjectCodes) == 0 {
			t.Error("the lipid finding dropped its codes entirely; " +
				"the sentence is for the officer and the codes are for whoever debugs the rule")
		}
	}
	if _, ok := review.Stations["STN_CONSULTATION"]; !ok {
		t.Fatal("the review carried no station catalogue, so this file never reached the engine")
	}
}

// The two rules the appendix named, asserted as one phrase each rather than as a list of parts.
//
// `CHOL_LDL or CHOL_TOTAL` is not two things an officer should reason about; it is a lipid
// profile, and which component the lab happened to report is not their problem. Same for the two
// monofilament feet.
func TestARuleThatNamesSeveralCodesSaysWhatTheyAre(t *testing.T) {
	r := newQARig(t)
	sheet := r.submitted(t)

	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]struct{ phrase, absent string }{
		"DIABETES_LIPIDS": {"lipid profile", "CHOL"},
		"DIABETES_FOOT":   {"foot sensation test", "MONOFILAMENT"},
	}
	seen := map[string]bool{}
	for _, f := range review.Findings {
		expect, ours := want[f.RuleCode]
		if !ours {
			continue
		}
		seen[f.RuleCode] = true
		if !strings.Contains(f.SubjectEN, expect.phrase) {
			t.Errorf("%s reads %q; it should say %q, because that is the one clinical thing it "+
				"is looking for", f.RuleCode, f.SubjectEN, expect.phrase)
		}
		if strings.Contains(f.SubjectEN, expect.absent) {
			t.Errorf("%s is still listing its parts: %q", f.RuleCode, f.SubjectEN)
		}
		if strings.TrimSpace(f.SubjectBN) == "" || f.SubjectBN == f.SubjectEN {
			t.Errorf("%s has no Bangla subject of its own: %q", f.RuleCode, f.SubjectBN)
		}
		// The parts are still there, as the detail.
		if len(f.SubjectCodes) < 2 {
			t.Errorf("%s summarised its codes away instead of carrying them: %v",
				f.RuleCode, f.SubjectCodes)
		}
	}
	for code := range want {
		if !seen[code] {
			t.Fatalf("%s did not fire on an untouched file, so nothing was asserted", code)
		}
	}
}

// "in the last 1 year" is a machine counting. Nobody says it, and it was on the screen.
func TestAWindowIsSaidTheWayAPersonSaysIt(t *testing.T) {
	r := newQARig(t)
	r.onlyRule(t, "DIABETES_FOOT")
	sheet := r.submitted(t)

	review, err := r.service.Review(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Findings) != 1 {
		t.Fatalf("expected the foot rule alone: %+v", review.Findings)
	}
	subject := review.Findings[0].SubjectEN
	if strings.Contains(subject, "1 year") || strings.Contains(subject, "365 days") {
		t.Fatalf("the window is being counted rather than said: %q", subject)
	}
	if !strings.Contains(subject, "the last year") {
		t.Fatalf("expected \"the last year\": %q", subject)
	}
	if bn := review.Findings[0].SubjectBN; strings.ContainsAny(bn, "0123456789") {
		t.Fatalf("Latin digits in a Bangla clinical sentence: %q", bn)
	}
}
