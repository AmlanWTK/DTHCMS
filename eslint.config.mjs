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
      '**/storybook-static/**',
      '**/test-results/**',
      '**/playwright-report/**',
      '**/next-env.d.ts',
      // Throwaway working files. Gitignored, never shipped, and holding them to the
      // rules that apply to shipped code only produces noise that trains people to
      // ignore lint output.
      'scratch/**',
    ],
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
  {
    /*
     * ADR-0010: session credentials never touch web storage.
     *
     * The decision is recorded in docs/adr/0010-no-session-tokens-in-web-storage.md. This
     * is what makes it hold. A token in localStorage turns one cross-site scripting hole
     * into stolen credentials that keep working from the attacker's own machine, and this
     * application renders patient names, free-text notes and OCR output from photographs
     * of paper the clinic did not write — which is to say, text from outside.
     *
     * The rule is deliberately a build failure rather than a review note. It is the kind
     * of shortcut that gets taken at the end of a long day, by someone who means to come
     * back to it.
     *
     * A screen that genuinely needs storage for something that is not a credential — a
     * remembered filter, an unsent draft — adds a disable comment at that one call site,
     * saying why. Then the exception is visible in review, which is the point.
     */
    files: ['web/src/**/*.{ts,tsx}', 'mobile/src/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-globals': [
        'error',
        {
          name: 'localStorage',
          message:
            'ADR-0010: no session credential in web storage. Sessions are httpOnly cookies. If this is not a credential, disable this rule on the line and say what it is.',
        },
        {
          name: 'sessionStorage',
          message:
            'ADR-0010: no session credential in web storage. Sessions are httpOnly cookies. If this is not a credential, disable this rule on the line and say what it is.',
        },
      ],
      'no-restricted-properties': [
        'error',
        {
          object: 'window',
          property: 'localStorage',
          message: 'ADR-0010: no session credential in web storage.',
        },
        {
          object: 'window',
          property: 'sessionStorage',
          message: 'ADR-0010: no session credential in web storage.',
        },
        {
          object: 'globalThis',
          property: 'localStorage',
          message: 'ADR-0010: no session credential in web storage.',
        },
        {
          object: 'document',
          property: 'cookie',
          message:
            'ADR-0010: a cookie JavaScript can read is not httpOnly. The locale cookie is written by a server action; a session cookie is written by the server and never read here.',
        },
      ],
    },
  },
);
