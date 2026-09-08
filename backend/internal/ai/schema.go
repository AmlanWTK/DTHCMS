package ai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Strict output validation (§10.3 step 6, criterion 4).
//
// # Why this is hand-written rather than a JSON Schema library
//
// Two reasons, and the second is the interesting one. The mundane one is that the module proxy is
// not reachable from this environment, so a dependency could not be added even if one were wanted.
//
// The one that would still apply: a full JSON Schema implementation is *permissive by default*.
// `additionalProperties` is true unless you say otherwise, a missing `type` matches anything, and
// an unknown keyword is ignored rather than refused. Every one of those defaults is the wrong way
// round for a clinical system reading text produced by a model, where the failure being guarded
// against is precisely a field nobody expected carrying a value nobody validated. Here the zero
// value of [Schema] refuses everything it was not told about: extra properties are a violation
// unless AdditionalProperties is set, and a schema with no Type is a programming error rather than
// a wildcard.
//
// The subset is small because §10.4's agent outputs are small: objects of strings, numbers,
// booleans and arrays of those. When an agent needs more, this grows — and it grows in a file with
// tests rather than by adopting a specification whose defaults have to be fought.

// Schema describes what a model is allowed to have said.
type Schema struct {
	// Type is one of object, array, string, number, integer, boolean. Required: an untyped schema
	// would validate anything, which is the opposite of the point.
	Type string `json:"type"`

	Properties map[string]*Schema `json:"properties,omitempty"`
	Required   []string           `json:"required,omitempty"`
	// AdditionalProperties defaults to false. A model that invents a field is a model that has
	// misunderstood the instruction, and a validator that shrugged at it would let the invention
	// through to whatever read the object next.
	AdditionalProperties bool `json:"additional_properties,omitempty"`

	Items *Schema `json:"items,omitempty"`

	Enum      []string `json:"enum,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	MinLength *int     `json:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty"`
	MinItems  *int     `json:"min_items,omitempty"`
	MaxItems  *int     `json:"max_items,omitempty"`
}

// Violation is one thing wrong with a model's answer.
type Violation struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (v Violation) String() string {
	if v.Path == "" {
		return v.Reason
	}
	return v.Path + ": " + v.Reason
}

// Validate parses a model's raw text as JSON and checks it.
//
// Parsing is part of validation rather than a step before it: a model that returned prose where an
// object was asked for has failed in exactly the way this exists to catch, and reporting that as a
// parse error somewhere else in the call stack would lose the retry that fixes it more than half
// the time.
func (s *Schema) Validate(raw string) (map[string]any, []Violation) {
	trimmed := strings.TrimSpace(raw)
	// Models fence JSON in markdown more often than not, whatever the instruction said. Unwrapping
	// it here rather than in each agent means the retry budget is spent on real disagreements
	// rather than on three backticks.
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)

	var decoded any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, []Violation{{Reason: "the answer is not JSON: " + err.Error()}}
	}
	// Trailing content is a violation rather than something to ignore. A model that emitted the
	// object and then an explanatory paragraph has produced something no caller should parse
	// halfway.
	if decoder.More() {
		return nil, []Violation{{Reason: "the answer carries content after the JSON value"}}
	}

	violations := s.check("", decoded)
	if len(violations) > 0 {
		return nil, violations
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, []Violation{{Reason: "the top level of an agent's answer must be an object"}}
	}
	return object, nil
}

func (s *Schema) check(path string, value any) []Violation {
	if s == nil {
		return []Violation{{Path: path, Reason: "no schema for this position"}}
	}
	switch s.Type {
	case "object":
		return s.checkObject(path, value)
	case "array":
		return s.checkArray(path, value)
	case "string":
		return s.checkString(path, value)
	case "number", "integer":
		return s.checkNumber(path, value)
	case "boolean":
		if _, ok := value.(bool); !ok {
			return []Violation{{Path: path, Reason: "expected a boolean"}}
		}
		return nil
	}
	return []Violation{{Path: path, Reason: fmt.Sprintf("the schema names an unknown type %q", s.Type)}}
}

func (s *Schema) checkObject(path string, value any) []Violation {
	object, ok := value.(map[string]any)
	if !ok {
		return []Violation{{Path: path, Reason: "expected an object"}}
	}
	var violations []Violation
	for _, name := range s.Required {
		if _, present := object[name]; !present {
			violations = append(violations, Violation{Path: join(path, name), Reason: "required and absent"})
		}
	}
	// Sorted so that a failure message is the same on every run. A test comparing violation text
	// against a map iteration order is a test that fails one time in six.
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		sub, known := s.Properties[key]
		if !known {
			if !s.AdditionalProperties {
				violations = append(violations, Violation{
					Path:   join(path, key),
					Reason: "not in the schema, and this object does not allow extra fields",
				})
			}
			continue
		}
		violations = append(violations, sub.check(join(path, key), object[key])...)
	}
	return violations
}

