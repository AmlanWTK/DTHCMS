-- The signature, and the thing a stranger can check (CP84, CP85, docs/signing.md).
--
-- # The one sentence this migration exists for
--
-- **A prescription cannot be in a signed state without a signature that covers it.**
--
-- CP80 already made a signed prescription unchangeable: `core.prescription_is_frozen()` refuses
-- a content change, `core.prescription_item_is_frozen()` lets a line be written only while the
-- sheet is DRAFT, and `dthcms_app` holds SELECT and nothing else on both tables. CP83 added the
-- clearance gate. All three say *the database will not let you change it*.
--
-- What they cannot say is *nobody did*. They are promises made by the same system that holds the
-- data, and a reader who does not trust that system has no way to check them. The signature is
-- the part that survives distrust: a canonical serialisation of the clinical facts, signed by a
-- key the application cannot read, verifiable by anybody holding the public half.
--
-- So this migration does two things and refuses to do a third:
--
--  1. It gives a signature somewhere to live that nothing can rewrite — `read.prescription_
--     signature`, insert-once, with an UPDATE trigger that refuses every column.
--  2. It makes the signature **structurally required**: a deferred constraint trigger refuses a
--     commit that leaves a prescription in SIGNED, PRINTED or DISPENSED with no signature row.
--     Deferred, because the transition and the signature arrive from one event in one
--     transaction and neither can be written first.
--  3. It does **not** re-implement CP83's clearance gate. That trigger is still there, still on
--     entry to the same three statuses, and `core.assert_the_qa_gate_is_wired` still checks it.
--     Two gates on one door written by two checkpoints is how one of them quietly stops being
--     the one that fires.
--
-- # Why the public key is stored beside the signature
--
-- Verification must not depend on a key register's *current* shape. A key rotated, retired or
-- moved to a managed service next year must not make a prescription signed this morning
-- unverifiable — and a verifier that looked the key up by id would be exactly that. The 32 bytes
-- that verify this signature are written next to it, once, and the key id travels too so the
-- provenance question ("which key was that") stays answerable.
--
-- That is not a weakening. A public key is public; what makes the signature evidence is that the
-- *private* half was never in this database and never in this process's memory for longer than
-- one signing call.
--
-- # Why the signer kind is a column and not an inference
--
-- `docs/signing.md` §2: there is no key management service yet, the local signer's key is a file,
-- and the acceptance criterion "the signing key is non-exportable" is **not met by it and cannot
-- be**. The seam exists so CP03 can arrive as configuration; `signer_kind` exists so that after
-- CP03 nobody has to infer, from a date or a key id format, which prescriptions carry the weaker
-- guarantee. `core.signer_kind` is read-only to the application for the reason `core.qa_rule_kind`
-- is: a row naming a signer nothing implements is a claim no code backs.
--
-- # The verification token, and why the digest is what is stored
--
-- CP85's QR carries an opaque token — 160 bits from crypto/rand, never patient data, never
-- derived from the prescription id. What this table keeps is its **SHA-256 digest**, the same
-- shape `core.short_token` keeps a session challenge in, and for the same reason: somebody who
-- reads this table cannot mint the QR code that is on a piece of paper in a patient's hand.
-- The lookup is by digest, which is a unique index, so it is one probe rather than a scan.
--
-- # The attempt log is deliberately thin
--
-- `ops.prescription_verification_attempt` is the only table in this system written by an
-- unauthenticated caller. It carries the digest that was presented, the outcome, and a digest of
-- the client address. **No patient, no drug, no name, no raw address.** An abuse investigation
-- needs to group attempts and to know what was tried; it does not need to know who the
-- prescription was for, and a public endpoint that wrote a patient id into a table on every scan
-- would be a PHI leak wearing an audit trail's clothes.

-- +goose Up

-- ---------------------------------------------------------------------------
-- 1. The signer catalogue
-- ---------------------------------------------------------------------------

CREATE TABLE core.signer_kind (
  kind        text PRIMARY KEY,
  facility_id uuid REFERENCES core.facility(id),
  -- non_exportable records the property the acceptance criterion asks for, as a fact about the
  -- *kind* rather than as a claim in a commit message. LOCAL is false and honestly so.
  non_exportable boolean NOT NULL,
  display_en  text NOT NULL,
  display_bn  text NOT NULL,
  note        text NOT NULL
);

