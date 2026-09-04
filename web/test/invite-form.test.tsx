import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { isUuidV7 } from '@dthcms/shared-schemas';

import { renderWithProviders } from './render';
import { passwordAcceptable, type AdminAccount, type RoleDefinition } from '@/features/users';

/**
 * Inviting a colleague at the desk (CP21).
 *
 * This is the screen that creates an account and hands somebody their first password, so
 * it is the screen an attacker would most like to reach. Three things have to be true of
 * every invitation it sends, and each of them fails silently:
 *
 *   - the administrator proves it is them *first*. The step-up is minted for
 *     `user.manage` and no other purpose — a token good for anything would let a session
 *     left unlocked at a nurses' station create an account with every role;
 *   - that token, the forgery guard and a fresh idempotency key all reach the server on
 *     the invitation itself. Without the last one, a tapped-twice button or a dropped
 *     connection is two colleagues where the clinic hired one;
 *   - a cancelled prompt creates nothing. The person changed their mind; there must be no
 *     account, and no error blaming them for it either.
 *
 * The rest is about not wasting somebody's time: the employee code and password rules are
 * the server's, applied here so a refusal does not arrive after an authenticator code has
 * been typed, and a refusal that does arrive lands on the field that caused it.
 */

const router = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn(), refresh: vi.fn() }));

vi.mock('next/navigation', () => ({
  useRouter: () => router,
  usePathname: () => '/admin/users',
  useSearchParams: () => new URLSearchParams(''),
}));
vi.mock('@/lib/i18n/actions', () => ({ setLocale: vi.fn() }));

// jsdom has no <dialog>.showModal. Give it one that toggles `open`, which is all the
// step-up prompt relies on.
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function close() {
    this.removeAttribute('open');
  };
});

const { StepUpProvider } = await import('@/features/auth');
const { InviteForm, generatePassword } = await import('@/features/users/components/InviteForm');

const CATALOGUE: RoleDefinition[] = [
  {
    code: 'NUTRITIONIST',
    name_en: 'Clinical nutritionist',
    name_bn: 'পুষ্টিবিদ',
    is_clinical: true,
    station: 'nutrition',
    permissions: ['observation.write.nutrition', 'patient.read.demographics'],
  },
  {
    code: 'HR',
    name_en: 'Human resources officer',
    name_bn: 'এইচআর',
    is_clinical: false,
    station: '',
    permissions: ['user.read'],
  },
];

const CREATED: AdminAccount = {
  id: 'u1',
  employee_code: 'N006',
  name_en: 'Rafiq Hasan',
  name_bn: 'রফিক হাসান',
  phone: '+8801700000000',
  email: 'rafiq@example.org',
  status: 'invited',
  status_reason: '',
  status_since: '2026-09-04T09:00:00Z',
  last_login_at: null,
  roles: ['NUTRITIONIST'],
  permissions: ['observation.write.nutrition', 'patient.read.demographics'],
  second_factor: { required: true, enrolled: false, pending: false, recovery_codes_left: 0 },
  created_at: '2026-09-04T09:00:00Z',
  sessions: [],
  grant_history: [],
};

const PASSWORD = 'correct horse battery';

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

function mount(overrides: { explain?: (error: unknown) => string } = {}) {
  const onInvited = vi.fn();
  const onCancel = vi.fn();
  const explain = vi.fn(overrides.explain ?? (() => 'That did not complete.'));
  renderWithProviders(
    <StepUpProvider>
      <InviteForm
        catalogue={CATALOGUE}
        explain={explain}
        onInvited={onInvited}
        onCancel={onCancel}
      />
    </StepUpProvider>,
  );
  return { onInvited, onCancel, explain };
}

type User = ReturnType<typeof userEvent.setup>;

/** Everything the form needs before it will send anything. */
async function fillIn(user: User, { code = 'n006 ' }: { code?: string } = {}) {
  await user.type(screen.getByLabelText(/^Employee code/), code);
  await user.type(screen.getByLabelText(/^Name \(English\)/), '  Rafiq Hasan  ');
  await user.type(screen.getByLabelText(/^Name \(Bengali\)/), 'রফিক হাসান');
  await user.type(screen.getByLabelText(/^Phone/), '+8801700000000');
  await user.type(screen.getByLabelText(/^E-mail/), 'rafiq@example.org');
  await user.type(screen.getByLabelText(/^First password/), PASSWORD);
  await user.click(screen.getByRole('checkbox', { name: /Clinical nutritionist/ }));
}

