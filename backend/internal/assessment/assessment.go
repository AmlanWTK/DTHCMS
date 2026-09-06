// Package assessment is station 3's lifestyle questionnaires and the composite risk score
// (CP58, §3 step 3, §12).
//
// # Why this is not a corner of `clinical`
//
// ADR-0030 has the argument. In short: an instrument is not an observation. Forcing a
// questionnaire into `read.observation` leaves the same two bad options CP53 met — a JSON blob
// wearing a schema, or one observation per item, which throws away the version answered, the item
// order and the option that was chosen. All three are exactly what §12's cohorting reads.
//
// It imports `clinical` rather than duplicating it, because the score it produces **is** a
// clinical derived value and must be written by the one code path that knows how to write those.
//
// # The one rule that decides the schema
//
// Acceptance criterion 1: **raw item responses are stored, not just totals.** A total of 14 on a
// stress scale cannot be re-analysed, re-scored under a corrected formula, or compared against a
// study that weighted the items differently. Stored items are data; a stored total is a number
// somebody has to trust — so there is no total column anywhere, and every total on this package's
// surface is computed from the items on the way out.
//
// # D-26
//
// Several validated instruments are copyrighted, and which ones this clinic may run has not been
// decided. The framework ships; the content is rows; the copyrighted candidates are registered
// unusable and a database invariant refuses their items. Nothing here silently degrades if the
// answer never comes — `Instruments` reports what may not be used, with the sentence saying why.
package assessment

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Instrument is one questionnaire, and whether this clinic may run it.
type Instrument struct {
	Code      string `json:"code"`
	NameEN    string `json:"name_en"`
	NameBN    string `json:"name_bn"`
	PurposeEN string `json:"purpose_en,omitempty"`
	PurposeBN string `json:"purpose_bn,omitempty"`

	// Domain is what it asks about, for grouping a screen and for the composite to know which
	// part of somebody's life a subscale belongs to.
	Domain string `json:"domain"`

	// Usable is false for the copyrighted ones nobody has licensed. The row is returned anyway:
	// the absence of PHQ-9 from a screen is a decision somebody made, and a clinician who
	// expected to find it deserves the sentence rather than a gap.
	Usable          bool   `json:"usable"`
	CopyrightHolder string `json:"copyright_holder,omitempty"`
	// The licence note in both languages. The one sentence that must not be misunderstood — why a
	// counsellor cannot run PHQ-9 today — was English only until a Bangla-reading counsellor met
	// it on a screen.
	LicenceNote   string `json:"licence_note,omitempty"`
	LicenceNoteBN string `json:"licence_note_bn,omitempty"`
	// Provenance says whether this is published literature or something this clinic wrote. A fact
	// rather than a string comparison against seeded English prose: it holds up the rule that the
	// clinic's own readiness question must never read as a validated instrument.
	Provenance string `json:"provenance"`
	Ordering   int    `json:"ordering"`
	Version    int    `json:"version,omitempty"`
	// Scoring is how the item scores add up under the published version. Reported because a total
	// means different things under different schemes, and a screen showing "Total 3" for a
	// one-item question whose own note says the score means nothing on its own is a screen
	// inventing a finding.
	Scoring          string `json:"scoring,omitempty"`
	VersionPublished bool   `json:"version_published"`

	Items []Item `json:"items,omitempty"`
}

// Item is one question.
type Item struct {
	ItemCode   string `json:"item_code"`
	Ordering   int    `json:"ordering"`
	PromptEN   string `json:"prompt_en"`
	PromptBN   string `json:"prompt_bn"`
	AnswerType string `json:"answer_type"`

	Unit     string   `json:"unit,omitempty"`
	MinValue *float64 `json:"min_value,omitempty"`
	MaxValue *float64 `json:"max_value,omitempty"`
	Required bool     `json:"required"`

	Options []Option `json:"options,omitempty"`
}

// Option is one coded answer, and what it is worth.
type Option struct {
	OptionCode string `json:"option_code"`
	Ordering   int    `json:"ordering"`
	LabelEN    string `json:"label_en"`
	LabelBN    string `json:"label_bn"`
	Score      int    `json:"score"`
}

