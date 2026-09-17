package signing_test

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/prescription"
	"github.com/AmlanWTK/DTHCMS/backend/internal/signing"
)

// The canonical form's properties (CP84, docs/signing.md §3).
//
// # What these tests are actually for
//
// The defect this checkpoint cannot tolerate is **a signature that covers less than it claims
// to**. It is silent: the signature is valid, verification passes, and the public page says
// VERIFIED for a prescription somebody edited. No error, no log line, nothing to notice. So the
// tests below are not "does canonicalisation work" — they are a set of falsifiable claims about
// what it covers and about what could make it non-deterministic:
//
//   - every field it says it covers is in the output, by name, pinned;
//   - changing any one of them changes the bytes;
//   - the bytes do not depend on map iteration, on the process, on the machine's time zone, or
//     on which language a screen happens to be in.
//
// The mutation test in this checkpoint's report is run against the third of these: remove one
// field from `canonicaliseV1` and `TestEveryCoveredFieldChangesTheBytes` and
// `TestTheCanonicalFormCoversEveryFieldItClaimsTo` both fail, by name.

// --- a fully populated subject -------------------------------------------
//
// Rahima Begum of Boalmari, 61, eleven years of type 2 diabetes, on metformin and now also
// empagliflozin. The same fixture as station 10's tests and the screenshots, because a fixture
// whose name is a placeholder is a fixture nobody notices is wrong.

func faridpurSubject() signing.Subject {
	facility := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	sheet := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	patient := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	visit := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	prescriber := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	review := uuid.MustParse("66666666-6666-4666-8666-666666666666")
	corrects := uuid.MustParse("77777777-7777-4777-8777-777777777777")
	carried := uuid.MustParse("88888888-8888-4888-8888-888888888888")

	written := time.Date(2026, 9, 14, 9, 14, 21, 145000000, time.UTC)
	dose := 1000.0
	quantity := 60.0
	duration := 30

	return signing.Subject{
		Sheet: prescription.Prescription{
			ID: sheet, FacilityID: facility, PatientID: patient, VisitID: visit,
			Status:    prescription.StatusQAReview,
			CreatedAt: written, CreatedBy: prescriber, CreatedRole: "PHYSICIAN",
			Corrects: &corrects, CorrectionReason: "The evening metformin dose was doubled in error.",
			CorrectsDispensedOriginal: true,
			CarriedForwardFrom:        &carried,
			Items: []prescription.Item{
				{
					ID: uuid.MustParse("99999999-9999-4999-8999-999999999999"), LineNo: 1,
					ProductID: &carried,
					Label:     "Comet 500", GenericName: "Metformin Hydrochloride",
					Strength: "500 mg", FormCode: "TAB",
					Dose: "1 tablet", DailyDose: &dose, DoseUnit: "mg",
					Frequency: "twice daily", DurationDays: &duration, Route: "oral",
					Quantity:       &quantity,
					InstructionsEN: "After food.", InstructionsBN: "খাবারের পরে।",
					Price: &prescription.CapturedPrice{
						AmountPoisha: 425, AmountBDT: "4.25",
						PriceID:       uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
						EffectiveFrom: "2026-07-01", Verification: "VERIFIED",
						CapturedAt: written,
					},
					RecordedAt: written, RecordedBy: prescriber,
				},
				{
					// A second line with **no price**, which is a real state and not zero, and
					// a removed third line below which must not appear in the form at all.
					ID: uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), LineNo: 2,
					Label: "Jardin 10", GenericName: "Empagliflozin",
					Strength: "10 mg", FormCode: "TAB",
					Dose: "1 tablet", Frequency: "once daily", Route: "oral",
					InstructionsEN: "In the morning, with water.",
					InstructionsBN: "সকালে, পানি দিয়ে।",
					RecordedAt:     written, RecordedBy: prescriber,
				},
			},
		},
		SignedAt: time.Date(2026, 9, 14, 10, 2, 3, 456789000, time.UTC),
		SignedBy: prescriber,
		Clearance: signing.Clearance{
			ReviewID:  review,
			DecidedAt: time.Date(2026, 9, 14, 9, 58, 0, 0, time.UTC),
		},
	}
}

func canonicalOf(t *testing.T, in signing.Subject) []byte {
	t.Helper()
	out, err := signing.Canonicalise(signing.CanonicalVersion, in)
	if err != nil {
		t.Fatalf("canonicalising: %v", err)
	}
	return out
}

