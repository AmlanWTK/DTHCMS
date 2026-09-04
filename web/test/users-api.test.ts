import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';
import { isUuidV7 } from '@dthcms/shared-schemas';

import {
  changeStatus,
  endSessions,
  getUser,
  grantRole,
  inviteUser,
  listRoles,
  listUsers,
  reasonRequiredFor,
  resetSecondFactor,
  revokeRole,
  setPassword,
  transitionsFor,
  type AdminAccount,
  type Invitation,
  type UserStatus,
} from '@/features/users/api/users';

/**
 * What the administration console puts on the wire (CP21).
 *
 * These are the calls that suspend a colleague, hand out a permission, end somebody's
 * sessions and set their password. Three things travel with every one of them, and each
 * fails silently if it stops:
 *
 *   - the step-up token, which is the proof the person at the keyboard is the
 *     administrator whose session this is. Dropped, the server refuses — but a token
 *     carried in the wrong header, or the token from an earlier action, is the failure
 *     that looks like it worked;
 *   - the forgery guard, without which a page in another tab can make these calls;
 *   - a fresh idempotency key, without which a retried suspension is two audit entries
 *     and a granted role becomes a second grant.
 *
 * Which purpose a token was minted for is the calling screen's decision and is asserted
 * where that decision is made (test/invite-form.test.tsx). What is asserted here is that
 * the token the screen obtained is the one that reaches the server, on every write path —
 * because a write that quietly carries no token is a write that will be refused in the
 * clinic and nowhere else.
 *
 * The lifecycle assertions matter for a different reason: a console that offers a move
 * the server will reject teaches an administrator that the software is unreliable, and a
 * suspension recorded without a reason is an account nobody can explain later.
 */

const ACCOUNT: AdminAccount = {
  id: 'u1',
  employee_code: 'N006',
  name_en: 'Rafiq Hasan',
  name_bn: 'রফিক হাসান',
  phone: '+8801700000000',
  email: 'rafiq@example.org',
  status: 'active',
  status_reason: '',
  status_since: '2026-09-01T09:00:00Z',
  last_login_at: '2026-09-03T08:00:00Z',
  roles: ['NUTRITIONIST'],
  permissions: ['patient.read.demographics'],
  second_factor: { required: true, enrolled: true, pending: false, recovery_codes_left: 10 },
  created_at: '2026-08-01T09:00:00Z',
  sessions: [],
  grant_history: [],
};

