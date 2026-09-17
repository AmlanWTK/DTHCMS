// Package clinicalterm turns the strings this system stores into the strings a clinician reads.
//
// # Why this exists as a package rather than a helper in whichever module needed it first
//
// Two checkpoints independently put an internal code on a screen, because the code was the string
// to hand. CP83's QA review said *"no HBA1C in the last 6 months, and none ordered"*; CP82's
// suggestion panel said *"obs.hba1c:2026-09-01 · obs.egfr:2026-09-01"*. Neither is a translation
// bug or a typo — both are what you get when a screen is written by somebody holding a
// `[]string` of codes and no way to ask what they are called. A third checkpoint would have done
// it again.
//
// So the answer is not two fixes; it is one place that owns the question **what is this code
// called, in the language the reader is reading**, and two callers that use it. That is this
// package. It is a leaf: it depends on `platform` and on nothing clinical, so every module that
// renders a code can reach it without the dependency graph acquiring a cycle.
//
// # The code does not disappear, it stops being the headline
//
// Somebody debugging a rule at eight in the evening needs `CHOL_LDL`, and a screen that has
// thrown it away has traded one person's problem for another's. Every [Term] keeps its Code, and
// the callers render it as a detail — a title attribute, a second line — so the officer reads
// English and the engineer can still see the handle.
//
// # No PHI
//
// A code is not a patient. Nothing here takes a patient identifier, a name or a value, and the
// lexicon is facility-independent reference data that could be printed on a poster.
package clinicalterm

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// A term
// ---------------------------------------------------------------------------

// Term is one coded thing with the two names a reader of this system may need.
//
// Both languages always, never one with a translation applied on the way out: a finding that
// appears in English for an officer who works in Bangla has not appeared.
type Term struct {
	// Code is the internal handle — `HBA1C`, `CHOL_LDL`. Kept so a caller can offer it as a
	// detail, never as the sentence.
	Code string
	EN   string
	BN   string
}

// Text returns the name in one of the two languages this clinic works in. Anything that is not
// "bn" is English, because an unknown locale rendering nothing would be worse than an unknown
// locale rendering the language most of the software is written in.
func (t Term) Text(lang string) string {
	if lang == "bn" && strings.TrimSpace(t.BN) != "" {
		return t.BN
	}
	return t.EN
}

// ---------------------------------------------------------------------------
// The lexicon
// ---------------------------------------------------------------------------

// Lexicon is the facility's observation catalogue, by code.
//
// A value and not a pointer, and safe to copy: it is built once and never written to afterwards.
// [Load] takes the snapshot; the zero value is a working lexicon that knows nothing and falls
// back for every code, which is what a unit test wants and what a process that failed to load
// gets rather than a nil dereference.
type Lexicon struct {
	obs map[string]Term
}

// Load reads `core.observation_code`.
//
// Retired codes are included deliberately. A finding may name a code that was withdrawn last
// year — an old order, a rule nobody updated — and the reader still needs to know what it was
// called. Withdrawing a code removes it from the pickers, not from the past.
func Load(ctx context.Context, pool *pgxpool.Pool) (Lexicon, error) {
	rows, err := pool.Query(ctx,
		`SELECT code, display_en, display_bn FROM core.observation_code`)
	if err != nil {
		return Lexicon{}, err
	}
	defer rows.Close()

	out := Lexicon{obs: map[string]Term{}}
	for rows.Next() {
		var t Term
		if err := rows.Scan(&t.Code, &t.EN, &t.BN); err != nil {
			return Lexicon{}, err
		}
		out.obs[t.Code] = t
	}
	return out, rows.Err()
}

// Observation is what to call one observation code.
//
// **It never returns the raw code as the name.** An unknown code — a typo in a rule row, a code
// from a migration that has not run here — renders as its words with the underscores gone, which
// is imperfect English and is still a sentence a person can read. Returning `CHOL_LDL` would put
// us back exactly where this package started, and the failure would look like content rather
// than like the missing row it is.
func (l Lexicon) Observation(code string) Term {
	code = strings.TrimSpace(code)
	if t, ok := l.obs[code]; ok {
		return t
	}
	spelled := Spell(code)
	return Term{Code: code, EN: spelled, BN: spelled}
}

// Known reports whether this code is in the catalogue. Callers that want to say something
// different about a code nobody has defined use this rather than comparing against [Spell].
func (l Lexicon) Known(code string) bool {
	_, ok := l.obs[strings.TrimSpace(code)]
	return ok
}

// Spell turns an internal handle into words.
//
// `CHOL_LDL` becomes "Chol ldl" and `MONOFILAMENT_LEFT` becomes "Monofilament left". Not good
// English, and it is not meant to be — it is the honest fallback for a code the catalogue does
// not define, and a rule whose finding reads like that is a rule whose row needs fixing. The
// point is only that it does not read like a database column on a screen a clinician is holding
// in front of a patient.
func Spell(code string) string {
	words := strings.FieldsFunc(strings.ToLower(code), func(r rune) bool {
		return r == '_' || r == '.' || r == '-' || unicode.IsSpace(r)
	})
	if len(words) == 0 {
		return ""
	}
	joined := strings.Join(words, " ")
	return strings.ToUpper(joined[:1]) + joined[1:]
}

