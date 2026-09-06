// Package nutrition is station 7's 24-hour recall (CP59, §12.1).
//
// # The design decision that matters, stated once
//
// Criterion 2 and [R-01]/[R-02] ask that two assistants contribute to one recall from two devices,
// concurrently, each attributed. The obvious implementation is a document two devices edit, and
// then a lock, or a merge, or last-write-wins. All three answer a question this shape does not
// have, and the third loses a patient's breakfast without telling anybody.
//
// **A 24-hour recall is not a document. It is a set of things a patient said they ate.** Each is
// its own row, written once, by one named person, never edited. Two operators cannot collide
// because they never write the same row — the conflict is designed out rather than resolved, which
// is the only version of concurrent editing that is correct at four in the afternoon with a queue
// waiting.
//
// The one real collision — the same food entered twice by two people who did not see each other —
// is a **duplicate**, not a conflict, and it is handled where duplicates belong: visibly, by
// showing who already recorded what, and corrected by a withdrawal somebody signed.
//
// # Household measures
//
// A patient says "two cups", not "three hundred grams". A screen that asks for grams asks the
// operator to convert in their head in front of a patient, and the number that reaches the record
// is then the operator's arithmetic rather than the patient's answer. So the operator records the
// measure and the count, and the weight is the food table's business — both are stored, because the
// answer as given is the evidence and the grams are an interpretation of it.
package nutrition

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

// Food is one row of the composition table.
type Food struct {
	Code           string  `json:"code"`
	NameEN         string  `json:"name_en"`
	NameBN         string  `json:"name_bn"`
	Group          string  `json:"group_code"`
	KcalPer100g    float64 `json:"kcal_per_100g"`
	ProteinPer100g float64 `json:"protein_per_100g"`
	CarbPer100g    float64 `json:"carb_per_100g"`
	FatPer100g     float64 `json:"fat_per_100g"`

	// Source is where the figures came from, on every row, because a composition table assembled
	// from three sources and remembered as one is a table nobody can check.
	Source string `json:"source"`
	// Approved is false on everything seeded. The plan names the food table as a content
	// dependency needing a national source; this is what stops a starter list being mistaken for
	// the clinic's agreed table.
	Approved   bool       `json:"approved"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`

	Portions []Portion `json:"portions,omitempty"`
}

// Portion is what one household measure of one food weighs.
type Portion struct {
	MeasureCode string  `json:"measure_code"`
	MeasureEN   string  `json:"measure_en"`
	MeasureBN   string  `json:"measure_bn"`
	Grams       float64 `json:"grams"`
	// NoteEN and NoteBN say how big: "one medium ruti", "a small teacup". A measure without a
	// size is a measure two operators use differently.
	NoteEN string `json:"note_en,omitempty"`
	NoteBN string `json:"note_bn,omitempty"`
	// Universal marks a measure that means the same for every food. Only grams.
	Universal bool `json:"universal"`
	// MaxQuantity is the most of this measure one entry may carry, so a screen offering a portion
	// also knows what it may accept against it.
	MaxQuantity int `json:"max_quantity"`
}

// Meal is one of the day's, with its name.
type Meal struct {
	Code     string `json:"code"`
	NameEN   string `json:"name_en"`
	NameBN   string `json:"name_bn"`
	Ordering int    `json:"ordering"`
}

// Day is a date this patient has a recall for, and how much is on it.
type Day struct {
	Date    string `json:"date"`
	Entries int    `json:"entries"`
}

// Measure is one household measure.
type Measure struct {
	Code      string `json:"code"`
	NameEN    string `json:"name_en"`
	NameBN    string `json:"name_bn"`
	Universal bool   `json:"universal"`
	// MaxQuantity is the most of this measure one entry may carry, on the measure rather than as a
	// pair of payload-level numbers keyed by a string. A hundred cups is nobody's lunch; two
	// hundred grams is an ordinary plate of rice — and a client discriminating those by comparing
	// a code against "GRAM" would be right until the second universal measure was seeded.
	MaxQuantity int `json:"max_quantity"`
	Ordering    int `json:"ordering"`
}

