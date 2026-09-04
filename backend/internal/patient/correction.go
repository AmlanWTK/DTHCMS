package patient

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/textmatch"
)

// Demographic corrections (CP35, §4.3).
//
// Never an overwrite. A correction is an event carrying what the value was, what it is now,
// and why — and the original stays in the ledger forever. The reason this matters more for
// demographics than it looks: a wrong date of birth changes every pediatric percentile ever
// computed for that patient, and those numbers have already been read, acted on and in some
// cases printed. Somebody has to be able to find out that they changed and why.
//
// A correction to a **high-impact** field — the date of birth, its precision, the sex, the
// English name — needs a step-up as well as the write permission. The plan says "elevated
// permission"; the catalogue has no code for it, and inventing one would mean deciding, per
// role, who may correct a name but not a birth date. A step-up is elevation of the kind the
// system already has, and it is the right kind here: the risk is a session left open on a
// desk, not a person who should never have been able to do this at all. The deviation is in
// ADR-0020's successor note and this comment.

var (
	// ErrNothingToCorrect is a correction that changes nothing. Refused rather than
	// accepted as a no-op: it would sit in the history looking like something happened.
	ErrNothingToCorrect = errors.New("patient: nothing in that request differs from the record")
	// ErrReasonRequired is a correction with no usable reason.
	ErrReasonRequired = errors.New("patient: a correction needs a reason a reader can act on")
)

// Correction is what a caller asks to change. A nil field is one they are not touching,
// which is what stops a form that renders six fields from silently rewriting five of them.
type Correction struct {
	EventID uuid.UUID

	NameEN         *string
	NameBN         *string
	Sex            *string
	BirthDate      *string
	DOBPrecision   *string
	DOBSource      *string
	PhonePrimary   *string
	PhoneSecondary *string
	Division       *string
	District       *string
	Upazila        *string
	AddressLine    *string
	Postcode       *string

	Reason string
	Source eventstore.Source
}

// Applied is what one correction did.
type Applied struct {
	Patient Patient
	Changes []eventstore.FieldChange
	// HighImpact says whether anything already computed was invalidated.
	HighImpact bool
	// Invalidated is what the dependency register says must be recomputed or reviewed. The
	// recomputations happen in the same transaction; the reviews are for a person.
	Invalidated []Dependency
	Event       eventstore.Event
}

// Dependency is one derived value that depended on a field that changed.
type Dependency struct {
	DerivedName string `json:"derived_name"`
	DependsOn   string `json:"depends_on"`
	Action      string `json:"action"`
	Description string `json:"description"`
}

// NeedsStepUp reports whether a correction touches a field other values were computed from.
//
// Computed from the request before anything is written, so the route can demand the step-up
// before the work rather than after it.
func (c Correction) NeedsStepUp(current Patient) bool {
	for _, change := range c.diff(current) {
		for _, field := range eventstore.HighImpactFields {
			if change.Field == field {
				return true
			}
		}
	}
	return false
}

