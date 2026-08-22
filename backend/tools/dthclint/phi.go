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
)

// bannedLogKeys are structured-logging keys that would put identifiable patient data
// into logs. Logs are not access-controlled, not covered by the clinical audit trail,
// and are frequently shipped to third-party services — so patient identifiers must
// never reach them. Log the patient's ID; resolve it through the audited path when a
// human genuinely needs to know who it is.
var bannedLogKeys = map[string]string{
	"name":            "log patient_id instead",
	"patient_name":    "log patient_id instead",
	"full_name":       "log patient_id instead",
	"name_bn":         "log patient_id instead",
	"name_en":         "log patient_id instead",
	"nid":             "national IDs must never be logged, not even masked",
	"national_id":     "national IDs must never be logged, not even masked",
	"phone":           "log patient_id instead",
	"mobile":          "log patient_id instead",
	"address":         "log patient_id instead",
	"dob":             "log age_years or age_months if you need it",
	"date_of_birth":   "log age_years or age_months if you need it",
	"email":           "log user_id instead",
	"photo":           "never log image data or its location",
	"diagnosis":       "clinical detail belongs in the event ledger, not in logs",
	"prescription":    "clinical detail belongs in the event ledger, not in logs",
	"password":        "never log credentials, even hashed",
	"token":           "never log credentials",
	"secret":          "never log credentials",
	"otp":             "never log authentication codes",
	"totp_secret":     "never log authentication secrets",
	"national_id_raw": "national IDs must never be logged",
}

// logFuncs are the call names treated as logging calls.
var logFuncs = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
	"With": true, "Printf": true, "Print": true, "Println": true,
	"String": true, "Any": true, "Group": true,
}

const ignoreDirective = "phicheck:ignore"

// RunPHI scans Go sources for patient identifiers used as structured-logging keys.
func RunPHI(root string) ([]Finding, error) {
	var findings []Finding

	err := filepath.WalkDir(root, walkGoFiles(func(path string) error {
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
		if !ok || !isLogCall(call) {
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
			hint, banned := bannedLogKeys[key]
			if !banned {
				continue
			}

			line := fset.Position(lit.Pos()).Line
			if ignored[line] {
				continue
			}

			findings = append(findings, Finding{
				Check:   "phi",
				File:    relative(root, path),
				Line:    line,
				Message: fmt.Sprintf("patient or credential data logged under key %q", value),
				Hint:    hint + fmt.Sprintf("  (suppress with a trailing // %s <reason> only when the value provably contains no patient data)", ignoreDirective),
			})
		}
		return true
	})

	return findings, nil
}

// isLogCall reports whether a call looks like structured logging: log.Info(...),
// slog.String(...), logger.With(...) and similar.
func isLogCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return logFuncs[sel.Sel.Name]
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
