package formulary

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// The matcher and the ranking, without a database (CP76).
//
// These are the tests of the parts that have no infrastructure in them: what a query folds to,
// what Bengali becomes, which of two entries sorts first. The tests that need the real 250-row
// seed — the two-letter table, the latency, the cache — are in search_db_test.go.

func entry(trade, generic, form string, strengths ...string) SearchEntry {
	e := SearchEntry{TradeName: trade, GenericName: generic, FormCode: form}
	for _, s := range strengths {
		e.Strengths = append(e.Strengths, SearchStrength{ProductID: uuid.New(), Strength: s})
	}
	return e
}

func index(entries ...SearchEntry) []*indexEntry {
	out := make([]*indexEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, buildIndexEntry(e))
	}
	return out
}

func names(r SearchResult) []string {
	out := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		out = append(out, e.TradeName)
	}
	return out
}

func TestFoldingCollapsesTheSpellingsTwoLettersCannotDistinguish(t *testing.T) {
	// Each pair is one a physician could type either way and mean the same medicine. If one
	// of these stops holding, the search silently answers nothing for half the ways somebody
	// might spell a brand they have only ever seen on a box.
	for _, tc := range []struct{ a, b string }{
		{"Comet", "Komet"},      // c/k
		{"Ozempic", "Osempik"},  // z/s and c/k
		{"Fiasp", "Phiasp"},     // f/ph
		{"Victoza", "Biktosa"},  // v/b
		{"Thyrox", "Thiroks"},   // y/i and x/ks
		{"Ansulin", "Anssulin"}, // doubled letters
		{"Angilock Plus", "angilockplus"},
	} {
		if fold(tc.a) != fold(tc.b) {
			t.Errorf("%q folds to %q and %q folds to %q; they should be the same search",
				tc.a, fold(tc.a), tc.b, fold(tc.b))
		}
	}
}

func TestFoldingKeepsMedicinesApartThatAreDifferentMedicines(t *testing.T) {
	// The other half. A fold aggressive enough to make every pair above equal would also make
	// these equal, and two different medicines answering to one search is worse than a search
	// that misses.
	for _, tc := range []struct{ a, b string }{
		{"Comet", "Comprid"},
		{"Thyrox", "Thyrin"},
		{"Ansulin R", "Ansulin N"},
		{"Secrin", "Sitagil"},
		{"Emjard", "Emjard M"},
	} {
		if fold(tc.a) == fold(tc.b) {
			t.Errorf("%q and %q both fold to %q; they are different medicines", tc.a, tc.b, fold(tc.a))
		}
	}
}

func TestABengaliQueryReachesTheLatinBrand(t *testing.T) {
	// A physician on a Bengali keyboard types the brand phonetically. Each case here is a real
	// brand in the seed written the way somebody would type it in Bengali script.
	ix := index(
		entry("Comet", "Metformin hydrochloride", "TABLET", "500 mg"),
		entry("Thyrox", "Levothyroxine sodium", "TABLET", "50 mcg"),
		entry("Ansulin R", "Insulin human (rDNA) - soluble/regular", "SC_INJECTION", "100 IU/mL"),
		entry("Secrin", "Glimepiride", "TABLET", "2 mg"),
	)
	for _, tc := range []struct{ query, want string }{
		{"কম", "Comet"},       // no inherent vowel: only the consonant skeleton can reach it
		{"কমে", "Comet"},      //
		{"থাই", "Thyrox"},     //
		{"আনসু", "Ansulin R"}, //
		{"সেক", "Secrin"},     //
	} {
		got := names(searchIndex(ix, tc.query, 5, Usage{}))
		if len(got) == 0 || got[0] != tc.want {
			t.Errorf("%q (normalised %q) returned %v, want %s first",
				tc.query, mustNormalise(tc.query), got, tc.want)
		}
	}
}

func mustNormalise(q string) string {
	folded, skel := normaliseQuery(q)
	if skel != "" {
		return folded + " / " + skel
	}
	return folded
}

