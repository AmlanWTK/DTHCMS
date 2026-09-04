package patient

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/textmatch"
)

// Duplicate detection (CP30, §3 Step 1's "strict duplicate-record prevention").
//
// A duplicate patient destroys longitudinal history, corrupts every research cohort the
// person belongs to, and is close to unfixable once a year of records has accumulated
// against both halves. So the check happens *before* creation, and it happens in two
// passes with very different characters.
//
// **Deterministic.** An identity number already on file, or the same mobile and the same
// exact date of birth. These are not scores: they are the same person, and registration is
// **blocked** with the existing record shown. There is no threshold to tune and no judgement
// to make.
//
// **Probabilistic.** Everything else — a name spelled differently, a date of birth a year
// out, a phone with a transposed digit. These are **surfaced for review** and never act on
// their own. The reason is a fact about Bangladeshi naming rather than caution in general:
// a clinic's register contains many people genuinely named Md Rahim, born in the same year,
// living in the same upazila. A matcher aggressive enough to catch every duplicate here
// would spend a busy morning telling a registration officer that two different people are
// the same, after which the warnings stop being read. So the review threshold is set where
// a *person* would want to look, not where the machine is confident.
//
// **Nothing is ever merged automatically** (the plan says so twice). A wrong merge is worse
// than a duplicate: a duplicate is two incomplete histories, a wrong merge is one history
// containing another person's clinical facts.

// Verdict is what the matcher concluded.
type Verdict string

const (
	// VerdictClear means nothing plausible was found.
	VerdictClear Verdict = "clear"
	// VerdictReview means one or more candidates deserve a person's eye. Registration
	// continues if the officer says these are different people.
	VerdictReview Verdict = "review"
	// VerdictBlocked means an identity number or a phone-and-date-of-birth pair already
	// belongs to somebody. Registration does not continue.
	VerdictBlocked Verdict = "blocked"
)

// Thresholds are where the two lines sit.
//
// **These are proposed values.** The plan marks them as requiring approval after
// measurement, and the measurement is `TestPrecisionAndRecallOnTheLabelledSet`, which runs
// them against a fixture set of true duplicates and true distinct-but-similar patients and
// prints the numbers. They will be re-tuned against real spellings during the pilot, which
// is why they are a struct and not constants.
type Thresholds struct {
	// Block is where the score alone is enough to refuse. Deliberately just below 1: the
	// deterministic pass is what blocks, and this exists only for the case where every
	// probabilistic signal agrees perfectly — same name, same date, same phone, same
	// address — which is a duplicate by any reading.
	Block float64
	// Review is where a person should look. Chosen from the labelled set as the point that
	// catches every true duplicate it can while keeping the false-positive rate low enough
	// that a registration officer keeps reading the warnings.
	Review float64
}

// DefaultThresholds are the proposed values, measured on the labelled fixture set.
var DefaultThresholds = Thresholds{Block: 0.95, Review: 0.62}

// weights are how much each signal contributes. They sum to 1.
//
// Name dominates, because it is the only signal present in every registration. Date of
// birth is next and is the strongest *discriminator* — two people with the same name and
// different birth years are two people. Phone is powerful when it matches and says nothing
// when it does not, since a household shares one number. Address is a tiebreaker only.
var weights = struct{ Name, Birth, Phone, Address, Sex float64 }{
	Name: 0.42, Birth: 0.30, Phone: 0.18, Address: 0.06, Sex: 0.04,
}

// Candidate is one existing patient the matcher thinks is worth showing.
type Candidate struct {
	PatientID  uuid.UUID `json:"patient_id"`
	ClinicalID string    `json:"clinical_id"`
	NameEN     string    `json:"name_en"`
	NameBN     string    `json:"name_bn"`
	Sex        string    `json:"sex"`
	BirthDate  string    `json:"birth_date"`
	// PhoneMasked shows enough to recognise a household's number without printing it in
	// full on a screen the patient can see.
	PhoneMasked  string    `json:"phone_masked"`
	District     string    `json:"district"`
	RegisteredAt time.Time `json:"registered_at"`

	Score float64 `json:"score"`
	// Reasons say *why*, in the operator's language rather than in weights. "Same national
	// ID" and "the name sounds the same and the birth year matches" call for different
	// decisions, and a bare 0.71 calls for neither.
	Reasons []Reason `json:"reasons"`
	// Deterministic is true when this candidate blocks rather than warns.
	Deterministic bool `json:"deterministic"`
}

