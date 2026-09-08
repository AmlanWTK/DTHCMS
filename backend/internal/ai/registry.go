package ai

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The prompt registry (§10.5, criterion 2).
//
// # Versioned artefacts in the repository
//
// §10.5 asks for prompts that are *"versioned artefacts in the repository with an agent_code,
// semantic version, input schema, output schema, and a changelog … deployed like code, reviewed
// like code"*. That is what `prompts/*.json` is. The files are embedded in the binary rather than
// read from disk, for the same reason the migrations are: a process that read prompt files from a
// directory would send whatever happened to be in that directory on that machine, which is not
// necessarily what was reviewed.
//
// # And a copy in the database, which is the part that earns its keep
//
// An interaction row from eight months ago says `prompt_version: 2.1.0`. Resolving that to the text
// that produced it means either checking out the commit that was deployed then — which nobody does,
// and which is impossible once the answer matters in a dispute — or storing the text. So start-up
// writes each version to `core.ai_prompt_version` and the row is never updated afterwards.
//
// The interesting case is a version that is already there with **different** content. That is
// somebody editing a prompt without bumping the version, and it is the single change that makes
// every stored interaction unreproducible: the record would name a version whose text no longer
// exists anywhere. So it is refused at start-up, loudly, rather than accepted — a process that will
// not start is a bad morning, and a clinical audit trail that quietly stopped meaning anything is a
// worse year.

//go:embed prompts/*.json
var promptFiles embed.FS

