// Package signing is the medico-legal core of a prescription: a signature over a canonical
// serialisation of what the physician decided, and a way for a stranger holding the paper to
// check it (CP84, CP85, `docs/signing.md`).
//
// # One sentence everything here follows from
//
// **The signature proves nobody altered the prescription; it does not prove the database is
// honest, and it does not yet prove anything to a Bangladeshi court.**
//
// Those three clauses are three different guarantees and this package is careful about which it
// offers. CP80 made a signed prescription unchangeable — grants, two freeze triggers, a
// transition matrix in a table. Every one of those is a promise made by the system that holds
// the data, and a reader who does not trust that system cannot check any of them. What a
// signature adds is the part that survives distrust: recompute the canonical bytes, verify them
// against the stored signature with a public key, and a single altered character anywhere the
// canonical form covers makes the verification fail. That is checkable by somebody with no
// account and no reason to believe us.
//
// The third clause is D-04 and it is counsel's, not engineering's. `docs/signing.md` §1 states
// the honest position in the meantime and this package does not overstate it anywhere.
//
// # What is signed, and what is deliberately not
//
// A **canonical, versioned serialisation of the clinical facts** — never the rendered image.
// See canonical.go, which argues the encoding and lists what it covers. Two consequences worth
// repeating here because they surprise people:
//
//   - **Re-rendering the prescription in the other language does not break the signature.** The
//     Bangla sheet and the English sheet are two renderings of one clinical fact, and the
//     canonical form covers both instruction strings and neither rendering.
//   - **The signature does not attest to how the paper looked.** If that is ever needed it is a
//     second signature over the rendered artefact, and it is CP89's.
//
// # The signature image is not the signature
//
// §7.3 wants the physician's handwritten signature visible on the paper. That image is a
// picture. It has no cryptographic role whatsoever, it is trivially forged, and nothing in this
// package touches it — [SignatureImage] exists precisely so that the two cannot be confused in
// code, and it is a type with no bytes and no verification method on it. The printed sheet
// carries both; the QR is what lets a stranger check the one that matters.
//
// # No PHI leaves this package, and the public surface most of all
//
// Nothing here logs, traces or counts a drug, a dose, a diagnosis, a patient or a name. The
// public verification endpoint is the system's only unauthenticated surface besides login: what
// it returns is enumerated in public.go as an allowlist, and a test asserts the response carries
// those keys and no others.
//
// # The key is never in an error, a log or a span
//
// The private half exists inside a [Signer] and nowhere else. No type in this package has a
// field that could hold one, no error message formats one, and [LocalSigner.String] deliberately
// does not exist so that a `%v` of one cannot print it.
package signing

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Algorithm is the only signature algorithm this system produces.
//
// Ed25519, and named as a constant rather than a configuration value. A prescription signed with
// an algorithm chosen by a setting would be a prescription whose strength depends on a file
// nobody reads, and the alternatives worth having (RSA, ECDSA) are worse on every axis that
// matters here: bigger signatures on a QR-sized budget, more parameters to get wrong, and
// malleability. The column has a CHECK constraint saying the same thing.
const Algorithm = "Ed25519"

// SignerKind names which implementation of the [Signer] seam produced a signature.
//
// It is recorded on every signature, and `docs/signing.md` §2 is why: there is no key management
// service yet, the local signer's key is a file, and if the pilot ever runs before CP03 the
// clinic must be able to say **exactly** which prescriptions carry the weaker guarantee — by
// query, not by inference from a date or the shape of a key id.
type SignerKind string

const (
	// SignerLocal holds an Ed25519 key in this deployment's configuration.
	//
	// **It does not satisfy "the signing key is non-exportable" and cannot.** The key is a
	// file; anybody who can read the configuration can copy it; a copied key signs. It is
	// refused outside local, test and dev by a check the service makes about itself at boot —
	// see [NewSigner] — rather than by a flag somebody can set.
	SignerLocal SignerKind = "LOCAL"
	// SignerManaged is a key management service that signs on request and never releases the
	// private half. CP03. The constant exists now so that the vocabulary does not change when
	// the implementation arrives.
	SignerManaged SignerKind = "MANAGED"
)

