package medsafety

import (
	"context"
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

// The rule library over HTTP (CP77).
//
// # Identity, and the door that is not used here
//
// Every handler takes its identity from `httpx.PrincipalFrom`, never `eventstore.ActorFrom`.
// This module cannot reach the event store at all — `architecture.json` allows it `platform`,
// `formulary` and `clinical` — and that is the right boundary: a rule library is reference data
// in `core`, not a clinical fact about a patient. `ActorFrom` is also the write envelope and
// refuses a session with no enrolled device, which is every browser, and the physician authors
// these at a browser. `dthclint readpath` is what keeps that from coming back.
//
// # Why publishing is its own endpoint with its own permission and its own step-up
//
// Saving a draft is cheap and reversible. Publishing makes a rule stop prescriptions for every
// physician in the building from that second onwards, freezes the version forever, and retires
// whatever was live. Those are not the same act. Three separate decisions stand between an author
// and a live rule: press *Publish*, read the plain-language sentence of what will happen and
// confirm, and prove it is still you with a fresh second factor.
//
// # Why the sandbox takes a typed-in patient and not a real one
//
// The acceptance criterion is "run it against a test patient, see the result before publishing".
// The test patient is described on the form — an age, an eGFR, a pregnancy state, a diagnosis
// list, an allergy, the drugs. Not a patient id.
//
// Three reasons, and the first is sufficient. A physician testing a renal rule needs eGFR 29 and
// eGFR 31 and no eGFR at all, and no patient in the register is all three. Second, the sandbox is
// where somebody would most reasonably have added a log line, and a log line here would be a
// patient's clinical picture in a file with different access controls. Third, it keeps this
// module free of every patient-reading dependency, which is what lets CP78 own that assembly and
// own it in one place.
//
// **Nothing in this file logs the sandbox context, and nothing counts it.** The only thing that
// reaches the logger from a sandbox request is an error from the database.

// Handlers serve the rule library.
type Handlers struct {
	store  *Store
	audit  Auditor
	stepUp httpx.StepUpVerifier
	clock  interface{ Now() time.Time }
	logger *slog.Logger
}

// Auditor records what the security trail needs. Implemented by a bridge in cmd/api, because this
// module may not import `audit` — the reason every bridge in that binary exists: a module able to
// write audit entries directly grows a second, differently-shaped way of describing what
// happened, and the trail's value is that there is exactly one.
type Auditor interface {
	// RulePublished carries the **whole** version, per the checkpoint: a rule that changed
	// with no record of what it said is the defect this must not have.
	RulePublished(ctx context.Context, published Publication) error
	RuleWithdrawn(ctx context.Context, withdrawn Withdrawal) error
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Store  *Store
	Audit  Auditor
	StepUp httpx.StepUpVerifier
	Clock  interface{ Now() time.Time }
	Logger *slog.Logger
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{
		store: cfg.Store, audit: cfg.Audit, stepUp: cfg.StepUp,
		clock: cfg.Clock, logger: cfg.Logger,
	}
}

// Mount attaches /v1/medication-rules.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermRead, PermWrite, PermPublish)
	write := httpx.Permission(PermWrite)
	publish := httpx.Permission(PermPublish)

	r.Route("/medication-rules", func(m chi.Router) {
		// What the authoring form is built from: the eight rule types, which tests each may
		// use, the severities, and this clinic's molecules, classes and allergen groups. One
		// endpoint rather than five, for the reason the formulary's catalogue is one: a form
		// that had the classes but not the allergen groups would let somebody compose a rule
		// the validator then refuses.
		m.Method("GET", "/vocabulary", httpx.Declare(read, h.vocabulary))
		m.Method("GET", "/allergens", httpx.Declare(read, h.allergens))

		m.Method("GET", "/", httpx.Declare(read, h.list))
		m.Method("POST", "/", httpx.Declare(write, h.create))

		// Import and export, for review away from the screen.
		m.Method("GET", "/export", httpx.Declare(read, h.export))
		m.Method("POST", "/import", httpx.Declare(write, h.runImport))

		// The two that answer without changing anything: the plain-language preview, and the
		// sandbox. Both POST because they carry a rule in the body.
		m.Method("POST", "/preview", httpx.Declare(write, h.preview))
		m.Method("POST", "/sandbox", httpx.Declare(write, h.sandbox))

		// Versions are nested under their rule. A flat `/versions/{id}` beside
		// `/{ruleId}/...` is a path an OpenAPI reader cannot resolve without knowing that
		// chi prefers a literal segment over a parameter — and a contract that only works if
		// you know the router's tie-break rules is not a contract.
		m.Method("GET", "/{ruleId}", httpx.Declare(read, h.rule))
		m.Method("POST", "/{ruleId}/versions", httpx.Declare(write, h.newVersion))
		m.Method("PUT", "/{ruleId}/versions/{versionId}", httpx.Declare(write, h.saveDraft))

		// Its own endpoint, its own permission, its own step-up. See the note above.
		stepped := httpx.RequireStepUp(h.logger, h.stepUp, StepUpPurpose)(
			http.HandlerFunc(h.publish))
		m.Method("POST", "/{ruleId}/versions/{versionId}/publish",
			httpx.Declare(publish, stepped.ServeHTTP))

		withdrawn := httpx.RequireStepUp(h.logger, h.stepUp, StepUpPurpose)(
			http.HandlerFunc(h.withdraw))
		m.Method("POST", "/{ruleId}/withdraw", httpx.Declare(publish, withdrawn.ServeHTTP))

		approved := httpx.RequireStepUp(h.logger, h.stepUp, StepUpPurpose)(
			http.HandlerFunc(h.approveCrossReaction))
		m.Method("POST", "/allergens/cross-reactions/{id}/approve",
			httpx.Declare(publish, approved.ServeHTTP))
	})
}

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

