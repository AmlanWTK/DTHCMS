// Package history is what the patient brings with them (CP53, §3 station 4, §11.1).
//
// # Why this is not the observation model
//
// An observation is a point measurement: one code, one value, one moment, and it never
// changes. A history item has an identity that outlives the visit — a complaint that started
// three weeks ago is still there next month, and a patient is either still on metformin or
// has stopped. Two of this checkpoint's four criteria are statements about that persistence,
// and neither is a sentence one can say about a measurement. ADR-0028 has the whole argument.
//
// # The four acts
//
// An item is **recorded**, and after that it can be **confirmed** (still true), **amended**
// (something about it changed), or **removed** (it should not have been recorded). What can
// never change is what the item *is*: the kind and the coding are fixed at recording, because
// changing them is removing one item and adding another, and collapsing those two acts is how
// an audit trail stops answering "when did this become metformin".
//
// # Carry-forward is a read
//
// Criterion 3 — prior history is presented for confirmation, never auto-accepted — is a
// safety property. A system that rolled last month's history into this month's would
// eventually assert, in a signed clinical document, that a patient is on a drug they stopped
// in March, and nobody could say who claimed it, because nobody did.
//
// So there is no carry-forward write. `ForPatient` returns the open items with the date each
// was last confirmed, and confirming is an event with an actor. Twenty items carried forward
// is twenty confirmations.
package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Kind is one of the six things a history is made of, and the rules for it.
//
// The rules live in the database beside the list rather than in six code paths, so a family
// history with no relation and a complaint with no duration are refused in one place — by a
// trigger the projection rebuild meets too.
type Kind struct {
	Kind      string `json:"kind"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`

	// CodeSystem is the catalogue this kind draws on. Named so a picker asks for the right
	// one without the screen deciding: a screen that chose ICD for complaints would produce
	// a record whose complaints are diagnoses, which is a different claim about the patient.
	CodeSystem string `json:"code_system"`

	RequiresRelation bool `json:"requires_relation"`
	RequiresDuration bool `json:"requires_duration"`
	AllowsSeverity   bool `json:"allows_severity"`
	AllowsOnset      bool `json:"allows_onset"`
	IsMedication     bool `json:"is_medication"`

	Ordering int `json:"ordering"`
}

