import { ApiError, writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, authenticatedFetch, unwrap } from '@/lib/api';

/**
 * The medicine formulary, typed against the contract (CP75, §10, §16.1, D-56).
 *
 * # What the screen behind this is for
 *
 * Two people open it. A pharmacist doing the monthly price review, and a physician checking
 * what a brand costs before he prescribes it. Everything here is arranged so that the first
 * one's question — *which of these has nobody checked* — is a filter rather than a report they
 * have to assemble.
 *
 * # A provisional price is not a price
 *
 * All 250 seeded prices are published MRP from medex.com.bd read on 8 September 2026. They are
 * **not what this clinic charges**, and nobody has reviewed them. `verification` carries that on
 * every price, and there is deliberately no helper here that hides it: `priceLabel` takes the
 * whole price rather than the amount, so a component cannot render the number without having
 * been handed the fact that nobody has checked it.
 *
 * # The amount is text as well as an integer, and the text is what you draw
 *
 * `amount_poisha` is the number to compute with; `amount_bdt` is the string to render. Never
 * `amount_poisha / 100` in TypeScript — that is a float division, and 0.34 is exactly the value
 * it gets wrong. The server has already done it in integer arithmetic.
 *
 * # No patient is ever in any of this
 *
 * There is no patient id in the formulary, and nothing in this module logs or reports who looked
 * up what. That is what lets the pharmacist — a role §4.4 blinds from clinical data — own the
 * screen.
 */

export type FormularyProduct = components['schemas']['FormularyProduct'];
export type FormularyPrice = components['schemas']['FormularyPrice'];
export type FormularyCatalogue = components['schemas']['FormularyCatalogue'];
export type FormularyImport = components['schemas']['FormularyImport'];
export type FormularyImportRow = components['schemas']['FormularyImportRow'];
export type FormularyReviewState = components['schemas']['FormularyReviewState'];
export type FormularyGeneric = components['schemas']['FormularyGeneric'];

/* ------------------------------------------------------------------------- */
/* Cache keys                                                                 */
/* ------------------------------------------------------------------------- */

export const FORMULARY_KEY = ['formulary'] as const;
export const CATALOGUE_KEY = ['formulary', 'catalogue'] as const;
export const REVIEW_KEY = ['formulary', 'review'] as const;
export const IMPORTS_KEY = ['formulary', 'imports'] as const;

export interface ProductFilter {
  q: string;
  klass: string;
  activeOnly: boolean;
  unverifiedOnly: boolean;
}

export function productsKey(filter: ProductFilter) {
  // The filter is part of the key. "Everything" and "only the unchecked ones" are different
  // answers to different questions, and a cache that treated them as one would show the review's
  // working list under the full formulary's heading.
  return [
    'formulary',
    'products',
    filter.q,
    filter.klass,
    filter.activeOnly,
    filter.unverifiedOnly,
  ] as const;
}

export function pricesKey(productId: string) {
  return ['formulary', 'prices', productId] as const;
}

export function importKey(id: string) {
  return ['formulary', 'import', id] as const;
}

/* ------------------------------------------------------------------------- */
/* Reads                                                                      */
/* ------------------------------------------------------------------------- */

export async function getCatalogue(): Promise<FormularyCatalogue> {
  return unwrap(api.GET('/v1/formulary/catalogue', {}));
}

export async function listProducts(filter: ProductFilter, limit = 100) {
  return unwrap(
    api.GET('/v1/formulary/products', {
      params: {
        query: {
          q: filter.q || undefined,
          class: filter.klass || undefined,
          active: filter.activeOnly || undefined,
          unverified: filter.unverifiedOnly || undefined,
          limit,
        },
      },
    }),
  );
}

export async function getPriceHistory(productId: string): Promise<FormularyPrice[]> {
  const body = await unwrap(
    api.GET('/v1/formulary/products/{id}/prices', {
      params: { path: { id: productId } },
    }),
  );
  return body.prices;
}