// --- the covered set, pinned ---------------------------------------------

// wholeCoveredSet is every field name version 1 writes, in order.
//
// **Pinned deliberately.** A test that merely asserted "the list is non-empty" would pass with
// one field in it. This one fails the moment anything is added, removed or reordered — and all
// three are changes that must be accompanied by a version bump, because all three change every
// signature this build produces.
var wholeCoveredSet = []string{
	"DTHCMS-PRESCRIPTION-CANONICAL",
	"prescription.id",
	"prescription.facility",
	"prescription.patient",
	"prescription.visit",
	"prescription.created_at",
	"prescription.created_by",
	"prescription.corrects",
	"prescription.correction_reason",
	"prescription.corrects_dispensed_original",
	"prescription.carried_forward_from",
	"signature.signed_at",
	"signature.signed_by",
	"clearance.review",
	"clearance.decided_at",
	"items.count",
	"item.0.id", "item.0.line_no", "item.0.product", "item.0.label", "item.0.generic",
	"item.0.strength", "item.0.form", "item.0.dose", "item.0.daily_dose", "item.0.dose_unit",
	"item.0.frequency", "item.0.duration_days", "item.0.route", "item.0.quantity",
	"item.0.instructions_en", "item.0.instructions_bn",
	"item.0.price.amount_poisha", "item.0.price.id", "item.0.price.effective_from",
	"item.0.price.verification",
	"item.1.id", "item.1.line_no", "item.1.product", "item.1.label", "item.1.generic",
	"item.1.strength", "item.1.form", "item.1.dose", "item.1.daily_dose", "item.1.dose_unit",
	"item.1.frequency", "item.1.duration_days", "item.1.route", "item.1.quantity",
	"item.1.instructions_en", "item.1.instructions_bn",
	"item.1.price.amount_poisha", "item.1.price.id", "item.1.price.effective_from",
	"item.1.price.verification",
}

func TestTheCanonicalFormCoversEveryFieldItClaimsTo(t *testing.T) {
	got, err := signing.CanonicalFields(signing.CanonicalVersion, faridpurSubject())
	if err != nil {
		t.Fatalf("listing the covered fields: %v", err)
	}
	if len(got) != len(wholeCoveredSet) {
		t.Fatalf("the canonical form writes %d fields and this test pins %d\n  got:  %v\n  want: %v",
			len(got), len(wholeCoveredSet), got, wholeCoveredSet)
	}
	for i := range got {
		if got[i] != wholeCoveredSet[i] {
			t.Errorf("field %d is %q, pinned as %q — a field was added, removed or reordered, "+
				"and every one of those changes every signature this build produces. "+
				"Bump signing.CanonicalVersion and keep v1's function.", i, got[i], wholeCoveredSet[i])
		}
	}
}

// --- determinism ----------------------------------------------------------

// TestTheCanonicalFormIsIdenticalAcrossManyRuns is the map-ordering test.
//
// Five hundred iterations in one process. Go randomises map iteration order **per range
// statement**, not per process, so a single map anywhere in the canonicalisation path would
// produce different bytes across iterations with overwhelming probability — which is precisely
// what a single comparison of two calls would miss.
func TestTheCanonicalFormIsIdenticalAcrossManyRuns(t *testing.T) {
	first := canonicalOf(t, faridpurSubject())
	for i := 0; i < 500; i++ {
		again := canonicalOf(t, faridpurSubject())
		if !bytes.Equal(first, again) {
			t.Fatalf("iteration %d produced different bytes; something in the canonical path "+
				"iterates a map, and every signature this system has made is at risk", i)
		}
	}
}

// TestTheOrderTheItemsArriveInDoesNotMatter is the other half of the same worry.
//
// The read model returns items in whatever order its query produced; a canonical form that
// depended on that would be one `ORDER BY` away from invalidating everything.
func TestTheOrderTheItemsArriveInDoesNotMatter(t *testing.T) {
	forwards := faridpurSubject()
	backwards := faridpurSubject()
	items := backwards.Sheet.Items
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	if !bytes.Equal(canonicalOf(t, forwards), canonicalOf(t, backwards)) {
		t.Fatal("reversing the item slice changed the canonical bytes; the form depends on the " +
			"order the query happened to return")
	}
}

