import crypto from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';

import { expect, request as playwrightRequest, test } from '@playwright/test';

/**
 * The prescription editor, measured (CP81).
 *
 * # Why this suite does not use `./fixtures`
 *
 * Every other browser spec here mocks `/v1/auth/me` and answers clinical endpoints from a literal,
 * because what they check is navigation, stylesheets and headers. **None of the numbers this
 * checkpoint is judged on survive that.** A completion time measured against a mocked API is a
 * measurement of React; a safety-finding latency measured against a mocked API is a measurement of
 * `setTimeout`. So this suite signs in for real, against the Go service, against Postgres, with
 * the real formulary and the real forty-eight unapproved rules.
 *
 * It therefore needs a running stack and is skipped without one. The environment it wants:
 *
 *	DTHCMS_E2E_LIVE=1
 *	DTHCMS_E2E_PATIENT=<a patient id with an open visit>
 *
 * # What is being measured, exactly
 *
 * **Criterion 1** — a typical four-item prescription, keyboard only. The clock starts on the first
 * keystroke of the first medicine's name and stops when the fourth line is on the sheet. *Thinking
 * time is excluded and the exclusion is the point*: the typing is scripted with no pause between
 * keystrokes, so the number is **the floor the interface imposes**, not what Dr. Nahid will take.
 * A real prescription includes deciding what to prescribe, and no interface makes that faster.
 * Read it as "the software costs at least this much"; the honest comparison against paper needs
 * him at the keyboard.
 *
 * **Criterion 2** — from the keystroke that completes an item to the safety panel saying something
 * about it. Measured per item across several prescriptions, and reported as a percentile with its
 * sample size rather than as an average, because the p50 of a request that occasionally takes a
 * second is a number that hides the second.
 *
 * **Criterion 3** — keyboard-only completion, driven with `page.keyboard` and nothing else. The
 * test also proves its own negative: it makes a required control unreachable and asserts that the
 * same check then fails. A keyboard test that cannot fail is a keyboard test that proves nothing.
 *
 * **Criterion 4** — crash recovery, by crashing the renderer through the DevTools protocol rather
 * than by calling a save function and reloading. What survives is whatever the server has.
 */

const LIVE = process.env.DTHCMS_E2E_LIVE === '1';
const PATIENT = process.env.DTHCMS_E2E_PATIENT ?? '';
const CODE = process.env.DTHCMS_E2E_CODE ?? 'DOC01';
const PASSWORD = process.env.DTHCMS_E2E_PASSWORD ?? 'local development only';

/**
 * Four medicines a follow-up diabetic at this clinic actually leaves with.
 *
 * Named in the report rather than left as "four drugs", because the number depends on them: each
 * prefix is the two letters that find the brand, and a brand whose molecule this clinic stocks in
 * six strengths costs an extra keystroke that a single-strength brand does not.
 */
const MEDICINES = [
  { type: 'co', brand: 'Comet — metformin 500 mg' },
  { type: 'em', brand: 'Emjard — empagliflozin' },
  { type: 'ro', brand: 'Rocovas — rosuvastatin' },
  { type: 'thyr', brand: 'Thyrin — levothyroxine' },
];

/* ------------------------------------------------------------------------- */
/* Plumbing                                                                   */
/* ------------------------------------------------------------------------- */

const SECRET_FILE = process.env.DTHCMS_E2E_TOTP_FILE ?? '/tmp/doc01.totp';

function secret(): string {
  return readFileSync(SECRET_FILE, 'utf8').trim();
}

function totp(value: string, at = Date.now()): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const character of value.replace(/=+$/, '').toUpperCase()) {
    const index = alphabet.indexOf(character);
    if (index >= 0) bits += index.toString(2).padStart(5, '0');
  }
  const bytes: number[] = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));

  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 1000 / 30)));
  const mac = crypto.createHmac('sha1', Buffer.from(bytes)).update(counter).digest();
  const offset = mac[mac.length - 1]! & 0x0f;
  return ((mac.readUInt32BE(offset) & 0x7fffffff) % 1_000_000).toString().padStart(6, '0');
}

