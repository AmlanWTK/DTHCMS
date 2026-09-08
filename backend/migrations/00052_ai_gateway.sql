-- The AI gateway (CP70, §10.3, D-07, D-08).
--
-- # What this schema is for
--
-- §10.3 says the gateway is *"the only path to any model"*, and the reason it is built before any
-- agent is that ten agents are coming (§7.2). PHI minimisation, cost metering, versioning and
-- auditability are either inherited by all ten or retrofitted into all ten, and the second of those
-- does not happen.
--
-- Four rules are made structural here rather than left to the Go that will call this.
--
--  1. **No identifier reaches a provider.** `ops.carries_identifier` refuses a payload that names a
--     person, whether the name is a JSON key or a phone number buried in a sentence of clinical
--     prose. It guards the outbound column, so the gateway physically cannot record having sent
--     one — and since the record is written before the call, a payload that cannot be recorded is a
--     payload that is never sent.
--  2. **A free-tier credential never sees a real patient.** The check constraint on
--     `core.ai_interaction` refuses the row outright, and invariant 99 re-checks the whole table,
--     because a constraint added later does not validate what is already there. D-07's table of
--     Google's terms is the argument: the free tier trains on what it is given and human reviewers
--     read it, and Google's own instruction is *"do not submit sensitive, confidential, or personal
--     information to the Unpaid Services"*.
--  3. **Every call is recorded, including the ones that failed.** A call that errored is exactly
--     the one somebody wants to see afterwards, so the row is written before the provider is
--     contacted and updated when it answers. A crashed process leaves an `IN_FLIGHT` row, which is
--     a worse-looking and more honest outcome than no row at all.
--  4. **Model versions are pinned.** `core.ai_model` holds explicit versions and their prices.
--     D-13: no floating aliases, which matters more on Gemini than most, because preview and
--     free-tier aliases are retired quickly (D-07).
--
-- # Why the synthetic register exists, and what it is really doing
--
-- Criterion 1b is *"a real-patient payload cannot be sent on a free-tier credential"*. The obvious
-- shape — a boolean on the request saying "this one is synthetic" — fails in the way that matters:
-- an agent that forgets the flag sends real patient data on a free credential and nothing notices.
-- A flag a caller sets is a claim nobody checks.
--
-- So there is no such flag anywhere in this design. `core.ai_synthetic_subject` is a register of
-- record ids that somebody **deliberately entered**, and the gateway resolves provenance by looking
-- the subject up: found means fabricated, absent means real, and a lookup that errors means real.
-- The default is therefore the safe answer, and the unsafe one has to be argued for by an act that
-- leaves a row with a reason and an author on it.
--
-- The residual risk, stated plainly: whoever can write to this register can lie to it, and a real
-- patient's id entered here would open the free tier to that patient's data. That is a deliberate,
-- attributed, reviewable act rather than a forgotten boolean — which is the whole of the
-- improvement, and it is a real one. What it is not is a proof. `docs/ai-gateway.md` names the two
-- further mitigations: the register is written only by the synthetic-data loader, and the free tier
-- is refused outside local, test and dev regardless of what the register says.

-- +goose Up

-- ---------------------------------------------------------------------------
-- The PHI key list learns to say which rule it belongs to
-- ---------------------------------------------------------------------------

-- `ops.phi_key` (00048) is the database's copy of `logging.PHIKeys`, and until now every consumer
-- wanted the same answer for every key on it: never, anywhere.
--
-- The gateway cannot want that. An AI payload's whole purpose is to carry clinical content — a
-- diagnosis is exactly what §7.1's synthesis is summarising — so a rule that refused every key here
-- would refuse every payload the AI framework exists to send, and the feature would be built around
-- the check rather than through it. What must be refused is narrower and sharper: anything that
-- says *which person* this is.
--
-- The obvious move is a second list of identifier keys. It is the wrong one, and silently so: a key
-- added to the logging list and forgotten on the gateway's list is an identifier the gateway
-- happily forwards. One list, with a class on each row, cannot drift that way — and the Go map has
-- the same shape, compared against this table in both directions by the same test that has been
-- comparing the guidance strings since CP69.
ALTER TABLE ops.phi_key ADD COLUMN IF NOT EXISTS class text NOT NULL DEFAULT 'IDENTIFIER';

ALTER TABLE ops.phi_key DROP CONSTRAINT IF EXISTS phi_key_class_known;
ALTER TABLE ops.phi_key ADD CONSTRAINT phi_key_class_known
  CHECK (class IN ('IDENTIFIER', 'CLINICAL', 'CREDENTIAL'));

COMMENT ON COLUMN ops.phi_key.class IS
  'IDENTIFIER: says which person this is; never leaves the boundary. CLINICAL: what is wrong with them; banned from logs and job arguments, permitted in a minimised AI payload because summarising it is what the model is for. CREDENTIAL: grants access; belongs nowhere.';

UPDATE ops.phi_key SET class = 'CLINICAL'   WHERE key IN ('diagnosis', 'prescription');
UPDATE ops.phi_key SET class = 'CREDENTIAL' WHERE key IN ('password', 'token', 'secret', 'otp', 'totp_secret');

-- ---------------------------------------------------------------------------
-- Identifiers hiding in prose
-- ---------------------------------------------------------------------------

