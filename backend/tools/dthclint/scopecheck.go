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

// A route that defers resource scope must have somewhere to defer it to (CP83).
//
// # What went wrong
//
// `HTTPAuthorizer.Authorize` built `Resource{Kind: "route", FacilityID: f}` and asked
// `rbac.Can` about it. Can enforces *resource* scope: a station-scoped role needs the
// resource to name a station, a field worker needs it to name an owner, and a route
// resource names neither — so both were refused, always. `patient.write.demographics` is
// held by REGISTRATION and FIELD_WORKER and nobody else, so `POST /v1/patients` answered
// 403 to every role in the catalogue; seventy-eight declared routes were refused the same
// way. It stayed hidden because every browser write already failed earlier with
// DEVICE_REQUIRED, so nothing reached the scope test, and because the refusal read
// "out_of_scope" — a sentence about the caller, which is where everybody looked.
//
// The fix splits the question: `rbac.Reaches` answers what a route can answer and *reports*
// the reach it did not apply. A route may opt into being entered anyway, with
// `httpx.PermissionScoped`, and its handler then owes the resource half.
//
// # What this check enforces
//
// The promise. A route declared `httpx.PermissionScoped` whose handler cannot reach
// `rbac.Authorize` or `rbac.AuthorizeCreation` is a route that would enter a handler with
// nobody left to judge the resource. At runtime the scope debt (httpx/scopedebt.go) refuses
// the response, so the hole is closed either way; this check is what turns that refusal
// from a production incident into a build failure.
//
// The other direction needs no check: a route that does *not* opt in is refused by the
// guard for exactly the roles whose reach is narrow, which is the same answer it gave
// before CP83 and is fail-closed by construction.
//
// # Why the handler is resolved through its receiver, and not by name
//
// readpath.go's note records that keying declarations by their bare name cost it two bugs.
// It keys `Type.Method` now, but it still *finds* a mounted handler by scanning for the one
// declaration with that method name and gives up when two match. Giving up is the right
// failure for readpath, which reports a forbidden reach: skipping can only under-report.
//
// It would be the wrong failure here, because this check reports a *missing* call: a
// package with two methods called `register` would silently stop being checked, which is
// the hole the rule exists to close. So the receiver is resolved rather than guessed —
// `h.register` inside a method on `*Handlers` is `Handlers.register`, through the same
// field index readpath uses for `h.photos.ViewURL()` — and a handler that cannot be
// resolved exactly is reported, not skipped.
//
// # The escape hatch
//
//	//dthclint:scopecheck <reason>
//
// on the handler declaration. The reason has to be written down, and "the runtime debt will
// catch it" is not one: a route that reaches no authorisation call is refused at runtime,
// so marking it here only hides a route nobody can use.

const scopeCheckDirective = "//dthclint:scopecheck"

// The service-layer doors.
//
//   - Authorize judges a resource the handler has loaded.
//   - AuthorizeCreation states that the act creates the resource and there is nothing yet
//     to judge.
//   - AuthorizeStationRead and AuthorizeStationWrite ask the reach query (ADR-0036 §1):
//     does this subject's station hold this patient, at read strength or write strength.
//     Two names and not one because they are two different questions, and a handler that
//     reaches for the wrong one should be visible in a diff.
//   - AuthorizeList and AuthorizeOwnList answer the same question for a read that returns
//     many rows, where there is no single resource: they return the row restriction the
//     handler must apply — a station's patients, or the caller's own records.
//
// Adding a name here widens what counts as "the resource was judged", so a name belongs
// here only if the function it names cannot return nil without having judged something.
const scopeDoorPkg = "rbac"

