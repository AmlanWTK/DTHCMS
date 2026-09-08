package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/ai/evalset"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform"
)

// Rebuilding the frozen set (CP72).
//
// # This is run deliberately, rarely, and never by CI
//
// Freezing takes the payloads and answers of real runs out of `core.ai_interaction` and writes
// them to disk as cases. It is how the set was created and how it will be extended when there are
// answers from a real model to extend it with; it is not something a build does, because a set
// that regenerates itself is not frozen and a gate against it measures nothing.
//
// The rows it reads are already de-identified twice over: the payload is what the minimiser
// produced, and the answer is what the model returned before the gateway restored anything. Both
// name a pseudonym. The cohort behind them is `cmd/synthload`'s fabricated one. That is three
// independent reasons why what lands on disk carries no patient, and the reason to state all three
// is that this command writes clinical prose into a git repository.
//
// # The injections, and why each one is here
//
// One per failure the checkpoint's testing note names, plus the second shape of date and one
// deliberately subtle case:
//
//	fabricated-hba1c      a laboratory value that appears nowhere in the context
//	digit-swap            a real value with one digit changed — the failure a reader would not see
//	wrong-date-iso        a measurement moved to a date the record does not contain
//	wrong-date-prose      the same, written the way a person writes a date
//	invented-drug         a medicine the system cannot verify, because there is no formulary
//	invented-citation     a fact reference that is not in the index
//	forged-bracket        a citation in prose whose date has been altered by one day
//
// Each case names the arm it must fire on and the token that must be reported, so that a validator
// which caught it for some unrelated reason is a failure rather than a pass.

type frozenCase struct {
	ID          string                    `json:"id"`
	Description string                    `json:"description"`
	Expect      evalset.Expectation       `json:"expect"`
	DerivedFrom string                    `json:"derived_from,omitempty"`
	Injection   string                    `json:"injection,omitempty"`
	Payload     map[string]any            `json:"payload,omitempty"`
	Output      map[string]any            `json:"output"`
	Expected    []evalset.ExpectedFinding `json:"expected_findings,omitempty"`
}

