// Package evalset is the frozen evaluation set and the harness that runs it (CP72, §10.5).
//
// # Why the set is frozen, and what "frozen" is made of
//
// §10.5: *"every change to a prompt or model runs the frozen evaluation set in CI; results are
// recorded, and a regression blocks the merge."* A gate run against a case set somebody can edit
// in the same commit that broke something is a gate that passes by construction — the easiest way
// to make a regression disappear is to change the case that caught it, and nobody doing it would
// think of themselves as cheating.
//
// So the set is a directory of files and a manifest naming each file's SHA-256 and a hash over all
// of them. [Load] recomputes both and **refuses to return a set that disagrees with its manifest**.
// Editing a case is therefore a two-file change with a hash in the diff, which is not a barrier to
// anybody honest and is a barrier to doing it by accident. The set hash is recorded on every
// `ops.ai_evaluation_run` row, so two runs can be compared only when they ran the same cases.
//
// # Where the cases came from, and what they are not
//
// The twenty GROUNDED cases are the twenty summaries CP71 produced from the synthetic cohort,
// taken from `core.ai_interaction` — the minimised payload that went out and the answer that came
// back, both pseudonymised, both de-identified by construction rather than by a scrubbing pass
// somebody has to trust.
//
// **They were written by a deterministic composer, not by a language model** (`tools/synthshots`),
// and that limits exactly one of the two numbers this set produces. The detection rate is
// unaffected: an injected hallucination is injected into text, and the validator does not know or
// care what wrote the text around it. The false-positive rate *is* affected, and the direction is
// flattering: the composer writes only values it read out of the payload, so the corpus contains
// almost none of the paraphrase a real model produces, which is where the validator's real
// false-positive risk lives. `docs/ai-grounding.md` states what that costs the measurement, and
// this comment exists so that nobody reads a low number here as a finished answer.
//
// The seven HALLUCINATION cases are deterministic mutations of those payloads' answers, one per
// failure the checkpoint names plus the two shapes of date. Each names the arm it must fire and
// the token that must be reported, so a validator that caught the case *for the wrong reason*
// fails the harness rather than passing it.
package evalset

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
)

//go:embed cases/*.json manifest.json baseline.json
var files embed.FS

// Expectation is what the set says should happen to a case.
type Expectation string

const (
	// Grounded is an answer known to be correct. A finding on one of these is a false positive,
	// and the count of them is acceptance criterion 2.
	Grounded Expectation = "GROUNDED"
	// Hallucination is an answer somebody deliberately corrupted. Missing one is acceptance
	// criterion 1 failing, and there is no tolerance for it.
	Hallucination Expectation = "HALLUCINATION"
)

// Case is one row of the set.
type Case struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Expect      Expectation `json:"expect"`

	// DerivedFrom names the GROUNDED case whose payload this one reuses. Empty on a GROUNDED case.
	//
	// Injected cases share a payload with the case they were made from rather than carrying a
	// second copy of it, and that is not only about the four hundred kilobytes: a reviewer reading
	// the diff between `case-03` and `case-03-fabricated-hba1c` should see exactly the sentence
	// somebody corrupted, and nothing else.
	DerivedFrom string `json:"derived_from,omitempty"`
	// Injection says, in a sentence, what was done to the answer. Read by a person, not by code.
	Injection string `json:"injection,omitempty"`

	// Payload is what the model was shown: `core.ai_interaction.outbound.payload`, minimised and
	// pseudonymised. Absent on a derived case.
	Payload map[string]any `json:"payload,omitempty"`
	// Output is the model's answer before the gateway restores identifiers.
	Output map[string]any `json:"output"`

	// Expected is what the check must find. Empty on a GROUNDED case, and on a HALLUCINATION case
	// every entry must appear in the report — a case caught for the wrong reason is a case the
	// harness should not be reassured by.
	Expected []ExpectedFinding `json:"expected_findings,omitempty"`

	// sha256 is the file this case was read from, and is what the manifest pins.
	sha256 string
}

// ExpectedFinding is one thing a hallucination case must be caught on.
type ExpectedFinding struct {
	Arm   ai.GroundingArm `json:"arm"`
	Token string          `json:"token"`
}

// Manifest is the frozen set's index.
type Manifest struct {
	Name      string `json:"name"`
	AgentCode string `json:"agent_code"`
	// SHA256 is over every case's id and hash, sorted. Adding, removing or editing any case
	// changes it, which is what makes it usable as "these two runs ran the same thing".
	SHA256 string `json:"sha256"`
	Notes  string `json:"notes"`
	Cases  []struct {
		ID     string `json:"id"`
		SHA256 string `json:"sha256"`
	} `json:"cases"`
}

// Set is the loaded, verified set.
type Set struct {
	Name      string
	AgentCode string
	SHA256    string
	Cases     []Case
}

// Load reads the embedded set and checks it against its manifest.
func Load() (Set, error) { return LoadFS(files) }

