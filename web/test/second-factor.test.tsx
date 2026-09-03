import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import { useSessionStore, userFromServer } from '@/stores/session';
import type { CurrentUser } from '@dthcms/api-client';

/**
 * CP17 on the client: the second step of sign-in, the security page from nothing to
 * enrolled, and the step-up prompt. The server is scripted; what is asserted is what the
 * screen does with each answer and what it sends — because a step-up token in the wrong
 * header is a privileged action that silently fails, and a recovery code shown twice is a
 * recovery code that is not one.
 */

const router = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn(), refresh: vi.fn() }));
const location = vi.hoisted(() => ({ pathname: '/account/security', search: '' }));

vi.mock('next/navigation', () => ({
  useRouter: () => router,
  usePathname: () => location.pathname,
  useSearchParams: () => new URLSearchParams(location.search),
}));
vi.mock('@/lib/i18n/actions', () => ({ setLocale: vi.fn() }));
// jsdom has no <dialog>.showModal. Give it one that toggles `open`, which is all the
// component relies on.
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function close() {
    this.removeAttribute('open');
  };
});

const { LoginForm, SecuritySettings, StepUpProvider } = await import('@/features/auth');

const enrolled: CurrentUser = {
  id: 'u1',
  employee_code: 'E001',
  name_en: 'Dr Test',
  name_bn: 'ডা. পরীক্ষা',
  status: 'active',
  roles: ['PHYSICIAN'],
  permissions: [],
  grants: [],
  second_factor: { required: true, enrolled: true, pending: false, recovery_codes_left: 10 },
};
const notEnrolled: CurrentUser = {
  ...enrolled,
  second_factor: { required: true, enrolled: false, pending: false, recovery_codes_left: 0 },
};

const unauthenticated = {
  error: {
    code: 'UNAUTHENTICATED',
    kind: 'auth',
    message: 'Please sign in again.',
    message_bn: 'অনুগ্রহ করে আবার সাইন ইন করুন।',
  },
};
const CODES = [
  'AAAA-BBBB-CCCC-DDDD',
  'EEEE-FFFF-GGGG-HHHH',
  'IIII-JJJJ-KKKK-LLLL',
  'MMMM-NNNN-OOOO-PPPP',
  'QQQQ-RRRR-SSSS-TTTT',
  'UUUU-VVVV-WWWW-XXXX',
  'YYYY-ZZZZ-2222-3333',
  '4444-5555-6666-7777',
  'ABCD-EFGH-IJKL-MNOP',
  'QRST-UVWX-YZ23-4567',
];

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

const initial = useSessionStore.getInitialState();

