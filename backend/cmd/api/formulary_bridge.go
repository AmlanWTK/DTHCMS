package main

import (
	"context"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
)

// A medicine's price changing, on its way to the security audit trail (CP75).
//
// `formulary` may not import `audit` (architecture.json), and the reason is the one every bridge
// in this binary exists for: a module able to write audit entries directly grows a second,
// differently-shaped way of describing what happened, and the trail's value is that there is
// exactly one. The translation between "the pharmacist put Comet 500 mg at 5.50" and an audit row
// belongs here, in the only place allowed to know both.
//
// # Why a price change is in the *security* trail rather than only in the price history
//
// The price history already records what the price became and who recorded it — that is the
// table, and it is the answer to "what did this cost in March". This is the answer to a different
// question: **who has been changing prices**. That question is asked by whoever reviews access,
// beside the role grants and the credential resets, and it is asked about a person rather than
// about a medicine. A trail that could only be read one medicine at a time could not answer it.
//
// The criterion the checkpoint states is "price changes audited with the actor", and it is
// satisfied twice over on purpose: `core.medication_price.recorded_by` is a column with a CHECK
// constraint and a registered invariant behind it, and this is the hash-chained trail. The first
// cannot be missing; the second cannot be quietly edited.
type formularyAuditBridge struct {
	recorder *audit.Recorder
}

var _ formulary.Auditor = (*formularyAuditBridge)(nil)

// PriceChanged records one price superseding another.
//
// The sentence names the old price as well as the new one, because "who raised the price of
// insulin" is a question about a difference and a trail that only carried the new number would
// make it unanswerable without a second lookup per row.
func (b *formularyAuditBridge) PriceChanged(ctx context.Context, change formulary.PriceChange) error {
	actor := change.ActorID
	details := map[string]any{
		"medicine":     change.TradeName + " " + change.Strength,
		"price":        change.Amount.String(),
		"from":         change.From,
		"verification": change.Verification,
		"origin":       change.Origin,
	}
	kind := "formulary.price_set"
	if change.HadPrevious {
		kind = "formulary.price_changed"
		details["previous"] = change.Previous.String()
	}
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       kind,
		FacilityID: change.FacilityID,
		ActorID:    &actor,
		ActorCode:  change.ActorCode,
		ActorRole:  change.ActorRole,
		Details:    details,
	})
	return err
}

// ReviewOpened records the month's cycle being opened by the scheduled job.
//
// **No actor**, and that is honest: nobody opened it, a clock did. The recorder accepts an entry
// with no actor and the sentence renders it without a name; inventing a system user to put on the
// line would make the trail say a person did something they did not do.
func (b *formularyAuditBridge) ReviewOpened(ctx context.Context, opened formulary.ReviewOpened) error {
	details := map[string]any{
		"month":      opened.PeriodMonth,
		"owner_role": opened.OwnerRole,
		"products":   opened.Products,
		"unverified": opened.Unverified,
	}
	entry := audit.Entry{
		Kind:       "formulary.review_opened",
		FacilityID: opened.FacilityID,
		Details:    details,
	}
	// Where a person is named as the owner, they are the *target* rather than the actor: the
	// entry is about somebody being asked to do something, not about them doing it.
	if opened.OwnerUserID != nil {
		entry.TargetUserID = opened.OwnerUserID
	}
	_, err := b.recorder.Record(ctx, entry)
	return err
}

// ReviewCompleted records a cycle being closed.
//
// The count of prices still unverified is on the entry deliberately. Closing a review with two
// hundred prices nobody checked is a legitimate thing to do — the clinic may have decided this
// month's list was fine — and it is also exactly the thing somebody should be able to see was
// done, without having to reconstruct the state of the formulary on that day.
func (b *formularyAuditBridge) ReviewCompleted(ctx context.Context, done formulary.ReviewCompletion) error {
	actor := done.ActorID
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       "formulary.review_completed",
		FacilityID: done.FacilityID,
		ActorID:    &actor,
		ActorCode:  done.ActorCode,
		ActorRole:  done.ActorRole,
		Reason:     done.Note,
		Details: map[string]any{
			"month":      done.PeriodMonth,
			"unverified": done.Unverified,
		},
	})
	return err
}
