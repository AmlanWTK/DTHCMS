package main

import (
	"strings"
	"testing"
)

func TestTestOnlyFindsEveryDoorProductionCodeOpens(t *testing.T) {
	findings, err := RunTestOnly("testdata/badtestonly")
	if err != nil {
		t.Fatalf("RunTestOnly: %v", err)
	}

	want := map[string]bool{
		"cmd/api/main.go":           false, // a composition root is not exempt
		"internal/handler/write.go": false, // an aliased import still resolves
		"internal/ledger/inside.go": false, // nor is the declaring package itself
	}
	for _, f := range findings {
		if _, expected := want[f.File]; !expected {
			t.Errorf("unexpected finding in %s:%d — %s", f.File, f.Line, f.Message)
			continue
		}
		want[f.File] = true
		if f.Line == 0 {
			t.Errorf("%s: no line number; a finding must be navigable from the CI log", f.File)
		}
		if !strings.Contains(f.Hint, "ActorFrom") {
			t.Errorf("%s: the hint does not say what to do instead: %s", f.File, f.Hint)
		}
	}
	for file, found := range want {
		if !found {
			t.Errorf("%s calls a test-only function and was not reported", file)
		}
	}
}

func TestTestOnlyLeavesTestsAndLookalikesAlone(t *testing.T) {
	findings, err := RunTestOnly("testdata/badtestonly")
	if err != nil {
		t.Fatalf("RunTestOnly: %v", err)
	}
	for _, f := range findings {
		if strings.HasSuffix(f.File, "_test.go") {
			t.Errorf("a test file was reported: %s:%d", f.File, f.Line)
		}
		if f.File == "internal/handler/other.go" {
			t.Errorf("a same-named function in another package was reported as the marked one: %s:%d", f.File, f.Line)
		}
	}
}

func TestTestOnlyIsQuietWhenNoDoorIsMarked(t *testing.T) {
	findings, err := RunTestOnly("testdata/good")
	if err != nil {
		t.Fatalf("RunTestOnly: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no violations, got %v", findings)
	}
}

// The rule's real subject: eventstore.ActorForTest, in the real tree.
func TestTheRealActorDoorIsMarkedAndUnused(t *testing.T) {
	marked, err := markedFuncs("../..", "github.com/AmlanWTK/DTHCMS/backend")
	if err != nil {
		t.Fatalf("markedFuncs: %v", err)
	}
	doors := marked["ActorForTest"]
	if len(doors) != 1 || doors[0].Package != "eventstore" {
		t.Fatalf("eventstore.ActorForTest is not marked //dthclint:testonly: %+v", doors)
	}
	findings, err := RunTestOnly("../..")
	if err != nil {
		t.Fatalf("RunTestOnly: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("production code calls a test-only door: %v", findings)
	}
}
