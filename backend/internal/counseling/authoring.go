package counseling

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// The authoring path (CP55, criterion 1: a new template can be authored and published by a
// physician without a code change).
//
// # Why a draft's items are replaced wholesale
//
// The authoring UI sends the list it is showing. A patch-style update would leave the stored
// version disagreeing with the screen the author is looking at, and the way that surfaces is
// somebody publishing an item they thought they had deleted — on a checklist a counsellor then
// reads to a patient.
//
// # Why publishing is its own act
//
// Saving a draft is cheap and reversible. Publishing puts a checklist on every phone on the
// floor within seconds, freezes it forever, and retires whatever was there before. Those are
// not the same act and they do not share an endpoint; publishing additionally needs a step-up,
// for the same reason an administrator's writes do.

// Draft is a version as an author submits it.
type Draft struct {
	Notes string
	Items []DraftItem
}

// DraftItem is one item as an author submits it. `ItemCode` is stable across versions on
// purpose: it is what a tick references, so renaming the text of an item keeps its history.
type DraftItem struct {
	ItemCode string
	Ordering int

	TextEN string
	TextBN string

	GuidanceEN string
	GuidanceBN string

	Mandatory bool
	Room      string
}

// Service writes templates.
type Service struct {
	store *Store
}

func NewService(store *Store) *Service { return &Service{store: store} }

// CreateTemplate makes an empty checklist with its first draft version.
//
// The version is created with the template, because a template with no versions is a row that
// cannot be edited or published and looks, in every listing, like a mistake.
func (s *Service) CreateTemplate(ctx context.Context, code, titleEN, titleBN string,
	author uuid.UUID) (Template, error) {

	trimmed(&code, &titleEN, &titleBN)
	code = strings.ToUpper(code)
	if code == "" || titleEN == "" || titleBN == "" {
		return Template{}, fmt.Errorf("%w: a template needs a code and a title in both languages",
			ErrNotBilingual)
	}

	var out Template
	err := s.store.InTransaction(ctx, func(ctx context.Context, _ pgx.Tx, q *dbgen.Queries) error {
		row, err := q.CreateCounselingTemplate(ctx, dbgen.CreateCounselingTemplateParams{
			Code: code, TitleEn: titleEN, TitleBn: titleBN,
			CreatedBy: uuid.NullUUID{UUID: author, Valid: true},
		})
		if err != nil {
			if strings.Contains(err.Error(), "counseling_template_code_key") {
				return ErrDuplicateCode
			}
			return err
		}
		out = Template{ID: row.ID, Code: row.Code, TitleEN: row.TitleEn, TitleBN: row.TitleBn}
		return q.CreateCounselingVersion(ctx, dbgen.CreateCounselingVersionParams{
			TemplateID: row.ID, Version: 1, Notes: "",
			CreatedBy: uuid.NullUUID{UUID: author, Valid: true},
		})
	})
	if err != nil {
		return Template{}, err
	}
	out.LatestVersion = 1
	out.DraftCount = 1
	return out, nil
}

