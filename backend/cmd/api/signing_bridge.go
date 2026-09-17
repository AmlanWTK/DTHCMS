package main

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/clinicalterm"
	"github.com/AmlanWTK/DTHCMS/backend/internal/qa"
	"github.com/AmlanWTK/DTHCMS/backend/internal/signing"
)

// Signing's one bridge (CP84).
//
// # Why station 10 reaches signing through four lines rather than an import
//
// `internal/signing` may not import `internal/qa`, and the reason is the direction of the
// dependency rather than a rule for its own sake: QA is a workflow with rules, a queue, an
// override valve and a review screen, and signing needs exactly one fact out of all of it —
// *was this prescription cleared, and by which review*. An import would put the whole of station
// 10 inside the blast radius of every change to signing, and would let a future version of this
// package clear a prescription it is about to sign, which is the separation the checkpoint
// exists to hold.
//
// So the fact crosses an interface signing declares and QA satisfies, and this is the only place
// in the repository that knows both names.
//
// # Why the clearance is covered by the signature at all
//
// "This prescription was cleared before it was signed" is part of what the signature attests.
// Without the review id in the canonical form, a clearance could be swapped afterwards for a
// different one — a later one, a weaker one, one granted on an override — and the signature would
// have nothing to say about it.
type signingClearanceBridge struct {
	qa *qa.Store
}

var _ signing.Clearances = (*signingClearanceBridge)(nil)

// StandingClearance is station 10's decision since the prescription was last bounced.
//
// **A bounce is not a clearance and neither is silence.** The only outcome that answers true is
// CLEARED, and `qa.Store.StandingDecision` already applies the "since the last bounce" clause
// that `core.qa_clearance_stands()` applies — so the review this names is the same review the
// database's own gate is looking at, rather than a second opinion about the same question.
func (b *signingClearanceBridge) StandingClearance(ctx context.Context, facility,
	prescription uuid.UUID) (signing.Clearance, bool, error) {

	decision, found, err := b.qa.StandingDecision(ctx, facility, prescription)
	if err != nil || !found || decision.Outcome != qa.OutcomeCleared {
		return signing.Clearance{}, false, err
	}
	return signing.Clearance{ReviewID: decision.ID, DecidedAt: decision.DecidedAt}, true, nil
}

// signingDates renders a date for the public verification page (CP85).
//
// # The second four-line bridge in this file, and the same reason as the first
//
// `internal/clinicalterm` owns the question *what does this look like to a person reading it, in
// the language they are reading* — it is the package CP83's three machine-shaped strings were
// fixed with, and rendering an issue date is precisely its job. `internal/signing` may not import
// it: `architecture.json` lists signing's imports as platform, eventstore, rbac and prescription,
// and that file's header says changing a rule requires an ADR.
//
// That constraint is not an obstacle here, it is the right answer. The public verification page
// must carry no clinical vocabulary at all, and a signing package that could reach the clinical
// lexicon is one somebody can put a diagnosis on that page from. So signing declares the narrowest
// possible seam — a day in, two strings out — and this is the only place in the repository that
// knows both names.
type signingDates struct{}

var _ signing.Dates = signingDates{}

func (signingDates) EN(t time.Time) string { return clinicalterm.DateEN(t) }
func (signingDates) BN(t time.Time) string { return clinicalterm.DateBN(t) }
