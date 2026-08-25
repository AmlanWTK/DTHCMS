import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';
import { z } from 'zod';

import {
  cursorPageSchema,
  errorBodySchema,
  errorEnvelopeSchema,
  errorKindSchema,
  isKnownErrorKind,
  livenessResponseSchema,
  pageInfoSchema,
  readinessResponseSchema,
  versionResponseSchema,
} from '../src/index';

/**
 * These schemas are hand-written, and the document they mirror is hand-written too. That
 * is two places to change and one to forget, so this file closes the gap: it reads
 * api/openapi.yaml and fails when a schema here and the contract there disagree.
 *
 * Property *names* are checked rather than full JSON-Schema equivalence. Names are what
 * break silently — a field renamed on the wire arrives as `undefined` and is rendered as
 * a blank cell. A loosened type shows up in review; a renamed field does not.
 */

const here = dirname(fileURLToPath(import.meta.url));
const specPath = resolve(here, '../../../api/openapi.yaml');
const spec = parse(readFileSync(specPath, 'utf8')) as {
  components: {
    schemas: Record<string, { properties?: Record<string, unknown>; enum?: string[] }>;
  };
};

/** The property names a Zod object schema accepts. */
function keysOf(schema: z.ZodObject<z.ZodRawShape>): string[] {
  return Object.keys(schema.shape).sort();
}

/** The property names a component schema in the OpenAPI document declares. */
function specKeysOf(name: string): string[] {
  const component = spec.components.schemas[name];
  expect(component, `api/openapi.yaml has no component schema named ${name}`).toBeDefined();
  return Object.keys(component?.properties ?? {}).sort();
}

describe('the Zod schemas match api/openapi.yaml', () => {
  it.each([
    ['ErrorBody', errorBodySchema],
    ['PageInfo', pageInfoSchema],
    ['LivenessResponse', livenessResponseSchema],
    ['ReadinessResponse', readinessResponseSchema],
    ['VersionResponse', versionResponseSchema],
  ])('%s declares the same properties', (name, schema) => {
    expect(keysOf(schema as z.ZodObject<z.ZodRawShape>)).toEqual(specKeysOf(name));
  });

  it('the envelope wraps the body under `error`, as the contract says', () => {
    expect(keysOf(errorEnvelopeSchema)).toEqual(specKeysOf('Error'));
  });

  it('ErrorKind lists exactly the kinds the contract does', () => {
    expect([...errorKindSchema.options].sort()).toEqual(
      [...(spec.components.schemas.ErrorKind?.enum ?? [])].sort(),
    );
  });
});

describe('the error envelope', () => {
  const valid = {
    error: {
      code: 'VALIDATION_FAILED',
      kind: 'validation',
      message: 'Some values need correcting.',
      message_bn: 'কিছু তথ্য সংশোধন করতে হবে।',
      fields: { date_of_birth: 'A date of birth cannot be in the future.' },
      correlation_id: '0198c4e2-7f3a-7000-8c1d-2b4e6a8f0c3d',
    },
  };

  it('accepts the shape the backend sends', () => {
    expect(errorEnvelopeSchema.parse(valid)).toEqual(valid);
  });

  it('accepts one without the optional parts — most errors have neither', () => {
    const minimal = {
      error: {
        code: 'NOT_FOUND',
        kind: 'not_found',
        message: 'Not found.',
        message_bn: 'পাওয়া যায়নি।',
      },
    };
    expect(errorEnvelopeSchema.safeParse(minimal).success).toBe(true);
  });

  it('requires the Bangla message — a missing one is a bug, not a fallback', () => {
    const noBangla = {
      error: { code: 'NOT_FOUND', kind: 'not_found', message: 'Not found.' },
    };
    expect(errorEnvelopeSchema.safeParse(noBangla).success).toBe(false);
  });

  it('accepts a kind this build has never heard of', () => {
    // Adding an enum member is an additive change the versioning rule permits. An old
    // station build must still show the operator the message rather than failing to
    // parse the envelope carrying it.
    const future = {
      error: {
        code: 'SOMETHING_NEW',
        kind: 'a_kind_invented_at_cp90',
        message: 'A new kind of problem.',
        message_bn: 'নতুন ধরনের সমস্যা।',
      },
    };
    expect(errorEnvelopeSchema.safeParse(future).success).toBe(true);
    expect(isKnownErrorKind('a_kind_invented_at_cp90')).toBe(false);
    expect(isKnownErrorKind('clinical')).toBe(true);
  });
});

describe('cursor pagination', () => {
  it('treats a null cursor as the last page', () => {
    expect(pageInfoSchema.parse({ next_cursor: null, has_more: false })).toEqual({
      next_cursor: null,
      has_more: false,
    });
  });

  it('requires has_more — a short page is not an end-of-list signal', () => {
    expect(pageInfoSchema.safeParse({ next_cursor: 'abc' }).success).toBe(false);
  });
});

describe('a list response', () => {
  const patient = z.object({ id: z.string(), name: z.string() });

  it('wraps items and page information together', () => {
    // Every list endpoint returns this shape, so a screen that can render one paginated
    // list can render all of them.
    const page = cursorPageSchema(patient);
    const parsed = page.parse({
      items: [{ id: '1', name: 'A' }],
      page: { next_cursor: 'abc', has_more: true },
    });

    expect(parsed.items).toHaveLength(1);
    expect(parsed.page.has_more).toBe(true);
  });

  it('accepts an empty list — no patients waiting is information, not absence', () => {
    const page = cursorPageSchema(patient);
    expect(page.parse({ items: [], page: { next_cursor: null, has_more: false } }).items).toEqual(
      [],
    );
  });

  it('rejects items that are not the shape the caller asked for', () => {
    // The generated types describe what the contract promises; this is what the client
    // will accept. A renamed field fails here rather than as a blank table cell.
    const page = cursorPageSchema(patient);
    expect(page.safeParse({ items: [{ id: '1' }], page: { has_more: false } }).success).toBe(false);
  });

  it('requires the page block, so pagination cannot be silently dropped', () => {
    const page = cursorPageSchema(patient);
    expect(page.safeParse({ items: [] }).success).toBe(false);
  });
});
