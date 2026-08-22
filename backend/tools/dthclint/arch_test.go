package main

import (
	"strings"
	"testing"
)

func TestArchAcceptsCompliantModules(t *testing.T) {
	findings, err := RunArch("testdata/good")
	if err != nil {
		t.Fatalf("RunArch: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no violations, got %d: %v", len(findings), findings)
	}
}

func TestArchRejectsUndeclaredDependency(t *testing.T) {
	findings, err := RunArch("testdata/badarch")
	if err != nil {
		t.Fatalf("RunArch: %v", err)
	}

	var gotForbiddenImport, gotUndeclaredModule bool
	for _, f := range findings {
		switch {
		case strings.Contains(f.Message, `module "prescription" may not import module "records"`):
			gotForbiddenImport = true
			if f.Line == 0 {
				t.Error("finding has no line number; it must be navigable from the CI log")
			}
		case strings.Contains(f.Message, `module "billing" is not declared`):
			gotUndeclaredModule = true
		}
	}

	if !gotForbiddenImport {
		t.Errorf("the forbidden prescription -> records import was not reported; findings: %v", findings)
	}
	if !gotUndeclaredModule {
		t.Errorf("the undeclared billing module was not reported; findings: %v", findings)
	}
}

func TestArchAllowsSelfAndDeclaredDependencies(t *testing.T) {
	arch, err := LoadArchitecture("testdata/good")
	if err != nil {
		t.Fatalf("LoadArchitecture: %v", err)
	}

	cases := []struct {
		from, to string
		want     bool
	}{
		{"prescription", "prescription", true},
		{"prescription", "patient", true},
		{"prescription", "platform", true},
		{"prescription", "records", false},
		{"platform", "patient", false},
		{"patient", "prescription", false},
	}

	for _, c := range cases {
		if got := arch.Allows(c.from, c.to); got != c.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestRealArchitectureFileIsValid(t *testing.T) {
	// The production rules must always parse, and platform must remain a leaf module:
	// everything depends on it, so a dependency out of platform would create a cycle
	// through most of the system.
	arch, err := LoadArchitecture("../..")
	if err != nil {
		t.Fatalf("architecture.json is not valid: %v", err)
	}
	if len(arch.Modules["platform"]) != 0 {
		t.Errorf("platform must not depend on any module, got %v", arch.Modules["platform"])
	}
	for module, deps := range arch.Modules {
		for _, dep := range deps {
			if _, ok := arch.Modules[dep]; !ok {
				t.Errorf("module %q depends on %q, which is not declared", module, dep)
			}
			if dep == module {
				t.Errorf("module %q lists itself as a dependency", module)
			}
		}
	}
}
