package main

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Architecture is the machine-readable form of the module dependency rules.
type Architecture struct {
	ModulePath       string              `json:"modulePath"`
	InternalRoot     string              `json:"internalRoot"`
	CompositionRoots []string            `json:"compositionRoots"`
	Modules          map[string][]string `json:"modules"`
}

// LoadArchitecture reads architecture.json from the backend module root.
func LoadArchitecture(root string) (*Architecture, error) {
	path := filepath.Join(root, "architecture.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var arch Architecture
	if err := json.Unmarshal(data, &arch); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if arch.ModulePath == "" || arch.InternalRoot == "" {
		return nil, fmt.Errorf("%s: modulePath and internalRoot are required", path)
	}

	// Composition roots assemble the application and may import anything, so a name
	// cannot be both a composition root and a bounded module — that would silently
	// exempt a module from every rule in this file.
	for _, root := range arch.CompositionRoots {
		if _, clash := arch.Modules[root]; clash {
			return nil, fmt.Errorf(
				"%s: %q is declared both as a composition root and as a module; "+
					"a composition root is exempt from dependency rules, so this would "+
					"disable checking for that module entirely", path, root)
		}
	}

	return &arch, nil
}

// IsCompositionRoot reports whether a top-level directory assembles the application
// rather than being a bounded module. Composition roots (cmd, tools) may import anything.
func (a *Architecture) IsCompositionRoot(dir string) bool {
	for _, root := range a.CompositionRoots {
		if root == dir {
			return true
		}
	}
	return false
}

// Allows reports whether module "from" may import module "to".
func (a *Architecture) Allows(from, to string) bool {
	if from == to {
		return true
	}
	for _, allowed := range a.Modules[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// RunArch checks every Go file under the internal root against the dependency allowlist.
func RunArch(root string) ([]Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", root, err)
	}

	arch, err := LoadArchitecture(root)
	if err != nil {
		return nil, err
	}
	return archCheck(root, arch)
}

func archCheck(root string, arch *Architecture) ([]Finding, error) {
	internalDir := filepath.Join(root, arch.InternalRoot)
	if _, err := os.Stat(internalDir); os.IsNotExist(err) {
		// No modules yet. The rules are still loaded and validated, which is the point
		// at CP02: the guardrail exists before the code it guards.
		return nil, nil
	}

	prefix := arch.ModulePath + "/" + arch.InternalRoot + "/"
	var findings []Finding

	err := filepath.WalkDir(internalDir, walkGoFiles(func(path string) error {
		module := moduleOf(internalDir, path)
		if module == "" {
			return nil
		}

		// Test files are exempt, and the reason is not leniency (CP26).
		//
		// The rule exists to govern what the *shipped* binary depends on. A _test.go file
		// is not in it, and — more to the point — a test-only import cannot become a
		// production dependency by accident: the moment a non-test file in the package
		// uses the imported package, that file must import it too, and *that* import is
		// checked here. The compiler and this checker together still make it impossible
		// for a module to depend on one it may not.
		//
		// What the exemption buys is the ability to test a module against the real thing.
		// realtime may not import auth; its RBAC filter is nevertheless meaningless unless
		// a test can build a subject holding real roles and ask for real permissions, and
		// a test that asserted against invented strings would assert nothing at all.
		if strings.HasSuffix(filepath.Base(path), "_test.go") {
			return nil
		}

		if _, declared := arch.Modules[module]; !declared {
			findings = append(findings, Finding{
				Check:   "arch",
				File:    relative(root, path),
				Line:    1,
				Message: fmt.Sprintf("module %q is not declared in architecture.json", module),
				Hint:    "Declare the module and its allowed dependencies, in the same change that creates it.",
			})
			return nil
		}

		imports, err := importsOf(path)
		if err != nil {
			return err
		}

		for _, imp := range imports {
			if !strings.HasPrefix(imp.Path, prefix) {
				continue
			}
			target := strings.SplitN(strings.TrimPrefix(imp.Path, prefix), "/", 2)[0]
			if arch.Allows(module, target) {
				continue
			}

			findings = append(findings, Finding{
				Check:   "arch",
				File:    relative(root, path),
				Line:    imp.Line,
				Message: fmt.Sprintf("module %q may not import module %q", module, target),
				Hint: fmt.Sprintf(
					"%q may import: %s. If this dependency is genuinely correct, it is an "+
						"architecture change: write an ADR and update architecture.json in the same PR.",
					module, allowedList(arch.Modules[module])),
			})
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

func allowedList(allowed []string) string {
	if len(allowed) == 0 {
		return "nothing (it is a leaf module)"
	}
	return strings.Join(allowed, ", ")
}

// moduleOf returns the top-level module directory a file belongs to.
func moduleOf(internalDir, path string) string {
	rel, err := filepath.Rel(internalDir, path)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

type importRef struct {
	Path string
	Line int
}

func importsOf(path string) ([]importRef, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	refs := make([]importRef, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		refs = append(refs, importRef{Path: value, Line: fset.Position(spec.Pos()).Line})
	}
	return refs, nil
}

func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
