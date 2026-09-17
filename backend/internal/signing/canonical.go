package signing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
)

// The canonical form (CP84, `docs/signing.md` §3).
//
// # What this file has to be, stated as properties rather than as intentions
//
//  1. **Deterministic.** The same prescription produces the same bytes on any platform, in any
//     Go version, under any locale, in any process, however many times it is asked. Not "we
//     were careful"; a property the tests can falsify.
//  2. **Versioned.** The version travels with the signature and verification picks the version
//     the signature names. A change to this file must not silently invalidate every historical
//     prescription — that is the risk the implementation plan names as this checkpoint's main
//     one, and versioning is the whole answer to it.
//  3. **Covering the clinical facts and nothing cosmetic.** What the prescription *is*: the
//     patient, the visit, the prescriber, every live line with its dose, frequency, duration,
//     route and captured price, and the QA clearance that permitted signing. Not the layout, not
//     the fonts, not the letterhead, not the language the screen happened to be in.
//
// # Why this is not JSON
//
// `encoding/json` is the obvious choice and it is the wrong one, for three reasons that each
// individually disqualify it.
//
//   - **Map ordering.** A `map[string]any` marshals with sorted keys today. That is a documented
//     property of the standard library and it is still a property of a library rather than of
//     this system, and the failure mode — a Go release that changed it, or one struct somewhere
//     holding a map — is that every prescription signed before the change stops verifying, with
//     no error that says why.
//   - **Float formatting.** JSON numbers go through `strconv.AppendFloat` with `-1` precision,
//     which emits the shortest representation that round-trips. That is a function of the float
//     bits, and a daily dose that arrived as `numeric(12,3)` from PostgreSQL and as a literal in
//     a test are not guaranteed to be the same bits.
//   - **Ambiguity.** `{"a":"b,c"}` and `{"a":"b","c":""}` are different, but the moment anything
//     concatenates, a value containing the delimiter is a forgery vector. Length-prefixing makes
//     that inexpressible rather than merely unlikely.
//
// So: an explicit, length-prefixed, tag-value encoding written field by field in a fixed order,
// with no map anywhere in the path and no formatting decision left to a library.
//
// # The shape of one field
//
//	name US length US value RS
//
// where US is 0x1f and RS is 0x1e, `length` is the decimal byte length of `value`, and an absent
// optional value has length `-1` and an empty value. The length prefix is what makes the encoding
// unambiguous: a value containing 0x1e cannot be made to look like a field boundary, because the
// reader (and any auditor writing an independent verifier) counts bytes rather than scanning for
// a separator.
//
// # Making the wrong call inexpressible
//
// There is no `func (c *canonical) raw(string)`. Every field goes through a typed method — text,
// id, integer, decimal, instant, flag — so that no call site can decide for itself how to format
// a number or a time. That is deliberate and it is the reason this file is longer than it needs
// to be: the way canonicalisation breaks is not that somebody writes a bad encoder, it is that
// somebody adds one field and formats it the way that was convenient at the time.
//
// # Times, and the microsecond
//
// PostgreSQL's `timestamptz` holds microseconds. A `time.Time` in Go holds nanoseconds. A value
// signed from memory and verified after a round trip through the database would therefore differ
// in the last three digits — and the signature would fail on a prescription nobody touched.
// Every instant is truncated to the microsecond here, once, so the two agree by construction
// rather than by the caller remembering to.
//
// # Numbers, and the third decimal
//
// `daily_dose`, `quantity` and the rest are `numeric(12,3)` in the database. They are formatted
// with exactly three decimals, fixed notation, never scientific — the precision the column
// holds, so a value cannot be signed at a precision the storage will not return.

// CanonicalVersion is the serialisation this build produces.
//
// Bumped when the covered set or the encoding changes in any way that would make a signature
// made under the old rules fail under the new ones. **The old version's code stays**, because a
// prescription signed under it must still verify: see [Canonicalise]'s switch, which is the
// mechanism and not a formality.
const CanonicalVersion = 1