INSERT INTO core.signer_kind (kind, non_exportable, display_en, display_bn, note) VALUES
  ('LOCAL', false,
   'Local development key', 'স্থানীয় ডেভেলপমেন্ট কী',
   'An Ed25519 key held in this deployment''s configuration. It proves the prescription has not '
   'been altered since it was signed. It does NOT satisfy "the signing key is non-exportable": '
   'the key is a file, anybody who can read the configuration can copy it, and a copied key can '
   'sign. Refused outside local, test and dev by a check the service makes about itself at boot '
   '(docs/signing.md §2).'),
  ('MANAGED', true,
   'Managed signing service', 'ব্যবস্থাপিত স্বাক্ষর সেবা',
   'A key held by a key management service, which signs on request and never releases the '
   'private half. Arrives with CP03; the row exists now so that the vocabulary does not change '
   'when it does.')
ON CONFLICT (kind) DO NOTHING;

GRANT SELECT ON core.signer_kind TO dthcms_app, dthcms_projector;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON core.signer_kind FROM dthcms_app;

COMMENT ON TABLE core.signer_kind IS
  'Which implementation of the Signer seam produced a signature (CP84, docs/signing.md §2). '
  'Read-only to the application: a signature naming a signer nothing implements is a claim no '
  'code backs.';

-- ---------------------------------------------------------------------------
-- 2. The signature
-- ---------------------------------------------------------------------------

CREATE TABLE read.prescription_signature (
  -- One signature per prescription, and the prescription's own id is the key. There is no
  -- re-signing: a signed prescription that was wrong is corrected, which is a new prescription
  -- with its own signature (docs/signing.md §7).
  prescription_id uuid PRIMARY KEY REFERENCES read.prescription(id),
  facility_id     uuid NOT NULL REFERENCES core.facility(id),

  -- ---- what was signed ------------------------------------------------
  --
  -- canonical_version is the serialisation the signature was made over, and it travels with the
  -- signature rather than being assumed. This is the whole answer to the checkpoint's named
  -- risk: a change to the canonical form must not silently invalidate every historical
  -- prescription, so verification picks the version the signature names.
  canonical_version int   NOT NULL CHECK (canonical_version >= 1),
  -- The SHA-256 of the canonical bytes. Stored so that "did the canonical form change" and "did
  -- the signature fail" are two answerable questions rather than one indistinguishable failure.
  canonical_sha256  bytea NOT NULL CHECK (octet_length(canonical_sha256) = 32),

  -- ---- who signed it, and with what -----------------------------------
  algorithm  text  NOT NULL CHECK (algorithm = 'Ed25519'),
  signer_kind text NOT NULL REFERENCES core.signer_kind(kind),
  key_id     text  NOT NULL CHECK (btrim(key_id) <> ''),
  -- 32 bytes of Ed25519 public key, by value. See the header: verification must not depend on a
  -- key register's current shape.
  public_key bytea NOT NULL CHECK (octet_length(public_key) = 32),
  signature  bytea NOT NULL CHECK (octet_length(signature) = 64),

  signed_at timestamptz NOT NULL,
  signed_by uuid        NOT NULL,

  -- ---- the circumstances of the signing -------------------------------
  --
  -- device_assurance is ADR-0021's PROVEN/NAMED, recorded rather than required
  -- (docs/signing.md §4). Signing demands a step-up second factor and does **not** demand a
  -- proven device; recording the assurance is what preserves the option to demand one later
  -- with the evidence already collected, rather than starting the count from the day somebody
  -- changes their mind. NONE is legal and means the session named no device at all.
  device_assurance text NOT NULL CHECK (device_assurance IN ('PROVEN', 'NAMED', 'NONE')),
  -- ---- the clearance, by value ----------------------------------------
  --
  -- The QA decision that permitted this signature, copied onto the signature rather than joined
  -- to at verification time.
  --
  -- **Both columns, and by value, because verification must read nothing mutable.** The canonical
  -- form covers the clearance — "this prescription was cleared before it was signed" is part of
  -- what the signature attests — so the two values used to compute the bytes have to be the two
  -- values available when they are recomputed. A verifier that looked the clearance up again
  -- would be verifying against whatever station 10's record says *now*: a second CLEARED decision
  -- recorded later, a `decided_at` rewritten by a backfill or a projection replay, and a
  -- prescription nobody touched fails verification and is reported as altered. That is an
  -- accusation produced by a join.
  --
  -- This is the house pattern and there are two precedents next door. CP80 copies the captured
  -- price onto the prescription item instead of joining to the formulary, so that what the
  -- patient was quoted stays what the patient was quoted after the price list moves. CP82 stores
  -- an AI suggestion exactly as it was offered and never mutates it, so that "what was the
  -- physician shown" has an answer. Both for this reason: a record whose meaning depends on a row
  -- somebody can still edit is not a record.
  --
  -- NOT NULL, because `Sign` refuses a prescription with no standing clearance and CP83's trigger
  -- refuses the transition underneath it. The invariant is real, so it is a constraint rather
  -- than a guard in Go that can never fire.
  qa_review_id  uuid        NOT NULL,
  qa_cleared_at timestamptz NOT NULL,

  -- ---- CP85's token ---------------------------------------------------
  --
  -- Two columns, and the split is the whole of what makes the QR both safe and reprintable.
  --
  -- `verification_token_digest` is what a presented token is looked up by: a unique index, one
  -- probe, and a value from which the token cannot be recovered.
  --
  -- `verification_token_sealed` is the token itself, **sealed** with the secret ring (ADR-0012),
  -- exactly as a TOTP seed is. It exists because a prescription is printed more than once — a
  -- patient loses the paper, a pharmacy keeps a copy — and every printing must carry the same
  -- QR. Digest-only storage would have made a reprint impossible, which is the kind of defect
  -- that is discovered on a Tuesday morning at a counter rather than in a test.
  --
  -- What that costs, stated plainly: somebody who holds **both** this database and the ring key
  -- can mint a working QR code for any signed prescription. That is a strictly smaller set than
  -- "anybody who can read this table", which is what storing the token in clear would have
  -- meant, and it is the same trade the second factor already makes for its seeds.
  verification_token_digest bytea NOT NULL UNIQUE
    CHECK (octet_length(verification_token_digest) = 32),
  verification_token_sealed bytea NOT NULL CHECK (octet_length(verification_token_sealed) > 0),
  verification_token_key_id text  NOT NULL CHECK (btrim(verification_token_key_id) <> ''),

  event_id   uuid   NOT NULL UNIQUE,
  global_seq bigint NOT NULL
);

