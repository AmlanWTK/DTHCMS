-- Counselling templates (CP55).

-- name: CounselingRooms :many
-- The rooms counselling walks through, in the configured sequence (section 5.2).
SELECT room, display_en, display_bn, station_code, ordering
  FROM core.counseling_room ORDER BY ordering;

-- name: CounselingTemplates :many
-- Every template with the version a new session would get, if it has one. A template with no
-- published version is a draft in progress and is listed anyway: an author who cannot see their
-- own unpublished work has no way back to it.
SELECT t.id, t.code, t.title_en, t.title_bn, t.retired_at,
       p.version AS published_version, p.published_at, p.approved_at,
       (SELECT max(version) FROM core.counseling_template_version v WHERE v.template_id = t.id)
         AS latest_version,
       (SELECT count(*) FROM core.counseling_template_version v
         WHERE v.template_id = t.id AND v.status = 'DRAFT') AS draft_count
  FROM core.counseling_template t
  LEFT JOIN core.counseling_template_version p
    ON p.template_id = t.id AND p.status = 'PUBLISHED'
 ORDER BY t.title_en;

-- name: CounselingTemplateByCode :one
SELECT id, code, title_en, title_bn, retired_at FROM core.counseling_template WHERE code = $1;

-- name: CounselingTemplateByID :one
SELECT id, code, title_en, title_bn, retired_at FROM core.counseling_template WHERE id = $1;

-- name: CounselingVersions :many
SELECT template_id, version, status, notes,
       created_at, created_by, published_at, published_by, published_source,
       retired_at, approved_at, approved_by
  FROM core.counseling_template_version
 WHERE template_id = $1 ORDER BY version DESC;

-- name: CounselingVersion :one
SELECT template_id, version, status, notes,
       created_at, created_by, published_at, published_by, published_source,
       retired_at, approved_at, approved_by
  FROM core.counseling_template_version
 WHERE template_id = $1 AND version = $2;

-- name: PublishedCounselingVersion :one
-- What a new session gets. At most one exists -- a unique index says so -- because two would
-- make "which checklist" a question with two answers.
SELECT template_id, version, status, notes,
       created_at, created_by, published_at, published_by, published_source,
       retired_at, approved_at, approved_by
  FROM core.counseling_template_version
 WHERE template_id = $1 AND status = 'PUBLISHED';

-- name: CounselingItems :many
-- One version's items, in the order the counsellor works through them. Joined to the room so a
-- screen can group by it without a second round trip -- the grouping is the flow (section 5.2).
SELECT i.template_id, i.version, i.item_code, i.ordering,
       i.text_en, i.text_bn, i.guidance_en, i.guidance_bn,
       i.is_mandatory, i.room,
       r.display_en AS room_en, r.display_bn AS room_bn,
       r.ordering AS room_ordering, r.station_code
  FROM core.counseling_item i
  JOIN core.counseling_room r ON r.room = i.room
 WHERE i.template_id = $1 AND i.version = $2
 ORDER BY i.ordering;

-- name: NextCounselingVersion :one
SELECT coalesce(max(version), 0) + 1 AS next FROM core.counseling_template_version
 WHERE template_id = $1;

-- name: CreateCounselingTemplate :one
INSERT INTO core.counseling_template (code, title_en, title_bn, created_by)
VALUES ($1, $2, $3, $4)
RETURNING id, code, title_en, title_bn, retired_at;

-- name: CreateCounselingVersion :exec
INSERT INTO core.counseling_template_version (template_id, version, status, notes, created_by)
VALUES ($1, $2, 'DRAFT', $3, $4);

-- name: ReplaceCounselingItems :exec
-- A draft's items are replaced wholesale rather than patched. An authoring UI sends the list it
-- is showing, and a partial update would leave the stored version disagreeing with the screen
-- the author is looking at -- which is how somebody publishes an item they thought they deleted.
DELETE FROM core.counseling_item WHERE template_id = $1 AND version = $2;

-- name: AddCounselingItem :exec
INSERT INTO core.counseling_item
  (template_id, version, item_code, ordering, text_en, text_bn,
   guidance_en, guidance_bn, is_mandatory, room)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: RetirePublishedCounselingVersion :exec
-- Two statements rather than one, and the reason is a real bug rather than taste.
--
-- As a single statement with a data-modifying CTE, the retire and the publish run against the
-- same snapshot: the unique index that allows one published version per template still sees the
-- old row as PUBLISHED when the new one is set, and the publish fails. The transaction is what
-- makes the pair atomic; the statement boundary is what makes them sequential.
UPDATE core.counseling_template_version
   SET status = 'RETIRED', retired_at = now()
 WHERE template_id = $1 AND status = 'PUBLISHED';

-- name: PublishCounselingVersion :exec
UPDATE core.counseling_template_version
   SET status = 'PUBLISHED', published_at = now(), published_by = $3,
       published_source = 'USER'
 WHERE template_id = $1 AND version = $2 AND status = 'DRAFT';

-- name: CounselingAssignments :many
SELECT a.id, a.template_id, t.code AS template_code, t.title_en, t.title_bn,
       a.code_system, a.code_version, a.code_prefix, a.priority
  FROM core.counseling_assignment a
  JOIN core.counseling_template t ON t.id = a.template_id
 WHERE a.retired_at IS NULL
 ORDER BY a.priority DESC, a.code_prefix;

-- name: TemplatesForCoding :many
-- Which checklists a recorded diagnosis calls for, best first.
--
-- Prefix matching rather than equality: ICD-10 groups a family under E11, and a rule per member
-- would be sixteen rows that drift apart. Only published versions come back, because a draft is
-- not something to hand a counsellor.
SELECT DISTINCT ON (t.id)
       t.id, t.code, t.title_en, t.title_bn, v.version, a.priority
  FROM core.counseling_assignment a
  JOIN core.counseling_template t ON t.id = a.template_id
  JOIN core.counseling_template_version v
    ON v.template_id = t.id AND v.status = 'PUBLISHED'
 WHERE a.retired_at IS NULL AND t.retired_at IS NULL
   AND a.code_system = $1 AND a.code_version = $2
   AND $3::text LIKE a.code_prefix || '%'
 ORDER BY t.id, a.priority DESC;