func (h *Handlers) now() time.Time {
	if h.clock == nil {
		return time.Now().UTC()
	}
	return h.clock.Now().UTC()
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// predicateChoice is one test the form may offer, with its label in both languages.
type predicateChoice struct {
	Kind   PredicateKind `json:"kind"`
	NameEN string        `json:"name_en"`
	NameBN string        `json:"name_bn"`
	// Needs is the clinical fact this test reads, so the form can warn the author that a rule
	// using it will answer "cannot verify" on any patient who has not had it recorded.
	Needs   Datum  `json:"needs,omitempty"`
	NeedsEN string `json:"needs_en,omitempty"`
	NeedsBN string `json:"needs_bn,omitempty"`
}

type typeChoice struct {
	Type       RuleType          `json:"type"`
	NameEN     string            `json:"name_en"`
	NameBN     string            `json:"name_bn"`
	HintEN     string            `json:"hint_en"`
	HintBN     string            `json:"hint_bn"`
	Predicates []predicateChoice `json:"predicates"`
}

func (h *Handlers) vocabulary(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	vocab, err := h.store.Vocabulary(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}

	generics := make([]string, 0, len(vocab.Generics))
	for _, name := range vocab.Generics {
		generics = append(generics, name)
	}
	sortStrings(generics)
	classes := make([]string, 0, len(vocab.Classes))
	for code := range vocab.Classes {
		classes = append(classes, code)
	}
	sortStrings(classes)
	groups := make([]string, 0, len(vocab.AllergenGroups))
	for code := range vocab.AllergenGroups {
		groups = append(groups, code)
	}
	sortStrings(groups)

	types := make([]typeChoice, 0, len(AllRuleTypes))
	for _, t := range AllRuleTypes {
		choice := typeChoice{
			Type:   t,
			NameEN: typeNameEN[t], NameBN: typeNameBN[t],
			HintEN: typeHintEN[t], HintBN: typeHintBN[t],
		}
		// The form's field list is built from the same table the validator uses, so the two
		// cannot disagree about what a renal rule may contain.
		for _, k := range predicateOrder {
			if !allowedPredicates[t][k] {
				continue
			}
			p := predicateChoice{Kind: k, NameEN: predicateNameEN[k], NameBN: predicateNameBN[k]}
			if d := (Predicate{Kind: k, CurrentMedications: true}).Needs(); d != "" {
				p.Needs, p.NeedsEN, p.NeedsBN = d, datumEN[d], datumBN[d]
			}
			choice.Predicates = append(choice.Predicates, p)
		}
		types = append(types, choice)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"types":            types,
		"severities":       AllSeverities,
		"operators":        []Operator{OpLessThan, OpAtMost, OpGreaterThan, OpAtLeast},
		"pregnancy_states": PregnancyStates,
		"hepatic_grades":   HepaticGrades,
		"generics":         generics,
		"classes":          classes,
		"allergen_groups":  groups,
	})
}

