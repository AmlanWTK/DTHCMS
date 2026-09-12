package formulary

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The formulary over HTTP (CP75).
//
// # Identity, and the door that is not used here
//
// Every handler in this file — read and write — takes its identity from `httpx.PrincipalFrom`,
// never from `eventstore.ActorFrom`. Two reasons, and either alone would be enough:
//
//  1. `architecture.json` allows `formulary` to import `platform` and nothing else. It cannot
//     reach the event store, and that is the right boundary: a formulary is reference data in
//     `core`, not a clinical fact in the ledger.
//  2. `ActorFrom` is the **write envelope** and refuses a session with no enrolled device —
//     which is every browser. The formulary is administered from a browser by a pharmacist, so a
//     handler that reached it would answer "this action must be done from an enrolled clinic
//     device" to somebody sitting at the clinic's own desktop. That is the CP74 defect, and
//     `dthclint readpath` exists to keep it from coming back.
//
// # Why a price change is a POST and not a PUT
//
// PUT says "make it be this". A new price does not replace the old one — it supersedes it from a
// date, and the old one stays and stays true. POST to a collection of prices is what that is:
// appending a row to a history, not setting a field.
//
// # The as-of route
//
// `GET /v1/formulary/products/{id}/price?on=2024-03-04` is criterion 1 with a URL on it, and it
// is a route of its own rather than a query parameter on the product read because CP127 will
// call it for thousands of prescriptions and should not be fetching a product's whole record to
// do it.

// Handlers serve the formulary.
type Handlers struct {
	store  *Store
	cache  *Cache
	audit  Auditor
	clock  interface{ Now() time.Time }
	logger *slog.Logger
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Store *Store
	// Cache is CP76's in-process formulary. Nil in the contract test, which assembles the
	// route table and nothing below it; the search handler answers 503 without one rather
	// than panicking, because a route that exists and cannot work should say so.
	Cache  *Cache
	Audit  Auditor
	Clock  interface{ Now() time.Time }
	Logger *slog.Logger
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{
		store: cfg.Store, cache: cfg.Cache, audit: cfg.Audit,
		clock: cfg.Clock, logger: cfg.Logger,
	}
}

// Mount attaches /v1/formulary.
func (h *Handlers) Mount(r chi.Router) {
	// Reading is wide — a physician prescribing, a pharmacist dispensing, the education
	// officer explaining a cost. Writing is the pharmacist, the physician and the
	// administrator, per §16.1.
	read := httpx.Permission(PermRead, PermWrite, PermReview)
	write := httpx.Permission(PermWrite)
	review := httpx.Permission(PermReview)

	r.Route("/formulary", func(f chi.Router) {
		f.Method("GET", "/catalogue", httpx.Declare(read, h.catalogue))
		f.Method("GET", "/generics", httpx.Declare(read, h.generics))

		// CP76. Its own route rather than a mode of /products, because it answers a
		// different question with a different shape and a different ranking, and one
		// endpoint that did both would be one cache invalidation away from the admin list
		// and the prescribing list disagreeing about what the clinic stocks.
		f.Method("GET", "/search", httpx.Declare(read, h.search))

		f.Method("GET", "/products", httpx.Declare(read, h.products))
		f.Method("POST", "/products", httpx.Declare(write, h.addProduct))
		f.Method("GET", "/products/{id}", httpx.Declare(read, h.product))
		f.Method("PUT", "/products/{id}", httpx.Declare(write, h.editProduct))
		f.Method("POST", "/products/{id}/withdraw", httpx.Declare(write, h.withdraw))
		f.Method("POST", "/products/{id}/reinstate", httpx.Declare(write, h.reinstate))

		// Criterion 1, and its history.
		f.Method("GET", "/products/{id}/price", httpx.Declare(read, h.priceOn))
		f.Method("GET", "/products/{id}/prices", httpx.Declare(read, h.priceHistory))
		f.Method("POST", "/products/{id}/prices", httpx.Declare(write, h.recordPrice))

		// Criterion 2.
		f.Method("GET", "/imports", httpx.Declare(read, h.imports))
		f.Method("POST", "/imports", httpx.Declare(write, h.runImport))
		f.Method("GET", "/imports/{id}", httpx.Declare(read, h.importReport))

		// Criterion 4.
		f.Method("GET", "/review", httpx.Declare(read, h.review))
		f.Method("PUT", "/review/owner", httpx.Declare(review, h.setOwner))
		f.Method("POST", "/review/{id}/complete", httpx.Declare(review, h.completeReview))
	})
}

