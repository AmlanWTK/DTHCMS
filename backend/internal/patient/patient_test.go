package patient_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/secretbox"
)

// The patient domain, without a database (CP28).
//
// What is asserted here is the part that is a decision: which telephone numbers the clinic
// will accept, what a date of birth has to be before a percentile may be computed from it,
// and — the one that is expensive to fix later — that a research id carries nothing of the
// patient it belongs to.

// --- telephone numbers ---

func TestAMobileNumberIsStoredOneWayHoweverItIsTyped(t *testing.T) {
	// A phone stored three ways is a phone that matches nothing, and an SMS reminder
	// (§11) that silently fails for a fraction of patients is worse than one that fails
	// for all of them.
	for _, typed := range []string{
		"01712345678",
		"+8801712345678",
		"8801712345678",
		"1712345678",
		" 01712-345678 ",
		"017 1234 5678",
		"+880 1712 345 678",
	} {
		got, ok := patient.NormalisePhone(typed)
		if !ok {
			t.Errorf("%q was refused; a registration desk types it every day", typed)
			continue
		}
		if got != "+8801712345678" {
			t.Errorf("%q normalised to %q", typed, got)
		}
	}
}

func TestWhatIsNotABangladeshiMobile(t *testing.T) {
	for name, typed := range map[string]string{
		"empty":                  "",
		"too short":              "0171234567",
		"too long":               "017123456789",
		"a landline":             "0288123456",
		"an unassigned operator": "01212345678",
		"an overseas number":     "+919812345678",
		"letters":                "01712ABCDEF",
		// Bengali numerals are decimal digits too. A number typed in them must be refused
		// rather than silently stripped to something shorter that happens to parse.
		"Bengali numerals": "০১৭১২৩৪৫৬৭৮",
	} {
		if got, ok := patient.NormalisePhone(typed); ok {
			t.Errorf("%s (%q) was accepted as %q", name, typed, got)
		}
	}
}

func TestTheSecondNumberTakesALandlineOrAnOverseasNumber(t *testing.T) {
	// The second field exists so that a patient reachable only on a relative's landline,
	// or on a son's number in Dubai, can still be registered.
	for typed, want := range map[string]string{
		"":                "",
		"01812345678":     "+8801812345678",
		"02-8812345":      "+88028812345",
		"+971501234567":   "+971501234567",
		"031 123456":      "+88031123456",
		"  +44207123456 ": "+44207123456",
	} {
		got, ok := patient.NormaliseSecondaryPhone(typed)
		if !ok {
			t.Errorf("%q was refused", typed)
			continue
		}
		if got != want {
			t.Errorf("%q normalised to %q, want %q", typed, got, want)
		}
	}
	for _, bad := range []string{"12", "not a number", "+1"} {
		if _, ok := patient.NormaliseSecondaryPhone(bad); ok {
			t.Errorf("%q was accepted as a telephone number", bad)
		}
	}
}

// --- the date of birth ---

// today is the clinic's day for these tests. Fixed, because "is this in the future" is a
// question about a clock and a test that cannot fix the clock is a test that fails at
// midnight.
var today = time.Date(2026, 9, 3, 10, 0, 0, 0, patient.Dhaka)

func valid() patient.Registration {
	return patient.Registration{
		NameEN:           "Rahima Begum",
		NameBN:           "রহিমা বেগম",
		Sex:              patient.SexFemale,
		BirthDate:        time.Date(1979, 4, 12, 0, 0, 0, 0, patient.Dhaka),
		DOBPrecision:     patient.PrecisionDay,
		DOBSource:        patient.SourceNationalID,
		PhonePrimary:     "01712345678",
		ConsentReference: "consent_2026_0001",
	}
}

func TestAValidRegistrationIsAccepted(t *testing.T) {
	if err := valid().Validate(today); err != nil {
		t.Fatalf("a complete registration was refused: %v", err)
	}
}

