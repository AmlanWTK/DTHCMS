import { REQUESTED_WITH_HEADER, REQUESTED_WITH_VALUE, REFRESH_PATH } from '@dthcms/api-client';
import type { SessionResponse } from '@dthcms/api-client';

import { APP_VERSION } from '@/lib/build';
import { signRequest } from '@/lib/device';
import { deleteSecureItem, getSecureItem, setSecureItem } from '@/lib/secure-storage';

/**
 * The station app's credentials, and where each one lives.
 *
 * Two tokens, two homes, on purpose:
 *
 *   - the **refresh token** goes in the device Keystore. It is the one credential that
 *     must outlive a restart — a nurse should not sign in again because the tablet
 *     rebooted — and CP11's acceptance criterion says nothing sensitive is stored outside
 *     secure storage;
 *   - the **access token** is held in this module's memory and nowhere else. It lives
 *     fifteen minutes, so persisting it buys nothing and would put a live credential on
 *     disk for no reason.
 *
 * This module has no dependency on the API client, because the client depends on it: the
 * refreshing fetch asks here for the token to attach and for the refresh to run. The one
 * request made from here — the refresh itself — uses raw fetch for that reason.
 */

let accessToken: string | null = null;

/** The token to attach to the next request, or null when the app holds none. */
export function getAccessToken(): string | null {
  return accessToken;
}

/**
 * Keep a freshly issued pair: the access token in memory, the refresh token in the
 * Keystore. Called after sign-in and after every refresh.
 */
export async function keepCredentials(issued: SessionResponse): Promise<void> {
  accessToken = issued.access_token;
  if (issued.refresh_token) {
    await setSecureItem('refreshToken', issued.refresh_token);
  }
}

/** Forget everything. Sign-out, and the end of a session the server no longer honours. */
export async function forgetCredentials(): Promise<void> {
  accessToken = null;
  try {
    await deleteSecureItem('refreshToken');
  } catch {
    // The Keystore refusing to delete is not a reason to keep acting signed in.
  }
}

/** Whether a restart could be recovered from — a refresh token is on the device. */
export async function hasStoredRefreshToken(): Promise<boolean> {
  try {
    return (await getSecureItem('refreshToken')) !== null;
  } catch {
    return false;
  }
}

export interface RefreshOptions {
  baseUrl: string;
  fetch?: typeof globalThis.fetch;
}

/**
 * Exchange the stored refresh token for a new pair.
 *
 * Resolves to whether it worked. On a refusal the stored token is discarded, because a
 * refused refresh token is either spent or expired and presenting it again would look like
 * a replay. On a network failure it is kept — the token may be perfectly good and the
 * tablet merely in the corridor without signal.
 */
export async function refreshCredentials(options: RefreshOptions): Promise<boolean> {
  const doFetch = options.fetch ?? ((input: Request) => globalThis.fetch(input));

  let stored: string | null;
  try {
    stored = await getSecureItem('refreshToken');
  } catch {
    return false;
  }
  if (!stored) return false;

  const url = `${options.baseUrl}${REFRESH_PATH}`;
  const body = JSON.stringify({ refresh_token: stored });
  let response: Response;
  try {
    response = await doFetch(
      new Request(url, {
        method: 'POST',
        // No cookies, ever. The body is the credential.
        credentials: 'omit',
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
          [REQUESTED_WITH_HEADER]: REQUESTED_WITH_VALUE,
          // A session opened from this device is refreshed from it (CP18). Empty when
          // the device is not enrolled.
          ...(await signRequest('POST', url, body, APP_VERSION)),
        },
        body,
      }),
    );
  } catch {
    return false;
  }

  if (!response.ok) {
    if (response.status === 401) await forgetCredentials();
    return false;
  }

  const issued = (await response.json()) as SessionResponse;
  await keepCredentials(issued);
  return true;
}
