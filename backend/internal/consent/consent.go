// Package consent is what a patient has agreed to, and what that permits (CP36, §15.1, D-02).
//
// The engine, not the wording. D-02 — what exactly is consented to, in what words, with what
// withdrawal semantics — is Dr. Nahid's and counsel's, and is deferred. What is built here is
// everything that has to be true whatever the words turn out to be, arranged so that loading
// the approved text later is an INSERT rather than a migration.
//
// Three things are worth reading as decisions rather than as code (ADR-0022):
//
//	layered      five types, each independently grantable and revocable. A patient who wants
//	             treatment but not an SMS at seven in the morning is expressing a preference
//	             that a single "I consent" box cannot record and cannot answer for later
//	versioned    the record carries the template version, the language shown and the digest
//	             of the exact text. "The patient consented to research" is not an answer
//	             anybody can act on in 2031
//	enforced     at the point of use, not merely recorded (§15.1). Research is enforced by
//	             database privilege; everything else goes through Gate, and a caller that
//	             forgets to ask is a caller that does not compile against Sender
package consent

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
)

// Type is one of the five things a patient consents to. The vocabulary lives in the event
// schema, because an event is immutable and a category once written exists for as long as
// the deployment does.
type Type string

const (
	// Care is treatment itself. Without it there is nothing lawful to do with the record.
	Care Type = "care"
	// Communication is telephone calls and SMS (§11.2). A reminder is not treatment.
	Communication Type = "communication"
	// Research is inclusion in the anonymised cohort (§12). Opt-in, never assumed.
	Research Type = "research"
	// AIProcessing is the AI gateway reading the record (§7). Separate, because a patient
	// may accept a human reading their notes and not a model.
	AIProcessing Type = "ai_processing"
	// Outreach is community follow-up — a home visit, a camp invitation.
	Outreach Type = "outreach"
)

// Types is every type, in the order a capture screen should offer them.
var Types = func() []Type {
	out := make([]Type, 0, len(eventstore.ConsentTypes))
	for _, name := range eventstore.ConsentTypes {
		out = append(out, Type(name))
	}
	return out
}()

// Known says whether a string is a consent type this deployment knows.
func Known(t Type) bool {
	for _, candidate := range Types {
		if candidate == t {
			return true
		}
	}
	return false
}

// CaptureMethod is how the consent was actually taken.
type CaptureMethod string

const (
	Signature  CaptureMethod = "signature"
	Thumbprint CaptureMethod = "thumbprint"
	// VerbalAttested is a staff attestation with a witness named. Weaker evidence than a
	// thumbprint, and here because refusing it would not make consent better recorded — it
	// would make it recorded on paper and not here. The record says which it is.
	VerbalAttested CaptureMethod = "verbal_attested"
	PaperForm      CaptureMethod = "paper_form"
)

// Status is where a consent stands now.
type Status string

const (
	Granted Status = "granted"
	Revoked Status = "revoked"
	// Absent is never stored: it is what the gate returns for a consent that was never
	// taken. Distinguished from Revoked on purpose — "never asked" and "asked and refused"
	// are different failures and want different screens.
	Absent Status = "absent"
)

// Template is one version of one consent's wording, in one language.
type Template struct {
	ID          uuid.UUID `json:"-"`
	ConsentType Type      `json:"consent_type"`
	Version     int       `json:"version"`
	Language    string    `json:"language"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	// Digest is the SHA-256 of Body, in hex. It travels into the event, so a template row
	// altered by somebody with database access is detectable from the ledger.
	Digest        string     `json:"digest"`
	Status        string     `json:"status"`
	EffectiveFrom *time.Time `json:"effective_from,omitempty"`
}

// Record is what a patient has agreed to, as it stands.
type Record struct {
	PatientID   uuid.UUID `json:"-"`
	ConsentType Type      `json:"consent_type"`
	Status      Status    `json:"status"`

	TemplateVersion int    `json:"template_version"`
	Language        string `json:"language"`

	CaptureMethod   CaptureMethod `json:"capture_method"`
	PaperReference  string        `json:"paper_reference,omitempty"`
	WitnessedByCode string        `json:"witnessed_by_code,omitempty"`

	GrantedForRelation string `json:"granted_for_relation,omitempty"`
	GrantedForName     string `json:"granted_for_name,omitempty"`

	GrantedAt     time.Time  `json:"granted_at"`
	GrantedByCode string     `json:"granted_by_code,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	RevokedByCode string     `json:"revoked_by_code,omitempty"`
	RevokeReason  string     `json:"revoke_reason,omitempty"`
	// HasEvidence says an image exists without handing over a URL. A signed URL in a list
	// of consents is a URL minted for every row whether or not anybody opens one.
	HasEvidence bool `json:"has_evidence"`
}

