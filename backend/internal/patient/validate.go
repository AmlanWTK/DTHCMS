package patient

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// What must be true before a patient record exists (CP28, CP29).
//
// The required set is the *clinical minimum*, confirmed with Dr Nahid: an English name, a
// sex, an exact date of birth, one mobile, and the consent record. Everything else is
// prompted and skippable, so that a registration desk can complete a record while the
// patient waits at the next station rather than holding a queue for an income band nobody
// present knows.
//
// Every refusal names a field and carries both languages. "Invalid patient" is not
// something a registration officer can act on; "the mobile number must be a Bangladeshi
// mobile, like 01712345678" is.

// Dhaka is the clinic's calendar. A patient registered at 00:30 on 1 January in Dhaka was
// born before today by the clinic's reckoning, not UTC's, and a date-of-birth check that
// used UTC would refuse a perfectly ordinary registration for six hours a day.
var Dhaka = time.FixedZone("Asia/Dhaka", 6*60*60)

// MaxPlausibleAge is where "this is a typing error" begins. The oldest verified human
// lived to 122; 130 leaves room and still catches a year typed as 1093.
const MaxPlausibleAge = 130

var (
	// A Bangladeshi mobile: 01, then an operator digit 3–9, then eight more.
	// Grameenphone 017/013, Robi 018/016, Banglalink 019/014, Teletalk 015, Airtel 016.
	bdMobile = regexp.MustCompile(`^01[3-9][0-9]{8}$`)
	// Anything else with a plausible international shape, for the optional second number.
	international = regexp.MustCompile(`^\+[0-9]{8,15}$`)
	// Not \d: Bengali digits are decimal too, and a phone number typed in Bengali numerals
	// must be rejected rather than silently accepted as something else.
	notASCIIDigit = regexp.MustCompile(`[^0-9]`)
)

// NormalisePhone turns what a person typed into the one form the database stores.
//
// Accepted: 01712345678, +8801712345678, 8801712345678, and any of those with spaces or
// hyphens. Everything becomes +8801712345678, because a phone stored three ways is a phone
// that matches nothing — and an SMS reminder (§11) that silently fails for a fraction of
// patients is worse than one that fails for all of them.
func NormalisePhone(raw string) (string, bool) {
	digits := notASCIIDigit.ReplaceAllString(strings.TrimSpace(raw), "")
	switch {
	case strings.HasPrefix(digits, "880"):
		digits = "0" + strings.TrimPrefix(digits, "880")
	case strings.HasPrefix(digits, "0"):
		// Already local.
	case len(digits) == 10 && strings.HasPrefix(digits, "1"):
		// A number written without the leading zero, which people do.
		digits = "0" + digits
	}
	if !bdMobile.MatchString(digits) {
		return "", false
	}
	return "+880" + strings.TrimPrefix(digits, "0"), true
}

// NormaliseSecondaryPhone accepts a landline or an overseas number as well as a mobile.
// The second field exists so that a patient reachable only on a relative's landline can
// still be registered, which the confirmed rule requires.
func NormaliseSecondaryPhone(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", true
	}
	if mobile, ok := NormalisePhone(trimmed); ok {
		return mobile, true
	}
	digits := notASCIIDigit.ReplaceAllString(trimmed, "")
	if strings.HasPrefix(digits, "0") && len(digits) >= 9 && len(digits) <= 11 {
		// A Bangladeshi landline: 02-xxxxxxxx in Dhaka, 031-xxxxxx in Chattogram.
		digits = "880" + strings.TrimPrefix(digits, "0")
	}
	candidate := "+" + digits
	if !international.MatchString(candidate) {
		return "", false
	}
	return candidate, true
}

// Registration is what a caller asks for. Kept apart from Patient because a registration
// carries things a patient row does not — the raw identifiers, the consent reference — and
// because the fields a caller may set are exactly the fields a caller may set.
type Registration struct {
	NameEN string
	NameBN string
	Sex    Sex

	BirthDate    time.Time
	DOBPrecision DOBPrecision
	DOBSource    DOBSource

	PhonePrimary   string
	PhoneSecondary string

	Address   Address
	Emergency EmergencyContact
	Socio     Socioeconomic

	// NationalID and the rest, as typed or as read by OCR. Hashed and sealed before they
	// reach a column; never stored as given (D-47).
	Identifiers map[IdentifierKind]string

	// ConsentReference is the consent record this registration was taken under. Required:
	// §15.1 makes consent tracking binding, and a patient record with no consent behind it
	// is one nothing may lawfully be done with.
	ConsentReference string
}

