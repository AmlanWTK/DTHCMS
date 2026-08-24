import { z } from 'zod';

/**
 * The wire shapes of /healthz and /readyz, as httpx.healthResponse defines them.
 *
 * Written by hand exactly once. CP12 generates these from the OpenAPI document and this
 * file goes away — the point of it now is to establish that a feature owns the shape of
 * what it fetches, and that the shape is parsed rather than asserted.
 */

export const healthResponseSchema = z.object({
  status: z.string(),
  service: z.string(),
  version: z.string(),
  /** Present on /readyz: dependency name to a status word. Never an error message. */
  checks: z.record(z.string(), z.string()).optional(),
});

export type HealthResponse = z.infer<typeof healthResponseSchema>;
