package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
)

// The hub: which connections exist, what each subscribes to, and how a message reaches the
// ones entitled to it.
//
// One instance's connections live here. Messages reach other instances through Redis
// (bridge.go), which delivers them back into every instance's Deliver — including the one
// that published, so that a single code path fans out and there is no "local plus remote"
// asymmetry to get wrong.

// Limits bound what one person, and one process, can hold open.
type Limits struct {
	// PerUser is how many connections one person may hold at once — a browser tab, a
	// second tab, a phone. Beyond it, the oldest is closed rather than the newest refused:
	// the newest is the one the person is looking at.
	PerUser int
	// PerDevice is the same for one enrolled device.
	PerDevice int
	// Total is the process's ceiling. At it, a new connection is refused with 1013 (try
	// again later), which is honest — the client should retry elsewhere or in a moment.
	Total int
	// SendQueue is how many messages may wait for one slow socket before the connection is
	// told it has fallen behind. See send() for what happens then.
	SendQueue int
}

func (l *Limits) defaults() {
	if l.PerUser <= 0 {
		l.PerUser = 8
	}
	if l.PerDevice <= 0 {
		l.PerDevice = 4
	}
	if l.Total <= 0 {
		l.Total = 5000
	}
	if l.SendQueue <= 0 {
		l.SendQueue = 256
	}
}

// ErrTooManyConnections is what Register returns at the process ceiling.
var ErrTooManyConnections = fmt.Errorf("realtime: too many connections")

// Connection is one subscriber, as the hub sees it. The socket itself is behind Send.
type Connection struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	DeviceID uuid.UUID
	Facility uuid.UUID

	// subject is the RBAC subject every message is filtered against. It is replaced on
	// re-authentication (a token refresh, a role switch) under the mutex, so a role
	// revoked mid-connection stops applying at the next message rather than at reconnect.
	subject rbac.Subject

	topics map[Topic]bool
	// out is this connection's queue. Bounded: an unbounded queue in front of a slow
	// reader is a memory leak with a client attached.
	out chan Envelope

	// cursor is the highest sequence sent, and what a resume is measured from.
	cursor atomic.Int64
	// dropped counts what could not be queued. Non-zero is the client's instruction to
	// reconcile by pull.
	dropped atomic.Int64

	openedAt time.Time
	mu       sync.RWMutex
	closed   bool
	closeCh  chan struct{}
	// closer shuts the socket. The hub does not know what a socket is, but it does decide
	// when a connection ends — an eviction, a shutdown — and a connection whose socket
	// stays open after the hub has forgotten it is a goroutine and a file descriptor
	// nobody will ever reclaim.
	closer func(reason string)
}

// OnClose registers what to do when the hub drops this connection. Set once, by the
// handler, immediately after Register.
func (c *Connection) OnClose(fn func(reason string)) {
	c.mu.Lock()
	c.closer = fn
	c.mu.Unlock()
}

// Cursor is the highest sequence this connection has been sent.
func (c *Connection) Cursor() int64 { return c.cursor.Load() }

// Dropped is how many messages this connection missed by being too slow.
func (c *Connection) Dropped() int64 { return c.dropped.Load() }

// Out is the queue the writer goroutine drains.
func (c *Connection) Out() <-chan Envelope { return c.out }

// Done closes when the hub has unregistered this connection.
func (c *Connection) Done() <-chan struct{} { return c.closeCh }

// Subject returns the current RBAC subject.
func (c *Connection) Subject() rbac.Subject {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.subject
}

// Reauthenticate replaces the subject a connection is filtered against.
//
// A WebSocket outlives an access token. Without this, a role revoked at 09:05 would keep
// receiving what it was entitled to at 09:00 until the socket dropped, which is the exact
// failure the "revoked within one request" rule (CP16) exists to prevent on the HTTP side.
func (c *Connection) Reauthenticate(subject rbac.Subject) {
	c.mu.Lock()
	c.subject = subject
	c.mu.Unlock()
}