// ---------------------------------------------------------------------------
// Lists
// ---------------------------------------------------------------------------

// ListEN joins names the way a person says a short list: "a, b or c".
//
// "or" and not "and" because every caller of this today is asking whether **any one** of several
// codes satisfies a rule. A caller that means "and" should not reach for this function; it should
// say so on the rule, which is what [Lexicon] cannot know and the row can.
func ListEN(names []string) string { return join(names, ", ", " or ") }

// ListBN is the same list in Bangla.
func ListBN(names []string) string { return join(names, ", ", " বা ") }

func join(names []string, sep, last string) string {
	kept := make([]string, 0, len(names))
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			kept = append(kept, strings.TrimSpace(n))
		}
	}
	switch len(kept) {
	case 0:
		return ""
	case 1:
		return kept[0]
	}
	return strings.Join(kept[:len(kept)-1], sep) + last + kept[len(kept)-1]
}

// ---------------------------------------------------------------------------
// Windows
// ---------------------------------------------------------------------------

// WindowEN says how far back a rule looked, the way somebody says it out loud.
//
// Two things it fixes, both of which were on a screen. "in the last 180 days" is nobody's
// description of six months, and **"in the last 1 year" is not a phrase in the language** — the
// number one before a singular noun is a machine counting. So a window of exactly one unit drops
// the number entirely ("the last year"), and the rest are spelled in words up to ten, because
// "the last six months" is prose and "the last 6 months" is a field.
func WindowEN(days int) string {
	switch n, unit := windowOf(days); {
	case n == 1:
		return "the last " + unit
	default:
		return "the last " + Count(n) + " " + unit + "s"
	}
}

// WindowBN is the same in Bangla.
//
// Bengali numerals rather than words, which is the opposite choice from English and is right for
// the same reason: a Bangla clinical sentence carrying Latin digits reads half-translated, and
// Bengali digits are how the number is written. One drops the numeral as English does — "গত
// বছরে", not "গত ১ বছরে".
func WindowBN(days int) string {
	n, unit := windowOf(days)
	bnUnit := map[string]string{"day": "দিনে", "month": "মাসে", "year": "বছরে"}[unit]
	if n == 1 {
		return "গত " + bnUnit
	}
	return "গত " + Digits(n) + " " + bnUnit
}

// windowOf reduces a day count to the largest whole unit that divides it. 180 is six months;
// 181 is 181 days, because a window nobody chose round is a window whose exact size is the point.
func windowOf(days int) (int, string) {
	switch {
	case days%365 == 0 && days >= 365:
		return days / 365, "year"
	case days%30 == 0 && days >= 30:
		return days / 30, "month"
	default:
		return days, "day"
	}
}

// Count writes a small number in English words.
//
// Up to ten, because that is where written English stops spelling and starts numbering, and
// because every window this clinic uses — six months, twelve months, three days — is inside it.
func Count(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five",
		"six", "seven", "eight", "nine", "ten"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return itoa(n)
}

