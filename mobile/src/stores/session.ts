import {
  ApiError,
  NetworkError,
  apiErrorFromBody,
  guarded,
  type CurrentUser,
  type SessionResponse,
} from '@dthcms/api-client';
import { create } from 'zustand';

import { setCurrentActiveRole } from '@/lib/active-role';
import { api, onSessionLost, unwrap } from '@/lib/api';
import { forgetCredentials, hasStoredRefreshToken, keepCredentials } from '@/lib/credentials';

/**
 * The session store — what the server last said about who is holding this tablet.
 *
 * Nothing here persists. The one thing that outlives a restart is the refresh token, and
 * it lives in the Keystore behind `lib/credentials`, never in this store or any other. On
 * start-up the store asks for a refresh with it; if that works the operator is signed in
 * without typing anything, and if it does not the sign-in screen is where they land.
 *
 * `status` is three-valued so a screen can tell "not yet known" from "nobody" and show a
 * splash rather than flashing the sign-in form at somebody who is, in fact, signed in.
 */

export type SessionStatus = 'unknown' | 'anonymous' | 'authenticated';

export interface OperatorSession {
  id: string;
  employeeCode: string;
  nameEN: string;
  nameBN: string;
  roleCodes: string[];
  /**
   * What each hat confers, and which station it works (CP41, [R-02]).
   *
   * The station app needs the per-role breakdown rather than the union, because the whole
   * point of switching is that the screen shows one hat's worth of forms. An operator
   * wearing the anthropometry hat should not see the vitals form, even though the same
   * person may write vitals a minute later.
   */
  grants: Record<string, { permissions: string[]; station: string }>;
  /** A courtesy for hiding controls, never a control. The server decides. */
  permissions: string[];
  secondFactor: { required: boolean; enrolled: boolean };
}

/** What `signIn` resolves to: a session, or a challenge the code must come back with. */
export type SignInResult = { kind: 'signed-in' } | { kind: 'second-factor'; challenge: string };

/** A second-factor proof: a six-digit code, or a recovery code. */
export type Proof = { code: string } | { recoveryCode: string };

interface SessionState {
  status: SessionStatus;
  operator: OperatorSession | null;
  /**
   * The hat being worn (CP41, [R-02]). Sent as `X-Active-Role` on every request, so it is
   * what the server decides against — not a display preference.
   *
   * Never null while signed in: the store picks the first granted role at sign-in, because
   * a station app with no role sends no header and gets the union of every hat, which is
   * exactly the over-grant §4.4 exists to stop.
   */
  activeRole: string | null;

  /** Recover a session from the stored refresh token, if there is one. Resolves either way. */
  hydrate: () => Promise<void>;
  /**
   * Exchange credentials for a session — or, for an enrolled account, for a challenge.
   * Throws the ApiError or NetworkError on failure.
   */
  signIn: (employeeCode: string, password: string) => Promise<SignInResult>;
  /** The second step: the challenge from `signIn` and a proof. Throws on refusal. */
  completeSecondFactor: (challenge: string, proof: Proof) => Promise<void>;
  /** End this session and forget the credentials. Never throws. */
  signOut: () => Promise<void>;
  /**
   * Wear a different hat. Confirms with the server, which refuses a role the person does
   * not hold and records the switch — no re-authentication, which is the requirement.
   * Throws the ApiError on refusal so the switcher can say why.
   */
  switchRole: (role: string) => Promise<void>;
  /** Forget the session without telling the server — it has already told us. */
  clear: () => void;
}

export function operatorFromServer(current: CurrentUser): OperatorSession {
  return {
    id: current.id,
    employeeCode: current.employee_code,
    nameEN: current.name_en,
    nameBN: current.name_bn,
    roleCodes: [...current.roles],
    grants: Object.fromEntries(
      current.grants.map((grant) => [
        grant.role,
        { permissions: [...grant.permissions], station: grant.station ?? '' },
      ]),
    ),
    permissions: [...current.permissions],
    secondFactor: {
      required: current.second_factor.required,
      enrolled: current.second_factor.enrolled,
    },
  };
}

/** The name in the interface's language, falling back to the other. */
export function displayName(operator: OperatorSession, language: 'en' | 'bn'): string {
  const preferred = language === 'bn' ? operator.nameBN : operator.nameEN;
  return preferred || operator.nameEN || operator.nameBN || operator.employeeCode;
}

const signedOut = { status: 'anonymous', operator: null, activeRole: null } as const;

/**
 * Which hat to wear when a session appears.
 *
 * The first granted role, in the order the server listed them — which is the order they
 * were granted, so the role somebody was hired for comes first. Deliberately not "the one
 * they wore last": a tablet is shared, and restoring the previous operator's hat for the
 * next operator is how somebody writes a blood pressure as an anthropometry officer.
 */
