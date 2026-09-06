import { readFileSync } from 'node:fs';
import { test } from './fixtures';

const F = JSON.parse(readFileSync('/tmp/cp57-fixture.json', 'utf8'));
const OUT = '/home/claude/dthcms/Claude outputs';
const PATIENT = '0190d000-0000-7000-8000-000000000001';

const json = (body: unknown) => ({
  status: 200,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

function routes(page: import('@playwright/test').Page, overridden: boolean) {
  return page.route('**/v1/**', (route) => {
    const path = new URL(route.request().url()).pathname;
    if (route.request().method() !== 'GET') return route.fallback();
    if (path.endsWith('/visits') && path.includes('/patients/'))
      return route.fulfill(json({ visits: F.visits }));
    if (path.endsWith('/gate'))
      return route.fulfill(json({ gate: overridden ? F.gateOverridden : F.gate }));
    if (path.endsWith('/sessions')) return route.fulfill(json({ sessions: F.sessions }));
    if (path.includes('/counseling/sessions/')) return route.fulfill(json({ session: F.session }));
    if (path.includes('/patients/')) return route.fallback();
    return route.fallback();
  });
}

test('panel, gate holding', async ({ signedIn: page }) => {
  await routes(page, false);
  await page.setViewportSize({ width: 1280, height: 1600 });
  await page.goto(`/patients/${PATIENT}/counseling`);
  await page.getByTestId('counseling-gate').waitFor();
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${OUT}/cp57-panel-blocked.png`, fullPage: true });
});

test('panel, overridden', async ({ signedIn: page }) => {
  await routes(page, true);
  await page.setViewportSize({ width: 1280, height: 1600 });
  await page.goto(`/patients/${PATIENT}/counseling`);
  await page.getByTestId('override-record').waitFor();
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${OUT}/cp57-panel-overridden.png`, fullPage: true });
});

test('panel in bangla', async ({ bangla: page }) => {
  await routes(page, false);
  await page.setViewportSize({ width: 1280, height: 1600 });
  await page.goto(`/patients/${PATIENT}/counseling`);
  await page.getByTestId('counseling-gate').waitFor();
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${OUT}/cp57-panel-bn.png`, fullPage: true });
});