// NewVersion opens a draft from whatever the template has now.
//
// It copies the current published version's items, because that is what an author almost always
// wants: a new version exists to change one thing. Starting empty would mean retyping seven
// items to fix a typo in one, and a system that makes the safe path expensive gets the unsafe
// one instead.
func (s *Service) NewVersion(ctx context.Context, template uuid.UUID, notes string,
	author uuid.UUID) (Version, error) {

	var created int
	err := s.store.InTransaction(ctx, func(ctx context.Context, _ pgx.Tx, q *dbgen.Queries) error {
		next, err := q.NextCounselingVersion(ctx, template)
		if err != nil {
			return err
		}
		created = int(next)
		if err := q.CreateCounselingVersion(ctx, dbgen.CreateCounselingVersionParams{
			TemplateID: template, Version: next, Notes: strings.TrimSpace(notes),
			CreatedBy: uuid.NullUUID{UUID: author, Valid: true},
		}); err != nil {
			return err
		}

		// Copy from the published version if there is one, else from the highest earlier
		// version. An author who drafted twice without publishing should continue from their
		// own last draft rather than from a version they already moved past.
		source, err := q.PublishedCounselingVersion(ctx, template)
		var from int32
		switch {
		case err == nil:
			from = source.Version
		case errors.Is(err, pgx.ErrNoRows):
			from = next - 1
		default:
			return err
		}
		if from < 1 {
			return nil
		}
		items, err := q.CounselingItems(ctx, dbgen.CounselingItemsParams{
			TemplateID: template, Version: from,
		})
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := q.AddCounselingItem(ctx, dbgen.AddCounselingItemParams{
				TemplateID: template, Version: next, ItemCode: item.ItemCode,
				Ordering: item.Ordering, TextEn: item.TextEn, TextBn: item.TextBn,
				GuidanceEn: item.GuidanceEn, GuidanceBn: item.GuidanceBn,
				IsMandatory: item.IsMandatory, Room: item.Room,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Version{}, err
	}
	return s.store.Version(ctx, template, created)
}

// SaveDraft replaces a draft version's items with the ones an author submitted.
//
// Half-written is allowed. A draft with an English item and no Bengali one is a normal state
// for somebody mid-sentence, and a rule refusing it would make the authoring screen fight the
// person using it. The bilingual requirement is checked at the publish transition instead —
// which is also where a database trigger checks it, for the paths that are not this one.
func (s *Service) SaveDraft(ctx context.Context, template uuid.UUID, version int,
	draft Draft) (Version, error) {

	rooms, err := s.store.Rooms(ctx)
	if err != nil {
		return Version{}, err
	}
	known := map[string]bool{}
	for _, room := range rooms {
		known[room.Room] = true
	}

	seen := map[string]bool{}
	for i := range draft.Items {
		item := &draft.Items[i]
		trimmed(&item.ItemCode, &item.TextEN, &item.TextBN,
			&item.GuidanceEN, &item.GuidanceBN, &item.Room)
		item.ItemCode = strings.ToUpper(item.ItemCode)
		if item.ItemCode == "" {
			return Version{}, errors.New("counseling: every item needs a stable code")
		}
		if seen[item.ItemCode] {
			// Two items sharing a code would make a tick ambiguous, and the ambiguity would
			// only surface as a gate that never closes.
			return Version{}, fmt.Errorf("counseling: item code %s appears twice", item.ItemCode)
		}
		seen[item.ItemCode] = true
		if !known[item.Room] {
			return Version{}, fmt.Errorf("%w: %s", ErrUnknownRoom, item.Room)
		}
		if item.TextEN == "" && item.TextBN == "" {
			return Version{}, errors.New("counseling: an item with no text in either language")
		}
		if item.Ordering == 0 {
			item.Ordering = i + 1
		}
	}

	err = s.store.InTransaction(ctx, func(ctx context.Context, _ pgx.Tx, q *dbgen.Queries) error {
		existing, err := q.CounselingVersion(ctx, dbgen.CounselingVersionParams{
			TemplateID: template, Version: int32(version),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if existing.Status != "DRAFT" {
			return ErrNotDraft
		}
		if err := q.ReplaceCounselingItems(ctx, dbgen.ReplaceCounselingItemsParams{
			TemplateID: template, Version: int32(version),
		}); err != nil {
			return err
		}
		for _, item := range draft.Items {
			if err := q.AddCounselingItem(ctx, dbgen.AddCounselingItemParams{
				TemplateID: template, Version: int32(version), ItemCode: item.ItemCode,
				Ordering: int32(item.Ordering), TextEn: item.TextEN, TextBn: item.TextBN,
				GuidanceEn: item.GuidanceEN, GuidanceBn: item.GuidanceBN,
				IsMandatory: item.Mandatory, Room: item.Room,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Version{}, err
	}
	return s.store.Version(ctx, template, version)
}

// Publish freezes a draft and makes it what every new session gets.
//
// The retire-and-publish is one statement in the database, so there is no instant in which a
// clinic has two live checklists or none. The bilingual and non-empty checks are here for the
// author's sake and in a trigger for everybody else's.
func (s *Service) Publish(ctx context.Context, template uuid.UUID, version int,
	publisher uuid.UUID) (Version, error) {

	items, err := s.store.Items(ctx, template, version)
	if err != nil {
		return Version{}, err
	}
	if len(items) == 0 {
		return Version{}, ErrEmpty
	}
	for _, item := range items {
		if strings.TrimSpace(item.TextEN) == "" || strings.TrimSpace(item.TextBN) == "" {
			return Version{}, fmt.Errorf("%w: %s", ErrNotBilingual, item.ItemCode)
		}
	}

	err = s.store.InTransaction(ctx, func(ctx context.Context, _ pgx.Tx, q *dbgen.Queries) error {
		existing, err := q.CounselingVersion(ctx, dbgen.CounselingVersionParams{
			TemplateID: template, Version: int32(version),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if existing.Status != "DRAFT" {
			return ErrNotDraft
		}
		// Retire first, then publish, in that order inside one transaction. As a single
		// statement they race the unique index that allows one published version per
		// template — see the note on the query.
		if err := q.RetirePublishedCounselingVersion(ctx, template); err != nil {
			return err
		}
		return q.PublishCounselingVersion(ctx, dbgen.PublishCounselingVersionParams{
			TemplateID: template, Version: int32(version),
			PublishedBy: uuid.NullUUID{UUID: publisher, Valid: true},
		})
	})
	if err != nil {
		return Version{}, err
	}
	return s.store.Version(ctx, template, version)
}
