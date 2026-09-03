package realtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime/ws"
)

// The gateway end to end: a real HTTP server, a real WebSocket handshake, real frames.
//
// Nothing is mocked below the protocol, because every acceptance criterion this checkpoint
// has is about what actually arrives at a socket. What *is* substituted is Redis: the hub's
// Deliver is called directly, which is exactly what the bridge does when a message arrives
// on the channel, so the fan-out under test is the production one. The bridge itself has
// its own test against a real Redis.

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// --- the harness ---

type gateway struct {
	t        *testing.T
	server   *httptest.Server
	hub      *realtime.Hub
	facility uuid.UUID
	clock    *clock.Fixed
	seq      atomic.Int64
}

// fixedResolver is the RBAC resolver, without a database: the tests decide who holds what,
// which is the only thing that varies between them.
type fixedResolver struct {
	mu       sync.Mutex
	subjects map[uuid.UUID]rbac.Subject
	fail     map[uuid.UUID]bool
}

func (f *fixedResolver) Subject(_ context.Context, userID, facilityID uuid.UUID, activeRole string) (rbac.Subject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail[userID] {
		return rbac.Subject{}, fmt.Errorf("this account no longer holds a role here")
	}
	subject, ok := f.subjects[userID]
	if !ok {
		return rbac.Subject{}, fmt.Errorf("unknown user")
	}
	subject.UserID, subject.FacilityID = userID, facilityID
	if activeRole != "" {
		subject.ActiveRole = auth.RoleCode(activeRole)
	}
	return subject, nil
}

func (f *fixedResolver) set(userID uuid.UUID, subject rbac.Subject) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subjects[userID] = subject
}

func (f *fixedResolver) revoke(userID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[userID] = true
}

// staffed makes a subject for a role, with the permissions the catalogue gives it.
func staffed(role auth.RoleCode, facility uuid.UUID, station *uuid.UUID) rbac.Subject {
	return rbac.Subject{
		FacilityID: facility, Roles: []auth.RoleCode{role}, ActiveRole: role,
		StationID: station, Permissions: rbac.RolePermissions[role],
	}
}

func newGateway(t *testing.T, resolver *fixedResolver, limits realtime.Limits) *gateway {
	t.Helper()
	g := &gateway{
		t: t, facility: uuid.New(),
		clock: clock.NewFixed(time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC)),
	}
	g.hub = realtime.NewHub(realtime.HubConfig{
		Filter: realtime.RBACFilter{}, Limits: limits, Clock: g.clock, Logger: testLogger(),
	})
	handler := realtime.NewHandler(realtime.HandlerConfig{
		Hub: g.hub, Resolver: resolver, Clock: g.clock, Logger: testLogger(),
		Heartbeat: 200 * time.Millisecond, ReauthEvery: 150 * time.Millisecond,
	})

	// The chain the binary mounts, minus the parts that need a database: a caller on the
	// context is what Authenticate would have left there.
	mux := http.NewServeMux()
	mux.Handle("/v1/realtime", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-Test-User")
		role := r.Header.Get("X-Active-Role")
		device := r.Header.Get("X-Test-Device")
		caller := httpx.Caller{UserID: userID, FacilityID: g.facility.String(), ActiveRole: role, DeviceID: device}
		httpx.CallerForTest(caller, handler).ServeHTTP(w, r)
	}))
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

// client is one subscriber's socket.
//
// One goroutine reads it and everything else takes from a channel. A WebSocket connection
// has exactly one reader by construction (§5.4's reassembly is stateful), and a test that
// reads from two places at once produces protocol errors that look like server bugs.
type client struct {
	t      *testing.T
	conn   *ws.Conn
	in     chan realtime.Envelope
	closed chan error
}

func (g *gateway) connect(userID uuid.UUID, role auth.RoleCode) *client {
	g.t.Helper()
	return g.connectAs(userID, role, uuid.Nil)
}

