package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
)

// The AI gateway (CP70), the half that needs no database.
//
// The five acceptance criteria and where they are proved:
//
//	1   no identifier reaches a provider — this file, adversarially, and ai_db_test.go for the
//	    constraint that backs it up in the database;
//	1b  a real-patient payload cannot go out on a free credential — ai_db_test.go, because the
//	    decision is a database lookup and mocking it would be testing the mock;
//	2   every call recorded — ai_db_test.go;
//	3   provider swappable by configuration — this file (the interface has two implementations and
//	    the gateway holds neither concretely) and ai_db_test.go (the same pipeline, both providers);
//	4   invalid output rejected, retried, then failed cleanly — this file for the validator,
//	    ai_db_test.go for the retry budget and the clean failure;
//	5   budget alerts fire at configured thresholds — ai_db_test.go.

const testPepper = "dGVzdC1wZXBwZXItZm9yLXVuaXQtdGVzdHMtb25seS0xMjM0"

func minimiser(t *testing.T) *ai.Minimiser {
	t.Helper()
	m, err := ai.NewMinimiser(testPepper)
	if err != nil {
		t.Fatalf("building the minimiser: %v", err)
	}
	return m
}

// ayesha is the subject every minimisation test uses: a Bangladeshi patient with the identifier
// shapes that actually turn up at this clinic — a Bangla name, an English name, a mobile number
// written with a hyphen, and a thirteen-digit national ID.
func ayesha() ai.Subject {
	return ai.Subject{
		PatientID: uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		AgeMonths: 511,
		Sex:       "female",
		Identifiers: map[string]string{
			"name_en":       "Ayesha Rahman",
			"name_bn":       "আয়েশা রহমান",
			"phone":         "01711-234567",
			"nid":           "1990123456789",
			"guardian_name": "Abdul Rahman",
			"address":       "House 12, Road 3, Faridpur",
		},
	}
}

// --- criterion 1: no identifier reaches a provider ---

// TestIdentifiersInFreeTextDoNotSurviveMinimisation is the checkpoint's named case.
//
// The plan says it in as many words: *"PHI leakage through free-text fields is the main one"*, and
// names the shape — an identifier in the middle of clinical prose, in either script. Every string
// below is written the way a counsellor at this clinic types one, and none of them is a
// well-formed field.
func TestIdentifiersInFreeTextDoNotSurviveMinimisation(t *testing.T) {
	subject := ayesha()

	for name, note := range map[string]string{
		// The plan's own example.
		"a phone number in an English sentence":     "Poor adherence this quarter; rang her son Rafiq on 01711-234567 and he confirmed she stopped metformin.",
		"a phone number in Bangla prose":            "রোগীর ছেলেকে ০১৭১১২৩৪৫৬৭ নম্বরে ফোন করা হয়েছে, তিনি জানিয়েছেন ওষুধ বন্ধ আছে।",
		"a national ID quoted mid-sentence":         "Insurance rejected the claim; NID on the form reads 1990123456789 which matches the card.",
		"a number retyped without its hyphen":       "Contact 01711234567 (mobile) before the next appointment.",
		"an international prefix and spaces":        "Best number is +880 1711 234567, she answers after six.",
		"the patient's own name inside a narrative": "Ayesha Rahman attends alone; she reports two hypoglycaemic episodes since March.",
		"the Bangla name inside Bangla narrative":   "আয়েশা রহমান একাই আসেন; মার্চ থেকে দুইবার সুগার কমে গেছে বলে জানান।",
		"a relative's name with an honorific":       "Escorted by Md. Rafiqul Islam, who takes the prescription to the pharmacy.",
		"an email address":                          "Follow-up letter bounced from ayesha.rahman@example.com.",
		"an address in the middle of a plan":        "Home visit arranged; she lives at House 12, Road 3, Faridpur, close to the clinic.",
		"several at once, in one sentence":          "Call 01711-234567 or email ayesha.rahman@example.com; NID 1990123456789 on file.",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := minimiser(t).Minimise("gateway.echo", subject, map[string]any{
				"counselling_note": note,
			})
			if err != nil {
				t.Fatalf("minimising: %v", err)
			}

			encoded, err := json.Marshal(out.Payload)
			if err != nil {
				t.Fatal(err)
			}
			sent := string(encoded)

			// Every identifier, and also the bare-digit form of every number, because a check that
			// only compared exact strings would pass "01711234567" when the identifier on file is
			// "01711-234567" — which is the case a clinician retyping a number produces.
			for label, value := range subject.Identifiers {
				if strings.Contains(sent, value) {
					t.Errorf("%s reached the payload:\n%s", label, sent)
				}
			}
			for _, bare := range []string{"01711234567", "1990123456789", "8801711234567"} {
				if strings.Contains(stripPunctuation(sent), bare) {
					t.Errorf("the digits of an identifier reached the payload (%s):\n%s", bare, sent)
				}
			}
			if strings.Contains(sent, "Rafiqul") {
				t.Errorf("a third party's honorific-prefixed name reached the payload:\n%s", sent)
			}
			// And the scrubber must say what it did, because the outbound log is the mitigation
			// and a log that recorded nothing would say the scrubber never ran.
			if len(out.Removed) == 0 {
				t.Errorf("nothing was recorded as removed, yet something was:\n%s", sent)
			}
		})
	}
}