// Correct records the change.
func (s *Service) Correct(ctx context.Context, patientID uuid.UUID, in Correction) (Applied, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Applied{}, err
	}
	if len(strings.TrimSpace(in.Reason)) < 10 {
		return Applied{}, ErrReasonRequired
	}

	// A retry that already landed, answered from the ledger.
	if existing, err := s.events.ByID(ctx, in.EventID); err == nil {
		current, err := s.store.ByID(ctx, patientID, actor.FacilityID())
		if err != nil {
			return Applied{}, err
		}
		return Applied{Patient: current, Event: existing}, nil
	} else if !errors.Is(err, eventstore.ErrNotFound) {
		return Applied{}, err
	}

	current, err := s.store.ByID(ctx, patientID, actor.FacilityID())
	if err != nil {
		return Applied{}, err
	}

	in, err = in.normalised()
	if err != nil {
		return Applied{}, err
	}
	changes := in.diff(current)
	if len(changes) == 0 {
		return Applied{}, ErrNothingToCorrect
	}

	high := false
	fields := make([]string, 0, len(changes))
	for _, change := range changes {
		fields = append(fields, change.Field)
		for _, name := range eventstore.HighImpactFields {
			if change.Field == name {
				high = true
			}
		}
	}

	// What this invalidates, read from the register rather than from a list in this file.
	// The plan's own risk note asks for the enumeration to be explicit; a table means a
	// checkpoint that adds a derived value adds a row, and this path picks it up without
	// being edited.
	invalidated, err := s.store.DependenciesOn(ctx, fields)
	if err != nil {
		return Applied{}, err
	}

	now := s.clock.Now().UTC()
	payload, err := json.Marshal(in.payload(actor, patientID, changes, high, now))
	if err != nil {
		return Applied{}, err
	}

	var written eventstore.Event
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		id := patientID
		written, err = s.events.AppendInTx(ctx, tx, eventstore.Envelope{
			EventID: in.EventID, AggregateType: "PATIENT", AggregateID: patientID, PatientID: &id,
			EventType: "PATIENT_DEMOGRAPHICS_CORRECTED", EventVersion: 1,
			OccurredAt: now, Actor: actor, Source: in.Source, Payload: payload,
		})
		if err != nil {
			return err
		}
		// core.patient follows the read model. The write side is corrected here rather
		// than by the projection because it is not a derived value — it is the record.
		return q.CorrectPatient(ctx, in.correctParams(patientID, actor.FacilityID(), current))
	})
	if err != nil {
		return Applied{}, err
	}

	updated, err := s.store.ByID(ctx, patientID, actor.FacilityID())
	if err != nil {
		return Applied{}, err
	}
	return Applied{
		Patient: updated, Changes: changes, HighImpact: high,
		Invalidated: invalidated, Event: written,
	}, nil
}

// History is every correction ever made to a patient, newest first.
func (s *Store) History(ctx context.Context, patientID, facility uuid.UUID) ([]dbgen.ReadPatientCorrection, error) {
	return s.q.PatientCorrections(ctx, dbgen.PatientCorrectionsParams{
		PatientID: patientID, FacilityID: facility,
	})
}

// DependenciesOn is what the register says depends on these fields.
func (s *Store) DependenciesOn(ctx context.Context, fields []string) ([]Dependency, error) {
	rows, err := s.q.DerivedDependencies(ctx, fields)
	if err != nil {
		return nil, err
	}
	out := make([]Dependency, 0, len(rows))
	for _, row := range rows {
		out = append(out, Dependency{
			DerivedName: row.DerivedName, DependsOn: row.DependsOn,
			Action: row.Action, Description: row.Description,
		})
	}
	return out, nil
}

// --- shaping ---

// normalised puts the incoming values into the form the record stores, so a "correction"
// that only changes how a telephone number was typed is correctly seen as no change at all.
func (c Correction) normalised() (Correction, error) {
	if c.PhonePrimary != nil {
		normalised, ok := NormalisePhone(*c.PhonePrimary)
		if !ok {
			return c, Errors{{
				Field: "phone_primary", Code: "invalid",
				Message:   "Enter a Bangladeshi mobile number, like 01712345678.",
				MessageBN: "বাংলাদেশি মোবাইল নম্বর দিন, যেমন ০১৭১২৩৪৫৬৭৮।",
			}}
		}
		c.PhonePrimary = &normalised
	}
	if c.PhoneSecondary != nil {
		normalised, ok := NormaliseSecondaryPhone(*c.PhoneSecondary)
		if !ok {
			return c, Errors{{
				Field: "phone_secondary", Code: "invalid",
				Message:   "Enter a valid phone number, or leave it blank.",
				MessageBN: "সঠিক ফোন নম্বর দিন, অথবা খালি রাখুন।",
			}}
		}
		c.PhoneSecondary = &normalised
	}
	if c.NameEN != nil {
		trimmed := strings.TrimSpace(*c.NameEN)
		c.NameEN = &trimmed
	}
	if c.NameBN != nil {
		trimmed := strings.TrimSpace(*c.NameBN)
		c.NameBN = &trimmed
	}
	return c, nil
}