// Entry is one thing the patient said they ate.
type Entry struct {
	ID        uuid.UUID  `json:"id"`
	PatientID uuid.UUID  `json:"patient_id"`
	VisitID   *uuid.UUID `json:"visit_id,omitempty"`

	RecallDate  string `json:"recall_date"`
	Meal        string `json:"meal"`
	EatenAtHour *int   `json:"eaten_at_hour,omitempty"`

	FoodCode string `json:"food_code"`
	FoodEN   string `json:"food_en"`
	FoodBN   string `json:"food_bn"`

	// Quantity and MeasureCode are the answer as given — "two cups" — and Grams is what the table
	// says that weighs. Both, because a record holding only the grams could never be re-derived
	// when the portion table is corrected.
	Quantity    float64 `json:"quantity"`
	MeasureCode string  `json:"measure_code"`
	MeasureEN   string  `json:"measure_en"`
	MeasureBN   string  `json:"measure_bn"`
	Grams       float64 `json:"grams"`

	Kcal    float64 `json:"kcal"`
	Protein float64 `json:"protein"`
	Carb    float64 `json:"carb"`
	Fat     float64 `json:"fat"`

	Note string `json:"note,omitempty"`

	RecordedAt   time.Time `json:"recorded_at"`
	RecordedBy   uuid.UUID `json:"recorded_by"`
	RecordedRole string    `json:"recorded_role,omitempty"`
	StationCode  string    `json:"station_code,omitempty"`
	DeviceID     string    `json:"device_id,omitempty"`
	Source       string    `json:"source,omitempty"`

	RecordedByCode   string `json:"recorded_by_code,omitempty"`
	RecordedByNameEN string `json:"recorded_by_name_en,omitempty"`
	RecordedByNameBN string `json:"recorded_by_name_bn,omitempty"`

	WithdrawnAt *time.Time `json:"withdrawn_at,omitempty"`
	// WithdrawnBy was selected and then dropped, while RecordedBy was reported — so a client could
	// say "you recorded this" and could not say "you took this back". Asymmetric for no reason.
	WithdrawnBy       string `json:"withdrawn_by,omitempty"`
	WithdrawnReason   string `json:"withdrawn_reason,omitempty"`
	WithdrawnByCode   string `json:"withdrawn_by_code,omitempty"`
	WithdrawnByNameEN string `json:"withdrawn_by_name_en,omitempty"`
	WithdrawnByNameBN string `json:"withdrawn_by_name_bn,omitempty"`
}

// Standing says whether this entry still counts.
func (e Entry) Standing() bool { return e.WithdrawnAt == nil }

// Totals is a day's eating, summed over the entries that still stand.
type Totals struct {
	Kcal    float64 `json:"kcal"`
	Protein float64 `json:"protein"`
	Carb    float64 `json:"carb"`
	Fat     float64 `json:"fat"`
	Entries int     `json:"entries"`
	// Contributors is how many people recorded part of this recall. Reported because a recall two
	// assistants built is the thing [R-01] asked for, and a screen that could not show it would
	// make the feature invisible to the people using it.
	Contributors int `json:"contributors"`
}

// Recall is one day, as the operator sees it.
type Recall struct {
	PatientID  uuid.UUID `json:"patient_id"`
	RecallDate string    `json:"recall_date"`
	Entries    []Entry   `json:"entries"`
	Totals     Totals    `json:"totals"`
}

var (
	// ErrUnknownFood is a food that is not in the table.
	ErrUnknownFood = errors.New("nutrition: that food is not in the table")
	// ErrUnknownMeasure is a measure the table has no portion row for. Refused rather than
	// guessed: a guessed weight becomes a calorie count somebody acts on.
	ErrUnknownMeasure = errors.New("nutrition: the table does not know what that measure of that food weighs")
	// ErrNoEntry is an entry that is not there.
	ErrNoEntry = errors.New("nutrition: no such entry")
	// ErrAlreadyWithdrawn is a second withdrawal.
	ErrAlreadyWithdrawn = errors.New("nutrition: that entry has already been taken back")
	// ErrReasonRequired is a withdrawal that says nothing. An entry that vanishes with no reason
	// is a gap the other operator has to explain.
	ErrReasonRequired = errors.New("nutrition: say why the entry is being taken back")
	// ErrBadQuantity is a quantity of none, or of a hundred cups.
	ErrBadQuantity = errors.New("nutrition: that is not a quantity somebody ate")
	// ErrBadMeal is a meal that is not one of the day's.
	ErrBadMeal = errors.New("nutrition: that is not one of the day's meals")
	// ErrBadDate is a recall date that is not a date.
	ErrBadDate = errors.New("nutrition: that is not a day")
)