// Topics returns what this connection is subscribed to, sorted for a stable log.
func (c *Connection) Topics() []Topic {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Topic, 0, len(c.topics))
	for t := range c.topics {
		out = append(out, t)
	}
	sortTopics(out)
	return out
}

func sortTopics(topics []Topic) {
	for i := 1; i < len(topics); i++ {
		for j := i; j > 0 && topics[j] < topics[j-1]; j-- {
			topics[j], topics[j-1] = topics[j-1], topics[j]
		}
	}
}

// HubConfig assembles a Hub.
type HubConfig struct {
	Filter Filter
	Limits Limits
	Clock  clock.Clock
	Logger *slog.Logger
	// Metrics is optional; nil means the hub counts nothing.
	Metrics *Metrics
}

// Hub is the registry and the fan-out.
type Hub struct {
	filter  Filter
	limits  Limits
	clock   clock.Clock
	logger  *slog.Logger
	metrics *Metrics

	mu          sync.RWMutex
	connections map[uuid.UUID]*Connection
	// byTopic is the fan-out index. Without it, delivering one message means walking every
	// connection, which is fine at ten and not at two hundred.
	byTopic  map[Topic]map[uuid.UUID]*Connection
	byUser   map[uuid.UUID]map[uuid.UUID]*Connection
	byDevice map[uuid.UUID]map[uuid.UUID]*Connection
}