// Reason is one signal, said in both languages.
type Reason struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	MessageBN string `json:"message_bn"`
}

// Match is the answer to "is this person already here?".
type Match struct {
	Verdict    Verdict     `json:"verdict"`
	Candidates []Candidate `json:"candidates"`
}

// Blocking returns the candidate that refuses the registration, if any.
func (m Match) Blocking() (Candidate, bool) {
	for _, candidate := range m.Candidates {
		if candidate.Deterministic {
			return candidate, true
		}
	}
	return Candidate{}, false
}

// ErrBlockedByDuplicate is the refusal a blocked registration carries.
var ErrBlockedByDuplicate = errors.New("patient: this person is already registered")

// Matcher finds existing patients who may be the person in front of the desk.
type Matcher struct {
	store      *Store
	sealer     *IdentifierSealer
	Thresholds Thresholds
}

func NewMatcher(store *Store, sealer *IdentifierSealer) *Matcher {
	return &Matcher{store: store, sealer: sealer, Thresholds: DefaultThresholds}
}

// Check runs both passes for a registration that has not happened yet.
func (m *Matcher) Check(ctx context.Context, facility uuid.UUID, in Registration) (Match, error) {
	in = in.normalised()

	// --- the deterministic pass ---
	//
	// Run first and returned alone. Once an identity number is known to belong to
	// somebody, a list of people whose names merely sound similar is noise on the screen
	// at the moment the officer has one thing to do.
	if blocked, found, err := m.deterministic(ctx, facility, in); err != nil {
		return Match{}, err
	} else if found {
		return Match{Verdict: VerdictBlocked, Candidates: []Candidate{blocked}}, nil
	}

	// --- the probabilistic pass ---
	rows, err := m.store.MatchCandidates(ctx, facility, MatchProbe{
		BirthDate:  in.BirthDate.In(Dhaka),
		NameKeyEN:  textmatch.Key(in.NameEN),
		NameEN:     in.NameEN,
		NameBN:     in.NameBN,
		Phone:      in.PhonePrimary,
		YearWindow: 2,
	})
	if err != nil {
		return Match{}, err
	}

	out := Match{Verdict: VerdictClear}
	for _, row := range rows {
		candidate := m.score(in, row)
		if candidate.Score < m.Thresholds.Review {
			continue
		}
		out.Candidates = append(out.Candidates, candidate)
		if candidate.Score >= m.Thresholds.Block {
			candidate.Deterministic = true
			out.Verdict = VerdictBlocked
		} else if out.Verdict == VerdictClear {
			out.Verdict = VerdictReview
		}
	}
	sortByScore(out.Candidates)
	return out, nil
}

// deterministic is the pass with no threshold: an identity number already on file, or the
// same mobile and the same exact date of birth.
func (m *Matcher) deterministic(ctx context.Context, facility uuid.UUID, in Registration) (Candidate, bool, error) {
	if m.sealer != nil {
		for _, kind := range in.identifierOrder() {
			existing, err := m.store.ByIdentifier(ctx, facility, kind, m.sealer.Digest(kind, in.Identifiers[kind]))
			switch {
			case err == nil:
				return candidateOf(existing, 1, true, Reason{
					Code:    "same_identifier",
					Message: fmt.Sprintf("This %s already belongs to %s.", label(kind), existing.ClinicalID),
					MessageBN: fmt.Sprintf("এই %s ইতিমধ্যে %s-এর নামে নিবন্ধিত।",
						labelBN(kind), existing.ClinicalID),
				}), true, nil
			case errors.Is(err, ErrNotFound):
			default:
				return Candidate{}, false, err
			}
		}
	}

	// A shared household telephone is common, and a shared telephone *and* an identical
	// date of birth is not: that pair is a second registration of one person often enough
	// that blocking is right, and the officer is shown the record and can say so.
	if in.PhonePrimary != "" && !in.BirthDate.IsZero() {
		existing, err := m.store.ByPhoneAndBirthDate(ctx, facility, in.PhonePrimary, in.BirthDate.In(Dhaka))
		switch {
		case err == nil:
			return candidateOf(existing, 1, true, Reason{
				Code:      "same_phone_and_birth_date",
				Message:   fmt.Sprintf("%s is registered with this mobile and this exact date of birth.", existing.ClinicalID),
				MessageBN: fmt.Sprintf("এই মোবাইল ও এই জন্ম তারিখ দিয়ে %s ইতিমধ্যে নিবন্ধিত।", existing.ClinicalID),
			}), true, nil
		case errors.Is(err, ErrNotFound):
		default:
			return Candidate{}, false, err
		}
	}
	return Candidate{}, false, nil
}

