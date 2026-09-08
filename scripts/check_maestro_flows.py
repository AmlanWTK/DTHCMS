#!/usr/bin/env python3
"""Check the Maestro flows without a device (CP68).

The flows in mobile/maestro/ cannot run until D-59 names the clinic's tablet. That is a
statement about verification, not about correctness: a flow with a mistyped command or a
selector nobody ever wrote is broken today, and discovering it on the afternoon the device
arrives turns "run the suite" back into a project.

So this checks the three things that are checkable with no device at all:

  1. every flow names the application this repository builds, so a flow cannot quietly
     point at nothing;
  2. every command in every flow is one Maestro actually has - the failure this catches is
     a plausible-looking typo like `setAirPlaneMode`, which Maestro reports as an unknown
     command at run time, on the one day nobody wants to be debugging YAML;
  3. every flow the §13.10 scenario registry claims exists is on disk, and every flow on
     disk is claimed by a scenario or listed here as a deliberate extra.

The third is the one worth having. `mobile/test/offline/scenarios.ts` tells a reader which
half of each scenario runs in CI and which waits for hardware, and that table is only worth
reading if the flows it names are real files. A registry that named a deleted flow would be
the exact failure this checkpoint exists to stop producing: documentation of a check that
does not happen.

Deliberately not a YAML parser. The check needs command names and one key, the repository
has no Python dependencies, and adding one to read four files would be a worse trade than
the twenty lines below - the same argument backend/internal/platform/apispec records for
scanning the OpenAPI document with a scanner rather than a library.

Usage:
    python scripts/check_maestro_flows.py
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FLOWS = ROOT / "mobile" / "maestro"
APP_JSON = ROOT / "mobile" / "app.json"
SCENARIOS = ROOT / "mobile" / "test" / "offline" / "scenarios.ts"

# Maestro's command vocabulary, as of 1.36. Extend it deliberately: an unknown command here
# is either a typo or a command from a newer Maestro than the flows claim to need, and both
# are worth a sentence in review.
COMMANDS = {
    "assertNotVisible",
    "assertTrue",
    "assertVisible",
    "back",
    "clearState",
    "copyTextFrom",
    "eraseText",
    "evalScript",
    "extendedWaitUntil",
    "hideKeyboard",
    "inputRandomNumber",
    "inputRandomText",
    "inputText",
    "killApp",
    "launchApp",
    "longPressOn",
    "openLink",
    "pressKey",
    "repeat",
    "runFlow",
    "runScript",
    "scroll",
    "scrollUntilVisible",
    "setAirplaneMode",
    "setLocation",
    "startRecording",
    "stopApp",
    "stopRecording",
    "swipe",
    "takeScreenshot",
    "tapOn",
    "toggleAirplaneMode",
    "travel",
    "waitForAnimationToEnd",
}

# Flows that are not one of §13.10's ten, and why each is here anyway.
EXTRAS = {
    "smoke.yaml": "CP11: the shell comes up and an operator can read it, in both languages.",
    "offline/sync-status-is-never-a-lie.yaml": (
        "§13.9: the indicator says the work is with the clinic only when it is, in both scripts. "
        "Not one of §13.10's ten, and the requirement whose consequence the blueprint states "
        "most sharply."
    ),
}


def app_id() -> str:
    document = json.loads(APP_JSON.read_text(encoding="utf-8"))
    return str(document["expo"]["android"]["package"])


def flow_files() -> list[Path]:
    return sorted(path for path in FLOWS.rglob("*.yaml") if path.is_file())


def commands_in(body: str) -> list[str]:
    """Every top-level command in a flow body, in order.

    A Maestro command is a list item at the start of a line: `- tapOn:` or `- back`. Nested
    lines are indented, so a check anchored at column zero sees each command once and none
    of its arguments.
    """
    found = []
    for line in body.splitlines():
        match = re.match(r"^- ([A-Za-z]+)", line)
        if match:
            found.append(match.group(1))
    return found


def check() -> list[str]:
    problems: list[str] = []
    expected_app = app_id()
    files = flow_files()
    if not files:
        # A check that finds nothing must fail rather than report success. A glob that has
        # quietly stopped matching is the same failure as a test that silently skips.
        return ["no Maestro flows found under mobile/maestro/ - has the directory moved?"]

    on_disk = set()
    for path in files:
        relative = path.relative_to(FLOWS).as_posix()
        on_disk.add(relative)
        text = path.read_text(encoding="utf-8")
        if "\n---\n" not in text:
            problems.append(f"{relative}: no `---` separating the header from the commands")
            continue
        header, body = text.split("\n---\n", 1)

        if f"appId: {expected_app}" not in header:
            problems.append(f"{relative}: does not declare `appId: {expected_app}`")

        used = commands_in(body)
        if not used:
            problems.append(f"{relative}: no commands")
        for command in used:
            if command not in COMMANDS:
                problems.append(f"{relative}: `{command}` is not a Maestro command")

    claimed = set(re.findall(r"device: '([^']+\.yaml)'", SCENARIOS.read_text(encoding="utf-8")))
    for flow in sorted(claimed):
        if flow not in on_disk:
            problems.append(f"scenarios.ts names {flow}, which is not on disk")
    for flow in sorted(on_disk):
        if flow not in claimed and flow not in EXTRAS:
            problems.append(
                f"{flow} is not named by any §13.10 scenario and is not a declared extra. "
                "Point a scenario at it, or add it to EXTRAS with the reason it exists."
            )
    return problems


def main() -> int:
    problems = check()
    if problems:
        print("Maestro flows:")
        for problem in problems:
            print(f"  {problem}")
        return 1
    print(f"Maestro flows: {len(flow_files())} checked, all sound.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