// Live says whether this consent permits the thing it covers, right now.
func (r Record) Live() bool { return r.Status == Granted }

// Grant is a consent being taken.
type Grant struct {
	EventID     uuid.UUID
	ConsentType Type
	Language    string

	CaptureMethod  CaptureMethod
	EvidenceKey    string
	EvidenceSHA256 string
	PaperReference string

	WitnessedBy uuid.UUID

	GrantedForRelation string
	GrantedForName     string

	Source eventstore.Source
}

// Revocation is a consent being withdrawn.
type Revocation struct {
	EventID     uuid.UUID
	ConsentType Type
	// Reason is optional, deliberately. A patient withdrawing consent does not owe anybody
	// an explanation, and a mandatory field here would be filled in with "revoked" by an
	// operator standing in front of somebody who wants to leave.
	Reason string
	// RequestedBy is patient, guardian or clinic.
	RequestedBy string
	Source      eventstore.Source
}

// The refusals this package raises.
var (
	// ErrUnknownType is a consent type this deployment does not have.
	ErrUnknownType = errors.New("consent: that is not a consent type this clinic records")
	// ErrNoTemplate is a consent type with no active wording in the requested language —
	// which is the normal state until D-02 is answered and the text is loaded.
	ErrNoTemplate = errors.New("consent: no approved wording is published for that consent in that language")
	// ErrNotGranted is a revocation of something that was never granted.
	ErrNotGranted = errors.New("consent: there is nothing to revoke")
	// ErrWitnessRequired is a thumbprint or a verbal attestation with nobody watching.
	ErrWitnessRequired = errors.New("consent: that capture method needs a witness")
	// ErrEvidenceRequired is a signature or thumbprint with no image behind it.
	ErrEvidenceRequired = errors.New("consent: a signature or thumbprint needs its image")
	// ErrReplayed is one event id used for two different things.
	ErrReplayed = errors.New("consent: that event id already belongs to a different event")
)

// Denied is the gate's refusal, carrying which consent was missing and what state it was in.
//
// A typed error rather than a boolean, because the two failures want different words on a
// screen: "this patient has never been asked" is an action for the desk, and "this patient
// withdrew consent on 14 September" is not.
type Denied struct {
	PatientID   uuid.UUID
	ConsentType Type
	Status      Status
	RevokedAt   *time.Time
}

func (d Denied) Error() string {
	switch d.Status {
	case Revoked:
		return fmt.Sprintf("consent: %s consent was withdrawn", d.ConsentType)
	default:
		return fmt.Sprintf("consent: no %s consent has been recorded", d.ConsentType)
	}
}

// Is lets callers write errors.Is(err, consent.ErrDenied).
func (d Denied) Is(target error) bool { return target == ErrDenied }

// ErrDenied is what every gate refusal matches.
var ErrDenied = errors.New("consent: not consented")

// validate checks what can be checked without the database.
func (g Grant) validate() error {
	if !Known(g.ConsentType) {
		return fmt.Errorf("%w: %q", ErrUnknownType, g.ConsentType)
	}
	switch g.CaptureMethod {
	case Signature, Thumbprint:
		if strings.TrimSpace(g.EvidenceKey) == "" || len(g.EvidenceSHA256) != 64 {
			return ErrEvidenceRequired
		}
	case PaperForm:
		if strings.TrimSpace(g.PaperReference) == "" {
			return fmt.Errorf("consent: a paper consent needs the form reference, or nobody can find it")
		}
	case VerbalAttested:
	default:
		return fmt.Errorf("consent: %q is not a capture method", g.CaptureMethod)
	}
	if g.CaptureMethod == Thumbprint || g.CaptureMethod == VerbalAttested {
		if g.WitnessedBy == uuid.Nil {
			return fmt.Errorf("%w: %s", ErrWitnessRequired, g.CaptureMethod)
		}
	}
	if (g.GrantedForName == "") != (g.GrantedForRelation == "") {
		return errors.New("consent: a consent given by somebody else needs both their name and their relation")
	}
	return nil
}
