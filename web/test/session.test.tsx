import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import { API_BASE_URL } from '@/lib/api';
import { can } from '@/lib/permissions';
import {
  activePermissions,
  displayName,
  useSessionStore,
  userFromServer,
  type SessionUser,
} from '@/stores/session';
import type { CurrentUser } from '@dthcms/api-client';

/**
 * The session, end to end on the client: what the store does with what the server says,
 * what the gate shows for each answer, and what the form does with a refusal.
 *
 * The server is a scripted fetch. Every request the store makes is asserted on — its
 * path, its credentials mode, and the forgery guard — because those are the things that
 * make the cookie transport work at all, and none of them are visible on screen.
 */

const router = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn(), refresh: vi.fn() }));
const location = vi.hoisted(() => ({ pathname: '/dashboard', search: '' }));

vi.mock('next/navigation', () => ({
  useRouter: () => router,
  usePathname: () => location.pathname,
  useSearchParams: () => new URLSearchParams(location.search),
}));
vi.mock('@/lib/i18n/actions', () => ({ setLocale: vi.fn() }));

const { SessionGate } = await import('@/components/SessionGate');
const { LoginForm, safeNext } = await import('@/features/auth');

const currentUser: CurrentUser = {
  id: '0190a8f2-0000-7000-8000-00000000000a',
  employee_code: 'E001',
  name_en: 'Dr Test Physician',
  name_bn: 'ডা. পরীক্ষা চিকিৎসক',
  status: 'active',
  facility_id: '11111111-1111-4111-8111-111111111111',
  roles: ['PHYSICIAN'],
  permissions: ['patient.read.demographics'],
  grants: [{ role: 'PHYSICIAN', permissions: ['patient.read.demographics'] }],
  second_factor: { required: true, enrolled: true, pending: false, recovery_codes_left: 10 },
};

const unauthenticatedBody = {
  error: {
    code: 'UNAUTHENTICATED',
    kind: 'auth',
    message: 'Please sign in again.',
    message_bn: 'অনুগ্রহ করে আবার সাইন ইন করুন।',
    correlation_id: 'req_1',
  },
};

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_1' },
  });
}

/** Routes requests by path. Anything unlisted is a test bug, and says so. */
function server(routes: Record<string, (request: Request) => Response | Promise<Response>>) {
  const calls: Request[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    calls.push(request);
    const key = `${request.method} ${new URL(request.url).pathname}`;
    const handler = routes[key];
    if (!handler) throw new Error(`no route for ${key}`);
    return handler(request);
  });
  vi.stubGlobal('fetch', fetchMock);
  return calls;
}

const initial = useSessionStore.getInitialState();