/**
 * What a medicine cost on a given day — the checkpoint's first criterion.
 *
 * `null` when nothing covered that day, which the server answers with a `404`. That is a real
 * state and not an error: every history starts somewhere, and a screen that showed a zero would
 * be telling somebody a medicine was free.
 */
export async function getPriceOn(productId: string, on: string): Promise<FormularyPrice | null> {
  try {
    const body = await unwrap(
      api.GET('/v1/formulary/products/{id}/price', {
        params: { path: { id: productId }, query: { on } },
      }),
    );
    return body.price;
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null;
    throw error;
  }
}

export async function getReview(): Promise<FormularyReviewState> {
  return unwrap(api.GET('/v1/formulary/review', {}));
}

export async function listImports(): Promise<FormularyImport[]> {
  const body = await unwrap(api.GET('/v1/formulary/imports', {}));
  return body.imports;
}

/* ------------------------------------------------------------------------- */
/* Writes                                                                     */
/* ------------------------------------------------------------------------- */

export interface RecordPriceInput {
  productId: string;
  amountBdt: string;
  effectiveFrom?: string;
  verified: boolean;
  sourceNote?: string;
}

export async function recordPrice(input: RecordPriceInput): Promise<FormularyPrice> {
  const body = await unwrap(
    api.POST('/v1/formulary/products/{id}/prices', {
      params: { ...writing(), path: { id: input.productId } },
      body: {
        amount_bdt: input.amountBdt,
        effective_from: input.effectiveFrom,
        verified: input.verified,
        source_note: input.sourceNote,
      },
    }),
  );
  return body.price;
}

export async function withdrawProduct(productId: string, reason: string) {
  return unwrap(
    api.POST('/v1/formulary/products/{id}/withdraw', {
      params: { ...writing(), path: { id: productId } },
      body: { reason },
    }),
  );
}

export async function reinstateProduct(productId: string) {
  return unwrap(
    api.POST('/v1/formulary/products/{id}/reinstate', {
      params: { ...writing(), path: { id: productId } },
    }),
  );
}

export async function completeReview(
  reviewId: string,
  note: string,
): Promise<FormularyReviewState> {
  return unwrap(
    api.POST('/v1/formulary/review/{id}/complete', {
      params: { ...writing(), path: { id: reviewId } },
      body: { note: note || undefined },
    }),
  );
}

/**
 * Upload a price list.
 *
 * The typed client is not used for this one: the body is `text/csv`, which the generated types
 * describe as a string but which `openapi-fetch` would serialise as JSON. The raw authenticated
 * fetch sends the bytes, and the response is the same report shape either way.
 *
 * `apply` defaults to **false**, matching the server. A screen whose upload button applied by
 * default would make the dry run a thing somebody had to remember to ask for.
 */
export async function runImport(options: {
  file: File;
  apply: boolean;
  verified: boolean;
}): Promise<FormularyImport> {
  const query = new URLSearchParams({
    mode: options.apply ? 'apply' : 'dry-run',
    filename: options.file.name,
  });
  if (options.verified) query.set('verified', 'true');

  const response = await authenticatedFetch(`/v1/formulary/imports?${query.toString()}`, {
    method: 'POST',
    // The typed client sets this on every call; this one does not go through it, and without
    // it the browser withholds the `dthcms.session` cookie and the upload comes back 401 with
    // nothing on the screen to say why (ADR-0010: the web application never holds the token —
    // the cookie is the whole of its credential).
    credentials: 'include',
    headers: {
      'Content-Type': 'text/csv',
      'X-Requested-With': 'DTHCMS',
      'Idempotency-Key': crypto.randomUUID(),
    },
    body: await options.file.text(),
  });
  const body = (await response.json()) as {
    import?: FormularyImport;
    error?: {
      code?: string;
      kind?: string;
      message?: string;
      message_bn?: string;
      fields?: Record<string, string>;
      fields_bn?: Record<string, string>;
      correlation_id?: string;
    };
  };
  if (!response.ok) {
    // The same envelope every other refusal uses, rebuilt by hand because this one request
    // does not go through the typed client. A screen that got a bare Error here would have
    // lost the bilingual message and the correlation id — which is the pair a person in the
    // clinic photographs when something goes wrong.
    throw new ApiError({
      status: response.status,
      code: body.error?.code ?? 'INTERNAL',
      kind: body.error?.kind ?? 'technical',
      messageEN: body.error?.message ?? 'The upload could not be read.',
      messageBN: body.error?.message_bn ?? 'ফাইলটি পড়া যায়নি।',
      fields: body.error?.fields,
      fieldsBN: body.error?.fields_bn,
      correlationID: body.error?.correlation_id ?? response.headers.get('X-Request-ID') ?? '',
    });
  }
  return body.import as FormularyImport;
}

