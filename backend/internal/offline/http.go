package offline

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

// The sync protocol over HTTP (CP65).
//
// # Who may push, and why it is not a new permission
//
// Pushing is not a privileged act: it is the same clinical write the station already had the right
// to make, arriving later. So the guard on `POST /v1/sync/events` is **the union of every write
// permission a station holds**, and what each individual event may do is decided where it always
// was — by the validation and the projections the ledger runs on it.
//
// A separate `sync.push` permission would have been simpler to write and wrong in a specific way:
// it would be a permission that means "may write clinical data", held by everybody who has any of
// the others, and the first access review to look at it would grant it to somebody who should have
// had none of them.
//
// Pulling is `observation.read.values` or better — see `Pullable()`, where each type names its own.

const (
	// PermQuarantineRead is seeing what was held, including its clinical content. Sensitive: it
	// is the only permission in this system that shows a clinical value from outside the ledger.
	PermQuarantineRead = "sync.quarantine.read"
	// PermQuarantineRelease is deciding whether a held event enters the permanent record.
	PermQuarantineRelease = "sync.quarantine.release"
)

// writePermissions is every permission that lets a station record something. A caller holding any
// of them may push a batch; what is in the batch is judged event by event.
var writePermissions = []string{
	"observation.write.anthro",
	"observation.write.vitals",
	"observation.write.lifestyle",
	"observation.write.history",
	"observation.write.nutrition",
	"observation.write.exercise",
	"observation.write.exam",
	"counseling.tick",
	"allergy.write",
	"history.write",
	"visit.attend",
}

// Handlers serve the sync protocol.
type Handlers struct {
	service *Service
	store   *Store
	clock   interface{ Now() time.Time }
	logger  *slog.Logger
}

// HandlersConfig builds Handlers.
type HandlersConfig struct {
	Service *Service
	Store   *Store
	Clock   interface{ Now() time.Time }
	Logger  *slog.Logger
}

// NewHandlers builds them.
func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{service: cfg.Service, store: cfg.Store, clock: cfg.Clock, logger: cfg.Logger}
}

// Mount attaches /v1/sync.
func (h *Handlers) Mount(r chi.Router) {
	push := httpx.Permission(writePermissions...)
	pull := httpx.Permission("observation.read.values", "patient.read.demographics", "visit.read")
	read := httpx.Permission(PermQuarantineRead)
	release := httpx.Permission(PermQuarantineRelease)

	r.Route("/sync", func(s chi.Router) {
		s.Method("POST", "/events", httpx.Declare(push, h.push))
		s.Method("GET", "/events", httpx.Declare(pull, h.pull))
		// The receipt, for a client that lost the response to its own push.
		s.Method("GET", "/batches/{id}", httpx.Declare(push, h.receipt))
		// Reference fingerprints: one small request that tells a phone whether the catalogues it
		// is holding for the morning have moved.
		s.Method("GET", "/reference", httpx.Declare(pull, h.reference))
		s.Method("GET", "/state", httpx.Declare(push, h.state))

		s.Method("GET", "/quarantine", httpx.Declare(read, h.quarantine))
		s.Method("GET", "/quarantine/{id}", httpx.Declare(read, h.heldEvent))
		s.Method("POST", "/quarantine/{id}/release", httpx.Declare(release, h.release))
		s.Method("POST", "/quarantine/{id}/discard", httpx.Declare(release, h.discard))
	})
}

func (h *Handlers) push(w http.ResponseWriter, r *http.Request) {
	var body Batch
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if body.BatchID == uuid.Nil {
		// The batch id is the client's receipt number. Without it a lost response is
		// unrecoverable, which is the one failure this endpoint exists to survive.
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("batch_id",
			"Every batch needs its own identifier, so its result can be asked for again.",
			"প্রতিটি ব্যাচের নিজস্ব পরিচিতি থাকতে হবে, যাতে ফলাফল আবার জানা যায়।"))
		return
	}
	// `device_id` is optional now, and checked rather than trusted: the sending device comes from
	// the signature the middleware verified. A client that sends it is telling us which device it
	// believes it is, and a mismatch is refused.

	receipt, err := h.service.Push(r.Context(), body)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSync(err))
		return
	}
	if receipt.Quarantined > 0 {
		// The one log line an operator's attention hangs on. At warn rather than info: work that
		// was held is work that will not be in the record unless a person acts.
		h.logger.WarnContext(r.Context(), "offline events were held for review",
			"device_id", body.DeviceID.String(), "batch_id", body.BatchID.String(),
			"held", receipt.Quarantined, "of", receipt.Events)
	}
	// 200 rather than 201 even when events were created, and deliberately: the interesting
	// answer is the per-event body, and a status code that said "created" for a batch in which
	// half the events were held would be the wrong headline.
	httpx.WriteJSON(w, http.StatusOK, receipt)
}

