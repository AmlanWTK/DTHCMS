package version

import "testing"

// TestCurrentReportsVersion is deliberately trivial. Its purpose at CP01 is to prove
// the Go test runner executes in CI and that a failing test fails the build.
func TestCurrentReportsVersion(t *testing.T) {
	got := Current()

	if got.Version == "" {
		t.Error("Version is empty; every build must report a version")
	}
	if got.Commit == "" {
		t.Error("Commit is empty; every build must be traceable to a commit")
	}
}

func TestStringIncludesVersion(t *testing.T) {
	if s := String(); s == "" {
		t.Fatal("String() returned an empty string")
	}
}
