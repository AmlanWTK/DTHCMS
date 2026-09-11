package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The read path may not open the write door (CP74).
//
// # What went wrong
//
// eventstore.ActorFrom builds the *write* envelope, and refuses a request whose session
// carries no enrolled device — correctly, because a clinical event's device_id is evidence
// [R-03]. Read handlers needed the same facts for other reasons (the facility to scope a
// query to, the user and role to put on the audit entry) and there was no other door, so
// they called ActorFrom as well.
//
// The result: 28 of the API's 118 GET routes were unreachable from a browser, which has no
// device by design. The patient list, the traffic board, every station queue, the timeline,
// growth, observations, alerts, consents, corrections, the patient summary — all answering
// "this action must be done from an enrolled clinic device" to a plain read. It survived
// because every domain test builds identity with ActorForTest, which takes a device id and
// therefore always has one, so nothing ever exercised a read with a browser's identity.
//
// eventstore.ReaderFrom is the read door. This check is what keeps handlers using it: a
// handler reachable from a GET route may not reach ActorFrom, transitively, within its own
// package.
//
// # Two bugs this check itself had, and what they cost
//
// Worth writing down, because both were in the guardrail rather than in the code it guards.
//
//  1. The first version keyed every function by its bare name, so the builtin `append`
//     matched internal/consent's method of the same name and the walk wandered into
//     functions the handler never calls.
//  2. It then only followed method calls whose receiver was a bare identifier — `h.thing()`
//     — and so missed `h.photos.ViewURL()`, which is exactly how GET /{id}/photo reached
//     the write door. The runtime sweep found that one; the static check did not. Following
//     selector receivers fixed it and immediately introduced over-reporting, because two
//     services in one package can both have a method named Suggest.
//
// Both are the same mistake: identifying a function by its name rather than by what it is.
// Declarations are now keyed `Type.Method`, and a call written `h.photos.ViewURL()` is
// resolved through the receiver's own struct fields to `PhotoService.ViewURL`.
//
// # The escape hatch, and why it is narrow
//
// Some GET routes genuinely belong to an enrolled device — the tablet's sync pull is the
// real example, and a browser calling it is a client bug, not a person being locked out.
// Marking the handler
//
//	//dthclint:writeonread <reason>
//
// allows it and forces the reason to be written down next to the code.

const writeOnReadDirective = "//dthclint:writeonread"

// the function every read handler must not reach.
const writeDoorPkg, writeDoorFunc = "eventstore", "ActorFrom"

// readPathFunc is one declaration, filed the way a caller inside the package reaches it:
// "Type.Method" for a method, "Name" for a plain function.
type readPathFunc struct {
	Decl *ast.FuncDecl
	File string
	Line int
}

func funcKey(fn *ast.FuncDecl) string {
	if recv := receiverType(fn); recv != "" {
		return recv + "." + fn.Name.Name
	}
	return fn.Name.Name
}

func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return typeName(fn.Recv.List[0].Type)
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	return fn.Recv.List[0].Names[0].Name
}

// typeName is the bare name of a type expression, following pointers. A type from another
// package resolves to "", and a method on one of those is outside this package's walk.
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return typeName(t.X)
	case *ast.IndexListExpr:
		return typeName(t.X)
	}
	return ""
}

// fieldIndex maps "OwnerType.field" to the field's declared type, so a call written
// h.photos.ViewURL() resolves to PhotoService.ViewURL.
type fieldIndex map[string]string

func indexFields(pkg *ast.Package) fieldIndex {
	fields := fieldIndex{}
	for _, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, f := range st.Fields.List {
				declared := typeName(f.Type)
				if declared == "" {
					continue
				}
				for _, name := range f.Names {
					fields[spec.Name.Name+"."+name.Name] = declared
				}
			}
			return true
		})
	}
	return fields
}

