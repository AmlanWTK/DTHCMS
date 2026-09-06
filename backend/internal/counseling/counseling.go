// Package counseling is the checklist a counsellor works through, authored by a physician
// rather than by a release (CP55, §5.1, [R-07]).
//
// # Why versioning is the whole checkpoint
//
// §5.1 asks for templates configurable "without code changes". That is the easy half. The hard
// half is criterion 2: a completed session retains the version it used.
//
// Without it, editing the diabetes template next March silently rewrites what every counsellor
// was asked to cover last October, and a record saying "all seven ticked" becomes a claim about
// a checklist that did not exist at the time. Six months of counselling audit would quietly
// become unreadable, and nothing would look wrong.
//
// So a published version is frozen — by a trigger, not by this package remembering. Editing
// means drafting a new version, which is more work for the author and the right trade: a typo
// fixed in a new version is honest, and one fixed in place is a small lie told to every past
// session.
//
// # What is open
//
// **D-53.** The seven diabetes items are transcribed from §5.1 and are the launch minimum, not
// a clinical author's list; their Bengali is mine and their guidance text certainly is. The
// version reports `approved_at` as null so that nothing presents them as settled — the same
// mechanism the plausibility bands and critical thresholds use.
//
// Which items are mandatory is also clinical. Everything is mandatory in the seed, because a
// gate that lets a diabetic patient reach the consultant without hearing about their
// complications is a gate doing nothing, and it is easier for a clinician to relax one than to
// notice a missing one.
package counseling

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Room is one of the physical rooms counselling walks through (§5.2).
type Room struct {
	Room      string `json:"room"`
	DisplayEN string `json:"display_en"`
	DisplayBN string `json:"display_bn"`
	// StationCode is the queue this room belongs to, where it has one. The insulin corner is
	// part of the counselling station rather than a station of its own, and a room claiming to
	// be a station would show on the traffic board as a queue nobody is called to.
	StationCode string `json:"station_code,omitempty"`
	Ordering    int    `json:"ordering"`
}

// Item is one thing a counsellor covers.
type Item struct {
	ItemCode string `json:"item_code"`
	Ordering int    `json:"ordering"`

	TextEN string `json:"text_en"`
	TextBN string `json:"text_bn"`

	// Guidance is what to actually say, where the item needs it. §5.4 is the reason it exists:
	// the physician spot-questions the patient afterwards, and two counsellors who covered
	// "injection sites" differently make that check useless.
	GuidanceEN string `json:"guidance_en,omitempty"`
	GuidanceBN string `json:"guidance_bn,omitempty"`

	// Mandatory is what §5.5's gate reads. A column rather than a hardcoded list, because which
	// items are mandatory is a clinical decision that will change without a release.
	Mandatory bool `json:"mandatory"`

	Room     string `json:"room"`
	RoomEN   string `json:"room_en,omitempty"`
	RoomBN   string `json:"room_bn,omitempty"`
	RoomStep int    `json:"room_step,omitempty"`
	// RoomStation is the queue that room belongs to. Carried on the item because the phone
	// needs it to know whether the item in front of it belongs to the room the operator is
	// standing in — and without it every client fetches the room catalogue a second time to
	// learn that the insulin corner is part of the counselling station.
	RoomStation string `json:"room_station,omitempty"`
}

// Version is one revision of a template. Frozen once published.
type Version struct {
	TemplateID uuid.UUID `json:"template_id"`
	Version    int       `json:"version"`
	Status     string    `json:"status"`
	Notes      string    `json:"notes,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`

	PublishedAt *time.Time `json:"published_at,omitempty"`
	PublishedBy string     `json:"published_by,omitempty"`
	// PublishedSource is USER or MIGRATION. The seeded diabetes version says MIGRATION, because
	// no person published it — and an invented user id would be the only attribution in this
	// system naming somebody who did not do the thing.
	PublishedSource string `json:"published_source,omitempty"`

	RetiredAt *time.Time `json:"retired_at,omitempty"`

	// ApprovedAt is null until a clinician signs off on the content (D-53). Reported rather than
	// hidden, so an interface never presents a proposal as settled.
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	ApprovedBy string     `json:"approved_by,omitempty"`

	Items []Item `json:"items,omitempty"`
}

// Approved says whether a clinician has signed off on this version's content.
func (v Version) Approved() bool { return v.ApprovedAt != nil }

// Mandatory is the items §5.5's gate requires. The gate lives in CP57; this is the list it
// reads, computed here so there is one answer to "what must be ticked".
func (v Version) Mandatory() []Item {
	out := make([]Item, 0, len(v.Items))
	for _, item := range v.Items {
		if item.Mandatory {
			out = append(out, item)
		}
	}
	return out
}

