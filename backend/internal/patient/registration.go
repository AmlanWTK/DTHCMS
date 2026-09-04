package patient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// Registration as an act rather than a form (CP29).
//
// The whole of it is one transaction: the clinical id is drawn, the patient row written,
// the identifiers sealed and stored, the anonymised research row and its link created,
// PATIENT_REGISTERED appended to the ledger, and the synchronous projection applied. All of
// it or none of it.
//
// That is not tidiness. A patient row with no event behind it is a fact with no history; an
// event with no patient is a history of nothing; a patient with no research id is a silent
// gap in every cohort they should have been in, which nobody would notice for a year. Each
// of those is unfixable after the fact in a way a failed request is not.

// ErrDuplicateEvent is a re-submission: the same event_id has already been registered. The
// caller is handed the original rather than a second patient.
var ErrDuplicateEvent = errors.New("patient: that registration has already been recorded")

// Service registers patients. It is the only thing that may.
type Service struct {
	store  *Store
	events *eventstore.Store
	sealer *IdentifierSealer
	clock  clock.Clock
	// Duplicates is the CP30 hook. Called with the sealed identifiers and the normalised
	// registration before anything is written; a non-nil error refuses the registration.
	// Nil means only the database's unique constraint stands between two records of one
	// person, which is enough to prevent a duplicate and not enough to explain it.
	Duplicates DuplicateCheck
}

// DuplicateCheck is what CP30 will implement: given a registration about to be written,
// decide whether it is a person the clinic already knows.
//
// Deterministic matching (an identifier digest already on file) is the part that exists
// now, in Store.ByIdentifier. Probabilistic matching — trigram similarity on bilingual
// names, date-of-birth proximity, phone edit distance — is CP30's, and it plugs in here
// rather than being retrofitted into a handler that has meanwhile grown around its absence.
type DuplicateCheck func(ctx context.Context, facility uuid.UUID, in Registration, identifiers []Identifier) error

type ServiceConfig struct {
	Store      *Store
	Events     *eventstore.Store
	Sealer     *IdentifierSealer
	Clock      clock.Clock
	Duplicates DuplicateCheck
}

func NewService(cfg ServiceConfig) *Service {
	s := &Service{
		store: cfg.Store, events: cfg.Events, sealer: cfg.Sealer,
		clock: cfg.Clock, Duplicates: cfg.Duplicates,
	}
	if s.clock == nil {
		s.clock = clock.Real{}
	}
	return s
}

// Registered is what one registration produced.
type Registered struct {
	Patient Patient
	Event   eventstore.Event
	// Duplicate is true when the event_id had already been used and this call wrote
	// nothing. The patient returned is the one the original registration created.
	Duplicate bool
}

// Register is the write path.
//
// `eventID` is the client's idempotency key, and it is the client's on purpose: a tablet
// that sends a registration, loses the reply and sends it again must create one patient.
// The check is the ledger's — `event_id` is unique there — so the answer is the same
// whether the retry arrives a second or a week later, and no separate table has to be kept
// in step with it.
func (s *Service) Register(ctx context.Context, in Registration, eventID uuid.UUID, source eventstore.Source) (Registered, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Registered{}, err
	}
	now := s.clock.Now().UTC()

	if err := in.Validate(now); err != nil {
		return Registered{}, err
	}
	in = in.normalised()

	// A retry that already landed: hand back the original, having written nothing. Checked
	// before the identifiers are sealed, because sealing is the expensive part and a retry
	// is the common case on a tablet with a poor connection.
	if existing, err := s.events.ByID(ctx, eventID); err == nil {
		return s.replay(ctx, existing, actor)
	} else if !errors.Is(err, eventstore.ErrNotFound) {
		return Registered{}, err
	}

	identifiers, err := s.seal(in)
	if err != nil {
		return Registered{}, err
	}
	if s.Duplicates != nil {
		if err := s.Duplicates(ctx, actor.FacilityID(), in, identifiers); err != nil {
			return Registered{}, err
		}
	}

	researchID, err := NewResearchID()
	if err != nil {
		return Registered{}, err
	}
	facilityCode, err := s.store.FacilityCode(ctx, actor.FacilityID())
	if err != nil {
		return Registered{}, err
	}

	var written eventstore.Event
	created, err := s.store.Create(ctx, NewPatient{
		Patient:      in.patient(actor, now),
		Identifiers:  identifiers,
		ResearchID:   researchID,
		FacilityCode: facilityCode,
		InTx: func(ctx context.Context, tx pgx.Tx, created Patient) error {
			payload, err := json.Marshal(in.payload(created))
			if err != nil {
				return err
			}
			patientID := created.ID
			written, err = s.events.AppendInTx(ctx, tx, eventstore.Envelope{
				EventID: eventID,
				// The patient is their own aggregate, and this is its first event.
				AggregateType: "PATIENT", AggregateID: created.ID, PatientID: &patientID,
				EventType: "PATIENT_REGISTERED", EventVersion: 1,
				OccurredAt: now, Actor: actor, Source: source,
				Payload: payload,
				// The head is expected to be empty. Two registrations racing on one
				// patient id cannot happen — the id is generated here — but saying so
				// costs nothing and makes the intent readable.
				ExpectedSequence: 0,
			})
			return err
		},
	})
	if err != nil {
		return Registered{}, err
	}
	return Registered{Patient: created, Event: written}, nil
}

