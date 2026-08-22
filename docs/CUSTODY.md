# Document custody

Blueprint v2.0, Appendix B:

> On Dr. Nahid's ratification, this file is frozen and its SHA-256 fingerprint recorded in
> the repository as the custody reference for v2.0. Any change after freeze requires a v2.1
> revision with its own delta log.

This file holds those fingerprints. `scripts/check_custody.py` records them (`--write`) and
verifies them on every CI run, so the specification cannot be altered in place — accidentally
or otherwise — without the build failing and someone having to explain why.

## Recorded fingerprints

<!-- CUSTODY:BEGIN -->

| File | SHA-256 |
|---|---|
| `docs/blueprint-v2.0.docx` | `d29034a80684aa4314f93eb299c68bfb47185d0463ec4b99fb9d34f465676a4d` |
| `docs/blueprint-v2.0.md` | `9ccb5065a1a2bbe9ea5339f958258ad6c47467ed2481debb0a0c23a45154b565` |

_Recorded 2026-08-22 by `scripts/check_custody.py`._

<!-- CUSTODY:END -->

## If the check fails

A failing custody check means one of the tracked files changed. That is not automatically
wrong — but it is never routine:

1. **Unintended edit** — restore the file from git history. Nothing else to do.
2. **A genuine revision** — it becomes **v2.1** with its own delta log, committed as a new
   file alongside v2.0, and the hashes are re-recorded deliberately with `--write`.
   v2.0 is retained for archive, exactly as v1.0 was.

The Markdown extraction is a convenience for diffing and searching. **The `.docx` is the
document Dr. Nahid ratified**, and it is the one that matters in a dispute.