// score is the probabilistic pass for one candidate row.
func (m *Matcher) score(in Registration, row MatchRow) Candidate {
	candidate := Candidate{
		PatientID: row.PatientID, ClinicalID: row.ClinicalID,
		NameEN: row.NameEN, NameBN: row.NameBN, Sex: row.Sex,
		BirthDate: row.BirthDate.Format(time.DateOnly), PhoneMasked: maskPhone(row.Phone),
		District: row.District, RegisteredAt: row.RegisteredAt,
	}

	name := m.nameScore(in, row, &candidate)
	birth := m.birthScore(in, row, &candidate)
	phone := m.phoneScore(in, row, &candidate)

	address := 0.0
	if in.Address.District != "" && in.Address.District == row.District {
		address = 0.6
		if in.Address.Upazila != "" && in.Address.Upazila == row.Upazila {
			address = 1
		}
	}

	// Sex is a *discriminator*, not evidence. Matching sex says almost nothing (half the
	// register matches); differing sex is a strong signal of two people, and the one case
	// that makes this worth a weight at all is twins, who share a birth date and often a
	// surname.
	sex := 1.0
	if string(in.Sex) != row.Sex {
		sex = 0
		candidate.Reasons = append(candidate.Reasons, Reason{
			Code:      "different_sex",
			Message:   "The recorded sex is different.",
			MessageBN: "লিপিবদ্ধ লিঙ্গ ভিন্ন।",
		})
	}

	candidate.Score = round4(
		weights.Name*name + weights.Birth*birth + weights.Phone*phone +
			weights.Address*address + weights.Sex*sex)
	return candidate
}

func (m *Matcher) nameScore(in Registration, row MatchRow, candidate *Candidate) float64 {
	best := textmatch.Similarity(in.NameEN, row.NameEN)
	how := "spelled almost the same"
	howBN := "বানান প্রায় একই"

	if in.NameBN != "" && row.NameBN != "" {
		if bn := textmatch.Similarity(in.NameBN, row.NameBN); bn > best {
			best, how, howBN = bn, "written almost the same in Bangla", "বাংলায় প্রায় একই"
		}
	}
	// The phonetic key: what makes Mohammad Rahim and Muhammad Raheem one candidate.
	// Scored slightly below an exact textual match, because the key is deliberately lossy
	// and a key collision is weaker evidence than the same letters.
	if key := textmatch.Key(in.NameEN); key != "" && row.NameKeyEN != "" {
		if key == row.NameKeyEN && 0.92 > best {
			best, how, howBN = 0.92, "sounds the same", "উচ্চারণ একই"
		} else if similar := textmatch.Similarity(key, row.NameKeyEN) * 0.9; similar > best {
			best, how, howBN = similar, "sounds similar", "উচ্চারণ কাছাকাছি"
		}
	}

	if best >= 0.55 {
		candidate.Reasons = append(candidate.Reasons, Reason{
			Code:      "similar_name",
			Message:   fmt.Sprintf("The name is %s: %s.", how, row.NameEN),
			MessageBN: fmt.Sprintf("নাম %s: %s।", howBN, nameOrEN(row)),
		})
	}
	return best
}

func (m *Matcher) birthScore(in Registration, row MatchRow, candidate *Candidate) float64 {
	if in.BirthDate.IsZero() {
		return 0
	}
	born := in.BirthDate.In(Dhaka)
	existing := row.BirthDate.In(Dhaka)

	switch {
	case born.Equal(existing):
		candidate.Reasons = append(candidate.Reasons, Reason{
			Code:      "same_birth_date",
			Message:   "The date of birth is exactly the same.",
			MessageBN: "জন্ম তারিখ হুবহু এক।",
		})
		return 1
	case born.Year() == existing.Year() && born.Month() == existing.Month():
		return 0.75
	case born.Year() == existing.Year():
		// Very common and very weak: a year-precision date of birth is recorded as
		// 1 January, so every patient who did not know their birthday shares one.
		candidate.Reasons = append(candidate.Reasons, Reason{
			Code:      "same_birth_year",
			Message:   "The birth year is the same.",
			MessageBN: "জন্মসাল একই।",
		})
		return 0.55
	case math.Abs(float64(born.Year()-existing.Year())) <= 2:
		return 0.2
	}
	return 0
}

