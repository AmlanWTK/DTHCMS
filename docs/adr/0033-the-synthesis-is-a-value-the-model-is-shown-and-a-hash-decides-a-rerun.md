# ADR-0033 · The synthesis is a value the model is shown, and a hash of it decides a re-run

**Status:** Accepted · **Date:** 8 Sep 2026 · **Checkpoints:** CP71, and CP72 depends on it ·
**Related:** D-15, D-28, R-05, R-06, ADR-0031, ADR-0032

## Context

§7.1 asks for a one-page clinical overview and a draft plan, ready before the patient reaches the
consultation room, produced automatically and within five minutes. §10.4's A1 spells out how: _"deterministic
assembly of a structured clinical context → LLM produces a one-page narrative + a structured draft
plan → grounding check → deterministic rules annotate the draft"_.

Three things about that sentence had to be decided rather than transcribed.

1. **Where the boundary between "assembled" and "generated" sits**, and what shape the assembled
   thing is. CP72's grounding check — _"every number in the summary must correspond to an actual
   stored observation"_ — is only implementable if there is something to check against.
2. **What "all stations complete" means**, since §7.1's automatic trigger fires on it.
3. **What "material new data" means**, since §7.1 asks for an incremental re-run when it arrives.

## Decision

### 1. The assembled context is a value, stored beside the answer

`Assemble(Raw) Context` is a **pure function**. It reads no database, contacts nothing, and takes
its clock as an argument. Everything that decides what a summary can say happens inside it, and its
output is a Go value that a test constructs and asserts against.

The context is then stored, in full, on `core.ai_synthesis.context` beside the model's answer.

It carries a **flat fact index**: every citable thing the assembler found, each with a short stable
reference (`obs.hba1c:2026-03-12`). The prompt requires every clinical claim to cite the references
it rests on, and the agent's output schema has a `citations` array.

### 2. Completeness is `core.station_sequence`'s own definition

_Every required station in this visit type's plan, at a position before the consultation, has a
finished encounter._ Nothing about that lives in Go: the sequence, the `required` column and the
position of `STN_CONSULTATION` are all rows the clinic owns.

The trigger fires from `visit.Service.Depart`, through a `StationHook` interface `visit` declares
and the composition root wires, **inside the departing operator's transaction** — so the queue's
`EnqueueTx` guarantee holds and a rolled-back station touch takes its summary request with it.

### 3. Materiality is "the assembled context changed"

`Context.MaterialSHA256` hashes the fact index, the complaint, the station picture, the allergy
status, the gap list and the assembler version. A re-run assembles, compares, and calls no model
when the hash is unchanged — recording state `UNCHANGED` rather than doing nothing.

## Why, for each

### Why a pure function and a stored value, rather than a query the validator re-runs

The alternative is to store only the answer and have CP72's validator re-derive the patient's record
when it wants to check a number. It fails in two ways, and the second is fatal.

The mundane one: the validator would be a second implementation of the assembler, and the two would
disagree the first time either changed.

The one that decides it: **the record moves.** A summary produced at 10:04 is checked at 10:06,
after a correction landed at 10:05. Re-deriving finds the corrected value, the summary's number no
longer matches, and a correct summary is reported as a hallucination. Grounding is a claim about
_what the model was shown_, and that is only answerable if what it was shown is kept.

The cost is a JSON document per run — a few kilobytes — and one more thing that has to stay in step
with the payload. The second is handled by construction: the payload handed to the gateway is the
serialisation of the same value, so they cannot drift.

### Why a fact index with citations, rather than leaving grounding to a numeric scan

CP72 could be built by scanning the narrative for numbers and looking each one up. It would work,
badly. "8.2" appears in a narrative for several reasons and a scanner cannot tell a quoted HbA1c
from a page number; dates written in prose ("March") do not match a stored `2026-03-12`; and a
model's sentence about a value it inferred looks exactly like one about a value it was given.

Requiring a citation moves the problem from parsing to set membership. It also has an effect on the
model that the numeric scan does not: a model told it must cite says less when it cannot find a
fact, which is the failure direction to prefer in a clinical summary.

The cost is that the model must cooperate, and one that ignores the instruction produces an
uncitable narrative. That is a _detectable_ failure — CP72 rejects it — rather than a silent one.

### Why the references look the way they do

Readable rather than opaque, because the person most likely to follow one is a physician reading the
page.

Written `obs.hba1c:2026-03-12` with a **colon** before the date, because the gateway's free-text
scrubber replaces nine digits separated by space, dot, bracket, plus or hyphen with `[NUMBER]`. A
date is eight such digits; anything that puts a ninth in front of it makes the whole reference look
like a telephone number. `obs.spo2.2026-06-09` does exactly that — the code ends in a digit and the
dot is a separator the pattern counts — and the reference would leave the building redacted, so the
model would be asked to cite something it had never seen. This was not reasoned out in advance: the
database refused to store the context for a third of the cohort, which is the check constraint doing
its job.

