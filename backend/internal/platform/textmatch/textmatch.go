// Package textmatch compares names the way a registration desk gets them wrong (CP30).
//
// It lives in platform because two modules need it and neither may import the other: the
// patient module scores candidates with it, and the projection that maintains the search
// columns computes its keys. It is text handling, not clinical logic.
//
// The problem it solves is specific to this clinic. A Bangladeshi name reaches the database
// through at least three routes — typed in Bangla, typed in English by the patient, typed in
// English by an operator hearing it spoken — and the English spellings disagree far more
// than English names do. Mohammad, Muhammad, Mohammod and Md are one name. Chowdhury,
// Choudhury and Chaudhuri are one name. Begum and Begom are one name. Trigram similarity
// alone puts "Mohammad Rahim" and "Muhammad Raheem" at about 0.45, which is below any
// threshold that does not also match half the clinic.
//
// So the comparison is done twice: on the text, and on a phonetic key built for exactly
// these transliteration habits. The key is deliberately lossy — it is a blocking key, meant
// to bring plausible candidates together for scoring, not to decide anything on its own.
package textmatch

import (
	"strings"
	"unicode"
)

// Key is the phonetic form of a romanised Bangladeshi name.
//
// Lossy on purpose, and in one direction: two spellings of one name must produce the same
// key, and it is acceptable for two different names to collide — a collision costs a
// candidate that scoring then rejects, while a miss costs a duplicate patient record that
// nobody notices for a year.
//
// The steps, and why each is here:
//
//	aspirates     kh gh th dh bh ph — Bengali aspirates, which English spellings render
//	              inconsistently: Faruk and Farukh, Fatema and Fathima. Folded to the
//	              unaspirated consonant. This loses a real distinction (খ is not ক), and
//	              that is the right trade for a *blocking* key: a collision costs a
//	              candidate that scoring then rejects, a miss costs a duplicate patient.
//	w             Dropped. It is a glide that is written or not according to taste —
//	              Anwar, Anowar, Anoar — and never a /v/.
//	z → j         Bengali has no /z/; জ is written both ways. Zaman and Jaman are one name.
//	v → b         Likewise no /v/. Naveed, Nabid, Navid.
//	q → k         Qadir and Kadir.
//	y → i         Sayed, Sayeed, Saeed, Said.
//	vowels        Dropped except a leading one, Soundex-style. This is what makes Mohammad,
//	              Muhammad and Mohammod one key, and it is the single most valuable rule
//	              here because the honorific is written a dozen ways.
//	doubles       Collapsed, because a doubled consonant in a romanised Bangladeshi name
//	              carries no information — Rahman and Rahmaan.
func Key(name string) string {
	// Every non-letter becomes a space rather than vanishing: "Md.Rahim" is two words, and
	// joining them would make it a different name from "Md Rahim".
	letters := make([]rune, 0, len(name))
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) && r < 128 {
			letters = append(letters, r)
		} else {
			letters = append(letters, ' ')
		}
	}
	out := make([]string, 0, 4)
	for _, word := range strings.Fields(string(letters)) {
		if key := wordKey(word); key != "" {
			out = append(out, key)
		}
	}
	return strings.Join(out, " ")
}

// digraphs are folded first, before any single-letter rule can split them.
var digraphs = strings.NewReplacer(
	// Aspirates, to their unaspirated consonant. `ch` and `jh` keep distinct symbols
	// because চ and ঝ are not ক and জ to any ear.
	"kh", "k", "gh", "g", "th", "t", "dh", "d", "bh", "b", "ph", "f", "sh", "s",
	"ch", "C", "jh", "J", "zh", "s", "ck", "k",
	// Vowel clusters, so that the "keep the first letter" rule below sees one vowel.
	"ee", "i", "oo", "u", "ou", "u", "ow", "u", "au", "u", "aa", "a",
)

var singles = strings.NewReplacer(
	"z", "j", "v", "b", "q", "k", "x", "ks", "y", "i", "c", "k",
)

func wordKey(word string) string {
	// The honorific every second male patient's name begins with, and its female
	// counterpart. Expanded to a fixed symbol rather than dropped: dropping it would make
	// "Md Rahim" and "Rahim" one key, and they are different names.
	switch word {
	case "md", "mohd", "mohammad", "muhammad", "mohammed", "mohammod", "muhammed":
		return "MHMD"
	case "mst", "most", "mosammat", "musammat":
		return "MST"
	}

	folded := singles.Replace(digraphs.Replace(word))
	out := make([]rune, 0, len(folded))
	for i, r := range folded {
		switch {
		case r == 'w':
			continue // a glide, written or not
		case isVowel(r) && i > 0:
			continue
		case r == 'h' && i > 0:
			// Silent after a consonant in these spellings: Rahman, Sheikh, Mahmud.
			continue
		}
		if len(out) > 0 && out[len(out)-1] == r {
			continue // a doubled letter carries nothing
		}
		out = append(out, r)
	}
	return string(out)
}

func isVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

// Trigrams returns the set of trigrams of a string, the way pg_trgm does: lower-cased, with
// the string padded by two leading spaces and one trailing.
//
// Reimplemented rather than delegated so that a score can be computed in Go — for the
// candidate the desk is *about to* create, which has no row yet and therefore nothing for
// the database to compare against.
func Trigrams(s string) map[string]struct{} {
	normalised := normaliseForTrigrams(s)
	out := map[string]struct{}{}
	for _, word := range strings.Fields(normalised) {
		padded := "  " + word + " "
		runes := []rune(padded)
		for i := 0; i+3 <= len(runes); i++ {
			out[string(runes[i:i+3])] = struct{}{}
		}
	}
	return out
}

func normaliseForTrigrams(s string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		} else {
			out.WriteRune(' ')
		}
	}
	return out.String()
}

// Similarity is the Jaccard-style trigram similarity pg_trgm computes: the shared trigrams
// over the union. 0 for nothing in common, 1 for identical.
func Similarity(a, b string) float64 {
	left, right := Trigrams(a), Trigrams(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	shared := 0
	for trigram := range left {
		if _, ok := right[trigram]; ok {
			shared++
		}
	}
	union := len(left) + len(right) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// Distance is the Levenshtein edit distance, for telephone numbers and short codes where a
// transposed or mistyped digit is the failure being looked for. Not used on names: an edit
// distance on a romanised Bangladeshi name says more about spelling convention than about
// whether two people are the same person.
func Distance(a, b string) int {
	if a == b {
		return 0
	}
	left, right := []rune(a), []rune(b)
	if len(left) == 0 {
		return len(right)
	}
	if len(right) == 0 {
		return len(left)
	}
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(left); i++ {
		current[0] = i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			current[j] = min3(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
