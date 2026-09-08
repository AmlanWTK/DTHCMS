package ai

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
)

// PHI minimisation (D-08, §10.3 step 3).
//
// # The policy, in one sentence
//
// Default deny: the model is told a pseudonym, an age in months, a sex and a clinical picture, and
// is told nothing whatever about which person that is.
//
// # The three mechanisms, and why one of them is not enough
//
//  1. **Substitution.** The caller hands the subject's identifiers to [Minimiser.Minimise] as a
//     separate map. They never enter the payload; a stable pseudonym does. On the way back,
//     [Minimised.Restore] puts them into the response, which is what makes a summary readable
//     without the name having crossed the boundary to produce it. This is the "strip-and-restore"
//     the checkpoint asks for, and it is the only mechanism here that is exact.
//
//  2. **Key refusal.** Any key in the payload whose class is IDENTIFIER or CREDENTIAL is refused —
//     not deleted. See the note on [ErrPayloadNamesAPerson] for why refusing beats stripping here.
//
//  3. **Pattern scrubbing.** Every string value is run through the shared pattern list, which
//     catches phone numbers, national IDs and email addresses wherever they appear, including in
//     the middle of a sentence. This is the mechanism for the risk the plan names as the main one:
//     *"PHI leakage through free-text fields"*.
//
// Each covers a case the others do not. Substitution knows exactly who the subject is and nothing
// about a third party; key refusal sees structure and not prose; patterns see prose and cannot see
// names.
//
// # What is left over, said plainly
//
// A bare given name of a third party, written into clinical prose by a clinician — "her son Rafiq
// takes her to the pharmacy" — is not caught by any of the three. Nothing that runs in this process
// can catch it: "Rafiq" is a word, and there is no expression that separates it from a drug, a
// district or a diagnosis. The mitigations are that it is not an identifier of the *patient*, that
// the honorific pattern catches the very common "Md. Rafiq" spelling, and that every outbound
// payload is stored in full for a person to read. That is the residual risk of this checkpoint, and
// it is the reason `docs/ai-gateway.md` asks for the outbound log to be reviewed rather than
// assumed correct.

// Minimiser turns a caller's payload into something safe to send.
type Minimiser struct {
	// pseudonymKey is derived from the deployment's identifier pepper with a domain-separation
	// label, so the pseudonyms cannot be reversed by anybody who does not hold the pepper, and so
	// this use of the pepper cannot weaken the one it already has.
	pseudonymKey []byte
	patterns     []Pattern
}

// NewMinimiser builds one. The pepper is `Secrets.IdentifierPepper`; see [PseudonymKey].
func NewMinimiser(pepper string) (*Minimiser, error) {
	compiled, err := CompilePatterns(DefaultPatterns)
	if err != nil {
		return nil, err
	}
	return &Minimiser{pseudonymKey: PseudonymKey(pepper), patterns: compiled}, nil
}

// PseudonymKey derives the pseudonym key from the deployment's identifier pepper.
//
// A separate key rather than the pepper itself, and separate rather than a new environment
// variable, for two reasons that pull in the same direction. Reusing the pepper directly would mean
// a pseudonym and a national-ID digest were computed with the same secret, so anybody holding a
// table of pseudonyms could test guesses against the duplicate-detection index — key separation is
// cheap and its absence is the sort of thing found years later. And a *new* secret would be one
// more thing an operator has to set correctly before the AI features work at all, with a failure
// mode (a default value nobody changed) that is worse than the problem it solves.
//
// HMAC with a fixed label is the standard shape for this. The label carries a version so that
// re-deriving is possible later without changing the pepper.
func PseudonymKey(pepper string) []byte {
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte("dthcms/ai/pseudonym/v1"))
	return mac.Sum(nil)
}

