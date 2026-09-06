package main

import (
	"context"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/counseling"
)

// Publishing a counselling template, on its way to the security audit trail (CP55).
//
// `counseling` may not import `audit`, and the reason is the one every bridge in this binary
// exists for: a module able to write audit entries directly would grow a second,
// differently-shaped way of describing what happened, and the trail's value is that there is
// exactly one. The translation between "a physician published version 3" and an audit row
// belongs here, in the only place allowed to know both.
type counselingAuditBridge struct {
	recorder *audit.Recorder
}

var _ counseling.Auditor = (*counselingAuditBridge)(nil)

func (b *counselingAuditBridge) TemplatePublished(ctx context.Context, p counseling.Publication) error {
	actor := p.ActorID
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       "counseling.template_published",
		FacilityID: p.FacilityID,
		ActorID:    &actor,
		ActorCode:  p.ActorCode,
		ActorRole:  p.ActorRole,
		Details: map[string]any{
			"template": p.Template,
			"version":  p.Version,
			"items":    p.Items,
		},
	})
	return err
}

// GateOverridden is one patient sent past the counselling gate (CP57 criterion 3).
//
// In the security trail beside role grants and credential resets, because that is the log
// somebody reads when asking "who decided this". The clinical half — which items the patient
// was not counselled about — is on the physician's panel, where the person who needs it is.
func (b *counselingAuditBridge) GateOverridden(ctx context.Context, o counseling.GateOverride) error {
	actor := o.ActorID
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       "counseling.gate_overridden",
		FacilityID: o.FacilityID,
		ActorID:    &actor,
		ActorCode:  o.ActorCode,
		ActorRole:  o.ActorRole,
		PatientID:  &o.PatientID,
		Details: map[string]any{
			"visit":   o.VisitID.String(),
			"reason":  o.Reason,
			"missing": o.Missing,
		},
	})
	return err
}
