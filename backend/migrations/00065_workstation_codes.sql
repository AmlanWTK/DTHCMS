-- A browser session names the workstation it was opened at (CP82, ADR-0021, D-71).
--
-- # The defect this closes
--
-- `eventstore.ActorFrom` refuses to build an attribution envelope without a device_id,
-- because [R-03] and D-46 say a clinical write is evidence and evidence names the machine
-- it came from. A tablet supplies one: it holds an Ed25519 key in Android's Keystore and
-- signs every request (CP18, ADR-0013). A browser cannot — anywhere a browser could keep a
-- private key is reachable by any script that gets onto the page, which is why ADR-0010
-- already forbids keeping a session token there.
--
-- The consequence was not theoretical. Every clinical write from the web was refused with
-- DEVICE_REQUIRED, on the surface CP32 built as the registration desk's primary one, and
-- the only way anybody measured the prescription editor was a signing proxy held outside
-- the application (scratch/cp81/device-proxy.mjs, now deleted).
--
-- ADR-0021 decides that a browser session is **bound at sign-in to an enrolled workstation,
-- named rather than proven**. An administrator enrols the desk's computer as a `desktop`
-- device; the enrolment mints a short code — `FRD-REG-1` — which is printed and stuck to
-- the monitor; the person types it at sign-in; the server resolves it to a device in their
-- own facility and records it on the session row.
--
-- # The honesty this rests on, and why the schema carries it
--
-- The workstation is *named*, not *authenticated*. Somebody who types another desk's code
-- produces an event attributed to the wrong machine. That is accepted knowingly, and it is
-- the reason the code grants nothing: it opens no door, confers no permission, and every
-- event it appears on also names the person, whose credential *is* authenticated.
--
-- But an attribution that is sometimes proof and sometimes a claim, with nothing recording
-- which, is worse than either — it invites a later reader to treat a typed code as a
-- signature. So `core.session.device_binding` records *how* the device was established,
-- and it is NOT NULL exactly when a device is present. The three-way coherence is a CHECK
-- rather than a habit of the application, because the one state that must be impossible —
-- a device with no account of how it got there — is the state a future refactor produces
-- by forgetting a field.
--
-- # Why 'PROVEN' is restricted to a tablet or a phone
--
-- A signature proves a key was present, and a key is only a device identity when it lives
-- somewhere the operating system will not hand out: Android's Keystore, and nothing in a
-- browser (ADR-0013). `desktop` is the kind this checkpoint introduces for machines that
-- have no such place. Invariant 126 therefore refuses to let any session record PROVEN
-- against a desktop, so that "named" can never be laundered into "proven" by a code path
-- that sets the stronger value because the weaker one was inconvenient.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The workstation code
-- ---------------------------------------------------------------------------

ALTER TABLE core.device
  ADD COLUMN workstation_code text;

COMMENT ON COLUMN core.device.workstation_code IS
  'The printed label on the monitor (FRD-REG-1). Not a secret and not a credential: it '
  'names a desk so a browser session can say which one it was opened at (ADR-0021).';

-- The shape. Short enough to type without looking, structured enough that a person reading
-- an audit line a year later can tell a workstation code from an employee code or a patient
-- id at a glance. Letters and digits only, upper case only: a code that is stuck to a
-- monitor and typed by somebody in a hurry must not have a case, a space or a punctuation
-- mark in it that can be got wrong.
ALTER TABLE core.device
  ADD CONSTRAINT device_workstation_code_shape CHECK (
    workstation_code IS NULL
    OR workstation_code ~ '^[A-Z]{2,4}(-[A-Z0-9]{1,6}){1,3}$');

-- Only a desktop may carry one, and this is the constraint that stops the whole mechanism
-- being quietly inverted.
--
-- A tablet's device_id is evidence: it is derived from a signature only that tablet's
-- Keystore could produce. If a tablet could also carry a workstation code, then a person
-- who could read the code off the tablet's own screen could sign in from a browser and
-- produce events attributed to the *tablet* — turning a claim into something that reads,
-- downstream and forever, exactly like proof. The strength of an attribution would then
-- depend on which of two doors the session came through, which is unknowable from the row.
ALTER TABLE core.device
  ADD CONSTRAINT device_workstation_code_is_a_desktop CHECK (
    workstation_code IS NULL OR kind = 'desktop');

-- One code per desk, per facility. Partial, because the overwhelming majority of devices
-- are tablets with no code at all and NULLs must not collide with each other.
--
-- This index is also the *allocator*: core.assign_workstation_code below does not check
-- whether a candidate is free, it tries to take it and retries when the database says no.
-- Uniqueness that is checked in the application is uniqueness under a race; uniqueness
-- enforced here holds no matter how many administrators are enrolling at once.
CREATE UNIQUE INDEX device_workstation_code_idx
  ON core.device (facility_id, workstation_code)
  WHERE workstation_code IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Minting a code
