import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import { ROUTE_GROUPS } from '@/lib/navigation';
import { ROLES, roleCan } from '@/lib/permissions';
import { useSessionStore } from '@/stores/session';
import { useUiStore } from '@/stores/ui';

/**
 * The shell, rendered.
 *
 * `next/navigation` and the locale server action are the two things a component test
 * cannot have for real — one needs a router, the other needs a request. Both are replaced
 * here, and nothing else is.
 */

const pathname = vi.hoisted(() => ({ current: '/dashboard' }));

vi.mock('next/navigation', () => ({
  usePathname: () => pathname.current,
  useRouter: () => ({ push: vi.fn(), refresh: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
}));

const setLocale = vi.hoisted(() => vi.fn());
vi.mock('@/lib/i18n/actions', () => ({ setLocale }));

const { Sidebar } = await import('@/components/Sidebar');
const { Topbar } = await import('@/components/Topbar');
const { Breadcrumbs } = await import('@/components/Breadcrumbs');
const { LanguageToggle } = await import('@/components/LanguageToggle');
const { OfflineBanner } = await import('@/components/OfflineBanner');
const { Can } = await import('@/components/Can');

const initialSession = useSessionStore.getState();
const initialUi = useUiStore.getState();

beforeEach(() => {
  pathname.current = '/dashboard';
  useSessionStore.setState(initialSession, true);
  useUiStore.setState(initialUi, true);
  setLocale.mockClear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the sidebar is the navigation definition', () => {
  it('has no navigation item that no role can reach', () => {
    // A dead entry in the definition is invisible: it renders for nobody, so no screen
    // ever looks wrong. This is the only place it can be caught.
    const unreachable = ROUTE_GROUPS.flatMap((group) => group.items).filter(
      (item) => !ROLES.some((role) => roleCan(role, item.permission)),
    );
    expect(
      unreachable.map((item) => item.href),
      'Navigation items no role has permission to see',
    ).toEqual([]);
  });

  it('renders every item the definition declares, for a role that may see it', () => {
    // Rendered per group rather than all at once, because the sidebar shows one role's
    // view and no single role holds every permission — which is the point of it.
    for (const group of ROUTE_GROUPS) {
      for (const item of group.items) {
        const role = ROLES.find((candidate) => roleCan(candidate, item.permission));
        expect(role, `no role grants ${item.permission}`).toBeDefined();

        useSessionStore.setState({ activeRole: role });
        const view = renderWithProviders(<Sidebar />);

        const nav = screen.getByRole('navigation', { name: 'Primary' });
        expect(within(nav).getByRole('link', { name: labelFor(item.labelKey) })).toHaveAttribute(
          'href',
          item.href,
        );

        view.unmount();
      }
    }
  });

  it('marks the current page, and only the current page', () => {
    pathname.current = '/patients';
    renderWithProviders(<Sidebar />);

    const current = screen.getAllByRole('link').filter((link) => link.getAttribute('aria-current'));
    expect(current).toHaveLength(1);
    expect(current[0]).toHaveAttribute('href', '/patients');
  });

  it('keeps a page marked on a deeper path within it', () => {
    // /patients/0190a8f2 is still the Patients area. Losing the highlight on a detail
    // screen is how an operator loses track of where they are.
    pathname.current = '/patients/0190a8f2-0000-7000-8000-000000000001';
    renderWithProviders(<Sidebar />);
    expect(screen.getByRole('link', { name: 'Patients' })).toHaveAttribute('aria-current', 'page');
  });
});

describe('the sidebar shows only what the role can reach', () => {
  it('hides a group entirely when the role has none of its items', () => {
    useSessionStore.setState({ activeRole: 'pharmacy' });
    renderWithProviders(<Sidebar />);

    expect(screen.getByRole('link', { name: 'Pharmacy' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Research' })).not.toBeInTheDocument();
    // The heading goes with it. A heading over nothing tells the operator a section
    // exists and then does not say where it went.
    expect(screen.queryByText('Research')).not.toBeInTheDocument();
  });

  it('shows nothing at all when there is no active role', () => {
    useSessionStore.setState({ activeRole: null });
    renderWithProviders(<Sidebar />);
    expect(screen.queryAllByRole('link')).toHaveLength(0);
  });
});

describe('Can gates on the active role', () => {
  it('renders children for a permitted action', () => {
    useSessionStore.setState({ activeRole: 'physician' });
    renderWithProviders(
      <Can action="clinical.prescribe">
        <button type="button">Sign</button>
      </Can>,
    );
    expect(screen.getByRole('button', { name: 'Sign' })).toBeInTheDocument();
  });

  it('renders nothing for an action the role lacks', () => {
    useSessionStore.setState({ activeRole: 'pharmacy' });
    renderWithProviders(
      <Can action="clinical.prescribe">
        <button type="button">Sign</button>
      </Can>,
    );
    expect(screen.queryByRole('button', { name: 'Sign' })).not.toBeInTheDocument();
  });

  it('renders the fallback when one is given', () => {
    useSessionStore.setState({ activeRole: 'pharmacy' });
    renderWithProviders(
      <Can action="clinical.prescribe" fallback={<p>Not available</p>}>
        <button type="button">Sign</button>
      </Can>,
    );
    expect(screen.getByText('Not available')).toBeInTheDocument();
  });
});

describe('the language switch', () => {
  it('labels each language in its own language', () => {
    // "Bengali" written in English is no use to somebody who cannot read English.
    renderWithProviders(<LanguageToggle />);
    expect(screen.getByText('English')).toBeInTheDocument();
    expect(screen.getByText('বাংলা')).toBeInTheDocument();
  });

  it('marks the active language as pressed', () => {
    renderWithProviders(<LanguageToggle />, { locale: 'bn' });
    expect(screen.getByRole('button', { name: /বাংলা/ })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: /English/ })).toHaveAttribute(
      'aria-pressed',
      'false',
    );
  });

  it('tags each option with its own lang, so it is announced correctly', () => {
    renderWithProviders(<LanguageToggle />);
    expect(screen.getByText('বাংলা').closest('button')).toHaveAttribute('lang', 'bn');
  });

  it('asks the server to change the language', async () => {
    const user = userEvent.setup();
    renderWithProviders(<LanguageToggle />);
    await user.click(screen.getByRole('button', { name: /বাংলা/ }));
    expect(setLocale).toHaveBeenCalledWith('bn');
  });

  it('does nothing when the active language is clicked', async () => {
    const user = userEvent.setup();
    renderWithProviders(<LanguageToggle />);
    await user.click(screen.getByRole('button', { name: /English/ }));
    expect(setLocale).not.toHaveBeenCalled();
  });
});

describe('the shell speaks the interface language', () => {
  it('renders navigation in Bangla', () => {
    renderWithProviders(<Sidebar />, { locale: 'bn' });
    expect(screen.getByRole('link', { name: 'রোগী' })).toHaveAttribute('href', '/patients');
    expect(screen.queryByRole('link', { name: 'Patients' })).not.toBeInTheDocument();
  });

  it('renders breadcrumbs in Bangla', () => {
    pathname.current = '/patients';
    renderWithProviders(<Breadcrumbs />, { locale: 'bn' });
    expect(screen.getByText('রোগী')).toBeInTheDocument();
    expect(screen.getByText('চিকিৎসা')).toBeInTheDocument();
  });
});

describe('breadcrumbs', () => {
  it('names the group and the screen, not the path segments', () => {
    pathname.current = '/patients';
    renderWithProviders(<Breadcrumbs />);
    const trail = screen.getByRole('navigation', { name: 'Breadcrumb' });
    expect(within(trail).getByText('Clinical')).toBeInTheDocument();
    expect(within(trail).getByText('Patients')).toHaveAttribute('aria-current', 'page');
  });

  it('renders nothing on a path outside the navigation', () => {
    // The login page and the public verification page have no trail to show, and an
    // empty breadcrumb bar is furniture with no information in it.
    pathname.current = '/login';
    const { container } = renderWithProviders(<Breadcrumbs />);
    expect(container).toBeEmptyDOMElement();
  });

  it('does not put an identifier in the trail', () => {
    pathname.current = '/patients/0190a8f2-0000-7000-8000-000000000001';
    renderWithProviders(<Breadcrumbs />);
    expect(screen.queryByText(/0190a8f2/)).not.toBeInTheDocument();
  });
});

describe('the top bar', () => {
  it('names who is signed in', () => {
    renderWithProviders(<Topbar />);
    expect(screen.getByText(/Placeholder operator/)).toBeInTheDocument();
  });

  it('offers the roles the placeholder session holds, and switching changes the shell', async () => {
    // Scaffolding, and tested anyway: it is what makes "navigate all route groups"
    // possible for a reviewer before CP16 exists, and a broken switch would make five of
    // the nine groups unreachable without anybody noticing.
    const user = userEvent.setup();
    renderWithProviders(<Topbar />);

    const select = screen.getByRole('combobox', { name: 'Acting as' });
    expect(select).toHaveValue('physician');

    await user.selectOptions(select, 'admin');
    expect(useSessionStore.getState().activeRole).toBe('admin');
  });

  it('shows the newly chosen role its own areas', async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <Topbar />
        <Sidebar />
      </>,
    );

    expect(screen.queryByRole('link', { name: 'Administration' })).not.toBeInTheDocument();
    await user.selectOptions(screen.getByRole('combobox', { name: 'Acting as' }), 'admin');
    expect(screen.getByRole('link', { name: 'Administration' })).toBeInTheDocument();
  });

  it('opens and closes the sidebar drawer', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Topbar />);

    expect(useUiStore.getState().sidebarOpen).toBe(false);
    await user.click(screen.getByRole('button', { name: 'Menu' }));
    expect(useUiStore.getState().sidebarOpen).toBe(true);
  });

  it('closes the drawer when a destination is chosen', async () => {
    const user = userEvent.setup();
    useUiStore.setState({ sidebarOpen: true });
    renderWithProviders(<Sidebar />);

    await user.click(screen.getByRole('link', { name: 'Patients' }));
    expect(useUiStore.getState().sidebarOpen).toBe(false);
  });
});

describe('the connection banner', () => {
  it('says nothing while the device is online', () => {
    const { container } = renderWithProviders(<OfflineBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it('appears when the device goes offline, and says the entry is not lost', async () => {
    renderWithProviders(<OfflineBanner />);

    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    window.dispatchEvent(new Event('offline'));

    expect(await screen.findByText('No connection')).toBeInTheDocument();
    expect(
      screen.getByText(/will not be lost|will not reach the clinic record/),
    ).toBeInTheDocument();
  });

  it('is polite rather than assertive, so it does not interrupt', async () => {
    // A banner about the network interrupting a screen reader mid-sentence, while the
    // operator is reading a dose, is worse than the news it carries.
    renderWithProviders(<OfflineBanner />);
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    window.dispatchEvent(new Event('offline'));

    const banner = await screen.findByRole('status');
    expect(banner).toHaveAttribute('aria-live', 'polite');
  });
});

/** Resolves a message key to its English text, so assertions read as the screen does. */
function labelFor(key: string): string {
  const parts = key.split('.');
  let node: unknown = messagesEn;
  for (const part of parts) node = (node as Record<string, unknown>)[part];
  return String(node);
}

const messagesEn = (await import('../messages/en.json')).default;
