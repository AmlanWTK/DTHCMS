import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, API_BASE_URL, NetworkError } from '@/lib/api';
import {
  acknowledgeAlert,
  byUrgency,
  hasEscalated,
  listEscalation,
  listOpenAlerts,
  listPatientAlerts,
  listRules,
  noteAcceptable,
  readAlert,
  stillOpen,
  type CriticalAlert,
} from '@/features/alerts/api/alerts';

/**
 * The client half of critical values (CP50).
 *
 * Most of this file is ordinary: a path, an envelope unwrapped, a refusal that arrives as
 * an ApiError rather than as an empty list. Three things are not ordinary, and they are the
 * reason the file exists.
 *
 *  - **A 409 is not a failure.** Two clinicians reaching for the same alert is the system
 *    working, and the server attaches the alert so the second one can be told who has it and
 *    what they said they were doing. Passing that through the usual error path would throw
 *    away the only fact the moment needs, and the screen would say "something went wrong"
 *    about a situation in which nothing did.
 *  - **The write carries the forgery guard and a fresh idempotency key**, and two
 *    acknowledgements must not share one. A replayed key is answered from the store, which
 *    looks like success and leaves an alert unacknowledged with its escalation still running.
 *  - **The board's order is the server's facts, not a clinical opinion.** `byUrgency` sorts
 *    by how far the escalation has travelled and how long the alert has waited. It must never
 *    decide that one code is worse than another; the rules table is the only opinion here.
 */

function server(routes: Record<string, (request: Request) => Response>): Request[] {
  const seen: Request[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = new Request(input, init);
      seen.push(request);
      const key = `${request.method} ${new URL(request.url).pathname}`;
      const handler = routes[key];
      if (!handler) throw new Error(`no route for ${key}`);
      return handler(request);
    }),
  );
  return seen;
}

function respond(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_alerts_1' },
  });
}

const UUID_V7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

const ALERT_ID = '0190a8f2-0000-7000-8000-0000000000c1';
const PATIENT_ID = '0190a8f2-0000-7000-8000-0000000000p1';

function alert(over: Partial<CriticalAlert> = {}): CriticalAlert {
  return {
    id: ALERT_ID,
    patient_id: PATIENT_ID,
    observation_id: '0190a8f2-0000-7000-8000-0000000000o1',
    code: 'SPO2',
    display_en: 'Oxygen saturation',
    display_bn: 'অক্সিজেন সম্পৃক্তি',
    value: 88,
    unit: '%',
    breached: 'low',
    threshold: 92,
    action_en: 'Give oxygen and call the consultant now.',
    action_bn: 'অক্সিজেন দিন এবং এখনই পরামর্শকে ডাকুন।',
    raised_at: '2026-09-14T04:42:00Z',
    raised_by: '0190a8f2-0000-7000-8000-0000000000u1',
    station_code: 'STN_EXAMINATION',
    status: 'OPEN',
    escalation_step: 1,
    delivered: true,
    recipients: 3,
    ...over,
  };
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('reading what nobody has answered', () => {
  it('asks the facility endpoint and hands back the alerts, not the envelope', async () => {
    // A caller given `{alerts: …}` renders an empty board with no error anywhere, which is
    // the one thing this screen must never do.
    const seen = server({ 'GET /v1/alerts': () => respond({ alerts: [alert()] }) });

    const open = await listOpenAlerts();

    expect(seen[0]!.url.startsWith(API_BASE_URL)).toBe(true);
    expect(new URL(seen[0]!.url).pathname).toBe('/v1/alerts');
    expect(open).toHaveLength(1);
    expect(open[0]!.code).toBe('SPO2');
  });

  it('asks for no limit at all rather than an empty one', async () => {
    // `?limit=` is not "no limit" to a server that parses it; the contract's default is the
    // whole board, and a list long enough to need paging is a clinic in trouble.
    const seen = server({ 'GET /v1/alerts': () => respond({ alerts: [] }) });

    await listOpenAlerts();

    expect(new URL(seen[0]!.url).search).toBe('');
  });

  it('names a limit when one is asked for', async () => {
    const seen = server({ 'GET /v1/alerts': () => respond({ alerts: [] }) });

    await listOpenAlerts(20);

    expect(new URL(seen[0]!.url).searchParams.get('limit')).toBe('20');
  });

  it('reads one alert out of its envelope too', async () => {
    server({ [`GET /v1/alerts/${ALERT_ID}`]: () => respond({ alert: alert() }) });

    await expect(readAlert(ALERT_ID)).resolves.toMatchObject({ id: ALERT_ID });
  });

  it('raises the shared ApiError when the role may not read alerts', async () => {
    // The board draws a refusal and an unreachable server differently, and it picks between
    // them by the class thrown here.
    server({
      'GET /v1/alerts': () =>
        respond(
          {
            error: {
              code: 'FORBIDDEN',
              kind: 'permission',
              message: 'This role cannot read alerts.',
              message_bn: 'এই ভূমিকা সতর্কতা দেখতে পারে না।',
            },
          },
          403,
        ),
    });

    const error = await listOpenAlerts().catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('FORBIDDEN');
    expect((error as ApiError).messageBN).toBe('এই ভূমিকা সতর্কতা দেখতে পারে না।');
    expect((error as ApiError).correlationID).toBe('req_alerts_1');
  });

  it('raises a NetworkError when the request never arrives', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));

    await expect(listOpenAlerts()).rejects.toBeInstanceOf(NetworkError);
  });
});

