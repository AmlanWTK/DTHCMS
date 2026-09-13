package education_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/education"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The education station and the improvement score (CP88, CP92).
//
// # What each test here is a claim about, because they are not equally strong
//
//	reference data   TestTheChecklistIsChosenByTheDevicesOnTheSheet drives the real selection
//	                 query against the real formulary seed and the real rule table. It is a claim
//	                 about the *data*, and TestBreakingTheDeviceMappingBreaksTheSelection is the
//	                 check on that check: it removes the rules and asserts this test would fail,
//	                 so a mapping that had quietly stopped working could not show up as green.
//	enforced         TestNotApplicableIsNotMissing and TestTheDatabaseRefusesAFourthState make
//	                 their claims against the database, bypassing every line of Go here.
//	behavioural      the flag, the refusals, the preamble. Ordinary confidence about what the
//	                 code does today.
//
// The claim this package cannot make is the one that matters most — that a physician cannot
// record an improvement score. That needs the real router, the real middleware and the real RBAC
// engine, and it lives in cmd/api/education_route_db_test.go.

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

type rig struct {
	*testsupport.DB
	pool     *pgxpool.Pool
	clock    *clock.Fixed
	store    *education.Store
	values   *clinical.Service
	service  *education.Service
	server   *httptest.Server
	sheet    *stubSheets
	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	visit    uuid.UUID
	// firstVisit is a second patient with no earlier visit, for §2's not-applicable rule.
	firstPatient uuid.UUID
	firstVisit   uuid.UUID

	role string
	held []string
}

// stubSheets stands in for the composition root's prescription bridge.
//
// A stub and not the real one, deliberately. What is under test is the *rule that maps a
// prescribed product to a checklist*, and driving it through a real prescription would mean a
// draft, a safety check, a QA submission and a signature before the question under test could
// be asked — four other checkpoints' failures arriving as failures of this one. The bridge
// itself is exercised where it lives, in cmd/api.
type stubSheets struct{ lines []education.PrescribedLine }

func (s *stubSheets) PrescribedDevices(context.Context,
	uuid.UUID, uuid.UUID, uuid.UUID) ([]education.PrescribedLine, error) {

	return s.lines, nil
}

func (s *stubSheets) prescribe(products ...uuid.UUID) {
	s.lines = nil
	for i := range products {
		product := products[i]
		s.lines = append(s.lines, education.PrescribedLine{
			ProductID: &product, Label: "line " + product.String()[:8],
		})
	}
}