func (g *gateway) connectAs(userID uuid.UUID, role auth.RoleCode, device uuid.UUID) *client {
	g.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	header := http.Header{}
	header.Set("X-Test-User", userID.String())
	header.Set("X-Active-Role", string(role))
	if device != uuid.Nil {
		header.Set("X-Test-Device", device.String())
	}
	conn, response, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(g.server.URL, "http")+"/v1/realtime",
		ws.DialOptions{Header: header, ReadTimeout: 30 * time.Second})
	if err != nil {
		g.t.Fatalf("dial: %v (%v)", err, response)
	}

	c := &client{t: g.t, conn: conn, in: make(chan realtime.Envelope, 512), closed: make(chan error, 1)}
	go c.pump()
	g.t.Cleanup(func() { _ = conn.Close(ws.StatusNormalClosure, "") })

	if welcome := c.next(); welcome.Type != "welcome" {
		g.t.Fatalf("the first envelope was %q", welcome.Type)
	}
	return c
}

// pump is the connection's single reader.
func (c *client) pump() {
	for {
		_, payload, err := c.conn.Read()
		if err != nil {
			c.closed <- err
			close(c.in)
			return
		}
		var envelope realtime.Envelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			c.closed <- err
			close(c.in)
			return
		}
		c.in <- envelope
	}
}

func (c *client) send(command realtime.Command) {
	c.t.Helper()
	payload, err := json.Marshal(command)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.conn.Write(ws.OpText, payload); err != nil {
		c.t.Fatal(err)
	}
}

// next reads one envelope, failing the test on a timeout rather than hanging.
func (c *client) next() realtime.Envelope {
	c.t.Helper()
	select {
	case envelope, ok := <-c.in:
		if !ok {
			c.t.Fatalf("the connection closed: %v", <-c.closed)
		}
		return envelope
	case <-time.After(5 * time.Second):
		c.t.Fatal("no envelope arrived")
		return realtime.Envelope{}
	}
}

// nextOfType reads until it sees the type it wants, skipping the acknowledgements.
func (c *client) nextOfType(want string) realtime.Envelope {
	c.t.Helper()
	for i := 0; i < 50; i++ {
		envelope := c.next()
		if envelope.Type == want {
			return envelope
		}
	}
	c.t.Fatalf("no %q envelope arrived", want)
	return realtime.Envelope{}
}

// ended asserts the connection closed, which is how an eviction and a lost authorisation
// are observed from the client's side.
func (c *client) ended(within time.Duration) {
	c.t.Helper()
	deadline := time.After(within)
	for {
		select {
		case _, ok := <-c.in:
			if !ok {
				return
			}
		case <-deadline:
			c.t.Fatal("the connection was not closed")
		}
	}
}

// silent asserts that nothing arrives within a short window — which is how "a subscriber
// never receives what their role may not see" is actually checked.
func (c *client) silent(d time.Duration) {
	c.t.Helper()
	select {
	case envelope, ok := <-c.in:
		if !ok {
			return // a closed connection delivers nothing, which is silence enough
		}
		c.t.Fatalf("a message arrived that should not have: %s %+v", envelope.Type, envelope.Message)
	case <-time.After(d):
	}
}

func (c *client) subscribe(topics ...realtime.Topic) realtime.Envelope {
	c.t.Helper()
	c.send(realtime.Command{Type: "subscribe", Topics: topics})
	return c.nextOfType("subscribed")
}

// publish is what the bridge does when a message arrives from Redis.
func (g *gateway) publish(m realtime.Message) int {
	g.t.Helper()
	if m.Seq == 0 {
		m.Seq = g.seq.Add(1)
	}
	if m.FacilityID == "" {
		m.FacilityID = g.facility.String()
	}
	if m.At.IsZero() {
		m.At = g.clock.Now()
	}
	if err := m.Validate(); err != nil {
		g.t.Fatalf("the test published an invalid message: %v", err)
	}
	return g.hub.Deliver(m)
}

// --- criterion 1: an update appears on subscribed clients ---

