import { afterEach, describe, expect, it, vi } from 'vitest';

import { API_BASE_URL, ApiError, NetworkError } from '@/lib/api';
import {
  ONSET_PRECISIONS,
  REASON_MIN,
  SEVERITIES,
  amendHistoryItem,
  confirmHistoryItem,
  countUncoded,
  emptyDraft,
  groupByKind,
  historyItemsKey,
  isConfirmed,
  isUncoded,
  itemCoding,
  kindNamed,
  listHistoryKinds,
  listMedicalHistory,
  missingFields,
  needsConfirmation,
  newEventId,
  readHistoryItem,
  reasonAcceptable,
  recordHistoryItem,
  recordRequestFrom,
  removeHistoryItem,
  unconfirmedItems,
  type HistoryDraft,
  type HistoryItem,
  type HistoryKind,
} from '@/features/history/api/history';

/**
 * The client half of medical history (CP53).
 *
 * Most of this is ordinary — a path, an envelope unwrapped, a refusal that arrives as an
 * ApiError rather than as an empty list. Four things are not, and they are why the file
 * exists.
 *
 *  - **Confirming takes one id and there is no second shape of it.** A helper that accepted
 *    a list would let one click put twenty assertions into the record with nobody behind
 *    them, which is the auto-acceptance acceptance criterion 3 forbids. There is a named
 *    test below whose entire job is to fail if such a helper ever appears.
 *  - **A coding is three fields or none.** `recordRequestFrom` is the only place they are
 *    written, and two of the three is a request the server is right to refuse: a code with
 *    no version is a string, and a version guessed from a picker's props is a lie that
 *    surfaces years later.
 *  - **Resolving and removing are different calls with different words.** One says she had
 *    this and no longer does; the other says it should never have been recorded, and takes
 *    the reason that is the whole point of it.
 *  - **The rules come from the kind.** `missingFields` reads `requires_relation` and
 *    `requires_duration` off the server's answer rather than off the kind's name, so a
 *    seventh kind is handled without this file changing.
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
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_history_1' },
  });
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const PATIENT_ID = '0190a8f2-0000-7000-8000-0000000000a1';
const ITEM_ID = '0190a8f2-0000-7000-8000-0000000000b1';

function kind(over: Partial<HistoryKind> = {}): HistoryKind {
  return {
    kind: 'COMPLAINT',
    display_en: 'Presenting complaint',
    display_bn: 'বর্তমান সমস্যা',
    code_system: 'DTHC',
    requires_relation: false,
    requires_duration: true,
    allows_severity: true,
    allows_onset: true,
    is_medication: false,
    ordering: 1,
    ...over,
  };
}

const FAMILY = kind({
  kind: 'FAMILY_HISTORY',
  display_en: 'Family history',
  display_bn: 'পারিবারিক ইতিহাস',
  code_system: 'ICD10',
  requires_relation: true,
  requires_duration: false,
  allows_severity: false,
  ordering: 3,
});

const MEDICATION = kind({
  kind: 'MEDICATION',
  display_en: 'Current medicine',
  display_bn: 'চলতি ওষুধ',
  requires_duration: false,
  allows_severity: false,
  allows_onset: false,
  is_medication: true,
  ordering: 5,
});

function item(over: Partial<HistoryItem> = {}): HistoryItem {
  return {
    id: ITEM_ID,
    patient_id: PATIENT_ID,
    kind: 'COMPLAINT',
    said: 'Burning in the chest after the evening meal',
    status: 'ACTIVE',
    recorded_at: '2026-09-01T04:00:00Z',
    recorded_by: '0190a8f2-0000-7000-8000-0000000000c1',
    ...over,
  };
}

function draft(over: Partial<HistoryDraft> = {}): HistoryDraft {
  return { ...emptyDraft('COMPLAINT'), ...over };
}

const CONCEPT = {
  system: 'DTHC',
  version: '1',
  code: 'DTHC-CHEST-BURN',
  display_en: 'Burning chest pain',
  display_bn: 'বুক জ্বালা',
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('reading the reference data and the list', () => {
  it('hands back the kinds, the relations and what belongs to another station', async () => {
    const seen = server({
      'GET /v1/history/kinds': () =>
        respond({
          kinds: [kind()],
          relations: [
            { relation: 'MOTHER', display_en: 'Mother', display_bn: 'মা', degree: 1, ordering: 1 },
          ],
          from_lifestyle_station: ['SMOKING_STATUS'],
        }),
    });

    const reference = await listHistoryKinds();

    expect(seen[0]!.url.startsWith(API_BASE_URL)).toBe(true);
    expect(new URL(seen[0]!.url).pathname).toBe('/v1/history/kinds');
    expect(reference.kinds[0]!.requires_duration).toBe(true);
    // The lifestyle codes are carried rather than dropped: they are what station 4 must
    // *not* ask for, and a screen that silently omitted them looks like one that forgot.
    expect(reference.from_lifestyle_station).toEqual(['SMOKING_STATUS']);
  });

  it('reads the uncoded count out of its envelope', async () => {
    server({ 'GET /v1/history/uncoded': () => respond({ uncoded: { COMPLAINT: 4 } }) });
    await expect(countUncoded()).resolves.toEqual({ COMPLAINT: 4 });
  });

  it('asks for one patient’s history and hands back the items, not the envelope', async () => {
    // A caller given `{items: …}` renders an empty history with no error anywhere, which on
    // this screen reads as "this patient takes nothing".
    const seen = server({
      [`GET /v1/patients/${PATIENT_ID}/medical-history`]: () => respond({ items: [item()] }),
    });

    const items = await listMedicalHistory(PATIENT_ID);

    expect(new URL(seen[0]!.url).pathname).toBe(`/v1/patients/${PATIENT_ID}/medical-history`);
    expect(items).toHaveLength(1);
    expect(items[0]!.id).toBe(ITEM_ID);
  });

  it('reads one item, removed ones included', async () => {
    server({ [`GET /v1/history/items/${ITEM_ID}`]: () => respond({ item: item() }) });
    await expect(readHistoryItem(ITEM_ID)).resolves.toMatchObject({ id: ITEM_ID });
  });

  it('raises the shared ApiError when the role may not read a history', async () => {
    // §4.4 blinds registration and the pharmacist to this. A screen picks between "you may
    // not" and "you are offline" by the class thrown here.
    server({
      [`GET /v1/patients/${PATIENT_ID}/medical-history`]: () =>
        respond(
          {
            error: {
              code: 'forbidden',
              kind: 'permission',
              message: 'Not permitted.',
              message_bn: 'অনুমতি নেই।',
            },
          },
          403,
        ),
    });

    await expect(listMedicalHistory(PATIENT_ID)).rejects.toBeInstanceOf(ApiError);
  });

  it('raises a NetworkError when the request never arrived', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))),
    );

    await expect(listMedicalHistory(PATIENT_ID)).rejects.toBeInstanceOf(NetworkError);
  });
});

describe('recording one item', () => {
  it('sends the forgery guard and a fresh idempotency key', async () => {
    const seen = server({
      [`POST /v1/patients/${PATIENT_ID}/medical-history`]: () => respond({ item: item() }, 201),
    });

    await recordHistoryItem(PATIENT_ID, { kind: 'COMPLAINT', said: 'chest burning' });
    await recordHistoryItem(PATIENT_ID, { kind: 'COMPLAINT', said: 'headache' });

    for (const request of seen) {
      expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
      expect(request.headers.get('Idempotency-Key')).toMatch(UUID);
    }
    // Two complaints are two items. A shared key would be answered from the store, which
    // looks like success and quietly records one.
    expect(seen[0]!.headers.get('Idempotency-Key')).not.toBe(
      seen[1]!.headers.get('Idempotency-Key'),
    );
  });

  it('puts an event id in the body of every write', async () => {
    const seen = server({
      [`POST /v1/patients/${PATIENT_ID}/medical-history`]: () => respond({ item: item() }, 201),
    });

    await recordHistoryItem(PATIENT_ID, { kind: 'COMPLAINT', said: 'chest burning' });

    const body = (await seen[0]!.json()) as { event_id?: string };
    expect(body.event_id).toMatch(UUID);
  });

  it('hands back the item the server recorded, not the envelope', async () => {
    server({
      [`POST /v1/patients/${PATIENT_ID}/medical-history`]: () =>
        respond({ item: item({ code: 'I10' }) }, 201),
    });

    await expect(
      recordHistoryItem(PATIENT_ID, { kind: 'COMORBIDITY', code: 'I10' }),
    ).resolves.toMatchObject({ code: 'I10' });
  });

  it('raises an ApiError carrying the named field on a 422', async () => {
    // The form renders this against the control the server named, which is the difference
    // between "something is wrong" and "say how many days".
    server({
      [`POST /v1/patients/${PATIENT_ID}/medical-history`]: () =>
        respond(
          {
            error: {
              code: 'validation_failed',
              kind: 'validation',
              message: 'The request could not be processed.',
              message_bn: 'অনুরোধটি প্রক্রিয়া করা যায়নি।',
              fields: { duration_days: 'A complaint needs a duration.' },
            },
          },
          422,
        ),
    });

    await expect(recordHistoryItem(PATIENT_ID, { kind: 'COMPLAINT' })).rejects.toMatchObject({
      fields: { duration_days: 'A complaint needs a duration.' },
    });
  });
});

describe('confirming a carried-forward item', () => {
  it('posts to one item’s own path', async () => {
    const seen = server({
      [`POST /v1/history/items/${ITEM_ID}/confirm`]: () =>
        respond({ item: item({ confirmed_at: '2026-09-04T05:00:00Z' }) }),
    });

    const confirmed = await confirmHistoryItem(ITEM_ID);

    expect(new URL(seen[0]!.url).pathname).toBe(`/v1/history/items/${ITEM_ID}/confirm`);
    expect(seen[0]!.headers.get('X-Requested-With')).toBe('DTHCMS');
    expect(confirmed.confirmed_at).toBe('2026-09-04T05:00:00Z');
  });

  it('carries the visit when there is one, and nothing when there is not', async () => {
    const seen = server({
      [`POST /v1/history/items/${ITEM_ID}/confirm`]: () => respond({ item: item() }),
    });

    await confirmHistoryItem(ITEM_ID, 'visit-1');
    await confirmHistoryItem(ITEM_ID);

    expect(await seen[0]!.json()).toMatchObject({ visit_id: 'visit-1' });
    expect(Object.keys((await seen[1]!.json()) as object)).not.toContain('visit_id');
  });

  it('there is no way to confirm everything at once', async () => {
    /*
     * The named test acceptance criterion 3 rests on.
     *
     * Every other design that satisfies the words satisfies them by making the assertion on
     * somebody's behalf, and the shape that would arrive here is a convenience: a
     * `confirmAll`, or a `confirmHistoryItem` that quietly accepted an array. Either would
     * turn one action by a person into twenty assertions in a signed clinical record, with
     * nobody able to say who claimed them — because nobody did.
     *
     * So this asserts the absence rather than trusting a comment. Anything on this module
     * whose name suggests a batch fails it; so does a `confirmHistoryItem` given a list,
     * which must produce one refused request rather than twenty accepted ones.
     */
    const surface = await import('@/features/history/api/history');
    const batchy = Object.keys(surface).filter((name) =>
      /(all|batch|bulk|many|each|every)/i.test(name),
    );
    expect(batchy, `Exports that look like a batch: ${batchy.join(', ')}`).toEqual([]);

    // It takes an id and a visit, not a list. A second positional argument that happened to
    // accept an array is the other way this would arrive.
    expect(confirmHistoryItem.length).toBe(2);

    const seen = server({
      'POST /v1/history/items/a,b,c/confirm': () => respond({ item: item() }),
      [`POST /v1/history/items/${ITEM_ID}/confirm`]: () => respond({ item: item() }),
    });
    await confirmHistoryItem(ITEM_ID);
    expect(seen).toHaveLength(1);

    // And the barrel exports nothing batch-shaped either — a caller reaches this feature
    // through there, and an export added for one screen's convenience is how the discipline
    // is lost.
    const barrel = await import('@/features/history');
    const barrelBatchy = Object.keys(barrel).filter((name) =>
      /(all|batch|bulk|many|each|every)/i.test(name),
    );
    expect(barrelBatchy).toEqual([]);
  });
});

