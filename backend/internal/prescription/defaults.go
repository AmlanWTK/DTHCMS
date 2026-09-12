package prescription

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The suggestions the prescription editor offers (CP81, migration 00064).
//
// # The one thing this file must get right
//
// A default a machine wrote must never look like a default the physician set. Everything below
// is arranged around that sentence: [PrescribingDefault.Approved] is a field rather than an
// inference, [PrescribingDefault.SourceCitation] is never empty because the database refuses an
// empty one, and the API returns all three of status, origin and citation on every row so that a
// screen cannot render the suggestion without also being handed what it would need to say where
// it came from.
//
// The editor's obligation on top of that is in the web feature and is tested there: an
// unapproved suggestion does not fill a field, it offers to.
//
// # Why the whole set is loaded rather than queried per drug
//
// Twenty-eight rows and fourteen sentences. CP76 put the entire formulary in the API process for
// the same reason and the reasoning has not changed: the number this checkpoint is measured on
// is how long a four-item prescription takes, and a round trip per line to ask "what is the usual
// dose of metformin" is latency somebody added on purpose.
//
// Unlike CP76 this is not cached in the process. It is a single indexed read of twenty-eight
// rows on a screen that opens once per consultation, and a cache is how a default a physician
// approved at 10:00 goes on saying "nobody has checked this" until somebody restarts the API.

// Approval is what a physician has, or has not, done to a piece of clinic content.
type Approval struct {
	// Approved is the field a screen branches on. Never inferred from the presence of a name.
	Approved bool `json:"approved"`
	// Origin is SEED — this system drafted it from published guidance — or AUTHORED.
	Origin string `json:"origin"`
	// ApprovedAt and ApprovedByName are present only when Approved.
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
	ApprovedByName string     `json:"approved_by_name,omitempty"`
	ApprovedByBN   string     `json:"approved_by_name_bn,omitempty"`
	// SourceCitation is where the content came from. Never empty: the database refuses a row
	// without one.
	SourceCitation string `json:"source_citation"`
}