func TestTheDateOfBirthIsMandatoryAndValidated(t *testing.T) {
	// [R-06]: pediatric percentiles are computed from age, so a guessed date of birth is a
	// clinical number that looks like a measurement. Acceptance criterion 1.
	for name, mutate := range map[string]func(*patient.Registration){
		"absent": func(r *patient.Registration) { r.BirthDate = time.Time{} },
		"tomorrow": func(r *patient.Registration) {
			r.BirthDate = today.AddDate(0, 0, 1)
		},
		"a year typed as 1093": func(r *patient.Registration) {
			r.BirthDate = time.Date(1093, 4, 12, 0, 0, 0, 0, patient.Dhaka)
		},
		"no precision": func(r *patient.Registration) { r.DOBPrecision = "" },
		"an invented precision": func(r *patient.Registration) {
			r.DOBPrecision = "approximately"
		},
		"no source":          func(r *patient.Registration) { r.DOBSource = "" },
		"an invented source": func(r *patient.Registration) { r.DOBSource = "a guess" },
	} {
		r := valid()
		mutate(&r)
		err := r.Validate(today)
		if err == nil {
			t.Errorf("a date of birth that is %s was accepted", name)
			continue
		}
		assertBilingual(t, name, err)
	}
}

func TestTodayIsAcceptedBecauseNewbornsAreRegistered(t *testing.T) {
	// A baby born this morning is registered this afternoon, and by the clinic's calendar
	// rather than UTC's — which matters for the six hours a day the two disagree.
	r := valid()
	r.BirthDate = today
	r.DOBPrecision = patient.PrecisionDay
	r.DOBSource = patient.SourceBirthCertificate
	if err := r.Validate(today); err != nil {
		t.Fatalf("a newborn was refused: %v", err)
	}
}

func TestADocumentCarriesAnExactDate(t *testing.T) {
	// A birth certificate has a day on it. "Birth certificate, year precision" is almost
	// always a transcription error, and catching it at the desk is much cheaper than
	// finding it in a growth chart two years later.
	r := valid()
	r.DOBSource = patient.SourceBirthCertificate
	r.DOBPrecision = patient.PrecisionYear
	err := r.Validate(today)
	if err == nil {
		t.Fatal("a birth certificate with a year-only date was accepted")
	}
	if !mentions(err, "dob_precision") {
		t.Errorf("the refusal did not name dob_precision: %v", err)
	}

	// And the honest version of the same record is fine: the year is what the patient said.
	r.DOBSource = patient.SourcePatientStated
	if err := r.Validate(today); err != nil {
		t.Fatalf("a year-precision, patient-stated date was refused: %v", err)
	}
}

func TestAgeIsWholeYearsOnTheDay(t *testing.T) {
	born := patient.BirthDate{Date: time.Date(2000, 9, 4, 0, 0, 0, 0, patient.Dhaka)}
	for _, c := range []struct {
		on   time.Time
		want int
	}{
		{time.Date(2026, 9, 3, 0, 0, 0, 0, patient.Dhaka), 25}, // the day before the birthday
		{time.Date(2026, 9, 4, 0, 0, 0, 0, patient.Dhaka), 26}, // the birthday
		{time.Date(2026, 9, 5, 0, 0, 0, 0, patient.Dhaka), 26},
		{time.Date(2000, 9, 4, 0, 0, 0, 0, patient.Dhaka), 0}, // the day itself
		// A visit recorded at 23:00 UTC is the next morning in Dhaka, and the clinic's
		// calendar is the one on the wall.
		{time.Date(2026, 9, 3, 23, 0, 0, 0, time.UTC), 26},
	} {
		if got := born.Age(c.on); got != c.want {
			t.Errorf("age on %s = %d, want %d", c.on.Format(time.DateOnly), got, c.want)
		}
	}

	// A leap-year birthday. Comparing day-of-year rather than month-and-day makes
	// everybody born in a leap year a year younger for one day, which is a wrong age on a
	// pediatric percentile chart.
	leapling := patient.BirthDate{Date: time.Date(2000, 2, 29, 0, 0, 0, 0, patient.Dhaka)}
	for _, c := range []struct {
		on   time.Time
		want int
	}{
		{time.Date(2026, 2, 28, 0, 0, 0, 0, patient.Dhaka), 25},
		{time.Date(2026, 3, 1, 0, 0, 0, 0, patient.Dhaka), 26},
		{time.Date(2024, 2, 29, 0, 0, 0, 0, patient.Dhaka), 24},
	} {
		if got := leapling.Age(c.on); got != c.want {
			t.Errorf("a leapling's age on %s = %d, want %d", c.on.Format(time.DateOnly), got, c.want)
		}
	}
}

