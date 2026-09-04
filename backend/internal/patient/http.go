package patient

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Handlers serve /v1/patients (CP29).
//
// Registering needs `patient.write.demographics`, which the access matrix gives to
// REGISTRATION at their station and to FIELD_WORKER for their own captures. The plan names
// a `patient.create` permission; there is no such code in the catalogue, and inventing one
// would mean a migration and a decision — per role — about who may register but not
// correct. At this clinic's size those are one authority, held by one desk, so the existing
// permission is used and the deviation is recorded in ADR-0020. Splitting them is a
// catalogue change, which is Dr Nahid's to make.
type Handlers struct {
	service *Service
	store   *Store
	matcher *Matcher
	stepUp  httpx.StepUpVerifier
	audit   AuditRecorder
	photos  *PhotoService
	sub     []func(chi.Router)
	clock   clock.Clock
	logger  *slog.Logger
}

// The catalogue permissions this module checks. Strings rather than auth's constants
// because patient does not import auth; cmd/api's contract test compares them.
const (
	PermPatientWriteDemographics = "patient.write.demographics"
	PermPatientReadDemographics  = "patient.read.demographics"
	PermPatientMerge             = "patient.merge"
)

// PurposeMerge is the step-up purpose merging requires. A merge is irreversible in effect
// — two histories become one — so a session left open on a desk must not be able to do it.
const PurposeMerge = "patient_merge"

type HandlersConfig struct {
	Service *Service
	Store   *Store
	Matcher *Matcher
	// StepUp verifies the token a merge carries; nil refuses every merge.
	StepUp httpx.StepUpVerifier
	// Audit records searches and record openings (CP31). Nil records nothing, which is
	// what the routing tests want and never what a deployment wants.
	Audit AuditRecorder
	// Photos issues upload URLs and attaches what was uploaded (CP34). Nil answers the
	// photograph endpoints with 503 rather than pretending.
	Photos *PhotoService
	// Sub mounts routes that hang off a patient but belong to another module — consent
	// (CP36) is the first. The alternative was importing those modules here, which would
	// invert the dependency the architecture check enforces: a patient is the thing other
	// modules are *about*, and it must not know which ones exist. The composition root
	// passes them in, which is where knowing about every module is the job.
	Sub    []func(chi.Router)
	Clock  clock.Clock
	Logger *slog.Logger
}