func (h *Handlers) receipt(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	receipt, found, err := h.store.Receipt(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	if !found {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return
	}
	receipt.ServerTime = h.clock.Now().UTC()
	httpx.WriteJSON(w, http.StatusOK, receipt)
}

func (h *Handlers) pull(w http.ResponseWriter, r *http.Request) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	since := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("since",
				"The cursor is a sequence number from a previous page.",
				"কার্সারটি আগের পাতার একটি ক্রমিক সংখ্যা।"))
			return
		}
		since = parsed
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	page, err := h.service.Pull(r.Context(), since, limit, caller.Permissions)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSync(err))
		return
	}
	// Where this device has got to, recorded so that a phone which was wiped and re-enrolled has
	// somewhere to resume from other than the beginning of time. Best-effort: a cursor that was
	// not written is a page the client already has, and failing the read over it would be worse.
	//
	// The device comes from the **principal** rather than the caller: the principal is what the
	// authorisation chain verified, and the caller is what the session claimed. They agree, and
	// taking the verified one costs nothing and cannot be wrong.
	if device, err := deviceOf(r); err == nil && page.Cursor > since {
		if err := h.service.NotePulled(r.Context(), device, page.Cursor); err != nil {
			h.logger.WarnContext(r.Context(), "could not record a device's sync cursor",
				"error", err.Error())
		}
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handlers) reference(w http.ResponseWriter, r *http.Request) {
	versions, err := h.store.Reference(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"catalogues": versions,
		// On the payload so a client can measure its own skew from the same response that tells
		// it what to re-fetch, rather than making a second request to find out.
		"server_time": h.clock.Now().UTC(),
	})
}

func (h *Handlers) state(w http.ResponseWriter, r *http.Request) {
	device, err := deviceOf(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("device",
			"This request did not come from an enrolled device.",
			"এই অনুরোধটি নিবন্ধিত কোনও যন্ত্র থেকে আসেনি।"))
		return
	}
	state, err := h.store.State(r.Context(), device)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"state": state, "server_time": h.clock.Now().UTC(),
	})
}

func (h *Handlers) quarantine(w http.ResponseWriter, r *http.Request) {
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	facility, err := uuid.Parse(caller.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	// Held by default. The list exists to be worked through, and one that opened on everything
	// ever resolved would bury the four things somebody has to decide about this morning.
	status := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	if status == "" {
		status = "HELD"
	}
	if status == "ALL" {
		status = ""
	} else if status != "HELD" && status != "RELEASED" && status != "DISCARDED" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("status",
			"That is not a quarantine status.", "এটি কোনও কোয়ারেন্টিন অবস্থা নয়।"))
		return
	}
	var device *uuid.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("device_id")); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("device_id",
				"That is not a device identifier.", "এটি কোনও যন্ত্রের পরিচিতি নয়।"))
			return
		}
		device = &parsed
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	held, err := h.store.Held(r.Context(), facility, status, device, limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"held": held})
}

func (h *Handlers) heldEvent(w http.ResponseWriter, r *http.Request) {
	id, facility, ok := h.heldID(w, r)
	if !ok {
		return
	}
	held, err := h.store.HeldEvent(r.Context(), id, facility)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSync(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"held": held})
}

type decisionRequest struct {
	Note string `json:"note"`
}

func (h *Handlers) release(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, true)
}

func (h *Handlers) discard(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, false)
}

