package prescription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Reading and writing CP82's three tables.
//
// # Why these statements are written here rather than in queries/
//
// Every other statement in this module goes through `sqlc`, and that is the right default: a
// column renamed in a migration and not in a query is then a compile error rather than a runtime
// one. These do not, for one reason that is about this checkpoint rather than about style.
//
// `sqlc` regeneration in this repository runs the generator in a container (see the Makefile note
// about a locally-installed version rewriting every header), and a checkpoint that cannot be
// built without a working container daemon is a checkpoint that cannot be built. The trade is
// stated rather than hidden: the statements below are checked by the database at run time and by
// the tests in `cp82_db_test.go` at build time, and a column renamed out from under them fails
// there instead of at `go build`. `internal/terminology` made the same trade for the same reason
// and it is the precedent, not an exception invented here.
//
// # The write side is deliberately small
//
// Three inserts and no updates, because there is nothing here that is ever updated: a run
// happened, a suggestion was offered, a decision was taken. The triggers in migration 00069 say
// so for every role; the absence of an update method here is what stops a future caller in this
// package finding one to call.

// ---------------------------------------------------------------------------
// The rejection vocabulary (§5)
// ---------------------------------------------------------------------------

// RejectReasons is §5's list, live and retired ones apart.
//
// Retired reasons are excluded from what a screen offers and remain readable on a decision that
// used one, which is why `reject_reason_code` is a foreign key rather than a copied string: the
// wording Dr Nahid corrects next year corrects every decision's label with it, and the code on
// the decision row is what makes that safe.
func (s *Store) RejectReasons(ctx context.Context) ([]RejectReason, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT code, label_en, label_bn, ordering
		  FROM core.ai_suggestion_reject_reason
		 WHERE retired_at IS NULL
		 ORDER BY ordering, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RejectReason{}
	for rows.Next() {
		var r RejectReason
		if err := rows.Scan(&r.Code, &r.LabelEN, &r.LabelBN, &r.Ordering); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// The candidate shortlist the model chooses from (§2)
// ---------------------------------------------------------------------------

// Candidate is one product the agent is allowed to propose.
//
// This struct is the whole of §2's first two refusals. The model is shown these and nothing else,
// and its answer names an id from this list — so "a medicine that is not in the formulary" and "a
// controlled or scheduled drug" are not instructions it may disregard but shapes it cannot write.
// They are re-checked on the way back in anyway, in [Service.sift], because a prompt is a request
// and a table is a fact.
type Candidate struct {
	// Ref is the handle the model answers with: `F001`, `F002`. Short and opaque, and **not the
	// product's uuid**, for two reasons that both bite.
	//
	// The first is a collision with the gateway's own rules: `ops.carries_identifier` refuses an
	// outbound payload containing nine or more digits separated by hyphens, which is what a uuid
	// is, so a shortlist of 120 product ids cannot be recorded and therefore cannot be sent. That
	// is the constraint working as designed — it cannot tell a product id from a phone number, and
	// the safe answer is the default.
	//
	// The second is the one that would have mattered anyway: a three-character handle is a handle
	// the model can copy without transcription error, and one it cannot invent a plausible-looking
	// value for. An unknown uuid and a mistyped uuid are indistinguishable; an unknown `F999` is
	// obviously not on the list.
	Ref string `json:"ref"`

	ProductID uuid.UUID `json:"-"`
	Label     string    `json:"product_label"`
	// GenericName is serialised as `generic` and **not** as `generic_name`, which is not
	// cosmetic: `ops.phi_key` holds `name`, the identifier check suffix-matches keys, and a
	// payload with a `generic_name` key in it is refused by the constraint on
	// `core.ai_interaction.outbound` — so the call cannot be recorded and therefore is not made.
	// The check is right and the key was wrong: `patient_name` is exactly the shape it exists to
	// catch, and a rule that let `generic_name` through would let that through too.
	GenericName string `json:"generic"`
	Strength    string `json:"strength,omitempty"`
	FormCode    string `json:"form_code,omitempty"`
	ClassCode   string `json:"class_code,omitempty"`
	// UnitPriceBDT is what the patient would pay per unit, as "12.34", or empty when the product
	// has no price on file. §5 makes cost a rejection reason of its own — *"in Faridpur they are
	// different facts with different fixes"* — so the model is shown the price it is proposing
	// somebody pay, rather than being asked to be thrifty in the abstract.
	UnitPriceBDT string `json:"unit_price_bdt,omitempty"`
}

// Candidates is the shortlist, already filtered by §2 and bounded by [MaxCandidates].
//
// Ordered by class and then by price, so that the truncation at the cap drops the most expensive
// brand of a class rather than an arbitrary one, and so that two runs against an unchanged
// formulary send the model the same bytes — which is what makes the gateway's response cache
// possible at all.
func (s *Store) Candidates(ctx context.Context, facility uuid.UUID, on time.Time,
	limit int) ([]Candidate, error) {

	if limit <= 0 || limit > MaxCandidates {
		limit = MaxCandidates
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.trade_name, g.name, p.strength, p.form_code, g.class_code,
		       pr.unit_price_poisha
		  FROM core.medication_product p
		  JOIN core.generic g ON g.id = p.generic_id
		  LEFT JOIN LATERAL (
		    SELECT mp.unit_price_poisha
		      FROM core.medication_price mp
		     WHERE mp.product_id = p.id
		       AND mp.effective_from <= $2::date
		       AND (mp.effective_to IS NULL OR mp.effective_to > $2::date)
		     ORDER BY mp.effective_from DESC
		     LIMIT 1) pr ON true
		 WHERE p.facility_id = $1
		   AND p.is_active
		   AND g.is_active
		   -- §2, in the shortlist rather than in the prompt, and through the same predicate the
		   -- answer check reads. The register is keyed on the molecule rather than on a formulary
		   -- row, so a molecule registered before this clinic stocked it excludes the product from
		   -- the day it is added, with no change to the register in between.
		   AND NOT core.generic_is_controlled(g.id)
		 ORDER BY g.class_code, coalesce(pr.unit_price_poisha, 9223372036854775807), g.name, p.trade_name
		 LIMIT $3`, facility, on, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Candidate{}
	for rows.Next() {
		var c Candidate
		var poisha *int64
		if err := rows.Scan(&c.ProductID, &c.Label, &c.GenericName, &c.Strength,
			&c.FormCode, &c.ClassCode, &poisha); err != nil {
			return nil, err
		}
		if poisha != nil {
			// Rendered rather than sent as a number, so nobody downstream divides by a hundred in
			// floating point. CP75's argument, and it holds wherever money crosses a boundary.
			c.UnitPriceBDT = fmt.Sprintf("%d.%02d", *poisha/100, *poisha%100)
		}
		c.Ref = fmt.Sprintf("F%03d", len(out)+1)
		out = append(out, c)
	}
	return out, rows.Err()
}

// controlledProducts is the second half of §2's controlled-drug refusal: the set of product ids
// this clinic treats as controlled, asked of the database rather than inferred from the shortlist's
// absence.
//
// Asked separately on purpose. "Not on the shortlist" and "controlled" are different facts and the
// run's drop counts need to tell them apart — a model repeatedly proposing pregabalin is a prompt
// problem, and a model proposing a product that was withdrawn this morning is not.
//
// Through `core.generic_is_controlled` rather than through a join, so that this and the shortlist
// subtraction are one definition. A join here would be a second copy of the rule, and the copy that
// was wrong would be whichever nobody was reading at the time.
func (s *Store) controlledProducts(ctx context.Context, facility uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id
		  FROM core.medication_product p
		 WHERE p.facility_id = $1 AND core.generic_is_controlled(p.generic_id)`, facility)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// controlledGenerics is every formulary generic that carries a registered molecule.
//
// Separate from [Store.controlledProducts] because the two answer different questions: this one is
// asked of CP81's dose guidance, which is keyed on the generic, and that one is asked of a model's
// answer, which names a product. Both go through `core.generic_is_controlled`.
//
// Note the direction of the query: it walks `core.generic` and asks the predicate, rather than
// walking the register and resolving each molecule to a generic. The register holds molecules this
// clinic may not stock, so resolving from that side would answer a different and smaller question.
func (s *Store) controlledGenerics(ctx context.Context) (map[uuid.UUID]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id FROM core.generic g WHERE core.generic_is_controlled(g.id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Runs and suggestions
// ---------------------------------------------------------------------------

// insertRun records one ask, whatever came of it.
func (s *Store) insertRun(ctx context.Context, tx pgx.Tx, run Run, facility uuid.UUID) error {
	reasons, err := json.Marshal(run.DroppedReasons)
	if err != nil {
		return err
	}
	if run.DroppedReasons == nil {
		reasons = []byte(`{}`)
	}
	var refusal *string
	if run.Refusal != "" {
		text := string(run.Refusal)
		refusal = &text
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO core.ai_prescribing_run (
		  id, facility_id, patient_id, visit_id, prescription_id,
		  state, refusal, ai_interaction_id, prompt_version, model_version,
		  offered_count, dropped_count, dropped_reasons, failure_detail,
		  requested_at, requested_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,nullif($9,''),nullif($10,''),$11,$12,$13,$14,$15,$16)`,
		run.ID, facility, run.PatientID, run.VisitID, run.PrescriptionID,
		string(run.State), refusal, run.InteractionID, run.PromptVersion, run.ModelVersion,
		run.OfferedCount, run.DroppedCount, reasons, run.FailureDetail,
		run.RequestedAt, run.RequestedBy)
	return err
}

// insertSuggestion stores one proposal, as offered.
func (s *Store) insertSuggestion(ctx context.Context, tx pgx.Tx, runID, facility,
	prescription uuid.UUID, in Suggestion) error {

	basis, err := json.Marshal(in.Basis)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO core.ai_prescribing_suggestion (
		  id, run_id, facility_id, prescription_id, ordinal,
		  product_id, product_label, generic_name, strength, form_code,
		  dose, daily_dose, dose_unit, frequency, duration_days, route,
		  rationale_en, rationale_bn, basis, offered_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		in.ID, runID, facility, prescription, in.Ordinal,
		in.ProductID, in.Label, in.GenericName, in.Strength, in.FormCode,
		in.Dose, in.DailyDose, in.DoseUnit, in.Frequency, in.DurationDays, in.Route,
		in.RationaleEN, in.RationaleBN, basis, in.OfferedAt)
	return err
}

// CurrentRun is the newest ask for one draft, with every suggestion and its decision.
//
// Newest rather than all, because a physician looking at the panel is looking at what is being
// proposed now. The history is every row and is read by the audit view, not by the editor.
func (s *Store) CurrentRun(ctx context.Context, prescription, facility uuid.UUID) (Run, error) {
	var run Run
	var refusal *string
	var reasons []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, prescription_id, patient_id, visit_id, state, refusal,
		       ai_interaction_id, coalesce(prompt_version,''), coalesce(model_version,''),
		       offered_count, dropped_count, dropped_reasons, failure_detail,
		       requested_at, requested_by
		  FROM core.ai_prescribing_run
		 WHERE prescription_id = $1 AND facility_id = $2
		 ORDER BY requested_at DESC, id DESC
		 LIMIT 1`, prescription, facility).Scan(
		&run.ID, &run.PrescriptionID, &run.PatientID, &run.VisitID, &run.State, &refusal,
		&run.InteractionID, &run.PromptVersion, &run.ModelVersion,
		&run.OfferedCount, &run.DroppedCount, &reasons, &run.FailureDetail,
		&run.RequestedAt, &run.RequestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	if refusal != nil {
		run.Refusal = Refusal(*refusal)
	}
	if len(reasons) > 0 {
		_ = json.Unmarshal(reasons, &run.DroppedReasons)
	}
	run.Suggestions, err = s.suggestionsOf(ctx, run.ID)
	return run, err
}

// suggestionsOf reads one run's proposals with each one's decision, or nil.
//
// A LEFT JOIN, and the NULL it produces is the whole of §1's *"unactioned is not a rejection"*:
// there is no row to read for a suggestion nobody answered, so there is no value to mistake for
// one. A query written with an inner join would silently return only the answered ones, which is
// the failure this shape makes visible rather than the one it hides.
func (s *Store) suggestionsOf(ctx context.Context, runID uuid.UUID) ([]Suggestion, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sg.id, sg.run_id, sg.ordinal,
		       sg.product_id, sg.product_label, sg.generic_name, sg.strength, sg.form_code,
		       sg.dose, sg.daily_dose, sg.dose_unit, sg.frequency, sg.duration_days, sg.route,
		       sg.rationale_en, sg.rationale_bn, sg.basis, sg.offered_at,
		       d.id, d.decision, d.decided_by, d.decided_at, d.prescription_item_id,
		       coalesce(d.reject_reason_code,''), coalesce(d.reject_note,'')
		  FROM core.ai_prescribing_suggestion sg
		  LEFT JOIN core.ai_prescribing_decision d ON d.suggestion_id = sg.id
		 WHERE sg.run_id = $1
		 ORDER BY sg.ordinal`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Suggestion{}
	for rows.Next() {
		var s Suggestion
		var basis []byte
		var daily *float64
		var decisionID *uuid.UUID
		var kind *string
		var decidedBy *uuid.UUID
		var decidedAt *time.Time
		var itemID *uuid.UUID
		var reason, note string
		if err := rows.Scan(&s.ID, &s.RunID, &s.Ordinal,
			&s.ProductID, &s.Label, &s.GenericName, &s.Strength, &s.FormCode,
			&s.Dose, &daily, &s.DoseUnit, &s.Frequency, &s.DurationDays, &s.Route,
			&s.RationaleEN, &s.RationaleBN, &basis, &s.OfferedAt,
			&decisionID, &kind, &decidedBy, &decidedAt, &itemID, &reason, &note); err != nil {
			return nil, err
		}
		s.DailyDose = daily
		if len(basis) > 0 {
			_ = json.Unmarshal(basis, &s.Basis)
		}
		if decisionID != nil && kind != nil && decidedBy != nil && decidedAt != nil {
			s.Decision = &Decision{
				ID: *decisionID, SuggestionID: s.ID, Kind: DecisionKind(*kind),
				DecidedBy: *decidedBy, DecidedAt: *decidedAt, ItemID: itemID,
				ReasonCode: reason, Note: note,
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// suggestion reads one proposal, scoped to the facility and to the prescription it belongs to.
//
// Both are in the WHERE clause rather than checked afterwards. A caller holding a suggestion id
// from another patient's consultation gets [ErrNoSuggestion] — the same answer a suggestion that
// does not exist gives, because a different answer would confirm that it does.
func (s *Store) suggestion(ctx context.Context, id, prescription, facility uuid.UUID) (Suggestion, error) {
	var out Suggestion
	var basis []byte
	var daily *float64
	err := s.pool.QueryRow(ctx, `
		SELECT id, run_id, ordinal, product_id, product_label, generic_name, strength, form_code,
		       dose, daily_dose, dose_unit, frequency, duration_days, route,
		       rationale_en, rationale_bn, basis, offered_at
		  FROM core.ai_prescribing_suggestion
		 WHERE id = $1 AND prescription_id = $2 AND facility_id = $3`,
		id, prescription, facility).Scan(
		&out.ID, &out.RunID, &out.Ordinal, &out.ProductID, &out.Label, &out.GenericName,
		&out.Strength, &out.FormCode, &out.Dose, &daily, &out.DoseUnit, &out.Frequency,
		&out.DurationDays, &out.Route, &out.RationaleEN, &out.RationaleBN, &basis, &out.OfferedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Suggestion{}, ErrNoSuggestion
	}
	if err != nil {
		return Suggestion{}, err
	}
	out.DailyDose = daily
	if len(basis) > 0 {
		_ = json.Unmarshal(basis, &out.Basis)
	}
	return out, nil
}

// decisionOf reads the decision on one suggestion, or nil.
func (s *Store) decisionOf(ctx context.Context, suggestion uuid.UUID) (*Decision, error) {
	var d Decision
	var reason, note string
	err := s.pool.QueryRow(ctx, `
		SELECT id, suggestion_id, decision, decided_by, decided_at, prescription_item_id,
		       coalesce(reject_reason_code,''), coalesce(reject_note,'')
		  FROM core.ai_prescribing_decision WHERE suggestion_id = $1`, suggestion).Scan(
		&d.ID, &d.SuggestionID, &d.Kind, &d.DecidedBy, &d.DecidedAt, &d.ItemID, &reason, &note)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.ReasonCode, d.Note = reason, note
	return &d, nil
}

// insertDecision writes the decision inside the caller's transaction.
//
// **Inside the caller's transaction is the point of the signature.** For an acceptance or an edit
// this row and the prescription item's event have to commit together, and migration 00069's
// deferred constraint trigger is what refuses a commit where only one of them is there.
func (s *Store) insertDecision(ctx context.Context, tx pgx.Tx, facility uuid.UUID,
	d Decision, eventID uuid.UUID) error {

	var reason *string
	if strings.TrimSpace(d.ReasonCode) != "" {
		code := d.ReasonCode
		reason = &code
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO core.ai_prescribing_decision (
		  id, suggestion_id, facility_id, decision, decided_by, decided_at,
		  prescription_item_id, reject_reason_code, reject_note, event_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		d.ID, d.SuggestionID, facility, string(d.Kind), d.DecidedBy, d.DecidedAt,
		d.ItemID, reason, d.Note, eventID)
	return err
}

// knownRejectReason reports whether a code is one this clinic uses and has not retired.
func (s *Store) knownRejectReason(ctx context.Context, code string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM core.ai_suggestion_reject_reason
		                WHERE code = $1 AND retired_at IS NULL)`, code).Scan(&ok)
	return ok, err
}
