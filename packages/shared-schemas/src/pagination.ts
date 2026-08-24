import { z } from 'zod';

/**
 * Cursor pagination — see docs/api-conventions.md §3 for why cursors rather than pages.
 *
 * The short version: clinical lists change underneath the person reading them. A queue
 * gains a patient while an operator is on page two, and with offsets that shifts every
 * subsequent row — so the operator sees one patient twice and never sees another at all.
 */

export const pageInfoSchema = z.object({
  /** Pass back as `cursor` for the next page. `null` on the last page. */
  next_cursor: z.string().nullable().optional(),
  /**
   * The only reliable end-of-list signal. A page shorter than `limit` does not mean the
   * end — the server may return fewer items than asked for at any time.
   */
  has_more: z.boolean(),
});

export type PageInfo = z.infer<typeof pageInfoSchema>;

/**
 * A list response: `{ items: [...], page: { ... } }`.
 *
 * Every list endpoint in DTHCMS returns this shape, so a screen that can render one
 * paginated list can render all of them.
 */
export function cursorPageSchema<T extends z.ZodTypeAny>(item: T) {
  return z.object({
    items: z.array(item),
    page: pageInfoSchema,
  });
}

export type CursorPage<T> = {
  items: T[];
  page: PageInfo;
};