func stripPunctuation(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "+", "", "(", "", ")", "", ".", "").Replace(s)
}

// TestTheScrubberLeavesTheClinicalPictureIntact is the other half of the criterion, and the half a
// scrubber that redacted everything would pass without.
//
// D-08 is default deny, not deny everything: a summary the physician cannot read is a failure of
// this checkpoint too, just a quieter one. The thresholds in patterns.go were chosen against
// exactly these strings.
func TestTheScrubberLeavesTheClinicalPictureIntact(t *testing.T) {
	for name, note := range map[string]string{
		"vitals":            "BP 120/80, pulse 72, weight 61.4 kg.",
		"a series of dates": "HbA1c 8.2 on 2026-03-12, 7.6 on 2026-06-04, 7.1 on 2026-09-01.",
		"a dose":            "Metformin 500 mg twice daily, gliclazide 80 mg in the morning.",
		"several numbers":   "Fasting 7.8 9.1 8.4 over three mornings; post-prandial 12 14 16.",
		"bangla clinical":   "রক্তচাপ ১২০/৮০, ওজন ৬১.৪ কেজি, সুগার নিয়ন্ত্রণে নেই।",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := minimiser(t).Minimise("gateway.echo",
				ai.Subject{PatientID: uuid.New()}, map[string]any{"note": note})
			if err != nil {
				t.Fatalf("minimising: %v", err)
			}
			if got := out.Payload["note"]; got != note {
				t.Errorf("clinical prose was altered by the scrubber:\n  before: %s\n  after:  %v", note, got)
			}
		})
	}
}