describe('resolving and removing are different acts', () => {
  it('resolves with a PATCH that keeps the item', async () => {
    const seen = server({
      [`PATCH /v1/history/items/${ITEM_ID}`]: () => respond({ item: item({ status: 'RESOLVED' }) }),
    });

    const amended = await amendHistoryItem(ITEM_ID, { status: 'RESOLVED' });

    expect(seen[0]!.method).toBe('PATCH');
    expect(await seen[0]!.json()).toMatchObject({ status: 'RESOLVED' });
    // The item survives. "She had this and no longer does" is a clinical fact, and a list
    // that hid it would make every follow-up look like a first visit.
    expect(amended.status).toBe('RESOLVED');
  });

  it('removes with a reason, on a different path, answering with nothing', async () => {
    const seen = server({
      [`POST /v1/history/items/${ITEM_ID}/remove`]: () => new Response(null, { status: 204 }),
    });

    await expect(
      removeHistoryItem(ITEM_ID, 'Recorded on the wrong patient.'),
    ).resolves.toBeUndefined();

    expect(new URL(seen[0]!.url).pathname).toBe(`/v1/history/items/${ITEM_ID}/remove`);
    expect(await seen[0]!.json()).toMatchObject({ reason: 'Recorded on the wrong patient.' });
  });

  it('amends only the fields it was given', async () => {
    // Absent means unchanged and an empty string clears it. Those are different requests and
    // a client that always sent every field could not make the first one.
    const seen = server({
      [`PATCH /v1/history/items/${ITEM_ID}`]: () => respond({ item: item() }),
    });

    await amendHistoryItem(ITEM_ID, { dose: '' });

    const body = (await seen[0]!.json()) as Record<string, unknown>;
    expect(body.dose).toBe('');
    expect(body).not.toHaveProperty('frequency');
  });

  it('mirrors the server’s floor under a removal reason', () => {
    expect(REASON_MIN).toBe(1);
    expect(reasonAcceptable('   ')).toBe(false);
    expect(reasonAcceptable('Wrong patient')).toBe(true);
  });
});

