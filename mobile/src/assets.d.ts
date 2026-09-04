/**
 * Asset modules, as Metro resolves them.
 *
 * Metro turns `import alarm from './x.wav'` into an integer module id, which is what every
 * Expo media API takes. TypeScript does not know that, and the alternative — `require()` at
 * the call site — is a form this repository's ESLint configuration forbids, for the good
 * reason that it hides a dependency from every tool that reads imports.
 *
 * Declared here rather than in a component so that the next asset, of whatever kind, does not
 * arrive with a second copy of this reasoning.
 */
declare module '*.wav' {
  const asset: number;
  export default asset;
}