--
-- Codes are generated, never typed by the administrator. Two reasons, both learned
-- elsewhere in this schema: a human-chosen identifier drifts into meaning ("REG-1" at one
-- desk and "reg1" at another, which the unique index treats as different desks), and a
-- human-chosen identifier is chosen *while looking at the list*, which is a race.
--
-- The ordinal is per facility and per station, so the second registration desk is FRD-REG-2
-- rather than FRD-REG-1 with a suffix nobody can read aloud down a phone line.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.workstation_prefix(p_facility uuid) RETURNS text
LANGUAGE plpgsql STABLE AS $$
DECLARE
  raw text;
  candidate text;
BEGIN
  SELECT upper(btrim(code)) INTO raw FROM core.facility WHERE id = p_facility;
  IF raw IS NULL THEN
    RAISE EXCEPTION 'no such facility: %', p_facility;
  END IF;

  -- The clinic's code is 'DTHC-FRD': the organisation, then the town. The town is what is
  -- printed on the monitor, because every code in this building would otherwise start with
  -- the same four characters and the part that distinguishes them would be the part people
  -- stop reading. So the last alphabetic segment wins, and the first is the fallback for a
  -- facility code that has only one.
  SELECT t.s INTO candidate
    FROM unnest(string_to_array(regexp_replace(raw, '[^A-Z-]', '', 'g'), '-'))
         WITH ORDINALITY AS t(s, ord)
   WHERE t.s ~ '^[A-Z]{2,4}$'
   ORDER BY t.ord DESC
   LIMIT 1;

  IF candidate IS NULL THEN
    candidate := left(regexp_replace(raw, '[^A-Z]', '', 'g'), 4);
  END IF;
  IF length(candidate) < 2 THEN
    -- A facility whose code carries no letters at all. Refused rather than padded: a code
    -- that does not name its clinic is worse than an enrolment that stops and asks.
    RAISE EXCEPTION 'facility % has no code a workstation prefix can be derived from', p_facility
      USING HINT = 'core.facility.code must contain a segment of two to four letters, '
                   'such as the FRD in DTHC-FRD.';
  END IF;
  RETURN candidate;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.workstation_prefix(uuid) IS
  'The facility half of a workstation code, derived from core.facility.code.';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assign_workstation_code(p_device uuid, p_station text)
RETURNS text
LANGUAGE plpgsql AS $$
DECLARE
  facility uuid;
  device_kind text;
  existing text;
  prefix text;
  station text;
  ordinal integer;
  candidate text;
BEGIN
  SELECT facility_id, kind, workstation_code
    INTO facility, device_kind, existing
    FROM core.device WHERE id = p_device;
  IF facility IS NULL THEN
    RAISE EXCEPTION 'no such device: %', p_device;
  END IF;
  -- Idempotent. A desk's code is printed and stuck to its monitor, so re-enrolling the same
  -- machine — a reinstalled browser, a replaced hard disk keeping the name — must not
  -- silently invalidate the label somebody has been typing for a year.
  IF existing IS NOT NULL THEN
    RETURN existing;
  END IF;
  IF device_kind <> 'desktop' THEN
    -- The CHECK would refuse the UPDATE anyway. Saying it here says *why*, which is the
    -- difference between an administrator reading "a tablet cannot be a workstation" and
    -- reading "new row violates check constraint device_workstation_code_is_a_desktop".
    RAISE EXCEPTION 'a % cannot be given a workstation code', device_kind
      USING HINT = 'Only a desktop is named by a printed code (ADR-0021). A tablet proves '
                   'its identity with a signature and needs no label.';
  END IF;

  prefix  := core.workstation_prefix(facility);
  station := upper(regexp_replace(coalesce(p_station, ''), '[^A-Za-z0-9]', '', 'g'));
  station := left(station, 6);
  IF length(station) = 0 THEN
    station := 'WS';
  END IF;

  -- Take the next free ordinal by *trying* it. The loop is the honest form: reading the
  -- maximum and adding one is correct only until two administrators enrol at the same
  -- moment, and the failure then is not a duplicate — it is a unique-violation thrown in
  -- the face of whoever was second, in the middle of enrolling a desk.
  FOR ordinal IN 1..999 LOOP
    candidate := prefix || '-' || station || '-' || ordinal::text;
    BEGIN
      UPDATE core.device SET workstation_code = candidate WHERE id = p_device;
      RETURN candidate;
    EXCEPTION WHEN unique_violation THEN
      -- Taken, by this station or by a concurrent enrolment. Try the next one.
      NULL;
    END;
  END LOOP;

  RAISE EXCEPTION 'no free workstation code for station % at facility %', station, facility
    USING HINT = 'There are already 999 workstations at this station name, which is far '
                 'more likely to be a loop in the caller than a clinic.';
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION core.assign_workstation_code(uuid, text) IS
  'Mints and takes the next free workstation code for a desktop. Uniqueness comes from '
  'device_workstation_code_idx, not from a read-then-write in the application.';

