package prescription

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Reading the prescription read model (CP80).
//
// Every statement in `queries/prescription.sql` is a SELECT, and that is not a style choice: the
// application role holds no INSERT, UPDATE or DELETE on either table. A write here would not
// compile into a working binary — it would fail at runtime with a permission error, every time,
// in every environment, which is the point.

// Store reads prescriptions.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: dbgen.New(pool)} }

// InTransaction runs fn in one transaction, so an event and its synchronous projection commit
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

// Machine builds the state machine from `core.prescription_transition`.
//
// Read once at start-up by the composition root. Built from the rows rather than from a literal
// because two copies of a state machine agree until somebody edits one, and the failure then is
// an application that permits a transition the trigger refuses — or the other way round, which
// is worse, because it means an event reached the ledger describing a state change the read
// model then refused to apply.
func (s *Store) Machine(ctx context.Context) (*Machine, error) {
	rows, err := s.q.PrescriptionTransitions(ctx)
	if err != nil {
		return nil, err
	}
	edges := make([]Edge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, Edge{
			From: Status(row.FromStatus), To: Status(row.ToStatus),
			EventType: row.EventType, NoteEN: row.NoteEn, NoteBN: row.NoteBn,
			OwnedBy: row.OwnedBy,
		})
	}
	if len(edges) == 0 {
		// An empty matrix would silently refuse every transition, which looks exactly like a
		// very safe system and is in fact a broken one.
		return nil, errors.New("core.prescription_transition is empty: the state machine has no legal edges")
	}
	return NewMachine(edges), nil
}

// StatusLabel is one row of `core.prescription_status`.
type StatusLabel struct {
	Status    Status `json:"status"`
	NameEN    string `json:"name_en"`
	NameBN    string `json:"name_bn"`
	MeaningEN string `json:"meaning_en"`
	MeaningBN string `json:"meaning_bn"`
	Frozen    bool   `json:"is_frozen"`
	Terminal  bool   `json:"is_terminal"`
}

// Statuses is the catalogue, for a screen that would otherwise keep its own copy.
func (s *Store) Statuses(ctx context.Context) ([]StatusLabel, error) {
	rows, err := s.q.PrescriptionStatuses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]StatusLabel, 0, len(rows))
	for _, row := range rows {
		out = append(out, StatusLabel{
			Status: Status(row.Status), NameEN: row.NameEn, NameBN: row.NameBn,
			MeaningEN: row.MeaningEn, MeaningBN: row.MeaningBn,
			Frozen: row.IsFrozen, Terminal: row.IsTerminal,
		})
	}
	return out, nil
}

// ByID reads one prescription with every line it has ever carried.
func (s *Store) ByID(ctx context.Context, id, facility uuid.UUID) (Prescription, error) {
	row, err := s.q.Prescription(ctx, dbgen.PrescriptionParams{ID: id, FacilityID: facility})
	if errors.Is(err, pgx.ErrNoRows) {
		return Prescription{}, ErrNotFound
	}
	if err != nil {
		return Prescription{}, err
	}
	out := prescriptionOf(row)
	items, err := s.q.PrescriptionItems(ctx, id)
	if err != nil {
		return Prescription{}, err
	}
	out.Items = make([]Item, 0, len(items))
	for _, item := range items {
		out.Items = append(out.Items, itemOf(item))
	}
	return out, nil
}

