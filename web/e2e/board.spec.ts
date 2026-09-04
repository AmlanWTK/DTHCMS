import { expect, test } from './fixtures';

/**
 * The Clinic Traffic Control board in a real browser (CP40, §5.2).
 *
 * What only a browser can show: that the wall variant is legible without controls, that a
 * bottleneck is visually distinct *and* labelled in words, and that a reroute started from
 * the board sends the right thing to the server.
 *
 * The privacy property — no name, no diagnosis, no patient id — is proven where it is
 * enforced: in the database view and in the Go suite. A browser test asserting the absence
 * of a field the server never sends would be a test of the mock.
 */

const BOARD = {
  day: '2026-09-14',
  generated_at: '2026-09-14T04:42:00Z',
  settings: {
    identify_by: 'code',
    busy_wait_seconds: 900,
    busy_depth: 4,
    bottleneck_wait_seconds: 1800,
    bottleneck_depth: 7,
  },
  stations: [
    {
      station_code: 'STN_REGISTRATION',
      position: 1,
      heat: 'calm',
      waiting: 1,
      called: 0,
      in_service: 1,
      longest_wait_seconds: 120,
      entries: [
        {
          entry_id: 'e-reg-1',
          visit_id: 'v-1',
          label: 'V-2026-0914-021',
          status: 'waiting',
          priority: 0,
          flagged: false,
          counseling_done: false,
          waited_seconds: 120,
        },
      ],
    },
    {
      station_code: 'STN_ANTHROPOMETRY',
      position: 2,
      heat: 'busy',
      waiting: 5,
      called: 1,
      in_service: 1,
      longest_wait_seconds: 1020,
      entries: [
        {
          entry_id: 'e-anth-1',
          visit_id: 'v-2',
          label: 'V-2026-0914-014',
          status: 'waiting',
          priority: 5,
          flagged: true,
          counseling_done: true,
          waited_seconds: 1020,
        },
        {
          entry_id: 'e-anth-2',
          visit_id: 'v-3',
          label: 'V-2026-0914-018',
          status: 'called',
          priority: 0,
          flagged: false,
          counseling_done: false,
          waited_seconds: 300,
        },
      ],
    },
    {
      station_code: 'STN_EXAMINATION',
      position: 5,
      heat: 'bottleneck',
      waiting: 8,
      called: 0,
      in_service: 1,
      longest_wait_seconds: 2280,
      entries: [
        {
          entry_id: 'e-exam-1',
          visit_id: 'v-4',
          label: 'V-2026-0914-006',
          status: 'waiting',
          priority: 0,
          flagged: false,
          counseling_done: true,
          waited_seconds: 2280,
        },
        {
          entry_id: 'e-exam-2',
          visit_id: 'v-5',
          label: 'V-2026-0914-009',
          status: 'waiting',
          priority: 0,
          flagged: false,
          counseling_done: true,
          waited_seconds: 1500,
        },
      ],
    },
    {
      station_code: 'STN_NUTRITION',
      position: 7,
      heat: 'calm',
      waiting: 0,
      called: 0,
      in_service: 1,
      longest_wait_seconds: 0,
      entries: [
        {
          entry_id: 'e-nut-1',
          visit_id: 'v-6',
          label: 'V-2026-0914-003',
          status: 'in_service',
          priority: 0,
          flagged: false,
          counseling_done: true,
          waited_seconds: 60,
        },
      ],
    },
  ],
  suggestions: [
    {
      entry_id: 'e-exam-1',
      label: 'V-2026-0914-006',
      from: 'STN_EXAMINATION',
      to: 'STN_NUTRITION',
      waited_seconds: 2280,
      from_waiting: 8,
    },
  ],
  waiting_total: 14,
  in_building_total: 18,
};

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

test.beforeEach(async ({ page }) => {
  await page.route('**/v1/board**', (route) =>
    route.request().method() === 'GET' ? route.fulfill(json(BOARD)) : route.fallback(),
  );
});

test('the supervisor sees where the floor is backed up', async ({ signedIn: page }) => {
  await page.goto('/board');

  await expect(page.getByText('Backed up at Examination')).toBeVisible();
  // Colour is never the only signal: the level is spelled out, so the board still works for
  // a colour-blind supervisor and on a projector with a tired lamp.
  await expect(page.getByTestId('board-STN_EXAMINATION').getByTestId('heat')).toHaveText(
    'Backed up',
  );
  await expect(page.getByTestId('board-STN_NUTRITION').getByTestId('heat')).toHaveText('Clear');
});

test('the board offers a reroute and applies it on one tap', async ({ signedIn: page }) => {
  let sent: unknown = null;
  await page.route('**/v1/board/reroute/**', async (route) => {
    sent = route.request().postDataJSON();
    await route.fulfill(json({ entry: { id: 'e-new' } }));
  });

  await page.goto('/board');
  await page.getByRole('button', { name: 'Apply' }).click();

  // Arrives with the destination and the reason already filled in — that is what makes it
  // one tap rather than a form.
  await expect(page.getByLabel('Why')).toHaveValue('Examination has 8 waiting; Nutrition is free');
  await page.getByRole('button', { name: 'Move the patient' }).click();

  await expect.poll(() => sent).not.toBeNull();
  expect(sent).toMatchObject({
    to: 'STN_NUTRITION',
    // Composed in the reader's language and stored as what the supervisor actually saw.
    reason: 'Examination has 8 waiting; Nutrition is free',
  });
});

test('the wall display has nothing to click', async ({ signedIn: page }) => {
  // Somebody will lean on it.
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto('/board?display=wall');

  await expect(page.getByTestId('board-STN_EXAMINATION')).toContainText('Backed up');
  await expect(page.getByRole('button', { name: 'Apply' })).toHaveCount(0);
  await expect(page.getByText('Suggested moves')).toHaveCount(0);
});

test('the board reads in Bangla', async ({ bangla: page }) => {
  await page.goto('/board');
  await expect(page.getByRole('heading', { name: 'শারীরিক পরীক্ষা' })).toBeVisible();
  await expect(page.getByTestId('board-STN_EXAMINATION').getByTestId('heat')).toHaveText('জট');
});