type staff struct {
	facility, device uuid.UUID
	user             *uuid.UUID
	permissions      *[]string
	role             *string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(*s.user, []byte("session")).String(),
		Code:      "EDU01", Permissions: *s.permissions, Roles: []string{*s.role},
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller,
	anyOf []string) (context.Context, httpx.AuthzDecision) {

	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want != held {
				continue
			}
			granted := httpx.WithPrincipal(ctx, httpx.Principal{
				UserID: caller.UserID, FacilityID: caller.FacilityID,
				SessionID: caller.SessionID, Code: caller.Code,
				DeviceID: s.device.String(), Role: *s.role,
				Station:         "STN_RX_EDUCATION",
				DeviceAssurance: httpx.AssuranceProven,
			})
			granted = rbac.GrantedForTest(granted, caller, *s.role, "STN_RX_EDUCATION")
			return granted, httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	r := &rig{DB: base, pool: pool, user: uuid.New(), device: uuid.New(),
		role: "RX_EDUCATOR", sheet: &stubSheets{}}
	r.held = []string{
		education.PermReference, education.PermRead, education.PermRecord,
		education.PermWritePRO,
	}
	r.clock = clock.NewFixed(time.Date(2026, 9, 14, 11, 20, 0, 0, time.UTC))
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&r.facility); err != nil {
		t.Fatal(err)
	}

	events := eventstore.New(eventstore.Config{
		Pool: pool, Clock: r.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	if err := projection.NewEngineWithEvents(pool, projection.Default, events).Register(ctx); err != nil {
		t.Fatal(err)
	}

	r.store = education.NewStore(pool)
	r.values = clinical.NewService(clinical.NewStore(pool), events, r.clock)
	r.service = education.NewService(r.store, r.values, r.sheet, r.clock)
	r.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := education.NewHandlers(education.HandlersConfig{
		Service: r.service, Store: r.store, Clock: r.clock, Logger: logger,
	})
	who := staff{facility: r.facility, device: r.device,
		user: &r.user, permissions: &r.held, role: &r.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 18, RequestTimeout: 30 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(rt chi.Router) {
			handlers.Mount(rt)
			rt.Route("/patients", func(p chi.Router) { handlers.MountPatient(p) })
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.server = httptest.NewServer(router)
	t.Cleanup(r.server.Close)
	return r
}

// seed puts two real-looking Faridpur patients in the building: one on a second visit, one on
// their first.
func (r *rig) seed(t *testing.T) {
	t.Helper()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'EDU01', 'Shirin Akter', 'শিরীন আক্তার', 'active')`,
		r.user, r.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Education 1', 'tablet', 'active', now())`,
		r.device, r.facility); err != nil {
		t.Fatal(err)
	}

	r.patient, r.visit = r.patientWithVisits(t, "+8801711119011", "DTHC-FRD-2026-000901",
		"Mosammat Rahima Begum", "মোসাম্মৎ রহিমা বেগম", "1965-04-17", 2)
	r.firstPatient, r.firstVisit = r.patientWithVisits(t, "+8801711119022", "DTHC-FRD-2026-000902",
		"Abdul Karim Mridha", "আব্দুল করিম মৃধা", "1972-11-03", 1)
}

// patientWithVisits registers one patient and opens `visits` visits, returning the newest.
//
// More than one visit matters: §2's question is anchored to "your last visit", and a patient
// with exactly one has no comparison point. The two fixtures here are the two sides of that
// rule and every test about it uses one or the other rather than inventing a third.
func (r *rig) patientWithVisits(t *testing.T, phone string,
	clinicalID, nameEN, nameBN, born string, visits int) (uuid.UUID, uuid.UUID) {

	t.Helper()
	patient := uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, name_bn, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, $3, $4, $5, 'female', $6::date, 'day', 'national_id', $7, 'active', $8, now())`,
		patient, r.facility, clinicalID, nameEN, nameBN, born, phone, r.user); err != nil {
		t.Fatalf("registering %s: %v", nameEN, err)
	}
	var newest uuid.UUID
	for i := 0; i < visits; i++ {
		id := uuid.New()
		kind := "follow_up"
		if i == 0 {
			kind = "new"
		}
		if _, err := r.SQL.Exec(`
			INSERT INTO core.visit (id, facility_id, patient_id, visit_code, visit_type, status,
			                        clinic_day, opened_at, opened_by)
			VALUES ($1, $2, $3, $4, $5, 'open', current_date - ($6::int),
			        now() - make_interval(days => $6::int), $7)`,
			id, r.facility, patient, "V-EDU-"+id.String()[:8], kind, (visits-1-i)*90,
			r.user); err != nil {
			t.Fatalf("opening visit %d for %s: %v", i, nameEN, err)
		}
		// Everything but the newest is closed, in a second statement rather than as a
		// conditional value in the first.
		//
		// A patient may have exactly one open visit (`visit_one_open_per_patient`), which is the
		// clinic's own rule and not a fixture detail: "your last visit" means the one before this
		// one, and a patient with two open visits would make that phrase ambiguous for the officer
		// as well as for the unique index.
		if i < visits-1 {
			if _, err := r.SQL.Exec(`
				UPDATE core.visit
				   SET status = 'closed', closed_at = opened_at + interval '2 hours', closed_by = $2
				 WHERE id = $1`, id, r.user); err != nil {
				t.Fatalf("closing visit %d for %s: %v", i, nameEN, err)
			}
		}
		newest = id
	}
	return patient, newest
}

// product finds one seeded formulary product by generic name and dispensing unit.
//
// Real seeded products, so the selection runs against CP75's own data rather than against a
// fixture invented here — which is the whole point: the rule is supposed to work on the
// formulary this clinic actually has.
func (r *rig) product(t *testing.T, generic, unit string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := r.SQL.QueryRow(`
		SELECT p.id FROM core.medication_product p
		  JOIN core.generic g ON g.id = p.generic_id
		 WHERE p.facility_id = $1 AND lower(g.name) LIKE lower($2) AND p.dispense_unit = $3
		 ORDER BY p.trade_name LIMIT 1`, r.facility, generic, unit).Scan(&id); err != nil {
		t.Fatalf("the formulary has no %q dispensed as %q: %v", generic, unit, err)
	}
	return id
}

// itemText is one checklist item's English wording, from the reference data.
func (r *rig) itemText(t *testing.T, code string) string {
	t.Helper()
	items, _, err := r.store.ItemIndex(r.ctx())
	if err != nil {
		t.Fatal(err)
	}
	item, ok := items[code]
	if !ok {
		t.Fatalf("%s is not on any live checklist", code)
	}
	return item.TextEN
}

// allItemText is every word a session would put in front of the officer.
func allItemText(session education.Session) string {
	var out strings.Builder
	for _, checklist := range session.Checklists {
		for _, item := range checklist.Items {
			out.WriteString(item.TextEN)
			out.WriteString("\n")
		}
	}
	return out.String()
}

// anyProduct finds one seeded product of a generic, whatever it is dispensed in.
func (r *rig) anyProduct(t *testing.T, generic string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := r.SQL.QueryRow(`
		SELECT p.id FROM core.medication_product p
		  JOIN core.generic g ON g.id = p.generic_id
		 WHERE p.facility_id = $1 AND lower(g.name) = lower($2)
		 ORDER BY p.trade_name LIMIT 1`, r.facility, generic).Scan(&id); err != nil {
		t.Fatalf("the formulary has no %q: %v", generic, err)
	}
	return id
}

// newGLP1 adds a GLP-1 receptor agonist the station has never heard of, as the pharmacist would
// when a new agent reaches Bangladesh.
func (r *rig) newGLP1(t *testing.T, molecule string) uuid.UUID {
	t.Helper()
	var generic uuid.UUID
	if err := r.SQL.QueryRow(`
		INSERT INTO core.generic (name, class_code) VALUES ($1, 'GLP_1_RECEPTOR_AGONIST')
		RETURNING id`, molecule).Scan(&generic); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := r.SQL.QueryRow(`
		INSERT INTO core.medication_product
		  (facility_id, generic_id, trade_name, strength, form_code, manufacturer, dispense_unit)
		VALUES ($1, $2, $3, '5 mg/0.5 mL', 'SC_INJECTION', 'Incepta Pharmaceuticals Ltd.', 'pen')
		RETURNING id`, r.facility, generic, molecule+" pen").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// meter adds a blood glucose meter to this facility's formulary.
//
// It has to be added because **the seeded formulary has none** — migration 00056 seeds medicines
// and this clinic prescribes strips and meters on paper. Migration 00070 adds the
// classification (a class, a form, a dispensing unit) because that is reference data and the
// same everywhere; it deliberately adds no product and no price, because which brand a clinic
// stocks and what it costs is the pharmacist's. So this is what the pharmacist will do, done
// here so the meter checklist can be tested at all.
func (r *rig) meter(t *testing.T) uuid.UUID {
	t.Helper()
	var generic uuid.UUID
	if err := r.SQL.QueryRow(`
		INSERT INTO core.generic (name, class_code)
		VALUES ('Blood glucose test strips', 'SELF_MONITORING_GLUCOSE')
		ON CONFLICT (lower(name)) DO UPDATE SET class_code = EXCLUDED.class_code
		RETURNING id`).Scan(&generic); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := r.SQL.QueryRow(`
		INSERT INTO core.medication_product
		  (facility_id, generic_id, trade_name, strength, form_code, manufacturer, dispense_unit)
		VALUES ($1, $2, 'Accu-Chek Active', '50 strips', 'TEST_STRIP', 'Roche Diagnostics', 'strip')
		RETURNING id`, r.facility, generic).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *rig) ctx() context.Context {
	caller := httpx.Caller{UserID: r.user.String(), FacilityID: r.facility.String(),
		SessionID: uuid.NewSHA1(r.user, []byte("session")).String(), Code: "EDU01",
		Permissions: r.held, Roles: []string{r.role}}
	ctx := httpx.WithPrincipal(context.Background(), httpx.Principal{
		UserID: caller.UserID, FacilityID: caller.FacilityID, SessionID: caller.SessionID,
		Code: caller.Code, DeviceID: r.device.String(), Role: r.role,
		Station: "STN_RX_EDUCATION", DeviceAssurance: httpx.AssuranceProven,
	})
	return rbac.GrantedForTest(ctx, caller, r.role, "STN_RX_EDUCATION")
}

func (r *rig) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, r.server.URL+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set(httpx.ActiveRoleHeader, r.role)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	resp, err := r.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

// checklistCodes is the codes a session offers, sorted so an assertion reads as a set.
func checklistCodes(session education.Session) []string {
	out := make([]string, 0, len(session.Checklists))
	for _, checklist := range session.Checklists {
		out = append(out, checklist.Code)
	}
	sort.Strings(out)
	return out
}

// answersFor scores every item of the named checklists the same way, for the tests that care
// about how many rather than which.
func (r *rig) answersFor(t *testing.T, session education.Session,
	states ...education.State) []education.ItemResult {

	t.Helper()
	var out []education.ItemResult
	for _, checklist := range session.Checklists {
		for i, item := range checklist.Items {
			state := education.Demonstrated
			if i < len(states) {
				state = states[i]
			}
			out = append(out, education.ItemResult{Code: item.Code, State: state})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. CP92 criterion 1: the checklist is chosen by the devices on the sheet
// ---------------------------------------------------------------------------

// TestTheChecklistIsChosenByTheDevicesOnTheSheet is the acceptance criterion, in its three
// shapes.
//
// *"An insulin pen on the sheet brings up the pen checklist with no manual selection. A patient
// on two devices gets both."* And the third shape, which is the one that makes the other two
// mean something: a patient on tablets alone gets none, so the test cannot be passed by a rule
// that returns every checklist.
func TestTheChecklistIsChosenByTheDevicesOnTheSheet(t *testing.T) {
	r := newRig(t)

	pen := r.product(t, "%isophane insulin human%", "pen")
	meter := r.meter(t)
	tablet := r.product(t, "metformin hydrochloride", "tablet")

	for _, scenario := range []struct {
		name     string
		products []uuid.UUID
		want     []string
	}{
		{"an insulin pen brings up the pen checklist", []uuid.UUID{pen},
			[]string{"PEN_TECHNIQUE"}},
		{"a pen and a meter bring up both", []uuid.UUID{pen, meter},
			[]string{"METER_TECHNIQUE", "PEN_TECHNIQUE"}},
		{"tablets alone bring up neither", []uuid.UUID{tablet}, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r.sheet.prescribe(scenario.products...)
			session, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
			if err != nil {
				t.Fatal(err)
			}
			got := checklistCodes(session)
			if strings.Join(got, ",") != strings.Join(scenario.want, ",") {
				t.Fatalf("the sheet brought up %v, want %v.\n"+
					"CP92 criterion 1: the checklist matches the prescribed device automatically. "+
					"A checklist that fails to appear looks, from the officer's screen, exactly "+
					"like a patient who is not on a device.", got, scenario.want)
			}
		})
	}
}

// TestAVialAndAPenAreDifferentChecklists is the case the mapping is most likely to get wrong.
//
// Insulin in a vial and insulin in a pen are the *same medication class*. A rule keyed on the
// class alone would put one checklist in front of both patients and would pass every other test
// in this file — which is why the rule table has a dispensing-unit column and why this test
// exists separately from the one above.
func TestAVialAndAPenAreDifferentChecklists(t *testing.T) {
	r := newRig(t)
	pen := r.product(t, "%isophane insulin human%", "pen")
	vial := r.product(t, "%isophane insulin human%", "vial")

	r.sheet.prescribe(pen)
	penSession, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	r.sheet.prescribe(vial)
	vialSession, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if got := checklistCodes(penSession); strings.Join(got, ",") != "PEN_TECHNIQUE" {
		t.Errorf("the pen brought up %v", got)
	}
	if got := checklistCodes(vialSession); strings.Join(got, ",") != "VIAL_TECHNIQUE" {
		t.Errorf("the vial brought up %v — the same insulin, and a different device. If this "+
			"says PEN_TECHNIQUE the rule is keyed on the class alone.", got)
	}
}

// ---------------------------------------------------------------------------
// 1a. Spec §6.4: the timing item follows the molecule, not the class
// ---------------------------------------------------------------------------

// TestADailyGLP1IsNotTaughtTheWeeklyRhythm is the correction to the spec's first draft, asserted
// from both sides.
//
// Semaglutide and dulaglutide are weekly; liraglutide is once daily; all three are one class. A
// rule keyed on the class would hand a Victoza patient a checklist asking them to name the day of
// the week they inject — and §6.4 is explicit that this is not a cosmetic mismatch: the commonest
// dosing error in this class is a patient moving between a daily and a weekly agent and carrying
// the old rhythm across. A checklist that asks the wrong timing question does not merely fail to
// teach, it teaches the wrong rhythm, in the officer's voice, with the patient's own pen in hand.
//
// So the assertion is about the *item*, not about the checklist code. Checking that liraglutide
// selects `GLP1_DAILY_TECHNIQUE` would pass for a daily checklist that had been filled with the
// weekly wording; what must be true is that the day-of-the-week question never reaches a daily
// patient and the time-of-day question never reaches a weekly one.
func TestADailyGLP1IsNotTaughtTheWeeklyRhythm(t *testing.T) {
	r := newRig(t)

	// The wording of the two timing items, read from the reference data rather than retyped, so
	// that an edit to the checklist cannot make this test assert against a sentence the clinic
	// has stopped using.
	weeklyTiming := r.itemText(t, "EDU_GLP_03")
	dailyTiming := r.itemText(t, "EDU_GLPD_03")
	if !strings.Contains(weeklyTiming, "day of the week") {
		t.Fatalf("the weekly timing item no longer asks for a day of the week: %q", weeklyTiming)
	}
	if !strings.Contains(dailyTiming, "time of day") {
		t.Fatalf("the daily timing item no longer asks for a time of day: %q", dailyTiming)
	}

	for _, scenario := range []struct {
		name      string
		generic   string
		wants     string
		forbids   string
		checklist string
	}{
		{
			name:    "liraglutide is daily and is never asked which day of the week",
			generic: "liraglutide", wants: dailyTiming, forbids: weeklyTiming,
			checklist: "GLP1_DAILY_TECHNIQUE",
		},
		{
			name:    "semaglutide is weekly and is never asked what time of day",
			generic: "semaglutide", wants: weeklyTiming, forbids: dailyTiming,
			checklist: "GLP1_TECHNIQUE",
		},
		{
			name:    "dulaglutide is weekly too",
			generic: "dulaglutide", wants: weeklyTiming, forbids: dailyTiming,
			checklist: "GLP1_TECHNIQUE",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r.sheet.prescribe(r.anyProduct(t, scenario.generic))
			session, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
			if err != nil {
				t.Fatal(err)
			}
			// Reported and not fatal, so that the wording assertions below still run. Which
			// checklist code arrived is the mechanism; which question reaches the patient is
			// the claim, and a failure that stopped before saying so would send the next
			// reader to the rule table instead of to the item.
			if got := strings.Join(checklistCodes(session), ","); got != scenario.checklist {
				t.Errorf("%s brought up %s, want %s", scenario.generic, got, scenario.checklist)
			}
			wording := allItemText(session)
			if !strings.Contains(wording, scenario.wants) {
				t.Errorf("%s was not asked %q", scenario.generic, scenario.wants)
			}
			if strings.Contains(wording, scenario.forbids) {
				t.Fatalf("%s was asked %q.\n"+
					"Spec §6.4: a checklist teaches the dosing rhythm, and this one teaches the "+
					"wrong one. The commonest error in this class is a patient carrying the "+
					"rhythm across from a previous agent, and this is the station doing it for "+
					"them.", scenario.generic, scenario.forbids)
			}
		})
	}
}

// TestAnUnknownGLP1InheritsNothingAndIsNamed is the fallback decision, asserted.
//
// §6.4 gives the GLP-1 rules no class-level rule behind them, so a molecule added to the
// formulary next year matches nothing. Two things have to be true about that, and only the first
// is obvious: it must not inherit a sibling's checklist — a daily patient told to inject on
// Fridays is worse than a patient told nothing — and it must not be *silent*, because on the
// officer's screen "no checklist" is also what a patient on tablets alone looks like.
func TestAnUnknownGLP1InheritsNothingAndIsNamed(t *testing.T) {
	r := newRig(t)
	newAgent := r.newGLP1(t, "Tirzepatide")

	r.sheet.prescribe(newAgent)
	session, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Checklists) != 0 {
		t.Fatalf("an unrecognised GLP-1 inherited %v.\n"+
			"A checklist is an instruction and inheriting one asserts a dosing rhythm nobody "+
			"confirmed.", checklistCodes(session))
	}
	if len(session.Unclassified) != 1 {
		t.Fatalf("%d unclassified devices, want 1. Without this the officer cannot tell an "+
			"unrecognised pen from a patient on tablets alone.", len(session.Unclassified))
	}
	if session.Unclassified[0].GenericName != "Tirzepatide" {
		t.Errorf("the unclassified line names %q", session.Unclassified[0].GenericName)
	}
	if session.Unclassified[0].ClassNameEN == "" || session.Unclassified[0].ClassNameBN == "" {
		t.Error("the unclassified line does not name its class in both languages")
	}

	// And a patient on tablets alone is still silent, which is what makes the warning mean
	// something. Metformin's class carries no device rule at all.
	r.sheet.prescribe(r.product(t, "metformin hydrochloride", "tablet"))
	plain, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Unclassified) != 0 {
		t.Errorf("a tablet was reported as an unrecognised device: %v", plain.Unclassified)
	}
}

// TestBreakingTheDeviceMappingBreaksTheSelection is the check on the check.
//
// The selection test above passes if the rules are right. It would *also* pass if something
// else in the system happened to return the pen checklist for every patient — and it would go
// on passing if the rules were silently emptied, provided the emptying also emptied the
// expectation. Invariant 131's note is the standing lesson here: a seed that matched nothing
// looked exactly like a seed that worked.
//
// So this removes the pen rule and asserts the selection *stops working*. A green run of this
// test means the assertion above is load-bearing; a failure here means the pen checklist is
// arriving from somewhere other than the rule table, and the acceptance criterion is being met
// by accident.
func TestBreakingTheDeviceMappingBreaksTheSelection(t *testing.T) {
	r := newRig(t)
	pen := r.product(t, "%isophane insulin human%", "pen")
	r.sheet.prescribe(pen)

	before, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(checklistCodes(before), ",") != "PEN_TECHNIQUE" {
		t.Fatalf("the fixture does not select the pen checklist to begin with: %v",
			checklistCodes(before))
	}

	// The mutation: retire every rule that leads to the pen checklist. Nothing else changes —
	// the checklist is still live, its items are still there, the product is still on the sheet.
	if _, err := r.SQL.Exec(`
		UPDATE core.education_device_rule SET retired_at = now()
		 WHERE checklist_code = 'PEN_TECHNIQUE'`); err != nil {
		t.Fatal(err)
	}

	after, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Checklists) != 0 {
		t.Fatalf("with the pen rules removed the station still offers %v.\n"+
			"That means the checklist is not coming from core.education_device_rule, and "+
			"TestTheChecklistIsChosenByTheDevicesOnTheSheet is passing for a reason other than "+
			"the one it claims.", checklistCodes(after))
	}
	if len(after.Devices) != 0 {
		t.Errorf("the session still names %d selected device(s) with no rule to select them",
			len(after.Devices))
	}
}

// ---------------------------------------------------------------------------
// 2. CP92 §5: the re-education flag
// ---------------------------------------------------------------------------

// TestTheReeducationFlagFiresOnAnUnableAndOnThreeCorrectedToday walks the threshold from both
// sides.
//
// The negative case is the one that carries it. A flag that fired on two "corrected today"
// answers would fire for most patients on a first pen, and a flag that fires for everybody is a
// flag the physician learns to scroll past — which is the same outcome as not having one.
func TestTheReeducationFlagFiresOnAnUnableAndOnThreeCorrectedToday(t *testing.T) {
	r := newRig(t)
	pen := r.product(t, "%isophane insulin human%", "pen")
	r.sheet.prescribe(pen)

	for _, scenario := range []struct {
		name   string
		states []education.State
		want   bool
	}{
		{"one unable raises it", []education.State{education.Unable}, true},
		{"two corrected today do not", []education.State{
			education.CorrectedToday, education.CorrectedToday}, false},
		{"three corrected today do", []education.State{
			education.CorrectedToday, education.CorrectedToday, education.CorrectedToday}, true},
		{"a clean demonstration does not", nil, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			session, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
			if err != nil {
				t.Fatal(err)
			}
			result, err := r.service.Record(r.ctx(), education.Assessment{
				EventID: uuid.New(), PatientID: r.patient, VisitID: r.visit,
				Items:        r.answersFor(t, session, scenario.states...),
				LedgerSource: eventstore.SourceWeb,
			})
			if err != nil {
				t.Fatalf("recording: %v", err)
			}
			if result.Reeducation != scenario.want {
				t.Fatalf("the flag read %v with %d unable and %d corrected today, want %v.\n"+
					"Spec §5: any Unable, or three or more Corrected today. The threshold is "+
					"core.education_reeducation_policy and not a constant.",
					result.Reeducation, result.Unable, result.CorrectedToday, scenario.want)
			}
			// And it is on the record, not only in the reply. A flag the officer saw and the
			// physician never did would be the worst of both.
			var stored bool
			if err := r.SQL.QueryRow(`
				SELECT value_bool FROM read.observation
				 WHERE patient_id = $1 AND code = 'EDU_REEDUCATION_FLAG' AND status = 'ACTIVE'
				 ORDER BY global_seq DESC LIMIT 1`, r.patient).Scan(&stored); err != nil {
				t.Fatalf("reading the stored flag: %v", err)
			}
			if stored != scenario.want {
				t.Errorf("the reply said %v and the record says %v", result.Reeducation, stored)
			}
		})
	}
}

// TestTheThresholdIsDataAndNotAConstant changes the threshold and expects the answer to change.
//
// §5: *"That threshold is a judgement and is a configurable number, not a constant."* A
// threshold that was configurable in principle and hard-coded in fact would pass every test
// above, because every test above uses the seeded value of three.
func TestTheThresholdIsDataAndNotAConstant(t *testing.T) {
	r := newRig(t)
	pen := r.product(t, "%isophane insulin human%", "pen")
	r.sheet.prescribe(pen)

	if _, err := r.SQL.Exec(`
		UPDATE core.education_reeducation_policy
		   SET corrected_today_threshold = 2 WHERE code = 'DEFAULT'`); err != nil {
		t.Fatal(err)
	}
	session, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.service.Record(r.ctx(), education.Assessment{
		EventID: uuid.New(), PatientID: r.patient, VisitID: r.visit,
		Items:        r.answersFor(t, session, education.CorrectedToday, education.CorrectedToday),
		LedgerSource: eventstore.SourceWeb,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reeducation {
		t.Fatal("with the threshold lowered to two, two corrections did not raise the flag. " +
			"The number is being read from somewhere other than " +
			"core.education_reeducation_policy.")
	}
}

// ---------------------------------------------------------------------------
// 3. CP88 §2: not applicable is not missing
// ---------------------------------------------------------------------------

// TestNotApplicableIsNotMissing asserts the distinction directly, in the three places it could
// be lost: the API shape, the database, and what the station's own screen reads back.
//
// This is CP82's rule about unactioned suggestions, applied to a number. A first-visit patient
// who was never asked and a first-visit patient the officer deliberately marked as
// not-applicable are different facts, and an analysis that merged them would count first visits
// as non-responders — or, worse, a design with a magic value would let them be *averaged*.
func TestNotApplicableIsNotMissing(t *testing.T) {
	r := newRig(t)

	// 1. Nobody has asked. The zero Answer, and it says so.
	unasked, err := r.store.ImprovementFor(r.ctx(), r.firstPatient, r.facility, r.firstVisit)
	if err != nil {
		t.Fatal(err)
	}
	if unasked.Answered() {
		t.Fatal("a visit nobody has asked about reports an answer")
	}
	if _, ok := unasked.NotApplicable(); ok {
		t.Fatal("an unasked visit reports a not-applicable reason. That is the confusion this " +
			"whole design exists to prevent: nobody got to it, and somebody deciding the " +
			"question does not apply, are different facts.")
	}

	// 2. The officer records that it did not apply.
	if _, err := r.service.Record(r.ctx(), education.Assessment{
		EventID: uuid.New(), PatientID: r.firstPatient, VisitID: r.firstVisit,
		Improvement:  education.NotAsked("first_visit"),
		LedgerSource: eventstore.SourceWeb,
	}); err != nil {
		t.Fatalf("recording not-applicable: %v", err)
	}

	answered, err := r.store.ImprovementFor(r.ctx(), r.firstPatient, r.facility, r.firstVisit)
	if err != nil {
		t.Fatal(err)
	}
	if !answered.Answered() {
		t.Fatal("a recorded not-applicable reads as unanswered")
	}
	if _, ok := answered.Score(); ok {
		t.Fatal("a not-applicable answer carries a score")
	}
	reason, ok := answered.NotApplicable()
	if !ok || reason != "first_visit" {
		t.Fatalf("the reason reads %q, %v", reason, ok)
	}

	// 3. In the database: two codes, and the not-applicable row carries no number at all — so
	// no query can average it into the scores by accident.
	var scores, markers int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FILTER (WHERE code = 'IMPROVEMENT_SCORE'),
		       count(*) FILTER (WHERE code = 'IMPROVEMENT_SCORE_NA')
		  FROM read.observation WHERE patient_id = $1 AND status = 'ACTIVE'`,
		r.firstPatient).Scan(&scores, &markers); err != nil {
		t.Fatal(err)
	}
	if scores != 0 || markers != 1 {
		t.Fatalf("%d score rows and %d not-applicable rows, want 0 and 1", scores, markers)
	}
	var numeric *float64
	if err := r.SQL.QueryRow(`
		SELECT value_num FROM read.observation
		 WHERE patient_id = $1 AND code = 'IMPROVEMENT_SCORE_NA'`, r.firstPatient).Scan(&numeric); err != nil {
		t.Fatal(err)
	}
	if numeric != nil {
		t.Fatalf("the not-applicable row carries the number %v. A number here is a number "+
			"AVG() will one day include.", *numeric)
	}

	// 4. On the wire: the two shapes have no key in common, and the unanswered one is null.
	resp, body := r.do(t, "GET",
		"/v1/patients/"+r.firstPatient.String()+"/education?visit_id="+r.firstVisit.String(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET education: %d %v", resp.StatusCode, body)
	}
	improvement, present := body["improvement"].(map[string]any)
	if !present {
		t.Fatalf("the session's improvement is %#v", body["improvement"])
	}
	if _, hasScore := improvement["score"]; hasScore {
		t.Error("a not-applicable answer serialises a score key, so a client testing for one " +
			"cannot tell the two apart")
	}
	if improvement["not_applicable_reason"] != "first_visit" {
		t.Errorf("the wire reason is %v", improvement["not_applicable_reason"])
	}

	// And the unasked visit serialises null rather than an object with empty fields.
	resp, body = r.do(t, "GET",
		"/v1/patients/"+r.patient.String()+"/education?visit_id="+r.visit.String(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET education: %d", resp.StatusCode)
	}
	if body["improvement"] != nil {
		t.Errorf("an unasked visit serialises %#v rather than null", body["improvement"])
	}
}

// TestAnAnswerCannotBeBothAScoreAndNotApplicable is the shape rule, at the boundary where a
// client's mistake arrives.
func TestAnAnswerCannotBeBothAScoreAndNotApplicable(t *testing.T) {
	var answer education.Answer
	err := json.Unmarshal([]byte(`{"score": 7, "not_applicable_reason": "first_visit"}`), &answer)
	if err == nil {
		t.Fatal("an answer carrying both was accepted. There is no fact it could describe: " +
			"either the patient said seven or the question was not asked.")
	}
}

// TestAScoreIsRefusedOnAFirstVisit is §2's other half.
//
// *"For a first visit there is no last visit, so the score is not asked... A first-visit score
// would be a number answering a different question."* Refused rather than stored, because the
// damage is downstream and silent: it would be averaged with the rest by every analysis that
// ever touches the column.
func TestAScoreIsRefusedOnAFirstVisit(t *testing.T) {
	r := newRig(t)
	_, err := r.service.Record(r.ctx(), education.Assessment{
		EventID: uuid.New(), PatientID: r.firstPatient, VisitID: r.firstVisit,
		Improvement:  education.Scored(9),
		LedgerSource: eventstore.SourceWeb,
	})
	if err == nil {
		t.Fatal("a score was recorded against a first visit")
	}
	if !strings.Contains(err.Error(), "no last visit") {
		t.Errorf("the refusal reads %q, which does not tell the officer what to do instead", err)
	}

	// And the same patient's second visit accepts one, so the refusal is about the first visit
	// and not about the patient.
	if _, err := r.SQL.Exec(`
		UPDATE core.visit SET status = 'closed', closed_at = now(), closed_by = $2
		 WHERE id = $1`, r.firstVisit, r.user); err != nil {
		t.Fatal(err)
	}
	second := uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.visit (id, facility_id, patient_id, visit_code, visit_type, status,
		                        clinic_day, opened_at, opened_by)
		VALUES ($1, $2, $3, 'V-EDU-SECOND', 'follow_up', 'open', current_date, now(), $4)`,
		second, r.facility, r.firstPatient, r.user); err != nil {
		t.Fatal(err)
	}
	if _, err := r.service.Record(r.ctx(), education.Assessment{
		EventID: uuid.New(), PatientID: r.firstPatient, VisitID: second,
		Improvement:  education.Scored(7),
		LedgerSource: eventstore.SourceWeb,
	}); err != nil {
		t.Fatalf("a score on the second visit was refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 4. The database's own refusals
// ---------------------------------------------------------------------------

// TestTheDatabaseRefusesAFourthState writes directly as the projector — the role that actually
// holds the grant — bypassing every line of Go in this repository.
//
// §5 names three states. A fourth is not a new option, it is a value every report and every
// research query would have to be taught about, and the teaching would happen after the rows
// existed.
func TestTheDatabaseRefusesAFourthState(t *testing.T) {
	r := newRig(t)
	_, err := r.SQL.Exec(`
		INSERT INTO read.observation
		  (id, facility_id, patient_id, code, category, value_type, value_code,
		   effective_at, recorded_at, source, recorded_by, recorded_role, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'EDU_PEN_04', 'EXAM', 'coded', 'nearly',
		        now(), now(), 'STATION', $3, 'RX_EDUCATOR', gen_random_uuid(),
		        (SELECT coalesce(max(global_seq), 0) + 1 FROM read.observation))`,
		r.facility, r.patient, r.user)
	if err == nil {
		t.Fatal("the database accepted a fourth state for a checklist item. The vocabulary is " +
			"core.observation_answer and migration 00033's trigger is what holds it.")
	}
}

// TestTheDatabaseRefusesAPatientReportedValueFromTheWrongRole is CP88 §1 as a property of the
// data rather than of whichever code path happened to run.
//
// Written as the projector, which is how a row would arrive during a projection rebuild — the
// case where every Go-level check in this system is bypassed by construction.
func TestTheDatabaseRefusesAPatientReportedValueFromTheWrongRole(t *testing.T) {
	r := newRig(t)
	_, err := r.SQL.Exec(`
		INSERT INTO read.observation
		  (id, facility_id, patient_id, code, category, value_type, value_num, unit,
		   entered_num, entered_unit,
		   effective_at, recorded_at, source, recorded_by, recorded_role, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 'IMPROVEMENT_SCORE', 'PRO', 'numeric', 9, '1',
		        9, '1', now(), now(), 'PATIENT', $3, 'PHYSICIAN', gen_random_uuid(),
		        (SELECT coalesce(max(global_seq), 0) + 1 FROM read.observation))`,
		r.facility, r.patient, r.user)
	if err == nil {
		t.Fatal("the database stored an improvement score recorded by PHYSICIAN.\n" +
			"CP88 §1 is that the score is asked by somebody with no stake in the answer. If a " +
			"row can say otherwise, the column is a mixture of two different measurements and " +
			"nothing downstream can tell which is which.")
	}
	if !strings.Contains(err.Error(), "observation.write.pro") {
		t.Errorf("the refusal reads %q and does not name the permission that is missing", err)
	}
}

// ---------------------------------------------------------------------------
// 5. The wording that does the work
// ---------------------------------------------------------------------------

// TestTheComplianceQuestionKeepsItsPreamble guards one sentence.
//
// §7: *"The preamble is not politeness. It tells the patient that missing doses is normal and
// expected, which is what makes the true number sayable."* It is the half a UI tidy-up removes
// first, because it looks like filler — and without it the question has one socially acceptable
// answer and the number that comes back is decoration.
func TestTheComplianceQuestionKeepsItsPreamble(t *testing.T) {
	if !strings.HasPrefix(education.ComplianceQuestionEN, "Most people miss a dose sometimes.") {
		t.Errorf("the English question has lost its preamble: %q", education.ComplianceQuestionEN)
	}
	if !strings.HasPrefix(education.ComplianceQuestionBN, "প্রায় সবারই") {
		t.Errorf("the Bangla question has lost its preamble: %q", education.ComplianceQuestionBN)
	}
	for _, question := range []string{education.ComplianceQuestionEN, education.ComplianceQuestionBN} {
		if !strings.Contains(question, "?") && !strings.Contains(question, "？") {
			t.Errorf("%q is not a question", question)
		}
	}
}

// TestTheQuestionAndTheChecklistsAreBilingual is the rendering claim at the API boundary: a
// client asking for the reference data gets both languages for everything a patient or an
// officer reads.
func TestTheQuestionAndTheChecklistsAreBilingual(t *testing.T) {
	r := newRig(t)
	reference, err := r.store.Reference(r.ctx())
	if err != nil {
		t.Fatal(err)
	}
	if reference.Scale.QuestionBN == "" || reference.Scale.QuestionEN == "" {
		t.Fatal("the improvement question is not bilingual")
	}
	// Five, not §6's four: §6.4 is two checklists, because semaglutide is weekly and
	// liraglutide is daily and the timing item is the one thing that must not be shared.
	if len(reference.Checklists) != 5 {
		t.Fatalf("%d checklists, want five — spec §6's four lists, with §6.4 split into a "+
			"weekly and a daily GLP-1", len(reference.Checklists))
	}
	for _, checklist := range reference.Checklists {
		if checklist.TitleBN == "" || checklist.TitleEN == "" {
			t.Errorf("%s has a title in one language only", checklist.Code)
		}
		if len(checklist.Items) == 0 {
			t.Errorf("%s has no items", checklist.Code)
		}
		for _, item := range checklist.Items {
			if strings.TrimSpace(item.TextBN) == "" || strings.TrimSpace(item.TextEN) == "" {
				t.Errorf("%s item %d is not bilingual", checklist.Code, item.Ordinal)
			}
			// A Bangla string that is really English would pass an emptiness check and fail a
			// patient. The cheapest true test is that it contains Bengali script at all.
			if !hasBengali(item.TextBN) {
				t.Errorf("%s item %d's Bangla contains no Bengali script: %q",
					checklist.Code, item.Ordinal, item.TextBN)
			}
		}
	}
	for _, anchor := range reference.Scale.Anchors {
		if !hasBengali(anchor.LabelBN) {
			t.Errorf("the %q anchor has no Bengali label", anchor.LabelEN)
		}
	}
	for _, reason := range reference.MissReasons {
		if !hasBengali(reason.DisplayBN) {
			t.Errorf("the %q reason has no Bengali label", reason.Code)
		}
	}
}

func hasBengali(s string) bool {
	for _, r := range s {
		if r >= 0x0980 && r <= 0x09FF {
			return true
		}
	}
	return false
}

// TestTheScaleCoversItsRangeExactlyOnce is the property invariant 132 holds, asserted here too
// because this is where a screen would break: a value with no band cannot be selected, and a
// value with two bands draws two faces and means whichever the operator tapped.
func TestTheScaleCoversItsRangeExactlyOnce(t *testing.T) {
	r := newRig(t)
	scale, err := r.store.Scale(r.ctx())
	if err != nil {
		t.Fatal(err)
	}
	for value := scale.MinValue; value <= scale.MaxValue; value++ {
		matches := 0
		for _, anchor := range scale.Anchors {
			if value >= anchor.FromValue && value <= anchor.ToValue {
				matches++
			}
		}
		if matches != 1 {
			t.Errorf("%d is covered by %d bands, want exactly 1", value, matches)
		}
	}
	if _, ok := scale.AnchorFor(scale.NeutralValue); !ok {
		t.Error("the neutral value has no band")
	}
}

// ---------------------------------------------------------------------------
// 6. Compliance, and what it refuses
// ---------------------------------------------------------------------------

// TestMissedDosesAndTheirReasonsAreRecorded walks §7's answer end to end, including the
// distinction between "they said none" and "we did not ask".
func TestMissedDosesAndTheirReasonsAreRecorded(t *testing.T) {
	r := newRig(t)
	missed := 3
	if _, err := r.service.Record(r.ctx(), education.Assessment{
		EventID: uuid.New(), PatientID: r.patient, VisitID: r.visit,
		Compliance: education.Compliance{
			MissedDoses: &missed, Reasons: []string{"cost", "ran_out"},
		},
		LedgerSource: eventstore.SourceWeb,
	}); err != nil {
		t.Fatal(err)
	}

	var count float64
	if err := r.SQL.QueryRow(`
		SELECT value_num FROM read.observation
		 WHERE patient_id = $1 AND code = 'MEDICATION_MISSED_DOSES_7D' AND status = 'ACTIVE'`,
		r.patient).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("the record says %v missed doses, want 3", count)
	}

	var reasons []string
	rows, err := r.SQL.Query(`
		SELECT value_code FROM read.observation
		 WHERE patient_id = $1 AND code = 'MEDICATION_MISS_REASON' AND status = 'ACTIVE'
		 ORDER BY value_code`, r.patient)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil {
			t.Fatal(err)
		}
		reasons = append(reasons, reason)
	}
	if strings.Join(reasons, ",") != "cost,ran_out" {
		t.Errorf("the reasons read %v, want cost and ran_out. §7: the fix for each is different "+
			"and the distinction is invisible in a single adherence percentage.", reasons)
	}

	// A reason outside the vocabulary is refused rather than stored as free text.
	if _, err := r.service.Record(r.ctx(), education.Assessment{
		EventID: uuid.New(), PatientID: r.patient, VisitID: r.visit,
		Compliance:   education.Compliance{Reasons: []string{"could not be bothered"}},
		LedgerSource: eventstore.SourceWeb,
	}); err == nil {
		t.Error("an uncoded reason was accepted")
	}
}

// TestAnAnswerAgainstAnUnselectedChecklistIsRefused is the other half of criterion 1.
//
// The selection is only meaningful if it constrains the write. A pen checklist filled in for a
// patient on tablets alone is either a mis-tap or a record about somebody else, and both are
// worth a sentence while the patient is still in the room.
func TestAnAnswerAgainstAnUnselectedChecklistIsRefused(t *testing.T) {
	r := newRig(t)
	r.sheet.prescribe(r.product(t, "metformin hydrochloride", "tablet"))
	_, err := r.service.Record(r.ctx(), education.Assessment{
		EventID: uuid.New(), PatientID: r.patient, VisitID: r.visit,
		Items: []education.ItemResult{
			{Code: "EDU_PEN_04", State: education.Demonstrated},
		},
		LedgerSource: eventstore.SourceWeb,
	})
	if err == nil {
		t.Fatal("the pen checklist was recorded for a patient on tablets alone")
	}
}

// TestRecordingTwiceWithTheSameEventIdRecordsOnce is the retry property.
//
// A station tablet that lost the reply and pressed save again must not produce a second
// assessment: for a station whose whole output is "this happened in front of somebody", a
// duplicate is not a duplicate row, it is a demonstration that never happened.
func TestRecordingTwiceWithTheSameEventIdRecordsOnce(t *testing.T) {
	r := newRig(t)
	pen := r.product(t, "%isophane insulin human%", "pen")
	r.sheet.prescribe(pen)
	session, err := r.service.Session(r.ctx(), r.patient, r.visit, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	assessment := education.Assessment{
		EventID: uuid.New(), PatientID: r.patient, VisitID: r.visit,
		Items:        r.answersFor(t, session),
		LedgerSource: eventstore.SourceWeb,
	}
	if _, err := r.service.Record(r.ctx(), assessment); err != nil {
		t.Fatal(err)
	}
	// The second call carries the same ids. Whether it errors or succeeds is not the property
	// under test; what must be true is that the record holds one assessment afterwards.
	_, _ = r.service.Record(r.ctx(), assessment)

	var rows int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM read.observation
		 WHERE patient_id = $1 AND code = 'EDU_PEN_04'`, r.patient).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d rows for one item after two saves of the same assessment, want 1", rows)
	}
}