// Prompt is one version of one agent's instructions, as the file declares it.
type Prompt struct {
	AgentCode string `json:"agent_code"`
	Version   string `json:"version"`
	Changelog string `json:"changelog"`

	// ModelVersion is pinned and explicit (D-13). The registry refuses an alias.
	ModelVersion string `json:"model_version"`
	// FallbackModelVersion is what answers when the primary is failing. §10.4 A1 wants a Pro-class
	// model for synthesis; when Pro is unreachable, a Flash answer clearly recorded as a fallback
	// is better than no answer, and much better than a fabricated one (D-15).
	FallbackModelVersion string `json:"fallback_model_version"`

	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"max_output_tokens"`
	// TimeoutSeconds is this agent's whole budget, across every attempt. §7.1's five minutes is a
	// promise about a pipeline; a single model call inside it gets far less.
	TimeoutSeconds int `json:"timeout_seconds"`
	// MaxAttempts includes the first. Three means one try and two retries.
	MaxAttempts int `json:"max_attempts"`
	// CacheTTLSeconds is how long an identical input may be answered from a previous call. Zero
	// disables caching for this agent, which is right for anything whose answer depends on
	// something outside the payload.
	CacheTTLSeconds int `json:"cache_ttl_seconds"`

	System       string `json:"system"`
	UserTemplate string `json:"user_template"`

	OutputSchema *Schema `json:"output_schema"`

	// Content is the exact file, and SHA256 is its hash. Both are stored, so a dispute about what
	// a version said is settled by reading it rather than by trusting a number.
	Content string `json:"-"`
	SHA256  string `json:"-"`

	major, minor, patch int
}

// Registry is every prompt version this build contains.
type Registry struct {
	// byAgent holds versions newest-first, so Latest is the head of the slice.
	byAgent map[string][]Prompt
}

// LoadRegistry reads the embedded prompts and checks every one of them.
//
// Everything it refuses, it refuses at start-up. A prompt with an unparseable version, a missing
// schema or a floating model alias is a deployment fault, and the only good time to find one is
// before the process is serving.
func LoadRegistry() (*Registry, error) {
	entries, err := fs.Glob(promptFiles, "prompts/*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)

	registry := &Registry{byAgent: map[string][]Prompt{}}
	for _, name := range entries {
		raw, err := promptFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}
		prompt, err := parsePrompt(string(raw))
		if err != nil {
			return nil, fmt.Errorf("ai: %s: %w", path.Base(name), err)
		}
		// The filename repeats the agent and version on purpose: a directory listing is the
		// quickest review of what is deployed, and a file whose name disagreed with its contents
		// would make that listing a lie.
		want := prompt.AgentCode + "." + prompt.Version + ".json"
		if path.Base(name) != want {
			return nil, fmt.Errorf("ai: %s declares %s %s and should therefore be named %s",
				path.Base(name), prompt.AgentCode, prompt.Version, want)
		}
		registry.byAgent[prompt.AgentCode] = append(registry.byAgent[prompt.AgentCode], prompt)
	}

	for agent, versions := range registry.byAgent {
		sort.Slice(versions, func(i, j int) bool {
			if versions[i].major != versions[j].major {
				return versions[i].major > versions[j].major
			}
			if versions[i].minor != versions[j].minor {
				return versions[i].minor > versions[j].minor
			}
			return versions[i].patch > versions[j].patch
		})
		registry.byAgent[agent] = versions
	}
	if len(registry.byAgent) == 0 {
		return nil, fmt.Errorf("ai: the prompt registry is empty; the embed pattern has stopped matching")
	}
	return registry, nil
}

func parsePrompt(raw string) (Prompt, error) {
	var prompt Prompt
	decoder := json.NewDecoder(strings.NewReader(raw))
	// A field the parser does not know is a typo — `max_tokens` for `max_output_tokens` — and
	// silently ignoring it would deploy a prompt with a budget nobody set.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&prompt); err != nil {
		return Prompt{}, fmt.Errorf("not a prompt file: %w", err)
	}

	major, minor, patch, err := parseSemver(prompt.Version)
	if err != nil {
		return Prompt{}, err
	}
	prompt.major, prompt.minor, prompt.patch = major, minor, patch

	switch {
	case strings.TrimSpace(prompt.AgentCode) == "":
		return Prompt{}, fmt.Errorf("no agent_code")
	case strings.TrimSpace(prompt.Changelog) == "":
		// §10.5 requires a changelog. A version without one is a change nobody can review after
		// the fact, which is the only time anybody wants to.
		return Prompt{}, fmt.Errorf("no changelog: a prompt version has to say why it exists")
	case strings.TrimSpace(prompt.System) == "" && strings.TrimSpace(prompt.UserTemplate) == "":
		return Prompt{}, fmt.Errorf("no prompt text at all")
	case !strings.Contains(prompt.UserTemplate, payloadSlot):
		return Prompt{}, fmt.Errorf("the user template never uses %s, so the payload would not reach the model", payloadSlot)
	case prompt.OutputSchema == nil:
		// Criterion 4 rests on this. An agent without an output schema is an agent whose answer is
		// passed through unvalidated, which is the case the criterion exists to make impossible.
		return Prompt{}, fmt.Errorf("no output_schema: an agent whose answer cannot be validated cannot be registered")
	case prompt.OutputSchema.Type != "object":
		return Prompt{}, fmt.Errorf("output_schema must describe an object")
	case prompt.ModelVersion == "" || strings.Contains(prompt.ModelVersion, "latest") ||
		strings.Contains(prompt.ModelVersion, "preview"):
		// D-13, and D-07's warning that free-tier and preview aliases are retired quickly. A prompt
		// naming an alias is a prompt that will one day be answered by a model nobody evaluated,
		// and the record would say it had used "latest".
		return Prompt{}, fmt.Errorf("model_version %q is an alias; pin an explicit version", prompt.ModelVersion)
	case prompt.FallbackModelVersion == prompt.ModelVersion && prompt.FallbackModelVersion != "":
		return Prompt{}, fmt.Errorf("the fallback model is the primary model, so there is no fallback")
	case prompt.MaxAttempts < 1 || prompt.MaxAttempts > 5:
		return Prompt{}, fmt.Errorf("max_attempts is %d; between 1 and 5", prompt.MaxAttempts)
	case prompt.TimeoutSeconds < 1 || prompt.TimeoutSeconds > 600:
		return Prompt{}, fmt.Errorf("timeout_seconds is %d; between 1 and 600", prompt.TimeoutSeconds)
	case prompt.MaxOutputTokens < 1:
		return Prompt{}, fmt.Errorf("max_output_tokens must be set: an unbounded answer is an unbounded bill")
	case prompt.CacheTTLSeconds < 0:
		return Prompt{}, fmt.Errorf("cache_ttl_seconds cannot be negative")
	}

	prompt.Content = raw
	sum := sha256.Sum256([]byte(raw))
	prompt.SHA256 = hex.EncodeToString(sum[:])
	return prompt, nil
}

// payloadSlot is where the minimised payload is substituted into the user template.
const payloadSlot = "{{payload}}"

func parseSemver(version string) (int, int, int, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("version %q is not major.minor.patch", version)
	}
	out := make([]int, 3)
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0, 0, 0, fmt.Errorf("version %q is not major.minor.patch", version)
		}
		out[i] = value
	}
	return out[0], out[1], out[2], nil
}

// Latest is the highest version of one agent.
func (r *Registry) Latest(agentCode string) (Prompt, bool) {
	versions := r.byAgent[agentCode]
	if len(versions) == 0 {
		return Prompt{}, false
	}
	return versions[0], true
}

// Version is one exact version, for reproducing an old interaction.
func (r *Registry) Version(agentCode, version string) (Prompt, bool) {
	for _, candidate := range r.byAgent[agentCode] {
		if candidate.Version == version {
			return candidate, true
		}
	}
	return Prompt{}, false
}

// All is every version in the build, agent by agent, newest first.
func (r *Registry) All() []Prompt {
	agents := make([]string, 0, len(r.byAgent))
	for agent := range r.byAgent {
		agents = append(agents, agent)
	}
	sort.Strings(agents)

	out := make([]Prompt, 0, len(agents))
	for _, agent := range agents {
		out = append(out, r.byAgent[agent]...)
	}
	return out
}

// Render fills the user template with the minimised payload.
func (p Prompt) Render(payload map[string]any) (string, error) {
	// Indented, and this is not cosmetic: a model reading a wall of minified JSON produces
	// measurably worse structured output than one reading the same object laid out, and the cost
	// is a few tokens of whitespace against an answer that has to be retried.
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(p.UserTemplate, payloadSlot, string(encoded)), nil
}

// Deploy writes every version in the build to the database, refusing a silent edit.
//
// Called once at start-up. It is idempotent for an unchanged build and fatal for a changed prompt
// under an unchanged version — see the note at the top of this file for why that is the right way
// round.
func (r *Registry) Deploy(ctx context.Context, store *Store, now time.Time) error {
	for _, prompt := range r.All() {
		stored, inserted, err := store.DeployPrompt(ctx, prompt, now)
		if err != nil {
			return fmt.Errorf("ai: deploying %s %s: %w", prompt.AgentCode, prompt.Version, err)
		}
		if inserted {
			continue
		}
		if stored != prompt.SHA256 {
			return fmt.Errorf(
				"ai: %s %s is already deployed with different text (stored %s, build %s). "+
					"A prompt that changes under a version number makes every interaction that "+
					"names that version unreproducible. Bump the version instead",
				prompt.AgentCode, prompt.Version, stored[:12], prompt.SHA256[:12])
		}
	}
	return nil
}
