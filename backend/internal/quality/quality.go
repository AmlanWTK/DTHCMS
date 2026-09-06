// Package quality is the operator quality record (CP63, §4.3).
//
// # What this is for, in the plan's own words
//
// §4.3 states the purpose of the correction workflow: *"recurring patterns per operator surface
// so targeted retraining happens and the same mistake does not repeat."* CP62 built the routing.
// Without this package the corrections are a pile of rows nobody reads.
//
// # The risk that shaped every decision here
//
// The plan states it as a risk in its own words: *"a metric that feels punitive damages data
// honesty — staff hide errors instead of correcting them."* Read that as a design constraint
// rather than a caveat, and four things follow, all of which are visible in this code:
//
//  1. An operator reads their own record without a permission (`Record` on the caller's own id).
//  2. A rate is never returned without its denominator (`Record.Entries` is always populated).
//  3. Rejected requests are reported separately and never counted as errors.
//  4. A threshold that no clinician has approved is reported as unapproved, every time.
//
// # Why there is no aggregation table
//
// ADR-0029. Every count is taken at read time, over indexed columns, on a few tens of thousands
// of rows. A nightly job would buy nothing and cost the one failure this feature cannot
// survive: a job that stops quietly, leaving a supervisor reading numbers that were true last
// Tuesday and an operator told about a pattern they fixed a week ago.
package quality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Zone is the clinic's wall clock. The time-of-day pattern §4.3 names is meaningless in UTC:
// "corrections cluster at the end of the shift" is a claim about the hour on the wall in
// Faridpur, and this clinic is six hours ahead of it.
const Zone = "Asia/Dhaka"

// Pattern is one of the three shapes §4.3 names.
type Pattern string

const (
	// Transcription is a repeated typing error: the operator read the instrument correctly and
	// put a different number on the screen.
	Transcription Pattern = "TRANSCRIPTION"
	// SameCode is the same measurement going wrong again and again. Usually the instrument
	// rather than the person, which is why the threshold's suggested action says so.
	SameCode Pattern = "SAME_CODE"
	// EndOfShift is corrections clustering late in the working day. Usually a rota problem.
	EndOfShift Pattern = "END_OF_SHIFT"
)

// Threshold is when a pattern is worth somebody looking at.
//
// Approved is null on every row this system ships with. The plan lists the numbers as an open
// decision requiring approval, and the API reports the absence rather than hiding it — the same
// discipline the critical-value table follows (CP50).
type Threshold struct {
	Code    string  `json:"code"`
	Pattern Pattern `json:"pattern"`

	WindowDays int `json:"window_days"`
	MinCount   int `json:"min_count"`
	// MinEntries is the floor under the denominator. Three corrections out of five entries is a
	// new operator on their first morning, and flagging them for retraining on their first
	// morning is how a clinic teaches its staff to stop asking for help.
	MinEntries int  `json:"min_entries"`
	AfterHour  *int `json:"after_hour,omitempty"`

	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	// ActionEN and ActionBN are what to do about it. A flag with no suggested action is a
	// complaint, and a complaint is what makes this feature feel punitive.
	ActionEN string `json:"action_en"`
	ActionBN string `json:"action_bn"`

	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	// Approved is the same fact stated so a client cannot forget to check for the null.
	Approved bool `json:"approved"`
	Ordering int  `json:"ordering"`
}

// Window is the period a record covers.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Days int       `json:"days"`
}

// ReasonCount is corrections grouped by why the value was wrong.
//
// No denominator on this one, and it is the only breakdown without one: "how many values did you
// enter for the reason WRONG_UNIT" is not a question — a reason describes a correction, not an
// entry. The whole is `Record.Corrections`, and the client renders against that.
type ReasonCount struct {
	ReasonCode    string `json:"reason_code"`
	DisplayEN     string `json:"display_en"`
	DisplayBN     string `json:"display_bn"`
	Transcription bool   `json:"transcription"`
	Corrections   int    `json:"corrections"`
}

