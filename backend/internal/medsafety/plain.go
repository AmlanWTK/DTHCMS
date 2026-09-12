package medsafety

import (
	"strings"
)

// Saying a rule back to the physician in words (CP77, acceptance criterion 1 and 4).
//
// # Why this is on the server
//
// The authoring form could compose this sentence in TypeScript from the fields on screen, and it
// would be one fewer round trip. It is here instead, and rendered from the **stored canonical
// condition**, because of what the preview is for: it is the physician's check that what the
// system understood is what he meant. A sentence built from the form's own state can only ever
// agree with the form. One built from the bytes that will be evaluated can disagree — and the
// case where it disagrees is exactly the case worth catching.
//
// It is also what the publish confirmation shows, what the sandbox heads its result with, and
// what goes in the audit entry, so a second implementation would be four chances to drift.
//
// # The Bengali is a clinical sentence, not a translation
//
// "When Metformin is prescribed and eGFR is below 30, block" does not become Bengali by
// substituting words; the verb goes last and the condition leads. What is written below is how a
// Bangladeshi physician would say the thing to a colleague. It is still mine and not his — D-24
// has Dr. Nahid reviewing the whole clinical register, and this belongs in that review.

// Plain is a rule stated in words, in both languages.
type Plain struct {
	// EN and BN are the whole sentence — what the rule watches for and what it will do.
	EN string `json:"en"`
	BN string `json:"bn"`
	// NeedsEN and NeedsBN name the clinical facts this rule cannot answer without. Shown to
	// the author beside the preview, because a rule that needs an eGFR will say "cannot
	// verify" on every patient who has not had one, and that is worth knowing before
	// publishing rather than after.
	NeedsEN []string `json:"needs_en"`
	NeedsBN []string `json:"needs_bn"`
}

// Explain renders a version as the sentence a physician reads.
func (v Version) Explain() Plain {
	p := Plain{}
	whenEN, whenBN := v.Condition.clauses()

	subjectEN, subjectBN := v.Condition.Subject.phrase()

	// "When X is prescribed and A and B, <do>."
	p.EN = "When " + subjectEN + " is prescribed"
	if len(whenEN) > 0 {
		p.EN += " and " + joinEN(whenEN)
	}
	p.EN += ", " + actionEN(v.Severity) + "."

	// Bengali leads with the condition and puts the verb last.
	p.BN = subjectBN + " দেওয়া হলে"
	if len(whenBN) > 0 {
		p.BN += " এবং " + strings.Join(whenBN, " এবং ")
	}
	p.BN += ", " + actionBN(v.Severity) + "।"

	seen := map[Datum]bool{}
	for _, pred := range v.Condition.When {
		d := pred.Needs()
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		p.NeedsEN = append(p.NeedsEN, datumEN[d])
		p.NeedsBN = append(p.NeedsBN, datumBN[d])
	}
	return p
}

// actionEN and actionBN say what the severity does, in consequences rather than in labels.
//
// "BLOCK" on a form is a word somebody has to be taught. "stop the prescription until the
// physician overrides it with a reason" is what will actually happen, and the author who reads
// that is the one who picks the right severity.
func actionEN(s Severity) string {
	switch s {
	case SeverityBlock:
		return "stop the prescription and require a written reason to go ahead"
	case SeverityWarn:
		return "show a warning that the physician can prescribe past"
	default:
		return "show a note beside the medicine"
	}
}

func actionBN(s Severity) string {
	switch s {
	case SeverityBlock:
		return "ব্যবস্থাপত্রটি আটকে দেওয়া হবে এবং এগোতে হলে লিখিত কারণ লাগবে"
	case SeverityWarn:
		return "একটি সতর্কবার্তা দেখানো হবে, চিকিৎসক চাইলে এগোতে পারবেন"
	default:
		return "ওষুধটির পাশে একটি তথ্য দেখানো হবে"
	}
}

// phrase names a target the way a person would.
func (t Target) phrase() (string, string) {
	switch t.Match {
	case MatchGeneric:
		// The molecule names themselves stay in Latin script in both languages — a
		// transliterated generic name is a second spelling of the thing every rule matches on
		// — but the *list* is joined the way each language joins a list. "A, B and C" inside a
		// Bengali sentence is the half-translated interface this project keeps refusing to
		// ship.
		return joinEN(titleAll(t.Generics)), joinBN(titleAll(t.Generics))
	case MatchClass:
		en := joinEN(classPhraseEN(t.Classes))
		bn := joinBNOr(classCodes(t.Classes)) + " শ্রেণির ওষুধ"
		return en, bn
	default:
		return "any medicine", "যেকোনো ওষুধ"
	}
}

