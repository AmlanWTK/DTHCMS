package eventstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// BenchmarkAppend: `go test ./internal/eventstore -bench Append -run ^$`. One writer, one
// aggregate — the serialised worst case, which is also the one the p95 budget is about.
func BenchmarkAppend(b *testing.B) {
	h := newHarness(&testing.T{})
	ctx := context.Background()
	visit := uuid.New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := h.height(visit, 150)
		e.OccurredAt = time.Now()
		if _, err := h.store.Append(ctx, e); err != nil {
			b.Fatal(err)
		}
	}
}