func freezeSet(dir string, count int) error {
	ctx := context.Background()
	rt, err := platform.Boot(ctx, platform.Options{
		Service: "aieval", NeedsDB: true, NoTelemetry: true,
	})
	if err != nil {
		return err
	}
	defer rt.Close()
	if rt.Config.Env.IsProduction() {
		return fmt.Errorf("refused: this writes clinical prose to files and the environment is production")
	}

	rows, err := rt.DB.Pool.Query(ctx, `
		SELECT i.outbound -> 'payload', i.response
		  FROM core.ai_interaction i
		 WHERE i.agent_code = $1
		   AND i.status = 'SUCCEEDED'
		   AND i.outbound IS NOT NULL AND i.response IS NOT NULL
		   -- Only fabricated subjects reach the frozen set. The provenance column is resolved from
		   -- the register rather than claimed by anybody, so this is the same guarantee the tier
		   -- guard rests on rather than a second, weaker one written here.
		   AND i.provenance = 'SYNTHETIC'
		 ORDER BY i.started_at
		 LIMIT $2`, "clinical.synthesis", count)
	if err != nil {
		return err
	}
	defer rows.Close()

	var frozen []frozenCase
	index := 0
	for rows.Next() {
		var payloadRaw, responseRaw []byte
		if err := rows.Scan(&payloadRaw, &responseRaw); err != nil {
			return err
		}
		var payload, response map[string]any
		if err := json.Unmarshal(payloadRaw, &payload); err != nil {
			return err
		}
		if err := json.Unmarshal(responseRaw, &response); err != nil {
			return err
		}
		index++
		frozen = append(frozen, frozenCase{
			ID:     fmt.Sprintf("case-%02d", index),
			Expect: evalset.Grounded,
			Description: "A summary produced by the CP71 pipeline from the synthetic cohort, and " +
				"believed correct: every claim in it was written from a value in the context beside it.",
			Payload: payload, Output: response,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(frozen) == 0 {
		return fmt.Errorf("no successful synthetic interactions found; run `go run ./tools/synthshots` first")
	}

	injected, err := inject(frozen)
	if err != nil {
		return err
	}
	frozen = append(frozen, injected...)

	casesDir := filepath.Join(dir, "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		return err
	}
	existing, err := filepath.Glob(filepath.Join(casesDir, "*.json"))
	if err != nil {
		return err
	}
	for _, name := range existing {
		if err := os.Remove(name); err != nil {
			return err
		}
	}

	type manifestCase struct {
		ID     string `json:"id"`
		SHA256 string `json:"sha256"`
	}
	manifest := struct {
		Name      string         `json:"name"`
		AgentCode string         `json:"agent_code"`
		SHA256    string         `json:"sha256"`
		Notes     string         `json:"notes"`
		Cases     []manifestCase `json:"cases"`
	}{
		Name:      "clinical.synthesis.v1",
		AgentCode: "clinical.synthesis",
		Notes: "Frozen at CP72 from the CP71 synthetic cohort. The GROUNDED answers were written " +
			"by tools/synthshots' deterministic composer, not by a language model: the detection " +
			"rate this set measures is unaffected by that, the false-positive rate is, and " +
			"docs/ai-grounding.md says how much.",
	}

	for _, one := range frozen {
		encoded, err := json.MarshalIndent(one, "", "  ")
		if err != nil {
			return err
		}
		encoded = append(encoded, '\n')
		if err := os.WriteFile(filepath.Join(casesDir, one.ID+".json"), encoded, 0o644); err != nil {
			return err
		}
	}

	// Reload from disk so that the hashes in the manifest are hashes of the bytes that are really
	// there, rather than of the bytes this process intended to write. It is the same reason the
	// prompt registry hashes the file it read rather than the struct it parsed.
	loaded, err := hashCases(casesDir)
	if err != nil {
		return err
	}
	for _, one := range loaded {
		manifest.Cases = append(manifest.Cases, manifestCase{ID: one.ID, SHA256: one.FileSHA256()})
	}
	sort.Slice(manifest.Cases, func(i, j int) bool { return manifest.Cases[i].ID < manifest.Cases[j].ID })
	manifest.SHA256 = evalset.HashOf(loaded)

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("Froze %d cases (%d correct, %d injected) to %s\nSet hash %s\n"+
		"Now run `go run ./tools/aieval -bless` and commit both.\n",
		len(loaded), count, len(injected), casesDir, manifest.SHA256)
	return nil
}

// inject builds the hallucination cases by corrupting the answers of the first few correct ones.
//
// Every injected token is checked against the case's own grounds before it is written: a
// "fabricated" HbA1c that happened to appear somewhere in the payload would be a case that passes
// for the wrong reason and would quietly stop testing anything.
func inject(correct []frozenCase) ([]frozenCase, error) {
	if len(correct) < 4 {
		return nil, fmt.Errorf("need at least four correct cases to derive injections from, have %d", len(correct))
	}

	type recipe struct {
		suffix      string
		description string
		injection   string
		from        int
		mutate      func(out map[string]any) (evalset.ExpectedFinding, error)
	}

	recipes := []recipe{
		{
			suffix: "fabricated-hba1c", from: 0,
			description: "§10.2's own example: a laboratory value the model was never shown, written " +
				"into the narrative as though it had been.",
			injection: "A sentence quoting an HbA1c of 12.4% was appended to narrative_en. No such " +
				"value appears anywhere in the context.",
			mutate: func(out map[string]any) (evalset.ExpectedFinding, error) {
				return appendSentence(out, "HbA1c is 12.4 % and has risen since the last visit.", "12.4")
			},
		},
		{
			suffix: "digit-swap", from: 1,
			description: "The failure a reader would not catch: a real measurement with one digit " +
				"changed, in a sentence that still cites the fact it came from.",
			injection: "One digit of a value quoted in narrative_en was changed. The citation beside " +
				"it is untouched and still resolves.",
			mutate: swapADigit,
		},
		{
			suffix: "wrong-date-iso", from: 2,
			description: "A measurement moved to a date the record does not contain, written the way " +
				"the context writes dates.",
			injection: "A sentence dating a result to 2019-04-02 was appended to narrative_en.",
			mutate: func(out map[string]any) (evalset.ExpectedFinding, error) {
				finding, err := appendSentence(out,
					"The comparison above is against the reading taken on 2019-04-02.", "2019-04-02")
				finding.Arm = ai.ArmDate
				return finding, err
			},
		},
		{
			suffix: "wrong-date-prose", from: 3,
			description: "The same fabrication written the way a person writes a date, which is the " +
				"form a numeric scan alone would miss.",
			injection: "A sentence dating a result to 12 March 2019 was appended to narrative_en.",
			mutate: func(out map[string]any) (evalset.ExpectedFinding, error) {
				finding, err := appendSentence(out,
					"The previous assessment was carried out on 12 March 2019.", "12 March 2019")
				finding.Arm = ai.ArmDate
				return finding, err
			},
		},
		{
			suffix: "invented-drug", from: 0,
			description: "A drafted medication. There is no formulary until CP75, so no drug name " +
				"can be verified and none may be displayed (§10.4 A1).",
			injection: "A draft_medications entry naming Semaglutide was added.",
			mutate: func(out map[string]any) (evalset.ExpectedFinding, error) {
				out["draft_medications"] = []any{map[string]any{
					"drug": "Semaglutide", "dose": "0.25 mg", "frequency": "weekly",
					"route":     "subcutaneous",
					"rationale": "Suggested for weight and glycaemic control.",
				}}
				return evalset.ExpectedFinding{Arm: ai.ArmDrug, Token: "Semaglutide"}, nil
			},
		},
		{
			suffix: "invented-citation", from: 1,
			description: "A fact reference that is not in the index: the model citing something it " +
				"was never given.",
			injection: "The reference obs.tsh:2026-01-05 was added to citations. The context has no " +
				"thyroid analyte in it at all — see docs/synthesis.md on that gap.",
			mutate: func(out map[string]any) (evalset.ExpectedFinding, error) {
				citations, _ := out["citations"].([]any)
				out["citations"] = append(citations, "obs.tsh:2026-01-05")
				return evalset.ExpectedFinding{Arm: ai.ArmCitation, Token: "obs.tsh:2026-01-05"}, nil
			},
		},
		{
			suffix: "forged-bracket", from: 2,
			description: "A bracketed citation in prose whose date has been altered, so the sentence " +
				"reads as sourced and points at nothing.",
			injection: "The date inside one [ref] in narrative_en was moved by a year.",
			mutate:    forgeABracket,
		},
	}

	var out []frozenCase
	for _, r := range recipes {
		parent := correct[r.from]
		clone, err := deepCopy(parent.Output)
		if err != nil {
			return nil, err
		}
		expected, err := r.mutate(clone)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.suffix, err)
		}
		if expected.Arm == "" {
			expected.Arm = ai.ArmNumber
		}
		// The check that keeps a case honest: the token must really be absent from what the model
		// was shown, or the case tests nothing.
		if expected.Arm == ai.ArmNumber || expected.Arm == ai.ArmDate {
			grounds := ai.GroundsFrom(parent.Payload)
			probe := grounds.Check(map[string]any{"probe": expected.Token}, nil)
			if probe.OK() {
				return nil, fmt.Errorf("%s: the injected token %q is already grounded in %s's context, "+
					"so the case would pass for the wrong reason", r.suffix, expected.Token, parent.ID)
			}
		}
		out = append(out, frozenCase{
			ID: parent.ID + "-" + r.suffix, Expect: evalset.Hallucination,
			Description: r.description, Injection: r.injection,
			DerivedFrom: parent.ID, Output: clone,
			Expected: []evalset.ExpectedFinding{expected},
		})
	}
	return out, nil
}

func appendSentence(out map[string]any, sentence, token string) (evalset.ExpectedFinding, error) {
	narrative, ok := out["narrative_en"].(string)
	if !ok {
		return evalset.ExpectedFinding{}, fmt.Errorf("the answer has no narrative_en to corrupt")
	}
	out["narrative_en"] = strings.TrimSpace(narrative) + " " + sentence
	return evalset.ExpectedFinding{Arm: ai.ArmNumber, Token: token}, nil
}

// swapADigit changes one digit of a value the narrative quotes, leaving its citation intact.
//
// This is the subtle case, and the first version of it was wrong in a way worth recording: it
// scanned for "a run of digits containing a dot" and found `08.` at the end of the sentence
// "attending a follow-up visit on 2026-09-08.", turning the *date* into 2026-09-58. The harness
// caught it — the case fired the DATE arm instead of the NUMBER arm it declared, and a case caught
// on the wrong arm is reported as a miss rather than as a pass, which is exactly the property that
// made the mistake visible in one run. Hence the deliberate exclusions below.
func swapADigit(out map[string]any) (evalset.ExpectedFinding, error) {
	narrative, ok := out["narrative_en"].(string)
	if !ok {
		return evalset.ExpectedFinding{}, fmt.Errorf("the answer has no narrative_en to corrupt")
	}
	inBracket := make([]bool, len(narrative))
	depth := 0
	for i, r := range narrative {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		}
		inBracket[i] = depth > 0 || r == ']'
	}
	for _, span := range decimalValue.FindAllStringIndex(narrative, -1) {
		start, end := span[0], span[1]
		if inBracket[start] {
			continue
		}
		// Not part of a date, on either side. A hyphen before or after a number in this prose is
		// either an ISO date or a signed delta, and neither is the measurement this case is about.
		if start > 0 && (narrative[start-1] == '-' || narrative[start-1] == '.') {
			continue
		}
		if end < len(narrative) && narrative[end] == '-' {
			continue
		}
		original := narrative[start:end]
		lead := original[0]
		swapped := byte('5')
		if lead == '5' {
			swapped = '8'
		}
		corrupted := string(swapped) + original[1:]
		out["narrative_en"] = narrative[:start] + corrupted + narrative[end:]
		return evalset.ExpectedFinding{Arm: ai.ArmNumber, Token: corrupted}, nil
	}
	return evalset.ExpectedFinding{}, fmt.Errorf("no decimal value in the narrative to corrupt")
}