beforeEach(() => {
  useSessionStore.setState(initial, true);
  router.replace.mockClear();
  location.pathname = '/dashboard';
  location.search = '';
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('the store', () => {
  it('starts not knowing, and asks the server', async () => {
    expect(useSessionStore.getState().status).toBe('unknown');
    const calls = server({ 'GET /v1/auth/me': () => respond(currentUser) });

    await useSessionStore.getState().hydrate();

    const state = useSessionStore.getState();
    expect(state.status).toBe('authenticated');
    expect(state.user?.employeeCode).toBe('E001');
    expect(state.user?.roles).toEqual(['PHYSICIAN']);
    expect(state.activeRole).toBe('PHYSICIAN');

    // The cookie is the credential. The request must carry it and nothing else.
    expect(calls[0]?.credentials).toBe('include');
    expect(calls[0]?.headers.get('Authorization')).toBeNull();
    expect(calls[0]?.url).toBe(`${API_BASE_URL}/v1/auth/me`);
  });

  it('is anonymous when the server says so, after one refresh attempt', async () => {
    const calls = server({
      'GET /v1/auth/me': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticatedBody, 401),
    });

    await useSessionStore.getState().hydrate();

    expect(useSessionStore.getState().status).toBe('anonymous');
    expect(calls.map((c) => new URL(c.url).pathname)).toEqual(['/v1/auth/me', '/v1/auth/refresh']);
    expect(calls[1]?.headers.get('X-Requested-With')).toBe('DTHCMS');
  });

  it('recovers an expired access token through the refresh cookie', async () => {
    let meCalls = 0;
    server({
      'GET /v1/auth/me': () =>
        meCalls++ === 0 ? respond(unauthenticatedBody, 401) : respond(currentUser),
      'POST /v1/auth/refresh': () =>
        respond({ access_token: 'x', expires_at: '', user: currentUser }),
    });

    await useSessionStore.getState().hydrate();

    expect(useSessionStore.getState().status).toBe('authenticated');
    expect(meCalls).toBe(2);
  });

  it('does not sign somebody out because the server is unreachable', async () => {
    useSessionStore.setState({
      status: 'authenticated',
      user: userFromServer(currentUser),
      activeRole: 'PHYSICIAN',
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch');
      }),
    );

    await useSessionStore.getState().hydrate();

    expect(useSessionStore.getState().status).toBe('authenticated');
  });

  it('signs in with the guard header and keeps nothing but what the server said', async () => {
    const calls = server({
      'POST /v1/auth/login': () =>
        respond({
          access_token: 'never-kept',
          expires_at: '2026-09-03T09:15:00Z',
          user: currentUser,
        }),
    });

    await useSessionStore.getState().signIn('E001', 'correct horse battery');

    const state = useSessionStore.getState();
    expect(state.status).toBe('authenticated');
    expect(JSON.stringify(state)).not.toContain('never-kept');

    const request = calls[0]!;
    expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(request.credentials).toBe('include');
    expect(await request.json()).toEqual({
      employee_code: 'E001',
      password: 'correct horse battery',
      transport: 'cookie',
    });
  });

  it('throws the refusal so the form can show it', async () => {
    server({ 'POST /v1/auth/login': () => respond(unauthenticatedBody, 401) });

    await expect(useSessionStore.getState().signIn('E001', 'wrong')).rejects.toMatchObject({
      status: 401,
      messageBN: 'অনুগ্রহ করে আবার সাইন ইন করুন।',
    });
    expect(useSessionStore.getState().status).toBe('unknown');
  });

  it('signs out locally whatever the server says', async () => {
    useSessionStore.setState({
      status: 'authenticated',
      user: userFromServer(currentUser),
      activeRole: 'PHYSICIAN',
    });
    const calls = server({ 'POST /v1/auth/logout': () => new Response(null, { status: 500 }) });

    await useSessionStore.getState().signOut();

    expect(useSessionStore.getState().status).toBe('anonymous');
    expect(useSessionStore.getState().user).toBeNull();
    expect(calls[0]?.headers.get('X-Requested-With')).toBe('DTHCMS');
  });

  it('forgets the session when a refresh fails mid-session', async () => {
    // The screen was open; the token expired; the refresh cookie had expired too.
    useSessionStore.setState({
      status: 'authenticated',
      user: userFromServer(currentUser),
      activeRole: 'PHYSICIAN',
    });
    server({
      'GET /v1/auth/me': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticatedBody, 401),
    });

    await useSessionStore.getState().hydrate();

    expect(useSessionStore.getState().status).toBe('anonymous');
  });

  it('only switches to a role the account holds', () => {
    useSessionStore.setState({
      status: 'authenticated',
      user: userFromServer(currentUser),
      activeRole: 'PHYSICIAN',
    });
    useSessionStore.getState().setActiveRole('admin');
    expect(useSessionStore.getState().activeRole).toBe('PHYSICIAN');
  });
});

describe('permissions from the server', () => {
  it('answers an interface action from the server permissions behind it', () => {
    expect(can(new Set(['prescription.dispense']), 'pharmacy.view')).toBe(true);
    expect(can(['device.revoke'], 'admin.devices.manage')).toBe(true);
    expect(can(['device.revoke'], 'clinical.prescribe')).toBe(false);
    expect(can([], 'account.view')).toBe(true);
    expect(can([], 'admin.view')).toBe(false);
  });

  it('narrows to the active role, and falls back to the union without grants', () => {
    const user = userFromServer({
      ...currentUser,
      roles: ['PHYSICIAN', 'PHARMACIST'],
      permissions: ['prescription.sign', 'prescription.dispense'],
      grants: [
        { role: 'PHYSICIAN', permissions: ['prescription.sign'] },
        { role: 'PHARMACIST', permissions: ['prescription.dispense'] },
      ],
    });
    expect([...activePermissions(user, 'PHARMACIST')]).toEqual(['prescription.dispense']);
    expect([...activePermissions(user, 'PHYSICIAN')]).toEqual(['prescription.sign']);
    expect([...activePermissions(user, null)].sort()).toEqual([
      'prescription.dispense',
      'prescription.sign',
    ]);
    expect(activePermissions(null, 'PHYSICIAN').size).toBe(0);
  });

  it('sends the active role with every request', async () => {
    const calls = server({
      'GET /v1/auth/me': () =>
        respond({ ...currentUser, roles: ['PHYSICIAN', 'ADMIN'], grants: [] }),
      'POST /v1/auth/logout': () => new Response(null, { status: 204 }),
    });
    await useSessionStore.getState().hydrate();
    useSessionStore.getState().setActiveRole('ADMIN');
    await useSessionStore.getState().signOut();
    const logout = calls.find((c) => new URL(c.url).pathname === '/v1/auth/logout');
    expect(logout?.headers.get('X-Active-Role')).toBe('ADMIN');
    // Signed out: no hat.
    expect(useSessionStore.getState().activeRole).toBeNull();
  });
});

