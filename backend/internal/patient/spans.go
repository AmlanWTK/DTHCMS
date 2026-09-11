package patient

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The timeline in the shape a chart can draw (CP74, blueprint §8).
//
// # Why this is a second endpoint and not a bigger page of the first
//
// `GET /v1/patients/{id}/timeline` (CP37) answers *what happened, newest first, a page at a
// time*, and its own comment says why the page is capped at 500: "a decade of a diabetic
// patient's history is thousands of rows and nothing renders them all at once."
//
// CP74 is the screen that renders a decade at once. That justification does not cover this
// consumer, and the two available answers were both wrong:
//
//   - **Raise the cap.** The cap is not arbitrary — it is what stops a paging list from
//     shipping a megabyte to a phone. Raising it for one screen raises it for every caller.
//   - **Page twenty times.** Twenty round trips before the first pixel, on a clinic's shared
//     connection, for a screen whose acceptance criterion is *smooth interaction*.
//
// So the response is bounded by **what can be drawn** rather than by how many rows exist.
// That is a different bound, and it is much smaller: a medication a patient has been on for
// two years is one bar with a beginning and an end, not forty refill rows.
//
// # Why the duration bar is the whole point, and not a nicety
//
// Acceptance criterion 4 is *the correlation between an intervention and a value change is
// visually apparent*. A scatter of forty identical dots labelled "Metformin 500mg" does not
// carry a beginning; a bar does, and the eye reads "this started here and the line bent
// there" without being told. Collapsing repeats into spans is what makes criterion 4
// achievable at all, and it is done on the server so that two clients cannot collapse them
// two ways.
//
// # Where each half comes from, and why they come from different places
//
// **Lanes** are read from `read.patient_timeline` — CP37's projection, whose whole reason for
// existing is that four screens do not each write their own query over the ledger.
//
// **Series** are read from the observation read model, through an interface this module is
// handed (see [SeriesReader]). That is a fan-out in the shape CP73's dashboard already uses,
// and it is deliberate rather than convenient:
//
//   - the observation row carries `recorded_by` as the **user id**, which the directory
//     resolves to a name; the timeline row carries an employee code resolved at projection
//     time. Criterion 3 wants a name in a tooltip, and one of those two answers it;
//   - an observation carries `effective_at` **and** `recorded_at`, its status (was this value
//     corrected?), its unit and its own id. A chart that plots a value a physician can then
//     ask questions about needs all of them;
//   - it is gated by its own permission, `observation.read.values`, which is narrower than
//     the route's guard. A caller may read a patient's timeline and not their numbers, and
//     the response says so in `omitted` rather than drawing an empty chart.

// SpanAttribution is who put a mark or a point on the record.
//
// On every mark and on every point, never joined and never summarised — criterion 3 is
// *hovering any value shows its attribution*, and a tooltip that cannot say who recorded a
// value is a bug rather than a style choice. `ActorID` is carried alongside `ActorCode`
// because the client resolves a **name** through CP61's directory, and a code is not a name.
type SpanAttribution struct {
	ActorID      uuid.UUID `json:"actor_id"`
	ActorCode    string    `json:"actor_code"`
	ActorRole    string    `json:"actor_role"`
	ActorStation string    `json:"actor_station,omitempty"`
	// Source is three-valued on the wire exactly as docs/attribution.md requires: absent
	// where the record has no such field, empty where it has one nobody filled, a string
	// otherwise. Empty is not the same as station-entered and must not draw like it.
	Source     string    `json:"source"`
	RecordedAt time.Time `json:"recorded_at"`
}