const INVITATION: Invitation = {
  employee_code: 'N006',
  name_en: 'Rafiq Hasan',
  name_bn: 'রফিক হাসান',
  phone: '+8801700000000',
  email: 'rafiq@example.org',
  roles: ['NUTRITIONIST'],
  password: 'correct horse battery',
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

/** Every administrative write, answered the way the contract says it is answered. */
function adminServer() {
  return server({
    'POST /v1/admin/users': () => respond(ACCOUNT, 201),
    'POST /v1/admin/users/u1/status': () => respond({ ...ACCOUNT, status: 'suspended' }),
    'POST /v1/admin/users/u1/roles': () => respond(ACCOUNT),
    'POST /v1/admin/users/u1/roles/NUTRITIONIST/revoke': () => respond(ACCOUNT),
    'POST /v1/admin/users/u1/sessions/end': () => respond({ sessions_ended: 3 }),
    'POST /v1/admin/users/u1/password': () => new Response(null, { status: 204 }),
    'POST /v1/admin/users/u1/second-factor/reset': () => new Response(null, { status: 204 }),
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('the reads behind the console', () => {
  it('asks for everybody when no status is chosen', async () => {
    const calls = server({ 'GET /v1/admin/users': () => respond({ users: [ACCOUNT] }) });

    const users = await listUsers();

    expect(users).toHaveLength(1);
    expect(users[0]?.employee_code).toBe('N006');
    // No `?status=undefined`: the filter is absent, not empty, or the server would answer
    // with the list of people whose status is the literal string "undefined" — none.
    expect(new URL(calls[0]!.url).search).toBe('');
  });

  it('narrows the list to one status when the administrator picks one', async () => {
    const calls = server({ 'GET /v1/admin/users': () => respond({ users: [] }) });

    await listUsers('suspended');

    expect(new URL(calls[0]!.url).searchParams.get('status')).toBe('suspended');
  });

  it('fetches one account by id, with its sessions and its grant history', async () => {
    const calls = server({
      'GET /v1/admin/users/u1': () =>
        respond({ ...ACCOUNT, grant_history: [{ role: 'NUTRITIONIST' }] }),
    });

    const account = await getUser('u1');

    expect(new URL(calls[0]!.url).pathname).toBe('/v1/admin/users/u1');
    expect(account.grant_history).toHaveLength(1);
  });

  it('unwraps the role catalogue the permission preview is drawn from', async () => {
    server({
      'GET /v1/admin/roles': () =>
        respond({
          roles: [
            {
              code: 'NUTRITIONIST',
              name_en: 'Clinical nutritionist',
              name_bn: 'পুষ্টিবিদ',
              is_clinical: true,
              station: 'nutrition',
              permissions: ['observation.write.nutrition'],
            },
          ],
        }),
    });

    const roles = await listRoles();

    expect(roles.map((role) => role.code)).toEqual(['NUTRITIONIST']);
  });
});

const WRITES: { what: string; token: string; call: (token: string) => Promise<unknown> }[] = [
  {
    what: 'inviting a colleague',
    token: 'su-user-manage',
    call: (token) => inviteUser(INVITATION, token),
  },
  {
    what: 'suspending an account',
    token: 'su-user-manage',
    call: (token) => changeStatus('u1', 'suspended', 'left without notice', token),
  },
  {
    what: 'granting a role',
    token: 'su-user-manage',
    call: (token) => grantRole('u1', 'NUTRITIONIST', token),
  },
  {
    what: 'revoking a role',
    token: 'su-user-manage',
    call: (token) => revokeRole('u1', 'NUTRITIONIST', 'moved to another station', token),
  },
  {
    what: 'ending every session',
    token: 'su-credential-reset',
    call: (token) => endSessions('u1', 'phone lost', token),
  },
  {
    what: 'setting a password',
    token: 'su-credential-reset',
    call: (token) => setPassword('u1', 'correct horse battery', 'forgotten', token),
  },
  {
    what: 'resetting an authenticator',
    token: 'su-credential-reset',
    call: (token) => resetSecondFactor('u1', 'phone and codes both lost', token),
  },
];

describe('what every administrative write carries', () => {
  it.each(WRITES)('carries the step-up token when $what', async ({ token, call }) => {
    const calls = adminServer();

    await call(token);

    expect(calls[0]!.headers.get('X-Step-Up-Token')).toBe(token);
  });

  it.each(WRITES)('carries the forgery guard when $what', async ({ token, call }) => {
    const calls = adminServer();

    await call(token);

    expect(calls[0]!.headers.get('X-Requested-With')).toBe('DTHCMS');
  });

  it.each(WRITES)('carries a UUIDv7 idempotency key when $what', async ({ token, call }) => {
    const calls = adminServer();

    await call(token);

    // Not merely present: the server keys its stored response on a UUIDv7, and a key of
    // another shape is refused rather than replayed.
    expect(isUuidV7(calls[0]!.headers.get('Idempotency-Key') ?? '')).toBe(true);
  });

  it('mints a new key for each act, so a second grant is not read as a retried first', async () => {
    const calls = adminServer();

    await grantRole('u1', 'NUTRITIONIST', 'su-user-manage');
    await grantRole('u1', 'EXERCISE', 'su-user-manage');

    const [first, second] = calls.map((call) => call.headers.get('Idempotency-Key'));
    expect(first).not.toBe(second);
  });

  it('lets a spent or wrong-purpose token come back as the refusal it is', async () => {
    // The console shows the server's sentence. Swallowing this would leave an
    // administrator believing a suspension took effect when it did not.
    server({
      'POST /v1/admin/users/u1/status': () =>
        respond(
          {
            error: {
              code: 'STEP_UP_REQUIRED',
              kind: 'auth',
              message: 'Confirm it is you and try again.',
              message_bn: 'এটি আপনি কিনা নিশ্চিত করে আবার চেষ্টা করুন।',
            },
          },
          403,
        ),
    });

    const refusal = await changeStatus('u1', 'suspended', 'left', 'su-spent').catch(
      (error: unknown) => error,
    );

    expect(refusal).toBeInstanceOf(ApiError);
    expect((refusal as ApiError).status).toBe(403);
    expect((refusal as ApiError).code).toBe('STEP_UP_REQUIRED');
  });
});

describe('what each write sends and gets back', () => {
  it('posts the invitation as the administrator filled it in', async () => {
    const calls = adminServer();

    const account = await inviteUser(INVITATION, 'su-user-manage');

    expect(new URL(calls[0]!.url).pathname).toBe('/v1/admin/users');
    expect(await calls[0]!.json()).toEqual(INVITATION);
    expect(account.id).toBe('u1');
  });

  it('sends the reason with a status change that has one', async () => {
    const calls = adminServer();

    const account = await changeStatus('u1', 'suspended', 'left without notice', 'su-user-manage');

    expect(await calls[0]!.json()).toEqual({
      status: 'suspended',
      reason: 'left without notice',
    });
    expect(account.status).toBe('suspended');
  });

  it('omits the reason rather than sending an empty one', async () => {
    // The column is checked, not merely typed: an empty string would be refused, and a
    // reactivation legitimately has nothing to say.
    const calls = adminServer();

    await changeStatus('u1', 'active', '', 'su-user-manage');

    expect(await calls[0]!.json()).toEqual({ status: 'active' });
  });

  it('names the role in the body when granting one', async () => {
    const calls = adminServer();

    await grantRole('u1', 'NUTRITIONIST', 'su-user-manage');

    expect(new URL(calls[0]!.url).pathname).toBe('/v1/admin/users/u1/roles');
    expect(await calls[0]!.json()).toEqual({ role: 'NUTRITIONIST' });
  });

  it('names the role in the path when revoking one, and keeps the reason with it', async () => {
    const calls = adminServer();

    await revokeRole('u1', 'NUTRITIONIST', 'moved to another station', 'su-user-manage');

    expect(new URL(calls[0]!.url).pathname).toBe('/v1/admin/users/u1/roles/NUTRITIONIST/revoke');
    expect(await calls[0]!.json()).toEqual({ reason: 'moved to another station' });
  });

  it('reports how many sessions were actually ended', async () => {
    // The console tells the administrator the number. "Signed out everywhere" over an
    // account that had no open session is a different sentence from one that had three.
    adminServer();

    await expect(endSessions('u1', 'phone lost', 'su-credential-reset')).resolves.toBe(3);
  });

  it('sends the new password and its reason, and resolves on the empty answer', async () => {
    const calls = adminServer();

    await expect(
      setPassword('u1', 'correct horse battery', 'forgotten', 'su-credential-reset'),
    ).resolves.toBeUndefined();
    expect(await calls[0]!.json()).toEqual({
      password: 'correct horse battery',
      reason: 'forgotten',
    });
  });

  it('sends the reason for an authenticator reset, and resolves on the empty answer', async () => {
    const calls = adminServer();

    await expect(
      resetSecondFactor('u1', 'phone and codes both lost', 'su-credential-reset'),
    ).resolves.toBeUndefined();
    expect(await calls[0]!.json()).toEqual({ reason: 'phone and codes both lost' });
  });
});

describe('the moves the console is willing to offer', () => {
  const STATUSES: UserStatus[] = ['invited', 'active', 'suspended', 'deactivated'];

  it.each(STATUSES)('never offers %s a move to the status it is already in', (status) => {
    expect(transitionsFor(status)).not.toContain(status);
  });

  it('offers suspension from active alone, which is the only place the server allows it', () => {
    const offering = STATUSES.filter((status) => transitionsFor(status).includes('suspended'));
    expect(offering).toEqual(['active']);
  });

  it('leaves a deactivated account one way back and nothing else', () => {
    // Deactivation is for somebody who has left. Offering "suspend" from there would be a
    // move the server refuses, on an account nobody is using.
    expect(transitionsFor('deactivated')).toEqual(['active']);
  });

  it('offers a way back to active from everywhere an account can get stuck', () => {
    for (const status of STATUSES.filter((s) => s !== 'active')) {
      expect(transitionsFor(status)).toContain('active');
    }
  });

  it('demands a reason for a suspension and for no other move', () => {
    // The database CHECK insists, so a console that let this through would produce a
    // refusal the administrator cannot act on.
    const offered = [...new Set(STATUSES.flatMap((status) => transitionsFor(status)))];
    expect(offered.filter(reasonRequiredFor)).toEqual(['suspended']);
  });
});