CREATE INDEX prescription_signature_by_facility
  ON read.prescription_signature (facility_id, signed_at DESC);
-- Which prescriptions carry the weaker guarantee, answerable as a query rather than by
-- inference. docs/signing.md §2 is explicit that this must be possible if the pilot ever runs
-- before CP03.
CREATE INDEX prescription_signature_by_signer_kind
  ON read.prescription_signature (signer_kind, signed_at DESC);

GRANT SELECT ON read.prescription_signature TO dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON read.prescription_signature TO dthcms_projector;

COMMENT ON TABLE read.prescription_signature IS
  'The physician''s cryptographic signature over a canonical serialisation of one prescription '
  '(CP84). Written once by the projection and never updated. This is not the signature image: '
  'that is a picture for a human to read and has no cryptographic role (docs/signing.md §6).';

-- +goose StatementBegin
-- A signature is written once.
--
-- An explicit refusal of every UPDATE rather than a list of immutable columns, because there is
-- no column here that has a legitimate second value. A signature whose key id could be corrected
-- afterwards would be a signature whose provenance is a matter of opinion.
CREATE OR REPLACE FUNCTION core.prescription_signature_is_written_once() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    -- The rebuild's escape hatch, exactly as `core.prescription_is_frozen()` has one and for
    -- the same reason: what this protects is the projection, and the ledger behind it cannot be
    -- rewritten at all, so a table somebody emptied rebuilds into what it was.
    IF coalesce(current_setting('dthcms.rebuilding', true), '') = 'on' THEN
      RETURN OLD;
    END IF;
    RAISE EXCEPTION 'a prescription signature is never deleted (prescription %)', OLD.prescription_id
      USING HINT = 'There is no unsigning. A signed prescription that was wrong is corrected.';
  END IF;

  RAISE EXCEPTION 'a prescription signature is written once (prescription %)', OLD.prescription_id
    USING HINT = 'Nothing about a signature has a second value. Correct the prescription instead.';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER prescription_signature_written_once
  BEFORE UPDATE OR DELETE ON read.prescription_signature
  FOR EACH ROW EXECUTE FUNCTION core.prescription_signature_is_written_once();