-- The key check catches `{"patient": {"phone": "01711-234567"}}`. It does nothing at all about
-- `{"note": "rang her son Rafiq on 01711-234567"}`, and free text is where the plan says the
-- leakage will actually happen: *"PHI leakage through free-text fields is the main one"*.
--
-- So the patterns are a table for the same reason the key list is: a constraint cannot read a Go
-- variable, and the two copies must be held together by a test rather than by hope. The pattern
-- strings are written in the intersection of PostgreSQL's advanced regular expressions and Go's
-- RE2 — character classes, bounded repetition, non-capturing groups, `(?i)` at the front, and
-- nothing else — so that the *same string* compiles and behaves identically in both engines. A test
-- runs a fixture corpus through both and fails if they ever disagree, which is the only version of
-- "one source of truth" that is worth the words.
--
-- These are deliberately wide. Redacting a laboratory accession number because it is nine digits
-- long costs the model a little context; letting a national ID through costs a patient their
-- privacy and the clinic its compliance with D-01. Default deny (D-08) means the first is the
-- acceptable error.
--
-- What patterns cannot do is names. "Rafiq" is a word; there is no expression that separates it
-- from a drug or a district. Three things stand in for that gap and none of them closes it: the
-- caller's own identifiers are struck out literally (the strip-and-restore mechanism), the
-- honorific pattern below catches the very common "Md. X" / "Mst. Y" spelling used in Bangladesh,
-- and every outbound payload is stored for a person to read. The residual risk is a bare given name
-- of a third party in clinical prose, and it is written down rather than papered over.
CREATE TABLE ops.pii_pattern (
  kind text PRIMARY KEY,
  -- Valid in both PostgreSQL ARE and Go RE2. See the note above; a pattern using a feature of one
  -- engine and not the other is a rule that is enforced in one place and believed in two.
  pattern text NOT NULL,
  -- What a match is replaced with on the way out. Distinct per kind so that a human reading the
  -- outbound log can see *what sort* of thing was removed without seeing the thing.
  --
  -- A leading `${1}` is Go's back-reference to the pattern's first capture group, and it is here
  -- because neither engine can express a word boundary in a spelling the other understands:
  -- PostgreSQL has `\y`, Go has `\b`, and a pattern using either is a rule enforced in one place
  -- and merely believed in the other. The portable form is to *consume* the preceding character in
  -- a group and put it back on replacement. The database only ever asks whether a string matches,
  -- so it never reads this column; Go hands it straight to ReplaceAllString. One mechanism, no
  -- special case, and the cross-engine corpus test covers the seam.
  replacement text NOT NULL,

  description_en text NOT NULL,
  description_bn text NOT NULL,

  CONSTRAINT pii_pattern_format CHECK (kind ~ '^[a-z][a-z0-9_]{2,63}$'),
  CONSTRAINT pii_pattern_replacement_is_a_token
    CHECK (replacement ~ '^(\$\{1\})?\[[A-Z_]+\]$'),
  CONSTRAINT pii_pattern_bilingual
    CHECK (btrim(description_en) <> '' AND btrim(description_bn) <> '')
);

GRANT SELECT ON ops.pii_pattern TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('ops', 'pii_pattern', 'A list of shapes a phone number can take is not a clinic''s data.')
ON CONFLICT DO NOTHING;

INSERT INTO ops.pii_pattern (kind, pattern, replacement, description_en, description_bn) VALUES
  -- Seven or more digits with nothing between them. A national ID is ten, thirteen or seventeen;
  -- a mobile number without separators is eleven. Nothing clinical is seven digits long: a
  -- haemoglobin is three characters, a date has no run longer than four.
  ('digit_run_latin', '[0-9]{7,}', '[NUMBER]',
   'A run of seven or more digits: a national ID, a mobile number, an account number',
   'সাতটি বা তার বেশি সংখ্যার ধারা: জাতীয় পরিচয়পত্র, মোবাইল নম্বর বা হিসাব নম্বর'),

  -- The same thing typed in Bangla numerals, which is how a registration desk writes it half the
  -- time. A scrubber that only understood Latin digits would pass every one of those through.
  ('digit_run_bangla', '[০-৯]{7,}', '[NUMBER]',
   'A run of seven or more Bangla digits',
   'সাতটি বা তার বেশি বাংলা সংখ্যার ধারা'),

  -- Nine or more digits with spaces, dots, brackets, plus or hyphen between them: "01711-234567",
  -- "+880 1711 234567", "(0171) 123 4567". Nine rather than seven because a clinical narrative
  -- legitimately contains short number sequences — "BP 120 80, pulse 72" is eight digits — and a
  -- rule that redacted those would return a summary the physician cannot read.
  ('separated_number_latin', '(?:[0-9][ .()+-]{0,2}){9,}', '[NUMBER]',
   'Nine or more digits separated by spaces, dots, brackets, plus or hyphen: a written-out phone number',
   'ফাঁকা, বিন্দু, বন্ধনী, যোগ বা হাইফেন দিয়ে লেখা নয় বা তার বেশি সংখ্যা: হাতে লেখা ফোন নম্বর'),

  ('separated_number_bangla', '(?:[০-৯][ .()+-]{0,2}){9,}', '[NUMBER]',
   'The same, written in Bangla numerals',
   'একই জিনিস, বাংলা সংখ্যায় লেখা'),

  ('email', '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+[.][A-Za-z]{2,}', '[EMAIL]',
   'An email address',
   'একটি ইমেইল ঠিকানা'),

  -- The one name pattern that is worth having. Bangladeshi names are very commonly written with an
  -- honorific attached — "Md. Rafiqul Islam", "Mst. Ayesha" — and the honorific is the part a
  -- machine can see. It will also strike out "Dr. Nahid" wherever a note mentions the consultant,
  -- which is over-redaction and the correct direction to err in.
  --
  -- The leading `(^|[^a-z])` is a consumed word boundary, put back by the `${1}` on the
  -- replacement. Without it the pattern matches the "ms " at the end of "symptoms include" and
  -- returns "sympto[NAME]" to the model, which is the kind of over-redaction that makes a
  -- clinician stop trusting the summary rather than merely lose a little context.
  ('honorific_name', '(?i)(^|[^a-z])(mr|mrs|ms|md|mst|dr|prof)[.]? +[a-z]+', '${1}[NAME]',
   'A name written with an honorific: Md., Mst., Dr., Mr.',
   'সম্মানসূচক পদবি সহ লেখা নাম: মো., মোসা., ডা., জনাব')