describe('the gate', () => {
  it('shows a skeleton, not the sign-in page, until the server has answered', () => {
    server({ 'GET /v1/auth/me': () => new Promise(() => {}) });
    renderWithProviders(
      <SessionGate>
        <p>secret</p>
      </SessionGate>,
    );

    expect(screen.queryByText('secret')).not.toBeInTheDocument();
    expect(screen.getByText('Checking your session…')).toBeInTheDocument();
    expect(router.replace).not.toHaveBeenCalled();
  });

  it('renders the screen once a session is confirmed', async () => {
    server({ 'GET /v1/auth/me': () => respond(currentUser) });
    renderWithProviders(
      <SessionGate>
        <p>secret</p>
      </SessionGate>,
    );

    await screen.findByText('secret');
    expect(router.replace).not.toHaveBeenCalled();
  });

  it('sends an anonymous visitor to sign in, remembering where they were going', async () => {
    location.pathname = '/patients/abc';
    server({
      'GET /v1/auth/me': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticatedBody, 401),
    });
    renderWithProviders(
      <SessionGate>
        <p>secret</p>
      </SessionGate>,
    );

    await waitFor(() =>
      expect(router.replace).toHaveBeenCalledWith('/login?next=%2Fpatients%2Fabc'),
    );
    expect(screen.queryByText('secret')).not.toBeInTheDocument();
  });
});

describe('the sign-in form', () => {
  it('signs in and goes where the person was going', async () => {
    location.search = '?next=%2Fpatients%2Fabc';
    const user = userEvent.setup();
    server({
      'GET /v1/auth/me': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/login': () =>
        respond({ access_token: 'x', expires_at: '', user: currentUser }),
    });
    renderWithProviders(<LoginForm />);

    await user.type(screen.getByLabelText(/^Employee code/), 'e001');
    await user.type(screen.getByLabelText(/^Password/), 'correct horse battery');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    await waitFor(() => expect(router.replace).toHaveBeenCalledWith('/patients/abc'));
    expect(useSessionStore.getState().status).toBe('authenticated');
  });

  it('shows the server’s one refusal message and clears the password', async () => {
    const user = userEvent.setup();
    server({
      'GET /v1/auth/me': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/login': () => respond(unauthenticatedBody, 401),
    });
    renderWithProviders(<LoginForm />);

    await user.type(screen.getByLabelText(/^Employee code/), 'E001');
    await user.type(screen.getByLabelText(/^Password/), 'wrong');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(await screen.findByText('Please sign in again.')).toBeInTheDocument();
    expect(screen.getByLabelText(/^Password/)).toHaveValue('');
    expect(screen.getByLabelText(/^Employee code/)).toHaveValue('E001');
    expect(router.replace).not.toHaveBeenCalled();
  });

  it('says the refusal in Bangla when the interface is in Bangla', async () => {
    const user = userEvent.setup();
    server({
      'GET /v1/auth/me': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/login': () => respond(unauthenticatedBody, 401),
    });
    renderWithProviders(<LoginForm />, { locale: 'bn' });

    await user.type(screen.getByLabelText(/^কর্মী কোড/), 'E001');
    await user.type(screen.getByLabelText(/^পাসওয়ার্ড/), 'wrong');
    await user.click(screen.getByRole('button', { name: 'সাইন ইন' }));

    expect(await screen.findByText('অনুগ্রহ করে আবার সাইন ইন করুন।')).toBeInTheDocument();
  });

  it('distinguishes an unreachable server from a refusal', async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch');
      }),
    );
    renderWithProviders(<LoginForm />);

    await user.type(screen.getByLabelText(/^Employee code/), 'E001');
    await user.type(screen.getByLabelText(/^Password/), 'anything');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(await screen.findByText('The clinic server cannot be reached')).toBeInTheDocument();
  });

  it('will not submit an empty form', () => {
    server({
      'GET /v1/auth/me': () => respond(unauthenticatedBody, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticatedBody, 401),
    });
    renderWithProviders(<LoginForm />);
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeDisabled();
  });

  it('sends somebody already signed in straight on', async () => {
    server({ 'GET /v1/auth/me': () => respond(currentUser) });
    renderWithProviders(<LoginForm />);
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith('/dashboard'));
  });
});

describe('safeNext', () => {
  it('accepts only a path on this site', () => {
    expect(safeNext('/patients/abc')).toBe('/patients/abc');
    expect(safeNext('/patients?x=1')).toBe('/patients?x=1');
    expect(safeNext(null)).toBe('/dashboard');
    expect(safeNext('')).toBe('/dashboard');
    expect(safeNext('https://evil.example')).toBe('/dashboard');
    expect(safeNext('//evil.example')).toBe('/dashboard');
    expect(safeNext('/\\evil.example')).toBe('/dashboard');
    expect(safeNext('javascript:alert(1)')).toBe('/dashboard');
    // Bouncing back to the sign-in page is a loop, not a destination.
    expect(safeNext('/login?next=/x')).toBe('/dashboard');
  });
});

describe('the display name', () => {
  it('follows the interface language and falls back to whatever exists', () => {
    const user: SessionUser = userFromServer(currentUser);
    expect(displayName(user, 'en')).toBe('Dr Test Physician');
    expect(displayName(user, 'bn')).toBe('ডা. পরীক্ষা চিকিৎসক');
    expect(displayName({ ...user, nameBN: '' }, 'bn')).toBe('Dr Test Physician');
    expect(displayName({ ...user, nameBN: '', nameEN: '' }, 'en')).toBe('E001');
  });
});
