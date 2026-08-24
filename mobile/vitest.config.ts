import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
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
