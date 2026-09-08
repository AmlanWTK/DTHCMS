/**
 * Reading an environment variable from a test, without pretending this package is a Node package.
 *
 * `mobile/tsconfig.json` sets `types: ["vitest/globals", "nativewind/types"]` deliberately: the
 * station app runs in Hermes on a tablet, and letting `@types/node` into its global scope is how a
 * screen ends up calling something that does not exist on a device. The tests do run in Node, and
 * exactly two of them want an environment variable — the seed of a failed chaos run, so that the
 * run can be repeated by the person debugging it.
 *
 * So the global is reached through `globalThis` with a local type rather than through an ambient
 * declaration. Nothing is declared globally, nothing leaks into `src`, and the cost is this
 * paragraph.
 */

interface MaybeNode {
  process?: { env?: Record<string, string | undefined> };
}

/** One environment variable, or null when it is unset, empty, or there is no environment at all. */
export function envValue(name: string): string | null {
  const value = (globalThis as MaybeNode).process?.env?.[name];
  return value === undefined || value === '' ? null : value;
}

/**
 * An integer from the environment, or the fallback.
 *
 * Non-numeric input takes the fallback rather than failing: a mistyped seed should run the ordinary
 * suite, not turn a green build red for a reason that has nothing to do with the code.
 */
export function envSeed(name: string, fallback: number): number {
  const raw = envValue(name);
  if (raw === null) return fallback;
  const value = Number.parseInt(raw, 10);
  return Number.isFinite(value) ? value : fallback;
}
