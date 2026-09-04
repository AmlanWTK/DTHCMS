/**
 * `@dthcms/shared-schemas` — the runtime half of the API contract.
 *
 * `@dthcms/api-client` carries the generated *types*; this package carries the *parsers*.
 * Both are derived from `api/openapi.yaml`, and a test in this package fails if they
 * drift apart from it.
 */

export {
  errorKindSchema,
  errorBodySchema,
  errorEnvelopeSchema,
  isKnownErrorKind,
  type ErrorKind,
  type ErrorBody,
  type ErrorEnvelope,
} from './error';

export { pageInfoSchema, cursorPageSchema, type PageInfo, type CursorPage } from './pagination';

export {
  livenessResponseSchema,
  readinessResponseSchema,
  versionResponseSchema,
  type LivenessResponse,
  type ReadinessResponse,
  type VersionResponse,
} from './operational';

export {
  uuidv7,
  isUuidV7,
  uuidV7Timestamp,
  idempotencyKey,
  platformRandomBytes,
  type RandomBytes,
  type UuidV7Options,
} from './ids';

export {
  ageOn,
  birthDateProblem,
  documentNeedsExactDate,
  isComplete,
  normalisePhone,
  readDate,
  requiredState,
  type Age,
  type DateParts,
  type Precision,
  type RequiredState,
} from './registration';