func (h *Handlers) allergens(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.caller(w, r); !ok {
		return
	}
	groups, cross, err := h.store.Allergens(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"groups": groups, "cross_reactions": cross,
	})
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	filter := RuleFilter{
		Type:           RuleType(strings.TrimSpace(query.Get("type"))),
		UnapprovedOnly: query.Get("unapproved") == "true",
		ActiveOnly:     query.Get("active") == "true",
	}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("limit",
				"Ask for a whole number of rules.", "কতগুলো নিয়ম চান, পূর্ণসংখ্যায় লিখুন।"))
			return
		}
		filter.Limit = parsed
	}
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("offset",
				"Start from a whole number of rules.", "কত নম্বর থেকে শুরু করবেন, পূর্ণসংখ্যায় লিখুন।"))
			return
		}
		filter.Offset = parsed
	}
	page, err := h.store.Rules(r.Context(), who.facilityID, filter)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

// versionView is a version as the screens read it: the stored fields, plus the plain-language
// sentence rendered from the stored condition.
//
// The sentence is computed here rather than stored, so that a change to how a rule reads applies
// to every rule at once. What is stored, and what reproducibility rests on, is the condition.
type versionView struct {
	Version
	Plain Plain `json:"plain"`
}

func view(v Version) versionView { return versionView{Version: v, Plain: v.Explain()} }

