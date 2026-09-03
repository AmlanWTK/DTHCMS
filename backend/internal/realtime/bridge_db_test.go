package realtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/testsupport"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

// The Redis bridge against a real Redis.
//
// The property under test is the one the clinic actually depends on: a measurement recorded
// on the instance the nurse's tablet is talking to reaches the screen connected to a
// different instance. Without it the gateway is worse than nothing, because it is
// intermittently right.

func TestAMessagePublishedOnOneInstanceReachesAnother(t *testing.T) {
	cache := testsupport.Redis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	// Two gateways: two processes, as far as anything below the sockets is concerned.
	first := newGateway(t, resolver, realtime.Limits{})
	second := newGateway(t, resolver, realtime.Limits{})
	second.facility = first.facility

	for _, g := range []*gateway{first, second} {
		bridge := realtime.NewBridge(cache.Client, g.hub, testLogger())
		go func() { _ = bridge.Run(ctx) }()
	}

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	idA, idB := uuid.New(), uuid.New()
	resolver.set(idA, staffed(auth.RolePhysician, first.facility, nil))
	resolver.set(idB, staffed(auth.RolePhysician, first.facility, nil))

	onFirst := first.connect(idA, auth.RolePhysician)
	onSecond := second.connect(idB, auth.RolePhysician)
	onFirst.subscribe(topic)
	onSecond.subscribe(topic)

	// Give both bridges a moment to be subscribed. Redis pub/sub delivers nothing to a
	// subscriber that was not there yet, which is the one thing about it that a test has
	// to accommodate and production does not (a gateway subscribes before it serves).
	time.Sleep(250 * time.Millisecond)

	publisher := realtime.NewPublisher(cache.Client, testLogger())
	message := realtime.Message{
		Seq: 42, Topic: topic, Kind: "measurement.recorded",
		Requires: auth.PermObservationReadValues, PatientID: patient.String(),
		FacilityID: first.facility.String(), At: first.clock.Now(),
	}
	if err := publisher.Publish(ctx, message); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]*client{"the publishing instance": onFirst, "the other instance": onSecond} {
		envelope := c.nextOfType("message")
		if envelope.Message == nil || envelope.Message.Seq != 42 {
			t.Errorf("%s received %+v", name, envelope.Message)
		}
		if envelope.Message.PatientID != patient.String() {
			t.Errorf("%s received the wrong patient", name)
		}
	}
}

// The filtering facts cross Redis. If they did not, the receiving instance would have no
// permission to check and would either deliver everything or nothing.
func TestTheFilteringFactsSurviveTheJourney(t *testing.T) {
	cache := testsupport.Redis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resolver := &fixedResolver{subjects: map[uuid.UUID]rbac.Subject{}, fail: map[uuid.UUID]bool{}}
	g := newGateway(t, resolver, realtime.Limits{})
	go func() { _ = realtime.NewBridge(cache.Client, g.hub, testLogger()).Run(ctx) }()

	patient := uuid.New()
	topic := realtime.PatientTopic(patient)
	pharmacy := uuid.New()
	pharmacistID := uuid.New()
	resolver.set(pharmacistID, staffed(auth.RolePharmacist, g.facility, &pharmacy))
	pharmacist := g.connect(pharmacistID, auth.RolePharmacist)
	pharmacist.subscribe(topic)
	time.Sleep(250 * time.Millisecond)

	publisher := realtime.NewPublisher(cache.Client, testLogger())
	// Sensitive, and a blinded role: refused on the far side, which is only possible if
	// `sensitive` crossed the wire.
	if err := publisher.Publish(ctx, realtime.Message{
		Seq: 1, Topic: topic, Kind: "diagnosis.recorded", Requires: auth.PermPrescriptionRead,
		PatientID: patient.String(), FacilityID: g.facility.String(),
		Station: pharmacy.String(), Sensitive: true, At: g.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// And one that should arrive, so the silence above is the filter and not the bridge.
	if err := publisher.Publish(ctx, realtime.Message{
		Seq: 2, Topic: topic, Kind: "prescription.dispensable", Requires: auth.PermPrescriptionRead,
		PatientID: patient.String(), FacilityID: g.facility.String(),
		Station: pharmacy.String(), At: g.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	envelope := pharmacist.nextOfType("message")
	if envelope.Message.Seq != 2 {
		t.Fatalf("the pharmacist received seq %d; the sensitive message crossed the filter", envelope.Message.Seq)
	}
	pharmacist.silent(200 * time.Millisecond)
}

// A publisher refuses a message that cannot be filtered on arrival. Publishing one would be
// worse than not publishing at all: the receiving instance would have to guess.
func TestAnIncompleteMessageIsNotPublished(t *testing.T) {
	cache := testsupport.Redis(t)
	publisher := realtime.NewPublisher(cache.Client, testLogger())
	ctx := context.Background()

	for name, m := range map[string]realtime.Message{
		"no sequence":   {Topic: realtime.QueueTopic(uuid.New()), Kind: "queue.changed", Requires: "x", FacilityID: uuid.New().String()},
		"no topic":      {Seq: 1, Kind: "queue.changed", Requires: "x", FacilityID: uuid.New().String()},
		"a bad topic":   {Seq: 1, Topic: "everything:*", Kind: "queue.changed", Requires: "x", FacilityID: uuid.New().String()},
		"no kind":       {Seq: 1, Topic: realtime.QueueTopic(uuid.New()), Requires: "x", FacilityID: uuid.New().String()},
		"no permission": {Seq: 1, Topic: realtime.QueueTopic(uuid.New()), Kind: "queue.changed", FacilityID: uuid.New().String()},
		"no facility":   {Seq: 1, Topic: realtime.QueueTopic(uuid.New()), Kind: "queue.changed", Requires: "x"},
	} {
		if err := publisher.Publish(ctx, m); err == nil {
			t.Errorf("a message with %s was published", name)
		}
	}
}
