package medsafety

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/formulary"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Reading and writing the rule library (CP77).

// Permission codes. Named here so the router, the contract test and the migration cannot drift.
const (
	// PermRead — see the library and its versions.
	PermRead = "medication.rule.read"
	// PermWrite — draft, edit and test a rule.
	PermWrite = "medication.rule.write"
	// PermPublish — approve, publish or withdraw. Physician only, with a step-up in front.
	PermPublish = "medication.rule.publish"
)

// StepUpPurpose is what a step-up token for publishing is minted for. One purpose, one use, five
// minutes — the same shape as publishing a counselling checklist, because it is the same act: a
// thing that changes what happens to every patient from that second onwards.
const StepUpPurpose = "medication_rule.publish"

var (
	// ErrNotFound is a rule or version this facility does not have.
	ErrNotFound = errors.New("medsafety: no such rule")
	// ErrDuplicateCode is a rule code already in use.
	ErrDuplicateCode = errors.New("medsafety: a rule with that code already exists")
	// ErrNotADraft is an attempt to edit or publish something already published.
	ErrNotADraft = errors.New("medsafety: that version has been published and cannot be changed")
	// ErrAmbiguousRuleset is more than one live version of one rule at one instant. Impossible
	// through the EXCLUDE constraint; refused loudly rather than resolved, because a second
	// answer means the guarantee has failed and taking the first would hide it.
	ErrAmbiguousRuleset = errors.New("medsafety: two versions of one rule were live at once")
	// ErrWithdrawn is an attempt to publish onto a rule that has been withdrawn.
	ErrWithdrawn = errors.New("medsafety: that rule has been withdrawn")
)

// Store is the rule library in PostgreSQL.
//
// It holds a `formulary.Store` rather than reading `core.generic` and `core.medication_class`
// itself. Those tables belong to CP75, and a second module reading them directly is how a
// vocabulary comes to mean two slightly different things — which, for the molecule names every
// rule matches on, would mean rules that silently never fire. `architecture.json` allows
// `medsafety` to import `formulary` for exactly this.
type Store struct {
	pool      *pgxpool.Pool
	q         *dbgen.Queries
	formulary *formulary.Store
}

// NewStore builds one.
func NewStore(pool *pgxpool.Pool, catalogue *formulary.Store) *Store {
	return &Store{pool: pool, q: dbgen.New(pool), formulary: catalogue}
}

