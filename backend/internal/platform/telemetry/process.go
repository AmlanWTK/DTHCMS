package telemetry

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// RegisterProcessMetrics publishes the saturation signals for the Go process itself.
//
// These are deliberately few. A saturation dashboard is read during an incident by
// someone who needs to answer one question — is the process itself in trouble, or is it
// waiting on something else — and four numbers answer it. Thirty do not.
//
// Goroutine count is the one that earns its place most often: a leak shows as a line
// that only goes up, hours before memory pressure makes it obvious, and it distinguishes
// "we are slow because we are busy" from "we are slow because ten thousand goroutines are
// blocked on a dependency that stopped answering".
//
// The Go runtime's own metrics package exposes far more, and an exporter for it can be
// added when there is a question these four cannot answer.
func RegisterProcessMetrics(meter metric.Meter) error {
	started := time.Now()

	goroutines, err := meter.Int64ObservableGauge("process.runtime.go.goroutines",
		metric.WithDescription("Goroutines currently running. A line that only rises is a leak."),
		metric.WithUnit("{goroutine}"))
	if err != nil {
		return fmt.Errorf("creating the goroutine gauge: %w", err)
	}

	heap, err := meter.Int64ObservableGauge("process.runtime.go.mem.heap_inuse",
		metric.WithDescription("Heap memory in use."),
		metric.WithUnit("By"))
	if err != nil {
		return fmt.Errorf("creating the heap gauge: %w", err)
	}

	gc, err := meter.Int64ObservableCounter("process.runtime.go.gc.count",
		metric.WithDescription("Completed garbage collections."),
		metric.WithUnit("{collection}"))
	if err != nil {
		return fmt.Errorf("creating the GC counter: %w", err)
	}

	uptime, err := meter.Float64ObservableGauge("process.uptime",
		metric.WithDescription("Seconds since start. A sawtooth here means the process is restarting."),
		metric.WithUnit("s"))
	if err != nil {
		return fmt.Errorf("creating the uptime gauge: %w", err)
	}

	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)

		observer.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
		observer.ObserveInt64(heap, int64(stats.HeapInuse))
		observer.ObserveInt64(gc, int64(stats.NumGC))
		observer.ObserveFloat64(uptime, time.Since(started).Seconds())
		return nil
	}, goroutines, heap, gc, uptime)
	if err != nil {
		return fmt.Errorf("registering the process metrics callback: %w", err)
	}
	return nil
}