// classPhraseEN turns BIGUANIDE into "any biguanide". The class codes are the formulary's own
// vocabulary and are readable once the underscores are gone; a lookup of the bilingual class
// names would be better and is a round trip this renderer deliberately does not make: the
// preview has to be renderable from a condition alone, including in the import validator, where
// there is no facility to look a class name up in.
func classPhraseEN(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, "any "+strings.ToLower(strings.ReplaceAll(c, "_", " ")))
	}
	return out
}

func titleAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, capitalise(s))
	}
	return out
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// clauses renders each predicate as a phrase.
func (c Condition) clauses() (en []string, bn []string) {
	for _, p := range c.When {
		a, b := p.phrase()
		en = append(en, a)
		bn = append(bn, b)
	}
	return en, bn
}

func (p Predicate) phrase() (string, string) {
	switch p.Kind {
	case PredEGFR:
		return "the eGFR is " + relEN(p.Operator) + " " + trimFloat(p.Value) + " " + p.Unit,
			"eGFR " + trimFloat(p.Value) + " " + p.Unit + relBN(p.Operator)

	case PredAge:
		return "the patient's age is " + relEN(p.Operator) + " " + trimFloat(p.Value) + " " + p.Unit,
			"রোগীর বয়স " + trimFloat(p.Value) + " " + unitBN(p.Unit) + relBN(p.Operator)

	case PredDailyDose:
		return "the total daily dose is " + relEN(p.Operator) + " " + trimFloat(p.Value) + " " + p.Unit,
			"দৈনিক মোট মাত্রা " + trimFloat(p.Value) + " " + p.Unit + relBN(p.Operator)

	case PredPregnancy:
		return "the patient is " + joinOrEN(pregnancyPhrasesEN(p.States)),
			"রোগী " + joinBNOr(pregnancyPhrasesBN(p.States))

	case PredHepatic:
		return "hepatic impairment is " + joinOrEN(lowerAll(p.States)),
			"যকৃতের দুর্বলতা " + joinBNOr(hepaticPhrasesBN(p.States))

	case PredDiagnosis:
		return "the patient carries the diagnosis " + joinOrEN(p.DiagnosisCodes),
			"রোগীর রোগনির্ণয়ে " + joinBNOr(p.DiagnosisCodes) + " রয়েছে"

	case PredAllergy:
		en := "the patient is allergic to " + strings.ToLower(p.AllergenGroup)
		bn := "রোগীর " + p.AllergenGroup + "-এ অ্যালার্জি আছে"
		if p.CrossReactive {
			en += " or to anything that cross-reacts with it"
			bn += " বা এর সঙ্গে ক্রস-রিঅ্যাক্ট করে এমন কিছুতে"
		}
		return en, bn

	case PredCoPrescribed:
		withEN, withBN := p.With.phrase()
		en := withEN + " is on the same prescription"
		bn := withBN + " একই ব্যবস্থাপত্রে আছে"
		if p.CurrentMedications {
			en = withEN + " is on the same prescription or already being taken"
			bn = withBN + " একই ব্যবস্থাপত্রে আছে বা রোগী ইতিমধ্যে খাচ্ছেন"
		}
		return en, bn

	case PredDuplicate:
		what, whatBN := "the same molecule", "একই অণুর"
		if p.With.Match == MatchClass {
			what, whatBN = "the same therapeutic class", "একই চিকিৎসা-শ্রেণির"
		}
		en := "another medicine of " + what + " is on the same prescription"
		bn := whatBN + " আরেকটি ওষুধ একই ব্যবস্থাপত্রে আছে"
		if p.CurrentMedications {
			en += " or already being taken"
			bn += " বা রোগী ইতিমধ্যে খাচ্ছেন"
		}
		return en, bn
	}
	return string(p.Kind), string(p.Kind)
}

