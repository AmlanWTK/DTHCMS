import js from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    ignores: ['**/node_modules/**', '**/dist/**', '**/build/**', '**/.next/**', '**/coverage/**'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    // Tooling configuration files are CommonJS and run in Node.
    files: ['**/*.cjs'],
    languageOptions: {
      sourceType: 'commonjs',
      globals: {
        module: 'writable',
        require: 'readonly',
        process: 'readonly',
        __dirname: 'readonly',
      },
    },
  },
  {
    rules: {
      'no-console': 'warn',

      // Feature boundaries (docs/architecture-boundaries.md). A feature exposes its
      // public surface through index.ts; reaching into another feature's internals
      // couples the two and is what turns a modular frontend into a tangle.
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['**/features/*/*'],
              message:
                'Import a feature through its index.ts, not its internals. If you need something it does not export, export it deliberately — or the code belongs somewhere else.',
            },
            {
              group: ['**/../../*'],
              message:
                'Deep relative imports cross boundaries invisibly. Use the workspace alias or the feature index.',
            },
          ],
        },
      ],
    },
  },
);
