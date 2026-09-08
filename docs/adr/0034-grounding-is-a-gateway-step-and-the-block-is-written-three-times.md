# ADR-0034 · Grounding is a step in the gateway, and the block is written three times

**Status:** Accepted · **Date:** 8 Sep 2026 · **Checkpoints:** CP72 ·
**Related:** ADR-0032, ADR-0033, D-13, D-15, §10.2, §10.5, §10.6

## Context

§10.2's pipeline puts a **grounding check** between schema validation and the physician: _"every
number in it is matched against the structured record … or the sentence is rejected"_. §10.6's
fifth permanent invariant states it as a rule: _"every numeric claim is grounded against structured
data before display."_ CP70 built the gateway and deliberately left the step out; CP71 built the
context it would check against and said so in ADR-0033.

Four things had to be decided rather than transcribed.

1. **Where the check runs**, given that it must be impossible for a future agent author to omit.
2. **Where the block lives**, given that a check which logs and lets the sentence through is a
   metric rather than a control.
3. **What "every numeric value, date and drug name" means in practice**, given that a naive
   implementation of it blocks a quarter of legitimate summaries.
4. **What happens to a violation**, given that CP70's answer to a bad model answer is a retry.

## Decision

### 1. The check is a step in the gateway, and every exit runs it

`Gateway.Invoke` grounds the model's answer against the **minimised payload** — the object the model
was actually shown — and returns `ErrUngrounded` with no output when a claim does not resolve.

There are three places a `Result` can be built: the ordinary path, a cache hit, and the retry after
an undecodable cache entry. All three now build a `Result` **with no `Output`** and hand it to one
function, `Gateway.deliver`, which is the only line in the package that sets `Output`.

### 2. Whether an agent is grounded is resolved from `core.ai_agent`, and defaults to true

`grounding_required boolean NOT NULL DEFAULT true`, with `grounding_exempt_reason` and a check
constraint requiring twenty characters of it. One row is exempt: `gateway.echo`, CP70's fixture,
whose answer is a count of the payload's fields.

### 3. The block is written three times, in three places that fail independently

- **Go**, in the gateway: no output is returned.
- **A check constraint**, twice: `core.ai_interaction` refuses a failed verdict against a
  successful status; `core.ai_synthesis` refuses a `READY` row whose `grounding_state` is anything
  but `PASSED`.
- **An invariant**, over the whole table, for the row that arrived some other way.

Plus a fourth in `synthesis.settle`, which converts a `READY` write without a passing verdict into a
withheld run rather than trusting its caller.

### 4. Four arms, and the third of them is unarmed and says so

**Citation** — every fact reference the model writes must be in the context's fact index.
**Number** — every numeric token in prose must appear in the payload, or be a count of a collection
the payload contains. **Date** — ISO and prose dates must resolve to a date the payload carries.
**Drug** — every drug name must be in the formulary; there is no formulary, so every drug name is
refused and the report says `DRUG_ARM: UNARMED`.

### 5. A violation is a defect, and is never retried

`core.ai_grounding_defect`, one row per finding, not deletable by the application, carrying the
prompt version, the model version, the arm, the token, the excerpt — and a human's later verdict on
whether the check was right.

## Why, for each

### Why the gateway rather than the agent

The alternative is a `synthesis.Ground(context, output)` called by the agent that produced both. It
is easier to write, it can use the agent's own Go types instead of walking JSON, and it is wrong for
the same reason CP70's tier guard is not a field on the request: **the check would then be a thing
each of §7.2's ten agents has to remember**. By the time there are nine agents, the tenth is written
by copying the ninth, and whichever safety step the ninth forgot is now a property of the system.

The cost is that the check works on JSON rather than on typed values, and therefore cannot know
that `narrative_en` is prose and `confidence` is a score. It compensates by walking **every string
at any depth**, which is the fail-closed reading, and by taking its fact index from the payload's
own `facts` array — a convention ADR-0033 established and which any future agent gets by writing a
payload in the same shape.

### Why every exit funnels through one function

Three exits is three places to add the check and three places for the fourth exit, added in two
years by somebody reading the second, not to have it. So the three build a `Result` with no
`Output`, and `deliver` is the only line that sets it. A future exit that forgets produces an answer
with no output at all — which fails loudly at its first caller rather than quietly at a physician's
screen.

