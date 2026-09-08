# The AI gateway

CP70. Blueprint §7, implementation plan §10.3, D-07, D-08, ADR-0007, ADR-0032.

The only path from this system to any model. Nothing else in the repository may call one, and that
is the point: §7.2 names ten agents, and PHI minimisation, cost metering, prompt versioning and
auditability are either inherited by all ten or retrofitted into all ten. The second of those does
not happen — by the time there are nine agents in production, the tenth is written by copying the
ninth, and whichever of the four the ninth forgot is now a property of the system.

---

## What a call actually does

```
ai.Gateway.Invoke(ctx, Request{AgentCode, Subject, Payload})
  │
  1  resolve the agent's prompt version, pinned model, schema, timeout and budget
  │     unknown agent → fail immediately, nothing recorded (it is not a call)
  2  resolve provenance from core.ai_synthetic_subject      ← never from the request
  3  minimise (D-08): substitute, refuse identifier keys, scrub free text
  │     still names a person → record REFUSED_PHI without the payload, fail
  4  the tier guard (criterion 1b)
  │     free credential, not fabricated → record REFUSED_TIER, fail closed
  5  the response cache, on sha256 of the *minimised* payload plus the prompt version
  │     hit → record CACHED at zero cost, restore, return
  6  ── write the record ──────────────────────────  IN_FLIGHT, before anybody is contacted
  7  call the provider: agent timeout, retry with jitter, circuit breaker, fallback model
  8  validate against the agent's schema; violation → retry with a repair instruction
  9  finish the record: tokens, cost, latency, validation result, which model answered
 10  meter the day and fire any budget threshold just crossed
 11  restore the subject's identifiers into the answer; return it marked ai_generated
```

Step 7 of §10.3 — the grounding check — is **not here**. That is CP72, explicitly out of scope, and
the seam it will occupy is between 8 and 9.

---

## The two rules this exists to hold

### 1. No identifier reaches a provider

Three mechanisms, because each covers a case the others cannot.

**Substitution.** The caller hands the subject's identifiers to the gateway as a separate map on
`Subject`. They never enter the payload; a pseudonym does. On the way back they are put into the
answer, so the physician reads a name that never left the building to produce the sentence. This is
the strip-and-restore the checkpoint asks for, and it is the only one of the three that is exact.

**Key refusal.** Any payload key whose class is `IDENTIFIER` or `CREDENTIAL` is **refused, not
stripped** — see below.

**Pattern scrubbing.** Every string, at every depth, goes through the shared pattern list: runs of
digits in either script, phone numbers written with separators, email addresses, names carrying an
honorific. This is the mechanism for the risk the plan names as its main one, _"PHI leakage through
free-text fields"_.

Behind all three, a check constraint on `core.ai_interaction.outbound` runs the same rules in SQL.
Because the record is written **before** the provider is contacted, a payload that cannot be
recorded is a payload that is never sent — the database check is not alongside the call, it is in
front of it.

#### Why an identifier key is refused rather than stripped

D-08's own wording is "strips", and refusing is the better reading of it. Stripping is safer in the
moment and worse in every month afterwards: the agent's prompt still refers to the field, so the
model receives a template with a hole in it and writes a sentence about a patient whose name is
blank — and nobody ever learns the payload was assembled wrongly. Refusing is loud, happens at the
earliest possible point, and names the key. The clinical consequence is covered by D-15: the
physician's screen degrades to the raw structured record rather than to nothing, so a refused
synthesis costs a summary and not a consultation.

#### What is left over

**A bare given name of a third party in clinical prose.** "Her son Rafiq takes her to the pharmacy"
is not caught by any of the three mechanisms, and nothing that runs in this process can catch it:
"Rafiq" is a word, and no expression separates it from a drug, a district or a diagnosis. The
honorific pattern catches the very common "Md. Rafiq" spelling, which is most of them at this
clinic, and the phone number in the same sentence is caught outright. **The remainder is the
residual risk of this checkpoint**, and it is the reason the outbound log below exists to be read
rather than assumed correct.

### 2. A real patient never goes out on a free credential

ADR-0032 in full. The short version: **there is no flag on the request.** Provenance is looked up in
a register somebody had to write to, and every answer except "the register says this one is
fabricated" is treated as a real patient — including the answer a failed lookup gives, because a
database that cannot answer must not become permission.