// SpanMark is one thing on a lane: a point in time, or a bar with a beginning and an end.
type SpanMark struct {
	OccurredAt time.Time `json:"occurred_at"`
	// EndedAt is absent on a point mark and on a bar nobody has closed. The two are
	// distinguished by OpenEnded, because "we know it stopped on this day" and "it was last
	// seen on this day and may still be running" are different clinical facts and a chart
	// that drew them identically would be asserting the first.
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	OpenEnded bool       `json:"open_ended"`
	// LastSeenAt is the newest row folded into this bar. On an open-ended bar it is where
	// the evidence stops, which is what the chart draws a change of texture at.
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`

	Kind    string `json:"kind"`
	LabelEN string `json:"label_en"`
	LabelBN string `json:"label_bn"`
	Value   string `json:"value,omitempty"`
	Unit    string `json:"unit,omitempty"`

	Flags []string `json:"flags"`
	// Count is how many timeline rows this bar folds. One on a point mark. Reported rather
	// than hidden: a bar standing for forty refills is a different fact from a bar standing
	// for one prescription nobody renewed, and a physician deciding about adherence wants it.
	Count int `json:"count"`

	EventID uuid.UUID `json:"event_id"`
	Item    string    `json:"item,omitempty"`

	SpanAttribution
}

// SpanLane is one row of the chart.
//
// The key is one of §8's named lanes rather than the projection's `category`, and the
// mapping is [laneFor]. Two reasons: §8 names six lanes the closed category list does not
// have a member for — investigations, procedures, admissions, lifestyle interventions —
// and a screen whose lane set was the category set would be unable to draw four of the six
// the checkpoint is judged on. Nothing is dropped: a category with no named lane gets one
// of its own.
type SpanLane struct {
	Key string `json:"key"`
	// Durative says whether marks on this lane are bars or points. On the wire so that a
	// client cannot decide it differently — a medication drawn as a dot is criterion 4
	// silently not holding.
	Durative bool       `json:"durative"`
	Marks    []SpanMark `json:"marks"`
}

// SeriesPoint is one number on the shared value axis.
type SeriesPoint struct {
	// ObservationID is what a physician's next question is about. Carried so the tooltip
	// can offer the value's own record rather than making somebody search for it.
	ObservationID uuid.UUID `json:"observation_id"`
	At            time.Time `json:"at"`
	ValueNum      float64   `json:"value_num"`
	Unit          string    `json:"unit,omitempty"`
	// Flags are what a screen acts on: `corrected` and `superseded` say this point is not
	// the value that stands today, which is the one thing a trend line must not hide.
	Flags []string `json:"flags"`

	SpanAttribution
}

// SpanSeries is every point of one code, in order.
type SpanSeries struct {
	Code string `json:"code"`
	// Unit is the series' unit where every point agrees on one, and empty where they do not.
	// Empty rather than the newest point's unit: labelling a mixed series with one unit is
	// how a chart states something no record says.
	Unit   string        `json:"unit,omitempty"`
	Points []SeriesPoint `json:"points"`
}

// SpanOmission is a part of the answer the caller may not have.
//
// The same distinction CP73's dashboard draws and for the same reason: a series that is
// **absent** because the caller may not read observations and a series that is **empty**
// because this patient has never had one mean opposite things, and the one that looks
// reassuring is the wrong one.
type SpanOmission struct {
	Part      string `json:"part"`
	Needs     string `json:"needs"`
	ReasonEN  string `json:"reason_en"`
	ReasonBN  string `json:"reason_bn"`
	Withheld  bool   `json:"withheld"`
	Truncated bool   `json:"truncated,omitempty"`
}

// SpansView is one response: everything the chart draws.
type SpansView struct {
	Lanes  []SpanLane   `json:"lanes"`
	Series []SpanSeries `json:"series"`
	// Earliest and Latest are the whole span on record whatever window was asked for, so the
	// screen offers a range rather than guessing one — and so "nothing in this window" is
	// distinguishable from "nothing at all".
	Earliest *time.Time     `json:"earliest,omitempty"`
	Latest   *time.Time     `json:"latest,omitempty"`
	Omitted  []SpanOmission `json:"omitted"`
}

// SpansQuery is what the chart asks for.
type SpansQuery struct {
	From time.Time
	To   time.Time
	// Codes are the numeric series to overlay. Never defaulted to "everything": a decade of
	// every code a patient has is a payload nobody asked for and a chart nobody can read.
	Codes []string
	// Permissions is what the caller actually holds, off the verified principal. Never a
	// request parameter: a client that could name the permissions to filter by could name
	// all of them.
	Permissions []string
}

// SeriesReader is the observation read model, as this module needs it.
//
// An interface rather than an import, because `patient` may not import `clinical` —
// architecture.json, and the reason is the one in the file: a patient is the thing other
// modules are *about* and must not know which of them exist. `cmd/api` owns the bridge,
// which is where knowing about every module is the job.
type SeriesReader interface {
	// Series is every recorded value of these codes in this window, oldest first,
	// **including values that were later corrected or superseded** — a trend that quietly
	// dropped a corrected reading would show a patient a history they did not have.
	Series(ctx context.Context, patientID, facility uuid.UUID,
		codes []string, from, to time.Time, limit int) (
		points map[string][]SeriesPoint, units map[string]string, err error)
}

// PermObservationValues is what a caller needs to see the numeric overlays. Narrower than
// the route's own guard on purpose: reading that a patient attended and reading their
// glucose are different acts, and §4.4 blinds several roles from the second.
const PermObservationValues = "observation.read.values"

const (
	// SpansMaxRows bounds the lane half. Ten years of quarterly visits at forty rows a
	// visit is 1,600 rows; this is an order of magnitude above the record the system can
	// currently represent, and it is a ceiling rather than a page — passing it sets
	// `truncated` on the omission rather than silently returning a prefix.
	SpansMaxRows = 20000
	// SpansMaxPoints bounds the series half, per request rather than per code.
	SpansMaxPoints = 20000
	// SpansMaxCodes is how many overlays one request may ask for. A shared value axis
	// stops being readable long before this; the number is here so a malformed client
	// cannot ask for the whole registry.
	SpansMaxCodes = 12
)

// DefaultSeriesCodes are the plan's three: HbA1c, weight, blood pressure.
//
// Which values are overlaid **by default** is the checkpoint's own open decision and Dr.
// Nahid's to make. These are what §8 names in its own sentence, and the screen lets the
// reader change them.
var DefaultSeriesCodes = []string{"HBA1C", "BODY_WEIGHT", "BP_SYSTOLIC", "BP_DIASTOLIC"}

// ErrTooManySeries is a request for more overlays than a shared axis can carry.
var ErrTooManySeries = errors.New("patient: too many series for one chart")

// ErrBadSeriesCode is a code that is not shaped like a code. Refused rather than returned
// empty, for the reason `types` is refused on CP37's route: a silently empty series is
// indistinguishable from a patient who has never had that test.
var ErrBadSeriesCode = errors.New("patient: that is not an observation code")

// codeShape is the registry's own spelling: upper case, digits and underscores.
var codeShape = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

// Spans reads one patient's timeline in the shape a chart draws.
func (s *Store) Spans(ctx context.Context, patientID, facility uuid.UUID,
	q SpansQuery, series SeriesReader) (SpansView, error) {

	view := SpansView{Lanes: []SpanLane{}, Series: []SpanSeries{}, Omitted: []SpanOmission{}}

	for _, code := range q.Codes {
		if !codeShape.MatchString(code) {
			return SpansView{}, fmt.Errorf("%w: %q", ErrBadSeriesCode, code)
		}
	}
	if len(q.Codes) > SpansMaxCodes {
		return SpansView{}, ErrTooManySeries
	}
	if len(q.Permissions) == 0 {
		// Empty rather than everything. A caller that passed nothing has a bug, and the safe
		// reading of a bug in a permission filter is "no rows".
		return view, nil
	}
	if q.From.IsZero() {
		q.From = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if q.To.IsZero() {
		q.To = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	rows, truncated, err := s.timelineRowsForSpans(ctx, patientID, facility, q)
	if err != nil {
		return SpansView{}, err
	}
	view.Lanes = lanesOf(rows)
	if truncated {
		view.Omitted = append(view.Omitted, SpanOmission{
			Part: "lanes", Withheld: false, Truncated: true,
			ReasonEN: "This record is longer than one chart can hold. The oldest entries are not drawn.",
			ReasonBN: "এই রেকর্ড এক চার্টে ধরার চেয়ে বড়। সবচেয়ে পুরোনো এন্ট্রিগুলো আঁকা হয়নি।",
		})
	}

	earliest, latest, err := s.timelineSpan(ctx, patientID, facility, q.Permissions)
	if err != nil {
		return SpansView{}, err
	}
	view.Earliest, view.Latest = earliest, latest

	// The numeric overlays, and the permission that gates them. Decided here rather than in
	// the handler so that a second caller of this store cannot forget it.
	if !holds(q.Permissions, PermObservationValues) {
		view.Omitted = append(view.Omitted, SpanOmission{
			Part: "series", Needs: PermObservationValues, Withheld: true,
			ReasonEN: "You were not shown the measured values on this record.",
			ReasonBN: "এই রেকর্ডের পরিমাপ করা মানগুলো আপনাকে দেখানো হয়নি।",
		})
		return view, nil
	}
	if series == nil || len(q.Codes) == 0 {
		return view, nil
	}

	points, units, err := series.Series(ctx, patientID, facility, q.Codes,
		q.From, q.To, SpansMaxPoints)
	if err != nil {
		return SpansView{}, err
	}
	view.Series = seriesOf(q.Codes, points, units)
	return view, nil
}

// timelineRowsForSpans reads the window oldest-first, with no paging.
//
// Hand-written rather than generated, like [Store.timelineSpan] beside it: what this needs is
// a ceiling with a flag rather than a page with an offset, and sqlc's shape for "limit, and
// tell me whether there was more" is a second query. One row over the ceiling is read on
// purpose — it is how `truncated` is *known* rather than inferred from a full page, which is
// the bug where exactly SpansMaxRows rows report as truncated and are not.
func (s *Store) timelineRowsForSpans(ctx context.Context, patientID, facility uuid.UUID,
	q SpansQuery) ([]spanRow, bool, error) {

	rows, err := s.pool.Query(ctx, `
		SELECT occurred_at, recorded_at, category, kind, label_en, label_bn,
		       value, unit, actor_id, actor_code, actor_role, actor_station, source,
		       flags, event_id, event_type, item
		  FROM read.patient_timeline
		 WHERE patient_id = $1
		   AND facility_id = $2
		   AND occurred_at >= $3
		   AND occurred_at < $4
		   AND needs_permission = ANY($5::text[])
		 ORDER BY occurred_at ASC, id ASC
		 LIMIT $6`,
		patientID, facility, q.From, q.To, q.Permissions, SpansMaxRows+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []spanRow
	for rows.Next() {
		var row spanRow
		var eventType string
		if err := rows.Scan(&row.OccurredAt, &row.RecordedAt, &row.Category, &row.Kind,
			&row.LabelEN, &row.LabelBN, &row.Value, &row.Unit,
			&row.ActorID, &row.ActorCode, &row.ActorRole, &row.ActorStation, &row.Source,
			&row.Flags, &row.EventID, &eventType, &row.Item); err != nil {
			return nil, false, err
		}
		row.OccurredAt = row.OccurredAt.UTC()
		row.RecordedAt = row.RecordedAt.UTC()
		if row.Flags == nil {
			row.Flags = []string{}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	truncated := len(out) > SpansMaxRows
	if truncated {
		// The ceiling drops the **oldest**, which is the opposite of what the query's own
		// ordering would do. A chart missing last month is a chart a physician acts on
		// wrongly; a chart missing 2014 is a chart with a note on it saying so.
		out = out[len(out)-SpansMaxRows:]
	}
	return out, truncated, nil
}

// spanRow is one timeline row as this file reads it.
//
// Deliberately not [TimelineEntry]: that type is CP37's wire shape and carries no actor id,
// because its own screen resolves nobody. This one does — the chart's tooltip resolves a
// **name** through CP61's directory, and a name is what criterion 3 is asking for.
type spanRow struct {
	OccurredAt, RecordedAt              time.Time
	Category, Kind                      string
	LabelEN, LabelBN, Value, Unit       string
	ActorID                             uuid.UUID
	ActorCode, ActorRole, ActorStation  string
	Source                              string
	Flags                               []string
	EventID                             uuid.UUID
	Item                                string
}

// laneKey is one of §8's named lanes.
const (
	laneDiagnoses      = "diagnoses"
	laneMedications    = "medications"
	laneInvestigations = "investigations"
	laneProcedures     = "procedures"
	laneAdmissions     = "admissions"
	laneLifestyle      = "lifestyle"
	laneVisits         = "visits"
	laneObservations   = "observations"
	laneDocuments      = "documents"
	laneCommunication  = "communication"
	laneAlerts         = "alerts"
	laneConsent        = "consent"
	laneAdministrative = "administrative"
	laneRegistration   = "registration"
)

// SpanLanes is the order the chart draws lanes in, and the closed list a filter offers.
//
// Interventions above measurements, and medications directly above the value axis, because
// criterion 4 is about the eye travelling a short distance between a bar starting and a line
// bending. A lane order chosen alphabetically would put admissions between them.
var SpanLanes = []string{
	laneDiagnoses, laneMedications, laneProcedures, laneAdmissions,
	laneInvestigations, laneLifestyle, laneVisits, laneObservations,
	laneAlerts, laneDocuments, laneCommunication, laneConsent,
	laneAdministrative, laneRegistration,
}

// durativeLanes are the lanes whose marks are bars rather than points.
//
// A medication, a diagnosis, an admission and a lifestyle prescription all **run**; an
// investigation, a procedure and a visit **happen**. That is a clinical distinction rather
// than a drawing one, which is why it is decided here and sent on the wire.
var durativeLanes = map[string]bool{
	laneMedications: true, laneDiagnoses: true,
	laneAdmissions: true, laneLifestyle: true,
}

// laneFor decides which lane a timeline row belongs on.
//
// The category decides it unless the **kind** says otherwise, and the kind is consulted
// first. §8 names lanes the closed category list has no member for — investigations,
// procedures, admissions, lifestyle interventions — and docs/timeline.md is explicit that a
// new *category* is a decision while a new *kind* is not. So a projection can put an
// admission on the admissions lane by calling it `admission.started`, with no migration and
// no change here.
//
// Nothing is ever dropped. A category this build has never heard of gets a lane named after
// itself, which is a lane a physician can read and a filter can offer; silently discarding it
// would be a screen that is missing something and says nothing.
func laneFor(category, kind string) string {
	head := kind
	if dot := strings.IndexByte(kind, '.'); dot > 0 {
		head = kind[:dot]
	}
	switch head {
	case "admission", "admitted", "discharge":
		return laneAdmissions
	case "procedure", "surgery", "operation":
		return laneProcedures
	case "investigation", "lab", "imaging", "order":
		return laneInvestigations
	case "lifestyle", "diet", "nutrition", "exercise", "counseling":
		return laneLifestyle
	}
	switch category {
	case "diagnosis":
		return laneDiagnoses
	case "medication":
		return laneMedications
	case "visit":
		return laneVisits
	case "observation":
		return laneObservations
	case "document":
		return laneDocuments
	case "communication":
		return laneCommunication
	case "alert":
		return laneAlerts
	case "consent":
		return laneConsent
	case "administrative":
		return laneAdministrative
	case "registration":
		return laneRegistration
	}
	return category
}

// closingKinds end a bar.
//
// Suffixes rather than whole kinds, because `kind` is open by design: `medication.stopped`
// and `glp1.stopped` must both close. A row that closes nothing — a stop for a drug with no
// start on record — still draws, as a zero-length bar at the moment it happened, because a
// patient whose record says a drug was stopped and never says it was started is a record
// worth seeing rather than a row worth hiding.
var closingKinds = []string{
	"stopped", "ended", "discontinued", "resolved", "completed", "cancelled",
	"discharged", "withdrawn", "expired",
}

func closesASpan(kind string) bool {
	tail := kind
	if dot := strings.LastIndexByte(kind, '.'); dot >= 0 {
		tail = kind[dot+1:]
	}
	for _, closing := range closingKinds {
		if tail == closing {
			return true
		}
	}
	return false
}

// lanesOf folds rows into lanes, collapsing a durative lane's repeats into bars.
//
// # The collapsing rule, stated once so it can be argued with
//
// On a durative lane, rows are grouped by their **English label**, which is the only stable
// identity a timeline row carries for its subject — a drug's name, a condition's name. The
// first row for a label opens a bar. Every later row for the same label extends it and
// raises its count. A row whose kind ends in a closing verb closes it.
//
// A bar nobody closed is **open-ended**, not "ended at the last refill". The distinction is
// the difference between a chart that says a patient is still on metformin and one that
// says they stopped taking it on the day of their last prescription, and only one of those
// is something the record actually claims.
//
// Grouping on the label rather than on a subject id is a limitation and is named as such:
// two drugs whose labels differ by a strength — "Metformin 500mg" and "Metformin 1g" — are
// two bars, which is right, and the same drug relabelled mid-course is two bars, which is
// wrong. The honest fix is a subject key on the projection row (a generic id, a condition
// code), and CP81's prescriptions are where one becomes available. Until then this is the
// best identity the data carries, and inventing a fuzzy match would be inventing a clinical
// judgement in a chart.
func lanesOf(rows []spanRow) []SpanLane {
	byLane := map[string][]SpanMark{}
	// Where each open bar lives, keyed lane+label, so a closing row can find it. Only a
	// durative lane ever puts anything in here.
	type slot struct {
		lane  string
		index int
	}
	open := map[string]slot{}

	for _, row := range rows {
		lane := laneFor(row.Category, row.Kind)
		if !durativeLanes[lane] {
			byLane[lane] = append(byLane[lane], markOf(row))
			continue
		}
		key := lane + "\x00" + strings.ToLower(strings.TrimSpace(row.LabelEN))
		if at, isOpen := open[key]; isOpen {
			bar := &byLane[at.lane][at.index]
			bar.Count++
			occurred := row.OccurredAt
			bar.LastSeenAt = &occurred
			if closesASpan(row.Kind) {
				bar.EndedAt = &occurred
				bar.OpenEnded = false
				delete(open, key)
			}
			continue
		}
		mark := markOf(row)
		if closesASpan(row.Kind) {
			// A stop with no start. Drawn where it happened, closed on itself, and never
			// opened — a bar running from 1900 to here would be a claim.
			at := row.OccurredAt
			mark.EndedAt = &at
			mark.LastSeenAt = &at
			byLane[lane] = append(byLane[lane], mark)
			continue
		}
		mark.OpenEnded = true
		at := row.OccurredAt
		mark.LastSeenAt = &at
		byLane[lane] = append(byLane[lane], mark)
		open[key] = slot{lane: lane, index: len(byLane[lane]) - 1}
	}

	out := make([]SpanLane, 0, len(byLane))
	for _, key := range SpanLanes {
		if marks, ok := byLane[key]; ok {
			out = append(out, SpanLane{Key: key, Durative: durativeLanes[key], Marks: marks})
			delete(byLane, key)
		}
	}
	// Whatever is left is a category nobody named a lane for. Sorted, so the order is the
	// same on two reads of the same record.
	rest := make([]string, 0, len(byLane))
	for key := range byLane {
		rest = append(rest, key)
	}
	sort.Strings(rest)
	for _, key := range rest {
		out = append(out, SpanLane{Key: key, Durative: false, Marks: byLane[key]})
	}
	return out
}

func markOf(row spanRow) SpanMark {
	return SpanMark{
		OccurredAt: row.OccurredAt,
		Kind:       row.Kind, LabelEN: row.LabelEN, LabelBN: row.LabelBN,
		Value: row.Value, Unit: row.Unit,
		Flags: row.Flags, Count: 1,
		EventID: row.EventID, Item: row.Item,
		SpanAttribution: SpanAttribution{
			ActorID: row.ActorID, ActorCode: row.ActorCode, ActorRole: row.ActorRole,
			ActorStation: row.ActorStation, Source: row.Source, RecordedAt: row.RecordedAt,
		},
	}
}

// seriesOf groups points by code, **including a code that has none**.
//
// An empty series for a requested code is the answer to a different question from a missing
// one: "this patient has no HbA1c on record" against "you did not ask for HbA1c". A chart
// that dropped the first would draw three overlays where four were requested and say nothing
// about the fourth.
func seriesOf(codes []string, byCode map[string][]SeriesPoint, units map[string]string) []SpanSeries {
	out := make([]SpanSeries, 0, len(codes))
	for _, code := range codes {
		series := SpanSeries{Code: code, Unit: units[code], Points: byCode[code]}
		if series.Points == nil {
			series.Points = []SeriesPoint{}
		}
		out = append(out, series)
	}
	return out
}

func holds(permissions []string, want string) bool {
	for _, permission := range permissions {
		if permission == want {
			return true
		}
	}
	return false
}
