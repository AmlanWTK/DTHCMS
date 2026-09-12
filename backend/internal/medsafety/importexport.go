package medsafety

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Taking the rule library off the screen and bringing it back (CP77).
//
// # What this is for
//
// Reviewing forty rules on a web page is not how a physician reviews forty rules. He wants them
// in a file he can read on a plane, mark up, and send to a colleague at BIRDEM to argue with.
// Export gives him that; import brings the marked-up file back.
//
// # Import can only ever create drafts
//
// **Nothing imported is ever live, and nothing imported carries an approval.** A file is a thing
// that can be edited by anybody with a text editor and mailed by anybody at all, and an import
// path that could set `approved_by` would be a way to publish a clinical rule without a
// physician's second factor — which is the whole of what the publish endpoint is guarding.
//
// So the import writes DRAFT versions with `origin = IMPORTED`, and publishing them is the same
// three-step act it is for a rule typed on the form. The export carries the approval fields so
// that a reviewer can see what was live; the import reads them and ignores them, and says so in
// the report rather than silently.
//
// # Dry run by default
//
// `mode` defaults to DRY_RUN: the whole document is validated, the whole report is written, and
// not one row is touched. The same decision, for the same reason, as the formulary's CSV import —
// nothing should land by surprise, and a file somebody mailed you is exactly the case.
//
// # Per-rule partial acceptance, and why that is safe here
//
// One bad rule does not block thirty-nine good ones. The rules are independent — nothing in rule
// 30 depends on rule 29 — so a partial result is a smaller set of complete drafts rather than a
// half-applied change, and every rule's outcome is in the report with the reason in both
// languages. The persistence is still all-or-nothing: the accepted drafts commit in one
// transaction, so a database failure halfway leaves no import that half-happened.

// ExportDocument is the whole library as a file.
type ExportDocument struct {
	// Format is a version number for this document's own shape, so an import can refuse a file
	// written by a future build rather than reading it with fields missing.
	Format int `json:"format"`
	// ExportedAt and ExportedFrom are provenance for the person reading the file. Ignored on
	// import.
	ExportedAt   time.Time `json:"exported_at,omitempty"`
	ExportedFrom string    `json:"exported_from,omitempty"`

	Rules []ExportRule `json:"rules"`

	// Allergens travels with the rules because a contraindication rule naming an allergen
	// group is unreadable without them, and a reviewer with half the document would be
	// reviewing half the rule.
	Allergens []AllergenGroup `json:"allergen_groups,omitempty"`
	Cross     []CrossReaction `json:"cross_reactions,omitempty"`
}

// ExportFormat is the current document shape.
const ExportFormat = 1

// ExportRule is one rule in the file.
type ExportRule struct {
	Code string   `json:"code"`
	Type RuleType `json:"type"`

	Severity  Severity  `json:"severity"`
	NameEN    string    `json:"name_en"`
	NameBN    string    `json:"name_bn"`
	MessageEN string    `json:"message_en"`
	MessageBN string    `json:"message_bn"`
	AdviceEN  string    `json:"advice_en,omitempty"`
	AdviceBN  string    `json:"advice_bn,omitempty"`
	Condition Condition `json:"condition"`
	Source    string    `json:"source"`
	Notes     string    `json:"notes,omitempty"`

	// Version, Status, Origin and Approved are provenance for the reader. **Every one of them
	// is ignored on import**, and the report says so per rule.
	Version  int    `json:"version,omitempty"`
	Status   Status `json:"status,omitempty"`
	Origin   Origin `json:"origin,omitempty"`
	Approved bool   `json:"approved,omitempty"`

	// Plain is the rule in words, both languages. Not read back on import — it is derived from
	// the condition — and present because the file's whole purpose is being readable by
	// somebody who will not read JSON.
	Plain Plain `json:"plain,omitempty"`
}

