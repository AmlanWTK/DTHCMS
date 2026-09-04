import { APP_VERSION } from '@/lib/build';
import { API_BASE_URL } from '@/lib/api';
import { getAccessToken } from '@/lib/credentials';
import { signRequest } from '@/lib/device';

/**
 * The station app's half of the realtime handshake (CP27).
 *
 * Separated from the provider that uses it, and not for tidiness: the provider is React
 * and cannot be tested without a device, while everything below is a decision — which
 * credential goes on the wire, and what a background transition should do — and decisions
 * are exactly what a test should hold. `mobile/vitest.config.mts` records the same rule
 * for the other React modules in lib.
 */

/** The gateway's URL, derived from the API's. */
export function realtimeUrl(base: string = API_BASE_URL): string {
  const secure = /^https:/i.test(base);
  const withoutScheme = base.replace(/^https?:\/\//i, '').replace(/\/+$/, '');
  return `${secure ? 'wss' : 'ws'}://${withoutScheme}/v1/realtime`;
}

/**
 * The headers the handshake carries: the same credential every other request from this
 * tablet carries.
 *
 * React Native's WebSocket takes headers where a browser's does not, so the station app
 * authenticates the way it always does — bearer token plus the CP18 device signature —
 * and the gateway checks it with the same middleware as any other request. Nothing goes
 * in the query string: a token in a URL is a token in a log.
 *
 * The signature covers a GET with no body, which is what a handshake is. It carries a
 * timestamp and a nonce, so it must be minted immediately before the connection and again
 * before every reconnect; a stale one is refused, correctly.
 */
export async function handshakeHeaders(
  url: string = realtimeUrl(),
  token: string | null = getAccessToken(),
): Promise<Record<string, string>> {
  const headers: Record<string, string> = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  const signed = await signRequest('GET', url.replace(/^ws/i, 'http'), null, APP_VERSION);
  Object.assign(headers, signed);
  return headers;
}

/** What an app-state change means for the connection. */
export type ConnectionAction = 'resume' | 'disconnect' | 'none';

/**
 * The background/foreground policy.
 *
 * Going to the background closes the socket. An OS that suspends the process will drop it
 * anyway, and a socket the app believes is open while the OS has quietly killed it is how
 * a station screen goes stale without saying so — which is the failure this whole
 * checkpoint exists to prevent.
 *
 * Coming back resumes at once rather than waiting out a backoff that may have grown to
 * half a minute while the tablet was in a drawer. The operator has just picked it up and
 * is about to act on what is on it.
 *
 * `inactive` is neither: on iOS it is the state during a notification banner, an incoming
 * call or the app switcher, and tearing the connection down for a banner would mean
 * reconnecting several times a minute.
 */
export function connectionAction(previous: string, next: string): ConnectionAction {
  if (next === previous) return 'none';
  if (next === 'active') return previous === 'background' ? 'resume' : 'none';
  if (next === 'background')
    return previous === 'active' || previous === 'inactive' ? 'disconnect' : 'none';
  return 'none';
}
