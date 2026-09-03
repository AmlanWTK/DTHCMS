import { fileURLToPath } from 'node:url';

import { defineConfig } from 'vitest/config';

import { DEFAULT_FLOOR, coverage } from '@dthcms/test-config';

export default defineConfig({
  resolve: {
    /*
     * The same `@/` alias Metro and tsc resolve.
     *
     * Its absence is why `lib/secure-storage.ts` had no test until CP13: the module
     * imports `@/lib/secure-keys`, so any test that touched it failed to resolve and the
     * easiest thing was to test the allowlist in isolation instead. A missing alias
     * quietly decides which modules get tested.
     */
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      // The native random source, replaced wholesale rather than mocked per test: every
      // test that touches the device module needs it, and none needs the real one.
      'expo-crypto': fileURLToPath(new URL('./test/stubs/expo-crypto.ts', import.meta.url)),
    },
  },
  test: {
    coverage: coverage(DEFAULT_FLOOR, {
      /*
       * Screens and components are gated on hardware, not here — the Maestro flow on the
       * clinic's device, once D-59 names it. CP11 recorded that deliberately: rendering
       * React Native in jsdom proves nothing a device would not disprove.
       *
       * This is the one exclusion in the repository that does NOT currently name a live
       * covering layer, because Maestro cannot run until the device is chosen. It is a
       * known, recorded gap rather than a hidden one, and it closes when D-59 does.
       */
      exclude: [
        'src/app/**',
        'src/components/**',
        // The two React modules in lib, for the same reason and with the same gap: a
        // provider and a hook that wire NetInfo and use-intl into component state. There
        // is nothing to assert about either without rendering.
        'src/lib/i18n.tsx',
        'src/lib/connectivity.ts',
      ],
    }),
    environment: 'node',
    globals: true,
    // Only pure-TypeScript tests run here: message discipline, the navigation
    // definition, the secure-key allowlist, the token pipeline. Anything that renders a
    // React Native component needs a device or an emulator, and pretending otherwise in
    // jsdom proves nothing — that is the Maestro flow's job, on hardware, when the
    // clinic's device is confirmed (see docs/mobile-shell.md).
    include: ['test/**/*.test.ts'],
  },
});