// ForPatient is this patient's prescriptions, newest first, without their items.
func (s *Store) ForPatient(ctx context.Context, patient, facility uuid.UUID, limit int) ([]Prescription, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.q.PrescriptionsForPatient(ctx, dbgen.PrescriptionsForPatientParams{
		PatientID: patient, FacilityID: facility, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Prescription, 0, len(rows))
	for _, row := range rows {
		out = append(out, prescriptionOf(row))
	}
	return out, nil
}

// nextLine is the next free line number on a draft.
//
// From the table rather than from `len(items)`, because two items added from two tabs would
// otherwise both be line 3. Removed lines keep their numbers: reusing one would make two rows in
// the same sheet's history claim the same position.
func (s *Store) nextLine(ctx context.Context, prescription uuid.UUID) (int, error) {
	n, err := s.q.NextPrescriptionLine(ctx, prescription)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func prescriptionOf(row dbgen.ReadPrescription) Prescription {
	status := Status(row.Status)
	out := Prescription{
		ID: row.ID, FacilityID: row.FacilityID, PatientID: row.PatientID, VisitID: row.VisitID,
		Status:      status,
		CreatedAt:   row.CreatedAt,
		CreatedBy:   row.CreatedBy,
		CreatedRole: row.CreatedRole,
		SubmittedAt: row.SubmittedAt, BouncedAt: row.BouncedAt,
		SignedAt: row.SignedAt, PrintedAt: row.PrintedAt, DispensedAt: row.DispensedAt,
		CancelledAt: row.CancelledAt, CancelledReason: row.CancelledReason,
		CorrectedAt:               row.CorrectedAt,
		CorrectionReason:          row.CorrectionReason,
		CorrectsDispensedOriginal: row.CorrectsDispensedOriginal,
		UpdatedAt:                 row.UpdatedAt,
		Editable:                  status.Editable(),
		Items:                     []Item{},
	}
	out.SubmittedBy = uuidOrNil(row.SubmittedBy)
	out.SignedBy = uuidOrNil(row.SignedBy)
	out.CancelledBy = uuidOrNil(row.CancelledBy)
	out.CorrectedBy = uuidOrNil(row.CorrectedBy)
	out.Corrects = uuidOrNil(row.CorrectsPrescriptionID)
	out.CorrectedBySuccessor = uuidOrNil(row.CorrectedByPrescriptionID)
	out.CarriedForwardFrom = uuidOrNil(row.CarriedForwardFrom)
	return out
}

func itemOf(row dbgen.ReadPrescriptionItem) Item {
	out := Item{
		ID: row.ID, LineNo: int(row.LineNo),
		ProductID:   uuidOrNil(row.ProductID),
		Label:       row.ProductLabel,
		GenericName: row.GenericName, Strength: row.Strength, FormCode: row.FormCode,
		Dose: row.Dose, DoseUnit: row.DoseUnit, Frequency: row.Frequency, Route: row.Route,
		InstructionsEN: row.InstructionsEn, InstructionsBN: row.InstructionsBn,
		RecordedAt: row.RecordedAt, RecordedBy: row.RecordedBy,
		ModifiedAt: row.ModifiedAt, RemovedAt: row.RemovedAt,
		RemovedReason: row.RemovedReason,
	}
	out.ModifiedBy = uuidOrNil(row.ModifiedBy)
	out.RemovedBy = uuidOrNil(row.RemovedBy)
	out.CarriedForwardFromItem = uuidOrNil(row.CarriedForwardFromItem)
	out.DailyDose = floatOf(row.DailyDose)
	out.Quantity = floatOf(row.Quantity)
	if row.DurationDays != nil {
		days := int(*row.DurationDays)
		out.DurationDays = &days
	}
	if row.PricePoisha != nil && row.PriceID.Valid && row.PriceCapturedAt != nil {
		price := CapturedPrice{
			AmountPoisha: *row.PricePoisha,
			AmountBDT:    takaOf(*row.PricePoisha),
			PriceID:      row.PriceID.UUID,
			CapturedAt:   *row.PriceCapturedAt,
		}
		if row.PriceEffectiveFrom != nil {
			price.EffectiveFrom = row.PriceEffectiveFrom.Format("2006-01-02")
		}
		if row.PriceVerification != nil {
			price.Verification = *row.PriceVerification
		}
		out.Price = &price
	}
	return out
}

func uuidOrNil(in uuid.NullUUID) *uuid.UUID {
	if !in.Valid {
		return nil
	}
	out := in.UUID
	return &out
}

// floatOf reads a numeric column.
//
// `pgtype.Numeric` rather than float64 all the way through, because a daily dose of 0.125 mg —
// levothyroxine, prescribed in this clinic every day — is not representable in binary floating
// point, and the column is numeric precisely so the stored value is exact. The conversion
// happens once, here, at the edge.
func floatOf(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	v, err := n.Float64Value()
	if err != nil || !v.Valid {
		return nil
	}
	out := v.Float64
	return &out
}

// takaOf renders poisha as taka, by integer arithmetic. A prescription is a financial document
// and 1234/100.0 is 12.339999999999999 often enough to matter.
func takaOf(poisha int64) string {
	negative := poisha < 0
	if negative {
		poisha = -poisha
	}
	whole, part := poisha/100, poisha%100
	out := strconv.FormatInt(whole, 10) + "." + strings.Repeat("0", 2-len(strconv.FormatInt(part, 10))) +
		strconv.FormatInt(part, 10)
	if negative {
		return "-" + out
	}
	return out
}

// day formats an instant on the clinic's calendar for a price lookup.
func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
