package patient

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The store: the patient tables, and nothing about why a row is being written.
//
// The one thing here that is a decision rather than SQL is `Create`. A patient, their
// identifiers, their anonymised research row and the link between the two are written in
// **one transaction**, because a patient with no research id is a gap in every cohort they
// should have been in and nobody would notice for a year.

var (
	// ErrNotFound is a patient this facility does not have. Deliberately the same whether
	// the id is unknown or belongs to another facility: a 404 that distinguishes them is a
	// way to enumerate patients.
	ErrNotFound = errors.New("patient: not found")
	// ErrDuplicateIdentifier is the database refusing a national ID that already belongs to
	// somebody. The constraint is what makes §3 Step 1's "strict duplicate-record
	// prevention" a property rather than a promise.
	ErrDuplicateIdentifier = errors.New("patient: that identifier already belongs to a patient")
	// ErrDuplicateClinicalID means two registrations drew the same number, which the
	// counter's row lock should make impossible. It is here so that if it ever happens it
	// is a named failure and not a constraint violation nobody can read.
	ErrDuplicateClinicalID = errors.New("patient: the clinical id was already taken")
)

// Store is the patient tables.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

// NewPatient is everything one registration writes.
type NewPatient struct {
	Patient     Patient
	Identifiers []Identifier
	// ResearchID is assigned by the caller so that the same value reaches the event, the
	// anonymised row and the link — and so that a retry of the same registration does not
	// mint a second one.
	ResearchID   string
	FacilityCode string
	// InTx runs inside the same transaction as the patient row, after it exists and before
	// the commit. It is how the clinical event is appended atomically with the record it
	// describes (CP29): a patient row with no event behind it is a fact with no history.
	InTx func(ctx context.Context, tx pgx.Tx, created Patient) error
}

// Create writes the patient, the identifiers, the research subject and the link, in one
// transaction, and runs the caller's work inside it.
func (s *Store) Create(ctx context.Context, in NewPatient) (Patient, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Patient{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	clinicalID, err := q.NextClinicalID(ctx, dbgen.NextClinicalIDParams{
		PFacility: in.Patient.FacilityID,
		//nolint:gosec // a calendar year
		PYear: int16(in.Patient.RegisteredAt.In(Dhaka).Year()),
	})
	if err != nil {
		return Patient{}, fmt.Errorf("drawing a clinical id: %w", err)
	}

	row, err := q.InsertPatient(ctx, insertParams(in.Patient, clinicalID))
	if err != nil {
		return Patient{}, translate(err)
	}
	created := fromRow(row)

	for _, identifier := range in.Identifiers {
		if _, err := q.InsertPatientIdentifier(ctx, dbgen.InsertPatientIdentifierParams{
			FacilityID: created.FacilityID, PatientID: created.ID, Kind: string(identifier.Kind),
			Digest: identifier.Digest, Sealed: identifier.Sealed, KeyID: identifier.KeyID,
			Masked: identifier.Masked, CaptureMethod: captureOr(identifier.CaptureMethod),
		}); err != nil {
			return Patient{}, translate(err)
		}
	}

	// The anonymised row and the link, in the same transaction as the patient. §12's
	// analyses are only trustworthy if every patient is in exactly one cohort, and a
	// registration that half-succeeded would leave one out silently.
	if err := q.InsertResearchSubject(ctx, researchParams(created, in.ResearchID, in.FacilityCode)); err != nil {
		return Patient{}, fmt.Errorf("creating the research subject: %w", err)
	}
	if err := q.LinkResearchSubject(ctx, dbgen.LinkResearchSubjectParams{
		PatientID: created.ID, ResearchID: in.ResearchID, FacilityID: created.FacilityID,
	}); err != nil {
		return Patient{}, fmt.Errorf("linking the research subject: %w", err)
	}

	if in.InTx != nil {
		if err := in.InTx(ctx, tx, created); err != nil {
			return Patient{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Patient{}, err
	}
	return created, nil
}

// FacilityCode is the clinic's short code, for the anonymised research row. A separate
// read rather than a field on the actor: the code is a property of the facility and one
// day it will be corrected, and an actor carrying a stale copy would write it into a
// research row that outlives the correction.
func (s *Store) FacilityCode(ctx context.Context, facility uuid.UUID) (string, error) {
	code, err := s.q.FacilityCode(ctx, facility)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return code, nil
}

// ByID reads one patient, scoped to a facility.
func (s *Store) ByID(ctx context.Context, id, facility uuid.UUID) (Patient, error) {
	row, err := s.q.PatientByID(ctx, dbgen.PatientByIDParams{ID: id, FacilityID: facility})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Patient{}, ErrNotFound
		}
		return Patient{}, err
	}
	return fromRow(row), nil
}

// ByClinicalID reads one patient by the number spoken at the desk.
func (s *Store) ByClinicalID(ctx context.Context, clinicalID string) (Patient, error) {
	row, err := s.q.PatientByClinicalID(ctx, clinicalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Patient{}, ErrNotFound
		}
		return Patient{}, err
	}
	return fromRow(row), nil
}