// Digits writes a number in Bengali digits.
func Digits(n int) string {
	if n == 0 {
		return "০"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []rune("০১২৩৪৫৬৭৮৯")
	var out []rune
	for n > 0 {
		out = append([]rune{digits[n%10]}, out...)
		n /= 10
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// Dates
// ---------------------------------------------------------------------------

// monthsEN and monthsBN are the short forms a clinician writes on a slip. Not `time.Format`,
// because Go's layout has no Bengali and a date rendered in one language beside a sentence in the
// other is the half-translated screen this package exists to stop.
var monthsEN = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun",
	"Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

var monthsBN = [...]string{"জানু", "ফেব্রু", "মার্চ", "এপ্রিল", "মে", "জুন",
	"জুলাই", "আগস্ট", "সেপ্ট", "অক্টো", "নভে", "ডিসে"}

// DateEN renders a day the way the reader's eye expects it: "1 Sep 2026", not "2026-09-01".
//
// ISO is a storage format. It is unambiguous, which is why it is stored, and it is also the
// format in which nobody says a date aloud — and CP82's panel was showing it to a physician
// scanning three suggestions in five seconds.
func DateEN(t time.Time) string {
	return itoa(t.Day()) + " " + monthsEN[int(t.Month())-1] + " " + itoa(t.Year())
}

// DateBN is the same date in Bengali digits and month names.
func DateBN(t time.Time) string {
	return Digits(t.Day()) + " " + monthsBN[int(t.Month())-1] + " " + Digits(t.Year())
}

// ---------------------------------------------------------------------------
// Fact references (CP71/CP82)
// ---------------------------------------------------------------------------

// Reference is one CP71 fact reference rendered for a person.
//
// The raw reference is kept because it is the thing CP72's grounding arm checked and the thing an
// engineer greps for; [Reference.EN] and [Reference.BN] are what goes on the screen.
type Reference struct {
	Raw string
	EN  string
	BN  string
}

// Refer renders a fact reference such as `obs.hba1c:2026-09-01` as "HbA1c, 1 Sep 2026".
//
// # Why this parses a string instead of being handed the fact
//
// It would be better to render from the [Fact] the model was shown, which carries a label
// already. The suggestion row does not store the facts — it stores the references, because those
// are what grounding validated — and a context assembled eight weeks ago may no longer be
// reconstructible. So the reference is parsed, which is lossy in one specific way worth stating:
// the slug is a *slugged label*, so `obs.hba1c` finds the catalogue entry for `HBA1C` and
// `history.family_diabetes` does not find anything and falls back to its words. Observations are
// the kind that matter here and the kind the catalogue can answer; everything else reads as the
// label it was made from, which is what it already was.
func Refer(lex Lexicon, raw string) Reference {
	out := Reference{Raw: strings.TrimSpace(raw)}
	body, date := out.Raw, ""
	if at := strings.LastIndex(out.Raw, ":"); at >= 0 {
		body, date = out.Raw[:at], out.Raw[at+1:]
	}

	kind, slug := "", body
	if dot := strings.Index(body, "."); dot >= 0 {
		kind, slug = body[:dot], body[dot+1:]
	}

	// A duplicate within one context acquires a letter suffix — `obs.hba1c:2026-09-01.b` is the
	// repeat after a suspicious first reading. The suffix is not part of the name of the thing,
	// and a screen that rendered "HbA1c b" would be inventing a test.
	if dot := strings.LastIndex(date, "."); dot >= 0 {
		date = date[:dot]
	}

	var term Term
	switch kind {
	case "obs", "change":
		term = lex.Observation(strings.ToUpper(slug))
	default:
		spelled := Spell(slug)
		term = Term{Code: out.Raw, EN: spelled, BN: spelled}
	}
	out.EN, out.BN = term.EN, term.BN
	if out.EN == "" {
		out.EN, out.BN = Spell(out.Raw), Spell(out.Raw)
	}

	if on, err := time.Parse("2006-01-02", date); err == nil {
		out.EN += ", " + DateEN(on)
		out.BN += ", " + DateBN(on)
	}
	return out
}

// ---------------------------------------------------------------------------
// A cache for a process
// ---------------------------------------------------------------------------

// Cache holds one lexicon for the life of a process, loaded on first use.
//
// The catalogue is reference data that changes when somebody runs a migration, so a process-long
// snapshot is right and a per-request query would be a join nobody needs on every screen. The
// honest limit: a code added by a migration while the server is running is spelled rather than
// named until the next restart, which is a worse sentence and not a wrong one.
//
// A failure to load is not cached. A database that was briefly unreachable should not leave every
// screen in the process spelling codes for the rest of the day.
type Cache struct {
	pool *pgxpool.Pool

	mu     sync.RWMutex
	loaded bool
	lex    Lexicon
}

// NewCache builds one. A nil pool is legal and answers the empty lexicon, so a service assembled
// without a database in a unit test renders spelled codes rather than panicking.
func NewCache(pool *pgxpool.Pool) *Cache { return &Cache{pool: pool} }

// Get returns the lexicon, loading it once.
func (c *Cache) Get(ctx context.Context) Lexicon {
	if c == nil || c.pool == nil {
		return Lexicon{}
	}
	c.mu.RLock()
	if c.loaded {
		defer c.mu.RUnlock()
		return c.lex
	}
	c.mu.RUnlock()

	lex, err := Load(ctx, c.pool)
	if err != nil {
		return Lexicon{}
	}
	c.mu.Lock()
	c.lex, c.loaded = lex, true
	c.mu.Unlock()
	return lex
}

// ---------------------------------------------------------------------------
// Counting things on a screen
// ---------------------------------------------------------------------------

// PluralEN counts something in English the way a person writes it: "one check", "three checks".
//
// The digit is not wrong, it is just not prose, and these strings are sentences an officer reads
// rather than fields they scan. Above ten the digit comes back, because "seventeen blocking
// findings" is harder to take in at a glance than "17".
func PluralEN(n int, one, many string) string {
	if n == 1 {
		return Count(1) + " " + one
	}
	return Count(n) + " " + many
}

// Sentence capitalises the first letter, for a phrase that is being used to start one.
//
// Only the first rune, and only if it has an upper case: Bengali has no case, so this is a no-op
// on Bangla and does not need a language argument.
func Sentence(s string) string {
	for i, r := range s {
		up := unicode.ToUpper(r)
		if up == r {
			return s
		}
		return string(up) + s[i+len(string(r)):]
	}
	return s
}

// New builds a lexicon from a fixed list, for a test or for a caller that has the names already.
//
// [Load] is the production path. This exists so that a unit test of a rendering can assert
// against the clinic's real display names instead of against the fallback, which is what a test
// written around the spelled form would quietly become a test of.
func New(terms ...Term) Lexicon {
	out := Lexicon{obs: make(map[string]Term, len(terms))}
	for _, t := range terms {
		out.obs[t.Code] = t
	}
	return out
}