ON CONFLICT (kind) DO UPDATE SET
  pattern = EXCLUDED.pattern, replacement = EXCLUDED.replacement,
  description_en = EXCLUDED.description_en, description_bn = EXCLUDED.description_bn;

-- Whether a string carries something that names a person.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.text_carries_identifier(t text) RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT t IS NOT NULL AND EXISTS (SELECT 1 FROM ops.pii_pattern WHERE t ~ pattern);
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION ops.text_carries_identifier(text) TO dthcms_app;

-- Whether a JSON document carries something that names a person: an identifier-class or
-- credential-class key anywhere in it, or a string value matching one of the patterns above.
--
-- Deliberately *not* `ops.carries_phi`, and the difference is the point of the class column. A
-- minimised AI payload is allowed to say `{"diagnosis": "type 2 diabetes"}` — that is what it is
-- for — and is not allowed to say `{"phone": …}` or to hide `01711-234567` inside a sentence.
--
-- Recursive over objects and arrays, because `{"visit": {"patient": {"phone": …}}}` hides it two
-- levels down and a check that only looked at the top level would pass exactly the payload somebody
-- was most likely to write.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.carries_identifier(p jsonb) RETURNS boolean
LANGUAGE plpgsql STABLE AS $$
DECLARE
  k text;
  v jsonb;
BEGIN
  IF p IS NULL OR jsonb_typeof(p) = 'null' THEN
    RETURN false;
  END IF;
  IF jsonb_typeof(p) = 'string' THEN
    RETURN ops.text_carries_identifier(p #>> '{}');
  END IF;
  IF jsonb_typeof(p) = 'array' THEN
    FOR v IN SELECT value FROM jsonb_array_elements(p) LOOP
      IF ops.carries_identifier(v) THEN
        RETURN true;
      END IF;
    END LOOP;
    RETURN false;
  END IF;
  IF jsonb_typeof(p) <> 'object' THEN
    RETURN false;
  END IF;
  FOR k, v IN SELECT key, value FROM jsonb_each(p) LOOP
    -- The suffix match is the one the log handler and the job-argument check already do:
    -- `guardian_phone` and `patient_name` are the shapes a developer reaches for when the bare
    -- key feels wrong.
    IF EXISTS (SELECT 1 FROM ops.phi_key
                WHERE class <> 'CLINICAL'
                  AND (lower(k) = key OR lower(k) LIKE '%\_' || key)) THEN
      RETURN true;
    END IF;
    IF ops.carries_identifier(v) THEN
      RETURN true;
    END IF;
  END LOOP;
  RETURN false;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION ops.carries_identifier(jsonb) TO dthcms_app;

-- ---------------------------------------------------------------------------
-- Pinned models
-- ---------------------------------------------------------------------------

-- D-13, made a table: *"pin explicit model versions; never use floating aliases"*. On Gemini this
-- is not fastidiousness — preview and free-tier aliases are retired on a matter of weeks (D-07),
-- and a system that named `gemini-flash-latest` would one day summarise a patient with a model
-- nobody evaluated and record that it had used "latest".
--
-- Prices live here too, in **integer micro-dollars per million tokens**, because a cost record is
-- money and money in a float is a number that stops adding up. They are configuration rather than
-- code so that a price change is a migration somebody reviews rather than a constant somebody
-- forgot.
CREATE TABLE core.ai_model (
  -- The family, as the provider names it in a URL: gemini-2.5-flash.
  model text NOT NULL,
  -- The pinned version, as the provider names it: gemini-2.5-flash-001. Never an alias.
  model_version text NOT NULL,

  provider text NOT NULL CHECK (provider IN ('gemini', 'mock')),

  -- Per million tokens, in micro-dollars. $0.30/1M is 300000.
  input_micro_usd_per_million  bigint NOT NULL CHECK (input_micro_usd_per_million >= 0),
  output_micro_usd_per_million bigint NOT NULL CHECK (output_micro_usd_per_million >= 0),

  description_en text NOT NULL,
  description_bn text NOT NULL,

  retired_at timestamptz,

  PRIMARY KEY (model_version),
  CONSTRAINT ai_model_version_is_not_an_alias CHECK (
    model_version <> model
    AND model_version NOT LIKE '%latest%'
    AND model_version NOT LIKE '%preview%'),
  CONSTRAINT ai_model_bilingual
    CHECK (btrim(description_en) <> '' AND btrim(description_bn) <> '')
);

GRANT SELECT ON core.ai_model TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'ai_model', 'Which models this deployment may call is a property of the build, not of a clinic.')
ON CONFLICT DO NOTHING;

INSERT INTO core.ai_model (model, model_version, provider,
                           input_micro_usd_per_million, output_micro_usd_per_million,
                           description_en, description_bn) VALUES
  -- The mock's "version" is a version so that the pinning rule has nothing to make an exception
  -- for, and so a cost of zero is a fact on the row rather than a special case in the code.
  ('mock', 'mock-000', 'mock', 0, 0,
   'The in-process mock. Contacts nothing, costs nothing, answers the same way every time',
   'প্রক্রিয়ার ভিতরের নকল মডেল। কিছুর সঙ্গে যোগাযোগ করে না, খরচ নেই, প্রতিবার একই উত্তর দেয়'),

  -- Prices are the published list rates at the time of writing and are **a proposal to be checked
  -- against the first real invoice**, which is what D-14 says: *"this must be measured at CP71, not
  -- assumed"*.
  ('gemini-2.5-flash', 'gemini-2.5-flash-001', 'gemini', 300000, 2500000,
   'High volume, low risk: classification, extraction, digest assembly',
   'বেশি পরিমাণ, কম ঝুঁকির কাজ: শ্রেণিবিন্যাস, তথ্য নিষ্কাশন, সারসংক্ষেপ তৈরি'),

  ('gemini-2.5-pro', 'gemini-2.5-pro-001', 'gemini', 1250000, 10000000,
   'The pre-consultation synthesis, where quality decides whether the physician trusts the system',
   'পরামর্শ-পূর্ব সারসংক্ষেপ, যার মান ঠিক করে চিকিৎসক ব্যবস্থাটিকে বিশ্বাস করবেন কি না')
