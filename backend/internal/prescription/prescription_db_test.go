package prescription_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
)

// The prescription aggregate (CP80).
//
// Four acceptance criteria, and the way they are written here is decided by one sentence in the
// brief: *this project's recurring failure is green checks that confirm something EXISTS rather
// than WORKS.* So:
//
//   - The transition matrix is asserted **cell by cell**, all forty-nine of them, rather than by
//     sampling three illegal moves. A sampled matrix is a matrix with an untested hole in it,
//     and the hole is always the transition somebody adds by accident.
//   - Signed immutability is asserted **from four paths**: the service, the HTTP API, a plain
//     UPDATE as `dthcms_app`, and a plain UPDATE as `dthcms_projector` — the role that actually
//     holds the grant. The last one is the one that matters: a guarantee that rests on a grant
//     is a guarantee that ends the day somebody widens the grant, and the trigger is what
//     survives that.
//   - Price capture is asserted **by changing the price afterwards** and then by **rebuilding
//     the projection from the ledger**, because a test that only read the row back would pass
//     against an implementation that joined to the price table at read time.

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

type rig struct {
	*testsupport.DB
	pool    *pgxpool.Pool
	store   *prescription.Store
	service *prescription.Service
	machine *prescription.Machine
	events  *eventstore.Store
	engine  *projection.Engine
	server  *httptest.Server
	clock   *clock.Fixed

	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	visit    uuid.UUID
	product  uuid.UUID
	role     string
	held     []string
}

// stubHeader stands in for the composition root's patient bridge (CP81).
type stubHeader struct{}

func (stubHeader) PrescriptionHeader(context.Context, uuid.UUID,
	uuid.UUID) (prescription.HeaderFacts, error) {

	age := 52
	return prescription.HeaderFacts{
		ClinicalID: "DTHC-FRD-2026-000137", NameEN: "Md Rahim Uddin",
		NameBN: "মোঃ রহিম উদ্দিন", Sex: "male", AgeYears: &age,
	}, nil
}

type staff struct {
	facility, user, device uuid.UUID
	permissions            *[]string
	role                   *string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      "DOC01", Permissions: *s.permissions, Roles: []string{*s.role},
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code,
					DeviceID: s.device.String(),
					Role:     *s.role, Station: "STN_CONSULT",
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

// ctx is a context carrying a verified principal, which is the only way a service call can get
// an attribution envelope. There is no test-only door into `eventstore.Actor` that a handler
// could also reach — `ActorFrom` refuses without this, which is CP24's whole design.
func (r *rig) ctx() context.Context {
	return httpx.WithPrincipal(context.Background(), httpx.Principal{
		UserID: r.user.String(), FacilityID: r.facility.String(),
		SessionID: uuid.NewSHA1(r.user, []byte("session")).String(),
		Code:      "DOC01", DeviceID: r.device.String(),
		Role: r.role, Station: "STN_CONSULT",
	})
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

	r := &rig{DB: base, pool: pool, user: uuid.New(), device: uuid.New(), role: "PHYSICIAN"}
	r.held = []string{prescription.PermRead, prescription.PermDraft, prescription.PermSafetyCheck}
	r.clock = clock.NewFixed(time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC))
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

	r.store = prescription.NewStore(pool)
	r.machine, err = r.store.Machine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	catalogue := formulary.NewStore(pool)
	r.service = prescription.NewService(r.store, r.events, r.machine, catalogue, r.clock)
	r.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := prescription.NewHandlers(prescription.HandlersConfig{
		Service: r.service, Store: r.store, Clock: r.clock, Logger: logger,
		// CP81's print model needs a name and an age. A stub rather than the real bridge,
		// because `prescription` may not import `patient` and this package is its test.
		Header: stubHeader{},
	})
	who := staff{facility: r.facility, user: r.user, device: r.device,
		permissions: &r.held, role: &r.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 18, RequestTimeout: 30 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(rt chi.Router) {
			handlers.Mount(rt)
			handlers.MountContent(rt)
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

func (r *rig) seed(t *testing.T) {
	t.Helper()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'DOC01', 'Nahid', 'নাহিদ', 'active')`, r.user, r.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Consult 1', 'tablet', 'active', now())`, r.device, r.facility); err != nil {
		t.Fatal(err)
	}
	r.patient = uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000801', 'Rahima Begum', 'female', DATE '1965-04-17',
		        'day', 'national_id', '+8801711111801', 'active', $3, now())`,
		r.patient, r.facility, r.user); err != nil {
		t.Fatal(err)
	}
	r.visit = uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.visit (id, facility_id, patient_id, visit_code, visit_type, status,
		                        clinic_day, opened_at, opened_by)
		VALUES ($1, $2, $3, 'V-CP80-1', 'follow_up', 'open', current_date, now(), $4)`,
		r.visit, r.facility, r.patient, r.user); err != nil {
		t.Fatal(err)
	}
	// A real seeded product, so the price path runs against CP75's own data rather than a
	// fixture invented here.
	if err := r.SQL.QueryRow(`
		SELECT p.id FROM core.medication_product p
		  JOIN core.generic g ON g.id = p.generic_id
		 WHERE p.facility_id = $1 AND lower(g.name) = 'metformin hydrochloride'
		 ORDER BY p.trade_name LIMIT 1`, r.facility).Scan(&r.product); err != nil {
		t.Fatalf("the formulary has no metformin product to prescribe: %v", err)
	}
}

// draft opens a prescription with one metformin line on it.
func (r *rig) draft(t *testing.T) prescription.Prescription {
	t.Helper()
	sheet, err := r.service.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatalf("creating a draft: %v", err)
	}
	product := r.product
	if _, err := r.service.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: sheet.ID, ProductID: &product,
		Dose: "1 tablet", DailyDose: fp(1000), DoseUnit: "mg", Frequency: "twice daily",
		DurationDays: ip(30), Route: "oral",
		InstructionsEN: "After food.", InstructionsBN: "খাবারের পরে।",
	}); err != nil {
		t.Fatalf("adding an item: %v", err)
	}
	out, err := r.store.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// advance drives a prescription to a status through the legal path, using the service methods
