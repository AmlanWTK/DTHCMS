package signing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
)

// Signing and verifying (CP84).
//
// # What this service is careful about, in order
//
//  1. **It reads the prescription back before signing it.** The canonical bytes are computed
//     from the read model, not from anything the caller sent and not from an in-memory copy held
//     across the transaction. A signature made over what the caller *said* the prescription was
//     would be worthless, and a signature made over nanosecond-precision timestamps that
//     PostgreSQL will round on the way back would fail verification on a prescription nobody
//     touched.
//  2. **It does not re-implement the clearance gate.** CP83's trigger refuses entry to SIGNED
//     without a standing clearance, for every path including the ones nobody remembers. This
//     service reads the clearance so the canonical form can cover it and so the physician gets a
//     sentence rather than a constraint violation — and if that check is deleted the database
//     still refuses. `core.assert_signing_still_sits_behind_the_clearance_gate` is what notices
//     if the trigger goes away.
//  3. **The step-up is not here.** It is `httpx.RequireStepUp` on the route, with
//     `auth.PurposeSignPrescription`, which means the token is consumed before this code runs
//     and a token minted for any other purpose never reaches it. Putting the check here as well
//     would be a second copy of an authorisation rule, and the failure mode of two copies is
//     that they disagree.
//
// # Nothing here logs a drug, a dose or a patient
//
// Errors returned from this file name a prescription id and a status and nothing else. The key
// is never in an error: the only error that mentions the signer at all names its *kind*.

