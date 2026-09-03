package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The test-only door (CP24).
//
// Some constructors exist so that a test can build a value production code must never
// build by hand. eventstore.ActorForTest is the first: the attribution envelope's fields
// are unexported precisely so that no package outside eventstore can write down who made
// an event, and the whole guarantee collapses the moment a handler calls a public
// constructor that fills them in.
//
// The compiler cannot express "only from a test", so this check does. A function whose
// doc comment carries the directive
//
//	//dthclint:testonly
//
// may be called from _test.go files and from nothing else. The rule is deliberately not
// hard-coded to one function: the next test-only door gets the same treatment by writing
// the directive above it, in the same change that opens it.
//
// Scope: the whole backend module, including cmd and tools, which the architecture check
// exempts. A composition root has no more business forging an actor than a handler does.
const testOnlyDirective = "//dthclint:testonly"

// testOnlyFunc is a function the directive marks, identified the way a caller writes it.
type testOnlyFunc struct {
	ImportPath string // "…/internal/eventstore"
	Package    string // "eventstore"
	Name       string // "ActorForTest"
	File       string
	Line       int
}

// RunTestOnly finds every marked function, then every call to one from a file that is not
// a test.
func RunTestOnly(root string) ([]Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", root, err)
	}
	arch, err := LoadArchitecture(root)
	if err != nil {
		return nil, err
	}

	marked, err := markedFuncs(root, arch.ModulePath)
	if err != nil {
		return nil, err
	}
	if len(marked) == 0 {
		return nil, nil
	}
	return testOnlyCalls(root, arch.ModulePath, marked)
}

// markedFuncs collects the directive's targets. Test files are scanned too: a door
// declared in a test file is still a door, and naming it keeps the report honest.
func markedFuncs(root, modulePath string) (map[string][]testOnlyFunc, error) {
	byName := map[string][]testOnlyFunc{}

	err := filepath.WalkDir(root, walkGoFiles(func(path string) error {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Doc == nil || fn.Recv != nil {
				continue
			}
			if !hasDirective(fn.Doc, testOnlyDirective) {
				continue
			}
			byName[fn.Name.Name] = append(byName[fn.Name.Name], testOnlyFunc{
				ImportPath: importPathOf(root, modulePath, path),
				Package:    file.Name.Name,
				Name:       fn.Name.Name,
				File:       relative(root, path),
				Line:       fset.Position(fn.Pos()).Line,
			})
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}
	return byName, nil
}

func hasDirective(doc *ast.CommentGroup, directive string) bool {
	for _, c := range doc.List {
		if strings.TrimSpace(c.Text) == directive {
			return true
		}
	}
	return false
}

// importPathOf turns a file's directory into the import path a caller would write.
func importPathOf(root, modulePath, path string) string {
	dir := filepath.ToSlash(filepath.Dir(relative(root, path)))
	if dir == "." {
		return modulePath
	}
	return modulePath + "/" + dir
}

func testOnlyCalls(root, modulePath string, marked map[string][]testOnlyFunc) ([]Finding, error) {
	var findings []Finding

	err := filepath.WalkDir(root, walkGoFiles(func(path string) error {
		if strings.HasSuffix(filepath.Base(path), "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}

		// Which local name each import goes by in this file, so that an aliased or
		// dot-free import still resolves to the package the directive was written in.
		aliases := map[string]string{}
		for _, spec := range file.Imports {
			value, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			name := value[strings.LastIndex(value, "/")+1:]
			if spec.Name != nil {
				name = spec.Name.Name
			}
			aliases[name] = value
		}
		self := importPathOf(root, modulePath, path)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var pkgName, funcName string
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				ident, ok := fn.X.(*ast.Ident)
				if !ok {
					return true
				}
				pkgName, funcName = ident.Name, fn.Sel.Name
			case *ast.Ident:
				funcName = fn.Name
			default:
				return true
			}

			for _, target := range marked[funcName] {
				// Qualified: the local name must be an import of the declaring package.
				if pkgName != "" && aliases[pkgName] != target.ImportPath {
					continue
				}
				// Unqualified: only a call from inside the declaring package counts.
				if pkgName == "" && target.ImportPath != self {
					continue
				}
				findings = append(findings, Finding{
					Check:   "testonly",
					File:    relative(root, path),
					Line:    fset.Position(call.Pos()).Line,
					Message: fmt.Sprintf("%s.%s is test-only and cannot be called from production code", target.Package, target.Name),
					Hint: fmt.Sprintf(
						"Declared %s:%d. This door exists so tests can build a value the "+
							"type system forbids elsewhere; calling it here reopens exactly "+
							"the hole it was closed to prevent. Build the value the real way "+
							"(for an actor: eventstore.ActorFrom, from the request context).",
						target.File, target.Line),
				})
				return true
			}
			return true
		})
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
