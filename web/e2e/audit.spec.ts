import { expect, test } from './fixtures';

/**
 * CP22 in a browser: the audit viewer with its sentences in both languages, the chain
 * verification, the signed export, the administrator alarm, and the break-glass door.
 */

const json = (body: unknown, status = 200) => ({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
});

const KINDS = [
  { kind: 'break_glass.opened', label_en: 'Break-glass access', label_bn: 'জরুরি প্রবেশাধিকার' },
  { kind: 'role.granted', label_en: 'Role granted', label_bn: 'ভূমিকা প্রদান' },
  { kind: 'session.login', label_en: 'Signed in', label_bn: 'সাইন ইন' },
];

const EVENTS = [
  {
    seq: 3,
    kind: 'break_glass.opened',
    label_en: 'Break-glass access',
    label_bn: 'জরুরি প্রবেশাধিকার',
    recorded_at: '2026-09-03T04:42:00Z',
    actor: { id: '0190a8f2-0000-7000-8000-0000000000c1', code: 'JD01' },
    actor_role: 'JUNIOR_DOCTOR',
    target: null,
    patient_id: '0190a8f2-0000-7000-8000-0000000000b1',
    device_id: null,
    reason: 'unconscious patient, regular physician away',
    details: {},
    sentence_en:
      'JD01 broke the glass for patient 0190a8f2-0000-7000-8000-0000000000b1 until 14:42: unconscious patient, regular physician away',
    sentence_bn:
      'JD01 14:42 পর্যন্ত patient 0190a8f2-0000-7000-8000-0000000000b1-এর জন্য জরুরি প্রবেশাধিকার নিয়েছেন: unconscious patient, regular physician away',
    hash: 'ab'.repeat(32),
  },
  {
    seq: 2,
    kind: 'role.granted',
    label_en: 'Role granted',
    label_bn: 'ভূমিকা প্রদান',
    recorded_at: '2026-09-03T04:30:00Z',
    actor: { id: '0190a8f2-0000-7000-8000-00000000000a', code: 'E001' },
    actor_role: 'ADMIN',
    target: { id: '0190a8f2-0000-7000-8000-0000000000b2', code: 'N006' },
    patient_id: null,
    device_id: null,
    reason: '',
    details: { role: 'NUTRITIONIST' },
    sentence_en: 'E001 granted NUTRITIONIST to N006',
    sentence_bn: 'E001 N006-কে NUTRITIONIST ভূমিকা দিয়েছেন',
    hash: 'cd'.repeat(32),
  },
  {
    seq: 1,
    kind: 'session.login',
    label_en: 'Signed in',
    label_bn: 'সাইন ইন',
    recorded_at: '2026-09-03T03:05:00Z',
    actor: { id: '0190a8f2-0000-7000-8000-00000000000a', code: 'E001' },
    actor_role: '',
    target: null,
    patient_id: null,
    device_id: null,
    reason: '',
    details: {},
    sentence_en: 'E001 signed in',
    sentence_bn: 'E001 সাইন ইন করেছেন',
    hash: 'ef'.repeat(32),
  },
];

async function actAsAdmin(page: import('@playwright/test').Page) {
  await page.getByLabel('Acting as').selectOption('ADMIN');
}

