package main

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/ai"
	"github.com/AmlanWTK/DTHCMS/backend/internal/allergy"
	"github.com/AmlanWTK/DTHCMS/backend/internal/synthesis"
)

// CP82's prescribing agent, wired.
//
// Two adapters, and both exist for the reason every bridge in this directory exists:
// `architecture.json` gives `prescription` no access to `synthesis` and none to `allergy`, and it
// should not. A prescribing module that could read a patient's allergy record directly would be a
// second answer to a question CP54 settled in one place, and one that could assemble its own
// clinical context would be a second answer to what the model is shown.
//
// What crosses is deliberately narrow: one word about allergy status, and one already-assembled
// context with the identifiers to strike out of it. Neither adapter decides anything.

// prescribingBriefingBridge hands CP71's assembled context and the gateway's subject to CP82.
//
// It calls `synthesis.Service.Briefing`, which **assembles rather than reads a stored summary**.
// See that method for why: a prescription is written minutes after the last station finished, and
// the summary the physician read on arrival may predate a measurement taken since.
type prescribingBriefingBridge struct{ synthesis *synthesis.Service }

func (b prescribingBriefingBridge) PrescribingBriefing(ctx context.Context,
	facility, patient, visit uuid.UUID) (ai.Subject, map[string]any, error) {

	assembled, err := b.synthesis.Briefing(ctx, visit, facility)
	if err != nil {
		return ai.Subject{}, nil, err
	}
	subject, err := b.synthesis.Subject(ctx, patient, facility, assembled.Demographics)
	if err != nil {
		return ai.Subject{}, nil, err
	}
	// Through JSON rather than by hand, for the reason `synthesis.payloadOf` gives: what the model
	// is shown and what `core.ai_synthesis.context` would store are then the same bytes by
	// construction. A hand-written projection would be a second thing to keep in step, and the day
	// it drifted the grounding check would be comparing the model's answer against a context the
	// model never saw.
	encoded, err := json.Marshal(assembled)
	if err != nil {
		return ai.Subject{}, nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return ai.Subject{}, nil, err
	}
	return subject, payload, nil
}

// allergyGateBridge answers CP54's question for CP82's hard stop.
//
// `allergy.Store.Status` calls `core.allergy_status()` — the same function the trigger on the queue
// calls — so "does this patient have allergy status" is answered by one function for the gate that
// stops a patient advancing and for the gate that stops a machine proposing. Two implementations
// would be two chances for one of them to say yes when the other said no, and the one that said yes
// would be the one nobody noticed.
//
// The facility is accepted and not used: allergy status is a property of a patient, and
// `core.allergy_status` takes only the patient id. It is in the signature because the interface on
// the prescribing side is written for a facility-scoped world and a future store that needs it
// should not force a signature change through three files.
type allergyGateBridge struct{ store *allergy.Store }

func (b allergyGateBridge) AllergyStatus(ctx context.Context, _, patient uuid.UUID) (string, error) {
	return b.store.Status(ctx, patient)
}
