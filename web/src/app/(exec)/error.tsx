'use client';

import { RouteError } from '@/components/RouteError';

/*
 * The boundary for the exec area. Everything it does is in RouteError; this file
 * exists because Next attaches boundaries by file position.
 */
export default function GroupError(props: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return <RouteError {...props} />;
}