// caller is the identity every handler here works from. Never eventstore.ActorFrom; see the note.
type caller struct {
	userID     uuid.UUID
	facilityID uuid.UUID
	code       string
	role       string
}

func (h *Handlers) caller(w http.ResponseWriter, r *http.Request) (caller, bool) {
	principal, present := httpx.PrincipalFrom(r.Context())
	if !present {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return caller{}, false
	}
	user, err := uuid.Parse(principal.UserID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return caller{}, false
	}
	facility, err := uuid.Parse(principal.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return caller{}, false
	}
	return caller{userID: user, facilityID: facility, code: principal.Code, role: principal.Role}, true
}

// invalidate drops the autocomplete's snapshot of this facility so the next search rebuilds it.
//
// A latency optimisation and not the mechanism behind criterion 4 — the refresh loop in
// cache.go is, and it is the one the test exercises, because this one only works for a change
// made by *this* process. See the note at the top of cache.go.
func (h *Handlers) invalidate(facility uuid.UUID) {
	if h.cache != nil {
		h.cache.Invalidate(facility)
	}
}

func (h *Handlers) now() time.Time {
	if h.clock == nil {
		return time.Now().UTC()
	}
	return h.clock.Now().UTC()
}

// today is the clinic's calendar day. UTC-truncated, matching the migration's note: a price is a
// daily fact, and the six hours between Dhaka and UTC are not a thing anybody prices against.
func (h *Handlers) today() time.Time { return h.now().Truncate(24 * time.Hour) }

func (h *Handlers) catalogue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.caller(w, r); !ok {
		return
	}
	catalogue, err := h.store.Catalogue(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, catalogue)
}

func (h *Handlers) generics(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	generics, err := h.store.Generics(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"generics": generics})
}

// search is the two-letter prescribing autocomplete (CP76, §10.1).
//
// # Why there is no 422 here
//
// Every other list endpoint in this module refuses a malformed `limit`. This one clamps it. The
// difference is who is typing: a combobox sends this on every keystroke, and a physician mid-word
// whose autocomplete returned a validation error instead of results would have no idea what he
// did. A limit of 0, of -4 or of 900 is answered with [DefaultSearchLimit] or [MaxSearchLimit],
// and an empty query is answered with an empty list rather than with the whole formulary.
//
// # What is not logged
//
// The query string. See the note at the top of cache.go: in a clinic whose formulary is mostly
// diabetes and thyroid drugs, what a physician is typing says what he is treating.
func (h *Handlers) search(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	if h.cache == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable.WithDetail(
			errors.New("the formulary cache is not configured")))
		return
	}

	query := r.URL.Query()
	limit := 0
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		// Ignored rather than refused when it is nonsense; see the note above.
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}

	// The prescriber is the caller. Criterion 3 is about *this physician*'s own recent
	// prescriptions, so the identity that reaches the ranking is the one holding the keyboard
	// — not the facility, which would make every doctor in the building share one ordering.
	result, err := h.cache.Search(r.Context(), who.facilityID, who.userID,
		query.Get("q"), limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handlers) products(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	search := Search{
		Query:          strings.TrimSpace(query.Get("q")),
		Class:          strings.TrimSpace(query.Get("class")),
		ActiveOnly:     query.Get("active") == "true",
		UnverifiedOnly: query.Get("unverified") == "true",
	}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("limit",
				"Ask for a whole number of rows.", "কতগুলো সারি চান, পূর্ণসংখ্যায় লিখুন।"))
			return
		}
		search.Limit = parsed
	}
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("offset",
				"Start from a whole number of rows.", "কত নম্বর সারি থেকে শুরু করবেন, পূর্ণসংখ্যায় লিখুন।"))
			return
		}
		search.Offset = parsed
	}

	page, err := h.store.Products(r.Context(), who.facilityID, search)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handlers) product(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.productID(w, r)
	if !ok {
		return
	}
	on, ok := h.onDate(w, r)
	if !ok {
		return
	}
	product, err := h.store.Product(r.Context(), who.facilityID, id, on)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": product, "on": day(on)})
}

func (h *Handlers) priceOn(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.productID(w, r)
	if !ok {
		return
	}
	on, ok := h.onDate(w, r)
	if !ok {
		return
	}
	price, err := h.store.PriceOn(r.Context(), who.facilityID, id, on)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	// The day is echoed back beside the price. A client that asked for a date and got a price
	// with no date on it is one keystroke away from filing the answer against the wrong day.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"price": price, "on": day(on)})
}