Four enforcements: the register lookup, the environment condition (free tier is refused outside
`local`, `test` and `dev` whatever the register says), `config.Load`'s refusal of
`DTHCMS_AI_TIER=free` in production, and a check constraint plus invariant 99 that refuse to record
such a call at all.

**The register is not yet populated.** `cmd/synthload` does not call `Store.RegisterSynthetic`, so
today the free tier refuses everything. That is the correct direction to fail in, and wiring the
loader is the next task rather than a defect.

---

## The outbound log, and how to read it

`GET /v1/ops/ai/interactions` and `GET /v1/ops/ai/interactions/{id}`, permission
`ai.gateway.read` (administrator, QA, physician).

This is the plan's own stated mitigation for its own headline risk — _"the scrubber plus a
human-reviewable outbound log"_ — and a log nobody opens is not a mitigation. **Somebody should read
a sample of it weekly until CP72, and after any prompt change.** What to look for:

- a `removed` array that is empty on a note you would expect to contain a number;
- a name in the payload — which should be impossible, and is the one finding that matters;
- a `REFUSED_PHI` row, which means an agent is assembling payloads wrongly and its author has not
  noticed;
- a `REFUSED_TIER` row outside development, which means somebody has configured a free credential
  where one does not belong.

The list view carries no payloads. The detail view carries one at a time, and opening it is logged:
a list endpoint returning two hundred clinical payloads would be a bulk export of the clinic's
caseload wearing an operational screen's clothes.

### The manual verification for this checkpoint

```
1  set DTHCMS_AI_TIER=mock and start the stack (make up)
2  invoke the gateway.echo agent against a synthetic patient
3  GET /v1/ops/ai/interactions?agent_code=gateway.echo
4  open the newest one and read `outbound.payload` end to end
   → confirm no name, no national ID, no phone number, no address, no date of birth
   → confirm `outbound.removed` names what was taken out, and nothing else
5  repeat with DTHCMS_AI_TIER=free and a patient nobody registered
   → the call must be refused, and the refusal must be in the log
```

---

## Every call is recorded

Including the ones that failed, and that is the whole of the word "every": a call that errored is
exactly the one somebody asks about afterwards.

| Status                       | What it means                                                                               |
| ---------------------------- | ------------------------------------------------------------------------------------------- |
| `IN_FLIGHT`                  | written before the provider was contacted; a row still here is a process that died mid-call |
| `SUCCEEDED`                  | the answer passed its schema                                                                |
| `CACHED`                     | an identical input answered from a previous call, at zero cost                              |
| `INVALID_OUTPUT`             | the model answered, the answer failed its schema, the retries are spent                     |
| `PROVIDER_ERROR` / `TIMEOUT` | the provider                                                                                |
| `CIRCUIT_OPEN`               | not attempted; this process had decided the model was down and there was no fallback        |
| `REFUSED_TIER`               | criterion 1b happening                                                                      |
| `REFUSED_PHI`                | minimisation could not make this payload safe                                               |

Two of these keep deliberately less than the others. `REFUSED_PHI` stores no payload — that is
precisely the thing we decided was unsafe, and keeping it would move the leak from the provider to
our own audit table, where more people can read it and nobody is watching. `REFUSED_TIER` stores
none either: the payload is safe by construction, but a call nobody made is a payload with no
reader.

Retention is **D-67, open**. Nothing is deleted, and the application role holds no `DELETE` on the
table. A retention rule applied before anybody agreed one would be a decision made by default.

---

## Cost, and the budget alert

Money is in **integer micro-dollars**, rounded up. A cost record is money, and money in a float is a
number that stops adding up over a month of calls; rounding up means the meter is never optimistic,
which is the direction a budget alert has to err in.

Prices are on `core.ai_model`, per million tokens, beside the pinned model version. They are **the
published list rates at the time of writing and a proposal to be checked against the first real
invoice** — D-14 says the per-encounter figure "must be measured at CP71, not assumed".

`GET /v1/ops/ai/spend` shows the day per agent and for the deployment, with thirty days of threshold
crossings beside it. The deployment-wide budget — the row whose `agent_code` is empty — is the one
that catches a runaway in an agent nobody was watching. It is seeded at ten US dollars a day, which
is a proposal set where a runaway is caught within hours and a normal clinic day never reaches it.

