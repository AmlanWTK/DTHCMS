package patient

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/errs"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// The timeline over HTTP (CP37).
//
// `GET /v1/patients/{id}/timeline?from=&to=&types=`. One route, because the four screens the
// plan names all want the same thing with a different window and a different filter.
//
// **Row-level filtering comes from the caller's own permissions**, read off the verified
// principal rather than passed in. A client that could name the permissions it wanted to
// filter by is a client that could name all of them.

func (h *Handlers) mountTimeline(p chi.Router) {
	read := httpx.Permission(PermPatientReadDemographics)
	p.Method("GET", "/{id}/timeline", httpx.Declare(read, h.timeline))
}

func (h *Handlers) timeline(w http.ResponseWriter, r *http.Request) {
	id, ok := h.patientParam(w, r)
	if !ok {
		return
	}
	actor, err := eventstore.ActorFrom(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}
	// The permissions the session actually holds, off the verified caller. Not a parameter:
	// a client that could name the permissions to filter by could name all of them.
	caller, ok := httpx.CallerFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errs.ErrUnauthenticated)
		return
	}

	// The patient must exist and be this facility's before anything is read: a timeline for
	// an id the caller may not see is a way to learn that the id exists.
	if _, err := h.store.ByID(r.Context(), id, actor.FacilityID()); err != nil {
		httpx.WriteError(w, r, h.logger, translateForClient(err))
		return
	}

	query, bad := timelineQuery(r, caller.Permissions)
	if bad != nil {
		httpx.WriteError(w, r, h.logger, bad)
		return
	}

	page, err := h.store.Timeline(r.Context(), id, actor.FacilityID(), query)
	if err != nil {
		httpx.WriteError(w, r, h.logger, translateTimeline(err))
		return
	}

	// Audited as a record opening. A timeline is the whole record in one response, which is
	// exactly what a bulk read looks like from the outside (CP31).
	h.recordAccess(r, AccessEntry{
		Kind: "patient.viewed", ActorID: actor.UserID(), ActorCode: actor.Code(),
		ActorRole: actor.Role(), FacilityID: actor.FacilityID(),
		PatientID: &id, At: h.clock.Now(),
		Count: len(page.Entries), By: "timeline",
	})
	httpx.WriteJSON(w, http.StatusOK, page)
}

func timelineQuery(r *http.Request, permissions []string) (TimelineQuery, error) {
	query := TimelineQuery{Permissions: permissions, Limit: TimelineMaxPage}

	values := r.URL.Query()
	if raw := values.Get("from"); raw != "" {
		at, err := parseWhen(raw)
		if err != nil {
			return TimelineQuery{}, errs.ErrValidation.WithFieldIn("from",
				"Use a date like 2026-01-31, or a full timestamp.",
				"2026-01-31 এর মতো একটি তারিখ বা সম্পূর্ণ সময় দিন।")
		}
		query.From = at
	}
	if raw := values.Get("to"); raw != "" {
		at, err := parseWhen(raw)
		if err != nil {
			return TimelineQuery{}, errs.ErrValidation.WithFieldIn("to",
				"Use a date like 2026-01-31, or a full timestamp.",
				"2026-01-31 এর মতো একটি তারিখ বা সম্পূর্ণ সময় দিন।")
		}
		// A date-only `to` means the whole of that day. Somebody asking for 1 Jan to 31 Jan
		// means January, and an exclusive bound at midnight silently drops the last day.
		if len(raw) == len("2006-01-02") {
			at = at.AddDate(0, 0, 1)
		}
		query.To = at
	}
	if !query.From.IsZero() && !query.To.IsZero() && !query.To.After(query.From) {
		return TimelineQuery{}, errs.ErrValidation.WithFieldIn("to",
			"The end of the range must be after its start.",
			"সময়সীমার শেষ অবশ্যই শুরুর পরে হতে হবে।")
	}

	if raw := values.Get("types"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				query.Categories = append(query.Categories, trimmed)
			}
		}
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return TimelineQuery{}, errs.ErrValidation.WithFieldIn("limit",
				"How many entries to return, at most "+strconv.Itoa(TimelineMaxPage)+".",
				"সর্বোচ্চ "+strconv.Itoa(TimelineMaxPage)+"টি এন্ট্রি ফেরত দেওয়া হয়।")
		}
		query.Limit = limit
	}
	if raw := values.Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return TimelineQuery{}, errs.ErrValidation.WithFieldIn("offset",
				"How many entries to skip.", "কতগুলো এন্ট্রি বাদ দিতে হবে।")
		}
		query.Offset = offset
	}
	return query, nil
}

// parseWhen takes a date or a full timestamp. A date is read in the clinic's calendar, not
// UTC: a clinician asking for "today" means the day the clinic is having.
func parseWhen(raw string) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, raw); err == nil {
		return at, nil
	}
	return time.ParseInLocation("2006-01-02", raw, Dhaka)
}

func translateTimeline(err error) error {
	if errors.Is(err, ErrUnknownCategory) {
		return errs.ErrValidation.WithFieldIn("types",
			"Unknown entry type. Use: "+strings.Join(TimelineCategories, ", ")+".",
			"অজানা ধরন। ব্যবহার করুন: "+strings.Join(TimelineCategories, ", ")+"।")
	}
	return translateForClient(err)
}
