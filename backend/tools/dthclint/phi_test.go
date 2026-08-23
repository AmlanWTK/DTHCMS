package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPHIRejectsPatientIdentifiersInLogs(t *testing.T) {
	findings, err := RunPHI("testdata/badphi")
	if err != nil {
		t.Fatalf("RunPHI: %v", err)
	}

	want := []string{"name", "national_id", "phone", "password"}
	for _, key := range want {
		found := false
		for _, f := range findings {
			if strings.Contains(f.Message, `"`+key+`"`) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("logging under key %q was not reported; findings: %v", key, findings)
		}
	}
}

func TestPHIRejectsPatientIdentifiersInSpansAndMetrics(t *testing.T) {
	findings, err := RunPHI("testdata/badphi")
	if err != nil {
		t.Fatalf("RunPHI: %v", err)
	}

	// A span attribute and a metric label reach the same backend as a log line, so the
	// same rule applies. enduser.email is the namespaced form: OpenTelemetry conventions
	// prefix their keys, and a checker that only matched whole keys would miss all of them.
	for _, key := range []string{"patient_name", "enduser.email", "national_id", "phone"} {
		var found *Finding
		for i, f := range findings {
			if strings.Contains(f.Message, `"`+key+`"`) && strings.Contains(f.File, "telemetry.go") {
				found = &findings[i]
				break
			}
		}
		if found == nil {
			t.Errorf("span or metric key %q was not reported; findings: %v", key, findings)
			continue
		}
		if !strings.Contains(found.Message, "span attribute or metric label") {
			t.Errorf("finding for %q should say it is telemetry, not logging: %s", key, found.Message)
		}
	}
}

func TestPHIAllowsSafeKeysAndReviewedExceptions(t *testing.T) {
	findings, err := RunPHI("testdata/badphi")
	if err != nil {
		t.Fatalf("RunPHI: %v", err)
	}

	// Eight offenders in the fixture: four log calls, three span attributes and one
	// metric label. The patient_id / age_months / http.route calls and the explicitly
	// ignored line must not be reported.
	if len(findings) != 8 {
		t.Fatalf("expected exactly 8 findings, got %d: %v", len(findings), findings)
	}

	for _, f := range findings {
		if strings.Contains(f.Message, "patient_id") ||
			strings.Contains(f.Message, "age_months") ||
			strings.Contains(f.Message, "http.route") {
			t.Errorf("safe key reported as a violation: %v", f)
		}
		if f.Hint == "" {
			t.Errorf("finding has no hint; a rule that does not say what to do instead gets ignored: %v", f)
		}
	}
}

// TestPHIActuallyScansTheTree exists because the check below did not, for two
// checkpoints, and reported success while scanning nothing.
//
// walkGoFiles skipped any directory whose name began with a dot, and ".." begins with a
// dot. A walk rooted at a relative parent path therefore stopped at the first entry and
// returned no findings — indistinguishable, from the outside, from a clean tree. That is
// the failure mode a guardrail must not have: it did not break, it went quiet.
//
// So the tree scan is now asserted to have actually read files, not merely to have
// returned nothing.
func TestPHIActuallyScansTheTree(t *testing.T) {
	for _, root := range []string{"../..", "."} {
		scanned := 0
		err := filepath.WalkDir(mustAbs(t, root), walkGoFiles(func(string) error {
			scanned++
			return nil
		}))
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
		if scanned == 0 {
			t.Errorf("walking %q visited no Go files; the check would pass without reading anything", root)
		}
	}
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolving %s: %v", path, err)
	}
	return abs
}

func TestPHICleanOnRealSource(t *testing.T) {
	// The production tree must always be clean. This is the test that would have caught
	// a patient name reaching a log line, on the commit that introduced it.
	findings, err := RunPHI("../..")
	if err != nil {
		t.Fatalf("RunPHI: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("PHI violations in the backend tree: %v", findings)
	}
}