var (
	knownSex        = map[Sex]bool{SexFemale: true, SexMale: true, SexOther: true}
	knownPrecision  = map[DOBPrecision]bool{PrecisionDay: true, PrecisionMonth: true, PrecisionYear: true}
	knownDOBSources = map[DOBSource]bool{
		SourceBirthCertificate: true, SourceNationalID: true, SourcePassport: true,
		SourceImmunisation: true, SourcePatientStated: true, SourceGuardianStated: true,
		SourceEstimated: true,
	}
	knownIdentifierKinds = map[IdentifierKind]bool{
		NationalID: true, BirthCertificate: true, Passport: true,
		DrivingLicence: true, OtherIdentifier: true,
	}
)

// Validate checks a registration against the confirmed rules and returns every problem.
//
// `now` is passed rather than read, because "is this date in the future" is a question
// about a clock and a test that cannot fix the clock is a test that fails at midnight.
func (r Registration) Validate(now time.Time) error {
	var errs Errors
	add := func(field, code, en, bn string) {
		errs = append(errs, FieldError{Field: field, Code: code, Message: en, MessageBN: bn})
	}

	if strings.TrimSpace(r.NameEN) == "" {
		add("name_en", "required", "The patient's name in English is required.",
			"রোগীর ইংরেজি নাম আবশ্যক।")
	} else if len([]rune(r.NameEN)) > 120 {
		add("name_en", "too_long", "The name is too long.", "নামটি অনেক বড়।")
	}
	if len([]rune(r.NameBN)) > 120 {
		add("name_bn", "too_long", "The name is too long.", "নামটি অনেক বড়।")
	}

	if !knownSex[r.Sex] {
		add("sex", "invalid", "Choose female, male or other.",
			"নারী, পুরুষ বা অন্যান্য — একটি বেছে নিন।")
	}

	validateBirthDate(r, now, add)

	if _, ok := NormalisePhone(r.PhonePrimary); !ok {
		add("phone_primary", "invalid",
			"Enter a Bangladeshi mobile number, like 01712345678.",
			"বাংলাদেশি মোবাইল নম্বর দিন, যেমন ০১৭১২৩৪৫৬৭৮।")
	}
	if _, ok := NormaliseSecondaryPhone(r.PhoneSecondary); !ok {
		add("phone_secondary", "invalid",
			"Enter a valid phone number, or leave it blank.",
			"সঠিক ফোন নম্বর দিন, অথবা খালি রাখুন।")
	}
	if em := strings.TrimSpace(r.Emergency.Phone); em != "" {
		if _, ok := NormaliseSecondaryPhone(em); !ok {
			add("emergency_contact.phone", "invalid",
				"Enter a valid phone number for the emergency contact, or leave it blank.",
				"জরুরি যোগাযোগের সঠিক ফোন নম্বর দিন, অথবা খালি রাখুন।")
		}
	}

	if strings.TrimSpace(r.ConsentReference) == "" {
		add("consent_reference", "required",
			"A patient cannot be registered without a recorded consent.",
			"সম্মতি রেকর্ড না করে রোগী নিবন্ধন করা যাবে না।")
	}

	validateSocioeconomic(r.Socio, add)

	for kind, value := range r.Identifiers {
		if !knownIdentifierKinds[kind] {
			add("identifiers", "unknown_kind", fmt.Sprintf("%q is not an identifier this clinic records.", kind),
				"এই ধরনের পরিচয়পত্র ক্লিনিক সংরক্ষণ করে না।")
			continue
		}
		if strings.TrimSpace(value) == "" {
			add("identifiers."+string(kind), "required",
				"Enter the number, or remove the identifier.",
				"নম্বরটি দিন, অথবা পরিচয়পত্রটি বাদ দিন।")
		}
	}

	return errs.OrNil()
}