// CP83, CP84, CP89 and CP118 will drive. Nothing here bypasses the machine.
func (r *rig) advance(t *testing.T, id uuid.UUID, to prescription.Status) prescription.Prescription {
	t.Helper()
	path := map[prescription.Status][]prescription.Status{
		prescription.StatusDraft:     {},
		prescription.StatusQAReview:  {prescription.StatusQAReview},
		prescription.StatusSigned:    {prescription.StatusQAReview, prescription.StatusSigned},
		prescription.StatusPrinted:   {prescription.StatusQAReview, prescription.StatusSigned, prescription.StatusPrinted},
		prescription.StatusDispensed: {prescription.StatusQAReview, prescription.StatusSigned, prescription.StatusDispensed},
		prescription.StatusCancelled: {prescription.StatusCancelled},
	}[to]

	var out prescription.Prescription
	var err error
	for _, step := range path {
		switch step {
		case prescription.StatusQAReview:
			out, err = r.service.Submit(r.ctx(), uuid.New(), id, eventstore.SourceWeb)
		case prescription.StatusSigned:
			out, err = r.service.Sign(r.ctx(), uuid.New(), id, eventstore.SourceWeb)
		case prescription.StatusPrinted:
			out, err = r.service.Print(r.ctx(), uuid.New(), id, eventstore.SourceWeb)
		case prescription.StatusDispensed:
			out, err = r.service.Dispense(r.ctx(), uuid.New(), id, eventstore.SourceWeb)
		case prescription.StatusCancelled:
			out, err = r.service.Cancel(r.ctx(), uuid.New(), id, "withdrawn in the test",
				eventstore.SourceWeb)
		}
		if err != nil {
			t.Fatalf("advancing to %s: %v", step, err)
		}
	}
	if to == prescription.StatusDraft {
		out, err = r.store.ByID(r.ctx(), id, r.facility)
		if err != nil {
			t.Fatal(err)
		}
	}
	if out.Status != to {
		t.Fatalf("wanted to reach %s, reached %s", to, out.Status)
	}
	return out
}

// freshProduct adds a metformin product with no price history at all, so a test can own the
// whole of one product's price timeline.
func (r *rig) freshProduct(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := r.SQL.QueryRow(`
		INSERT INTO core.medication_product
		  (facility_id, generic_id, trade_name, strength, form_code, manufacturer, dispense_unit)
		SELECT $1, g.id, $3, '500 mg', p.form_code, 'Test Pharma', p.dispense_unit
		  FROM core.medication_product p JOIN core.generic g ON g.id = p.generic_id
		 WHERE p.id = $2
		RETURNING id`, r.facility, r.product, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *rig) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, r.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }

// ---------------------------------------------------------------------------
// Criterion 4 — every illegal transition, individually
// ---------------------------------------------------------------------------

func TestEveryCellOfTheTransitionMatrix(t *testing.T) {
	// Forty-nine cells. Twelve legal, thirty-seven refused, and **each refused cell is its own
	// assertion** rather than a sample. A sampled matrix is a matrix with an untested hole in
	// it, and the hole is always the transition somebody adds by accident while fixing
	// something else.
	//
	// Checked against the machine here and against the database in
	// `TestTheDatabaseRefusesAnIllegalTransition`, because the two enforce it separately and
	// the dangerous failure is them disagreeing: an application that permits what the trigger
	// refuses writes a permanent ledger event the projection then cannot apply.
	r := newRig(t)

	legal := map[string]bool{}
	for _, edge := range r.machine.Edges() {
		legal[string(edge.From)+"->"+string(edge.To)] = true
	}
	if len(legal) != 12 {
		t.Fatalf("the matrix has %d edges, want 12. If an edge was added on purpose, this "+
			"number changes in the same commit as the migration and somebody reads both.",
			len(legal))
	}

	refused := 0
	for _, from := range prescription.AllStatuses {
		for _, to := range prescription.AllStatuses {
			key := string(from) + "->" + string(to)
			err := r.machine.Check(from, to)
			if legal[key] {
				if err != nil {
					t.Errorf("%s is a legal edge and was refused: %v", key, err)
				}
				continue
			}
			refused++
			if err == nil {
				t.Errorf("%s is not in core.prescription_transition and was allowed", key)
				continue
			}
			var te *prescription.TransitionError
			if !errors.As(err, &te) {
				t.Errorf("%s was refused with %T, want a TransitionError a screen can render", key, err)
				continue
			}
			if te.MessageEN() == "" || te.MessageBN() == "" {
				t.Errorf("%s was refused without a sentence in both languages", key)
			}
		}
	}
	if want := len(prescription.AllStatuses)*len(prescription.AllStatuses) - 12; refused != want {
		t.Errorf("%d cells were refused, want %d", refused, want)
	}
}