The cache hit is grounded too. The verdict cannot differ — the cache is keyed on the hash of the
same payload — and that is precisely why it would have been reasonable to skip, and precisely why it
is not skipped. The guarantee being made is not "the verdict is right on the cached path", it is
"there is no exit that does not run the check".

### Why the requirement is resolved from a register, and why the default is the safe answer

ADR-0032's argument, a second time. A field on the request saying "this one does not need grounding"
is a claim nobody checks, set in a branch that turns out to be reachable with a real patient in it.

The **default** is the load-bearing part, not the column. A future checkpoint that adds an agent and
forgets this table gets grounding, because forgetting means taking the default. Forgetting in the
other direction is impossible: an exemption is a row somebody wrote a sentence into.

A failed lookup returns "required", the same rule `ProvenanceOf` follows: a database that cannot
answer must not become permission. The cost is a summary withheld on the day the database is slow,
which is a cost D-15's degraded screen was designed to absorb.

### Why `core.ai_synthesis` does not consult that register

Its constraint demands `PASSED` outright and does not accept `NOT_REQUIRED`. So flipping
`clinical.synthesis` to exempt would not quietly disable the block — it would refuse to store any
summary at all, loudly, on the first run.

Two guarantees are only two if the second does not read the first. This is the one place in the
design where the register could have been trusted and deliberately is not.

### Why a defect rather than a retry, and what that costs

CP70 retries a malformed answer, and it is right to: JSON that does not parse is a transport
problem and the second attempt usually fixes it. An invented HbA1c is not a transport problem.

Retrying it would _work_, most of the time, and that is the objection. The evidence would be gone;
the physician would read a second answer nobody had compared with the first; and the rate of the
failure this whole checkpoint exists to measure would read as zero. A metric that reads zero because
the events are being repaired before anybody counts them is worse than no metric, because somebody
will quote it.

The cost is a summary the physician does not get, on a visit where a retry might have produced a
good one. D-15 already covers the clinical consequence: the structured record is on the screen with
a sentence saying why the narrative is missing.

### Why a fact index is not enough, and the number arm exists anyway

ADR-0033 argues that requiring citations turns grounding from parsing into set membership, and it is
right — the citation arm has no false positives by construction. It is also not sufficient. A model
can cite `obs.hba1c:2026-03-12` correctly and write the wrong number beside it, and that sentence
passes a citation check and is exactly the failure §10.2 describes.

So both arms run. The citation arm catches a model that invents a source; the number arm catches a
model that misquotes a real one. The frozen evaluation set's `digit-swap` case is the second, and
the citation beside it resolves.

### Why counts are not hallucinations, and why the rule is narrow

The first implementation of the number arm flagged every numeric token not present in the payload.
Run over CP71's twenty summaries it produced 24 findings, and **five of the twenty summaries would
have been blocked** — a 25% false-positive rate, on prose written by a rule-based composer that
cannot invent anything. Two classes accounted for all of it:

- the `confidence` score, a JSON number that is a field of the answer rather than a quotation;
- sentences of the form "a further 7 measurements are on the record".

The second is a model _counting things it was shown_, which is not inventing. The rule adopted is
deliberately narrow and derived from the payload rather than from a list somebody maintains: a whole
number followed, within four words with no punctuation between, by the name of a collection the
payload actually contains. `measurements`, `stations`, `gaps` — the plural and singular of every
array key at any depth.

The false negative it admits is stated rather than discovered: a model writing "HbA1c 9
measurements" would have its 9 accepted. That sentence does not occur in clinical prose. The
alternative was measured, and it blocks a quarter of legitimate summaries, which is a check the
clinic turns off.

JSON numbers are not scanned at all, for a related reason: their meaning is fixed by the agent's
schema and a model cannot make a clinical claim in one that the schema did not already invite. The
residual is an agent whose schema grows a numeric _clinical_ field; the answer is that agents quote
the record in prose and cite it, and that is what the citation arm is for.

### Why the drug arm is built and unarmed rather than stubbed