// TestIdentifiersAreFoundAtEveryDepth. `{"visit": {"patient": {"phone": …}}}` hides an identifier
// two levels down, and a check that only looked at the top level would pass exactly the payload
// somebody is most likely to write.
func TestIdentifiersAreFoundAtEveryDepth(t *testing.T) {
	_, err := minimiser(t).Minimise("gateway.echo", ayesha(), map[string]any{
		"visits": []any{
			map[string]any{"date": "2026-03-12", "notes": "routine"},
			map[string]any{"date": "2026-06-04", "guardian": map[string]any{
				"guardian_phone": "01711-234567",
			}},
		},
	})
	if !errors.Is(err, ai.ErrPayloadNamesAPerson) {
		t.Fatalf("a nested identifier key was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "guardian_phone") {
		t.Errorf("the refusal must name the key so the fix is obvious, got: %v", err)
	}
}

// TestEveryIdentifierKeyIsRefusedAndEveryClinicalKeyIsNot walks the shared list itself.
//
// Not a hand-written selection of keys: the whole of logging.PHIKeys, so a key added there tomorrow
// is covered by this test the moment it is added. That is the property the class column exists for,
// and the direction of each answer is what the AI gateway needs and the log handler does not.
func TestEveryIdentifierKeyIsRefusedAndEveryClinicalKeyIsNot(t *testing.T) {
	for key, entry := range logging.PHIKeys {
		t.Run(key, func(t *testing.T) {
			_, err := minimiser(t).Minimise("gateway.echo", ayesha(), map[string]any{
				key: "some value that is long enough to be interesting",
			})
			switch entry.Class {
			case logging.ClassClinical:
				// A diagnosis is what the model is being asked about. Refusing it would make the
				// gateway useless for the thing §7.1 exists to do.
				if err != nil {
					t.Errorf("%q is CLINICAL and was refused: %v", key, err)
				}
			default:
				if !errors.Is(err, ai.ErrPayloadNamesAPerson) {
					t.Errorf("%q is %s and was accepted", key, entry.Class)
				}
			}
		})
	}
}

// TestSuffixedIdentifierKeysAreRefused. `guardian_phone` and `patient_name` are what a developer
// reaches for when the bare key feels wrong, and they are the same rule.
func TestSuffixedIdentifierKeysAreRefused(t *testing.T) {
	for _, key := range []string{"guardian_phone", "emergency_contact_phone", "mother_name_bn", "next_of_kin_address"} {
		if _, err := minimiser(t).Minimise("gateway.echo", ayesha(),
			map[string]any{key: "value"}); !errors.Is(err, ai.ErrPayloadNamesAPerson) {
			t.Errorf("%q was accepted", key)
		}
	}
}

// --- strip and restore ---

// TestRestorePutsTheIdentifiersBack is the other half of the mechanism the checkpoint asks for.
//
// A summary reading "PT-3f9a12bc0d44 has had diabetes for eleven years" is correct and useless. The
// physician needs the name, and the name never left the building to produce the sentence.
func TestRestorePutsTheIdentifiersBack(t *testing.T) {
	subject := ayesha()
	out, err := minimiser(t).Minimise("gateway.echo", subject, map[string]any{
		"note": "Ayesha Rahman was escorted by Abdul Rahman.",
	})
	if err != nil {
		t.Fatal(err)
	}

	// What the model saw, and what a model would plausibly echo back.
	scrubbed, _ := out.Payload["note"].(string)
	if strings.Contains(scrubbed, "Ayesha") {
		t.Fatalf("the name survived minimisation: %q", scrubbed)
	}

	restored := out.Restore("Summary: " + scrubbed)
	if !strings.Contains(restored, "Ayesha Rahman") {
		t.Errorf("the patient's name was not restored: %q", restored)
	}
	if !strings.Contains(restored, "Abdul Rahman") {
		t.Errorf("the guardian's name was not restored: %q", restored)
	}
}

// TestRestoreReachesEveryStringInAnAnswer, because a model's answer is an object and the name it
// used may be three levels inside it.
func TestRestoreReachesEveryStringInAnAnswer(t *testing.T) {
	subject := ayesha()
	out, err := minimiser(t).Minimise("gateway.echo", subject, map[string]any{
		"note": "Ayesha Rahman attends alone.",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSuffix(strings.TrimPrefix(out.Payload["note"].(string), ""), " attends alone.")

	answer := map[string]any{
		"narrative": token + " is due for review.",
		"points":    []any{map[string]any{"text": "Discuss adherence with " + token + "."}},
	}
	restored, ok := out.RestoreInto(answer).(map[string]any)
	if !ok {
		t.Fatal("RestoreInto did not return an object")
	}
	encoded, _ := json.Marshal(restored)
	if strings.Count(string(encoded), "Ayesha Rahman") != 2 {
		t.Errorf("the name was not restored everywhere it appeared: %s", encoded)
	}
}

// TestTheLongestIdentifierIsSubstitutedFirst.
//
// "Ayesha Rahman" has to go before "Ayesha", or the shorter one wins and leaves "Rahman" in the
// text — a surname reaching a provider through the mechanism that exists to stop exactly that.
func TestTheLongestIdentifierIsSubstitutedFirst(t *testing.T) {
	subject := ai.Subject{
		PatientID:   uuid.New(),
		Identifiers: map[string]string{"given": "Ayesha", "full": "Ayesha Rahman"},
	}
	out, err := minimiser(t).Minimise("gateway.echo", subject, map[string]any{
		"note": "Ayesha Rahman was seen today.",
	})
	if err != nil {
		t.Fatal(err)
	}
	note, _ := out.Payload["note"].(string)
	if strings.Contains(note, "Rahman") {
		t.Errorf("a surname was left behind by a shorter substitution: %q", note)
	}
}

// TestSubjectTokensCannotCollideWithPatternReplacements.
//
// Restore only knows how to put back what it took out. A caller labelling an identifier "name"
// would produce `[NAME]` if the tokens were unprefixed, collide with the honorific pattern's
// replacement, and have the subject's name substituted into a place where some *third party's* name
// had been scrubbed — turning the scrubber into a leak.
func TestSubjectTokensCannotCollideWithPatternReplacements(t *testing.T) {
	for _, pattern := range ai.DefaultPatterns {
		if strings.Contains(pattern.Replacement, "[SUBJECT_") {
			t.Errorf("pattern %q replaces with %q, which is in the subject tokens' namespace",
				pattern.Kind, pattern.Replacement)
		}
	}

	subject := ai.Subject{
		PatientID:   uuid.New(),
		Identifiers: map[string]string{"name": "Ayesha Rahman"},
	}
	out, err := minimiser(t).Minimise("gateway.echo", subject, map[string]any{
		"note": "Ayesha Rahman came with Md. Rafiqul.",
	})
	if err != nil {
		t.Fatal(err)
	}
	restored := out.Restore(out.Payload["note"].(string))
	if strings.Count(restored, "Ayesha Rahman") != 1 {
		t.Errorf("restoration put the subject's name somewhere it had not been: %q", restored)
	}
}

// TestAVeryShortIdentifierIsNotSubstituted. Replacing every "A" in a clinical note would destroy
// the note and protect nobody.
func TestAVeryShortIdentifierIsNotSubstituted(t *testing.T) {
	subject := ai.Subject{PatientID: uuid.New(), Identifiers: map[string]string{"initial": "A"}}
	out, err := minimiser(t).Minimise("gateway.echo", subject, map[string]any{
		"note": "A routine visit. Adherence adequate.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Payload["note"]; got != "A routine visit. Adherence adequate." {
		t.Errorf("a one-character identifier was substituted: %v", got)
	}
}

// TestThePseudonymIsStablePerAgentAndDifferentBetweenThem.
//
// Stable, because the response cache is keyed on the hash of a payload containing it — a fresh
// random token per call would make the cache never hit, which kills §10.3's own cost control.
// Different per agent, so that two agents' outbound logs cannot be joined by anybody who obtains
// both.
func TestThePseudonymIsStablePerAgentAndDifferentBetweenThem(t *testing.T) {
	m := minimiser(t)
	subject := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	first := m.Pseudonym(subject, "gateway.echo")
	if second := m.Pseudonym(subject, "gateway.echo"); first != second {
		t.Errorf("the pseudonym is not stable: %q then %q", first, second)
	}
	if other := m.Pseudonym(subject, "clinical.synthesis"); other == first {
		t.Error("the same patient has the same pseudonym under two agents; the logs can be joined")
	}
	if other := m.Pseudonym(uuid.New(), "gateway.echo"); other == first {
		t.Error("two patients share a pseudonym")
	}
	if strings.Contains(first, subject.String()[:8]) {
		t.Errorf("the pseudonym contains part of the subject id: %q", first)
	}
}

// TestAPseudonymNeverLooksLikeAnIdentifierToTheScrubber.
//
// The regression test for a defect that was found the only way it could have been. The pseudonym
// was twelve hex characters, so roughly one in six contained a run of seven digits — which is
// exactly what `digit_run_latin` catches. The gateway's own placeholder tripped the gateway's own
// scrubber, the check constraint refused the record, and the call failed: not on an unusual name,
// not on a payload anybody wrote, but on one subject in six, at random.
//
// Ten thousand subjects rather than a handful, because at one in six a three-case test would have
// been green about half the time.
func TestAPseudonymNeverLooksLikeAnIdentifierToTheScrubber(t *testing.T) {
	m := minimiser(t)
	patterns, err := ai.CompilePatterns(ai.DefaultPatterns)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		pseudonym := m.Pseudonym(uuid.New(), "gateway.echo")
		for _, pattern := range patterns {
			if pattern.Matches(pseudonym) {
				t.Fatalf("the pseudonym %q matches the %s pattern; the gateway would refuse its own payload",
					pseudonym, pattern.Kind)
			}
		}
	}
}

// TestAPseudonymCannotBeReproducedWithoutThePepper. It is an HMAC, and a deployment that leaked its
// outbound log without its pepper has leaked pseudonyms and not patients.
func TestAPseudonymCannotBeReproducedWithoutThePepper(t *testing.T) {
	subject := uuid.New()
	ours := minimiser(t).Pseudonym(subject, "gateway.echo")

	theirs, err := ai.NewMinimiser("a-different-pepper-entirely")
	if err != nil {
		t.Fatal(err)
	}
	if theirs.Pseudonym(subject, "gateway.echo") == ours {
		t.Error("the pseudonym does not depend on the pepper")
	}
}

// TestTheSubjectBlockIsWrittenByTheGatewayNotTheCaller. D-08 permits exactly two demographics
// through; they are put there by the minimiser so a caller cannot decide to send a third.
func TestTheSubjectBlockIsWrittenByTheGatewayNotTheCaller(t *testing.T) {
	out, err := minimiser(t).Minimise("gateway.echo", ayesha(), map[string]any{"note": "routine"})
	if err != nil {
		t.Fatal(err)
	}
	block, ok := out.Payload["subject"].(map[string]any)
	if !ok {
		t.Fatal("no subject block was written")
	}
	if block["age_months"] != 511 || block["sex"] != "female" {
		t.Errorf("the subject block is wrong: %v", block)
	}
	if block["pseudonym"] != out.Pseudonym {
		t.Errorf("the subject block does not carry the pseudonym: %v", block)
	}
	for _, forbidden := range []string{"dob", "birth_date", "name", "name_en", "nid"} {
		if _, present := block[forbidden]; present {
			t.Errorf("the subject block carries %q", forbidden)
		}
	}
}

// --- criterion 4: the validator ---

func schema() *ai.Schema {
	max := 400
	min := 1
	lo, hi := 0.0, 1000.0
	return &ai.Schema{
		Type:     "object",
		Required: []string{"summary", "field_count"},
		Properties: map[string]*ai.Schema{
			"summary":     {Type: "string", MinLength: &min, MaxLength: &max},
			"field_count": {Type: "integer", Minimum: &lo, Maximum: &hi},
		},
	}
}

func TestTheValidatorRefusesWhatItShould(t *testing.T) {
	for name, tc := range map[string]struct {
		answer string
		reason string
	}{
		"prose instead of JSON": {
			answer: "Sure! Here is your summary: the patient is doing well.",
			reason: "not JSON",
		},
		"a missing required field": {
			answer: `{"summary":"fine"}`,
			reason: "required and absent",
		},
		"the wrong type": {
			answer: `{"summary":"fine","field_count":"three"}`,
			reason: "expected a number",
		},
		"a field nobody asked for": {
			answer: `{"summary":"fine","field_count":3,"confidence":0.9}`,
			reason: "not in the schema",
		},
		"a number outside its range": {
			answer: `{"summary":"fine","field_count":9999}`,
			reason: "above the maximum",
		},
		"an empty string where one is required": {
			answer: `{"summary":"","field_count":3}`,
			reason: "shorter than",
		},
		"an explanation after the object": {
			answer: `{"summary":"fine","field_count":3} — hope that helps!`,
			reason: "content after the JSON value",
		},
		"an array at the top level": {
			answer: `[{"summary":"fine","field_count":3}]`,
			reason: "expected an object",
		},
	} {
		t.Run(name, func(t *testing.T) {
			object, violations := schema().Validate(tc.answer)
			if len(violations) == 0 {
				t.Fatalf("accepted: %s", tc.answer)
			}
			if object != nil {
				t.Error("a rejected answer must not be handed back half-parsed")
			}
			var joined []string
			for _, v := range violations {
				joined = append(joined, v.String())
			}
			if !strings.Contains(strings.Join(joined, "; "), tc.reason) {
				t.Errorf("violations do not mention %q: %v", tc.reason, joined)
			}
		})
	}
}

// TestTheValidatorAcceptsWhatItShould, including the markdown fence every model reaches for
// whatever the instruction said. Unwrapping it here means the retry budget is spent on real
// disagreements rather than on three backticks.
func TestTheValidatorAcceptsWhatItShould(t *testing.T) {
	for name, answer := range map[string]string{
		"plain":                    `{"summary":"fine","field_count":3}`,
		"fenced":                   "```json\n{\"summary\":\"fine\",\"field_count\":3}\n```",
		"fenced untagged":          "```\n{\"summary\":\"fine\",\"field_count\":3}\n```",
		"surrounded by whitespace": "  \n {\"summary\":\"fine\",\"field_count\":3}  \n ",
	} {
		t.Run(name, func(t *testing.T) {
			object, violations := schema().Validate(answer)
			if len(violations) > 0 {
				t.Fatalf("rejected: %v", violations)
			}
			if object["summary"] != "fine" {
				t.Errorf("parsed wrongly: %v", object)
			}
		})
	}
}

// TestTheMockAnswersItsOwnSchema.
//
// If it did not, every test of the happy path would silently be a test of the retry path: CI would
// be green, three times slower, and testing something other than what it claimed.
func TestTheMockAnswersItsOwnSchema(t *testing.T) {
	registry, err := ai.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range registry.All() {
		t.Run(prompt.AgentCode+"@"+prompt.Version, func(t *testing.T) {
			response, err := ai.NewMock().Generate(context.Background(), ai.ProviderRequest{
				ModelVersion: prompt.ModelVersion, System: prompt.System, User: "x",
				OutputSchema: prompt.OutputSchema,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, violations := prompt.OutputSchema.Validate(response.Text); len(violations) > 0 {
				t.Errorf("the mock's answer fails %s's own schema: %v", prompt.AgentCode, violations)
			}
			if response.ModelVersion != ai.MockModelVersion {
				t.Errorf("the mock reported %q; it must answer as itself so the record is honest",
					response.ModelVersion)
			}
		})
	}
}

func TestTheMockIsDeterministic(t *testing.T) {
	req := ai.ProviderRequest{ModelVersion: "mock-000", User: "the same prompt", OutputSchema: schema()}
	first, err := ai.NewMock().Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ai.NewMock().Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != second.Text {
		t.Error("the same prompt produced two answers; a cache-hit test would then pass for the wrong reason")
	}
}

// --- the registry ---

func TestEveryPromptInTheBuildIsUsable(t *testing.T) {
	registry, err := ai.LoadRegistry()
	if err != nil {
		t.Fatalf("the registry does not load: %v", err)
	}
	prompts := registry.All()
	if len(prompts) == 0 {
		t.Fatal("the registry is empty; the embed pattern has stopped matching")
	}
	for _, prompt := range prompts {
		if strings.Contains(prompt.ModelVersion, "latest") || strings.Contains(prompt.ModelVersion, "preview") {
			t.Errorf("%s pins an alias: %s", prompt.AgentCode, prompt.ModelVersion)
		}
		if prompt.OutputSchema == nil {
			t.Errorf("%s has no output schema, so its answer would be passed through unvalidated", prompt.AgentCode)
		}
		if strings.TrimSpace(prompt.Changelog) == "" {
			t.Errorf("%s %s has no changelog", prompt.AgentCode, prompt.Version)
		}
		if prompt.SHA256 == "" || len(prompt.SHA256) != 64 {
			t.Errorf("%s %s has no content hash", prompt.AgentCode, prompt.Version)
		}
		rendered, err := prompt.Render(map[string]any{"probe": "value"})
		if err != nil {
			t.Fatalf("%s does not render: %v", prompt.AgentCode, err)
		}
		if !strings.Contains(rendered, "probe") {
			t.Errorf("%s renders without the payload reaching the model", prompt.AgentCode)
		}
		if strings.Contains(rendered, "{{payload}}") {
			t.Errorf("%s left the payload slot unfilled", prompt.AgentCode)
		}
	}
}

// TestNoPromptCarriesAnIdentifier. A prompt is a template with slots; a literal phone number
// committed into one would be sent on every invocation for as long as that version was deployed.
// The database has the same constraint; this catches it before the migration does.
func TestNoPromptCarriesAnIdentifier(t *testing.T) {
	registry, err := ai.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	patterns, err := ai.CompilePatterns(ai.DefaultPatterns)
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range registry.All() {
		for _, pattern := range patterns {
			if pattern.Matches(prompt.Content) {
				t.Errorf("%s %s matches the %s pattern; a prompt must not carry an identifier",
					prompt.AgentCode, prompt.Version, pattern.Kind)
			}
		}
	}
}

// --- the circuit breaker ---

// TestTheBreakerOpensAfterConsecutiveFailuresAndProbesOnce.
//
// What it protects is us, not the provider: with Gemini down, every synthesis in the queue spends
// its whole timeout waiting, three times over, and §7.1's five minutes fails for every patient in
// the building rather than for the one whose call was in flight.
func TestTheBreakerOpensAfterConsecutiveFailuresAndProbesOnce(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	breaker := ai.NewBreaker(3, 30*time.Second, clock)

	for i := 0; i < 2; i++ {
		breaker.Failed("m")
		if !breaker.Allow("m") {
			t.Fatalf("the breaker opened after %d failures; one is a packet and two is a coincidence", i+1)
		}
	}
	breaker.Failed("m")
	if breaker.Allow("m") {
		t.Fatal("the breaker did not open after three consecutive failures")
	}
	if !breaker.Open("m") {
		t.Error("Open disagrees with Allow about an open circuit")
	}

	// A second model is unaffected: the registered fallback is exactly what must still be
	// reachable when the primary is not.
	if !breaker.Allow("other") {
		t.Error("opening one model's circuit closed another's")
	}

	// Half-open: one probe after the cooldown, and only one.
	now = now.Add(31 * time.Second)
	if !breaker.Allow("m") {
		t.Fatal("the breaker never re-opened after its cooldown")
	}
	if breaker.Allow("m") {
		t.Error("a second call was let through while the probe was still outstanding")
	}

	// A failed probe re-opens for a fresh cooldown rather than earning three more failures.
	breaker.Failed("m")
	if breaker.Allow("m") {
		t.Error("a failed probe did not re-open the circuit")
	}

	// And a success closes it.
	now = now.Add(31 * time.Second)
	if !breaker.Allow("m") {
		t.Fatal("no second probe after the second cooldown")
	}
	breaker.Succeeded("m")
	if !breaker.Allow("m") || breaker.Open("m") {
		t.Error("a success did not close the circuit")
	}
}

// TestObservingTheBreakerDoesNotConsumeItsProbe.
//
// A metric callback asking the breaker's state every fifteen seconds would otherwise take the one
// request meant to find out whether the provider had recovered, and the circuit would stay open for
// as long as anything was watching it.
func TestObservingTheBreakerDoesNotConsumeItsProbe(t *testing.T) {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	breaker := ai.NewBreaker(1, 10*time.Second, func() time.Time { return now })
	breaker.Failed("m")

	now = now.Add(11 * time.Second)
	for i := 0; i < 5; i++ {
		breaker.Open("m")
	}
	if !breaker.Allow("m") {
		t.Fatal("asking the breaker's state consumed the half-open probe")
	}
}

// --- cost arithmetic ---

// TestCostIsIntegerAndNeverOptimistic.
//
// Money in a float is a number that stops adding up over a month. Rounding up means the meter is
// never optimistic, which is the direction a budget alert has to err in: a meter that under-reported
// would cross its threshold late, which is the same as not having one.
func TestCostIsIntegerAndNeverOptimistic(t *testing.T) {
	flash := ai.Model{InputMicroUSDPerMillion: 300000, OutputMicroUSDPerMillion: 2500000}

	if got := flash.Cost(1_000_000, 1_000_000); got != 2_800_000 {
		t.Errorf("a million tokens each way cost %d micro-dollars, want 2800000", got)
	}
	// One token of input is 0.3 micro-dollars, which must not round to nothing: a million calls
	// each rounding a fraction to zero is how a meter reports nothing while the invoice arrives.
	if got := flash.Cost(1, 0); got != 1 {
		t.Errorf("one input token cost %d, want 1 (rounded up, never down)", got)
	}
	if got := flash.Cost(0, 0); got != 0 {
		t.Errorf("nothing cost %d", got)
	}
	mock := ai.Model{}
	if got := mock.Cost(10_000, 10_000); got != 0 {
		t.Errorf("the mock cost %d; it must be free by arithmetic rather than by a special case", got)
	}
}
