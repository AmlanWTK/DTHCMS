package ids

import (
	"sort"
	"testing"
)

func TestUUIDv7IsUniqueAndValid(t *testing.T) {
	gen := UUIDv7{}
	seen := make(map[string]bool, 1000)

	for i := 0; i < 1000; i++ {
		id := gen.New()
		if !Valid(id) {
			t.Fatalf("generated an invalid UUID: %q", id)
		}
		if seen[id] {
			t.Fatalf("generated a duplicate identifier: %q", id)
		}
		seen[id] = true
	}
}

func TestUUIDv7IsTimeOrdered(t *testing.T) {
	// Time ordering is why v7 was chosen: the event ledger will hold millions of rows,
	// and index locality depends on identifiers increasing over time.
	gen := UUIDv7{}
	ids := make([]string, 200)
	for i := range ids {
		ids[i] = gen.New()
	}

	sorted := make([]string, len(ids))
	copy(sorted, ids)
	sort.Strings(sorted)

	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("identifiers are not lexically time-ordered at position %d", i)
		}
	}
}

func TestSequentialIsPredictable(t *testing.T) {
	gen := &Sequential{Prefix: "evt"}

	if got := gen.New(); got != "evt_00000001" {
		t.Errorf("first = %q, want evt_00000001", got)
	}
	if got := gen.New(); got != "evt_00000002" {
		t.Errorf("second = %q, want evt_00000002", got)
	}
}

func TestValidRejectsNonsense(t *testing.T) {
	for _, bad := range []string{"", "not-a-uuid", "12345", "pat_00000001"} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true, want false", bad)
		}
	}
}
