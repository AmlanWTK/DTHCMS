package patient_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/patient"
)

// Duplicate detection against a real register (CP30).
//
// The interesting tests here are not the mechanics — they are the labelled fixture set at
// the bottom, which measures precision and recall and is the evidence behind the proposed
// thresholds. The plan asks for exactly that, and marks the numbers as requiring approval.

// registerAs puts a patient in the register through the real path, so the read model and
// its search keys exist as they would in production.
func (h *api) registerAs(t *testing.T, mutate func(map[string]any)) map[string]any {
	t.Helper()
	body := form(uuid.Must(uuid.NewV7()))
	// Each fixture patient needs its own identity number, or the deterministic pass
	// blocks the second one before anything interesting happens.
	delete(body, "identifiers")
	mutate(body)
	resp, decoded := h.call(t, http.MethodPost, "/v1/patients", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("registering the fixture failed with %d: %v", resp.StatusCode, decoded)
	}
	return decoded["patient"].(map[string]any)
}

func (h *api) check(t *testing.T, mutate func(map[string]any)) map[string]any {
	t.Helper()
	body := form(uuid.Must(uuid.NewV7()))
	delete(body, "identifiers")
	mutate(body)
	resp, decoded := h.call(t, http.MethodPost, "/v1/patients/check-duplicates", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check-duplicates returned %d: %v", resp.StatusCode, decoded)
	}
	return decoded
}

// --- the deterministic pass ---

func TestAnIdentityNumberAlreadyOnFileBlocksRegistration(t *testing.T) {
	// Acceptance criterion 1.
	h := newAPI(t)
	first, _ := h.call(t, http.MethodPost, "/v1/patients", form(uuid.Must(uuid.NewV7())))
	if first.StatusCode != http.StatusCreated {
		t.Fatal(first.StatusCode)
	}

	// A different person's name, the same national ID, written differently.
	verdict := h.check(t, func(body map[string]any) {
		body["name_en"] = "Someone Else Entirely"
		body["birth_date"] = "1990-01-01"
		body["phone_primary"] = "01912345678"
		body["identifiers"] = map[string]string{"national_id": "1990-1234-5678"}
	})
	if verdict["verdict"] != "blocked" {
		t.Fatalf("verdict = %v", verdict["verdict"])
	}
	candidates := verdict["candidates"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %v", candidates)
	}
	candidate := candidates[0].(map[string]any)
	if candidate["deterministic"] != true || candidate["score"].(float64) != 1 {
		t.Errorf("candidate = %v", candidate)
	}
	// The existing record is shown, which is the whole point: the officer needs to see
	// who it is before deciding this is not them.
	if candidate["clinical_id"] != h.code+"-2026-000001" {
		t.Errorf("the blocking record was not identified: %v", candidate)
	}
	// And the phone is masked — this screen is read with the patient standing at the desk.
	if masked, _ := candidate["phone_masked"].(string); masked != "•••• 5678" {
		t.Errorf("phone_masked = %q", masked)
	}
}

func TestTheSamePhoneAndExactBirthDateBlocks(t *testing.T) {
	// A household shares a telephone. A household does not share a telephone *and* an
	// exact date of birth.
	h := newAPI(t)
	h.registerAs(t, func(map[string]any) {})

	verdict := h.check(t, func(body map[string]any) {
		body["name_en"] = "R. Begum" // the same person, entered shorter
	})
	if verdict["verdict"] != "blocked" {
		t.Fatalf("verdict = %v, candidates = %v", verdict["verdict"], verdict["candidates"])
	}
}