var scopeDoorFuncs = map[string]bool{
	"Authorize":             true,
	"AuthorizeCreation":     true,
	"AuthorizeStationRead":  true,
	"AuthorizeStationWrite": true,
	// The list pair (CP85). A route that returns many rows has no "the resource" to judge,
	// so these do not judge one: they *hand the handler the restriction* the rows must be
	// filtered by, as a value no other package can construct and the store cannot be called
	// without. They belong here on the same footing as AuthorizeCreation — what they cannot
	// do is return without the engine having decided what this subject may be shown.
	"AuthorizeList":    true,
	"AuthorizeOwnList": true,
	// The same two decisions with the refusal written for the caller. They cannot return
	// true without having judged, which is the property that makes a name belong here.
	"GuardPatientRead":  true,
	"GuardPatientWrite": true,
}

// scopedRequirement is the constructor a route uses to promise it judges the resource.
const scopedRequirement = "PermissionScoped"

// RunScopeCheck reports routes that defer resource scope to a handler that never judges it.
func RunScopeCheck(root string) ([]Finding, error) {
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
		found, err := scopeCheckInPackage(root, dir)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// scopedMount is one `PermissionScoped` route: where it is mounted and what it mounts.
type scopedMount struct {
	file string
	line int
	// route is the pattern as written at the mount site, without the prefix the parent
	// router adds. Enough to name the offender in a message.
	route string
	// handlerKey is the declaration the route mounts, as "Type.Method" or "Name".
	handlerKey string
	// unresolved is set when the handler expression could not be resolved to a declaration
	// in this package. Reported rather than skipped.
	unresolved string
}

func scopeCheckInPackage(root, dir string) ([]Finding, error) {
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

		var mounts []scopedMount
		for name, file := range pkg.Files {
			rel, _ := filepath.Rel(root, name)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				// The receiver of the function doing the mounting is what `h` in
				// `h.register` means. Resolving it is what makes this check exact.
				owner, recv := receiverType(fn), receiverName(fn)
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					route, scoped := scopedMountRoute(call)
					if !scoped {
						return true
					}
					pos := fset.Position(call.Pos())
					for _, expr := range handlerExprs(call) {
						m := scopedMount{file: rel, line: pos.Line, route: route}
						key, ok := resolveHandler(expr, owner, recv, fields, funcs)
						if ok {
							m.handlerKey = key
						} else {
							m.unresolved = exprText(expr)
						}
						mounts = append(mounts, m)
					}
					return true
				})
			}
		}

		for _, m := range mounts {
			if m.unresolved != "" {
				findings = append(findings, Finding{
					Check: "scopecheck", File: m.file, Line: m.line,
					Message: fmt.Sprintf("%s declares httpx.%s but its handler %q could not be resolved to a declaration in this package",
						m.route, scopedRequirement, m.unresolved),
					Hint: "A scoped route's handler must be checkable, because something has to be seen to " +
						"call rbac.Authorize or rbac.AuthorizeCreation on the resource. Mount a named method " +
						"on the receiver that declares the route, rather than a closure or a value from " +
						"another package.",
				})
				continue
			}
			fn, known := funcs[m.handlerKey]
			if !known {
				findings = append(findings, Finding{
					Check: "scopecheck", File: m.file, Line: m.line,
					Message: fmt.Sprintf("%s declares httpx.%s but %s is not declared in this package",
						m.route, scopedRequirement, m.handlerKey),
					Hint: "A scoped route's handler must be checkable in the package that mounts it.",
				})
				continue
			}
			if carriesDirective(fn.Decl.Doc, scopeCheckDirective) {
				continue
			}
			if reachesScopeDoor(m.handlerKey, funcs, fields) != nil {
				continue
			}
			findings = append(findings, Finding{
				Check: "scopecheck", File: fn.File, Line: fn.Line,
				Message: fmt.Sprintf("%s declares httpx.%s, but %s reaches neither %s.Authorize nor %s.AuthorizeCreation",
					m.route, scopedRequirement, m.handlerKey, scopeDoorPkg, scopeDoorPkg),
				Hint: "The route guard defers this permission's reach — it is narrower than the facility for " +
					"some role that holds it — and something must judge the resource. Call rbac.Authorize with " +
					"the resource you loaded, or rbac.AuthorizeCreation if the act creates it. Until then the " +
					"scope debt refuses the response at runtime, so this route is unusable, not merely unchecked.",
			})
		}
	}
	return findings, nil
}

