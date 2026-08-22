# ADR-0001: Record architecture decisions

- **Status:** Accepted
- **Date:** 2026-08-22
- **Deciders:** Dr. K. M. Nahid Ul Haque, Amlan Sarkar

## Context

DTHCMS will be built over several years, one checkpoint at a time, by a very small team
in which one participant — Claude — carries no memory between sessions. The repository is
therefore not merely where the code lives; it is the project's only continuity.

Decisions made in conversation evaporate. Six months from now, "why is prescribing logic
hard-coded rather than driven by the AI?" must be answerable from the repository alone,
because the person asking may be a developer hired long after the conversation happened.

## Decision

Every significant architectural or technical decision is recorded as an ADR in
`docs/adr/`, committed in the same pull request as the change it justifies.

The implementation plan's open-decision register (`docs/implementation-plan.md` §3) holds
questions **not yet decided**; ADRs hold decisions **already made**. A question moves from
one to the other when it is answered.

## Alternatives considered

**Decisions recorded in the implementation plan only.** The plan is a single 5,000-line
document; decisions made later would be buried in it, and its §3 register is deliberately
about open questions. Rejected as unfindable.

**No formal record; rely on commit messages and code comments.** Commit messages explain
what changed, rarely what was rejected and why. Rejected as insufficient.

## Consequences

**Good**

- A new engineer can read a dozen short documents and understand the shape of the system
  and its reasoning, without archaeology.
- Revisiting a decision starts from what we actually knew at the time, not from memory.
- Writing the consequences down makes weak decisions visibly weak while they are still cheap.

**Bad — and we accept these knowingly**

- It is friction on every meaningful change, and friction is sometimes skipped under
  deadline pressure. The code review checklist asks for the ADR precisely because of that.
- ADRs go stale. We mitigate with supersession rather than editing, which trades tidiness
  for an honest trail.

**Revisit when**

- Never, realistically. If ADRs are being skipped, the answer is to enforce them, not to
  abandon the practice.