// --- the rest of the required set ---

func TestTheRequiredSetIsTheClinicalMinimum(t *testing.T) {
	// Confirmed with Dr Nahid: an English name, a sex, a date of birth, one mobile, and
	// the consent record. Everything else is prompted and skippable, so a desk can finish
	// a record while the patient walks to the next station.
	for field, mutate := range map[string]func(*patient.Registration){
		"name_en":           func(r *patient.Registration) { r.NameEN = "   " },
		"sex":               func(r *patient.Registration) { r.Sex = "" },
		"phone_primary":     func(r *patient.Registration) { r.PhonePrimary = "" },
		"consent_reference": func(r *patient.Registration) { r.ConsentReference = "" },
	} {
		r := valid()
		mutate(&r)
		err := r.Validate(today)
		if err == nil {
			t.Errorf("a registration with no %s was accepted", field)
			continue
		}
		if !mentions(err, field) {
			t.Errorf("the refusal did not name %s: %v", field, err)
		}
		assertBilingual(t, field, err)
	}

	// And the skippable ones really are skippable.
	bare := patient.Registration{
		NameEN: "Abdul Karim", Sex: patient.SexMale,
		BirthDate:    time.Date(1990, 1, 1, 0, 0, 0, 0, patient.Dhaka),
		DOBPrecision: patient.PrecisionYear, DOBSource: patient.SourcePatientStated,
		PhonePrimary: "01912345678", ConsentReference: "consent_2026_0002",
	}
	if err := bare.Validate(today); err != nil {
		t.Fatalf("the clinical minimum was refused: %v", err)
	}
}

func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	// A desk that fixes one field, resubmits, and is told about the next one is a desk
	// that holds a queue four times over.
	r := patient.Registration{}
	err := r.Validate(today)
	var errs patient.Errors
	if !asErrors(err, &errs) {
		t.Fatalf("Validate returned %T, want patient.Errors", err)
	}
	for _, want := range []string{"name_en", "sex", "birth_date", "phone_primary", "consent_reference"} {
		if !contains(errs.Fields(), want) {
			t.Errorf("an empty registration was not refused for %s; got %v", want, errs.Fields())
		}
	}
}

func TestTheSocioEconomicValuesAreAClosedList(t *testing.T) {
	// §12: a research variable whose categories can be edited from the application is a
	// variable whose cohorts stop being comparable between one paper and the next.
	// Acceptance criterion 4.
	for field, mutate := range map[string]func(*patient.Socioeconomic){
		"education_level":     func(s *patient.Socioeconomic) { s.Education = "class five" },
		"occupation_category": func(s *patient.Socioeconomic) { s.Occupation = "rickshaw" },
		"income_band":         func(s *patient.Socioeconomic) { s.IncomeBand = "12000" },
		"residence_type":      func(s *patient.Socioeconomic) { s.Residence = "town" },
		"medicine_payer":      func(s *patient.Socioeconomic) { s.MedicinePayer = "insurance" },
		"household_size":      func(s *patient.Socioeconomic) { s.HouseholdSize = 41 },
	} {
		r := valid()
		mutate(&r.Socio)
		err := r.Validate(today)
		if err == nil {
			t.Errorf("an invented %s was accepted", field)
			continue
		}
		if !mentions(err, "socioeconomic."+field) {
			t.Errorf("the refusal did not name socioeconomic.%s: %v", field, err)
		}
		assertBilingual(t, field, err)
	}

	// Every listed value is accepted, so the Go list and the CHECK constraint in
	// migration 00016 cannot drift apart without a test failing.
	for _, education := range patient.EducationLevels {
		for _, payer := range patient.MedicinePayers {
			r := valid()
			r.Socio.Education, r.Socio.MedicinePayer = education, payer
			if err := r.Validate(today); err != nil {
				t.Fatalf("%s/%s was refused: %v", education, payer, err)
			}
		}
	}
}