// Template is a checklist with its versions.
type Template struct {
	ID      uuid.UUID `json:"id"`
	Code    string    `json:"code"`
	TitleEN string    `json:"title_en"`
	TitleBN string    `json:"title_bn"`
	Retired bool      `json:"retired"`

	// PublishedVersion is what a new session gets, absent when nothing is published yet.
	PublishedVersion *int       `json:"published_version,omitempty"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	ApprovedAt       *time.Time `json:"approved_at,omitempty"`

	LatestVersion int `json:"latest_version"`
	DraftCount    int `json:"draft_count"`
}

// Assignment is a rule saying which diagnosis calls for which checklist.
type Assignment struct {
	ID           uuid.UUID `json:"id"`
	TemplateID   uuid.UUID `json:"template_id"`
	TemplateCode string    `json:"template_code"`
	TitleEN      string    `json:"title_en"`
	TitleBN      string    `json:"title_bn"`

	CodeSystem  string `json:"code_system"`
	CodeVersion string `json:"code_version"`
	// CodePrefix matches the start of a recorded diagnosis code. `E11` catches the whole family,
	// which is what a clinician means by "type 2 diabetes" — and a rule per member would be
	// sixteen rows that drift apart.
	CodePrefix string `json:"code_prefix"`
	Priority   int    `json:"priority"`
}

var (
	// ErrNotFound is a template or version that is not there.
	ErrNotFound = errors.New("counseling: no such template or version")

	// ErrNotDraft is an edit to a version somebody already published.
	//
	// Also enforced by a trigger, and the two are not redundant: this one gives an author a
	// sentence, and the trigger holds for the migration, the support script and the second
	// client. The change it stops is a well-meaning typo fix.
	ErrNotDraft = errors.New("counseling: that version is published and cannot be edited")

	// ErrNotBilingual is a publish attempt on a version with an item in one language.
	ErrNotBilingual = errors.New("counseling: every item must read in both languages before publishing")

	// ErrEmpty is a publish attempt on a version with no items. An empty checklist on a phone is
	// a checklist that reads as complete the moment it opens.
	ErrEmpty = errors.New("counseling: a version with no items cannot be published")

	// ErrUnknownRoom is an item assigned to a room the clinic does not have.
	ErrUnknownRoom = errors.New("counseling: no such counselling room")

	// ErrDuplicateCode is a template code somebody already used.
	ErrDuplicateCode = errors.New("counseling: a template with that code already exists")
)

// Store reads and writes templates.
type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

func (s *Store) InTransaction(ctx context.Context,
	fn func(context.Context, pgx.Tx, *dbgen.Queries) error) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx, s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Rooms is the sequence counselling walks through.
func (s *Store) Rooms(ctx context.Context) ([]Room, error) {
	rows, err := s.q.CounselingRooms(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Room, 0, len(rows))
	for _, row := range rows {
		out = append(out, Room{
			Room: row.Room, DisplayEN: row.DisplayEn, DisplayBN: row.DisplayBn,
			StationCode: row.StationCode, Ordering: int(row.Ordering),
		})
	}
	return out, nil
}

// Templates lists every checklist with the version a new session would get.
func (s *Store) Templates(ctx context.Context) ([]Template, error) {
	rows, err := s.q.CounselingTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(rows))
	for _, row := range rows {
		item := Template{
			ID: row.ID, Code: row.Code, TitleEN: row.TitleEn, TitleBN: row.TitleBn,
			Retired:     row.RetiredAt != nil,
			PublishedAt: row.PublishedAt, ApprovedAt: row.ApprovedAt,
			DraftCount: int(row.DraftCount),
		}
		if row.PublishedVersion != nil {
			version := int(*row.PublishedVersion)
			item.PublishedVersion = &version
		}
		if row.LatestVersion != nil {
			if latest, ok := row.LatestVersion.(int32); ok {
				item.LatestVersion = int(latest)
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// Version reads one revision with its items.
func (s *Store) Version(ctx context.Context, template uuid.UUID, version int) (Version, error) {
	row, err := s.q.CounselingVersion(ctx, dbgen.CounselingVersionParams{
		TemplateID: template, Version: int32(version),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, err
	}
	out := versionFrom(row)
	items, err := s.Items(ctx, template, version)
	if err != nil {
		return Version{}, err
	}
	out.Items = items
	return out, nil
}

// Published is the version a new session gets, and the one §5.5's gate reads.
func (s *Store) Published(ctx context.Context, template uuid.UUID) (Version, error) {
	row, err := s.q.PublishedCounselingVersion(ctx, template)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, err
	}
	out := versionFrom(row)
	items, err := s.Items(ctx, template, int(row.Version))
	if err != nil {
		return Version{}, err
	}
	out.Items = items
	return out, nil
}

// Versions lists every revision of a template, newest first.
func (s *Store) Versions(ctx context.Context, template uuid.UUID) ([]Version, error) {
	rows, err := s.q.CounselingVersions(ctx, template)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(rows))
	for _, row := range rows {
		out = append(out, versionFrom(row))
	}
	return out, nil
}

// Items is one version's list, in working order.
func (s *Store) Items(ctx context.Context, template uuid.UUID, version int) ([]Item, error) {
	rows, err := s.q.CounselingItems(ctx, dbgen.CounselingItemsParams{
		TemplateID: template, Version: int32(version),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		out = append(out, Item{
			ItemCode: row.ItemCode, Ordering: int(row.Ordering),
			TextEN: row.TextEn, TextBN: row.TextBn,
			GuidanceEN: row.GuidanceEn, GuidanceBN: row.GuidanceBn,
			Mandatory: row.IsMandatory, Room: row.Room,
			RoomEN: row.RoomEn, RoomBN: row.RoomBn, RoomStep: int(row.RoomOrdering),
			RoomStation: row.StationCode,
		})
	}
	return out, nil
}

// ByCode finds a template by its stable code.
func (s *Store) ByCode(ctx context.Context, code string) (Template, error) {
	row, err := s.q.CounselingTemplateByCode(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrNotFound
	}
	if err != nil {
		return Template{}, err
	}
	return Template{
		ID: row.ID, Code: row.Code, TitleEN: row.TitleEn, TitleBN: row.TitleBn,
		Retired: row.RetiredAt != nil,
	}, nil
}

// Assignments is every live rule mapping a diagnosis to a checklist.
func (s *Store) Assignments(ctx context.Context) ([]Assignment, error) {
	rows, err := s.q.CounselingAssignments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		out = append(out, Assignment{
			ID: row.ID, TemplateID: row.TemplateID, TemplateCode: row.TemplateCode,
			TitleEN: row.TitleEn, TitleBN: row.TitleBn,
			CodeSystem: row.CodeSystem, CodeVersion: row.CodeVersion,
			CodePrefix: row.CodePrefix, Priority: int(row.Priority),
		})
	}
	return out, nil
}

// Match is a template a coding calls for.
type Match struct {
	TemplateID uuid.UUID `json:"template_id"`
	Code       string    `json:"code"`
	TitleEN    string    `json:"title_en"`
	TitleBN    string    `json:"title_bn"`
	Version    int       `json:"version"`
	Priority   int       `json:"priority"`
}

// For is which checklists a recorded diagnosis calls for, best first.
//
// Only published versions come back. A draft is not something to hand a counsellor, and a
// session started against one would reference a version that can still change under it.
func (s *Store) For(ctx context.Context, system, version, code string) ([]Match, error) {
	rows, err := s.q.TemplatesForCoding(ctx, dbgen.TemplatesForCodingParams{
		CodeSystem: system, CodeVersion: version, Column3: code,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Match, 0, len(rows))
	for _, row := range rows {
		out = append(out, Match{
			TemplateID: row.ID, Code: row.Code, TitleEN: row.TitleEn, TitleBN: row.TitleBn,
			Version: int(row.Version), Priority: int(row.Priority),
		})
	}
	return out, nil
}

func versionFrom(row dbgen.CoreCounselingTemplateVersion) Version {
	out := Version{
		TemplateID: row.TemplateID, Version: int(row.Version), Status: row.Status,
		Notes: row.Notes, CreatedAt: row.CreatedAt,
		PublishedAt: row.PublishedAt, PublishedSource: row.PublishedSource,
		RetiredAt: row.RetiredAt, ApprovedAt: row.ApprovedAt,
	}
	if row.CreatedBy.Valid {
		out.CreatedBy = row.CreatedBy.UUID.String()
	}
	if row.PublishedBy.Valid {
		out.PublishedBy = row.PublishedBy.UUID.String()
	}
	if row.ApprovedBy.Valid {
		out.ApprovedBy = row.ApprovedBy.UUID.String()
	}
	return out
}

func trimmed(values ...*string) {
	for _, v := range values {
		*v = strings.TrimSpace(*v)
	}
}