func (s *Schema) checkArray(path string, value any) []Violation {
	items, ok := value.([]any)
	if !ok {
		return []Violation{{Path: path, Reason: "expected an array"}}
	}
	var violations []Violation
	if s.MinItems != nil && len(items) < *s.MinItems {
		violations = append(violations, Violation{
			Path: path, Reason: fmt.Sprintf("needs at least %d items, has %d", *s.MinItems, len(items))})
	}
	if s.MaxItems != nil && len(items) > *s.MaxItems {
		violations = append(violations, Violation{
			Path: path, Reason: fmt.Sprintf("allows at most %d items, has %d", *s.MaxItems, len(items))})
	}
	for i, item := range items {
		violations = append(violations, s.Items.check(fmt.Sprintf("%s.%d", path, i), item)...)
	}
	return violations
}

func (s *Schema) checkString(path string, value any) []Violation {
	text, ok := value.(string)
	if !ok {
		return []Violation{{Path: path, Reason: "expected a string"}}
	}
	var violations []Violation
	if s.MinLength != nil && len([]rune(text)) < *s.MinLength {
		violations = append(violations, Violation{
			Path: path, Reason: fmt.Sprintf("shorter than the %d characters required", *s.MinLength)})
	}
	if s.MaxLength != nil && len([]rune(text)) > *s.MaxLength {
		violations = append(violations, Violation{
			Path: path, Reason: fmt.Sprintf("longer than the %d characters allowed", *s.MaxLength)})
	}
	if len(s.Enum) > 0 {
		for _, allowed := range s.Enum {
			if text == allowed {
				return violations
			}
		}
		violations = append(violations, Violation{
			Path: path, Reason: "not one of: " + strings.Join(s.Enum, ", ")})
	}
	return violations
}

func (s *Schema) checkNumber(path string, value any) []Violation {
	number, ok := value.(json.Number)
	if !ok {
		return []Violation{{Path: path, Reason: "expected a number"}}
	}
	if s.Type == "integer" {
		if _, err := number.Int64(); err != nil {
			return []Violation{{Path: path, Reason: "expected a whole number"}}
		}
	}
	asFloat, err := number.Float64()
	if err != nil {
		return []Violation{{Path: path, Reason: "the number cannot be read: " + err.Error()}}
	}
	var violations []Violation
	if s.Minimum != nil && asFloat < *s.Minimum {
		violations = append(violations, Violation{
			Path: path, Reason: fmt.Sprintf("below the minimum of %v", *s.Minimum)})
	}
	if s.Maximum != nil && asFloat > *s.Maximum {
		violations = append(violations, Violation{
			Path: path, Reason: fmt.Sprintf("above the maximum of %v", *s.Maximum)})
	}
	return violations
}

// Example builds a value that satisfies this schema.
//
// It exists for the mock provider, and the reason it is here rather than there is that a mock whose
// answers do not satisfy the schema would make every test of the happy path a test of the retry
// path instead — CI would be green, slow, and testing something other than what it claimed. Built
// from the schema itself, so an agent that changes its output shape gets a mock that follows
// without anybody remembering to update a fixture.
func (s *Schema) Example() any {
	if s == nil {
		return nil
	}
	switch s.Type {
	case "object":
		out := map[string]any{}
		// Only the required fields. An example carrying every optional field would hide the case
		// where a schema marks something required that the model is never told to produce.
		for _, name := range s.Required {
			out[name] = s.Properties[name].Example()
		}
		return out
	case "array":
		count := 1
		if s.MinItems != nil && *s.MinItems > count {
			count = *s.MinItems
		}
		if s.MaxItems != nil && *s.MaxItems < count {
			count = *s.MaxItems
		}
		out := make([]any, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, s.Items.Example())
		}
		return out
	case "string":
		if len(s.Enum) > 0 {
			return s.Enum[0]
		}
		text := "MOCK — no model was contacted."
		if s.MinLength != nil && len([]rune(text)) < *s.MinLength {
			text += strings.Repeat(" .", *s.MinLength)
		}
		if s.MaxLength != nil && len([]rune(text)) > *s.MaxLength {
			text = string([]rune(text)[:*s.MaxLength])
		}
		return text
	case "integer":
		value := 0
		if s.Minimum != nil && float64(value) < *s.Minimum {
			value = int(*s.Minimum)
		}
		if s.Maximum != nil && float64(value) > *s.Maximum {
			value = int(*s.Maximum)
		}
		return value
	case "number":
		value := 0.0
		if s.Minimum != nil && value < *s.Minimum {
			value = *s.Minimum
		}
		if s.Maximum != nil && value > *s.Maximum {
			value = *s.Maximum
		}
		return value
	case "boolean":
		return false
	}
	return nil
}