// replay answers a re-submission from the ledger, without writing anything.
func (s *Service) replay(ctx context.Context, existing eventstore.Event, actor eventstore.Actor) (Registered, error) {
	if existing.EventType != "PATIENT_REGISTERED" || existing.PatientID == nil {
		// The same event_id used for something else entirely. Refused rather than
		// answered, because the client has a bug and a plausible-looking patient would
		// hide it.
		return Registered{}, fmt.Errorf("%w: as a %s", ErrDuplicateEvent, existing.EventType)
	}
	original, err := s.store.ByID(ctx, *existing.PatientID, actor.FacilityID())
	if err != nil {
		return Registered{}, err
	}
	existing.Duplicate = true
	return Registered{Patient: original, Event: existing, Duplicate: true}, nil
}

func (s *Service) seal(in Registration) ([]Identifier, error) {
	if s.sealer == nil && len(in.Identifiers) > 0 {
		return nil, errors.New("patient: no identifier sealer configured")
	}
	out := make([]Identifier, 0, len(in.Identifiers))
	for _, kind := range in.identifierOrder() {
		sealed, err := s.sealer.Seal(kind, in.Identifiers[kind])
		if err != nil {
			return nil, err
		}
		out = append(out, sealed)
	}
	return out, nil
}

// --- shaping ---

// normalised returns the registration with every value in the form the database stores.
// Done once, here, so the row, the event payload and the read model cannot disagree about
// what a telephone number looks like.
func (r Registration) normalised() Registration {
	r.NameEN = strings.TrimSpace(r.NameEN)
	r.NameBN = strings.TrimSpace(r.NameBN)
	r.PhonePrimary, _ = NormalisePhone(r.PhonePrimary)
	r.PhoneSecondary, _ = NormaliseSecondaryPhone(r.PhoneSecondary)
	r.Emergency.Phone, _ = NormaliseSecondaryPhone(r.Emergency.Phone)
	r.ConsentReference = strings.TrimSpace(r.ConsentReference)
	return r
}

// identifierOrder is the kinds this registration carries, in catalogue order. A map's
// iteration order would make the event payload and the identifier rows differ between two
// runs of the same registration, which is the sort of thing that makes a replay test flap
// once a month and be marked flaky rather than fixed.
func (r Registration) identifierOrder() []IdentifierKind {
	out := make([]IdentifierKind, 0, len(r.Identifiers))
	for _, kind := range []IdentifierKind{NationalID, BirthCertificate, Passport, DrivingLicence, OtherIdentifier} {
		if _, ok := r.Identifiers[kind]; ok {
			out = append(out, kind)
		}
	}
	return out
}

// patient is the row this registration writes. The clinical id and the id itself are the
// database's; everything else is here.
func (r Registration) patient(actor eventstore.Actor, now time.Time) Patient {
	registeredBy := actor.UserID()
	return Patient{
		FacilityID: actor.FacilityID(),
		NameEN:     r.NameEN, NameBN: r.NameBN, Sex: r.Sex,
		Birth: BirthDate{
			Date: r.BirthDate.In(Dhaka), Precision: r.DOBPrecision, Source: r.DOBSource,
		},
		PhonePrimary: r.PhonePrimary, PhoneSecondary: r.PhoneSecondary,
		Address: r.Address, Emergency: r.Emergency, Socio: r.Socio,
		Status:       StatusActive,
		RegisteredBy: &registeredBy, RegisteredAt: now,
	}
}

// payload is the event. Flat, complete, and carrying neither an identifier number nor the
// research id — ADR-0020 §5 says why both absences are load-bearing.
func (r Registration) payload(created Patient) eventstore.PatientRegistered {
	kinds := make([]string, 0, len(r.Identifiers))
	for _, kind := range r.identifierOrder() {
		kinds = append(kinds, string(kind))
	}
	return eventstore.PatientRegistered{
		FacilityID: created.FacilityID.String(),
		PatientID:  created.ID.String(),
		ClinicalID: created.ClinicalID,

		NameEN: r.NameEN, NameBN: r.NameBN, Sex: string(r.Sex),

		BirthDate:    r.BirthDate.In(Dhaka).Format(time.DateOnly),
		DOBPrecision: string(r.DOBPrecision),
		DOBSource:    string(r.DOBSource),

		PhonePrimary: r.PhonePrimary, PhoneSecondary: r.PhoneSecondary,

		Division: r.Address.Division, District: r.Address.District, Upazila: r.Address.Upazila,
		AddressLine: r.Address.AddressLine, Postcode: r.Address.Postcode,

		EmergencyName: r.Emergency.Name, EmergencyRelation: r.Emergency.Relation,
		EmergencyPhone: r.Emergency.Phone,

		EducationLevel: r.Socio.Education, OccupationCategory: r.Socio.Occupation,
		IncomeBand: r.Socio.IncomeBand, HouseholdSize: r.Socio.HouseholdSize,
		ResidenceType: r.Socio.Residence, MedicinePayer: r.Socio.MedicinePayer,

		IdentifierKinds:  kinds,
		ConsentReference: r.ConsentReference,
	}
}