// Identifiers reads a patient's official numbers, sealed. Opening one is a separate,
// step-upped act.
func (s *Store) Identifiers(ctx context.Context, patientID uuid.UUID) ([]Identifier, error) {
	rows, err := s.q.IdentifiersForPatient(ctx, patientID)
	if err != nil {
		return nil, err
	}
	out := make([]Identifier, 0, len(rows))
	for _, row := range rows {
		out = append(out, Identifier{
			ID: row.ID, FacilityID: row.FacilityID, PatientID: row.PatientID,
			Kind: IdentifierKind(row.Kind), Digest: row.Digest, Sealed: row.Sealed,
			KeyID: row.KeyID, Masked: row.Masked, CaptureMethod: row.CaptureMethod,
			VerifiedAt: row.VerifiedAt, VerifiedBy: nullableUUID(row.VerifiedBy),
		})
	}
	return out, nil
}

// ByIdentifier finds the patient an official number already belongs to. The duplicate
// check the desk sees before the constraint refuses the insert.
func (s *Store) ByIdentifier(ctx context.Context, facility uuid.UUID, kind IdentifierKind, digest []byte) (Patient, error) {
	row, err := s.q.PatientByIdentifierDigest(ctx, dbgen.PatientByIdentifierDigestParams{
		FacilityID: facility, Kind: string(kind), Digest: digest,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Patient{}, ErrNotFound
		}
		return Patient{}, err
	}
	return fromRow(row), nil
}

// InTransaction runs the caller's work in one transaction with the store's queries bound
// to it. The seam that lets a module write its own tables and append its event together
// without the store having to know what the event says.
func (s *Store) InTransaction(ctx context.Context, fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error {
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

// numeric turns a score into the column's type. Four decimal places, matching the CHECK:
// a match score is a comparison, not a measurement, and printing it to more places would
// suggest a precision the weights do not have.
func numeric(score float64) pgtype.Numeric {
	var out pgtype.Numeric
	//nolint:errcheck // a float in 0..1 always scans
	_ = out.Scan(strconv.FormatFloat(score, 'f', 4, 64))
	return out
}

// --- duplicate detection (CP30) ---

// MatchProbe is what the matcher knows about the person at the desk.
type MatchProbe struct {
	BirthDate time.Time
	NameKeyEN string
	NameEN    string
	NameBN    string
	Phone     string
	// YearWindow is how far either side of the birth year the caller wants candidates.
	// Kept as a field rather than a constant because the pilot will tune it, and because a
	// paediatric register wants a narrower window than an adult one.
	YearWindow int
}

// MatchRow is one existing patient, from the read model rather than from core: the search
// keys live there, and a duplicate check must never be the reason a clinical read touches
// the write side.
type MatchRow struct {
	PatientID    uuid.UUID
	ClinicalID   string
	NameEN       string
	NameBN       string
	NameKeyEN    string
	Sex          string
	BirthDate    time.Time
	Phone        string
	District     string
	Upazila      string
	RegisteredAt time.Time
}

// MatchCandidates is the blocking query: everyone this registration could plausibly be.
//
// It only has to not miss. Scoring happens in Go, over a handful of rows, where the rules
// can be read and changed by somebody who is not fluent in plpgsql.
func (s *Store) MatchCandidates(ctx context.Context, facility uuid.UUID, probe MatchProbe) ([]MatchRow, error) {
	rows, err := s.q.MatchCandidates(ctx, dbgen.MatchCandidatesParams{
		FacilityID: facility, NameKeyEn: probe.NameKeyEN,
		NameEn: probe.NameEN, NameBn: probe.NameBN,
		Phone: probe.Phone, BirthDate: probe.BirthDate,
	})
	if err != nil {
		return nil, err
	}
	out := make([]MatchRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, MatchRow{
			PatientID: row.PatientID, ClinicalID: row.ClinicalID,
			NameEN: row.NameEn, NameBN: row.NameBn, NameKeyEN: row.NameKeyEn,
			Sex: row.Sex, BirthDate: row.BirthDate, Phone: row.PhonePrimary,
			District: row.District, Upazila: row.Upazila, RegisteredAt: row.RegisteredAt,
		})
	}
	return out, nil
}

// ByPhoneAndBirthDate is the second deterministic rule.
func (s *Store) ByPhoneAndBirthDate(ctx context.Context, facility uuid.UUID, phone string, born time.Time) (Patient, error) {
	row, err := s.q.PatientByPhoneAndBirthDate(ctx, dbgen.PatientByPhoneAndBirthDateParams{
		FacilityID: facility, PhonePrimary: phone, BirthDate: born,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Patient{}, ErrNotFound
		}
		return Patient{}, err
	}
	return fromRow(row), nil
}

// --- translation ---

func translate(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "patient_identifier_unique":
		return ErrDuplicateIdentifier
	case "patient_clinical_id_unique":
		return ErrDuplicateClinicalID
	}
	return err
}

