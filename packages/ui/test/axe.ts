import axe, { type AxeResults, type RunOptions } from 'axe-core';

/**
 * Runs axe against a rendered container.
 *
 * One rule is disabled, deliberately and with a replacement: `color-contrast`. jsdom has
 * no layout engine and no canvas, so axe cannot resolve a computed colour against what is
 * actually behind it — it either errors or reports "incomplete", and a check that always
 * returns "cannot tell" is worse than no check because it looks like coverage.
 *
 * Contrast is not therefore unchecked. It is checked far more thoroughly than axe would,
 * in @dthcms/design-tokens, where every pair in the contract is measured against a stated
 * threshold with a stated reason. What axe covers here is the half that token maths
 * cannot see: labels, roles, ARIA relationships, and duplicate ids.
 */
export async function checkA11y(container: HTMLElement, options: RunOptions = {}) {
  const results: AxeResults = await axe.run(container, {
    rules: { 'color-contrast': { enabled: false } },
    ...options,
  });
  return results;
}

/** Formats violations so a failure names the element and the rule, not just a count. */
export function describeViolations(results: AxeResults): string {
  return results.violations
    .map((violation) => {
      const nodes = violation.nodes.map((node) => `      ${node.html}`).join('\n');
      return `  ${violation.id} (${violation.impact}): ${violation.help}\n${nodes}`;
    })
    .join('\n');
}