test.describe('CP22: the audit trail', () => {
  test('shows sentences, narrows by kind, and verifies the chain', async ({ signedIn: page }) => {
    await page.route('**/v1/audit/kinds', (route) => route.fulfill(json({ kinds: KINDS })));
    await page.route('**/v1/audit/events**', (route) => {
      const url = new URL(route.request().url());
      const kind = url.searchParams.get('kind');
      const events = kind ? EVENTS.filter((e) => e.kind === kind) : EVENTS;
      return route.fulfill(json({ events, next_before: null }));
    });
    await page.route('**/v1/audit/chain', (route) =>
      route.fulfill(
        json({ ok: true, checked: 3, head_seq: 3, broken_at: null, problem: '', strays: 0 }),
      ),
    );

    await page.route('**/v1/audit/break-glass', (route) =>
      route.fulfill(
        json({
          accesses: [
            {
              id: '0190a8f2-0000-7000-8000-0000000000d1',
              user_id: '0190a8f2-0000-7000-8000-0000000000c1',
              active_role: 'JUNIOR_DOCTOR',
              scope_kind: 'patient',
              scope_ref: '0190a8f2-0000-7000-8000-0000000000b1',
              justification: 'unconscious patient, regular physician away',
              granted_at: '2026-09-03T04:42:00Z',
              expires_at: '2026-09-03T08:42:00Z',
              ended_at: null,
              end_reason: '',
              acknowledged_at: null,
              audit_seq: 3,
            },
          ],
        }),
      ),
    );

    await page.goto('/admin/audit');
    await actAsAdmin(page);
    await expect(page.getByRole('heading', { name: 'Audit trail', level: 1 })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Open emergency accesses' })).toBeVisible();
    await expect(page.getByText('acting as JUNIOR_DOCTOR · until 3 Sept, 14:42')).toBeVisible();
    await expect(page.getByText('E001 granted NUTRITIONIST to N006')).toBeVisible();
    await expect(page.getByText('E001 signed in')).toBeVisible();
    await expect(page.getByText('acting as JUNIOR_DOCTOR', { exact: true })).toBeVisible();

    await page.getByLabel(/^Kind/).selectOption('break_glass.opened');
    await page.getByRole('button', { name: 'Apply' }).click();
    await expect(page.getByText('E001 signed in')).toHaveCount(0);
    await expect(page.getByText(/JD01 broke the glass/)).toBeVisible();

    await page.getByRole('button', { name: 'Verify the chain' }).click();
    await expect(page.getByText('3 entries, chain intact')).toBeVisible();
  });

  test('a broken chain is reported at the row', async ({ signedIn: page }) => {
    await page.route('**/v1/audit/kinds', (route) => route.fulfill(json({ kinds: KINDS })));
    await page.route('**/v1/audit/events**', (route) =>
      route.fulfill(json({ events: EVENTS, next_before: null })),
    );
    await page.route('**/v1/audit/chain', (route) =>
      route.fulfill(
        json({
          ok: false,
          checked: 1,
          head_seq: 1,
          broken_at: 2,
          problem: 'row 2 does not hash to what it claims',
          strays: 0,
        }),
      ),
    );
    await page.goto('/admin/audit');
    await actAsAdmin(page);
    await page.getByRole('button', { name: 'Verify the chain' }).click();
    await expect(page.getByText('The chain is broken at entry #2')).toBeVisible();
    await expect(page.getByText('row 2 does not hash to what it claims')).toBeVisible();
  });

  test('exports the trail as a signed PDF with its signature beside it', async ({
    signedIn: page,
  }) => {
    await page.route('**/v1/audit/kinds', (route) => route.fulfill(json({ kinds: KINDS })));
    await page.route('**/v1/audit/events**', (route) =>
      route.fulfill(json({ events: EVENTS, next_before: null })),
    );
    await page.route('**/v1/audit/export**', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/pdf',
        headers: {
          'Content-Disposition': 'attachment; filename="dthcms-audit-20260903-1042.pdf"',
          'X-Audit-Signature': 'c2ln',
          'X-Audit-Key-Id': 'audit-local-1',
          'X-Audit-Digest': 'ZGln',
          'Access-Control-Expose-Headers':
            'X-Audit-Signature, X-Audit-Key-Id, X-Audit-Digest, Content-Disposition',
        },
        body: '%PDF-1.4\n%%EOF\n',
      }),
    );
    await page.goto('/admin/audit');
    await actAsAdmin(page);

    const downloads: string[] = [];
    page.on('download', (d) => downloads.push(d.suggestedFilename()));
    await page.getByRole('button', { name: 'Export signed PDF' }).click();
    await expect(
      page.getByText(/Saved dthcms-audit-20260903-1042.pdf and its signature/),
    ).toBeVisible();
    await expect
      .poll(() => downloads.sort())
      .toEqual(['dthcms-audit-20260903-1042.pdf', 'dthcms-audit-20260903-1042.pdf.sig.json']);
  });

  test('an administrator sees the alarm on any screen and can acknowledge it', async ({
    signedIn: page,
  }) => {
    let open = [
      {
        id: '0190a8f2-0000-7000-8000-0000000000a1',
        kind: 'break_glass',
        severity: 'high',
        message_en: 'JD01 broke the glass for patient 0190a8f2-… until 14:42: unconscious patient',
        message_bn: 'JD01 14:42 পর্যন্ত জরুরি প্রবেশাধিকার নিয়েছেন',
        reference: {},
        audit_seq: 3,
        created_at: '2026-09-03T04:42:00Z',
      },
    ];
    await page.route('**/v1/audit/alerts', (route) => route.fulfill(json({ alerts: open })));
    await page.route('**/v1/audit/alerts/*/acknowledge', (route) => {
      const acked = open[0];
      open = [];
      return route.fulfill(json(acked));
    });
    await page.route('**/v1/devices', (route) => route.fulfill(json({ devices: [] })));

    await page.goto('/admin/devices');
    await actAsAdmin(page);
    const region = page.getByRole('region', { name: 'Administrator alerts' });
    await expect(region.getByText('Someone has broken the glass')).toBeVisible();
    await expect(region.getByText(/JD01 broke the glass/)).toBeVisible();
    await region.getByRole('button', { name: 'I have seen this' }).click();
    await expect(region).toHaveCount(0);
  });

  test('breaking the glass needs twenty typed characters and a step-up', async ({
    signedIn: page,
  }) => {
    let mine: unknown[] = [];
    await page.route('**/v1/audit/break-glass/mine', (route) =>
      route.fulfill(json({ accesses: mine })),
    );
    await page.route('**/v1/auth/step-up', (route) => {
      const body = route.request().postDataJSON() as { purpose: string };
      expect(body.purpose).toBe('break_glass');
      return route.fulfill(json({ step_up_token: 'su-bg', purpose: body.purpose, expires_at: '' }));
    });
    await page.route('**/v1/audit/break-glass', (route) => {
      expect(route.request().headers()['x-step-up-token']).toBe('su-bg');
      const body = route.request().postDataJSON() as {
        scope_kind: string;
        scope_ref: string;
        justification: string;
        hours: number;
      };
      expect(body.scope_kind).toBe('patient');
      expect(body.hours).toBe(4);
      const access = {
        id: '0190a8f2-0000-7000-8000-0000000000d1',
        user_id: '0190a8f2-0000-7000-8000-00000000000a',
        active_role: 'PHYSICIAN',
        scope_kind: body.scope_kind,
        scope_ref: body.scope_ref,
        justification: body.justification,
        granted_at: '2026-09-03T04:42:00Z',
        expires_at: '2026-09-03T08:42:00Z',
        ended_at: null,
        end_reason: '',
        acknowledged_at: null,
        audit_seq: 4,
      };
      mine = [access];
      return route.fulfill(json(access, 201));
    });

    await page.goto('/break-glass');
    await expect(page.getByRole('heading', { name: 'Emergency access', level: 1 })).toBeVisible();
    await page.getByLabel(/^Patient id/).fill('0190a8f2-0000-7000-8000-0000000000b1');
    await page.getByLabel(/^Why, in your words/).fill('urgent');
    await expect(page.getByText('14 more characters needed')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Break the glass' })).toBeDisabled();

    await page
      .getByLabel(/^Why, in your words/)
      .fill('Unconscious patient in room 2; regular physician unreachable.');
    await page.getByRole('button', { name: 'Break the glass' }).click();

    const stepUp = page.getByRole('dialog', { name: /Confirm it is you/ });
    await expect(stepUp.getByText('Open emergency access')).toBeVisible();
    await stepUp.getByLabel(/^Authenticator code/).fill('123456');
    await stepUp.getByRole('button', { name: 'Confirm' }).click();

    await expect(page.getByText(/Emergency access is open until/)).toBeVisible();
    await expect(page.getByText('Patient 0190a8f2-0000-7000-8000-0000000000b1')).toBeVisible();
    await expect(page.getByText('Not yet seen')).toBeVisible();
  });

  test('is in Bangla when the interface is', async ({ bangla: page }) => {
    await page.route('**/v1/audit/kinds', (route) => route.fulfill(json({ kinds: KINDS })));
    await page.route('**/v1/audit/events**', (route) =>
      route.fulfill(json({ events: EVENTS, next_before: null })),
    );
    await page.goto('/admin/audit');
    await page.getByLabel('যে ভূমিকায় কাজ করছেন').selectOption('ADMIN');
    await expect(page.getByRole('heading', { level: 1, name: 'অডিট ট্রেইল' })).toBeVisible();
    await expect(page.getByText('E001 N006-কে NUTRITIONIST ভূমিকা দিয়েছেন')).toBeVisible();
  });
});