func NewHub(cfg HubConfig) *Hub {
	cfg.Limits.defaults()
	if cfg.Filter == nil {
		cfg.Filter = RBACFilter{}
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Hub{
		filter: cfg.Filter, limits: cfg.Limits, clock: cfg.Clock,
		logger: cfg.Logger, metrics: cfg.Metrics,
		connections: map[uuid.UUID]*Connection{},
		byTopic:     map[Topic]map[uuid.UUID]*Connection{},
		byUser:      map[uuid.UUID]map[uuid.UUID]*Connection{},
		byDevice:    map[uuid.UUID]map[uuid.UUID]*Connection{},
	}
}

// Register admits a connection, enforcing the limits.
func (h *Hub) Register(subject rbac.Subject, deviceID uuid.UUID) (*Connection, error) {
	h.mu.Lock()
	if len(h.connections) >= h.limits.Total {
		h.mu.Unlock()
		return nil, ErrTooManyConnections
	}

	conn := &Connection{
		ID: uuid.New(), UserID: subject.UserID, DeviceID: deviceID, Facility: subject.FacilityID,
		subject: subject, topics: map[Topic]bool{},
		out: make(chan Envelope, h.limits.SendQueue), openedAt: h.clock.Now(),
		closeCh: make(chan struct{}),
	}
	h.connections[conn.ID] = conn
	index(h.byUser, subject.UserID, conn)
	if deviceID != uuid.Nil {
		index(h.byDevice, deviceID, conn)
	}

	// Over the per-user or per-device limit, close the oldest. Refusing the newest would
	// mean a person who left a tab open on Friday cannot open one on Monday.
	evict := append(overLimit(h.byUser[subject.UserID], h.limits.PerUser),
		overLimit(h.byDevice[deviceID], h.limits.PerDevice)...)
	h.mu.Unlock()

	for _, old := range evict {
		if old.ID == conn.ID {
			continue
		}
		h.logger.Info("closing the oldest connection for this user",
			"user_id", old.UserID.String(), "connection_id", old.ID.String())
		h.Unregister(old, StatusPolicyViolationTooMany)
	}
	if h.metrics != nil {
		h.metrics.connectionOpened(len(h.connections))
	}
	return conn, nil
}

// StatusPolicyViolationTooMany is the reason an evicted connection is given, so a client
// can tell "you have too many open" from "you are not allowed here".
const StatusPolicyViolationTooMany = "too_many_connections"

func index(m map[uuid.UUID]map[uuid.UUID]*Connection, key uuid.UUID, conn *Connection) {
	if m[key] == nil {
		m[key] = map[uuid.UUID]*Connection{}
	}
	m[key][conn.ID] = conn
}

// overLimit returns the oldest connections beyond the limit.
func overLimit(set map[uuid.UUID]*Connection, limit int) []*Connection {
	if len(set) <= limit {
		return nil
	}
	all := make([]*Connection, 0, len(set))
	for _, c := range set {
		all = append(all, c)
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].openedAt.Before(all[j-1].openedAt); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	return all[:len(all)-limit]
}

// Unregister removes a connection and closes its queue. Idempotent: the reader goroutine
// and the writer goroutine both call it when they finish.
func (h *Hub) Unregister(conn *Connection, reason string) {
	conn.mu.Lock()
	if conn.closed {
		conn.mu.Unlock()
		return
	}
	conn.closed = true
	closer := conn.closer
	topics := make([]Topic, 0, len(conn.topics))
	for t := range conn.topics {
		topics = append(topics, t)
	}
	conn.mu.Unlock()

	h.mu.Lock()
	delete(h.connections, conn.ID)
	for _, t := range topics {
		if set := h.byTopic[t]; set != nil {
			delete(set, conn.ID)
			if len(set) == 0 {
				delete(h.byTopic, t)
			}
		}
	}
	unindex(h.byUser, conn.UserID, conn.ID)
	unindex(h.byDevice, conn.DeviceID, conn.ID)
	remaining := len(h.connections)
	h.mu.Unlock()

	close(conn.closeCh)
	close(conn.out)
	if closer != nil {
		closer(reason)
	}
	if h.metrics != nil {
		h.metrics.connectionClosed(reason, remaining, h.clock.Now().Sub(conn.openedAt))
	}
}

func unindex(m map[uuid.UUID]map[uuid.UUID]*Connection, key, id uuid.UUID) {
	if set := m[key]; set != nil {
		delete(set, id)
		if len(set) == 0 {
			delete(m, key)
		}
	}
}

// Subscribe adds topics to a connection, refusing the ones the subject may not watch.
//
// It returns the topics actually subscribed and the ones refused, rather than failing the
// whole command: a client asking for five topics and being entitled to four should get the
// four and be told about the fifth.
func (h *Hub) Subscribe(conn *Connection, topics []Topic) (added, refused []Topic, err error) {
	subject := conn.Subject()
	for _, t := range topics {
		if err := t.Validate(); err != nil {
			refused = append(refused, t)
			continue
		}
		if !maySubscribe(subject, conn.Facility, t) {
			refused = append(refused, t)
			continue
		}
		added = append(added, t)
	}

	conn.mu.Lock()
	if len(conn.topics)+len(added) > MaxTopicsPerConnection {
		conn.mu.Unlock()
		return nil, topics, fmt.Errorf("realtime: a connection may hold at most %d subscriptions", MaxTopicsPerConnection)
	}
	for _, t := range added {
		conn.topics[t] = true
	}
	conn.mu.Unlock()

	h.mu.Lock()
	for _, t := range added {
		if h.byTopic[t] == nil {
			h.byTopic[t] = map[uuid.UUID]*Connection{}
		}
		h.byTopic[t][conn.ID] = conn
	}
	h.mu.Unlock()

	sortTopics(added)
	sortTopics(refused)
	return added, refused, nil
}

// Unsubscribe removes topics.
func (h *Hub) Unsubscribe(conn *Connection, topics []Topic) []Topic {
	conn.mu.Lock()
	var removed []Topic
	for _, t := range topics {
		if conn.topics[t] {
			delete(conn.topics, t)
			removed = append(removed, t)
		}
	}
	conn.mu.Unlock()

	h.mu.Lock()
	for _, t := range removed {
		if set := h.byTopic[t]; set != nil {
			delete(set, conn.ID)
			if len(set) == 0 {
				delete(h.byTopic, t)
			}
		}
	}
	h.mu.Unlock()
	sortTopics(removed)
	return removed
}

// Deliver fans one message out to the connections subscribed to its topic and entitled to
// it. Returns how many sockets it reached.
//
// The RBAC decision is made here, per connection, per message — not at subscription time.
// That is criterion 2, and it is also what makes a role revoked mid-connection take effect
// on the next message rather than at reconnect.
func (h *Hub) Deliver(m Message) int {
	h.mu.RLock()
	subscribers := make([]*Connection, 0, len(h.byTopic[m.Topic]))
	for _, conn := range h.byTopic[m.Topic] {
		subscribers = append(subscribers, conn)
	}
	h.mu.RUnlock()

	delivered, refused := 0, 0
	for _, conn := range subscribers {
		if !h.filter.Allow(conn.Subject(), m) {
			refused++
			continue
		}
		message := m
		if h.send(conn, Envelope{Type: "message", Message: &message, At: m.At}) {
			// The cursor is only advanced for a message that was actually queued. A
			// resume must not skip what the client never saw.
			advance(&conn.cursor, m.Seq)
			delivered++
		}
	}
	if h.metrics != nil {
		h.metrics.messageDelivered(string(m.Topic.Kind()), delivered, refused)
	}
	return delivered
}

// send queues one envelope, or records that it could not.
//
// Backpressure: the queue is bounded, and a full queue does not block the fan-out — one
// slow client must not be able to stall every other subscriber, which is what a blocking
// send would do. The message is dropped and counted, and the count travels to the client on
// the next envelope that fits, telling it to reconcile by pull. Nothing is lost: the events
// are in the ledger (criterion 4).
func (h *Hub) send(conn *Connection, envelope Envelope) bool {
	conn.mu.RLock()
	closed := conn.closed
	conn.mu.RUnlock()
	if closed {
		return false
	}
	if dropped := conn.dropped.Load(); dropped > 0 && envelope.Type == "message" {
		envelope.Dropped = dropped
	}
	select {
	case conn.out <- envelope:
		if envelope.Dropped > 0 {
			conn.dropped.Add(-envelope.Dropped)
		}
		return true
	default:
		conn.dropped.Add(1)
		if h.metrics != nil {
			h.metrics.messageDropped()
		}
		return false
	}
}

// Send queues an envelope the protocol produced — a welcome, an acknowledgement, an error.
func (h *Hub) Send(conn *Connection, envelope Envelope) bool { return h.send(conn, envelope) }

func advance(cursor *atomic.Int64, seq int64) {
	for {
		current := cursor.Load()
		if seq <= current || cursor.CompareAndSwap(current, seq) {
			return
		}
	}
}

// Count is how many connections this instance holds.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}

// ConnectionsFor returns one user's connections, for the re-authentication path and for
// tests.
func (h *Hub) ConnectionsFor(userID uuid.UUID) []*Connection {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Connection, 0, len(h.byUser[userID]))
	for _, conn := range h.byUser[userID] {
		out = append(out, conn)
	}
	return out
}

// CloseAll shuts every connection down, for a graceful stop.
func (h *Hub) CloseAll(ctx context.Context, reason string) {
	h.mu.RLock()
	all := make([]*Connection, 0, len(h.connections))
	for _, conn := range h.connections {
		all = append(all, conn)
	}
	h.mu.RUnlock()
	for _, conn := range all {
		h.send(conn, Envelope{Type: "closing", Detail: reason, At: h.clock.Now()})
		h.Unregister(conn, reason)
		if ctx.Err() != nil {
			return
		}
	}
}

// marshal is here rather than at the call site so that every envelope on the wire has the
// same shape and one place decides it.
func marshal(envelope Envelope) ([]byte, error) { return json.Marshal(envelope) }