beforeEach(() => {
  useSessionStore.setState(initial, true);
  router.replace.mockClear();
  location.pathname = '/account/security';
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('signing in with a second factor', () => {
  it('asks for a code after the password, then signs in', async () => {
    location.pathname = '/login';
    const user = userEvent.setup();
    const calls = server({
      'GET /v1/auth/me': () => respond(unauthenticated, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticated, 401),
      'POST /v1/auth/login': () =>
        respond({ challenge: 'ch-1', expires_at: '2026-09-03T09:05:00Z' }, 202),
      'POST /v1/auth/login/second-factor': () =>
        respond({ access_token: 'x', expires_at: '', user: enrolled }),
    });
    renderWithProviders(<LoginForm />);

    await user.type(screen.getByLabelText(/^Employee code/), 'E001');
    await user.type(screen.getByLabelText(/^Password/), 'correct horse battery');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    // The second step, and no session yet.
    expect(await screen.findByText('Enter your authenticator code')).toBeInTheDocument();
    expect(useSessionStore.getState().status).not.toBe('authenticated');
    expect(router.replace).not.toHaveBeenCalled();

    await user.type(screen.getByLabelText(/^Authenticator code/), '123456');
    await user.click(screen.getByRole('button', { name: 'Continue' }));

    await waitFor(() => expect(router.replace).toHaveBeenCalledWith('/dashboard'));
    const completion = calls.find(
      (c) => new URL(c.url).pathname === '/v1/auth/login/second-factor',
    )!;
    expect(await completion.json()).toEqual({
      challenge: 'ch-1',
      transport: 'cookie',
      code: '123456',
    });
    expect(completion.headers.get('X-Requested-With')).toBe('DTHCMS');
  });

  it('accepts a recovery code instead', async () => {
    location.pathname = '/login';
    const user = userEvent.setup();
    const calls = server({
      'GET /v1/auth/me': () => respond(unauthenticated, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticated, 401),
      'POST /v1/auth/login': () => respond({ challenge: 'ch-1', expires_at: '' }, 202),
      'POST /v1/auth/login/second-factor': () =>
        respond({ access_token: 'x', expires_at: '', user: enrolled }),
    });
    renderWithProviders(<LoginForm />);

    await user.type(screen.getByLabelText(/^Employee code/), 'E001');
    await user.type(screen.getByLabelText(/^Password/), 'pw');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await screen.findByText('Enter your authenticator code');

    await user.click(screen.getByRole('button', { name: 'Use a recovery code instead' }));
    await user.type(screen.getByLabelText(/^Recovery code/), 'aaaa-bbbb-cccc-dddd');
    await user.click(screen.getByRole('button', { name: 'Continue' }));

    await waitFor(() => expect(router.replace).toHaveBeenCalled());
    const completion = calls.find(
      (c) => new URL(c.url).pathname === '/v1/auth/login/second-factor',
    )!;
    expect(await completion.json()).toEqual({
      challenge: 'ch-1',
      transport: 'cookie',
      recovery_code: 'aaaa-bbbb-cccc-dddd',
    });
  });

  it('shows one refusal for a wrong code and keeps the step', async () => {
    location.pathname = '/login';
    const user = userEvent.setup();
    server({
      'GET /v1/auth/me': () => respond(unauthenticated, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticated, 401),
      'POST /v1/auth/login': () => respond({ challenge: 'ch-1', expires_at: '' }, 202),
      'POST /v1/auth/login/second-factor': () => respond(unauthenticated, 401),
    });
    renderWithProviders(<LoginForm />);

    await user.type(screen.getByLabelText(/^Employee code/), 'E001');
    await user.type(screen.getByLabelText(/^Password/), 'pw');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
    await user.type(await screen.findByLabelText(/^Authenticator code/), '000000');
    await user.click(screen.getByRole('button', { name: 'Continue' }));

    expect(await screen.findByText('That code was not accepted.')).toBeInTheDocument();
    expect(screen.getByLabelText(/^Authenticator code/)).toHaveValue('');
    expect(router.replace).not.toHaveBeenCalled();
  });

  it('sends a person whose role requires enrolment to the security page, not the dashboard', async () => {
    location.pathname = '/login';
    location.search = '?next=%2Fpatients';
    const user = userEvent.setup();
    server({
      'GET /v1/auth/me': () => respond(unauthenticated, 401),
      'POST /v1/auth/refresh': () => respond(unauthenticated, 401),
      'POST /v1/auth/login': () =>
        respond({ access_token: 'x', expires_at: '', user: notEnrolled }),
    });
    renderWithProviders(<LoginForm />);

    await user.type(screen.getByLabelText(/^Employee code/), 'E001');
    await user.type(screen.getByLabelText(/^Password/), 'pw');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));

    await waitFor(() => expect(router.replace).toHaveBeenCalledWith('/account/security'));
    location.search = '';
  });
});