// DeviceAssurance is how the signing session's device was established (ADR-0021).
//
// Recorded on the signature, **not required** of it. `docs/signing.md` §4 takes that decision
// against ADR-0021's instinct and gives the reasoning: the consultant signs at his desk on a
// browser, a rule whose effect is that prescriptions stop being signed has not made them safer,
// and the assurance here comes from the second factor he holds and a key the application cannot
// read rather than from the machine. Recording it is what keeps the option open — the rule can
// tighten later with the evidence already collected.
type DeviceAssurance string

const (
	// AssuranceProven — the server verified an Ed25519 signature from a key in the device's
	// secure storage (CP18, ADR-0013).
	AssuranceProven DeviceAssurance = "PROVEN"
	// AssuranceNamed — somebody typed the workstation code printed on the monitor. A claim,
	// corroborated by the authenticated person beside it (ADR-0021).
	AssuranceNamed DeviceAssurance = "NAMED"
	// AssuranceNone — the session named no device at all.
	AssuranceNone DeviceAssurance = "NONE"
)

// assuranceOf maps the transport's word onto this package's vocabulary, and answers
// [AssuranceNone] for anything it does not recognise.
//
// Fail-closed rather than fail-open: an unrecognised assurance recorded as PROVEN would be a
// signature claiming a strength nobody checked, and the whole value of this column is that it
// can be trusted when somebody comes back to it.
func assuranceOf(word string) DeviceAssurance {
	switch DeviceAssurance(word) {
	case AssuranceProven:
		return AssuranceProven
	case AssuranceNamed:
		return AssuranceNamed
	default:
		return AssuranceNone
	}
}

// Signature is what was recorded when a prescription was signed.
//
// The private key is not here, is not reachable from here, and no field on this struct could
// hold one. The public key is, by value: verification must not depend on a key register's
// current shape, because a key rotated or retired next year must not make a prescription signed
// this morning unverifiable.
type Signature struct {
	PrescriptionID uuid.UUID `json:"prescription_id"`
	FacilityID     uuid.UUID `json:"facility_id"`

	// CanonicalVersion is the serialisation the signature was made over. It travels with the
	// signature rather than being assumed, which is the whole answer to this checkpoint's named
	// risk: verification picks the version the signature names, so a change to the canonical
	// form cannot silently invalidate every historical prescription.
	CanonicalVersion int `json:"canonical_version"`
	// CanonicalSHA256 is the digest of those bytes, hex. Kept so that "the canonical form
	// changed" and "the signature is wrong" are two answerable questions rather than one
	// indistinguishable failure.
	CanonicalSHA256 string `json:"canonical_sha256"`

	Algorithm  string     `json:"algorithm"`
	SignerKind SignerKind `json:"signer_kind"`
	KeyID      string     `json:"key_id"`
	// PublicKey is 32 bytes, hex. Public by definition; what makes the signature evidence is
	// that the private half was never in this database.
	PublicKey string `json:"public_key"`
	// Value is the 64-byte Ed25519 signature, hex.
	Value string `json:"signature"`

	SignedAt time.Time `json:"signed_at"`
	SignedBy uuid.UUID `json:"signed_by"`
	// SignedByCode, SignedByNameEN and SignedByNameBN name the physician. "Who signed this" is
	// a question about a person and a uuid answers a different one.
	SignedByCode   string `json:"signed_by_code,omitempty"`
	SignedByNameEN string `json:"signed_by_name_en,omitempty"`
	SignedByNameBN string `json:"signed_by_name_bn,omitempty"`

	DeviceAssurance DeviceAssurance `json:"device_assurance"`

	// QAReviewID and QAClearedAt are station 10's decision that permitted this signature,
	// **copied onto the signature rather than joined to**.
	//
	// The canonical form covers the clearance, so these two values are what [Service.Verify]
	// recomputes the bytes from. It asks station 10 nothing. A verifier that looked the
	// clearance up again would be verifying against whatever stands *now* — a second CLEARED
	// decision recorded later, or a `decided_at` rewritten by a backfill or a projection replay
	// — and an untouched prescription would fail verification and be reported as altered. That
	// is an accusation produced by a join.
	//
	// The precedents are next door and the reasoning is theirs: CP80 copies the captured price
	// onto the prescription item instead of joining to the formulary, so what the patient was
	// quoted stays what the patient was quoted; CP82 stores an AI suggestion exactly as it was
	// offered and never mutates it, so "what was the physician shown" has an answer. A record
	// whose meaning depends on a row somebody can still edit is not a record.
	//
	// Not pointers. [Sign] refuses a prescription with no standing clearance, CP83's trigger
	// refuses the transition underneath it, and the columns are NOT NULL — so the invariant is
	// expressed where it is enforced rather than as a nil check that cannot fire.
	QAReviewID  uuid.UUID `json:"qa_review_id"`
	QAClearedAt time.Time `json:"qa_cleared_at"`

	// NonExportableKey is what the signer kind guarantees, restated on the record the physician
	// and the auditor read. False for [SignerLocal], honestly.
	NonExportableKey bool `json:"non_exportable_key"`
}

