package main

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinical"
	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/quality"
)

// qualityReviewBridge asks the quality module to look at an operator after a correction on their
// record is answered (CP63).
//
// `clinical` may not import `quality` and `quality` may not import `clinical`
// (architecture.json), and the direction of that rule is the right way round: the correction
// workflow has no need to know a quality record exists, and a clinic that removed the quality
// record would not have to change a line of the correction workflow.
//
// The facility comes from the ledger envelope rather than from an argument, for the same reason
// every other attribution in this system does: the clinic a record belongs to is the one the
// writer was signed in to, not one a caller can name.
type qualityReviewBridge struct {
	detector *quality.Detector
	logger   *slog.Logger
}

var _ clinical.QualityReviewer = (*qualityReviewBridge)(nil)

func (b *qualityReviewBridge) ReviewOperator(ctx context.Context, operator uuid.UUID) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return
	}
	// The error is logged and dropped. A missed review is a flag raised on the next correction
	// instead; a returned error here would make an operator unable to fix a wrong height because
	// a counting query was slow.
	if _, err := b.detector.Review(ctx, actor.FacilityID(), operator); err != nil {
		b.logger.WarnContext(ctx, "could not review an operator's quality record",
			"operator_id", operator.String(), "error", err.Error())
	}
}