func TestNotCapturedAndNotKnownAreDifferentAnswers(t *testing.T) {
	// A household that cannot state its monthly income is a different cohort from one
	// that was never asked, and both are legitimate.
	notCaptured, notKnown := valid(), valid()
	notKnown.Socio.IncomeBand = "unknown"
	if err := notCaptured.Validate(today); err != nil {
		t.Fatalf("an unasked income band was refused: %v", err)
	}
	if err := notKnown.Validate(today); err != nil {
		t.Fatalf("an unknown income band was refused: %v", err)
	}
	if notCaptured.Socio.IncomeBand == notKnown.Socio.IncomeBand {
		t.Error("the two answers are stored identically; the distinction is lost")
	}
}

func TestAnIdentifierMustBeAKindTheClinicRecords(t *testing.T) {
	r := valid()
	r.Identifiers = map[patient.IdentifierKind]string{"voter_card": "12345"}
	if err := r.Validate(today); err == nil {
		t.Fatal("an unknown identifier kind was accepted")
	}

	r.Identifiers = map[patient.IdentifierKind]string{patient.NationalID: "  "}
	err := r.Validate(today)
	if err == nil {
		t.Fatal("an empty national ID was accepted")
	}
	if !mentions(err, "identifiers.national_id") {
		t.Errorf("the refusal did not name the identifier: %v", err)
	}
}

// --- the research id ---

func TestAResearchIDIsOpaque(t *testing.T) {
	// Acceptance criterion 3, and §12's expensive-to-fix-later detail: a research finding
	// must not be traceable back to a person by inspection.
	const n = 1000
	ids := make([]string, 0, n)
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		id, err := patient.NewResearchID()
		if err != nil {
			t.Fatal(err)
		}
		if !patient.ValidResearchID(id) {
			t.Fatalf("%q is not the shape the database will accept", id)
		}
		if seen[id] {
			t.Fatalf("%q was minted twice in %d draws", id, n)
		}
		seen[id] = true
		ids = append(ids, id)
	}

	// The one that matters: sorting by the id must produce no ordering in the sequence
	// they were minted in. A sequential — or merely correlated — id would sort back into
	// registration order, and registration order plus a birth year is a person.
	ascending := 0
	for i := 1; i < n; i++ {
		if ids[i-1] < ids[i] {
			ascending++
		}
	}
	if ascending < n*4/10 || ascending > n*6/10 {
		t.Errorf("%d of %d consecutive ids ascend; a research id carries registration order",
			ascending, n-1)
	}

	// And the alphabet is Crockford's: no I, L, O or U, so an id read off a printout is
	// not transcribed into a different one.
	for _, id := range ids[:20] {
		if strings.ContainsAny(id[len(patient.ResearchIDPrefix):], "ILOU") {
			t.Errorf("%q contains a character that is misread by hand", id)
		}
	}
}

