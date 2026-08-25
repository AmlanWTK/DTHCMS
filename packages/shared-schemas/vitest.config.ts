import { defineConfig } from 'vitest/config';

import { DEFAULT_FLOOR, coverage } from '@dthcms/test-config';

export default defineConfig({
  test: {
    coverage: coverage(DEFAULT_FLOOR),
  },
});
