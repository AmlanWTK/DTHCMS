package main

import (
	"context"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
)

// A medicine price review opening, on its way to the security audit trail (CP75).
//
// The worker's half of the formulary bridge, and only its half: this binary runs the scheduled
// sweep and never changes a price or closes a review, so it implements `formulary.ReviewOpener`
// rather than the whole of `formulary.Auditor`. The API binary has the other two translations,
// beside the handlers that need them. Sharing one type between the binaries would mean this
// process carrying two translations it can never reach, and a change to either would have to be
// kept in step with a caller that does not exist.
//
// `formulary` may not import `audit` (architecture.json), which is why this lives in a
// composition root rather than in the module.
type formularyAuditBridge struct {
	recorder *audit.Recorder
}

var _ formulary.ReviewOpener = (*formularyAuditBridge)(nil)

// ReviewOpened records the month's cycle being opened.
//
// **No actor.** Nobody opened it; a clock did. The sentence registry renders the entry without a
// name, and inventing a system user to put on the line would make the trail say a person did
// something they did not do. Where a person is named as the owner they are the *target* — the
// entry is about somebody being asked to do something, not about them doing it.
func (b *formularyAuditBridge) ReviewOpened(ctx context.Context, opened formulary.ReviewOpened) error {
	entry := audit.Entry{
		Kind:       "formulary.review_opened",
		FacilityID: opened.FacilityID,
		Details: map[string]any{
			"month":      opened.PeriodMonth,
			"owner_role": opened.OwnerRole,
			"products":   opened.Products,
			"unverified": opened.Unverified,
		},
	}
	if opened.OwnerUserID != nil {
		entry.TargetUserID = opened.OwnerUserID
	}
	_, err := b.recorder.Record(ctx, entry)
	return err
}