describe("reading one patient's history", () => {
  it('keeps the answered ones, because the episode still happened', async () => {
    // An alert that vanished once somebody acknowledged it would make the record say a
    // saturation of 88 was never raised.
    server({
      [`GET /v1/patients/${PATIENT_ID}/alerts`]: () =>
        respond({
          alerts: [
            alert(),
            alert({ id: 'a2', status: 'ACKNOWLEDGED', acknowledgement: 'gave oxygen' }),
          ],
        }),
    });

    const history = await listPatientAlerts(PATIENT_ID);

    expect(history).toHaveLength(2);
    expect(history.filter(stillOpen)).toHaveLength(1);
  });

  it('escapes the patient id rather than pasting it into the path', async () => {
    const seen = server({ 'GET /v1/patients/p%2F1/alerts': () => respond({ alerts: [] }) });

    await listPatientAlerts('p/1');

    expect(seen).toHaveLength(1);
  });
});

describe('reading the reference data', () => {
  it('hands the thresholds back in the order the server resolved them', async () => {
    // Most specific first, and never re-ranked here. A client that sorted these could sound
    // an alarm the server did not raise, or stay quiet when it did.
    server({
      'GET /v1/alerts/rules': () =>
        respond({
          rules: [
            {
              id: 'r1',
              code: 'SPO2',
              min_age_years: 0,
              max_age_years: 5,
              low: 94,
              approved: false,
            },
            { id: 'r2', code: 'SPO2', low: 92, approved: false },
          ],
        }),
    });

    const rules = await listRules();

    expect(rules.map((rule) => rule.id)).toEqual(['r1', 'r2']);
  });

  it('hands back an escalation chain whose last step notifies nobody', async () => {
    // Deliberate, not a gap: the final link is a person walking to another person. A chain
    // whose last link is another notification has no end.
    server({
      'GET /v1/alerts/escalation': () =>
        respond({
          steps: [
            { step: 1, after_seconds: 0, notify_role: 'PHYSICIAN', approved: false },
            { step: 2, after_seconds: 120, notify_role: 'JUNIOR_DOCTOR', approved: false },
            { step: 3, after_seconds: 300, approved: false },
          ],
        }),
    });

    const steps = await listEscalation();

    expect(steps).toHaveLength(3);
    expect(steps[2]!.notify_role).toBeUndefined();
  });
});