// Answer is one item, answered.
type Answer struct {
	ItemCode   string   `json:"item_code"`
	OptionCode string   `json:"option_code,omitempty"`
	ValueNum   *float64 `json:"value_num,omitempty"`
	ValueBool  *bool    `json:"value_bool,omitempty"`

	// Score is what this answer was worth under the version answered, decided by the server from
	// the option table and never read from the request. A client that could send a score could
	// send any total it liked.
	Score int `json:"score"`

	// The rendering, joined so a timeline reads "2–4 times a month" rather than a code.
	PromptEN string `json:"prompt_en,omitempty"`
	PromptBN string `json:"prompt_bn,omitempty"`
	OptionEN string `json:"option_en,omitempty"`
	OptionBN string `json:"option_bn,omitempty"`
}

// Response is one questionnaire, answered, by one person, at one moment.
type Response struct {
	ID        uuid.UUID  `json:"id"`
	PatientID uuid.UUID  `json:"patient_id"`
	VisitID   *uuid.UUID `json:"visit_id,omitempty"`

	InstrumentCode string `json:"instrument_code"`
	// InstrumentVersion is the wording answered. Criterion 4.
	InstrumentVersion int `json:"instrument_version"`

	RecordedAt   time.Time `json:"recorded_at"`
	RecordedBy   uuid.UUID `json:"recorded_by"`
	RecordedRole string    `json:"recorded_role,omitempty"`
	StationCode  string    `json:"station_code,omitempty"`
	DeviceID     string    `json:"device_id,omitempty"`
	Source       string    `json:"source,omitempty"`

	RecordedByCode   string `json:"recorded_by_code,omitempty"`
	RecordedByNameEN string `json:"recorded_by_name_en,omitempty"`
	RecordedByNameBN string `json:"recorded_by_name_bn,omitempty"`

	Status string `json:"status"`

	// Total is computed from the answers every time, never stored. Two columns that ought to
	// agree are two columns that will not, and on the day they disagree nobody can say which was
	// right.
	Total int `json:"total"`

	Answers []Answer `json:"answers"`
}

var (
	// ErrUnknownInstrument is a code that is not in the catalogue.
	ErrUnknownInstrument = errors.New("assessment: no such instrument")
	// ErrInstrumentNotLicensed is a questionnaire this clinic may not run (D-26). Refused rather
	// than silently skipped: an operator who was shown a form must be told why it will not save,
	// and a clinic that never sees this refusal never chases the licence.
	ErrInstrumentNotLicensed = errors.New("assessment: this clinic may not use that questionnaire")
	// ErrNoPublishedVersion is an instrument with no published wording to answer.
	ErrNoPublishedVersion = errors.New("assessment: that questionnaire has no published version")
	// ErrUnknownItem is an answer to a question the version does not ask.
	ErrUnknownItem = errors.New("assessment: that questionnaire does not ask that")
	// ErrUnknownOption is an option that is not one of the item's.
	ErrUnknownOption = errors.New("assessment: that is not one of the answers offered")
	// ErrWrongAnswerShape is a number where a choice was asked for, or the reverse.
	ErrWrongAnswerShape = errors.New("assessment: that answer is not the shape the question asks for")
	// ErrItemMissing is a required item left unanswered.
	ErrItemMissing = errors.New("assessment: a required question was not answered")
	// ErrOutOfRange is a numeric answer outside the item's declared band.
	ErrOutOfRange = errors.New("assessment: that answer is outside the range the question allows")
	// ErrNoResponse is a response that is not there.
	ErrNoResponse = errors.New("assessment: no such response")
	// ErrNoDeriver is a service assembled without the thing that writes clinical values. Not a
	// user-facing refusal: it is how a test that only wants to check the answers says so, and it
	// is reported rather than silently producing no score.
	ErrNoDeriver = errors.New("assessment: no deriver is attached")
)

