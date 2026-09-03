import {
  ApiError,
  NetworkError,
  apiErrorFromBody,
  guarded,
  type CurrentUser,
  type SessionResponse,
} from '@dthcms/api-client';
import { create } from 'zustand';

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

const signedOut = { status: 'anonymous', operator: null } as const;

async function keepIssued(issued: SessionResponse): Promise<void> {
  await keepCredentials(issued);
}

export const useSession = create<SessionState>((set, get) => ({
  status: 'unknown',
  operator: null,

  hydrate: async () => {
    if (!(await hasStoredRefreshToken())) {
      set(signedOut);
      return;
    }
    try {
      // The client attaches no token yet; the 401 triggers the refresh, which stores a
      // new pair, and the retry carries it. One call, and the whole recovery has happened.
      const current = await unwrap(api.GET('/v1/auth/me'));
      set({ status: 'authenticated', operator: operatorFromServer(current) });
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        await forgetCredentials();
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
    set({ status: 'authenticated', operator: operatorFromServer(data.user) });
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
    set({ status: 'authenticated', operator: operatorFromServer(issued.user) });
  },

  signOut: async () => {
    try {
      await unwrap(api.POST('/v1/auth/logout', { params: guarded }));
    } catch {
      // The server may already consider the session gone. Either way, so do we.
    }
    await forgetCredentials();
    set(signedOut);
  },

  clear: () => set(signedOut),
}));

// A refresh that fails means the session is over, whichever screen noticed first.
onSessionLost(() => useSession.getState().clear());