// Sheets is the prescription read model this service signs.
//
// An interface rather than `*prescription.Store` so that the service can be exercised without
// the whole prescribing stack, and so that what signing is allowed to do to a prescription is
// visible in three lines: read one, transition it, and nothing else.
type Sheets interface {
	ByID(ctx context.Context, id, facility uuid.UUID) (prescription.Prescription, error)
	InTransaction(ctx context.Context, fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error
}

// Clearances is station 10's standing decision on a prescription.
//
// An interface, and deliberately narrow: the only thing signing may ask QA is "was this cleared,
// and by which review". It may not clear, may not bounce and may not override. `qa.Store`
// satisfies this through a two-line adapter in the composition root, which is where the two
// modules are allowed to know about each other.
type Clearances interface {
	StandingClearance(ctx context.Context, facility, prescription uuid.UUID) (Clearance, bool, error)
}

// Service signs prescriptions and verifies signatures.
type Service struct {
	sheets     Sheets
	store      *Store
	events     *eventstore.Store
	signer     Signer
	clearances Clearances
	ring       *secretbox.Ring
	clock      interface{ Now() time.Time }
}

// ServiceConfig assembles one.
type ServiceConfig struct {
	Sheets Sheets
	Store  *Store
	Events *eventstore.Store
	// Signer is the seam. Never nil in a wired service: [NewSigner] is what decides which one
	// this deployment may have, and it refuses at boot rather than here.
	Signer     Signer
	Clearances Clearances
	// Ring seals the verification token so a reprint carries the same QR. See
	// [Service.VerificationToken]. Nil makes signing fail rather than storing a token in clear.
	Ring  *secretbox.Ring
	Clock interface{ Now() time.Time }
}

// NewService builds one.
func NewService(cfg ServiceConfig) *Service {
	return &Service{sheets: cfg.Sheets, store: cfg.Store, events: cfg.Events,
		signer: cfg.Signer, clearances: cfg.Clearances, ring: cfg.Ring, clock: cfg.Clock}
}

func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

// Signed is what a signing returns: the signature as recorded, and the token that goes in the QR.
type Signed struct {
	Signature Signature
	// Token is the plaintext verification token. **Returned exactly once, here.** It is not in
	// the database, not in the ledger, not in a log line and not retrievable afterwards — what
	// is stored is its digest. The sheet that prints carries it; nothing else does.
	Token string
}

// Sign signs a cleared prescription.
//
// # What happens, in the order it has to happen in
//
//  1. Read the prescription back from the read model, in this facility.
//  2. Refuse a second signature, and refuse one on a sheet that is not waiting for it.
//  3. Read station 10's standing clearance, so the canonical form covers the gate it came
//     through.
//  4. Canonicalise, at [CanonicalVersion].
//  5. Ask the [Signer] to sign those exact bytes. The private half never comes back.
//  6. Mint a verification token, keep its digest.
//  7. Append `PRESCRIPTION_SIGNED` v2 — the transition and the signature, one event, because
//     they are one fact — with its synchronous projection inside the same transaction. The
//     deferred trigger refuses the commit if the two ever come apart.
//
// The step-up is the route's (see the package comment). The device assurance is *recorded* from
// the principal rather than required of it: `docs/signing.md` §4.
func (s *Service) Sign(ctx context.Context, eventID, id uuid.UUID) (Signed, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return Signed{}, err
	}
	facility := actor.FacilityID()
	now := s.now()

	sheet, err := s.sheets.ByID(ctx, id, facility)
	if errors.Is(err, prescription.ErrNotFound) {
		return Signed{}, ErrNotFound
	}
	if err != nil {
		return Signed{}, err
	}

	// A second signature is refused here rather than left to the primary key, because the
	// sentence a physician needs ("this is already signed; correct it instead") is not the one a
	// unique violation produces. The primary key is still what makes it impossible.
	if _, err := s.store.ByPrescription(ctx, facility, id); err == nil {
		return Signed{}, ErrAlreadySigned
	} else if !errors.Is(err, ErrNotSigned) {
		return Signed{}, err
	}
	if sheet.Status != prescription.StatusQAReview {
		// The machine says the same thing and the trigger says it again. This one exists to
		// say it in words: a draft is submitted, not signed.
		return Signed{}, &prescription.TransitionError{
			From: sheet.Status, To: prescription.StatusSigned, Reason: "no_edge"}
	}

	clearance, cleared, err := s.clearance(ctx, facility, id)
	if err != nil {
		return Signed{}, err
	}
	if !cleared {
		// The database would refuse this a moment later. Refusing here turns a constraint
		// violation into a sentence, and **does not replace** the trigger: see the file header.
		return Signed{}, ErrNotCleared
	}

	subject := Subject{Sheet: sheet, SignedAt: now, SignedBy: actor.UserID(), Clearance: clearance}
	canonical, err := Canonicalise(CanonicalVersion, subject)
	if err != nil {
		return Signed{}, err
	}
	signature, err := s.signer.Sign(ctx, canonical)
	if err != nil {
		return Signed{}, err
	}
	token, err := NewToken()
	if err != nil {
		return Signed{}, err
	}
	// Sealed, never stored in clear. The additional data binds the ciphertext to this
	// prescription, so a sealed token lifted from one row cannot be pasted into another.
	if s.ring == nil {
		return Signed{}, errors.New("signing: no secret ring; refusing to store a verification token in clear")
	}
	sealed, keyID, err := s.ring.Seal([]byte(token.Plaintext), id[:])
	if err != nil {
		return Signed{}, err
	}

	payload := eventstore.PrescriptionSigned{
		PrescriptionID: id.String(),
		FromStatus:     string(sheet.Status),
		ToStatus:       string(prescription.StatusSigned),
		At:             now,
		FacilityID:     facility.String(),

		CanonicalVersion: CanonicalVersion,
		CanonicalSHA256:  CanonicalDigest(canonical),
		Algorithm:        Algorithm,
		SignerKind:       string(s.signer.Kind()),
		KeyID:            s.signer.KeyID(),
		PublicKey:        hex.EncodeToString(s.signer.PublicKey()),
		Signature:        hex.EncodeToString(signature),

		DeviceAssurance: string(assuranceOfContext(ctx)),
		// The clearance **by value**, both halves, and the same two values the canonical form
		// above was computed from. This is what lets verification recompute the bytes without
		// asking station 10 anything. See [Verification] and `signing.Signature`.
		QAReviewID:              clearance.ReviewID.String(),
		QAClearedAt:             clearance.DecidedAt,
		VerificationTokenDigest: hex.EncodeToString(token.Digest),
		VerificationTokenSealed: hex.EncodeToString(sealed),
		VerificationTokenKeyID:  keyID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Signed{}, err
	}
	if eventID == uuid.Nil {
		eventID = uuid.New()
	}

	if err := s.sheets.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, _ *dbgen.Queries) error {
		_, err := s.events.AppendInTx(ctx, tx, eventstore.Envelope{
			EventID:       eventID,
			AggregateType: "PRESCRIPTION",
			AggregateID:   id,
			PatientID:     &sheet.PatientID,
			VisitID:       &sheet.VisitID,
			EventType:     "PRESCRIPTION_SIGNED",
			// Version 2. The signature travels on the transition because they are one fact,
			// and a v1 event is still decodable through an upcaster that invents nothing.
			EventVersion: 2,
			OccurredAt:   now,
			Actor:        actor,
			Source:       eventstore.SourceWeb,
			Payload:      encoded,
		})
		return err
	}); err != nil {
		return Signed{}, err
	}

	recorded, err := s.store.ByPrescription(ctx, facility, id)
	if err != nil {
		return Signed{}, err
	}
	return Signed{Signature: recorded, Token: token.Plaintext}, nil
}