// TestTheMachinesTimeZoneDoesNotChangeTheBytes is the locale test.
//
// Go has no locale in the C sense, and the thing that plays its part here is `time.Local`: a
// `time.Time` formats in its own location, so a driver, a container or a developer's laptop set
// to Asia/Dhaka would render the same instant differently. This sets the process's local zone
// to Dhaka, builds the subject there, and requires the bytes to be identical to the UTC ones.
//
// It also changes `time.Local` for the whole process, which is why it does not run in parallel.
func TestTheMachinesTimeZoneDoesNotChangeTheBytes(t *testing.T) {
	inUTC := canonicalOf(t, faridpurSubject())

	dhaka, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		// A container with no zoneinfo. Fall back to a fixed offset, which exercises the same
		// property: a non-UTC location on the value.
		dhaka = time.FixedZone("Asia/Dhaka", 6*60*60)
	}
	previous := time.Local
	time.Local = dhaka
	t.Cleanup(func() { time.Local = previous })
	t.Setenv("TZ", "Asia/Dhaka")

	shifted := faridpurSubject()
	shifted.Sheet.CreatedAt = shifted.Sheet.CreatedAt.In(dhaka)
	shifted.SignedAt = shifted.SignedAt.In(dhaka)
	shifted.Clearance.DecidedAt = shifted.Clearance.DecidedAt.In(dhaka)
	for i := range shifted.Sheet.Items {
		shifted.Sheet.Items[i].RecordedAt = shifted.Sheet.Items[i].RecordedAt.In(dhaka)
	}

	if !bytes.Equal(inUTC, canonicalOf(t, shifted)) {
		t.Fatalf("the same instants rendered in Asia/Dhaka produced different bytes; the "+
			"canonical form depends on the machine's time zone (TZ=%s)", os.Getenv("TZ"))
	}
}

// TestNanosecondPrecisionDoesNotSurviveIntoTheSignature is the round-trip test.
//
// PostgreSQL holds microseconds. A signature made over nanoseconds would fail the moment the
// prescription was read back, on a sheet nobody touched — the worst possible failure, because it
// looks exactly like tampering.
func TestNanosecondPrecisionDoesNotSurviveIntoTheSignature(t *testing.T) {
	inMemory := faridpurSubject()
	inMemory.SignedAt = time.Date(2026, 9, 14, 10, 2, 3, 456789123, time.UTC)

	readBack := faridpurSubject()
	readBack.SignedAt = time.Date(2026, 9, 14, 10, 2, 3, 456789000, time.UTC)

	if !bytes.Equal(canonicalOf(t, inMemory), canonicalOf(t, readBack)) {
		t.Fatal("a nanosecond-precision instant and its microsecond-rounded self canonicalise " +
			"differently; every signature would fail after a round trip through PostgreSQL")
	}
}

// TestRenderingInTheOtherLanguageDoesNotBreakTheSignature is docs/signing.md §3's promise.
//
// The Bangla sheet and the English sheet are two renderings of one clinical fact. What the
// canonical form covers is **both instruction strings**; which one a printer shows is not in the
// form at all. So there is nothing a language switch can change — and the test states that as a
// property by checking that the form contains both strings and that no field name mentions a
// language choice.
func TestRenderingInTheOtherLanguageDoesNotBreakTheSignature(t *testing.T) {
	subject := faridpurSubject()
	form := string(canonicalOf(t, subject))

	for _, want := range []string{"After food.", "খাবারের পরে।", "In the morning, with water.", "সকালে, পানি দিয়ে।"} {
		if !strings.Contains(form, want) {
			t.Errorf("the canonical form does not cover %q; one language's instruction is "+
				"unsigned and could be edited without breaking the signature", want)
		}
	}

	fields, err := signing.CanonicalFields(signing.CanonicalVersion, subject)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range fields {
		switch name {
		case "language", "locale", "rendered_language", "print.language":
			t.Errorf("the canonical form covers %q; a prescription re-rendered in the other "+
				"language would stop verifying, which docs/signing.md §3 says must not happen", name)
		}
	}
}

