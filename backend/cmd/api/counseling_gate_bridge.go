package main

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
	"github.com/AmlanWTK/DTHCMS/backend/internal/visit"
)

// counselingGateBridge is the counselling checkpoint, as the queue sees it (CP57).
//
// `visit` may not import `counseling`, and the reason is the one every bridge here exists for: a
// queue that knew about checklists would grow a second opinion about what counselling means.
// What it needs to know is "may this patient go to that station, and if not, what do I tell the
// operator" — which is exactly what `visit.Gate` asks and no more.
//
// # Why this decides which stations the gate applies to
//
// §5.5 gates step 9, the consultation. The database trigger reads the station's own
// `sequence_hint` so a clinic that reorders its floor does not silently move the checkpoint, and
// this does the same by asking about the station code the queue was given. The two have to agree,
// and they agree because the trigger is the enforcement and this is the message: a disagreement
// where this says yes shows up as a 500 from the trigger, which is loud. A disagreement the other
// way is a patient held with an explanation, which is safe.
//
// # Why the message is built here
//
// The words belong to the checklist's version — the item text is `text_en`/`text_bn` on a frozen
// row — and `visit` has no way to read them. So the sentence is assembled where the items are,
// in both languages, and the queue only carries it.
type counselingGateBridge struct {
	store *counseling.Store
	// gatedFrom is the station the checkpoint sits in front of. A field rather than a constant
	// so a test can gate a different station without a migration.
	gatedFrom string
}

var _ visit.Gate = (*counselingGateBridge)(nil)

func (b *counselingGateBridge) Check(ctx context.Context, id uuid.UUID,
	station string) (visit.GateDecision, error) {

	gatedFrom := b.gatedFrom
	if gatedFrom == "" {
		gatedFrom = "STN_CONSULTATION"
	}
	// Only the station the checkpoint guards. Gating the counselling rooms themselves would be a
	// checkpoint refusing the patient at the door of the room where it would be satisfied.
	if station != gatedFrom {
		return visit.GateDecision{}, nil
	}

	status, err := b.store.Gate(ctx, id)
	if err != nil {
		return visit.GateDecision{}, err
	}

	decision := visit.GateDecision{
		Applies:    true,
		Name:       counseling.GateName,
		Allowed:    !status.Blocked,
		Overridden: status.Overridden,
	}
	if len(status.Missing) == 0 {
		return decision, nil
	}

	codes := make([]string, 0, len(status.Missing))
	english := make([]string, 0, len(status.Missing))
	bangla := make([]string, 0, len(status.Missing))
	for _, item := range status.Missing {
		codes = append(codes, item.ItemCode)
		english = append(english, item.TextEN)
		bangla = append(bangla, item.TextBN)
	}
	decision.Missing = codes

	// Named in full rather than counted. "Two items are missing" sends an operator back to look
	// for them; "insulin injection sites and technique" sends them to the insulin corner.
	decision.MissingEN = "Counselling is not finished: " + strings.Join(english, "; ") + "."
	decision.MissingBN = "কাউন্সেলিং শেষ হয়নি: " + strings.Join(bangla, "; ") + "।"
	return decision, nil
}