func TestAResearchIDIsNotDerivedFromAnything(t *testing.T) {
	// The structural half of the same criterion. NewResearchID takes no argument, so
	// there is nothing about the patient it *could* be derived from; this test states the
	// property that would break if somebody later "improved" it into a keyed hash of the
	// clinical id — which would be reversible the day the key leaked.
	//
	// What a derived id looks like from outside is *low entropy*: a hash truncated to 26
	// Crockford characters would still look random, but an id built from a facility code,
	// a year and a counter would use a small part of the alphabet heavily. So the
	// assertion is that every character position draws uniformly from all 32 symbols.
	const draws = 1000
	frequency := map[rune]int{}
	for range draws {
		id, err := patient.NewResearchID()
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range id[len(patient.ResearchIDPrefix):] {
			frequency[r]++
		}
	}

	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	// 1000 ids x 26 characters over 32 symbols is 812 apiece. The band is roughly seven
	// standard deviations wide, so a uniform generator will not trip it and a generator
	// that leans on part of the alphabet cannot avoid it.
	const low, high = 600, 1050
	for _, symbol := range alphabet {
		n := frequency[symbol]
		if n < low || n > high {
			t.Errorf("%q appears %d times in %d ids; the id is not uniformly random",
				string(symbol), n, draws)
		}
	}
	if len(frequency) != len(alphabet) {
		t.Errorf("the ids use %d of the %d symbols", len(frequency), len(alphabet))
	}
}

func TestValidResearchIDRefusesTheWrongShape(t *testing.T) {
	for name, id := range map[string]string{
		"no prefix":          "7K2MQ9XW4B6NPRTVYZ3H5DFGJ8",
		"the wrong prefix":   "RX-7K2MQ9XW4B6NPRTVYZ3H5DFGJ8",
		"too short":          "RS-7K2MQ9XW4B6NPRTVYZ3H5DFG",
		"too long":           "RS-7K2MQ9XW4B6NPRTVYZ3H5DFGJ88",
		"a forbidden letter": "RS-IK2MQ9XW4B6NPRTVYZ3H5DFGJ8",
		"lower case":         "RS-7k2mq9xw4b6nprtvyz3h5dfgj8",
		"empty":              "",
	} {
		if patient.ValidResearchID(id) {
			t.Errorf("%s (%q) was accepted", name, id)
		}
	}
}

// --- official numbers ---

func sealer(t *testing.T) *patient.IdentifierSealer {
	t.Helper()
	ring, err := secretbox.NewRing(secretbox.Key{ID: "k1", Material: bytes32(7)})
	if err != nil {
		t.Fatal(err)
	}
	s, err := patient.NewIdentifierSealer(bytes32(11), ring)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func bytes32(fill byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = fill
	}
	return out
}

func TestAnIdentifierIsHashedSealedAndMaskedButNeverStored(t *testing.T) {
	// D-47, confirmed: the digest finds duplicates, the sealed value is read under a
	// step-up, the mask is what a screen shows. None of the three is the number.
	s := sealer(t)
	const nid = "1990 1234 5678 9"

	id, err := s.Seal(patient.NationalID, nid)
	if err != nil {
		t.Fatal(err)
	}
	if len(id.Digest) != 32 {
		t.Errorf("digest is %d bytes; the column requires 32", len(id.Digest))
	}
	normalised := patient.NormaliseIdentifier(nid)
	if strings.Contains(string(id.Sealed), normalised) {
		t.Error("the sealed value contains the number")
	}
	if strings.Contains(id.Masked, normalised) || !strings.Contains(id.Masked, "*") {
		t.Errorf("the mask %q is not a mask", id.Masked)
	}
	if !strings.HasSuffix(id.Masked, "6789") {
		t.Errorf("the mask %q does not end in the four digits a patient recognises", id.Masked)
	}

	opened, err := s.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	if opened != normalised {
		t.Errorf("opened %q, want %q", opened, normalised)
	}
}

func TestOneNumberWrittenTwoWaysIsOneNumber(t *testing.T) {
	// A duplicate check that treats "1990-1234-5678" and "1990 1234 5678" as two numbers
	// is a duplicate check that does not work (§3 Step 1).
	s := sealer(t)
	a := s.Digest(patient.NationalID, "1990-1234-5678")
	b := s.Digest(patient.NationalID, " 1990 1234 5678 ")
	if string(a) != string(b) {
		t.Error("the same number written two ways produced two digests")
	}
}