// CodeCount is corrections grouped by what was being measured, against how many of that
// measurement the operator took.
//
// The denominator is the point. Three corrections on a weight is a different fact when the
// operator weighed four hundred people and when they weighed nine, and a breakdown that reported
// only the first number would be the accusation this whole design exists to avoid — reintroduced
// one level down from where it was removed.
type CodeCount struct {
	Code        string `json:"code"`
	DisplayEN   string `json:"display_en"`
	DisplayBN   string `json:"display_bn"`
	Corrections int    `json:"corrections"`
	Entries     int    `json:"entries"`
}

// HourCount is corrections grouped by the hour the value was recorded, on the clinic's wall
// clock — not the hour it was flagged. A physician reviewing yesterday's file at nine in the
// morning would otherwise make every operator look like a morning problem.
//
// `Entries` is the hour's own denominator, and without it the end-of-shift pattern is unfair by
// construction: an operator who works only the late shift will always cluster late, and a screen
// that showed the numerator alone would present a rota as a person.
type HourCount struct {
	Hour        int `json:"hour"`
	Corrections int `json:"corrections"`
	Entries     int `json:"entries"`
}

// Record is one operator's quality record over one window.
//
// Every field that could be read as an accusation is accompanied by the field that makes it
// legible. Corrections without Entries is the number that makes staff hide their mistakes;
// Upheld without Rejected implies every flag was right.
type Record struct {
	OperatorID uuid.UUID `json:"operator_id"`
	// The names, so a supervisor opening somebody's month gets a heading rather than a uuid.
	// Joined rather than copied, for CP61's reason.
	OperatorCode   string `json:"operator_code,omitempty"`
	OperatorNameEN string `json:"operator_name_en,omitempty"`
	OperatorNameBN string `json:"operator_name_bn,omitempty"`
	OperatorStatus string `json:"operator_status,omitempty"`

	Window Window `json:"window"`

	// Entries is values this operator typed in the window. Derived values are excluded: they
	// are computed by the server, not typed by anybody, and counting them would inflate the
	// denominator of whoever pressed the button.
	Entries int `json:"entries"`

	Corrections int `json:"corrections"`
	// Upheld is corrections the operator applied themselves.
	Upheld int `json:"upheld"`
	// Overridden is corrections a supervisor applied instead. Counted apart from Upheld, because
	// CP62 made a supervisor's fix a different event precisely so that an operator's record would
	// not read it as though they had put it right themselves — and folding the two together here
	// would throw that distinction away at the one place it was created for, telling somebody
	// "you put three values right" about values they never touched.
	Overridden int `json:"overridden"`
	// Rejected is requests the operator answered by saying the value stands, and they were not
	// overruled. Reported separately and never counted as an error: an operator who defends a
	// correct reading is doing the job, and a metric that punished it would teach everyone to
	// accept every flag without looking.
	Rejected int `json:"rejected"`
	Open     int `json:"open"`

	// Rate is corrections that stood — applied or overridden — per hundred entries, rounded to
	// one decimal. Null when there are too few entries to mean anything: a rate computed from
	// four entries is noise, and showing it as a number invites somebody to act on it.
	//
	// **Not `omitempty`.** The contract says this field is null below the floor, and a client
	// checking `rate === null` was reading a field the encoder had dropped. An absent field and a
	// null one are the same fact here and must arrive spelled the same way.
	Rate *float64 `json:"rate"`
	// AnsweredFlagsKeptDays is how long an answered flag keeps appearing here, so a screen can
	// say "answered notes stay for a month" rather than letting one disappear unexplained.
	AnsweredFlagsKeptDays int `json:"answered_flags_kept_days"`
	// RateFloor is the number of entries below which no rate is computed. On the payload because
	// a screen that cannot name the floor can only say "too few", where it could say "eight more
	// values and you will see a rate" — and being specific is most of what makes this feature
	// feel like arithmetic rather than a judgement.
	RateFloor int `json:"rate_floor"`

	ByReason []ReasonCount `json:"by_reason"`
	ByCode   []CodeCount   `json:"by_code"`
	ByHour   []HourCount   `json:"by_hour"`

	// Flags are the patterns on this record — open **and recently answered**, not only open.
	//
	// Open-only was the first version and it produced the ambush from the other end: the operator
	// is told on their own device that somebody wrote a note about them, a supervisor
	// acknowledges or dismisses it, and it silently vanishes with no resolution and no reason.
	// Somebody watching their own record would learn that a note about them had been closed by
	// its disappearance. The answered ones stay for `resolvedFlagsStayFor`.
	Flags []Flag `json:"flags"`
}