// scopedMountRoute reports whether a call mounts a route whose requirement is
// PermissionScoped, and the pattern it mounts. Both router shapes are recognised:
//
//	r.Method("POST", "/", httpx.Declare(httpx.PermissionScoped(perm), h.register))
//	r.Post("/", httpx.Declare(httpx.PermissionScoped(perm), h.register))
func scopedMountRoute(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	var route string
	switch sel.Sel.Name {
	case "Method":
		if len(call.Args) < 2 {
			return "", false
		}
		method, _ := stringLit(call.Args[0])
		pattern, _ := stringLit(call.Args[1])
		route = method + " " + pattern
	case "Get", "Post", "Put", "Patch", "Delete", "Head", "Options":
		if len(call.Args) < 1 {
			return "", false
		}
		pattern, ok := stringLit(call.Args[0])
		if !ok {
			return "", false
		}
		route = strings.ToUpper(sel.Sel.Name) + " " + pattern
	default:
		return "", false
	}
	if !mentionsScopedRequirement(call) {
		return "", false
	}
	return route, true
}

func mentionsScopedRequirement(call *ast.CallExpr) bool {
	found := false
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != scopedRequirement {
				return true
			}
			found = true
			return false
		})
	}
	return found
}

// handlerExprs pulls the handler arguments out of a mount call: everything that is not the
// requirement expression and not a string literal.
func handlerExprs(call *ast.CallExpr) []ast.Expr {
	var out []ast.Expr
	var visit func(e ast.Expr)
	visit = func(e ast.Expr) {
		switch t := e.(type) {
		case *ast.BasicLit:
			return
		case *ast.CallExpr:
			if sel, ok := t.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == scopedRequirement {
				return
			}
			for _, a := range t.Args {
				visit(a)
			}
		case *ast.SelectorExpr, *ast.Ident, *ast.FuncLit:
			out = append(out, e)
		}
	}
	for _, arg := range call.Args {
		visit(arg)
	}
	return out
}

// resolveHandler turns the expression a route mounts into a declaration key.
//
// `h.register` on a method whose receiver is `h *Handlers` is `Handlers.register`;
// `h.photos.serve` resolves through the field index the same way readpath does. A bare
// identifier is a package-level function. Anything else — a closure, a value from another
// package, a wrapper built inline — is not resolvable here, and the caller reports it
// rather than skipping it.
func resolveHandler(e ast.Expr, owner, recv string, fields fieldIndex, funcs map[string]readPathFunc) (string, bool) {
	switch t := e.(type) {
	case *ast.Ident:
		if _, ok := funcs[t.Name]; ok {
			return t.Name, true
		}
	case *ast.SelectorExpr:
		if base, ok := t.X.(*ast.Ident); ok {
			if owner != "" && recv != "" && base.Name == recv {
				return owner + "." + t.Sel.Name, true
			}
		}
		if inner, ok := t.X.(*ast.SelectorExpr); ok {
			if base, isIdent := inner.X.(*ast.Ident); isIdent && owner != "" && base.Name == recv {
				if declared, known := fields[owner+"."+inner.Sel.Name]; known {
					return declared + "." + t.Sel.Name, true
				}
			}
		}
	}
	return "", false
}

func exprText(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprText(t.X) + "." + t.Sel.Name
	case *ast.FuncLit:
		return "func literal"
	}
	return "expression"
}

// reachesScopeDoor walks calls within the package from start, returning the chain that
// arrives at a service-layer authorisation call, or nil. The walk is readpath's, with the
// target inverted: there, arriving is the violation; here, arriving is the requirement.
func reachesScopeDoor(start string, funcs map[string]readPathFunc, fields fieldIndex) []string {
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
					if base.Name == scopeDoorPkg && scopeDoorFuncs[fun.Sel.Name] {
						hit = true
						return false
					}
					if owner != "" && recv != "" && base.Name == recv {
						next = append(next, owner+"."+fun.Sel.Name)
					}
					return true
				}
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