func TestAMessageReachesEverySubscriberAndNobodyElse(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	patient := uuid.New()
	watched, unwatched := realtime.PatientTopic(patient), realtime.PatientTopic(uuid.New())

	var clients []*client
	for i := 0; i < 3; i++ {
		id := uuid.New()
		resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
		c := g.connect(id, auth.RolePhysician)
		c.subscribe(watched)
		clients = append(clients, c)
	}
	// A fourth, watching a different patient.
	elsewhere := uuid.New()
	resolver.set(elsewhere, staffed(auth.RolePhysician, g.facility, nil))
	other := g.connect(elsewhere, auth.RolePhysician)
	other.subscribe(unwatched)

	started := time.Now()
	delivered := g.publish(realtime.Message{
		Topic: watched, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
		PatientID: patient.String(), Summary: map[string]any{"code": "HEIGHT"},
	})
	if delivered != 3 {
		t.Fatalf("delivered to %d sockets, want 3", delivered)
	}

	for i, c := range clients {
		envelope := c.nextOfType("message")
		if envelope.Message == nil || envelope.Message.Kind != "measurement.recorded" {
			t.Errorf("client %d got %+v", i, envelope)
		}
		if envelope.Message.PatientID != patient.String() {
			t.Errorf("client %d got another patient's message", i)
		}
	}
	// Criterion 1: under a second at this load. In practice it is microseconds; the budget
	// is what the plan asks for and what a regression would blow.
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("fan-out took %s", elapsed)
	}
	other.silent(200 * time.Millisecond)
}

// --- criterion 2: a subscriber never receives data their role cannot read ---

// The plan's own example: a nutritionist's socket must never receive a prescription event.
func TestANutritionistNeverReceivesAPrescription(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	nutrition := uuid.New() // the station the nutritionist is working

	nutritionistID, physicianID := uuid.New(), uuid.New()
	resolver.set(nutritionistID, staffed(auth.RoleNutritionist, g.facility, &nutrition))
	resolver.set(physicianID, staffed(auth.RolePhysician, g.facility, nil))

	nutritionist := g.connect(nutritionistID, auth.RoleNutritionist)
	physician := g.connect(physicianID, auth.RolePhysician)
	nutritionist.subscribe(topic)
	physician.subscribe(topic)

	// Both are watching the same patient, and a prescription is signed at that station.
	// Even there, the nutritionist does not hold prescription.read.
	delivered := g.publish(realtime.Message{
		Topic: topic, Kind: "prescription.signed", Requires: auth.PermPrescriptionRead,
		PatientID: patient.String(), Station: nutrition.String(), Sensitive: true,
	})
	if delivered != 1 {
		t.Fatalf("a prescription reached %d sockets, want only the physician's", delivered)
	}
	if got := physician.nextOfType("message"); got.Message.Kind != "prescription.signed" {
		t.Errorf("the physician got %+v", got.Message)
	}
	nutritionist.silent(200 * time.Millisecond)

	// And a measurement taken at their station reaches both, so the refusal above was the
	// permission and not the subscription quietly failing.
	if delivered := g.publish(realtime.Message{
		Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
		PatientID: patient.String(), Station: nutrition.String(),
	}); delivered != 2 {
		t.Fatalf("a measurement reached %d sockets, want 2", delivered)
	}
	if got := nutritionist.nextOfType("message"); got.Message.Kind != "measurement.recorded" {
		t.Errorf("the nutritionist got %+v", got.Message)
	}

	// The same measurement taken at *another* station reaches only the physician, whose
	// reach is the clinic. A station-scoped role sees the station it is working.
	if delivered := g.publish(realtime.Message{
		Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
		PatientID: patient.String(), Station: uuid.New().String(),
	}); delivered != 1 {
		t.Fatalf("a measurement from another station reached %d sockets, want 1", delivered)
	}
	nutritionist.silent(200 * time.Millisecond)
}

