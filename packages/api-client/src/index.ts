/**
 * `@dthcms/api-client` — the one API surface for web and mobile.
 *
 * `src/schema.ts` is generated from `api/openapi.yaml` by `pnpm --filter @dthcms/api-client
 * run generate`, and CI fails if the committed file differs from what the spec produces.
 * Never edit it by hand.
 *
 * The runtime around it is hand-written and small, for the reasons in `client.ts`.
 */

export { createApiClient, unwrap, apiFetch, type ApiClient, type ApiClientOptions } from './client';

export { ApiError, NetworkError, REQUEST_ID_HEADER, apiErrorFromBody, toApiError } from './errors';

export { shouldRetryQuery, shouldRetryMutation, queryRetryDelay } from './retry';

export type { paths, components, operations } from './schema';

/** The response bodies, named. Saves every call site writing the lookup out longhand. */
export type LivenessResponse = import('./schema').components['schemas']['LivenessResponse'];
export type ReadinessResponse = import('./schema').components['schemas']['ReadinessResponse'];
export type VersionResponse = import('./schema').components['schemas']['VersionResponse'];
export type ApiErrorBody = import('./schema').components['schemas']['ErrorBody'];
export type PageInfo = import('./schema').components['schemas']['PageInfo'];