func (h *Handlers) rule(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.uuidParam(w, r, "ruleId")
	if !ok {
		return
	}
	rule, versions, err := h.store.Rule(r.Context(), who.facilityID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	views := make([]versionView, 0, len(versions))
	for _, v := range versions {
		views = append(views, view(v))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rule": rule, "versions": views})
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// draftBody is a version as the form posts it.
type draftBody struct {
	Severity  Severity  `json:"severity"`
	NameEN    string    `json:"name_en"`
	NameBN    string    `json:"name_bn"`
	MessageEN string    `json:"message_en"`
	MessageBN string    `json:"message_bn"`
	AdviceEN  string    `json:"advice_en"`
	AdviceBN  string    `json:"advice_bn"`
	Condition Condition `json:"condition"`
	Source    string    `json:"source"`
	Notes     string    `json:"notes"`
}

func (b draftBody) version() Version {
	return Version{
		Severity: b.Severity, NameEN: strings.TrimSpace(b.NameEN),
		NameBN:    strings.TrimSpace(b.NameBN),
		MessageEN: strings.TrimSpace(b.MessageEN), MessageBN: strings.TrimSpace(b.MessageBN),
		AdviceEN: strings.TrimSpace(b.AdviceEN), AdviceBN: strings.TrimSpace(b.AdviceBN),
		Condition: b.Condition.Canonical(), Source: strings.TrimSpace(b.Source),
		Notes: strings.TrimSpace(b.Notes),
	}
}

type createBody struct {
	Code string   `json:"code"`
	Type RuleType `json:"type"`
	draftBody
}

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body createBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	vocab, err := h.store.Vocabulary(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	id, err := h.store.CreateRule(r.Context(), vocab, NewRule{
		FacilityID: who.facilityID, Code: body.Code, Type: body.Type,
		Draft: body.version(), ActorID: who.userID,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	rule, versions, err := h.store.Rule(r.Context(), who.facilityID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"rule": rule, "version": view(versions[0]),
	})
}

func (h *Handlers) newVersion(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.uuidParam(w, r, "ruleId")
	if !ok {
		return
	}
	created, err := h.store.DraftVersion(r.Context(), who.facilityID, id, who.userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"version": view(created)})
}

func (h *Handlers) saveDraft(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.uuidParam(w, r, "versionId")
	if !ok {
		return
	}
	var body draftBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	vocab, err := h.store.Vocabulary(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	if err := h.store.SaveDraft(r.Context(), vocab, who.facilityID, id,
		body.version(), who.userID); err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	saved, err := h.store.Version(r.Context(), who.facilityID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": view(saved)})
}

func (h *Handlers) publish(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.uuidParam(w, r, "versionId")
	if !ok {
		return
	}
	published, err := h.store.Publish(r.Context(), who.facilityID, id, who.userID, h.now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}

	// Audited after the write, never before: an entry for a publication that then failed is a
	// trail saying something happened which did not.
	published.ActorCode, published.ActorRole = who.code, who.role
	h.record(r, func() error { return h.audit.RulePublished(r.Context(), published) },
		"recording a medication rule publication")

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"version": view(published.Version), "supersedes": published.Supersedes,
	})
}

type withdrawBody struct {
	Reason string `json:"reason"`
}

func (h *Handlers) withdraw(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.uuidParam(w, r, "ruleId")
	if !ok {
		return
	}
	var body withdrawBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("reason",
			"Say why this rule is being withdrawn.", "নিয়মটি কেন তুলে নেওয়া হচ্ছে লিখুন।"))
		return
	}
	withdrawn, err := h.store.Withdraw(r.Context(), who.facilityID, id, body.Reason,
		who.userID, h.now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	withdrawn.ActorCode, withdrawn.ActorRole = who.code, who.role
	h.record(r, func() error { return h.audit.RuleWithdrawn(r.Context(), withdrawn) },
		"recording a medication rule withdrawal")

	rule, _, err := h.store.Rule(r.Context(), who.facilityID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rule": rule})
}

func (h *Handlers) approveCrossReaction(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	id, ok := h.uuidParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.store.ApproveCrossReaction(r.Context(), id, who.userID, h.now()); err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	groups, cross, err := h.store.Allergens(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups, "cross_reactions": cross})
}

// ---------------------------------------------------------------------------
// Preview and sandbox
// ---------------------------------------------------------------------------

type previewBody struct {
	Type RuleType `json:"type"`
	// Code is accepted and ignored. The authoring screen holds one object and posts it to the
	// preview on every pause in typing and to the create endpoint once; a preview that refused
	// the object the form is holding would make the screen maintain two shapes of the same
	// rule, which is how the two come to disagree.
	Code string `json:"code,omitempty"`
	draftBody
}

// preview says a rule back in words without storing it.
//
// It validates too, and returns the validation error rather than a 422, because on this screen a
// half-written rule is the normal state: the author is typing. A form that showed a red error
// envelope after every keystroke would be one he stops reading.
func (h *Handlers) preview(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body previewBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	vocab, err := h.store.Vocabulary(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	version := body.version()
	out := map[string]any{"plain": version.Explain(), "valid": true}
	if err := version.Validate(vocab, body.Type); err != nil {
		out["valid"] = false
		out["problem"] = err.Error()
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// sandboxBody is a rule and a patient described by hand.
type sandboxBody struct {
	// VersionID runs a stored version. Either this or `rule`, never both: running "the version
	// I have open" and "the text on screen" through one field is how somebody publishes having
	// tested something else.
	VersionID string `json:"version_id,omitempty"`

	Type RuleType   `json:"type,omitempty"`
	Rule *draftBody `json:"rule,omitempty"`

	Patient Context `json:"patient"`
}

// sandbox runs one rule against one hand-typed clinical picture.
//
// **This is the same evaluator CP78 will run.** Not a preview of it, not a simplified version:
// `Version.Evaluate` is what the sandbox calls and what the engine will call. A sandbox that ran
// a second implementation would be showing the physician the behaviour of a program that never
// meets a patient.
func (h *Handlers) sandbox(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	var body sandboxBody
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if (body.VersionID == "") == (body.Rule == nil) {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("rule",
			"Send either a saved version to test or a rule to test, not both and not neither.",
			"পরীক্ষার জন্য হয় একটি সংরক্ষিত সংস্করণ পাঠান, নয় একটি নিয়ম — দুটো একসঙ্গে নয়, কোনোটিই না-পাঠানোও নয়।"))
		return
	}

	var rule Rule
	var version Version

	switch {
	case body.VersionID != "":
		id, err := uuid.Parse(body.VersionID)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("version_id",
				"That is not a version.", "এটি কোনো সংস্করণ নয়।"))
			return
		}
		version, err = h.store.Version(r.Context(), who.facilityID, id)
		if err != nil {
			httpx.WriteError(w, r, h.logger, h.translate(err))
			return
		}
		stored, _, err := h.store.Rule(r.Context(), who.facilityID, version.RuleID)
		if err != nil {
			httpx.WriteError(w, r, h.logger, h.translate(err))
			return
		}
		rule = stored
	default:
		vocab, err := h.store.Vocabulary(r.Context(), who.facilityID)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
			return
		}
		version = body.Rule.version()
		if err := version.Validate(vocab, body.Type); err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("condition",
				err.Error(), "নিয়মটি এখনও এমন কিছু বলছে না যা যাচাই করা যায়: "+err.Error()))
			return
		}
		rule = Rule{Code: "DRAFT", Type: body.Type}
	}

	finding := version.Evaluate(rule, body.Patient)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"finding": finding,
		"plain":   version.Explain(),
		// Echoed so the screen can say "this is what was tested" beside the verdict. A
		// physician who cannot see which picture produced a result cannot trust the result.
		"patient": body.Patient,
		// True when the rule under test is one that could actually fire on a real
		// prescription today. False for a draft, and that is the sentence the sandbox has to
		// say out loud: *this rule is not live; nothing you see here is happening yet.*
		"live": version.Live(h.now()),
	})
}