// Relation is who a family history is about. `Degree` is why this is a table: first-degree
// family history is a risk factor with a number attached, and a query that had to enumerate
// which relations are first-degree would be a clinical rule in a WHERE clause.
type Relation struct {
	Relation  string `json:"relation"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	Degree    int    `json:"degree"`
	Ordering  int    `json:"ordering"`
}

// Item is one thing the patient brought with them.
type Item struct {
	ID        uuid.UUID `json:"id"`
	PatientID uuid.UUID `json:"patient_id"`
	Kind      string    `json:"kind"`

	// The coding (CP52). All three or none — a code with no system and no version is a
	// string, and an item may legitimately have none of the three when the catalogue has
	// nothing for what the patient described.
	CodeSystem  string `json:"code_system,omitempty"`
	CodeVersion string `json:"code_version,omitempty"`
	Code        string `json:"code,omitempty"`

	// The catalogue's words, joined rather than copied onto the item: a title corrected next
	// year should read correctly on every item coded with it.
	DisplayEN string `json:"display_en,omitempty"`
	DisplayBN string `json:"display_bn,omitempty"`
	Heading   string `json:"heading,omitempty"`
	HeadingBN string `json:"heading_bn,omitempty"`

	// Said is what the patient told this officer on this day, and it is the item's own. The
	// catalogue says "Type 2 diabetes mellitus without complications"; the patient said
	// "sugar since the flood", and the second one is the clinical detail.
	Said string `json:"said,omitempty"`

	Relation       string `json:"relation,omitempty"`
	DurationDays   *int   `json:"duration_days,omitempty"`
	Severity       string `json:"severity,omitempty"`
	OnsetOn        string `json:"onset_on,omitempty"`
	OnsetPrecision string `json:"onset_precision,omitempty"`

	Dose      string `json:"dose,omitempty"`
	Frequency string `json:"frequency,omitempty"`

	FormularyProductID string `json:"formulary_product_id,omitempty"`
	Reconciliation     string `json:"reconciliation,omitempty"`

	Status string `json:"status"`

	// Criterion 4, at every stage of the item's life.
	RecordedAt    time.Time `json:"recorded_at"`
	RecordedBy    uuid.UUID `json:"recorded_by"`
	RecordedRole  string    `json:"recorded_role,omitempty"`
	RecordedVisit string    `json:"recorded_visit,omitempty"`

	// Criterion 3. Absent means nobody has said this is still true — which is exactly what
	// station 4 is looking at when the patient comes back.
	ConfirmedAt    *time.Time `json:"confirmed_at,omitempty"`
	ConfirmedBy    string     `json:"confirmed_by,omitempty"`
	ConfirmedVisit string     `json:"confirmed_visit,omitempty"`

	AmendedAt *time.Time `json:"amended_at,omitempty"`
	AmendedBy string     `json:"amended_by,omitempty"`
}

// Coded says whether this item carries a coding at all.
//
// Public because it is the number criterion 1 is actually measured by. "Complaints and
// comorbidities are coded, not free text" is met by a catalogue good enough that officers use
// it, and the honest way to know whether it is good enough is to count the items that could
// not be coded — not to forbid the escape hatch and push the content into a note field where
// nothing can find it.
func (i Item) Coded() bool { return i.Code != "" }

// NeedsConfirmation is what carry-forward means: an item nobody has confirmed since it was
// recorded, or since a given moment. The moment is the caller's, because "this visit" is a
// question about the visit and this package does not own visits.
func (i Item) NeedsConfirmation(since time.Time) bool {
	if i.Status != "ACTIVE" {
		return false
	}
	if i.ConfirmedAt == nil {
		return true
	}
	return i.ConfirmedAt.Before(since)
}

var (
	// ErrUnknownKind is a kind of history that is not one of the six.
	ErrUnknownKind = errors.New("history: no such kind of history")

	// ErrNotFound is an item id that is not in this facility's record.
	ErrNotFound = errors.New("history: no such item")

	// ErrPartialCoding is two out of the three coding fields. Refused rather than repaired:
	// guessing the missing third is how a coding acquires a version nobody searched.
	ErrPartialCoding = errors.New("history: a coding is a system, a version and a code, or none")

	// ErrNothingSaid is an item with neither a coding nor words. It asserts that the patient
	// has something.
	ErrNothingSaid = errors.New("history: an uncoded item must say what was meant")

	// ErrWrongCatalogue is a concept from a terminology this kind does not draw on — an ICD
	// diagnosis filed as a presenting complaint. Refused because the resulting record would
	// assert that a patient *presented with* type 2 diabetes, which is a claim nobody made.
	ErrWrongCatalogue = errors.New("history: that concept is from the wrong catalogue for this kind")

	// ErrNeedsRelation, ErrNeedsDuration and their neighbours are the per-kind rules. Each
	// is also a database trigger; these exist so the officer sees a sentence rather than a
	// 500, and the trigger exists so the rule survives every path that is not this one.
	ErrNeedsRelation = errors.New("history: family history is about a relative — name the relation")
	ErrNeedsDuration = errors.New("history: a complaint says how long it has been going on")
	ErrNoSeverity    = errors.New("history: this kind of history carries no severity")
	ErrNoOnset       = errors.New("history: this kind of history carries no onset date")
	ErrNoDose        = errors.New("history: only a medicine carries a dose")
	ErrOnsetPartial  = errors.New("history: an onset date and its precision travel together")

	// ErrRemoved is an act on an item somebody has already removed.
	ErrRemoved = errors.New("history: that item was removed")
)

// Store reads the history and the two catalogues behind it.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

// InTransaction runs work against one transaction, so an event and its projection commit
// together or not at all.
func (s *Store) InTransaction(ctx context.Context,
	fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Kinds is the six, with their rules. Reference data: a station app fetches it once and
// renders the right fields from it, which is what stops a screen asking for a relation on a
// complaint or forgetting to ask for one on a family history.
func (s *Store) Kinds(ctx context.Context) ([]Kind, error) {
	rows, err := s.q.HistoryKinds(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Kind, 0, len(rows))
	for _, row := range rows {
		out = append(out, Kind{
			Kind: row.Kind, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			CodeSystem:       row.CodeSystem,
			RequiresRelation: row.RequiresRelation, RequiresDuration: row.RequiresDuration,
			AllowsSeverity: row.AllowsSeverity, AllowsOnset: row.AllowsOnset,
			IsMedication: row.IsMedication, Ordering: int(row.Ordering),
		})
	}
	return out, nil
}

// Relations is who a family history can be about.
func (s *Store) Relations(ctx context.Context) ([]Relation, error) {
	rows, err := s.q.FamilyRelations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Relation, 0, len(rows))
	for _, row := range rows {
		out = append(out, Relation{
			Relation: row.Relation, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			Degree: int(row.Degree), Ordering: int(row.Ordering),
		})
	}
	return out, nil
}

func (s *Store) kind(ctx context.Context, name string) (Kind, error) {
	row, err := s.q.HistoryKind(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Kind{}, fmt.Errorf("%w: %s", ErrUnknownKind, name)
	}
	if err != nil {
		return Kind{}, err
	}
	return Kind{
		Kind: row.Kind, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
		CodeSystem:       row.CodeSystem,
		RequiresRelation: row.RequiresRelation, RequiresDuration: row.RequiresDuration,
		AllowsSeverity: row.AllowsSeverity, AllowsOnset: row.AllowsOnset,
		IsMedication: row.IsMedication, Ordering: int(row.Ordering),
	}, nil
}

// ForPatient is everything currently believed about this patient, in station 4's order.
//
// Removed items are absent; RESOLVED ones are not. "She had this and no longer does" is a
// clinical fact, and a list that hid it would make every follow-up look like a first visit.
func (s *Store) ForPatient(ctx context.Context, patient uuid.UUID) ([]Item, error) {
	rows, err := s.q.HistoryForPatient(ctx, patient)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		out = append(out, itemFromRow(row))
	}
	return out, nil
}

// ByID reads one item, removed ones included — a removal is a thing somebody may need to look
// at, and hiding it here would mean the only way to see why is to read the ledger.
func (s *Store) ByID(ctx context.Context, id uuid.UUID) (Item, error) {
	row, err := s.q.HistoryItem(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	item := Item{
		ID: row.ID, PatientID: row.PatientID, Kind: row.Kind,
		DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
		Heading: row.Heading, HeadingBN: row.HeadingBn,
		Said: row.Said, Dose: row.Dose, Frequency: row.Frequency,
		Status:     row.Status,
		RecordedAt: row.RecordedAt, RecordedBy: row.RecordedBy, RecordedRole: row.RecordedRole,
		ConfirmedAt: row.ConfirmedAt, AmendedAt: row.AmendedAt,
	}
	fillOptional(&item, row.CodeSystem, row.CodeVersion, row.Code, row.Relation,
		row.DurationDays, row.Severity, row.OnsetOn, row.OnsetPrecision,
		row.Reconciliation, row.FormularyProductID,
		row.RecordedVisit, row.ConfirmedBy, row.ConfirmedVisit, row.AmendedBy)
	return item, nil
}

// Uncoded is how much of this facility's history could not be coded, by kind — the number
// that keeps the escape hatch honest. A growing count means the catalogue is wrong, not that
// the officers are.
func (s *Store) Uncoded(ctx context.Context, facility uuid.UUID) (map[string]int, error) {
	rows, err := s.q.UncodedHistoryCount(ctx, facility)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, row := range rows {
		out[row.Kind] = int(row.Uncoded)
	}
	return out, nil
}

func itemFromRow(row dbgen.HistoryForPatientRow) Item {
	item := Item{
		ID: row.ID, PatientID: row.PatientID, Kind: row.Kind,
		DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
		Heading: row.Heading, HeadingBN: row.HeadingBn,
		Said: row.Said, Dose: row.Dose, Frequency: row.Frequency,
		Status:     row.Status,
		RecordedAt: row.RecordedAt, RecordedBy: row.RecordedBy, RecordedRole: row.RecordedRole,
		ConfirmedAt: row.ConfirmedAt, AmendedAt: row.AmendedAt,
	}
	fillOptional(&item, row.CodeSystem, row.CodeVersion, row.Code, row.Relation,
		row.DurationDays, row.Severity, row.OnsetOn, row.OnsetPrecision,
		row.Reconciliation, row.FormularyProductID,
		row.RecordedVisit, row.ConfirmedBy, row.ConfirmedVisit, row.AmendedBy)
	return item
}

// fillOptional copies the nullable half of a row onto an item.
//
// Long, and factored out rather than repeated in two readers, because the two readers must
// not drift: an item read one way that carried its onset and the same item read the other way
// that did not would be a bug nobody finds until a clinician notices a date has vanished.
func fillOptional(item *Item, system, version, code, relation *string, duration *int32,
	severity *string, onset pgtype.Date, precision *string, reconciliation *string,
	product uuid.NullUUID,
	recordedVisit uuid.NullUUID, confirmedBy, confirmedVisit, amendedBy uuid.NullUUID) {

	// Null on everything that is not a drug, and that is the honest answer: the question of
	// whether the formulary has this was never asked of a vaccination.
	if reconciliation != nil {
		item.Reconciliation = *reconciliation
	}
	if system != nil {
		item.CodeSystem = *system
	}
	if version != nil {
		item.CodeVersion = *version
	}
	if code != nil {
		item.Code = *code
	}
	if relation != nil {
		item.Relation = *relation
	}
	if duration != nil {
		days := int(*duration)
		item.DurationDays = &days
	}
	if severity != nil {
		item.Severity = *severity
	}
	if onset.Valid {
		item.OnsetOn = onset.Time.Format("2006-01-02")
	}
	if precision != nil {
		item.OnsetPrecision = *precision
	}
	if product.Valid {
		item.FormularyProductID = product.UUID.String()
	}
	if recordedVisit.Valid {
		item.RecordedVisit = recordedVisit.UUID.String()
	}
	if confirmedBy.Valid {
		item.ConfirmedBy = confirmedBy.UUID.String()
	}
	if confirmedVisit.Valid {
		item.ConfirmedVisit = confirmedVisit.UUID.String()
	}
	if amendedBy.Valid {
		item.AmendedBy = amendedBy.UUID.String()
	}
}

// encode is the payload of an event, as JSON.
func encode(payload any) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding history event: %w", err)
	}
	return raw, nil
}

// trimmed is every string field on the way in. A complaint of "   " is not a complaint, and
// an officer who tabbed through a field should meet the same refusal as one who left it
// empty rather than storing a space and having the count of uncoded items miss it.
func trimmed(values ...*string) {
	for _, v := range values {
		*v = strings.TrimSpace(*v)
	}
}