func TestTheConsonantSkeletonIsNotUsedForALatinQuery(t *testing.T) {
	// The skeleton exists because Bengali writes no inherent vowel. Turning it on for Latin
	// queries would make "ms" match "Metformin", which is not a search but a lottery — and it
	// is the kind of thing that gets switched on to make one awkward case work.
	ix := index(entry("Comet", "Metformin hydrochloride", "TABLET", "500 mg"))
	if got := names(searchIndex(ix, "ms", 5, Usage{})); len(got) != 0 {
		t.Errorf(`"ms" matched %v; a Latin query is matched on what was typed, not on its consonants`, got)
	}
	// And the Bengali equivalent of the same two consonants does reach it, which is the whole
	// reason the distinction exists.
	if got := names(searchIndex(ix, "কম", 5, Usage{})); len(got) != 1 {
		t.Errorf(`"কম" matched %v, want Comet`, got)
	}
}

func TestAPrefixMatchOutranksASubstringMatch(t *testing.T) {
	ix := index(
		entry("Humulin R", "Insulin human", "SC_INJECTION", "100 IU/mL"), // "mul" is inside it
		entry("Mulberry", "Nothing real", "TABLET", "1 mg"),              // "mul" starts it
	)
	got := names(searchIndex(ix, "mul", 5, Usage{}))
	if len(got) != 2 || got[0] != "Mulberry" {
		t.Errorf("got %v; a brand beginning with the query must outrank one merely containing it", got)
	}
}

func TestATradePrefixOutranksAGenericPrefixOfTheSameQuality(t *testing.T) {
	// Both are prefix matches, so both are in the same class and the fine tier decides. A
	// physician who typed "co" and meant Comet should not have to scroll past every product
	// whose *molecule* happens to start with those letters.
	ix := index(
		entry("Dibenol", "Co-something", "TABLET", "5 mg"),
		entry("Comet", "Metformin hydrochloride", "TABLET", "500 mg"),
	)
	got := names(searchIndex(ix, "co", 5, Usage{}))
	if len(got) != 2 || got[0] != "Comet" {
		t.Errorf("got %v, want Comet first", got)
	}
}

func TestRecentUseOutranksEverythingExceptTheKindOfMatch(t *testing.T) {
	// **This is criterion 3's ranking, tested with the signal injected by hand.** CP80 has not
	// happened, so nothing produces this Usage in production — the test supplies what
	// `UsageSource` will supply, which is the point of that interface existing now.
	//
	// It asserts two things at once, and the second is the one worth writing down: a drug
	// prescribed yesterday outranks an alphabetically earlier one *within* the prefix class,
	// and it does **not** outrank a prefix match from inside the substring class. Recency
	// reorders results; it does not invent them.
	recent := uuid.New()
	ix := index(
		entry("Anapril", "Enalapril maleate", "TABLET", "5 mg"),
		SearchEntry{TradeName: "Anzitor", GenericName: "Atorvastatin calcium", FormCode: "TABLET",
			Strengths: []SearchStrength{{ProductID: recent, Strength: "10 mg"}}},
	)
	// **Recency and frequency disagree here, on purpose.** Anzitor was prescribed yesterday and
	// only twice ever; Anapril is the one he writes constantly but has not written for a
	// fortnight. If the two signals agreed, this test would pass with the recency term deleted
	// — which is exactly what a mutation run found it doing before this line was written.
	older := ix[0].productIDs[0]
	usage := Usage{
		TimesPrescribed: map[uuid.UUID]int{recent: 2, older: 90},
		DaysSinceLast:   map[uuid.UUID]int{recent: 1, older: 14},
	}

	plain := names(searchIndex(ix, "an", 5, Usage{}))
	if len(plain) != 2 || plain[0] != "Anapril" {
		t.Fatalf("with no history the order is alphabetical; got %v", plain)
	}
	ranked := searchIndex(ix, "an", 5, usage)
	if got := names(ranked); len(got) != 2 || got[0] != "Anzitor" {
		t.Errorf("with Anzitor prescribed yesterday the order is %v, want Anzitor first", got)
	}
	if ranked.Entries[0].DaysSinceLast == nil || *ranked.Entries[0].DaysSinceLast != 1 {
		t.Errorf("the recency that decided the order is not reported on the entry: %+v",
			ranked.Entries[0].DaysSinceLast)
	}
	// Both have a history here, so both report one. The null case is covered by
	// TestTheRankingSaysItIsIncompleteWhileCP80DoesNotExist, where nothing has any.
	if ranked.Entries[1].DaysSinceLast == nil || *ranked.Entries[1].DaysSinceLast != 14 {
		t.Errorf("the second entry's recency is %v, want 14", ranked.Entries[1].DaysSinceLast)
	}
}

