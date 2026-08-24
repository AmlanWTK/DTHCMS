package httpx

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/ids"
)

// The contract test.
//
// api/openapi.yaml is the contract of record, which is a claim worth exactly as much as
// the mechanism that enforces it. This is that mechanism: it walks the router the service
// actually builds and fails when the router and the document disagree in either
// direction — an undocumented route, or a documented route nobody implemented.
//
// Both directions matter, and the second is the one people forget. A path left in the
// document after the endpoint was renamed generates a client method that 404s, and the
// generated client compiles perfectly while doing it.
//
// The document is read with a small mapping-key scanner rather than a YAML library. The
// test needs one thing from YAML — the keys — and the backend module has no YAML
// dependency to reuse; adding one to read a flat list of route names is a poor trade. The
// scanner refuses to report success on an empty result (see specDocument), so a scanner
// that silently stops working fails the build rather than passing it vacuously.

const specRelativePath = "../../../../api/openapi.yaml"

// httpMethods are the path-item keys the scanner treats as operations. Anything else at
// that level (summary, parameters, servers) is structure, not an endpoint.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// --- the document ---

// specDocument maps a dotted key path to that mapping's child keys, in file order.
//
// Only mapping keys are collected. Block scalars (`description: |`) are skipped wholesale
// by indentation, which is what keeps prose containing a colon from being read as
// structure.
type specDocument map[string][]string

func (d specDocument) childrenOf(t *testing.T, path string) []string {
	t.Helper()
	children, ok := d[path]
	if !ok {
		t.Fatalf("%s declares nothing at %q — the document's shape has changed, "+
			"or this scanner has stopped understanding it", specRelativePath, path)
	}
	return children
}

func loadSpec(t *testing.T) specDocument {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(specRelativePath))
	if err != nil {
		t.Fatalf("cannot read the API contract at %s: %v", specRelativePath, err)
	}

	doc := specDocument{}

	// stack[i] is the key owning the mapping at indent depth i.
	var stack []struct {
		indent int
		key    string
	}

	// Inside a block scalar, every line indented deeper than the key that introduced it
	// is prose and must not be read as structure.
	blockScalarIndent := -1

	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))

		if blockScalarIndent >= 0 {
			if indent > blockScalarIndent {
				continue
			}
			blockScalarIndent = -1
		}

		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "- ") {
			continue // a sequence item, not a mapping key
		}

		colon := strings.Index(trimmed, ":")
		if colon < 0 {
			continue
		}
		key := strings.Trim(strings.TrimSpace(trimmed[:colon]), `"'`)
		if key == "" {
			continue
		}

		// Pop to the parent of this indent level.
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		parent := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1].key
		}

		full := key
		if parent != "" {
			full = parent + "." + key
		}
		doc[parent] = append(doc[parent], key)

		value := strings.TrimSpace(trimmed[colon+1:])
		if value == "|" || value == ">" || value == "|-" || value == ">-" {
			blockScalarIndent = indent
			continue
		}
		if value != "" {
			continue // a scalar, not a nested mapping
		}

		stack = append(stack, struct {
			indent int
			key    string
		}{indent: indent, key: full})
	}

	if len(doc["paths"]) == 0 {
		t.Fatalf("read %s but found no paths — the scanner is broken, and a broken "+
			"scanner must fail rather than report that everything conforms",
			specRelativePath)
	}
	return doc
}