// decimalValue is a number with a fractional part: a measurement rather than a count, and never
// part of an ISO date, which has no dot in it.
var decimalValue = regexp.MustCompile(`[0-9]+\.[0-9]+`)

// forgeABracket moves the year inside one bracketed citation.
func forgeABracket(out map[string]any) (evalset.ExpectedFinding, error) {
	narrative, ok := out["narrative_en"].(string)
	if !ok {
		return evalset.ExpectedFinding{}, fmt.Errorf("the answer has no narrative_en to corrupt")
	}
	open := strings.Index(narrative, "[")
	closed := strings.Index(narrative, "]")
	if open < 0 || closed < open {
		return evalset.ExpectedFinding{}, fmt.Errorf("no bracketed citation in the narrative to forge")
	}
	inner := narrative[open+1 : closed]
	forged := strings.Replace(inner, ":2026-", ":2025-", 1)
	if forged == inner {
		return evalset.ExpectedFinding{}, fmt.Errorf("the first citation %q has no year to move", inner)
	}
	out["narrative_en"] = narrative[:open+1] + forged + narrative[closed:]
	return evalset.ExpectedFinding{Arm: ai.ArmCitation, Token: forged}, nil
}

func deepCopy(in map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// hashCases re-reads what was just written, so the manifest pins the bytes on disk.
func hashCases(dir string) ([]evalset.Case, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []evalset.Case
	for _, name := range names {
		body, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		one, err := evalset.CaseFromBytes(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(name), err)
		}
		out = append(out, one)
	}
	return out, nil
}

