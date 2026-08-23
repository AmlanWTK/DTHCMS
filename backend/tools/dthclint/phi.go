package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/logging"
)

// The banned key list is not defined here. It lives in
// internal/platform/logging.PHIKeys, alongside the runtime handler that redacts the same
// keys, because a build-time rule and a run-time rule that can disagree will eventually
// disagree — and the one that gets forgotten is always the one that mattered.
//
// This checker and that handler are two layers over one list. Static analysis cannot see
// a key assembled from a variable; a runtime handler cannot stop a developer shipping the
// mistake. Neither is sufficient; together they are hard to get past by accident.

// logFuncs are the call names treated as logging calls.
var logFuncs = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
	"With": true, "Printf": true, "Print": true, "Println": true,
	"String": true, "Any": true, "Group": true,
}

// telemetryFuncs are the calls that put a key on a span or a metric.
//
// A span attribute and a metric label are exported to the same third-party backend as a
// log line, are retained as long, and are read by the same people — but they do not look
// like logging, so the rule that covers logging gets forgotten. attribute.String is one
// character away from slog.String and carries exactly the same risk.
//
// Metric labels carry a second hazard that logs do not: unbounded cardinality. A label
// whose value is a patient's name creates one time series per patient, which breaks the
// metrics backend as surely as it breaks confidentiality.
var telemetryFuncs = map[string]bool{
	"Key":           true,
	"String":        true,
	"StringSlice":   true,
	"Int":           true,
	"Int64":         true,
	"IntSlice":      true,
	"Int64Slice":    true,
	"Float64":       true,
	"Float64Slice":  true,
	"Bool":          true,
	"BoolSlice":     true,
	"Stringer":      true,
	"SetAttributes": true,
}

// telemetryPackages are the receivers whose calls are treated as telemetry rather than
// logging. Without this, every method named String in the tree would be inspected.
var telemetryPackages = map[string]bool{
	"attribute": true,
	"semconv":   true,
	"metric":    true,
}

const ignoreDirective = "phicheck:ignore"

// RunPHI scans Go sources for patient identifiers used as structured-logging keys.
func RunPHI(root string) ([]Finding, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", root, err)
	}

	var findings []Finding

	err = filepath.WalkDir(root, walkGoFiles(func(path string) error {
		fileFindings, err := phiCheckFile(path, root)
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)
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

func phiCheckFile(path, root string) ([]Finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	ignored := ignoredLines(fset, file)
	var findings []Finding

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		kind, ok := callKind(call)
		if !ok {
			return true
		}

		for _, arg := range call.Args {
			lit, ok := arg.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}

			key := strings.ToLower(strings.TrimSpace(value))
			hint, banned := logging.PHIKeys[key]
			if !banned {
				// OpenTelemetry namespaces its attribute keys — enduser.name,
				// user.email — so the last segment is checked too. A rule that only
				// matched whole keys would miss every conventional attribute name.
				if idx := strings.LastIndexAny(key, "._"); idx >= 0 && idx+1 < len(key) {
					hint, banned = logging.PHIKeys[key[idx+1:]]
				}
			}
			if !banned {
				continue
			}

			line := fset.Position(lit.Pos()).Line
			if ignored[line] {
				continue
			}

			message := fmt.Sprintf("patient or credential data logged under key %q", value)
			if kind == kindTelemetry {
				message = fmt.Sprintf(
					"patient or credential data recorded under span attribute or metric label key %q", value)
				hint += "; span attributes and metric labels leave the process exactly as log lines do"
			}

			findings = append(findings, Finding{
				Check:   "phi",
				File:    relative(root, path),
				Line:    line,
				Message: message,
				Hint:    hint + fmt.Sprintf("  (suppress with a trailing // %s <reason> only when the value provably contains no patient data)", ignoreDirective),
			})
		}
		return true
	})

	return findings, nil
}

const (
	kindLog       = "log"
	kindTelemetry = "telemetry"
)

// callKind reports whether a call carries structured keys, and of which sort.
//
// slog.String and attribute.String are both matched, and the receiver decides which
// message the developer sees. Being told "this is a span attribute" rather than "this is
// a log key" is the difference between fixing the line and arguing with the linter.
func callKind(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	if ident, ok := sel.X.(*ast.Ident); ok && telemetryPackages[ident.Name] && telemetryFuncs[sel.Sel.Name] {
		return kindTelemetry, true
	}
	// SetAttributes is called on a span value, whose receiver name varies.
	if sel.Sel.Name == "SetAttributes" {
		return kindTelemetry, true
	}
	if logFuncs[sel.Sel.Name] {
		return kindLog, true
	}
	return "", false
}

func ignoredLines(fset *token.FileSet, file *ast.File) map[int]bool {
	ignored := make(map[int]bool)
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.Contains(comment.Text, ignoreDirective) {
				ignored[fset.Position(comment.Pos()).Line] = true
			}
		}
	}
	return ignored
}
