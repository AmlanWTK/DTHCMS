# DTHCMS documentation

| Document                                           | What it is                                                                                                                                                   |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| [`blueprint-v2.0.md`](blueprint-v2.0.md)           | **The specification.** Dr. Nahid's complete blueprint v2.0, extracted to Markdown for diffing and searching. Nothing outside this document is authoritative. |
| `blueprint-v2.0.docx`                              | The original handover document, byte-for-byte as received. This is the custody artefact.                                                                     |
| [`CUSTODY.md`](CUSTODY.md)                         | SHA-256 fingerprints of both files, verified on every CI run (blueprint Appendix B).                                                                         |
| [`implementation-plan.md`](implementation-plan.md) | The delivery plan: 160 checkpoints, architecture, open decisions, estimates.                                                                                 |

## Engineering

| Document                                                   | What it is                                                               |
| ---------------------------------------------------------- | ------------------------------------------------------------------------ |
| [`adr/`](adr/)                                             | Architecture decision records — what was decided, why, and what it costs |
| [`engineering-standards.md`](engineering-standards.md)     | Conventions for Go, TypeScript, database, events, API, git and review    |
| [`architecture-boundaries.md`](architecture-boundaries.md) | Module dependency rules, and how the build enforces them                 |
| [`definition-of-done.md`](definition-of-done.md)           | What "done" means for a checkpoint                                       |
| [`local-development.md`](local-development.md)             | Running the stack on your machine, and what is in it                     |
| [`synthetic-data-profile.md`](synthetic-data-profile.md)   | The clinical case-mix the synthetic generator is built against           |

## What has been built

One page per area, written as the checkpoint that established it closed. Each records the
decisions taken, the departures from the plan, and what was deliberately left undone.

| Document                                   | Established at                  |
| ------------------------------------------ | ------------------------------- |
| [`progress.md`](progress.md)               | The running record — start here |
| [`database.md`](database.md)               | CP06                            |
| [`observability.md`](observability.md)     | CP07                            |
| [`design-system.md`](design-system.md)     | CP09                            |
| [`web-shell.md`](web-shell.md)             | CP10                            |
| [`mobile-shell.md`](mobile-shell.md)       | CP11                            |
| [`api-conventions.md`](api-conventions.md) | CP12                            |
| [`testing.md`](testing.md)                 | CP13                            |

## Arriving later

| Document                                             | Checkpoint |
| ---------------------------------------------------- | ---------- |
| `runbooks/` — deployment, restore, incident response | CP159      |

## A note on the open-decision register

Section 3 of the implementation plan is a **living register**, not a snapshot. When a
decision is made it is recorded there with its date and rationale; when a new ambiguity
is discovered during implementation, it is added there rather than resolved silently.
The register is the reason a session six months from now can tell what was decided and why.
