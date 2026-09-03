import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { APP_VERSION } from '../src/lib/build';

describe('the build version', () => {
  it('is the one app.json declares', () => {
    const appJson = JSON.parse(
      readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', 'app.json'), 'utf8'),
    ) as { expo: { version: string } };
    expect(APP_VERSION).toBe(appJson.expo.version);
  });
});