// Store reads the catalogue and the answers.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds a store over a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// Instruments is the catalogue: what may be run, and what may not.
//
// `withItems` loads the questions too. A station app fetches them once per session and then works
// offline, which is why the whole questionnaire comes down in one response rather than an
// instrument list followed by a request per instrument.
func (s *Store) Instruments(ctx context.Context, withItems bool) ([]Instrument, error) {
	rows, err := s.q.UsableInstruments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(rows))
	for _, row := range rows {
		instrument := Instrument{
			Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn,
			PurposeEN: row.PurposeEn, PurposeBN: row.PurposeBn,
			Domain: row.Domain, Usable: row.Usable,
			CopyrightHolder: row.CopyrightHolder,
			LicenceNote:     row.LicenceNote, LicenceNoteBN: row.LicenceNoteBn,
			Provenance: row.Provenance,
			Ordering:   int(row.Ordering),
		}
		// An unusable instrument has no items — an invariant sees to that — so there is nothing
		// to load and asking would be a query per row for nothing.
		if withItems && row.Usable {
			version, scoring, items, err := s.published(ctx, row.Code)
			if err != nil && !errors.Is(err, ErrNoPublishedVersion) {
				return nil, err
			}
			if err == nil {
				instrument.Version = version
				instrument.Scoring = scoring
				instrument.VersionPublished = true
				instrument.Items = items
			}
		}
		out = append(out, instrument)
	}
	return out, nil
}

// published is the latest published version of one instrument, with its items and options.
func (s *Store) published(ctx context.Context, code string) (int, string, []Item, error) {
	version, err := s.q.LatestInstrumentVersion(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", nil, ErrNoPublishedVersion
	}
	if err != nil {
		return 0, "", nil, err
	}

	itemRows, err := s.q.InstrumentItems(ctx, dbgen.InstrumentItemsParams{
		InstrumentCode: code, Version: version.Version,
	})
	if err != nil {
		return 0, "", nil, err
	}
	optionRows, err := s.q.InstrumentOptions(ctx, dbgen.InstrumentOptionsParams{
		InstrumentCode: code, Version: version.Version,
	})
	if err != nil {
		return 0, "", nil, err
	}
	byItem := map[string][]Option{}
	for _, row := range optionRows {
		byItem[row.ItemCode] = append(byItem[row.ItemCode], Option{
			OptionCode: row.OptionCode, Ordering: int(row.Ordering),
			LabelEN: row.LabelEn, LabelBN: row.LabelBn, Score: int(row.Score),
		})
	}

	items := make([]Item, 0, len(itemRows))
	for _, row := range itemRows {
		item := Item{
			ItemCode: row.ItemCode, Ordering: int(row.Ordering),
			PromptEN: row.PromptEn, PromptBN: row.PromptBn,
			AnswerType: row.AnswerType, Required: row.Required,
			Options: byItem[row.ItemCode],
		}
		if row.Unit != nil {
			item.Unit = *row.Unit
		}
		item.MinValue = numericOf(row.MinValue)
		item.MaxValue = numericOf(row.MaxValue)
		items = append(items, item)
	}
	return int(version.Version), version.Scoring, items, nil
}

// CatalogueVersion is when the catalogue last changed, so a tablet holding it for a morning can
// re-check cheaply rather than discovering a republished version as a 422 after a patient has
// answered.
func (s *Store) CatalogueVersion(ctx context.Context) (time.Time, error) {
	return s.q.CatalogueVersion(ctx)
}