// minimumEntriesForARate is the floor under Rate. Below it the rate is nil.
//
// Twenty is the same floor the seeded thresholds use, and it is a judgement rather than a
// finding: it is roughly a morning's work at one station, which is the smallest amount of work
// anybody should draw a conclusion from.
const minimumEntriesForARate = 20

// Evidence is one correction behind a flag.
//
// It names no patient and carries no value — `core.assert_no_quality_record_names_a_patient`
// refuses a row that does. A supervisor reading a patient's values through their staff's error
// history would be reading clinical data through a side door.
type Evidence struct {
	RequestID  uuid.UUID `json:"request_id"`
	ReasonCode string    `json:"reason_code"`
	ReasonEN   string    `json:"reason_en,omitempty"`
	ReasonBN   string    `json:"reason_bn,omitempty"`
	Code       string    `json:"code"`
	CodeEN     string    `json:"code_en,omitempty"`
	CodeBN     string    `json:"code_bn,omitempty"`
	At         time.Time `json:"at"`
	// Status is what the request was **when the flag was raised**, frozen with everything else on
	// the row. A request that was open then and rejected since still reads OPEN here, and that is
	// the honest reading of a frozen record rather than a bug — the contract says so, and
	// `StatusAsOf` carries the moment so a screen can say it too.
	Status     string    `json:"status"`
	StatusAsOf time.Time `json:"status_as_of"`
	Hour       int       `json:"hour"`
}

