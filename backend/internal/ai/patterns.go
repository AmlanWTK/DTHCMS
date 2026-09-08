package ai

// The free-text scrubber's rules, in Go.
//
// This list and `ops.pii_pattern` are two representations of one thing, and the second one is not
// decoration: `core.ai_interaction.outbound` carries a check constraint that runs these expressions
// in the database, so a payload the Go scrubber missed cannot be *recorded* — and because the
// record is written before the provider is contacted, a payload that cannot be recorded is never
// sent. Two enforcements of one rule, in two engines.
//
// Which is exactly why the two copies have to be held together by something better than care.
// TestTheScrubberAgreesWithTheDatabase compares the lists in both directions and then runs a
// fixture corpus through both engines, failing when they disagree about any string. Two regular
// expression implementations agreeing about a list of patterns is not something to assume: they are
// different engines with different syntax, and the parts they share are the parts these expressions
// are written in — character classes, bounded repetition, non-capturing groups, and `(?i)` at the
// front. Nothing else.
//
// # Why they are wide
//
// Redacting a laboratory accession number because it happens to be nine digits long costs the model
// a little context. Letting a national ID through costs a patient their privacy and, under D-07's
// free tier, puts it in front of a human reviewer outside Bangladesh. D-08's default deny means the
// first is the error to make.
//
// The thresholds are chosen against real clinical prose rather than by feel. Seven digits with
// nothing between them: a national ID is ten, thirteen or seventeen, a mobile number is eleven, and
// nothing clinical is seven — a haemoglobin is three characters and a date has no run longer than
// four. Nine digits when separators are allowed: "BP 120 80, pulse 72" is eight, and a rule that
// redacted that would hand the physician a summary they cannot read, which is its own kind of
// failure.
var DefaultPatterns = []Pattern{
	{
		Kind:          "digit_run_bangla",
		Expression:    `[০-৯]{7,}`,
		Replacement:   "[NUMBER]",
		DescriptionEN: "A run of seven or more Bangla digits",
		DescriptionBN: "সাতটি বা তার বেশি বাংলা সংখ্যার ধারা",
	},
	{
		Kind:          "digit_run_latin",
		Expression:    `[0-9]{7,}`,
		Replacement:   "[NUMBER]",
		DescriptionEN: "A run of seven or more digits: a national ID, a mobile number, an account number",
		DescriptionBN: "সাতটি বা তার বেশি সংখ্যার ধারা: জাতীয় পরিচয়পত্র, মোবাইল নম্বর বা হিসাব নম্বর",
	},
	{
		Kind:          "email",
		Expression:    `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+[.][A-Za-z]{2,}`,
		Replacement:   "[EMAIL]",
		DescriptionEN: "An email address",
		DescriptionBN: "একটি ইমেইল ঠিকানা",
	},
	{
		// The `${1}` puts back the character the boundary group consumed. Neither engine can spell
		// a word boundary in a way the other understands — PostgreSQL has `\y`, Go has `\b` — so
		// the portable form is to consume it and replace it. Without the boundary this matches the
		// "ms " at the end of "symptoms include" and returns "sympto[NAME]" to the model.
		Kind:          "honorific_name",
		Expression:    `(?i)(^|[^a-z])(mr|mrs|ms|md|mst|dr|prof)[.]? +[a-z]+`,
		Replacement:   "${1}[NAME]",
		DescriptionEN: "A name written with an honorific: Md., Mst., Dr., Mr.",
		DescriptionBN: "সম্মানসূচক পদবি সহ লেখা নাম: মো., মোসা., ডা., জনাব",
	},
	{
		Kind:          "separated_number_bangla",
		Expression:    `(?:[০-৯][ .()+-]{0,2}){9,}`,
		Replacement:   "[NUMBER]",
		DescriptionEN: "The same, written in Bangla numerals",
		DescriptionBN: "একই জিনিস, বাংলা সংখ্যায় লেখা",
	},
	{
		Kind:          "separated_number_latin",
		Expression:    `(?:[0-9][ .()+-]{0,2}){9,}`,
		Replacement:   "[NUMBER]",
		DescriptionEN: "Nine or more digits separated by spaces, dots, brackets, plus or hyphen: a written-out phone number",
		DescriptionBN: "ফাঁকা, বিন্দু, বন্ধনী, যোগ বা হাইফেন দিয়ে লেখা নয় বা তার বেশি সংখ্যা: হাতে লেখা ফোন নম্বর",
	},
}
