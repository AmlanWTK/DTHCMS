package main

import (
	"context"
	"log/slog"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/quality"
	"github.com/AmlanWTK/DTHCMS/backend/internal/realtime"
)

// qualityBridge carries a retraining flag to the audit trail and to the operator's screen (CP63).
//
// `quality` may import neither `audit` nor `realtime` (architecture.json), so the translation
// lives here — the same shape as `auditBridge` and `alertBridge`, and for the same reason.
//
// # Why the operator is told, and not only the supervisor
//
// The plan's stated risk for this checkpoint is that a metric which feels punitive makes staff
// hide their errors rather than correct them. A flag somebody first learns about from their
// supervisor is exactly the ambush that produces that; telling them at the same moment costs one
// message and is most of the defence. So the message goes to the person it is about, and the
// supervisor reads a list they open — because the supervisor is looking for it and the operator
// is not.
//
// The payload carries the threshold and nothing else. No count, no evidence, no measurement: the
// screen re-reads through the API, which puts the read back through the permission that decides
// whether this reader may see a record at all.
type qualityBridge struct {
	recorder  *audit.Recorder
	publisher realtime.Publisher
	logger    *slog.Logger
}

var (
	_ quality.AuditSink = (*qualityBridge)(nil)
	_ quality.Notifier  = (*qualityBridge)(nil)
)

// QualityFlagRaised writes the audit row and returns its sequence, so the flag can point back at
// it. A raised flag with no audit row is still a flag — the detector ignores this error for
// exactly that reason — but the two read together is what makes a review possible a year later.
func (b *qualityBridge) QualityFlagRaised(ctx context.Context, flag quality.Flag) (int64, error) {
	operator := flag.OperatorID
	record, err := b.recorder.Record(ctx, audit.Entry{
		Kind:         "quality.flag_raised",
		FacilityID:   flag.FacilityID,
		TargetUserID: &operator,
		TargetCode:   flag.OperatorCode,
		Details: map[string]any{
			"threshold": flag.ThresholdEN,
			"observed":  flag.ObservedCount,
			// The denominator, in the sentence itself. A trail entry reading "three corrections"
			// without "out of four hundred entries" is the accusation this checkpoint is
			// arranged to avoid, and an audit row is where such a sentence would outlive
			// everybody's good intentions.
			"entries": flag.EntriesCount,
			"days":    flag.Window.Days,
		},
		At: flag.RaisedAt,
	})
	if err != nil {
		b.logger.WarnContext(ctx, "a quality flag was raised but not recorded on the audit trail",
			"flag_id", flag.ID.String(), "error", err.Error())
		return 0, err
	}
	return record.Seq, nil
}

// QualityFlagResolved records a supervisor's answer. Acknowledged and dismissed are both
// recorded: "nobody thought this was a problem" is a fact a later review needs as much as the
// flag itself.
func (b *qualityBridge) QualityFlagResolved(ctx context.Context, flag quality.Flag) (int64, error) {
	operator := flag.OperatorID
	entry := audit.Entry{
		Kind:         "quality.flag_resolved",
		FacilityID:   flag.FacilityID,
		TargetUserID: &operator,
		TargetCode:   flag.OperatorCode,
		Reason:       flag.Resolution,
		Details: map[string]any{
			"threshold": flag.ThresholdEN,
			"status":    flag.Status,
			"reason":    flag.Resolution,
		},
	}
	if flag.ResolvedAt != nil {
		entry.At = *flag.ResolvedAt
	}
	record, err := b.recorder.Record(ctx, entry)
	if err != nil {
		return 0, err
	}
	return record.Seq, nil
}

// PublishQualityFlag tells the operator, on their own topic — when it is raised, and again when
// somebody answers it.
//
// The second half is not symmetry for its own sake. A note that appeared on somebody's device and
// then silently disappeared when a supervisor closed it would teach them that things are decided
// about them out of sight, which is precisely the thing this checkpoint is arranged to avoid.
func (b *qualityBridge) PublishQualityFlag(ctx context.Context, flag quality.Flag) {
	kind := "quality.flag_raised"
	at := flag.RaisedAt
	if !flag.Open() {
		kind = "quality.flag_resolved"
		if flag.ResolvedAt != nil {
			at = *flag.ResolvedAt
		}
	}
	message := realtime.Message{
		Topic:      realtime.UserTopic(flag.OperatorID),
		Kind:       kind,
		FacilityID: flag.FacilityID.String(),
		// No permission: this goes to one person about themselves, and their own record needs
		// none (see the note on the route). A `Requires` here would mean an operator without a
		// supervisory permission never heard about their own flag.
		Requires:  "",
		Sensitive: false,
		At:        at,
		Summary: map[string]any{
			"flag_id":        flag.ID.String(),
			"threshold_code": flag.ThresholdCode,
			"status":         flag.Status,
		},
	}
	if err := b.publisher.Publish(ctx, message); err != nil {
		b.logger.WarnContext(ctx, "the operator was not told about a quality flag",
			"flag_id", flag.ID.String(), "error", err.Error())
	}
}
