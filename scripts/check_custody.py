#!/usr/bin/env python3
"""Blueprint custody check.

Blueprint v2.0, Appendix B: on ratification the specification is frozen and its SHA-256
fingerprint recorded as the custody reference. Any change after freeze requires a v2.1
revision with its own delta log.

This script records those fingerprints (--write) and verifies them on every CI run,
so an accidental or silent edit to the specification cannot pass unnoticed.

Usage:
    python scripts/check_custody.py            # verify (exit 1 on mismatch)
    python scripts/check_custody.py --write    # record current hashes
"""

from __future__ import annotations

import hashlib
import re
import sys
from datetime import date
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CUSTODY = ROOT / "docs" / "CUSTODY.md"

TRACKED = [
    Path("docs/blueprint-v2.0.docx"),
    Path("docs/blueprint-v2.0.md"),
]

MARKER_START = "<!-- CUSTODY:BEGIN -->"
MARKER_END = "<!-- CUSTODY:END -->"


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def current_hashes() -> dict[str, str]:
    result: dict[str, str] = {}
    for rel in TRACKED:
        path = ROOT / rel
        if not path.exists():
            print(f"MISSING: {rel} is not present in the repository", file=sys.stderr)
            sys.exit(1)
        result[rel.as_posix()] = sha256(path)
    return result


def recorded_hashes() -> dict[str, str]:
    if not CUSTODY.exists():
        return {}
    text = CUSTODY.read_text(encoding="utf-8")
    block = text.split(MARKER_START)[-1].split(MARKER_END)[0]
    return {
        m.group(1): m.group(2)
        for m in re.finditer(r"^\|\s*`([^`]+)`\s*\|\s*`([0-9a-f]{64})`\s*\|", block, re.M)
    }


def write(hashes: dict[str, str]) -> None:
    rows = "\n".join(f"| `{name}` | `{digest}` |" for name, digest in hashes.items())
    block = (
        f"{MARKER_START}\n\n"
        f"| File | SHA-256 |\n|---|---|\n{rows}\n\n"
        f"_Recorded {date.today().isoformat()} by `scripts/check_custody.py`._\n\n"
        f"{MARKER_END}"
    )
    text = CUSTODY.read_text(encoding="utf-8")
    before = text.split(MARKER_START)[0]
    after = text.split(MARKER_END)[-1]
    CUSTODY.write_text(before + block + after, encoding="utf-8")
    for name, digest in hashes.items():
        print(f"recorded {name}  {digest}")


def verify(hashes: dict[str, str]) -> int:
    recorded = recorded_hashes()
    if not recorded:
        print(
            "No custody hashes recorded yet. Run: python scripts/check_custody.py --write",
            file=sys.stderr,
        )
        return 1

    failures = []
    for name, digest in hashes.items():
        expected = recorded.get(name)
        if expected is None:
            failures.append(f"{name}: not recorded in docs/CUSTODY.md")
        elif expected != digest:
            failures.append(
                f"{name}: CHANGED\n    recorded {expected}\n    actual   {digest}"
            )

    if failures:
        print("Blueprint custody check FAILED.\n", file=sys.stderr)
        for failure in failures:
            print(f"  {failure}", file=sys.stderr)
        print(
            "\nThe ratified specification must not change in place. If this change is"
            "\nintended, it is a new revision (v2.1) with its own delta log, and the"
            "\nhashes are re-recorded deliberately with --write.",
            file=sys.stderr,
        )
        return 1

    print(f"Blueprint custody verified ({len(hashes)} files unchanged).")
    return 0


def main() -> int:
    hashes = current_hashes()
    if "--write" in sys.argv:
        write(hashes)
        return 0
    return verify(hashes)


if __name__ == "__main__":
    raise SystemExit(main())
