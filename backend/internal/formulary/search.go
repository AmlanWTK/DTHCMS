package formulary

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// The two-letter prescribing autocomplete's matcher and its ranking (CP76, §10.1).
//
// # What is being ranked is a brand, not a row
//
// Levothyroxine is stocked here as Thyrox in six strengths and Thyrin in four. A result list of
// products would answer "th" with ten rows of levothyroxine before it reached Thyzol, and the
// checkpoint's own manual verification — *the intended drug is in the top three* — would be
// unreachable for any brand whose molecule has more than three strengths in the formulary.
//
// So a result is a **brand**: one trade name in one form, carrying its strengths. The physician
// chooses the medicine and then the strength, which is the order he chooses them in anyway, and
// it is the shape CP81 needs to hang a smart default dose on. The cost is that "Thyrox 50 mcg"
// is two keystrokes rather than one, and that is the trade this makes deliberately.
//
// The group key is the trade name **and the form**, because "Comet" and "Comet XR" are different
// medicines with different dosing, and because within one brand and form a strength can still
// repeat across dispensing units — Ansulin R 100 IU/mL is a vial and a cartridge at prices two
// hundred taka apart.
//
// # Everything is matched on a folded alphabet
//
// Both the query and the index are pushed through [fold], which lowercases, drops punctuation
// and collapses the spellings a physician cannot be expected to distinguish at two letters:
// c/k/q, s/z, f/ph, v/w/b, y/i, x/ks, and doubled letters. "Ci" and "ki" are the same two
// keystrokes as far as this is concerned, which is right — there is no brand in this formulary
// where telling them apart helps anybody, and there are several where not telling them apart
// saves a failed search.
//
// # Bengali script
//
// A physician typing on a Bengali keyboard types phonetically. The query is transliterated
// grapheme by grapheme into Latin and folded like everything else. That alone is not enough,
// because Bengali writes no inherent vowel: কমেট transliterates to "kmet" and will never be a
// prefix of "komet". So a Bengali query is *also* matched on the consonant skeleton — the folded
// form with its vowels removed — of which "km" is a prefix of Comet's "kmt".
//
// The skeleton is only used for Bengali queries. Turning it on for Latin ones would make "ms"
// match "Metformin", which is not a search, it is a lottery.
//
// # What is not searched
//
// The manufacturer. The admin list matches it (a pharmacist does ask "what do we have from
// Square"); the prescribing autocomplete does not, because "In" would then return every Incepta
// product in the formulary and bury Insulatard under sixty rows of things whose names do not
// begin with those letters.

// MatchKind names why an entry matched, in the order the ranking prefers them.
//
// It is returned to the client as well as used for ordering. A physician who cannot see why a
// result is where it is has no way to learn to type the two letters that get him what he wants,
// and a ranking nobody can predict is one people stop trusting and start scrolling past.
type MatchKind string

const (
	// MatchTradePrefix — the brand begins with what was typed. "co" → Comet.
	MatchTradePrefix MatchKind = "TRADE_PREFIX"
	// MatchGenericPrefix — the molecule begins with it. "me" → Metformin, so every metformin.
	MatchGenericPrefix MatchKind = "GENERIC_PREFIX"
	// MatchTradeWord — a later word of the brand begins with it. "pl" → Angilock Plus.
	MatchTradeWord MatchKind = "TRADE_WORD"
	// MatchGenericWord — a later word of the molecule does. "hy" → Losartan + Hydrochlorothiazide.
	MatchGenericWord MatchKind = "GENERIC_WORD"
	// MatchTradeContains — it appears inside the brand. "mul" → Humulin.
	MatchTradeContains MatchKind = "TRADE_CONTAINS"
	// MatchGenericContains — inside the molecule.
	MatchGenericContains MatchKind = "GENERIC_CONTAINS"
	// MatchTrigram — neither, but the trigrams overlap enough to be a misspelling. Four
	// characters and up only; below that a trigram set is one trigram and every word in the
	// formulary is 34% similar to something.
	MatchTrigram MatchKind = "TRIGRAM"
)