// TestARemovedLineIsNotInTheCanonicalForm — a line taken off a draft is not on the sheet, and
// signing it would attest to a medicine the patient was not prescribed.
func TestARemovedLineIsNotInTheCanonicalForm(t *testing.T) {
	withRemoved := faridpurSubject()
	removedAt := time.Date(2026, 9, 14, 9, 30, 0, 0, time.UTC)
	remover := uuid.New()
	withRemoved.Sheet.Items = append(withRemoved.Sheet.Items, prescription.Item{
		ID: uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc"), LineNo: 3,
		Label: "Glimepiride 2", Dose: "1 tablet", Frequency: "once daily",
		RemovedAt: &removedAt, RemovedBy: &remover, RemovedReason: "Hypoglycaemia risk.",
	})
	if !bytes.Equal(canonicalOf(t, faridpurSubject()), canonicalOf(t, withRemoved)) {
		t.Fatal("a removed line changed the canonical form; the signature would attest to a " +
			"medicine that is not on the sheet")
	}
	if strings.Contains(string(canonicalOf(t, withRemoved)), "Glimepiride") {
		t.Fatal("a removed line's medicine appears in the canonical form")
	}
}

// --- the tamper matrix ----------------------------------------------------

// TestEveryCoveredFieldChangesTheBytes is the mutation-resistance test.
//
// One case per covered fact, each mutating exactly one field of a fully-populated subject and
// requiring the bytes to change. **This is the test that fails when somebody drops a field from
// the canonical form**, which is the defect that matters: a signature covering less than it
// claims still verifies, and still says VERIFIED on a public page for a prescription somebody
// edited.
//
// It works in memory rather than against the database on purpose — it is exhaustive and fast, so
// nobody is tempted to trim it. The database half, which proves the same thing against a real
// `UPDATE` through a disabled trigger, is `TestAlteringAStoredFieldBreaksVerification` in
// cp84_db_test.go.
func TestEveryCoveredFieldChangesTheBytes(t *testing.T) {
	base := canonicalOf(t, faridpurSubject())

	other := uuid.MustParse("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	otherTime := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	otherFloat := 999.0
	otherInt := 7

	cases := []struct {
		field  string
		change func(*signing.Subject)
	}{
		{"prescription.id", func(s *signing.Subject) { s.Sheet.ID = other }},
		{"prescription.facility", func(s *signing.Subject) { s.Sheet.FacilityID = other }},
		{"prescription.patient", func(s *signing.Subject) { s.Sheet.PatientID = other }},
		{"prescription.visit", func(s *signing.Subject) { s.Sheet.VisitID = other }},
		{"prescription.created_at", func(s *signing.Subject) { s.Sheet.CreatedAt = otherTime }},
		{"prescription.created_by", func(s *signing.Subject) { s.Sheet.CreatedBy = other }},
		{"prescription.corrects", func(s *signing.Subject) { s.Sheet.Corrects = &other }},
		{"prescription.corrects (to absent)", func(s *signing.Subject) { s.Sheet.Corrects = nil }},
		{"prescription.correction_reason", func(s *signing.Subject) { s.Sheet.CorrectionReason = "Something else." }},
		{"prescription.corrects_dispensed_original", func(s *signing.Subject) { s.Sheet.CorrectsDispensedOriginal = false }},
		{"prescription.carried_forward_from", func(s *signing.Subject) { s.Sheet.CarriedForwardFrom = &other }},
		{"signature.signed_at", func(s *signing.Subject) { s.SignedAt = otherTime }},
		{"signature.signed_by", func(s *signing.Subject) { s.SignedBy = other }},
		{"clearance.review", func(s *signing.Subject) { s.Clearance.ReviewID = other }},
		{"clearance.decided_at", func(s *signing.Subject) { s.Clearance.DecidedAt = otherTime }},
		{"items.count", func(s *signing.Subject) { s.Sheet.Items = s.Sheet.Items[:1] }},

		{"item.0.id", func(s *signing.Subject) { s.Sheet.Items[0].ID = other }},
		{"item.0.line_no", func(s *signing.Subject) { s.Sheet.Items[0].LineNo = 9 }},
		{"item.0.product", func(s *signing.Subject) { s.Sheet.Items[0].ProductID = &other }},
		{"item.0.label", func(s *signing.Subject) { s.Sheet.Items[0].Label = "Comet 850" }},
		{"item.0.generic", func(s *signing.Subject) { s.Sheet.Items[0].GenericName = "Gliclazide" }},
		{"item.0.strength", func(s *signing.Subject) { s.Sheet.Items[0].Strength = "850 mg" }},
		{"item.0.form", func(s *signing.Subject) { s.Sheet.Items[0].FormCode = "CAP" }},
		{"item.0.dose", func(s *signing.Subject) { s.Sheet.Items[0].Dose = "2 tablets" }},
		{"item.0.daily_dose", func(s *signing.Subject) { s.Sheet.Items[0].DailyDose = &otherFloat }},
		{"item.0.dose_unit", func(s *signing.Subject) { s.Sheet.Items[0].DoseUnit = "g" }},
		{"item.0.frequency", func(s *signing.Subject) { s.Sheet.Items[0].Frequency = "three times daily" }},
		{"item.0.duration_days", func(s *signing.Subject) { s.Sheet.Items[0].DurationDays = &otherInt }},
		{"item.0.route", func(s *signing.Subject) { s.Sheet.Items[0].Route = "sublingual" }},
		{"item.0.quantity", func(s *signing.Subject) { s.Sheet.Items[0].Quantity = &otherFloat }},
		{"item.0.instructions_en", func(s *signing.Subject) { s.Sheet.Items[0].InstructionsEN = "Before food." }},
		{"item.0.instructions_bn", func(s *signing.Subject) { s.Sheet.Items[0].InstructionsBN = "খাবারের আগে।" }},
		{"item.0.price.amount_poisha", func(s *signing.Subject) { s.Sheet.Items[0].Price.AmountPoisha = 9900 }},
		{"item.0.price.id", func(s *signing.Subject) { s.Sheet.Items[0].Price.PriceID = other }},
		{"item.0.price.effective_from", func(s *signing.Subject) { s.Sheet.Items[0].Price.EffectiveFrom = "2026-01-01" }},
		{"item.0.price.verification", func(s *signing.Subject) { s.Sheet.Items[0].Price.Verification = "PROVISIONAL" }},
		{"item.0.price (removed entirely)", func(s *signing.Subject) { s.Sheet.Items[0].Price = nil }},

		{"item.1.label", func(s *signing.Subject) { s.Sheet.Items[1].Label = "Jardin 25" }},
		{"item.1.instructions_bn", func(s *signing.Subject) { s.Sheet.Items[1].InstructionsBN = "রাতে।" }},
		{"item.1.price (given one it did not have)", func(s *signing.Subject) {
			s.Sheet.Items[1].Price = &prescription.CapturedPrice{
				AmountPoisha: 1, AmountBDT: "0.01", PriceID: other,
				EffectiveFrom: "2026-07-01", Verification: "VERIFIED",
			}
		}},
		{"the two lines swapped", func(s *signing.Subject) {
			s.Sheet.Items[0].LineNo, s.Sheet.Items[1].LineNo = 2, 1
		}},
	}

	for _, one := range cases {
		t.Run(one.field, func(t *testing.T) {
			altered := faridpurSubject()
			one.change(&altered)
			if bytes.Equal(base, canonicalOf(t, altered)) {
				t.Fatalf("changing %s did not change the canonical bytes: this field is NOT "+
					"covered by the signature, and it can be edited in the database without "+
					"verification failing", one.field)
			}
		})
	}
}

// TestAnUnknownCanonicalVersionIsRefusedRatherThanGuessed.
//
// A build that cannot reproduce the form a signature names must say so. Silently falling back to
// the current version would report every historical prescription as tampered — or, on the day
// two versions happened to agree for one sheet, report a tampered one as genuine.
func TestAnUnknownCanonicalVersionIsRefusedRatherThanGuessed(t *testing.T) {
	if _, err := signing.Canonicalise(99, faridpurSubject()); err == nil {
		t.Fatal("canonicalising at an unimplemented version succeeded")
	}
	if _, err := signing.Canonicalise(0, faridpurSubject()); err == nil {
		t.Fatal("canonicalising at version 0 succeeded")
	}
}

// TestTheCanonicalDigestIsStable pins the digest of the fixture above.
//
// A golden value, and the bluntest instrument here: **any** change to the encoding, the field
// order, the number formatting or the time layout changes it. It is deliberately a single
// constant somebody has to update on purpose, in the same commit as the version bump that
// legitimises the change.
func TestTheCanonicalDigestIsStable(t *testing.T) {
	const pinned = "0bccfce73835ac32acdaf29fedf4392720de54256e6671062c100d3abadb35cf"
	got := signing.CanonicalDigest(canonicalOf(t, faridpurSubject()))
	if got != pinned {
		t.Fatalf("the canonical digest of the fixture is %s and was pinned as %s.\n"+
			"Something about the encoding changed. If that was deliberate, bump "+
			"signing.CanonicalVersion, keep v1's function, and update this constant in the "+
			"same commit.", got, pinned)
	}
}
