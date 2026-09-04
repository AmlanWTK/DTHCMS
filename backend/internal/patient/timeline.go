package patient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The patient timeline (CP37, blueprint §8).
//
// One chronological read of everything known about a patient. The physician dashboard, the
// timeline visualisation, the AI synthesis and the records chronology all read this rather
// than each writing its own query over the ledger — four queries is four places for a fact to
// be missing from one of them, and the one it is missing from is always the one somebody is
// looking at.
//
// The **permission filter is in the SQL**, not applied to the results in Go. A post-filter is
// how a count comes back larger than the rows returned, and how a paging cursor skips what it
// hid — the second is worse, because the user sees a page that is short and has no way to know
// why.

// TimelineEntry is one line, in the shape every kind shares.
type TimelineEntry struct {
	OccurredAt time.Time `json:"occurred_at"`
	// RecordedAt is when it reached the system. A vital taken at 09:10 and entered at 11:40
	// belongs at 09:10, and the difference is itself worth seeing.
	RecordedAt time.Time `json:"recorded_at"`

	Category string   `json:"category"`
	Kind     string   `json:"kind"`
	LabelEN  string   `json:"label_en"`
	LabelBN  string   `json:"label_bn"`
	Value    string   `json:"value,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	ValueNum *float64 `json:"value_num,omitempty"`

	// Attribution, on every row (§8). Denormalised at projection time: attribution resolved
	// by a join is attribution that disappears when the join is expensive or when the person
	// who recorded it has left.
	ActorCode    string `json:"actor_code"`
	ActorRole    string `json:"actor_role"`
	ActorStation string `json:"actor_station,omitempty"`
	Source       string `json:"source,omitempty"`

	Flags []string `json:"flags"`

	EventID   uuid.UUID `json:"event_id"`
	EventType string    `json:"event_type"`
	Item      string    `json:"item,omitempty"`
}

// TimelineQuery is what a screen asks for.
type TimelineQuery struct {
	From time.Time
	To   time.Time
	// Categories narrows to "medication only" and similar. Empty means every category the
	// caller may see.
	Categories []string
	// Permissions is what the caller actually holds. Never defaulted and never widened here:
	// the caller passes what the session says, and a handler that forgets gets no rows
	// rather than every row.
	Permissions []string
	Limit       int
	Offset      int
}

// TimelinePage is one page of the timeline, with the total so a screen can page honestly.
type TimelinePage struct {
	Entries []TimelineEntry `json:"entries"`
	Total   int64           `json:"total"`
	// Earliest and Latest are the whole span on record, so a screen can offer a range
	// rather than guess one — and so "nothing in this window" is distinguishable from
	// "nothing at all".
	Earliest *time.Time `json:"earliest,omitempty"`
	Latest   *time.Time `json:"latest,omitempty"`
}

// TimelineCategories are the families a filter may offer. Closed, because a new category is
// a decision; `kind` inside one is open, because a new observation type is not.
var TimelineCategories = []string{
	"registration", "visit", "observation", "diagnosis", "medication",
	"document", "communication", "alert", "consent", "administrative",
}

// ErrUnknownCategory is a filter naming a category this deployment does not have. Refused
// rather than ignored: silently returning everything is how a "medication only" screen shows
// a diagnosis to somebody who filtered it out.
var ErrUnknownCategory = errors.New("patient: that is not a timeline category")

// TimelineMaxPage is the largest page the API will return. A decade of a diabetic patient's
// history is thousands of rows and nothing renders them all at once.
const TimelineMaxPage = 500

// Timeline reads one patient's timeline.
func (s *Store) Timeline(ctx context.Context, patientID, facility uuid.UUID, q TimelineQuery) (TimelinePage, error) {
	for _, category := range q.Categories {
		if !knownCategory(category) {
			return TimelinePage{}, fmt.Errorf("%w: %q", ErrUnknownCategory, category)
		}
	}
	if len(q.Permissions) == 0 {
		// Deliberately empty rather than everything. A caller that passed nothing has a bug,
		// and the safe reading of a bug in a permission filter is "no rows".
		return TimelinePage{Entries: []TimelineEntry{}}, nil
	}
	if q.Limit <= 0 || q.Limit > TimelineMaxPage {
		q.Limit = TimelineMaxPage
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	if q.From.IsZero() {
		q.From = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if q.To.IsZero() {
		q.To = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if q.Categories == nil {
		q.Categories = []string{}
	}

	rows, err := s.q.PatientTimeline(ctx, dbgen.PatientTimelineParams{
		PatientID: patientID, FacilityID: facility,
		OccurredAt: q.From, OccurredAt_2: q.To,
		Categories: q.Categories, Permissions: q.Permissions,
		Limit: int32(q.Limit), Offset: int32(q.Offset), //nolint:gosec // both are bounded above
	})
	if err != nil {
		return TimelinePage{}, err
	}

	total, err := s.q.PatientTimelineCount(ctx, dbgen.PatientTimelineCountParams{
		PatientID: patientID, FacilityID: facility,
		OccurredAt: q.From, OccurredAt_2: q.To,
		Categories: q.Categories, Permissions: q.Permissions,
	})
	if err != nil {
		return TimelinePage{}, err
	}

	earliest, latest, err := s.timelineSpan(ctx, patientID, facility, q.Permissions)
	if err != nil {
		return TimelinePage{}, err
	}

	page := TimelinePage{Entries: make([]TimelineEntry, 0, len(rows)), Total: total}
	for _, r := range rows {
		page.Entries = append(page.Entries, entryOf(r))
	}
	page.Earliest, page.Latest = earliest, latest
	return page, nil
}

// timelineSpan is the first and last thing on record, whatever the requested window.
//
// Hand-written rather than generated. `min()` over an empty set is NULL of an indeterminate
// type and sqlc emits `interface{}`; written as ordered subqueries it emits a non-nullable
// `time.Time`, which fails to scan the NULL a patient with no timeline produces. Both
// generated shapes are wrong in a way that only shows up on the empty case — which is every
// patient on their first day.
func (s *Store) timelineSpan(ctx context.Context, patientID, facility uuid.UUID,
	permissions []string) (*time.Time, *time.Time, error) {
	var earliest, latest *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT min(occurred_at), max(occurred_at)
		  FROM read.patient_timeline
		 WHERE patient_id = $1 AND facility_id = $2 AND needs_permission = ANY($3::text[])`,
		patientID, facility, permissions).Scan(&earliest, &latest)
	if err != nil {
		return nil, nil, err
	}
	if earliest != nil {
		at := earliest.UTC()
		earliest = &at
	}
	if latest != nil {
		at := latest.UTC()
		latest = &at
	}
	return earliest, latest, nil
}

func entryOf(r dbgen.PatientTimelineRow) TimelineEntry {
	entry := TimelineEntry{
		OccurredAt: r.OccurredAt.UTC(), RecordedAt: r.RecordedAt.UTC(),
		Category: r.Category, Kind: r.Kind,
		LabelEN: r.LabelEn, LabelBN: r.LabelBn,
		Value: r.Value, Unit: r.Unit,
		ActorCode: r.ActorCode, ActorRole: r.ActorRole, ActorStation: r.ActorStation,
		Source: r.Source, Flags: r.Flags,
		EventID: r.EventID, EventType: r.EventType, Item: r.Item,
	}
	if entry.Flags == nil {
		entry.Flags = []string{}
	}
	if value, ok := numericValue(r.ValueNum); ok {
		entry.ValueNum = &value
	}
	return entry
}

func numericValue(n pgtype.Numeric) (float64, bool) {
	if !n.Valid {
		return 0, false
	}
	value, err := n.Float64Value()
	if err != nil || !value.Valid {
		return 0, false
	}
	return value.Float64, true
}

func knownCategory(name string) bool {
	for _, candidate := range TimelineCategories {
		if candidate == name {
			return true
		}
	}
	return false
}