-- ---------------------------------------------------------------------------
-- How a session's device was established
-- ---------------------------------------------------------------------------

ALTER TABLE core.session
  ADD COLUMN device_binding text;

COMMENT ON COLUMN core.session.device_binding IS
  'How the session''s device was established: PROVEN (a signature, CP18) or NAMED (a '
  'typed workstation code, ADR-0021). NULL exactly when there is no device.';

ALTER TABLE core.session
  ADD CONSTRAINT session_device_binding_known CHECK (
    device_binding IS NULL OR device_binding IN ('PROVEN', 'NAMED'));

-- Existing sessions, backfilled before the constraint can be validated against them.
--
-- Every session with a device today got it from CP18's signed headers, so PROVEN is the
-- truthful value — except where the device is a desktop. CP81's measurement proxy enrolled
-- a `desktop` with a real key and signed with it, and those sessions really were proved.
-- They are recorded as NAMED anyway, because invariant 126 forbids PROVEN on a desktop and
-- the safe direction to be wrong in is the one that understates an attribution. Overstating
-- it is the failure this whole column exists to prevent.
UPDATE core.session s
   SET device_binding = CASE WHEN d.kind IN ('tablet', 'phone') THEN 'PROVEN' ELSE 'NAMED' END
  FROM core.device d
 WHERE s.device_id = d.id AND s.device_binding IS NULL;

-- The three-way coherence, as one constraint rather than two.
--
-- Written as an equality between two null-tests because that is the whole rule and it
-- cannot be half-satisfied: no device means no binding, and a device means a binding. The
-- state this forbids is a session that names a machine without saying whether the machine
-- was proved or merely claimed — which is precisely the row a later reader would have to
-- guess about, and would guess in the direction that makes the audit trail look stronger
-- than it is.
ALTER TABLE core.session
  ADD CONSTRAINT session_device_binding_coherent CHECK (
    (device_id IS NULL) = (device_binding IS NULL));

-- ---------------------------------------------------------------------------
-- Invariant 60, amended: an enrolled workstation holds no key, and must not
-- ---------------------------------------------------------------------------
--
-- `assert_device_keys_sound` (00010) says an active device has exactly one live key, on the
-- reasoning that an active device with no key cannot sign anything and is therefore a row
-- the code never writes. That reasoning was complete when every device was a tablet.
--
-- A workstation is the first active device that is *supposed* to be unable to sign. There is
-- no key to give it: the whole decision in ADR-0021 is that a browser has nowhere to keep
-- one, so enrolling a desk is a registration rather than a key exchange — the administrator
-- creates it, the database mints its code, and the code is printed. Leaving it `pending`
-- instead would be worse than the exemption: `pending` means "a code has been issued and no
-- key has arrived", and a desk whose key will never arrive would sit in that state forever,
-- indistinguishable from a tablet somebody forgot to finish enrolling.
--
-- The exemption is narrowed to exactly what it must cover, and the same statement adds the
-- converse, which is the part that carries the weight: **a device with a workstation code
-- must have no live key at all.** A machine that could both sign and be named by a printed
-- code would have two identities of different strengths under one device_id, and invariant
-- 126 — which reads the *kind* to decide whether PROVEN is honest — would be answering a
-- question about the wrong one.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_device_keys_sound() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  -- An active device with no live key cannot sign anything; a pending one with a key was
  -- enrolled without the status change. Both are states the code never writes, so a row in
  -- either is a bug or a hand edit — unless the device is a named workstation, which is
  -- active precisely because it will never hold a key (ADR-0021).
  SELECT string_agg(d.id::text, ', ') INTO offending
  FROM core.device d
  LEFT JOIN core.device_key k ON k.device_id = d.id AND k.retired_at IS NULL
  WHERE (d.status = 'active' AND k.id IS NULL AND d.workstation_code IS NULL)
     OR (d.status = 'pending' AND k.id IS NOT NULL);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'device status and live key disagree: %', offending
      USING HINT = 'An active device has exactly one live key; a pending one has none. A '
                   'named workstation (core.device.workstation_code) has none either, and '
                   'is the only device that may be active without one.';
  END IF;

  -- The converse, which is the load-bearing half. A workstation that could also sign would
  -- be two identities of different strengths behind one device_id.
  SELECT string_agg(d.id::text || ' (' || d.workstation_code || ')', ', ' ORDER BY d.id)
    INTO offending
    FROM core.device d
    JOIN core.device_key k ON k.device_id = d.id AND k.retired_at IS NULL
   WHERE d.workstation_code IS NOT NULL;

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'a named workstation holds a live signing key: %', offending
      USING HINT =
        'A workstation is named by a printed code, not proved by a signature. Giving one a key '
        'puts two claims of different strengths behind one device_id, and every reader '
        'downstream sees only the id. Retire the key or clear the workstation code; do not '
        'keep both.';
  END IF;

  -- The private key column that must never exist.
  SELECT string_agg(format('%s.%s', c.table_name, c.column_name), ', ') INTO offending
  FROM information_schema.columns c
  WHERE c.table_schema = 'core' AND c.table_name LIKE 'device%'
    AND (c.column_name LIKE '%private%' OR c.column_name LIKE '%secret%'
         OR (c.table_name = 'device_enrolment' AND c.column_name LIKE '%code%'
             AND c.column_name <> 'code_digest'));

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'a device secret appears to be stored in the clear: %', offending
      USING HINT = 'Private keys stay on the device; enrolment codes are digests.';
  END IF;
