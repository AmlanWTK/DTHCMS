package realtime

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Who is actually looking at a screen right now (CP50 criterion 4).
//
// # Why this exists at all
//
// Everything else in this package is fire-and-forget by design: a message goes to Redis, each
// instance fans it out, and a client that missed one reconciles by reading. That is the right
// design for a queue update, and it is not enough for a critical value. "The consultant sees
// the alert within five seconds" cannot be promised by a channel that does not know whether
// anybody is on the other end — and the honest response to nobody being there is not a retry,
// it is telling the operator to walk down the corridor.
//
// So a connection that *may receive alerts* registers itself here, with a short lease, and
// refreshes on every heartbeat. Counting the set answers one question: if this alert goes out
// now, will a person see it?
//
// # Why the lease is short
//
// A stale presence entry is worse than no presence registry: it makes the system say the
// consultant was told when the consultant's tablet was in a drawer. The lease is a small
// multiple of the heartbeat, so a screen that stopped answering disappears from the count
// within one missed beat rather than at some future cleanup.
//
// # What is not stored
//
// A user id, a facility, and nothing else. No name, no role, no patient — this is a set of
// people who are online, and if it leaked it would say only that. The capability is part of
// the key rather than a stored attribute so that a subscriber who loses the permission simply
// stops registering under it.
type Presence struct {
	client redis.UniversalClient
}

func NewPresence(client redis.UniversalClient) *Presence {
	return &Presence{client: client}
}

// CapabilityAlerts is the audience for critical values: connections whose subject may read
// them. Named as a constant because it is half of a Redis key, and a key built by
// concatenation at three call sites is a key that is spelt three ways.
const CapabilityAlerts = "alerts"

// PresenceLease is how long an arrival is believed without a refresh.
//
// Deliberately short. The heartbeat is 20 seconds by default, so a connection refreshes three
// times inside one lease — and a tablet that went into a bag stops counting within a minute,
// which is inside the escalation chain's first window rather than outside it.
const PresenceLease = 60 * time.Second

func presenceKey(capability string, facility uuid.UUID) string {
	return fmt.Sprintf("dthcms:presence:%s:%s", capability, facility)
}

// Arrive records that this user has a live screen that can receive `capability`, and renews
// the lease. Called on connect and again on every heartbeat.
func (p *Presence) Arrive(ctx context.Context, capability string, facility, user uuid.UUID) error {
	if p == nil || p.client == nil {
		return nil
	}
	key := presenceKey(capability, facility)
	now := time.Now().UnixMilli()
	// A sorted set scored by arrival time rather than a plain set, because expiry has to be
	// per-member: one key with a TTL would drop every subscriber the moment the last one to
	// refresh let it lapse.
	if err := p.client.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: user.String()}).Err(); err != nil {
		return err
	}
	// A ceiling on the key itself as well, so a facility that goes quiet for a night leaves
	// nothing behind.
	return p.client.Expire(ctx, key, PresenceLease*4).Err()
}

// Depart removes a user when their last connection closes.
func (p *Presence) Depart(ctx context.Context, capability string, facility, user uuid.UUID) error {
	if p == nil || p.client == nil {
		return nil
	}
	return p.client.ZRem(ctx, presenceKey(capability, facility), user.String()).Err()
}

// Count is how many people could see an alert published right now.
//
// Entries older than the lease are swept before counting rather than trusted: a process that
// died without calling Depart would otherwise leave its user counted forever, and the whole
// value of this number is that it is not optimistic.
func (p *Presence) Count(ctx context.Context, capability string, facility uuid.UUID) (int, error) {
	if p == nil || p.client == nil {
		return 0, nil
	}
	key := presenceKey(capability, facility)
	cutoff := time.Now().Add(-PresenceLease).UnixMilli()
	if err := p.client.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("(%d", cutoff)).Err(); err != nil {
		return 0, err
	}
	n, err := p.client.ZCard(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// Present lists who is there, for the escalation sweep — which needs to address the next
// role's people individually, on their own topics.
func (p *Presence) Present(ctx context.Context, capability string, facility uuid.UUID) ([]uuid.UUID, error) {
	if p == nil || p.client == nil {
		return nil, nil
	}
	key := presenceKey(capability, facility)
	cutoff := time.Now().Add(-PresenceLease).UnixMilli()
	if err := p.client.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("(%d", cutoff)).Err(); err != nil {
		return nil, err
	}
	members, err := p.client.ZRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(members))
	for _, member := range members {
		if id, parseErr := uuid.Parse(member); parseErr == nil {
			out = append(out, id)
		}
	}
	return out, nil
}