func validateBirthDate(r Registration, now time.Time, add func(field, code, en, bn string)) {
	if r.BirthDate.IsZero() {
		// [R-06]: not optional, and not defaultable. A percentile computed from a guessed
		// age is a clinical number that looks like a measurement.
		add("birth_date", "required",
			"The date of birth is required, and must be as exact as the patient can give.",
			"জন্ম তারিখ আবশ্যক, এবং রোগী যতটা নির্দিষ্ট করে বলতে পারেন ততটা নির্দিষ্ট হতে হবে।")
		return
	}
	today := now.In(Dhaka)
	born := r.BirthDate.In(Dhaka)
	if born.After(today) {
		add("birth_date", "future",
			"The date of birth cannot be in the future.",
			"জন্ম তারিখ ভবিষ্যতের হতে পারে না।")
	}
	if today.Year()-born.Year() > MaxPlausibleAge {
		add("birth_date", "implausible",
			fmt.Sprintf("That date implies an age over %d. Check the year.", MaxPlausibleAge),
			fmt.Sprintf("এই তারিখ অনুযায়ী বয়স %d বছরের বেশি হয়। সালটি দেখে নিন।", MaxPlausibleAge))
	}
	if !knownPrecision[r.DOBPrecision] {
		add("dob_precision", "invalid",
			"Say how exact the date is: day, month or year.",
			"তারিখটি কতটা নির্দিষ্ট বলুন: দিন, মাস না বছর।")
	}
	if !knownDOBSources[r.DOBSource] {
		add("dob_verified_by", "invalid",
			"Say what the date of birth came from.",
			"জন্ম তারিখ কোথা থেকে পাওয়া গেছে তা বলুন।")
	}
	// A document was seen, but the date it carries is only approximate: the two disagree,
	// and the disagreement is almost always a transcription error worth catching at the
	// desk rather than in a growth chart two years later.
	if r.DOBPrecision != PrecisionDay {
		switch r.DOBSource {
		case SourceBirthCertificate, SourceNationalID, SourcePassport, SourceImmunisation:
			add("dob_precision", "document_is_exact",
				"A document carries an exact date. Enter the day, or say the date was stated rather than seen.",
				"নথিতে নির্দিষ্ট তারিখ থাকে। দিনটি লিখুন, অথবা বলুন তারিখটি বলা হয়েছে, দেখা যায়নি।")
		}
	}
}

func validateSocioeconomic(s Socioeconomic, add func(field, code, en, bn string)) {
	oneOf := func(field, value string, allowed []string, en, bn string) {
		if value == "" {
			return // not captured, which the confirmed required set allows
		}
		for _, candidate := range allowed {
			if candidate == value {
				return
			}
		}
		add("socioeconomic."+field, "invalid", en, bn)
	}
	oneOf("education_level", s.Education, EducationLevels,
		"Choose one of the listed education levels.", "তালিকা থেকে শিক্ষাগত যোগ্যতা বেছে নিন।")
	oneOf("occupation_category", s.Occupation, OccupationCategories,
		"Choose one of the listed occupations.", "তালিকা থেকে পেশা বেছে নিন।")
	oneOf("income_band", s.IncomeBand, IncomeBands,
		"Choose one of the listed income bands.", "তালিকা থেকে আয়ের সীমা বেছে নিন।")
	oneOf("residence_type", s.Residence, ResidenceTypes,
		"Choose urban, semi-urban or rural.", "শহর, উপ-শহর বা গ্রাম বেছে নিন।")
	oneOf("medicine_payer", s.MedicinePayer, MedicinePayers,
		"Choose who pays for the patient's medicines.", "রোগীর ওষুধের খরচ কে দেন তা বেছে নিন।")

	if s.HouseholdSize != 0 && (s.HouseholdSize < 1 || s.HouseholdSize > 40) {
		add("socioeconomic.household_size", "out_of_range",
			"The household size looks wrong. Enter a number between 1 and 40.",
			"পরিবারের সদস্য সংখ্যা ঠিক মনে হচ্ছে না। ১ থেকে ৪০-এর মধ্যে একটি সংখ্যা দিন।")
	}
}