Each threshold fires **once per agent per day**. The mechanism is the primary key on
`core.ai_budget_alert` and an `ON CONFLICT DO NOTHING` whose "did a row appear" answer is what
decides whether anybody is told — not a read-then-decide-then-write, which leaves a window where two
workers both conclude "not yet raised". An alert that arrived four hundred times would be one
somebody turned off, and then there is no alerting at all on the day it matters.

---

## Prompts and models

`internal/ai/prompts/<agent_code>.<version>.json`, embedded in the binary. §10.5: versioned
artefacts in the repository, deployed like code, reviewed like code. Start-up copies each version to
`core.ai_prompt_version` — text and all, not just a hash — so that an interaction from eight months
ago can be resolved to the exact prompt that produced it after the file has moved on four versions.

**A prompt edited under an unchanged version number is refused at start-up.** It is the single
change that would make every stored interaction unreproducible: the record names a version whose
text no longer exists anywhere. A process that will not start is a bad morning; an audit trail that
quietly stopped meaning anything is a worse year.

Model versions are explicit and pinned (D-13). A check constraint refuses anything containing
`latest` or `preview`, or equal to the family name. This matters more on Gemini than on most
providers: D-07 records that preview and free-tier aliases are retired within weeks, and a system
naming `gemini-flash-latest` would one day be answered by a model nobody evaluated and would record
that it had used "latest".

`gateway.echo` is the one agent registered today and it is **not** one of §7.2's ten. It is the
gateway's own fixture: the thing the manual verification invokes and the thing the tests drive the
whole pipeline through. CP71's synthesis agent is a second row, not the first.

---

## Providers

```
Provider interface { Name(); Generate(ctx, ProviderRequest) (ProviderResponse, error) }
```

A provider turns a prompt into text and reports the token counts. It does not minimise, validate,
cache, meter, retry, record, or decide anything about tiers — all of that is the gateway's, and it
is the gateway's so that adding an eleventh provider cannot ship a path that skips one of them.

Two implementations, plus a third thing that is not a provider:

|                | What it is                                | Used for                                                                 |
| -------------- | ----------------------------------------- | ------------------------------------------------------------------------ |
| `ai.Gemini`    | the real HTTP adapter, `generateContent`  | production (paid / Vertex), and pointed at mockai locally                |
| `ai.Mock`      | in-process, deterministic, free           | unit and database tests; `DTHCMS_AI_TIER=mock`                           |
| `tools/mockai` | a **server** speaking the Gemini protocol | the compose stack: exercises the real adapter with no network and no key |

The last one already existed and is deliberately kept. It and `ai.Mock` do different jobs: one
proves the adapter's request assembly, response parsing and error classification are right, and the
other makes a unit test instant. Neither replaces the other.

Swapping provider is `DTHCMS_AI_BASE_URL`, `DTHCMS_AI_API_KEY` and `DTHCMS_AI_TIER`. Vertex AI is
the same protocol behind a different base URL and credential, which is what makes D-07's option B a
configuration change rather than a second adapter.

The one thing the adapter does that looks like policy is **classifying failures**, and it is not
policy: only the adapter knows that this provider says 429 with a `Retry-After` header and puts a
refusal in `finishReason` rather than in a status code. Translating that into sentinel errors is
what lets the gateway have one retry policy rather than one per provider.

| Provider failure                      | Retried?                     | Why                                                                                          |
| ------------------------------------- | ---------------------------- | -------------------------------------------------------------------------------------------- |
| 429                                   | yes, honouring `Retry-After` | answering a 429 with our own curve is a second 429                                           |
| 5xx, transport fault, unparseable 200 | yes, exponential with jitter | usually transient; jitter stops forty jobs retrying in lockstep                              |
| safety refusal                        | **no**                       | asking the same question again is the one thing guaranteed not to help                       |
| 4xx                                   | **no**                       | a retired model version or a rejected key; retrying makes a fast deployment error a slow one |

---

## The circuit breaker

Per pinned model version, in this process, three consecutive failures, thirty-second cooldown, one
half-open probe.

