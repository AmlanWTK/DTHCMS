import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import { transitionsFor } from '@/features/devices';
import type { components } from '@dthcms/api-client';

/**
 * CP18 on the client: the device console. The server is scripted; what is asserted is
 * what the administrator sees and what the console sends — the reason travels with a
 * revocation, the code is shown once and then gone, and a terminal device offers nothing
 * that would pretend to bring it back.
 */

const router = vi.hoisted(() => ({ replace: vi.fn(), push: vi.fn(), refresh: vi.fn() }));
vi.mock('next/navigation', () => ({
  useRouter: () => router,
  usePathname: () => '/admin/devices',
  useSearchParams: () => new URLSearchParams(''),
}));
vi.mock('@/lib/i18n/actions', () => ({ setLocale: vi.fn() }));
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function close() {
    this.removeAttribute('open');
  };
});

const { DeviceConsole } = await import('@/features/devices');

type Device = components['schemas']['Device'];

const active: Device = {
  id: 'd1',
  name: 'Anthropometry tablet 1',
  kind: 'tablet',
  status: 'active',
  enrolled_at: '2026-09-03T09:00:00Z',
  model: 'Samsung SM-X200',
  os_version: 'Android 13',
  app_version: '1.2.0',
  last_seen_at: '2026-09-03T10:42:00Z',
  status_changed_at: '2026-09-03T09:00:00Z',
  status_reason: '',
  created_at: '2026-09-03T08:55:00Z',
};
const pending: Device = {
  ...active,
  id: 'd2',
  name: 'Spare phone',
  kind: 'phone',
  status: 'pending',
  enrolled_at: null,
  model: '',
  os_version: '',
  app_version: '',
  last_seen_at: null,
};
const lost: Device = {
  ...active,
  id: 'd3',
  name: 'Old tablet',
  status: 'lost',
  status_reason: 'not seen since Tuesday',
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

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('the device list', () => {
  it('shows every device with its status, hardware, app version and last-seen', async () => {
    server({ 'GET /v1/devices': () => respond({ devices: [active, pending, lost] }) });
    renderWithProviders(<DeviceConsole />);

    const row = (await screen.findByRole('row', { name: /Anthropometry tablet 1/ })) as HTMLElement;
    expect(within(row).getByText('Active')).toBeInTheDocument();
    expect(within(row).getByText('Samsung SM-X200 · Android 13')).toBeInTheDocument();
    expect(within(row).getByText('1.2.0')).toBeInTheDocument();
    expect(within(row).getByRole('button', { name: 'Suspend' })).toBeInTheDocument();
    expect(within(row).getByRole('button', { name: 'Report lost' })).toBeInTheDocument();

    const spare = screen.getByRole('row', { name: /Spare phone/ });
    expect(within(spare).getByText('Awaiting enrolment')).toBeInTheDocument();
    expect(within(spare).getByText('Never')).toBeInTheDocument();
    expect(within(spare).queryByRole('button', { name: 'Suspend' })).not.toBeInTheDocument();

    // A lost device: its reason, and no way back.
    const old = screen.getByRole('row', { name: /Old tablet/ });
    expect(within(old).getByText('not seen since Tuesday')).toBeInTheDocument();
    expect(within(old).queryAllByRole('button')).toHaveLength(0);
  });

  it('mirrors the server’s transition table', () => {
    expect(transitionsFor('active')).toEqual(['suspend', 'revoke', 'lost']);
    expect(transitionsFor('suspended')).toEqual(['reinstate', 'revoke', 'lost']);
    expect(transitionsFor('pending')).toEqual(['revoke']);
    expect(transitionsFor('revoked')).toEqual([]);
    expect(transitionsFor('lost')).toEqual([]);
  });
});

describe('registering a device', () => {
  it('shows the code once, then the list again', async () => {
    const user = userEvent.setup();
    let devices = [active];
    const calls = server({
      'GET /v1/devices': () => respond({ devices }),
      'POST /v1/devices': () => {
        devices = [...devices, pending];
        return respond(
          { device: pending, code: 'K7Q2M-9XWRD', expires_at: '2026-09-03T11:00:00Z' },
          201,
        );
      },
    });
    renderWithProviders(<DeviceConsole />);
    await screen.findByRole('row', { name: /Anthropometry tablet 1/ });

    await user.type(screen.getByLabelText(/^Name/), 'Spare phone');
    await user.selectOptions(screen.getByLabelText(/^Kind/), 'phone');
    await user.click(screen.getByRole('button', { name: 'Register and get a code' }));

    expect(await screen.findByText('K7Q2M-9XWRD')).toBeInTheDocument();
    const issue = calls.find((c) => c.method === 'POST')!;
    expect(issue.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(await issue.json()).toEqual({ name: 'Spare phone', kind: 'phone' });

    // The list refreshed behind the code; closing the panel forgets the code.
    expect(screen.getByRole('row', { name: /Spare phone/ })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByText('K7Q2M-9XWRD')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Register and get a code' })).toBeInTheDocument();
  });

  it('says so when the name is taken', async () => {
    const user = userEvent.setup();
    server({
      'GET /v1/devices': () => respond({ devices: [active] }),
      'POST /v1/devices': () =>
        respond(
          {
            error: {
              code: 'VALIDATION_FAILED',
              kind: 'validation',
              message: 'Validation failed.',
              message_bn: 'যাচাই ব্যর্থ।',
              fields: { name: 'a device with that name already exists' },
            },
          },
          422,
        ),
    });
    renderWithProviders(<DeviceConsole />);
    await screen.findByRole('row', { name: /Anthropometry tablet 1/ });

    await user.type(screen.getByLabelText(/^Name/), 'Anthropometry tablet 1');
    await user.click(screen.getByRole('button', { name: 'Register and get a code' }));
    expect(await screen.findByText('A device with that name already exists.')).toBeInTheDocument();
  });
});

describe('revoking a device', () => {
  it('asks for a reason and sends it', async () => {
    const user = userEvent.setup();
    let devices = [active];
    const calls = server({
      'GET /v1/devices': () => respond({ devices }),
      'POST /v1/devices/d1/revoke': () => {
        devices = [{ ...active, status: 'revoked', status_reason: 'screen cracked' }];
        return respond(devices[0]);
      },
    });
    renderWithProviders(<DeviceConsole />);
    const row = await screen.findByRole('row', { name: /Anthropometry tablet 1/ });

    await user.click(within(row).getByRole('button', { name: 'Revoke' }));
    const dialog = screen.getByRole('dialog', { name: 'Revoke Anthropometry tablet 1?' });
    const confirm = within(dialog).getByRole('button', { name: 'Revoke' });
    expect(confirm).toBeDisabled();
    await user.type(within(dialog).getByLabelText(/^Reason/), 'screen cracked');
    await user.click(confirm);

    await waitFor(() =>
      expect(screen.getByText('Anthropometry tablet 1 is now Revoked.')).toBeInTheDocument(),
    );
    const revoke = calls.find((c) => new URL(c.url).pathname === '/v1/devices/d1/revoke')!;
    expect(await revoke.json()).toEqual({ reason: 'screen cracked' });
    // The dialog closed. jsdom keeps the element; what matters is that it is no longer open.
    expect(document.querySelector('dialog[open]')).toBeNull();
    const after = screen.getByRole('row', { name: /Anthropometry tablet 1/ });
    expect(within(after).getByText('Revoked')).toBeInTheDocument();
    expect(within(after).queryAllByRole('button')).toHaveLength(0);
  });
});
