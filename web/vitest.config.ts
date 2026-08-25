import { fileURLToPath } from 'node:url';

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

import { DEFAULT_FLOOR, coverage } from '@dthcms/test-config';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    coverage: coverage(DEFAULT_FLOOR, {
      // Route files are page shells whose gate is Playwright, in web/e2e — running in a
      // real browser against a production build, where the Content Security Policy and
      // the error boundaries behave as they will in the clinic. Counting them here would
      // measure the wrong layer and invite jsdom tests that prove a page imports.
      exclude: ['src/app/**'],
    }),
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./test/setup.ts'],
    // Playwright lives in e2e/ and runs from `pnpm run e2e`. It needs a browser download
    // and a built application, so it is deliberately not part of `pnpm run verify`:
    // a verification step that cannot run on a fresh clone is a verification step people
    // learn to skip.
    exclude: ['node_modules/**', 'e2e/**', '.next/**'],
  },
});