const (
	// unitSeparator (0x1f) sits between a field's name, its length and its value.
	unitSeparator = 0x1f
	// recordSeparator (0x1e) ends a field.
	recordSeparator = 0x1e
	// absentLength is the length written for an optional value that is not present. It is
	// distinct from `0`, because "no duration was stated" and "a duration of nothing" are
	// different facts and a canonical form that conflated them would let one be changed into
	// the other without breaking the signature.
	absentLength = "-1"
)

// canonicalHeader names the format and the version in the bytes themselves.
//
// Belt and braces with the `canonical_version` column: a verifier handed loose bytes with no
// database around them can still tell what it is holding, and a v1 signature can never be
// checked against v2 bytes by accident because the first field of each differs.
const canonicalHeader = "DTHCMS-PRESCRIPTION-CANONICAL"

// Clearance is station 10's decision, as the canonical form covers it.
//
// It is covered because "this prescription was cleared before it was signed" is part of what the
// signature attests. A clearance that could be swapped afterwards for a different one would make
// the signature silent about the gate it passed through.
type Clearance struct {
	ReviewID  uuid.UUID
	DecidedAt time.Time
}

// Subject is everything the canonical form is computed from.
//
// A struct rather than a long argument list so that adding a covered fact is a field here and a
// line in [canonicaliseV1], both of which show up in a diff — and so that the coverage test can
// build a fully-populated one and assert the field list has not shrunk.
type Subject struct {
	// Sheet is the prescription as the read model holds it. Read back from the database rather
	// than held from the write, because the bytes that are signed must be the bytes that will
	// be read back at verification time — including PostgreSQL's rounding of every timestamp.
	Sheet prescription.Prescription

	// SignedAt and SignedBy are the signing act itself, which cannot come from the sheet: at
	// the moment the canonical form is computed the transition has not happened and the sheet's
	// `signed_at` is still null. At verification time they are read from the signature row,
	// which the same event wrote.
	SignedAt time.Time
	SignedBy uuid.UUID

	// Clearance is the QA decision that permitted signing. Zero is legal and is written as an
	// absent clearance, which is the state of a prescription signed in a facility whose gate
	// was satisfied by an override rather than a clearance — the override is itself recorded
	// against the review, so the zero case is narrow and is not a hole.
	Clearance Clearance
}

// Canonicalise produces the bytes a signature is made over, at the version named.
//
// **The switch is the mechanism the checkpoint's main risk is answered by**, and it is why this
// function takes a version rather than reading the constant. Signing always asks for
// [CanonicalVersion]; verification always asks for the version the signature recorded. When v2
// ships, v1's function stays exactly as it is and every prescription signed under it keeps
// verifying — which is the difference between a versioned format and a format with a version
// number on it.
func Canonicalise(version int, in Subject) ([]byte, error) {
	switch version {
	case 1:
		return canonicaliseV1(in), nil
	default:
		return nil, fmt.Errorf("%w: v%d", ErrUnknownCanonicalVersion, version)
	}
}

// CanonicalDigest is the SHA-256 of the canonical bytes, hex-encoded.
//
// Recorded beside the signature so that a failed verification can be told apart from a canonical
// form that has moved on: if the digest recomputes to what was stored and the signature still
// fails, the key is wrong; if the digest differs, the content changed.
func CanonicalDigest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// canonicaliseV1 is version 1, and the order of these lines is part of the format.
//
// Reordering two calls here changes every signature this build produces and invalidates every
// one it has produced. That is what [CanonicalVersion] is for; it is written down here because
// the reordering that does the damage is the innocent-looking one.
func canonicaliseV1(in Subject) []byte { return canonicalV1Writer(in).bytes() }

