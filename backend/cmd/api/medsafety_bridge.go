package main

import (
	"context"
	"strings"

	"github.com/AmlanWTK/DTHCMS/backend/internal/audit"
	"github.com/AmlanWTK/DTHCMS/backend/internal/medsafety"
)

// A clinical safety rule going live, on its way to the security audit trail (CP77).
//
// `medsafety` may not import `audit` (architecture.json), for the reason every bridge in this
// binary exists: a module able to write audit entries directly grows a second, differently-shaped
// way of describing what happened, and the trail's value is that there is exactly one.
//
// # Why the whole rule is in the entry, when nothing else in this trail carries contents
//
// The checkpoint asks for it, and the reason is worth restating because it looks like
// over-recording. A finding cites a rule code and a version number. Months later, when a
// prescription written under that rule is questioned — by a patient, by a colleague, by a
// regulator — the question is *what did the rule say at the time*, and the rule's own table can
// answer it only as long as nobody has published a v3 and nobody has withdrawn it. Both of those
// are ordinary things to have happened.
//
// So this entry carries the severity, both messages, the condition document and the citation, and
// it carries them into the hash-chained trail, where they cannot be quietly edited. The version
// table is the fast answer; this is the one that is still true when the fast answer is gone.
//
// # No patient, ever
//
// A rule names molecules, classes and diagnosis codes. It names no patient, and the sandbox —
// which is where a patient's picture briefly exists — records nothing at all.
type medsafetyAuditBridge struct {
	recorder *audit.Recorder
}

var _ medsafety.Auditor = (*medsafetyAuditBridge)(nil)

func (b *medsafetyAuditBridge) RulePublished(ctx context.Context, published medsafety.Publication) error {
	actor := published.ActorID
	details := map[string]any{
		"rule":     published.Rule.Code,
		"type":     string(published.Rule.Type),
		"version":  published.Version.Version,
		"severity": string(published.Version.Severity),
		// The sentence a person reads, in both languages, rendered from the exact condition
		// that was stored. `plain` is what the rendered audit line quotes.
		"plain":    published.Plain.EN,
		"plain_bn": published.Plain.BN,

		// The content itself. Everything a check depended on.
		"name_en":    published.Version.NameEN,
		"name_bn":    published.Version.NameBN,
		"message_en": published.Version.MessageEN,
		"message_bn": published.Version.MessageBN,
		"advice_en":  published.Version.AdviceEN,
		"advice_bn":  published.Version.AdviceBN,
		"source":     published.Version.Source,
		"origin":     string(published.Version.Origin),
		"condition":  published.Version.Condition,
	}
	if published.Supersedes > 0 {
		details["supersedes_version"] = published.Supersedes
	}
	if published.Version.EffectiveFrom != nil {
		details["effective_from"] = published.Version.EffectiveFrom.UTC()
	}
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       "medication_rule.published",
		FacilityID: published.FacilityID,
		ActorID:    &actor,
		ActorCode:  published.ActorCode,
		ActorRole:  published.ActorRole,
		Details:    details,
	})
	return err
}

func (b *medsafetyAuditBridge) RuleWithdrawn(ctx context.Context, withdrawn medsafety.Withdrawal) error {
	actor := withdrawn.ActorID
	details := map[string]any{
		"rule":    withdrawn.Rule.Code,
		"type":    string(withdrawn.Rule.Type),
		"version": withdrawn.Version.Version,
	}
	// A rule withdrawn before it was ever published has no live version, and the entry says so
	// rather than printing a zero that reads like version 0.
	if withdrawn.Version.Version == 0 {
		details["version"] = "none — it was never published"
	} else {
		details["message_en"] = withdrawn.Version.MessageEN
		details["message_bn"] = withdrawn.Version.MessageBN
		details["condition"] = withdrawn.Version.Condition
	}
	reason := strings.TrimSpace(withdrawn.Reason)
	_, err := b.recorder.Record(ctx, audit.Entry{
		Kind:       "medication_rule.withdrawn",
		FacilityID: withdrawn.FacilityID,
		ActorID:    &actor,
		ActorCode:  withdrawn.ActorCode,
		ActorRole:  withdrawn.ActorRole,
		Reason:     reason,
		Details:    details,
	})
	return err
}