// ImportOutcome is what happened to one rule in the file.
type ImportOutcome struct {
	// Line is the rule's position in the document, 1-based. The reader's own way of finding it.
	Line int      `json:"line"`
	Code string   `json:"code"`
	Type RuleType `json:"type"`

	// Result is CREATED, DRAFTED, REJECTED or UNCHANGED.
	//
	//	CREATED   — the code was new; a rule and its version 1 were drafted.
	//	DRAFTED   — the code existed; a new draft version was added to it.
	//	UNCHANGED — the code existed and the content is identical to its newest version.
	//	REJECTED  — the reason is below, in both languages.
	Result string `json:"result"`

	ReasonEN string `json:"reason_en,omitempty"`
	ReasonBN string `json:"reason_bn,omitempty"`
	// Version is the version number drafted, when one was.
	Version int `json:"version,omitempty"`
}

// ImportReport is what the import answers with.
type ImportReport struct {
	DryRun bool `json:"dry_run"`

	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
	Skipped  int `json:"skipped"`

	Outcomes []ImportOutcome `json:"outcomes"`

	// IgnoredFields names what the file carried and the import deliberately did not read.
	// Stated rather than left silent, because a reviewer who exported an approved rule and
	// imported it back would otherwise reasonably expect it to still be approved.
	IgnoredFieldsEN []string `json:"ignored_fields_en"`
	IgnoredFieldsBN []string `json:"ignored_fields_bn"`
}

// Export writes the whole library out.
func (s *Store) Export(ctx context.Context, facility uuid.UUID) (ExportDocument, error) {
	page, err := s.Rules(ctx, facility, RuleFilter{Limit: 200})
	if err != nil {
		return ExportDocument{}, err
	}
	doc := ExportDocument{Format: ExportFormat, ExportedAt: time.Now().UTC()}
	for _, summary := range page.Items {
		_, versions, err := s.Rule(ctx, facility, summary.ID)
		if err != nil {
			return ExportDocument{}, err
		}
		if len(versions) == 0 {
			continue
		}
		// The published version where there is one, else the newest draft. What a reviewer
		// wants is what the clinic is using, or what it is about to.
		chosen := versions[0]
		for _, v := range versions {
			if v.Status == StatusPublished {
				chosen = v
				break
			}
		}
		doc.Rules = append(doc.Rules, ExportRule{
			Code: summary.Code, Type: summary.Type,
			Severity: chosen.Severity, NameEN: chosen.NameEN, NameBN: chosen.NameBN,
			MessageEN: chosen.MessageEN, MessageBN: chosen.MessageBN,
			AdviceEN: chosen.AdviceEN, AdviceBN: chosen.AdviceBN,
			Condition: chosen.Condition, Source: chosen.Source, Notes: chosen.Notes,
			Version: chosen.Version, Status: chosen.Status, Origin: chosen.Origin,
			Approved: chosen.Approved(), Plain: chosen.Explain(),
		})
	}

	groups, cross, err := s.Allergens(ctx)
	if err != nil {
		return ExportDocument{}, err
	}
	doc.Allergens, doc.Cross = groups, cross
	return doc, nil
}

