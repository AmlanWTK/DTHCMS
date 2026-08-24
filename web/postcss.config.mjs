/*
 * Tailwind v4 is used for layout only.
 *
 * The primitives in @dthcms/ui ship their own stylesheet keyed to token variables, on
 * purpose: a shared component library styled with utility classes renders correctly only
 * if every consumer configures Tailwind identically. Application layout has no such
 * problem, and utilities are good at exactly the churn that layout goes through.
 */
const config = {
  plugins: {
    '@tailwindcss/postcss': {},
  },
};

export default config;