§10.4's A1 requires drug names validated against the formulary. There is no formulary — it is CP75 —
and `architecture.json` does not let `ai` import one in any case. So the arm takes a `DrugCheck`
function that the composition root will supply, and a nil one means **every drug name is
unrecognised**, which is A1's own rule (_"an unrecognised drug name is dropped, not displayed"_)
applied honestly to a system that recognises nothing.

The alternative — a list of thirty medicines typed into a Go file — would be worse than nothing. It
would certify as verified whatever somebody happened to type, it would be out of date within a
month, and the first person to see a green tick beside a drug name would reasonably believe
something had checked it.

**The cost is real and is not hidden.** With a real model and no formulary, any summary that drafts
a medication is withheld in full. CP71's deterministic composer names no drug, so nothing is blocked
today; a Gemini answer to the same prompt very probably would be. There are two ways out and both
are decisions this checkpoint does not get to take: CP75 supplies the formulary, or the agent's
prompt stops inviting `draft_medications` until it does.

Blocking the whole answer rather than removing the item is also deliberate. A redaction the
physician cannot see is a second thing to trust; the missing formulary should be visible rather than
tolerable.

### Why the false-positive rate is absent rather than zero when nobody has looked

`FalsePositiveRate()` returns nil until a defect has been reviewed, the gauge is not published, and
the API omits the field. A validator nobody has checked has an **unknown** false-positive rate, and
publishing 0% for it would put a green number on a dashboard that means "we have never looked".

That is the criterion most easily faked, and the easiest way to fake it is not dishonesty — it is a
`float64` that defaults to zero.

## Consequences

**Good.** No agent can get a model's answer out of this system without it having been grounded, and
no future agent has to remember anything. The check is a pure function of two JSON documents, so its
tests need no database and no model, and the frozen evaluation set runs in about a second in CI. A
withheld summary still shows the physician the whole assembled record.

**Costly.** Any summary drafting a medication is withheld until CP75. The check walks every string
in every answer, which is a few hundred microseconds per summary and irrelevant beside a model call.
A defect table grows with every violation and is never pruned, which is right until D-67 decides
retention and wrong afterwards.

**Accepted risks.**

The number arm's real false-positive risk is a model **paraphrasing** rather than quoting — writing
"Type 2 diabetes" where the record says "T2DM", or converting a unit. The frozen corpus cannot
exercise it, because a deterministic composer only ever writes what it read. The measured 0% is
therefore a floor and not an answer, and `docs/ai-grounding.md` says so in as many words.

A bare month name is not checked, because "May" at the start of a sentence collides with the month.
A month with a day or a year is checked.

An agent whose citation array is under an unexpected key, or whose prose lives in a field the walk
does not reach, would lose an arm silently. The report's `citations_checked` / `numbers_checked`
counters are the defence: a verdict of PASSED from a check that examined nothing is the failure this
mechanism is most likely to have, and it is invisible without a denominator. The dashboard plots it
and an alert fires on it.

## Alternatives considered

**Grounding in the agent, against `synthesis.Context`.** Typed, simpler, more precise — and each of
§7.2's ten agents would have to remember it. Rejected for the reason the gateway exists at all.

**Re-deriving the record at check time instead of using the stored payload.** Rejected in ADR-0033,
and the argument is unchanged: a summary written at 10:04 and checked at 10:06 against a record
corrected at 10:05 would be reported as a hallucination that never happened.

**Redacting the offending item instead of blocking the answer.** §10.4 says an unrecognised drug
name is "dropped, not displayed", so this is the plan's own wording for the drug arm. Rejected for
now because it splits the stored answer from the displayed one and puts the safety property in the
display layer — which is CP73's, does not exist, and is exactly the "a future caller forgets" shape
this checkpoint is about. Worth revisiting when CP73 builds the panel.

**A `paths:` filter in CI to run the evaluation set only when a prompt changes.** The obvious
reading of §10.5, and a gate that silently stops applying the day somebody moves the prompts
directory. Rejected in favour of the baseline recording each prompt's content hash: a build whose
prompts do not match the ones the baseline was blessed against fails, and that check cannot stop
applying. A model change is a prompt change, because `model_version` is a field in the prompt file.

**Numeric comparison with a tolerance.** Rejected because ADR-0033 already formats every fact value
as a string, exactly so that this comparison is exact. A tolerance is a number somebody would have
to defend, per unit, forever.