ON CONFLICT (model_version) DO UPDATE SET
  model = EXCLUDED.model, provider = EXCLUDED.provider,
  input_micro_usd_per_million = EXCLUDED.input_micro_usd_per_million,
  output_micro_usd_per_million = EXCLUDED.output_micro_usd_per_million,
  description_en = EXCLUDED.description_en, description_bn = EXCLUDED.description_bn;

-- ---------------------------------------------------------------------------
-- The agent catalogue and the prompt registry
-- ---------------------------------------------------------------------------

-- §10.5: *"prompts are versioned artefacts in the repository … deployed like code, reviewed like
-- code"*. The files under `internal/ai/prompts` are the source of truth; these two tables are the
-- deployed copy, so that an interaction row from eight months ago can be resolved to the exact text
-- that produced it even after the file has moved on four versions.
--
-- The synchronisation runs at start-up and is one-way: the repository writes, the database never
-- writes back. A version whose content hash differs from the stored one is refused rather than
-- overwritten, because a prompt that changed under a version number is the single change that would
-- make every stored interaction unreproducible, and it is exactly the change somebody makes in a
-- hurry.
CREATE TABLE core.ai_agent (
  agent_code text PRIMARY KEY,

  -- §10.1's taxonomy. Only GENERATIVE agents go through a model at all; the column exists so that
  -- an agent registered under the wrong technology is a refusal rather than a surprise, and so a
  -- reader of this table can see at a glance that the medication safety engine is not an LLM.
  technology text NOT NULL CHECK (technology IN ('GENERATIVE', 'RETRIEVAL', 'SPEECH')),

  description_en text NOT NULL,
  description_bn text NOT NULL,

  CONSTRAINT ai_agent_format CHECK (agent_code ~ '^[a-z][a-z0-9_.]{2,63}$'),
  CONSTRAINT ai_agent_bilingual
    CHECK (btrim(description_en) <> '' AND btrim(description_bn) <> '')
);

GRANT SELECT ON core.ai_agent TO dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'ai_agent', 'The list of agents this build contains is code, not a clinic''s records.')
ON CONFLICT DO NOTHING;

-- The one agent CP70 ships, and it is not one of §7.2's ten.
--
-- "Any specific agent" is out of scope for this checkpoint, and this is not one: it is the
-- gateway's own fixture — the thing the manual verification step invokes to read the recorded
-- outbound payload, and the thing the tests exercise the whole pipeline through. It is registered
-- here rather than created by a test so that a person following `docs/ai-gateway.md` on a fresh
-- database can invoke it, and so that a real agent added at CP71 is a second row rather than the
-- first one.
INSERT INTO core.ai_agent (agent_code, technology, description_en, description_bn) VALUES
  ('gateway.echo', 'GENERATIVE',
   'The gateway''s own test agent. Not a clinical agent: it summarises nothing and is shown to nobody',
   'গেটওয়ের নিজস্ব পরীক্ষামূলক এজেন্ট। এটি ক্লিনিকাল এজেন্ট নয়: কিছুর সারসংক্ষেপ করে না, কাউকে দেখানো হয় না')
ON CONFLICT (agent_code) DO UPDATE SET
  technology = EXCLUDED.technology,
  description_en = EXCLUDED.description_en, description_bn = EXCLUDED.description_bn;

CREATE TABLE core.ai_prompt_version (
  agent_code text NOT NULL REFERENCES core.ai_agent(agent_code),
  -- Semantic, and ordered by the three integers rather than by the string, so that 1.10.0 sorts
  -- after 1.9.0 instead of before it.
  version text NOT NULL,
  major integer NOT NULL CHECK (major >= 0),
  minor integer NOT NULL CHECK (minor >= 0),
  patch integer NOT NULL CHECK (patch >= 0),

  model_version text NOT NULL REFERENCES core.ai_model(model_version),
  -- The model to fall back to when the primary is failing. §10.4 A1 wants a Pro-class model for
  -- synthesis and a Flash-class one for volume; when Pro is down, a Flash answer clearly recorded
  -- as a fallback is better than no answer, and much better than a fabricated one (D-15).
  fallback_model_version text REFERENCES core.ai_model(model_version),

  -- The whole file, hashed. This is what makes an eight-month-old interaction reproducible: the
  -- version number says which prompt, and the hash says the file really is the one that was
  -- deployed under that number.
  content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
  -- And the file itself, because a hash tells you the prompt changed and not what it said. A few
  -- kilobytes per version, kept forever, is the cheapest audit record in this system.
  content text NOT NULL,

  -- Why this version exists. §10.5 requires a changelog; a version without one is a change nobody
  -- can review after the fact.
  changelog text NOT NULL,

  deployed_at timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (agent_code, version),
  CONSTRAINT ai_prompt_version_semver CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
  CONSTRAINT ai_prompt_version_parts_match_the_string
    CHECK (version = major || '.' || minor || '.' || patch),
  CONSTRAINT ai_prompt_version_says_why CHECK (btrim(changelog) <> ''),
  -- A prompt is not a place to put a patient. It is a template with slots; the values arrive at
  -- run time, already minimised. A literal identifier committed into a prompt file would be sent
  -- on every single invocation for as long as that version was deployed.
  CONSTRAINT ai_prompt_version_carries_no_identifier
    CHECK (NOT ops.text_carries_identifier(content)),
  CONSTRAINT ai_prompt_version_fallback_is_a_different_model
    CHECK (fallback_model_version IS NULL OR fallback_model_version <> model_version)
);