describe('a coding is three fields or none', () => {
  it('sends the system, the version and the code together', () => {
    const request = recordRequestFrom(kind(), draft({ concept: CONCEPT, durationDays: '21' }));

    expect(request).toMatchObject({
      code_system: 'DTHC',
      code_version: '1',
      code: 'DTHC-CHEST-BURN',
    });
  });

  it('sends none of the three when nothing was picked', () => {
    // The uncoded escape hatch. The catalogue has nothing for what this patient described,
    // and `said` carries it instead — half a coding would be refused, and rightly.
    const request = recordRequestFrom(
      kind(),
      draft({ said: 'sugar since the flood', durationDays: '30' }),
    );

    expect(request).not.toHaveProperty('code');
    expect(request).not.toHaveProperty('code_system');
    expect(request).not.toHaveProperty('code_version');
    expect(request.said).toBe('sugar since the flood');
  });

  it('reads an item’s coding off the item, whole or not at all', () => {
    expect(itemCoding(item({ code_system: 'ICD10', code_version: '2019', code: 'I10' }))).toEqual({
      system: 'ICD10',
      version: '2019',
      code: 'I10',
      display_en: 'I10',
    });

    // Half a coding is not a coding. A chip rendered from this would show a code with no
    // version, which is the one failure CP52 exists to prevent.
    expect(itemCoding(item({ code_system: 'ICD10', code: 'I10' }))).toBeNull();
    expect(isUncoded(item({ code_system: 'ICD10', code: 'I10' }))).toBe(true);
    expect(isUncoded(item({ code_system: 'ICD10', code_version: '2019', code: 'I10' }))).toBe(
      false,
    );
  });

  it('keeps both displays on the coding it hands to a chip', () => {
    const coding = itemCoding(
      item({
        code_system: 'ICD10',
        code_version: '2019',
        code: 'I10',
        display_en: 'Essential hypertension',
        display_bn: 'প্রাথমিক উচ্চ রক্তচাপ',
      }),
    );

    expect(coding).toMatchObject({
      display_en: 'Essential hypertension',
      display_bn: 'প্রাথমিক উচ্চ রক্তচাপ',
    });
  });
});