func TestTheServiceRefusesEveryIllegalTransitionOnRealPrescriptions(t *testing.T) {
	// The matrix above is the machine in isolation. This drives real prescriptions into each
	// reachable status and attempts every illegal move on them, through the service — which is
	// what a handler calls and therefore what a defect would actually reach.
	r := newRig(t)
	legal := map[string]bool{}
	for _, edge := range r.machine.Edges() {
		legal[string(edge.From)+"->"+string(edge.To)] = true
	}

	reachable := []prescription.Status{
		prescription.StatusDraft, prescription.StatusQAReview, prescription.StatusSigned,
		prescription.StatusPrinted, prescription.StatusDispensed, prescription.StatusCancelled,
	}
	attempt := map[prescription.Status]func(*rig, uuid.UUID) error{
		prescription.StatusQAReview: func(r *rig, id uuid.UUID) error {
			_, err := r.service.Submit(r.ctx(), uuid.New(), id, eventstore.SourceWeb)
			return err
		},
		prescription.StatusDraft: func(r *rig, id uuid.UUID) error {
			_, err := r.service.QABounce(r.ctx(), uuid.New(), id, "bounced", eventstore.SourceWeb)
			return err
		},
		prescription.StatusSigned: func(r *rig, id uuid.UUID) error {
			_, err := r.service.Sign(r.ctx(), uuid.New(), id, eventstore.SourceWeb)
			return err
		},
		prescription.StatusPrinted: func(r *rig, id uuid.UUID) error {
			_, err := r.service.Print(r.ctx(), uuid.New(), id, eventstore.SourceWeb)
			return err
		},
		prescription.StatusDispensed: func(r *rig, id uuid.UUID) error {
			_, err := r.service.Dispense(r.ctx(), uuid.New(), id, eventstore.SourceWeb)
			return err
		},
		prescription.StatusCancelled: func(r *rig, id uuid.UUID) error {
			_, err := r.service.Cancel(r.ctx(), uuid.New(), id, "no longer needed", eventstore.SourceWeb)
			return err
		},
	}

	for _, from := range reachable {
		for to, act := range attempt {
			if legal[string(from)+"->"+string(to)] || from == to {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				sheet := r.draft(t)
				sheet = r.advance(t, sheet.ID, from)
				if err := act(r, sheet.ID); err == nil {
					t.Fatalf("a %s prescription was moved to %s", from, to)
				}
				// And it did not move.
				after, err := r.store.ByID(r.ctx(), sheet.ID, r.facility)
				if err != nil {
					t.Fatal(err)
				}
				if after.Status != from {
					t.Fatalf("the refused transition changed the status to %s", after.Status)
				}
			})
		}
	}
}

func TestTheDatabaseRefusesAnIllegalTransitionEvenFromTheProjector(t *testing.T) {
	// The trigger, not the Go check. `dthcms_projector` is the role that actually holds UPDATE
	// on this table, so it is the role a broken projection — or a maintenance script written by
	// somebody in a hurry — would be running as.
	r := newRig(t)
	sheet := r.advance(t, r.draft(t).ID, prescription.StatusDispensed)
	projector := r.OpenAs(t, "dthcms_projector_local", "dthcms_local_only")

	// DISPENSED -> DRAFT is not an edge. Nothing in Go is involved in this statement.
	_, err := projector.Exec(
		`UPDATE read.prescription SET status = 'DRAFT' WHERE id = $1`, sheet.ID)
	if err == nil {
		t.Fatal("the database let a dispensed prescription be dragged back to DRAFT")
	}
	_, err = projector.Exec(
		`UPDATE read.prescription SET status = 'SIGNED' WHERE id = $1`, sheet.ID)
	if err == nil {
		t.Fatal("the database let a dispensed prescription be moved back to SIGNED")
	}
}

// ---------------------------------------------------------------------------
// Criterion 1 — a signed prescription cannot be modified by any path
// ---------------------------------------------------------------------------

func TestASignedPrescriptionCannotBeModifiedThroughTheService(t *testing.T) {
	r := newRig(t)
	sheet := r.draft(t)
	item := sheet.Items[0].ID
	sheet = r.advance(t, sheet.ID, prescription.StatusSigned)

	product := r.product
	if _, err := r.service.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: sheet.ID, ProductID: &product,
		Dose: "1 tablet", Frequency: "at night",
	}); !errors.Is(err, prescription.ErrNotEditable) {
		t.Errorf("adding an item to a signed prescription gave %v, want ErrNotEditable", err)
	}
	if _, err := r.service.ModifyItem(r.ctx(), prescription.Modification{
		PrescriptionID: sheet.ID, ItemID: item, Dose: "2 tablets", Frequency: "twice daily",
	}); !errors.Is(err, prescription.ErrNotEditable) {
		t.Errorf("modifying an item on a signed prescription gave %v, want ErrNotEditable", err)
	}
	if err := r.service.RemoveItem(r.ctx(), uuid.New(), sheet.ID, item, "changed my mind",
		eventstore.SourceWeb); !errors.Is(err, prescription.ErrNotEditable) {
		t.Errorf("removing an item from a signed prescription gave %v, want ErrNotEditable", err)
	}
}

func TestASignedPrescriptionCannotBeModifiedThroughTheAPI(t *testing.T) {
	// The same three attempts over HTTP, and the refusal has to be a 409 whose message points
	// at the correction path. A 409 that said only "conflict" would leave the physician
	// clicking the same button again.
	r := newRig(t)
	sheet := r.draft(t)
	item := sheet.Items[0].ID
	sheet = r.advance(t, sheet.ID, prescription.StatusSigned)
	base := "/v1/prescriptions/" + sheet.ID.String()

	cases := []struct {
		name, method, path string
		body               any
	}{
		{"add", http.MethodPost, base + "/items",
			map[string]any{"product_id": r.product.String(), "dose": "1 tablet", "frequency": "daily"}},
		{"modify", http.MethodPatch, base + "/items/" + item.String(),
			map[string]any{"dose": "2 tablets", "frequency": "twice daily"}},
		{"remove", http.MethodDelete, base + "/items/" + item.String(),
			map[string]any{"reason": "changed my mind"}},
		{"submit", http.MethodPost, base + "/submit", map[string]any{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, decoded := r.do(t, c.method, c.path, c.body)
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status %d, want 409. body: %v", resp.StatusCode, decoded)
			}
			envelope, _ := decoded["error"].(map[string]any)
			en, _ := envelope["message"].(string)
			bn, _ := envelope["message_bn"].(string)
			if en == "" || bn == "" {
				t.Fatalf("the refusal has no sentence in one of the two languages: %v", decoded)
			}
			// The sentence has to name the way out. A 409 saying only "conflict" leaves the
			// physician clicking the same button again.
			if !contains(lower(en), "correct") {
				t.Errorf("the refusal does not point at the correction path: %q", en)
			}
		})
	}
}

