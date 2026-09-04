import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

/**
 * In-session role switching (CP41, [R-02]).
 *
 * "The same assistant enters BP, then switches to anthropometry entry, from the same
 * phone." The screen half of that is verified on the tablet; what is testable here is the
 * half that decides whether a write is attributed correctly — that the hat travels on every
 * request, that it is confirmed before the interface changes, and that the app never starts
 * a session with no hat at all.
 *
 * The last one is the subtle one. A station app that sent no `X-Active-Role` would be
 * authorised against the *union* of every role the person holds, which is exactly the
 * over-grant §4.4 exists to stop — and it would fail silently, because everything would
 * work.
 */

const keystore = new Map<string, string>();
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async (key: string, value: string) => {
    keystore.set(key, value);
  }),
  getItemAsync: vi.fn(async (key: string) => keystore.get(key) ?? null),
  deleteItemAsync: vi.fn(async (key: string) => {
    keystore.delete(key);
  }),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));
vi.mock('expo-localization', () => ({ getLocales: () => [{ languageCode: 'en' }] }));

const { forgetCredentials } = await import('../src/lib/credentials');
const { currentActiveRole } = await import('../src/lib/active-role');
const { activePermissions, activeStation, useSession } = await import('../src/stores/session');

const FACILITY = '11111111-1111-4111-8111-111111111111';

const twoHats = {
  id: 'u1',
  employee_code: 'A014',
  name_en: 'Shirin Akter',
  name_bn: 'শিরীন আক্তার',
  status: 'active' as const,
  facility_id: FACILITY,
  roles: ['ANTHROPOMETRY', 'CLINICAL_ASSISTANT'],
  permissions: ['observation.write.anthro', 'observation.write.vitals'],
  grants: [
    {
      role: 'ANTHROPOMETRY',
      permissions: ['observation.write.anthro'],
      station: 'STN_ANTHROPOMETRY',
    },
    {
      role: 'CLINICAL_ASSISTANT',
      permissions: ['observation.write.vitals'],
      station: 'STN_EXAMINATION',
    },
  ],
  second_factor: { required: false, enrolled: false, pending: false, recovery_codes_left: 0 },
};

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_1' },
  });
}

function server(routes: Record<string, (request: Request) => Response | Promise<Response>>) {
  const calls: Request[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      calls.push(request);
      const key = `${request.method} ${new URL(request.url).pathname}`;
      const handler = routes[key];
      if (!handler) throw new Error(`no route for ${key}`);
      return handler(request);
    }),
  );
  return calls;
}

const initial = useSession.getInitialState();