// rewriteManifest re-hashes the case files that are on disk and rewrites the manifest, without
// touching a case or needing a database.
//
// # Why this exists, and why it is a separate door from -freeze
//
// The manifest's whole job is to notice that a case changed. It does that by hashing bytes, which
// means it also notices a change that is not a change: a formatter tidying whitespace alters every
// hash and leaves thirteen tests red over content nobody edited. That happened — `prettier --write`
// reformatted the cases, and the gate correctly refused to run against a set it could no longer
// vouch for.
//
// The wrong repair is `-freeze`, which rebuilds the cases from `core.ai_interaction` and would
// silently replace the frozen answers with whatever the database holds today. That is how a
// regression gate quietly starts measuring a different question. This does the narrow thing
// instead: the bytes on disk become the new canonical bytes, and the operator is asserting that
// only their formatting moved.
//
// It is deliberately not automatic. A hash mismatch should stop a build and make somebody look;
// running this is that person saying they looked.
func rewriteManifest(dir string) error {
	casesDir := filepath.Join(dir, "cases")

	// Read the manifest that is there, so the prose in it survives — it explains what the set
	// measures and what it does not, and regenerating that from a literal would let it drift from
	// whatever the set has since become.
	existing, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("reading the manifest to keep its notes: %w", err)
	}
	var manifest struct {
		Name      string `json:"name"`
		AgentCode string `json:"agent_code"`
		SHA256    string `json:"sha256"`
		Notes     string `json:"notes"`
		Cases     []struct {
			ID     string `json:"id"`
			SHA256 string `json:"sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(existing, &manifest); err != nil {
		return fmt.Errorf("parsing the manifest: %w", err)
	}
	before := manifest.SHA256

	loaded, err := hashCases(casesDir)
	if err != nil {
		return err
	}
	if len(loaded) == 0 {
		return fmt.Errorf("no cases found under %s; refusing to write a manifest for an empty set", casesDir)
	}

	manifest.Cases = manifest.Cases[:0]
	for _, one := range loaded {
		manifest.Cases = append(manifest.Cases, struct {
			ID     string `json:"id"`
			SHA256 string `json:"sha256"`
		}{ID: one.ID, SHA256: one.FileSHA256()})
	}
	sort.Slice(manifest.Cases, func(i, j int) bool { return manifest.Cases[i].ID < manifest.Cases[j].ID })
	manifest.SHA256 = evalset.HashOf(loaded)

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(encoded, '\n'), 0o644); err != nil {
		return err
	}

	fmt.Printf("Re-hashed %d cases in place. No case content was read or rewritten.\n"+
		"  set hash %s\n       now %s\n\n"+
		"Only do this when you know the change was formatting. If a case's *content* moved, the\n"+
		"numbers this set produces have moved with it, and the baseline needs re-blessing too.\n",
		len(loaded), before, manifest.SHA256)
	return nil
}