func (h *Handlers) priceHistory(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.productID(w, r)
	if !ok {
		return
	}
	// The product is read first so that a history request for something this facility does not
	// have is a 404 rather than an empty list — "no prices" and "no such medicine" are
	// different answers and a screen should not have to guess which it got.
	if _, err := h.store.Product(r.Context(), who.facilityID, id, h.today()); err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	prices, err := h.store.PriceHistory(r.Context(), who.facilityID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"prices": prices})
}

type recordPriceRequest struct {
	// AmountBDT as text, not a number. A JSON number is a float in every client that parses
	// it, and 0.34 is not representable — which is the whole reason this module carries poisha.
	AmountBDT     string `json:"amount_bdt"`
	EffectiveFrom string `json:"effective_from,omitempty"`
	Verified      bool   `json:"verified,omitempty"`
	SourceNote    string `json:"source_note,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
}

func (h *Handlers) recordPrice(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.productID(w, r)
	if !ok {
		return
	}
	var body recordPriceRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	amount, err := ParseMoney(body.AmountBDT)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.moneyError(err, body.AmountBDT))
		return
	}
	from := h.today()
	if raw := strings.TrimSpace(body.EffectiveFrom); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("effective_from",
				"Write the date as 2026-03-04.", "তারিখটি 2026-03-04 এভাবে লিখুন।"))
			return
		}
		from = parsed.UTC()
	}

	origin := OriginManual
	if body.Verified {
		origin = OriginReview
	}
	change, err := h.store.RecordPrice(r.Context(), Recording{
		FacilityID: who.facilityID, ProductID: id, Amount: amount, From: from,
		Verified: body.Verified, Origin: origin,
		SourceNote: body.SourceNote, SourceURL: body.SourceURL,
		ActorID: who.userID,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}

	// Audited after the write, never before: an audit entry for a price change that then
	// failed is a trail saying something happened which did not.
	change.ActorCode, change.ActorRole = who.code, who.role
	h.record(r, func() error { return h.audit.PriceChanged(r.Context(), change) },
		"recording a formulary price change")

	price, err := h.store.PriceOn(r.Context(), who.facilityID, id, from)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	h.invalidate(who.facilityID)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"price": price})
}

type productRequest struct {
	Generic          string `json:"generic"`
	TradeName        string `json:"trade_name"`
	Strength         string `json:"strength"`
	Form             string `json:"form_code"`
	Manufacturer     string `json:"manufacturer"`
	DispenseUnit     string `json:"dispense_unit"`
	DGDARegistration string `json:"dgda_registration,omitempty"`
	Notes            string `json:"notes,omitempty"`
	SourceURL        string `json:"source_url,omitempty"`
}

func (h *Handlers) addProduct(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body productRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	for field, value := range map[string]string{
		"generic": body.Generic, "trade_name": body.TradeName, "strength": body.Strength,
		"form_code": body.Form, "manufacturer": body.Manufacturer,
		"dispense_unit": body.DispenseUnit,
	} {
		if strings.TrimSpace(value) == "" {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn(field,
				"This is required.", "এটি দিতেই হবে।"))
			return
		}
	}

	id, err := h.store.AddProduct(r.Context(), NewProduct{
		FacilityID: who.facilityID, GenericName: body.Generic, TradeName: body.TradeName,
		Strength: body.Strength, FormCode: body.Form, Manufacturer: body.Manufacturer,
		DispenseUnit: body.DispenseUnit, DGDARegistration: body.DGDARegistration,
		Notes: body.Notes, SourceURL: body.SourceURL, ActorID: who.userID,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	product, err := h.store.Product(r.Context(), who.facilityID, id, h.today())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	h.invalidate(who.facilityID)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"product": product})
}

func (h *Handlers) editProduct(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.productID(w, r)
	if !ok {
		return
	}
	var body productRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if strings.TrimSpace(body.Generic) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("generic",
			"Name the generic this product is.", "এটি কোন জেনেরিকের ওষুধ, তা জানান।"))
		return
	}
	if err := h.store.EditProduct(r.Context(), ProductEdit{
		FacilityID: who.facilityID, ProductID: id, GenericName: body.Generic,
		DGDARegistration: body.DGDARegistration, Notes: body.Notes, SourceURL: body.SourceURL,
		ActorID: who.userID,
	}); err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	product, err := h.store.Product(r.Context(), who.facilityID, id, h.today())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	h.invalidate(who.facilityID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": product})
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

func (h *Handlers) withdraw(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.productID(w, r)
	if !ok {
		return
	}
	var body reasonRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"Say why this product is being withdrawn.",
			"এই ওষুধটি কেন বাদ দেওয়া হচ্ছে, তা লিখুন।"))
		return
	}
	if err := h.store.Withdraw(r.Context(), who.facilityID, id, body.Reason, who.userID); err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	product, err := h.store.Product(r.Context(), who.facilityID, id, h.today())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	h.invalidate(who.facilityID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": product})
}

func (h *Handlers) reinstate(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.productID(w, r)
	if !ok {
		return
	}
	if err := h.store.Reinstate(r.Context(), who.facilityID, id, who.userID); err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	product, err := h.store.Product(r.Context(), who.facilityID, id, h.today())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	h.invalidate(who.facilityID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": product})
}

// MaxImportBytes bounds an upload. The seed workbook is 250 rows and about 40 KB; a megabyte is
// four thousand rows, which is more than a curated formulary will ever be and small enough that
// a person who uploaded the wrong file finds out immediately.
const MaxImportBytes = 1 << 20

func (h *Handlers) runImport(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	mode := ModeDryRun
	if query.Get("mode") == "apply" {
		mode = ModeApply
	}
	filename := strings.TrimSpace(query.Get("filename"))
	if filename == "" {
		filename = "price-list.csv"
	}
	from := h.today()
	if raw := strings.TrimSpace(query.Get("effective_from")); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("effective_from",
				"Write the date as 2026-03-04.", "তারিখটি 2026-03-04 এভাবে লিখুন।"))
			return
		}
		from = parsed.UTC()
	}

	result, err := h.store.Import(r.Context(), ImportRequest{
		FacilityID: who.facilityID, Filename: filename, Mode: mode,
		Verified: query.Get("verified") == "true", EffectiveFrom: from,
		ActorID: who.userID, Body: http.MaxBytesReader(w, r.Body, MaxImportBytes),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	// 200 rather than 201 even on apply. What was created is a report, and the report is the
	// thing the caller asked for; a 201 with a Location pointing at the import record would be
	// technically defensible and would tell a client the wrong thing about what to do next.
	h.invalidate(who.facilityID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"import": result})
}

func (h *Handlers) imports(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("limit",
				"Ask for a whole number of rows.", "কতগুলো সারি চান, পূর্ণসংখ্যায় লিখুন।"))
			return
		}
		limit = parsed
	}
	list, err := h.store.Imports(r.Context(), who.facilityID, limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"imports": list})
}

func (h *Handlers) importReport(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	report, err := h.store.ImportReport(r.Context(), who.facilityID, id,
		r.URL.Query().Get("rejected") == "true")
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"import": report})
}

func (h *Handlers) review(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	state, err := h.store.ReviewState(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, state)
}

type ownerRequest struct {
	Role   string `json:"owner_role"`
	UserID string `json:"owner_user_id,omitempty"`
	DueDay int    `json:"due_day_of_month"`
}

func (h *Handlers) setOwner(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body ownerRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if strings.TrimSpace(body.Role) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("owner_role",
			"Name the role that owns the price review.",
			"দামের পর্যালোচনা কোন পদের দায়িত্ব, তা জানান।"))
		return
	}
	var user *uuid.UUID
	if raw := strings.TrimSpace(body.UserID); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("owner_user_id",
				"That is not a person's identifier.", "এটি কোনও ব্যক্তির পরিচিতি নয়।"))
			return
		}
		user = &parsed
	}
	if body.DueDay == 0 {
		body.DueDay = 1
	}
	if err := h.store.SetOwner(r.Context(), who.facilityID, body.Role, user,
		body.DueDay, who.userID); err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	state, err := h.store.ReviewState(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, state)
}

type completeRequest struct {
	Note string `json:"note,omitempty"`
}

func (h *Handlers) completeReview(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	var body completeRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	done, err := h.store.CompleteReview(r.Context(), who.facilityID, id, body.Note, who.userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	done.ActorCode, done.ActorRole = who.code, who.role
	h.record(r, func() error { return h.audit.ReviewCompleted(r.Context(), done) },
		"recording a completed price review")

	state, err := h.store.ReviewState(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, state)
}

// --- the small shared pieces ---

func (h *Handlers) productID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// onDate reads `?on=2024-03-04`, defaulting to today.
func (h *Handlers) onDate(w http.ResponseWriter, r *http.Request) (time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("on"))
	if raw == "" {
		return h.today(), true
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("on",
			"Write the date as 2026-03-04.", "তারিখটি 2026-03-04 এভাবে লিখুন।"))
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

// record runs an audit write and logs a failure rather than failing the request.
//
// A failed audit write does not un-change a price, and pretending it does would be worse. The
// entry's absence is itself detectable — the trail is hash-chained.
func (h *Handlers) record(r *http.Request, write func() error, what string) {
	if h.audit == nil {
		return
	}
	if err := write(); err != nil {
		h.logger.ErrorContext(r.Context(), what, "error", err.Error())
	}
}

func (h *Handlers) moneyError(err error, given string) error {
	switch {
	case errors.Is(err, ErrMoneyPrecision):
		return errs.ErrValidation.WithFieldIn("amount_bdt",
			"A price cannot be finer than one poisha — at most two decimal places.",
			"দাম এক পয়সার চেয়ে ছোট হতে পারে না — দশমিকের পরে সর্বোচ্চ দুই ঘর।")
	case errors.Is(err, ErrMoneyRange):
		return errs.ErrValidation.WithFieldIn("amount_bdt",
			"That is not a plausible price for one unit.",
			"একক প্রতি এই দামটি বিশ্বাসযোগ্য নয়।")
	default:
		_ = given
		return errs.ErrValidation.WithFieldIn("amount_bdt",
			"Write the price in taka, like 12.50.",
			"দামটি টাকায় লিখুন, যেমন 12.50।")
	}
}

// translate turns the store's sentinels into answers a client can act on.
func (h *Handlers) translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrPriceNotYetSet):
		// 404 rather than 200 with a null price: the caller asked for the price on a day, and
		// there was not one. A 200 carrying nothing is the shape a client renders as zero.
		return errs.ErrNotFound
	case errors.Is(err, ErrDuplicateProduct):
		return errs.ErrConflict.WithFieldIn("trade_name",
			"That brand, strength, form, manufacturer and unit are already in the formulary.",
			"ওই ব্র্যান্ড, মাত্রা, ধরন, উৎপাদক ও একক আগে থেকেই ফর্মুলারিতে আছে।")
	case errors.Is(err, ErrUnknownGeneric):
		return errs.ErrValidation.WithFieldIn("generic",
			"That is not a generic this clinic holds. Add the generic first, or correct the spelling.",
			"ওই নামে কোনও জেনেরিক এই ক্লিনিকে নেই। আগে জেনেরিকটি যোগ করুন, নয়তো বানান ঠিক করুন।")
	case errors.Is(err, ErrUnknownVocabulary):
		return errs.ErrValidation.WithFieldIn("form_code",
			"That dosage form or dispensing unit is not one this clinic lists.",
			"ওই ধরন বা সরবরাহ একক এই ক্লিনিকের তালিকায় নেই।")
	case errors.Is(err, ErrWithdrawn):
		return errs.ErrConflict.WithFieldIn("product",
			"That product has been withdrawn. Reinstate it before pricing it.",
			"ওই ওষুধটি বাদ দেওয়া হয়েছে। দাম দেওয়ার আগে সেটি ফিরিয়ে আনুন।")
	case errors.Is(err, ErrPriceOverlaps):
		return errs.ErrConflict.WithFieldIn("effective_from",
			"Another price already covers that date. A price that has been superseded is not rewritten.",
			"ওই তারিখে আগে থেকেই আরেকটি দাম চালু আছে। পুরোনো দাম বদলানো যায় না।")
	case errors.Is(err, ErrEffectiveBeforeCurrent):
		return errs.ErrConflict.WithFieldIn("effective_from",
			"The current price already begins on or after that date. Choose a later day.",
			"বর্তমান দামটি ওই তারিখে বা তার পরে শুরু হয়েছে। পরের কোনও দিন বেছে নিন।")
	case errors.Is(err, ErrNoOpenReview):
		return errs.ErrConflict.WithFieldIn("review",
			"No price review is open. It may already have been completed.",
			"এখন কোনও দাম পর্যালোচনা খোলা নেই। হয়তো আগেই শেষ করা হয়েছে।")
	case errors.Is(err, ErrBadHeader):
		return errs.ErrValidation.WithFieldIn("file",
			"The first line of the file is not a header this import understands. It needs generic, trade_name, strength, form, manufacturer, unit_price_bdt and unit.",
			"ফাইলের প্রথম লাইনটি এই ইমপোর্ট বোঝে না। এতে generic, trade_name, strength, form, manufacturer, unit_price_bdt ও unit কলামগুলো থাকতে হবে।")
	case errors.Is(err, ErrMoneyPrecision), errors.Is(err, ErrMoneyRange), errors.Is(err, ErrMoneyFormat):
		return h.moneyError(err, "")
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}