-- +goose StatementBegin
-- A prescription in a signed state has a signature.
--
-- **Deferred**, and that is not laxity. The transition and the signature come from one event
-- applied by one projection inside one transaction, and neither can be written first: the
-- signature references `read.prescription(id)` and the prescription's status is what makes the
-- signature required. Checking at commit is the only point at which both halves exist.
--
-- What it closes is the hole CP83's gate leaves open: clearance says the sheet was *allowed* to
-- be signed, and says nothing about whether anything actually signed it. Without this, a
-- transition event written by hand — a support script, a second client, a replay — would move a
-- prescription to SIGNED with no signature and nothing would notice until somebody scanned the
-- QR.
CREATE OR REPLACE FUNCTION core.prescription_signed_state_has_a_signature() RETURNS trigger
LANGUAGE plpgsql
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  IF NEW.status NOT IN ('SIGNED', 'PRINTED', 'DISPENSED') THEN
    RETURN NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM read.prescription_signature s WHERE s.prescription_id = NEW.id) THEN
    RETURN NULL;
  END IF;
  RAISE EXCEPTION 'prescription % is % and carries no signature', NEW.id, NEW.status
    USING HINT = 'A prescription enters a signed state only through CP84''s signing path, which '
                 'writes the signature in the same transaction.';
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER prescription_signed_state_has_a_signature_trigger
  AFTER INSERT OR UPDATE ON read.prescription
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION core.prescription_signed_state_has_a_signature();

-- ---------------------------------------------------------------------------
-- 3. The public endpoint's log
-- ---------------------------------------------------------------------------