// A blinded role is refused a sensitive message whatever its permissions say — the same
// rule the serialiser applies to a response body (§4.4).
func TestABlindedRoleIsRefusedSensitiveMessages(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	pharmacy := uuid.New()
	pharmacistID := uuid.New()
	resolver.set(pharmacistID, staffed(auth.RolePharmacist, g.facility, &pharmacy))
	pharmacist := g.connect(pharmacistID, auth.RolePharmacist)
	pharmacist.subscribe(topic)

	// A pharmacist holds prescription.read, and is blinded to clinical interpretation.
	if delivered := g.publish(realtime.Message{
		Topic: topic, Kind: "prescription.dispensable", Requires: auth.PermPrescriptionRead,
		PatientID: patient.String(), Station: pharmacy.String(),
	}); delivered != 1 {
		t.Fatalf("a dispensable prescription reached %d sockets", delivered)
	}
	pharmacist.nextOfType("message")

	if delivered := g.publish(realtime.Message{
		Topic: topic, Kind: "diagnosis.recorded", Requires: auth.PermPrescriptionRead,
		PatientID: patient.String(), Station: pharmacy.String(), Sensitive: true,
	}); delivered != 0 {
		t.Fatalf("a blinded role received a sensitive message")
	}
	pharmacist.silent(200 * time.Millisecond)
}

// A subscriber in another facility receives nothing, checked before the permission is.
func TestAnotherFacilityReceivesNothing(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	id := uuid.New()
	resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
	c := g.connect(id, auth.RolePhysician)
	c.subscribe(topic)

	if delivered := g.publish(realtime.Message{
		Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
		PatientID: patient.String(), FacilityID: uuid.New().String(),
	}); delivered != 0 {
		t.Fatal("another facility's message was delivered")
	}
	c.silent(200 * time.Millisecond)
}

// Subscribing to a topic a role may not watch is refused, and named. A subscription that
// quietly delivered nothing would be the hardest kind of bug to find from the client's side
// — and an enumerable one, since the client could tell which users exist by watching.
func TestASubscriptionOutsideYourReachIsRefusedAndNamed(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	mine, theirs := uuid.New(), uuid.New()
	resolver.set(mine, staffed(auth.RolePhysician, g.facility, nil))
	c := g.connect(mine, auth.RolePhysician)

	c.send(realtime.Command{Type: "subscribe", Topics: []realtime.Topic{
		realtime.UserTopic(mine),
		realtime.UserTopic(theirs),
		realtime.Topic("wildcard:*"),
		realtime.Topic("patient:not-a-uuid"),
	}})
	subscribed := c.nextOfType("subscribed")
	if len(subscribed.Topics) != 1 || subscribed.Topics[0] != realtime.UserTopic(mine) {
		t.Errorf("subscribed to %v; only your own user topic is yours", subscribed.Topics)
	}
	refused := c.nextOfType("refused")
	if len(refused.Topics) != 3 {
		t.Errorf("refused %v, want the other user's topic and the two malformed ones", refused.Topics)
	}
	if refused.Error != "not_permitted" {
		t.Errorf("the refusal is %q", refused.Error)
	}
}