GRANT SELECT, INSERT ON core.ai_prompt_version TO dthcms_app;

-- Never updated and never deleted by the application. A prompt version is evidence about how a
-- clinical draft was produced; rewriting one silently rewrites the provenance of every interaction
-- that names it.
REVOKE UPDATE, DELETE ON core.ai_prompt_version FROM dthcms_app;

INSERT INTO core.facility_scope_exemption (schema_name, table_name, reason) VALUES
  ('core', 'ai_prompt_version', 'A prompt is a versioned artefact in the repository; it belongs to the build and not to a clinic.')
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Which record ids are fabricated
-- ---------------------------------------------------------------------------

-- The register criterion 1b rests on. See the note at the top of this file for why it is a register
-- somebody writes to rather than a flag a caller sets.
--
-- Two properties of the shape are load-bearing:
--
--   * **`reason` is required and long.** An entry without one is somebody clicking through, and the
--     whole value of the register is that entering a row is an act a reviewer can read.
--   * **The application may insert and may not delete.** A deletion would make a subject real
--     again, which is safe; but it would also erase the evidence that it had ever been treated as
--     synthetic, which is the record an audit of a free-tier leak would be looking for.
CREATE TABLE core.ai_synthetic_subject (
  subject_id uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  -- Deliberately not a foreign key to core.patient. The register must be able to hold the id of a
  -- fabricated subject that is not a patient row at all — a generated document, a load-test
  -- cohort — and, more importantly, a foreign key here would make "is this fabricated" a question
  -- about the patient table, which is the table this rule exists to be independent of.
  reason text NOT NULL,
  registered_by uuid REFERENCES core.app_user(id),
  registered_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT ai_synthetic_subject_reason_is_meaningful CHECK (length(btrim(reason)) >= 20)
);

GRANT SELECT, INSERT ON core.ai_synthetic_subject TO dthcms_app;
REVOKE UPDATE, DELETE ON core.ai_synthetic_subject FROM dthcms_app;

COMMENT ON TABLE core.ai_synthetic_subject IS
  'Record ids known to be fabricated. The AI gateway resolves provenance by looking a subject up here; absent means real, and a failed lookup means real. Never a flag on the request.';

-- ---------------------------------------------------------------------------
-- Every call
-- ---------------------------------------------------------------------------

