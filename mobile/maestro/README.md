# `mobile/maestro/` — the station app on hardware

**These flows cannot run yet.** They wait on **D-59**: the clinic's device model and
Android floor. That is not a scheduling excuse — CP11's acceptance criteria 1–3 are
measured on the clinic's actual hardware, and a flow that passes on an emulator says
nothing about a five-year-old tablet on a clinic's Wi-Fi.

They are written now so that the day the device is chosen, verification is a command
rather than a project.

```bash
maestro test mobile/maestro/smoke.yaml       # once a device is connected
maestro test mobile/maestro/                 # the whole suite
```

## Why Maestro rather than jsdom

CP11 recorded the reasoning: rendering React Native components in jsdom proves nothing a
device would disprove. Mobile's Vitest suite covers message discipline, the navigation
definition, the secure-key allowlist and the crash scrubber — all pure TypeScript. The
compile gate is `bundle:check`, which runs Metro over every screen on each push. The
rendering gate is this directory, on hardware.

That is also why `mobile/src/app/**` and `mobile/src/components/**` sit outside the
coverage denominator. It is the one exclusion in the repository whose covering layer is
not yet live, and it closes when D-59 does.