describe('the security page', () => {
  function signedIn(current: CurrentUser) {
    useSessionStore.setState({
      status: 'authenticated',
      user: userFromServer(current),
      activeRole: 'physician',
    });
  }

  it('walks from not enrolled to enrolled, and shows the recovery codes once', async () => {
    signedIn(notEnrolled);
    const user = userEvent.setup();
    const calls = server({
      'POST /v1/auth/second-factor/enrol': () =>
        respond({
          secret: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP',
          otpauth_uri: 'otpauth://totp/DTHCMS:E001?secret=JBSWY3DPEHPK3PXP&issuer=DTHCMS',
        }),
      'POST /v1/auth/second-factor/confirm': () => respond({ recovery_codes: CODES }),
      'GET /v1/auth/me': () => respond(enrolled),
    });
    renderWithProviders(
      <StepUpProvider>
        <SecuritySettings />
      </StepUpProvider>,
    );

    expect(screen.getByText('Your role requires an authenticator')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Set up authenticator' }));

    // The QR is drawn from the URI; the key is there for typing by hand.
    expect(
      await screen.findByRole('img', { name: 'QR code for your authenticator app' }),
    ).toBeInTheDocument();
    await user.click(screen.getByText('Cannot scan? Enter the key by hand'));
    expect(screen.getByLabelText('Setup key')).toHaveTextContent('JBSW Y3DP EHPK 3PXP');

    await user.type(screen.getByLabelText(/^Authenticator code/), '123456');
    await user.click(screen.getByRole('button', { name: 'Confirm and turn on' }));

    const list = await screen.findByRole('list', { name: 'Recovery codes' });
    expect(within(list).getAllByRole('listitem')).toHaveLength(10);
    expect(within(list).getByText('AAAA-BBBB-CCCC-DDDD')).toBeInTheDocument();
    expect(screen.getByText('These are shown once')).toBeInTheDocument();

    const confirm = calls.find(
      (c) => new URL(c.url).pathname === '/v1/auth/second-factor/confirm',
    )!;
    expect(await confirm.json()).toEqual({ code: '123456' });

    // "I have saved them" — and they are gone from the screen.
    await user.click(screen.getByRole('button', { name: 'I have saved them' }));
    expect(screen.queryByText('AAAA-BBBB-CCCC-DDDD')).not.toBeInTheDocument();
    expect(screen.getByText('On')).toBeInTheDocument();
    expect(useSessionStore.getState().user?.secondFactor.enrolled).toBe(true);
  });

  it('keeps the enrolment open on a wrong first code', async () => {
    signedIn(notEnrolled);
    const user = userEvent.setup();
    server({
      'POST /v1/auth/second-factor/enrol': () =>
        respond({
          secret: 'ABCDEFGHIJKLMNOP',
          otpauth_uri: 'otpauth://totp/x?secret=ABCDEFGHIJKLMNOP',
        }),
      'POST /v1/auth/second-factor/confirm': () =>
        respond(
          {
            error: {
              code: 'VALIDATION_FAILED',
              kind: 'validation',
              message: 'x',
              message_bn: 'y',
              fields: { code: 'nope' },
            },
          },
          422,
        ),
    });
    renderWithProviders(
      <StepUpProvider>
        <SecuritySettings />
      </StepUpProvider>,
    );
    await user.click(screen.getByRole('button', { name: 'Set up authenticator' }));
    await user.type(await screen.findByLabelText(/^Authenticator code/), '000000');
    await user.click(screen.getByRole('button', { name: 'Confirm and turn on' }));

    expect(await screen.findByText(/The code did not match/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Confirm and turn on' })).toBeInTheDocument();
  });

  it('asks for a step-up before replacing recovery codes, and sends the token in the header', async () => {
    signedIn(enrolled);
    const user = userEvent.setup();
    const calls = server({
      'POST /v1/auth/step-up': () =>
        respond({ step_up_token: 'su-1', purpose: 'second_factor.recovery_codes', expires_at: '' }),
      'POST /v1/auth/second-factor/recovery-codes': () => respond({ recovery_codes: CODES }),
      'GET /v1/auth/me': () => respond(enrolled),
    });
    renderWithProviders(
      <StepUpProvider>
        <SecuritySettings />
      </StepUpProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'Replace recovery codes' }));

    // The prompt, with what the person is about to do.
    const dialog = await screen.findByRole('dialog', { hidden: true });
    expect(within(dialog).getByText('Confirm it is you')).toBeInTheDocument();
    expect(within(dialog).getByText(/Replace your recovery codes/)).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText(/^Authenticator code/), '654321');
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }));

    await screen.findByRole('list', { name: 'Recovery codes' });

    const stepUp = calls.find((c) => new URL(c.url).pathname === '/v1/auth/step-up')!;
    expect(await stepUp.json()).toEqual({
      purpose: 'second_factor.recovery_codes',
      code: '654321',
    });
    const regenerate = calls.find(
      (c) => new URL(c.url).pathname === '/v1/auth/second-factor/recovery-codes',
    )!;
    expect(regenerate.headers.get('X-Step-Up-Token')).toBe('su-1');
    expect(regenerate.headers.get('X-Requested-With')).toBe('DTHCMS');
  });

  it('does nothing when the step-up is cancelled', async () => {
    signedIn(enrolled);
    const user = userEvent.setup();
    const calls = server({});
    renderWithProviders(
      <StepUpProvider>
        <SecuritySettings />
      </StepUpProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'Turn off' }));
    const dialog = await screen.findByRole('dialog', { hidden: true });
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    expect(calls).toHaveLength(0);
    expect(screen.getByText('On')).toBeInTheDocument();
  });

  it('turns the factor off with a step-up', async () => {
    signedIn(enrolled);
    const user = userEvent.setup();
    const calls = server({
      'POST /v1/auth/step-up': () =>
        respond({ step_up_token: 'su-2', purpose: 'second_factor.disable', expires_at: '' }),
      'POST /v1/auth/second-factor/disable': () => new Response(null, { status: 204 }),
      'GET /v1/auth/me': () => respond(notEnrolled),
    });
    renderWithProviders(
      <StepUpProvider>
        <SecuritySettings />
      </StepUpProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'Turn off' }));
    const dialog = await screen.findByRole('dialog', { hidden: true });
    await user.type(within(dialog).getByLabelText(/^Authenticator code/), '111111');
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }));

    expect(
      await screen.findByText('The authenticator has been turned off for this account.'),
    ).toBeInTheDocument();
    const disable = calls.find(
      (c) => new URL(c.url).pathname === '/v1/auth/second-factor/disable',
    )!;
    expect(disable.headers.get('X-Step-Up-Token')).toBe('su-2');
    expect(screen.getByText('Off')).toBeInTheDocument();
  });

  it('shows the refusal inside the prompt for a wrong step-up code', async () => {
    signedIn(enrolled);
    const user = userEvent.setup();
    server({ 'POST /v1/auth/step-up': () => respond(unauthenticated, 401) });
    renderWithProviders(
      <StepUpProvider>
        <SecuritySettings />
      </StepUpProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'Turn off' }));
    const dialog = await screen.findByRole('dialog', { hidden: true });
    await user.type(within(dialog).getByLabelText(/^Authenticator code/), '000000');
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }));

    expect(await within(dialog).findByText('Please sign in again.')).toBeInTheDocument();
    expect(dialog).toHaveAttribute('open');
  });
});