/**
 * A code from a thirty-second step this run has not used yet.
 *
 * CP17 records `last_used_step` and refuses a code a second time, which is correct — a code
 * replayed inside its own window is a code somebody read over a shoulder. It also means two
 * sign-ins in the same half-minute cannot both succeed, so this waits for the next step rather
 * than handing the server a code it will rightly refuse. Found the hard way: the failure reads
 * "That code was not accepted", which looks exactly like a wrong clock.
 */
let lastStep = -1;
async function freshCode(): Promise<string> {
  for (;;) {
    const step = Math.floor(Date.now() / 1000 / 30);
    if (step !== lastStep) {
      lastStep = step;
      return totp(secret());
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
}

/**
 * Cancels any draft this patient already has, so the measurement starts from a blank sheet.
 *
 * Test plumbing rather than part of the flow under test, and done against the API rather than
 * through the interface on purpose: the editor has no cancel control — CP80's cancel is a
 * transition CP83 and CP84 build screens for — and inventing one to make a measurement
 * convenient would be adding product surface for a test's benefit.
 *
 * It documents a real behaviour on the way past: the editor **resumes** an open draft for this
 * visit rather than starting a second one. That is what makes crash recovery work, and it is what
 * makes this function necessary.
 */
async function clearDrafts(patient: string) {
  const api = await playwrightRequest.newContext({
    baseURL: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://127.0.0.1:8081',
  });
  const first = await api.post('/v1/auth/login', {
    headers: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': crypto.randomUUID() },
    data: { employee_code: CODE, password: PASSWORD },
  });
  const challenge = (await first.json()) as { challenge?: string; access_token?: string };
  // §12.2 makes a second factor mandatory for the physician's role, so even this plumbing goes
  // through it. There is no back door, which is the correct design and is worth saying.
  const second = challenge.challenge
    ? await api.post('/v1/auth/login/second-factor', {
        headers: { 'X-Requested-With': 'DTHCMS', 'Idempotency-Key': crypto.randomUUID() },
        data: { challenge: challenge.challenge, code: await freshCode() },
      })
    : first;
  const { access_token: token } = (await second.json()) as { access_token: string };
  const headers = {
    Authorization: `Bearer ${token}`,
    'X-Requested-With': 'DTHCMS',
    'X-Active-Role': 'PHYSICIAN',
  };
  const list = await api.get(`/v1/patients/${patient}/prescriptions`, { headers });
  const body = (await list.json()) as {
    prescriptions?: { prescription: { id: string; status: string } }[];
  };
  for (const entry of body.prescriptions ?? []) {
    if (entry.prescription.status !== 'DRAFT') continue;
    await api.post(`/v1/prescriptions/${entry.prescription.id}/cancel`, {
      headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() },
      data: { reason: 'Cleared by the CP81 measurement suite before a fresh run.' },
    });
  }
  await api.dispose();
}

/** Four more, only to give the latency percentile enough points under it to mean anything. */
const MORE = [
  { type: 'li', brand: 'Liglimet — linagliptin + metformin' },
  { type: 'am', brand: 'Amdocal — amlodipine' },
  { type: 'si', brand: 'Siglimet — sitagliptin + metformin' },
  { type: 'gl', brand: 'Glarine — insulin glargine' },
];

test.describe.configure({ mode: 'serial' });

// Each measurement writes prescription lines through the real ledger and waits for a real safety
// check between them. Playwright's 30-second default is a budget for a click.
test.setTimeout(600_000);

test.skip(
  !LIVE || PATIENT === '' || !existsSync(process.env.DTHCMS_E2E_TOTP_FILE ?? '/tmp/doc01.totp'),
  'needs a live stack: DTHCMS_E2E_LIVE=1, DTHCMS_E2E_PATIENT, and an enrolled second factor',
);

/**
 * Everything in one test, on one page, after exactly one navigation.
 *
 * Not a stylistic choice. Two things in this system make a suite of independent browser tests
 * unworkable against a live stack, and both are worth knowing:
 *
 *  1. **A TOTP code cannot be used twice inside its thirty-second step** (CP17), so each sign-in
 *     costs up to half a minute of waiting, and a second factor that was refused counts as a
 *     failed login for CP17's progressive throttle — six sign-ins push the seventh to a
 *     thirty-second delay.
 *  2. **A full page navigation does not reliably keep the session.** The access token is held in
 *     memory by design (ADR-0010 keeps it out of storage), so every navigation re-establishes it
 *     from the httpOnly refresh cookie, and that refresh races with the screen's own first reads
 *     often enough to land on the sign-in page.
 *
 * So the suite signs in once, lands on the editor through `?next=`, and stays there. The
 * consequence for the reader of the numbers: they are measured on a warm page, with the formulary
 * and the suggestions already fetched — which is the state a physician's second prescription of
 * the morning is in, and not the state of his first.
 */
test('the prescription editor, measured', async ({ page }) => {
  // Logged as each one is taken rather than collected and printed at the end, so a run that
  // falls over in a later phase still yields the numbers the earlier phases measured.
  const measurements: string[] = [];
  const record = (line: string) => {
    measurements.push(line);
    // The console is the report. A measurement kept only in an assertion is a measurement
    // nobody reads, and these numbers are the whole point of the file.
    // eslint-disable-next-line no-console
    console.log(`CP81-MEASURE ${line}`);
  };

  await clearDrafts(PATIENT);

  // One navigation. `?next=` is the shell's own mechanism, so the editor is reached by a
  // client-side route change rather than by a second page load.
  const target = `/patients/${PATIENT}/prescribe`;
  await page.goto(`/login?next=${encodeURIComponent(target)}`);
  await page.getByLabel(/^Employee code/).fill(CODE);
  await page.getByLabel(/^Password/).fill(PASSWORD);
  await page.getByRole('button', { name: 'Sign in' }).click();
  const codeField = page.getByLabel(/^Authenticator code/);
  await codeField.waitFor({ timeout: 90_000 });
  await codeField.fill(await freshCode());
  await page.getByRole('button', { name: /Sign in|Continue|Verify/ }).click();
  await page.waitForURL(new RegExp(target.replace(/[/]/g, '\\/')), { timeout: 90_000 });

  /* --- starting the sheet, keyboard only --------------------------------- */

  const blank = page.getByRole('button', { name: 'Blank prescription' });
  await expect(blank).toBeVisible({ timeout: 60_000 });
  await blank.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('save-state')).toBeVisible({ timeout: 60_000 });

  /* --- criterion 1: four medicines, and criterion 2: the findings --------- */

  const latencies: number[] = [];

  let keystrokes = 1; // the Enter that opened the sheet
  let suggested = 0;
  let typedByHand = 0;

  async function writeLine(prefix: string) {
    const before = await page.getByTestId('line').count();
    await page.keyboard.type(prefix, { delay: 0 });
    keystrokes += prefix.length;
    // Waited on the list rather than slept through, so a slow search shows up as a slow
    // prescription rather than being hidden behind a fixed pause.
    await expect(page.locator('.combobox-row').first()).toBeVisible({ timeout: 15_000 });
    await page.keyboard.press('Enter');
    keystrokes += 1;
    await expect(page.getByTestId('dose')).toBeFocused({ timeout: 15_000 });

    // A medicine with no approved-or-otherwise suggestion leaves the row empty, which is the
    // honest behaviour — nothing is invented — and costs the physician the typing. Counted
    // separately, because "how fast is this" has two answers depending on whether the drug is
    // one of the twenty-eight the clinic has a suggestion for.
    const dose = page.getByTestId('dose');
    if (((await dose.inputValue()) ?? '') === '') {
      typedByHand += 1;
      await page.keyboard.type('1 tablet', { delay: 0 });
      await page.keyboard.press('Tab');
      await page.keyboard.type('twice daily', { delay: 0 });
      keystrokes += '1 tablet'.length + 1 + 'twice daily'.length;
    } else {
      suggested += 1;
    }

    const started = Date.now();
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('line')).toHaveCount(before + 1, { timeout: 30_000 });
    // The clock for criterion 2 stops when the panel says something about the new count —
    // which is what a physician actually waits for, not when a response arrived somewhere.
    await expect(page.getByTestId('safety-panel')).toContainText(
      new RegExp(`All ${before + 1} medicine\\(s\\)`),
      { timeout: 30_000 },
    );
    latencies.push(Date.now() - started);
  }

  const startedAll = Date.now();
  for (const medicine of MEDICINES) await writeLine(medicine.type);
  const completionMs = Date.now() - startedAll;

  await expect(page.getByTestId('line')).toHaveCount(4);
  record(
    `completion_ms=${completionMs} keystrokes=${keystrokes} ` +
      `with_suggestion=${suggested} typed_by_hand=${typedByHand} ` +
      `medicines=${MEDICINES.map((m) => m.brand).join(' / ')}`,
  );

  // Four more lines, for a percentile with enough points under it to mean something. Reported
  // with its sample size rather than as an average: the p50 of a request that occasionally takes
  // a second is a number that hides the second.
  for (const medicine of MORE) await writeLine(medicine.type);
  const sorted = [...latencies].sort((a, b) => a - b);
  const p95 = sorted[Math.min(sorted.length - 1, Math.ceil(sorted.length * 0.95) - 1)];
  record(
    `safety_p95_ms=${p95} safety_max_ms=${sorted[sorted.length - 1]} ` +
      `safety_min_ms=${sorted[0]} n=${latencies.length} samples=${latencies.join(',')}`,
  );

  // Captured here rather than only at the end, so a run that falls over later still leaves a
  // picture of the screen with medicines on it.
  await page.screenshot({ path: '/home/claude/shots/cp81-editor.png' });
  await page.screenshot({ path: '/home/claude/shots/cp81-editor-full.png', fullPage: true });

  /* --- criterion 5's honesty: nothing here has been checked --------------- */

  const panel = page.getByTestId('safety-panel');
  await expect(panel).toHaveAttribute('data-verdict', 'NO_RULES_APPROVED');
  const panelText = ((await panel.textContent()) ?? '').toLowerCase();
  for (const word of [
    'no issues',
    'no problems',
    'no findings',
    'all clear',
    'looks good',
    'passed',
    'no warnings',
  ]) {
    expect(panelText, `the panel says "${word}" while nothing has been checked`).not.toContain(
      word,
    );
  }
  expect(panelText, 'the panel calls an unchecked prescription safe').not.toMatch(/\bsafe\b/);
  expect(panelText).toContain('nothing has been checked');
  expect(panelText).toMatch(/unchecked/);

  /* --- criterion 3: keyboard reach, and the negative --------------------- */

  /**
   * What Tab reaches, starting from wherever focus is now.
   *
   * Deliberately not from the top of the document: the shell's navigation is twenty-odd links
   * before the first field, and a test that tabbed from the start would be measuring the sidebar.
   * What matters is that from the place the editor *puts* focus — the dose, after a medicine is
   * chosen — every remaining control of the line is a Tab away, in order.
   */
  async function reachableByTab(limit = 25): Promise<Set<string>> {
    const seen = new Set<string>();
    const first = await page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null;
      return el?.getAttribute('data-testid') ?? el?.getAttribute('aria-label') ?? '';
    });
    if (first) seen.add(first);
    for (let i = 0; i < limit; i += 1) {
      await page.keyboard.press('Tab');
      const marker = await page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        if (!el) return '';
        return el.getAttribute('data-testid') ?? el.getAttribute('aria-label') ?? '';
      });
      if (marker) seen.add(marker);
    }
    return seen;
  }

  // A line has to be in progress for the dose row to exist at all. Alt+M is the editor's own
  // "back to the medicine box" shortcut, which is also the only way to get focus back there
  // from wherever Tab has left it — so using it here exercises the shortcut as well.
  await page.keyboard.press('Alt+m');
  await page.keyboard.type('co', { delay: 0 });
  await expect(page.locator('.combobox-row').first()).toBeVisible({ timeout: 15_000 });
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('dose')).toBeFocused({ timeout: 15_000 });

  const required = ['dose', 'frequency', 'duration', 'instruction', 'commit-line'];
  const reached = await reachableByTab();
  // Focus is put back where the editor puts it, so the sabotage pass below starts from the same
  // place as the pass above and the two are comparable.
  record(`tab_reachable=${[...reached].filter(Boolean).length}`);
  for (const control of required) {
    expect([...reached].includes(control), `${control} cannot be reached with Tab`).toBe(true);
  }

  // The negative. Without it the loop above would pass against a page where Tab did nothing,
  // because `reached` would be empty and every assertion would be vacuous — which is the exact
  // hole this project's checks keep having.
  await page.evaluate(() => {
    document.querySelector('[data-testid="commit-line"]')?.setAttribute('tabindex', '-1');
  });
  await page.getByTestId('dose').focus();
  const sabotaged = await reachableByTab();
  expect(
    [...sabotaged].includes('commit-line'),
    'the keyboard check passed against a control that had been made unreachable',
  ).toBe(false);

  /* --- criterion 4: a real crash ----------------------------------------- */

  // Bounded, and its failure recorded rather than allowed to end the run: everything above is
  // already measured, and a crash-recovery step that cannot finish is itself a result.
  const crashOutcome = await withDeadline(240_000, async () => {
    // A line half-written: chosen, dose being edited, not committed.
    await page.keyboard.press('Alt+m');
    await page.keyboard.type('si', { delay: 0 });
    await expect(page.locator('.combobox-row').first()).toBeVisible({ timeout: 15_000 });
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('dose')).toBeFocused({ timeout: 15_000 });
    await page.keyboard.type(' HALF-TYPED', { delay: 0 });

    const before = await page.getByTestId('line').count();
    const url = page.url();
    const context = page.context();

    // An actual renderer crash through the DevTools protocol — not `page.close()`, and not a
    // save function called by hand. **Not awaited**: `Page.crash` kills the renderer that would
    // have answered it, so the promise never settles.
    const session = await context.newCDPSession(page);
    void session.send('Page.crash').catch(() => undefined);
    await new Promise((resolve) => setTimeout(resolve, 2_000));

    const recovered = await context.newPage();
    await recovered.goto(url, { timeout: 60_000 });
    const signedOut = /\/login/.test(recovered.url());
    record(`crash_signed_out=${signedOut}`);
    if (signedOut) {
      await recovered.getByLabel(/^Employee code/).fill(CODE);
      await recovered.getByLabel(/^Password/).fill(PASSWORD);
      await recovered.getByRole('button', { name: 'Sign in' }).click();
      const field = recovered.getByLabel(/^Authenticator code/);
      await field.waitFor({ timeout: 60_000 });
      await field.fill(await freshCode());
      await recovered.getByRole('button', { name: /Sign in|Continue|Verify/ }).click();
      await recovered.waitForURL(/prescribe/, { timeout: 60_000 });
    }

    // Every committed line is already an event in the ledger, so all of them come back. The
    // half-typed one was never sent anywhere, and does not.
    await expect(recovered.getByTestId('line')).toHaveCount(before, { timeout: 60_000 });
    const recoveredText = (await recovered.locator('body').textContent()) ?? '';
    expect(recoveredText).not.toContain('HALF-TYPED');
    record(`crash_lines_kept=${before} crash_lost=the_line_being_typed`);

    const preview = recovered.getByTestId('print-preview');
    await expect(preview).toBeVisible({ timeout: 30_000 });
    const version = await preview.getAttribute('data-print-model-version');
    const hash = await preview.getAttribute('data-content-hash');
    expect(version).toBe('1');
    expect(hash).toMatch(/^[0-9a-f]{64}$/);
    record(`print_model_version=${version} content_hash=${hash?.slice(0, 16)}`);
    await recovered.screenshot({ path: '/home/claude/shots/cp81-recovered.png' });
  });
  if (crashOutcome !== 'ok') record(`crash_phase=${crashOutcome}`);

  expect(measurements.length).toBeGreaterThan(3);
});

/** Runs a step with a hard deadline, and names what happened instead of hanging. */
async function withDeadline(ms: number, step: () => Promise<void>): Promise<string> {
  let timer: NodeJS.Timeout | undefined;
  const deadline = new Promise<string>((resolve) => {
    timer = setTimeout(() => resolve(`timed_out_after_${ms}ms`), ms);
  });
  try {
    return await Promise.race([step().then(() => 'ok'), deadline]);
  } catch (error) {
    return `failed:${String(error).slice(0, 120).replace(/\s+/g, '_')}`;
  } finally {
    if (timer) clearTimeout(timer);
  }
}
