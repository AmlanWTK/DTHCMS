import { screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';

/**
 * The workstation code on the sign-in screen, and the label in the device console (CP82,
 * ADR-0021).
 *
 * Three things are asserted, and they are the three the decision rests on.
 *
 * **It is remembered.** The desk types `FRD-REG-1` once. That is a `localStorage` write, it
 * is deliberate, and ADR-0010 is untouched by it: the rule there forbids a *credential* in
 * web storage, and this is a label printed on a sticker on the monitor. A test that pins the
 * behaviour is also the thing that stops somebody "fixing" it later.
 *
 * **An unrecognised code still signs you in, and says so.** Refusing would let an
 * unauthenticated caller enumerate the clinic's desks, and would take a registration desk
 * down over a typo. What must not happen instead is silence: the person has to learn at
 * sign-in that this machine will not be able to save anything, rather than at their first
 * save. And the bad code must not be remembered, or every later sign-in fails the same way.
 *
 * **The console calls it a label, not a password.** An administrator who believes the code
 * is secret will not stick it to a monitor — and a code that is not on the monitor is a desk
 * that signs in with no device, which is exactly the state this checkpoint removes.
 */

const router = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn(), refresh: vi.fn() }));
vi.mock('next/navigation', () => ({
  useRouter: () => router,
  usePathname: () => '/login',
  useSearchParams: () => new URLSearchParams(''),
}));
vi.mock('@/lib/i18n/actions', () => ({ setLocale: vi.fn() }));

const { LoginForm, useWorkstationNotice } = await import('@/features/auth');
const { WorkstationNotice } = await import('@/components/WorkstationNotice');
const { DeviceConsole } = await import('@/features/devices');
const { normaliseWorkstation, readWorkstation, rememberWorkstation } =
  await import('@/features/auth/workstation');
const { useSessionStore } = await import('@/stores/session');

/**
 * The sign-in screen and the screen it redirects to, in one tree.
 *
 * This is the whole point of the test below. `router.replace` in the other tests is a spy
 * that navigates nowhere, so a banner rendered by the form stays on screen forever and a
 * test asserting it passes while the real product shows it for zero frames: the session
 * store marks the session authenticated synchronously inside `signIn()`, the redirect effect
 * fires in the same commit, and the form is gone before anything is painted.
 *
 * Here the spy really does swap the screen, exactly as the router does, and the destination
 * is the frame's notice — the same component `AppShell` renders above every signed-in page.
 * If the notice ever goes back to being owned by the form, this test fails.
 */
function SignInThenDestination() {
  const [path, setPath] = useState('/login');
  router.replace.mockImplementation((to: string) => setPath(to));

  if (path === '/login') return <LoginForm />;
  return (
    <main>
      <WorkstationNotice />
      <h1>Today</h1>
    </main>
  );
}

const STORAGE_KEY = 'dthcms.workstation';

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

const user = {
  id: 'u1',
  employee_code: 'REG01',
  name_en: 'Registration Clerk',
  name_bn: 'রেজিস্ট্রেশন কর্মী',
  facility_id: 'f1',
  roles: ['REGISTRATION'],
  grants: [{ role: 'REGISTRATION', permissions: ['patient.write.demographics'] }],
  permissions: ['patient.write.demographics'],
  second_factor: { required: false, enrolled: false, pending: false, recovery_codes_left: 0 },
  status: 'active',
};