func relEN(o Operator) string {
	switch o {
	case OpLessThan:
		return "below"
	case OpAtMost:
		return "at or below"
	case OpGreaterThan:
		return "above"
	default:
		return "at or above"
	}
}

func relBN(o Operator) string {
	switch o {
	case OpLessThan:
		return "-এর নিচে হলে"
	case OpAtMost:
		return "-এর সমান বা নিচে হলে"
	case OpGreaterThan:
		return "-এর উপরে হলে"
	default:
		return "-এর সমান বা উপরে হলে"
	}
}

func unitBN(u string) string {
	if strings.EqualFold(u, "years") {
		return "বছর"
	}
	return u
}

func pregnancyPhrasesEN(states []string) []string {
	out := make([]string, 0, len(states))
	for _, s := range states {
		if p, ok := pregnancyEN[s]; ok {
			out = append(out, p)
			continue
		}
		out = append(out, strings.ToLower(s))
	}
	return out
}

func pregnancyPhrasesBN(states []string) []string {
	out := make([]string, 0, len(states))
	for _, s := range states {
		if p, ok := pregnancyBN[s]; ok {
			out = append(out, p)
			continue
		}
		out = append(out, s)
	}
	return out
}

func hepaticPhrasesBN(states []string) []string {
	out := make([]string, 0, len(states))
	for _, s := range states {
		if p, ok := hepaticBN[s]; ok {
			out = append(out, p)
			continue
		}
		out = append(out, s)
	}
	return out
}

func lowerAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(s))
	}
	return out
}

// joinEN renders a list as "a, b and c". Oxford-comma-free, because the clinic writes British
// English and because the lists here are two items long more often than not.
func joinEN(in []string) string {
	switch len(in) {
	case 0:
		return ""
	case 1:
		return in[0]
	case 2:
		return in[0] + " and " + in[1]
	default:
		return strings.Join(in[:len(in)-1], ", ") + " and " + in[len(in)-1]
	}
}

// joinBN renders a list the way Bengali does: "ক, খ ও গ".
func joinBN(in []string) string {
	switch len(in) {
	case 0:
		return ""
	case 1:
		return in[0]
	case 2:
		return in[0] + " ও " + in[1]
	default:
		return strings.Join(in[:len(in)-1], ", ") + " ও " + in[len(in)-1]
	}
}

// joinBNOr is the same with "or" — "ক, খ বা গ".
func joinBNOr(in []string) string {
	switch len(in) {
	case 0:
		return ""
	case 1:
		return in[0]
	case 2:
		return in[0] + " বা " + in[1]
	default:
		return strings.Join(in[:len(in)-1], ", ") + " বা " + in[len(in)-1]
	}
}

// classCodes renders class codes readably without the "any" the English phrase carries — the
// Bengali sentence puts "শ্রেণির ওষুধ" after the list instead.
func classCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, strings.ToLower(strings.ReplaceAll(c, "_", " ")))
	}
	return out
}

func joinOrEN(in []string) string {
	switch len(in) {
	case 0:
		return ""
	case 1:
		return in[0]
	case 2:
		return in[0] + " or " + in[1]
	default:
		return strings.Join(in[:len(in)-1], ", ") + " or " + in[len(in)-1]
	}
}

var datumEN = map[Datum]string{
	DatumEGFR:        "a recent eGFR",
	DatumAge:         "the patient's age",
	DatumPregnancy:   "pregnancy status",
	DatumHepatic:     "an assessment of liver function",
	DatumDiagnoses:   "the coded diagnosis list",
	DatumAllergies:   "a recorded allergy status",
	DatumDailyDose:   "the total daily dose",
	DatumCurrentMeds: "the current medication list",
}

var datumBN = map[Datum]string{
	DatumEGFR:        "সাম্প্রতিক eGFR",
	DatumAge:         "রোগীর বয়স",
	DatumPregnancy:   "গর্ভাবস্থার তথ্য",
	DatumHepatic:     "যকৃতের কার্যকারিতার মূল্যায়ন",
	DatumDiagnoses:   "কোডকৃত রোগনির্ণয়ের তালিকা",
	DatumAllergies:   "নথিভুক্ত অ্যালার্জির তথ্য",
	DatumDailyDose:   "দৈনিক মোট মাত্রা",
	DatumCurrentMeds: "বর্তমান ওষুধের তালিকা",
}