// Meals are the day's, in the order it happens.
var Meals = []string{"BREAKFAST", "MID_MORNING", "LUNCH", "AFTERNOON", "DINNER", "BEDTIME", "OTHER"}

// Store reads the food table and the recall.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// Search is the picker.
func (s *Store) Search(ctx context.Context, term, group string, limit int) ([]Food, error) {
	params := dbgen.SearchFoodsParams{
		Term: strings.TrimSpace(term), RowLimit: int32(limit), //nolint:gosec // bounded by the handler
	}
	if group = strings.TrimSpace(group); group != "" {
		params.GroupCode = &group
	}
	rows, err := s.q.SearchFoods(ctx, params)
	if err != nil {
		return nil, err
	}
	foods := make([]Food, 0, len(rows))
	codes := make([]string, 0, len(rows))
	for _, row := range rows {
		foods = append(foods, foodOf(dbgen.FoodByCodeRow(row)))
		codes = append(codes, row.Code)
	}
	if len(codes) == 0 {
		return foods, nil
	}
	// The portions come with the foods, in one more query rather than one per food. A picker that
	// showed a food and then asked what a cup of it weighed would be a second round trip inside
	// the four minutes criterion 1 allows for the whole recall.
	portions, err := s.q.PortionsFor(ctx, codes)
	if err != nil {
		return nil, err
	}
	byFood := map[string][]Portion{}
	for _, row := range portions {
		byFood[row.FoodCode] = append(byFood[row.FoodCode], Portion{
			MeasureCode: row.MeasureCode, MeasureEN: row.MeasureEn, MeasureBN: row.MeasureBn,
			Grams: floatOf(row.Grams), NoteEN: row.NoteEn, NoteBN: row.NoteBn,
			Universal: row.Universal, MaxQuantity: int(row.MaxQuantity),
		})
	}
	for i := range foods {
		foods[i].Portions = byFood[foods[i].Code]
	}
	return foods, nil
}

// Measures is the household vocabulary.
func (s *Store) Measures(ctx context.Context) ([]Measure, error) {
	rows, err := s.q.FoodMeasures(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Measure, 0, len(rows))
	for _, row := range rows {
		out = append(out, Measure{
			Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn,
			Universal: row.Universal, MaxQuantity: int(row.MaxQuantity),
			Ordering: int(row.Ordering),
		})
	}
	return out, nil
}

// Recall is one day's eating for one patient.
func (s *Store) Recall(ctx context.Context, patient, facility uuid.UUID,
	day time.Time) (Recall, error) {

	rows, err := s.q.DietEntries(ctx, dbgen.DietEntriesParams{
		PatientID: patient, FacilityID: facility, RecallDate: day,
	})
	if err != nil {
		return Recall{}, err
	}
	out := Recall{
		PatientID: patient, RecallDate: day.Format("2006-01-02"),
		Entries: make([]Entry, 0, len(rows)),
	}
	for _, row := range rows {
		out.Entries = append(out.Entries, entryOf(row))
	}

	totals, err := s.q.DietTotals(ctx, dbgen.DietTotalsParams{
		PatientID: patient, FacilityID: facility, RecallDate: day,
	})
	if err != nil {
		return Recall{}, err
	}
	out.Totals = Totals{
		Kcal: floatOf(totals.Kcal), Protein: floatOf(totals.Protein),
		Carb: floatOf(totals.Carb), Fat: floatOf(totals.Fat),
		Entries: int(totals.Entries), Contributors: int(totals.Contributors),
	}
	return out, nil
}

// Meals is the day's, with their names.
func (s *Store) Meals(ctx context.Context) ([]Meal, error) {
	rows, err := s.q.Meals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Meal, 0, len(rows))
	for _, row := range rows {
		out = append(out, Meal{
			Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn,
			Ordering: int(row.Ordering),
		})
	}
	return out, nil
}