// canonicalV1Writer is v1's body, returning the writer rather than the bytes.
//
// One function and not two, so that [CanonicalFields] reports the names the code that produces
// the bytes actually wrote. A second list maintained beside this one would drift from it, and it
// would drift silently in the direction that matters — a field removed here and left in the list
// there reads as covered and is not.
func canonicalV1Writer(in Subject) *canonical {
	c := newCanonical(1)

	// ---- what this prescription is -------------------------------------
	c.id("prescription.id", in.Sheet.ID)
	c.id("prescription.facility", in.Sheet.FacilityID)
	c.id("prescription.patient", in.Sheet.PatientID)
	c.id("prescription.visit", in.Sheet.VisitID)
	c.instant("prescription.created_at", in.Sheet.CreatedAt)
	c.id("prescription.created_by", in.Sheet.CreatedBy)

	// The correction link. Covered because "this sheet supersedes that one, for this reason"
	// is a clinical fact about the prescription rather than a note about it: a correction
	// whose reason could be rewritten after signing would be a correction nobody could rely on.
	//
	// `status` is deliberately **not** covered: a signed prescription goes on to be PRINTED and
	// then DISPENSED, and a canonical form that included the status would break its own
	// signature the moment the paper came out of the printer.
	c.optionalID("prescription.corrects", in.Sheet.Corrects)
	c.text("prescription.correction_reason", in.Sheet.CorrectionReason)
	c.flag("prescription.corrects_dispensed_original", in.Sheet.CorrectsDispensedOriginal)
	c.optionalID("prescription.carried_forward_from", in.Sheet.CarriedForwardFrom)

	// ---- the signing act ------------------------------------------------
	c.instant("signature.signed_at", in.SignedAt)
	c.id("signature.signed_by", in.SignedBy)

	// ---- the gate it came through ---------------------------------------
	c.optionalID("clearance.review", nonZeroID(in.Clearance.ReviewID))
	c.optionalInstant("clearance.decided_at", nonZeroTime(in.Clearance.DecidedAt))

	// ---- the lines -------------------------------------------------------
	//
	// Live lines only, and **the count is written explicitly**. Without the count, a forger who
	// could delete the last item from the read model would produce bytes that are a prefix of
	// the signed ones — and a length-prefixed format truncated at a field boundary is still a
	// well-formed message. The count makes truncation as detectable as substitution.
	live := liveItemsInOrder(in.Sheet.Items)
	c.integer("items.count", int64(len(live)))
	for i, item := range live {
		c.item(i, item)
	}

	return c
}

// liveItemsInOrder is the sheet's lines, sorted so that the order in the slice cannot matter.
//
// By line number first, because that is what the sheet means, and by id as the tie-break —
// because two lines with the same number is a state the database permits (nothing constrains
// `line_no` to be unique) and a canonical form whose output depended on which of them the query
// returned first would be non-deterministic in exactly the case nobody tests.
func liveItemsInOrder(items []prescription.Item) []prescription.Item {
	live := make([]prescription.Item, 0, len(items))
	for _, item := range items {
		if item.Live() {
			live = append(live, item)
		}
	}
	sort.Slice(live, func(a, b int) bool {
		if live[a].LineNo != live[b].LineNo {
			return live[a].LineNo < live[b].LineNo
		}
		return live[a].ID.String() < live[b].ID.String()
	})
	return live
}

