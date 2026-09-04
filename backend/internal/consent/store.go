package consent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Store reads consent state and the wording behind it.
//
// The application reads templates and never writes them. Publishing legal text is an
// administrative act done by the deployment's owner, and a handler that could insert a
// template is a handler that could quietly change what a patient is agreeing to.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

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

// ActiveTemplate is the wording in force for one consent in one language.
//
// A missing template is `ErrNoTemplate`, not an empty struct, because until D-02 is answered
// that is the *normal* state and the right behaviour is to refuse to take a consent rather
// than to take one against no words at all.
func (s *Store) ActiveTemplate(ctx context.Context, t Type, language string) (Template, error) {
	row, err := s.q.ActiveConsentTemplate(ctx, dbgen.ActiveConsentTemplateParams{
		ConsentType: string(t), Language: language,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, fmt.Errorf("%w: %s in %s", ErrNoTemplate, t, language)
	}
	if err != nil {
		return Template{}, err
	}
	return template(row.ID, row.ConsentType, row.Version, row.Language, row.Title, row.Body,
		row.BodyDigest, row.Status, row.EffectiveFrom), nil
}

// ActiveTemplates is every consent's current wording in one language, for a capture screen.
func (s *Store) ActiveTemplates(ctx context.Context, language string) ([]Template, error) {
	rows, err := s.q.ActiveConsentTemplates(ctx, language)
	if err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(rows))
	for _, row := range rows {
		out = append(out, template(row.ID, row.ConsentType, row.Version, row.Language, row.Title,
			row.Body, row.BodyDigest, row.Status, row.EffectiveFrom))
	}
	return out, nil
}

// TemplateVersion is the exact wording a recorded consent was taken against.
//
// The reason this exists at all: a consent is only meaningful if the words can be produced
// years later, and "the current template" is not those words.
func (s *Store) TemplateVersion(ctx context.Context, t Type, language string, version int) (Template, error) {
	row, err := s.q.ConsentTemplateVersion(ctx, dbgen.ConsentTemplateVersionParams{
		ConsentType: string(t), Language: language, Version: int32(version), //nolint:gosec // versions are small
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, fmt.Errorf("%w: %s v%d in %s", ErrNoTemplate, t, version, language)
	}
	if err != nil {
		return Template{}, err
	}
	return template(row.ID, row.ConsentType, row.Version, row.Language, row.Title, row.Body,
		row.BodyDigest, row.Status, row.EffectiveFrom), nil
}

// All is every consent recorded for a patient, whatever its state.
//
// Including the revoked ones, deliberately. A screen that shows only live consents cannot
// distinguish "never asked" from "asked and withdrawn", and those want different words.
func (s *Store) All(ctx context.Context, patientID, facility uuid.UUID) ([]Record, error) {
	rows, err := s.q.PatientConsents(ctx, dbgen.PatientConsentsParams{
		PatientID: patientID, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rows))
	for _, row := range rows {
		out = append(out, record(consentRow(row)))
	}
	return out, nil
}

// One is a single consent's state, or Absent.
func (s *Store) One(ctx context.Context, patientID, facility uuid.UUID, t Type) (Record, error) {
	row, err := s.q.PatientConsent(ctx, dbgen.PatientConsentParams{
		PatientID: patientID, FacilityID: facility, ConsentType: string(t),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{PatientID: patientID, ConsentType: t, Status: Absent}, nil
	}
	if err != nil {
		return Record{}, err
	}
	return record(consentRow(row)), nil
}

// Entry is one line of the consent history.
type Entry struct {
	ConsentType     Type      `json:"consent_type"`
	Action          string    `json:"action"`
	TemplateVersion *int      `json:"template_version,omitempty"`
	Language        string    `json:"language,omitempty"`
	CaptureMethod   string    `json:"capture_method,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	RequestedBy     string    `json:"requested_by,omitempty"`
	ActorCode       string    `json:"actor_code,omitempty"`
	OccurredAt      string    `json:"occurred_at"`
	EventID         uuid.UUID `json:"event_id"`
}

// History is every grant and revocation, newest first.
//
// The current state answers "may I send this now". This answers "was that send lawful in
// March", which is the question a complaint actually asks.
func (s *Store) History(ctx context.Context, patientID, facility uuid.UUID) ([]Entry, error) {
	rows, err := s.q.PatientConsentHistory(ctx, dbgen.PatientConsentHistoryParams{
		PatientID: patientID, FacilityID: facility,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		entry := Entry{
			ConsentType: Type(row.ConsentType), Action: row.Action,
			Language: row.Language, CaptureMethod: row.CaptureMethod,
			Reason: row.Reason, RequestedBy: row.RequestedBy, ActorCode: row.ActorCode,
			OccurredAt: row.OccurredAt.UTC().Format("2006-01-02T15:04:05Z"),
			EventID:    row.EventID,
		}
		if row.TemplateVersion != nil {
			version := int(*row.TemplateVersion)
			entry.TemplateVersion = &version
		}
		out = append(out, entry)
	}
	return out, nil
}

// --- mapping ---

func template(id uuid.UUID, kind string, version int32, language, title, body, digest, status string,
	effective *time.Time) Template {
	return Template{
		ID: id, ConsentType: Type(kind), Version: int(version), Language: language,
		Title: title, Body: body, Digest: digest, Status: status, EffectiveFrom: effective,
	}
}

// consentRow is what both consent queries return, structurally. sqlc generates a distinct
// type per query, and mapping each one separately would be two copies of the same fifteen
// assignments — which is where a field gets dropped from one and not the other.
type consentRow struct {
	PatientID          uuid.UUID
	ConsentType        string
	Status             string
	TemplateVersion    int32
	Language           string
	CaptureMethod      string
	EvidenceKey        string
	PaperReference     string
	WitnessedByCode    string
	GrantedForRelation string
	GrantedForName     string
	GrantedAt          time.Time
	GrantedByCode      string
	RevokedAt          *time.Time
	RevokedByCode      string
	RevokeReason       string
}

func record(row consentRow) Record {
	return Record{
		PatientID: row.PatientID, ConsentType: Type(row.ConsentType), Status: Status(row.Status),
		TemplateVersion: int(row.TemplateVersion), Language: row.Language,
		CaptureMethod:  CaptureMethod(row.CaptureMethod),
		PaperReference: row.PaperReference, WitnessedByCode: row.WitnessedByCode,
		GrantedForRelation: row.GrantedForRelation, GrantedForName: row.GrantedForName,
		GrantedAt: row.GrantedAt, GrantedByCode: row.GrantedByCode,
		RevokedAt: row.RevokedAt, RevokedByCode: row.RevokedByCode,
		RevokeReason: row.RevokeReason,
		// The key itself never leaves the server. A signed URL is minted per request, by
		// the one route that serves the image.
		HasEvidence: row.EvidenceKey != "",
	}
}
