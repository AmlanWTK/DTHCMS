package formulary_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
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

// The two-letter prescribing autocomplete, against the real 250-row seed (CP76, §10.1).
//
// Four acceptance criteria:
//
//	1. p99 under 50ms including network on the clinic LAN;
//	2. two characters return useful ranked results;
//	3. the physician's own recent prescriptions rank higher;
//	4. a formulary change is reflected within 60 seconds.
//
// Criterion 3 has no source until CP80 builds prescriptions. What can be tested is that the
// ranking *uses* the signal when it is given one, and that is tested in search_test.go with the
// signal injected by hand; what is tested here is that the shipped configuration says the
// ranking is incomplete rather than presenting alphabetical order as personalised.
//
// # Why criterion 4 is tested by changing a price behind the process's back
//
// The handlers call `Cache.Invalidate` after every write, so a test that changes a price through
// the API and then searches would pass in about a microsecond — and would pass equally well if
// the refresh loop had never been written. The clinic's formulary is changed by the pharmacist's
// browser talking to whichever API process the load balancer picked, by the monthly review job
// in the *worker* binary, and by a migration. None of those is this process.
//
// So `TestAChangeMadeOutsideThisProcessReachesTheAutocompleteInTime` writes the price with SQL,
// which no amount of in-process invalidation can see, and polls the HTTP endpoint until the new
// number appears — with the **production** refresh interval, not a shortened one.

// ---------------------------------------------------------------------------
// The harness
// ---------------------------------------------------------------------------

type searchAPI struct {
	*testsupport.DB
	store    *formulary.Store
	cache    *formulary.Cache
	server   *httptest.Server
	facility uuid.UUID
	doctor   uuid.UUID
	held     []string
}