// item writes one line.
//
// The ordinal is the line's position **in this canonical form**, not its `line_no`: both are
// written, and they are different facts. `line_no` is what the sheet says; the ordinal is what
// makes each field name unique so that two lines cannot be swapped without changing the bytes.
func (c *canonical) item(ordinal int, in prescription.Item) {
	p := "item." + strconv.Itoa(ordinal) + "."

	c.id(p+"id", in.ID)
	c.integer(p+"line_no", int64(in.LineNo))
	c.optionalID(p+"product", in.ProductID)

	// The medicine as it was named on the day. These are copies taken at prescribing time, not
	// joins, and covering them is what makes "the pharmacy was told to dispense this" provable
	// rather than dependent on a formulary that may have been edited since.
	c.text(p+"label", in.Label)
	c.text(p+"generic", in.GenericName)
	c.text(p+"strength", in.Strength)
	c.text(p+"form", in.FormCode)

	// How it is taken. The whole clinical instruction, and the reason every one of these is
	// covered individually rather than as a joined sentence: a joined sentence is a rendering,
	// and a rendering is the thing this signature deliberately does not attest to.
	c.text(p+"dose", in.Dose)
	c.decimal(p+"daily_dose", in.DailyDose)
	c.text(p+"dose_unit", in.DoseUnit)
	c.text(p+"frequency", in.Frequency)
	c.optionalInteger(p+"duration_days", in.DurationDays)
	c.text(p+"route", in.Route)
	c.decimal(p+"quantity", in.Quantity)

	// **Both languages, always.** This is what makes `docs/signing.md` §3's promise true: the
	// Bangla sheet and the English sheet are two renderings of one fact, both instruction
	// strings are covered, and which one a printer chooses to show is not in here at all. So
	// re-rendering in the other language cannot break the signature, and editing either
	// instruction can.
	c.text(p+"instructions_en", in.InstructionsEN)
	c.text(p+"instructions_bn", in.InstructionsBN)

	// The price of the day, by value — what the patient was told to pay. Covered because §9
	// makes the captured price part of the prescription rather than a commercial note beside
	// it, and an unsigned price is a number anybody can move.
	if in.Price == nil {
		// A product with no price on the day is a real state and is not zero. Written as an
		// explicit absence so that "there was no price" and "the price was nothing" are
		// different bytes.
		c.absent(p + "price.amount_poisha")
		c.absent(p + "price.id")
		c.absent(p + "price.effective_from")
		c.absent(p + "price.verification")
		return
	}
	c.integer(p+"price.amount_poisha", in.Price.AmountPoisha)
	c.id(p+"price.id", in.Price.PriceID)
	c.text(p+"price.effective_from", in.Price.EffectiveFrom)
	c.text(p+"price.verification", in.Price.Verification)
}

// ---------------------------------------------------------------------------
// The writer
// ---------------------------------------------------------------------------

// canonical accumulates the bytes, and remembers the field names for the coverage test.
//
// `names` is not part of the output. It exists so that
// `TestTheCanonicalFormCoversEveryFieldItClaimsTo` can assert the exact list, which is the test
// that fails when somebody quietly drops a field — the defect that matters here, because a
// signature covering less than it claims still verifies and still says VERIFIED on a public page.
type canonical struct {
	buf   bytes.Buffer
	names []string
}

func newCanonical(version int) *canonical {
	c := &canonical{}
	c.text(canonicalHeader, "v"+strconv.Itoa(version))
	return c
}

func (c *canonical) bytes() []byte { return append([]byte(nil), c.buf.Bytes()...) }

// Fields is every field name written, in order. For the coverage test only.
func (c *canonical) fields() []string { return append([]string(nil), c.names...) }

// write is the single place a field reaches the buffer. Unexported and called only by the typed
// methods below; there is deliberately no exported way to write a pre-formatted value.
func (c *canonical) write(name, value string, present bool) {
	c.names = append(c.names, name)
	c.buf.WriteString(name)
	c.buf.WriteByte(unitSeparator)
	if !present {
		c.buf.WriteString(absentLength)
		c.buf.WriteByte(unitSeparator)
		c.buf.WriteByte(recordSeparator)
		return
	}
	c.buf.WriteString(strconv.Itoa(len(value)))
	c.buf.WriteByte(unitSeparator)
	c.buf.WriteString(value)
	c.buf.WriteByte(recordSeparator)
}

