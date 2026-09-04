package terminology

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The coded catalogue over HTTP (CP52).
//
// # Why this is not under /v1/observations
//
// A terminology is not a measurement and not a patient. These four endpoints answer questions
// about the WHO's classification and the clinic's own complaint dictionary, and the answers
// are identical for every person in the building — which is why they are cacheable, why they
// carry no facility scope, and why they sit at the top level rather than hanging off a record.
//
// # Why the version comes back on every row
//
// Criterion 2 says every coding stores its system and its version. A client that never names
// a version still receives the resolved one in each result, so the picker has both halves in
// hand at the moment the clinician chooses — rather than the recording code guessing later,
// which is how a diagnosis ends up stamped with a version nobody searched.
//
// # Why nothing here is audited
//
// There is no patient in a terminology search. Recording "somebody typed 'dia'" would create
// a trail with no subject and no use, and the audit trail is not a keystroke log.

// PermRead guards all of it. Reading the classification is not reading a patient — see the
// note in the migration that grants it.
const PermRead = "terminology.read"

type Handlers struct {
	store  *Store
	logger *slog.Logger
}

type HandlersConfig struct {
	Store  *Store
	Logger *slog.Logger
}

func NewHandlers(cfg HandlersConfig) *Handlers {
	return &Handlers{store: cfg.Store, logger: cfg.Logger}
}

// Mount attaches the catalogue under /v1/terminology.
func (h *Handlers) Mount(r chi.Router) {
	read := httpx.Permission(PermRead)
	r.Route("/terminology", func(t chi.Router) {
		// Which terminologies exist and what may be done with each. The licence note is part
		// of the answer: a client that cannot offer SNOMED should be able to say why.
		t.Method("GET", "/systems", httpx.Declare(read, h.systems))
		// The clinic's twenty, before anybody has typed. Its own endpoint as well as the
		// empty-query behaviour of search, because a picker that opens on a list should not
		// have to send a search to get one.
		t.Method("GET", "/favourites", httpx.Declare(read, h.favourites))
		t.Method("GET", "/search", httpx.Declare(read, h.search))
		// One concept and its mappings, so a screen can render a coding recorded years ago
		// under a version nobody uses any more. Query parameters rather than path segments:
		// an ICD-10 code contains a full stop and a version may contain anything the
		// publisher chose, and neither belongs in a path a proxy might normalise.
		t.Method("GET", "/concept", httpx.Declare(read, h.concept))
	})
}

func (h *Handlers) systems(w http.ResponseWriter, r *http.Request) {
	systems, err := h.store.Systems(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"systems": systems})
}

func (h *Handlers) search(w http.ResponseWriter, r *http.Request) {
	system := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("system")))
	if system == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("system",
			"Name the terminology to search.", "কোন টার্মিনোলজিতে খুঁজবেন তা জানান।"))
		return
	}
	version := strings.TrimSpace(r.URL.Query().Get("version"))

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("limit",
				"Ask for a whole number of results.", "কতগুলো ফলাফল চান, পূর্ণসংখ্যায় লিখুন।"))
			return
		}
		limit = parsed
	}

	// The resolved version is returned beside the results rather than only inside them, so a
	// client with an empty result set still learns which version it just searched — which is
	// what it will stamp on the coding if the clinician types the code by hand.
	resolved, err := h.store.Resolve(r.Context(), system, version)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}

	concepts, err := h.store.Search(r.Context(), system, resolved, r.URL.Query().Get("q"), limit)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"system": system, "version": resolved, "concepts": concepts,
	})
}

func (h *Handlers) favourites(w http.ResponseWriter, r *http.Request) {
	system := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("system")))
	if system == "" {
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn("system",
			"Name the terminology.", "কোন টার্মিনোলজি, তা জানান।"))
		return
	}
	version := strings.TrimSpace(r.URL.Query().Get("version"))

	resolved, err := h.store.Resolve(r.Context(), system, version)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	concepts, err := h.store.Favourites(r.Context(), system, resolved)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"system": system, "version": resolved, "concepts": concepts,
	})
}

func (h *Handlers) concept(w http.ResponseWriter, r *http.Request) {
	system := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("system")))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if system == "" || code == "" {
		field := "system"
		if system != "" {
			field = "code"
		}
		httpx.WriteError(w, r, h.logger, errs.ErrValidation.WithFieldIn(field,
			"Name the terminology and the code.", "টার্মিনোলজি ও কোড দুটোই জানান।"))
		return
	}
	version := strings.TrimSpace(r.URL.Query().Get("version"))

	resolved, err := h.store.Resolve(r.Context(), system, version)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	concept, err := h.store.Concept(r.Context(), system, resolved, code)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	mappings, err := h.store.Mappings(r.Context(), system, resolved, code)
	if err != nil {
		httpx.WriteError(w, r, h.logger, h.translate(err))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"concept": concept, "mappings": mappings,
	})
}

// translate turns the store's sentinels into the four answers a client can act on.
//
// The unusable case is 422 rather than 403 on purpose. 403 would say "you may not" to a
// person who may — the refusal is a property of this deployment's licensing, not of the
// caller's role, and pointing an operator at their permissions when the answer is D-24 sends
// them to the wrong place entirely.
func (h *Handlers) translate(err error) error {
	switch {
	case errors.Is(err, ErrUnknownConcept):
		return errs.ErrNotFound
	case errors.Is(err, ErrUnknownSystem):
		return errs.ErrValidation.WithFieldIn("system",
			"That terminology is not one this clinic holds.",
			"এই ক্লিনিকে ওই টার্মিনোলজি নেই।")
	case errors.Is(err, ErrUnknownVersion):
		return errs.ErrValidation.WithFieldIn("version",
			"That version of the terminology is not loaded here.",
			"ওই সংস্করণটি এখানে লোড করা নেই।")
	case errors.Is(err, ErrNoDefaultVersion):
		return errs.ErrValidation.WithFieldIn("version",
			"No version of that terminology has been loaded yet. Name one explicitly.",
			"ওই টার্মিনোলজির কোনো সংস্করণ এখনো লোড হয়নি। নির্দিষ্ট করে একটি জানান।")
	case errors.Is(err, ErrUnusableSystem):
		return errs.ErrValidation.WithFieldIn("system",
			"This clinic is not licensed to use that terminology.",
			"ওই টার্মিনোলজি ব্যবহারের লাইসেন্স এই ক্লিনিকের নেই।")
	default:
		return errs.ErrInternal.WithDetail(err)
	}
}