What it protects is us, not Google. With Gemini down, every synthesis in the queue spends its whole
timeout waiting, three times over, and §7.1's five minutes fails for every patient in the building
rather than for the one whose call happened to be in flight. An open circuit converts a slow,
expensive, repeated failure into an immediate one — which is what lets D-15's degraded mode ("AI
summary unavailable, here is the structured record") appear before the consultation rather than five
minutes into it.

**It is per process and not shared.** Two API instances and a worker open three breakers
independently, each paying its own three failures to learn what the others know. That is a real cost
and it is accepted: sharing it means a round trip to Redis on the hot path of every AI call to save
nine wasted requests during an outage, plus a new failure mode in which the store holding the
breaker is itself what is down.

---

## The one list, in three places

`logging.PHIKeys` has been the single source of truth for "this key must never carry a value" since
CP02, enforced statically by `dthclint` and dynamically by the log handler, and mirrored into
`ops.phi_key` at CP69 so a check constraint could read it.

CP70 added a **class** to each entry, and the reason is worth stating because the obvious
alternative is a trap. The logging, telemetry and job-argument rules want the same answer for every
key on the list: never, anywhere. The gateway cannot want that — an AI payload's whole purpose is to
carry clinical content, and a diagnosis is exactly what §7.1's synthesis summarises. A rule that
refused every key here would refuse every payload the AI framework exists to send.

The obvious move is a second list of "identifier keys". It is silently wrong: a key added to the
logging list and forgotten on the gateway's list is an identifier the gateway happily forwards. One
list with a class on each row cannot drift that way, and `TestThePHIKeyListMatchesTheDatabase`
compares both fields in both directions.

| Class        | Example                       | Logs    | Job args | AI payload    |
| ------------ | ----------------------------- | ------- | -------- | ------------- |
| `IDENTIFIER` | `name`, `nid`, `phone`, `dob` | refused | refused  | **refused**   |
| `CLINICAL`   | `diagnosis`, `prescription`   | refused | refused  | **permitted** |
| `CREDENTIAL` | `password`, `token`, `otp`    | refused | refused  | **refused**   |

The free-text patterns get the same treatment: `ops.pii_pattern` and `ai.DefaultPatterns` are two
copies of one list, and the test that holds them together does more than compare strings — it runs a
fixture corpus through **both regular-expression engines** and fails when they disagree. PostgreSQL's
ARE and Go's RE2 are different implementations; the expressions are deliberately written in the
intersection of the two, and "one source of truth" is only worth the words if something checks it.

---

## Invariants

| #   | Name                                                | What it refuses                                                                      |
| --- | --------------------------------------------------- | ------------------------------------------------------------------------------------ |
| 98  | `assert_no_ai_payload_names_a_person`               | a recorded outbound payload, or a deployed prompt, carrying an identifier            |
| 99  | `assert_no_free_tier_call_touched_a_real_patient`   | criterion 1b, over the whole table                                                   |
| 100 | `assert_every_ai_call_names_what_produced_it`       | a call that reached a model without its prompt version, model version and input hash |
| 101 | `assert_every_ai_catalogue_reads_in_both_languages` | an agent, model or pattern that does not read in Bangla                              |
| 102 | `assert_every_ai_budget_can_actually_alert`         | a budget with no thresholds, or with them out of range or out of order               |

102 exists because "budget alerts fire at configured thresholds" is false in two silent ways: a
budget with no thresholds never alerts, and thresholds out of order fire the wrong one and then
never fire the others. Both configurations look perfectly reasonable in the table.

---

## What is deliberately absent

**No invoke endpoint, and there never will be.** An agent decides a call is warranted from inside
the module that owns the data and calls `Gateway.Invoke` directly. An HTTP route taking an agent
code and a payload would put the caller in charge of assembling the payload — and therefore in
charge of deciding what counts as an identifier — which is the one thing this module exists to take
away from them. It would be reached for exactly when somebody was in a hurry.

**No write path into the clinical record.** `architecture.json` gives `ai` the `platform` module and
nothing else, so it does not know what a patient is and could not append an event if it wanted to.
§10.6's first permanent invariant is "AI never writes to the clinical record"; the cheapest way to
hold that across ten agents is for the thing they all call to be structurally incapable of it.

**No grounding check.** CP72.

**No prompt A/B testing in production.** §10.5 is explicit: comparison happens offline against the
evaluation set, never as a silent live experiment on patients.