func captureOr(method string) string {
	if method == "" {
		return "typed"
	}
	return method
}

func insertParams(p Patient, clinicalID string) dbgen.InsertPatientParams {
	return dbgen.InsertPatientParams{
		FacilityID: p.FacilityID, ClinicalID: clinicalID,
		NameEn: p.NameEN, NameBn: p.NameBN, Sex: string(p.Sex),
		BirthDate: p.Birth.Date, DobPrecision: string(p.Birth.Precision),
		DobVerifiedBy: string(p.Birth.Source), DobVerifiedAt: p.Birth.VerifiedAt,
		DobVerifiedUserID: nullUUID(p.Birth.VerifiedBy),
		PhonePrimary:      p.PhonePrimary, PhoneSecondary: p.PhoneSecondary,
		Division: p.Address.Division, District: p.Address.District, Upazila: p.Address.Upazila,
		AddressLine: p.Address.AddressLine, Postcode: p.Address.Postcode,
		EmergencyName: p.Emergency.Name, EmergencyRelation: p.Emergency.Relation,
		EmergencyPhone:     p.Emergency.Phone,
		EducationLevel:     optional(p.Socio.Education),
		OccupationCategory: optional(p.Socio.Occupation),
		IncomeBand:         optional(p.Socio.IncomeBand),
		HouseholdSize:      optionalSize(p.Socio.HouseholdSize),
		ResidenceType:      optional(p.Socio.Residence),
		MedicinePayer:      optional(p.Socio.MedicinePayer),
		RegisteredBy:       nullUUID(p.RegisteredBy), RegisteredAt: p.RegisteredAt,
	}
}

func researchParams(p Patient, researchID, facilityCode string) dbgen.InsertResearchSubjectParams {
	// The month, not the day. A registration date to the day, plus a birth year and a sex,
	// narrows a small population further than any cohort analysis needs.
	month := time.Date(p.RegisteredAt.In(Dhaka).Year(), p.RegisteredAt.In(Dhaka).Month(), 1, 0, 0, 0, 0, time.UTC)
	return dbgen.InsertResearchSubjectParams{
		ResearchID: researchID, FacilityCode: facilityCode, EnrolledMonth: month,
		//nolint:gosec // a calendar year
		BirthYear: int16(p.Birth.Date.Year()), Sex: string(p.Sex),
		EducationLevel:     optional(p.Socio.Education),
		OccupationCategory: optional(p.Socio.Occupation),
		IncomeBand:         optional(p.Socio.IncomeBand),
		HouseholdSize:      optionalSize(p.Socio.HouseholdSize),
		ResidenceType:      optional(p.Socio.Residence),
		MedicinePayer:      optional(p.Socio.MedicinePayer),
	}
}

func fromRow(row dbgen.CorePatient) Patient {
	return Patient{
		ID: row.ID, FacilityID: row.FacilityID, ClinicalID: row.ClinicalID,
		NameEN: row.NameEn, NameBN: row.NameBn, Sex: Sex(row.Sex),
		Birth: BirthDate{
			Date: row.BirthDate, Precision: DOBPrecision(row.DobPrecision),
			Source: DOBSource(row.DobVerifiedBy), VerifiedAt: row.DobVerifiedAt,
			VerifiedBy: nullableUUID(row.DobVerifiedUserID),
		},
		PhonePrimary: row.PhonePrimary, PhoneSecondary: row.PhoneSecondary,
		Address: Address{
			Division: row.Division, District: row.District, Upazila: row.Upazila,
			AddressLine: row.AddressLine, Postcode: row.Postcode,
		},
		Emergency: EmergencyContact{
			Name: row.EmergencyName, Relation: row.EmergencyRelation, Phone: row.EmergencyPhone,
		},
		Socio: Socioeconomic{
			Education: value(row.EducationLevel), Occupation: value(row.OccupationCategory),
			IncomeBand: value(row.IncomeBand), HouseholdSize: size(row.HouseholdSize),
			Residence: value(row.ResidenceType), MedicinePayer: value(row.MedicinePayer),
		},
		PhotoObjectKey: row.PhotoObjectKey,
		Status:         Status(row.Status), StatusReason: row.StatusReason,
		MergedIntoID: nullableUUID(row.MergedIntoID),
		RegisteredBy: nullableUUID(row.RegisteredBy), RegisteredAt: row.RegisteredAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optionalSize(n int) *int16 {
	if n == 0 {
		return nil
	}
	//nolint:gosec // validated 1..40
	small := int16(n)
	return &small
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func size(p *int16) int {
	if p == nil {
		return 0
	}
	return int(*p)
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func nullableUUID(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id := n.UUID
	return &id
}