// Subject is who a payload is about.
//
// Note what is absent: there is no field saying whether this subject is real or fabricated. That is
// deliberate and it is the whole of acceptance criterion 1b — see the package comment in ai.go.
type Subject struct {
	// PatientID is the record this payload was assembled from, or the zero UUID when it derives
	// from no patient record at all. The gateway resolves provenance from it, by lookup.
	PatientID uuid.UUID
	// AgeMonths and Sex are the two demographics D-08 lets through. Months rather than years
	// because [R-06]'s paediatric percentiles are meaningless at a year's resolution, and a date
	// of birth is an identifier.
	AgeMonths int
	Sex       string
	// Identifiers are the strings that name this person, by label: "name_en", "phone", "nid",
	// "guardian_name". Each is struck out of every string in the payload and restored in the
	// response. A caller that leaves one out has not leaked anything by itself — the patterns and
	// the key check still run — but has lost the exact match for that one string.
	Identifiers map[string]string
}

// Minimised is a payload that may be sent, plus what it took to get there.
type Minimised struct {
	// Payload is what goes to the provider. Nothing in it names a person.
	Payload map[string]any
	// Pseudonym stands in for the subject throughout. Stable for a (subject, agent) pair, which
	// is what makes the response cache possible at all — see the note on [Minimiser.Pseudonym].
	Pseudonym string
	// Removed is what the scrubber took out: the kind of thing and where, never the thing. It is
	// written to the interaction record so that a reviewer can see the scrubber working without
	// the review itself becoming a second copy of the data.
	Removed []Removal

	// restore maps a token back to the string it replaced. Unexported: it is the one part of this
	// struct that holds identifiers, and a caller that could reach it could log it.
	restore map[string]string
}

// Removal is one thing the scrubber took out.
type Removal struct {
	// Kind is the pattern that matched, or "subject_identifier:<label>" for an exact substitution.
	Kind string `json:"kind"`
	// Path is where in the payload, in dotted form: "note", "visits.2.summary".
	Path string `json:"path"`
	// Count is how many times. A note with four phone numbers in it is a different thing from a
	// note with one, and an operator reviewing the log should be able to see that without the
	// numbers.
	Count int `json:"count"`
}

// Errors the minimiser returns.
var (
	// ErrPayloadNamesAPerson is a payload carrying an identifier-class or credential-class key.
	//
	// **Refused rather than stripped**, which is worth arguing for because D-08's own wording is
	// "strips". Stripping is safer in the moment and worse in every month afterwards: the agent's
	// prompt still refers to the field, so the model receives a template with a hole in it and
	// produces a sentence about a patient whose name is blank — and nobody ever learns that the
	// payload was assembled wrongly. Refusing is loud, happens at the earliest possible point, and
	// names the key so the fix is obvious. The clinical consequence of the refusal is covered:
	// D-15 requires the physician's screen to degrade to raw structured data rather than to
	// nothing, so a refused synthesis costs a summary and not a consultation.
	//
	// The identifiers that *are* stripped are the ones the caller declares on [Subject], where the
	// gateway knows what they are and can put them back.
	ErrPayloadNamesAPerson = fmt.Errorf("ai: the payload carries a key that names a person")
	// ErrIdentifierSurvived is the last check: an identifier the caller declared is still present
	// in the payload after everything above has run. It should be impossible; it is checked
	// because "should be impossible" is not a property, and because this is the one failure in
	// this package whose cost is a patient's data on somebody else's servers.
	ErrIdentifierSurvived = fmt.Errorf("ai: an identifier survived minimisation")
)

