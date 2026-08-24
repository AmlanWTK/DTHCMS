# The station application shell

Established at CP11 — partially, on purpose. The software side is complete and verified
as far as software can be verified without the clinic's hardware; the on-device half of
the checkpoint waits on a decision only Dr. Nahid can take. This page is the decisions
and the honest state.

Detail lives beside the code — [`mobile/README.md`](../mobile/README.md).

---

## 1. What is done, and what is deliberately not

Done: the Expo project (SDK 57, Expo Router, NativeWind), the five route groups from
§14.10 with a real queue empty-state and an inert login placeholder, bilingual i18n with
the same automated completeness check as the web, the secure-storage allowlist, the
connectivity banner, the crash-capture seam with PHI scrubbing, `allowBackup=false`, a
CI step that compiles the Android bundle on every push, and a dormant EAS build job.

**Not done, and recorded rather than pretended:** everything the checkpoint measures on
hardware. Acceptance criteria 1–3 — installs on the clinic's device, cold start ≤3s on
it, Bangla at 200% font scale on it — are meaningless until D-59 names that device; the
plan says so itself. The Maestro smoke flow and the startup measurement land with the
device. Criterion 5 (a CI-built APK) is committed and gated on an `EXPO_TOKEN` secret,
so it turns on the day the Expo account exists, with no further change.

## 2. The bundle check is the compile gate

No emulator runs in CI, and a test that pretends React Native renders in jsdom proves
nothing a device would not disprove. What CI does instead is `expo export`: Metro
compiles every screen, resolves every import, processes NativeWind and emits the Hermes
bytecode bundle — headlessly, on every push.

It caught a real defect on its first run: NativeWind's JSX runtime
(`react-native-css-interop`) is a transitive dependency that pnpm's strict linking hides
from Metro, so every screen failed to resolve. The fix is a declared dependency; the
lesson is the same one CP10 taught twice — a compile that never runs is a compile that
passes.

## 3. One token source, third surface

The NativeWind theme is the generated `nativewind-theme.js`, from the same JSON that
produces the web CSS variables and the print stylesheet; CP09's build test asserts all
three agree. Classes cover what does not change with the theme; semantic colour comes
through a `useTokens()` hook that picks the light or dark palette by the device's colour
scheme — and a test greps `mobile/src` for hex literals, so a screen cannot smuggle in a
colour any more than a web stylesheet can.

`AppText` is where CP09's bilingual rule lives on mobile: font family and line height
switch together with the language, never separately, and `variant="clinicalValue"` pins
Latin digits regardless of interface language. Both faces are compiled into the APK — a
station tablet may be offline all session, and Android's fallback Bengali face has poor
conjunct coverage.

## 4. Secure storage is an allowlist, not a convention

Criterion 4 says nothing sensitive outside secure storage. The wrapper enforces the
inverse too: every key that touches the Keystore is declared in `SECURE_KEYS` with a
comment saying what it holds and why it is sensitive, and an undeclared key throws. The
complete inventory of the app's secrets is one screenful, which is what an auditor —
or CP16's reviewer — actually needs.

AsyncStorage is banned by lint the way `localStorage` is on the web (ADR-0010): it is
plaintext on disk. Durable non-sensitive state — the language preference, today — waits
for the encrypted local database at CP64, and until then falls back to the device
locale on restart, which is almost always the same answer.

## 5. Crash reports pass one choke point, scrubbed

The vendor needs an account nobody has; the seam cannot wait for it. Every uncaught
error passes through `lib/crash.ts`, which scrubs before anything could leave the
process: long digit runs (phones, NIDs), email addresses, values behind
known-sensitive labels. Deny-by-pattern rather than the backend's allow-by-key, because
a crash message is free text that may quote user input verbatim — an over-scrubbed
stack trace is an inconvenience, an under-scrubbed one is a breach. Wiring a vendor
later changes one function.

## 6. Departure from the plan

**`use-intl` rather than `i18n-js`.** It is the framework-agnostic core of the
`next-intl` the web already uses: same ICU message format, same key discipline, and the
same completeness test runs against both message sets. Two ICU dialects across two
surfaces is the kind of drift the token pipeline exists to prevent in colour; this
prevents it in language.

## 7. Open decisions

| Decision                                 | Default taken                          | Needs     |
| ---------------------------------------- | -------------------------------------- | --------- |
| D-59: clinic device model, Android floor | None — the acceptance tests wait on it | Dr. Nahid |
| Crash-reporting vendor                   | Local scrubbed handler, vendor seam    | Amlan     |
| Expo account for EAS builds              | CI job dormant behind `EXPO_TOKEN`     | Amlan     |

## 8. Carried forward

| Item                                                 | Blocked by            | Lands when       |
| ---------------------------------------------------- | --------------------- | ---------------- |
| Install, cold-start and 200%-font-scale verification | D-59                  | Device confirmed |
| Maestro smoke flow                                   | D-59 (needs hardware) | Device confirmed |
| EAS pipeline live                                    | Expo account          | `EXPO_TOKEN` set |
| Crash vendor wired to the seam                       | Vendor + account      | With EAS, likely |
| Language preference persistence                      | Local database        | CP64             |
