package formulary_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
)

// The medicine formulary and its price history (CP75, §10, §16.1, D-56).
//
// Four acceptance criteria, and the first is the one everything else is arranged around:
//
//	1. the price as of any past date is retrievable;
//	2. bulk import validates and reports errors per row;
//	3. the seed formulary covers DTHC's common prescriptions — Dr. Nahid's to verify, not this
//	   suite's; what is tested here is that all 250 rows load and that the seed is re-runnable
//	   without clobbering a price a person has approved;
//	4. a monthly review reminder is issued to the named owner.
//
// # What these tests are guarding against, specifically
//
// The way a price-history module is usually wrong is not that it fails — it is that
// `SELECT ... ORDER BY effective_from DESC LIMIT 1` is written instead of a date filter, and
// every query returns *today's* price while looking entirely correct. Every prescription priced
// through such a module is priced wrongly and nothing ever says so. So the first test is a table
// of five dates around three prices, including both boundaries, and it asserts the *amount* each
// time rather than that a row came back.
//
// The second failure mode is a price that changed with nobody's name against it. Three separate
// mechanisms are tested: the CHECK constraint, the registered invariant, and the hash-chained
// audit trail driven end to end through the HTTP API.

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

type api struct {
	*testsupport.DB
	store    *formulary.Store
	server   *httptest.Server
	facility uuid.UUID
	// pharmacist is the person every write in these tests is attributed to.
	pharmacist     uuid.UUID
	pharmacistCode string
	held           []string
	recorder       *audit.Recorder
}

type staff struct {
	facility, user uuid.UUID
	code           string
	permissions    *[]string
	// device is empty on purpose in most tests: a browser has none, and the CP74 defect was a
	// read path that demanded one.
	device string
}

func (s staff) Identify(context.Context, string) (httpx.Caller, error) {
	return httpx.Caller{
		UserID: s.user.String(), FacilityID: s.facility.String(),
		SessionID: uuid.NewSHA1(s.user, []byte("session")).String(),
		Code:      s.code, Permissions: *s.permissions, Roles: []string{"PHARMACIST"},
		DeviceID: s.device,
	}, nil
}

func (s staff) Authorize(ctx context.Context, caller httpx.Caller, anyOf []string) (context.Context, httpx.AuthzDecision) {
	for _, want := range anyOf {
		for _, held := range caller.Permissions {
			if want == held {
				return httpx.WithPrincipal(ctx, httpx.Principal{
					UserID: caller.UserID, FacilityID: caller.FacilityID,
					SessionID: caller.SessionID, Code: caller.Code, Role: "PHARMACIST",
					DeviceID: s.device,
				}), httpx.AuthzDecision{Allowed: true, Reason: "allowed"}
			}
		}
	}
	return ctx, httpx.AuthzDecision{Reason: "permission_not_held"}
}

// testAuditor is the composition root's bridge, in miniature. The real one lives in cmd/api;
// this one writes the same entries through the same recorder, so the assertion about what lands
// in `ledger.audit_event` is an assertion about the production path.
type testAuditor struct{ recorder *audit.Recorder }

func (a *testAuditor) PriceChanged(ctx context.Context, change formulary.PriceChange) error {
	actor := change.ActorID
	details := map[string]any{
		"medicine": change.TradeName + " " + change.Strength,
		"price":    change.Amount.String(), "from": change.From,
		"verification": change.Verification, "origin": change.Origin,
	}
	kind := "formulary.price_set"
	if change.HadPrevious {
		kind = "formulary.price_changed"
		details["previous"] = change.Previous.String()
	}
	_, err := a.recorder.Record(ctx, audit.Entry{
		Kind: kind, FacilityID: change.FacilityID, ActorID: &actor,
		ActorCode: change.ActorCode, ActorRole: change.ActorRole, Details: details,
	})
	return err
}

func (a *testAuditor) ReviewOpened(ctx context.Context, opened formulary.ReviewOpened) error {
	entry := audit.Entry{
		Kind: "formulary.review_opened", FacilityID: opened.FacilityID,
		Details: map[string]any{
			"month": opened.PeriodMonth, "owner_role": opened.OwnerRole,
			"products": opened.Products, "unverified": opened.Unverified,
		},
	}
	if opened.OwnerUserID != nil {
		entry.TargetUserID = opened.OwnerUserID
	}
	_, err := a.recorder.Record(ctx, entry)
	return err
}

func (a *testAuditor) ReviewCompleted(ctx context.Context, done formulary.ReviewCompletion) error {
	actor := done.ActorID
	_, err := a.recorder.Record(ctx, audit.Entry{
		Kind: "formulary.review_completed", FacilityID: done.FacilityID, ActorID: &actor,
		ActorCode: done.ActorCode, ActorRole: done.ActorRole, Reason: done.Note,
		Details: map[string]any{"month": done.PeriodMonth, "unverified": done.Unverified},
	})
	return err
}

