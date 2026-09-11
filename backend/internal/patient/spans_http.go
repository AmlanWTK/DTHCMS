package patient

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The scrubbable timeline over HTTP (CP74, §8).
//
// `GET /v1/patients/{id}/timeline/spans?from=&to=&series=`
//
// One response, bounded by what can be drawn. The reasoning for the shape is in spans.go;
// what this file adds is the three things a handler owes:
//
//  1. **The read door, not the write one.** `eventstore.ReaderFrom`, never `ActorFrom`.
//     ActorFrom builds a write envelope and refuses any session with no enrolled device —
//     which is every browser — and this is a browser screen. `go run ./tools/dthclint
//     readpath` is what keeps that true; 28 GET routes had it the other way round until
//     this checkpoint, and every one of them answered `DEVICE_REQUIRED` to a plain read.
//  2. **Permissions off the verified caller.** `httpx.CallerFrom`, passed into the SQL. Not
//     a query parameter: a client that could name the permissions to filter by could name
//     all of them.
//  3. **Audited as a record opening**, with `by: timeline`. The same kind CP31's search and
//     CP37's timeline use, because a review asking "who opened this patient's record" must
//     get one answer rather than one per screen somebody remembered to instrument.

func (h *Handlers) mountSpans(p chi.Router) {
	read := httpx.Permission(PermPatientReadDemographics)
	p.Method("GET", "/{id}/timeline/spans", httpx.Declare(read, h.timelineSpans))
}

func (h *Handlers) timelineSpans(w http.ResponseWriter, r *http.Request) {
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	reader, err := eventstore.ReaderFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}

	// The patient must exist and be this facility's before anything is read: a chart for an
	// id the caller may not see is a way to learn that the id exists.
	if _, err := h.store.ByID(r.Context(), id, reader.FacilityID()); err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}

	query, bad := spansQuery(r, caller.Permissions)
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}

	view, err := h.store.Spans(r.Context(), id, reader.FacilityID(), query, h.series)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateSpans(err))
		return
	}

	h.recordAccess(r, AccessEntry{
		Kind: "patient.viewed", ActorID: reader.UserID(), ActorCode: reader.Code(),
		ActorRole: reader.Role(), FacilityID: reader.FacilityID(),
		PatientID: &id, At: h.clock.Now(),
		Count: countMarks(view), By: "timeline",
	})
	httpx.WriteJSON(w, http.StatusOK, view)
}

// countMarks is what reaches the audit trail: how much of the record was drawn.
//
// A count, never content. The trail is read by administrators who may hold no clinical
// permission at all, and a line naming which drugs were on screen would put the record into
// the table that exists to police access to it.
func countMarks(view SpansView) int {
	total := 0
	for _, lane := range view.Lanes {
		total += len(lane.Marks)
	}
	for _, series := range view.Series {
		total += len(series.Points)
	}
	return total
}

func spansQuery(r *http.Request, permissions []string) (SpansQuery, error) {
	query := SpansQuery{Permissions: permissions}
	values := r.URL.Query()

	if raw := values.Get("from"); raw != "" {
		at, err := parseWhen(raw)
		if err != nil {
			return SpansQuery{}, errs.ErrValidation.WithFieldIn("from",
				"Use a date like 2026-01-31, or a full timestamp.",
				"2026-01-31 এর মতো একটি তারিখ বা সম্পূর্ণ সময় দিন।")
		}
		query.From = at
	}
	if raw := values.Get("to"); raw != "" {
		at, err := parseWhen(raw)
		if err != nil {
			return SpansQuery{}, errs.ErrValidation.WithFieldIn("to",
				"Use a date like 2026-01-31, or a full timestamp.",
				"2026-01-31 এর মতো একটি তারিখ বা সম্পূর্ণ সময় দিন।")
		}
		// A date-only `to` means the whole of that day, exactly as on CP37's route. Somebody
		// asking for 1 to 31 January means January, and an exclusive bound at midnight
		// silently drops the last day — which on a chart is a missing mark rather than a
		// missing row, and therefore even harder to notice.
		if len(raw) == len("2006-01-02") {
			at = at.AddDate(0, 0, 1)
		}
		query.To = at
	}
	if !query.From.IsZero() && !query.To.IsZero() && !query.To.After(query.From) {
		return SpansQuery{}, errs.ErrValidation.WithFieldIn("to",
			"The end of the range must be after its start.",
			"সময়সীমার শেষ অবশ্যই শুরুর পরে হতে হবে।")
	}

	// `series` absent means the plan's defaults; `series=` present and empty means the
	// reader turned every overlay off, which is a legitimate thing to ask for and must not
	// be rounded up to the defaults.
	if raw, present := values["series"]; present {
		query.Codes = []string{}
		for _, part := range strings.Split(strings.Join(raw, ","), ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				query.Codes = append(query.Codes, trimmed)
			}
		}
	} else {
		query.Codes = append([]string{}, DefaultSeriesCodes...)
	}
	return query, nil
}

func translateSpans(err error) error {
	if errors.Is(err, ErrBadSeriesCode) {
		return errs.ErrValidation.WithFieldIn("series",
			"Use observation codes, comma separated — HBA1C, BODY_WEIGHT.",
			"অবজারভেশন কোড কমা দিয়ে দিন — HBA1C, BODY_WEIGHT।")
	}
	if errors.Is(err, ErrTooManySeries) {
		return errs.ErrValidation.WithFieldIn("series",
			"At most "+strconv.Itoa(SpansMaxCodes)+" value series on one chart.",
			"একটি চার্টে সর্বোচ্চ "+strconv.Itoa(SpansMaxCodes)+"টি মানের সিরিজ দেওয়া যায়।")
	}
	return translateForClient(err)
}
