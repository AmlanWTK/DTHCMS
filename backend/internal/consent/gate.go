package consent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Enforcement (CP36, §15.1: "consent enforcement at the point of use, not merely recorded").
//
// A consent table that nothing consults is a compliance artefact, not a control. What makes
// this one a control is that the modules which act on a patient cannot act without going
// through a Gate, and the two that matter most cannot even be *written* without one:
//
//	research      enforced in the database. `dthcms_research` has no SELECT on
//	              research.research_subject at all; it reads `research.cohort`, which filters
//	              on live consent. A researcher cannot query somebody who said no even by
//	              writing the query themselves
//	communication `Sender` takes a patient id, so a caller who wants to send anything has to
//	              hold a Sender, and the only Sender the composition root hands out is a
//	              gated one. There is no "remember to check" step to forget
//	AI            the same shape, via `Guard`, when the gateway lands
//
// The one-minute criterion is met by construction rather than by machinery: the row the gate
// reads is written by the same COMMIT that writes the revocation event, so there is no
// interval in which the ledger and the gate disagree. The cache below is what would break
// that, which is why its lifetime is measured in seconds and why it is invalidated by the
// service on every write.

// Reader is the part of Store a gate needs. An interface so an enforcement point can be
// tested without a database and so the gate cannot accidentally acquire write access.
type Reader interface {
	One(ctx context.Context, patientID, facility uuid.UUID, t Type) (Record, error)
}

// Gate answers "may the clinic do this to this patient".
type Gate struct {
	reader Reader
	clock  func() time.Time

	// A cache, because an outreach run asks about the same patient several times in a
	// second and a database round trip per question is a run nobody will use. Deliberately
	// tiny: `CacheTTL` is the *whole* exposure of the revocation criterion, so it is
	// seconds, and a write invalidates the entry outright rather than waiting for it.
	mu     sync.RWMutex
	cached map[cacheKey]cacheEntry
}

// CacheTTL is how long a consent answer may be reused.
//
// Five seconds, against a criterion of sixty. The number is not a performance tuning: it is
// the maximum time the system may keep sending messages to somebody who has just withdrawn
// consent, and it is chosen to be obviously inside the budget rather than close to it.
const CacheTTL = 5 * time.Second

type cacheKey struct {
	patient  uuid.UUID
	facility uuid.UUID
	kind     Type
}

type cacheEntry struct {
	record Record
	until  time.Time
}

func NewGate(reader Reader, clock func() time.Time) *Gate {
	if clock == nil {
		clock = time.Now
	}
	return &Gate{reader: reader, clock: clock, cached: map[cacheKey]cacheEntry{}}
}

// Check returns nil when the consent is live, and a Denied otherwise.
//
// Errors, not booleans. A boolean at a call site is a boolean somebody negates wrongly at
// four in the afternoon; an error carries what was missing and what state it was in, which
// is what the refusal has to say on a screen.
func (g *Gate) Check(ctx context.Context, patientID, facility uuid.UUID, t Type) error {
	record, err := g.state(ctx, patientID, facility, t)
	if err != nil {
		return err
	}
	if record.Live() {
		return nil
	}
	return Denied{PatientID: patientID, ConsentType: t, Status: record.Status, RevokedAt: record.RevokedAt}
}

// Allows is Check as a boolean, for a screen deciding whether to show a button. Never for
// deciding whether to do the thing: an error that is discarded is a check that did not
// happen, and `if allowed` reads identically whether or not the lookup failed.
func (g *Gate) Allows(ctx context.Context, patientID, facility uuid.UUID, t Type) bool {
	return g.Check(ctx, patientID, facility, t) == nil
}

// Forget drops a patient's cached answers. Called by the service on every write, so a
// revocation takes effect on the next question rather than up to CacheTTL later.
func (g *Gate) Forget(patientID uuid.UUID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for key := range g.cached {
		if key.patient == patientID {
			delete(g.cached, key)
		}
	}
}