CREATE TABLE core.ai_interaction (
  id uuid PRIMARY KEY,
  facility_id uuid NOT NULL REFERENCES core.facility(id),

  agent_code text NOT NULL REFERENCES core.ai_agent(agent_code),
  prompt_version text,
  -- The model that actually answered, which is not always the one the prompt named: a fallback
  -- records the model that produced the text rather than the one that was asked first.
  model_version text REFERENCES core.ai_model(model_version),
  requested_model_version text REFERENCES core.ai_model(model_version),

  -- Which credential this went out on. The whole of criterion 1b hangs off the constraint below.
  tier text NOT NULL CHECK (tier IN ('free', 'paid', 'mock')),
  -- How the gateway decided what this payload was about. Never taken from the request.
  provenance text NOT NULL CHECK (provenance IN ('SYNTHETIC', 'REAL_PATIENT', 'UNKNOWN')),

  -- The patient, when there is one. Nullable because an extraction from a document that has not
  -- been linked to anybody yet is still an AI call somebody must be able to audit.
  --
  -- **Deliberately not a foreign key**, for the same reason `core.ai_synthetic_subject` is not one.
  -- The subject of an AI call is whatever the calling agent named, and that is not always a row in
  -- `core.patient`: a records-extraction job names a document, a load test names a cohort, and the
  -- synthetic loader's fabricated subjects need not exist as patients at all. A foreign key here
  -- would make "what was this call about" a question about the patient table — and the effect
  -- would be to refuse the *record*, which is to say to refuse the call, for the payloads whose
  -- provenance is least clear. That is exactly backwards: the harder a subject is to place, the
  -- more the audit trail needs it written down.
  subject_patient_id uuid,
  -- The pseudonym that stood in for the subject in the payload. Stored so that two interactions
  -- about the same person can be related without either of them naming that person — which is what
  -- makes the outbound log reviewable at all.
  subject_pseudonym text NOT NULL DEFAULT '',

  status text NOT NULL CHECK (status IN (
    'IN_FLIGHT',      -- written before the provider is contacted; a crash leaves this behind
    'SUCCEEDED',
    'CACHED',         -- answered from a previous identical call; recorded, and not re-billed
    'INVALID_OUTPUT', -- the model answered, the answer failed its schema, retries were spent
    'PROVIDER_ERROR',
    'TIMEOUT',
    'CIRCUIT_OPEN',   -- not attempted: the breaker was open and there was no fallback
    'REFUSED_TIER',   -- criterion 1b: a real patient on a free credential
    'REFUSED_PHI')),  -- minimisation could not make this payload safe to send

  -- What was actually sent, after minimisation. The human-reviewable outbound log the plan names as
  -- the mitigation for its own headline risk. Guarded by the constraint below, so a payload that
  -- names a person cannot be recorded — and because the row is written *before* the call, a payload
  -- that cannot be recorded is a payload that is never sent.
  --
  -- Null for REFUSED_PHI: that is precisely the payload we have decided is unsafe, and storing it
  -- would move the leak from the provider to our own audit table. What is stored instead is
  -- `refusal_detail`, which names the key or pattern and never the value.
  outbound jsonb,
  -- What came back. Kept for reproducibility and for the evaluation set (CP72).
  response jsonb,
  refusal_detail text NOT NULL DEFAULT '',

  -- The hash the cache is keyed on: the minimised payload together with the prompt version, which
  -- itself pins the model version — a model change is a change to the prompt file, and therefore a
  -- new prompt version, and therefore a different hash. Never the raw input, which would key a
  -- cache on a patient's name and make the key itself a derived identifier.
  input_sha256 text NOT NULL CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),

  input_tokens  integer NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens integer NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  -- Integer micro-dollars. Money in a float is a number that stops adding up, and this one is
  -- summed per agent per day to decide whether an alert fires.
  cost_micro_usd bigint NOT NULL DEFAULT 0 CHECK (cost_micro_usd >= 0),

  latency_ms integer NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  -- Whether the answer passed the output schema. Three states rather than two: null means it was
  -- never validated because the call never got that far, and a dashboard that showed those as
  -- failures would report a provider outage as a quality problem.
  output_valid boolean,
  -- True when the primary model was unreachable and the registered fallback answered instead.
  used_fallback boolean NOT NULL DEFAULT false,

  started_at  timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,

  -- **Criterion 1b, as a constraint.** A free credential may carry a payload the register says is
  -- fabricated and nothing else. Not a warning, not a downgrade to the paid tier behind the
  -- caller's back — a refusal, at the point of record, so that the row cannot exist.
  CONSTRAINT ai_interaction_free_tier_is_synthetic_only CHECK (
    tier <> 'free' OR provenance = 'SYNTHETIC' OR status = 'REFUSED_TIER'),

  -- **Criterion 1, as a constraint.** The Go minimiser refuses first, so the error names the key;
  -- this catches a payload the minimiser had a hole for, and invariant 98 re-checks the whole table.
  CONSTRAINT ai_interaction_outbound_carries_no_identifier CHECK (
    outbound IS NULL OR NOT ops.carries_identifier(outbound)),

  -- **Criterion 2, as a constraint.** A call that reached a model and did not record which prompt
  -- and which model version produced it is a call nobody can reproduce, which is the one thing §10.6
  -- invariant 4 requires of every AI output in this system.
  CONSTRAINT ai_interaction_that_ran_names_its_versions CHECK (
    status NOT IN ('SUCCEEDED', 'CACHED', 'INVALID_OUTPUT')
    OR (prompt_version IS NOT NULL AND model_version IS NOT NULL)),

  CONSTRAINT ai_interaction_finished_says_when CHECK (
    (status = 'IN_FLIGHT') = (finished_at IS NULL)),

  -- A refusal that does not say what it objected to is a refusal somebody will work around by
  -- turning the check off.
  CONSTRAINT ai_interaction_refusal_says_why CHECK (
    status NOT IN ('REFUSED_TIER', 'REFUSED_PHI') OR btrim(refusal_detail) <> ''),

  -- And the payload we called unsafe is not kept. See the note on `outbound`.
  CONSTRAINT ai_interaction_refused_phi_keeps_no_payload CHECK (
    status <> 'REFUSED_PHI' OR outbound IS NULL),

  -- A cache hit is free by definition. If one ever costs money the accounting is wrong somewhere,
  -- and it is better to find that out here than in a monthly total nobody can explain.
  CONSTRAINT ai_interaction_a_cache_hit_is_free CHECK (
    status <> 'CACHED' OR cost_micro_usd = 0)
);

-- The outbound log, newest first, which is how a person reads it.
CREATE INDEX ai_interaction_recent ON core.ai_interaction (facility_id, started_at DESC);
-- The cache lookup: the most recent success for this exact input.
CREATE INDEX ai_interaction_cache ON core.ai_interaction (input_sha256, started_at DESC)
  WHERE status = 'SUCCEEDED';
-- The metering query: what one agent spent today.
CREATE INDEX ai_interaction_spend ON core.ai_interaction (facility_id, agent_code, started_at);
-- One patient's AI history, for the audit question "what has this system said about her".
CREATE INDEX ai_interaction_by_subject ON core.ai_interaction (subject_patient_id, started_at DESC)
  WHERE subject_patient_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE ON core.ai_interaction TO dthcms_app;
-- D-67 will decide retention. Until it does, nothing is deleted: an AI interaction is the evidence
-- for how a clinical draft came to say what it said, and a retention rule that removed it before
-- anybody had agreed one is a decision made by default.
REVOKE DELETE ON core.ai_interaction FROM dthcms_app;

-- ---------------------------------------------------------------------------
-- Budgets, and alerts that fire once
-- ---------------------------------------------------------------------------

-- D-14: *"meter every AI call … set a monthly budget with alerting at 60/80/100%"*.
--
-- The budget is a daily figure rather than a monthly one, and the reason is what an alert is for. A
-- monthly budget crossed on the 8th tells a clinic that the last three weeks are unfunded; a daily
-- one crossed at eleven in the morning tells them something changed today, while the change is
-- still findable. The monthly figure is the daily one multiplied out, and the operator screen shows
-- both.
CREATE TABLE core.ai_budget (
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  -- Null means "every agent together": the deployment-wide budget, which is the one that catches a
  -- runaway nobody predicted because it is in an agent nobody was watching.
  agent_code text REFERENCES core.ai_agent(agent_code),

  daily_micro_usd bigint NOT NULL CHECK (daily_micro_usd > 0),
  -- Percentages of the daily figure at which somebody is told. D-14 names 60, 80 and 100.
  thresholds integer[] NOT NULL DEFAULT ARRAY[60, 80, 100],

  -- The length is all a check constraint can say here: testing every element needs `unnest`, and
  -- PostgreSQL refuses a subquery in a CHECK. The rest of the rule — every element a percentage,
  -- in ascending order — is invariant 102, which may use one. Same division of labour as
  -- everywhere else in this schema: the constraint guards each row as it is written, the invariant
  -- re-reads the whole table afterwards.
  CONSTRAINT ai_budget_names_at_least_one_threshold CHECK (
    array_length(thresholds, 1) BETWEEN 1 AND 10)
);

