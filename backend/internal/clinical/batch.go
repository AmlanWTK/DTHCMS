package clinical

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// A station's worth of measurements, written together (CP45).
//
// # Why a batch exists at all
//
// An anthropometry entry is six measurements and four derived values. Ten round trips over
// clinic wifi is ten chances to lose one, and criterion (3) gives the whole entry thirty
// seconds — most of which should be the operator handling a tape measure, not a progress
// spinner.
//
// # What a batch is and is not
//
// It is **one transaction**, not one event. Six measurements are six facts and the ledger
// records six of them, each with its own id, its own idempotency and its own attribution.
// What the transaction buys is that the record never holds three of six: a set of
// measurements taken in one sitting either all landed or none did, and an operator who saw a
// failure knows to type it again rather than wondering which three went through.
//
// # Why the derivations come last, and inside
//
// A BMI computed from a height written in this same transaction has to see that height. So
// the derivations run after the measurements, through the transaction's own connection —
// otherwise the server would derive from the previous visit's numbers while the phone,
// deriving locally from what is on screen, showed today's. That is exactly the
// server-disagrees-with-client failure criterion (2) exists to rule out.

// Batch is a set of values recorded together, with the derivations to run afterwards.
type Batch struct {
	// EventID identifies this batch, and is what makes a retry safe. Each record carries its
	// own event id for the ledger; this one seeds the ids of the *derived* values, which the
	// client cannot supply because it does not know in advance which derivations will
	// succeed. A tablet that lost the reply and pressed save again therefore writes the same
	// derived ids, and the ledger's primary key absorbs the retry.
	EventID uuid.UUID
	// Records are the measured values, in the order the screen collected them.
	Records []Recording
	// Derive names the values to compute once the measurements have landed. Server-computed
	// from the record, never accepted as numbers — CP43's rule, unchanged.
	Derive []Derivable
	// AsianScale picks the obesity cut-offs for any classification a derivation produces.
	AsianScale   bool
	PatientID    uuid.UUID
	VisitID      *uuid.UUID
	LedgerSource eventstore.Source
}

// ErrBatchEmpty is a batch with nothing in it — almost always a client bug, and cheaper to
// name than to let through as a successful write of nothing.
var ErrBatchEmpty = errors.New("clinical: a batch has to contain something")

// ErrBatchTooLarge is a batch beyond what one station entry could plausibly be.
var ErrBatchTooLarge = errors.New("clinical: that is more values than one station entry")

// BatchItemError says which value in the batch was refused, and why.
//
// The index matters more than it looks. An operator who saved six measurements and got back
// "that value is outside the plausible range" has to re-read all six; one who got back
// "value 3" looks at the waist and finds the extra zero.
type BatchItemError struct {
	Index int
	Err   error
}

func (e *BatchItemError) Error() string { return e.Err.Error() }
func (e *BatchItemError) Unwrap() error { return e.Err }

// MaxBatch is the ceiling. Twenty is roughly twice the largest real station form (vitals with
// two blood-pressure readings and their context), which leaves room without turning the
// endpoint into a bulk import that would hold a transaction open for seconds.
const MaxBatch = 20

// RecordBatch writes every value and then every derivation, in one transaction.
//
// The returned observations are in the order they were written: the measurements first, in
// the order given, then the derived values in the order named. A screen that shows them back
// therefore shows them in the order the operator typed them.
func (s *Service) RecordBatch(ctx context.Context, in Batch) ([]Observation, []Alert, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(in.Records) == 0 && len(in.Derive) == 0 {
		return nil, nil, ErrBatchEmpty
	}
	if in.EventID == uuid.Nil {
		return nil, nil, fmt.Errorf("%w: a batch needs its own event id", ErrWrongShape)
	}
	if len(in.Records)+len(in.Derive) > MaxBatch {
		return nil, nil, fmt.Errorf("%w: %d values, and the limit is %d",
			ErrBatchTooLarge, len(in.Records)+len(in.Derive), MaxBatch)
	}
	// Every record in a batch is for the batch's patient. Not a convenience: a batch that
	// could span patients is a batch where a mis-set field puts one person's weight on
	// another person's chart, inside a transaction that makes it look deliberate.
	for i := range in.Records {
		if in.Records[i].PatientID != in.PatientID {
			return nil, nil, fmt.Errorf("%w: every value in a batch is for the same patient", ErrWrongShape)
		}
	}

	var ids []uuid.UUID
	var alerts []Alert
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		ids = ids[:0]
		alerts = alerts[:0]
		for i, record := range in.Records {
			id, raised, err := s.appendRecording(ctx, tx, q, actor, record)
			if err != nil {
				return &BatchItemError{Index: i, Err: err}
			}
			ids = append(ids, id)
			// A form of six values can raise more than one alert — a blood pressure of
			// 200/120 raises two — and the operator is shown all of them. Collapsing them
			// into "one alert for this entry" would let the second one disappear behind
			// whichever the screen happened to draw.
			if raised != nil {
				alerts = append(alerts, *raised)
			}
		}

		for _, what := range in.Derive {
			id, err := s.appendDerivation(ctx, tx, q, actor, Derivation{
				EventID:      derivedEventID(in.EventID, in.PatientID, what),
				PatientID:    in.PatientID,
				VisitID:      in.VisitID,
				What:         what,
				AsianScale:   in.AsianScale,
				LedgerSource: in.LedgerSource,
			})
			if err != nil {
				// A derivation whose inputs are not in the record is not a failed batch.
				// Waist without hip is a normal half-finished anthropometry entry, and
				// refusing the whole write because a ratio could not be computed would
				// throw away five measurements to protect one that was never promised.
				if errors.Is(err, ErrInputsMissing) || errors.Is(err, ErrCannotCompute) {
					continue
				}
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// After the commit, never before it (CP50).
	s.notify(ctx, alerts)

	out := make([]Observation, 0, len(ids))
	for _, id := range ids {
		observation, err := s.store.ByID(ctx, id, actor.FacilityID())
		if err != nil {
			return nil, nil, err
		}
		out = append(out, observation)
	}
	return out, alerts, nil
}

// derivedEventID gives each derivation in a batch a ledger id derived from the batch's own
// id, so that a tablet which lost the reply and pressed save again writes the same ids and
// the ledger's primary key absorbs the retry — the same trick CP40's reroute uses. A random
// id here would make every retry a duplicate BMI.
//
// The patient is in the seed as well as the batch id. Two batches can only collide if a
// client reuses an event id, and if one ever does, this makes the collision land on the same
// patient's own row rather than writing a BMI onto somebody else's chart.
func derivedEventID(batch, patient uuid.UUID, what Derivable) uuid.UUID {
	return uuid.NewSHA1(batchNamespace,
		[]byte(batch.String()+":"+patient.String()+":derive:"+string(what)))
}

var batchNamespace = uuid.MustParse("9e2a7c40-5f18-4b6d-8a31-2c7d94e0b5f6")
