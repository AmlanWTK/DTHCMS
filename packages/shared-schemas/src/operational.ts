import { z } from 'zod';

/**
 * The three operational endpoints. Unauthenticated, unversioned, and the only routes the
 * API actually serves at CP12.
 */

export const livenessResponseSchema = z.object({
  status: z.literal('ok'),
  service: z.string(),
  version: z.string(),
});

export const readinessResponseSchema = z.object({
  status: z.enum(['ok', 'unready']),
  service: z.string(),
  version: z.string(),
  /**
   * A status word per dependency — never the underlying error, which may carry a host, a
   * user name or a password. The detail goes to the log, where it is access-controlled.
   */
  checks: z.record(z.string(), z.string()).optional(),
});

export const versionResponseSchema = z.object({
  service: z.string(),
  version: z.string(),
  commit: z.string(),
  build_time: z.string(),
});

export type LivenessResponse = z.infer<typeof livenessResponseSchema>;
export type ReadinessResponse = z.infer<typeof readinessResponseSchema>;
export type VersionResponse = z.infer<typeof versionResponseSchema>;
