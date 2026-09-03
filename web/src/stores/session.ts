import { create } from 'zustand';

import {
  ApiError,
  NetworkError,
  apiErrorFromBody,
  guarded,
  type CurrentUser,
} from '@dthcms/api-client';

import { setCurrentActiveRole } from '@/lib/active-role';
import { api, onSessionLost, unwrap } from '@/lib/api';

/**
 * The session store.
 *
 * What it holds is what the server last said about the current session, and nothing more.
 * Nothing here is persisted and no credential ever passes through it: the access token
 * lives in an httpOnly cookie the browser attaches and this code cannot read (ADR-0010).
 * On reload the store starts empty and asks `/v1/auth/me` who it is talking to — one
 * request, and the thing that makes revocation from another device actually take effect.
 *
 * `status` is three-valued on purpose. `unknown` is the state before that first answer,
 * and it is distinct from `anonymous` so that a screen can show a skeleton rather than
 * flashing the sign-in page at somebody who is, in fact, signed in.
 */

export type SessionStatus = 'unknown' | 'anonymous' | 'authenticated';

export interface SecondFactorState {
  /** One of the person's roles mandates an authenticator (D-45). */
  required: boolean;
  enrolled: boolean;
  pending: boolean;
  recoveryCodesLeft: number;
}

export interface SessionUser {
  id: string;
  employeeCode: string;
  nameEN: string;
  nameBN: string;
  /** The server's role codes, in the order the server listed them. */
  roles: string[];
  /** Which role confers which permissions, from the server. */
  grants: Record<string, string[]>;
  /** The union across every role. A courtesy for hiding controls, never a control. */
  permissions: string[];
  secondFactor: SecondFactorState;
}

/** What `signIn` resolves to: a session, or a challenge the code must come back with. */
export type SignInResult =
  { kind: 'signed-in' } | { kind: 'second-factor'; challenge: string; expiresAt: string };

/** A second-factor proof: a six-digit code, or a recovery code. */
export type Proof = { code: string } | { recoveryCode: string };

interface SessionState {
  status: SessionStatus;
  user: SessionUser | null;
  /** The role the interface is acting as. One of `user.roles`; sent as X-Active-Role. */
  activeRole: string | null;

  /** Ask the server who this is. Resolves either way; the outcome is in `status`. */
  hydrate: () => Promise<void>;
  /**
   * Exchange credentials for a session — or, for an enrolled account, for a challenge.
   * Throws the ApiError or NetworkError on failure.
   */
  signIn: (employeeCode: string, password: string) => Promise<SignInResult>;
  /** The second step: the challenge from `signIn` and a proof. Throws on refusal. */
  completeSecondFactor: (challenge: string, proof: Proof) => Promise<void>;
  /** Re-read the account from the server, e.g. after enrolling an authenticator. */
  refresh: () => Promise<void>;
  /** End this session. Never throws: the local state is cleared whatever the server said. */
  signOut: () => Promise<void>;
  /** End every session of this user, on every device. */
  signOutEverywhere: () => Promise<void>;
  setActiveRole: (role: string) => void;

  /** Forget the session without telling the server — it has already told us. */
  clear: () => void;
}

export function userFromServer(current: CurrentUser): SessionUser {
  return {
    id: current.id,
    employeeCode: current.employee_code,
    nameEN: current.name_en,
    nameBN: current.name_bn,
    roles: [...current.roles],
    grants: Object.fromEntries(current.grants.map((g) => [g.role, [...g.permissions]])),
    permissions: [...current.permissions],
    secondFactor: {
      required: current.second_factor.required,
      enrolled: current.second_factor.enrolled,
      pending: current.second_factor.pending,
      recoveryCodesLeft: current.second_factor.recovery_codes_left,
    },
  };
}

/** Whether the person must set up an authenticator before doing anything else. */
export function needsEnrolment(user: SessionUser | null): boolean {
  return user !== null && user.secondFactor.required && !user.secondFactor.enrolled;
}

/** The name in the interface's current language, falling back to the other. */
export function displayName(user: SessionUser, locale: string): string {
  const preferred = locale === 'bn' ? user.nameBN : user.nameEN;
  return preferred || user.nameEN || user.nameBN || user.employeeCode;
}