// ---------------------------------------------------------------------------
// Import and export
// ---------------------------------------------------------------------------

func (h *Handlers) export(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	doc, err := h.store.Export(r.Context(), who.facilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, doc)
}

func (h *Handlers) runImport(w http.ResponseWriter, r *http.Request) {
	who, ok := h.caller(w, r)
	if !ok {
		return
	}
	var doc ExportDocument
	if err := httpx.DecodeJSON(w, r, &doc); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	dryRun := r.URL.Query().Get("mode") != "APPLY"
	report, err := h.store.Import(r.Context(), who.facilityID, doc, who.userID, dryRun)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, report)
}

// ---------------------------------------------------------------------------
// Plumbing
// ---------------------------------------------------------------------------

func (h *Handlers) uuidParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		// 404 rather than 422: a malformed id and an id this facility does not have are the
		// same answer, for the reason every other module gives — telling somebody a thing
		// exists but is out of reach is itself a disclosure.
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// record runs an audit write and logs a failure without failing the request.
//
// The act has happened and is in `core`; an audit write that failed afterwards is a gap in the
// trail, which is worth an error in the log and is not worth telling the physician his rule did
// not publish when it did.
func (h *Handlers) record(r *http.Request, write func() error, what string) {
	if h.audit == nil {
		return
	}
	if err := write(); err != nil {
		h.logger.ErrorContext(r.Context(), what+" failed", slog.String("error", err.Error()))
	}
}

func (h *Handlers) translate(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrDuplicateCode):
		return errs.ErrConflict.WithDetail(err).WithFieldIn("code",
			"A rule with that code already exists.", "ওই কোডের একটি নিয়ম ইতিমধ্যে আছে।")
	case errors.Is(err, ErrNotADraft):
		return errs.ErrConflict.WithDetail(err).WithFieldIn("version",
			"That version has been published. Draft a new one instead.",
			"ওই সংস্করণটি প্রকাশিত হয়ে গেছে। নতুন একটি খসড়া করুন।")
	case errors.Is(err, ErrWithdrawn):
		return errs.ErrConflict.WithDetail(err).WithFieldIn("rule",
			"That rule has been withdrawn.", "ওই নিয়মটি তুলে নেওয়া হয়েছে।")
	case errors.Is(err, ErrNotBilingual), errors.Is(err, ErrInvalidRule):
		return errs.ErrValidation.WithDetail(err).WithFieldIn("condition",
			err.Error(), "নিয়মটি এখনও যাচাইযোগ্য কিছু বলছে না: "+err.Error())
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}
