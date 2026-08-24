/**
 * Crash capture, with PHI scrubbing — the seam, ahead of the vendor.
 *
 * CP11's scope says "crash reporting configured with PHI scrubbing". The vendor half
 * needs an account and a DSN nobody has yet, so what ships now is the half that must
 * exist BEFORE any vendor is wired: a single choke point every uncaught error passes
 * through, which scrubs before anything leaves the process. Wiring Sentry (or another
 * collector) later means changing `report` — one function — not auditing every screen.
 *
 * Scrubbing is deny-by-pattern here, unlike the backend's allow-by-key redaction,
 * because a crash message is free text: it may quote a component prop, a route param,
 * or user input verbatim. The patterns below remove what PHI looks like in transit —
 * long digit runs (phones, NIDs, registration numbers), email addresses, and anything
 * following a known-sensitive label — accepting false positives, because an
 * over-scrubbed stack trace is an inconvenience and an under-scrubbed one is a breach.
 */

const SENSITIVE_LABEL =
  /\b(name|phone|mobile|address|nid|dob|diagnosis|note|patient[_-]?id)\b\s*[:=]\s*\S+/gi;
const LONG_DIGITS = /\b\d{6,}\b/g;
const EMAIL = /\b[\w.+-]+@[\w-]+\.[\w.]+\b/g;

export function scrub(text: string): string {
  return text
    .replace(SENSITIVE_LABEL, (match) => `${match.split(/[:=]/)[0] ?? ''}:[scrubbed]`)
    .replace(EMAIL, '[scrubbed-email]')
    .replace(LONG_DIGITS, '[scrubbed-number]');
}

export interface CrashReport {
  message: string;
  stack: string;
  isFatal: boolean;
  occurredAt: string;
}

/** Replaced by the vendor integration when an account exists. See docs/mobile-shell.md. */
function report(crash: CrashReport): void {
  // eslint-disable-next-line no-console
  console.error('[dthcms] crash', crash);
}

export function toCrashReport(error: unknown, isFatal: boolean): CrashReport {
  const err = error instanceof Error ? error : new Error(String(error));
  return {
    message: scrub(err.message),
    stack: scrub(err.stack ?? ''),
    isFatal,
    occurredAt: new Date().toISOString(),
  };
}

interface ErrorUtilsLike {
  getGlobalHandler(): (error: unknown, isFatal?: boolean) => void;
  setGlobalHandler(handler: (error: unknown, isFatal?: boolean) => void): void;
}

/** Installs the choke point. Called once, from the root layout. */
export function installCrashHandler(): void {
  const errorUtils = (globalThis as { ErrorUtils?: ErrorUtilsLike }).ErrorUtils;
  if (!errorUtils) return;

  const previous = errorUtils.getGlobalHandler();
  errorUtils.setGlobalHandler((error, isFatal) => {
    report(toCrashReport(error, isFatal === true));
    // The previous handler shows the red box in development and performs the platform's
    // fatal-crash teardown in production. Swallowing it would turn crashes into hangs.
    previous(error, isFatal);
  });
}
