package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The attribution directory (CP61, §4.2, [R-03]).
//
// # Why a directory rather than a name on every value
//
// §4.2 asks that a reviewer see who entered a value **instantly, without digging**. Every
// clinical read already carries the author's id, their role at the time, the station and the
// device; what it does not carry is a name, and a uuid answers a different question from the one
// a physician is asking.
//
// The obvious fix — join the staff record into every clinical query — was rejected. A patient's
// timeline is hundreds of values written by a dozen people; joining two rows onto each of them
// copies the same twelve names into every payload all day, and it means every future clinical
// read has to remember to do the join or quietly render a blank. This is one small request per
// session instead, cached by the client, and it makes the name a *lookup* rather than a
// property of the value — which is what it actually is.
//
// # What is in it, and what is deliberately not
//
// Names, staff codes, device names, station names. No contact details, no credentials, no role
// grants: the role a value carries is the role its author was wearing at the time, and that is
// on the value rather than on the person.
//
// **Deactivated staff and retired devices stay listed.** Attribution on a value taken last March
// names whoever took it, and much of what a reviewer asks about is somebody who has since left. A
// directory of current staff only would render a blank for exactly the person the question is
// about.
//
// # Why a session is the only requirement
//
// Every clinical screen renders attribution, so every role that may see a value may see who
// entered it — the plan says so in as many words. There is no patient in this response, so
// gating it behind a clinical permission would only mean a station app that cannot name the
// colleague whose value it is showing.

// DirectoryPerson is one member of staff, as attribution needs them.
type DirectoryPerson struct {
	ID     uuid.UUID `json:"id"`
	Code   string    `json:"code"`
	NameEN string    `json:"name_en"`
	NameBN string    `json:"name_bn"`
	// Status is active, suspended or deactivated. Reported so a screen can say "no longer at
	// the clinic" rather than presenting a departed colleague as somebody to go and ask.
	Status string `json:"status"`
}

// DirectoryDevice is one tablet or phone.
type DirectoryDevice struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	Status string    `json:"status"`
}

// DirectoryStation is one station, named in both languages.
type DirectoryStation struct {
	Code     string `json:"code"`
	NameEN   string `json:"name_en"`
	NameBN   string `json:"name_bn"`
	Sequence int    `json:"sequence"`
}

// Directory is everything a client needs to render an attribution in words.
type Directory struct {
	Staff    []DirectoryPerson  `json:"staff"`
	Devices  []DirectoryDevice  `json:"devices"`
	Stations []DirectoryStation `json:"stations"`
	// AsOf is when this was read. A client caching the directory for an hour can say how old
	// its answer is rather than presenting a stale list as current.
	AsOf time.Time `json:"as_of"`
}

// Directory reads it.
func (s *PostgresStore) Directory(ctx context.Context, facility uuid.UUID) (Directory, error) {
	out := Directory{Staff: []DirectoryPerson{}, Devices: []DirectoryDevice{},
		Stations: []DirectoryStation{}}

	staff, err := s.q.DirectoryStaff(ctx, facility)
	if err != nil {
		return Directory{}, err
	}
	for _, row := range staff {
		out.Staff = append(out.Staff, DirectoryPerson{
			ID: row.ID, Code: row.EmployeeCode, NameEN: row.NameEn, NameBN: row.NameBn,
			Status: row.Status,
		})
	}

	devices, err := s.q.DirectoryDevices(ctx, facility)
	if err != nil {
		return Directory{}, err
	}
	for _, row := range devices {
		out.Devices = append(out.Devices, DirectoryDevice{
			ID: row.ID, Name: row.Name, Kind: row.Kind, Status: row.Status,
		})
	}

	stations, err := s.q.DirectoryStations(ctx, facility)
	if err != nil {
		return Directory{}, err
	}
	for _, row := range stations {
		out.Stations = append(out.Stations, DirectoryStation{
			Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn,
			Sequence: int(row.SequenceHint),
		})
	}
	return out, nil
}

// DirectoryHandlers serve /v1/directory (CP61).
type DirectoryHandlers struct {
	store  *PostgresStore
	clock  interface{ Now() time.Time }
	logger *slog.Logger
}

// DirectoryHandlersConfig assembles it.
type DirectoryHandlersConfig struct {
	Store  *PostgresStore
	Clock  interface{ Now() time.Time }
	Logger *slog.Logger
}

func NewDirectoryHandlers(cfg DirectoryHandlersConfig) *DirectoryHandlers {
	return &DirectoryHandlers{store: cfg.Store, clock: cfg.Clock, logger: cfg.Logger}
}

// Mount attaches GET /v1/directory.
func (h *DirectoryHandlers) Mount(r chi.Router) {
	r.Method("GET", "/directory", httpx.Declare(httpx.Session(), h.directory))
}

func (h *DirectoryHandlers) directory(w http.ResponseWriter, r *http.Request) {
	// The *caller*, not the principal: a principal is what the permission decision leaves
	// behind, and this route asks for no permission. A handler that read one here would refuse
	// every request while looking like an authentication failure.
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}
	facility, err := uuid.Parse(caller.FacilityID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated.WithDetail(err))
		return
	}
	if h.store == nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal)
		return
	}

	directory, err := h.store.Directory(r.Context(), facility)
	if err != nil {
		httpx.WriteError(w, r, h.logger, errs.ErrInternal.WithDetail(err))
		return
	}
	directory.AsOf = h.now()
	httpx.WriteJSON(w, http.StatusOK, directory)
}

func (h *DirectoryHandlers) now() time.Time {
	if h.clock == nil {
		return time.Now().UTC()
	}
	return h.clock.Now().UTC()
}