const submit = () => screen.getByRole('button', { name: 'Create the account' });

/** Answers the step-up prompt with a code, which is how every invitation is authorised. */
async function confirmStepUp(user: User) {
  const dialog = await screen.findByRole('dialog', { hidden: true });
  await within(dialog).findByText('Invite Rafiq Hasan');
  await user.type(within(dialog).getByLabelText(/^Authenticator code/), '123456');
  await user.click(within(dialog).getByRole('button', { name: 'Confirm' }));
  return dialog;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('what the form checks before the server is troubled', () => {
  it('will not send an invitation with no role on it', async () => {
    // An account with no role can sign in and do nothing — a colleague standing at a
    // screen that refuses them, and an administrator who thinks the job is done.
    const user = userEvent.setup();
    mount();

    expect(submit()).toBeDisabled();
    await fillIn(user);
    expect(submit()).toBeEnabled();

    await user.click(screen.getByRole('checkbox', { name: /Clinical nutritionist/ }));
    expect(submit()).toBeDisabled();
  });

  it('will not send an employee code the server would refuse', async () => {
    const user = userEvent.setup();
    mount();

    await fillIn(user, { code: 'N-006' });

    expect(submit()).toBeDisabled();
    expect(
      screen.getByText(
        'Capitals, digits and underscores; two to sixteen characters. Unique within the clinic.',
      ),
    ).toBeInTheDocument();
  });

  it('will not send a password shorter than the policy allows', async () => {
    const user = userEvent.setup();
    mount();

    await fillIn(user);
    await user.clear(screen.getByLabelText(/^First password/));
    await user.type(screen.getByLabelText(/^First password/), 'short');

    expect(submit()).toBeDisabled();
  });

  it('offers a generated password rather than letting somebody invent one', async () => {
    const user = userEvent.setup();
    mount();

    await fillIn(user);
    await user.clear(screen.getByLabelText(/^First password/));
    expect(submit()).toBeDisabled();

    await user.click(screen.getByRole('button', { name: 'Generate' }));

    const generated = (screen.getByLabelText(/^First password/) as HTMLInputElement).value;
    expect(passwordAcceptable(generated)).toBe(true);
    expect(generated).toMatch(/^[a-zA-Z2-9]{4}(-[a-zA-Z2-9]{4}){3}$/);
    expect(submit()).toBeEnabled();
  });

  it('generates from an alphabet with no look-alike characters', () => {
    // Read across a desk to a colleague. A 0 that is an O is a password nobody can use.
    for (let i = 0; i < 25; i++) expect(generatePassword()).not.toMatch(/[0O1lI]/);
  });
});

describe('sending the invitation', () => {
  it('proves it is the administrator first, with a token minted for user.manage alone', async () => {
    const user = userEvent.setup();
    const calls = server({
      'POST /v1/auth/step-up': () =>
        respond({ step_up_token: 'su-1', purpose: 'user.manage', expires_at: '' }),
      'POST /v1/admin/users': () => respond(CREATED, 201),
    });
    const { onInvited } = mount();

    await fillIn(user);
    await user.click(submit());
    await confirmStepUp(user);
    await waitFor(() => expect(onInvited).toHaveBeenCalled());

    const stepUp = calls.find((call) => new URL(call.url).pathname === '/v1/auth/step-up')!;
    expect(await stepUp.json()).toEqual({ purpose: 'user.manage', code: '123456' });
    // The order matters: nothing was created before the prompt was answered.
    expect(calls.map((call) => new URL(call.url).pathname)).toEqual([
      '/v1/auth/step-up',
      '/v1/admin/users',
    ]);
  });

  it('carries that token, the forgery guard and a fresh key to the invitation itself', async () => {
    const user = userEvent.setup();
    const calls = server({
      'POST /v1/auth/step-up': () =>
        respond({ step_up_token: 'su-1', purpose: 'user.manage', expires_at: '' }),
      'POST /v1/admin/users': () => respond(CREATED, 201),
    });
    const { onInvited } = mount();

    await fillIn(user);
    await user.click(submit());
    await confirmStepUp(user);
    await waitFor(() => expect(onInvited).toHaveBeenCalled());

    const invite = calls.find((call) => new URL(call.url).pathname === '/v1/admin/users')!;
    expect(invite.headers.get('X-Step-Up-Token')).toBe('su-1');
    expect(invite.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(isUuidV7(invite.headers.get('Idempotency-Key') ?? '')).toBe(true);
  });

  it('sends the details tidied the way the server stores them', async () => {
    const user = userEvent.setup();
    const calls = server({
      'POST /v1/auth/step-up': () =>
        respond({ step_up_token: 'su-1', purpose: 'user.manage', expires_at: '' }),
      'POST /v1/admin/users': () => respond(CREATED, 201),
    });
    const { onInvited } = mount();

    await fillIn(user);
    await user.click(submit());
    await confirmStepUp(user);
    await waitFor(() => expect(onInvited).toHaveBeenCalled());

    const invite = calls.find((call) => new URL(call.url).pathname === '/v1/admin/users')!;
    expect(await invite.json()).toEqual({
      employee_code: 'N006',
      name_en: 'Rafiq Hasan',
      name_bn: 'রফিক হাসান',
      phone: '+8801700000000',
      email: 'rafiq@example.org',
      roles: ['NUTRITIONIST'],
      password: PASSWORD,
    });
  });

  it('hands the new account and the password back, because it is shown once', async () => {
    const user = userEvent.setup();
    server({
      'POST /v1/auth/step-up': () =>
        respond({ step_up_token: 'su-1', purpose: 'user.manage', expires_at: '' }),
      'POST /v1/admin/users': () => respond(CREATED, 201),
    });
    const { onInvited } = mount();

    await fillIn(user);
    await user.click(submit());
    await confirmStepUp(user);

    await waitFor(() => expect(onInvited).toHaveBeenCalledWith(CREATED, PASSWORD));
  });
});

describe('when the invitation does not go through', () => {
  it('creates nothing and blames nobody when the prompt is cancelled', async () => {
    const user = userEvent.setup();
    const calls = server({});
    const { onInvited } = mount();

    await fillIn(user);
    await user.click(submit());
    const dialog = await screen.findByRole('dialog', { hidden: true });
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    expect(calls).toHaveLength(0);
    expect(onInvited).not.toHaveBeenCalled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    // And the form is usable again rather than stuck behind a spinner.
    await waitFor(() => expect(submit()).toBeEnabled());
  });

  it('marks the fields the server refused and says so once, at the top', async () => {
    const user = userEvent.setup();
    server({
      'POST /v1/auth/step-up': () =>
        respond({ step_up_token: 'su-1', purpose: 'user.manage', expires_at: '' }),
      'POST /v1/admin/users': () =>
        respond(
          {
            error: {
              code: 'VALIDATION_FAILED',
              kind: 'validation',
              message: 'Some fields were not accepted.',
              message_bn: 'কিছু ঘর গ্রহণ করা হয়নি।',
              fields: {
                employee_code: 'That code is already in use.',
                roles: 'That role cannot be granted from here.',
              },
            },
          },
          422,
        ),
    });
    const { onInvited, explain } = mount();

    await fillIn(user);
    await user.click(submit());
    await confirmStepUp(user);

    expect(
      await screen.findByText('Some details were not accepted. Check the fields marked below.'),
    ).toBeInTheDocument();
    expect(screen.getByText('That code is already in use.')).toBeInTheDocument();
    expect(screen.getByText('That role cannot be granted from here.')).toBeInTheDocument();
    // A per-field refusal is not something `explain` should be asked to paraphrase.
    expect(explain).not.toHaveBeenCalled();
    expect(onInvited).not.toHaveBeenCalled();
  });

  it('lets the screen say what any other refusal means', async () => {
    const user = userEvent.setup();
    server({
      'POST /v1/auth/step-up': () =>
        respond({ step_up_token: 'su-1', purpose: 'user.manage', expires_at: '' }),
      'POST /v1/admin/users': () =>
        respond(
          {
            error: {
              code: 'INTERNAL',
              kind: 'internal',
              message: 'Something went wrong.',
              message_bn: 'কিছু একটা সমস্যা হয়েছে।',
            },
          },
          500,
        ),
    });
    const { explain, onInvited } = mount({
      explain: () => 'That did not complete. Try again; if it keeps failing, tell the developer.',
    });

    await fillIn(user);
    await user.click(submit());
    await confirmStepUp(user);

    expect(
      await screen.findByText(
        'That did not complete. Try again; if it keeps failing, tell the developer.',
      ),
    ).toBeInTheDocument();
    expect(explain).toHaveBeenCalledOnce();
    expect(onInvited).not.toHaveBeenCalled();
  });
});

describe('leaving the form', () => {
  it('gives the way out back to the screen that opened it', async () => {
    const user = userEvent.setup();
    const { onCancel } = mount();

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalled();
  });
});