/**
 * The permissions the interface should act on: the active role's, or the union when the
 * server did not say which role confers what. The same narrowing the server applies to
 * a request carrying X-Active-Role, so the two never disagree about a button.
 */
export function activePermissions(
  user: SessionUser | null,
  activeRole: string | null,
): Set<string> {
  if (!user) return new Set();
  if (activeRole && user.grants[activeRole]) return new Set(user.grants[activeRole]);
  return new Set(user.permissions);
}

/** The role to act as when none was chosen: the first the server listed. */
function defaultRole(user: SessionUser): string | null {
  return user.roles[0] ?? null;
}

/**
 * The state for a person the server has just described. The hat they were wearing is
 * kept when they still hold it — a re-hydration or a refresh must not quietly put an
 * administrator back into their first role mid-task.
 */
function signedIn(user: SessionUser, previous: string | null = null): Partial<SessionState> {
  const activeRole = previous && user.roles.includes(previous) ? previous : defaultRole(user);
  setCurrentActiveRole(activeRole);
  return { status: 'authenticated', user, activeRole };
}

function signedOutState(): Partial<SessionState> {
  setCurrentActiveRole(null);
  return { status: 'anonymous', user: null, activeRole: null };
}

export const useSessionStore = create<SessionState>((set, get) => ({
  status: 'unknown',
  user: null,
  activeRole: null,

  hydrate: async () => {
    try {
      const current = await unwrap(api.GET('/v1/auth/me'));
      set(signedIn(userFromServer(current), get().activeRole));
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        set(signedOutState());
        return;
      }
      // Anything else — the server is down, the network is gone — is not "you are signed
      // out". The status stays as it was, so a screen that was authenticated stays
      // authenticated and shows the offline banner rather than the sign-in page.
      if (get().status === 'unknown') set(signedOutState());
    }
  },

  signIn: async (employeeCode, password) => {
    // Not through unwrap(): a 202 is a success that unwrap() would not know how to hand
    // back. The one thing unwrap() does that matters here — turning a request that never
    // arrived into a NetworkError — is done by hand.
    const { data, error, response } = await api
      .POST('/v1/auth/login', {
        params: guarded,
        // Cookies, explicitly. The browser never holds the token (ADR-0010).
        body: { employee_code: employeeCode, password, transport: 'cookie' },
      })
      .catch((cause: unknown) => {
        throw new NetworkError(cause);
      });
    if (!response.ok || error !== undefined) {
      throw apiErrorFromBody(error, response);
    }
    // 202: the password was right and a code is owed. No session yet.
    if (response.status === 202 && data && 'challenge' in data) {
      return { kind: 'second-factor', challenge: data.challenge, expiresAt: data.expires_at };
    }
    if (!data || !('user' in data)) {
      throw apiErrorFromBody(undefined, response);
    }
    // The response body carries the access token for the station app. The browser has no
    // use for it and does not keep it: the cookie the server set alongside is the session.
    set(signedIn(userFromServer(data.user)));
    return { kind: 'signed-in' };
  },

  completeSecondFactor: async (challenge, proof) => {
    const response = await unwrap(
      api.POST('/v1/auth/login/second-factor', {
        params: guarded,
        body: {
          challenge,
          transport: 'cookie',
          ...('code' in proof ? { code: proof.code } : { recovery_code: proof.recoveryCode }),
        },
      }),
    );
    set(signedIn(userFromServer(response.user)));
  },

  refresh: async () => {
    const current = await unwrap(api.GET('/v1/auth/me'));
    set(signedIn(userFromServer(current), get().activeRole));
  },

  signOut: async () => {
    try {
      await unwrap(api.POST('/v1/auth/logout', { params: guarded }));
    } catch {
      // The server may already consider the session gone. Either way, so do we.
    }
    set(signedOutState());
  },

  signOutEverywhere: async () => {
    try {
      await unwrap(api.POST('/v1/auth/logout-all', { params: guarded }));
    } catch {
      // As above.
    }
    set(signedOutState());
  },

  setActiveRole: (activeRole) => {
    const user = get().user;
    if (user && user.roles.includes(activeRole)) {
      setCurrentActiveRole(activeRole);
      set({ activeRole });
    }
  },

  clear: () => set(signedOutState()),
}));

// A refresh that fails means the session is over, whichever screen noticed first.
onSessionLost(() => useSessionStore.getState().clear());
