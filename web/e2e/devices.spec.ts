import { PHYSICIAN, expect, test } from './fixtures';

/**
 * CP18 in a browser: the device console. Register a tablet and read the code; revoke one
 * with a reason through a real <dialog>.
 */

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

interface DeviceRow {
  id: string;
  name: string;
  kind: string;
  status: string;
  enrolled_at: string | null;
  model: string;
  os_version: string;
  app_version: string;
  last_seen_at: string | null;
  status_changed_at: string;
  status_reason: string;
  created_at: string;
}

const TABLET: DeviceRow = {
  id: '0190a8f2-0000-7000-8000-0000000000d1',
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

test.describe('CP18: the device console', () => {
  test('registers a device and shows its code once', async ({ signedIn: page }) => {
    let devices: DeviceRow[] = [TABLET];
    await page.route('**/v1/devices', async (route) => {
      if (route.request().method() === 'POST') {
        const body = route.request().postDataJSON() as { name: string; kind: string };
        const created: DeviceRow = {
          ...TABLET,
          id: '0190a8f2-0000-7000-8000-0000000000d2',
          name: body.name,
          kind: body.kind,
          status: 'pending',
          enrolled_at: null,
          model: '',
          os_version: '',
          app_version: '',
          last_seen_at: null,
        };
        devices = [...devices, created];
        return route.fulfill(
          json({ device: created, code: 'K7Q2M-9XWRD', expires_at: '2099-01-01T00:15:00Z' }, 201),
        );
      }
      return route.fulfill(json({ devices }));
    });

    await page.goto('/admin/devices');
    await expect(page.getByRole('heading', { name: 'Devices', level: 1 })).toBeVisible();
    await expect(page.getByRole('row', { name: /Anthropometry tablet 1/ })).toBeVisible();

    await page.getByLabel(/^Name/).fill('Registration tablet');
    await page.getByRole('button', { name: 'Register and get a code' }).click();

    await expect(page.getByText('K7Q2M-9XWRD')).toBeVisible();
    await expect(page.getByRole('row', { name: /Registration tablet/ })).toContainText(
      'Awaiting enrolment',
    );
    await page.getByRole('button', { name: 'Done' }).click();
    await expect(page.getByText('K7Q2M-9XWRD')).toHaveCount(0);
  });

  test('revokes a device with a reason', async ({ signedIn: page }) => {
    let devices = [TABLET];
    await page.route('**/v1/devices', (route) => route.fulfill(json({ devices })));
    await page.route(`**/v1/devices/${TABLET.id}/revoke`, (route) => {
      const body = route.request().postDataJSON() as { reason: string };
      expect(body.reason).toBe('screen cracked');
      devices = [{ ...TABLET, status: 'revoked', status_reason: body.reason }];
      return route.fulfill(json(devices[0]));
    });

    await page.goto('/admin/devices');
    const row = page.getByRole('row', { name: /Anthropometry tablet 1/ });
    await row.getByRole('button', { name: 'Revoke' }).click();

    const dialog = page.getByRole('dialog', { name: 'Revoke Anthropometry tablet 1?' });
    await expect(dialog).toBeVisible();
    await dialog.getByLabel(/^Reason/).fill('screen cracked');
    await dialog.getByRole('button', { name: 'Revoke' }).click();

    await expect(page.getByText('Anthropometry tablet 1 is now Revoked.')).toBeVisible();
    await expect(row).toContainText('Revoked');
    await expect(row.getByRole('button')).toHaveCount(0);
    expect(PHYSICIAN.roles).toContain('ADMIN');
  });
});