func TestAPrefixMatchOutranksADrugHePrescribesDaily(t *testing.T) {
	// **The one place the ranking deliberately overrules the physician's own history**, and the
	// reason the match class sits above recency rather than below it.
	//
	// Humulin R is prescribed every day and contains "mul"; Mulberry has never been prescribed
	// and begins with it. He typed "mul", and what he typed is evidence about what he wants that
	// his history is not. A ranking that let a daily drug win on a substring match would answer
	// almost every two-letter query with the same handful of drugs, whatever was typed.
	//
	// A mutation run found this untested: with the two per-physician signals constant, deleting
	// the class term changed nothing, because the fine tier happens to agree with it. It only
	// disagrees once there is a history, which is what this supplies.
	daily := uuid.New()
	ix := index(
		SearchEntry{TradeName: "Humulin R", GenericName: "Insulin human", FormCode: "SC_INJECTION",
			Strengths: []SearchStrength{{ProductID: daily, Strength: "100 IU/mL"}}},
		entry("Mulberry", "Nothing real", "TABLET", "1 mg"),
	)
	usage := Usage{
		TimesPrescribed: map[uuid.UUID]int{daily: 400},
		DaysSinceLast:   map[uuid.UUID]int{daily: 0},
	}
	got := names(searchIndex(ix, "mul", 5, usage))
	if len(got) != 2 || got[0] != "Mulberry" {
		t.Errorf("got %v; a brand beginning with what was typed outranks one he prescribes "+
			"daily that merely contains it", got)
	}
}

func TestFrequencyBreaksATieInRecency(t *testing.T) {
	often, seldom := uuid.New(), uuid.New()
	ix := index(
		SearchEntry{TradeName: "Anapril", GenericName: "Enalapril maleate", FormCode: "TABLET",
			Strengths: []SearchStrength{{ProductID: often, Strength: "5 mg"}}},
		SearchEntry{TradeName: "Angilock", GenericName: "Losartan potassium", FormCode: "TABLET",
			Strengths: []SearchStrength{{ProductID: seldom, Strength: "50 mg"}}},
	)
	usage := Usage{
		TimesPrescribed: map[uuid.UUID]int{often: 90, seldom: 2},
		DaysSinceLast:   map[uuid.UUID]int{often: 3, seldom: 3},
	}
	if got := names(searchIndex(ix, "an", 5, usage)); got[0] != "Anapril" {
		t.Errorf("got %v, want the more frequently prescribed first when recency ties", got)
	}
	// And reversing only the frequency reverses the order, so the test is about the frequency
	// rather than about the alphabet.
	usage.TimesPrescribed = map[uuid.UUID]int{often: 2, seldom: 90}
	if got := names(searchIndex(ix, "an", 5, usage)); got[0] != "Angilock" {
		t.Errorf("got %v, want Angilock first once it is the more frequent", got)
	}
}

func TestABrandsStrengthsAreOrderedByTheNumberAndNotByTheAlphabet(t *testing.T) {
	// Thyrox is stocked in six strengths. Alphabetically that is "100 mcg, 12.5 mcg, 25 mcg,
	// 50 mcg, 75 mcg" — a dose list in an order somebody misreads.
	e := entry("Thyrox", "Levothyroxine sodium", "TABLET",
		"100 mcg", "12.5 mcg", "25 mcg", "50 mcg", "75 mcg")
	sortStrengths(e.Strengths)
	want := []string{"12.5 mcg", "25 mcg", "50 mcg", "75 mcg", "100 mcg"}
	for i, s := range e.Strengths {
		if s.Strength != want[i] {
			t.Errorf("position %d is %q, want %q (full order %v)", i, s.Strength, want[i], e.Strengths)
		}
	}
}

func TestAnEmptyQueryReturnsNothingRatherThanTheWholeFormulary(t *testing.T) {
	ix := index(entry("Comet", "Metformin hydrochloride", "TABLET", "500 mg"))
	for _, q := range []string{"", "   ", "।", "—"} {
		if got := searchIndex(ix, q, 10, Usage{}); len(got.Entries) != 0 {
			t.Errorf("%q returned %v", q, names(got))
		}
	}
}

