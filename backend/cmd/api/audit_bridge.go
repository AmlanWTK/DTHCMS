package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/auth"
)

// auditBridge carries auth's audit entries into the audit module (CP22).
//
// auth may not import audit and audit may not import auth (architecture.json), so the
// translation between "what the console says happened" and "a row in the chain" lives
// here, in the one place allowed to know both. It is small on purpose: a field is renamed
// only where the sentence template names it differently.
type auditBridge struct {
	recorder *audit.Recorder
}

var _ auth.AuditRecorder = (*auditBridge)(nil)

func (b *auditBridge) RecordAudit(ctx context.Context, e auth.AuditEntry) error {
	entry := audit.Entry{
		Kind: e.Kind, FacilityID: e.FacilityID, ActorCode: e.ActorCode, ActorRole: e.ActorRole,
		TargetUserID: e.TargetUserID, TargetCode: e.TargetCode, Reason: e.Reason,
		ClientDigest: e.ClientDigest, At: e.At, Details: detailsOf(e),
	}
	if e.ActorID != uuid.Nil {
		id := e.ActorID
		entry.ActorID = &id
	}
	_, err := b.recorder.Record(ctx, entry)
	return err
}

// detailsOf flattens before/after into what the templates read: a key present on both
// sides becomes {before}/{after}; a key on one side keeps its name; the console's
// "sessions_ended" is the sentence's {count}.
func detailsOf(e auth.AuditEntry) map[string]any {
	out := map[string]any{}
	for k, v := range e.After {
		if _, both := e.Before[k]; both {
			out["before"] = e.Before[k]
			out["after"] = v
			continue
		}
		out[k] = v
	}
	for k, v := range e.Before {
		if _, both := e.After[k]; !both {
			out[k] = v
		}
	}
	if n, ok := out["sessions_ended"]; ok {
		out["count"] = n
		delete(out, "sessions_ended")
	}
	return out
}
