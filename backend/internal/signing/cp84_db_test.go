package signing_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/config"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/projection"
	"github.com/AmlanWTK/DTHCMS/backend/internal/qa"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/signing"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// Signing and public verification against a real database (CP84, CP85).
//
// # What only a database can answer, and therefore what is here
//
//  1. **Tamper detection by direct modification.** The plan names this as the checkpoint's manual
//     verification: sign, verify, change a field directly in Postgres, verify again. It is
//     automated here — and it is done the way an attacker would have to do it, by disabling
//     CP80's freeze trigger as the table's owner, because that is the only way the row *can* be
//     changed and a test that could not reach it would be testing nothing.
//  2. **The clearance gate still holds.** CP84 wrote no gate of its own. This asserts that the
//     one CP83 built still refuses an uncleared prescription, through the signing path.
//  3. **Step-up.** Refused without a token, and refused with a token minted for another purpose.
//  4. **The public surface**: no PHI (as a positive assertion about the allowed key set),
//     non-enumerable tokens, rate limiting, and a tampered prescription showing as unverified.
//  5. **The attempt log**, which only exists because the endpoint is public.
//
// # The seed is a real Faridpur clinic morning
//
// Rahima Begum, 61, of Boalmari, eleven years of type 2 diabetes, on metformin. The same person
// station 10's tests see and the same person the screenshots show, because a fixture whose name
// is a placeholder is a fixture nobody notices is wrong.

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

type rig struct {
	*testsupport.DB
	pool   *pgxpool.Pool
	clock  *clock.Fixed
	events *eventstore.Store

	sheets      *prescription.Store
	prescribing *prescription.Service
	qaStore     *qa.Store
	qaService   *qa.Service

	store   *signing.Store
	service *signing.Service
	ring    *secretbox.Ring
	server  *httptest.Server
	stepUp  *stubStepUp
	limiter *stubLimiter

	facility uuid.UUID
	user     uuid.UUID
	device   uuid.UUID
	patient  uuid.UUID
	visit    uuid.UUID
	product  uuid.UUID

	role string
	held []string
}

// stubStepUp is the second factor. `purpose` records what the last presentation asked for, so a
// test can assert that a token minted for something else is refused *by purpose* rather than by
// happening to be wrong.
type stubStepUp struct {
	ok          bool
	mintedFor   string
	lastAskedAs string
}

func (s *stubStepUp) ConsumeStepUp(_ context.Context, _ string, _ string, purpose string) error {
	s.lastAskedAs = purpose
	if !s.ok {
		return errors.New("no second factor was presented")
	}
	if s.mintedFor != "" && s.mintedFor != purpose {
		// This is exactly what `auth.SecondFactor.ConsumeStepUp` does: a token is minted for
		// one purpose and is good for nothing else.
		return errors.New("the step-up token is unknown, expired, spent or for something else")
	}
	return nil
}

// stubLimiter is a token bucket in a map. Real enough for the property under test — that a
// budget exists, is spent, and refuses — without a Redis.
type stubLimiter struct {
	mu        sync.Mutex
	taken     map[string]int
	unhealthy bool
}

func (l *stubLimiter) Take(_ context.Context, key string, rule httpx.Rule, _ time.Time) (httpx.Decision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unhealthy {
		return httpx.Decision{}, errors.New("the counter is unreachable")
	}
	if l.taken == nil {
		l.taken = map[string]int{}
	}
	l.taken[key]++
	if l.taken[key] > rule.Burst {
		return httpx.Decision{Allowed: false, RetryAfter: rule.Every}, nil
	}
	return httpx.Decision{Allowed: true}, nil
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
		Code:      "DR01", Permissions: *s.permissions, Roles: []string{*s.role},
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
				DeviceID: s.device.String(), Role: *s.role, Station: "STN_CONSULT",
				// **NAMED, deliberately.** docs/signing.md §4: signing requires a step-up and
				// does not require a proven device, because the consultant signs at his desk on
				// a browser. A rig that used PROVEN everywhere would leave the decision
				// untested — and the whole point of the decision is that a browser can sign.
				DeviceAssurance: httpx.AssuranceNamed,
			})
			granted = rbac.GrantedForTest(granted, caller, *s.role, "STN_CONSULT")
			return granted, httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

func (r *rig) ctx() context.Context {
	return httpx.WithPrincipal(context.Background(), httpx.Principal{
		UserID: r.user.String(), FacilityID: r.facility.String(),
		SessionID: uuid.NewSHA1(r.user, []byte("session")).String(),
		Code:      "DR01", DeviceID: r.device.String(),
		Role: r.role, Station: "STN_CONSULT", DeviceAssurance: httpx.AssuranceNamed,
	})
}

