package realtime

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/telemetry"
)

// Connection metrics (CP26).
//
// The questions an operator actually asks about a realtime gateway are: how many people are
// connected, is anybody being dropped, and is a message getting there. So: a gauge of open
// connections, a counter of closes by reason, a counter of messages delivered and refused,
// a counter of drops, and a histogram of how long a connection lasted.
//
// "Refused" is counted separately from "delivered" on purpose. A refusal is the RBAC filter
// working, and a sudden change in the ratio is either a permission change or a bug — both
// worth being able to see.

// Metrics are the gateway's instruments.
type Metrics struct {
	open       atomic.Int64
	closes     metric.Int64Counter
	delivered  metric.Int64Counter
	refused    metric.Int64Counter
	dropped    metric.Int64Counter
	lifetime   metric.Float64Histogram
	unregister func() error
}

// NewMetrics registers the instruments. A nil provider yields a Metrics that counts
// nothing, which is what the tests want.
func NewMetrics(provider *telemetry.Provider) (*Metrics, error) {
	m := &Metrics{}
	meter := provider.Meter("dthcms/realtime")

	open, err := meter.Int64ObservableGauge("dthcms.realtime.connections",
		metric.WithDescription("WebSocket connections this instance holds open."),
		metric.WithUnit("{connection}"))
	if err != nil {
		return nil, fmt.Errorf("creating the connection gauge: %w", err)
	}
	if m.closes, err = meter.Int64Counter("dthcms.realtime.connections.closed",
		metric.WithDescription("Connections closed, by reason."),
		metric.WithUnit("{connection}")); err != nil {
		return nil, err
	}
	if m.delivered, err = meter.Int64Counter("dthcms.realtime.messages.delivered",
		metric.WithDescription("Messages written to a subscriber's socket."),
		metric.WithUnit("{message}")); err != nil {
		return nil, err
	}
	if m.refused, err = meter.Int64Counter("dthcms.realtime.messages.refused",
		metric.WithDescription("Messages withheld from a subscriber by the RBAC filter."),
		metric.WithUnit("{message}")); err != nil {
		return nil, err
	}
	if m.dropped, err = meter.Int64Counter("dthcms.realtime.messages.dropped",
		metric.WithDescription("Messages a subscriber was too slow to receive. The client reconciles by pull."),
		metric.WithUnit("{message}")); err != nil {
		return nil, err
	}
	if m.lifetime, err = meter.Float64Histogram("dthcms.realtime.connection.lifetime",
		metric.WithDescription("How long a connection lasted."),
		metric.WithUnit("s")); err != nil {
		return nil, err
	}

	registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		observer.ObserveInt64(open, m.open.Load())
		return nil
	}, open)
	if err != nil {
		return nil, fmt.Errorf("registering the connection gauge: %w", err)
	}
	m.unregister = registration.Unregister
	return m, nil
}

// Close unregisters the observable gauge.
func (m *Metrics) Close() error {
	if m == nil || m.unregister == nil {
		return nil
	}
	return m.unregister()
}

func (m *Metrics) connectionOpened(total int) {
	if m == nil {
		return
	}
	m.open.Store(int64(total))
}

func (m *Metrics) connectionClosed(reason string, remaining int, lifetime time.Duration) {
	if m == nil {
		return
	}
	m.open.Store(int64(remaining))
	if m.closes != nil {
		m.closes.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", reason)))
	}
	if m.lifetime != nil {
		m.lifetime.Record(context.Background(), lifetime.Seconds())
	}
}

func (m *Metrics) messageDelivered(topicKind string, delivered, refused int) {
	if m == nil {
		return
	}
	labels := metric.WithAttributes(attribute.String("topic_kind", topicKind))
	if delivered > 0 && m.delivered != nil {
		m.delivered.Add(context.Background(), int64(delivered), labels)
	}
	if refused > 0 && m.refused != nil {
		m.refused.Add(context.Background(), int64(refused), labels)
	}
}

func (m *Metrics) messageDropped() {
	if m == nil || m.dropped == nil {
		return
	}
	m.dropped.Add(context.Background(), 1)
}
