import { defineConfig, devices } from '@playwright/test';

/**
 * End-to-end configuration.
 *
 * Deliberately not part of `pnpm run verify`. This suite needs a browser download and a
 * production build, and a verification step that cannot run on a fresh clone is a
 * verification step people learn to skip. It runs from `pnpm run e2e`, after
 * `pnpm run e2e:install` once.
 *
 * It tests the production build rather than the dev server, because the two differ in
 * exactly the places this suite cares about: the Content Security Policy drops its
 * development relaxations, and the error boundary behaves differently when React is not
 * in development mode.
 */

const PORT = 3100;

/**
 * An escape hatch for a machine that already has a Chromium build.
 *
 * `pnpm run e2e:install` downloads the browser Playwright expects, which is the normal
 * path and the one CI takes. It is set when a sandbox or a build image ships its own
 * Chromium at a fixed revision, so the suite can run there without a second download of
 * a browser that is already present.
 */
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',

  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: 'on-first-retry',
  },

  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        ...(executablePath ? { channel: undefined, launchOptions: { executablePath } } : {}),
      },
    },
  ],

  webServer: {
    command: 'pnpm run build && pnpm run start',
    url: `http://127.0.0.1:${PORT}/login`,
    reuseExistingServer: !process.env.CI,
    timeout: 240_000,
    /*
     * The error probe is a route that throws, so acceptance criterion 3 can be proved
     * rather than assumed. It is a 404 without this flag, including in production.
     *
     * Set here rather than as a shell prefix on the command: `VAR=1 pnpm ...` is a Unix
     * shell construct and does nothing on Windows, which is where this repository is
     * developed.
     */
    env: { DTHCMS_ENABLE_ERROR_PROBE: '1' },
  },
});
