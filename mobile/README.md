# `@dthcms/mobile`

The station application — the surface the plan calls primary (P-1). Twelve clinical
stations will live here; at CP11 it is the shell they slot into: navigation, theme,
language, secure storage, connectivity, crash capture.

```bash
pnpm --filter @dthcms/mobile test           # 28 assertions, plain Node
pnpm --filter @dthcms/mobile run bundle:check  # Metro compiles the Android bundle
pnpm --filter @dthcms/mobile start          # Expo dev server (QR code for a device)
pnpm --filter @dthcms/mobile run android    # build and install on a connected device
```

The first `run android` needs Android Studio's SDK and a device with USB debugging, and
builds a development client. After that, `start` alone hot-reloads onto it.

---

## Where things go

```
src/
├── app/            Expo Router. (auth) (queue) (station) (patient) (sync) — §14.10.
├── components/     AppText, AppButton, ScreenShell, LanguageToggle, OfflineBanner.
├── lib/            tokens, i18n, navigation, secure storage, connectivity, crash.
├── stores/         Zustand: session (stub), preferences.
└── messages/       en.json, bn.json — same key discipline as web/messages.
```

## Decisions worth knowing about

**Colour comes from `useTokens()`, spacing and type from NativeWind classes.** Both read
the generated `nativewind-theme.js`; a test greps `src` for hex literals so a screen
cannot drift from the other surfaces. Dark mode is the device's colour scheme picking
the other palette from the same module.

**`AppText` switches family and line height together.** Bengali matras collide with the
line above at Latin leading — the same CP09 rule, expressed as a component because RN
has no `[lang]` selector. `variant="clinicalValue"` pins Latin tabular digits whatever
the interface language. Both faces are compiled into the APK; a station tablet may be
offline all session.

**Secure storage is an allowlist.** Every Keystore key is declared in `SECURE_KEYS`
with what it holds and why; an undeclared key throws. AsyncStorage is banned by lint —
plaintext on disk. Nothing else in the shell persists yet; that is CP64's encrypted
database.

**Crashes pass one scrubbed choke point** (`lib/crash.ts`) before any reporter exists,
so wiring a vendor later changes one function, not every screen.

**Tests are plain Node on purpose.** Message completeness, the navigation definition
against the disk, the allowlist, the scrubber, the token pipeline. Rendering RN
components in jsdom proves nothing a device would not disprove — the compile gate is
`bundle:check` (Metro, in CI), and the rendering gate is the Maestro flow once the
clinic's device (D-59) is confirmed.

## On-device verification — waiting on D-59

Acceptance criteria 1–3 are measured on the clinic's actual device: install, cold start
≤3s, Bangla at 200% font scale, rotation, the largest OS font. The checklist and the
measurement method are in [`docs/mobile-shell.md`](../docs/mobile-shell.md). Until the
model is named, any phone can _review_ the shell (`run android`), but nothing can
_accept_ it.

## EAS builds

`eas.json` defines development / preview / production profiles. The CI job is dormant
until an `EXPO_TOKEN` secret exists — create the Expo account, add the token, and
criterion 5 (an installable APK from CI) switches on without a code change.