END
$$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Invariant 125: a workstation code names a desktop
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_a_workstation_code_names_a_desktop() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offenders text;
  guarded boolean;
BEGIN
  -- The constraint first. A data scan alone would pass the moment somebody dropped the
  -- CHECK and before anybody used the hole, which is the window in which a migration gets
  -- reviewed and merged. An invariant that only notices the second step is an invariant
  -- that reports the escalation after it has happened.
  SELECT EXISTS (
    SELECT 1 FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
      JOIN pg_namespace n ON n.oid = t.relnamespace
     WHERE n.nspname = 'core' AND t.relname = 'device'
       AND c.conname = 'device_workstation_code_is_a_desktop'
  ) INTO guarded;

  IF NOT guarded THEN
    RAISE EXCEPTION
      'core.device has lost the constraint that keeps a workstation code on a desktop'
      USING HINT =
        'device_workstation_code_is_a_desktop is what stops a tablet being given a printed '
        'code. A tablet''s device_id is evidence — derived from a signature only its Keystore '
        'could produce — so a code that resolved to a tablet would let anyone who can read '
        'that tablet''s screen produce browser events attributed to it, and every reader '
        'downstream would see proof rather than a claim (ADR-0021).';
  END IF;

  SELECT string_agg(id::text || ' (' || kind || ', ' || workstation_code || ')', ', ' ORDER BY id)
    INTO offenders
    FROM core.device
   WHERE workstation_code IS NOT NULL AND kind <> 'desktop';

  IF offenders IS NOT NULL THEN
    RAISE EXCEPTION 'a workstation code is attached to something that is not a desktop: %', offenders
      USING HINT =
        'Only a desktop is named by a printed code. These rows let a typed code resolve to a '
        'device whose attribution is otherwise cryptographic, which silently promotes a claim '
        'to evidence. Clear workstation_code on them; never widen the constraint.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_a_workstation_code_names_a_desktop() FROM PUBLIC;

-- ---------------------------------------------------------------------------
-- Invariant 126: a named device is never recorded as proven
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_a_named_device_is_never_recorded_as_proven() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offenders text;
  guarded boolean;