// RunReadPath reports GET handlers that reach the write envelope.
func RunReadPath(root string) ([]Finding, error) {
	dirs := map[string]bool{}
	err := filepath.WalkDir(root, walkGoFiles(func(path string) error {
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dirs[filepath.Dir(path)] = true
		return nil
	}))
	if err != nil {
		return nil, err
	}

	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Strings(ordered)

	var findings []Finding
	for _, dir := range ordered {
		found, err := readPathInPackage(root, dir)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

type mountSite struct {
	file  string
	line  int
	route string
}

func readPathInPackage(root, dir string) ([]Finding, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", dir, err)
	}

	var findings []Finding
	for _, pkg := range pkgs {
		funcs := map[string]readPathFunc{}
		for name, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				pos := fset.Position(fn.Pos())
				rel, _ := filepath.Rel(root, name)
				funcs[funcKey(fn)] = readPathFunc{Decl: fn, File: rel, Line: pos.Line}
			}
		}
		fields := indexFields(pkg)

		entries := map[string]mountSite{}
		for name, file := range pkg.Files {
			rel, _ := filepath.Rel(root, name)
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				route, isGet := getMountRoute(call)
				if !isGet {
					return true
				}
				pos := fset.Position(call.Pos())
				for _, handler := range handlerNames(call) {
					if _, seen := entries[handler]; !seen {
						entries[handler] = mountSite{file: rel, line: pos.Line, route: route}
					}
				}
				return true
			})
		}

		for handler, at := range entries {
			key, start, ok := findHandler(handler, funcs)
			if !ok {
				continue
			}
			if carriesDirective(start.Decl.Doc, writeOnReadDirective) {
				continue
			}
			if path := reachesWriteDoor(key, funcs, fields); path != nil {
				findings = append(findings, Finding{
					Check:   "readpath",
					File:    at.file,
					Line:    at.line,
					Message: fmt.Sprintf("GET %s reaches %s.%s via %s", at.route, writeDoorPkg, writeDoorFunc, strings.Join(path, " -> ")),
					Hint: "A read needs eventstore.ReaderFrom, not ActorFrom: ActorFrom refuses a session with " +
						"no enrolled device, which is every browser. If this route genuinely belongs to an " +
						"enrolled device, mark the handler //dthclint:writeonread <reason>.",
				})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// findHandler locates a mounted handler by its method name. A route is mounted as
// `h.timeline`, which names the method but not its receiver; a package with exactly one
// declaration of that name has answered the question, and a package with two is ambiguous
// enough that guessing would be worse than skipping.
func findHandler(name string, funcs map[string]readPathFunc) (string, readPathFunc, bool) {
	var foundKey string
	var found readPathFunc
	matches := 0
	for key, fn := range funcs {
		if fn.Decl.Name.Name == name {
			foundKey, found = key, fn
			matches++
		}
	}
	if matches != 1 {
		return "", readPathFunc{}, false
	}
	return foundKey, found, true
}

// getMountRoute reports whether a call mounts a GET route, and the path it mounts.
// Two shapes are used in this codebase:
//
//	r.Method("GET", "/{id}/timeline", httpx.Declare(read, h.timeline))
//	r.Get("/{id}/timeline", h.timeline)
func getMountRoute(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "Method":
		if len(call.Args) < 2 {
			return "", false
		}
		if method, ok := stringLit(call.Args[0]); !ok || method != "GET" {
			return "", false
		}
		route, _ := stringLit(call.Args[1])
		return route, true
	case "Get":
		if len(call.Args) < 1 {
			return "", false
		}
		route, ok := stringLit(call.Args[0])
		if !ok {
			return "", false
		}
		return route, true
	}
	return "", false
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, `"`), true
}

// handlerNames pulls every `x.name` method value out of a mount call's arguments, however
// deeply it is wrapped — httpx.Declare(perm, h.timeline) and bare h.timeline both yield
// "timeline".
func handlerNames(call *ast.CallExpr) []string {
	var names []string
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, isIdent := sel.X.(*ast.Ident); isIdent {
				names = append(names, sel.Sel.Name)
			}
			return true
		})
	}
	return names
}

// reachesWriteDoor walks calls within the package from start, returning the chain that
// arrives at the write door, or nil.
func reachesWriteDoor(start string, funcs map[string]readPathFunc, fields fieldIndex) []string {
	type step struct {
		key  string
		path []string
	}
	seen := map[string]bool{start: true}
	queue := []step{{key: start, path: []string{start}}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		fn, ok := funcs[cur.key]
		if !ok {
			continue
		}
		owner := receiverType(fn.Decl)
		recv := receiverName(fn.Decl)

		var next []string
		hit := false
		ast.Inspect(fn.Decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if base, isIdent := fun.X.(*ast.Ident); isIdent {
					if base.Name == writeDoorPkg && fun.Sel.Name == writeDoorFunc {
						hit = true
						return false
					}
					// `h.method(...)` — a method on this function's own receiver.
					if owner != "" && recv != "" && base.Name == recv {
						next = append(next, owner+"."+fun.Sel.Name)
					}
					return true
				}
				// `h.field.Method(...)` — resolve the field to its declared type.
				if inner, isSel := fun.X.(*ast.SelectorExpr); isSel {
					if base, isIdent := inner.X.(*ast.Ident); isIdent && owner != "" && base.Name == recv {
						if declared, known := fields[owner+"."+inner.Sel.Name]; known {
							next = append(next, declared+"."+fun.Sel.Name)
						}
					}
				}
			case *ast.Ident:
				next = append(next, fun.Name)
			}
			return true
		})
		if hit {
			return cur.path
		}
		for _, key := range next {
			if seen[key] {
				continue
			}
			if _, known := funcs[key]; !known {
				continue
			}
			seen[key] = true
			queue = append(queue, step{key: key, path: append(append([]string{}, cur.path...), key)})
		}
	}
	return nil
}

// carriesDirective is a prefix match, unlike testonly.go's exact one: this directive takes
// a reason after it, and a reason is the point.
func carriesDirective(doc *ast.CommentGroup, directive string) bool {
	if doc == nil {
		return false
	}
	for _, c := range doc.List {
		if strings.HasPrefix(strings.TrimSpace(c.Text), directive) {
			return true
		}
	}
	return false
}