// tier orders the kinds. Lower is better.
func (k MatchKind) tier() int {
	switch k {
	case MatchTradePrefix:
		return 0
	case MatchGenericPrefix:
		return 1
	case MatchTradeWord:
		return 2
	case MatchGenericWord:
		return 3
	case MatchTradeContains:
		return 4
	case MatchGenericContains:
		return 5
	default:
		return 6
	}
}

// class is the coarse band the ranking sorts on before it looks at anything else: a prefix
// match of any sort beats a substring match of any sort, and both beat a fuzzy one.
//
// Coarse deliberately. The plan's ranking is "recent use by this physician, then frequency,
// then alphabetical", and putting the fine tier above recency would mean a drug he prescribed
// this morning loses to one he has never prescribed because the latter's brand happens to begin
// with the two letters rather than its molecule. Prefix-over-trigram is a statement about
// whether the match is real; which *kind* of prefix it is, is a tie-break.
func (k MatchKind) class() int {
	switch k.tier() {
	case 0, 1, 2, 3:
		return 0
	case 4, 5:
		return 1
	default:
		return 2
	}
}

// A Bengali-script query that matched through the consonant skeleton rather than through the
// transliteration is still a prefix match — it is how the script works, not a weaker signal —
// so it carries the same MatchKind and is separated only by [rankKey]'s final tie-break.

// SearchStrength is one orderable line: a strength, a dispensing unit, and what it costs.
type SearchStrength struct {
	ProductID    uuid.UUID `json:"product_id"`
	Strength     string    `json:"strength"`
	DispenseUnit string    `json:"dispense_unit"`
	UnitEN       string    `json:"unit_name_en"`
	UnitBN       string    `json:"unit_name_bn"`

	// Price is nil for a product nobody has priced yet. Shown as such rather than omitted: a
	// medicine the clinic stocks and has not priced is still a medicine the physician can
	// prescribe, and hiding it would teach him the formulary is missing things.
	Price *SearchPrice `json:"price,omitempty"`
}

// SearchPrice is the current price, and whether anybody has checked it.
//
// Both, always, for the reason CP75 gives: all 250 seeded prices are published MRP and nobody at
// this clinic has confirmed one. A number drawn without its verification state is a number the
// screen is implying somebody approved.
type SearchPrice struct {
	// Both forms, for the reason [Price] carries both: a client that divides the integer by a
	// hundred itself is a client that does it in floating point, and 0.34 is the value that
	// gets wrong.
	AmountPoisha Money  `json:"amount_poisha"`
	AmountBDT    string `json:"amount_bdt"`
	Verification string `json:"verification"`
	From         string `json:"effective_from"`
}

// SearchEntry is one brand in one form — what the physician picks.
type SearchEntry struct {
	TradeName    string `json:"trade_name"`
	GenericName  string `json:"generic_name"`
	Manufacturer string `json:"manufacturer"`

	ClassCode string `json:"class_code"`
	ClassEN   string `json:"class_name_en"`
	ClassBN   string `json:"class_name_bn"`

	FormCode string `json:"form_code"`
	FormEN   string `json:"form_name_en"`
	FormBN   string `json:"form_name_bn"`

	Strengths []SearchStrength `json:"strengths"`

	// Match is why this entry is here. Returned as well as used for ordering: a physician who
	// cannot see why a result is where it is has no way to learn which two letters get him
	// what he wants.
	Match MatchKind `json:"match"`

	// TimesPrescribed and DaysSinceLast are the CP80 signals, and are 0 and nil until CP80
	// exists. See [UsageSource].
	TimesPrescribed int  `json:"times_prescribed"`
	DaysSinceLast   *int `json:"days_since_last"`
}