// A role revoked while a socket is open stops applying without waiting for a reconnect.
func TestARevokedRoleStopsReceivingWithoutReconnecting(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	id := uuid.New()
	resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
	c := g.connect(id, auth.RolePhysician)
	c.subscribe(topic)

	g.publish(realtime.Message{
		Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
		PatientID: patient.String(),
	})
	c.nextOfType("message")

	// The physician is now a nutritionist, and a prescription must stop arriving.
	resolver.set(id, staffed(auth.RoleNutritionist, g.facility, nil))
	deadline := time.Now().Add(3 * time.Second)
	for {
		if delivered := g.publish(realtime.Message{
			Topic: topic, Kind: "prescription.signed", Requires: auth.PermPrescriptionRead,
			PatientID: patient.String(),
		}); delivered == 0 {
			return // the re-resolution has landed
		}
		c.nextOfType("message")
		if time.Now().After(deadline) {
			t.Fatal("a revoked role kept receiving; the connection was never re-authorised")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// An account that loses its access entirely is closed rather than left open and empty.
func TestAnAccountThatLosesItsAccessIsDisconnected(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	id := uuid.New()
	resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
	c := g.connect(id, auth.RolePhysician)
	resolver.revoke(id)

	envelope := c.nextOfType("error")
	if envelope.Error != "reauthentication_failed" {
		t.Errorf("the connection was told %q", envelope.Error)
	}
}

// --- criterion 3: reconnection resumes without loss and without duplicates ---

func TestReconnectionResumesFromTheCursor(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	id := uuid.New()
	resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))

	c := g.connect(id, auth.RolePhysician)
	c.subscribe(topic)
	for i := 0; i < 3; i++ {
		g.publish(realtime.Message{
			Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
			PatientID: patient.String(),
		})
	}
	var last int64
	for i := 0; i < 3; i++ {
		last = c.nextOfType("message").Message.Seq
	}

	// The client asks where it is. The gateway does not replay — it has no store of past
	// messages and inventing one would be a second copy of the ledger — so what `resume`
	// returns is precisely what the client must fetch over HTTP.
	c.send(realtime.Command{Type: "resume", Since: last})
	resumed := c.nextOfType("resumed")
	if resumed.Cursor != last {
		t.Errorf("cursor = %d, the client last saw %d", resumed.Cursor, last)
	}
	if !strings.Contains(resumed.Detail, "does not replay") {
		t.Errorf("the resume reply must say what the client has to do: %q", resumed.Detail)
	}

	// Messages published while the client is away are simply missed; the cursor on the new
	// connection tells it so, and criterion 4 is that this is safe because the pull is the
	// truth.
	_ = c.conn.Close(ws.StatusNormalClosure, "")
	for i := 0; i < 5; i++ {
		g.publish(realtime.Message{
			Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
			PatientID: patient.String(),
		})
	}
	again := g.connect(id, auth.RolePhysician)
	again.subscribe(topic)
	g.publish(realtime.Message{
		Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
		PatientID: patient.String(),
	})
	next := again.nextOfType("message")
	if next.Message.Seq <= last+5 {
		t.Errorf("the new connection got %d, which is not after the gap", next.Message.Seq)
	}
	// No duplicate of anything the first connection already saw.
	if next.Message.Seq <= last {
		t.Errorf("a message the client had already seen was delivered again")
	}
}

// --- ordering ---

func TestMessagesArriveInOrderOnATopic(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	id := uuid.New()
	resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
	c := g.connect(id, auth.RolePhysician)
	c.subscribe(topic)

	const count = 100
	for i := 0; i < count; i++ {
		g.publish(realtime.Message{
			Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
			PatientID: patient.String(), Summary: map[string]any{"n": i},
		})
	}
	var previous int64
	for i := 0; i < count; i++ {
		envelope := c.nextOfType("message")
		if envelope.Message.Seq <= previous {
			t.Fatalf("message %d arrived with seq %d after %d", i, envelope.Message.Seq, previous)
		}
		previous = envelope.Message.Seq
	}
}

// --- backpressure ---

// A client that stops reading must not be able to stall the fan-out. It is dropped from,
// counted, and told — and nothing is lost, because the events are in the ledger.
func TestASlowClientIsDroppedFromRatherThanBlockingEveryoneElse(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{SendQueue: 4})

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	slowID, fastID := uuid.New(), uuid.New()
	resolver.set(slowID, staffed(auth.RolePhysician, g.facility, nil))
	resolver.set(fastID, staffed(auth.RolePhysician, g.facility, nil))

	slow := g.connect(slowID, auth.RolePhysician)
	fast := g.connect(fastID, auth.RolePhysician)
	slow.subscribe(topic)
	fast.subscribe(topic)

	// The slow client never reads. Publishing must not block on it.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			g.publish(realtime.Message{
				Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
				PatientID: patient.String(),
			})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("one slow reader stalled the fan-out")
	}

	// The fast client got messages, and eventually one carrying a dropped count for the
	// slow one is not its business — but the slow one's own next envelope says so.
	if got := fast.nextOfType("message"); got.Message == nil {
		t.Error("the fast client received nothing")
	}
	for i := 0; i < 400; i++ {
		if slow.next().Dropped > 0 {
			return // told, which is its instruction to reconcile by pull
		}
	}
	t.Fatal("the slow client was never told it had missed anything")
}

// --- connection limits ---

func TestOneProcessRefusesMoreConnectionsThanItsCeiling(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{Total: 2, PerUser: 10})

	for i := 0; i < 2; i++ {
		id := uuid.New()
		resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
		g.connect(id, auth.RolePhysician)
	}
	id := uuid.New()
	resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("X-Test-User", id.String())
	header.Set("X-Active-Role", string(auth.RolePhysician))
	_, response, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(g.server.URL, "http")+"/v1/realtime",
		ws.DialOptions{Header: header})
	if err == nil {
		t.Fatal("the connection past the ceiling was accepted")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v; a full process should say so and invite a retry", response)
	}
	if response.Header.Get("Retry-After") == "" {
		t.Error("no Retry-After on a refusal that is temporary")
	}
}

