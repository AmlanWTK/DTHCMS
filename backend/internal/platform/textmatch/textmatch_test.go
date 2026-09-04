package textmatch_test

import (
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/textmatch"
)

// What the phonetic key has to get right, drawn from how Bangladeshi names actually reach a
// clinic's database: typed in Bangla, typed in English by the patient, and typed in English
// by an operator who heard it spoken.

func TestOneNameSpelledManyWaysIsOneKey(t *testing.T) {
	groups := [][]string{
		// The prefix every second male patient's name begins with.
		{"Md Rahim", "Mohammad Rahim", "Muhammad Raheem", "Mohd. Rahim", "Mohammod Rahim"},
		// The surname with no settled English spelling.
		{"Chowdhury", "Choudhury", "Chaudhuri", "Chowdhuri"},
		// Bengali has no /z/.
		{"Zaman", "Jaman"},
		{"Zakir", "Jakir"},
		// Nor /v/.
		{"Naveed", "Nabid", "Navid"},
		{"Anwar", "Anowar", "Anoar"},
		// Long vowels are written to taste.
		{"Rahman", "Rahmaan", "Rehman"},
		{"Begum", "Begom", "Begam"},
		{"Fatema", "Fatima", "Fathima"},
		{"Sayed", "Saeed", "Sayeed", "Said"},
		{"Kadir", "Qadir"},
		{"Farukh", "Faruk", "Farooq"},
	}
	for _, group := range groups {
		want := textmatch.Key(group[0])
		if want == "" {
			t.Errorf("%q has an empty key", group[0])
			continue
		}
		for _, spelling := range group[1:] {
			if got := textmatch.Key(spelling); got != want {
				t.Errorf("%q → %q but %q → %q; one name, two keys",
					group[0], want, spelling, got)
			}
		}
	}
}

func TestDifferentNamesKeepDifferentKeys(t *testing.T) {
	// The key is allowed to collide — a collision costs a candidate that scoring rejects,
	// while a miss costs a duplicate record. But it must not collapse names a registration
	// officer would never confuse, or every busy morning produces a screenful of warnings
	// and the warnings stop being read.
	pairs := [][2]string{
		{"Rahim", "Karim"},
		{"Fatema", "Salma"},
		{"Abdul Karim", "Abdul Hamid"},
		{"Rahima Begum", "Rahim Uddin"},
		{"Nasrin", "Nazrul"},
		{"Md Rahim", "Rahim"}, // the honorific is part of the name, not noise
	}
	for _, pair := range pairs {
		if textmatch.Key(pair[0]) == textmatch.Key(pair[1]) {
			t.Errorf("%q and %q share the key %q", pair[0], pair[1], textmatch.Key(pair[0]))
		}
	}
}

func TestTheKeyIgnoresPunctuationCaseAndSpacing(t *testing.T) {
	want := textmatch.Key("Md Rahim")
	for _, variant := range []string{"MD. RAHIM", "  md   rahim  ", "Md.Rahim", "md-rahim"} {
		if got := textmatch.Key(variant); got != want {
			t.Errorf("%q → %q, want %q", variant, got, want)
		}
	}
}

func TestTheKeyIgnoresBanglaScript(t *testing.T) {
	// The key is for romanised names. A Bangla name is compared as text, where trigram
	// similarity works well because there is only one way to spell it.
	if key := textmatch.Key("রহিমা বেগম"); key != "" {
		t.Errorf("a Bangla name produced the romanisation key %q", key)
	}
}

func TestSimilarityBehavesLikeTrigramMatching(t *testing.T) {
	if s := textmatch.Similarity("Rahima Begum", "Rahima Begum"); s != 1 {
		t.Errorf("identical strings scored %v", s)
	}
	if s := textmatch.Similarity("Rahima Begum", "Karim Uddin"); s > 0.15 {
		t.Errorf("unrelated names scored %v", s)
	}
	// A one-letter difference in a long name is a high score; a one-letter difference in a
	// short one is not, which is the property that makes trigrams right for names and
	// wrong for telephone numbers.
	if s := textmatch.Similarity("Rahima Begum", "Rahima Begom"); s < 0.5 {
		t.Errorf("one letter apart scored %v", s)
	}
	if s := textmatch.Similarity("", "Rahima"); s != 0 {
		t.Errorf("an empty string scored %v", s)
	}
	// Bangla works too: same code path, no romanisation.
	if s := textmatch.Similarity("রহিমা বেগম", "রহিমা বেগম"); s != 1 {
		t.Errorf("identical Bangla scored %v", s)
	}
	if s := textmatch.Similarity("রহিমা বেগম", "করিম উদ্দিন"); s > 0.2 {
		t.Errorf("unrelated Bangla scored %v", s)
	}
}

func TestDistanceCountsEdits(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"01712345678", "01712345678", 0},
		{"01712345678", "01712345687", 2}, // a transposition
		{"01712345678", "01712345679", 1}, // a mistyped digit
		{"01712345678", "0171234567", 1},  // a dropped digit
		{"", "abc", 3},
		{"abc", "", 3},
	} {
		if got := textmatch.Distance(c.a, c.b); got != c.want {
			t.Errorf("Distance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
