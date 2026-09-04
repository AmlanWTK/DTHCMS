package patient

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// Photographs over HTTP (CP34).
//
// Three endpoints, and the shape of them is the design: the API issues a URL, the client
// uploads to storage, the API is told. The bytes never come here.

func (h *Handlers) mountPhoto(p chi.Router) {
	write := httpx.Permission(PermPatientWriteDemographics)
	read := httpx.Permission(PermPatientReadDemographics)
	p.Method("POST", "/{id}/photo/upload-url", httpx.Declare(write, h.photoUploadURL))
	p.Method("POST", "/{id}/photo", httpx.Declare(write, h.attachPhoto))
	p.Method("GET", "/{id}/photo", httpx.Declare(read, h.viewPhoto))
}

type uploadURLRequest struct {
	ContentType string `json:"content_type"`
}

func (h *Handlers) photoUploadURL(w http.ResponseWriter, r *http.Request) {
	if h.photos == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable)
		return
	}
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	var req uploadURLRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	ticket, err := h.photos.IssueUpload(r.Context(), id, req.ContentType)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translatePhoto(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ticket)
}

type attachRequest struct {
	EventID     string `json:"event_id"`
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

func (h *Handlers) attachPhoto(w http.ResponseWriter, r *http.Request) {
	if h.photos == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable)
		return
	}
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	var req attachRequest
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

	photo, err := h.photos.Attach(r.Context(), id, AttachPhoto{
		EventID: eventID, ObjectKey: req.ObjectKey, ContentType: req.ContentType,
		Width: req.Width, Height: req.Height, Source: sourceOf(r),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, translatePhoto(err))
		return
	}
	// The response carries a fresh view URL, so the tablet that just uploaded can show the
	// photograph without a second round trip.
	url, expires, err := h.photos.ViewURL(r.Context(), id, blobstore.MaxSignedTTL)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translatePhoto(err))
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"photo": map[string]any{
			"object_key":     photo.ObjectKey,
			"content_type":   photo.ContentType,
			"byte_size":      photo.ByteSize,
			"captured_at":    photo.CapturedAt,
			"url":            url,
			"url_expires_at": expires,
		},
	})
}

func (h *Handlers) viewPhoto(w http.ResponseWriter, r *http.Request) {
	if h.photos == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnavailable)
		return
	}
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	// A caller may ask for less than the cap but never more. A wall display wants a longer
	// URL than a form; neither gets to choose the ceiling.
	ttl := blobstore.MaxSignedTTL
	if raw := r.URL.Query().Get("ttl_seconds"); raw != "" {
		if seconds := atoiOr(raw, 0); seconds > 0 && time.Duration(seconds)*time.Second < ttl {
			ttl = time.Duration(seconds) * time.Second
		}
	}
	url, expires, err := h.photos.ViewURL(r.Context(), id, ttl)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translatePhoto(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"url": url, "expires_at": expires})
}

// patientParam reads and validates the id, answering a bad one the same way as an unknown
// one: a 404 that distinguishes them is a way to learn which patients exist.
func (h *Handlers) patientParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, false
	}
	return id, true
}

func translatePhoto(err error) error {
	switch {
	case errors.Is(err, ErrNoPhoto):
		return errs.ErrNotFound.WithDetail(err)
	case errors.Is(err, ErrPhotoNotUploaded):
		return errs.ErrValidation.WithFieldIn("object_key",
			"The photograph was not uploaded. Try taking it again.",
			"ছবিটি আপলোড হয়নি। আবার তুলুন।").WithDetail(err)
	case errors.Is(err, ErrPhotoMismatch):
		return errs.ErrValidation.WithFieldIn("content_type",
			"That file is not a photograph this clinic can store.",
			"এই ফাইলটি ক্লিনিক সংরক্ষণ করতে পারে না।").WithDetail(err)
	}
	return translateForClient(err)
}