describe('taking an alert', () => {
  it('sends the note to the alert it belongs to', async () => {
    const seen = server({
      [`POST /v1/alerts/${ALERT_ID}/acknowledge`]: () =>
        respond({ alert: alert({ status: 'ACKNOWLEDGED' }) }),
    });

    const result = await acknowledgeAlert(ALERT_ID, 'Giving oxygen, reviewing in 5 minutes.');

    const body = (await seen[0]!.json()) as { note: string };
    expect(body.note).toBe('Giving oxygen, reviewing in 5 minutes.');
    expect(result.outcome).toBe('acknowledged');
    expect(result.alert?.status).toBe('ACKNOWLEDGED');
  });

  it('carries the forgery guard and a fresh idempotency key', async () => {
    const seen = server({
      [`POST /v1/alerts/${ALERT_ID}/acknowledge`]: () => respond({ alert: alert() }),
    });

    await acknowledgeAlert(ALERT_ID, 'giving oxygen');

    expect(seen[0]!.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(seen[0]!.headers.get('Idempotency-Key')).toMatch(UUID_V7);
  });

  it('gives two acknowledgements two keys, so the second one is not answered from the store', async () => {
    // A replayed key returns the first attempt's response. That looks like success and
    // leaves an alert unacknowledged with its escalation still running.
    const seen = server({
      [`POST /v1/alerts/${ALERT_ID}/acknowledge`]: () => respond({ alert: alert() }),
    });

    await acknowledgeAlert(ALERT_ID, 'first');
    await acknowledgeAlert(ALERT_ID, 'second');

    expect(seen[0]!.headers.get('Idempotency-Key')).not.toBe(
      seen[1]!.headers.get('Idempotency-Key'),
    );
  });

  it('reports a second acknowledgement as taken rather than as a failure', async () => {
    // The whole reason this call does not go through `unwrap`. Two clinicians reaching for
    // the same alert is the system working, and the screen has to be able to say so.
    server({
      [`POST /v1/alerts/${ALERT_ID}/acknowledge`]: () =>
        respond(
          {
            error: { code: 'ALERT_ALREADY_ACKNOWLEDGED', kind: 'conflict' },
            alert: alert({
              status: 'ACKNOWLEDGED',
              acknowledged_by: '0190a8f2-0000-7000-8000-0000000000u2',
              acknowledged_at: '2026-09-14T04:44:00Z',
              acknowledgement: 'Oxygen started, consultant called.',
            }),
          },
          409,
        ),
    });

    const result = await acknowledgeAlert(ALERT_ID, 'giving oxygen');

    expect(result.outcome).toBe('taken');
    expect(result.alert?.acknowledgement).toBe('Oxygen started, consultant called.');
  });

  it('still reports a conflict when the server attached no alert to it', async () => {
    // Degrades to "somebody else has it" rather than to a thrown error on a path where
    // something has already gone differently than expected.
    server({
      [`POST /v1/alerts/${ALERT_ID}/acknowledge`]: () =>
        respond({ error: { code: 'ALERT_ALREADY_ACKNOWLEDGED', kind: 'conflict' } }, 409),
    });

    const result = await acknowledgeAlert(ALERT_ID, 'giving oxygen');

    expect(result).toEqual({ outcome: 'taken', alert: null });
  });

  it('raises an ApiError when the server refuses the note', async () => {
    // A 422 is a genuine failure: nothing was recorded and the escalation is still running,
    // which is the opposite of a 409 and must not be reported the same way.
    server({
      [`POST /v1/alerts/${ALERT_ID}/acknowledge`]: () =>
        respond(
          {
            error: {
              code: 'VALIDATION_FAILED',
              kind: 'validation',
              message: 'The note is too short.',
              message_bn: 'নোটটি খুব ছোট।',
              fields: { note: 'at least 3 characters' },
            },
          },
          422,
        ),
    });

    const error = await acknowledgeAlert(ALERT_ID, 'ok').catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(422);
    expect((error as ApiError).fields.note).toBe('at least 3 characters');
  });

  it('raises a NetworkError when the acknowledgement never leaves the building', async () => {
    // "You are offline" and "the clinic refused this" are different instructions, and here
    // the first one means: nobody has been told, so go and find somebody.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));

    await expect(acknowledgeAlert(ALERT_ID, 'giving oxygen')).rejects.toBeInstanceOf(NetworkError);
  });
});

describe('what the board may decide for itself', () => {
  it('puts an alert nobody answered above one that was raised more recently', async () => {
    const fresh = alert({ id: 'fresh', raised_at: '2026-09-14T05:00:00Z', escalation_step: 1 });
    const unanswered = alert({
      id: 'unanswered',
      raised_at: '2026-09-14T04:40:00Z',
      escalation_step: 3,
    });

    expect(byUrgency([fresh, unanswered]).map((a) => a.id)).toEqual(['unanswered', 'fresh']);
  });

  it('puts the longest wait first among alerts at the same step', async () => {
    const older = alert({ id: 'older', raised_at: '2026-09-14T04:30:00Z' });
    const newer = alert({ id: 'newer', raised_at: '2026-09-14T04:50:00Z' });

    expect(byUrgency([newer, older]).map((a) => a.id)).toEqual(['older', 'newer']);
  });

  it('does not reorder the list it was given', async () => {
    // The board polls; a sort in place would shuffle the cached array under other readers.
    const list = [alert({ id: 'a', escalation_step: 1 }), alert({ id: 'b', escalation_step: 2 })];

    byUrgency(list);

    expect(list.map((a) => a.id)).toEqual(['a', 'b']);
  });

  it('calls an alert escalated only once the chain has moved past its first step', async () => {
    expect(hasEscalated(alert({ escalation_step: 1 }))).toBe(false);
    expect(hasEscalated(alert({ escalation_step: 2 }))).toBe(true);
  });

  it('refuses a note that says nothing, before the server has to', async () => {
    // A form that lets somebody type "ok" and then shows them a validation error has taught
    // them nothing and cost them a second at the worst possible moment.
    expect(noteAcceptable('')).toBe(false);
    expect(noteAcceptable('   ')).toBe(false);
    expect(noteAcceptable('ok')).toBe(false);
    expect(noteAcceptable('Giving oral glucose, rechecking in 15.')).toBe(true);
  });
});
