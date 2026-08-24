import { notFound } from 'next/navigation';

/**
 * A route that throws, so the error boundary can be proved rather than assumed.
 *
 * Acceptance criterion 3 asks that an unhandled error shows a friendly bilingual page
 * with a correlation ID. That cannot be verified without an unhandled error, and a
 * boundary nobody has ever seen fire is a boundary nobody knows works.
 *
 * It is off unless DTHCMS_ENABLE_ERROR_PROBE is set, or the application is running in
 * development. In production it is a 404, which is the same thing a route that does not
 * exist would give — no hint that there is something here to switch on.
 *
 * The flag is read at request time rather than baked in at build, so the end-to-end suite
 * can enable it against the same production build everything else is tested against.
 */
export default function ErrorProbePage() {
  const enabled =
    process.env.DTHCMS_ENABLE_ERROR_PROBE === '1' || process.env.NODE_ENV === 'development';

  if (!enabled) notFound();

  throw new Error('Deliberate failure from the error probe route.');
}
