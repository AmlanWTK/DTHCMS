# ADR-0032 · Provenance is resolved from a register, not declared by the caller

**Status:** Accepted · **Date:** 6 Sep 2026 · **Checkpoints:** CP70 · **Related:** D-07, D-08, ADR-0007

## Context

CP70's acceptance criterion 1b reads:

> A real-patient payload cannot be sent on a free-tier credential — the call fails closed, proven by
> test.

ADR-0007 already settled why. Google's Gemini API Terms say of the free tier that "Google uses the
content you submit … to provide, improve, and develop Google products and services", that "human
reviewers may read, annotate, and process your API input and output", and — Google's own
instruction — "do not submit sensitive, confidential, or personal information to the Unpaid
Services". Everything DTHCMS would send is health data. Sending it on a free credential breaches the
provider's terms, not merely a preference of ours, and puts Faridpur patients' clinical pictures in
front of reviewers outside Bangladesh.

So the rule is settled. What was open is **how the gateway knows which payloads the rule applies
to**, and that turns out to be the whole of the difficulty.

## The obvious design, and why it fails

The obvious shape is a flag on the request:

```go
type Request struct {
    AgentCode string
    Synthetic bool   // ← this
    Payload   map[string]any
}
```

It is wrong, and the way it is wrong is the reason this ADR exists. **Ten agents will call this
gateway** (§7.2). Each is written by somebody working on that agent's problem, mostly by adapting
the previous one. `Synthetic` is a boolean whose zero value is `false`, so forgetting it is
technically safe — but the field also has to be _set_ to `true` in every development and evaluation
path, which means every agent has a code path that sets it, which means the interesting failure is
not forgetting the field but setting it in a branch that turns out to be reachable with a real
patient in it. Nothing anywhere notices; the call succeeds, the record says `synthetic: true`, and
the only evidence is a line in Google's training corpus.

More fundamentally: a flag a caller sets is a claim nobody checks. Criterion 1b asks for a call that
_cannot_ be made, and "the caller promised" is not a mechanism.

## Decision

**There is no flag.** `ai.Request` has no field expressing whether the subject is real, and there is
nowhere to add one without changing the `ai` package itself.

Provenance is **resolved** by `Store.ProvenanceOf`, which looks the subject up in
`core.ai_synthetic_subject` — a register of record ids that somebody deliberately entered, with a
reason of at least twenty characters and an author on the row. The function has three branches and
only one of them permits the free tier:

| What the register says      | Provenance     | Free tier? |
| --------------------------- | -------------- | ---------- |
| this subject is entered     | `SYNTHETIC`    | yes        |
| nothing about this subject  | `REAL_PATIENT` | no         |
| no subject was named at all | `UNKNOWN`      | no         |
| the lookup failed           | `REAL_PATIENT` | no         |

The last row is the one worth arguing for: **a database that cannot answer must not become
permission.** A gateway that fell back to "synthetic" on a failed lookup would send a real patient to
the free tier on the day the database was slow, which is precisely the day nobody is reading logs.

Three further layers sit behind it, none relying on the caller:

1. The gateway refuses the free tier outside `local`, `test` and `dev` whatever the register says —
   because those are the only environments where the deployment as a whole is not permitted to hold
   real patient data. A staging system restored from a production backup is one bad `INSERT` away
   from having real patients registered, and this is what makes that insufficient rather than fatal.
2. `config.Load` already refuses `DTHCMS_AI_TIER=free` in production, so the credential cannot be
   configured there at all.
3. A check constraint on `core.ai_interaction` refuses to **record** a free-tier call that is not
   synthetic, and invariant 99 re-checks the whole table. Because the gateway writes the record
   _before_ contacting the provider, a call that cannot be recorded is a call that does not happen —
   so even a bug in the Go guard cannot produce the row.

Four enforcements of one rule. That is one more than the job queue's PHI check has, and the
justification is the same shape and stronger: the cost of the queue's rule failing is a patient's
name in a log this clinic owns, and the cost of this one failing is a patient's clinical record in a
third party's training data, irrevocably.

## Alternatives considered

**A boolean on the request.** Rejected above. It is what the checkpoint's own scope line suggests —
"a free-tier credential is only usable for requests flagged synthetic" — and the brief that
commissioned this work correctly identified it as the trap.

**A `synthetic` column on `core.patient`.** Better than a flag: it makes provenance a property of the
record rather than of the request. Rejected for two reasons. The `ai` module may import `platform`
only (`architecture.json`), so it cannot read the patient module's table without a dependency that
would let it read everything else too; and — more importantly — it makes "is this fabricated" a
question about the patient table, which cannot answer it for the subjects that are not patients: a
scanned document nobody has filed, a load-test cohort, a records-extraction job. Those are exactly
the subjects whose provenance is least clear, and a design that could not represent them would have
had to special-case them, which is where the exception gets made.

**Deriving it from the environment alone** — free tier available in dev, and every payload in dev is
by definition synthetic. Rejected: it is true only until somebody restores a production dump into a
development database to reproduce a bug, which is a normal and reasonable thing to do. It survives
as the _second_ condition rather than the only one.

**Refusing the free tier entirely** (D-07's option C, applied unconditionally). Genuinely safe, and
rejected because it makes every model call in development cost money before anybody has decided
whether the AI features are worth having. D-07's whole point is that free-tier-on-synthetic-data is
a legitimate and valuable use; removing it would make prompt iteration a billed activity from
CP71 onward.

## Consequences

**Good**

- The safe answer is the default. Every path that has not been argued for lands on `REAL_PATIENT`.
- The unsafe answer is an act with an author, a timestamp and a written reason attached — something
  a reviewer can read, rather than a boolean nobody can audit after the fact.
- The `ai` module stays on `platform` alone. It does not know what a patient is, and therefore
  cannot be the second write path into the clinical record that §10.6's first invariant forbids.

**Bad — and we accept these knowingly**

- **The register can be lied to.** Whoever can write to `core.ai_synthetic_subject` can enter a real
  patient's id, and that patient's data becomes sendable on a free credential. This is the residual
  risk and it is stated here rather than buried: the design converts an unfalsifiable claim into an
  attributed, reviewable one, which is a real improvement and is not a proof. Mitigations are that
  the register is written only by the synthetic-data loader, that entries are never deleted (so the
  evidence survives), and that the environment condition holds independently.
- **One lookup per invocation.** A single indexed primary-key read per AI call, against a call that
  costs hundreds of milliseconds at a model. Not worth caching, and a cache here would be a place
  for a stale "synthetic" to live.
- **The free tier is unusable until something populates the register.** `cmd/synthload` does not do
  so yet, so today the free tier refuses everything. That is the correct direction to fail in and it
  is written down in `docs/ai-gateway.md` as the next task rather than as a defect.

**Revisit when** D-01's legal opinion lands and either forbids cross-border processing outright — in
which case the free tier disappears and this machinery becomes a formality — or when a second
facility exists (D-61) and "which register" becomes a question.