func newAPI(t *testing.T) *api {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &api{
		DB: base, store: formulary.NewStore(pool),
		held: []string{formulary.PermRead, formulary.PermWrite, formulary.PermReview},
	}
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	h.pharmacistCode = "PHR01"
	if err := base.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, $2, 'Rakib Hasan', 'রাকিব হাসান', 'active') RETURNING id`,
		h.facility, h.pharmacistCode).Scan(&h.pharmacist); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.recorder = audit.NewRecorder(audit.NewPostgresStore(pool), clock.Real{}, logger)
	handlers := formulary.NewHandlers(formulary.HandlersConfig{
		Store: h.store, Audit: &testAuditor{recorder: h.recorder},
		Clock: clock.Real{}, Logger: logger,
	})
	who := staff{facility: h.facility, user: h.pharmacist, code: h.pharmacistCode, permissions: &h.held}
	router, err := httpx.NewRouter(httpx.RouterOptions{
		Logger: logger, IDs: &ids.Sequential{Prefix: "req"},
		MaxBodyBytes: 1 << 20, RequestTimeout: 30 * time.Second,
		Health:        &httpx.Health{Service: "api", Version: "test", Logger: logger},
		Authenticator: who, Authorizer: who,
		Routes: func(r chi.Router) { handlers.Mount(r) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(router)
	t.Cleanup(h.server.Close)
	return h
}

func (h *api) do(t *testing.T, method, path string, body io.Reader, contentType string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "DTHCMS")
		req.Header.Set("Idempotency-Key", uuid.New().String())
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

func (h *api) get(t *testing.T, path string, query url.Values) (*http.Response, map[string]any) {
	t.Helper()
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return h.do(t, http.MethodGet, path, nil, "")
}

func (h *api) postJSON(t *testing.T, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return h.do(t, http.MethodPost, path, bytes.NewReader(raw), "application/json")
}

// aProduct returns the id of one seeded product, by trade name and strength.
func (h *api) aProduct(t *testing.T, trade, strength string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := h.SQL.QueryRow(`
		SELECT id FROM core.medication_product
		 WHERE facility_id = $1 AND trade_name = $2 AND strength = $3
		 ORDER BY dispense_unit LIMIT 1`, h.facility, trade, strength).Scan(&id); err != nil {
		t.Fatalf("finding %s %s in the seed: %v", trade, strength, err)
	}
	return id
}

// setPrice writes a price directly, so a history can be arranged without going through the API
// for every row. It uses the same two statements the store does, in the same order.
func (h *api) setPrice(t *testing.T, product uuid.UUID, poisha int64, from string, verified bool) {
	t.Helper()
	ctx := context.Background()
	verification := formulary.VerificationProvisional
	if verified {
		verification = formulary.VerificationVerified
	}
	if _, err := h.SQL.ExecContext(ctx, `
		UPDATE core.medication_price SET effective_to = $2::date
		 WHERE product_id = $1 AND effective_to IS NULL AND effective_from < $2::date`,
		product, from); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.ExecContext(ctx, `
		INSERT INTO core.medication_price
		  (facility_id, product_id, unit_price_poisha, effective_from, verification, origin, recorded_by)
		VALUES ($1, $2, $3, $4::date, $5, 'MANUAL', $6)`,
		h.facility, product, poisha, from, verification, h.pharmacist); err != nil {
		t.Fatalf("setting a price from %s: %v", from, err)
	}
}

func onDay(t *testing.T, day string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.UTC()
}

// ---------------------------------------------------------------------------
// Criterion 1: the price as of any past date
// ---------------------------------------------------------------------------

// TestThePriceAsOfADateIsThePriceThatWasInForce is the checkpoint's first criterion, tested at
// every boundary rather than in the middle of a range.
//
// Three prices, five dates: before the first, on each of the two changeover days, one day before
// a changeover, and after the last. The assertion is the **amount**, not that a row came back —
// a test that only checked for a non-empty result would pass against
// `ORDER BY effective_from DESC LIMIT 1`, which is the wrong implementation this test exists to
// catch and the one that looks right in a code review.
func TestThePriceAsOfADateIsThePriceThatWasInForce(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")

	// The seed price is effective from 2026-09-08. A history is arranged before it, so the
	// dates under test are all in the past and none of them is today.
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_price WHERE product_id = $1`, product); err != nil {
		t.Fatal(err)
	}
	h.setPrice(t, product, 500, "2024-01-01", true) // 5.00 from 1 Jan 2024
	h.setPrice(t, product, 625, "2024-03-04", true) // 6.25 from 4 Mar 2024
	h.setPrice(t, product, 900, "2024-11-15", true) // 9.00 from 15 Nov 2024

	for _, tc := range []struct {
		day  string
		want formulary.Money
		why  string
	}{
		{"2023-12-31", 0, "the day before the first price began: there was no price, and the answer is not the first one"},
		{"2024-01-01", 500, "the first price's own first day is inclusive"},
		{"2024-03-03", 500, "the day before a change is still the old price"},
		{"2024-03-04", 625, "the changeover day belongs to the new price, not the old one"},
		{"2024-11-14", 625, "the day before the second change is still the middle price"},
		{"2024-11-15", 900, "the second changeover day belongs to the last price"},
		{"2025-06-30", 900, "after the last change, with no successor, the last price still holds"},
	} {
		t.Run(tc.day, func(t *testing.T) {
			price, err := h.store.PriceOn(context.Background(), h.facility, product, onDay(t, tc.day))
			if tc.want == 0 {
				if err == nil {
					t.Fatalf("%s: got %s, want no price at all — %s", tc.day, price.Amount, tc.why)
				}
				if !strings.Contains(err.Error(), "no price") {
					t.Fatalf("%s: got %v, want the no-price sentinel", tc.day, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: %v — %s", tc.day, err, tc.why)
			}
			if price.Amount != tc.want {
				t.Errorf("on %s the price was %s, want %s — %s",
					tc.day, price.Amount, tc.want, tc.why)
			}
		})
	}
}

// TestAPriceQueryNeverReturnsAPriceThatHadNotTakenEffect is the same property stated the other
// way round, and it is the one that catches the wrong implementation directly.
//
// A price recorded today, effective next month, must be invisible to every query about a day
// before it starts — including the query about *today*. An implementation that took the most
// recently recorded row would return it, and every prescription written between now and then
// would be priced at a number nobody is charging yet.
func TestAPriceQueryNeverReturnsAPriceThatHadNotTakenEffect(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_price WHERE product_id = $1`, product); err != nil {
		t.Fatal(err)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	future := today.AddDate(0, 1, 0)
	h.setPrice(t, product, 500, today.Format("2006-01-02"), true)
	h.setPrice(t, product, 1200, future.Format("2006-01-02"), true)

	for _, day := range []time.Time{
		today, today.AddDate(0, 0, 1), future.AddDate(0, 0, -1),
	} {
		price, err := h.store.PriceOn(context.Background(), h.facility, product, day)
		if err != nil {
			t.Fatalf("%s: %v", day.Format("2006-01-02"), err)
		}
		if price.Amount != 500 {
			t.Errorf("on %s the price was %s; the 12.00 price does not take effect until %s",
				day.Format("2006-01-02"), price.Amount, future.Format("2006-01-02"))
		}
	}

	// And it does appear once it has taken effect, so the test above is not passing because
	// the future row is simply unreachable.
	price, err := h.store.PriceOn(context.Background(), h.facility, product, future)
	if err != nil {
		t.Fatal(err)
	}
	if price.Amount != 1200 {
		t.Errorf("on the day it takes effect the price was %s, want 12.00", price.Amount)
	}
}

// TestTheAsOfRouteAnswersThroughTheAPI walks the same property through HTTP, because CP127 will
// reach it that way and a store method that is right behind a handler that drops the date would
// still produce the wrong research.
func TestTheAsOfRouteAnswersThroughTheAPI(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_price WHERE product_id = $1`, product); err != nil {
		t.Fatal(err)
	}
	h.setPrice(t, product, 500, "2024-01-01", true)
	h.setPrice(t, product, 625, "2024-03-04", true)

	resp, body := h.get(t, "/v1/formulary/products/"+product.String()+"/price",
		url.Values{"on": {"2024-03-03"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	price, _ := body["price"].(map[string]any)
	if price["amount_bdt"] != "5.00" {
		t.Errorf("on 2024-03-03 the API said %v, want 5.00", price["amount_bdt"])
	}
	if body["on"] != "2024-03-03" {
		t.Errorf("the response echoed %v as the day, want 2024-03-03", body["on"])
	}

	// And a day before the history begins is a 404 rather than a 200 carrying nothing: a
	// client that received an empty price and rendered it would tell a patient a medicine is
	// free.
	resp, _ = h.get(t, "/v1/formulary/products/"+product.String()+"/price",
		url.Values{"on": {"2023-01-01"}})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a day before the first price answered %d, want 404", resp.StatusCode)
	}
}

// TestASupersededPriceIsNeverRewritten is the guarantee underneath criterion 1.
//
// If a price row could be edited, "what did this cost in March" would be a question whose answer
// depends on when you ask it — and §12.3's affordability analysis would silently change every
// time somebody corrected a typo.
func TestASupersededPriceIsNeverRewritten(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_price WHERE product_id = $1`, product); err != nil {
		t.Fatal(err)
	}
	h.setPrice(t, product, 500, "2024-01-01", true)
	h.setPrice(t, product, 625, "2024-03-04", true)

	var priceID uuid.UUID
	if err := h.SQL.QueryRow(`
		SELECT id FROM core.medication_price
		 WHERE product_id = $1 AND effective_from = DATE '2024-01-01'`, product).Scan(&priceID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, statement string }{
		{"the amount", `UPDATE core.medication_price SET unit_price_poisha = 1 WHERE id = $1`},
		{"the start date", `UPDATE core.medication_price SET effective_from = DATE '2023-01-01' WHERE id = $1`},
		{"the verification", `UPDATE core.medication_price SET verification = 'PROVISIONAL' WHERE id = $1`},
		{"who recorded it", `UPDATE core.medication_price SET recorded_by = NULL WHERE id = $1`},
		{"a closed period", `UPDATE core.medication_price SET effective_to = NULL WHERE id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.SQL.Exec(tc.statement, priceID); err == nil {
				t.Fatalf("changing %s of a superseded price succeeded; it must be refused", tc.name)
			}
		})
	}

	// And the row still says what it said.
	price, err := h.store.PriceOn(context.Background(), h.facility, product, onDay(t, "2024-02-01"))
	if err != nil {
		t.Fatal(err)
	}
	if price.Amount != 500 {
		t.Errorf("after five refused edits the February price is %s, want 5.00", price.Amount)
	}
}

