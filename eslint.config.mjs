import js from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    ignores: [
      '**/node_modules/**',
      '**/dist/**',
      '**/build/**',
      '**/.next/**',
      '**/coverage/**',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    rules: {
      // Placeholder rule set. Project-wide standards are defined in CP02 —
      // this configuration exists only to prove the linter runs in CI.
      'no-console': 'warn',
    },
  },
);
