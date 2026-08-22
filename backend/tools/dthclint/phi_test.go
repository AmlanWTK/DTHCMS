package main

import (
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

func TestPHIAllowsSafeKeysAndReviewedExceptions(t *testing.T) {
	findings, err := RunPHI("testdata/badphi")
	if err != nil {
		t.Fatalf("RunPHI: %v", err)
	}

	// Four offenders in the fixture; the patient_id / age_months call and the
	// explicitly-ignored line must not be reported.
	if len(findings) != 4 {
		t.Fatalf("expected exactly 4 findings, got %d: %v", len(findings), findings)
	}

	for _, f := range findings {
		if strings.Contains(f.Message, "patient_id") || strings.Contains(f.Message, "age_months") {
			t.Errorf("safe key reported as a violation: %v", f)
		}
		if f.Hint == "" {
			t.Errorf("finding has no hint; a rule that does not say what to do instead gets ignored: %v", f)
		}
	}
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