func TestTheOldestConnectionGoesWhenOnePersonOpensTooMany(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{PerUser: 2})

	id := uuid.New()
	resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
	first := g.connect(id, auth.RolePhysician)
	g.clock.Advance(time.Second)
	g.connect(id, auth.RolePhysician)
	g.clock.Advance(time.Second)
	g.connect(id, auth.RolePhysician)

	// The oldest was closed: closing the newest instead would mean a tab left open on
	// Friday stops one being opened on Monday.
	first.ended(3 * time.Second)
	if got := g.hub.Count(); got != 2 {
		t.Errorf("%d connections are open, want the limit of 2", got)
	}
}

// --- scale ---

// Two hundred concurrent connections, as the plan asks. What is being checked is not
// throughput but that the hub's locking is right: every socket subscribed, every socket
// received, nothing deadlocked.
func TestTwoHundredConcurrentConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{Total: 500, PerUser: 4})

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)

	const count = 200
	clients := make([]*client, count)
	var wg sync.WaitGroup
	for i := range count {
		id := uuid.New()
		resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
		wg.Add(1)
		go func() {
			defer wg.Done()
			clients[i] = g.connect(id, auth.RolePhysician)
			clients[i].subscribe(topic)
		}()
	}
	wg.Wait()

	if open := g.hub.Count(); open != count {
		t.Fatalf("%d connections are open, want %d", open, count)
	}

	started := time.Now()
	delivered := g.publish(realtime.Message{
		Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
		PatientID: patient.String(),
	})
	if delivered != count {
		t.Fatalf("delivered to %d of %d", delivered, count)
	}

	var received atomic.Int64
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if envelope := c.nextOfType("message"); envelope.Message != nil {
				received.Add(1)
			}
		}()
	}
	wg.Wait()
	if received.Load() != count {
		t.Errorf("%d of %d sockets received it", received.Load(), count)
	}
	// Criterion 1 at the plan's stated load.
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("fan-out to %d connections took %s, over the one-second budget", count, elapsed)
	}
}

// --- the protocol's own replies ---

func TestTheGatewayAnswersItsCommands(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	id := uuid.New()
	resolver.set(id, staffed(auth.RolePhysician, g.facility, nil))
	c := g.connect(id, auth.RolePhysician)
	topic := realtime.PatientTopic(uuid.New())

	c.subscribe(topic)
	c.send(realtime.Command{Type: "unsubscribe", Topics: []realtime.Topic{topic}})
	if got := c.nextOfType("unsubscribed"); len(got.Topics) != 1 || got.Topics[0] != topic {
		t.Errorf("unsubscribe answered %v", got.Topics)
	}
	// And it really is unsubscribed.
	if delivered := g.publish(realtime.Message{
		Topic: topic, Kind: "measurement.recorded", Requires: auth.PermObservationReadValues,
	}); delivered != 0 {
		t.Error("a message arrived on an unsubscribed topic")
	}

	c.send(realtime.Command{Type: "ping"})
	c.nextOfType("pong")

	c.send(realtime.Command{Type: "explode"})
	if got := c.nextOfType("error"); got.Error != "unknown_command" {
		t.Errorf("an unknown command answered %q", got.Error)
	}

	if err := c.conn.Write(ws.OpText, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if got := c.nextOfType("error"); got.Error != "malformed_command" {
		t.Errorf("malformed JSON answered %q", got.Error)
	}
}

func TestAnUnauthenticatedRequestNeverReachesTheGateway(t *testing.T) {
	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// No X-Test-User: the caller has no identity the resolver knows.
	header := http.Header{}
	header.Set("X-Test-User", uuid.New().String())
	_, response, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(g.server.URL, "http")+"/v1/realtime",
		ws.DialOptions{Header: header})
	if err == nil {
		t.Fatal("an unknown user opened a connection")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v", response)
	}
}