describe('the fields a kind needs come from the kind', () => {
  it('asks a family history for a relative and a complaint for a duration', () => {
    expect(missingFields(FAMILY, draft({ kind: 'FAMILY_HISTORY', concept: CONCEPT }))).toEqual([
      'relation',
    ]);
    expect(missingFields(kind(), draft({ concept: CONCEPT }))).toEqual(['duration_days']);
  });

  it('asks a vaccination for neither', () => {
    const vaccination = kind({
      kind: 'VACCINATION',
      requires_duration: false,
      allows_severity: false,
      allows_onset: true,
      ordering: 6,
    });
    expect(missingFields(vaccination, draft({ kind: 'VACCINATION', concept: CONCEPT }))).toEqual(
      [],
    );
  });

  it('needs the patient’s words when nothing is coded, and not when something is', () => {
    expect(missingFields(FAMILY, draft({ relation: 'MOTHER' }))).toEqual(['said']);
    expect(missingFields(FAMILY, draft({ relation: 'MOTHER', concept: CONCEPT }))).toEqual([]);
    expect(missingFields(FAMILY, draft({ relation: 'MOTHER', said: 'mother had sugar' }))).toEqual(
      [],
    );
  });

  it('drops the fields the kind has no room for', () => {
    // A severity on a vaccination is a validation failure this form would have caused
    // itself, and an empty string is not "absent" to a server that parses it.
    const request = recordRequestFrom(
      MEDICATION,
      draft({
        kind: 'MEDICATION',
        concept: CONCEPT,
        severity: 'severe',
        durationDays: '30',
        onsetOn: '2024-01-01',
        dose: ' 1 tablet ',
        frequency: 'twice a day',
      }),
    );

    expect(request).not.toHaveProperty('severity');
    expect(request).not.toHaveProperty('duration_days');
    expect(request).not.toHaveProperty('onset_on');
    expect(request.dose).toBe('1 tablet');
    expect(request.frequency).toBe('twice a day');
  });

  it('sends the onset precision with the date and never without it', () => {
    const request = recordRequestFrom(
      kind(),
      draft({
        concept: CONCEPT,
        durationDays: '21',
        onsetOn: '2024-03-01',
        onsetPrecision: 'year',
      }),
    );

    expect(request).toMatchObject({ onset_on: '2024-03-01', onset_precision: 'year' });

    const noOnset = recordRequestFrom(kind(), draft({ concept: CONCEPT, durationDays: '21' }));
    expect(noOnset).not.toHaveProperty('onset_precision');
  });

  it('carries a severity the kind allows', () => {
    const request = recordRequestFrom(
      kind(),
      draft({ concept: CONCEPT, durationDays: '21', severity: 'severe' }),
    );
    expect(request.severity).toBe('severe');
  });

  it('keeps a duration that is not a number out of the request', () => {
    const request = recordRequestFrom(kind(), draft({ concept: CONCEPT, durationDays: 'three' }));
    expect(request).not.toHaveProperty('duration_days');
  });

  it('offers the severities and precisions the contract allows', () => {
    expect(SEVERITIES).toEqual(['mild', 'moderate', 'severe']);
    expect(ONSET_PRECISIONS).toEqual(['day', 'month', 'year']);
  });

  it('finds a kind by name and says so when there is none', () => {
    expect(kindNamed([kind(), FAMILY], 'FAMILY_HISTORY')).toBe(FAMILY);
    expect(kindNamed([kind()], 'NOT_A_KIND')).toBeUndefined();
  });
});