beforeEach(async () => {
  keystore.clear();
  await forgetCredentials();
  useSession.setState(initial, true);
  useSession.getState().clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function signIn() {
  server({
    'POST /v1/auth/login': () =>
      respond({
        access_token: 'access-1',
        expires_at: '2026-09-03T09:15:00Z',
        refresh_token: 'refresh-1',
        refresh_expires_at: '2026-09-17T09:00:00Z',
        user: twoHats,
      }),
  });
  await useSession.getState().signIn('A014', 'correct horse battery');
}

describe('the hat an operator is wearing', () => {
  it('is set the moment a session appears, never left empty', async () => {
    // The silent failure this prevents: no header means the server authorises against the
    // union of every role, and every screen works while every write is attributed to a hat
    // nobody chose.
    await signIn();

    expect(useSession.getState().activeRole).toBe('ANTHROPOMETRY');
    expect(currentActiveRole()).toBe('ANTHROPOMETRY');
  });

  it('is the first granted role and not the one somebody wore last', async () => {
    // A station tablet is shared. Restoring the previous operator's hat for the next
    // operator is how somebody records a blood pressure as an anthropometry officer.
    await signIn();
    expect(useSession.getState().activeRole).toBe(twoHats.roles[0]);
  });

  it('travels on every request', async () => {
    await signIn();
    const calls = server({
      'GET /v1/auth/me': () => respond(twoHats),
    });
    await useSession.getState().hydrate();

    const sent = calls.find((call) => call.url.endsWith('/v1/auth/me'));
    expect(sent?.headers.get('X-Active-Role')).toBe('ANTHROPOMETRY');
  });

  it('is forgotten on sign-out, so the next operator inherits nothing', async () => {
    await signIn();
    server({ 'POST /v1/auth/logout': () => respond({}) });

    await useSession.getState().signOut();

    expect(useSession.getState().activeRole).toBeNull();
    expect(currentActiveRole()).toBeNull();
  });
});

describe('switching', () => {
  it('confirms with the server before the interface changes', async () => {
    // Switching locally and letting the next write fail would show an operator a form they
    // are not allowed to submit — which is how somebody fills one in and loses the typing.
    await signIn();
    const calls = server({
      'POST /v1/auth/active-role': () =>
        respond({
          role: 'CLINICAL_ASSISTANT',
          grant: {
            role: 'CLINICAL_ASSISTANT',
            permissions: ['observation.write.vitals'],
            station: 'STN_EXAMINATION',
          },
        }),
    });

    await useSession.getState().switchRole('CLINICAL_ASSISTANT');

    expect(useSession.getState().activeRole).toBe('CLINICAL_ASSISTANT');
    expect(currentActiveRole()).toBe('CLINICAL_ASSISTANT');
    // The previous hat is sent so the audit sentence reads "switched from X to Y". The
    // server does not trust it — it is for the sentence, not the decision.
    expect(await calls[0]!.json()).toEqual({
      role: 'CLINICAL_ASSISTANT',
      from: 'ANTHROPOMETRY',
    });
  });

  it('leaves the hat alone when the server refuses', async () => {
    await signIn();
    server({
      'POST /v1/auth/active-role': () =>
        respond(
          {
            error: {
              code: 'FORBIDDEN',
              kind: 'authz',
              message: 'That role is not granted to this user.',
              message_bn: 'এই ভূমিকা এই ব্যবহারকারীকে দেওয়া হয়নি।',
              correlation_id: 'req_1',
            },
          },
          403,
        ),
    });

    await expect(useSession.getState().switchRole('PHYSICIAN')).rejects.toThrow();
    expect(useSession.getState().activeRole).toBe('ANTHROPOMETRY');
    expect(currentActiveRole()).toBe('ANTHROPOMETRY');
  });

  it('changes which forms the screen may offer', async () => {
    // Criterion 1's other half: the point of switching is that the screen shows one hat's
    // worth of forms. An operator wearing the anthropometry hat should not see the vitals
    // form, even though the same person will write vitals a minute later.
    await signIn();
    expect(activePermissions(useSession.getState())).toEqual(['observation.write.anthro']);
    expect(activeStation(useSession.getState())).toBe('STN_ANTHROPOMETRY');

    server({
      'POST /v1/auth/active-role': () =>
        respond({
          role: 'CLINICAL_ASSISTANT',
          grant: {
            role: 'CLINICAL_ASSISTANT',
            permissions: ['observation.write.vitals'],
            station: 'STN_EXAMINATION',
          },
        }),
    });
    await useSession.getState().switchRole('CLINICAL_ASSISTANT');

    expect(activePermissions(useSession.getState())).toEqual(['observation.write.vitals']);
    // And the station app now knows whose queue to show, without asking the operator —
    // asking is how somebody calls a patient to the wrong room (CP39).
    expect(activeStation(useSession.getState())).toBe('STN_EXAMINATION');
  });

  it('never offers the union of every hat', async () => {
    // The regression this guards: `activePermissions` falling back to `operator.permissions`
    // when a role is set. That fallback exists for the no-role case and must not leak into
    // the ordinary one.
    await signIn();
    const active = activePermissions(useSession.getState());
    expect(active).not.toContain('observation.write.vitals');
    expect(active.length).toBeLessThan(twoHats.permissions.length);
  });
});
