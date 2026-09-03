// Package apispec reads api/openapi.yaml well enough to check the service against it.
//
// It exists so that the conformance tests can live where the thing they check lives. The
// route tests must walk the router the api binary actually assembles, which means they
// belong to cmd/api; the schema tests reflect over types private to httpx, which means
// they belong there. Both need to read the contract, and a scanner copied into two places
// is a scanner that will disagree with itself.
//
// It is deliberately not a YAML library. The tests need one thing from YAML — the mapping
// keys — and the backend module has no YAML dependency to reuse; taking one on to read a
// flat list of route names is a poor trade. Block scalars are skipped wholesale by
// indentation, which is what keeps prose containing a colon from being read as structure.
//
// The scanner refuses to report success on an empty result: a scanner that has quietly
// stopped understanding the document must fail the build rather than agree that everything
// conforms.
package apispec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// httpMethods are the path-item keys that name an operation. Anything else at that level
// (summary, parameters, servers) is structure, not an endpoint.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// Document maps a dotted key path to that mapping's child keys, in file order.
type Document map[string][]string

// Load reads and scans the contract at path.
func Load(path string) (Document, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("cannot read the API contract at %s: %w", path, err)
	}

	doc := Document{}

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
		return nil, fmt.Errorf(
			"read %s but found no paths — the scanner is broken, and a broken scanner "+
				"must fail rather than report that everything conforms", path)
	}
	return doc, nil
}

// Children returns the child keys of a mapping, in file order.
func (d Document) Children(path string) ([]string, error) {
	children, ok := d[path]
	if !ok {
		return nil, fmt.Errorf("the contract declares nothing at %q — the document's "+
			"shape has changed, or this scanner has stopped understanding it", path)
	}
	return children, nil
}

// Operations returns the "METHOD /path" set the contract declares.
func (d Document) Operations() (map[string]bool, error) {
	paths, err := d.Children("paths")
	if err != nil {
		return nil, err
	}

	operations := map[string]bool{}
	for _, path := range paths {
		if !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("path %q does not begin with /", path)
		}
		for _, method := range d["paths."+path] {
			if httpMethods[method] {
				operations[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	return operations, nil
}