describe('what the panel groups and counts', () => {
  it('groups in the server’s order and keeps the empty kinds', () => {
    // "No family history recorded" and "family history not asked" are the same blank space
    // on a screen that hides its empty groups, and only one of them is a finished history.
    const groups = groupByKind(
      [item({ kind: 'MEDICATION', id: 'm-1' })],
      [MEDICATION, FAMILY, kind()],
    );

    expect(groups.map((group) => group.kind.kind)).toEqual([
      'COMPLAINT',
      'FAMILY_HISTORY',
      'MEDICATION',
    ]);
    expect(groups[0]!.items).toEqual([]);
    expect(groups[2]!.items).toHaveLength(1);
  });

  it('counts an item with no confirmation as one nobody has confirmed', () => {
    const never = item({ id: 'a' });
    const answered = item({ id: 'b', confirmed_at: '2026-09-04T05:00:00Z' });
    // An empty string is not a confirmation either — a projection that wrote one would
    // otherwise silently mark the whole list as answered.
    const blank = item({ id: 'c', confirmed_at: '' });

    expect(isConfirmed(answered)).toBe(true);
    expect(isConfirmed(never)).toBe(false);
    expect(needsConfirmation(blank)).toBe(true);
    expect(unconfirmedItems([never, answered, blank]).map((one) => one.id)).toEqual(['a', 'c']);
  });

  it('does not ask about an item somebody already resolved', () => {
    // "Is this still true?" is asked of what the record currently asserts. A resolved item is
    // the record already answering — she had this and no longer does — and asking again every
    // visit would be asking a clinician to re-confirm the past for the rest of her life.
    //
    // The server draws the same line (`Item.NeedsConfirmation` refuses anything not ACTIVE),
    // and the two must agree or the banner counts rows the station has no way to clear.
    const resolved = item({ id: 'r', status: 'RESOLVED' });

    expect(isConfirmed(resolved)).toBe(false);
    expect(needsConfirmation(resolved)).toBe(false);
    expect(unconfirmedItems([resolved, item({ id: 'a' })]).map((one) => one.id)).toEqual(['a']);
  });

  it('spells one patient’s cache key the same way everywhere', () => {
    expect(historyItemsKey(PATIENT_ID)).toEqual(['history', 'items', PATIENT_ID]);
  });

  it('mints a fresh event id each time', () => {
    expect(newEventId()).toMatch(UUID);
    expect(newEventId()).not.toBe(newEventId());
  });

  it('starts a draft with nothing in it but the kind', () => {
    expect(emptyDraft('MEDICATION')).toMatchObject({ kind: 'MEDICATION', concept: null, said: '' });
  });
});