func TestTheKindIsPartOfTheIdentity(t *testing.T) {
	// A passport number and a national ID that happen to share digits are two
	// identifiers, not a duplicate.
	s := sealer(t)
	if string(s.Digest(patient.NationalID, "12345678")) == string(s.Digest(patient.Passport, "12345678")) {
		t.Error("two kinds of identifier with the same digits collided")
	}

	// And a ciphertext cannot be moved to a row of another kind and opened there.
	id, err := s.Seal(patient.NationalID, "12345678")
	if err != nil {
		t.Fatal(err)
	}
	id.Kind = patient.Passport
	if _, err := s.Open(id); err == nil {
		t.Error("a sealed national ID opened as a passport")
	}
}

func TestADigestWithoutAPepperIsRefused(t *testing.T) {
	// A plain SHA-256 of a ten-digit number is reversible by anyone with a laptop and a
	// weekend, so the absence of a pepper is a refusal rather than a default.
	ring, err := secretbox.NewRing(secretbox.Key{ID: "k1", Material: bytes32(7)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := patient.NewIdentifierSealer(nil, ring); err == nil {
		t.Error("a sealer was built with no pepper")
	}
	if _, err := patient.NewIdentifierSealer(bytes32(11), nil); err == nil {
		t.Error("a sealer was built with no key ring")
	}
}

func TestTwoDeploymentsProduceDifferentDigests(t *testing.T) {
	// The point of a deployment-wide pepper: a digest lifted from this clinic's database
	// says nothing about a number in another clinic's.
	ring, err := secretbox.NewRing(secretbox.Key{ID: "k1", Material: bytes32(7)})
	if err != nil {
		t.Fatal(err)
	}
	here, err := patient.NewIdentifierSealer(bytes32(11), ring)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := patient.NewIdentifierSealer(bytes32(12), ring)
	if err != nil {
		t.Fatal(err)
	}
	if string(here.Digest(patient.NationalID, "12345678")) ==
		string(elsewhere.Digest(patient.NationalID, "12345678")) {
		t.Error("two deployments produced the same digest for one number")
	}
}

func TestMaskHidesEverythingButTheLastFour(t *testing.T) {
	for raw, want := range map[string]int{
		"1990123456789": 4, // a national ID
		"AB1234567":     4, // a passport
		"1234":          0, // too short to show anything
		"12":            0,
	} {
		masked := patient.Mask(raw)
		visible := 0
		for _, r := range masked {
			if r != '*' && r != ' ' {
				visible++
			}
		}
		if visible != want {
			t.Errorf("Mask(%q) = %q shows %d characters, want %d", raw, masked, visible, want)
		}
		if !strings.Contains(masked, "*") {
			t.Errorf("Mask(%q) = %q hides nothing", raw, masked)
		}
	}
}

// --- helpers ---

func assertBilingual(t *testing.T, subject string, err error) {
	t.Helper()
	var errs patient.Errors
	if !asErrors(err, &errs) {
		t.Errorf("%s: Validate returned %T, want patient.Errors", subject, err)
		return
	}
	for _, e := range errs {
		// Half the clinic's staff read Bangla, and a validation message that appears only
		// in English is a message they will guess at.
		if strings.TrimSpace(e.Message) == "" || strings.TrimSpace(e.MessageBN) == "" {
			t.Errorf("%s: %s is missing a language", subject, e.Field)
		}
		if e.Code == "" {
			t.Errorf("%s: %s has no machine-readable code", subject, e.Field)
		}
		if !strings.ContainsAny(e.MessageBN, "অআইঈউএকখগঘচছজঝটঠডঢণতথদধনপফবভমযরলশষসহ") {
			t.Errorf("%s: %s's Bangla message is not in Bangla: %q", subject, e.Field, e.MessageBN)
		}
	}
}

func mentions(err error, field string) bool {
	var errs patient.Errors
	if !asErrors(err, &errs) {
		return false
	}
	return contains(errs.Fields(), field)
}

func asErrors(err error, out *patient.Errors) bool {
	return errors.As(err, out)
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
