package rbac

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// The serialiser layer (CP20): a response is shaped by what the reader may see.
//
// Blocking an endpoint is not enough. The pharmacist may read a prescription and may not
// see its diagnosis, and "may not see" has to mean the key is absent from the bytes —
// not null, not empty, not hidden by a screen. So a response type declares, field by
// field, who may see it:
//
//	type PrescriptionView struct {
//	    Drugs     []DrugLine `json:"drugs"`
//	    Diagnosis string     `json:"diagnosis" visible:"diagnosis.read"`
//	}
//
// and Marshal removes every field whose permission the subject lacks, at any depth,
// through pointers, slices and maps.
//
// Default-restrictive: a field whose name looks clinical — diagnosis, impression,
// assessment, ICD, synthesis, clinical anything — must carry a visible tag, or Marshal
// refuses the whole type. A new endpoint cannot leak a diagnosis by forgetting; it can
// only leak one by declaring, in a tag a reviewer reads, that everyone may see it.

// visibleTag is the struct tag: one permission, or several separated by commas, any of
// which reveals the field.
const visibleTag = "visible"

// suspicious is what a clinical field is called. A field whose JSON name contains one of
// these must declare visibility.
var suspicious = []string{"diagnos", "impression", "assessment", "icd", "synthesis", "clinical"}

// Marshal serialises v for the subject: json.Marshal with the fields the subject may not
// see removed.
func Marshal(subject Subject, v any) ([]byte, error) {
	plan, err := planFor(reflect.TypeOf(v))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if plan == nil || !plan.hasGuards() {
		return raw, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	removed := 0
	decoded = plan.apply(decoded, func(perms []string) bool {
		for _, p := range perms {
			if Sees(subject, p) {
				return true
			}
		}
		removed++
		return false
	})
	if removed == 0 {
		// Nothing was taken out: the bytes json.Marshal produced, in the type's own field
		// order. A redacted payload has its keys sorted, which JSON does not mind.
		return raw, nil
	}
	return json.Marshal(decoded)
}

// --- plans: what to remove, computed once per type ---

// node describes how to redact one JSON shape.
type node struct {
	// fields, for an object: JSON name → guard and sub-plan.
	fields map[string]*field
	// elem, for an array or a map: the plan for each element.
	elem *node
	// guarded caches whether anything under this node is guarded.
	guarded bool
}

type field struct {
	perms []string
	sub   *node
}

func (n *node) hasGuards() bool { return n != nil && n.guarded }

func (n *node) apply(value any, sees func([]string) bool) any {
	if n == nil || !n.guarded {
		return value
	}
	switch v := value.(type) {
	case map[string]any:
		if n.fields != nil {
			for name, f := range n.fields {
				child, present := v[name]
				if !present {
					continue
				}
				if len(f.perms) > 0 && !sees(f.perms) {
					delete(v, name)
					continue
				}
				v[name] = f.sub.apply(child, sees)
			}
			return v
		}
		if n.elem != nil { // a map[string]T
			for k, child := range v {
				v[k] = n.elem.apply(child, sees)
			}
		}
		return v
	case []any:
		if n.elem != nil {
			for i, child := range v {
				v[i] = n.elem.apply(child, sees)
			}
		}
		return v
	default:
		return value
	}
}

var plans sync.Map // reflect.Type → *node

func planFor(t reflect.Type) (*node, error) {
	if t == nil {
		return nil, nil
	}
	if cached, ok := plans.Load(t); ok {
		return cached.(*node), nil
	}
	n, err := buildPlan(t, map[reflect.Type]bool{})
	if err != nil {
		return nil, err
	}
	plans.Store(t, n)
	return n, nil
}

var jsonMarshaler = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

func buildPlan(t reflect.Type, seen map[reflect.Type]bool) (*node, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	// A type that writes its own JSON is a leaf: time.Time, uuid.UUID. Nothing inside it
	// is a field of ours.
	if t.Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(jsonMarshaler) {
		return &node{}, nil
	}
	switch t.Kind() {
	case reflect.Struct:
		if seen[t] {
			// Recursive types: the plan for the outer occurrence covers the inner ones
			// only one level deep, which is where a clinical field would be. A deeper
			// guard would need a cyclic plan; refuse rather than guess.
			return nil, fmt.Errorf("rbac: %s is recursive; recursive response types are not supported", t)
		}
		seen[t] = true
		defer delete(seen, t)

		n := &node{fields: map[string]*field{}}
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			name, skip := jsonName(sf)
			if skip {
				continue
			}
			if sf.Anonymous && name == "" {
				// An embedded struct's fields are promoted. Merge its plan.
				sub, err := buildPlan(sf.Type, seen)
				if err != nil {
					return nil, err
				}
				for k, f := range sub.fields {
					n.fields[k] = f
				}
				n.guarded = n.guarded || sub.guarded
				continue
			}
			f := &field{}
			if tag, ok := sf.Tag.Lookup(visibleTag); ok {
				for _, p := range strings.Split(tag, ",") {
					if p = strings.TrimSpace(p); p != "" {
						if !knownActions[p] {
							return nil, fmt.Errorf("rbac: %s.%s: visible:%q is not a permission in the catalogue", t, sf.Name, p)
						}
						f.perms = append(f.perms, p)
					}
				}
				if len(f.perms) == 0 {
					return nil, fmt.Errorf("rbac: %s.%s: an empty visible tag", t, sf.Name)
				}
			} else if looksClinical(name) {
				return nil, fmt.Errorf("rbac: %s.%s (json %q) looks clinical and declares no visible tag; "+
					"say who may see it — default is deny", t, sf.Name, name)
			}
			sub, err := buildPlan(sf.Type, seen)
			if err != nil {
				return nil, err
			}
			f.sub = sub
			n.fields[name] = f
			if len(f.perms) > 0 || sub.hasGuards() {
				n.guarded = true
			}
		}
		return n, nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return &node{}, nil // bytes serialise as a string
		}
		elem, err := buildPlan(t.Elem(), seen)
		if err != nil {
			return nil, err
		}
		return &node{elem: elem, guarded: elem.hasGuards()}, nil
	case reflect.Map:
		elem, err := buildPlan(t.Elem(), seen)
		if err != nil {
			return nil, err
		}
		return &node{elem: elem, guarded: elem.hasGuards()}, nil
	case reflect.Interface:
		// The static type says nothing; whatever is inside is passed through. A response
		// type that hides a diagnosis behind `any` has defeated the point, so refuse it.
		return nil, fmt.Errorf("rbac: interface-typed fields cannot be redacted; use a concrete type")
	default:
		return &node{}, nil
	}
}

func jsonName(sf reflect.StructField) (name string, skip bool) {
	tag := sf.Tag.Get("json")
	if tag == "-" {
		return "", true
	}
	name = strings.Split(tag, ",")[0]
	if name == "" {
		if sf.Anonymous {
			return "", false
		}
		name = sf.Name
	}
	return name, false
}

func looksClinical(jsonName string) bool {
	lower := strings.ToLower(jsonName)
	for _, s := range suspicious {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}
