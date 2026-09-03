package rbac

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// CacheWindow is the longest a revoked role can go on working.
//
// The engine resolves a person's roles from a cache that is invalidated on every grant and
// revocation, so in the normal case a revoked role stops on the next request. The window
// is the bound for the abnormal case — an invalidation that did not reach a second
// process, a cache that was down when it was sent. Thirty seconds is the plan's proposal
// (CP19 criterion 5); TestRevocationTakesEffectWithinTheWindow holds it to that.
const CacheWindow = 30 * time.Second

// GrantReader reads a person's live roles. The auth store implements it.
type GrantReader interface {
	LiveGrants(ctx context.Context, userID uuid.UUID) ([]auth.Grant, error)
	GetUser(ctx context.Context, id uuid.UUID) (auth.User, error)
}

// Cache is what the resolver needs from Redis: a bounded, invalidatable memo. Memory in
// tests, Redis in production (platform/cache satisfies it).
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Membership is what the cache holds per person: live roles, and the account status that
// decides whether any of them count.
type Membership struct {
	Roles  []auth.RoleCode `json:"roles"`
	Active bool            `json:"active"`
}

// Resolver turns a user id into a Subject, through the cache.
type Resolver struct {
	grants GrantReader
	cache  Cache
	clock  clock.Clock
	window time.Duration
}

// ResolverConfig assembles one.
type ResolverConfig struct {
	Grants GrantReader
	Cache  Cache
	Clock  clock.Clock
	// Window overrides CacheWindow; tests only.
	Window time.Duration
}

func NewResolver(cfg ResolverConfig) *Resolver {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Window == 0 {
		cfg.Window = CacheWindow
	}
	if cfg.Cache == nil {
		cfg.Cache = NewMemoryCache(cfg.Clock)
	}
	return &Resolver{grants: cfg.Grants, cache: cfg.Cache, clock: cfg.Clock, window: cfg.Window}
}

func cacheKey(userID uuid.UUID) string { return "rbac:membership:" + userID.String() }

// Membership returns a person's live roles, from the cache when it is fresh.
func (r *Resolver) Membership(ctx context.Context, userID uuid.UUID) (Membership, error) {
	if raw, ok, err := r.cache.Get(ctx, cacheKey(userID)); err == nil && ok {
		var m Membership
		if json.Unmarshal(raw, &m) == nil {
			return m, nil
		}
	}
	user, err := r.grants.GetUser(ctx, userID)
	if err != nil {
		return Membership{}, fmt.Errorf("loading the user: %w", err)
	}
	live, err := r.grants.LiveGrants(ctx, userID)
	if err != nil {
		return Membership{}, fmt.Errorf("reading live grants: %w", err)
	}
	m := Membership{Active: user.Status == auth.StatusActive, Roles: make([]auth.RoleCode, 0, len(live))}
	for _, g := range live {
		m.Roles = append(m.Roles, g.RoleCode)
	}
	if raw, err := json.Marshal(m); err == nil {
		// A cache that cannot be written is a slower engine, not a wrong one.
		_ = r.cache.Set(ctx, cacheKey(userID), raw, r.window)
	}
	return m, nil
}

// Subject builds the engine's view of a person. activeRole may be empty; stationID nil.
func (r *Resolver) Subject(ctx context.Context, userID, facilityID uuid.UUID, activeRole auth.RoleCode, stationID *uuid.UUID) (Subject, error) {
	m, err := r.Membership(ctx, userID)
	if err != nil {
		return Subject{}, err
	}
	if !m.Active {
		// A suspended account holds no roles for the engine's purposes: every decision
		// denies with permission_not_held, which is the safe answer.
		return Subject{UserID: userID, FacilityID: facilityID, Permissions: auth.NewPermissionSet()}, nil
	}
	return Subject{
		UserID: userID, FacilityID: facilityID, Roles: m.Roles, ActiveRole: activeRole,
		StationID: stationID, Permissions: UnionFor(m.Roles),
	}, nil
}

// Invalidate drops a person's cached membership. Called by the auth service on every
// grant, revocation and status change.
func (r *Resolver) Invalidate(ctx context.Context, userID uuid.UUID) error {
	return r.cache.Delete(ctx, cacheKey(userID))
}

// --- the in-memory cache ---

// MemoryCache is a Cache for one process. Tests, and a fallback when Redis is not
// configured — bounded by the same window, so its staleness has the same ceiling.
type MemoryCache struct {
	clock clock.Clock
	mu    sync.Mutex
	items map[string]memoryItem
}

type memoryItem struct {
	value   []byte
	expires time.Time
}

func NewMemoryCache(c clock.Clock) *MemoryCache {
	return &MemoryCache{clock: c, items: map[string]memoryItem{}}
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[key]
	if !ok || !m.clock.Now().Before(item.expires) {
		delete(m.items, key)
		return nil, false, nil
	}
	return item.value, true, nil
}

func (m *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = memoryItem{value: value, expires: m.clock.Now().Add(ttl)}
	return nil
}

func (m *MemoryCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}