function firstRole(operator: OperatorSession): string | null {
  return operator.roleCodes[0] ?? null;
}

/** Sets the hat in one place: the store's state and the header the client sends. */
function wear(role: string | null): { activeRole: string | null } {
  setCurrentActiveRole(role);
  return { activeRole: role };
}

async function keepIssued(issued: SessionResponse): Promise<void> {
  await keepCredentials(issued);
}

export const useSession = create<SessionState>((set, get) => ({
  status: 'unknown',
  operator: null,
  activeRole: null,

  hydrate: async () => {
    if (!(await hasStoredRefreshToken())) {
      setCurrentActiveRole(null);
      set(signedOut);
      return;
    }
    try {
      // The client attaches no token yet; the 401 triggers the refresh, which stores a
      // new pair, and the retry carries it. One call, and the whole recovery has happened.
      const current = await unwrap(api.GET('/v1/auth/me'));
      const operator = operatorFromServer(current);
      set({ status: 'authenticated', operator, ...wear(firstRole(operator)) });
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        await forgetCredentials();
        setCurrentActiveRole(null);
        set(signedOut);
        return;
      }
      // The server is unreachable. A tablet in the corridor is not signed out; it is
      // offline. The stored token stays, and so does whatever the store already knew.
      if (get().status === 'unknown') set(signedOut);
    }
  },

  signIn: async (employeeCode, password) => {
    // Not through unwrap(): a 202 is a success that unwrap() would not know how to hand
    // back. The one thing it does that matters — a request that never arrived becoming a
    // NetworkError — is done by hand.
    const { data, error, response } = await api
      .POST('/v1/auth/login', {
        params: guarded,
        body: { employee_code: employeeCode, password, transport: 'bearer' },
      })
      .catch((cause: unknown) => {
        throw new NetworkError(cause);
      });
    if (!response.ok || error !== undefined) {
      throw apiErrorFromBody(error, response);
    }
    if (response.status === 202 && data && 'challenge' in data) {
      return { kind: 'second-factor', challenge: data.challenge };
    }
    if (!data || !('user' in data)) {
      throw apiErrorFromBody(undefined, response);
    }
    await keepIssued(data);
    const operator = operatorFromServer(data.user);
    set({ status: 'authenticated', operator, ...wear(firstRole(operator)) });
    return { kind: 'signed-in' };
  },

  completeSecondFactor: async (challenge, proof) => {
    const issued = await unwrap(
      api.POST('/v1/auth/login/second-factor', {
        params: guarded,
        body: {
          challenge,
          transport: 'bearer',
          ...('code' in proof ? { code: proof.code } : { recovery_code: proof.recoveryCode }),
        },
      }),
    );
    await keepIssued(issued);
    const operator = operatorFromServer(issued.user);
    set({ status: 'authenticated', operator, ...wear(firstRole(operator)) });
  },

  switchRole: async (role) => {
    const previous = get().activeRole;
    // Confirmed with the server before the interface changes. The alternative — switch
    // locally and let the next write fail — would show an operator a form they are not
    // allowed to submit, which is how somebody fills one in and loses the typing.
    const confirmed = await unwrap(
      api.POST('/v1/auth/active-role', {
        params: guarded,
        body: { role, ...(previous ? { from: previous } : {}) },
      }),
    );
    const operator = get().operator;
    if (operator) {
      set({
        operator: {
          ...operator,
          grants: {
            ...operator.grants,
            [confirmed.role]: {
              permissions: [...confirmed.grant.permissions],
              station: confirmed.grant.station ?? '',
            },
          },
        },
      });
    }
    set(wear(confirmed.role));
  },

  signOut: async () => {
    try {
      await unwrap(api.POST('/v1/auth/logout', { params: guarded }));
    } catch {
      // The server may already consider the session gone. Either way, so do we.
    }
    await forgetCredentials();
    setCurrentActiveRole(null);
    set(signedOut);
  },

  clear: () => {
    setCurrentActiveRole(null);
    set(signedOut);
  },
}));

/** The permissions of the hat being worn — not the union. Used to scope the station app. */
export function activePermissions(state: {
  operator: OperatorSession | null;
  activeRole: string | null;
}): string[] {
  if (!state.operator) return [];
  if (!state.activeRole) return state.operator.permissions;
  return state.operator.grants[state.activeRole]?.permissions ?? [];
}

/** Which station the hat being worn works. Empty for the roles that work none. */
export function activeStation(state: {
  operator: OperatorSession | null;
  activeRole: string | null;
}): string {
  if (!state.operator || !state.activeRole) return '';
  return state.operator.grants[state.activeRole]?.station ?? '';
}

// A refresh that fails means the session is over, whichever screen noticed first.
onSessionLost(() => useSession.getState().clear());