func NewHandlers(cfg HandlersConfig) *Handlers {
	h := &Handlers{
		service: cfg.Service, store: cfg.Store, matcher: cfg.Matcher,
		stepUp: cfg.StepUp, audit: cfg.Audit, photos: cfg.Photos, sub: cfg.Sub,
		clock: cfg.Clock, logger: cfg.Logger,
	}
	if h.clock == nil {
		h.clock = clock.Real{}
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	return h
}

// Mount attaches the endpoints under /v1/patients.
func (h *Handlers) Mount(r chi.Router) {
	r.Route("/patients", func(p chi.Router) {
		p.Method("POST", "/", httpx.Declare(httpx.Permission(PermPatientWriteDemographics), h.register))
		// The duplicate check needs the *write* permission, not the read one: it answers
		// "is this person already here" for somebody about to create a record, and it
		// returns names and dates of birth. A reader with no reason to register has no
		// reason to probe the register with arbitrary names either (CP30).
		p.Method("POST", "/check-duplicates", httpx.Declare(
			httpx.Permission(PermPatientWriteDemographics), h.checkDuplicates))
		p.Method("GET", "/{id}", httpx.Declare(httpx.Permission(PermPatientReadDemographics), h.byID))
		h.mountSearch(p)
		h.mountPhoto(p)
		h.mountCorrection(p)
		h.mountTimeline(p)
		p.Method("GET", "/{id}/merges", httpx.Declare(httpx.Permission(PermPatientReadDemographics), h.merges))
		// Merging needs its own permission *and* a step-up. Two histories become one, and
		// the change is irreversible in effect however well recorded the decision is.
		merge := httpx.RequireStepUp(h.logger, h.stepUp, PurposeMerge)(http.HandlerFunc(h.merge))
		p.Method("POST", "/{id}/merge", httpx.Declare(httpx.Permission(PermPatientMerge), merge.ServeHTTP))
		for _, mount := range h.sub {
			mount(p)
		}
	})
}

// --- requests ---

// registrationRequest is the wire form. Separate from Registration because what a client
// may set and what the domain holds are different lists: a client sets no facility, no
// status and no clinical id, and `event_id` belongs to the request rather than the patient.
type registrationRequest struct {
	// EventID is the client's idempotency key, UUIDv7. A tablet that sends a registration,
	// loses the reply and sends it again must create one patient; the ledger's uniqueness
	// on event_id is what makes that true, a week later as much as a second later.
	EventID string `json:"event_id"`

	NameEN string `json:"name_en"`
	NameBN string `json:"name_bn"`
	Sex    string `json:"sex"`

	BirthDate    string `json:"birth_date"`
	DOBPrecision string `json:"dob_precision"`
	DOBSource    string `json:"dob_source"`

	PhonePrimary   string `json:"phone_primary"`
	PhoneSecondary string `json:"phone_secondary"`

	Division    string `json:"division"`
	District    string `json:"district"`
	Upazila     string `json:"upazila"`
	AddressLine string `json:"address_line"`
	Postcode    string `json:"postcode"`

	EmergencyName     string `json:"emergency_name"`
	EmergencyRelation string `json:"emergency_relation"`
	EmergencyPhone    string `json:"emergency_phone"`

	EducationLevel     string `json:"education_level"`
	OccupationCategory string `json:"occupation_category"`
	IncomeBand         string `json:"income_band"`
	HouseholdSize      int    `json:"household_size"`
	ResidenceType      string `json:"residence_type"`
	MedicinePayer      string `json:"medicine_payer"`

	// Identifiers are the numbers as typed or as read by OCR, keyed by kind. They are
	// hashed and sealed before they reach a column and never appear in a response, a log
	// or an event (D-47).
	Identifiers map[string]string `json:"identifiers"`

	ConsentReference string `json:"consent_reference"`
}

func (h *Handlers) register(w http.ResponseWriter, r *http.Request) {
	var req registrationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	eventID, err := uuid.Parse(req.EventID)
	if err != nil || eventID == uuid.Nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"A client-generated UUID is required so a retry does not create a second patient.",
			"পুনরায় পাঠালে যেন দ্বিতীয় রোগী তৈরি না হয়, সে জন্য ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।"))
		return
	}

	registration, bad := req.registration()
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}

	result, err := h.service.Register(r.Context(), registration, eventID, sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}

	status := http.StatusCreated
	if result.Duplicate {
		// A retry that already landed. 200, not 201: nothing was created this time, and a
		// client that counts its 201s to reconcile an offline queue would otherwise
		// double-count (§7.5).
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, map[string]any{
		"patient":   h.view(r, result.Patient),
		"event_id":  result.Event.EventID,
		"duplicate": result.Duplicate,
	})
}

func (h *Handlers) byID(w http.ResponseWriter, r *http.Request) {
	// The facility comes from the verified actor rather than from the principal's string
	// fields, so that "which clinic is asking" is parsed in one place and a malformed one
	// is a refusal rather than a zero UUID that matches nothing.
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		// The same answer as a patient at another facility, deliberately: a 404 that
		// distinguishes a malformed id from an unknown one from a forbidden one is a way
		// to learn which patients exist.
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}

	found, err := h.store.ByID(r.Context(), id, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"patient": h.view(r, found)})
}

func (h *Handlers) checkDuplicates(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	var req registrationRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// The check runs while the desk is still typing, so a partial form must not be a 422:
	// the point is to warn before the record exists, and a form with no consent reference
	// yet is the normal state at that moment.
	registration, bad := req.registration()
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	if h.matcher == nil {
		httpx.WriteJSON(w, http.StatusOK, Match{Verdict: VerdictClear, Candidates: []Candidate{}})
		return
	}
	match, err := h.matcher.Check(r.Context(), actor.FacilityID(), registration)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	if match.Candidates == nil {
		match.Candidates = []Candidate{}
	}
	httpx.WriteJSON(w, http.StatusOK, match)
}

type mergeRequest struct {
	EventID string `json:"event_id"`
	// MergedID is the record that will redirect. The survivor is the one in the path, so
	// that the URL names the record that continues to exist.
	MergedID      string  `json:"merged_id"`
	Score         float64 `json:"score"`
	Decision      string  `json:"decision"`
	Justification string  `json:"justification"`
}