CREATE TABLE ops.prescription_verification_attempt (
  id uuid PRIMARY KEY,
  -- Null when the token matched nothing. An attempt against a token this clinic never issued
  -- belongs to no facility, and inventing one would put a stranger's typing in a clinic's
  -- records.
  facility_id uuid REFERENCES core.facility(id),
  -- Null for the same reason, and **only ever set when the token resolved**. This is the one
  -- column that could turn a public endpoint into a patient-linkage oracle, so it is written
  -- from the resolved signature row and never from anything the caller sent.
  prescription_id uuid REFERENCES read.prescription(id),

  -- What was presented, as a digest. Storing the token would put a working QR code in a table.
  presented_digest bytea NOT NULL CHECK (octet_length(presented_digest) = 32),
  -- VERIFIED, NOT_VERIFIED or RATE_LIMITED. Three words and no detail: "which field failed" is
  -- a hint to somebody forging, and the person holding the paper needs only the verdict.
  outcome text NOT NULL CHECK (outcome IN ('VERIFIED', 'NOT_VERIFIED', 'RATE_LIMITED')),

  -- A digest of the caller's address, so that abuse can be grouped without keeping addresses.
  -- Honest about what it is: an IPv4 address has 2^32 possibilities and a digest of one is
  -- reversible by anybody willing to spend a minute on it. It is kept because grouping is what
  -- an abuse investigation needs and the alternative — the address in clear — is worse.
  client_digest bytea NOT NULL CHECK (octet_length(client_digest) = 32),

  at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX prescription_verification_attempt_by_day
  ON ops.prescription_verification_attempt (at DESC);
CREATE INDEX prescription_verification_attempt_by_client
  ON ops.prescription_verification_attempt (client_digest, at DESC);

-- **In `ops` and not in `read`, and not in the ledger.** Not `read`, because every row there
-- arrives through a projection and `core.assert_read_models_derived()` enforces exactly that —
-- an attempt is not derived from anything, it is a thing that happened. Not the ledger, because
-- appending there is a clinical act and an unauthenticated caller must not be able to make one:
-- a QR scanned a thousand times would otherwise put a thousand rows into the clinical event
-- stream. `ops` is where the things that happen *to* this system rather than inside its clinic
-- already live — jobs, sync batches, idempotency records — and this belongs with them.
GRANT SELECT, INSERT ON ops.prescription_verification_attempt TO dthcms_app;
REVOKE UPDATE, DELETE, TRUNCATE ON ops.prescription_verification_attempt FROM dthcms_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ops.prescription_verification_attempt TO dthcms_projector;

COMMENT ON TABLE ops.prescription_verification_attempt IS
  'One scan of a prescription QR code (CP85). Carries no patient, no drug and no name: the '
  'public endpoint is the system''s only unauthenticated surface besides login and is treated '
  'as hostile.';

-- +goose StatementBegin
-- An attempt is never rewritten. The same argument as the signature: a log somebody can edit is
-- not a log.
CREATE OR REPLACE FUNCTION core.verification_attempt_is_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' AND coalesce(current_setting('dthcms.rebuilding', true), '') = 'on' THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'a verification attempt is append-only (id %)', OLD.id;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER verification_attempt_append_only
  BEFORE UPDATE OR DELETE ON ops.prescription_verification_attempt
  FOR EACH ROW EXECUTE FUNCTION core.verification_attempt_is_append_only();

-- ---------------------------------------------------------------------------
-- 4. The projection
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
-- One event, two rows: the transition and the signature.
--
-- `PRESCRIPTION_SIGNED` v2 carries both, and they are applied together because they are one
-- fact. Splitting them into two events — which is what CP83 did for its findings, deliberately —
-- would have been wrong here: a transition to SIGNED with the signature in a second event is
-- exactly the state the deferred trigger above exists to refuse, and a projection that produced
-- it for one instant inside a transaction would be relying on the ordering of two appends.
--
-- Idempotent, like every projection here. The signature insert is ON CONFLICT DO NOTHING on the
-- prescription id, and the transition is `read.apply_prescription_transition`'s own no-op when
-- the row is already there.
CREATE OR REPLACE FUNCTION read.apply_prescription_signed(p jsonb) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = read, core, pg_catalog
AS $$
BEGIN
  -- A v1 `PRESCRIPTION_SIGNED` carried no signature, because before CP84 there was no signing
  -- path to produce one — the transition existed and nothing drove it. Such an event is applied
  -- as the transition alone rather than being rejected or having a signature invented for it,
  -- and the deferred trigger above is what refuses the resulting state at commit. That is the
  -- correct order of events: the ledger keeps what was written, and the constraint refuses the
  -- projection it would produce.
  IF coalesce(p->>'signature', '') = '' THEN
    PERFORM read.apply_prescription_transition(p);
    RETURN;
  END IF;

  INSERT INTO read.prescription_signature (
    prescription_id, facility_id, canonical_version, canonical_sha256,
    algorithm, signer_kind, key_id, public_key, signature,
    signed_at, signed_by, device_assurance, qa_review_id, qa_cleared_at,
    verification_token_digest, verification_token_sealed, verification_token_key_id,
    event_id, global_seq)
  VALUES (
    (p->>'prescription_id')::uuid,
    (p->>'facility_id')::uuid,
    (p->>'canonical_version')::int,
    decode(p->>'canonical_sha256', 'hex'),
    p->>'algorithm',
    p->>'signer_kind',
    p->>'key_id',
    decode(p->>'public_key', 'hex'),
    decode(p->>'signature', 'hex'),
    (p->>'at')::timestamptz,
    (p->>'actor_id')::uuid,
    p->>'device_assurance',
    (p->>'qa_review_id')::uuid,
    (p->>'qa_cleared_at')::timestamptz,
    decode(p->>'verification_token_digest', 'hex'),
    decode(p->>'verification_token_sealed', 'hex'),
    p->>'verification_token_key_id',
    (p->>'event_id')::uuid,
    (p->>'global_seq')::bigint)
  ON CONFLICT (prescription_id) DO NOTHING;

  PERFORM read.apply_prescription_transition(p);
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION read.apply_prescription_signed(jsonb) TO dthcms_projector;

-- ---------------------------------------------------------------------------
-- 5. Invariants
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_signed_prescription_carries_a_signature() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.prescription p
   WHERE p.status IN ('SIGNED', 'PRINTED', 'DISPENSED')
     AND NOT EXISTS (SELECT 1 FROM read.prescription_signature s
                      WHERE s.prescription_id = p.id);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% prescriptions are in a signed state with no signature', offenders
      USING HINT = 'The deferred trigger prescription_signed_state_has_a_signature_trigger '
                   'should have made this impossible; if it fires, that trigger is gone.';
  END IF;

  -- The other direction: a signature on a prescription that is not signed. A draft with a
  -- signature row would mean something signed a sheet that is still being written.
  SELECT count(*) INTO offenders
    FROM read.prescription_signature s
    JOIN read.prescription p ON p.id = s.prescription_id
   WHERE p.status NOT IN ('SIGNED', 'PRINTED', 'DISPENSED', 'CANCELLED', 'CORRECTED');
  IF offenders > 0 THEN
    RAISE EXCEPTION '% signatures sit on prescriptions that were never signed', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The gate this checkpoint depends on and did not build.
--
-- CP83's clearance trigger is what makes "a prescription that has not cleared cannot be signed"
-- true, and CP84 deliberately did not write a second one. This assertion is the thing that
-- notices if it goes away — because the failure mode of depending on somebody else's trigger is
-- that it is dropped in a refactor and the depending checkpoint's tests all still pass.
CREATE OR REPLACE FUNCTION core.assert_signing_still_sits_behind_the_clearance_gate() RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger t
      JOIN pg_class c ON c.oid = t.tgrelid
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'read' AND c.relname = 'prescription'
       AND t.tgname = 'prescription_needs_qa_clearance_trigger'
       AND NOT t.tgisinternal)
  THEN
    RAISE EXCEPTION 'CP83''s clearance gate is not on read.prescription'
      USING HINT = 'CP84 signs only what CP83 cleared, and relies on that trigger rather than '
                   'duplicating it. Without it a prescription can be signed uncleared.';
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger t
      JOIN pg_class c ON c.oid = t.tgrelid
      JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'read' AND c.relname = 'prescription_signature'
       AND t.tgname = 'prescription_signature_written_once'
       AND NOT t.tgisinternal)
  THEN
    RAISE EXCEPTION 'read.prescription_signature can be rewritten';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A signature says which signer made it, and the catalogue says what that signer guarantees.