// Pseudonym is the token that stands in for a subject.
//
// # Why it is deterministic rather than per-request
//
// D-08 says "a per-request pseudonym", and a fresh random token per call would be marginally more
// unlinkable. It would also make two things impossible that matter more.
//
// The first is the response cache. §10.3 step 4 requires that identical input is never re-billed,
// and the cache is keyed on the hash of the minimised payload — which contains the pseudonym. A
// random pseudonym changes that hash on every call, so the cache never hits, and the checkpoint's
// own cost-control mechanism is dead on arrival.
//
// The second is review. The outbound log exists to be read by a person asking "what has this system
// been told about this patient". With a random token per call, the answer is not assemblable from
// the log at all without joining back to the patient id — which means the reviewer needs the
// identified data to review the de-identified data, and the log stops being safe to look at.
//
// What is kept is the property that actually matters: the token is an HMAC under a key held only by
// this deployment, so it cannot be reversed, and it is salted with the agent code, so the same
// patient is a different token to every agent and two agents' logs cannot be joined by anyone who
// obtains both.
func (m *Minimiser) Pseudonym(subject uuid.UUID, agentCode string) string {
	mac := hmac.New(sha256.New, m.pseudonymKey)
	mac.Write(subject[:])
	mac.Write([]byte{0})
	mac.Write([]byte(agentCode))
	sum := mac.Sum(nil)

	// Letters only, and this is not style. The first version of this was twelve hex characters,
	// which meant roughly one pseudonym in six contained a run of seven digits — and seven digits
	// is exactly what `digit_run_latin` exists to catch. The gateway's own placeholder tripped the
	// gateway's own scrubber, the check constraint refused the record, and the call failed. Not on
	// a patient with an unusual name, not on a payload anybody wrote: on one subject in six, at
	// random, forever.
	//
	// It surfaced because the database test inserts a real row for a random subject; nothing in the
	// pure-Go tests would ever have shown it, and in production it would have been an
	// intermittent, unreproducible failure attached to particular patients.
	//
	// Twelve characters from twenty-six is fifty-six bits, which is far more than enough to
	// separate this clinic's caseload, and it cannot look like a telephone number to the scrubber
	// or to the model.
	token := make([]byte, 12)
	for i := range token {
		token[i] = pseudonymAlphabet[int(sum[i])%len(pseudonymAlphabet)]
	}
	return "PT-" + string(token)
}

// pseudonymAlphabet has no digits in it, on purpose. See [Minimiser.Pseudonym].
const pseudonymAlphabet = "abcdefghijklmnopqrstuvwxyz"

// Minimise produces the payload that may be sent.
func (m *Minimiser) Minimise(agentCode string, subject Subject, payload map[string]any) (Minimised, error) {
	out := Minimised{
		Pseudonym: m.Pseudonym(subject.PatientID, agentCode),
		restore:   map[string]string{},
	}

	// Substitutions, longest value first. "Ayesha Rahman" has to be replaced before "Ayesha", or
	// the shorter one wins and leaves "Rahman" in the text — which is a surname reaching a
	// provider through the mechanism that exists to stop exactly that.
	type substitution struct{ token, value string }
	subs := make([]substitution, 0, len(subject.Identifiers))
	for label, value := range subject.Identifiers {
		trimmed := strings.TrimSpace(value)
		// A one- or two-character identifier is not an identifier, it is a substring: replacing
		// every "A" in a clinical note would destroy the note and protect nobody.
		if len(trimmed) < 3 {
			continue
		}
		token := subjectToken(label)
		subs = append(subs, substitution{token: token, value: trimmed})
		out.restore[token] = trimmed
	}
	sort.Slice(subs, func(i, j int) bool { return len(subs[i].value) > len(subs[j].value) })

	removals := map[string]*Removal{}
	note := func(kind, path string, n int) {
		if n == 0 {
			return
		}
		key := kind + "\x00" + path
		if existing, ok := removals[key]; ok {
			existing.Count += n
			return
		}
		removals[key] = &Removal{Kind: kind, Path: path, Count: n}
	}

	scrub := func(path, text string) string {
		for _, sub := range subs {
			if n := strings.Count(text, sub.value); n > 0 {
				text = strings.ReplaceAll(text, sub.value, sub.token)
				note("subject_identifier:"+strings.TrimSuffix(strings.TrimPrefix(sub.token, "[SUBJECT_"), "]"), path, n)
			}
		}
		for _, pattern := range m.patterns {
			matches := pattern.re.FindAllStringIndex(text, -1)
			if len(matches) == 0 {
				continue
			}
			text = pattern.re.ReplaceAllString(text, pattern.Replacement)
			note(pattern.Kind, path, len(matches))
		}
		return text
	}

	cleaned, err := walk("", payload, scrub)
	if err != nil {
		return Minimised{}, err
	}
	object, ok := cleaned.(map[string]any)
	if !ok {
		return Minimised{}, fmt.Errorf("ai: a payload must be an object")
	}

	// The two demographics D-08 permits, plus the pseudonym, written by the gateway rather than
	// accepted from the caller. A caller that supplied its own `subject` key would have had it
	// refused above (`dob` and every name key are identifier-class); this is where the replacement
	// comes from.
	object["subject"] = map[string]any{
		"pseudonym":  out.Pseudonym,
		"age_months": subject.AgeMonths,
		"sex":        subject.Sex,
	}
	out.Payload = object

	out.Removed = make([]Removal, 0, len(removals))
	for _, r := range removals {
		out.Removed = append(out.Removed, *r)
	}
	sort.Slice(out.Removed, func(i, j int) bool {
		if out.Removed[i].Path != out.Removed[j].Path {
			return out.Removed[i].Path < out.Removed[j].Path
		}
		return out.Removed[i].Kind < out.Removed[j].Kind
	})

	// The last gate, and the only one that looks at the finished article. Everything above works
	// on the way in; this asks the question the checkpoint actually poses — is there an identifier
	// in what we are about to send — of the bytes that are about to go.
	if label, found := m.survivingIdentifier(subject, out.Payload); found {
		return Minimised{}, fmt.Errorf("%w: %s", ErrIdentifierSurvived, label)
	}
	return out, nil
}