beforeEach(() => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  useWorkstationNotice.getState().clear();
  useSessionStore.setState({ status: 'unknown', user: null, activeRole: null });
  router.replace.mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('the workstation code is a remembered label', () => {
  it('forgives the way a person copies it off a sticker', () => {
    expect(normaliseWorkstation(' frd-reg-1 ')).toBe('FRD-REG-1');
    expect(normaliseWorkstation('FRD - REG - 1')).toBe('FRD-REG-1');
    expect(normaliseWorkstation('frd_reg_1')).toBe('FRD-REG-1');
  });

  it('ignores stored rubbish rather than sending it', () => {
    window.localStorage.setItem(STORAGE_KEY, 'not a workstation code at all');
    expect(readWorkstation()).toBe('');
  });

  it('round-trips through storage, and forgetting is writing nothing', () => {
    rememberWorkstation('frd-reg-1');
    expect(readWorkstation()).toBe('FRD-REG-1');
    rememberWorkstation('');
    expect(readWorkstation()).toBe('');
  });
});

describe('signing in at a desk', () => {
  it('sends the code, and remembers it when the server recognised it', async () => {
    const person = userEvent.setup();
    const calls = server({
      'GET /v1/auth/me': () => respond({ error: { code: 'UNAUTHENTICATED' } }, 401),
      'POST /v1/auth/login': () =>
        respond({
          access_token: 't',
          expires_at: '2026-09-12T10:00:00Z',
          user,
          workstation: 'FRD-REG-1',
          workstation_recognised: true,
        }),
    });

    renderWithProviders(<LoginForm />);
    await person.type(await screen.findByLabelText(/^Employee code/), 'REG01');
    await person.type(screen.getByLabelText(/^Password/), 'correct horse');
    await person.type(screen.getByLabelText(/^Workstation code/), 'frd-reg-1');
    await person.click(screen.getByRole('button', { name: 'Sign in' }));

    await waitFor(() => expect(window.localStorage.getItem(STORAGE_KEY)).toBe('FRD-REG-1'));

    const login = calls.find((call) => call.url.includes('/v1/auth/login'));
    expect(login).toBeDefined();
    // Upper case on the wire, whatever was typed: the server's lookup is an equality.
    await expect(login!.clone().json()).resolves.toMatchObject({ workstation: 'FRD-REG-1' });
  });

  it('offers the remembered code so the desk types it once', async () => {
    window.localStorage.setItem(STORAGE_KEY, 'FRD-REG-1');
    server({ 'GET /v1/auth/me': () => respond({ error: { code: 'UNAUTHENTICATED' } }, 401) });

    renderWithProviders(<LoginForm />);
    await waitFor(() =>
      expect(screen.getByLabelText(/^Workstation code/)).toHaveValue('FRD-REG-1'),
    );
  });

  it('signs the person in with an unrecognised code, says so, and does not remember it', async () => {
    const person = userEvent.setup();
    window.localStorage.setItem(STORAGE_KEY, 'FRD-REG-1');
    server({
      'GET /v1/auth/me': () => respond({ error: { code: 'UNAUTHENTICATED' } }, 401),
      'POST /v1/auth/login': () =>
        respond({
          access_token: 't',
          expires_at: '2026-09-12T10:00:00Z',
          user,
          workstation_recognised: false,
        }),
    });

    renderWithProviders(<LoginForm />);
    await person.clear(await screen.findByLabelText(/^Workstation code/));
    await person.type(screen.getByLabelText(/^Workstation code/), 'FRD-NOPE-9');
    await person.type(screen.getByLabelText(/^Employee code/), 'REG01');
    await person.type(screen.getByLabelText(/^Password/), 'correct horse');
    await person.click(screen.getByRole('button', { name: 'Sign in' }));

    // Said out loud. The sign-in succeeded; what failed is the desk.
    expect(await screen.findByText('This workstation was not recognised')).toBeInTheDocument();
    // And not treated as a credential failure: the person is signed in.
    expect(screen.queryByText(/employee code or password/i)).not.toBeInTheDocument();

    // A code that named nothing is forgotten, or every later sign-in here fails the same way.
    await waitFor(() => expect(window.localStorage.getItem(STORAGE_KEY)).toBeNull());
  });
});

describe('the notice survives the redirect that follows the sign-in', () => {
  it('is on screen after the sign-in screen has been replaced by the destination', async () => {
    const person = userEvent.setup();
    server({
      'GET /v1/auth/me': () => respond({ error: { code: 'UNAUTHENTICATED' } }, 401),
      'POST /v1/auth/login': () =>
        respond({
          access_token: 't',
          expires_at: '2026-09-12T10:00:00Z',
          user,
          workstation_recognised: false,
        }),
    });

    renderWithProviders(<SignInThenDestination />);

    await person.type(await screen.findByLabelText(/^Employee code/), 'REG01');
    await person.type(screen.getByLabelText(/^Password/), 'correct horse');
    await person.type(screen.getByLabelText(/^Workstation code/), 'FRD-NOPE-9');
    await person.click(screen.getByRole('button', { name: 'Sign in' }));

    // The redirect really happened: the sign-in form is gone and the destination is mounted.
    await screen.findByRole('heading', { name: 'Today' });
    expect(screen.queryByLabelText(/^Workstation code/)).not.toBeInTheDocument();
    expect(router.replace).toHaveBeenCalledWith('/dashboard');

    // And the sentence is on the destination screen, where the person actually is.
    expect(await screen.findByText('This workstation was not recognised')).toBeVisible();
    expect(
      screen.getByText(/nothing can be saved to a patient record from this computer/i),
    ).toBeVisible();

    // Still true a moment later — this is the four seconds of polling that found nothing.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(screen.getByText('This workstation was not recognised')).toBeVisible();

    // The code that named nothing is still not written to localStorage (ADR-0021 §4).
    expect(window.localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('can be acknowledged, and then stays gone', async () => {
    const person = userEvent.setup();
    useWorkstationNotice.getState().raise();
    renderWithProviders(<WorkstationNotice />);

    expect(await screen.findByText('This workstation was not recognised')).toBeVisible();
    await person.click(screen.getByRole('button', { name: /dismiss/i }));
    await waitFor(() =>
      expect(screen.queryByText('This workstation was not recognised')).not.toBeInTheDocument(),
    );
  });

  it('comes back after a reload, because the session it describes does', async () => {
    useWorkstationNotice.getState().raise();
    // A reload is a fresh store with whatever storage kept.
    useWorkstationNotice.setState({ unrecognised: false });

    renderWithProviders(<WorkstationNotice />);
    expect(await screen.findByText('This workstation was not recognised')).toBeVisible();
  });
});

describe('enrolling a desk in the console', () => {
  const desk = {
    id: 'd9',
    name: 'Registration desk 1',
    kind: 'desktop' as const,
    status: 'active' as const,
    enrolled_at: '2026-09-12T09:00:00Z',
    model: '',
    os_version: '',
    app_version: '',
    last_seen_at: null,
    status_changed_at: '2026-09-12T09:00:00Z',
    status_reason: '',
    created_at: '2026-09-12T09:00:00Z',
    workstation_code: 'FRD-REG-1',
  };

  it('asks for the desk, mints the code, and says it is not a password', async () => {
    const person = userEvent.setup();
    let devices: unknown[] = [];
    const calls = server({
      'GET /v1/devices': () => respond({ devices }),
      'POST /v1/devices': () => {
        devices = [desk];
        return respond({ device: desk, workstation_code: 'FRD-REG-1' }, 201);
      },
    });

    renderWithProviders(<DeviceConsole />);

    await person.type(await screen.findByLabelText(/^Name/), 'Registration desk 1');
    // The desk field appears only for a desktop: a tablet has no code to print.
    expect(screen.queryByLabelText(/^Desk/)).not.toBeInTheDocument();
    await person.selectOptions(screen.getByLabelText(/^Kind/), 'desktop');
    await person.type(await screen.findByLabelText(/^Desk/), 'REG');
    await person.click(screen.getByRole('button', { name: /Enrol and get a workstation code/ }));

    // The code appears as the thing to read out, on the card that will be printed and stuck
    // to the monitor, and again in the list — none of which is a leak, because it is a label.
    expect(await screen.findByLabelText('Workstation code')).toHaveTextContent('FRD-REG-1');
    expect(screen.getByText('This is a label, not a password')).toBeInTheDocument();
    // No fifteen-minute expiry and no "shown once": this is not that kind of code.
    expect(screen.queryByText('Shown once')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Print the card' })).toBeInTheDocument();

    const enrol = calls.find((call) => call.method === 'POST' && call.url.includes('/v1/devices'));
    await expect(enrol!.clone().json()).resolves.toMatchObject({
      kind: 'desktop',
      station: 'REG',
    });
  });

  it('shows the code in the list, and offers no new enrolment code for a desk', async () => {
    server({ 'GET /v1/devices': () => respond({ devices: [desk] }) });
    renderWithProviders(<DeviceConsole />);

    const row = (await screen.findByRole('row', { name: /Registration desk 1/ })) as HTMLElement;
    expect(row.textContent).toContain('FRD-REG-1');
    // A named workstation has no key to re-exchange; the server refuses one, so the console
    // must not offer it.
    expect(screen.queryByRole('button', { name: 'New code' })).not.toBeInTheDocument();
  });
});
