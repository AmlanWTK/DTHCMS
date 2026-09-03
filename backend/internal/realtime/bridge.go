package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// The Redis bridge: one instance publishes, every instance delivers.
//
// A clinic runs more than one API process, and a subscriber is connected to exactly one of
// them. Without a bridge, a measurement recorded on instance A would reach only the screens
// that happen to be connected to A — which is worse than no realtime at all, because it is
// intermittently right.
//
// Redis pub/sub is fire-and-forget and delivers nothing to a subscriber that was
// disconnected at the moment of publication. That is acceptable here and only here: the
// ledger is the record, the socket is a nicety, and a client that missed a message
// reconciles by pull (criterion 4). Nothing in this file is allowed to become the thing a
// clinical guarantee rests on.

// Channel is the Redis channel. One channel rather than one per topic: the fan-out index
// lives in the hub, subscribing to thousands of Redis channels would cost more than
// filtering locally, and a single channel means an instance cannot miss a topic it started
// caring about a moment ago.
const Channel = "dthcms:realtime"

// Publisher puts a message where every instance can see it.
type Publisher interface {
	Publish(ctx context.Context, m Message) error
}

// RedisPublisher publishes to the shared channel.
type RedisPublisher struct {
	redis  redis.UniversalClient
	logger *slog.Logger
}

func NewPublisher(client redis.UniversalClient, logger *slog.Logger) *RedisPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisPublisher{redis: client, logger: logger}
}

var _ Publisher = (*RedisPublisher)(nil)

// Publish validates and sends. It is called **after commit** and never inside a
// transaction: a message describing a write that then rolled back cannot be recalled.
func (p *RedisPublisher) Publish(ctx context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	payload, err := encode(m)
	if err != nil {
		return err
	}
	if err := p.redis.Publish(ctx, Channel, payload).Err(); err != nil {
		// A publication that failed is a screen that does not update until its next poll.
		// It is not a write that failed, and it must never be reported as one.
		return fmt.Errorf("realtime: publishing %s on %s: %w", m.Kind, m.Topic, err)
	}
	return nil
}

// Bridge subscribes to the channel and hands every message to the hub.
type Bridge struct {
	redis  redis.UniversalClient
	hub    *Hub
	logger *slog.Logger

	// Backoff is how long to wait before re-subscribing after the connection to Redis
	// fails. A gateway that gave up on the first error would stop updating screens for the
	// rest of the day over a Redis restart.
	Backoff time.Duration

	once sync.Once
}

func NewBridge(client redis.UniversalClient, hub *Hub, logger *slog.Logger) *Bridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &Bridge{redis: client, hub: hub, logger: logger, Backoff: time.Second}
}

// Run subscribes and delivers until the context is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	for {
		if err := b.follow(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			b.logger.ErrorContext(ctx, "realtime bridge lost its subscription; retrying",
				"error", err.Error(), "retry_in", b.Backoff.String())
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(b.Backoff):
			}
			continue
		}
		return nil
	}
}

func (b *Bridge) follow(ctx context.Context) error {
	sub := b.redis.Subscribe(ctx, Channel)
	defer func() { _ = sub.Close() }()

	// Wait for the subscription to be confirmed before reporting readiness: a gateway that
	// says it is ready and is not yet subscribed silently loses the first minute's traffic.
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	b.once.Do(func() { b.logger.InfoContext(ctx, "realtime bridge subscribed", "channel", Channel) })

	messages := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case raw, ok := <-messages:
			if !ok {
				return errors.New("the redis subscription closed")
			}
			m, err := decode([]byte(raw.Payload))
			if err != nil {
				// A message this instance cannot parse is one a newer instance published
				// in a shape this one does not know. Logging and continuing is right:
				// stopping would take the whole gateway down during a rolling deploy.
				b.logger.WarnContext(ctx, "realtime message not understood", "error", err.Error())
				continue
			}
			if err := m.Validate(); err != nil {
				b.logger.WarnContext(ctx, "realtime message refused", "error", err.Error())
				continue
			}
			b.hub.Deliver(m)
		}
	}
}
