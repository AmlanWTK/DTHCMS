import { test as base, type Page } from '@playwright/test';

/**
 * Fixtures for the browser suite.
 *
 * CP10's `shell.spec.ts` needs none of this — it walks routes and asserts landmarks. What
 * needs it is everything from CP16 onward, where a spec opens with "given a signed-in
 * physician looking at a patient" and the setup is four steps nobody should retype.
 *
 * Kept deliberately thin. A page-object layer that mirrors every component is a second
 * application to maintain, and it rots the moment somebody renames a heading. These are
 * the things that are genuinely awkward to do inline: switching language, and standing in
 * for a session that does not exist yet.
 */

export interface DthcmsFixtures {
  /** The application in Bangla, as half the clinic's staff will see it. */
  bangla: Page;
  /** A signed-in operator. Inert until CP16 — see below. */
  signedIn: Page;
}

export const test = base.extend<DthcmsFixtures>({
  bangla: async ({ page }, use) => {
    /*
     * The locale lives on the person, not in the URL (docs/web-shell.md §1), so there is
     * no `/bn` prefix to navigate to. It is a cookie, written by a server action.
     */
    await page
      .context()
      .addCookies([{ name: 'DTHCMS_LOCALE', value: 'bn', url: 'http://127.0.0.1:3100' }]);
    await use(page);
  },

  signedIn: async ({ page }, use) => {
    /*
     * A placeholder with the right shape.
     *
     * Authentication lands at CP16. Until then the `/v1` middleware chain is a set of
     * pass-throughs and every screen renders for anybody, so this fixture does nothing
     * but exist — which is the point. Specs written against `signedIn` today keep working
     * when CP16 fills it in, and the alternative is twenty specs each doing their own
     * sign-in the day it becomes real.
     */
    await use(page);
  },
});

export { expect } from '@playwright/test';