func (g *Gate) state(ctx context.Context, patientID, facility uuid.UUID, t Type) (Record, error) {
	key := cacheKey{patient: patientID, facility: facility, kind: t}
	now := g.clock()

	g.mu.RLock()
	entry, ok := g.cached[key]
	g.mu.RUnlock()
	if ok && now.Before(entry.until) {
		return entry.record, nil
	}

	record, err := g.reader.One(ctx, patientID, facility, t)
	if err != nil {
		// Deliberately not cached and deliberately not "allowed". A gate that fails open is
		// a gate that sends messages during a database incident.
		return Record{}, fmt.Errorf("consent: could not read %s consent: %w", t, err)
	}

	g.mu.Lock()
	g.cached[key] = cacheEntry{record: record, until: now.Add(CacheTTL)}
	g.mu.Unlock()
	return record, nil
}

// --- the module boundary ---

// Message is one thing about to be sent to a patient.
type Message struct {
	PatientID  uuid.UUID
	FacilityID uuid.UUID
	// Kind decides which consent applies. A clinical result and a camp invitation are not
	// the same permission, and §11.2 is explicit that transactional follow-up and outreach
	// must be separately consented and separately controllable.
	Kind Purpose
	Body string
}

// Purpose is what the clinic is about to do, expressed as the reason rather than as the
// consent — so a call site says what it is doing and the mapping lives in one place.
type Purpose string

const (
	// Treat is anything done as part of care.
	Treat Purpose = "treat"
	// Remind is an appointment reminder or a result notification: transactional, and still
	// a message, so still COMMUNICATION.
	Remind Purpose = "remind"
	// Invite is a camp invitation or a community follow-up: OUTREACH, separately consented
	// because a patient may want their results and not a Saturday visit (§11.2).
	Invite Purpose = "invite"
	// Analyse is inclusion in the anonymised cohort.
	Analyse Purpose = "analyse"
	// Interpret is the AI gateway reading the record.
	Interpret Purpose = "interpret"
)

// Requires maps a purpose to the consent it needs.
func Requires(p Purpose) (Type, bool) {
	switch p {
	case Treat:
		return Care, true
	case Remind:
		return Communication, true
	case Invite:
		return Outreach, true
	case Analyse:
		return Research, true
	case Interpret:
		return AIProcessing, true
	}
	return "", false
}

// Sender is the communication module's port, declared here rather than there.
//
// That inversion is the enforcement. A module that wants to send holds a Sender; the only
// Sender the composition root constructs is `Gated`, so there is no code path from "I have
// something to say" to "it was sent" that does not pass a consent check. Declaring the
// interface in the communication module and remembering to wrap it would be the same design
// with a step somebody can skip.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Gated wraps a Sender with the consent check for the message's purpose.
type Gated struct {
	gate *Gate
	next Sender
}

func NewGatedSender(gate *Gate, next Sender) Gated { return Gated{gate: gate, next: next} }

func (g Gated) Send(ctx context.Context, m Message) error {
	kind, ok := Requires(m.Kind)
	if !ok {
		// An unknown purpose is refused rather than allowed. A new kind of message added
		// without deciding which consent covers it is exactly the mistake this catches.
		return fmt.Errorf("consent: %q is not a purpose with a consent behind it", m.Kind)
	}
	if err := g.gate.Check(ctx, m.PatientID, m.FacilityID, kind); err != nil {
		return err
	}
	return g.next.Send(ctx, m)
}

// Guard is the same check for anything that is not a message — the AI gateway, an export,
// a report. Returns the gate's Denied unchanged, so a caller can say which consent was
// missing without knowing about consent types.
func Guard(ctx context.Context, gate *Gate, patientID, facility uuid.UUID, p Purpose) error {
	kind, ok := Requires(p)
	if !ok {
		return fmt.Errorf("consent: %q is not a purpose with a consent behind it", p)
	}
	return gate.Check(ctx, patientID, facility, kind)
}