// text writes a string exactly as the database holds it.
//
// No trimming, no case folding, no Unicode normalisation. Every one of those would be a
// transformation applied at signing time and not at storage time, which means two byte
// sequences that the database distinguishes would sign identically — and the whole point is that
// a change the database can hold is a change the signature can see.
func (c *canonical) text(name, v string) { c.write(name, v, true) }

func (c *canonical) id(name string, v uuid.UUID) { c.write(name, v.String(), true) }

func (c *canonical) optionalID(name string, v *uuid.UUID) {
	if v == nil {
		c.write(name, "", false)
		return
	}
	c.write(name, v.String(), true)
}

func (c *canonical) integer(name string, v int64) {
	c.write(name, strconv.FormatInt(v, 10), true)
}

func (c *canonical) optionalInteger(name string, v *int) {
	if v == nil {
		c.write(name, "", false)
		return
	}
	c.write(name, strconv.FormatInt(int64(*v), 10), true)
}

// decimal writes a number at the precision the column holds.
//
// `'f'` and three decimals: fixed notation, never scientific, never the shortest round-trip
// representation. `numeric(12,3)` is what PostgreSQL returns, so 12.5 is `12.500` whichever side
// of the database it came from, and no float's bit pattern can change the bytes.
func (c *canonical) decimal(name string, v *float64) {
	if v == nil {
		c.write(name, "", false)
		return
	}
	c.write(name, strconv.FormatFloat(*v, 'f', 3, 64), true)
}

// instant writes a time in UTC at microsecond precision, in a fixed-width format.
//
// Three separate decisions, each of which has broken somebody's signature scheme:
//
//   - **UTC, explicitly.** `Format` renders in the value's own location, so a `time.Time` that
//     came back from a driver configured for Asia/Dhaka would render differently from the same
//     instant in UTC. `.UTC()` makes the location irrelevant rather than conventionally correct.
//   - **Microseconds, truncated.** PostgreSQL stores microseconds; Go holds nanoseconds. Without
//     this, a signature made in memory fails after a round trip through the database.
//   - **Fixed width.** `time.RFC3339Nano` drops trailing zeros, so the same instant renders with
//     a different number of digits depending on its value. A fixed layout cannot.
func (c *canonical) instant(name string, v time.Time) {
	c.write(name, formatInstant(v), true)
}

func (c *canonical) optionalInstant(name string, v *time.Time) {
	if v == nil {
		c.write(name, "", false)
		return
	}
	c.write(name, formatInstant(*v), true)
}

func formatInstant(v time.Time) string {
	return v.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}

func (c *canonical) flag(name string, v bool) {
	word := "false"
	if v {
		word = "true"
	}
	c.write(name, word, true)
}

// absent writes a field that has no value, so that the field still appears in the form.
//
// Used where a whole block is missing — a line with no captured price. Writing the names with no
// values, rather than writing nothing, is what stops a priced line and an unpriced line from
// being made to look alike by deleting the price row.
func (c *canonical) absent(name string) { c.write(name, "", false) }

// nonZeroID turns a zero uuid into an absence, so that "no clearance" and "a clearance whose id
// happens to be all zeroes" are not the same bytes.
func nonZeroID(v uuid.UUID) *uuid.UUID {
	if v == uuid.Nil {
		return nil
	}
	return &v
}

func nonZeroTime(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}

// CanonicalFields is the ordered list of field names a fully-populated subject writes.
//
// Exported for the coverage test, which pins the list. It is the cheapest possible defence
// against the one defect this checkpoint cannot tolerate: a canonical form that quietly covers
// less than it says it does still produces a valid signature, still verifies, and still puts
// VERIFIED on a public page for a prescription somebody edited.
func CanonicalFields(version int, in Subject) ([]string, error) {
	switch version {
	case 1:
		return canonicalV1Writer(in).fields(), nil
	default:
		return nil, fmt.Errorf("%w: v%d", ErrUnknownCanonicalVersion, version)
	}
}
