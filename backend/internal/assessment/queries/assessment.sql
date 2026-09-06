-- The lifestyle assessment (CP58, §3 step 3).
--
-- Everything here is either the questionnaire catalogue or one patient's answers to it. There is
-- deliberately no query that returns a *total*: the total is `core.instrument_total`, a function
-- over the item rows, so that a screen and a research extract cannot get different answers to the
-- same question.

-- name: UsableInstruments :many
-- What station 3 may actually run today.
--
-- The unusable ones come back too, with their licence note, because the absence of PHQ-9 from a
-- screen is a decision somebody made and a clinician who expected to find it deserves the sentence
-- rather than a gap. The client decides what to do with an unusable row; the server does not hide
-- it.
SELECT code, name_en, name_bn, purpose_en, purpose_bn,
       copyright_holder, licence_note, licence_note_bn, provenance,
       usable, domain, ordering
  FROM core.instrument
 WHERE retired_at IS NULL
 ORDER BY ordering, code;

-- name: CatalogueVersion :one
-- When the catalogue last changed.
--
-- A tablet holds the whole catalogue for a morning and works from it offline; without this the
-- only signal that a version was republished is a 422 on an item code at submit — after the
-- patient has answered. This lets a client re-check for the price of one small request.
SELECT coalesce(max(published_at), 'epoch'::timestamptz)::timestamptz AS catalogue_version
  FROM core.instrument_version
 WHERE published_at IS NOT NULL;

-- name: LatestInstrumentVersion :one
SELECT instrument_code, version, scoring, published_at, note
  FROM core.instrument_version
 WHERE instrument_code = $1 AND published_at IS NOT NULL
 ORDER BY version DESC
 LIMIT 1;

-- name: InstrumentItems :many
SELECT item_code, ordering, prompt_en, prompt_bn, answer_type, unit,
       min_value, max_value, required
  FROM core.instrument_item
 WHERE instrument_code = $1 AND version = $2
 ORDER BY ordering, item_code;

-- name: InstrumentOptions :many
SELECT item_code, option_code, ordering, label_en, label_bn, score
  FROM core.instrument_option
 WHERE instrument_code = $1 AND version = $2
 ORDER BY item_code, ordering, option_code;

-- name: LiveResponse :one
-- The current answers to one instrument for one patient. Superseded ones stay in the table; this
-- is what the screen and the score read.
SELECT r.id, r.patient_id, r.visit_id, r.instrument_code, r.instrument_version,
       r.recorded_at, r.recorded_by, r.recorded_role, r.station_code, r.device_id, r.source,
       r.status,
       coalesce(u.employee_code, '') AS recorded_by_code,
       coalesce(u.name_en, '')       AS recorded_by_name_en,
       coalesce(u.name_bn, '')       AS recorded_by_name_bn,
       core.instrument_total(r.id)   AS total
  FROM read.instrument_response r
  LEFT JOIN core.app_user u ON u.id = r.recorded_by
 WHERE r.patient_id = $1 AND r.instrument_code = $2 AND r.facility_id = $3
   AND r.status = 'ACTIVE';

-- name: ResponsesForPatient :many
SELECT r.id, r.patient_id, r.visit_id, r.instrument_code, r.instrument_version,
       r.recorded_at, r.recorded_by, r.recorded_role, r.station_code, r.device_id, r.source,
       r.status,
       coalesce(u.employee_code, '') AS recorded_by_code,
       coalesce(u.name_en, '')       AS recorded_by_name_en,
       coalesce(u.name_bn, '')       AS recorded_by_name_bn,
       core.instrument_total(r.id)   AS total
  FROM read.instrument_response r
  LEFT JOIN core.app_user u ON u.id = r.recorded_by
 WHERE r.patient_id = $1 AND r.facility_id = $2
 ORDER BY r.recorded_at DESC, r.instrument_code
 LIMIT $3;

-- name: AnswersFor :many
-- The raw items, which is the whole point of the table (criterion 1). The option's label is joined
-- so that a timeline can render "2–4 times a month" rather than `TWO_TO_FOUR_MONTHLY`.
SELECT a.item_code, a.option_code, a.value_num, a.value_bool, a.score,
       coalesce(o.label_en, '') AS option_en,
       coalesce(o.label_bn, '') AS option_bn,
       coalesce(i.prompt_en, '') AS prompt_en,
       coalesce(i.prompt_bn, '') AS prompt_bn,
       coalesce(i.ordering, 0)   AS item_ordering
  FROM read.instrument_answer a
  JOIN read.instrument_response r ON r.id = a.response_id
  LEFT JOIN core.instrument_item i
    ON i.instrument_code = r.instrument_code AND i.version = r.instrument_version
   AND i.item_code = a.item_code
  LEFT JOIN core.instrument_option o
    ON o.instrument_code = r.instrument_code AND o.version = r.instrument_version
   AND o.item_code = a.item_code AND o.option_code = a.option_code
 WHERE a.response_id = $1
 ORDER BY item_ordering, a.item_code;
