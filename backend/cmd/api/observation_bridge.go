package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

// observationBridge tells an open screen that a value landed (CP73 criterion 4).
//
// `clinical` may not import `realtime` and `realtime` may not import `clinical`, so the
// translation lives here beside its siblings.
//
// # Why this did not exist before CP73
//
// Every earlier publisher carries an *exception*: an alert, a correction request, a
// counselling tick, a retraining flag. §8's snapshot panel is the first surface whose whole
// content is other people's ordinary work — the blood pressure station 2 is typing while the
// physician has the patient's dashboard open — and *"values update in real time without a
// refresh"* is the checkpoint's fourth acceptance criterion. A dashboard fed only by the
// existing publishers would come alive when something went wrong and sit frozen through a
// morning that went well, which is exactly backwards.
//
// # What is on the message, and what is deliberately not
//
// The **codes**, and never the values. CP27's rule is that a realtime message carries a
// notification and not a record: a client that wrote a value into its cache from a socket
// would have two paths producing what the screen shows, and on the day they disagree a
// clinician reads a number that no endpoint returned and no log explains. The codes are
// enough for a screen to decide whether it cares, and the re-read goes through the same
// authorisation and the same field-level redaction as every other read.
//
// A code is not a diagnosis, so the message is not marked sensitive — but it is guarded by
// `observation.read.values`, which is the permission that would let the subscriber read the
// value itself. A message naming `HBA1C` on a patient's topic tells a subscriber that
// somebody measured an HbA1c, and that is a clinical fact about the patient even though the
// number is absent.
//
// # Why one message per write and not one per value
//
// A form of six measurements entered at anthropometry is one act by one person. Six messages
// would be six invalidations of one query key and six refetches of one screen — the refetch
// storm CP27's own notes warn about, produced by the surface it was warning on behalf of.
type observationBridge struct {
	publisher realtime.Publisher
	clock     interface{ Now() time.Time }
	logger    *slog.Logger
}

var _ clinical.ValueNotifier = (*observationBridge)(nil)

func (b *observationBridge) ValuesRecorded(ctx context.Context, patientID uuid.UUID,
	visitID *uuid.UUID, codes []string) {

	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		// No actor on the context means this was not written through the request path. There
		// is nothing to publish *as*, and inventing a facility would put a message on a topic
		// belonging to a clinic that did not write it.
		return
	}

	message := realtime.Message{
		Topic:      realtime.PatientTopic(patientID),
		Kind:       "measurement.recorded",
		PatientID:  patientID.String(),
		FacilityID: actor.FacilityID().String(),
		// The permission that would let this subscriber read the value. A subscriber who may
		// not read observations has no business being told which ones exist.
		Requires: clinical.PermObservationRead,
		// Not sensitive: a code is not a diagnosis, and marking it so would hide the update
		// from the junior doctor and the station roles who are entitled to the numbers
		// themselves. The guard above is the real control.
		Sensitive: false,
		At:        b.clock.Now().UTC(),
		Summary:   map[string]any{"codes": codes},
	}
	if visitID != nil {
		message.VisitID = visitID.String()
	}

	if err := b.publisher.Publish(ctx, message); err != nil {
		// Warned, never fatal, and never returned: the value is stored and the pull is the
		// truth. A screen that missed this is one refresh behind, which is the state it would
		// have been in permanently before this bridge existed.
		b.logger.WarnContext(ctx, "an open screen was not told about a recorded value",
			"patient_id", patientID.String(), "error", err.Error())
	}
}
