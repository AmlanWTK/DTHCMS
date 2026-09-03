/**
 * Session plumbing shared by every client surface.
 *
 * Two things live here, both small and both easy to get subtly wrong in each application
 * separately:
 *
 *   1. the cross-site request forgery guard header, which the API requires on every request
 *      that changes state (api/openapi.yaml, "Authentication");
 *   2. a fetch that answers an expired access token by refreshing once and retrying, so a
 *      screen that has been open through a tea break carries on rather than bouncing the
 *      operator to the sign-in page every fifteen minutes.
 */

/** The forgery guard. Sent on every request; the server only checks it on unsafe ones. */
export const REQUESTED_WITH_HEADER = 'X-Requested-With';
export const REQUESTED_WITH_VALUE = 'DTHCMS';

/**
 * The guard as openapi-fetch `params`, for typed write calls.
 *
 * The contract declares the header `required`, so the generated types demand it on every
 * operation that changes state — which is the contract being honest rather than a
 * nuisance to type around. The client already sends the header by default; this satisfies
 * the type at the call site: `api.POST('/v1/auth/logout', { params: guarded })`.
 */
export const guarded = { header: { [REQUESTED_WITH_HEADER]: REQUESTED_WITH_VALUE } } as const;

export const REFRESH_PATH = '/v1/auth/refresh';
export const LOGIN_PATH = '/v1/auth/login';

export interface RefreshingFetchOptions {
  /** Where the API is. The default refresh call is made against this origin. */
  baseUrl: string;
  /** The fetch to wrap. Injectable for tests and for React Native. */
  fetch?: typeof globalThis.fetch;
  /**
   * Called once when a refresh fails — the refresh credential has expired or the session
   * was ended elsewhere. The application uses it to drop to the sign-in screen. It is not
   * called for a 401 from sign-in or refresh themselves, which are answers, not expiries.
   */
  onSessionLost?: () => void;
  /**
   * The bearer transport: attach the access token the client holds. Absent for the
   * browser, whose credential is a cookie it cannot see. Called for the retry as well,
   * so a token rotated by the refresh is what the retry carries.
   */
  authorize?: (request: Request) => Request | Promise<Request>;
  /**
   * The bearer transport: exchange the stored refresh token and put the new pair wherever
   * the client keeps them. Resolves to whether it worked. Absent for the browser, where
   * the default posts to the refresh endpoint and the cookies do the rest.
   */
  refresh?: () => Promise<boolean>;
}

/**
 * Wraps a fetch so that a 401 is answered by one refresh and one retry.
 *
 * Single-flight: several requests failing at once — the usual case, since a screen loads
 * its queries in parallel — share one refresh rather than each rotating the token and
 * tripping the reuse detector on the others. That detector treats a second use of a
 * refresh token as theft and revokes the whole family, so the naive version of this
 * function would sign the operator out every time a page had two queries.
 *
 * Cookies carry the credentials in the browser, so the refresh request needs nothing but
 * `credentials: 'include'`. A native client that holds a bearer token supplies its own
 * fetch that adds the header; the refresh logic is the same.
 */
export function createRefreshingFetch(options: RefreshingFetchOptions): typeof globalThis.fetch {
  const baseFetch = options.fetch ?? ((request: Request) => globalThis.fetch(request));
  const authorize = options.authorize ?? ((request: Request) => request);
  const refreshUrl = `${options.baseUrl}${REFRESH_PATH}`;

  let inFlight: Promise<boolean> | null = null;

  async function refreshByCookie(): Promise<boolean> {
    try {
      const response = await baseFetch(
        new Request(refreshUrl, {
          method: 'POST',
          credentials: 'include',
          headers: { Accept: 'application/json', [REQUESTED_WITH_HEADER]: REQUESTED_WITH_VALUE },
        }),
      );
      return response.ok;
    } catch {
      return false;
    }
  }

  async function refresh(): Promise<boolean> {
    if (!options.refresh) return refreshByCookie();
    try {
      return await options.refresh();
    } catch {
      return false;
    }
  }

  return async function refreshingFetch(input, init) {
    const request = new Request(input, init);
    // The body of a Request can be read once. The copy is taken before the first send so
    // that a retry has something to send.
    const retry = request.clone();

    const response = await baseFetch(await authorize(request));
    if (response.status !== 401 || isCredentialEndpoint(request.url)) {
      return response;
    }

    inFlight ??= refresh().finally(() => {
      inFlight = null;
    });
    const refreshed = await inFlight;

    if (!refreshed) {
      options.onSessionLost?.();
      return response;
    }
    return baseFetch(await authorize(retry));
  };
}

/** Attaches a bearer token when one is held; leaves the request alone when none is. */
export function bearerAuthorizer(getAccessToken: () => string | null) {
  return (request: Request): Request => {
    const token = getAccessToken();
    if (!token) return request;
    const headers = new Headers(request.headers);
    headers.set('Authorization', `Bearer ${token}`);
    return new Request(request, { headers });
  };
}

/**
 * A 401 from sign-in or refresh is the answer to the question asked, not an expired token
 * to be refreshed around — refreshing on a failed refresh is a loop.
 */
export function isCredentialEndpoint(url: string): boolean {
  let path: string;
  try {
    path = new URL(url, 'http://placeholder.invalid').pathname;
  } catch {
    return false;
  }
  return path.endsWith(LOGIN_PATH) || path.endsWith(REFRESH_PATH);
}
