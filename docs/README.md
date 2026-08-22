# DTHCMS documentation

| Document                                           | What it is                                                                                                                                                   |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| [`blueprint-v2.0.md`](blueprint-v2.0.md)           | **The specification.** Dr. Nahid's complete blueprint v2.0, extracted to Markdown for diffing and searching. Nothing outside this document is authoritative. |
| `blueprint-v2.0.docx`                              | The original handover document, byte-for-byte as received. This is the custody artefact.                                                                     |
| [`CUSTODY.md`](CUSTODY.md)                         | SHA-256 fingerprints of both files, verified on every CI run (blueprint Appendix B).                                                                         |
| [`implementation-plan.md`](implementation-plan.md) | The delivery plan: 160 checkpoints, architecture, open decisions, estimates.                                                                                 |

## Arriving later

| Document                                             | Checkpoint |
| ---------------------------------------------------- | ---------- |
| `adr/` — architecture decision records               | CP02       |
| `definition-of-done.md`                              | CP02       |
| `runbooks/` — deployment, restore, incident response | CP159      |

## A note on the open-decision register

Section 3 of the implementation plan is a **living register**, not a snapshot. When a
decision is made it is recorded there with its date and rationale; when a new ambiguity
is discovered during implementation, it is added there rather than resolved silently.
The register is the reason a session six months from now can tell what was decided and why.