// Restore puts the subject's identifiers back into a string the model returned.
//
// The other half of strip-and-restore. A summary that says "PT-3f9a12bc0d44 has had diabetes for
// eleven years" is correct and unreadable; the physician needs the name, and the name never left
// the building to produce the sentence.
func (m Minimised) Restore(text string) string {
	for token, value := range m.restore {
		text = strings.ReplaceAll(text, token, value)
	}
	return text
}

// RestoreInto walks a decoded JSON value and restores identifiers in every string it contains.
func (m Minimised) RestoreInto(value any) any {
	switch v := value.(type) {
	case string:
		return m.Restore(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, inner := range v {
			out[key] = m.RestoreInto(inner)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			out[i] = m.RestoreInto(inner)
		}
		return out
	}
	return value
}

// survivingIdentifier reports whether any declared identifier is still in the payload.
//
// Case-insensitive, and with separators removed as well, because "01711-234567" and "01711234567"
// are the same number and a check that only compared the exact string would pass the one a
// clinician retyped without the hyphen.
func (m *Minimiser) survivingIdentifier(subject Subject, payload map[string]any) (string, bool) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		// Unencodable means unsendable, so nothing can leak; the gateway will fail on the same
		// value a moment later with a message about JSON rather than about PHI, which is the
		// honest one.
		return "", false
	}
	haystack := strings.ToLower(string(encoded))
	stripped := stripSeparators(haystack)

	for label, value := range subject.Identifiers {
		needle := strings.ToLower(strings.TrimSpace(value))
		if len(needle) < 3 {
			continue
		}
		if strings.Contains(haystack, needle) {
			return label, true
		}
		if bare := stripSeparators(needle); len(bare) >= 6 && strings.Contains(stripped, bare) {
			return label, true
		}
	}
	return "", false
}

func stripSeparators(s string) string {
	return strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "", "+", "").Replace(s)
}

// subjectToken is the placeholder one identifier is replaced by.
//
// The `SUBJECT_` prefix keeps these disjoint from the pattern replacements — `[NAME]`, `[NUMBER]`,
// `[EMAIL]` — which matters because Restore only knows how to put back what it took out. A caller
// labelling an identifier "name" would otherwise produce `[NAME]`, collide with the honorific
// pattern's replacement, and have the subject's name substituted into a place where some third
// party's name had been scrubbed. TestSubjectTokensCannotCollideWithPatternReplacements holds it.
func subjectToken(label string) string {
	upper := strings.ToUpper(strings.TrimSpace(label))
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
			return r
		case r >= '0' && r <= '9':
			return r
		}
		return '_'
	}, upper)
	return "[SUBJECT_" + safe + "]"
}