// Live is the current answers to one instrument for one patient.
func (s *Store) Live(ctx context.Context, patient uuid.UUID, code string,
	facility uuid.UUID) (Response, error) {

	row, err := s.q.LiveResponse(ctx, dbgen.LiveResponseParams{
		PatientID: patient, InstrumentCode: code, FacilityID: facility,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Response{}, ErrNoResponse
	}
	if err != nil {
		return Response{}, err
	}
	response := responseOf(dbgen.ResponsesForPatientRow(row))
	response.Answers, err = s.answers(ctx, response.ID)
	if err != nil {
		return Response{}, err
	}
	return response, nil
}

// ForPatient is every response, newest first — including superseded ones, because a questionnaire
// answered differently three months ago is the change §12 is looking for.
func (s *Store) ForPatient(ctx context.Context, patient, facility uuid.UUID,
	limit int) ([]Response, error) {

	rows, err := s.q.ResponsesForPatient(ctx, dbgen.ResponsesForPatientParams{
		PatientID: patient, FacilityID: facility,
		Limit: int32(limit), //nolint:gosec // bounded by the handler
	})
	if err != nil {
		return nil, err
	}
	out := make([]Response, 0, len(rows))
	for _, row := range rows {
		response := responseOf(row)
		response.Answers, err = s.answers(ctx, response.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, response)
	}
	return out, nil
}

func (s *Store) answers(ctx context.Context, response uuid.UUID) ([]Answer, error) {
	rows, err := s.q.AnswersFor(ctx, response)
	if err != nil {
		return nil, err
	}
	out := make([]Answer, 0, len(rows))
	for _, row := range rows {
		answer := Answer{
			ItemCode: row.ItemCode, Score: int(row.Score),
			PromptEN: row.PromptEn, PromptBN: row.PromptBn,
			OptionEN: row.OptionEn, OptionBN: row.OptionBn,
		}
		if row.OptionCode != nil {
			answer.OptionCode = *row.OptionCode
		}
		answer.ValueNum = numericOf(row.ValueNum)
		answer.ValueBool = row.ValueBool
		out = append(out, answer)
	}
	return out, nil
}

func responseOf(row dbgen.ResponsesForPatientRow) Response {
	out := Response{
		ID: row.ID, PatientID: row.PatientID,
		InstrumentCode: row.InstrumentCode, InstrumentVersion: int(row.InstrumentVersion),
		RecordedAt: row.RecordedAt, RecordedBy: row.RecordedBy,
		RecordedRole: row.RecordedRole, StationCode: row.StationCode,
		Source: row.Source, Status: row.Status,
		RecordedByCode:   row.RecordedByCode,
		RecordedByNameEN: row.RecordedByNameEn, RecordedByNameBN: row.RecordedByNameBn,
		Answers: []Answer{},
	}
	out.Total = int(row.Total)
	if row.VisitID.Valid {
		visit := row.VisitID.UUID
		out.VisitID = &visit
	}
	if row.DeviceID.Valid {
		out.DeviceID = row.DeviceID.UUID.String()
	}
	return out
}

// find is the item with this code, or nothing.
func find(items []Item, code string) (Item, bool) {
	for _, item := range items {
		if item.ItemCode == code {
			return item, true
		}
	}
	return Item{}, false
}

func known(item Item, option string) (int, bool) {
	for _, candidate := range item.Options {
		if candidate.OptionCode == option {
			return candidate.Score, true
		}
	}
	return 0, false
}

func trimmed(s string) string { return strings.TrimSpace(s) }

// numericOf turns a database `numeric` into an optional float, absent when it is null.
//
// A local copy of `clinical`'s converter rather than an import: the exported surface of that
// package is its clinical vocabulary, and widening it to include a numeric-conversion helper would
// make a module boundary answer a question about a database driver. The conversion is at the edge
// here for the same reason it is there — the database keeps exact decimal arithmetic and JSON has
// one number type, so the lossy step belongs at serialisation.
func numericOf(n pgtype.Numeric) *float64 {
	if !n.Valid || n.NaN || n.Int == nil {
		return nil
	}
	value := new(big.Float).SetInt(n.Int)
	if n.Exp != 0 {
		scale := big.NewFloat(1)
		ten := big.NewFloat(10)
		exp := n.Exp
		if exp < 0 {
			exp = -exp
		}
		for i := int32(0); i < exp; i++ {
			scale.Mul(scale, ten)
		}
		if n.Exp > 0 {
			value.Mul(value, scale)
		} else {
			value.Quo(value, scale)
		}
	}
	out, _ := value.Float64()
	if math.IsNaN(out) {
		return nil
	}
	return &out
}