BEGIN
  SELECT EXISTS (
    SELECT 1 FROM pg_constraint c
      JOIN pg_class t ON t.oid = c.conrelid
      JOIN pg_namespace n ON n.oid = t.relnamespace
     WHERE n.nspname = 'core' AND t.relname = 'session'
       AND c.conname = 'session_device_binding_coherent'
  ) INTO guarded;

  IF NOT guarded THEN
    RAISE EXCEPTION
      'core.session has lost the constraint that keeps device_id and device_binding coherent'
      USING HINT =
        'session_device_binding_coherent is what makes "a device with no account of how it was '
        'established" impossible. Without it a session can name a machine while saying nothing '
        'about whether the machine was proved or merely typed, and the reader who finds that row '
        'in an incident has to guess (ADR-0021).';
  END IF;

  -- The escalation this prevents, stated as the query prevents it: a desktop is a machine
  -- with nowhere to keep a key an operating system will not hand out (ADR-0013). Whatever
  -- a session from one presented, it did not prove the machine. A row here would mean the
  -- application had recorded a typed label as a signature — after which nothing downstream
  -- can tell the difference, because the difference is exactly this column.
  SELECT string_agg(s.id::text || ' (device ' || d.id::text || ', kind ' || d.kind || ')',
                    ', ' ORDER BY s.id)
    INTO offenders
    FROM core.session s
    JOIN core.device d ON d.id = s.device_id
   WHERE s.device_binding = 'PROVEN' AND d.kind NOT IN ('tablet', 'phone');

  IF offenders IS NOT NULL THEN
    RAISE EXCEPTION 'a session records PROVEN for a device that cannot prove anything: %', offenders
      USING HINT =
        'PROVEN means the server checked an Ed25519 signature made by a key in secure storage '
        '(CP18, ADR-0013). A desktop has no such storage: its identity is a code printed on the '
        'monitor, which anybody can read and anybody can type. Recording PROVEN for one turns a '
        'named workstation into forensic evidence, which is the single misreading ADR-0021 exists '
        'to make impossible. The binding for a desktop is NAMED.';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION core.assert_a_named_device_is_never_recorded_as_proven() FROM PUBLIC;

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_a_workstation_code_names_a_desktop',
   'a printed workstation code names a desktop and never a device that signs', 125),
  ('assert_a_named_device_is_never_recorded_as_proven',
   'no session records a typed workstation as a proven device', 126)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- What the application may do
-- ---------------------------------------------------------------------------

-- The allocator runs as the application: enrolling a desk is an ordinary administrative
-- action through the API, not a DBA task. It is SECURITY INVOKER (the default), so the
-- UPDATE inside it is still subject to every grant and every constraint above.
GRANT EXECUTE ON FUNCTION core.assign_workstation_code(uuid, text) TO dthcms_app;
GRANT EXECUTE ON FUNCTION core.workstation_prefix(uuid) TO dthcms_app;

-- +goose Down

DELETE FROM ops.invariant
 WHERE function_name IN ('assert_a_workstation_code_names_a_desktop',
                         'assert_a_named_device_is_never_recorded_as_proven');

DROP FUNCTION IF EXISTS core.assert_a_named_device_is_never_recorded_as_proven();
DROP FUNCTION IF EXISTS core.assert_a_workstation_code_names_a_desktop();

-- assert_device_keys_sound goes back to 00010's text. It has to be restored *before* the
-- column it now reads is dropped, or the next verify raises "column workstation_code does
-- not exist" — a rollback that leaves the invariant set unrunnable is not a rollback.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_device_keys_sound() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  offending text;
BEGIN
  SELECT string_agg(d.id::text, ', ') INTO offending
  FROM core.device d
  LEFT JOIN core.device_key k ON k.device_id = d.id AND k.retired_at IS NULL
  WHERE (d.status = 'active' AND k.id IS NULL)
     OR (d.status = 'pending' AND k.id IS NOT NULL);

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'device status and live key disagree: %', offending
      USING HINT = 'An active device has exactly one live key; a pending one has none.';
  END IF;

  SELECT string_agg(format('%s.%s', c.table_name, c.column_name), ', ') INTO offending
  FROM information_schema.columns c
  WHERE c.table_schema = 'core' AND c.table_name LIKE 'device%'
    AND (c.column_name LIKE '%private%' OR c.column_name LIKE '%secret%'
         OR (c.table_name = 'device_enrolment' AND c.column_name LIKE '%code%'
             AND c.column_name <> 'code_digest'));

  IF offending IS NOT NULL THEN
    RAISE EXCEPTION 'a device secret appears to be stored in the clear: %', offending
      USING HINT = 'Private keys stay on the device; enrolment codes are digests.';
  END IF;
END
$$;
-- +goose StatementEnd
DROP FUNCTION IF EXISTS core.assign_workstation_code(uuid, text);
DROP FUNCTION IF EXISTS core.workstation_prefix(uuid);

ALTER TABLE core.session DROP CONSTRAINT IF EXISTS session_device_binding_coherent;
ALTER TABLE core.session DROP CONSTRAINT IF EXISTS session_device_binding_known;
ALTER TABLE core.session DROP COLUMN IF EXISTS device_binding;

DROP INDEX IF EXISTS core.device_workstation_code_idx;
ALTER TABLE core.device DROP CONSTRAINT IF EXISTS device_workstation_code_is_a_desktop;
ALTER TABLE core.device DROP CONSTRAINT IF EXISTS device_workstation_code_shape;
ALTER TABLE core.device DROP COLUMN IF EXISTS workstation_code;