// walk copies a decoded JSON value, refusing identifier keys and scrubbing every string.
//
// Recursive over objects and arrays, because `{"visit": {"patient": {"phone": …}}}` hides an
// identifier two levels down, and a check that only looked at the top level would pass exactly the
// payload somebody was most likely to write. The same shape as `ops.carries_identifier`, which is
// the database's copy of this rule.
func walk(path string, value any, scrub func(path, text string) string) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, inner := range v {
			if banned := identifierKeyIn(key); banned != "" {
				return nil, fmt.Errorf("%w: %s (%s)", ErrPayloadNamesAPerson, join(path, key),
					logging.PHIKeys[banned].Guidance)
			}
			cleaned, err := walk(join(path, key), inner, scrub)
			if err != nil {
				return nil, err
			}
			out[key] = cleaned
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			cleaned, err := walk(fmt.Sprintf("%s.%d", path, i), inner, scrub)
			if err != nil {
				return nil, err
			}
			out[i] = cleaned
		}
		return out, nil
	case string:
		return scrub(path, v), nil
	}
	return value, nil
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// identifierKeyIn returns the banned key this one matches, or "".
//
// Whole key, or any `_`-suffixed form of one: `guardian_phone` and `patient_name` are what a
// developer reaches for when the bare key feels wrong. Clinical-class keys pass — `diagnosis` is
// exactly what the model is being asked about.
//
// # Why this iterates the list instead of taking the last segment
//
// The obvious spelling — split on the final underscore and look that up — is what
// `internal/jobs` and the log handler do, and it is subtly narrower than the SQL rule it is
// supposed to mirror. `ops.carries_identifier` asks `lower(k) LIKE '%\_' || key` for every banned
// key, so it catches `mother_name_bn` (suffix `_name_bn`); the last-segment version looks up `bn`,
// finds nothing, and lets it through.
//
// That gap is not academic and its shape is instructive. Nothing would have leaked — the check
// constraint on `core.ai_interaction.outbound` refuses the row and the call never happens — but the
// refusal would have arrived as a constraint violation naming a constraint rather than as an error
// naming a key, which is the difference between a rule somebody fixes and a rule somebody works
// around. Twenty-two keys is a loop nobody will notice.
func identifierKeyIn(key string) string {
	lower := strings.ToLower(key)
	if logging.MustNotLeaveTheBoundary(lower) {
		return lower
	}
	for banned := range logging.PHIKeys {
		if strings.HasSuffix(lower, "_"+banned) && logging.MustNotLeaveTheBoundary(banned) {
			return banned
		}
	}
	return ""
}

// Pattern is one free-text rule, compiled.
type Pattern struct {
	Kind          string
	Expression    string
	Replacement   string
	DescriptionEN string
	DescriptionBN string

	re *regexp.Regexp
}

// Matches reports whether this pattern finds anything in s.
//
// Exported for the tests that hold the Go and database copies of the pattern list together, and for
// the one that checks no committed prompt carries an identifier. The gateway itself never calls it:
// minimisation replaces rather than asks.
func (p Pattern) Matches(s string) bool { return p.re != nil && p.re.MatchString(s) }

// CompilePatterns compiles the list, failing loudly rather than skipping one it cannot read.
//
// A scrubber that silently ran five of its six rules would be a scrubber that passed every test
// about the five and leaked through the sixth.
func CompilePatterns(patterns []Pattern) ([]Pattern, error) {
	out := make([]Pattern, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p.Expression)
		if err != nil {
			return nil, fmt.Errorf("ai: pattern %q does not compile in Go: %w", p.Kind, err)
		}
		p.re = re
		out = append(out, p)
	}
	return out, nil
}