func (s *Store) inTransaction(ctx context.Context, fn func(context.Context, *dbgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Vocabulary
// ---------------------------------------------------------------------------

// Vocabulary loads what a condition may name: this clinic's molecules, its therapeutic classes,
// and the allergen groups.
//
// Fetched per validation rather than cached. It is three small statements, validation happens on
// a form submission rather than on a keystroke, and a cached vocabulary is how a rule comes to
// name a molecule the formulary no longer has.
func (s *Store) Vocabulary(ctx context.Context, facility uuid.UUID) (Vocabulary, error) {
	v := Vocabulary{
		Generics:       map[string]string{},
		Classes:        map[string]bool{},
		AllergenGroups: map[string]bool{},
	}
	generics, err := s.formulary.Generics(ctx, facility)
	if err != nil {
		return Vocabulary{}, err
	}
	for _, g := range generics {
		v.Generics[strings.ToLower(g.Name)] = g.Name
	}
	catalogue, err := s.formulary.Catalogue(ctx)
	if err != nil {
		return Vocabulary{}, err
	}
	for _, c := range catalogue.Classes {
		v.Classes[c.Code] = true
	}
	groups, err := s.q.AllergenGroups(ctx)
	if err != nil {
		return Vocabulary{}, err
	}
	for _, g := range groups {
		v.AllergenGroups[g.Code] = true
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// RuleFilter narrows the library list.
type RuleFilter struct {
	Type RuleType
	// UnapprovedOnly is the working list this checkpoint creates: the forty seeded rules
	// nobody has read yet, and anything drafted since.
	UnapprovedOnly bool
	ActiveOnly     bool
	Limit          int
	Offset         int
}

// RuleSummary is one row of the library list.
type RuleSummary struct {
	Rule
	NameEN   string   `json:"name_en"`
	NameBN   string   `json:"name_bn"`
	Severity Severity `json:"severity"`
	Origin   Origin   `json:"origin"`
	// Approved is false while no version of this rule has ever been approved — which is what
	// the screen draws the "not approved" badge from, and what makes the forty seeded rules
	// visibly different from a rule the physician wrote.
	Approved bool `json:"approved"`
}

// RulePage is a page of the library.
type RulePage struct {
	Items []RuleSummary `json:"items"`
	Total int           `json:"total"`
}

// Rules lists the library.
func (s *Store) Rules(ctx context.Context, facility uuid.UUID, f RuleFilter) (RulePage, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.q.ListMedicationRules(ctx, dbgen.ListMedicationRulesParams{
		FacilityID: facility, PType: string(f.Type),
		PUnapprovedOnly: f.UnapprovedOnly, PActiveOnly: f.ActiveOnly,
		PLimit: int32(limit), POffset: int32(f.Offset),
	})
	if err != nil {
		return RulePage{}, err
	}
	page := RulePage{Items: make([]RuleSummary, 0, len(rows))}
	for _, r := range rows {
		page.Total = int(r.TotalCount)
		summary := RuleSummary{
			Rule: Rule{
				ID: r.ID, FacilityID: facility, Code: r.Code, Type: RuleType(r.RuleType),
				IsActive: r.IsActive, WithdrawnAt: r.WithdrawnAt,
				LatestVersion: int(r.LatestVersion), CreatedAt: r.CreatedAt,
			},
			NameEN: r.LatestNameEn, NameBN: r.LatestNameBn,
			Severity: Severity(r.LatestSeverity), Origin: Origin(r.LatestOrigin),
		}
		if r.WithdrawnReason != nil {
			summary.WithdrawnReason = *r.WithdrawnReason
		}
		if r.PublishedVersion > 0 {
			published := int(r.PublishedVersion)
			summary.PublishedVersion = &published
			summary.Approved = true
		}
		page.Items = append(page.Items, summary)
	}
	return page, nil
}

// Rule reads one rule and every version of it, newest first.
func (s *Store) Rule(ctx context.Context, facility, id uuid.UUID) (Rule, []Version, error) {
	row, err := s.q.MedicationRuleByID(ctx, dbgen.MedicationRuleByIDParams{
		ID: id, FacilityID: facility,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, nil, ErrNotFound
	}
	if err != nil {
		return Rule{}, nil, err
	}
	rule := Rule{
		ID: row.ID, FacilityID: row.FacilityID, Code: row.Code, Type: RuleType(row.RuleType),
		IsActive: row.IsActive, WithdrawnAt: row.WithdrawnAt, CreatedAt: row.CreatedAt,
	}
	if row.WithdrawnReason != nil {
		rule.WithdrawnReason = *row.WithdrawnReason
	}

	raw, err := s.q.MedicationRuleVersions(ctx, id)
	if err != nil {
		return Rule{}, nil, err
	}
	versions := make([]Version, 0, len(raw))
	for _, v := range raw {
		parsed, err := versionFrom(versionRow{
			ID: v.ID, RuleID: v.RuleID, Version: v.Version, Severity: v.Severity,
			NameEn: v.NameEn, NameBn: v.NameBn, MessageEn: v.MessageEn, MessageBn: v.MessageBn,
			AdviceEn: v.AdviceEn, AdviceBn: v.AdviceBn, Condition: v.Condition,
			SourceCitation: v.SourceCitation, Origin: v.Origin, Status: v.Status,
			AuthoredBy: v.AuthoredBy, AuthoredAt: v.AuthoredAt, AuthoredCode: v.AuthoredCode,
			ApprovedBy: v.ApprovedBy, ApprovedAt: v.ApprovedAt, ApprovedCode: v.ApprovedCode,
			EffectiveFrom: v.EffectiveFrom, EffectiveTo: v.EffectiveTo, Notes: v.Notes,
		})
		if err != nil {
			return Rule{}, nil, err
		}
		versions = append(versions, parsed)
		if parsed.Version > rule.LatestVersion {
			rule.LatestVersion = parsed.Version
		}
		if parsed.Status == StatusPublished {
			n := parsed.Version
			rule.PublishedVersion = &n
		}
	}
	return rule, versions, nil
}

// Version reads one version by id.
func (s *Store) Version(ctx context.Context, facility, id uuid.UUID) (Version, error) {
	v, err := s.q.MedicationRuleVersion(ctx, dbgen.MedicationRuleVersionParams{
		ID: id, FacilityID: facility,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, err
	}
	return versionFrom(versionRow{
		ID: v.ID, RuleID: v.RuleID, Version: v.Version, Severity: v.Severity,
		NameEn: v.NameEn, NameBn: v.NameBn, MessageEn: v.MessageEn, MessageBn: v.MessageBn,
		AdviceEn: v.AdviceEn, AdviceBn: v.AdviceBn, Condition: v.Condition,
		SourceCitation: v.SourceCitation, Origin: v.Origin, Status: v.Status,
		AuthoredBy: v.AuthoredBy, AuthoredAt: v.AuthoredAt, AuthoredCode: v.AuthoredCode,
		ApprovedBy: v.ApprovedBy, ApprovedAt: v.ApprovedAt, ApprovedCode: v.ApprovedCode,
		EffectiveFrom: v.EffectiveFrom, EffectiveTo: v.EffectiveTo, Notes: v.Notes,
	})
}

// versionRow is the shape both version queries return. One struct so [versionFrom] is written
// once: two nearly-identical conversions is how a field comes to be read in one path and not the
// other, and the field that goes missing is always the one nobody looks at — here, `approved_at`.
type versionRow struct {
	ID, RuleID                     uuid.UUID
	Version                        int32
	Severity                       string
	NameEn, NameBn                 string
	MessageEn, MessageBn           string
	AdviceEn, AdviceBn             string
	Condition                      []byte
	SourceCitation, Origin, Status string
	AuthoredBy                     uuid.NullUUID
	AuthoredAt                     time.Time
	AuthoredCode                   *string
	ApprovedBy                     uuid.NullUUID
	ApprovedAt                     *time.Time
	ApprovedCode                   *string
	EffectiveFrom, EffectiveTo     *time.Time
	Notes                          string
}

func versionFrom(r versionRow) (Version, error) {
	condition, err := UnmarshalCondition(r.Condition)
	if err != nil {
		// A stored condition this build cannot read. Refused rather than partially decoded:
		// evaluating a published rule with its unknown half silently dropped is precisely the
		// quiet wrong answer this module exists to prevent.
		return Version{}, fmt.Errorf("rule version %d: %w", r.Version, err)
	}
	v := Version{
		ID: r.ID, RuleID: r.RuleID, Version: int(r.Version), Severity: Severity(r.Severity),
		NameEN: r.NameEn, NameBN: r.NameBn, MessageEN: r.MessageEn, MessageBN: r.MessageBn,
		AdviceEN: r.AdviceEn, AdviceBN: r.AdviceBn, Condition: condition,
		Source: r.SourceCitation, Origin: Origin(r.Origin), Status: Status(r.Status),
		AuthoredAt: r.AuthoredAt, ApprovedAt: r.ApprovedAt,
		EffectiveFrom: r.EffectiveFrom, EffectiveTo: r.EffectiveTo, Notes: r.Notes,
	}
	if r.AuthoredBy.Valid {
		id := r.AuthoredBy.UUID
		v.AuthoredBy = &id
	}
	if r.ApprovedBy.Valid {
		id := r.ApprovedBy.UUID
		v.ApprovedBy = &id
	}
	if r.AuthoredCode != nil {
		v.AuthoredCode = *r.AuthoredCode
	}
	if r.ApprovedCode != nil {
		v.ApprovedCode = *r.ApprovedCode
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Writing
// ---------------------------------------------------------------------------

// NewRule is a rule being created with its first draft.
type NewRule struct {
	FacilityID uuid.UUID
	Code       string
	Type       RuleType
	Draft      Version
	ActorID    uuid.UUID
}

// CreateRule writes a rule and its version 1, as a draft.
//
// The version is created with the rule, for the reason CP55 gives for a counselling template: a
// rule with no versions is a row that cannot be edited or published and looks, in every listing,
// like a mistake. `assert_every_medication_rule_has_its_versions` is the backstop.
func (s *Store) CreateRule(ctx context.Context, vocab Vocabulary, in NewRule) (uuid.UUID, error) {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	if err := in.Draft.Validate(vocab, in.Type); err != nil {
		return uuid.Nil, err
	}
	condition, err := MarshalCondition(in.Draft.Condition)
	if err != nil {
		return uuid.Nil, err
	}

	var id uuid.UUID
	err = s.inTransaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		created, err := q.InsertMedicationRule(ctx, dbgen.InsertMedicationRuleParams{
			FacilityID: in.FacilityID, Code: in.Code, RuleType: string(in.Type),
			ActorID: in.ActorID,
		})
		if err != nil {
			if strings.Contains(err.Error(), "medication_rule_code_key") {
				return ErrDuplicateCode
			}
			return err
		}
		id = created
		_, err = q.InsertMedicationRuleVersion(ctx, dbgen.InsertMedicationRuleVersionParams{
			RuleID: created, Version: 1, Severity: string(in.Draft.Severity),
			NameEn: in.Draft.NameEN, NameBn: in.Draft.NameBN,
			MessageEn: in.Draft.MessageEN, MessageBn: in.Draft.MessageBN,
			AdviceEn: in.Draft.AdviceEN, AdviceBn: in.Draft.AdviceBN,
			Condition: condition, SourceCitation: in.Draft.Source,
			Origin: string(OriginAuthored), ActorID: in.ActorID, Notes: in.Draft.Notes,
		})
		return err
	})
	return id, err
}

// DraftVersion opens a new draft of an existing rule, copied from whatever is there now.
//
// Copied rather than started empty, for the reason CP55 gives: a new version usually exists to
// change one thing, and a system that makes the safe path expensive gets the unsafe one instead.
func (s *Store) DraftVersion(ctx context.Context, facility, ruleID uuid.UUID,
	actor uuid.UUID) (Version, error) {

	rule, versions, err := s.Rule(ctx, facility, ruleID)
	if err != nil {
		return Version{}, err
	}
	if !rule.IsActive {
		return Version{}, ErrWithdrawn
	}
	if len(versions) == 0 {
		return Version{}, ErrNotFound
	}

	// Copy the published version if there is one, else the newest draft. An author who drafted
	// twice without publishing continues from his own last draft rather than from a version he
	// has already moved past.
	source := versions[0]
	for _, v := range versions {
		if v.Status == StatusPublished {
			source = v
			break
		}
	}
	condition, err := MarshalCondition(source.Condition)
	if err != nil {
		return Version{}, err
	}

	var created Version
	err = s.inTransaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		next, err := q.NextMedicationRuleVersion(ctx, ruleID)
		if err != nil {
			return err
		}
		id, err := q.InsertMedicationRuleVersion(ctx, dbgen.InsertMedicationRuleVersionParams{
			RuleID: ruleID, Version: next, Severity: string(source.Severity),
			NameEn: source.NameEN, NameBn: source.NameBN,
			MessageEn: source.MessageEN, MessageBn: source.MessageBN,
			AdviceEn: source.AdviceEN, AdviceBn: source.AdviceBN,
			Condition: condition, SourceCitation: source.Source,
			Origin: string(OriginAuthored), ActorID: actor,
			Notes: "",
		})
		if err != nil {
			return err
		}
		created = source
		created.ID, created.Version, created.Status = id, int(next), StatusDraft
		created.Origin = OriginAuthored
		created.ApprovedAt, created.ApprovedBy = nil, nil
		created.EffectiveFrom, created.EffectiveTo = nil, nil
		created.Notes = ""
		return nil
	})
	return created, err
}

// SaveDraft replaces a draft's content.
func (s *Store) SaveDraft(ctx context.Context, vocab Vocabulary, facility, versionID uuid.UUID,
	draft Version, actor uuid.UUID) error {

	existing, err := s.Version(ctx, facility, versionID)
	if err != nil {
		return err
	}
	if existing.Status != StatusDraft {
		return ErrNotADraft
	}
	rule, _, err := s.Rule(ctx, facility, existing.RuleID)
	if err != nil {
		return err
	}
	if err := draft.Validate(vocab, rule.Type); err != nil {
		return err
	}
	condition, err := MarshalCondition(draft.Condition)
	if err != nil {
		return err
	}
	return s.q.UpdateMedicationRuleDraft(ctx, dbgen.UpdateMedicationRuleDraftParams{
		ID: versionID, Severity: string(draft.Severity),
		NameEn: draft.NameEN, NameBn: draft.NameBN,
		MessageEn: draft.MessageEN, MessageBn: draft.MessageBN,
		AdviceEn: draft.AdviceEN, AdviceBn: draft.AdviceBN,
		Condition: condition, SourceCitation: draft.Source,
		Notes: draft.Notes, ActorID: actor,
	})
}

// Publication is what publishing produced, for the audit entry.
//
// **It carries the whole version.** The checkpoint's audit requirement is the full rule content,
// and the reason is stated as a defect to avoid rather than as a policy: a rule that changed with
// no record of what it said is a rule nobody can reconstruct when a prescription written under it
// is questioned. The bridge in cmd/api turns this into an entry; nothing here decides what an
// audit row looks like.
type Publication struct {
	FacilityID uuid.UUID
	Rule       Rule
	Version    Version
	// Supersedes is the version number that was live and has now been retired, or 0.
	Supersedes int
	// Plain is the rule stated in words, both languages, from the exact stored condition. In
	// the audit entry as well as on screen, so that a person reading the trail in two years
	// does not have to parse a JSON document to see what was published.
	Plain Plain

	ActorID   uuid.UUID
	ActorCode string
	ActorRole string
	At        time.Time
}

// Publish approves a draft and makes it live at `at`.
//
// One transaction, and the order inside it is the whole of the reproducibility guarantee: the
// predecessor's period closes at exactly the instant the successor's opens, so a check run at any
// instant has exactly one answer. The EXCLUDE constraint refuses anything else.
func (s *Store) Publish(ctx context.Context, facility, versionID uuid.UUID,
	actor uuid.UUID, at time.Time) (Publication, error) {

	version, err := s.Version(ctx, facility, versionID)
	if err != nil {
		return Publication{}, err
	}
	if version.Status != StatusDraft {
		return Publication{}, ErrNotADraft
	}
	rule, versions, err := s.Rule(ctx, facility, version.RuleID)
	if err != nil {
		return Publication{}, err
	}
	if !rule.IsActive {
		return Publication{}, ErrWithdrawn
	}

	out := Publication{FacilityID: facility, Rule: rule, ActorID: actor, At: at}
	for _, v := range versions {
		if v.Status == StatusPublished {
			out.Supersedes = v.Version
		}
	}

	err = s.inTransaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		if out.Supersedes > 0 {
			if err := q.CloseMedicationRuleVersion(ctx, dbgen.CloseMedicationRuleVersionParams{
				RuleID: rule.ID, At: at, Status: string(StatusSuperseded), ActorID: actor,
			}); err != nil {
				return err
			}
		}
		row, err := q.PublishMedicationRuleVersion(ctx, dbgen.PublishMedicationRuleVersionParams{
			ID: versionID, ActorID: actor, At: at,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotADraft
		}
		if err != nil {
			return err
		}
		version.Status = StatusPublished
		version.ApprovedAt = &at
		version.ApprovedBy = &actor
		version.EffectiveFrom = row.EffectiveFrom
		return nil
	})
	if err != nil {
		return Publication{}, err
	}
	out.Version = version
	out.Plain = version.Explain()
	return out, nil
}

// Withdrawal is a rule being taken out of use.
type Withdrawal struct {
	FacilityID uuid.UUID
	Rule       Rule
	// Version is the version that was live and has been retired, or the zero Version when the
	// rule had never been published.
	Version   Version
	Reason    string
	ActorID   uuid.UUID
	ActorCode string
	ActorRole string
	At        time.Time
}

// Withdraw deactivates a rule and closes whatever version was live.
//
// Nothing is deleted. Every version stays, with its period intact, because a check run while the
// rule was live has to stay reproducible after the rule is gone — which is the case somebody
// actually asks about, months later, when a prescription is questioned.
func (s *Store) Withdraw(ctx context.Context, facility, ruleID uuid.UUID, reason string,
	actor uuid.UUID, at time.Time) (Withdrawal, error) {

	rule, versions, err := s.Rule(ctx, facility, ruleID)
	if err != nil {
		return Withdrawal{}, err
	}
	if !rule.IsActive {
		return Withdrawal{}, ErrWithdrawn
	}
	out := Withdrawal{
		FacilityID: facility, Rule: rule, Reason: strings.TrimSpace(reason),
		ActorID: actor, At: at,
	}
	for _, v := range versions {
		if v.Status == StatusPublished {
			out.Version = v
		}
	}

	err = s.inTransaction(ctx, func(ctx context.Context, q *dbgen.Queries) error {
		if out.Version.ID != uuid.Nil {
			if err := q.CloseMedicationRuleVersion(ctx, dbgen.CloseMedicationRuleVersionParams{
				RuleID: ruleID, At: at, Status: string(StatusWithdrawn), ActorID: actor,
			}); err != nil {
				return err
			}
		}
		return q.WithdrawMedicationRule(ctx, dbgen.WithdrawMedicationRuleParams{
			ID: ruleID, FacilityID: facility, At: at, Reason: out.Reason, ActorID: actor,
		})
	})
	return out, err
}

// ---------------------------------------------------------------------------
// The ruleset — the interface CP78 builds on
// ---------------------------------------------------------------------------

// RulesetAt loads the rules that were live at an instant.
//
// **This is criterion 3, and it is the seam for CP78.** `at` is `time.Now()` for a live check and
// the instant of a past check when reproducing one. Nothing else changes between those two calls,
// which is what makes "re-run yesterday's check" a parameter rather than a feature.
func (s *Store) RulesetAt(ctx context.Context, facility uuid.UUID, at time.Time) (Ruleset, error) {
	rows, err := s.q.RulesetAt(ctx, dbgen.RulesetAtParams{FacilityID: facility, At: at})
	if err != nil {
		return Ruleset{}, err
	}
	out := Ruleset{At: at}
	seen := map[uuid.UUID]bool{}
	for _, r := range rows {
		if seen[r.RuleID] {
			// The EXCLUDE constraint makes this impossible. Refused rather than resolved:
			// taking the first row would hide the failure of the one guarantee this table has.
			return Ruleset{}, fmt.Errorf("%w: rule %s at %s", ErrAmbiguousRuleset, r.Code, at)
		}
		seen[r.RuleID] = true

		version, err := versionFrom(versionRow{
			ID: r.VersionID, RuleID: r.RuleID, Version: r.Version, Severity: r.Severity,
			NameEn: r.NameEn, NameBn: r.NameBn, MessageEn: r.MessageEn, MessageBn: r.MessageBn,
			AdviceEn: r.AdviceEn, AdviceBn: r.AdviceBn, Condition: r.Condition,
			SourceCitation: r.SourceCitation, Origin: r.Origin, Status: r.Status,
			ApprovedBy: r.ApprovedBy, ApprovedAt: r.ApprovedAt,
			EffectiveFrom: r.EffectiveFrom, EffectiveTo: r.EffectiveTo,
		})
		if err != nil {
			return Ruleset{}, err
		}
		out.Rules = append(out.Rules, Rule{
			ID: r.RuleID, FacilityID: facility, Code: r.Code,
			Type: RuleType(r.RuleType), IsActive: r.IsActive,
		})
		out.Versions = append(out.Versions, version)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Allergen groups and cross-reactivity
// ---------------------------------------------------------------------------

// AllergenGroup is a group a patient's reported allergy maps onto.
type AllergenGroup struct {
	Code    string `json:"code"`
	NameEN  string `json:"name_en"`
	NameBN  string `json:"name_bn"`
	NotesEN string `json:"notes_en,omitempty"`
	NotesBN string `json:"notes_bn,omitempty"`
	Source  string `json:"source"`
	Origin  string `json:"origin"`
	// Approved is the same guarantee as a rule's. An unapproved group is drawn as unapproved
	// and CP78 will not expand an allergy through an unapproved cross-reaction.
	Approved   bool             `json:"approved"`
	ApprovedAt *time.Time       `json:"approved_at"`
	IsActive   bool             `json:"is_active"`
	Members    []AllergenMember `json:"members"`
}

// AllergenMember is one molecule or class inside a group.
type AllergenMember struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// CrossReaction is what a reaction to one group implies about another.
type CrossReaction struct {
	ID         uuid.UUID  `json:"id"`
	From       string     `json:"from_group"`
	To         string     `json:"to_group"`
	Risk       string     `json:"risk"`
	NoteEN     string     `json:"note_en"`
	NoteBN     string     `json:"note_bn"`
	Source     string     `json:"source"`
	Origin     string     `json:"origin"`
	Approved   bool       `json:"approved"`
	ApprovedAt *time.Time `json:"approved_at"`
	IsActive   bool       `json:"is_active"`
}

// Allergens reads the groups, their members and the cross-reactions.
func (s *Store) Allergens(ctx context.Context) ([]AllergenGroup, []CrossReaction, error) {
	groups, err := s.q.AllergenGroups(ctx)
	if err != nil {
		return nil, nil, err
	}
	members, err := s.q.AllergenGroupMembers(ctx)
	if err != nil {
		return nil, nil, err
	}
	byGroup := map[string][]AllergenMember{}
	for _, m := range members {
		byGroup[m.GroupCode] = append(byGroup[m.GroupCode], AllergenMember{
			Kind: m.MatchKind, Value: m.MatchValue, Source: m.SourceCitation,
		})
	}

	out := make([]AllergenGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, AllergenGroup{
			Code: g.Code, NameEN: g.NameEn, NameBN: g.NameBn,
			NotesEN: g.NotesEn, NotesBN: g.NotesBn,
			Source: g.SourceCitation, Origin: g.Origin,
			Approved: g.ApprovedAt != nil, ApprovedAt: g.ApprovedAt,
			IsActive: g.IsActive, Members: byGroup[g.Code],
		})
	}

	reactions, err := s.q.AllergenCrossReactions(ctx)
	if err != nil {
		return nil, nil, err
	}
	cross := make([]CrossReaction, 0, len(reactions))
	for _, r := range reactions {
		cross = append(cross, CrossReaction{
			ID: r.ID, From: r.FromGroup, To: r.ToGroup, Risk: r.Risk,
			NoteEN: r.NoteEn, NoteBN: r.NoteBn, Source: r.SourceCitation, Origin: r.Origin,
			Approved: r.ApprovedAt != nil, ApprovedAt: r.ApprovedAt, IsActive: r.IsActive,
		})
	}
	return out, cross, nil
}

// ApproveCrossReaction puts a physician's name on a seeded cross-reactivity mapping.
func (s *Store) ApproveCrossReaction(ctx context.Context, id, actor uuid.UUID, at time.Time) error {
	return s.q.ApproveAllergenCrossReaction(ctx, dbgen.ApproveAllergenCrossReactionParams{
		ID: id, ApprovedBy: actor, ApprovedAt: at,
	})
}

// ApproveAllergenGroup puts a physician's name on a seeded allergen grouping.
func (s *Store) ApproveAllergenGroup(ctx context.Context, code string, actor uuid.UUID,
	at time.Time) error {

	return s.q.ApproveAllergenGroup(ctx, dbgen.ApproveAllergenGroupParams{
		Code: code, ApprovedBy: actor, ApprovedAt: at,
	})
}
