package patient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Merging two records that are one person (CP30).
//
// Three properties hold, and each is a decision rather than an implementation detail:
//
//	never automatic     A wrong merge is worse than a duplicate. A duplicate is two
//	                    incomplete histories; a wrong merge is one history containing
//	                    another person's clinical facts, and nothing downstream can tell.
//	never a delete      The losing record stays and redirects. Every event ever written
//	                    against it still names it, with its original attribution, and
//	                    `read.surviving_patient()` is how a query follows the chain.
//	always justified    Who, when, on what score, and why — plus the candidate list that
//	                    was on screen at the moment of the decision, so that "why did we
//	                    merge these two" is answerable after the matcher has been retuned.

var (
	// ErrCannotMerge covers the shapes that are not merges: a record into itself, a record
	// that has already been merged away, one at another facility.
	ErrCannotMerge = errors.New("patient: these two records cannot be merged")
	// ErrAlreadyMerged means the losing record has already been merged into something. A
	// second merge would make "where did this history go" ambiguous.
	ErrAlreadyMerged = errors.New("patient: that record has already been merged")
)

// MergeRequest is one decision.
type MergeRequest struct {
	SurvivorID uuid.UUID
	MergedID   uuid.UUID
	// Score and Decision are the matcher's view and the person's. Both are recorded: a
	// merge against a low score is a human overruling the machine, which is legitimate and
	// is the case a reviewer will want to find.
	Score    float64
	Decision string
	// Justification is required and is free text. "Duplicate" is not a justification.
	Justification string
	// Candidates is the list that was on screen. Stored whole.
	Candidates []Candidate
	EventID    uuid.UUID
}

// Merge records the decision, redirects the losing record, and appends PATIENT_MERGED —
// all in one transaction, because a merge that half-happened leaves a record that is
// neither live nor redirected and no query can be written that copes with both.
func (s *Service) Merge(ctx context.Context, in MergeRequest, source eventstore.Source) (eventstore.Event, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return eventstore.Event{}, err
	}
	if in.SurvivorID == in.MergedID {
		return eventstore.Event{}, fmt.Errorf("%w: a record cannot be merged into itself", ErrCannotMerge)
	}

	// A retry of a merge that landed. Answered from the ledger for the same reason
	// registration is: the check is the ledger's own uniqueness on event_id.
	if existing, err := s.events.ByID(ctx, in.EventID); err == nil {
		return existing, nil
	} else if !errors.Is(err, eventstore.ErrNotFound) {
		return eventstore.Event{}, err
	}

	survivor, err := s.store.ByID(ctx, in.SurvivorID, actor.FacilityID())
	if err != nil {
		return eventstore.Event{}, err
	}
	losing, err := s.store.ByID(ctx, in.MergedID, actor.FacilityID())
	if err != nil {
		return eventstore.Event{}, err
	}
	if losing.Status == StatusMerged {
		return eventstore.Event{}, ErrAlreadyMerged
	}
	if survivor.Status == StatusMerged {
		// Merging into a record that itself redirects would build a chain nobody asked
		// for. The caller is told to merge into the survivor's survivor instead.
		return eventstore.Event{}, fmt.Errorf(
			"%w: %s has itself been merged into another record", ErrCannotMerge, survivor.ClinicalID)
	}

	candidates, err := json.Marshal(orEmpty(in.Candidates))
	if err != nil {
		return eventstore.Event{}, err
	}
	payload, err := json.Marshal(eventstore.PatientMerged{
		FacilityID: actor.FacilityID().String(),
		MergedID:   in.MergedID.String(), SurvivorID: in.SurvivorID.String(),
		Score: in.Score, Decision: in.Decision, Justification: in.Justification,
		CandidateIDs: candidateIDs(in.Candidates),
	})
	if err != nil {
		return eventstore.Event{}, err
	}

	var written eventstore.Event
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		mergedID := in.MergedID
		written, err = s.events.AppendInTx(ctx, tx, eventstore.Envelope{
			EventID: in.EventID,
			// On the losing aggregate: that is the record whose meaning changed.
			AggregateType: "PATIENT", AggregateID: in.MergedID, PatientID: &mergedID,
			EventType: "PATIENT_MERGED", EventVersion: 1,
			OccurredAt: s.clock.Now().UTC(), Actor: actor, Source: source,
			Payload: payload,
		})
		if err != nil {
			return err
		}
		if err := q.MarkPatientMerged(ctx, dbgen.MarkPatientMergedParams{
			ID: in.MergedID, MergedIntoID: uuid.NullUUID{UUID: in.SurvivorID, Valid: true},
			StatusReason: fmt.Sprintf("merged into %s", survivor.ClinicalID),
			FacilityID:   actor.FacilityID(),
		}); err != nil {
			return err
		}
		return q.InsertPatientMerge(ctx, dbgen.InsertPatientMergeParams{
			FacilityID: actor.FacilityID(), SurvivorID: in.SurvivorID, MergedID: in.MergedID,
			Score: numeric(in.Score), Decision: in.Decision, Justification: in.Justification,
			Candidates: candidates, MergedBy: actor.UserID(), EventID: in.EventID,
		})
	})
	if err != nil {
		return eventstore.Event{}, err
	}
	return written, nil
}

// SurvivingID follows the redirect chain to the record that is still live. Every read that
// takes a patient id from an old card, an old report or an old event should go through it.
func (s *Store) SurvivingID(ctx context.Context, patientID uuid.UUID) (uuid.UUID, error) {
	return s.q.SurvivingPatient(ctx, patientID)
}

// MergeHistory is every record that now redirects to this one, with the decision behind
// each. The merge screen shows it, and so does anybody asking why a chart has two names in
// its history.
func (s *Store) MergeHistory(ctx context.Context, survivorID uuid.UUID) ([]dbgen.CorePatientMerge, error) {
	return s.q.MergesForSurvivor(ctx, survivorID)
}

func candidateIDs(candidates []Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.PatientID.String())
	}
	return out
}

func orEmpty(candidates []Candidate) []Candidate {
	if candidates == nil {
		return []Candidate{}
	}
	return candidates
}