// specOperations returns the "METHOD /path" set the contract declares.
func specOperations(t *testing.T, doc specDocument) map[string]bool {
	t.Helper()

	operations := map[string]bool{}
	for _, path := range doc.childrenOf(t, "paths") {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("api/openapi.yaml: path %q does not begin with /", path)
			continue
		}
		for _, method := range doc["paths."+path] {
			if httpMethods[method] {
				operations[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	return operations
}

// --- the router ---

// conformanceRouter builds the router the api binary builds, with the same options.
func conformanceRouter(t *testing.T) *chi.Mux {
	t.Helper()
	return NewRouter(RouterOptions{
		Logger:         testLogger(),
		IDs:            &ids.Sequential{Prefix: "req"},
		AllowedOrigins: []string{"http://localhost:3000"},
		MaxBodyBytes:   1024,
		RequestTimeout: 5 * time.Second,
		Health:         &Health{Service: "api", Version: "test", Logger: testLogger()},
	})
}

// routerOperations returns the "METHOD /path" set the router actually serves.
func routerOperations(t *testing.T, router chi.Routes) map[string]bool {
	t.Helper()

	operations := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler) error {

		// A mounted subrouter with nothing in it leaves a wildcard behind. It is a
		// mount point rather than an endpoint, and documenting it would be a lie.
		route = strings.TrimSuffix(route, "/*")
		if route != "/" {
			route = strings.TrimSuffix(route, "/")
		}
		if route == "" || route == "/" || strings.Contains(route, "*") {
			return nil
		}

		operations[strings.ToUpper(method)+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking the router: %v", err)
	}
	return operations
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

// --- the tests ---

func TestEveryImplementedRouteIsDocumented(t *testing.T) {
	// Acceptance criterion 2. Add an endpoint without documenting it and this fails,
	// which is the entire reason the check exists — a contract nobody enforces is a
	// comment.
	spec := specOperations(t, loadSpec(t))
	router := routerOperations(t, conformanceRouter(t))

	var undocumented []string
	for operation := range router {
		if !spec[operation] {
			undocumented = append(undocumented, operation)
		}
	}
	sort.Strings(undocumented)

	if len(undocumented) > 0 {
		t.Errorf("these routes are served but absent from api/openapi.yaml:\n  %s\n\n"+
			"Add them to the contract. Three surfaces consume this API; an endpoint that "+
			"exists only in Go is an endpoint no client can be generated for.",
			strings.Join(undocumented, "\n  "))
	}
}

func TestEveryDocumentedRouteIsImplemented(t *testing.T) {
	// The direction people forget. A path left in the document after the endpoint was
	// renamed generates a client method that 404s — and the generated client compiles
	// perfectly while doing it.
	spec := specOperations(t, loadSpec(t))
	router := routerOperations(t, conformanceRouter(t))

	var unimplemented []string
	for operation := range spec {
		if !router[operation] {
			unimplemented = append(unimplemented, operation)
		}
	}
	sort.Strings(unimplemented)

	if len(unimplemented) > 0 {
		t.Errorf("these routes are documented but not served:\n  %s\n\n"+
			"Either implement them or remove them from api/openapi.yaml. Documented routes "+
			"that do not exist are generated into every client.",
			strings.Join(unimplemented, "\n  "))
	}
}

func TestTheOperationalRoutesAreTheOnesWeExpect(t *testing.T) {
	// A guard on the two tests above rather than a duplicate of them: if the walk or the
	// scanner silently returned nothing, both would pass by agreeing about an empty set.
	want := []string{"GET /healthz", "GET /readyz", "GET /version"}

	if got := sorted(routerOperations(t, conformanceRouter(t))); !reflect.DeepEqual(got, want) {
		t.Errorf("router serves %v, want %v", got, want)
	}
	if got := sorted(specOperations(t, loadSpec(t))); !reflect.DeepEqual(got, want) {
		t.Errorf("contract declares %v, want %v", got, want)
	}
}

// --- schema conformance ---

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

// required returns the wire names that are not omitempty — the fields always present.
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
	want := doc.childrenOf(t, "components.schemas.ErrorBody.properties")

	if !reflect.DeepEqual(got, want) {
		t.Errorf("errorBody serialises %v, contract declares %v", got, want)
	}
}

func TestHealthResponsesMatchTheContract(t *testing.T) {
	doc := loadSpec(t)

	// /readyz reports every field, checks included.
	got := jsonNames(t, healthResponse{})
	want := doc.childrenOf(t, "components.schemas.ReadinessResponse.properties")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("healthResponse serialises %v, ReadinessResponse declares %v", got, want)
	}

	// /healthz omits checks — it never looks at a dependency, so it has none to report.
	gotLive := requiredJSONNames(t, healthResponse{})
	wantLive := doc.childrenOf(t, "components.schemas.LivenessResponse.properties")
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
	want := loadSpec(t).childrenOf(t, "components.schemas.VersionResponse.properties")

	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildInfo writes %v, contract declares %v", got, want)
	}
}