// SignatureImage is the physician's handwritten signature as a picture (§7.3).
//
// # Why this type exists with no bytes in it
//
// `docs/signing.md` §6: the image and the signature must never be conflated, in the code or on
// the screen. The way to make that stick in code is not a comment — it is to give the picture a
// type that has no signature bytes, no public key, no [Verify] and nothing that returns a
// boolean about authenticity, so that no call site can accidentally treat one as the other.
//
// CP84 does not store or render the image; CP89 owns the paper. What is here is the name and the
// refusal, so that whoever adds it later adds it to a type that cannot pretend.
type SignatureImage struct {
	// PresentOnFile says whether the clinic holds a picture for this physician. Nothing else,
	// because nothing else is decidable without the image store CP89 will build.
	PresentOnFile bool `json:"present_on_file"`
	// CaveatEN and CaveatBN are what a screen must say beside the picture. A pasted image is
	// trivially forged and a reader who is not told that will believe it.
	CaveatEN string `json:"caveat_en"`
	CaveatBN string `json:"caveat_bn"`
}

// TheImageIsNotTheSignature is the sentence every screen that draws both must carry.
//
// A function rather than two constants so that a caller has to ask for the pair and cannot show
// the picture with no caveat by leaving one out.
func TheImageIsNotTheSignature() SignatureImage {
	return SignatureImage{
		PresentOnFile: false,
		CaveatEN: "This handwritten signature is a picture for readability. What proves this " +
			"prescription has not been altered is the QR code, not the image.",
		CaveatBN: "হাতে লেখা এই স্বাক্ষরটি কেবল পড়ার সুবিধার জন্য একটি ছবি। এই ব্যবস্থাপত্র " +
			"বদলানো হয়নি — তা প্রমাণ করে কিউআর কোড, ছবিটি নয়।",
	}
}

// Verdict is the answer to "is this prescription what it was when it was signed".
type Verdict string

const (
	// VerdictVerified — the canonical bytes recomputed from the stored prescription verify
	// against the stored signature.
	VerdictVerified Verdict = "VERIFIED"
	// VerdictNotVerified — they do not, or there is nothing to verify.
	//
	// **One word for every failure**, deliberately. "The dose changed" and "no such token" are
	// the same answer to the outside world: a verifier that said which field failed would be
	// telling somebody forging a prescription what to fix, and the person holding the paper
	// needs the verdict rather than the diagnosis.
	VerdictNotVerified Verdict = "NOT_VERIFIED"
)

// Errors this package returns.
var (
	// ErrNotSigned — the prescription carries no signature.
	ErrNotSigned = errors.New("this prescription has not been signed")
	// ErrAlreadySigned — it has one already. There is no re-signing; a signed prescription
	// that was wrong is corrected (`docs/signing.md` §7).
	ErrAlreadySigned = errors.New("this prescription has already been signed")
	// ErrNotCleared — station 10 has not cleared it. The database refuses the transition as
	// well; this is the sentence a physician reads.
	ErrNotCleared = errors.New("this prescription has not been cleared by QA")
	// ErrNotFound — no such prescription in this facility. Mapped to the same HTTP answer as a
	// refused reach, so a 403 does not reveal whether a resource exists.
	ErrNotFound = errors.New("prescription not found")
	// ErrUnknownCanonicalVersion — a signature names a serialisation this build does not
	// implement. Loud rather than silently verifying against the current one, which would
	// report a tampered prescription as genuine whenever the canonical form had moved on.
	ErrUnknownCanonicalVersion = errors.New("the signature names a canonical form this build does not implement")
	// ErrSignerRefused — the configured signer may not be used in this environment. See
	// [NewSigner]: this is the boot-time refusal, not a runtime one.
	ErrSignerRefused = errors.New("the configured signer is refused in this environment")
	// ErrManagedSignerUnavailable — the managed signer is CP03's and is not built.
	ErrManagedSignerUnavailable = errors.New("the managed signing service arrives with CP03")
)