// Import reads a document back, as drafts.
func (s *Store) Import(ctx context.Context, facility uuid.UUID, doc ExportDocument,
	actor uuid.UUID, dryRun bool) (ImportReport, error) {

	report := ImportReport{
		DryRun:   dryRun,
		Outcomes: []ImportOutcome{},
		IgnoredFieldsEN: []string{
			"status", "approved", "version", "origin",
			"the allergen groups and cross-reactions, which are read-only here",
		},
		IgnoredFieldsBN: []string{
			"স্ট্যাটাস", "অনুমোদন", "সংস্করণ নম্বর", "উৎস",
			"অ্যালার্জেন গ্রুপ ও ক্রস-রিঅ্যাকশন, যা এখানে শুধু পড়ার জন্য",
		},
	}
	if doc.Format > ExportFormat {
		return report, fmt.Errorf("%w: this file was written by a newer build (format %d); "+
			"this one reads format %d", ErrInvalidRule, doc.Format, ExportFormat)
	}

	vocab, err := s.Vocabulary(ctx, facility)
	if err != nil {
		return report, err
	}

	// Everything is decided before anything is written, so a dry run and a real run take
	// exactly the same decisions — the only difference is whether the transaction runs.
	type accepted struct {
		outcome int
		rule    ExportRule
		ruleID  uuid.UUID
		isNew   bool
	}
	var toWrite []accepted

	seen := map[string]bool{}
	for i, in := range doc.Rules {
		outcome := ImportOutcome{Line: i + 1, Code: strings.ToUpper(strings.TrimSpace(in.Code)),
			Type: in.Type}

		if outcome.Code == "" {
			outcome.Result = "REJECTED"
			outcome.ReasonEN = "The rule has no code."
			outcome.ReasonBN = "নিয়মটির কোনো কোড নেই।"
			report.Outcomes = append(report.Outcomes, outcome)
			report.Rejected++
			continue
		}
		if seen[outcome.Code] {
			outcome.Result = "REJECTED"
			outcome.ReasonEN = "The code " + outcome.Code + " appears more than once in this file."
			outcome.ReasonBN = "এই ফাইলে " + outcome.Code + " কোডটি একাধিকবার আছে।"
			report.Outcomes = append(report.Outcomes, outcome)
			report.Rejected++
			continue
		}
		seen[outcome.Code] = true

		version := Version{
			Severity: in.Severity, NameEN: strings.TrimSpace(in.NameEN),
			NameBN:    strings.TrimSpace(in.NameBN),
			MessageEN: strings.TrimSpace(in.MessageEN),
			MessageBN: strings.TrimSpace(in.MessageBN),
			AdviceEN:  strings.TrimSpace(in.AdviceEN), AdviceBN: strings.TrimSpace(in.AdviceBN),
			Condition: in.Condition.Canonical(), Source: strings.TrimSpace(in.Source),
			Notes: strings.TrimSpace(in.Notes),
		}
		if err := version.Validate(vocab, in.Type); err != nil {
			outcome.Result = "REJECTED"
			outcome.ReasonEN = err.Error()
			outcome.ReasonBN = "নিয়মটি গ্রহণ করা যায়নি: " + err.Error()
			report.Outcomes = append(report.Outcomes, outcome)
			report.Rejected++
			continue
		}

		existing, _, err := s.ruleByCode(ctx, facility, outcome.Code)
		switch {
		case err == nil && existing.Type != in.Type:
			// The type is a rule's identity. A file that changed it would silently turn a
			// renal rule into a pregnancy rule with the same code, and every finding ever
			// cited under that code would now mean something else.
			outcome.Result = "REJECTED"
			outcome.ReasonEN = "The rule " + outcome.Code + " is a " + string(existing.Type) +
				" rule here and the file calls it a " + string(in.Type) + " rule."
			outcome.ReasonBN = "এখানে " + outcome.Code + " একটি " + string(existing.Type) +
				" নিয়ম, কিন্তু ফাইলে একে " + string(in.Type) + " নিয়ম বলা হয়েছে।"
			report.Outcomes = append(report.Outcomes, outcome)
			report.Rejected++
			continue
		case err == nil:
			same, err := s.matchesNewest(ctx, facility, existing.ID, version)
			if err != nil {
				return report, err
			}
			if same {
				outcome.Result = "UNCHANGED"
				outcome.ReasonEN = "Identical to the newest version already here."
				outcome.ReasonBN = "এখানে থাকা সর্বশেষ সংস্করণের সঙ্গে হুবহু এক।"
				report.Outcomes = append(report.Outcomes, outcome)
				report.Skipped++
				continue
			}
			outcome.Result = "DRAFTED"
			toWrite = append(toWrite, accepted{outcome: len(report.Outcomes), rule: in,
				ruleID: existing.ID})
		default:
			outcome.Result = "CREATED"
			toWrite = append(toWrite, accepted{outcome: len(report.Outcomes), rule: in,
				isNew: true})
		}
		outcome.ReasonEN = "Drafted, unapproved. Publishing it is a separate act."
		outcome.ReasonBN = "খসড়া হিসেবে যোগ হয়েছে, অনুমোদিত নয়। প্রকাশ করা আলাদা একটি কাজ।"
		report.Outcomes = append(report.Outcomes, outcome)
		report.Accepted++
	}

	if dryRun || len(toWrite) == 0 {
		return report, nil
	}

	err = s.inTransaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		for _, item := range toWrite {
			ruleID := item.ruleID
			code := strings.ToUpper(strings.TrimSpace(item.rule.Code))
			if item.isNew {
				created, err := q.InsertMedicationRule(ctx, dbgen.InsertMedicationRuleParams{
					FacilityID: facility, Code: code,
					RuleType: string(item.rule.Type), ActorID: actor,
				})
				if err != nil {
					return err
				}
				ruleID = created
			}
			next, err := q.NextMedicationRuleVersion(ctx, ruleID)
			if err != nil {
				return err
			}
			condition, err := MarshalCondition(item.rule.Condition.Canonical())
			if err != nil {
				return err
			}
			if _, err := q.InsertMedicationRuleVersion(ctx, dbgen.InsertMedicationRuleVersionParams{
				RuleID: ruleID, Version: next, Severity: string(item.rule.Severity),
				NameEn:         strings.TrimSpace(item.rule.NameEN),
				NameBn:         strings.TrimSpace(item.rule.NameBN),
				MessageEn:      strings.TrimSpace(item.rule.MessageEN),
				MessageBn:      strings.TrimSpace(item.rule.MessageBN),
				AdviceEn:       strings.TrimSpace(item.rule.AdviceEN),
				AdviceBn:       strings.TrimSpace(item.rule.AdviceBN),
				Condition:      condition,
				SourceCitation: strings.TrimSpace(item.rule.Source),
				// **IMPORTED, never SEED and never AUTHORED.** Where a rule came from is a
				// fact a reviewer needs, and a file is not a physician typing on a form.
				Origin:  string(OriginImported),
				ActorID: actor,
				Notes:   strings.TrimSpace(item.rule.Notes),
			}); err != nil {
				return err
			}
			report.Outcomes[item.outcome].Version = int(next)
		}
		return nil
	})
	return report, err
}