func TestTheDatabaseRefusesToEditASignedPrescription(t *testing.T) {
	// **This is the one that matters.** A handler check is one refactor from disappearing; a
	// grant is one migration from being widened. So both are asserted, separately, and the
	// trigger is asserted against the role that actually holds the grant.
	r := newRig(t)
	sheet := r.draft(t)
	item := sheet.Items[0].ID
	sheet = r.advance(t, sheet.ID, prescription.StatusSigned)

	app := r.OpenAs(t, "dthcms_app_local", "dthcms_local_only")
	projector := r.OpenAs(t, "dthcms_projector_local", "dthcms_local_only")

	t.Run("the application role holds no write grant at all", func(t *testing.T) {
		for _, stmt := range []struct{ what, sql string }{
			{"update the prescription", `UPDATE read.prescription SET status = 'DRAFT' WHERE id = $1`},
			{"delete the prescription", `DELETE FROM read.prescription WHERE id = $1`},
			{"update an item", `UPDATE read.prescription_item SET dose = 'ten tablets' WHERE prescription_id = $1`},
			{"delete an item", `DELETE FROM read.prescription_item WHERE prescription_id = $1`},
		} {
			if _, err := app.Exec(stmt.sql, sheet.ID); err == nil {
				t.Errorf("dthcms_app could %s. The application must hold SELECT and nothing "+
					"else on the prescription read model.", stmt.what)
			}
		}
	})

	t.Run("the projector holds the grant and the trigger refuses anyway", func(t *testing.T) {
		// Proof that the guarantee is not merely the grant: this role *can* write the table,
		// and is still refused.
		var granted bool
		if err := r.SQL.QueryRow(`
			SELECT has_table_privilege('dthcms_projector', 'read.prescription', 'UPDATE')`).
			Scan(&granted); err != nil {
			t.Fatal(err)
		}
		if !granted {
			t.Fatal("dthcms_projector cannot UPDATE read.prescription, so this test proves " +
				"nothing about the trigger")
		}

		for _, stmt := range []struct{ what, sql string }{
			{"change the patient", `UPDATE read.prescription SET patient_id = gen_random_uuid() WHERE id = $1`},
			{"change the visit", `UPDATE read.prescription SET visit_id = gen_random_uuid() WHERE id = $1`},
			{"change who wrote it", `UPDATE read.prescription SET created_by = gen_random_uuid() WHERE id = $1`},
			{"move the signing instant", `UPDATE read.prescription SET signed_at = now() WHERE id = $1`},
			{"change who signed it", `UPDATE read.prescription SET signed_by = gen_random_uuid() WHERE id = $1`},
			{"invent a correction link", `UPDATE read.prescription SET corrects_prescription_id = gen_random_uuid() WHERE id = $1`},
			{"delete it", `DELETE FROM read.prescription WHERE id = $1`},
			{"truncate the table", `TRUNCATE read.prescription CASCADE`},
		} {
			if _, err := projector.Exec(stmt.sql, sheet.ID); err == nil {
				t.Errorf("dthcms_projector could %s on a signed prescription", stmt.what)
			}
		}
		for _, stmt := range []struct{ what, sql string }{
			{"change the dose", `UPDATE read.prescription_item SET dose = 'ten tablets' WHERE id = $1`},
			{"change the instructions", `UPDATE read.prescription_item SET instructions_bn = 'যা খুশি' WHERE id = $1`},
			{"remove the line", `UPDATE read.prescription_item SET removed_at = now(), removed_by = gen_random_uuid() WHERE id = $1`},
			{"delete the line", `DELETE FROM read.prescription_item WHERE id = $1`},
		} {
			if _, err := projector.Exec(stmt.sql, item); err == nil {
				t.Errorf("dthcms_projector could %s on a signed prescription", stmt.what)
			}
		}
	})

	// And the row is exactly as it was.
	after, err := r.store.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != prescription.StatusSigned || len(after.Items) != 1 ||
		after.Items[0].Dose != "1 tablet" || after.Items[0].RemovedAt != nil {
		t.Fatalf("something got through: %+v", after)
	}
}

