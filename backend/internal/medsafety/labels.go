package medsafety

import "sort"

// The words the authoring form is built from (CP77).
//
// Here rather than in the web application, and in both languages, for the reason the preview is
// rendered on the server: the form's field list and the validator's rules have to come from the
// same table, or a physician composes a rule the server then refuses and has no idea why. The
// labels travel with the list.
//
// The Bengali is mine. D-24 has Dr. Nahid reviewing the clinical register, and every string in
// this file belongs in that review — "duplicate therapy" and "contraindication" have settled
// Bengali forms among Bangladeshi physicians and mine may not be them.

var typeNameEN = map[RuleType]string{
	TypeInteraction:      "Drug interaction",
	TypeContraindication: "Contraindication",
	TypeRenal:            "Kidney function",
	TypeHepatic:          "Liver function",
	TypePregnancy:        "Pregnancy",
	TypePaediatric:       "Age",
	TypeDuplicateTherapy: "Duplicate therapy",
	TypeMaxDose:          "Maximum dose",
}

var typeNameBN = map[RuleType]string{
	TypeInteraction:      "ওষুধের পারস্পরিক প্রতিক্রিয়া",
	TypeContraindication: "নিষেধ",
	TypeRenal:            "কিডনির কার্যকারিতা",
	TypeHepatic:          "যকৃতের কার্যকারিতা",
	TypePregnancy:        "গর্ভাবস্থা",
	TypePaediatric:       "বয়স",
	TypeDuplicateTherapy: "একই চিকিৎসার পুনরাবৃত্তি",
	TypeMaxDose:          "সর্বোচ্চ মাত্রা",
}

// The hints are written as the question the author is actually answering, not as a definition.
// "This drug with that drug" is what a physician is thinking; "a rule expressing a pharmacokinetic
// or pharmacodynamic interaction" is what a textbook says and what nobody reads twice.
var typeHintEN = map[RuleType]string{
	TypeInteraction:      "This medicine together with that one.",
	TypeContraindication: "This medicine when the patient has that diagnosis or that allergy.",
	TypeRenal:            "This medicine when the kidneys are not clearing it.",
	TypeHepatic:          "This medicine when the liver is not handling it.",
	TypePregnancy:        "This medicine in pregnancy, while breastfeeding, or when pregnancy is planned.",
	TypePaediatric:       "This medicine below a certain age.",
	TypeDuplicateTherapy: "The same molecule, or the same class, given twice.",
	TypeMaxDose:          "More of this medicine in a day than the label allows.",
}

var typeHintBN = map[RuleType]string{
	TypeInteraction:      "এই ওষুধটির সঙ্গে ওই ওষুধটি।",
	TypeContraindication: "রোগীর ওই রোগনির্ণয় বা ওই অ্যালার্জি থাকলে এই ওষুধটি।",
	TypeRenal:            "কিডনি ওষুধটি বের করতে না পারলে।",
	TypeHepatic:          "যকৃৎ ওষুধটি সামলাতে না পারলে।",
	TypePregnancy:        "গর্ভাবস্থায়, স্তন্যদানের সময়, বা গর্ভধারণের পরিকল্পনা থাকলে এই ওষুধটি।",
	TypePaediatric:       "একটি নির্দিষ্ট বয়সের নিচে এই ওষুধটি।",
	TypeDuplicateTherapy: "একই অণু, বা একই শ্রেণি, দুবার দেওয়া।",
	TypeMaxDose:          "দিনে অনুমোদিত মাত্রার চেয়ে বেশি।",
}

// predicateOrder is the order the form lists the tests in. Stable, because a form whose fields
// move between two visits is one people mis-fill.
var predicateOrder = []PredicateKind{
	PredCoPrescribed, PredDuplicate, PredDiagnosis, PredAllergy,
	PredEGFR, PredHepatic, PredPregnancy, PredAge, PredDailyDose,
}

var predicateNameEN = map[PredicateKind]string{
	PredCoPrescribed: "another medicine is being given",
	PredDuplicate:    "the same thing is being given twice",
	PredDiagnosis:    "the patient has a diagnosis",
	PredAllergy:      "the patient has an allergy",
	PredEGFR:         "the eGFR is",
	PredHepatic:      "hepatic impairment is",
	PredPregnancy:    "the patient is",
	PredAge:          "the patient's age is",
	PredDailyDose:    "the total daily dose is",
}

var predicateNameBN = map[PredicateKind]string{
	PredCoPrescribed: "সঙ্গে আরেকটি ওষুধ দেওয়া হচ্ছে",
	PredDuplicate:    "একই জিনিস দুবার দেওয়া হচ্ছে",
	PredAllergy:      "রোগীর অ্যালার্জি আছে",
	PredDiagnosis:    "রোগীর রোগনির্ণয় আছে",
	PredEGFR:         "eGFR",
	PredHepatic:      "যকৃতের দুর্বলতা",
	PredPregnancy:    "রোগী",
	PredAge:          "রোগীর বয়স",
	PredDailyDose:    "দৈনিক মোট মাত্রা",
}

func sortStrings(in []string) { sort.Strings(in) }
