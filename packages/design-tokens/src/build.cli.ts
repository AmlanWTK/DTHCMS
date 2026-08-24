/**
 * The command-line entry for the token build.
 *
 *   pnpm --filter @dthcms/design-tokens build
 *
 * This file exists so `build.ts` can be imported without writing to disk. The test calls
 * `build()` directly rather than spawning a subprocess — `execFileSync('npx', ...)` passed
 * on Linux and failed on Windows with ENOENT, because npx there is `npx.cmd` and
 * `execFileSync` without a shell will not resolve one.
 */

import { build } from './build';

// eslint-disable-next-line no-console
build((line) => console.log(line));