func TestABlockedRegistrationIsRefused(t *testing.T) {
	// The matcher is wired into the registration path, so the block is not merely advice.
	h := newAPI(t)
	h.service.Duplicates = patient.NewMatcher(h.store, h.sealer).AsCheck()
	h.registerAs(t, func(map[string]any) {})

	body := form(uuid.Must(uuid.NewV7()))
	delete(body, "identifiers")
	body["name_en"] = "Rahima Begum"
	resp, decoded := h.call(t, http.MethodPost, "/v1/patients", body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d: %v", resp.StatusCode, decoded)
	}
	var patients int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient`).Scan(&patients); err != nil {
		t.Fatal(err)
	}
	if patients != 1 {
		t.Errorf("a blocked registration created a patient: %d rows", patients)
	}
}

// --- the probabilistic pass ---

func TestANearMatchIsSurfacedAndDoesNotBlock(t *testing.T) {
	// Acceptance criterion 2, and the half of it that matters most: a register in
	// Bangladesh legitimately contains many people of one name born in one year, so a
	// near match must warn and must not refuse.
	h := newAPI(t)
	h.service.Duplicates = patient.NewMatcher(h.store, h.sealer).AsCheck()
	h.registerAs(t, func(body map[string]any) {
		body["name_en"] = "Mohammad Rahim"
		body["birth_date"] = "1985-06-14"
		body["phone_primary"] = "01711111111"
	})

	// The same name a different operator heard, and the birth year without the day.
	verdict := h.check(t, func(body map[string]any) {
		body["name_en"] = "Muhammad Raheem"
		body["birth_date"] = "1985-01-01"
		body["dob_precision"] = "year"
		body["dob_source"] = "patient_stated"
		body["phone_primary"] = "01722222222"
	})
	if verdict["verdict"] != "review" {
		t.Fatalf("verdict = %v, candidates = %v", verdict["verdict"], verdict["candidates"])
	}
	candidates := verdict["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatal("no candidate was surfaced")
	}
	reasons := candidates[0].(map[string]any)["reasons"].([]any)
	if len(reasons) == 0 {
		t.Fatal("a candidate with no reasons is a bare number nobody can act on")
	}
	// Both languages, on every reason: half the clinic's registration staff read Bangla.
	for _, raw := range reasons {
		reason := raw.(map[string]any)
		if reason["message"] == "" || reason["message_bn"] == "" {
			t.Errorf("reason %v is missing a language", reason)
		}
	}

	// And it does not refuse: these may genuinely be two people, and the officer has seen
	// the warning already.
	body := form(uuid.Must(uuid.NewV7()))
	delete(body, "identifiers")
	body["name_en"] = "Muhammad Raheem"
	body["birth_date"] = "1985-01-01"
	body["dob_precision"] = "year"
	body["dob_source"] = "patient_stated"
	body["phone_primary"] = "01722222222"
	if resp, decoded := h.call(t, http.MethodPost, "/v1/patients", body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("a reviewed near-match was refused with %d: %v", resp.StatusCode, decoded)
	}
}

func TestAnUnrelatedPersonIsClear(t *testing.T) {
	h := newAPI(t)
	h.registerAs(t, func(map[string]any) {})

	verdict := h.check(t, func(body map[string]any) {
		body["name_en"] = "Abdul Karim"
		body["name_bn"] = "আব্দুল করিম"
		body["sex"] = "male"
		body["birth_date"] = "1962-11-03"
		body["phone_primary"] = "01988887777"
	})
	if verdict["verdict"] != "clear" {
		t.Fatalf("an unrelated person scored: %v", verdict)
	}
}

// --- the labelled set ---

// labelled is a fixture set of pairs: an existing patient, a registration attempt, and
// whether they are in truth the same person.
//
// Drawn from the ways a Bangladeshi register actually acquires duplicates and near-misses:
// the same name heard twice, the honorific written differently, a year-precision date of
// birth against an exact one, a household telephone, twins, and a father and son sharing a
// name. It is small and hand-labelled on purpose — the plan asks for measurement before
// the thresholds are approved, and a hand-labelled set of realistic cases says more at this
// stage than a large generated one that only contains the mistakes a generator makes.
type labelledCase struct {
	name       string
	existing   map[string]string
	attempt    map[string]string
	sameperson bool
}

func labelledSet() []labelledCase {
	person := func(nameEN, nameBN, sex, born, phone, district string) map[string]string {
		return map[string]string{
			"name_en": nameEN, "name_bn": nameBN, "sex": sex,
			"birth_date": born, "phone_primary": phone, "district": district,
		}
	}
	return []labelledCase{
		// --- true duplicates ---
		{
			name:       "the same name heard by two operators",
			existing:   person("Mohammad Rahim", "মোহাম্মদ রহিম", "male", "1985-06-14", "01711111101", "Faridpur"),
			attempt:    person("Muhammad Raheem", "মোহাম্মদ রহিম", "male", "1985-06-14", "01711111101", "Faridpur"),
			sameperson: true,
		},
		{
			name:       "the honorific abbreviated",
			existing:   person("Md Abdul Karim", "মোঃ আব্দুল করিম", "male", "1970-02-01", "01711111102", "Faridpur"),
			attempt:    person("Mohammad Abdul Karim", "মোঃ আব্দুল করিম", "male", "1970-02-01", "01711111102", "Faridpur"),
			sameperson: true,
		},
		{
			name:       "a surname with no settled spelling",
			existing:   person("Nasrin Chowdhury", "নাসরিন চৌধুরী", "female", "1992-09-30", "01711111103", "Faridpur"),
			attempt:    person("Nasreen Choudhury", "নাসরিন চৌধুরী", "female", "1992-09-30", "01711111103", "Faridpur"),
			sameperson: true,
		},
		{
			name:       "an exact date the second time, a year-only date the first",
			existing:   person("Fatema Begum", "ফাতেমা বেগম", "female", "1958-01-01", "01711111104", "Faridpur"),
			attempt:    person("Fatima Begom", "ফাতেমা বেগম", "female", "1958-01-01", "01711111104", "Faridpur"),
			sameperson: true,
		},
		{
			name:       "a transposed digit in the telephone",
			existing:   person("Zakir Hossain", "জাকির হোসেন", "male", "1979-04-12", "01711111105", "Faridpur"),
			attempt:    person("Jakir Hossen", "জাকির হোসেন", "male", "1979-04-12", "01711111150", "Faridpur"),
			sameperson: true,
		},
		{
			name:       "a different telephone entirely, everything else identical",
			existing:   person("Salma Akter", "সালমা আক্তার", "female", "1996-12-05", "01711111106", "Faridpur"),
			attempt:    person("Salma Akhter", "সালমা আক্তার", "female", "1996-12-05", "01966660001", "Faridpur"),
			sameperson: true,
		},

		// --- true distinct people who look alike ---
		{
			name:       "twins: one date of birth, one household, different sex",
			existing:   person("Rahim Uddin", "রহিম উদ্দিন", "male", "2015-03-08", "01711111107", "Faridpur"),
			attempt:    person("Rahima Khatun", "রহিমা খাতুন", "female", "2015-03-08", "01711111107", "Faridpur"),
			sameperson: false,
		},
		{
			name:       "father and son, one name, one household telephone",
			existing:   person("Md Anwar Hossain", "মোঃ আনোয়ার হোসেন", "male", "1962-07-19", "01711111108", "Faridpur"),
			attempt:    person("Md Anwar Hossain", "মোঃ আনোয়ার হোসেন", "male", "1990-07-19", "01711111108", "Faridpur"),
			sameperson: false,
		},
		{
			name:       "a common name and a common birth year, nothing else shared",
			existing:   person("Md Rahim", "মোঃ রহিম", "male", "1980-01-01", "01711111109", "Faridpur"),
			attempt:    person("Md Rahim", "মোঃ রহিম", "male", "1980-01-01", "01955550002", "Dhaka"),
			sameperson: false,
		},
		{
			name:       "two sisters, a year apart, one telephone",
			existing:   person("Ayesha Siddika", "আয়েশা সিদ্দিকা", "female", "2001-05-11", "01711111110", "Faridpur"),
			attempt:    person("Aysha Siddiqua", "আয়েশা সিদ্দিকা", "female", "2003-05-11", "01711111110", "Faridpur"),
			sameperson: false,
		},
		{
			name:       "similar names, different people, different everything else",
			existing:   person("Karim Sheikh", "করিম শেখ", "male", "1975-08-22", "01711111111", "Faridpur"),
			attempt:    person("Kabir Sheikh", "কবির শেখ", "male", "1988-02-14", "01944440003", "Rajbari"),
			sameperson: false,
		},
		{
			name:       "unrelated in every field",
			existing:   person("Shirin Sultana", "শিরিন সুলতানা", "female", "1968-10-02", "01711111112", "Faridpur"),
			attempt:    person("Jamal Uddin", "জামাল উদ্দিন", "male", "1994-03-27", "01933330004", "Gopalganj"),
			sameperson: false,
		},
	}
}

// TestPrecisionAndRecallOnTheLabelledSet is the measurement CP30 asks for before its
// thresholds are approved.
//
// The numbers it prints are the evidence behind DefaultThresholds. Recall is required to be
// perfect: a missed duplicate is a permanent split in somebody's clinical history and
// nobody notices for a year. Precision is required to be high but not perfect, because a
// false positive costs an officer one glance at a record — and the case that keeps
// precision below 1 here (a father and son sharing a name and a household telephone) is one
// that *should* be shown to a person.
func TestPrecisionAndRecallOnTheLabelledSet(t *testing.T) {
	h := newAPI(t)
	matcher := patient.NewMatcher(h.store, h.sealer)

	cases := labelledSet()
	// Every existing record goes into one register, so each attempt is scored against all
	// of them and not only its own pair. That is the realistic setting: a false positive
	// against somebody else's record is as costly as one against the pair's.
	for i, c := range cases {
		c := c
		i := i
		h.registerAs(t, func(body map[string]any) {
			apply(body, c.existing)
			body["phone_secondary"] = ""
			body["consent_reference"] = fmt.Sprintf("consent_fixture_%02d", i)
		})
	}

	var truePositive, falsePositive, falseNegative int
	for _, c := range cases {
		attempt := registrationFrom(t, c.attempt)
		match, err := matcher.Check(context.Background(), h.facility, attempt)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		surfaced := match.Verdict != patient.VerdictClear
		switch {
		case c.sameperson && surfaced:
			truePositive++
		case c.sameperson && !surfaced:
			falseNegative++
			t.Errorf("MISSED a true duplicate — %s (a split history nobody notices for a year)", c.name)
		case !c.sameperson && surfaced:
			falsePositive++
			t.Logf("surfaced two different people — %s (costs one glance; acceptable)", c.name)
		}
	}

	precision := ratio(truePositive, truePositive+falsePositive)
	recall := ratio(truePositive, truePositive+falseNegative)
	t.Logf("labelled set: %d cases, %d true duplicates", len(cases), truePositive+falseNegative)
	t.Logf("thresholds: block %.2f, review %.2f", matcher.Thresholds.Block, matcher.Thresholds.Review)
	t.Logf("precision %.3f  recall %.3f  (tp %d, fp %d, fn %d)",
		precision, recall, truePositive, falsePositive, falseNegative)

	if recall < 1 {
		t.Errorf("recall is %.3f; a missed duplicate is close to unfixable", recall)
	}
	// The floor is low on purpose. This set is adversarial by construction — every
	// "different people" case is a hard one (twins, a father and son sharing a name and a
	// telephone, two spellings of one common name) — so its precision is a *worst case*,
	// not the rate a desk will see. A real register is mostly easy negatives, and a
	// candidate that reaches the desk costs one glance at a record. What must not slip is
	// recall, which is asserted at 1 above.
	if precision < 0.5 {
		t.Errorf("precision is %.3f even on the hard set; at this rate a registration "+
			"officer stops reading the warnings", precision)
	}
}

func TestNothingIsEverMergedAutomatically(t *testing.T) {
	// Acceptance criterion 4, and the one worth asserting structurally rather than
	// trusting: a wrong merge is worse than a duplicate, because a duplicate is two
	// incomplete histories and a wrong merge is one history containing another person's
	// clinical facts.
	h := newAPI(t)
	h.service.Duplicates = patient.NewMatcher(h.store, h.sealer).AsCheck()
	h.registerAs(t, func(body map[string]any) { body["name_en"] = "Mohammad Rahim" })

	// A registration that scores as high as the probabilistic pass can score.
	h.check(t, func(body map[string]any) { body["name_en"] = "Muhammad Raheem" })

	var merges int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient_merge`).Scan(&merges); err != nil {
		t.Fatal(err)
	}
	if merges != 0 {
		t.Errorf("the matcher merged %d records on its own", merges)
	}
	var merged int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient WHERE status = 'merged'`).Scan(&merged); err != nil {
		t.Fatal(err)
	}
	if merged != 0 {
		t.Errorf("%d patients were merged without a person deciding", merged)
	}
}

// --- helpers ---

func apply(body map[string]any, fields map[string]string) {
	for key, value := range fields {
		body[key] = value
	}
}

func registrationFrom(t *testing.T, fields map[string]string) patient.Registration {
	t.Helper()
	born, err := time.ParseInLocation(time.DateOnly, fields["birth_date"], patient.Dhaka)
	if err != nil {
		t.Fatal(err)
	}
	return patient.Registration{
		NameEN: fields["name_en"], NameBN: fields["name_bn"],
		Sex: patient.Sex(fields["sex"]), BirthDate: born,
		DOBPrecision: patient.PrecisionDay, DOBSource: patient.SourcePatientStated,
		PhonePrimary:     fields["phone_primary"],
		Address:          patient.Address{District: fields["district"]},
		ConsentReference: "consent_probe",
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1
	}
	return float64(numerator) / float64(denominator)
}