func newSearchAPI(t *testing.T, refresh time.Duration) *searchAPI {
	t.Helper()
	base := testsupport.Postgres(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, base.DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	h := &searchAPI{
		DB: base, store: formulary.NewStore(pool),
		held: []string{formulary.PermRead, formulary.PermWrite, formulary.PermReview},
	}
	if err := base.SQL.QueryRow(`SELECT core.default_facility()`).Scan(&h.facility); err != nil {
		t.Fatal(err)
	}
	if err := base.SQL.QueryRow(`
		INSERT INTO core.app_user (facility_id, employee_code, name_en, name_bn, status)
		VALUES ($1, 'DOC77', 'Nahid', 'নাহিদ', 'active') RETURNING id`,
		h.facility).Scan(&h.doctor); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.cache = formulary.NewCache(formulary.CacheConfig{
		Store: h.store, Usage: formulary.NoUsage{}, Clock: clock.Real{},
		Refresh: refresh, Logger: logger,
	})
	go h.cache.Run(ctx)

	handlers := formulary.NewHandlers(formulary.HandlersConfig{
		Store: h.store, Cache: h.cache,
		Audit: &testAuditor{recorder: audit.NewRecorder(audit.NewPostgresStore(pool), clock.Real{}, logger)},
		Clock: clock.Real{}, Logger: logger,
	})
	who := staff{facility: h.facility, user: h.doctor, code: "DOC77", permissions: &h.held}
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

// searchResult is the wire shape, decoded. Declared here rather than reusing the Go type so that
// a renamed JSON tag fails this suite: the combobox reads these names, not the Go fields.
type searchResult struct {
	Query           string `json:"query"`
	Normalised      string `json:"normalised_query"`
	Total           int    `json:"total"`
	RankingComplete bool   `json:"ranking_complete"`
	ServedFrom      string `json:"served_from"`
	CacheAge        int    `json:"cache_age_seconds"`
	Entries         []struct {
		TradeName   string `json:"trade_name"`
		GenericName string `json:"generic_name"`
		FormCode    string `json:"form_code"`
		FormNameEN  string `json:"form_name_en"`
		FormNameBN  string `json:"form_name_bn"`
		ClassNameBN string `json:"class_name_bn"`
		Match       string `json:"match"`
		Times       int    `json:"times_prescribed"`
		DaysSince   *int   `json:"days_since_last"`
		Strengths   []struct {
			ProductID string `json:"product_id"`
			Strength  string `json:"strength"`
			UnitBN    string `json:"unit_name_bn"`
			Price     *struct {
				Poisha       int64  `json:"amount_poisha"`
				BDT          string `json:"amount_bdt"`
				Verification string `json:"verification"`
			} `json:"price"`
		} `json:"strengths"`
	} `json:"entries"`
}

func (h *searchAPI) search(t *testing.T, q string, limit int) (int, searchResult) {
	t.Helper()
	query := url.Values{"q": {q}}
	if limit > 0 {
		query.Set("limit", fmt.Sprint(limit))
	}
	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/v1/formulary/search?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out searchResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil && resp.StatusCode == http.StatusOK {
		t.Fatalf("decoding the search response: %v", err)
	}
	return resp.StatusCode, out
}

// rankOf returns the 1-based position of a trade name in a result, or 0.
func rankOf(res searchResult, trade string) int {
	for i, e := range res.Entries {
		if e.TradeName == trade {
			return i + 1
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Criterion 2: two characters return useful ranked results
// ---------------------------------------------------------------------------

// prescribed is the list the checkpoint's manual verification is about: "Dr. Nahid types two
// letters for his twenty most-prescribed drugs". Twenty-two brands from the real seed, covering
// what an endocrinology clinic in Faridpur actually writes — metformin, a sulphonylurea, the
// gliptins, the flozins, the GLP-1s, four insulins, levothyroxine and carbimazole, the statins,
// the antihypertensives, and pregabalin for diabetic neuropathy.
//
// **The assertions and the table are different things, deliberately.** The table of where each
// brand lands on two letters is printed for Dr. Nahid to read and argue with; it is a fact about
// this formulary, not a threshold somebody can tune. What is asserted is the two things that
// would be defects at any ranking: on two letters the brand must be found at all, and on three
// letters it must be in the top three.
var prescribed = []struct {
	trade string
	two   string
	three string
	note  string
}{
	{"Comet", "co", "com", "metformin, the first line"},
	{"Bigmet", "bi", "big", "metformin, Renata's"},
	{"Secrin", "se", "sec", "glimepiride"},
	{"Comprid", "co", "comp", "gliclazide"},
	{"Sitagil", "si", "sit", "sitagliptin"},
	{"Linatab", "li", "lin", "linagliptin"},
	{"Viglita", "vi", "vig", "vildagliptin"},
	{"Emjard", "em", "emj", "empagliflozin"},
	{"Dapaglip", "da", "dap", "dapagliflozin"},
	{"Ozempic", "oz", "oze", "semaglutide, the original"},
	{"Fitaro", "fi", "fit", "semaglutide, Incepta's"},
	{"Trulicity", "tr", "tru", "dulaglutide"},
	{"Ansulin", "an", "ans", "premixed human insulin, the workhorse"},
	{"Mixtard 30", "mi", "mix", "premixed human insulin"},
	{"Glarine", "gl", "gla", "insulin glargine"},
	{"Rapilog", "ra", "rap", "insulin aspart"},
	{"Thyrox", "th", "thy", "levothyroxine"},
	{"Thyrin", "th", "thyr", "levothyroxine, Square's"},
	{"Carbizol", "ca", "car", "carbimazole"},
	{"Rosuva", "ro", "ros", "rosuvastatin"},
	{"Anzitor", "an", "anz", "atorvastatin"},
	{"Telmilok", "te", "tel", "telmisartan"},
	{"Angilock", "an", "ang", "losartan"},
	{"Neurolin", "ne", "neu", "pregabalin, diabetic neuropathy"},
}

func TestTwoLettersFindTheDrugsThisClinicActuallyPrescribes(t *testing.T) {
	h := newSearchAPI(t, formulary.DefaultRefresh)

	t.Log("Two-letter ranking over the real 250-row seed. " +
		"`2-letter rank` is the position among brands; `of` is how many brands matched.")
	t.Log("brand           | typed | rank | of  | match kind      | 3 letters | rank | of")
	t.Log("----------------|-------|------|-----|-----------------|-----------|------|----")

	for _, p := range prescribed {
		_, two := h.search(t, p.two, formulary.MaxSearchLimit)
		_, three := h.search(t, p.three, formulary.MaxSearchLimit)
		twoRank, threeRank := rankOf(two, p.trade), rankOf(three, p.trade)

		kind := "—"
		if twoRank > 0 {
			kind = two.Entries[twoRank-1].Match
		}
		t.Logf("%-15s | %-5s | %4s | %3d | %-15s | %-9s | %4d | %3d",
			p.trade, p.two, rankLabel(twoRank, two.Total), two.Total, kind,
			p.three, threeRank, three.Total)

		// The two assertions. Both are about the search being usable rather than about a
		// particular order, and both fail loudly if the matcher regresses.
		if twoRank == 0 {
			t.Errorf("%s (%s): two letters %q did not find it at all among %d matches",
				p.trade, p.note, p.two, two.Total)
		}
		if threeRank == 0 || threeRank > 3 {
			t.Errorf("%s (%s): three letters %q put it at rank %d of %d; the physician should "+
				"not have to look past the third row after three keystrokes",
				p.trade, p.note, p.three, threeRank, three.Total)
		}
	}
}

func rankLabel(rank, total int) string {
	if rank == 0 {
		if total > formulary.MaxSearchLimit {
			return ">25"
		}
		return "—"
	}
	return fmt.Sprint(rank)
}

func TestTwoLettersReturnTheFactsAPhysicianNeedsToChoose(t *testing.T) {
	// "Two letters surface matching trade names **with generic, strength, form and price**".
	// A ranked list of brand names alone would satisfy criterion 2 read carelessly and would
	// send the physician to another screen to find out what he is prescribing.
	h := newSearchAPI(t, formulary.DefaultRefresh)
	_, res := h.search(t, "co", 25)
	comet := rankOf(res, "Comet")
	if comet == 0 {
		t.Fatal(`"co" did not find Comet`)
	}
	e := res.Entries[comet-1]
	switch {
	case e.GenericName != "Metformin hydrochloride":
		t.Errorf("the generic is %q", e.GenericName)
	case e.FormNameEN == "" || e.FormNameBN == "":
		t.Errorf("the form is not in both languages: %q / %q", e.FormNameEN, e.FormNameBN)
	case e.ClassNameBN == "":
		t.Error("the therapeutic class has no Bengali name")
	case len(e.Strengths) < 2:
		t.Errorf("Comet is stocked at 500 mg and 850 mg; the entry carries %d strengths", len(e.Strengths))
	}
	for _, s := range e.Strengths {
		if s.Strength == "" {
			t.Error("a strength came back empty")
		}
		if s.UnitBN == "" {
			t.Error("the dispensing unit has no Bengali name")
		}
		if s.Price == nil {
			t.Errorf("%s has no price; all 250 seeded products are priced", s.Strength)
			continue
		}
		if s.Price.Verification != "PROVISIONAL" {
			t.Errorf("%s is %s; every seeded price is provisional until somebody checks it",
				s.Strength, s.Price.Verification)
		}
		if s.Price.BDT == "" {
			t.Errorf("%s has an amount in poisha and no text to draw", s.Strength)
		}
	}
	if res.RankingComplete {
		t.Error("the ranking claims to be complete while CP80 does not exist")
	}
}

func TestABengaliQueryFindsTheLatinBrandThroughTheAPI(t *testing.T) {
	h := newSearchAPI(t, formulary.DefaultRefresh)
	for _, tc := range []struct{ typed, want string }{
		{"কম", "Comet"},
		{"থাই", "Thyrox"},
		{"সেক", "Secrin"},
	} {
		_, res := h.search(t, tc.typed, 10)
		if rankOf(res, tc.want) == 0 {
			t.Errorf("%q (normalised %q) returned %d brands, none of them %s",
				tc.typed, res.Normalised, res.Total, tc.want)
		}
		if res.Normalised == tc.typed {
			t.Errorf("%q was not transliterated; normalised_query is still %q", tc.typed, res.Normalised)
		}
	}
}

// ---------------------------------------------------------------------------
// Criterion 1: latency
// ---------------------------------------------------------------------------

func TestTheAutocompleteAnswersWellInsideTheBudget(t *testing.T) {
	// **Measured, not asserted from a constant.** 600 real HTTP round trips against the real
	// 250-row seed, through the real router with its middleware, and the p99 of what comes
	// back is compared against the checkpoint's 50ms.
	//
	// What this does not include is the clinic's LAN. The server is on loopback, so a hop of
	// roughly half a millisecond each way is missing from every figure below. That is stated
	// rather than padded, because a padded number is one nobody can compare against a later
	// measurement on real hardware.
	if testing.Short() {
		t.Skip("600 round trips")
	}
	h := newSearchAPI(t, formulary.DefaultRefresh)

	queries := []string{"co", "me", "th", "an", "si", "gl", "in", "em", "ra", "se", "কম", "metfromin"}
	// One pass to fill the cache, so the measurement is of a warm process — which is what a
	// clinic morning is. The cold first request is measured separately below.
	for _, q := range queries {
		h.search(t, q, 10)
	}

	samples := make([]time.Duration, 0, 600)
	for i := 0; i < 600; i++ {
		q := queries[i%len(queries)]
		start := time.Now()
		status, res := h.search(t, q, 10)
		elapsed := time.Since(start)
		if status != http.StatusOK {
			t.Fatalf("%q answered %d", q, status)
		}
		if res.ServedFrom != "cache" {
			t.Fatalf("%q was served from %q", q, res.ServedFrom)
		}
		samples = append(samples, elapsed)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	p := func(q float64) time.Duration { return samples[int(float64(len(samples)-1)*q)] }
	t.Logf("n=%d  p50=%v  p90=%v  p99=%v  max=%v  (loopback; a clinic LAN adds ~0.5ms each way)",
		len(samples), p(0.50), p(0.90), p(0.99), samples[len(samples)-1])

	if p99 := p(0.99); p99 > 50*time.Millisecond {
		t.Errorf("p99 is %v, and §10.1 asks for under 50ms including network", p99)
	}
}

func TestTheFirstSearchAfterAColdStartIsAlsoAnAnswer(t *testing.T) {
	// The cache is built lazily, so the very first search of a facility pays for loading 250
	// rows. Worth measuring: if that were seconds, the first physician of the morning would
	// meet a hang, and it is exactly the case a warm benchmark never sees.
	h := newSearchAPI(t, formulary.DefaultRefresh)
	start := time.Now()
	status, res := h.search(t, "co", 10)
	cold := time.Since(start)
	if status != http.StatusOK {
		t.Fatalf("the first search answered %d", status)
	}
	if res.Total == 0 {
		t.Fatal("the first search found nothing")
	}
	t.Logf("cold first search: %v (loads all 250 products and indexes them)", cold)
	if cold > 500*time.Millisecond {
		t.Errorf("the first search took %v; a physician would feel that as a hang", cold)
	}
}

// ---------------------------------------------------------------------------
// Criterion 4: a formulary change is reflected within 60 seconds
// ---------------------------------------------------------------------------

func TestAChangeMadeOutsideThisProcessReachesTheAutocompleteInTime(t *testing.T) {
	// The real criterion. The price is changed with SQL — the way the worker binary, a second
	// API process and a migration all change it as far as this process is concerned — and the
	// **production** refresh interval is used. Nothing calls Invalidate.
	if testing.Short() {
		t.Skip("waits for a real refresh interval")
	}
	h := newSearchAPI(t, formulary.DefaultRefresh)

	_, before := h.search(t, "co", 25)
	rank := rankOf(before, "Comet")
	if rank == 0 {
		t.Fatal(`"co" did not find Comet`)
	}
	product := before.Entries[rank-1].Strengths[0].ProductID
	was := before.Entries[rank-1].Strengths[0].Price.Poisha
	want := was + 137

	// Exactly what the store does: close the current row today, open the successor today. Done
	// in SQL so that no Go code in this process can know it happened.
	//
	// **Effective today, not tomorrow.** The cache serves the price *in force on the clinic's
	// day*, which is what a physician quoting a cost needs; a price recorded to start next
	// Monday is not today's price and must not appear as one. Dating this tomorrow would be
	// testing the opposite of what the module promises.
	if _, err := h.SQL.Exec(`
		UPDATE core.medication_price SET effective_to = CURRENT_DATE
		 WHERE product_id = $1 AND effective_to IS NULL`, product); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.medication_price
		  (facility_id, product_id, unit_price_poisha, effective_from, verification, origin,
		   recorded_by)
		VALUES ($1, $2, $3, CURRENT_DATE, 'VERIFIED', 'REVIEW', $4)`,
		h.facility, product, want, h.doctor); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(60 * time.Second)
	start := time.Now()
	for {
		_, now := h.search(t, "co", 25)
		if r := rankOf(now, "Comet"); r > 0 {
			for _, s := range now.Entries[r-1].Strengths {
				if s.ProductID == product && s.Price != nil && s.Price.Poisha == want {
					t.Logf("the change appeared after %v, with the production refresh "+
						"interval of %v and no in-process invalidation",
						time.Since(start).Round(100*time.Millisecond), formulary.DefaultRefresh)
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("60 seconds after a price changed in the database the autocomplete is "+
				"still serving %d poisha for product %s", was, product)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestAWithdrawalMadeOutsideThisProcessTakesTheMedicineOffTheList(t *testing.T) {
	// The other half of criterion 4, and the half with a clinical edge on it: a brand the
	// clinic has stopped stocking must stop being offered. Same mechanism, same interval, and
	// again nothing in this process is told.
	if testing.Short() {
		t.Skip("waits for a real refresh interval")
	}
	h := newSearchAPI(t, formulary.DefaultRefresh)
	if _, res := h.search(t, "neu", 25); rankOf(res, "Neurolin") == 0 {
		t.Fatal(`"neu" did not find Neurolin to begin with`)
	}
	if _, err := h.SQL.Exec(`
		UPDATE core.medication_product
		   SET is_active = false, withdrawn_at = now(), withdrawn_reason = 'test',
		       withdrawn_by = $2
		 WHERE facility_id = $1 AND trade_name = 'Neurolin'`, h.facility, h.doctor); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, res := h.search(t, "neu", 25); rankOf(res, "Neurolin") == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a withdrawn medicine is still being offered 60 seconds later")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestAPriceChangedThroughTheAPIIsReflectedAtOnce(t *testing.T) {
	// The optimisation, tested for what it is: a price recorded by *this* process should not
	// wait for the next tick. It is not what criterion 4 rests on — see the file note.
	h := newSearchAPI(t, formulary.DefaultRefresh)
	_, before := h.search(t, "co", 25)
	rank := rankOf(before, "Comet")
	if rank == 0 {
		t.Fatal(`"co" did not find Comet`)
	}
	product := before.Entries[rank-1].Strengths[0].ProductID

	body := strings.NewReader(`{"amount_bdt":"9.99","effective_from":"` +
		time.Now().UTC().Format("2006-01-02") + `","verified":true,"source_note":"test"}`)
	req, err := http.NewRequest(http.MethodPost,
		h.server.URL+"/v1/formulary/products/"+product+"/prices", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-Requested-With", "DTHCMS")
	req.Header.Set("Idempotency-Key", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recording the price answered %d: %s", resp.StatusCode, raw)
	}

	_, after := h.search(t, "co", 25)
	r := rankOf(after, "Comet")
	for _, s := range after.Entries[r-1].Strengths {
		if s.ProductID == product {
			if s.Price == nil || s.Price.BDT != "9.99" {
				t.Errorf("the next search still shows %+v", s.Price)
			}
			return
		}
	}
	t.Error("the product vanished from the results after its price was recorded")
}

func TestAFutureDatedPriceIsNotShownAsTodaysPrice(t *testing.T) {
	// The monthly review records a price to take effect from the first of next month, which is
	// its ordinary use. A cache that joined "the price whose period is still open" would put
	// that number on the prescribing screen today — invisible until somebody uses the feature
	// the price history exists for, and then wrong on the screen a cost is quoted from.
	h := newSearchAPI(t, formulary.DefaultRefresh)
	_, before := h.search(t, "co", 25)
	rank := rankOf(before, "Comet")
	if rank == 0 {
		t.Fatal(`"co" did not find Comet`)
	}
	product := before.Entries[rank-1].Strengths[0].ProductID
	was := before.Entries[rank-1].Strengths[0].Price.Poisha

	if _, err := h.SQL.Exec(`
		UPDATE core.medication_price SET effective_to = CURRENT_DATE + 30
		 WHERE product_id = $1 AND effective_to IS NULL`, product); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SQL.Exec(`
		INSERT INTO core.medication_price
		  (facility_id, product_id, unit_price_poisha, effective_from, verification, origin,
		   recorded_by)
		VALUES ($1, $2, $3, CURRENT_DATE + 30, 'VERIFIED', 'REVIEW', $4)`,
		h.facility, product, was*3, h.doctor); err != nil {
		t.Fatal(err)
	}
	h.cache.Invalidate(h.facility)

	_, after := h.search(t, "co", 25)
	r := rankOf(after, "Comet")
	for _, s := range after.Entries[r-1].Strengths {
		if s.ProductID != product {
			continue
		}
		if s.Price == nil || s.Price.Poisha != was {
			t.Fatalf("the autocomplete shows %+v; today's price is still %d poisha and next "+
				"month's is not today's", s.Price, was)
		}
		return
	}
	t.Error("the product vanished from the results")
}

func TestTheSearchIsReachableFromABrowserSessionWithNoDevice(t *testing.T) {
	// The CP74 defect, which is what `dthclint readpath` exists to keep out. The physician
	// prescribing sits at a browser, and a browser has no enrolled device by design.
	h := newSearchAPI(t, formulary.DefaultRefresh)
	status, res := h.search(t, "co", 10)
	if status != http.StatusOK {
		t.Fatalf("a browser session with no device got %d from the autocomplete", status)
	}
	if res.Total == 0 {
		t.Error("the search answered 200 with nothing in it")
	}
}