func (m *Matcher) phoneScore(in Registration, row MatchRow, candidate *Candidate) float64 {
	if in.PhonePrimary == "" || row.Phone == "" {
		return 0
	}
	if in.PhonePrimary == row.Phone {
		// A household shares a telephone, so this is not proof on its own — which is
		// exactly why the weight is 0.18 and not 0.5.
		candidate.Reasons = append(candidate.Reasons, Reason{
			Code:      "same_phone",
			Message:   "The mobile number is the same.",
			MessageBN: "মোবাইল নম্বর একই।",
		})
		return 1
	}
	switch textmatch.Distance(in.PhonePrimary, row.Phone) {
	case 1, 2:
		candidate.Reasons = append(candidate.Reasons, Reason{
			Code:      "similar_phone",
			Message:   "The mobile number differs by a digit or two, which is often a typing error.",
			MessageBN: "মোবাইল নম্বরে এক-দুই অঙ্কের পার্থক্য — সাধারণত টাইপের ভুল।",
		})
		return 0.6
	}
	return 0
}

// --- shaping ---

func candidateOf(p Patient, score float64, deterministic bool, reasons ...Reason) Candidate {
	return Candidate{
		PatientID: p.ID, ClinicalID: p.ClinicalID,
		NameEN: p.NameEN, NameBN: p.NameBN, Sex: string(p.Sex),
		BirthDate:   p.Birth.Date.In(Dhaka).Format(time.DateOnly),
		PhoneMasked: maskPhone(p.PhonePrimary), District: p.Address.District,
		RegisteredAt: p.RegisteredAt,
		Score:        score, Deterministic: deterministic, Reasons: reasons,
	}
}

// maskPhone shows the last four digits. A duplicate warning is read at a desk with the
// patient standing in front of it, and the person beside them can read the screen too.
func maskPhone(phone string) string {
	if len(phone) <= 4 {
		return phone
	}
	return "•••• " + phone[len(phone)-4:]
}

func nameOrEN(row MatchRow) string {
	if row.NameBN != "" {
		return row.NameBN
	}
	return row.NameEN
}

func label(kind IdentifierKind) string {
	switch kind {
	case NationalID:
		return "national ID"
	case BirthCertificate:
		return "birth certificate number"
	case Passport:
		return "passport number"
	case DrivingLicence:
		return "driving licence number"
	}
	return "identity number"
}

func labelBN(kind IdentifierKind) string {
	switch kind {
	case NationalID:
		return "জাতীয় পরিচয়পত্র নম্বর"
	case BirthCertificate:
		return "জন্ম নিবন্ধন নম্বর"
	case Passport:
		return "পাসপোর্ট নম্বর"
	case DrivingLicence:
		return "ড্রাইভিং লাইসেন্স নম্বর"
	}
	return "পরিচয় নম্বর"
}

func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

func sortByScore(candidates []Candidate) {
	// Insertion sort: the list is at most a handful long by construction, and a stable
	// order matters more than the algorithm.
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].Score > candidates[j-1].Score; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
}

// AsCheck adapts the matcher to the registration service's hook, refusing a registration
// the deterministic pass blocked and letting a reviewed one through.
//
// Review candidates do **not** refuse: the officer has already seen them (the desk calls
// check-duplicates as they type) and has decided these are different people. Refusing here
// would mean a registration that can never be completed for a patient who genuinely shares
// a name and a birth year with somebody, which in this register is thousands of people.
func (m *Matcher) AsCheck() DuplicateCheck {
	return func(ctx context.Context, facility uuid.UUID, in Registration, _ []Identifier) error {
		match, err := m.Check(ctx, facility, in)
		if err != nil {
			return err
		}
		if blocking, ok := match.Blocking(); ok {
			return fmt.Errorf("%w: %s", ErrBlockedByDuplicate, blocking.ClinicalID)
		}
		return nil
	}
}