// SearchResult is what the endpoint answers with.
type SearchResult struct {
	Query string `json:"query"`
	// Normalised is what the query became after transliteration and folding. Returned because
	// the Bengali case is otherwise unexplainable to the person typing: they typed কম and got
	// Comet, and this is the line that says why.
	Normalised string        `json:"normalised_query"`
	Entries    []SearchEntry `json:"entries"`
	// Total is how many brands matched before the limit. A count, never the rows.
	Total int `json:"total"`
	// RankingComplete is false while the per-physician signals are stubbed. The client draws a
	// note from it rather than silently presenting an alphabetical list as a personalised one.
	RankingComplete bool `json:"ranking_complete"`
	// ServedFrom is "cache" always today, and exists so a latency investigation can tell a
	// slow response from a cold one.
	ServedFrom string `json:"served_from"`
	// AgeSeconds is how long ago the cache this was served from was last refreshed.
	AgeSeconds int `json:"cache_age_seconds"`
}

// ---------------------------------------------------------------------------
// The index
// ---------------------------------------------------------------------------

// indexEntry is a brand group with its match keys precomputed.
//
// Precomputed because they are the same for every query and there are a few hundred of them:
// folding 250 trade names on every keystroke would be most of the work this endpoint does, and
// it is work whose answer never changes between two refreshes of the cache.
type indexEntry struct {
	entry SearchEntry

	tradeFolded   string
	tradeWords    []string
	tradeSkeleton string
	tradeTrigrams map[string]struct{}

	genericFolded   string
	genericWords    []string
	genericSkeleton string

	// productIDs is every product in this group, for the usage lookup.
	productIDs []uuid.UUID
}

func buildIndexEntry(e SearchEntry) *indexEntry {
	ix := &indexEntry{entry: e}
	ix.tradeFolded = fold(e.TradeName)
	ix.tradeWords = foldWords(e.TradeName)
	ix.tradeSkeleton = skeleton(ix.tradeFolded)
	ix.tradeTrigrams = trigrams(ix.tradeFolded)
	ix.genericFolded = fold(e.GenericName)
	ix.genericWords = foldWords(e.GenericName)
	ix.genericSkeleton = skeleton(ix.genericFolded)
	for _, s := range e.Strengths {
		ix.productIDs = append(ix.productIDs, s.ProductID)
	}
	return ix
}