### Why completeness is not a list in Go

Because the sequence is an operational decision the clinic owns and will change. §5.2 already
anticipates re-ordering rooms; a second definition in code drifts from the table the first morning
somebody does. The cost is that a facility with a mis-configured sequence gets a mis-timed summary,
which is a row to fix rather than a deployment.

### Why the hook runs inside the transaction, and why its failure does not fail the write

Inside, because ADR-0031's whole design rests on `EnqueueTx` taking the caller's transaction: a
station touch that rolls back must not leave a job behind that briefs a physician about a patient
who is not there.

Not fatal, because D-15 says the clinic never stops when AI stops, and refusing to record that a
patient left the examination room because a queue insert failed would be exactly that.

Those two pull against each other, and the naive reconciliation is silently broken: a failed
statement aborts the whole PostgreSQL transaction, so _swallowing_ the hook's error leaves the
departure's own event append failing a moment later with "current transaction is aborted" — the
clinical write is lost anyway and the log blames the wrong thing. So the hook runs in a
**savepoint**. The cost is one round trip per station touch, and it is what makes the rule true
rather than intended.

### Why materiality is a hash of the context rather than a list of event types

The obvious design is a list: `OBSERVATION_RECORDED` is material, `PATIENT_DEMOGRAPHICS_CORRECTED`
is not. It requires somebody to keep that list in step with every event type the system grows, and
the failure mode is silent in the dangerous direction — a new clinical event type nobody adds to the
list is new data the summary never reflects.

Hashing the assembled context inverts it: **a change is material when it changes what the model
would be shown.** A typo in a phone number cannot be material because no phone number ever enters a
context — the identifier rule has already made that class of change invisible here, which is a
pleasing consequence of a decision taken for another reason. A corrected blood pressure changes a
fact and is material. An operator's name, a device id, a re-print, a queue reroute: none of them
reach a context either.

Two things are deliberately outside the hash. `assembled_at`, or every look would conclude the
summary was stale and every open visit would buy a model call a minute. And the _ordering_ of
anything the assembler could reorder without meaning to — the lines are sorted before hashing — so a
database returning two same-day rows in a different order does not buy a call either.

The direction of error is chosen: too sensitive costs money and changes a physician's page under
them; too blunt shows them a summary that does not mention the potassium that came back ten minutes
ago. The second is worse, so every fact is in the hash rather than a hand-picked subset of
"important" codes somebody would have to maintain.

### Why `UNCHANGED` is a state rather than a no-op

"We looked and there was nothing new" is a different fact from "nobody looked", and only one of them
should reassure a physician reading a timestamp. It is also what makes the SLA report honest: an
unchanged run is a promise kept, and excluding it would let a clinic hit 95% by re-requesting
summaries it knew were fresh.

## Consequences

**Good.** CP72 becomes a set-membership test over one column instead of a second assembler. The
half of this checkpoint that decides quality has tests that need no database and no model. A failed
or queued synthesis still has a full structured record to render, which is most of D-15's degraded
state for free. Changing what "material" means is one function.

**Costly.** The context is stored twice in substance — once here and once as the gateway's outbound
payload — because they are read by different people for different reasons and the second exists only
for calls that were made. A JSON document per run per visit is real storage; at this clinic's volume
it is megabytes a year.

**Accepted risks.** The fact index is only as good as the assembler: something the assembler does
not name is something the model cannot say and CP72 cannot ground, so a coverage gap in `Gather`
becomes an invisible quality ceiling. That is the argument for reviewing the twenty summaries'
_context column_ rather than only their prose.

The re-run trigger fires from station touches and the button, and **not** from a correction typed at
the consultation desk. The materiality machinery would handle it; nothing calls it. That is a stated
gap, not a design position — see `docs/synthesis.md`.

## Alternatives considered

**A synthesis service that queries the model with tools.** Let the model ask for what it needs.
Rejected outright: §10.6's first permanent invariant is that AI never writes to the clinical record,
and a tool-calling agent is one prompt injection away from a read path nobody enumerated. It also
makes grounding impossible — there is no "what the model was shown" to check against.

**Storing only a hash of the context rather than the context.** Cheaper, and enough to decide a
re-run. Rejected because it gives CP72 nothing and gives D-15's degraded screen nothing.

**Deciding materiality at the request site instead of in the worker.** It would avoid queueing a job
that turns out to be unnecessary. Rejected: deciding it means assembling the whole context inside
somebody else's write transaction to answer a question that is usually "no". The current shape pays
one assembly in the worker and no model call, which is the cheap half of the same answer.