--
-- The point of this one is docs/signing.md §2's honesty clause: if the pilot ever runs before
-- CP03, the clinic must be able to say exactly which prescriptions carry a key that is a file.
-- An assertion rather than a comment, because the question is asked after something has gone
-- wrong and a comment is not queryable.
CREATE OR REPLACE FUNCTION core.assert_every_signature_names_a_signer_that_exists() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM read.prescription_signature s
   WHERE NOT EXISTS (SELECT 1 FROM core.signer_kind k WHERE k.kind = s.signer_kind);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% signatures name a signer the catalogue does not know', offenders;
  END IF;

  -- A verification attempt that claims it verified something names the prescription it
  -- verified. The reverse — a NOT_VERIFIED attempt with no prescription — is the normal case
  -- and is not checked.
  SELECT count(*) INTO offenders
    FROM ops.prescription_verification_attempt a
   WHERE a.outcome = 'VERIFIED' AND a.prescription_id IS NULL;
  IF offenders > 0 THEN
    RAISE EXCEPTION '% verification attempts report success without naming a prescription', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_every_signed_prescription_carries_a_signature',
   'every prescription in a signed state carries exactly one signature, and no unsigned prescription carries one', 139),
  ('assert_signing_still_sits_behind_the_clearance_gate',
   'CP83''s clearance trigger and the signature''s write-once trigger are both still attached', 140),
  ('assert_every_signature_names_a_signer_that_exists',
   'every signature names a signer the catalogue knows, and every successful verification names what it verified', 141)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- +goose Down

DELETE FROM ops.invariant WHERE function_name IN (
  'assert_every_signed_prescription_carries_a_signature',
  'assert_signing_still_sits_behind_the_clearance_gate',
  'assert_every_signature_names_a_signer_that_exists');
DROP FUNCTION IF EXISTS core.assert_every_signature_names_a_signer_that_exists();
DROP FUNCTION IF EXISTS core.assert_signing_still_sits_behind_the_clearance_gate();
DROP FUNCTION IF EXISTS core.assert_every_signed_prescription_carries_a_signature();

DROP FUNCTION IF EXISTS read.apply_prescription_signed(jsonb);

DROP TRIGGER IF EXISTS verification_attempt_append_only ON ops.prescription_verification_attempt;
DROP FUNCTION IF EXISTS core.verification_attempt_is_append_only();
DROP TABLE IF EXISTS ops.prescription_verification_attempt;

DROP TRIGGER IF EXISTS prescription_signed_state_has_a_signature_trigger ON read.prescription;
DROP FUNCTION IF EXISTS core.prescription_signed_state_has_a_signature();
DROP TRIGGER IF EXISTS prescription_signature_written_once ON read.prescription_signature;
DROP FUNCTION IF EXISTS core.prescription_signature_is_written_once();
DROP TABLE IF EXISTS read.prescription_signature;

DROP TABLE IF EXISTS core.signer_kind;