-- One row per (facility, agent), with the deployment-wide row keyed on a null agent. A partial
-- unique index rather than a primary key, because a primary key cannot contain a nullable column
-- and the deployment-wide budget is exactly the row whose agent is null.
CREATE UNIQUE INDEX ai_budget_per_agent ON core.ai_budget (facility_id, agent_code)
  WHERE agent_code IS NOT NULL;
CREATE UNIQUE INDEX ai_budget_deployment_wide ON core.ai_budget (facility_id)
  WHERE agent_code IS NULL;

GRANT SELECT ON core.ai_budget TO dthcms_app;

-- Every crossing, once.
--
-- The unique key is the mechanism, not bookkeeping about it: the gateway inserts the row it wants
-- to alert about with `ON CONFLICT DO NOTHING`, and *whether a row was inserted* is what decides
-- whether anybody is told. Without it, the 80% alert fires on every call for the rest of the day —
-- and an alert that fires four hundred times is an alert somebody turns off, which is how a clinic
-- ends up with no alerting at all on the day it matters.
CREATE TABLE core.ai_budget_alert (
  facility_id uuid NOT NULL REFERENCES core.facility(id),
  -- '' rather than NULL for the deployment-wide budget, because this is a primary key and a null
  -- in one would make "the deployment-wide 80% alert already fired" un-expressible.
  agent_code text NOT NULL DEFAULT '',
  day date NOT NULL,
  threshold_percent integer NOT NULL CHECK (threshold_percent BETWEEN 1 AND 1000),

  -- What was actually spent when it crossed, so the alert can say a number rather than a percentage.
  spend_micro_usd bigint NOT NULL CHECK (spend_micro_usd >= 0),
  budget_micro_usd bigint NOT NULL CHECK (budget_micro_usd > 0),
  raised_at timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (facility_id, agent_code, day, threshold_percent)
);

GRANT SELECT, INSERT ON core.ai_budget_alert TO dthcms_app;
REVOKE UPDATE, DELETE ON core.ai_budget_alert FROM dthcms_app;

-- A budget for the deployment as a whole, seeded so that criterion 5 is true of a fresh database
-- rather than of one somebody remembered to configure. Ten US dollars a day is a **proposal**: D-14
-- says the real figure has to be measured at CP71, and this one is set where a runaway would be
-- caught within hours without a normal clinic day ever reaching it.
INSERT INTO core.ai_budget (facility_id, agent_code, daily_micro_usd)
SELECT f.id, NULL, 10000000 FROM core.facility f
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- The invariants
-- ---------------------------------------------------------------------------

-- Criterion 1. The constraint guards rows written from here on; this notices one that arrived some
-- other way, and it is the check the "no identifier reaches a provider" rule is really about.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_ai_payload_names_a_person() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_interaction
   WHERE outbound IS NOT NULL AND ops.carries_identifier(outbound);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% AI payloads carry something that names a person', offenders;
  END IF;

  SELECT count(*) INTO offenders
    FROM core.ai_prompt_version
   WHERE ops.text_carries_identifier(content);
  IF offenders > 0 THEN
    RAISE EXCEPTION '% deployed prompts carry something that names a person', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 1b, re-checked over the whole table. A constraint added later does not validate what is
-- already there, and this is the one rule in this schema where "already there" would mean a
-- patient's clinical data has been trained on by a third party and read by its reviewers.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_no_free_tier_call_touched_a_real_patient() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_interaction
   WHERE tier = 'free' AND provenance <> 'SYNTHETIC' AND status <> 'REFUSED_TIER';
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% AI calls went out on a free-tier credential carrying something that was not fabricated (ADR-0007)',
      offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 2. §10.6's fourth permanent invariant: *"every AI output is stored with its inputs,
-- prompt version, and model version"*. Without this a prompt version column could be null on every
-- row and the cost report would still add up, which is the version of this failure somebody ships.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_ai_call_names_what_produced_it() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_interaction
   WHERE status IN ('SUCCEEDED', 'CACHED', 'INVALID_OUTPUT')
     AND (prompt_version IS NULL OR model_version IS NULL OR btrim(input_sha256) = '');
  IF offenders > 0 THEN
    RAISE EXCEPTION '% AI calls do not record the prompt and model that produced them', offenders;
  END IF;
END
$$;
-- +goose StatementEnd

-- Every catalogue in this system reads in both languages, because the person on the floor when it
-- goes red may be reading either. The AI catalogues are no different: the operator screen showing
-- which agent has spent the day's budget is read by whoever is on shift.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_ai_catalogue_reads_in_both_languages() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offender text;
BEGIN
  SELECT agent_code INTO offender FROM core.ai_agent
   WHERE btrim(description_en) = '' OR btrim(description_bn) = '' LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'AI agent % does not read in both languages', offender;
  END IF;

  SELECT model_version INTO offender FROM core.ai_model
   WHERE btrim(description_en) = '' OR btrim(description_bn) = '' LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'AI model % does not read in both languages', offender;
  END IF;

  SELECT kind INTO offender FROM ops.pii_pattern
   WHERE btrim(description_en) = '' OR btrim(description_bn) = '' LIMIT 1;
  IF offender IS NOT NULL THEN
    RAISE EXCEPTION 'PII pattern % does not read in both languages', offender;
  END IF;
END
$$;
-- +goose StatementEnd