func (h *Handlers) decide(w http.ResponseWriter, r *http.Request, release bool) {
	id, _, ok := h.heldID(w, r)
	if !ok {
		return
	}
	var body decisionRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	note := strings.TrimSpace(body.Note)
	if note == "" {
		// Required for both, not only for a discard. A measurement admitted to a patient's
		// permanent record from a device somebody had refused to trust is a decision that needs
		// a sentence beside it as much as a refusal does — more, since it is the one that is
		// harder to undo.
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("note",
			"Say why this decision was made.", "এই সিদ্ধান্তের কারণ লিখুন।"))
		return
	}

	var (
		held Held
		err  error
	)
	if release {
		held, err = h.service.Release(r.Context(), id, note)
	} else {
		held, err = h.service.Discard(r.Context(), id, note)
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSync(err))
		return
	}
	h.logger.WarnContext(r.Context(), "a held offline event was decided about",
		"quarantine_id", held.ID.String(), "outcome", held.Status,
		"event_type", held.EventType)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"held": held})
}

// deviceOf is the device the authorisation chain verified, not the one the session claimed. They
// agree; taking the verified one costs nothing and cannot be wrong.
func deviceOf(r *http.Request) (uuid.UUID, error) {
	principal, ok := httpx.PrincipalFrom(r.Context())
	if !ok {
		return uuid.Nil, errs.ErrUnauthenticated
	}
	return uuid.Parse(principal.DeviceID)
}

func (h *Handlers) heldID(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return uuid.Nil, uuid.Nil, false
	}
	facility, err := uuid.Parse(caller.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return uuid.Nil, uuid.Nil, false
	}
	return id, facility, true
}

func translateSync(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrUnknownDevice), errors.Is(err, ErrWrongFacility):
		// One answer for both. Whether a device exists and belongs to another clinic is itself
		// something a distinguishing error would disclose.
		return errs.ErrNotFound
	case errors.Is(err, ErrDeviceMismatch):
		// Refused rather than quietly overridden with the verified device: a body naming a
		// different one means the client is confused about which device it is, and carrying on
		// would record something neither side meant.
		return errs.ErrValidation.WithFieldIn("device_id",
			"This batch names a device other than the one that sent it.",
			"এই ব্যাচে যে যন্ত্রের কথা বলা হয়েছে, সেটি পাঠানো যন্ত্র নয়।")
	case errors.Is(err, ErrEmptyBatch):
		return errs.ErrValidation.WithFieldIn("events",
			"A batch needs at least one event.", "একটি ব্যাচে অন্তত একটি ঘটনা থাকতে হবে।")
	case errors.Is(err, ErrBatchTooLarge):
		return errs.New("SYNC_BATCH_TOO_LARGE", errs.KindValidation, http.StatusUnprocessableEntity,
			"That batch is too large. Send it in smaller pieces — nothing is lost by splitting it.",
			"এই ব্যাচটি অনেক বড়। ছোট ছোট ভাগে পাঠান — ভাগ করলে কিছুই হারাবে না।").WithDetail(err)
	case errors.Is(err, ErrQuarantineFull):
		// 429 rather than 409, and the sentence says what clears it. This is not a conflict with
		// something the client did — it is a budget that a *person* refills by working through the
		// triage list, so a client that retried in a second would be right to be told to wait and
		// wrong to be told to change anything. No Retry-After: the honest answer is "when somebody
		// has looked at it", and a number would be a guess a client would then obey.
		return errs.New("SYNC_QUARANTINE_FULL", errs.KindValidation, http.StatusTooManyRequests,
			"This device has as many events waiting to be reviewed as it may have. "+
				"Somebody at the clinic must work through them before it can send more.",
			"এই যন্ত্রের যতগুলো ঘটনা পর্যালোচনার অপেক্ষায় থাকতে পারে, ততগুলোই আছে। "+
				"আরও পাঠানোর আগে ক্লিনিকের কাউকে সেগুলো দেখে সিদ্ধান্ত নিতে হবে।").WithDetail(err)
	case errors.Is(err, ErrAlreadyResolved):
		return errs.New("SYNC_ALREADY_RESOLVED", errs.KindConflict, http.StatusConflict,
			"Somebody has already decided about this event.",
			"এই ঘটনাটি নিয়ে কেউ ইতিমধ্যে সিদ্ধান্ত নিয়েছেন।").WithDetail(err)
	}
	return errs.ErrInternal.WithDetail(err)
}