// Days is which dates this patient has a recall for, and how much is on each.
func (s *Store) Days(ctx context.Context, patient, facility uuid.UUID, limit int) ([]Day, error) {
	rows, err := s.q.RecallDates(ctx, dbgen.RecallDatesParams{
		PatientID: patient, FacilityID: facility,
		Limit: int32(limit), //nolint:gosec // bounded by the handler
	})
	if err != nil {
		return nil, err
	}
	// The count was computed and thrown away. A list of bare dates makes a client ask again to
	// find out which of them is worth opening.
	out := make([]Day, 0, len(rows))
	for _, row := range rows {
		out = append(out, Day{
			Date: row.RecallDate.Format("2006-01-02"), Entries: int(row.Entries),
		})
	}
	return out, nil
}

// KnowsPatient says whether this facility has a patient by that id.
//
// Used only so that an unknown id answers 404 rather than 200 with an empty recall — which, on a
// screen showing a day's food, is indistinguishable from a genuinely empty day.
func (s *Store) KnowsPatient(ctx context.Context, patient, facility uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM core.patient WHERE id = $1 AND facility_id = $2)`,
		patient, facility).Scan(&exists)
	return exists, err
}

func foodOf(row dbgen.FoodByCodeRow) Food {
	return Food{
		Code: row.Code, NameEN: row.NameEn, NameBN: row.NameBn, Group: row.GroupCode,
		KcalPer100g: floatOf(row.KcalPer100g), ProteinPer100g: floatOf(row.ProteinPer100g),
		CarbPer100g: floatOf(row.CarbPer100g), FatPer100g: floatOf(row.FatPer100g),
		Source: row.Source, Approved: row.ApprovedAt != nil, ApprovedAt: row.ApprovedAt,
	}
}

func entryOf(row dbgen.DietEntriesRow) Entry {
	out := Entry{
		ID: row.ID, PatientID: row.PatientID,
		RecallDate: row.RecallDate.Format("2006-01-02"),
		Meal:       row.Meal,
		FoodCode:   row.FoodCode, FoodEN: row.FoodEn, FoodBN: row.FoodBn,
		Quantity: floatOf(row.Quantity), MeasureCode: row.MeasureCode,
		MeasureEN: row.MeasureEn, MeasureBN: row.MeasureBn, Grams: floatOf(row.Grams),
		Kcal: floatOf(row.Kcal), Protein: floatOf(row.Protein),
		Carb: floatOf(row.Carb), Fat: floatOf(row.Fat),
		Note:       row.Note,
		RecordedAt: row.RecordedAt, RecordedBy: row.RecordedBy,
		RecordedRole: row.RecordedRole, StationCode: row.StationCode, Source: row.Source,
		RecordedByCode:   row.RecordedByCode,
		RecordedByNameEN: row.RecordedByNameEn, RecordedByNameBN: row.RecordedByNameBn,
		WithdrawnAt: row.WithdrawnAt, WithdrawnReason: row.WithdrawnReason,
		WithdrawnByCode:   row.WithdrawnByCode,
		WithdrawnByNameEN: row.WithdrawnByNameEn, WithdrawnByNameBN: row.WithdrawnByNameBn,
	}
	if row.VisitID.Valid {
		visit := row.VisitID.UUID
		out.VisitID = &visit
	}
	if row.DeviceID.Valid {
		out.DeviceID = row.DeviceID.UUID.String()
	}
	if row.WithdrawnBy.Valid {
		out.WithdrawnBy = row.WithdrawnBy.UUID.String()
	}
	if row.EatenAtHour != nil {
		hour := int(*row.EatenAtHour)
		out.EatenAtHour = &hour
	}
	return out
}

// floatOf turns a database `numeric` into a float at the edge, where the lossy step belongs: the
// database keeps exact decimal arithmetic and JSON has one number type.
func floatOf(n pgtype.Numeric) float64 {
	if !n.Valid || n.NaN || n.Int == nil {
		return 0
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
		return 0
	}
	return out
}

// InTransaction runs fn against a transaction on this store's pool.
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