func newRig(t *testing.T) *rig {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	r := &rig{DB: base, pool: pool, user: uuid.New(), device: uuid.New(), role: "PHYSICIAN"}
	r.clock = clock.NewFixed(time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC))
	r.held = []string{signing.PermSign, signing.PermRead, signing.PermVerificationLogRead,
		prescription.PermDraft, qa.PermReview, qa.PermClear, qa.PermBounce}
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&r.facility); err != nil {
		t.Fatal(err)
	}

	r.events = eventstore.New(eventstore.Config{
		Pool: pool, Clock: r.clock, Synchronous: projection.NewSyncSet(projection.Default),
	})
	engine := projection.NewEngineWithEvents(pool, projection.Default, r.events)
	if err := engine.Register(ctx); err != nil {
		t.Fatal(err)
	}

	catalogue := formulary.NewStore(pool)
	r.sheets = prescription.NewStore(pool)
	machine, err := r.sheets.Machine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r.prescribing = prescription.NewService(r.sheets, r.events, machine, catalogue, r.clock)

	medsafetyStore := medsafety.NewStore(pool, catalogue)
	r.qaStore = qa.NewStore(pool)
	r.qaService = qa.NewService(r.qaStore, r.events, qa.Sources{
		Sheets: r.sheets, Catalogue: catalogue, Values: clinical.NewStore(pool),
		Visits: visit.NewStore(pool), Histories: history.NewStore(pool),
		Allergies: allergy.NewStore(pool), Counsel: counseling.NewStore(pool),
		Educate: education.NewStore(pool),
		Safety:  medsafety.NewEngine(medsafetyStore, catalogue), Rules: medsafetyStore,
		Facts: quietFacts{}, Who: quietWho{}, Terms: clinicalterm.NewCache(pool),
	}, r.prescribing, r.clock)

	// **Every rule off.** docs/qa-rules.md §5's distinction, used here as a fixture: an empty
	// rule table means every prescription clears *after somebody looked*, and it cannot be made
	// to mean that nobody has to look. So these tests are about the gate rather than about the
	// eighteen rules, which `internal/qa` already covers — and
	// `TestAnUnclearedPrescriptionCannotBeSigned` still fails without a clearance, which is the
	// proof that turning the rules off did not turn the requirement off.
	if _, err := base.SQL.Exec(`UPDATE core.qa_rule SET enabled = false`); err != nil {
		t.Fatal(err)
	}

	signer, err := signing.NewSigner(config.EnvTest, signing.SignerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	material, err := base64.StdEncoding.DecodeString(config.LocalSecretKey)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := secretbox.NewRing(secretbox.Key{ID: "test-1", Material: material})
	if err != nil {
		t.Fatal(err)
	}
	r.ring = ring
	r.store = signing.NewStore(pool)
	r.service = signing.NewService(signing.ServiceConfig{
		Sheets: r.sheets, Store: r.store, Events: r.events, Signer: signer,
		Clearances: clearanceBridge{qa: r.qaStore}, Ring: ring, Clock: r.clock,
	})

	r.seed(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r.stepUp = &stubStepUp{ok: true}
	r.limiter = &stubLimiter{}
	handlers := signing.NewHandlers(signing.HandlersConfig{
		Service: r.service, Store: r.store, Sheets: r.sheets,
		StepUp: r.stepUp, Logger: logger,
	})
	// `stubDates` rather than `clinicalterm`: this package may not import it (architecture.json),
	// which is the whole reason the [signing.Dates] seam exists. What the rendering *is* is
	// asserted in `cmd/api`, which owns the bridge; what is asserted here is that the response
	// carries a rendered date in each language and never an ISO one.
	public, err := signing.NewPublicHandlers(signing.PublicHandlersConfig{
		Service: r.service, Store: r.store, Sheets: r.sheets,
		Dates: stubDates{}, Clock: r.clock, Logger: logger,
	})
	if err != nil {
		t.Fatalf("building the public verification handlers: %v", err)
	}
	who := staff{facility: r.facility, user: r.user, device: r.device,
		permissions: &r.held, role: &r.role}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 18, RequestTimeout: 30 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes:       func(rt chi.Router) { handlers.Mount(rt); handlers.MountOps(rt) },
		PublicRoutes: public.Mount,
		PublicPrefix: signing.PublicVerificationPathPrefix,
		// Five in a burst. Small so that the rate-limit test is a handful of requests rather
		// than a hundred; the production budget is in cmd/api and is its own decision.
		PublicRateLimit: httpx.Rule{Burst: 5, Every: 2 * time.Second},
		Limiter:         r.limiter,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.server = httptest.NewServer(router)
	t.Cleanup(r.server.Close)
	return r
}

// clearanceBridge is the same four lines cmd/api wires, held here so the test drives the real
// adapter rather than a fixture that always says "cleared".
type clearanceBridge struct{ qa *qa.Store }

func (b clearanceBridge) StandingClearance(ctx context.Context, facility,
	id uuid.UUID) (signing.Clearance, bool, error) {

	decision, found, err := b.qa.StandingDecision(ctx, facility, id)
	if err != nil || !found || decision.Outcome != qa.OutcomeCleared {
		return signing.Clearance{}, false, err
	}
	return signing.Clearance{ReviewID: decision.ID, DecidedAt: decision.DecidedAt}, true, nil
}

type quietFacts struct{}

func (quietFacts) Age(context.Context, uuid.UUID, uuid.UUID) (*float64, error) {
	years := 61.0
	return &years, nil
}
func (quietFacts) Pregnancy(context.Context, uuid.UUID, uuid.UUID) (string, error) { return "", nil }
func (quietFacts) Renal(context.Context, uuid.UUID, uuid.UUID) (*float64, *time.Time, error) {
	return nil, nil, nil
}
func (quietFacts) Hepatic(context.Context, uuid.UUID, uuid.UUID) (string, error) { return "", nil }
func (quietFacts) Diagnoses(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
	return []string{"E11.9"}, nil
}
func (quietFacts) Allergies(context.Context, uuid.UUID, uuid.UUID) ([]medsafety.ReportedAllergy, error) {
	return []medsafety.ReportedAllergy{}, nil
}
func (quietFacts) CurrentMedications(context.Context, uuid.UUID, uuid.UUID) ([]medsafety.Item, error) {
	return []medsafety.Item{}, nil
}

type quietWho struct{}

func (quietWho) AgeAndSex(context.Context, uuid.UUID, uuid.UUID) (*float64, string, error) {
	years := 61.0
	return &years, "female", nil
}

func (r *rig) seed(t *testing.T) {
	t.Helper()
	if _, err := r.SQL.Exec(`
		INSERT INTO core.app_user (id, facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'DR01', 'Dr K M Nahid Ul Haque', 'ডা. কে এম নাহিদ উল হক', 'active')`,
		r.user, r.facility); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SQL.Exec(`
		INSERT INTO core.device (id, facility_id, name, kind, status, enrolled_at)
		VALUES ($1, $2, 'Consultation desk 1', 'desktop', 'active', now())`,
		r.device, r.facility); err != nil {
		t.Fatal(err)
	}
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
		VALUES ($1, $2, $3, 'V-CP84-1', 'follow_up', 'open', current_date, now(), $4)`,
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
}

// submitted opens a sheet with one metformin line and sends it to QA.
func (r *rig) submitted(t *testing.T) prescription.Prescription {
	t.Helper()
	sheet, err := r.prescribing.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatalf("creating a draft: %v", err)
	}
	product := r.product
	duration := 30
	if _, err := r.prescribing.AddItem(r.ctx(), prescription.Addition{
		PrescriptionID: sheet.ID, ProductID: &product,
		Dose: "1 tablet", DoseUnit: "mg", Frequency: "twice daily",
		DurationDays: &duration, Route: "oral",
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

// cleared submits a sheet and has station 10 clear it.
func (r *rig) cleared(t *testing.T) prescription.Prescription {
	t.Helper()
	sheet := r.submitted(t)
	if _, err := r.qaService.Decide(r.ctx(), r.facility, qa.Deciding{
		PrescriptionID: sheet.ID, Outcome: qa.OutcomeCleared, Source: eventstore.SourceWeb,
	}); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	return sheet
}

func (r *rig) signed(t *testing.T) (prescription.Prescription, signing.Signed) {
	t.Helper()
	sheet := r.cleared(t)
	out, err := r.service.Sign(r.ctx(), uuid.New(), sheet.ID)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return sheet, out
}

// ---------------------------------------------------------------------------
// CP84
// ---------------------------------------------------------------------------

// TestASignedPrescriptionVerifiesAgainstItsStoredSignature is criterion 1.
func TestASignedPrescriptionVerifiesAgainstItsStoredSignature(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, out := r.signed(t)

	if out.Signature.Algorithm != signing.Algorithm {
		t.Errorf("algorithm = %q", out.Signature.Algorithm)
	}
	if out.Signature.SignerKind != signing.SignerLocal {
		t.Errorf("signer kind = %q", out.Signature.SignerKind)
	}
	// docs/signing.md §2's honesty clause, on the record rather than in a commit message.
	if out.Signature.NonExportableKey {
		t.Error("the local signer is recorded as holding a non-exportable key, which it does not")
	}
	// §4: the assurance is recorded, and the session was NAMED — a browser at a desk.
	if out.Signature.DeviceAssurance != signing.AssuranceNamed {
		t.Errorf("device assurance = %q, want NAMED: the consultant signed on a browser and "+
			"docs/signing.md §4 says that must be possible and must be recorded",
			out.Signature.DeviceAssurance)
	}
	// The clearance, by value, both halves. The review id alone would let verification depend on
	// a mutable row; see [signing.Signature].
	if out.Signature.QAReviewID == uuid.Nil {
		t.Error("the signature does not name the clearance it stood on")
	}
	if out.Signature.QAClearedAt.IsZero() {
		t.Error("the signature names a clearance and not when it was decided, so verification " +
			"cannot recompute the canonical bytes from the signature alone")
	}

	verification, err := r.service.Verify(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatalf("verifying: %v", err)
	}
	if verification.Verdict != signing.VerdictVerified {
		t.Fatalf("a freshly signed prescription does not verify: %s (%s)",
			verification.Verdict, verification.ReasonEN)
	}
	if verification.RecomputedSHA256 != out.Signature.CanonicalSHA256 {
		t.Errorf("the canonical digest recomputed as %s and was stored as %s",
			verification.RecomputedSHA256, out.Signature.CanonicalSHA256)
	}

	// The prescription is now SIGNED and, per CP80, immutable.
	after, err := r.sheets.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != prescription.StatusSigned {
		t.Fatalf("status = %s", after.Status)
	}
	if after.Editable {
		t.Error("a signed prescription reports itself editable")
	}
}

// TestAlteringAStoredFieldBreaksVerification is **the plan's named manual verification**,
// automated: sign, verify, change a field directly in Postgres, verify again.
//
// # Why the trigger is disabled to do it
//
// CP80's `prescription_item_is_frozen()` refuses this UPDATE, which is the point of CP80 and is
// not what CP84 is proving. What CP84 claims is that *if the row changed anyway* — a restored
// backup, a migration, a support script run as the owner, a future release with a bug — the
// signature would say so. The only way to reach that state is to do what somebody with owner
// rights would do, so the test does exactly that and puts the trigger back afterwards.
//
// It is table-driven over several fields, because a signature that covered the dose and not the
// frequency would pass a single-field version of this test.
func TestAlteringAStoredFieldBreaksVerification(t *testing.T) {
	t.Parallel()
	r := newRig(t)

	cases := []struct {
		what   string
		table  string
		update string
	}{
		{"the dose", "read.prescription_item", `UPDATE read.prescription_item SET dose = '2 tablets' WHERE prescription_id = $1`},
		{"the frequency", "read.prescription_item", `UPDATE read.prescription_item SET frequency = 'four times daily' WHERE prescription_id = $1`},
		{"the medicine", "read.prescription_item", `UPDATE read.prescription_item SET product_label = 'Glimepiride 2' WHERE prescription_id = $1`},
		{"the duration", "read.prescription_item", `UPDATE read.prescription_item SET duration_days = 90 WHERE prescription_id = $1`},
		{"one Bengali instruction", "read.prescription_item", `UPDATE read.prescription_item SET instructions_bn = 'খাবারের আগে।' WHERE prescription_id = $1`},
		{"the captured price", "read.prescription_item", `UPDATE read.prescription_item SET price_poisha = 1 WHERE prescription_id = $1`},
		{"which patient it is for", "read.prescription", `UPDATE read.prescription SET patient_id = gen_random_uuid() WHERE id = $1`},
		{"who wrote it", "read.prescription", `UPDATE read.prescription SET created_by = gen_random_uuid() WHERE id = $1`},
	}

	for _, one := range cases {
		t.Run(one.what, func(t *testing.T) {
			sheet, _ := r.signed(t)

			before, err := r.service.Verify(r.ctx(), r.facility, sheet.ID)
			if err != nil {
				t.Fatal(err)
			}
			if before.Verdict != signing.VerdictVerified {
				t.Fatalf("the prescription did not verify before being altered: %s", before.ReasonEN)
			}

			r.withTriggersOff(t, one.table, func() {
				if _, err := r.SQL.Exec(one.update, sheet.ID); err != nil {
					t.Fatalf("altering %s: %v", one.what, err)
				}
			})

			after, err := r.service.Verify(r.ctx(), r.facility, sheet.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Verdict != signing.VerdictVerified {
				return // refused, which is the point
			}
			t.Fatalf("%s was changed directly in the database and the prescription still "+
				"verifies. This field is not covered by the signature.", one.what)
		})
	}
}

// withTriggersOff does what somebody with owner rights on this database would have to do.
//
// `ALTER TABLE ... DISABLE TRIGGER USER` rather than dropping them: the triggers come back
// whatever the body does, so a failure inside it cannot leave the rest of the suite running
// against a table with no protection.
func (r *rig) withTriggersOff(t *testing.T, table string, body func()) {
	t.Helper()
	if _, err := r.SQL.Exec(`ALTER TABLE ` + table + ` DISABLE TRIGGER USER`); err != nil {
		t.Fatalf("disabling triggers on %s: %v", table, err)
	}
	defer func() {
		if _, err := r.SQL.Exec(`ALTER TABLE ` + table + ` ENABLE TRIGGER USER`); err != nil {
			t.Fatalf("re-enabling triggers on %s: %v", table, err)
		}
	}()
	body()
}

// TestCP80sTriggersStillRefuseTheAlterationSigningWouldDetect.
//
// The companion to the test above, and the reason it has to disable triggers. Without this one,
// a reader could conclude that CP84 replaced CP80's immutability with a check — it did not, and
// the ordinary path is still refused by the database before anything gets as far as a signature.
func TestCP80sTriggersStillRefuseTheAlterationSigningWouldDetect(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, _ := r.signed(t)

	_, err := r.SQL.Exec(`UPDATE read.prescription_item SET dose = '2 tablets' WHERE prescription_id = $1`, sheet.ID)
	if err == nil {
		t.Fatal("a signed prescription's dose was changed through the ordinary path; CP80's " +
			"freeze trigger is gone and the signature is now the only thing standing there")
	}
}

// TestAnUnclearedPrescriptionCannotBeSigned — the gate CP84 depends on and did not build.
func TestAnUnclearedPrescriptionCannotBeSigned(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet := r.submitted(t) // submitted, never cleared

	if _, err := r.service.Sign(r.ctx(), uuid.New(), sheet.ID); !errors.Is(err, signing.ErrNotCleared) {
		t.Fatalf("an uncleared prescription was signed (err=%v)", err)
	}

	after, err := r.sheets.ByID(r.ctx(), sheet.ID, r.facility)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != prescription.StatusQAReview {
		t.Fatalf("status moved to %s", after.Status)
	}
	if _, err := r.store.ByPrescription(r.ctx(), r.facility, sheet.ID); !errors.Is(err, signing.ErrNotSigned) {
		t.Fatalf("a signature exists for an uncleared prescription: %v", err)
	}
}

// TestTheDatabaseRefusesTheTransitionEvenWithoutTheServicesCheck is the mutation half of the
// test above: take the service's own refusal out of the picture by driving CP80's transition
// directly, and the trigger must still refuse.
//
// If this passed, the clearance gate would be a Go check somebody can refactor away.
func TestTheDatabaseRefusesTheTransitionEvenWithoutTheServicesCheck(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet := r.submitted(t)

	if _, err := r.prescribing.Sign(r.ctx(), uuid.New(), sheet.ID, eventstore.SourceWeb); err == nil {
		t.Fatal("CP80's transition moved an uncleared prescription to SIGNED; CP83's trigger " +
			"is not on read.prescription and every signing check in Go is now the only one")
	} else if !strings.Contains(strings.ToLower(err.Error()), "clearance") &&
		!strings.Contains(strings.ToLower(err.Error()), "qa") &&
		!strings.Contains(strings.ToLower(err.Error()), "signature") {
		t.Fatalf("refused, but not by a gate this checkpoint recognises: %v", err)
	}
}

// TestAPrescriptionIsSignedOnce. There is no re-signing; a signed prescription that was wrong is
// corrected (docs/signing.md §7).
func TestAPrescriptionIsSignedOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, _ := r.signed(t)

	if _, err := r.service.Sign(r.ctx(), uuid.New(), sheet.ID); !errors.Is(err, signing.ErrAlreadySigned) {
		t.Fatalf("a second signature was accepted (err=%v)", err)
	}
}

// TestASignatureCannotBeRewritten — the write-once trigger, tested as an attacker would meet it.
func TestASignatureCannotBeRewritten(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, _ := r.signed(t)

	if _, err := r.SQL.Exec(
		`UPDATE read.prescription_signature SET key_id = 'somebody-elses-key' WHERE prescription_id = $1`,
		sheet.ID); err == nil {
		t.Fatal("a signature's key id was changed; provenance is now a matter of opinion")
	}
	if _, err := r.SQL.Exec(
		`DELETE FROM read.prescription_signature WHERE prescription_id = $1`, sheet.ID); err == nil {
		t.Fatal("a signature was deleted")
	}
}

// ---------------------------------------------------------------------------
// Step-up (criterion 3)
// ---------------------------------------------------------------------------

// TestSigningRequiresAStepUp drives the real route, because the step-up is a property of the
// route rather than of the service — which is itself the thing worth asserting.
func TestSigningRequiresAStepUp(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet := r.cleared(t)

	// No token at all.
	r.stepUp.ok = false
	status, _ := r.post(t, "/v1/prescriptions/"+sheet.ID.String()+"/signature", "")
	if status != http.StatusForbidden {
		t.Fatalf("signing without a second factor answered %d, want 403", status)
	}
	if _, err := r.store.ByPrescription(r.ctx(), r.facility, sheet.ID); !errors.Is(err, signing.ErrNotSigned) {
		t.Fatal("a signature was written despite the step-up being refused")
	}

	// A token minted for a different purpose. This is the property purpose-binding exists for:
	// a token obtained to override a QA finding must not be spendable on a signature.
	r.stepUp.ok = true
	r.stepUp.mintedFor = qa.PurposeOverride
	status, _ = r.post(t, "/v1/prescriptions/"+sheet.ID.String()+"/signature", "a-token")
	if status != http.StatusForbidden {
		t.Fatalf("signing with a token minted for %q answered %d, want 403",
			qa.PurposeOverride, status)
	}

	// The right purpose.
	r.stepUp.mintedFor = signing.PurposeSign
	status, body := r.post(t, "/v1/prescriptions/"+sheet.ID.String()+"/signature", "a-token")
	if status != http.StatusCreated {
		t.Fatalf("signing with a correctly minted token answered %d: %s", status, body)
	}
	if r.stepUp.lastAskedAs != signing.PurposeSign {
		t.Fatalf("the route asked for purpose %q", r.stepUp.lastAskedAs)
	}

	// And the token it returns is the one thing the database does not hold.
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	token, _ := out["verification_token"].(string)
	if !signing.WellFormedToken(token) {
		t.Fatalf("the signing response did not carry a usable verification token: %q", token)
	}
	var stored int
	if err := r.SQL.QueryRow(
		`SELECT count(*) FROM read.prescription_signature WHERE verification_token_digest = $1`,
		signing.TokenDigest(token)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("the token's digest matched %d rows", stored)
	}
}

// TestTheStepUpPurposeIsTheOneAuthDeclares holds the two strings equal, as every other
// purpose-using module's contract test does. Two copies of a purpose agree until somebody edits
// one, and the failure then is a step-up that can never be satisfied.
func TestTheStepUpPurposeIsTheOneAuthDeclares(t *testing.T) {
	t.Parallel()
	if signing.PurposeSign != "prescription.sign" {
		t.Fatalf("signing.PurposeSign = %q", signing.PurposeSign)
	}
}

func (r *rig) post(t *testing.T, path, stepUp string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, r.server.URL+path, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("X-DTHCMS-Device", r.device.String())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	if stepUp != "" {
		req.Header.Set(httpx.StepUpHeader, stepUp)
	}
	resp, err := r.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func (r *rig) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The public endpoint is reached with no credentials at all — no Authorization, no device,
	// no CSRF header — because that is exactly how a phone's camera reaches it. The
	// authenticated routes below are reached through the same helper, which is why it carries
	// an Authorization header only when one is asked for.
	if strings.Contains(path, "/prescriptions/") || strings.Contains(path, "/ops/") {
		req.Header.Set("Authorization", "Bearer test")
		req.Header.Set("X-Requested-With", "DTHCMS")
	}
	resp, err := r.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// ---------------------------------------------------------------------------
// CP85 — the public surface
// ---------------------------------------------------------------------------

// publicResponseMayContain is the **whole** of what the public verification response is allowed
// to carry, as a positive assertion.
//
// A blacklist of forbidden words — "no patient name", "no drug" — passes the day somebody adds a
// field nobody thought to forbid. This is the other direction: every key in the response, at
// every depth, must be in this set, so a new field fails the test until a person adds it here
// and, in doing so, decides in a diff that a stranger may see it.
var publicResponseMayContain = map[string]bool{
	"verdict": true, "message_en": true, "message_bn": true,
	"prescription": true,
	"id":           true,
	// A date, in words, in both languages. Never an instant, and never ISO: this page is read by
	// a stranger holding paper, and `2026-09-14` is a storage format.
	"issued_on_en": true, "issued_on_bn": true,
	"physician_name_en": true, "physician_name_bn": true,
	"facility_name_en": true, "facility_name_bn": true,
	"item_count": true,
}

// TestThePublicResponseCarriesOnlyWhatItIsAllowedTo is the no-PHI test.
func TestThePublicResponseCarriesOnlyWhatItIsAllowedTo(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, out := r.signed(t)

	status, body := r.get(t, "/v1/verify/"+out.Token)
	if status != http.StatusOK {
		t.Fatalf("the public endpoint answered %d: %s", status, body)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding the public response: %v", err)
	}
	for _, key := range keysOf(decoded) {
		if !publicResponseMayContain[key] {
			t.Errorf("the public response carries %q, which is not in the allowed set. If a "+
				"stranger with no account may see it, add it to publicResponseMayContain and "+
				"say so in the diff.", key)
		}
	}

	if decoded["verdict"] != string(signing.VerdictVerified) {
		t.Fatalf("verdict = %v", decoded["verdict"])
	}
	block, _ := decoded["prescription"].(map[string]any)
	if block == nil {
		t.Fatal("a verified response carries no prescription block")
	}
	if block["physician_name_en"] != "Dr K M Nahid Ul Haque" {
		t.Errorf("issuing physician = %v", block["physician_name_en"])
	}
	if count, _ := block["item_count"].(float64); count != 1 {
		t.Errorf("item count = %v", block["item_count"])
	}
	// The date and not the instant: the minute a prescription was signed would let two sheets
	// be ordered against each other and correlated with a clinic's queue.
	//
	// And **a date in words**, in both languages, because this page is read by somebody holding
	// paper who told us no language. `2026-09-14` is a storage format and nobody says it aloud;
	// `internal/clinicalterm` is what renders it, the same package that fixed CP83's three
	// machine-shaped strings.
	issuedEN, _ := block["issued_on_en"].(string)
	issuedBN, _ := block["issued_on_bn"].(string)
	if !strings.Contains(issuedEN, "Sep") || !strings.Contains(issuedEN, "2026") {
		t.Errorf("issued_on_en = %q, which is not a date a person says aloud", issuedEN)
	}
	if strings.ContainsAny(issuedEN, "-") {
		t.Errorf("issued_on_en = %q, which still looks like ISO", issuedEN)
	}
	// Bengali digits, not Latin ones. A Bengali sentence with Latin numerals in it is the
	// half-translated screen `clinicalterm` exists to stop.
	if !strings.ContainsAny(issuedBN, "০১২৩৪৫৬৭৮৯") {
		t.Errorf("issued_on_bn = %q, which carries no Bengali digits", issuedBN)
	}
	if strings.ContainsAny(issuedBN, "0123456789") {
		t.Errorf("issued_on_bn = %q, which still carries Latin digits", issuedBN)
	}

	// And the belt-and-braces half: the patient's actual details, which the allowlist already
	// excludes structurally, do not appear anywhere in the bytes.
	for _, secret := range []string{
		"Rahima", "রহিমা", "DTHC-FRD-2026-000412", "+8801711204412", "Boalmari",
		"Metformin", "metformin", "twice daily", "After food", "খাবারের পরে",
		r.patient.String(), r.visit.String(), "E11.9",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("the public response contains %q", secret)
		}
	}
}

// keysOf collects every key at every depth.
func keysOf(value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		out := []string{}
		for key, nested := range typed {
			out = append(out, key)
			out = append(out, keysOf(nested)...)
		}
		return out
	case []any:
		out := []string{}
		for _, nested := range typed {
			out = append(out, keysOf(nested)...)
		}
		return out
	default:
		return nil
	}
}

// TestATamperedPrescriptionShowsAsUnverifiedAndSaysNothingElse is criterion 5.
func TestATamperedPrescriptionShowsAsUnverifiedAndSaysNothingElse(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, out := r.signed(t)

	r.withTriggersOff(t, "read.prescription_item", func() {
		if _, err := r.SQL.Exec(
			`UPDATE read.prescription_item SET dose = '4 tablets' WHERE prescription_id = $1`,
			sheet.ID); err != nil {
			t.Fatal(err)
		}
	})

	status, body := r.get(t, "/v1/verify/"+out.Token)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["verdict"] != string(signing.VerdictNotVerified) {
		t.Fatalf("a tampered prescription answered %v", decoded["verdict"])
	}
	if _, has := decoded["prescription"]; has {
		t.Error("an unverified response carries the prescription block; a stranger holding a " +
			"forged sheet is being shown a named physician beside an accusation")
	}

	// And an unknown token answers **identically**, which is what stops the endpoint being an
	// oracle over the token space.
	unknown, err := signing.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	_, unknownBody := r.get(t, "/v1/verify/"+unknown.Plaintext)
	if unknownBody != body {
		t.Errorf("a tampered prescription and an unknown token answer differently:\n"+
			" tampered: %s\n  unknown: %s", body, unknownBody)
	}
}

// TestAnUnknownTokenIsIndistinguishableFromAMalformedOne — the same argument one level down.
func TestAnUnknownTokenIsIndistinguishableFromAMalformedOne(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.signed(t)

	unknown, err := signing.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	statusA, bodyA := r.get(t, "/v1/verify/"+unknown.Plaintext)
	statusB, bodyB := r.get(t, "/v1/verify/not-a-token-at-all")
	if statusA != statusB || bodyA != bodyB {
		t.Fatalf("a well-formed unknown token and a malformed one answer differently:\n"+
			"  %d %s\n  %d %s", statusA, bodyA, statusB, bodyB)
	}
	if statusA != http.StatusOK {
		t.Fatalf("status = %d; an HTTP status that differed would put the answer where every "+
			"proxy and access log can see it", statusA)
	}
}

// TestThePublicEndpointIsRateLimited is criterion 4's first half.
func TestThePublicEndpointIsRateLimited(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, out := r.signed(t)

	// The rig's budget is five in a burst.
	for i := 0; i < 5; i++ {
		if status, body := r.get(t, "/v1/verify/"+out.Token); status != http.StatusOK {
			t.Fatalf("request %d answered %d: %s", i+1, status, body)
		}
	}
	status, body := r.get(t, "/v1/verify/"+out.Token)
	if status != http.StatusTooManyRequests {
		t.Fatalf("the sixth request in a five-request budget answered %d: %s", status, body)
	}
}

// TestThePublicEndpointFailsClosedWhenItCannotCount.
//
// The one place in this system where a limiter refuses rather than failing open. `httpx.RateLimit`
// argues at length for failing open on the clinical chain — a clinic must not stop taking blood
// pressures because Redis restarted — and neither half of that argument applies to a stranger's
// scan of a QR code.
func TestThePublicEndpointFailsClosedWhenItCannotCount(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, out := r.signed(t)

	r.limiter.mu.Lock()
	r.limiter.unhealthy = true
	r.limiter.mu.Unlock()

	if status, _ := r.get(t, "/v1/verify/"+out.Token); status != http.StatusTooManyRequests {
		t.Fatalf("with the counter unreachable the endpoint answered %d; it failed open on the "+
			"system's only unauthenticated surface", status)
	}
}

// TestEveryVerificationAttemptIsLogged, and the log carries nothing about a patient.
func TestEveryVerificationAttemptIsLogged(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, out := r.signed(t)

	unknown, err := signing.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	r.get(t, "/v1/verify/"+out.Token)
	r.get(t, "/v1/verify/"+unknown.Plaintext)

	var verified, notVerified int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FILTER (WHERE outcome = 'VERIFIED'),
		       count(*) FILTER (WHERE outcome = 'NOT_VERIFIED')
		  FROM ops.prescription_verification_attempt`).Scan(&verified, &notVerified); err != nil {
		t.Fatal(err)
	}
	if verified != 1 || notVerified != 1 {
		t.Fatalf("the log holds %d verified and %d unverified attempts, want 1 and 1",
			verified, notVerified)
	}

	// The successful attempt names the prescription it verified — from the resolved row, never
	// from the request. The unsuccessful one names nothing.
	var named *uuid.UUID
	if err := r.SQL.QueryRow(`
		SELECT prescription_id FROM ops.prescription_verification_attempt
		 WHERE outcome = 'NOT_VERIFIED'`).Scan(&named); err != nil {
		t.Fatal(err)
	}
	if named != nil {
		t.Errorf("an unsuccessful attempt named prescription %s; the public endpoint is a "+
			"linkage oracle", named)
	}
	if err := r.SQL.QueryRow(`
		SELECT prescription_id FROM ops.prescription_verification_attempt
		 WHERE outcome = 'VERIFIED'`).Scan(&named); err != nil {
		t.Fatal(err)
	}
	if named == nil || *named != sheet.ID {
		t.Errorf("the successful attempt names %v, want %s", named, sheet.ID)
	}

	// The presented token is not in the log.
	var rawTokens int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM ops.prescription_verification_attempt
		 WHERE encode(presented_digest, 'escape') LIKE '%' || $1 || '%'`,
		out.Token).Scan(&rawTokens); err != nil {
		t.Fatal(err)
	}
	if rawTokens != 0 {
		t.Error("a working verification token is stored in the attempt log")
	}

	// The log is append-only.
	if _, err := r.SQL.Exec(`DELETE FROM ops.prescription_verification_attempt`); err == nil {
		t.Error("the verification attempt log can be emptied")
	}
}

// TestTheAuthenticatedSignatureViewNeverLeavesTheFacility — a 403 that does not say whether the
// prescription exists.
func TestTheAuthenticatedSignatureViewNeverLeavesTheFacility(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, _ := r.signed(t)

	status, _ := r.get(t, "/v1/prescriptions/"+sheet.ID.String()+"/signature")
	if status != http.StatusOK {
		t.Fatalf("reading a signature in the caller's own facility answered %d", status)
	}

	// A prescription that does not exist, and one in another facility, answer the same way.
	statusMissing, bodyMissing := r.get(t, "/v1/prescriptions/"+uuid.NewString()+"/signature")
	if statusMissing != http.StatusForbidden {
		t.Fatalf("an unknown prescription answered %d, want 403 — a 404 here is an oracle", statusMissing)
	}
	if strings.Contains(bodyMissing, "not found") {
		t.Errorf("the refusal says the prescription was not found: %s", bodyMissing)
	}
}

// TestTheSignerIsRecordedOnEverySignature — docs/signing.md §2's "which prescriptions carry a
// weaker guarantee", asked as a query.
func TestTheSignerIsRecordedOnEverySignature(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.signed(t)

	rows, err := r.SQL.Query(`
		SELECT s.signer_kind, k.non_exportable
		  FROM read.prescription_signature s
		  JOIN core.signer_kind k ON k.kind = s.signer_kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	found := 0
	for rows.Next() {
		var kind string
		var nonExportable bool
		if err := rows.Scan(&kind, &nonExportable); err != nil {
			t.Fatal(err)
		}
		found++
		if kind != string(signing.SignerLocal) || nonExportable {
			t.Errorf("signature recorded as %s with non_exportable=%v", kind, nonExportable)
		}
	}
	if found != 1 {
		t.Fatalf("found %d signatures", found)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestTheInvariantsHold runs this migration's three assertions against a database with a signed
// prescription in it.
func TestTheInvariantsHold(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.signed(t)

	for _, assertion := range []string{
		"core.assert_every_signed_prescription_carries_a_signature",
		"core.assert_signing_still_sits_behind_the_clearance_gate",
		"core.assert_every_signature_names_a_signer_that_exists",
	} {
		if _, err := r.SQL.Exec(fmt.Sprintf(`SELECT %s()`, assertion)); err != nil {
			t.Errorf("%s: %v", assertion, err)
		}
	}
}

// TestAReprintCarriesTheSameQR is why the token is sealed rather than only digested.
//
// A prescription is printed more than once — the patient loses the paper, the pharmacy keeps a
// copy, CP89 renders the sheet a week later. A QR that changed between printings would make a
// prescription verified yesterday fail today, with nothing a patient could act on.
func TestAReprintCarriesTheSameQR(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, out := r.signed(t)

	again, err := r.service.VerificationToken(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatalf("recovering the token for a reprint: %v", err)
	}
	if again != out.Token {
		t.Fatalf("a reprint would carry a different QR:\n  first: %s\n  again: %s", out.Token, again)
	}

	// And the plaintext token is nowhere in the row. What is there is a ciphertext.
	var clear int
	if err := r.SQL.QueryRow(`
		SELECT count(*) FROM read.prescription_signature
		 WHERE encode(verification_token_sealed, 'escape') LIKE '%' || $1 || '%'`,
		out.Token).Scan(&clear); err != nil {
		t.Fatal(err)
	}
	if clear != 0 {
		t.Error("the verification token is stored in clear")
	}

	// A sealed token from one prescription cannot be opened as another's: the additional data
	// binds the ciphertext to the prescription it belongs to.
	other := uuid.New()
	sealed, keyID, err := r.store.SealedToken(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ring.Open(sealed, keyID, other[:]); err == nil {
		t.Error("a sealed token opened under a different prescription's id")
	}
}

// ---------------------------------------------------------------------------
// Readiness — the answer the prescriber cannot get anywhere else
// ---------------------------------------------------------------------------

// TestReadinessNamesWhichGateIsShut.
//
// A screen that offers a sign control the server will refuse is the CP92 defect on the most
// consequential write in this system, and it is *unavoidable* without this: the prescriber holds
// `prescription.sign` and not `qa.review`, so he cannot ask station 10 whether his own sheet was
// cleared. This asserts the readiness says which of the three gates is shut — and, in the last
// case, that it opens.
//
// It is driven through the service rather than the route because the route's own answer is
// asserted in [TestTheSignatureViewAnswersAnUnsignedPrescription]; what is under test here is
// that the three states are distinguishable at all, which is what the screen turns on.
func TestReadinessNamesWhichGateIsShut(t *testing.T) {
	t.Parallel()
	r := newRig(t)

	draft, err := r.prescribing.Create(r.ctx(), prescription.Creation{
		PatientID: r.patient, VisitID: r.visit,
	})
	if err != nil {
		t.Fatalf("creating a draft: %v", err)
	}
	stillADraft, err := r.service.SigningReadiness(r.ctx(), r.facility, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillADraft.MaySign || stillADraft.Status != string(prescription.StatusDraft) {
		t.Fatalf("a draft reads as %+v", stillADraft)
	}
	if !strings.Contains(stillADraft.ReasonEN, "DRAFT") {
		t.Errorf("the reason for a draft does not say it is a draft: %q", stillADraft.ReasonEN)
	}

	submitted := r.submitted(t)
	uncleared, err := r.service.SigningReadiness(r.ctx(), r.facility, submitted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if uncleared.MaySign || uncleared.Cleared {
		t.Fatalf("an uncleared prescription reads as signable: %+v", uncleared)
	}
	// The sentence has to name station 10 rather than say "you may not sign this", because the
	// physician's next act is to find the officer, not to try again.
	if !strings.Contains(uncleared.ReasonEN, "Station 10") {
		t.Errorf("the reason for an uncleared prescription does not name station 10: %q",
			uncleared.ReasonEN)
	}
	if uncleared.ReasonBN == "" {
		t.Error("the reason has no Bengali half; half this clinic reads the other one")
	}

	cleared := r.cleared(t)
	ready, err := r.service.SigningReadiness(r.ctx(), r.facility, cleared.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ready.MaySign || !ready.Cleared || ready.Signed {
		t.Fatalf("a cleared prescription does not read as signable: %+v", ready)
	}
	// **No reason beside an offered control.** A sentence printed next to a button somebody may
	// press reads as a warning about pressing it.
	if ready.ReasonEN != "" || ready.ReasonBN != "" {
		t.Errorf("a signable prescription carries a reason: %q / %q", ready.ReasonEN, ready.ReasonBN)
	}

	if _, err := r.service.Sign(r.ctx(), uuid.New(), cleared.ID); err != nil {
		t.Fatalf("signing: %v", err)
	}
	after, err := r.service.SigningReadiness(r.ctx(), r.facility, cleared.ID)
	if err != nil {
		t.Fatal(err)
	}
	// "Already signed" and "not cleared" are the same false and must not read as the same state.
	if after.MaySign || !after.Signed {
		t.Fatalf("a signed prescription reads as %+v", after)
	}
}

// TestTheSignatureViewAnswersAnUnsignedPrescription.
//
// 200 with `signed: false` rather than 403. The refusal the route used to give was correct about
// the danger and wrong about where it sits: what must not be knowable is whether a prescription
// *exists*, and that is settled by `resolve` before this body runs. A caller in another facility
// still gets the one 403 that says nothing — asserted in
// [TestTheAuthenticatedSignatureViewNeverLeavesTheFacility], which is unchanged.
func TestTheSignatureViewAnswersAnUnsignedPrescription(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet := r.submitted(t)

	status, body := r.get(t, "/v1/prescriptions/"+sheet.ID.String()+"/signature")
	if status != http.StatusOK {
		t.Fatalf("reading an unsigned prescription's signature answered %d: %s", status, body)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if signedFlag, _ := decoded["signed"].(bool); signedFlag {
		t.Error("an unsigned prescription reports signed: true")
	}
	if _, present := decoded["verification"]; present {
		t.Error("an unsigned prescription carries a verification; there is nothing to verify, " +
			"and a verdict about nothing is a verdict somebody will read")
	}
	readiness, _ := decoded["readiness"].(map[string]any)
	if readiness == nil {
		t.Fatal("no readiness block: the screen has no way to know why it may not offer a signature")
	}
	if maySign, _ := readiness["may_sign"].(bool); maySign {
		t.Error("an uncleared prescription reads as signable")
	}
	// §6 travels with every report of a signature, including the report that there is none: a
	// screen drawing the space where the handwritten image goes needs the caveat in both states.
	if _, present := decoded["signature_image"]; !present {
		t.Error("no signature_image caveat")
	}
}

// TestTheClearanceIsCoveredByValueAndNotByLookup.
//
// # The test whose absence let a real defect through
//
// The canonical form covers station 10's clearance, and an earlier version of `verifyAgainst`
// recomputed it by asking `StandingClearance` again — "what stands now". That looked correct and
// was not: the bytes a signature was made over must be recomputable from things that cannot
// change, and station 10's record can. A second CLEARED decision recorded later, or a
// `decided_at` rewritten by a backfill or a projection replay, and a prescription **nobody
// touched** verifies as altered — on the public page, that tells a stranger holding a genuine
// sheet it may not be real.
//
// So this does the thing the defect needed and the old suite never did: it signs, then moves the
// clearance underneath the signature, and asserts the verdict does not move with it. Both ways it
// can move are exercised, and the order is deliberate. The **timestamp rewrite** comes first
// because it is the mutation that tells the two candidate fixes apart: the review id does not
// change, so a fix that pinned the review — looking it up through `signature.qa_review_id` rather
// than storing the clearance by value — fails on that step, which is why the values are copied
// onto the signature instead. The **second clearance** follows, which both broken shapes fail.
//
// **To see it fail**, restore the lookup in `Service.verifyAgainst`:
//
//	found, ok, _ := s.clearance(ctx, sheet.FacilityID, sheet.ID)
//	if ok { clearance = found }
//
// and this test reports NOT_VERIFIED for a prescription it has not touched. The same happens for
// the half-fix that keeps `signature.QAReviewID` and looks `DecidedAt` up; both mutations are
// recorded in this checkpoint's report with their output.
func TestTheClearanceIsCoveredByValueAndNotByLookup(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sheet, _ := r.signed(t)

	signature, err := r.store.ByPrescription(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatalf("reading the signature back: %v", err)
	}
	if signature.QAReviewID == uuid.Nil || signature.QAClearedAt.IsZero() {
		t.Fatal("the signature does not carry the clearance by value, so this test cannot " +
			"distinguish a pinned clearance from a looked-up one")
	}

	before, err := r.service.Verify(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatalf("verifying: %v", err)
	}
	if before.Verdict != signing.VerdictVerified {
		t.Fatalf("the prescription did not verify before anything was moved: %+v", before)
	}

	// --- 1. the instant itself, rewritten, with the review unchanged -------
	//
	// First, because it is the mutation that tells the two candidate fixes apart. The review id
	// does not move here; only `decided_at` does — a backfill that normalised a time zone, a
	// replay that recomputed it, a corrected entry. A fix that pinned the *review* by looking it
	// up through `signature.qa_review_id` would sail past the swap below and fail right here,
	// because it would still be reading a mutable column.
	if _, err := r.SQL.Exec(
		`UPDATE read.qa_review SET decided_at = decided_at + interval '7 minutes'
		  WHERE prescription_id = $1`, sheet.ID); err != nil {
		t.Fatalf("rewriting the clearance's instant: %v", err)
	}

	rewritten, err := r.service.Verify(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatalf("verifying after the clearance's instant was rewritten: %v", err)
	}
	if rewritten.Verdict != signing.VerdictVerified {
		t.Fatalf("a rewritten clearance timestamp broke verification for a prescription nobody "+
			"touched: %s / %s — verification is reading a mutable column",
			rewritten.Verdict, rewritten.ReasonEN)
	}

	// --- 2. a second clearance, recorded after the signature ---------------
	//
	// Written straight into the read model rather than through `qa.Service`, because the service
	// will not clear a prescription that has left QA_REVIEW — which is exactly why the defect was
	// latent rather than live. The point is not that this path is reachable through the
	// application today; it is that the signature must not *depend* on its being unreachable. A
	// projection replay, a corrected review, or CP89 deciding a reprint needs a fresh decision
	// would all produce this row.
	later := signature.QAClearedAt.Add(2 * time.Hour)
	newReview := uuid.New()
	if _, err := r.SQL.Exec(`
		INSERT INTO read.qa_review (
			id, facility_id, prescription_id, patient_id, visit_id, outcome,
			decided_at, decided_by, findings, acknowledged, event_id, global_seq)
		SELECT $1, facility_id, prescription_id, patient_id, visit_id, outcome,
		       $2, decided_by, findings, acknowledged, $3, global_seq + 1000000
		  FROM read.qa_review
		 WHERE prescription_id = $4
		 ORDER BY decided_at DESC
		 LIMIT 1`, newReview, later, uuid.New(), sheet.ID); err != nil {
		t.Fatalf("recording a second clearance: %v", err)
	}

	standing, found, err := clearanceBridge{qa: r.qaStore}.StandingClearance(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatalf("reading the standing clearance: %v", err)
	}
	if !found || standing.ReviewID == signature.QAReviewID {
		t.Fatalf("the standing clearance did not move (found=%v, id=%v); this test would pass "+
			"vacuously", found, standing.ReviewID)
	}

	after, err := r.service.Verify(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatalf("verifying after a second clearance: %v", err)
	}
	if after.Verdict != signing.VerdictVerified {
		t.Fatalf("a prescription nobody touched stopped verifying because station 10's record "+
			"moved underneath it: %s / %s — verification is reading a mutable row",
			after.Verdict, after.ReasonEN)
	}

	// And the whole point of the signature still holds: the thing it *does* cover still breaks
	// it. Without this, a verifier that had stopped covering the clearance at all would pass
	// everything above.
	r.withTriggersOff(t, "read.prescription_item", func() {
		if _, err := r.SQL.Exec(
			`UPDATE read.prescription_item SET dose = '2 tablets' WHERE prescription_id = $1`,
			sheet.ID); err != nil {
			t.Fatalf("altering a dose: %v", err)
		}
	})
	tampered, err := r.service.Verify(r.ctx(), r.facility, sheet.ID)
	if err != nil {
		t.Fatalf("verifying a tampered prescription: %v", err)
	}
	if tampered.Verdict != signing.VerdictNotVerified {
		t.Fatal("a changed dose still verifies: this test is now asserting nothing")
	}
}

// stubDates stands in for `clinicalterm` in this package's tests.
//
// It renders the same *shape* the real one does — a day, a month in words, a year, and Bengali
// digits on the Bengali side — without the import this package is not allowed. That is enough for
// the assertions here, which are about the response carrying a human date in each language rather
// than about which abbreviation the clinic prefers; `internal/clinicalterm`'s own tests own that,
// and `cmd/api` owns the wiring that connects the two.
type stubDates struct{}

func (stubDates) EN(t time.Time) string {
	return fmt.Sprintf("%d %s %d", t.Day(), t.Month().String()[:3], t.Year())
}

func (stubDates) BN(t time.Time) string {
	bengali := func(n int) string {
		out := []rune{}
		for _, r := range fmt.Sprintf("%d", n) {
			out = append(out, rune('০'+(r-'0')))
		}
		return string(out)
	}
	months := [...]string{"জানু", "ফেব্রু", "মার্চ", "এপ্রিল", "মে", "জুন",
		"জুলাই", "আগস্ট", "সেপ্ট", "অক্টো", "নভে", "ডিসে"}
	return bengali(t.Day()) + " " + months[int(t.Month())-1] + " " + bengali(t.Year())
}