/* ------------------------------------------------------------------------- */
/* Predicates the screen reasons with                                         */
/* ------------------------------------------------------------------------- */

/**
 * Whether a person at this clinic has confirmed this is what it charges.
 *
 * Takes the price or its absence, because "nobody has checked this" and "nobody has priced this"
 * are both *not confirmed* and a caller that had to remember to handle the second separately
 * would eventually not.
 */
export function isConfirmed(price: FormularyPrice | undefined | null): boolean {
  return price?.verification === 'VERIFIED';
}

/** A seeded price: published MRP the migration loaded, which nobody here has looked at. */
export function isSeeded(price: FormularyPrice | undefined | null): boolean {
  return price?.origin === 'SEED';
}

/** Rejections first, then file order — the order the server returns and the screen keeps. */
export function rejectedRows(report: FormularyImport): FormularyImportRow[] {
  return (report.rows ?? []).filter((row) => row.outcome === 'REJECTED');
}

export function acceptedRows(report: FormularyImport): FormularyImportRow[] {
  return (report.rows ?? []).filter((row) => row.outcome !== 'REJECTED');
}

/**
 * How overdue the review is, in days, or null when it is not.
 *
 * `null` rather than a negative number: "not due yet" is a different sentence from "due in three
 * days", and a component branching on the sign of a number is one that will eventually render
 * "overdue by -3 days".
 */
export function daysOverdue(review: FormularyReviewState['current'], today: Date): number | null {
  if (!review || review.status !== 'OPEN') return null;
  const due = new Date(`${review.due_on}T00:00:00Z`);
  const days = Math.floor((today.getTime() - due.getTime()) / 86_400_000);
  return days > 0 ? days : null;
}

/** The conflict code on a 409, so the screen can say what actually happened. */
export function conflictOf(error: unknown): string | null {
  if (error instanceof ApiError && error.status === 409) return error.code;
  return null;
}

/** How long a list of products is, counting only the ones nobody has confirmed. */
export function unconfirmedCount(products: FormularyProduct[]): number {
  return products.filter((product) => !isConfirmed(product.price)).length;
}

/* ------------------------------------------------------------------------- */
/* CP76 — the prescribing autocomplete                                        */
/* ------------------------------------------------------------------------- */

export type FormularySearchResult = components['schemas']['FormularySearchResult'];
export type FormularySearchEntry = components['schemas']['FormularySearchEntry'];
export type FormularySearchStrength = components['schemas']['FormularySearchStrength'];

export function searchKey(query: string, limit: number) {
  return ['formulary', 'search', query, limit] as const;
}

/**
 * The two-letter autocomplete (CP76, §10.1).
 *
 * A separate call from `listProducts`, and deliberately so: that one is the pharmacist's admin
 * list, ordered by class and molecule so the four metformins sit together. This one is ranked for
 * a physician mid-prescription. One endpoint serving both would be two answers to one question,
 * and the one that would quietly win is whichever screen was written second.
 *
 * `q` is sent as typed — Latin or Bengali. The server transliterates and reports what it made of
 * it in `normalised_query`, which is the only way the Bengali case is explainable to the person
 * typing: they typed কম and got Comet.
 */
export async function searchFormulary(query: string, limit = 8): Promise<FormularySearchResult> {
  return unwrap(api.GET('/v1/formulary/search', { params: { query: { q: query, limit } } }));
}