// PrescribingDefault is a suggested dose, frequency and duration for one medicine.
type PrescribingDefault struct {
	ID          uuid.UUID `json:"id"`
	GenericID   uuid.UUID `json:"generic_id"`
	GenericName string    `json:"generic_name"`
	ClassCode   string    `json:"class_code,omitempty"`
	// Strength is the strength this suggestion is about, or empty for every strength of the
	// molecule. A client resolving a product matches the exact strength first.
	Strength string `json:"strength"`

	Dose         string   `json:"dose"`
	DailyDose    *float64 `json:"daily_dose,omitempty"`
	DoseUnit     string   `json:"dose_unit,omitempty"`
	Frequency    string   `json:"frequency"`
	FrequencyBN  string   `json:"frequency_bn"`
	DurationDays *int     `json:"duration_days,omitempty"`
	Route        string   `json:"route,omitempty"`

	// InstructionCode is the template offered with this suggestion, or empty.
	InstructionCode string `json:"instruction_code,omitempty"`

	// RationaleEN and RationaleBN say why this is the suggestion, in words a physician can
	// disagree with. Shown beside the suggestion rather than hidden behind a tooltip, because
	// the whole claim being made is "here is a proposal and here is its reasoning".
	RationaleEN string `json:"rationale_en"`
	RationaleBN string `json:"rationale_bn"`

	Approval  Approval  `json:"approval"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InstructionTemplate is a bilingual sentence a prescription line can be given.
type InstructionTemplate struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	// GenericName is the molecule this sentence belongs to, or empty for a general one.
	GenericName string `json:"generic_name,omitempty"`

	LabelEN string `json:"label_en"`
	LabelBN string `json:"label_bn"`
	TextEN  string `json:"text_en"`
	TextBN  string `json:"text_bn"`

	Ordering int `json:"ordering"`

	Approval  Approval  `json:"approval"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrNoSuchContent is a default or template id this facility does not hold.
var ErrNoSuchContent = errors.New("prescription: no such prescribing default or instruction template")

// PrescribingDefaults reads every suggestion this facility holds.
func (s *Store) PrescribingDefaults(ctx context.Context,
	facility uuid.UUID) ([]PrescribingDefault, error) {

	rows, err := s.q.PrescribingDefaults(ctx, facility)
	if err != nil {
		return nil, err
	}
	out := make([]PrescribingDefault, 0, len(rows))
	for _, row := range rows {
		d := PrescribingDefault{
			ID: row.ID, GenericID: row.GenericID, GenericName: row.GenericName,
			ClassCode: row.ClassCode, Strength: row.Strength,
			Dose: row.Dose, DoseUnit: row.DoseUnit,
			Frequency: row.Frequency, FrequencyBN: row.FrequencyBn,
			Route:       row.Route,
			RationaleEN: row.RationaleEn, RationaleBN: row.RationaleBn,
			Approval: Approval{
				Approved:       row.Status == "APPROVED",
				Origin:         row.Origin,
				ApprovedAt:     row.ApprovedAt,
				SourceCitation: row.SourceCitation,
			},
			UpdatedAt: row.UpdatedAt,
		}
		if row.ApprovedByName != nil {
			d.Approval.ApprovedByName = *row.ApprovedByName
		}
		if row.ApprovedByNameBn != nil {
			d.Approval.ApprovedByBN = *row.ApprovedByNameBn
		}
		if row.InstructionCode != nil {
			d.InstructionCode = *row.InstructionCode
		}
		if row.DurationDays != nil {
			days := int(*row.DurationDays)
			d.DurationDays = &days
		}
		if row.DailyDose.Valid {
			if f, err := row.DailyDose.Float64Value(); err == nil && f.Valid {
				value := f.Float64
				d.DailyDose = &value
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// InstructionTemplates reads every bilingual instruction this facility holds.
func (s *Store) InstructionTemplates(ctx context.Context,
	facility uuid.UUID) ([]InstructionTemplate, error) {

	rows, err := s.q.InstructionTemplates(ctx, facility)
	if err != nil {
		return nil, err
	}
	out := make([]InstructionTemplate, 0, len(rows))
	for _, row := range rows {
		t := InstructionTemplate{
			ID: row.ID, Code: row.Code,
			LabelEN: row.LabelEn, LabelBN: row.LabelBn,
			TextEN: row.TextEn, TextBN: row.TextBn,
			Ordering: int(row.Ordering),
			Approval: Approval{
				Approved:       row.Status == "APPROVED",
				Origin:         row.Origin,
				ApprovedAt:     row.ApprovedAt,
				SourceCitation: row.SourceCitation,
			},
			UpdatedAt: row.UpdatedAt,
		}
		if row.GenericName != nil {
			t.GenericName = *row.GenericName
		}
		if row.ApprovedByName != nil {
			t.Approval.ApprovedByName = *row.ApprovedByName
		}
		if row.ApprovedByNameBn != nil {
			t.Approval.ApprovedByBN = *row.ApprovedByNameBn
		}
		out = append(out, t)
	}
	return out, nil
}

// ApproveDefault records that a physician has read a suggestion and stands behind it.
//
// Idempotent in the statement rather than in a handler: approving an already-approved row returns
// it unchanged rather than replacing the first physician's name with the second's. The person who
// read it is the person whose name belongs on it.
func (s *Store) ApproveDefault(ctx context.Context, facility, id, actor uuid.UUID,
	at time.Time) (Approval, error) {

	row, err := s.q.ApprovePrescribingDefault(ctx, dbgen.ApprovePrescribingDefaultParams{
		ActorID: actor, At: at.UTC(), ID: id, FacilityID: facility,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, ErrNoSuchContent
	}
	if err != nil {
		return Approval{}, err
	}
	return Approval{Approved: row.Status == "APPROVED", ApprovedAt: row.ApprovedAt}, nil
}

// ApproveTemplate is the same, for an instruction.
func (s *Store) ApproveTemplate(ctx context.Context, facility, id, actor uuid.UUID,
	at time.Time) (Approval, error) {

	row, err := s.q.ApproveInstructionTemplate(ctx, dbgen.ApproveInstructionTemplateParams{
		ActorID: actor, At: at.UTC(), ID: id, FacilityID: facility,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, ErrNoSuchContent
	}
	if err != nil {
		return Approval{}, err
	}
	return Approval{Approved: row.Status == "APPROVED", ApprovedAt: row.ApprovedAt}, nil
}

// ResolveDefault picks the suggestion that applies to one generic at one strength.
//
// Exported because the resolution order is the contract, not an implementation detail: **the
// exact strength wins, and a molecule-wide row is a fallback.** A client that scanned the list
// itself and took the first match would get metformin's 500 mg row for a 1000 mg tablet, which is
// half the dose written confidently.
//
// Nothing is returned when nothing matches, and nothing is the honest answer: this clinic has
// twenty-eight suggestions and two hundred and fifty products, so most lines have none. A
// suggestion invented for the rest — "1 tablet once daily", which is right often enough to be
// dangerous — would be the machine prescribing.
func ResolveDefault(all []PrescribingDefault, generic, strength string) (PrescribingDefault, bool) {
	generic = strings.ToLower(strings.TrimSpace(generic))
	strength = strings.ToLower(strings.TrimSpace(strength))
	var fallback *PrescribingDefault
	for i := range all {
		if strings.ToLower(strings.TrimSpace(all[i].GenericName)) != generic {
			continue
		}
		candidate := strings.ToLower(strings.TrimSpace(all[i].Strength))
		if candidate == strength && strength != "" {
			return all[i], true
		}
		if candidate == "" {
			fallback = &all[i]
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return PrescribingDefault{}, false
}
