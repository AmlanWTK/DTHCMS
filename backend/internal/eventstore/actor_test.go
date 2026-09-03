package eventstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/httpx"
)

// CP24 criterion 3: client-supplied identity fields are ignored.
//
// The test is written from the outside, in package eventstore_test, because that is the
// position every caller is in: no access to the unexported fields, one legitimate way to
// obtain an Actor, and a request body that may say whatever it likes.

// clientBody is what a hostile or buggy client sends: its own idea of who it is.
const clientBody = `{
  "value": 150,
  "user_id":     "00000000-0000-0000-0000-0000000000ff",
  "device_id":   "00000000-0000-0000-0000-0000000000fe",
  "role":        "SUPER_ADMIN",
  "facility_id": "00000000-0000-0000-0000-0000000000fd",
  "station":     "PHARMACY",
  "actor":       {"user_id": "00000000-0000-0000-0000-0000000000fc", "role": "SUPER_ADMIN"}
}`

func TestTheActorComesFromTheSessionAndNeverFromTheBody(t *testing.T) {
	verified := httpx.Principal{
		UserID:     "0190a8f2-0000-7000-8000-000000000001",
		FacilityID: "0190a8f2-0000-7000-8000-000000000003",
		DeviceID:   "0190a8f2-0000-7000-8000-000000000002",
		Role:       "ANTHROPOMETRY",
		Station:    "0190a8f2-0000-7000-8000-000000000004",
		Code:       "DTHC-0042",
	}
	ctx := httpx.WithPrincipal(context.Background(), verified)

	// A handler decodes the body — which claims a different everything — and then asks for
	// the actor. There is no parameter it could pass the body's claims through.
	var claimed map[string]any
	if err := json.Unmarshal([]byte(clientBody), &claimed); err != nil {
		t.Fatal(err)
	}
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got := actor.UserID().String(); got != verified.UserID {
		t.Errorf("user = %s, want the session's %s", got, verified.UserID)
	}
	if got := actor.DeviceID().String(); got != verified.DeviceID {
		t.Errorf("device = %s, want the session's %s", got, verified.DeviceID)
	}
	if actor.Role() != "ANTHROPOMETRY" {
		t.Errorf("role = %s, want the role the engine confirmed", actor.Role())
	}
	if got := actor.FacilityID().String(); got != verified.FacilityID {
		t.Errorf("facility = %s, want the session's %s", got, verified.FacilityID)
	}
	if actor.Station() != verified.Station {
		t.Errorf("station = %s, want the session's %s", actor.Station(), verified.Station)
	}
	// Nothing the body said survived anywhere.
	for _, forged := range []string{"00000000-0000-0000-0000-0000000000ff", "SUPER_ADMIN", "PHARMACY"} {
		line := actor.UserID().String() + actor.DeviceID().String() + actor.Role() + actor.Station() + actor.FacilityID().String()
		if strings.Contains(line, forged) {
			t.Errorf("the body's %q reached the actor", forged)
		}
	}
}

// The other half of criterion 3: there is no way in at all without a verified principal,
// and no half-verified way in either. Each of these is a refusal, not a blank field.
func TestAnActorCannotBeBuiltWithoutAVerifiedPrincipal(t *testing.T) {
	for name, tc := range map[string]struct {
		ctx  context.Context
		want error
	}{
		"no principal at all": {context.Background(), eventstore.ErrNoPrincipal},
		"a principal whose user id is nonsense": {httpx.WithPrincipal(context.Background(), httpx.Principal{
			UserID: "not-a-uuid", FacilityID: uuid.New().String(), DeviceID: uuid.New().String(), Role: "PHYSICIAN",
		}), eventstore.ErrNoPrincipal},
		"a browser session with no device": {httpx.WithPrincipal(context.Background(), httpx.Principal{
			UserID: uuid.New().String(), FacilityID: uuid.New().String(), Role: "PHYSICIAN",
		}), eventstore.ErrNoDevice},
		"no confirmed role": {httpx.WithPrincipal(context.Background(), httpx.Principal{
			UserID: uuid.New().String(), FacilityID: uuid.New().String(), DeviceID: uuid.New().String(),
		}), eventstore.ErrNoRole},
	} {
		t.Run(name, func(t *testing.T) {
			actor, err := eventstore.ActorFrom(tc.ctx)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !actor.Zero() {
				t.Errorf("a refused actor is not zero: %s", actor.UserID())
			}
		})
	}
}

// And the envelope refuses what the actor refused: an unattributed event never reaches the
// table, whatever the caller does with the error.
func TestAnUnattributedEnvelopeIsRefusedBeforeTheDatabase(t *testing.T) {
	visit := uuid.New()
	e := eventstore.Envelope{
		EventID: uuid.Must(uuid.NewV7()), AggregateType: "VISIT", AggregateID: visit,
		EventType: "HEIGHT_RECORDED", EventVersion: 1, OccurredAt: nowForTest(),
		Source: eventstore.SourceWeb, Payload: json.RawMessage(`{"code":"HEIGHT","value":150,"unit":"cm"}`),
	}
	err := e.Validate()
	if !errors.Is(err, eventstore.ErrIncomplete) {
		t.Fatalf("err = %v, want ErrIncomplete", err)
	}
	for _, field := range []string{"actor.user_id", "actor.device_id", "actor.role", "actor.facility_id"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("%s is not named in %q", field, err)
		}
	}
}

func nowForTest() time.Time { return time.Date(2026, 9, 3, 4, 42, 0, 0, time.UTC) }
