import { z } from 'zod';

/**
 * The error envelope, as a runtime schema.
 *
 * The generated types in `@dthcms/api-client` describe what the contract *promises*.
 * This describes what the client will *accept* — and the two are not the same job. A
 * type is erased at runtime; a deployed backend that renames a field ships happily past
 * `tsc` and surfaces as `undefined` in a table cell, three screens away from the cause.
 *
 * So errors are parsed rather than cast. It costs a few microseconds on a path that is
 * already failing, and it turns a silent contract break into one readable message in one
 * place.
 */

/** Mirrors `ErrorKind` in api/openapi.yaml, and `errs.Kind` in the Go platform layer. */
export const errorKindSchema = z.enum([
  'validation',
  'auth',
  'not_found',
  'conflict',
  'clinical',
  'technical',
]);

export type ErrorKind = z.infer<typeof errorKindSchema>;

/**
 * `kind` is parsed leniently on purpose.
 *
 * Adding an enum member is an additive change the versioning rule permits, so a station
 * tablet running a three-week-old build will one day meet a kind it has never heard of.
 * Rejecting the whole envelope at that point would replace a specific, actionable error
 * with a generic parse failure — the interface would lose the message it was about to
 * show the operator in order to be pedantic about a field it uses for styling.
 */
export const errorBodySchema = z.object({
  code: z.string(),
  kind: z.string(),
  message: z.string(),
  message_bn: z.string(),
  fields: z.record(z.string(), z.string()).optional(),
  /**
   * The same per-field messages in Bangla, under the same keys (CP29).
   *
   * A parallel record rather than an object per field, because `fields` was already
   * published and consumed; a build that reads only `fields` keeps working. An interface
   * showing a validation error should prefer this one when the locale is `bn` and fall
   * back to `fields` — an English sentence is better than no sentence.
   */
  fields_bn: z.record(z.string(), z.string()).optional(),
  correlation_id: z.string().optional(),
});

export const errorEnvelopeSchema = z.object({
  error: errorBodySchema,
});

export type ErrorEnvelope = z.infer<typeof errorEnvelopeSchema>;
export type ErrorBody = z.infer<typeof errorBodySchema>;

/** Whether a kind is one this build knows how to treat specially. */
export function isKnownErrorKind(kind: string): kind is ErrorKind {
  return errorKindSchema.safeParse(kind).success;
}