// clearance reads station 10's standing decision, tolerating an unwired QA module.
//
// A nil `Clearances` means this process was assembled without station 10 — which is the state a
// unit-test rig is in and must never be the state a deployment is in. It does **not** make
// signing succeed: the clearance comes back absent, [Sign] refuses, and the database would
// refuse anyway. Failing closed on a missing dependency is the only safe direction for a gate.
func (s *Service) clearance(ctx context.Context, facility, id uuid.UUID) (Clearance, bool, error) {
	if s.clearances == nil {
		return Clearance{}, false, nil
	}
	return s.clearances.StandingClearance(ctx, facility, id)
}

// assuranceOfContext reads how the signing session's device was established.
//
// Absent principal means [AssuranceNone], which is a legal value and is recorded as such. It is
// not an error, because `docs/signing.md` §4 decided that signing does not require a device at
// all — what it requires is a step-up, and that has already happened by the time this runs.
func assuranceOfContext(ctx context.Context) DeviceAssurance {
	principal, ok := httpx.PrincipalFrom(ctx)
	if !ok {
		return AssuranceNone
	}
	return assuranceOf(principal.DeviceAssurance)
}

// VerificationToken recovers the token that goes in a printed QR code.
//
// # Why this exists at all, given that the token is returned once at signing
//
// Because a prescription is printed more than once. The patient loses the paper; the pharmacy
// keeps a copy; CP89 renders the sheet a week later from the print model. Every one of those has
// to carry **the same** QR, because a QR that changed would make a prescription verified
// yesterday fail today with no explanation a patient could act on.
//
// So the token is sealed at signing and opened here, and that is a deliberate widening of who
// can mint one: not "anybody who reads the database" (which storing it in clear would have
// meant), but "anybody who holds the database *and* the ring key". The same trade the second
// factor already makes for its TOTP seeds.
//
// **It is not on any route.** The print model reads it; no handler returns it on its own. A
// token endpoint would be a way to ask the system for a working QR code for an arbitrary
// prescription, which is a forging aid with an audit trail.
func (s *Service) VerificationToken(ctx context.Context, facility, id uuid.UUID) (string, error) {
	if s.ring == nil {
		return "", errors.New("signing: no secret ring")
	}
	sealed, keyID, err := s.store.SealedToken(ctx, facility, id)
	if err != nil {
		return "", err
	}
	plaintext, err := s.ring.Open(sealed, keyID, id[:])
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// PrintBlock is the signature block the printed sheet carries (CP84 §7.3, CP85).
//
// # It does not verify, and that is deliberate
//
// The sheet says *this prescription was signed, by this physician, on this day, and here is the
// code that lets you check it*. It does not say "verified", because a claim of verification
// printed on a piece of paper is worth nothing — the paper says whatever it was printed with.
// The check happens when somebody scans the QR and asks this system, which is the whole point of
// CP85 existing.
//
// # Both halves of §6, in one struct
//
// The block carries the caveat that the handwritten image is a picture, so a renderer cannot
// show the picture without it.
func (s *Service) PrintBlock(ctx context.Context, facility, id uuid.UUID) (prescription.PrintSignature, error) {
	signature, err := s.store.ByPrescription(ctx, facility, id)
	if err != nil {
		return prescription.PrintSignature{}, err
	}
	token, err := s.VerificationToken(ctx, facility, id)
	if err != nil {
		return prescription.PrintSignature{}, err
	}
	image := TheImageIsNotTheSignature()
	return prescription.PrintSignature{
		Signed: true,
		NoteEN: "Signed electronically. Scan the code to check this prescription is genuine " +
			"and has not been altered.",
		NoteBN: "ইলেকট্রনিকভাবে স্বাক্ষরিত। এই ব্যবস্থাপত্রটি আসল এবং অপরিবর্তিত কিনা দেখতে " +
			"কোডটি স্ক্যান করুন।",
		SignedOn:         signature.SignedAt.UTC().Format("2006-01-02"),
		PhysicianNameEN:  signature.SignedByNameEN,
		PhysicianNameBN:  signature.SignedByNameBN,
		VerificationPath: VerificationPath(token),
		ImageCaveatEN:    image.CaveatEN,
		ImageCaveatBN:    image.CaveatBN,
	}, nil
}

// ---------------------------------------------------------------------------
// Verifying
// ---------------------------------------------------------------------------

// Verification is the answer to "is this prescription what it was when it was signed".
type Verification struct {
	PrescriptionID uuid.UUID `json:"prescription_id"`
	Verdict        Verdict   `json:"verdict"`
	Signature      Signature `json:"signature"`

	// RecomputedSHA256 is the digest of the canonical bytes as they are **now**. Equal to the
	// stored one on a verified prescription; different on a tampered one, which is what turns
	// "it failed" into "the content changed" for whoever is investigating.
	RecomputedSHA256 string `json:"recomputed_canonical_sha256"`

	// ReasonEN and ReasonBN are for the authenticated screen only, and say what kind of failure
	// this was — not which field. The public page gets [Verdict] and nothing else: a verifier
	// that told a forger which field to fix would be a forging aid.
	ReasonEN string `json:"reason_en,omitempty"`
	ReasonBN string `json:"reason_bn,omitempty"`
}

// Verify recomputes the canonical form and checks it against the stored signature.
//
// # Why it re-reads rather than trusting anything
//
// Everything this function uses comes out of the database on this call: the prescription, its
// items, the signature, the public key, the canonical version. Nothing is cached and nothing is
// passed in. That is what makes it answer the question somebody actually has — *is what is
// stored now what was signed then* — rather than the question a cache would answer, which is
// whether two copies of the same in-memory object agree.
//
// # Why the canonical version comes from the signature
//
// `Canonicalise(sig.CanonicalVersion, …)`, never `CanonicalVersion`. A build that has moved on
// to v2 verifying a v1 signature against v2 bytes would report every historical prescription as
// tampered; a build that silently fell back to the current version would be worse, because on
// the day the two happened to agree for one prescription it would report a tampered one as
// genuine.
func (s *Service) Verify(ctx context.Context, facility, id uuid.UUID) (Verification, error) {
	signature, err := s.store.ByPrescription(ctx, facility, id)
	if err != nil {
		return Verification{}, err
	}
	sheet, err := s.sheets.ByID(ctx, id, facility)
	if errors.Is(err, prescription.ErrNotFound) {
		return Verification{}, ErrNotFound
	}
	if err != nil {
		return Verification{}, err
	}
	return s.verifyAgainst(sheet, signature)
}

// verifyAgainst recomputes the canonical bytes and checks them against the signature.
//
// **It takes no `context.Context`, and that is the point rather than an oversight.** Everything
// this function needs is already in its two arguments, so it must not reach a database — and the
// way to make that hard to undo is to remove the thing every database call in this codebase
// requires. A future edit that wants to look something up has to widen the signature, which is a
// line in a diff rather than a call somebody adds inside an existing block.
func (s *Service) verifyAgainst(sheet prescription.Prescription,
	signature Signature) (Verification, error) {

	out := Verification{PrescriptionID: sheet.ID, Verdict: VerdictNotVerified, Signature: signature}

	// The clearance comes from the **signature's own columns** and nothing asks station 10.
	//
	// This is the whole of the fix, and the defect it replaces is worth writing down because it
	// looked correct: the canonical form covers the clearance, and an earlier version recomputed
	// it by calling `StandingClearance` again — "what stands now". A second CLEARED decision
	// recorded later, or a `decided_at` rewritten by a backfill or a projection replay, and the
	// bytes differ from the ones that were signed. A prescription nobody touched then fails
	// verification, and on the public page a stranger is told a genuine sheet may not be real.
	// An accusation produced by a join.
	//
	// Looking the review up by `signature.QAReviewID` would have fixed the *swap* and left the
	// rewrite: verification would still depend on a mutable row. Copying both values onto the
	// signature at signing is what makes this function read nothing that can change. Precedents:
	// CP80's captured price on the item, CP82's AI suggestion stored as offered.
	canonical, err := Canonicalise(signature.CanonicalVersion, Subject{
		Sheet: sheet, SignedAt: signature.SignedAt, SignedBy: signature.SignedBy,
		Clearance: Clearance{
			ReviewID: signature.QAReviewID, DecidedAt: signature.QAClearedAt,
		},
	})
	if errors.Is(err, ErrUnknownCanonicalVersion) {
		// Loud, and NOT_VERIFIED rather than an error to the public: a build that cannot
		// reproduce the form a signature names cannot say the prescription is genuine, and
		// saying so is the honest answer.
		out.ReasonEN = "This build cannot reproduce the canonical form this signature was made over."
		out.ReasonBN = "এই সংস্করণ স্বাক্ষরের মূল রূপটি পুনরায় তৈরি করতে পারছে না।"
		return out, nil
	}
	if err != nil {
		return Verification{}, err
	}
	out.RecomputedSHA256 = CanonicalDigest(canonical)

	publicKey, keyErr := hex.DecodeString(signature.PublicKey)
	value, sigErr := hex.DecodeString(signature.Value)
	if keyErr != nil || sigErr != nil {
		out.ReasonEN = "The stored signature is not readable."
		out.ReasonBN = "সংরক্ষিত স্বাক্ষরটি পড়া যাচ্ছে না।"
		return out, nil
	}

	if !VerifyDetached(publicKey, canonical, value) {
		out.ReasonEN = "This prescription is not what it was when it was signed."
		out.ReasonBN = "স্বাক্ষরের সময় এই ব্যবস্থাপত্র যেমন ছিল, এখন তেমন নেই।"
		return out, nil
	}
	out.Verdict = VerdictVerified
	return out, nil
}

// ---------------------------------------------------------------------------
// Readiness
// ---------------------------------------------------------------------------

// Readiness is the answer to "may this prescription be signed, and if not, which gate is shut".
//
// # Why this exists, and why it is not a convenience
//
// A screen that offers a sign control on a prescription the server will refuse is the CP92
// defect: a button handed to somebody, answering 409. The gates are three — the status machine,
// CP83's clearance, and the fact that there is no re-signing — and **the physician can read none
// of them.** `GET /v1/prescriptions/{id}/qa` is behind `qa.review`, which QA holds and PHYSICIAN
// does not (migration 00006), so the prescriber cannot ask station 10 whether his own sheet was
// cleared. Without this, the interface would have to guess, and the only honest guess is to draw
// the control always and let the physician discover the refusal.
//
// So the server answers the question it alone can answer. Nothing here is a new rule: [Sign]
// refuses on exactly these three conditions in exactly this order, and the database refuses
// underneath it in any case. This is that decision, read-only, computed one step early.
//
// # It is not an authorisation
//
// It says nothing about permissions, because the caller already got past `prescription.read` and
// its patient guard to reach it, and because `MaySign` true does not mean *this* reader may sign
// — it means the **prescription** is in a state a signature may be added to. The step-up and
// `prescription.sign` are the route's, and they are checked when somebody actually signs.
type Readiness struct {
	// MaySign is whether a signature would be accepted right now.
	MaySign bool `json:"may_sign"`
	// Signed is whether one has already been made. Separated from MaySign because "already
	// signed" and "not cleared" are the same `false` and must not read as the same state.
	Signed bool `json:"signed"`
	// Cleared is station 10's standing decision. The gate CP83 owns.
	Cleared bool `json:"cleared"`
	// Status is the prescription's status, so a screen can say "this is still a draft" rather
	// than "you may not sign this".
	Status string `json:"status"`

	// ReasonEN and ReasonBN name **which** gate is shut, in the physician's own words. Empty
	// when MaySign is true: a reason beside an offered control is a reason somebody reads as a
	// warning.
	ReasonEN string `json:"reason_en,omitempty"`
	ReasonBN string `json:"reason_bn,omitempty"`
}

// SigningReadiness computes it.
//
// The order of the checks is [Sign]'s order, deliberately: already-signed first, then the status
// machine, then the clearance. A prescription that is both signed and a draft is impossible, but
// a prescription that is uncleared *and* still a draft is ordinary, and the sentence a physician
// needs for it is "this has not been submitted yet" rather than "station 10 has not cleared it".
func (s *Service) SigningReadiness(ctx context.Context, facility, id uuid.UUID) (Readiness, error) {
	sheet, err := s.sheets.ByID(ctx, id, facility)
	if errors.Is(err, prescription.ErrNotFound) {
		return Readiness{}, ErrNotFound
	}
	if err != nil {
		return Readiness{}, err
	}
	out := Readiness{Status: string(sheet.Status)}

	if _, err := s.store.ByPrescription(ctx, facility, id); err == nil {
		out.Signed = true
		out.ReasonEN = "This prescription has already been signed. It cannot be signed again; " +
			"a signed prescription that was wrong is corrected."
		out.ReasonBN = "এই ব্যবস্থাপত্রে ইতিমধ্যেই স্বাক্ষর করা হয়েছে। আবার স্বাক্ষর করা যায় না; " +
			"ভুল থাকলে সংশোধনী ব্যবস্থাপত্র লিখতে হবে।"
		return out, nil
	} else if !errors.Is(err, ErrNotSigned) {
		return Readiness{}, err
	}

	if sheet.Status != prescription.StatusQAReview {
		out.ReasonEN = "A prescription is signed after it has been submitted and cleared. " +
			"This one is " + string(sheet.Status) + "."
		out.ReasonBN = "জমা দেওয়া ও ছাড়পত্র পাওয়ার পর ব্যবস্থাপত্রে স্বাক্ষর করা হয়। " +
			"এটির বর্তমান অবস্থা " + string(sheet.Status) + "।"
		return out, nil
	}

	// Station 10's standing decision, from the same reader [Sign] uses. A nil `Clearances` — a
	// process assembled without station 10 — answers "not cleared", which is the only safe
	// direction for a gate and the answer [Sign] would act on a moment later.
	_, cleared, err := s.clearance(ctx, facility, id)
	if err != nil {
		return Readiness{}, err
	}
	out.Cleared = cleared
	if !cleared {
		out.ReasonEN = "Station 10 has not cleared this prescription. It cannot be signed until " +
			"the quality review clears it."
		out.ReasonBN = "১০ নম্বর কেন্দ্র এই ব্যবস্থাপত্রে ছাড়পত্র দেয়নি। মান যাচাইয়ের ছাড়পত্র " +
			"না পাওয়া পর্যন্ত স্বাক্ষর করা যাবে না।"
		return out, nil
	}
	out.MaySign = true
	return out, nil
}