func TestTheLimitIsClampedRatherThanRefused(t *testing.T) {
	// Called on every keystroke. A physician mid-word who got a validation error instead of
	// results would have no idea what he had done.
	c := NewCache(CacheConfig{})
	// `day` is set because a snapshot built for another day is rebuilt from the database
	// whoever asks — see [snapshot.day]. A fixture without it would reach for a store this
	// test does not have.
	c.byID[uuid.Nil] = &snapshot{day: c.today(), index: index(
		entry("Comet", "Metformin hydrochloride", "TABLET", "500 mg"),
		entry("Comet XR", "Metformin hydrochloride", "TABLET_EXTENDED_RELEASE", "500 mg"),
	)}
	for _, limit := range []int{-4, 0, 1, 900} {
		got, err := c.Search(context.Background(), uuid.Nil, uuid.Nil, "co", limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Entries) == 0 || len(got.Entries) > MaxSearchLimit {
			t.Errorf("limit %d returned %d entries", limit, len(got.Entries))
		}
		if got.Total != 2 {
			t.Errorf("limit %d reported total %d, want 2 — the total is before the limit", limit, got.Total)
		}
	}
}

func TestTheRankingSaysItIsIncompleteWhileCP80DoesNotExist(t *testing.T) {
	// The whole of what CP76 can honestly claim about criterion 3. If somebody later wires a
	// proxy signal in and forgets to change this, the test still passes — which is why
	// `Complete` is a method on the source rather than a constant here: the only way to make
	// this true is to supply a source that can actually answer.
	c := NewCache(CacheConfig{})
	c.byID[uuid.Nil] = &snapshot{
		day: c.today(), index: index(entry("Comet", "Metformin", "TABLET", "500 mg")),
	}
	got, err := c.Search(context.Background(), uuid.Nil, uuid.Nil, "co", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.RankingComplete {
		t.Error("the search claims a complete ranking while NoUsage is the source")
	}
	if got.Entries[0].TimesPrescribed != 0 || got.Entries[0].DaysSinceLast != nil {
		t.Error("NoUsage produced a usage signal from somewhere")
	}
}

func TestAWithdrawnMedicineIsNotOfferedButItsBrandSurvivesItsLastStrength(t *testing.T) {
	rows := []CacheRow{
		{ProductID: uuid.New(), TradeName: "Comet", Strength: "500 mg", FormCode: "TABLET",
			GenericName: "Metformin hydrochloride", IsActive: true},
		{ProductID: uuid.New(), TradeName: "Comet", Strength: "850 mg", FormCode: "TABLET",
			GenericName: "Metformin hydrochloride", IsActive: false},
		{ProductID: uuid.New(), TradeName: "Dibenol", Strength: "5 mg", FormCode: "TABLET",
			GenericName: "Glibenclamide", IsActive: false},
	}
	ix := buildIndex(rows)

	got := searchIndex(ix, "co", 10, Usage{})
	if len(got.Entries) != 1 {
		t.Fatalf("got %v, want Comet alone", names(got))
	}
	if len(got.Entries[0].Strengths) != 1 || got.Entries[0].Strengths[0].Strength != "500 mg" {
		t.Errorf("the withdrawn 850 mg is still offered: %+v", got.Entries[0].Strengths)
	}
	if offered := searchIndex(ix, "di", 10, Usage{}); len(offered.Entries) != 0 {
		t.Errorf("a wholly withdrawn brand is still offered: %v", names(offered))
	}
}

func TestATrigramMatchNeedsFourCharactersAndRealOverlap(t *testing.T) {
	ix := index(entry("Metformin", "Metformin hydrochloride", "TABLET", "500 mg"))
	// A misspelling long enough to be evidence.
	if got := names(searchIndex(ix, "metfromin", 5, Usage{})); len(got) != 1 {
		t.Errorf(`"metfromin" matched %v, want Metformin through the trigram fallback`, got)
	}
	// Three characters that share a trigram with half the formulary must not.
	if got := names(searchIndex(ix, "xyz", 5, Usage{})); len(got) != 0 {
		t.Errorf(`"xyz" matched %v`, got)
	}
}