// diff is what actually changes. Computed against the record rather than trusted from the
// request, so a form that submits every field it rendered produces a correction naming only
// what the operator altered.
func (c Correction) diff(current Patient) []eventstore.FieldChange {
	var changes []eventstore.FieldChange
	compare := func(field string, want *string, have string) {
		if want == nil || *want == have {
			return
		}
		changes = append(changes, eventstore.FieldChange{Field: field, Previous: have, Current: *want})
	}

	compare("name_en", c.NameEN, current.NameEN)
	compare("name_bn", c.NameBN, current.NameBN)
	compare("sex", c.Sex, string(current.Sex))
	compare("birth_date", c.BirthDate, current.Birth.Date.In(Dhaka).Format(time.DateOnly))
	compare("dob_precision", c.DOBPrecision, string(current.Birth.Precision))
	compare("dob_source", c.DOBSource, string(current.Birth.Source))
	compare("phone_primary", c.PhonePrimary, current.PhonePrimary)
	compare("phone_secondary", c.PhoneSecondary, current.PhoneSecondary)
	compare("division", c.Division, current.Address.Division)
	compare("district", c.District, current.Address.District)
	compare("upazila", c.Upazila, current.Address.Upazila)
	compare("address_line", c.AddressLine, current.Address.AddressLine)
	compare("postcode", c.Postcode, current.Address.Postcode)
	return changes
}

func (c Correction) payload(actor eventstore.Actor, patientID uuid.UUID,
	changes []eventstore.FieldChange, high bool, now time.Time,
) eventstore.PatientDemographicsCorrected {
	out := eventstore.PatientDemographicsCorrected{
		FacilityID: actor.FacilityID().String(), PatientID: patientID.String(),
		Changes: changes, Reason: strings.TrimSpace(c.Reason), HighImpact: high,
		CorrectedBy: actor.UserID().String(), CorrectedByCode: actor.Code(),
		CorrectedAt: now.Format(time.RFC3339Nano),
		NameEN:      c.NameEN, NameBN: c.NameBN, Sex: c.Sex,
		BirthDate: c.BirthDate, DOBPrecision: c.DOBPrecision, DOBSource: c.DOBSource,
		PhonePrimary: c.PhonePrimary, PhoneSecondary: c.PhoneSecondary,
		Division: c.Division, District: c.District, Upazila: c.Upazila,
		AddressLine: c.AddressLine, Postcode: c.Postcode,
	}
	if c.NameEN != nil {
		key := textmatch.Key(*c.NameEN)
		out.NameKeyEN = &key
	}
	return out
}

func (c Correction) correctParams(patientID, facility uuid.UUID, current Patient) dbgen.CorrectPatientParams {
	born := current.Birth.Date
	if c.BirthDate != nil {
		if parsed, err := time.ParseInLocation(time.DateOnly, *c.BirthDate, Dhaka); err == nil {
			born = parsed
		}
	}
	return dbgen.CorrectPatientParams{
		ID: patientID, FacilityID: facility,
		NameEn:         orCurrent(c.NameEN, current.NameEN),
		NameBn:         orCurrent(c.NameBN, current.NameBN),
		Sex:            orCurrent(c.Sex, string(current.Sex)),
		BirthDate:      born,
		DobPrecision:   orCurrent(c.DOBPrecision, string(current.Birth.Precision)),
		DobVerifiedBy:  orCurrent(c.DOBSource, string(current.Birth.Source)),
		PhonePrimary:   orCurrent(c.PhonePrimary, current.PhonePrimary),
		PhoneSecondary: orCurrent(c.PhoneSecondary, current.PhoneSecondary),
		Division:       orCurrent(c.Division, current.Address.Division),
		District:       orCurrent(c.District, current.Address.District),
		Upazila:        orCurrent(c.Upazila, current.Address.Upazila),
		AddressLine:    orCurrent(c.AddressLine, current.Address.AddressLine),
		Postcode:       orCurrent(c.Postcode, current.Address.Postcode),
	}
}

func orCurrent(want *string, have string) string {
	if want == nil {
		return have
	}
	return *want
}