-- Criterion 5, and the half of it that is easy to get wrong. "Budget alerts fire at configured
-- thresholds" is false in two silent ways: a budget with no thresholds never alerts, and a budget
-- whose thresholds are out of order fires the wrong one first and then never fires the others,
-- because the gateway records a crossing once and stops looking below it. Both configurations look
-- perfectly reasonable in the table.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION core.assert_every_ai_budget_can_actually_alert() RETURNS void
LANGUAGE plpgsql AS $$
DECLARE offenders bigint;
BEGIN
  SELECT count(*) INTO offenders
    FROM core.ai_budget b
   WHERE coalesce(array_length(b.thresholds, 1), 0) = 0
      OR EXISTS (SELECT 1 FROM unnest(b.thresholds) t WHERE t < 1 OR t > 1000)
      OR b.thresholds <> (SELECT array_agg(t ORDER BY t) FROM unnest(b.thresholds) t);
  IF offenders > 0 THEN
    RAISE EXCEPTION
      '% AI budgets name no usable thresholds: a budget with none, with a value outside 1-1000%%, or with them out of order never alerts as configured',
      offenders;
  END IF;
END
$$;
-- +goose StatementEnd

INSERT INTO ops.invariant (function_name, description, sequence) VALUES
  ('assert_no_ai_payload_names_a_person',
   'no payload sent to a model, and no deployed prompt, carries something that names a person', 98),
  ('assert_no_free_tier_call_touched_a_real_patient',
   'no AI call on a free-tier credential carried anything but fabricated data', 99),
  ('assert_every_ai_call_names_what_produced_it',
   'every AI call that reached a model records its prompt version, model version and input hash', 100),
  ('assert_every_ai_catalogue_reads_in_both_languages',
   'every AI agent, model and PII pattern reads in both languages', 101),
  ('assert_every_ai_budget_can_actually_alert',
   'every AI budget names ascending thresholds a crossing can be measured against', 102)
ON CONFLICT (schema_name, function_name) DO UPDATE SET
  description = EXCLUDED.description, sequence = EXCLUDED.sequence;

-- ---------------------------------------------------------------------------
-- Who may read the outbound log
-- ---------------------------------------------------------------------------

-- One permission, not two, because at CP70 there is nothing to manage: no pause, no retry, no
-- knob. What there is is the thing the plan calls the mitigation for its own headline risk — *"the
-- scrubber plus a human-reviewable outbound log"* — and a log nobody can open is not reviewable.
--
-- **Sensitive.** The outbound payload names no person, by construction and by three separate
-- checks; but it carries that person's clinical picture in full, which is exactly what §4.4 blinds
-- registration and the pharmacist to. A permission that showed a pharmacist every diagnosis in the
-- clinic, on the grounds that the name had been removed, would be §4.4 defeated by a technicality.
INSERT INTO core.permission (code, resource, action, scope, description, is_sensitive) VALUES
  ('ai.gateway.read', 'ai', 'gateway', 'read',
   'Read what the system sent to an AI model, what it cost, and which prompt and model version produced it',
   true)
ON CONFLICT (code) DO UPDATE SET
  description = EXCLUDED.description, is_sensitive = EXCLUDED.is_sensitive;

INSERT INTO core.role_permission (role_id, permission_code)
SELECT r.id, 'ai.gateway.read'
  FROM core.role r
 -- The administrator, because the budget alert lands on their console. QA, because D-68 makes AI
 -- safety governance theirs and a scrubber nobody audits is a scrubber nobody knows is working.
 -- The physician, for the reason §10.6 exists at all: they are accountable for what an AI draft
 -- said, and being able to open the exact payload and prompt behind a summary they were shown is
 -- the difference between a tool they can defend and one they must simply trust.
 WHERE r.code IN ('ADMIN', 'QA', 'PHYSICIAN')
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM core.role_permission WHERE permission_code = 'ai.gateway.read';
DELETE FROM core.permission WHERE code = 'ai.gateway.read';
DELETE FROM ops.invariant WHERE function_name IN (
  'assert_no_ai_payload_names_a_person',
  'assert_no_free_tier_call_touched_a_real_patient',
  'assert_every_ai_call_names_what_produced_it',
  'assert_every_ai_catalogue_reads_in_both_languages',
  'assert_every_ai_budget_can_actually_alert');
DROP FUNCTION IF EXISTS core.assert_every_ai_budget_can_actually_alert();
DROP FUNCTION IF EXISTS core.assert_every_ai_catalogue_reads_in_both_languages();
DROP FUNCTION IF EXISTS core.assert_every_ai_call_names_what_produced_it();
DROP FUNCTION IF EXISTS core.assert_no_free_tier_call_touched_a_real_patient();
DROP FUNCTION IF EXISTS core.assert_no_ai_payload_names_a_person();
DROP TABLE IF EXISTS core.ai_budget_alert;
DROP TABLE IF EXISTS core.ai_budget;
DROP TABLE IF EXISTS core.ai_interaction;
DROP TABLE IF EXISTS core.ai_synthetic_subject;
DROP TABLE IF EXISTS core.ai_prompt_version;
DROP TABLE IF EXISTS core.ai_agent;
DROP TABLE IF EXISTS core.ai_model;
DROP FUNCTION IF EXISTS ops.carries_identifier(jsonb);
DROP FUNCTION IF EXISTS ops.text_carries_identifier(text);
DROP TABLE IF EXISTS ops.pii_pattern;
ALTER TABLE ops.phi_key DROP CONSTRAINT IF EXISTS phi_key_class_known;
ALTER TABLE ops.phi_key DROP COLUMN IF EXISTS class;
DELETE FROM core.facility_scope_exemption
 WHERE (schema_name, table_name) IN
   (('ops', 'pii_pattern'), ('core', 'ai_model'), ('core', 'ai_agent'), ('core', 'ai_prompt_version'));
