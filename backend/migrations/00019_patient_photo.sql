-- Patient photographs (CP34, blueprint §3 Step 1).
--
-- The bytes are not here. They are in object storage, in the `identifier` data class, and
-- this table holds the reference and the provenance — who took it, when, on what device,
-- and what it replaced.
--
-- Three decisions worth reading:
--
-- **A photograph is replaced, never overwritten.** A new capture is a new object and a new
-- row; the old row stays with `replaced_at` set. Overwriting would mean a chart printed last
-- month showing a face that is no longer the one in the record, with nothing to explain it.
--
-- **The object key is derived, not chosen.** `patients/<uuid>/<capture uuid>.jpg`, built by
-- the server. A key a client could choose is a key that can be pointed at somebody else's
-- photograph, and a correctly signed URL would then serve it.
--
-- **The digest is stored.** A photograph that silently changes in storage is worse than one
-- that is missing, because nothing announces it. `sha256` is checked when the object is
-- attached and is available to anything that wants to check it later.

-- +goose Up

CREATE TABLE core.patient_photo (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  facility_id uuid        NOT NULL REFERENCES core.facility(id),
  patient_id  uuid        NOT NULL REFERENCES core.patient(id) ON DELETE RESTRICT,

  -- Where the bytes are. The class is stored as well as the key so a reader does not have
  -- to know the convention to find the object.
  object_class text       NOT NULL DEFAULT 'identifier'
               CHECK (object_class IN ('identifier', 'document', 'derived')),
  object_key   text       NOT NULL,

  content_type text       NOT NULL CHECK (content_type IN ('image/jpeg', 'image/png', 'image/webp')),
  byte_size    integer    NOT NULL CHECK (byte_size > 0 AND byte_size <= 8388608),
  -- Checked at attach time. A photograph that silently changes in storage is worse than
  -- one that is missing, because nothing announces it.
  sha256       bytea      NOT NULL CHECK (length(sha256) = 32),

  width        integer    CHECK (width IS NULL OR width > 0),
  height       integer    CHECK (height IS NULL OR height > 0),

  captured_by  uuid       NOT NULL REFERENCES core.app_user(id),
  captured_at  timestamptz NOT NULL DEFAULT now(),
  device_id    uuid       REFERENCES core.device(id),
  event_id     uuid       NOT NULL,

  -- A replacement points back at what it replaced, and the replaced row records when.
  replaces_id  uuid       REFERENCES core.patient_photo(id),
  replaced_at  timestamptz,

  created_at   timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT patient_photo_key_unique UNIQUE (object_key),
  CONSTRAINT patient_photo_not_itself CHECK (replaces_id IS DISTINCT FROM id)
);

-- One live photograph per patient. A partial unique index rather than a column, so the
-- history stays in the same table as the current one.
CREATE UNIQUE INDEX patient_photo_current ON core.patient_photo (patient_id)
  WHERE replaced_at IS NULL;

CREATE INDEX patient_photo_by_patient ON core.patient_photo (patient_id, captured_at DESC);

COMMENT ON TABLE core.patient_photo IS
  'The reference to a patient''s photograph in object storage, and its provenance. The bytes are never in the database (CP34).';
COMMENT ON COLUMN core.patient_photo.object_key IS
  'Derived by the server, never chosen by a client: a key a client picks is a key that can be pointed at somebody else''s photograph.';

-- ---------------------------------------------------------------------------
-- The invariant
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_photos_are_referenced_not_stored() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending bigint;
BEGIN
  -- The failure this catches is somebody adding a `bytes bytea` column in a hurry. A
  -- photograph in the database is a photograph in every backup, every replica and every
  -- `pg_dump` an engineer takes to debug something — none of which are in the identifier
  -- class's residency boundary (D-01).
  SELECT count(*) INTO offending
  FROM information_schema.columns
  WHERE table_schema = 'core' AND table_name = 'patient_photo'
    AND data_type = 'bytea' AND column_name <> 'sha256';

  IF offending > 0 THEN
    RAISE EXCEPTION 'core.patient_photo has % binary column(s) besides the digest', offending
      USING HINT = 'Photograph bytes belong in object storage, in the identifier data class. '
                   'The database holds the key (CP34).';
  END IF;

  SELECT count(*) INTO offending
  FROM core.patient_photo
  WHERE object_key = '' OR object_key LIKE '%..%';

  IF offending > 0 THEN
    RAISE EXCEPTION 'patient photographs with unusable object keys: % row(s)', offending;
  END IF;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assert_photos_are_referenced_not_stored() IS
  'Raises if photograph bytes have found their way into the database (CP34, D-01).';

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_photos_are_referenced_not_stored', 'patient photographs are references, not bytes', 33)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description,
  sequence    = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name = 'assert_photos_are_referenced_not_stored';
DROP FUNCTION IF EXISTS core.assert_photos_are_referenced_not_stored();
DROP TABLE IF EXISTS core.patient_photo;