func (h *Handlers) merge(w http.ResponseWriter, r *http.Request) {
	survivorID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	var req mergeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, err := uuid.Parse(req.EventID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("event_id",
			"A client-generated UUID is required.", "ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।"))
		return
	}
	mergedID, err := uuid.Parse(req.MergedID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("merged_id",
			"Name the record that should redirect.", "কোন রেকর্ডটি অন্যটিতে যুক্ত হবে তা দিন।"))
		return
	}

	event, err := h.service.Merge(r.Context(), MergeRequest{
		SurvivorID: survivorID, MergedID: mergedID,
		Score: req.Score, Decision: req.Decision, Justification: req.Justification,
		EventID: eventID,
	}, sourceOf(r))
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}

	survivor, err := h.store.ByID(r.Context(), survivorID, event.Actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"survivor": h.view(r, survivor),
		"event_id": event.EventID,
	})
}

func (h *Handlers) merges(w http.ResponseWriter, r *http.Request) {
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	if _, err := h.store.ByID(r.Context(), id, actor.FacilityID()); err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	history, err := h.store.MergeHistory(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	out := make([]map[string]any, 0, len(history))
	for _, row := range history {
		score, _ := row.Score.Float64Value()
		out = append(out, map[string]any{
			"merged_id":     row.MergedID,
			"score":         score.Float64,
			"decision":      row.Decision,
			"justification": row.Justification,
			"merged_by":     row.MergedBy,
			"merged_at":     row.MergedAt,
			"event_id":      row.EventID,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"merges": out})
}

// --- views ---

type birthView struct {
	Date      string `json:"date"`
	Precision string `json:"precision"`
	Source    string `json:"source"`
	// Age is whole years today. Rendered here rather than in the browser so that the
	// clinic's calendar decides it, and accompanied by the precision so a screen can say
	// "about 47" when the date is only a year.
	Age int `json:"age"`
}

type identifierView struct {
	Kind   string `json:"kind"`
	Masked string `json:"masked"`
}

type patientView struct {
	ID         uuid.UUID `json:"id"`
	ClinicalID string    `json:"clinical_id"`

	NameEN string    `json:"name_en"`
	NameBN string    `json:"name_bn"`
	Sex    string    `json:"sex"`
	Birth  birthView `json:"birth"`

	PhonePrimary   string `json:"phone_primary"`
	PhoneSecondary string `json:"phone_secondary"`

	Address   Address          `json:"address"`
	Emergency EmergencyContact `json:"emergency_contact"`
	Socio     Socioeconomic    `json:"socioeconomic"`

	Identifiers []identifierView `json:"identifiers"`

	Status       string    `json:"status"`
	RegisteredAt time.Time `json:"registered_at"`
}

func (h *Handlers) view(r *http.Request, p Patient) patientView {
	view := patientView{
		ID: p.ID, ClinicalID: p.ClinicalID,
		NameEN: p.NameEN, NameBN: p.NameBN, Sex: string(p.Sex),
		Birth: birthView{
			Date:      p.Birth.Date.In(Dhaka).Format(time.DateOnly),
			Precision: string(p.Birth.Precision),
			Source:    string(p.Birth.Source),
			Age:       p.Birth.Age(h.clock.Now()),
		},
		PhonePrimary: p.PhonePrimary, PhoneSecondary: p.PhoneSecondary,
		Address: p.Address, Emergency: p.Emergency, Socio: p.Socio,
		Identifiers:  []identifierView{},
		Status:       string(p.Status),
		RegisteredAt: p.RegisteredAt,
	}
	// Masks only. Revealing a number is a separate, step-upped act with its own audit
	// entry, and a list endpoint is not the place for it.
	identifiers, err := h.store.Identifiers(r.Context(), p.ID)
	if err != nil {
		// The patient is still the answer; the masks are a convenience. Logged rather than
		// failed, because a registration desk that cannot see a record at all because one
		// mask could not be read is worse off than one that sees the record.
		h.logger.WarnContext(r.Context(), "could not read a patient's identifier masks",
			"patient_id", p.ID, "error", err)
		return view
	}
	for _, identifier := range identifiers {
		view.Identifiers = append(view.Identifiers, identifierView{
			Kind: string(identifier.Kind), Masked: identifier.Masked,
		})
	}
	return view
}

// --- translation ---

// registration converts the wire form, reporting the field problems the domain cannot see:
// a value that is not a member of a closed set arrives here as a string.
func (r registrationRequest) registration() (Registration, error) {
	out := Registration{
		NameEN: r.NameEN, NameBN: r.NameBN, Sex: Sex(r.Sex),
		DOBPrecision: DOBPrecision(r.DOBPrecision), DOBSource: DOBSource(r.DOBSource),
		PhonePrimary: r.PhonePrimary, PhoneSecondary: r.PhoneSecondary,
		Address: Address{
			Division: r.Division, District: r.District, Upazila: r.Upazila,
			AddressLine: r.AddressLine, Postcode: r.Postcode,
		},
		Emergency: EmergencyContact{
			Name: r.EmergencyName, Relation: r.EmergencyRelation, Phone: r.EmergencyPhone,
		},
		Socio: Socioeconomic{
			Education: r.EducationLevel, Occupation: r.OccupationCategory,
			IncomeBand: r.IncomeBand, HouseholdSize: r.HouseholdSize,
			Residence: r.ResidenceType, MedicinePayer: r.MedicinePayer,
		},
		ConsentReference: r.ConsentReference,
	}
	if r.BirthDate != "" {
		born, err := time.ParseInLocation(time.DateOnly, r.BirthDate, Dhaka)
		if err != nil {
			return Registration{}, errs.ErrValidation.WithFieldIn("birth_date",
				"Enter the date of birth as YYYY-MM-DD.",
				"জন্ম তারিখ YYYY-MM-DD আকারে দিন।")
		}
		out.BirthDate = born
	}
	if len(r.Identifiers) > 0 {
		out.Identifiers = make(map[IdentifierKind]string, len(r.Identifiers))
		for kind, value := range r.Identifiers {
			out.Identifiers[IdentifierKind(kind)] = value
		}
	}
	return out, nil
}

// translateForClient turns a domain refusal into the envelope, keeping both languages.
func translateForClient(err error) error {
	var fields Errors
	if errors.As(err, &fields) {
		out := errs.ErrValidation
		for _, field := range fields {
			out = out.WithFieldIn(field.Field, field.Message, field.MessageBN)
		}
		return out
	}
	switch {
	case errors.Is(err, eventstore.ErrNoDevice):
		// D-46: a clinical write is evidence, and evidence names the device it came from.
		// Its own code so a client can say why rather than "no" — and so that when D-71
		// settles browser device identity, the browser's registration desk fails visibly
		// here rather than silently attributing an event to no device.
		return errs.ErrDeviceRequired.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoRole):
		return errs.ErrForbidden.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoPrincipal):
		return errs.ErrUnauthenticated.WithDetail(err)
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrDuplicateIdentifier):
		return errs.ErrConflict.WithFieldIn("identifiers",
			"That identity number already belongs to a patient at this clinic.",
			"এই পরিচয় নম্বরটি এই ক্লিনিকের অন্য একজন রোগীর।").WithDetail(err)
	case errors.Is(err, ErrBlockedByDuplicate):
		return errs.ErrConflict.WithFieldIn("duplicate",
			"This person is already registered at this clinic.",
			"এই ব্যক্তি ইতিমধ্যে এই ক্লিনিকে নিবন্ধিত।").WithDetail(err)
	case errors.Is(err, ErrAlreadyMerged):
		return errs.ErrConflict.WithFieldIn("merged_id",
			"That record has already been merged into another one.",
			"এই রেকর্ডটি আগেই অন্য একটি রেকর্ডে যুক্ত করা হয়েছে।").WithDetail(err)
	case errors.Is(err, ErrCannotMerge):
		return errs.ErrValidation.WithFieldIn("merged_id",
			"These two records cannot be merged.",
			"এই দুটি রেকর্ড একত্র করা যাবে না।").WithDetail(err)
	case errors.Is(err, ErrDuplicateEvent):
		return errs.ErrConflict.WithFieldIn("event_id",
			"That event id has already been used for something else.",
			"এই ইভেন্ট আইডি আগেই অন্য কাজে ব্যবহার করা হয়েছে।").WithDetail(err)
	case errors.Is(err, eventstore.ErrInvalidPayload):
		// The domain accepted it and the ledger's schema did not, which means the two
		// disagree. A 422 tells the client something true; the detail tells us where to
		// look, and a test holds the two validators in step.
		return errs.ErrValidation.WithDetail(err)
	}
	return err
}

// sourceOf says how the registration reached the server, for the envelope's `source`
// (§7.2). Read from the enrolled device rather than trusted from the body: a client that
// declares its own source can make a web registration look like a field capture.
func sourceOf(r *http.Request) eventstore.Source {
	principal, ok := httpx.PrincipalFrom(r.Context())
	if ok && principal.DeviceID != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}
