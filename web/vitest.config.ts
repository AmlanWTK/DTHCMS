import { fileURLToPath } from 'node:url';

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
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