func TestItemsAreFrozenInQAReviewAndNotOnlyOnceSigned(t *testing.T) {
	// The step that is easy to get wrong. A prescription whose lines changed underneath the
	// reviewer is a prescription that was reviewed and then altered — the same defect as
	// editing a signed one, one step earlier — so the freeze is on `DRAFT`, not on `not SIGNED`.
	r := newRig(t)
	sheet := r.advance(t, r.draft(t).ID, prescription.StatusQAReview)
	product := r.product
	if _, err := r.service.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: sheet.ID, ProductID: &product, Dose: "1 tablet", Frequency: "daily",
	}); !errors.Is(err, prescription.ErrNotEditable) {
		t.Fatalf("an item was added while the prescription was with QA: %v", err)
	}
	projector := r.OpenAs(t, "dthcms_projector_local", "dthcms_local_only")
	if _, err := projector.Exec(`
		INSERT INTO read.prescription_item
		  (id, prescription_id, facility_id, line_no, product_label, dose, frequency,
		   recorded_at, recorded_by, event_id, global_seq)
		VALUES (gen_random_uuid(), $1, $2, 99, 'Something', '1 tablet', 'daily',
		        now(), $3, gen_random_uuid(), 999999)`,
		sheet.ID, r.facility, r.user); err == nil {
		t.Fatal("the database let an item be inserted onto a prescription that was with QA")
	}

	// And a bounce makes it editable again, which is the other half of the rule.
	if _, err := r.service.QABounce(r.ctx(), uuid.New(), sheet.ID, "HbA1c not ordered",
		eventstore.SourceWeb); err != nil {
		t.Fatal(err)
	}
	if _, err := r.service.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: sheet.ID, ProductID: &product, Dose: "1 tablet", Frequency: "daily",
	}); err != nil {
		t.Fatalf("a bounced prescription is not editable again: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Criterion 3 — the price at prescribing time
// ---------------------------------------------------------------------------

func TestThePriceAtPrescribingTimeIsNeverBackUpdated(t *testing.T) {
	// Set a price, draft a prescription, change the price, and the draft still carries the old
	// amount — **and a rebuild of the projection from the ledger still carries the old amount**.
	//
	// The rebuild is the assertion that matters. A test that only read the row back would pass
	// against an implementation that joined to `core.medication_price` at read time, because the
	// row would be correct until the next rebuild and then quietly wrong forever.
	r := newRig(t)
	ctx := r.ctx()
	catalogue := formulary.NewStore(r.pool)

	// A product of its own, so that the two prices below are the whole of this product's
	// history and the scenario is exactly "the price changed after the prescription was
	// written" rather than "a seeded price happened to be in the way".
	r.product = r.freshProduct(t, "Pricechangin")

	before, err := catalogue.RecordPrice(ctx, formulary.Recording{
		FacilityID: r.facility, ProductID: r.product,
		Amount: formulary.Taka(9, 50), From: r.clock.Now().AddDate(0, 0, -7),
		Origin: "MANUAL", ActorID: r.user, SourceNote: "the price before",
	})
	if err != nil {
		t.Fatalf("recording the first price: %v", err)
	}

	sheet := r.draft(t)
	if len(sheet.Items) != 1 || sheet.Items[0].Price == nil {
		t.Fatalf("the line carries no price: %+v", sheet.Items)
	}
	captured := *sheet.Items[0].Price
	if captured.AmountPoisha != 950 {
		t.Fatalf("the line captured %d poisha, want 950", captured.AmountPoisha)
	}
	if captured.PriceID != before.PriceID {
		t.Errorf("the line names price %s, want %s", captured.PriceID, before.PriceID)
	}
	if captured.AmountBDT != "9.50" {
		t.Errorf("the rendered amount is %q, want \"9.50\"", captured.AmountBDT)
	}

	// The price changes. This is the whole scenario.
	if _, err := catalogue.RecordPrice(ctx, formulary.Recording{
		FacilityID: r.facility, ProductID: r.product,
		Amount: formulary.Taka(14, 0), From: r.clock.Now().AddDate(0, 0, 1),
		Origin: "MANUAL", ActorID: r.user, SourceNote: "the price after",
	}); err != nil {
		t.Fatalf("recording the second price: %v", err)
	}

	again, err := r.store.ByID(ctx, sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Items[0].Price.AmountPoisha; got != 950 {
		t.Fatalf("after the price rose, the prescription says %d poisha. It must still say 950: "+
			"what a prescription records is what the patient was told to pay.", got)
	}

	// **And the projection cannot be rebuilt into the new one.**
	if _, err := r.engine.Rebuild(context.Background(), projection.Prescription{}.Name(),
		projection.RebuildOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}); err != nil {
		t.Fatalf("rebuilding the prescription projection: %v", err)
	}
	rebuilt, err := r.store.ByID(ctx, sheet.ID, r.facility)
	if err != nil {
		t.Fatalf("the prescription did not survive a rebuild: %v", err)
	}
	if len(rebuilt.Items) != 1 {
		t.Fatalf("the rebuild produced %d items, want 1", len(rebuilt.Items))
	}
	if got := rebuilt.Items[0].Price.AmountPoisha; got != 950 {
		t.Fatalf("rebuilt from the ledger, the prescription says %d poisha, want 950. "+
			"The price is being looked up at projection time instead of read from the event.", got)
	}
	if rebuilt.Items[0].Price.PriceID != before.PriceID {
		t.Errorf("the rebuild named price %s, want %s",
			rebuilt.Items[0].Price.PriceID, before.PriceID)
	}
	if rebuilt.Status != sheet.Status || rebuilt.CreatedBy != r.user {
		t.Errorf("the rebuild lost the prescription's own fields: %+v", rebuilt)
	}
}

func TestThePriceOnADraftLineCannotBeMovedEither(t *testing.T) {
	// **This test exists because a mutation survived without it.**
	//
	// `core.prescription_item_is_frozen()` refuses every write on a non-DRAFT prescription
	// before it reaches the price check, so the price branch is only reachable while the
	// prescription is still a draft — and nothing was exercising it. Disabling that branch
	// left the whole suite green, which is exactly the failure mode this project's
	// verification bar is about: a check that passes while the thing it checks is broken.
	//
	// A draft is where the price *would* be quietly repriced, too: an editor that re-read the
	// price on every keystroke would land here and nowhere else.
	r := newRig(t)
	sheet := r.draft(t)
	item := sheet.Items[0].ID
	if sheet.Status != prescription.StatusDraft {
		t.Fatalf("the fixture is %s; this test is about a draft", sheet.Status)
	}
	before := sheet.Items[0].Price
	if before == nil {
		t.Fatal("the fixture line carries no price, so this proves nothing")
	}

	projector := r.OpenAs(t, "dthcms_projector_local", "dthcms_local_only")
	for _, stmt := range []struct{ what, sql string }{
		{"raise the amount", `UPDATE read.prescription_item SET price_poisha = price_poisha + 500 WHERE id = $1`},
		{"repoint the price row", `UPDATE read.prescription_item SET price_id = gen_random_uuid() WHERE id = $1`},
		{"move the effective date", `UPDATE read.prescription_item SET price_effective_from = current_date WHERE id = $1`},
		{"move the capture instant", `UPDATE read.prescription_item SET price_captured_at = now() WHERE id = $1`},
	} {
		if _, err := projector.Exec(stmt.sql, item); err == nil {
			t.Errorf("the database let the projector %s on a DRAFT line. The price at the "+
				"time of prescribing must not move even while the prescription is editable.",
				stmt.what)
		}
	}

	// A clinical change on the same draft line is still allowed, so the refusal above is
	// about the price and not about drafts being frozen.
	if _, err := projector.Exec(
		`UPDATE read.prescription_item SET dose = '2 tablets' WHERE id = $1`, item); err != nil {
		t.Fatalf("the trigger refuses an ordinary edit to a draft line: %v", err)
	}

	after, err := r.store.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if after.Items[0].Price.AmountPoisha != before.AmountPoisha ||
		after.Items[0].Price.PriceID != before.PriceID {
		t.Fatalf("the price moved: %+v", after.Items[0].Price)
	}
}

func TestAProductWithNoPriceProducesNoPriceRatherThanZero(t *testing.T) {
	// Zero would tell a patient a medicine is free. CP75 is explicit that a product's price
	// history starts somewhere and the gap before it is a real state.
	r := newRig(t)
	unpriced := r.freshProduct(t, "Nopricin")
	sheet, err := r.service.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.service.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: sheet.ID, ProductID: &unpriced, Dose: "1 tablet", Frequency: "daily",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.Price != nil {
		t.Fatalf("a product with no price captured %+v, want no price at all", item.Price)
	}
}

// ---------------------------------------------------------------------------
// Criterion 2 — every item change is an event with attribution
// ---------------------------------------------------------------------------

func TestEveryItemChangeIsAnEventWithAttribution(t *testing.T) {
	r := newRig(t)
	sheet := r.draft(t)
	item := sheet.Items[0].ID

	if _, err := r.service.ModifyItem(r.ctx(), prescription.Modification{
		PrescriptionID: sheet.ID, ItemID: item,
		Dose: "half a tablet", DailyDose: fp(500), DoseUnit: "mg", Frequency: "once daily",
		InstructionsEN: "With the evening meal.", InstructionsBN: "রাতের খাবারের সঙ্গে।",
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.service.RemoveItem(r.ctx(), uuid.New(), sheet.ID, item,
		"the patient could not tolerate it", eventstore.SourceWeb); err != nil {
		t.Fatal(err)
	}

	rows, err := r.SQL.Query(`
		SELECT event_type, actor_user_id, actor_role, actor_device_id
		  FROM ledger.event
		 WHERE aggregate_type = 'PRESCRIPTION' AND aggregate_id = $1
		 ORDER BY sequence`, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var kind string
		var user, role string
		var device *string
		if err := rows.Scan(&kind, &user, &role, &device); err != nil {
			t.Fatal(err)
		}
		types = append(types, kind)
		if user != r.user.String() {
			t.Errorf("%s is attributed to %s, not to the signed-in prescriber", kind, user)
		}
		if role != r.role {
			t.Errorf("%s carries role %q, want %q", kind, role, r.role)
		}
		if device == nil || *device != r.device.String() {
			t.Errorf("%s carries no device; the envelope is incomplete", kind)
		}
	}
	want := []string{"PRESCRIPTION_CREATED", "PRESCRIPTION_ITEM_ADDED",
		"PRESCRIPTION_ITEM_MODIFIED", "PRESCRIPTION_ITEM_REMOVED"}
	if len(types) != len(want) {
		t.Fatalf("the ledger holds %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("the ledger holds %v, want %v", types, want)
		}
	}

	// The removed line is still in the read model, which is what makes "what was on this sheet
	// at 14:05" answerable after 14:06.
	after, err := r.store.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 1 {
		t.Fatalf("the removed line is gone from the read model: %+v", after.Items)
	}
	if after.Items[0].RemovedAt == nil || after.Items[0].RemovedBy == nil ||
		after.Items[0].RemovedReason == "" {
		t.Errorf("the removal is not attributed: %+v", after.Items[0])
	}
	if after.Items[0].Dose != "half a tablet" {
		t.Errorf("the modification did not apply: %q", after.Items[0].Dose)
	}
}

// ---------------------------------------------------------------------------
// Correction
// ---------------------------------------------------------------------------

func TestACorrectionSupersedesAndNeverEdits(t *testing.T) {
	r := newRig(t)
	original := r.advance(t, r.draft(t).ID, prescription.StatusSigned)
	originalDose := original.Items[0].Dose

	correction, err := r.service.Correct(r.ctx(), prescription.CorrectionRequest{
		Corrects: original.ID, Reason: "the dose was written for a normal eGFR", CopyItems: true,
	})
	if err != nil {
		t.Fatalf("correcting a signed prescription: %v", err)
	}
	if correction.Status != prescription.StatusDraft {
		t.Errorf("the correction arrived in %s, want DRAFT — it has to be editable", correction.Status)
	}
	if correction.Corrects == nil || *correction.Corrects != original.ID {
		t.Errorf("the correction does not name what it corrects")
	}
	if correction.CorrectionReason == "" {
		t.Error("the correction carries no reason")
	}
	if len(correction.Items) != 1 {
		t.Errorf("copy_items produced %d lines, want 1", len(correction.Items))
	}

	after, err := r.store.ByID(r.ctx(), original.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != prescription.StatusCorrected {
		t.Errorf("the original is %s, want CORRECTED", after.Status)
	}
	if after.CorrectedBySuccessor == nil || *after.CorrectedBySuccessor != correction.ID {
		t.Error("the original is not linked forward to its correction")
	}
	// **Nothing about the original changed except its status.** That is the whole policy.
	if after.Items[0].Dose != originalDose || after.Items[0].RemovedAt != nil {
		t.Errorf("the original's content changed: %+v", after.Items[0])
	}
	if after.SignedAt == nil || !after.SignedAt.Equal(*original.SignedAt) {
		t.Error("the original's signing instant moved")
	}

	// One correction each.
	if _, err := r.service.Correct(r.ctx(), prescription.CorrectionRequest{
		Corrects: original.ID, Reason: "again",
	}); !errors.Is(err, prescription.ErrAlreadyCorrected) {
		t.Errorf("a second correction of the same prescription gave %v, want ErrAlreadyCorrected", err)
	}
}

func TestCorrectingADispensedPrescriptionRecordsThatMedicineHasLeftTheCounter(t *testing.T) {
	// The plan's open decision, decided. The fact is recorded **on the correction**, because the
	// original's status becomes CORRECTED the same instant and stops saying DISPENSED — so a
	// reader of the correction who had to look at the original would find nothing.
	r := newRig(t)
	dispensed := r.advance(t, r.draft(t).ID, prescription.StatusDispensed)

	correction, err := r.service.Correct(r.ctx(), prescription.CorrectionRequest{
		Corrects: dispensed.ID, Reason: "wrong strength dispensed", CopyItems: true,
	})
	if err != nil {
		t.Fatalf("a dispensed prescription must still be correctable: %v", err)
	}
	if !correction.CorrectsDispensedOriginal {
		t.Fatal("the correction does not record that the prescription it corrects had already " +
			"been dispensed. Anybody reading it would not know medicine has left the counter.")
	}

	original, err := r.store.ByID(r.ctx(), dispensed.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if original.Status != prescription.StatusCorrected {
		t.Errorf("the original is %s, want CORRECTED", original.Status)
	}
	if original.DispensedAt == nil {
		t.Error("the original lost its dispensing instant")
	}

	// A correction of a prescription that was never signed is refused: a draft is edited.
	fresh := r.draft(t)
	if _, err := r.service.Correct(r.ctx(), prescription.CorrectionRequest{
		Corrects: fresh.ID, Reason: "typo",
	}); !errors.Is(err, prescription.ErrNotCorrectable) {
		t.Errorf("correcting a draft gave %v, want ErrNotCorrectable", err)
	}
}

// ---------------------------------------------------------------------------
// Carry-forward
// ---------------------------------------------------------------------------

func TestCarryForwardNeedsAnExplicitYes(t *testing.T) {
	r := newRig(t)
	previous := r.advance(t, r.draft(t).ID, prescription.StatusSigned)
	from := previous.ID

	if _, err := r.service.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit, CarryForwardFrom: &from,
	}); !errors.Is(err, prescription.ErrCarryForwardNotConfirmed) {
		t.Fatalf("carry-forward without confirmation gave %v, want a refusal. A physician who "+
			"did not mean to carry forward and silently got last month's drugs might not notice.",
			err)
	}

	carried, err := r.service.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
		CarryForwardFrom: &from, CarryForwardConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(carried.Items) != 1 {
		t.Fatalf("carry-forward produced %d lines, want 1", len(carried.Items))
	}
	if carried.Items[0].CarriedForwardFromItem == nil ||
		*carried.Items[0].CarriedForwardFromItem != previous.Items[0].ID {
		t.Error("the carried line does not say where it came from")
	}
	if carried.CarriedForwardFrom == nil || *carried.CarriedForwardFrom != from {
		t.Error("the new prescription does not say which one it was carried from")
	}

	// A removed line is not carried forward: it came off the sheet on purpose.
	other := r.draft(t)
	if err := r.service.RemoveItem(r.ctx(), uuid.New(), other.ID, other.Items[0].ID,
		"stopped", eventstore.SourceWeb); err != nil {
		t.Fatal(err)
	}
	signed := r.advance(t, other.ID, prescription.StatusSigned)
	src := signed.ID
	empty, err := r.service.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
		CarryForwardFrom: &src, CarryForwardConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Items) != 0 {
		t.Errorf("a removed line was carried forward: %+v", empty.Items)
	}
}

func TestCarryForwardRefusesAnotherPatientsPrescription(t *testing.T) {
	// One id typed wrong, and the worst mistake this feature could make.
	r := newRig(t)
	source := r.advance(t, r.draft(t).ID, prescription.StatusSigned)

	var other uuid.UUID = uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.patient (id, facility_id, clinical_id, name_en, sex, birth_date,
		                          dob_precision, dob_verified_by, phone_primary, status,
		                          registered_by, registered_at)
		VALUES ($1, $2, 'DTHC-FRD-2026-000802', 'Hasina Akter', 'female', DATE '1972-02-02',
		        'day', 'national_id', '+8801711111802', 'active', $3, now())`,
		other, r.facility, r.user); err != nil {
		t.Fatal(err)
	}
	from := source.ID
	if _, err := r.service.Create(r.ctx(), prescription.Creation{
		PatientID: other, VisitID: r.visit,
		CarryForwardFrom: &from, CarryForwardConfirmed: true,
	}); err == nil {
		t.Fatal("one patient's prescription was carried forward onto another patient")
	}
}

// ---------------------------------------------------------------------------
// Shape
// ---------------------------------------------------------------------------

func TestGoAndTheDatabaseAgreeAboutWhatIsEditable(t *testing.T) {
	// Two copies of "which statuses are frozen" — `Status.Editable()` and
	// `core.prescription_status.is_frozen` — and the failure mode of two copies is that they
	// agree until somebody edits one.
	r := newRig(t)
	statuses, err := r.store.Statuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != len(prescription.AllStatuses) {
		t.Fatalf("the database holds %d statuses and Go knows %d",
			len(statuses), len(prescription.AllStatuses))
	}
	for _, s := range statuses {
		if s.Frozen == s.Status.Editable() {
			t.Errorf("%s: the database says frozen=%v and Go says editable=%v",
				s.Status, s.Frozen, s.Status.Editable())
		}
		if s.Terminal != s.Status.Terminal() {
			t.Errorf("%s: the database says terminal=%v and Go says %v",
				s.Status, s.Terminal, s.Status.Terminal())
		}
		if s.NameBN == "" || s.MeaningBN == "" {
			t.Errorf("%s has no Bengali", s.Status)
		}
	}
}

func TestThePrescriptionPayloadCarriesNoDiagnosis(t *testing.T) {
	// §4.4 blinds the pharmacist to diagnoses, and the pharmacist holds `prescription.read`.
	// That grant is not a leak because a prescription in this model cannot carry a diagnosis —
	// and this is what holds that true as fields are added. CP118's own redaction golden test
	// covers the pharmacy queue; this covers the shape underneath it.
	r := newRig(t)
	sheet := r.draft(t)
	resp, _ := r.do(t, http.MethodGet, "/v1/prescriptions/"+sheet.ID.String(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body := r.rawGet(t, "/v1/prescriptions/"+sheet.ID.String())
	for _, forbidden := range []string{"diagnos", "icd", "problem_list", "comorbid"} {
		if contains(lower(body), forbidden) {
			t.Errorf("the prescription payload contains %q: %s", forbidden, body)
		}
	}
}

func (r *rig) rawGet(t *testing.T, path string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func lower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// The CP78 seam
// ---------------------------------------------------------------------------

// stubFacts is a clinical picture with nothing hidden in it: an eGFR of 25 taken today, an
// allergy list that was read and was empty, a diagnosis list that was read and was empty.
//
// Nil versus empty is the whole fail-closed contract and it is easy to get wrong in the
// direction that looks fine, so this returns non-nil empty slices deliberately — "somebody
// asked and there were none" — which is what lets the metformin rule be the only thing that
// fires.
type stubFacts struct{ at time.Time }

func (s stubFacts) Age(context.Context, uuid.UUID, uuid.UUID) (*float64, error) {
	return fp(61), nil
}
func (s stubFacts) Pregnancy(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "NOT_PREGNANT", nil
}
func (s stubFacts) Renal(context.Context, uuid.UUID, uuid.UUID) (*float64, *time.Time, error) {
	when := s.at
	return fp(25), &when, nil
}
func (s stubFacts) Hepatic(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	return "NONE", nil
}
func (s stubFacts) Diagnoses(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
	return []string{}, nil
}
func (s stubFacts) Allergies(context.Context, uuid.UUID, uuid.UUID) ([]medsafety.ReportedAllergy, error) {
	return []medsafety.ReportedAllergy{}, nil
}
func (s stubFacts) CurrentMedications(context.Context, uuid.UUID, uuid.UUID) ([]medsafety.Item, error) {
	return []medsafety.Item{}, nil
}

func TestTheSafetyCheckRunsAgainstASavedPrescription(t *testing.T) {
	// **The seam CP78 left, used.** That checkpoint built the engine around a proposed item
	// list and wrote down what CP80's route would be: load the prescription, map its items,
	// call the same `Engine.Check`. This asserts three things about that:
	//
	//  1. the plan's own scenario — metformin at eGFR 25 — blocks through this route;
	//  2. each finding's `subject_ref` is the **prescription item id**, which is what CP78
	//     reserved `Item.Ref` for and what lets CP81 highlight the exact row;
	//  3. a line the physician removed produces no findings, because a check that reported on
	//     a drug he has already taken off would train him to ignore findings.
	r := newRig(t)
	ctx := context.Background()
	catalogue := formulary.NewStore(r.pool)
	rules := medsafety.NewStore(r.pool, catalogue)
	engine := medsafety.NewEngine(rules, catalogue)

	// Approve MET-RENAL-30 through the physician's own publish path, inside this test's
	// throwaway database. Nothing in the development database is ever approved by a test.
	var versionID uuid.UUID
	if err := r.SQL.QueryRow(`
		SELECT v.id FROM core.medication_rule rr
		  JOIN core.medication_rule_version v ON v.rule_id = rr.id
		 WHERE rr.facility_id = $1 AND rr.code = 'MET-RENAL-30' AND v.status = 'DRAFT'`,
		r.facility).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Publish(ctx, r.facility, versionID, r.user, r.clock.Now()); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handlers := prescription.NewHandlers(prescription.HandlersConfig{
		Service: r.service, Store: r.store, Engine: engine,
		Facts: stubFacts{at: r.clock.Now()}, Clock: r.clock, Logger: logger,
	})
	who := staff{facility: r.facility, user: r.user, device: r.device,
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
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	r.server = server

	sheet := r.draft(t)
	item := sheet.Items[0].ID

	resp, decoded := r.do(t, http.MethodPost,
		"/v1/prescriptions/"+sheet.ID.String()+"/safety-check", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, decoded)
	}
	result, _ := decoded["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result in %v", decoded)
	}
	if result["verdict"] != "BLOCKED" {
		t.Fatalf("metformin at eGFR 25 gave verdict %v, want BLOCKED", result["verdict"])
	}

	findings, _ := result["findings"].([]any)
	found := false
	for _, raw := range findings {
		f, _ := raw.(map[string]any)
		if f["rule_code"] != "MET-RENAL-30" {
			continue
		}
		found = true
		if f["subject_ref"] != item.String() {
			t.Errorf("the finding points at %v, want the prescription item id %s. "+
				"CP78 reserved Item.Ref for exactly this.", f["subject_ref"], item)
		}
	}
	if !found {
		t.Fatalf("MET-RENAL-30 did not fire: %v", findings)
	}

	// CP79's renal block travels with the result.
	renal, _ := result["renal"].(map[string]any)
	if renal == nil || renal["known"] != true {
		t.Fatalf("the result carries no renal status: %v", result["renal"])
	}
	if renal["stage"] != "G4" {
		t.Errorf("eGFR 25 is staged %v, want G4", renal["stage"])
	}
	if renal["egfr_as_of"] == nil {
		t.Error("the renal status carries no date. Criterion 1 of CP79 is the value AND its date.")
	}

	// Take the line off and the findings go with it.
	if err := r.service.RemoveItem(r.ctx(), uuid.New(), sheet.ID, item, "stopped",
		eventstore.SourceWeb); err != nil {
		t.Fatal(err)
	}
	resp, decoded = r.do(t, http.MethodPost,
		"/v1/prescriptions/"+sheet.ID.String()+"/safety-check", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, decoded)
	}
	result, _ = decoded["result"].(map[string]any)
	if result["verdict"] != "NOTHING_PROPOSED" {
		t.Errorf("after the only line was removed the verdict is %v, want NOTHING_PROPOSED. "+
			"A removed drug that goes on producing findings trains the physician to ignore them.",
			result["verdict"])
	}
}