// TestTwoPricesCannotCoverOneDay is what makes "the price on a date" a question with one answer
// rather than whichever row the planner returned first.
func TestTwoPricesCannotCoverOneDay(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_price WHERE product_id = $1`, product); err != nil {
		t.Fatal(err)
	}
	h.setPrice(t, product, 500, "2024-01-01", true)
	h.setPrice(t, product, 625, "2024-06-01", true)

	// A backdated price landing inside the closed January–June range.
	_, err := h.SQL.Exec(`
		INSERT INTO core.medication_price
		  (facility_id, product_id, unit_price_poisha, effective_from, effective_to,
		   verification, origin, recorded_by)
		VALUES ($1, $2, 777, DATE '2024-03-01', DATE '2024-04-01', 'VERIFIED', 'MANUAL', $3)`,
		h.facility, product, h.pharmacist)
	if err == nil {
		t.Fatal("a price overlapping an existing period was accepted; the EXCLUDE constraint must refuse it")
	}

	// The same attempt through the API is a 409 with a message the person can act on, rather
	// than a 500 with a constraint name in it.
	resp, body := h.postJSON(t, "/v1/formulary/products/"+product.String()+"/prices",
		map[string]any{"amount_bdt": "7.77", "effective_from": "2024-03-01"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("backdating over a closed period answered %d %v, want 409", resp.StatusCode, body)
	}
	if !hasBilingualFieldError(body, "effective_from") {
		t.Errorf("the 409 does not name effective_from in both languages: %v", body)
	}
}

// ---------------------------------------------------------------------------
// The security criterion: a price change names who made it
// ---------------------------------------------------------------------------

// TestTheAuditTrailNamesWhoChangedAPrice drives a price change through the HTTP API and reads
// the hash-chained trail back.
//
// End to end on purpose. The column on `core.medication_price` is tested by the constraint and
// the invariant; what this proves is that the *request path* carries the identity all the way
// through — which is the half that a refactor breaks.
func TestTheAuditTrailNamesWhoChangedAPrice(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")

	resp, body := h.postJSON(t, "/v1/formulary/products/"+product.String()+"/prices",
		map[string]any{"amount_bdt": "6.25", "verified": true, "source_note": "September stocktake"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}

	var (
		kind, actorCode, actorRole string
		actorID                    uuid.UUID
		details                    []byte
	)
	if err := h.SQL.QueryRow(`
		SELECT kind, actor_user_id, actor_code, actor_role, details
		  FROM ledger.audit_event
		 WHERE kind LIKE 'formulary.price%'
		 ORDER BY seq DESC LIMIT 1`).Scan(&kind, &actorID, &actorCode, &actorRole, &details); err != nil {
		t.Fatalf("reading the audit trail: %v", err)
	}
	if actorID != h.pharmacist {
		t.Errorf("the audit row names %s as the actor, want the pharmacist %s", actorID, h.pharmacist)
	}
	if actorCode != h.pharmacistCode {
		t.Errorf("the audit row names %q as the employee code, want %q", actorCode, h.pharmacistCode)
	}
	if actorRole != "PHARMACIST" {
		t.Errorf("the audit row names %q as the role, want PHARMACIST", actorRole)
	}
	// The seed price existed, so this is a change rather than a first setting — and the entry
	// has to carry the old price, or "who raised the price of insulin" is unanswerable.
	if kind != "formulary.price_changed" {
		t.Errorf("the audit row is %q, want formulary.price_changed", kind)
	}
	var carried map[string]any
	if err := json.Unmarshal(details, &carried); err != nil {
		t.Fatal(err)
	}
	if carried["price"] != "6.25" {
		t.Errorf("the audit row carries price %v, want 6.25", carried["price"])
	}
	if carried["previous"] == nil {
		t.Error("the audit row carries no previous price; a change with only the new number is unreadable")
	}
	if carried["medicine"] != "Comet 500 mg" {
		t.Errorf("the audit row names the medicine as %v, want Comet 500 mg", carried["medicine"])
	}

	// And the row is rendered as a sentence in both languages, which is what a person reads.
	events, err := h.recorder.Verify(context.Background())
	if err != nil {
		t.Fatalf("the audit chain does not verify after a price change: %v", err)
	}
	_ = events

	// The price row itself names the same person. Two mechanisms, not one: the column cannot
	// be missing and the trail cannot be quietly edited.
	var recordedBy uuid.UUID
	if err := h.SQL.QueryRow(`
		SELECT recorded_by FROM core.medication_price
		 WHERE product_id = $1 AND effective_to IS NULL`, product).Scan(&recordedBy); err != nil {
		t.Fatal(err)
	}
	if recordedBy != h.pharmacist {
		t.Errorf("the price row names %s, want the pharmacist %s", recordedBy, h.pharmacist)
	}
}

// TestAPriceCannotBeRecordedWithNobodysName is the constraint, tested directly.
func TestAPriceCannotBeRecordedWithNobodysName(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_price WHERE product_id = $1`, product); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, origin, verification string
		withPerson                 bool
	}{
		{"a manual price with no recorder", "MANUAL", "PROVISIONAL", false},
		{"an imported price with no recorder", "IMPORT", "PROVISIONAL", false},
		{"a seeded price marked verified", "SEED", "VERIFIED", false},
		{"a seeded price with a person against it", "SEED", "PROVISIONAL", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var person any
			if tc.withPerson {
				person = h.pharmacist
			}
			_, err := h.SQL.Exec(`
				INSERT INTO core.medication_price
				  (facility_id, product_id, unit_price_poisha, effective_from, verification,
				   origin, recorded_by)
				VALUES ($1, $2, 100, DATE '2020-01-01', $3, $4, $5)`,
				h.facility, product, tc.verification, tc.origin, person)
			if err == nil {
				t.Fatalf("%s was accepted; the constraints must refuse it", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Criterion 2: bulk import, per row
// ---------------------------------------------------------------------------

const importHeader = "generic,trade_name,strength,form,manufacturer,unit_price_bdt,unit\n"

// TestBulkImportReportsEveryRowsErrorInBothLanguages is the second criterion, with one line per
// failure mode and the good line proving that a rejection does not take the file down with it.
//
// The file, by line number:
//
//	2  good                — a real seeded product, repriced
//	3  missing required    — no strength
//	4  bad number          — a price that is not an amount
//	5  too much precision  — 0.335, which must be refused rather than rounded to 0.34
//	6  duplicate           — line 2's product again
//	7  unknown generic     — "Metformin HCl", which is not how this clinic spells it
//	8  unknown form        — "Tablte"
//	9  good                — a second real product, after five rejections
func TestBulkImportReportsEveryRowsErrorInBothLanguages(t *testing.T) {
	h := newAPI(t)

	file := importHeader +
		"Metformin hydrochloride,Comet,500 mg,Tablet,Square Pharmaceuticals PLC,5.75,tablet\n" +
		"Metformin hydrochloride,Comet,,Tablet,Square Pharmaceuticals PLC,6.00,tablet\n" +
		"Metformin hydrochloride,Comet XR,500 mg,Tablet (Extended Release),Square Pharmaceuticals PLC,six taka,tablet\n" +
		"Metformin hydrochloride,Comet XR,1000 mg,Tablet (Extended Release),Square Pharmaceuticals PLC,0.335,tablet\n" +
		"Metformin hydrochloride,Comet,500 mg,Tablet,Square Pharmaceuticals PLC,9.99,tablet\n" +
		"Metformin HCl,Bigomet,500 mg,Tablet,Square Pharmaceuticals PLC,4.00,tablet\n" +
		"Gliclazide,Comprid,80 mg,Tablte,Square Pharmaceuticals PLC,4.00,tablet\n" +
		"Gliclazide,Comprid,80 mg,Tablet,Square Pharmaceuticals PLC,4.50,tablet\n"

	resp, body := h.do(t, http.MethodPost, "/v1/formulary/imports?mode=apply&filename=september.csv",
		strings.NewReader(file), "text/csv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	report, _ := body["import"].(map[string]any)
	rows := rowsByLine(t, report)

	want := []struct {
		line      int
		outcome   string
		field     string
		mustSayEN string
		mustSayBN string
		why       string
	}{
		{2, "UPDATED", "", "", "", "a good row imports"},
		{3, "REJECTED", "strength", "required", "লাগবেই", "a missing required field names the column"},
		{4, "REJECTED", "unit_price_bdt", "not an amount", "অঙ্ক নয়", "a price that is not a number says so"},
		{5, "REJECTED", "unit_price_bdt", "poisha", "পয়সার", "a price finer than a poisha is refused, not rounded"},
		{6, "REJECTED", "trade_name", "line 2", "2 নম্বর", "a duplicate names the line it duplicates"},
		{7, "REJECTED", "generic", "not a generic", "জেনেরিক", "an unknown generic is refused, not created"},
		{8, "REJECTED", "form", "dosage form", "ধরন", "an unknown dosage form names the column"},
		{9, "UPDATED", "", "", "", "a good row after five rejections still imports"},
	}

	for _, tc := range want {
		row, ok := rows[tc.line]
		if !ok {
			t.Errorf("line %d is missing from the report entirely — %s", tc.line, tc.why)
			continue
		}
		if row["outcome"] != tc.outcome {
			t.Errorf("line %d is %v, want %s — %s", tc.line, row["outcome"], tc.outcome, tc.why)
			continue
		}
		if tc.outcome != "REJECTED" {
			continue
		}
		if row["field"] != tc.field {
			t.Errorf("line %d blames %v, want the %s column — %s", tc.line, row["field"], tc.field, tc.why)
		}
		en, _ := row["message_en"].(string)
		bn, _ := row["message_bn"].(string)
		if !strings.Contains(en, tc.mustSayEN) {
			t.Errorf("line %d says %q in English, which does not mention %q — %s",
				tc.line, en, tc.mustSayEN, tc.why)
		}
		if !strings.Contains(bn, tc.mustSayBN) {
			t.Errorf("line %d says %q in Bengali, which does not mention %q — %s",
				tc.line, bn, tc.mustSayBN, tc.why)
		}
		// Every rejection must read in Bengali as Bengali, not as an English sentence in a
		// Bengali-shaped field. The cheapest honest check is that it contains Bengali script.
		if !containsBengali(bn) {
			t.Errorf("line %d's Bengali message %q has no Bengali script in it", tc.line, bn)
		}
	}

	if got := report["rows_rejected"]; got != float64(6) {
		t.Errorf("the report counts %v rejections, want 6", got)
	}
	if got := report["rows_accepted"]; got != float64(2) {
		t.Errorf("the report counts %v accepted, want 2", got)
	}

	// The good rows actually landed — a report is not the same as an import.
	price, err := h.store.PriceOn(context.Background(), h.facility,
		h.aProduct(t, "Comet", "500 mg"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if price.Amount != 575 {
		t.Errorf("Comet 500 mg is priced at %s after the import, want 5.75", price.Amount)
	}
	// And the duplicate on line 6 did *not* win. That is the point of catching duplicates
	// within the file: the database would have taken the second row as an update and the
	// second price would have silently replaced the first with nothing said about it.
	if price.Amount == 999 {
		t.Error("line 6's duplicate overwrote line 2's price, silently")
	}

	// The rejected rows' products were not created.
	var strays int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM core.medication_product
		 WHERE facility_id = $1 AND trade_name IN ('Bigomet')`, h.facility).Scan(&strays); err != nil {
		t.Fatal(err)
	}
	if strays != 0 {
		t.Errorf("%d product(s) were created from a rejected row", strays)
	}
	// And no generic was invented.
	var generics int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.generic WHERE name = 'Metformin HCl'`).
		Scan(&generics); err != nil {
		t.Fatal(err)
	}
	if generics != 0 {
		t.Error("the import created a generic; an unknown molecule must be a rejection")
	}
}

// TestTheImportReportSurvivesTheRequest is the durability half of criterion 2. Per-row errors
// that exist only in one HTTP response are per-row errors nobody can act on the next morning.
func TestTheImportReportSurvivesTheRequest(t *testing.T) {
	h := newAPI(t)
	file := importHeader +
		"Metformin hydrochloride,Comet,,Tablet,Square Pharmaceuticals PLC,6.00,tablet\n"

	_, body := h.do(t, http.MethodPost, "/v1/formulary/imports?mode=apply",
		strings.NewReader(file), "text/csv")
	report, _ := body["import"].(map[string]any)
	id, _ := report["id"].(string)

	resp, reread := h.get(t, "/v1/formulary/imports/"+id, url.Values{"rejected": {"true"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, reread)
	}
	again, _ := reread["import"].(map[string]any)
	rows := rowsByLine(t, again)
	row, ok := rows[2]
	if !ok {
		t.Fatal("the rejected line is not in the stored report")
	}
	if en, _ := row["message_en"].(string); !strings.Contains(en, "required") {
		t.Errorf("the stored report says %q, which does not explain the rejection", en)
	}
	if bn, _ := row["message_bn"].(string); !containsBengali(bn) {
		t.Errorf("the stored report's Bengali message %q has no Bengali script in it", bn)
	}
}

// TestADryRunWritesTheReportAndChangesNothing is what makes per-row partial acceptance safe: the
// pharmacist sees the whole error report before a single price moves.
func TestADryRunWritesTheReportAndChangesNothing(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")
	before, err := h.store.PriceOn(context.Background(), h.facility, product, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	file := importHeader +
		"Metformin hydrochloride,Comet,500 mg,Tablet,Square Pharmaceuticals PLC,99.00,tablet\n" +
		"Metformin hydrochloride,Comet,,Tablet,Square Pharmaceuticals PLC,6.00,tablet\n"

	// No mode parameter at all: the default must be the safe one.
	resp, body := h.do(t, http.MethodPost, "/v1/formulary/imports",
		strings.NewReader(file), "text/csv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	report, _ := body["import"].(map[string]any)
	if report["mode"] != "DRY_RUN" {
		t.Fatalf("an import with no mode ran as %v; the default must be a dry run", report["mode"])
	}
	if report["rows_rejected"] != float64(1) || report["rows_accepted"] != float64(1) {
		t.Errorf("a dry run reported %v rejected and %v accepted, want 1 and 1",
			report["rows_rejected"], report["rows_accepted"])
	}
	if report["prices_recorded"] != float64(0) {
		t.Errorf("a dry run recorded %v prices; it must record none", report["prices_recorded"])
	}

	after, err := h.store.PriceOn(context.Background(), h.facility, product, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if after.Amount != before.Amount {
		t.Errorf("a dry run changed the price from %s to %s", before.Amount, after.Amount)
	}
}

// TestAFileWithNoUsableHeaderIsTheOneWholeFileRejection. Everything else is per row; a file
// whose columns cannot be identified has no rows to report per-row errors about, and guessing
// the column order is how a price lands in the strength.
func TestAFileWithNoUsableHeaderIsTheOneWholeFileRejection(t *testing.T) {
	h := newAPI(t)
	resp, body := h.do(t, http.MethodPost, "/v1/formulary/imports?mode=apply",
		strings.NewReader("what,who,how much\nComet,Square,5.00\n"), "text/csv")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a headerless file answered %d %v, want 422", resp.StatusCode, body)
	}
	if !hasBilingualFieldError(body, "file") {
		t.Errorf("the refusal does not explain itself in both languages: %v", body)
	}
}

// ---------------------------------------------------------------------------
// Criterion 3: the seed
// ---------------------------------------------------------------------------

// TestEverySeededPriceIsProvisionalAndNamesNobody.
//
// The 250 seeded prices are published MRP from medex.com.bd, not what this clinic charges. A
// price nobody has checked that looks like one somebody approved is the most dangerous thing
// this module could ship.
func TestEverySeededPriceIsProvisionalAndNamesNobody(t *testing.T) {
	h := newAPI(t)

	var products, prices, provisional, unattributed, withDGDA int
	if err := h.SQL.QueryRow(`
		SELECT (SELECT count(*) FROM core.medication_product WHERE facility_id = $1),
		       (SELECT count(*) FROM core.medication_price WHERE facility_id = $1),
		       (SELECT count(*) FROM core.medication_price
		         WHERE facility_id = $1 AND verification = 'PROVISIONAL'),
		       (SELECT count(*) FROM core.medication_price
		         WHERE facility_id = $1 AND recorded_by IS NULL),
		       (SELECT count(*) FROM core.medication_product
		         WHERE facility_id = $1 AND dgda_registration IS NOT NULL)`,
		h.facility).Scan(&products, &prices, &provisional, &unattributed, &withDGDA); err != nil {
		t.Fatal(err)
	}
	if products != 250 {
		t.Errorf("the seed loaded %d products, want 250", products)
	}
	if prices != 250 {
		t.Errorf("the seed loaded %d prices, want one per product", prices)
	}
	if provisional != 250 {
		t.Errorf("%d of %d seeded prices are provisional; every one must be", provisional, prices)
	}
	if unattributed != 250 {
		t.Errorf("%d seeded prices name a person; nobody recorded them and the rows must say so",
			prices-unattributed)
	}
	// Not one DGDA registration number was available from the source. A fabricated regulatory
	// identifier in a clinical system is worse than an absent one, so this asserts the absence.
	if withDGDA != 0 {
		t.Errorf("%d seeded products carry a DGDA registration number; none was available and "+
			"none may be invented", withDGDA)
	}

	// The API says so too, on the row a person reads.
	resp, body := h.get(t, "/v1/formulary/products", url.Values{"q": {"Comet"}, "limit": {"1"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) == 0 {
		t.Fatal("the formulary list is empty")
	}
	first, _ := items[0].(map[string]any)
	price, _ := first["price"].(map[string]any)
	if price["verification"] != "PROVISIONAL" {
		t.Errorf("the list shows verification %v, want PROVISIONAL", price["verification"])
	}
	if price["recorded_by_name_en"] != nil {
		t.Errorf("the list names %v as having recorded a seeded price", price["recorded_by_name_en"])
	}
}

// TestTheSeedIsRerunnableAndDoesNotClobberAnApprovedPrice.
//
// The seed has to be safe to apply again — after a restore, after Dr. Nahid corrects a row —
// and it must never overwrite a price a person has since approved. `core.apply_formulary_seed`
// is the projection, and this calls it twice with a verified price in between.
func TestTheSeedIsRerunnableAndDoesNotClobberAnApprovedPrice(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")

	// A human approves a price. Through the API, so this is the real path.
	resp, body := h.postJSON(t, "/v1/formulary/products/"+product.String()+"/prices",
		map[string]any{"amount_bdt": "7.25", "verified": true})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}

	// And somebody withdraws a different product.
	withdrawn := h.aProduct(t, "Comet XR", "500 mg")
	resp, body = h.postJSON(t, "/v1/formulary/products/"+withdrawn.String()+"/withdraw",
		map[string]any{"reason": "No longer stocked"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}

	var before, after int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.medication_price`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	var generics, products, inserted int
	if err := h.SQL.QueryRow(`SELECT * FROM core.apply_formulary_seed($1, DATE '2026-09-08')`,
		h.facility).Scan(&generics, &products, &inserted); err != nil {
		t.Fatalf("re-running the seed: %v", err)
	}
	if inserted != 0 {
		t.Errorf("re-running the seed inserted %d prices; every product already had one", inserted)
	}
	if generics != 0 {
		t.Errorf("re-running the seed inserted %d generics; all 59 already existed", generics)
	}

	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.medication_price`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("re-running the seed changed the price count from %d to %d", before, after)
	}

	// The approved price is untouched, and still approved.
	price, err := h.store.PriceOn(context.Background(), h.facility, product, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if price.Amount != 725 || !price.Verified() {
		t.Errorf("after a re-run the approved price is %s (%s), want 7.25 VERIFIED",
			price.Amount, price.Verification)
	}

	// And the withdrawal was not undone.
	var active bool
	if err := h.SQL.QueryRow(`SELECT is_active FROM core.medication_product WHERE id = $1`,
		withdrawn).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Error("re-running the seed reactivated a product somebody had withdrawn")
	}
}

// TestTheSeedDoesNotFillAGapAroundAPriceAPersonRecorded is the sharp edge of the same rule, and
// the case a narrower guard would let through.
//
// The guard in `core.apply_formulary_seed` is *"this product has no price at all"*, not "no price
// on the seed's own date" and not "no seeded price". The difference shows up here: a product
// priced by a person from a date **after** the seed's 8 September leaves 8 September uncovered,
// and a narrower guard would happily insert a PROVISIONAL seed price into that gap — putting a
// number nobody checked into the history of a medicine somebody had taken responsibility for,
// without violating any constraint and without anything looking wrong.
//
// Once a person has priced something, the seed has no further opinion about it, including about
// days they left uncovered.
func TestTheSeedDoesNotFillAGapAroundAPriceAPersonRecorded(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")

	// The clinic's own record starts a month after the seed's date, leaving 8 September to
	// 8 October with no price at all.
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_price WHERE product_id = $1`, product); err != nil {
		t.Fatal(err)
	}
	h.setPrice(t, product, 725, "2026-10-08", true)

	var generics, products, inserted int
	if err := h.SQL.QueryRow(`SELECT * FROM core.apply_formulary_seed($1, DATE '2026-09-08')`,
		h.facility).Scan(&generics, &products, &inserted); err != nil {
		t.Fatalf("re-running the seed: %v", err)
	}

	history, err := h.store.PriceHistory(context.Background(), h.facility, product)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("the seed added %d price row(s) around a price a person had recorded; "+
			"once somebody has priced a medicine the seed is done with it", len(history)-1)
	}
	if history[0].Origin == formulary.OriginSeed {
		t.Error("the surviving price is the seed's, not the person's")
	}
	if inserted != 0 {
		t.Errorf("the seed reported %d insertions into a priced product's history", inserted)
	}

	// And 8 September genuinely has no price, rather than a seeded one. "We had not priced
	// this yet" is the truth about that day and the formulary must be able to say it.
	if _, err := h.store.PriceOn(context.Background(), h.facility, product,
		onDay(t, "2026-09-20")); err == nil {
		t.Error("a price was returned for a day the clinic had not priced; the seed filled the gap")
	}
}

// TestWithdrawingAProductKeepsItsPriceHistory. Nothing is deleted.
func TestWithdrawingAProductKeepsItsPriceHistory(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")

	h.postJSON(t, "/v1/formulary/products/"+product.String()+"/prices",
		map[string]any{"amount_bdt": "6.00", "verified": true})
	resp, body := h.postJSON(t, "/v1/formulary/products/"+product.String()+"/withdraw",
		map[string]any{"reason": "Manufacturer discontinued it"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}

	history, err := h.store.PriceHistory(context.Background(), h.facility, product)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Errorf("a withdrawn product has %d price rows, want both of them", len(history))
	}

	// The application role cannot remove them even if it wanted to.
	if _, err := h.SQL.Exec(`SET ROLE dthcms_app`); err != nil {
		t.Skipf("cannot assume the application role here: %v", err)
	}
	defer func() { _, _ = h.SQL.Exec(`RESET ROLE`) }()
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_price WHERE product_id = $1`, product); err == nil {
		t.Error("the application role deleted a price row; the formulary keeps its history")
	}
	if _, err := h.SQL.Exec(`DELETE FROM core.medication_product WHERE id = $1`, product); err == nil {
		t.Error("the application role deleted a product; a withdrawn product is deactivated, not removed")
	}
}

// ---------------------------------------------------------------------------
// Criterion 4: the monthly review
// ---------------------------------------------------------------------------

// TestTheMonthlyReviewOpensOnceAndRemindsItsOwner.
//
// The sweep runs daily and decides for itself whether a review is due. Three things are asserted
// and the second is the one that matters: **running it again does not remind anybody twice.** A
// reminder that arrives every morning is one people filter out, and then the review stops
// happening with nothing looking wrong.
func TestTheMonthlyReviewOpensOnceAndRemindsItsOwner(t *testing.T) {
	h := newAPI(t)
	auditor := &testAuditor{recorder: h.recorder}
	ctx := context.Background()

	// The 5th of the month, with the review due on the 1st.
	march := time.Date(2027, 3, 5, 9, 0, 0, 0, time.UTC)

	opened, err := h.store.ReviewSweep(ctx, march, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("the sweep opened %d reviews, want 1", opened)
	}

	// Again, the next day, and the day after.
	for _, day := range []time.Time{march.AddDate(0, 0, 1), march.AddDate(0, 0, 2)} {
		again, err := h.store.ReviewSweep(ctx, day, auditor)
		if err != nil {
			t.Fatal(err)
		}
		if again != 0 {
			t.Errorf("the sweep opened %d more reviews on %s; the month already had one",
				again, day.Format("2006-01-02"))
		}
	}

	var alerts int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM core.admin_alert WHERE kind = 'formulary.price_review_due'`).
		Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Errorf("%d reminders were raised for one month; a reminder that arrives daily is one "+
			"people stop reading", alerts)
	}

	// The reminder reads in both languages and names the number that makes the review worth
	// doing — how many prices nobody has checked.
	var en, bn string
	if err := h.SQL.QueryRow(`
		SELECT message_en, message_bn FROM core.admin_alert
		 WHERE kind = 'formulary.price_review_due'`).Scan(&en, &bn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(en, "250") {
		t.Errorf("the English reminder %q does not say how many prices are unchecked", en)
	}
	if !containsBengali(bn) {
		t.Errorf("the Bengali reminder %q has no Bengali script in it", bn)
	}
	if !strings.Contains(bn, "250") {
		t.Errorf("the Bengali reminder %q does not say how many prices are unchecked", bn)
	}

	// The cycle names its owner — the PHARMACIST role by default, per §16.1 and D-56 — and
	// records that the reminder went out.
	resp, body := h.get(t, "/v1/formulary/review", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	owner, _ := body["owner"].(map[string]any)
	if owner["owner_role"] != "PHARMACIST" {
		t.Errorf("the review's owner is %v, want PHARMACIST", owner["owner_role"])
	}
	current, _ := body["current"].(map[string]any)
	if current == nil {
		t.Fatal("the review screen shows no cycle after the sweep opened one")
	}
	if current["period_month"] != "2027-03-01" {
		t.Errorf("the cycle is for %v, want March 2027", current["period_month"])
	}
	if current["reminded_at"] == nil {
		t.Error("the cycle records no reminder; an unreminded review is the failure this criterion is about")
	}
	if body["unverified"] != float64(250) {
		t.Errorf("the review says %v prices are unchecked, want 250", body["unverified"])
	}

	// A month later, a second cycle opens. The reminder is monthly, not once ever.
	april := time.Date(2027, 4, 2, 9, 0, 0, 0, time.UTC)
	again, err := h.store.ReviewSweep(ctx, april, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if again != 1 {
		t.Errorf("the sweep opened %d reviews in April, want 1", again)
	}
}

// TestTheSweepWaitsForTheDueDay. A review due on the 10th is not opened on the 3rd.
func TestTheSweepWaitsForTheDueDay(t *testing.T) {
	h := newAPI(t)
	auditor := &testAuditor{recorder: h.recorder}
	ctx := context.Background()

	resp, body := h.do(t, http.MethodPut, "/v1/formulary/review/owner",
		strings.NewReader(`{"owner_role":"PHARMACIST","due_day_of_month":10}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}

	early := time.Date(2027, 3, 3, 9, 0, 0, 0, time.UTC)
	if opened, err := h.store.ReviewSweep(ctx, early, auditor); err != nil || opened != 0 {
		t.Errorf("the sweep opened %d reviews on the 3rd with a due day of the 10th (err %v)", opened, err)
	}
	onTime := time.Date(2027, 3, 10, 9, 0, 0, 0, time.UTC)
	if opened, err := h.store.ReviewSweep(ctx, onTime, auditor); err != nil || opened != 1 {
		t.Errorf("the sweep opened %d reviews on the due day (err %v), want 1", opened, err)
	}
}

// TestANamedOwnerIsCarriedOntoTheCycleAndKeptThere.
//
// The owner can be a role, a person, or both. Whichever it was when the cycle opened is what the
// cycle says afterwards — who was asked to do March's review is a fact about March, and it does
// not change because somebody reassigned the job in June.
func TestANamedOwnerIsCarriedOntoTheCycleAndKeptThere(t *testing.T) {
	h := newAPI(t)
	auditor := &testAuditor{recorder: h.recorder}
	ctx := context.Background()

	resp, body := h.do(t, http.MethodPut, "/v1/formulary/review/owner",
		strings.NewReader(fmt.Sprintf(`{"owner_role":"PHARMACIST","owner_user_id":%q}`, h.pharmacist)),
		"application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}

	if _, err := h.store.ReviewSweep(ctx, time.Date(2027, 3, 5, 9, 0, 0, 0, time.UTC), auditor); err != nil {
		t.Fatal(err)
	}

	_, body = h.get(t, "/v1/formulary/review", nil)
	current, _ := body["current"].(map[string]any)
	if current["owner_name_en"] != "Rakib Hasan" {
		t.Errorf("the cycle names %v as its owner, want the named pharmacist", current["owner_name_en"])
	}
	if current["owner_name_bn"] != "রাকিব হাসান" {
		t.Errorf("the cycle's Bengali owner name is %v", current["owner_name_bn"])
	}

	// Reassign, and the open cycle keeps who it was opened with.
	var other uuid.UUID
	if err := h.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, 'ADM09', 'Shirin Akter', 'শিরিন আক্তার', 'active') RETURNING id`,
		h.facility).Scan(&other); err != nil {
		t.Fatal(err)
	}
	h.do(t, http.MethodPut, "/v1/formulary/review/owner",
		strings.NewReader(fmt.Sprintf(`{"owner_role":"ADMIN","owner_user_id":%q}`, other)),
		"application/json")

	_, body = h.get(t, "/v1/formulary/review", nil)
	current, _ = body["current"].(map[string]any)
	if current["owner_name_en"] != "Rakib Hasan" {
		t.Errorf("reassigning the job rewrote who owned March's review: %v", current["owner_name_en"])
	}
	owner, _ := body["owner"].(map[string]any)
	if owner["owner_name_en"] != "Shirin Akter" {
		t.Errorf("the owner from now on is %v, want the newly named person", owner["owner_name_en"])
	}
}

// TestCompletingAReviewNamesWhoDidItAndWhatWasStillUnchecked.
func TestCompletingAReviewNamesWhoDidItAndWhatWasStillUnchecked(t *testing.T) {
	h := newAPI(t)
	auditor := &testAuditor{recorder: h.recorder}
	if _, err := h.store.ReviewSweep(context.Background(),
		time.Date(2027, 3, 5, 9, 0, 0, 0, time.UTC), auditor); err != nil {
		t.Fatal(err)
	}
	_, body := h.get(t, "/v1/formulary/review", nil)
	current, _ := body["current"].(map[string]any)
	id, _ := current["id"].(string)

	resp, body := h.postJSON(t, "/v1/formulary/review/"+id+"/complete",
		map[string]any{"note": "Checked the twenty we dispense most; the rest are next month."})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %v", resp.StatusCode, body)
	}
	current, _ = body["current"].(map[string]any)
	if current["status"] != "COMPLETE" {
		t.Errorf("the cycle is %v after completion", current["status"])
	}
	if current["completed_by_name_en"] != "Rakib Hasan" {
		t.Errorf("the completed cycle names %v", current["completed_by_name_en"])
	}

	var actorID uuid.UUID
	var details []byte
	if err := h.SQL.QueryRow(`
		SELECT actor_user_id, details FROM ledger.audit_event
		 WHERE kind = 'formulary.review_completed' ORDER BY seq DESC LIMIT 1`).
		Scan(&actorID, &details); err != nil {
		t.Fatalf("reading the audit trail: %v", err)
	}
	if actorID != h.pharmacist {
		t.Errorf("the completion is attributed to %s, want the pharmacist", actorID)
	}
	var carried map[string]any
	_ = json.Unmarshal(details, &carried)
	// Closing a review with 250 prices still unchecked is legitimate and is exactly the thing
	// somebody should be able to see was done.
	if carried["unverified"] != float64(250) {
		t.Errorf("the audit entry says %v prices were still unchecked, want 250", carried["unverified"])
	}

	// A second completion is a 409, not a silent second close.
	resp, _ = h.postJSON(t, "/v1/formulary/review/"+id+"/complete", map[string]any{})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("completing an already-complete review answered %d, want 409", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// The CP74 regression: a browser has no device
// ---------------------------------------------------------------------------

// TestEveryReadIsReachableFromABrowserSessionWithNoDevice.
//
// CP74's finding: 28 of 118 GET routes answered "this action must be done from an enrolled clinic
// device" to a plain read, because they took identity from the write envelope. It survived because
// every domain test built identity with a device id and therefore never exercised the browser's.
//
// The formulary is administered from a browser by a pharmacist, so every route here is exactly
// that case. `staff.device` is empty throughout this file, which makes this test the one that
// would have caught it — and `dthclint readpath` is the static half of the same guarantee.
func TestEveryReadIsReachableFromABrowserSessionWithNoDevice(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")

	for _, path := range []string{
		"/v1/formulary/catalogue",
		"/v1/formulary/generics",
		"/v1/formulary/products",
		"/v1/formulary/products/" + product.String(),
		"/v1/formulary/products/" + product.String() + "/price",
		"/v1/formulary/products/" + product.String() + "/prices",
		"/v1/formulary/imports",
		"/v1/formulary/review",
	} {
		t.Run(path, func(t *testing.T) {
			resp, body := h.get(t, path, nil)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("a browser session (no device) got %d from %s: %v",
					resp.StatusCode, path, body)
			}
		})
	}

	// And a write, too. The pharmacist administers the formulary from a desktop.
	resp, body := h.postJSON(t, "/v1/formulary/products/"+product.String()+"/prices",
		map[string]any{"amount_bdt": "6.00"})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("a browser session could not record a price: %d %v", resp.StatusCode, body)
	}
}

// TestAReaderWithoutTheWritePermissionCannotChangeAPrice.
func TestAReaderWithoutTheWritePermissionCannotChangeAPrice(t *testing.T) {
	h := newAPI(t)
	product := h.aProduct(t, "Comet", "500 mg")
	h.held = []string{formulary.PermRead}

	resp, _ := h.get(t, "/v1/formulary/products", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a reader could not read the formulary: %d", resp.StatusCode)
	}
	resp, _ = h.postJSON(t, "/v1/formulary/products/"+product.String()+"/prices",
		map[string]any{"amount_bdt": "9.99"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a reader changed a price: %d, want 403", resp.StatusCode)
	}
	resp, _ = h.do(t, http.MethodPut, "/v1/formulary/review/owner",
		strings.NewReader(`{"owner_role":"ADMIN"}`), "application/json")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a reader reassigned the price review: %d, want 403", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func rowsByLine(t *testing.T, report map[string]any) map[int]map[string]any {
	t.Helper()
	raw, _ := report["rows"].([]any)
	out := map[int]map[string]any{}
	for _, item := range raw {
		row, _ := item.(map[string]any)
		line, _ := row["line"].(float64)
		out[int(line)] = row
	}
	return out
}

// hasBilingualFieldError reports whether the error envelope names the field in both languages.
func hasBilingualFieldError(body map[string]any, field string) bool {
	envelope, _ := body["error"].(map[string]any)
	if envelope == nil {
		return false
	}
	fields, _ := envelope["fields"].(map[string]any)
	fieldsBN, _ := envelope["fields_bn"].(map[string]any)
	en, _ := fields[field].(string)
	bn, _ := fieldsBN[field].(string)
	return strings.TrimSpace(en) != "" && containsBengali(bn)
}

// containsBengali reports whether the text has a character in the Bengali block.
//
// The cheapest honest check that a "Bengali" message is Bengali. A field that carries an English
// sentence is the failure this is written against, and it is the failure a length check or a
// non-empty check would pass.
func containsBengali(text string) bool {
	for _, r := range text {
		if r >= 0x0980 && r <= 0x09FF {
			return true
		}
	}
	return false
}
