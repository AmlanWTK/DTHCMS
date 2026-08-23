/**
 * Joins class names, dropping anything falsy.
 *
 * Deliberately not `clsx` or `classnames`. This is four lines, it is the only string
 * manipulation the component layer needs, and a shared primitive package that pulls a
 * dependency for it exports that dependency's version to every application downstream.
 */
export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ');
}
