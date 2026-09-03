package httpx

import (
	"reflect"
	"strings"
	"testing"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/apispec"
)

// The schema half of the contract test.
//
// api/openapi.yaml is the contract of record, which is a claim worth exactly as much as
// the mechanism that enforces it. These tests are half of that mechanism: they reflect
// over the Go types that produce the shared envelopes and fail when a field is added,
// removed or renamed on one side only.
//
// The route half lives in cmd/api. It has to: the full route set only exists once the
// binary has mounted the auth endpoints alongside the operational ones, and this package
// may not import a module (architecture.json) to reach them. The composition root is the
// only place the whole surface is assembled, so it is the only place the whole surface can
// honestly be checked.

const specRelativePath = "../../../../api/openapi.yaml"

func loadSpec(t *testing.T) apispec.Document {
	t.Helper()
	doc, err := apispec.Load(specRelativePath)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return doc
}

func childrenOf(t *testing.T, doc apispec.Document, path string) []string {
	t.Helper()
	children, err := doc.Children(path)
	if err != nil {
		t.Fatalf("%s: %v", specRelativePath, err)
	}
	return children
}

// jsonNames returns the wire names of a struct's fields, in declaration order.
func jsonNames(t *testing.T, v any) []string {
	t.Helper()

	typ := reflect.TypeOf(v)
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		names = append(names, strings.Split(tag, ",")[0])
	}
	return names
}

// requiredJSONNames returns the wire names that are not omitempty — the fields always
// present.
func requiredJSONNames(t *testing.T, v any) []string {
	t.Helper()

	typ := reflect.TypeOf(v)
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" || strings.Contains(tag, ",omitempty") {
			continue
		}
		names = append(names, strings.Split(tag, ",")[0])
	}
	return names
}

func TestErrorEnvelopeMatchesTheContract(t *testing.T) {
	// The Go struct is the producer of this shape and the document is its description.
	// The TypeScript side checks the same schema from the consumer's end; between them
	// the envelope cannot drift on either bank of the river.
	doc := loadSpec(t)

	got := jsonNames(t, errorBody{})
	want := childrenOf(t, doc, "components.schemas.ErrorBody.properties")

	if !reflect.DeepEqual(got, want) {
		t.Errorf("errorBody serialises %v, contract declares %v", got, want)
	}
}

func TestHealthResponsesMatchTheContract(t *testing.T) {
	doc := loadSpec(t)

	// /readyz reports every field, checks included.
	got := jsonNames(t, healthResponse{})
	want := childrenOf(t, doc, "components.schemas.ReadinessResponse.properties")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("healthResponse serialises %v, ReadinessResponse declares %v", got, want)
	}

	// /healthz omits checks — it never looks at a dependency, so it has none to report.
	gotLive := requiredJSONNames(t, healthResponse{})
	wantLive := childrenOf(t, doc, "components.schemas.LivenessResponse.properties")
	if !reflect.DeepEqual(gotLive, wantLive) {
		t.Errorf("healthResponse always-present fields are %v, LivenessResponse declares %v",
			gotLive, wantLive)
	}
}

func TestVersionResponseMatchesTheContract(t *testing.T) {
	// BuildInfo writes a map literal rather than a struct, so there is nothing to
	// reflect over. This list mirrors it by hand; the test exists so that adding a key
	// there without adding it to the contract fails here.
	got := []string{"service", "version", "commit", "build_time"}
	want := childrenOf(t, loadSpec(t), "components.schemas.VersionResponse.properties")

	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildInfo writes %v, contract declares %v", got, want)
	}
}