// LoadFS is [Load] against any filesystem, and it exists so that the refusal can be *tested*.
//
// Every failure here is a refusal rather than a warning. A harness that ran a set it could not
// vouch for would produce a number with a green tick on it, and the number would be the thing
// somebody quoted. But a refusal that only ever runs against a manifest that is correct is a
// refusal nobody has watched work — a mutation that disabled the hash comparison entirely survived
// the first version of this package's tests, because the committed manifest agrees with the
// committed cases and disabling a check that never fires changes nothing.
//
// So the reading is parameterised, and `TestATamperedCaseIsRefused` hands it a case whose bytes
// have been changed by one character.
func LoadFS(fsys fs.FS) (Set, error) {
	raw, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return Set{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Set{}, fmt.Errorf("evalset: the manifest does not parse: %w", err)
	}

	entries, err := fs.Glob(fsys, "cases/*.json")
	if err != nil {
		return Set{}, err
	}
	sort.Strings(entries)

	byID := map[string]Case{}
	set := Set{Name: manifest.Name, AgentCode: manifest.AgentCode}
	for _, name := range entries {
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return Set{}, err
		}
		one, err := CaseFromBytes(body)
		if err != nil {
			return Set{}, fmt.Errorf("evalset: %s: %w", path.Base(name), err)
		}
		if want := one.ID + ".json"; path.Base(name) != want {
			return Set{}, fmt.Errorf("evalset: %s declares id %q and should be named %s",
				path.Base(name), one.ID, want)
		}
		byID[one.ID] = one
		set.Cases = append(set.Cases, one)
	}
	if len(set.Cases) == 0 {
		return Set{}, fmt.Errorf("evalset: the set is empty; the embed pattern has stopped matching")
	}

	// Resolve derived payloads before the hash check, so that a case pointing at a sibling that
	// does not exist fails with a sentence naming both rather than with a nil map three functions
	// later.
	for i, one := range set.Cases {
		if one.DerivedFrom == "" {
			if len(one.Payload) == 0 {
				return Set{}, fmt.Errorf("evalset: %s has neither a payload nor a case to derive one from", one.ID)
			}
			continue
		}
		parent, found := byID[one.DerivedFrom]
		if !found {
			return Set{}, fmt.Errorf("evalset: %s derives from %s, which is not in the set", one.ID, one.DerivedFrom)
		}
		if len(parent.Payload) == 0 {
			return Set{}, fmt.Errorf("evalset: %s derives from %s, which has no payload of its own", one.ID, one.DerivedFrom)
		}
		set.Cases[i].Payload = parent.Payload
	}

	set.SHA256 = HashOf(set.Cases)
	if manifest.SHA256 != set.SHA256 {
		return Set{}, fmt.Errorf(
			"evalset: the case files hash to %s and the manifest says %s; a case was edited without "+
				"the manifest being regenerated (run `go run ./tools/aieval -freeze -manifest-only`)",
			set.SHA256, manifest.SHA256)
	}
	pinned := map[string]string{}
	for _, entry := range manifest.Cases {
		pinned[entry.ID] = entry.SHA256
	}
	for _, one := range set.Cases {
		want, listed := pinned[one.ID]
		if !listed {
			return Set{}, fmt.Errorf("evalset: %s is on disk and not in the manifest", one.ID)
		}
		if want != one.sha256 {
			return Set{}, fmt.Errorf("evalset: %s does not match the manifest's hash", one.ID)
		}
	}
	if len(pinned) != len(set.Cases) {
		return Set{}, fmt.Errorf("evalset: the manifest lists %d cases and %d are on disk",
			len(pinned), len(set.Cases))
	}
	return set, nil
}

// CaseFromBytes parses one case file and records the hash of the bytes it came from.
//
// Exported because the freezer has to hash exactly what it wrote rather than what it meant to
// write — the same reason `ai.parsePrompt` hashes the file rather than the struct. The two callers
// therefore agree by construction rather than by two people writing the same three lines.
func CaseFromBytes(body []byte) (Case, error) {
	var one Case
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	// A field the parser does not know is a case file somebody wrote by hand against an older
	// shape, and quietly ignoring it would run a case that does not mean what it says.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&one); err != nil {
		return Case{}, err
	}
	if one.ID == "" {
		return Case{}, fmt.Errorf("the case has no id")
	}
	sum := sha256.Sum256(body)
	one.sha256 = hex.EncodeToString(sum[:])
	return one, nil
}

// HashOf is the set's hash: every case id and file hash, sorted, joined.
//
// Over the ids and hashes rather than over a concatenation of the files themselves, so that the
// value does not depend on the order a filesystem happened to return them in.
func HashOf(cases []Case) string {
	lines := make([]string, 0, len(cases))
	for _, one := range cases {
		lines = append(lines, one.ID+" "+one.sha256)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// FileSHA256 is one case file's hash, for the freezer that writes the manifest.
func (c Case) FileSHA256() string { return c.sha256 }

// Counts is how many of each kind the set holds.
func (s Set) Counts() (grounded, hallucination int) {
	for _, one := range s.Cases {
		switch one.Expect {
		case Grounded:
			grounded++
		case Hallucination:
			hallucination++
		}
	}
	return grounded, hallucination
}