func (s *Store) ruleByCode(ctx context.Context, facility uuid.UUID, code string) (Rule, []Version, error) {
	row, err := s.q.MedicationRuleByCode(ctx, dbgen.MedicationRuleByCodeParams{
		Code: code, FacilityID: facility,
	})
	if err != nil {
		return Rule{}, nil, err
	}
	return Rule{
		ID: row.ID, FacilityID: row.FacilityID, Code: row.Code,
		Type: RuleType(row.RuleType), IsActive: row.IsActive,
	}, nil, nil
}

// matchesNewest reports whether the file's content is identical to the newest version already
// stored. Compared on the canonical condition and every rendered field, so a re-import of an
// unchanged file adds no version — the failure mode otherwise is a physician who exports, reads,
// changes nothing and imports, and ends up with forty new drafts to publish.
func (s *Store) matchesNewest(ctx context.Context, facility, ruleID uuid.UUID,
	candidate Version) (bool, error) {

	_, versions, err := s.Rule(ctx, facility, ruleID)
	if err != nil {
		return false, err
	}
	if len(versions) == 0 {
		return false, nil
	}
	newest := versions[0]
	a, err := MarshalCondition(newest.Condition)
	if err != nil {
		return false, err
	}
	b, err := MarshalCondition(candidate.Condition)
	if err != nil {
		return false, err
	}
	return string(a) == string(b) &&
		newest.Severity == candidate.Severity &&
		newest.NameEN == candidate.NameEN && newest.NameBN == candidate.NameBN &&
		newest.MessageEN == candidate.MessageEN && newest.MessageBN == candidate.MessageBN &&
		newest.AdviceEN == candidate.AdviceEN && newest.AdviceBN == candidate.AdviceBN &&
		newest.Source == candidate.Source, nil
}
