package consent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Consent over HTTP (CP36).
//
// Mounted under /v1/patients/{id}/consents rather than as its own top-level resource: a
// consent has no meaning apart from the patient it belongs to, and a URL that suggests it
// does is a URL somebody will eventually list.

// The catalogue permissions this module checks. Strings rather than auth's constants,
// because consent does not import auth; cmd/api's contract test compares them.
const (
	PermConsentRecord = "patient.consent.record"
	PermConsentRevoke = "patient.consent.revoke"
	PermPatientRead   = "patient.read.demographics"
)

type Handlers struct {
	service *Service
	store   *Store
	blobs   blobstore.Store
	clock   clock.Clock
	logger  *slog.Logger
}

type HandlersConfig struct {
	Service *Service
	Store   *Store
	// Blobs stores the signature and thumbprint images. Nil answers the evidence endpoints
	// with 503 rather than pretending a consent has an image behind it.
	Blobs  blobstore.Store
	Clock  clock.Clock
	Logger *slog.Logger
}

func NewHandlers(cfg HandlersConfig) *Handlers {
	h := &Handlers{service: cfg.Service, store: cfg.Store, blobs: cfg.Blobs,
		clock: cfg.Clock, logger: cfg.Logger}
	if h.clock == nil {
		h.clock = clock.Real{}
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	return h
}

// Mount attaches the endpoints under /v1/patients/{id}.
func (h *Handlers) Mount(p chi.Router) {
	read := httpx.Permission(PermPatientRead)
	p.Method("GET", "/{id}/consents", httpx.Declare(read, h.list))
	p.Method("GET", "/{id}/consents/history", httpx.Declare(read, h.history))
	p.Method("POST", "/{id}/consents", httpx.Declare(httpx.Permission(PermConsentRecord), h.grant))
	p.Method("POST", "/{id}/consents/evidence-url", httpx.Declare(
		httpx.Permission(PermConsentRecord), h.evidenceURL))
	// Revoking is its own permission, and deliberately a POST to a sub-resource rather than
	// a DELETE: nothing is deleted. The grant stays, and a revocation is recorded beside it.
	p.Method("POST", "/{id}/consents/{type}/revoke", httpx.Declare(
		httpx.Permission(PermConsentRevoke), h.revoke))
}

// MountTemplates attaches the wording endpoint under /v1.
func (h *Handlers) MountTemplates(r chi.Router) {
	r.Method("GET", "/consent-templates", httpx.Declare(
		httpx.Permission(PermConsentRecord), h.templates))
}

// --- reads ---

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	records, err := h.store.All(r.Context(), id, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	// Every type, always — including the ones never asked about. A screen that lists only
	// what exists cannot show the registration desk what it has not done, and "we never
	// asked" is the answer that matters at the point of care.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"consents": withGaps(id, records)})
}

func (h *Handlers) history(w http.ResponseWriter, r *http.Request) {
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	entries, err := h.store.History(r.Context(), id, actor.FacilityID())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handlers) templates(w http.ResponseWriter, r *http.Request) {
	language := r.URL.Query().Get("language")
	if language != "en" && language != "bn" {
		language = "bn"
	}
	templates, err := h.store.ActiveTemplates(r.Context(), language)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"language": language, "templates": templates})
}

// --- writes ---

type grantRequest struct {
	EventID     string `json:"event_id"`
	ConsentType string `json:"consent_type"`
	Language    string `json:"language"`

	CaptureMethod  string `json:"capture_method"`
	EvidenceKey    string `json:"evidence_key"`
	EvidenceSHA256 string `json:"evidence_sha256"`
	PaperReference string `json:"paper_reference"`

	WitnessedBy string `json:"witnessed_by"`

	GrantedForRelation string `json:"granted_for_relation"`
	GrantedForName     string `json:"granted_for_name"`
}

func (h *Handlers) grant(w http.ResponseWriter, r *http.Request) {
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	var req grantRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, bad := eventIDOf(req.EventID)
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	witness, err := optionalUUID(req.WitnessedBy)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("witnessed_by",
			"The witness must be a member of staff.", "সাক্ষী অবশ্যই একজন কর্মী হতে হবে।"))
		return
	}

	record, err := h.service.Grant(r.Context(), id, Grant{
		EventID: eventID, ConsentType: Type(req.ConsentType), Language: req.Language,
		CaptureMethod: CaptureMethod(req.CaptureMethod),
		EvidenceKey:   req.EvidenceKey, EvidenceSHA256: req.EvidenceSHA256,
		PaperReference: req.PaperReference, WitnessedBy: witness,
		GrantedForRelation: req.GrantedForRelation, GrantedForName: req.GrantedForName,
		Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"consent": record})
}