// Flag is a pattern somebody should look at, frozen at the moment it was noticed.
type Flag struct {
	ID uuid.UUID `json:"id"`
	// FacilityID is on the flag because the bridges that record and publish it are outside this
	// module and have no other honest source for it: a bridge that read the facility from the
	// request context would be a second answer to "which clinic is this", and the row is the
	// first one.
	FacilityID uuid.UUID `json:"facility_id"`
	OperatorID uuid.UUID `json:"operator_id"`
	// The names, joined rather than copied, for CP61's reason.
	OperatorCode   string `json:"operator_code,omitempty"`
	OperatorNameEN string `json:"operator_name_en,omitempty"`
	OperatorNameBN string `json:"operator_name_bn,omitempty"`

	ThresholdCode string `json:"threshold_code"`
	ThresholdEN   string `json:"threshold_en,omitempty"`
	ThresholdBN   string `json:"threshold_bn,omitempty"`
	ActionEN      string `json:"action_en,omitempty"`
	ActionBN      string `json:"action_bn,omitempty"`
	// ThresholdApproved says whether a clinician has signed off the numbers this was raised on.
	// False on everything this system ships with, and reported rather than hidden.
	ThresholdApproved bool `json:"threshold_approved"`

	RaisedAt time.Time `json:"raised_at"`
	Window   Window    `json:"window"`

	ObservedCount int `json:"observed_count"`
	// EntriesCount is the denominator, frozen with the count. A flag that showed only the
	// numerator would be the accusation this design exists to avoid.
	EntriesCount int `json:"entries_count"`

	Evidence []Evidence `json:"evidence"`

	Status     string     `json:"status"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy string     `json:"resolved_by,omitempty"`
	// The resolver's names, joined like the operator's. Two people on one row named two different
	// ways — one with a name and one with a uuid — is how a uuid ends up on a screen.
	ResolvedByCode   string `json:"resolved_by_code,omitempty"`
	ResolvedByNameEN string `json:"resolved_by_name_en,omitempty"`
	ResolvedByNameBN string `json:"resolved_by_name_bn,omitempty"`
	Resolution       string `json:"resolution,omitempty"`
}

// Open says whether this flag is still waiting for a supervisor.
func (f Flag) Open() bool { return f.Status == "OPEN" }

// Operator is one line of the supervisor's list.
type Operator struct {
	OperatorID  uuid.UUID `json:"operator_id"`
	Code        string    `json:"employee_code,omitempty"`
	NameEN      string    `json:"name_en,omitempty"`
	NameBN      string    `json:"name_bn,omitempty"`
	Status      string    `json:"status,omitempty"`
	Corrections int       `json:"corrections"`
	// Upheld, Overridden, Rejected and Open are the same split the record carries, so a
	// supervisor's landing screen can show a defended value as a defended value rather than
	// folding it into a count of things that went wrong.
	Upheld     int `json:"upheld"`
	Overridden int `json:"overridden"`
	Rejected   int `json:"rejected"`
	OpenCount  int `json:"open"`
	// Entries is the denominator, and it comes from the same query as the numerator. A list
	// without it puts the busiest operator at the top of something a reader takes as a ranking of
	// the worst.
	Entries int `json:"entries"`
	// Rate is corrections that stood per hundred entries — the **same arithmetic** the record
	// uses. It was corrections-over-entries here and upheld-over-entries there, so a supervisor
	// comparing a line against the record they opened in the next click saw two different numbers
	// for one person and one window, and would rightly conclude a screen was broken.
	Rate      *float64 `json:"rate"`
	RateFloor int      `json:"rate_floor"`
	Flags     int      `json:"open_flags"`
}

var (
	// ErrNoFlag is a flag that is not there.
	ErrNoFlag = errors.New("quality: no such flag")
	// ErrFlagClosed is a second answer to a flag somebody already answered.
	ErrFlagClosed = errors.New("quality: that flag has already been answered")
	// ErrReasonRequired is a dismissal with nothing said. Same rule as a rejected correction:
	// "no" with no reason teaches nobody anything.
	ErrReasonRequired = errors.New("quality: say why the flag is being dismissed")
	// ErrUnknownThreshold is a threshold code that is not in the table.
	ErrUnknownThreshold = errors.New("quality: no such threshold")
	// ErrUnknownStatus is a resolution that is neither acknowledging nor dismissing.
	ErrUnknownStatus = errors.New("quality: a flag is acknowledged or dismissed")
)

// Store reads the record and writes the flags.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds a store over a pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

// Thresholds is the vocabulary of patterns.
func (s *Store) Thresholds(ctx context.Context) ([]Threshold, error) {
	rows, err := s.q.QualityThresholds(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Threshold, 0, len(rows))
	for _, row := range rows {
		t := Threshold{
			Code: row.Code, Pattern: Pattern(row.Pattern),
			WindowDays: int(row.WindowDays), MinCount: int(row.MinCount),
			MinEntries: int(row.MinEntries),
			DisplayEN:  row.DisplayEn, DisplayBN: row.DisplayBn,
			ActionEN: row.ActionEn, ActionBN: row.ActionBn,
			ApprovedAt: row.ApprovedAt, Approved: row.ApprovedAt != nil,
			Ordering: int(row.Ordering),
		}
		if row.AfterHour != nil {
			hour := int(*row.AfterHour)
			t.AfterHour = &hour
		}
		out = append(out, t)
	}
	return out, nil
}

// Record is one operator's record over one window.
func (s *Store) Record(ctx context.Context, facility, operator uuid.UUID,
	from, to time.Time) (Record, error) {

	record := Record{
		OperatorID:            operator,
		Window:                Window{From: from, To: to, Days: int(to.Sub(from).Hours() / 24)},
		RateFloor:             minimumEntriesForARate,
		AnsweredFlagsKeptDays: AnsweredFlagsKeptDays,
	}

	if who, err := s.q.StaffMember(ctx, dbgen.StaffMemberParams{ID: operator, FacilityID: facility}); err == nil {
		record.OperatorCode = who.EmployeeCode
		record.OperatorNameEN = who.NameEn
		record.OperatorNameBN = who.NameBn
		record.OperatorStatus = who.Status
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Record{}, err
	}

	entries, err := s.q.EntriesRecorded(ctx, dbgen.EntriesRecordedParams{
		FacilityID: facility, RecordedBy: operator, RecordedAt: from, RecordedAt_2: to,
	})
	if err != nil {
		return Record{}, err
	}
	record.Entries = int(entries)

	received, err := s.q.CorrectionsReceived(ctx, dbgen.CorrectionsReceivedParams{
		FacilityID: facility, AssignedTo: operator, RequestedAt: from, RequestedAt_2: to,
	})
	if err != nil {
		return Record{}, err
	}
	record.Corrections = int(received.Total)
	record.Upheld = int(received.Upheld)
	record.Overridden = int(received.Overridden)
	record.Rejected = int(received.Rejected)
	record.Open = int(received.Open)
	record.Rate = rateOf(record.Upheld+record.Overridden, record.Entries)

	byReason, err := s.q.CorrectionsByReason(ctx, dbgen.CorrectionsByReasonParams{
		FacilityID: facility, AssignedTo: operator, RequestedAt: from, RequestedAt_2: to,
	})
	if err != nil {
		return Record{}, err
	}
	record.ByReason = make([]ReasonCount, 0, len(byReason))
	for _, row := range byReason {
		record.ByReason = append(record.ByReason, ReasonCount{
			ReasonCode: row.ReasonCode, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			Transcription: row.IsTranscription, Corrections: int(row.Corrections),
		})
	}

	// Each breakdown against its own denominator. Corrections-by-hour on its own says nothing:
	// an operator who works only the late shift will always cluster late.
	entriesByCode, err := s.q.EntriesByCode(ctx, dbgen.EntriesByCodeParams{
		FacilityID: facility, RecordedBy: operator, RecordedAt: from, RecordedAt_2: to,
	})
	if err != nil {
		return Record{}, err
	}
	perCode := map[string]int{}
	for _, row := range entriesByCode {
		perCode[row.Code] = int(row.Entries)
	}

	byCode, err := s.q.CorrectionsByCode(ctx, dbgen.CorrectionsByCodeParams{
		FacilityID: facility, AssignedTo: operator, RequestedAt: from, RequestedAt_2: to,
	})
	if err != nil {
		return Record{}, err
	}
	record.ByCode = make([]CodeCount, 0, len(byCode))
	for _, row := range byCode {
		record.ByCode = append(record.ByCode, CodeCount{
			Code: row.Code, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			Corrections: int(row.Corrections), Entries: perCode[row.Code],
		})
	}
	// Stable, and by code: a size-ranked list of the ways one person's values were questioned,
	// handed to that person, reads as a leaderboard however carefully it is labelled.
	sort.Slice(record.ByCode, func(i, j int) bool { return record.ByCode[i].Code < record.ByCode[j].Code })

	entriesByHour, err := s.q.EntriesByHour(ctx, dbgen.EntriesByHourParams{
		Zone: Zone, FacilityID: facility, RecordedBy: operator, FromAt: from, ToAt: to,
	})
	if err != nil {
		return Record{}, err
	}
	perHour := map[int]int{}
	for _, row := range entriesByHour {
		perHour[int(row.Hour)] = int(row.Entries)
	}

	byHour, err := s.q.CorrectionsByHour(ctx, dbgen.CorrectionsByHourParams{
		Zone: Zone, FacilityID: facility, AssignedTo: operator, FromAt: from, ToAt: to,
	})
	if err != nil {
		return Record{}, err
	}
	record.ByHour = make([]HourCount, 0, len(byHour))
	for _, row := range byHour {
		record.ByHour = append(record.ByHour, HourCount{
			Hour: int(row.Hour), Corrections: int(row.Corrections), Entries: perHour[int(row.Hour)],
		})
	}

	// Open **and recently answered**. See the note on Record.Flags: a note that vanished when a
	// supervisor closed it would be the ambush arriving from the other end.
	//
	// Filtered in SQL rather than in Go, because the limit applies before the filter does: an
	// operator with fifty flags across a year could have an older *open* one paged out by newer
	// answered ones, and the flag that got dropped would be the one still waiting for somebody.
	since := to.Add(-resolvedFlagsStayFor)
	flags, err := s.Flags(ctx, facility, &operator, false, 50, &since)
	if err != nil {
		return Record{}, err
	}
	record.Flags = flags
	return record, nil
}

// resolvedFlagsStayFor is how long an answered flag keeps appearing on the record it is about.
//
// Thirty days is a judgement rather than a finding: long enough that somebody who was away for a
// fortnight still sees what was decided about them, short enough that a record does not become an
// archive of every conversation anybody ever had.
const resolvedFlagsStayFor = 30 * 24 * time.Hour

// AnsweredFlagsKeptDays is `resolvedFlagsStayFor` in days, for the payload.
//
// On the response for the same reason `rate_floor` is: a screen that cannot name the number can
// only say "it will disappear eventually", where it could say "answered notes stay for a month".
// The same shape of problem, closed the same way.
var AnsweredFlagsKeptDays = int(resolvedFlagsStayFor / (24 * time.Hour))

// rateOf is corrections that stood, per hundred entries, or nil when there is too little to say.
//
// Nil rather than zero, and nil rather than a number: a rate computed from four entries is
// noise, and rendering noise as a number invites somebody to act on it.
func rateOf(upheld, entries int) *float64 {
	if entries < minimumEntriesForARate {
		return nil
	}
	rate := float64(upheld) * 100 / float64(entries)
	rounded := float64(int(rate*10+0.5)) / 10
	return &rounded
}

// Operators is the supervisor's list, in one query.
//
// `includeAll` decides whether this is a roster or a list of people with corrections. False, and
// the list is structurally "everybody who was corrected" — and no quantity of denominators
// printed beside the counts undoes what a list like that reads as to the people on it. True, and
// everybody who recorded anything in the window is on it, including the operators with four
// hundred entries and nothing corrected, who are the rows that make it a roster.
func (s *Store) Operators(ctx context.Context, facility uuid.UUID, from, to time.Time,
	includeAll bool, limit int) ([]Operator, error) {

	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.q.OperatorsWithCorrections(ctx, dbgen.OperatorsWithCorrectionsParams{
		FacilityID: facility, FromAt: from, ToAt: to, IncludeAll: includeAll,
		RowLimit: int32(limit), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, err
	}
	// The open-flag counts, in one read rather than one per operator, and unbounded rather than
	// capped: a facility with more than the old two hundred open flags would silently have shown
	// some operators fewer than they had.
	//
	// The same window the queue uses, which is now the same rule: an open flag is never outside
	// a window. Without that, a row said "two waiting to be looked at" beside a queue showing
	// none, and a supervisor met it the first time they picked seven days.
	flags, err := s.Flags(ctx, facility, nil, true, 1000, &from)
	if err != nil {
		return nil, err
	}
	open := map[uuid.UUID]int{}
	for _, flag := range flags {
		open[flag.OperatorID]++
	}

	out := make([]Operator, 0, len(rows))
	for _, row := range rows {
		operator := Operator{
			OperatorID: row.OperatorID, Code: row.EmployeeCode,
			NameEN: row.NameEn, NameBN: row.NameBn, Status: row.Status,
			Corrections: int(row.Corrections), Upheld: int(row.Upheld),
			Overridden: int(row.Overridden), Rejected: int(row.Rejected),
			OpenCount: int(row.StillOpen), Entries: int(row.Entries),
			RateFloor: minimumEntriesForARate,
			Flags:     open[row.OperatorID],
		}
		// The same arithmetic the record uses. It used to be corrections-over-entries here and
		// upheld-over-entries there, so one person and one window produced two numbers.
		operator.Rate = rateOf(operator.Upheld+operator.Overridden, operator.Entries)
		out = append(out, operator)
	}
	return out, nil
}

// Flags reads the raised patterns, optionally for one operator and optionally within a window.
func (s *Store) Flags(ctx context.Context, facility uuid.UUID, operator *uuid.UUID,
	openOnly bool, limit int, since *time.Time) ([]Flag, error) {

	params := dbgen.QualityFlagsParams{
		FacilityID: facility, OpenOnly: openOnly, RowLimit: int32(limit), //nolint:gosec // bounded by the caller
	}
	if operator != nil {
		params.OperatorID = uuid.NullUUID{UUID: *operator, Valid: true}
	}
	if since != nil {
		params.Since = since
	}
	rows, err := s.q.QualityFlags(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Flag, 0, len(rows))
	for _, row := range rows {
		out = append(out, flagOf(dbgen.QualityFlagByIDRow(row)))
	}
	return out, nil
}

// Flag reads one.
func (s *Store) Flag(ctx context.Context, id, facility uuid.UUID) (Flag, error) {
	row, err := s.q.QualityFlagByID(ctx, dbgen.QualityFlagByIDParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Flag{}, ErrNoFlag
	}
	if err != nil {
		return Flag{}, err
	}
	return flagOf(row), nil
}

func flagOf(row dbgen.QualityFlagByIDRow) Flag {
	flag := Flag{
		ID: row.ID, FacilityID: row.FacilityID, OperatorID: row.OperatorID,
		OperatorCode: row.OperatorCode, OperatorNameEN: row.OperatorNameEn,
		OperatorNameBN: row.OperatorNameBn,
		ThresholdCode:  row.ThresholdCode, ThresholdEN: row.ThresholdEn,
		ThresholdBN: row.ThresholdBn, ActionEN: row.ActionEn, ActionBN: row.ActionBn,
		ThresholdApproved: row.ThresholdApprovedAt != nil,
		RaisedAt:          row.RaisedAt,
		Window: Window{
			From: row.WindowFrom, To: row.WindowTo,
			Days: int(row.WindowTo.Sub(row.WindowFrom).Hours() / 24),
		},
		ObservedCount: int(row.ObservedCount), EntriesCount: int(row.EntriesCount),
		Status: row.Status, ResolvedAt: row.ResolvedAt, Resolution: row.Resolution,
		Evidence: []Evidence{},
	}
	if len(row.Evidence) > 0 {
		var evidence []Evidence
		if err := json.Unmarshal(row.Evidence, &evidence); err == nil && evidence != nil {
			flag.Evidence = evidence
		}
	}
	if row.ResolvedBy.Valid {
		flag.ResolvedBy = row.ResolvedBy.UUID.String()
		flag.ResolvedByCode = row.ResolvedByCode
		flag.ResolvedByNameEN = row.ResolvedByNameEn
		flag.ResolvedByNameBN = row.ResolvedByNameBn
	}
	return flag
}

// sortEvidence puts the most recent correction first, so a supervisor reads the thing that just
// happened rather than the thing that happened four weeks ago.
func sortEvidence(evidence []Evidence) {
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].At.After(evidence[j].At) })
}

// --- the small store helpers the detector uses ---
//
// They are here rather than in detect.go because they are the only place `dbgen` parameter
// structs are built, and keeping that in one file is what makes "does anything in this module
// select a patient id" a question somebody can answer by reading one file.

func openFlagParams(operator uuid.UUID, threshold string) dbgen.OpenQualityFlagForParams {
	return dbgen.OpenQualityFlagForParams{OperatorID: operator, ThresholdCode: threshold}
}

func byCodeParams(facility, operator uuid.UUID, from, to time.Time) dbgen.CorrectionsByCodeParams {
	return dbgen.CorrectionsByCodeParams{
		FacilityID: facility, AssignedTo: operator, RequestedAt: from, RequestedAt_2: to,
	}
}

func (s *Store) entries(ctx context.Context, facility, operator uuid.UUID,
	from, to time.Time) (int, error) {

	count, err := s.q.EntriesRecorded(ctx, dbgen.EntriesRecordedParams{
		FacilityID: facility, RecordedBy: operator, RecordedAt: from, RecordedAt_2: to,
	})
	return int(count), err
}

// evidence is the narrowed list of corrections a threshold counts.
//
// The row limit is the threshold's own arithmetic taken to an absurd number: a flag is raised on
// three, and a hundred is far more than any supervisor will read. It exists so that an operator
// with a bad month cannot produce a flag whose evidence blob is a megabyte.
func (s *Store) evidence(ctx context.Context, facility, operator uuid.UUID,
	from, to time.Time, transcriptionOnly bool, code *string, afterHour *int) ([]Evidence, error) {

	params := dbgen.SupportingCorrectionsParams{
		Zone: Zone, FacilityID: facility, AssignedTo: operator,
		FromAt: from, ToAt: to, TranscriptionOnly: transcriptionOnly, RowLimit: 100,
	}
	if code != nil {
		params.OnlyCode = code
	}
	if afterHour != nil {
		hour := int32(*afterHour) //nolint:gosec // 0..23, checked by the table
		params.AfterHour = &hour
	}
	rows, err := s.q.SupportingCorrections(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Evidence, 0, len(rows))
	for _, row := range rows {
		// A rejected request is not evidence of anything: the operator looked, said the value
		// stands, and nobody overruled them. Counting it would teach every operator to accept
		// every flag without looking, which is the opposite of what this is for.
		if row.Status == "REJECTED" {
			continue
		}
		out = append(out, Evidence{
			RequestID:  row.RequestID,
			ReasonCode: row.ReasonCode, ReasonEN: row.ReasonEn, ReasonBN: row.ReasonBn,
			Code: row.Code, CodeEN: row.CodeEn, CodeBN: row.CodeBn,
			At: row.RequestedAt, Status: row.Status, StatusAsOf: to,
			Hour: int(row.RecordedHour),
		})
	}
	return out, nil
}

// raise writes the flag. The unique index is what makes this idempotent: a second open flag for
// the same operator and threshold is refused by the database, and `raised` comes back false.
func (s *Store) raise(ctx context.Context, facility, operator uuid.UUID, threshold Threshold,
	from, to time.Time, observed, entries int, evidence []byte) (Flag, bool, error) {

	row, err := s.q.RaiseQualityFlag(ctx, dbgen.RaiseQualityFlagParams{
		FacilityID: facility, OperatorID: operator, ThresholdCode: threshold.Code,
		RaisedAt: to, WindowFrom: from, WindowTo: to,
		ObservedCount: int32(observed), //nolint:gosec // small counts
		EntriesCount:  int32(entries),  //nolint:gosec // small counts
		Evidence:      evidence,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT DO NOTHING returned nothing: somebody raised it between the check and here.
		return Flag{}, false, nil
	}
	if err != nil {
		return Flag{}, false, err
	}
	flag, err := s.Flag(ctx, row.ID, facility)
	if err != nil {
		return Flag{}, false, err
	}
	return flag, true, nil
}

func (s *Store) attachAudit(ctx context.Context, id uuid.UUID, seq int64) error {
	return s.q.AttachQualityFlagAudit(ctx, dbgen.AttachQualityFlagAuditParams{ID: id, AuditSeq: &seq})
}

// Resolve is a supervisor answering a flag.
//
// Acknowledged means "I have seen this and I am doing something about it"; dismissed means "this
// is not a problem", and dismissal must say why — the same rule as a rejected correction, for
// the same reason. Neither deletes anything.
func (s *Store) Resolve(ctx context.Context, id, facility, by uuid.UUID,
	status, resolution string, now time.Time) (Flag, error) {

	status = strings.ToUpper(strings.TrimSpace(status))
	resolution = strings.TrimSpace(resolution)
	if status != "ACKNOWLEDGED" && status != "DISMISSED" {
		return Flag{}, fmt.Errorf("%w: %s", ErrUnknownStatus, status)
	}
	if status == "DISMISSED" && resolution == "" {
		return Flag{}, ErrReasonRequired
	}

	// Read first, so that "no such flag" and "already answered" are different answers. A single
	// UPDATE returning nothing cannot tell them apart, and a supervisor told "not found" about a
	// flag their colleague acknowledged a minute ago will go looking for a bug.
	existing, err := s.Flag(ctx, id, facility)
	if err != nil {
		return Flag{}, err
	}
	if !existing.Open() {
		return Flag{}, ErrFlagClosed
	}

	if _, err := s.q.ResolveQualityFlag(ctx, dbgen.ResolveQualityFlagParams{
		ID: id, FacilityID: facility, Status: status,
		ResolvedAt: &now, ResolvedBy: uuid.NullUUID{UUID: by, Valid: true},
		Resolution: resolution,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Flag{}, ErrFlagClosed
		}
		return Flag{}, err
	}
	return s.Flag(ctx, id, facility)
}
