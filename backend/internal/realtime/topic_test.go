package realtime_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
	"github.com/AmlanWTK/DTHCMS/backend/internal/rbac"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

// Topics, and the two facts about them that matter: the namespace is closed, and a topic is
// not a permission.

func TestTheTopicNamespaceIsClosed(t *testing.T) {
	id := uuid.New()
	for _, topic := range []realtime.Topic{
		realtime.PatientTopic(id), realtime.StationTopic(id),
		realtime.QueueTopic(id), realtime.UserTopic(id),
	} {
		if err := topic.Validate(); err != nil {
			t.Errorf("%s: %v", topic, err)
		}
		if got, ok := topic.ID(); !ok || got != id {
			t.Errorf("%s does not carry its id", topic)
		}
	}

	for _, topic := range []realtime.Topic{
		"", "patient", "patient:", realtime.Topic(":" + id.String()),
		"patient:*", "patient:not-a-uuid", realtime.Topic("everything:" + id.String()),
		// the kind is lower-case; a near miss is still a miss
		realtime.Topic("PATIENT:" + id.String()),
	} {
		if err := topic.Validate(); err == nil {
			t.Errorf("%q was accepted as a topic", topic)
		}
	}
}

func TestATopicNamesItsKind(t *testing.T) {
	id := uuid.New()
	if got := realtime.PatientTopic(id).Kind(); got != realtime.TopicPatient {
		t.Errorf("kind = %q", got)
	}
	if got := realtime.Topic("nonsense").Kind(); got != "" {
		t.Errorf("a malformed topic has kind %q", got)
	}
}

// The subscription check names a permission by string, because realtime may not import the
// identity module. A string is only as good as the thing that keeps it true.
func TestTheSubscriptionPermissionIsInTheCatalogue(t *testing.T) {
	// The permission filter.go asks for, spelled out here so that renaming it in the
	// catalogue without renaming it there fails a test rather than silently refusing every
	// subscription in the clinic.
	const asked = "patient.read.demographics"
	if auth.PermPatientReadDemographics != asked {
		t.Fatalf("the catalogue calls it %q; realtime/filter.go asks for %q",
			auth.PermPatientReadDemographics, asked)
	}
	// And it must be a permission the engine knows, or every subscription is refused.
	subject := rbac.Subject{
		UserID: uuid.New(), FacilityID: uuid.New(),
		Roles: []auth.RoleCode{auth.RolePhysician}, ActiveRole: auth.RolePhysician,
	}
	if !rbac.Holds(subject, asked) {
		t.Errorf("a physician does not hold %q; no clinical role could subscribe", asked)
	}
	if rbac.Holds(subject, "not.a.permission") {
		t.Error("Holds accepted a permission that is not in the catalogue")
	}
}

// Holds is scope-free by design, and that is exactly why it must not be used for delivery.
// The test says so out loud, because the next person to reach for it will be deciding
// whether to.
func TestHoldsIgnoresScopeAndCanDoesNot(t *testing.T) {
	station := uuid.New()
	facility := uuid.New()
	nutritionist := rbac.Subject{
		UserID: uuid.New(), FacilityID: facility, StationID: &station,
		Roles: []auth.RoleCode{auth.RoleNutritionist}, ActiveRole: auth.RoleNutritionist,
	}
	if !rbac.Holds(nutritionist, auth.PermPatientReadDemographics) {
		t.Fatal("a nutritionist does not hold patient.read.demographics")
	}
	// The same permission, on a resource at another station, is refused.
	elsewhere := uuid.New()
	if rbac.Can(nutritionist, auth.PermPatientReadDemographics, rbac.Resource{
		Kind: "patient", FacilityID: facility, StationID: &elsewhere,
	}).Allowed {
		t.Error("a station-scoped role reached another station's resource")
	}
	if !rbac.Can(nutritionist, auth.PermPatientReadDemographics, rbac.Resource{
		Kind: "patient", FacilityID: facility, StationID: &station,
	}).Allowed {
		t.Error("a station-scoped role could not reach its own station")
	}
}

func TestAMessageIsRefusedWhenItCouldNotBeFiltered(t *testing.T) {
	complete := realtime.Message{
		Seq: 1, Topic: realtime.QueueTopic(uuid.New()), Kind: "queue.changed",
		Requires: auth.PermPatientReadDemographics, FacilityID: uuid.New().String(),
	}
	if err := complete.Validate(); err != nil {
		t.Fatalf("a complete message was refused: %v", err)
	}
	for name, mutate := range map[string]func(*realtime.Message){
		"seq":         func(m *realtime.Message) { m.Seq = 0 },
		"kind":        func(m *realtime.Message) { m.Kind = "" },
		"requires":    func(m *realtime.Message) { m.Requires = "" },
		"facility_id": func(m *realtime.Message) { m.FacilityID = "" },
		"topic":       func(m *realtime.Message) { m.Topic = "" },
	} {
		m := complete
		mutate(&m)
		err := m.Validate()
		if err == nil {
			t.Errorf("a message with no %s was accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), name) && name != "topic" {
			t.Errorf("the refusal for %s does not name it: %v", name, err)
		}
	}
}