// match decides whether this entry matches the query, and how.
//
// `skel` is the query's consonant skeleton, and is empty unless the query was written in
// Bengali script — see the package note on why it is not used for Latin queries.
func (ix *indexEntry) match(q, skel string) (MatchKind, bool) {
	switch {
	case strings.HasPrefix(ix.tradeFolded, q):
		return MatchTradePrefix, true
	case strings.HasPrefix(ix.genericFolded, q):
		return MatchGenericPrefix, true
	}
	if skel != "" {
		if strings.HasPrefix(ix.tradeSkeleton, skel) {
			return MatchTradePrefix, true
		}
		if strings.HasPrefix(ix.genericSkeleton, skel) {
			return MatchGenericPrefix, true
		}
	}
	for _, w := range ix.tradeWords[1:] {
		if strings.HasPrefix(w, q) {
			return MatchTradeWord, true
		}
	}
	for _, w := range ix.genericWords[1:] {
		if strings.HasPrefix(w, q) {
			return MatchGenericWord, true
		}
	}
	if strings.Contains(ix.tradeFolded, q) {
		return MatchTradeContains, true
	}
	if strings.Contains(ix.genericFolded, q) {
		return MatchGenericContains, true
	}
	// The fuzzy last resort. Only from four characters: at three the query is one trigram, and
	// one trigram in common is not evidence of anything.
	if len([]rune(q)) >= 4 && dice(trigrams(q), ix.tradeTrigrams) >= 0.34 {
		return MatchTrigram, true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Ranking
// ---------------------------------------------------------------------------

// rankKey is the sort key, in the order the plan states and with the two stubbed terms marked.
//
//  1. class          — a prefix match beats a substring beats a trigram
//  2. daysSinceLast  — **CP80**. "recent use by this physician"
//  3. -times         — **CP80**. "then frequency"
//  4. tier           — trade-prefix before generic-prefix, and so on
//  5. tradeFolded    — "then alphabetical"
//  6. tradeName      — the unfolded name, so the order is total and stable
//
// Terms 2 and 3 are constant until [UsageSource] has something behind it, which means the order
// a physician sees today is (1, 4, 5, 6). That is stated in the response as
// `ranking_complete: false` rather than left to be discovered.
type rankKey struct {
	class         int
	daysSinceLast int
	times         int
	tier          int
	tradeFolded   string
	tradeName     string
}

// neverPrescribed is the days-since-last of a drug with no history. Large enough that anything
// with a real history sorts above it, small enough to be obviously a sentinel in a debug dump.
const neverPrescribed = 99999

func less(a, b rankKey) bool {
	if a.class != b.class {
		return a.class < b.class
	}
	if a.daysSinceLast != b.daysSinceLast {
		return a.daysSinceLast < b.daysSinceLast
	}
	if a.times != b.times {
		return a.times > b.times
	}
	if a.tier != b.tier {
		return a.tier < b.tier
	}
	if a.tradeFolded != b.tradeFolded {
		return a.tradeFolded < b.tradeFolded
	}
	return a.tradeName < b.tradeName
}

// searchIndex runs one query against a prepared index.
//
// Pure, and takes the usage as an argument rather than fetching it, so that the ranking is
// testable without a database and so that CP80's signal arrives by the same door a test's does.
func searchIndex(index []*indexEntry, raw string, limit int, usage Usage) SearchResult {
	q, skel := normaliseQuery(raw)
	out := SearchResult{Query: raw, Normalised: q, Entries: []SearchEntry{}}
	if q == "" {
		return out
	}

	type scored struct {
		entry SearchEntry
		key   rankKey
	}
	matches := make([]scored, 0, 32)
	for _, ix := range index {
		kind, ok := ix.match(q, skel)
		if !ok {
			continue
		}
		e := ix.entry
		e.Match = kind
		times, days := usage.forProducts(ix.productIDs)
		e.TimesPrescribed = times
		if days != neverPrescribed {
			d := days
			e.DaysSinceLast = &d
		}
		matches = append(matches, scored{entry: e, key: rankKey{
			class:         kind.class(),
			daysSinceLast: days,
			times:         times,
			tier:          kind.tier(),
			tradeFolded:   ix.tradeFolded,
			tradeName:     e.TradeName,
		}})
	}

	sort.SliceStable(matches, func(i, j int) bool { return less(matches[i].key, matches[j].key) })

	out.Total = len(matches)
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	for _, m := range matches {
		out.Entries = append(out.Entries, m.entry)
	}
	return out
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

// normaliseQuery returns the folded query and, for a Bengali-script query, its consonant
// skeleton. The skeleton is empty for a Latin query; see the package note.
func normaliseQuery(raw string) (q, skel string) {
	bengali := false
	for _, r := range raw {
		if r >= 0x0980 && r <= 0x09FF {
			bengali = true
			break
		}
	}
	q = fold(raw)
	if bengali {
		skel = skeleton(q)
	}
	return q, skel
}

// fold reduces text to the alphabet the matcher compares on.
//
// Order matters: transliterate first (so Bengali arrives as Latin), then lowercase, then
// collapse the confusable spellings, then drop everything that is not a letter or a digit, then
// collapse doubled letters. "Fiasp FlexTouch" → "phiaspphlekstouch"; "Humalog 100" → "humalog100".
func fold(s string) string {
	s = transliterateBengali(s)
	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case 'c', 'q':
			// "Comet" and "Kometh" are two keystrokes apart in a physician's memory and zero
			// apart on the page a patient brings in.
			b.WriteRune('k')
		case 'z':
			b.WriteRune('s')
		case 'v', 'w':
			b.WriteRune('b')
		case 'y':
			b.WriteRune('i')
		case 'x':
			b.WriteString("ks")
		case 'f':
			b.WriteString("ph")
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
	}

	// Doubled letters: "Ossulin" and "Osulin" are the same search.
	folded := b.String()
	var out strings.Builder
	out.Grow(len(folded))
	var prev rune = -1
	for _, r := range folded {
		if r == prev {
			continue
		}
		out.WriteRune(r)
		prev = r
	}
	return out.String()
}

// foldWords folds each word separately, so that a later word of a name can be matched on its
// own start. Always at least one element, so `words[1:]` is safe.
func foldWords(s string) []string {
	fields := strings.FieldsFunc(transliterateBengali(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields)+1)
	for _, f := range fields {
		if folded := fold(f); folded != "" {
			out = append(out, folded)
		}
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

// skeleton drops the vowels. The Bengali half of the matcher; see the package note.
func skeleton(folded string) string {
	var b strings.Builder
	for _, r := range folded {
		switch r {
		case 'a', 'e', 'i', 'o', 'u':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// trigrams is the set of three-character windows, with the word padded at both ends so that
// the start and the end of a word are themselves evidence.
func trigrams(s string) map[string]struct{} {
	padded := "  " + s + " "
	out := make(map[string]struct{}, len(padded))
	runes := []rune(padded)
	for i := 0; i+3 <= len(runes); i++ {
		out[string(runes[i:i+3])] = struct{}{}
	}
	return out
}

// dice is the Sørensen–Dice coefficient of two trigram sets: 2|A∩B| / (|A|+|B|).
//
// Dice rather than Jaccard because it is what PostgreSQL's pg_trgm reports, so a later decision
// to push this into the database — the one this checkpoint deliberately did not make — would
// not silently change which results appear.
func dice(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for k := range a {
		if _, ok := b[k]; ok {
			shared++
		}
	}
	return 2 * float64(shared) / float64(len(a)+len(b))
}

// transliterateBengali rewrites Bengali graphemes as their Latin phonetic equivalents.
//
// Grapheme by grapheme and with no inherent vowel inserted, which is what makes the consonant
// skeleton necessary — see the package note. Non-Bengali runes pass through untouched, so
// running it over Latin text is a no-op and it can sit unconditionally at the front of [fold].
//
// **The Bengali digits are mapped too**, because a keyboard set to Bengali produces them and a
// physician typing a strength should not have to switch layouts. They are mapped to ASCII for
// the reason the design system gives: measurements, doses and identifiers are ASCII in both
// interfaces, so two numeral systems never circulate for one number.
func transliterateBengali(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r >= 0x0980 && r <= 0x09FF }) {
		return s
	}
	// The nukta letters, which are a base consonant plus U+09BC and therefore two runes each.
	// Replaced as strings before the rune loop, because the map below is keyed by rune and
	// would otherwise transliterate ড় as ড and drop the mark that changes the sound.
	s = strings.NewReplacer("\u09a1\u09bc", "r", "\u09a2\u09bc", "rh", "\u09af\u09bc", "y").Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if latin, ok := bengaliToLatin[r]; ok {
			b.WriteString(latin)
			continue
		}
		if r >= 0x0980 && r <= 0x09FF {
			// An unmapped Bengali rune — a rare conjunct sign, a currency mark. Dropped
			// rather than passed through: leaving it in would make the folded form contain a
			// character no index entry can ever contain, so the query would match nothing at
			// all rather than matching a little less precisely.
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// bengaliToLatin is a phonetic map, not a scholarly transliteration.
//
// It is tuned for a physician typing a trade name he has seen written in Latin on a box: ফ is
// "ph" because Bangladeshis write Fiasp with it, not "f" with a diacritic. Everything here is
// folded immediately afterwards anyway, which is why ব and ভ can both be "b" without loss.
var bengaliToLatin = map[rune]string{
	// Independent vowels.
	'অ': "o", 'আ': "a", 'ই': "i", 'ঈ': "i", 'উ': "u", 'ঊ': "u", 'ঋ': "ri",
	'এ': "e", 'ঐ': "oi", 'ও': "o", 'ঔ': "ou",
	// Dependent vowel signs.
	'া': "a", 'ি': "i", 'ী': "i", 'ু': "u", 'ূ': "u", 'ৃ': "ri",
	'ে': "e", 'ৈ': "oi", 'ো': "o", 'ৌ': "ou",
	// Consonants.
	'ক': "k", 'খ': "kh", 'গ': "g", 'ঘ': "gh", 'ঙ': "ng",
	'চ': "ch", 'ছ': "chh", 'জ': "j", 'ঝ': "jh", 'ঞ': "n",
	'ট': "t", 'ঠ': "th", 'ড': "d", 'ঢ': "dh", 'ণ': "n",
	'ত': "t", 'থ': "th", 'দ': "d", 'ধ': "dh", 'ন': "n",
	'প': "p", 'ফ': "ph", 'ব': "b", 'ভ': "bh", 'ম': "m",
	'য': "j", 'র': "r", 'ল': "l", 'শ': "sh", 'ষ': "sh", 'স': "s", 'হ': "h",
	'ৎ': "t",
	// The nukta itself. The three letters written with one — ড়, ঢ় and য় — are folded to
	// their base consonant a step earlier, in [transliterateBengali], because Go stores each
	// of them as two runes and a rune-keyed map cannot see the pair.
	'\u09bc': "",
	// Signs. The hasant marks a conjunct and contributes nothing; chandrabindu nasalises and
	// contributes nothing a two-letter search can use.
	'্': "", 'ং': "ng", 'ঃ': "h", 'ঁ': "",
	// Digits.
	'০': "0", '১': "1", '২': "2", '৩': "3", '৪': "4",
	'৫': "5", '৬': "6", '৭': "7", '৮': "8", '৯': "9",
}

// ---------------------------------------------------------------------------
// Strength ordering
// ---------------------------------------------------------------------------

// sortStrengths puts a brand's strengths in the order a physician reads them: ascending by the
// number, and by the dispensing unit where the number ties.
//
// Ascending rather than alphabetical because "100 mcg, 12.5 mcg, 25 mcg, 50 mcg, 75 mcg" is what
// alphabetical gives for Thyrox, and a dose list in that order is one somebody misreads.
func sortStrengths(in []SearchStrength) {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := leadingNumber(in[i].Strength), leadingNumber(in[j].Strength)
		if a != b {
			return a < b
		}
		if in[i].Strength != in[j].Strength {
			return in[i].Strength < in[j].Strength
		}
		return in[i].DispenseUnit < in[j].DispenseUnit
	})
}

// leadingNumber reads the number a strength starts with. "12.5 mcg" → 12.5, "30% + 70%" → 30,
// "" → 0. Strengths in this formulary are free text by design (they have to carry "1358.196 mg
// + 600 mg (elemental calcium) + 400 IU"), so this is a sort key and not a parsed quantity —
// nothing clinical is ever decided from it.
func leadingNumber(s string) float64 {
	s = strings.TrimSpace(s)
	end := 0
	seenDot := false
	for i, r := range s {
		if unicode.IsDigit(r) {
			end = i + 1
			continue
		}
		if r == '.' && !seenDot && end == i {
			seenDot = true
			end = i + 1
			continue
		}
		break
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.ParseFloat(strings.TrimSuffix(s[:end], "."), 64)
	if err != nil {
		return 0
	}
	return n
}