type revokeRequest struct {
	EventID     string `json:"event_id"`
	Reason      string `json:"reason"`
	RequestedBy string `json:"requested_by"`
}

func (h *Handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	var req revokeRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	eventID, bad := eventIDOf(req.EventID)
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}
	record, err := h.service.Revoke(r.Context(), id, Revocation{
		EventID: eventID, ConsentType: Type(chi.URLParam(r, "type")),
		Reason: req.Reason, RequestedBy: req.RequestedBy, Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"consent": record})
}

type evidenceRequest struct {
	ContentType string `json:"content_type"`
}

// EvidenceTTL is how long an evidence upload URL lives. Long enough for a signature to be
// drawn and uploaded on clinic wifi, short enough that one out of a browser's history is
// useless.
const EvidenceTTL = 10 * time.Minute

// evidenceURL mints a pre-signed PUT for a signature or thumbprint image.
//
// Same shape as a photograph (CP34), and for the same reason: a signature is identifier-class
// data, and one that never enters the API process cannot end up in a request log.
func (h *Handlers) evidenceURL(w http.ResponseWriter, r *http.Request) {
	if h.blobs == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable)
		return
	}
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	var req evidenceRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if req.ContentType != "image/png" {
		// PNG only. A signature is line art on a transparent ground, JPEG artefacts around
		// thin strokes are exactly what makes a signature arguable, and one format is one
		// fewer thing for a viewer to get wrong.
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("content_type",
			"A signature is stored as a PNG.", "স্বাক্ষর PNG হিসেবে সংরক্ষণ করা হয়।"))
		return
	}
	key, err := evidenceKey(id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	signer, ok := h.blobs.(interface {
		SignedUpload(context.Context, blobstore.Class, string, time.Duration, string) (string, error)
	})
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable)
		return
	}
	url, err := signer.SignedUpload(r.Context(), blobstore.ClassIdentifier, key, EvidenceTTL, req.ContentType)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"object_key": key, "upload_url": url,
		"expires_at": h.clock.Now().UTC().Add(EvidenceTTL),
	})
}

// --- helpers ---

func (h *Handlers) patientParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// evidenceKey is the server's, never the client's. A key a client could choose is a key
// that can be pointed at somebody else's signature, and a correctly signed URL would then
// serve it.
func evidenceKey(patientID uuid.UUID) (string, error) {
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return fmt.Sprintf("patients/%s/consent-%s.png", patientID, hex.EncodeToString(suffix)), nil
}

func eventIDOf(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errs.ErrValidation.WithFieldIn("event_id",
			"A client-generated UUID is required so a retry does not record the consent twice.",
			"পুনরায় পাঠালে যেন সম্মতি দুবার রেকর্ড না হয়, সে জন্য ক্লায়েন্ট-নির্মিত একটি UUID আবশ্যক।")
	}
	return id, nil
}

func optionalUUID(raw string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(raw)
}

// withGaps returns every consent type, with the ones never asked about marked Absent.
func withGaps(patientID uuid.UUID, have []Record) []Record {
	byType := make(map[Type]Record, len(have))
	for _, record := range have {
		byType[record.ConsentType] = record
	}
	out := make([]Record, 0, len(Types))
	for _, kind := range Types {
		if record, ok := byType[kind]; ok {
			out = append(out, record)
			continue
		}
		out = append(out, Record{PatientID: patientID, ConsentType: kind, Status: Absent})
	}
	return out
}

func translate(err error) error {
	var denied Denied
	switch {
	case errors.As(err, &denied):
		return errs.ErrForbidden.WithDetail(err)
	case errors.Is(err, ErrNoTemplate):
		// 503, not 422. The wording is a legal dependency (D-02) and its absence is a
		// deployment that is not finished, not a request that is wrong — and a 422 would
		// send a registration desk looking for a mistake it did not make.
		return errs.ErrUnavailable.WithDetail(err)
	case errors.Is(err, ErrUnknownType), errors.Is(err, ErrNotGranted),
		errors.Is(err, ErrWitnessRequired), errors.Is(err, ErrEvidenceRequired):
		return errs.ErrValidation.WithDetail(err)
	case errors.Is(err, ErrReplayed):
		return errs.ErrConflict.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoDevice):
		return errs.ErrDeviceRequired.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoRole):
		return errs.ErrForbidden.WithDetail(err)
	case errors.Is(err, eventstore.ErrNoPrincipal):
		return errs.ErrUnauthenticated.WithDetail(err)
	}
	return err
}

func sourceOf(r *http.Request) eventstore.Source {
	principal, ok := httpx.PrincipalFrom(r.Context())
	if ok && principal.DeviceID != "" {
		return eventstore.SourceMobileOnline
	}
	return eventstore.SourceWeb
}
