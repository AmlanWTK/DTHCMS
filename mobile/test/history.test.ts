import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import {
  CARRY_REASONS,
  DURATION_PRESETS,
  HISTORY_FIELDS,
  ONSET_PRECISIONS,
  PROBLEMS,
  SEVERITIES,
  asks,
  canRecord,
  carriedItem,
  carryForward,
  carryReasonFor,
  coded,
  codingFrom,
  edit,
  emptyDraft,
  fieldsFor,
  fromKindsCatalogue,
  groupByKind,
  itemCoding,
  itemLabel,
  kindNamed,
  kindsInOrder,
  lifestyleRows,
  needingConfirmation,
  needsConfirmation,
  ofUnknownKind,
  outstanding,
  parsedDuration,
  partialCoding,
  isPristine,
  problemsWith,
  refusalOn,
  relationsInOrder,
  removalRefused,
  setCoding,
  setKind,
  setOnset,
  toReactivation,
  toRecording,
  toRemoval,
  toResolution,
  uncodedCount,
  type FamilyRelation,
  type HistoryItem,
  type HistoryKind,
} from '../src/features/history/form';

/*
 * The station binding reaches the Keystore through lib/credentials, and the native module
 * cannot load under Node. Mocked exactly as api.test.ts and terminology.test.ts do; nothing
 * here exercises it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const historyApi = await import('../src/features/history/api');
const historyForm = await import('../src/features/history/form');
const {
  amendItem,
  confirmItem,
  countUncoded,
  getItem,
  listKinds,
  listMedicalHistory,
  recordItem,
  removeItem,
  troubleOf,
} = historyApi;

/**
 * Station 4's medical history (CP53).
 *
 * The screen is a React Native component and is judged on the clinic's tablet by a history
 * officer taking a history. What is checked here is every decision behind it, and three of
 * these tests matter more than the rest.
 *
 * **Confirming is one item at a time.** There is one confirm helper in this feature, it takes
 * one item id, and nothing else in the feature calls it. That is acceptance criterion 3 made
 * structural rather than remembered: a "confirm all" button would turn one action by a person
 * into twenty assertions in a clinical record, each carrying that person's name and none of
 * them made by them.
 *
 * **The form asks what the kind's rules say and nothing else.** The fixtures below are the
 * server's own rows, and the tests change the rules and watch the fields change — which is
 * the only way to know the screen is reading them rather than remembering them.
 *
 * **Resolving and removing are two acts.** `PATCH status: RESOLVED` says she had this and no
 * longer does; `POST /remove` says it was never true and takes a reason. A test that let the
 * two collapse would let the record collapse them.
 */

// --- the fixtures: the six kinds exactly as the clinic seeds them ---

const COMPLAINT: HistoryKind = {
  kind: 'COMPLAINT',
  display_en: 'Presenting complaint',
  display_bn: 'বর্তমান সমস্যা',
  code_system: 'DTHC',
  requires_relation: false,
  requires_duration: true,
  allows_severity: true,
  allows_onset: false,
  is_medication: false,
  ordering: 1,
};

const COMORBIDITY: HistoryKind = {
  ...COMPLAINT,
  kind: 'COMORBIDITY',
  display_en: 'Other condition',
  display_bn: 'অন্যান্য রোগ',
  code_system: 'ICD10',
  requires_duration: false,
  allows_onset: true,
  ordering: 2,
};

const FAMILY_HISTORY: HistoryKind = {
  ...COMORBIDITY,
  kind: 'FAMILY_HISTORY',
  display_en: 'In the family',
  display_bn: 'পরিবারে',
  requires_relation: true,
  allows_severity: false,
  ordering: 3,
};

const SURGICAL_HISTORY: HistoryKind = {
  ...FAMILY_HISTORY,
  kind: 'SURGICAL_HISTORY',
  display_en: 'Operation',
  display_bn: 'অস্ত্রোপচার',
  code_system: 'DTHC',
  requires_relation: false,
  ordering: 4,
};

const MEDICATION: HistoryKind = {
  ...SURGICAL_HISTORY,
  kind: 'MEDICATION',
  display_en: 'Current medicine',
  display_bn: 'বর্তমান ওষুধ',
  is_medication: true,
  ordering: 5,
};

const VACCINATION: HistoryKind = {
  ...SURGICAL_HISTORY,
  kind: 'VACCINATION',
  display_en: 'Vaccination',
  display_bn: 'টিকা',
  ordering: 6,
};

const KINDS: HistoryKind[] = [
  COMPLAINT,
  COMORBIDITY,
  FAMILY_HISTORY,
  SURGICAL_HISTORY,
  MEDICATION,
  VACCINATION,
];

const RELATIONS: FamilyRelation[] = [
  { relation: 'MOTHER', display_en: 'Mother', display_bn: 'মা', degree: 1, ordering: 1 },
  { relation: 'FATHER', display_en: 'Father', display_bn: 'বাবা', degree: 1, ordering: 2 },
  { relation: 'GRANDPARENT', display_en: 'Grandparent', display_bn: 'দাদা-দাদি', degree: 2, ordering: 5 }, // prettier-ignore
];

/** This visit began at eight this morning. Everything before it is carried forward. */
const VISIT_START = '2026-09-04T08:00:00.000Z';

const item = (over: Partial<HistoryItem> = {}): HistoryItem => ({
  id: 'item-1',
  patient_id: 'patient-1',
  kind: 'COMPLAINT',
  status: 'ACTIVE',
  recorded_at: '2026-03-11T10:15:00.000Z',
  recorded_by: 'officer-1',
  ...over,
});

const codedItem = (over: Partial<HistoryItem> = {}): HistoryItem =>
  item({
    code_system: 'ICD10',
    code_version: '2019',
    code: 'E11.9',
    display_en: 'Type 2 diabetes mellitus without complications',
    display_bn: 'টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন',
    ...over,
  });

const ids = { event: 'event-1', visit: 'visit-1' };

// --- the network ---

function respond(body: unknown, init: { status?: number } = {}) {
  const status = init.status ?? 200;
  if (status === 204) return new Response(null, { status });
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

interface Call {
  url: string;
  method: string;
  body: string;
  requestedWith: string | null;
}

/** Stubs the network and hands back what the client actually sent. */
function stubFetch(...responses: Response[]): Call[] {
  const calls: Call[] = [];
  let index = 0;
  const mock = vi.fn(async (request: Request) => {
    calls.push({
      url: request.url,
      method: request.method,
      body: await request.clone().text(),
      requestedWith: request.headers.get('X-Requested-With'),
    });
    const response = responses[Math.min(index, responses.length - 1)];
    index += 1;
    return response!.clone();
  });
  vi.stubGlobal('fetch', mock);
  return calls;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

// --- the fields are the kind's own rules ---

describe('the form asks what the kind says to ask, and nothing else', () => {
  it('asks for a relation on a family history and on nothing else', () => {
    expect(asks(FAMILY_HISTORY, 'relation')).toBe(true);
    for (const kind of KINDS.filter((k) => k.kind !== 'FAMILY_HISTORY')) {
      expect(asks(kind, 'relation'), kind.kind).toBe(false);
    }
  });

  it('asks for a duration on a complaint and on nothing else', () => {
    expect(asks(COMPLAINT, 'duration')).toBe(true);
    for (const kind of KINDS.filter((k) => k.kind !== 'COMPLAINT')) {
      expect(asks(kind, 'duration'), kind.kind).toBe(false);
    }
  });

  it('asks for a dose only on the one kind that is a medicine', () => {
    expect(KINDS.filter((kind) => kind.is_medication)).toHaveLength(1);
    expect(asks(MEDICATION, 'dose')).toBe(true);
    expect(asks(MEDICATION, 'frequency')).toBe(true);
    for (const kind of KINDS.filter((k) => !k.is_medication)) {
      expect(asks(kind, 'dose'), kind.kind).toBe(false);
      expect(asks(kind, 'frequency'), kind.kind).toBe(false);
    }
  });

  it('asks for neither a severity nor an onset where the kind forbids them', () => {
    // A vaccination has no severity; a complaint has no onset date, because its duration is
    // how a complaint says when it began.
    expect(asks(VACCINATION, 'severity')).toBe(false);
    expect(asks(COMPLAINT, 'onset')).toBe(false);
    expect(asks(COMORBIDITY, 'severity')).toBe(true);
    expect(asks(COMORBIDITY, 'onset')).toBe(true);
  });

  it('changes the fields when the rules change, because it reads the rules', () => {
    // The whole point of the server returning the rules rather than the names. A clinic that
    // decides an operation carries a severity changes one row, and this form changes with it
    // — which is only true if nothing here remembers what a kind needs.
    const before = fieldsFor(SURGICAL_HISTORY).map((ask) => ask.field);
    expect(before).not.toContain('severity');
    const after = fieldsFor({ ...SURGICAL_HISTORY, allows_severity: true }).map((a) => a.field);
    expect(after).toContain('severity');
  });

  it('names required only what the kind requires unconditionally', () => {
    const required = (kind: HistoryKind) =>
      fieldsFor(kind)
        .filter((ask) => ask.required)
        .map((ask) => ask.field);
    expect(required(FAMILY_HISTORY)).toEqual(['relation']);
    expect(required(COMPLAINT)).toEqual(['duration']);
    expect(required(VACCINATION)).toEqual([]);
    // `said` is never marked required, because whether it is depends on the coding — and a
    // label that says "required" over a box that may be left empty teaches people to ignore
    // the word.
    for (const kind of KINDS) expect(required(kind)).not.toContain('said');
  });

  it('asks in the order the conversation goes, always starting with the words', () => {
    expect(fieldsFor(MEDICATION).map((ask) => ask.field)).toEqual([
      'said',
      'onset',
      'dose',
      'frequency',
    ]);
    for (const kind of KINDS) expect(fieldsFor(kind)[0]?.field).toBe('said');
  });

  it('asks in the clinic’s order, and finds a kind by name', () => {
    expect(kindsInOrder([VACCINATION, COMPLAINT, FAMILY_HISTORY]).map((k) => k.kind)).toEqual([
      'COMPLAINT',
      'FAMILY_HISTORY',
      'VACCINATION',
    ]);
    expect(relationsInOrder([...RELATIONS].reverse()).map((r) => r.relation)).toEqual([
      'MOTHER',
      'FATHER',
      'GRANDPARENT',
    ]);
    expect(kindNamed(KINDS, 'MEDICATION')?.ordering).toBe(5);
    expect(kindNamed(KINDS, 'NOT_A_KIND')).toBeNull();
  });
});

// --- a coding is three fields ---

describe('a partial coding never reaches the server', () => {
  it('refuses two of the three, and says so as a refusal rather than repairing it', () => {
    // Guessing the missing third is how a coding acquires a version nobody searched.
    expect(codingFrom({ code_system: 'ICD10', code: 'E11.9' })).toBeNull();
    expect(partialCoding({ code_system: 'ICD10', code: 'E11.9' })).toBe(true);
    expect(partialCoding({ code: 'E11.9' })).toBe(true);
  });

  it('treats none of the three as a coding-free item rather than a broken one', () => {
    expect(codingFrom({})).toBeNull();
    expect(partialCoding({})).toBe(false);
    expect(partialCoding({ code_system: '  ', code_version: '', code: '' })).toBe(false);
  });

  it('hands back all three, trimmed, when all three are there', () => {
    expect(codingFrom({ code_system: ' ICD10 ', code_version: '2019', code: ' E11.9' })).toEqual({
      system: 'ICD10',
      version: '2019',
      code: 'E11.9',
    });
    expect(coded(codedItem())).toBe(true);
    expect(coded(item())).toBe(false);
    expect(itemCoding(codedItem())?.version).toBe('2019');
  });

  it('refuses a coding from another kind’s catalogue before the officer can send it', () => {
    // A complaint coded in ICD would make the record assert that a patient *presented with*
    // a diagnosis, which is a claim nobody made.
    const icd = { system: 'ICD10', version: '2019', code: 'E11.9' };
    expect(fromKindsCatalogue(COMORBIDITY, icd)).toBe(true);
    expect(fromKindsCatalogue(COMPLAINT, icd)).toBe(false);
    expect(fromKindsCatalogue(COMPLAINT, null)).toBe(true);
    // The server compares case-insensitively, so this does too.
    expect(fromKindsCatalogue(COMORBIDITY, { ...icd, system: 'icd10' })).toBe(true);

    const draft = setCoding(setKind(emptyDraft(), COMPLAINT), COMPLAINT, icd);
    expect(draft.coding, 'a wrong-catalogue coding is not stored at all').toBeNull();
  });

  it('refuses a hand-built draft carrying the wrong catalogue, in the server’s own words', () => {
    const draft = { ...emptyDraft('COMPLAINT'), coding: { system: 'ICD10', version: '2019', code: 'E11.9' }, duration: '7' }; // prettier-ignore
    expect(problemsWith(draft, COMPLAINT).map((r) => r.problem)).toContain('wrongCatalogue');
    expect(canRecord(draft, COMPLAINT)).toBe(false);
    expect(toRecording(draft, COMPLAINT, ids)).toBeNull();
  });
});

// --- an uncoded item is a real item ---

describe('an item the catalogue has nothing for', () => {
  it('keeps what the patient said and is marked uncoded', () => {
    const uncoded = item({ said: 'sugar since the flood' });
    const carried = carriedItem(uncoded, VISIT_START);
    expect(carried.coded).toBe(false);
    expect(carried.said).toBe('sugar since the flood');
    expect(itemCoding(uncoded)).toBeNull();
    // The words are what the row reads as, so an uncoded item is never a blank line.
    expect(itemLabel(uncoded, 'en')).toBe('sugar since the flood');
    expect(itemLabel(uncoded, 'bn')).toBe('sugar since the flood');
  });

  it('is recorded, rather than refused, as long as it says what was meant', () => {
    const draft = edit(setKind(emptyDraft(), COMPLAINT), {
      said: 'burning in both feet at night',
      duration: '90',
    });
    expect(canRecord(draft, COMPLAINT)).toBe(true);
    const body = toRecording(draft, COMPLAINT, ids);
    expect(body).toMatchObject({ kind: 'COMPLAINT', said: 'burning in both feet at night' });
    expect(body?.code).toBeUndefined();
    expect(body?.code_system).toBeUndefined();
    expect(body?.code_version).toBeUndefined();
  });

  it('is counted, by kind, so the catalogue can be corrected rather than the officers', () => {
    const counts = { COMPLAINT: 7, MEDICATION: 2 };
    expect(uncodedCount(counts, 'COMPLAINT')).toBe(7);
    expect(uncodedCount(counts, 'VACCINATION')).toBe(0);
  });

  it('keeps the patient’s words on a coded item too', () => {
    // The catalogue says "Type 2 diabetes mellitus without complications"; the patient said
    // "sugar since the flood", and the second one is the clinical detail.
    const both = codedItem({ said: 'sugar since the flood' });
    const carried = carriedItem(both, VISIT_START);
    expect(carried.coded).toBe(true);
    expect(carried.said).toBe('sugar since the flood');
    expect(itemLabel(both, 'en')).toBe('Type 2 diabetes mellitus without complications');
    expect(itemLabel(both, 'bn')).toBe('টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন');
  });

  it('never reads as an empty row', () => {
    // Bangla where there is Bangla, English where there is not, the patient's words after
    // that, and the bare code before nothing at all.
    expect(itemLabel(codedItem({ display_bn: '  ' }), 'bn')).toBe(
      'Type 2 diabetes mellitus without complications',
    );
    expect(itemLabel(item({ code: 'E11.9', display_en: '' }), 'en')).toBe('E11.9');
    expect(itemLabel(item(), 'en')).toBe('');
  });
});

// --- carry-forward ---

describe('carry-forward is confirmation, never auto-acceptance', () => {
  it('presents an item with no confirmed_at as one nobody has confirmed', () => {
    // Exactly what a returning patient's list looks like. Absent is not "confirmed by the
    // system"; it is "nobody has said this is still true since it was recorded".
    const carried = carriedItem(item(), VISIT_START);
    expect(carried.needsConfirmation).toBe(true);
    expect(carried.reason).toBe('neverConfirmed');
    expect(needsConfirmation(item(), VISIT_START)).toBe(true);
  });

  it('does not present an item confirmed this visit', () => {
    const today = item({ confirmed_at: '2026-09-04T08:41:00.000Z' });
    expect(carryReasonFor(today, VISIT_START)).toBeNull();
    expect(needsConfirmation(today, VISIT_START)).toBe(false);
  });

  it('presents an item last confirmed at an earlier visit, and says which of the two it is', () => {
    const march = item({ confirmed_at: '2026-03-11T10:15:00.000Z' });
    expect(carryReasonFor(march, VISIT_START)).toBe('notThisVisit');
  });

  it('asks again rather than assuming when a timestamp cannot be read', () => {
    // Asking twice costs a sentence; assuming a confirmation nobody made is the failure this
    // whole station exists to prevent.
    expect(carryReasonFor(item({ confirmed_at: 'not a date' }), VISIT_START)).toBe('notThisVisit');
    expect(carryReasonFor(item({ confirmed_at: '2026-09-04T08:41:00.000Z' }), '')).toBe(
      'notThisVisit',
    );
    expect(carryReasonFor(item({ confirmed_at: '   ' }), VISIT_START)).toBe('neverConfirmed');
  });

  it('does not ask about a resolved item, because it is not waiting on anybody', () => {
    const resolved = item({ status: 'RESOLVED' });
    expect(carryReasonFor(resolved, VISIT_START)).toBeNull();
    expect(carriedItem(resolved, VISIT_START).resolved).toBe(true);
  });

  it('counts what is outstanding without offering to clear it', () => {
    const items = [
      item({ id: 'a' }),
      item({ id: 'b', confirmed_at: '2026-09-04T08:41:00.000Z' }),
      item({ id: 'c', confirmed_at: '2026-03-11T10:15:00.000Z' }),
      item({ id: 'd', status: 'RESOLVED' }),
    ];
    // The resolved one is out of the denominator: a progress line that can never reach its
    // total is a line people stop reading.
    expect(outstanding(items, VISIT_START)).toEqual({ done: 1, total: 3 });
    expect(needingConfirmation(items, VISIT_START).map((c) => c.item.id)).toEqual(['a', 'c']);
  });

  it('keeps the server’s order rather than floating the unconfirmed ones to the top', () => {
    // Re-sorting would move an item away from the one recorded beside it on the same
    // afternoon, which is how the officer remembers them.
    const items = [
      item({ id: 'a', confirmed_at: '2026-09-04T08:41:00.000Z' }),
      item({ id: 'b' }),
      item({ id: 'c', confirmed_at: '2026-09-04T08:42:00.000Z' }),
    ];
    expect(carryForward(items, VISIT_START).map((c) => c.item.id)).toEqual(['a', 'b', 'c']);
  });

  it('groups the list under the six headings, keeping the empty ones', () => {
    // "No operations recorded" is an answer. A heading that vanished would leave the officer
    // unable to tell "nobody asked" from "asked, and there is none".
    const items = [
      item({ id: 'a', kind: 'MEDICATION' }),
      item({ id: 'b', kind: 'COMPLAINT', confirmed_at: '2026-09-04T08:41:00.000Z' }),
      item({ id: 'c', kind: 'MEDICATION' }),
    ];
    const groups = groupByKind(items, KINDS, VISIT_START);
    expect(groups.map((group) => group.kind.kind)).toEqual([
      'COMPLAINT',
      'COMORBIDITY',
      'FAMILY_HISTORY',
      'SURGICAL_HISTORY',
      'MEDICATION',
      'VACCINATION',
    ]);
    expect(groups[0]?.items.map((c) => c.item.id)).toEqual(['b']);
    expect(groups[0]?.outstanding).toBe(0);
    expect(groups[4]?.outstanding).toBe(2);
    expect(groups[1]?.items).toEqual([]);
  });

  it('surfaces an item of a kind this build has never heard of rather than dropping it', () => {
    // A tablet a version behind the server would otherwise hide a whole kind of history from
    // the officer working through the list.
    const items = [item({ id: 'a' }), item({ id: 'z', kind: 'DIETARY_HISTORY' })];
    expect(groupByKind(items, KINDS, VISIT_START).flatMap((g) => g.items.map((c) => c.item.id))).toEqual(['a']); // prettier-ignore
    expect(ofUnknownKind(items, KINDS, VISIT_START).map((c) => c.item.id)).toEqual(['z']);
    expect(ofUnknownKind(items, KINDS, VISIT_START)[0]?.needsConfirmation).toBe(true);
  });
});

describe('confirming is one item at a time', () => {
  it('has exactly one confirm helper in the whole feature, and it takes one item id', () => {
    // Acceptance criterion 3, by name. There is deliberately no batch endpoint behind this,
    // and a client-side helper would be the easiest place in the system to put the
    // auto-acceptance back — wearing the name of whoever pressed once.
    const exported = [...Object.keys(historyApi), ...Object.keys(historyForm)];
    expect(exported.filter((name) => /^confirm/i.test(name))).toEqual(['confirmItem']);
    expect(
      exported.filter((name) => /confirm.*(all|list|batch|many|each)|(all|batch).*confirm/i.test(name)), // prettier-ignore
      'nothing in this feature confirms a list',
    ).toEqual([]);
    // One id and one bag of ids. Neither argument is a list, and there is no third.
    expect(confirmItem.length).toBe(2);
  });

  it('is not called anywhere inside the feature, so nothing here can loop over it', () => {
    // The screen takes `onConfirm(itemId)` as a prop and the station container presses it
    // once per press. If a loop is ever added it will be visible in a diff, in a file that
    // is not this one.
    const dir = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'features', 'history');
    for (const file of readdirSync(dir)) {
      if (file === 'api.ts' || file === 'index.ts') continue;
      const source = readFileSync(join(dir, file), 'utf8');
      expect(source.includes('confirmItem'), file).toBe(false);
    }
  });

  it('sends one request per item, each naming its own item and its own event', async () => {
    const calls = stubFetch(respond({ item: item({ confirmed_at: VISIT_START }) }));
    for (const id of ['item-a', 'item-b', 'item-c']) {
      await confirmItem(id, { event: `event-${id}`, visit: 'visit-1' });
    }
    expect(calls, 'three items is three requests').toHaveLength(3);
    expect(calls.map((call) => new URL(call.url).pathname)).toEqual([
      '/v1/history/items/item-a/confirm',
      '/v1/history/items/item-b/confirm',
      '/v1/history/items/item-c/confirm',
    ]);
    for (const call of calls) {
      expect(call.method).toBe('POST');
      expect(call.requestedWith).toBe('DTHCMS');
    }
    // A separate event id per confirmation, so a retry over a bad link produces one
    // confirmation rather than four — and two items never share an idempotency key.
    const events = calls.map((call) => (JSON.parse(call.body) as { event_id: string }).event_id);
    expect(new Set(events).size).toBe(3);
    expect(JSON.parse(calls[0]!.body)).toEqual({ event_id: 'event-item-a', visit_id: 'visit-1' });
  });

  it('omits the visit rather than sending an empty one', async () => {
    const calls = stubFetch(respond({ item: item() }));
    await confirmItem('item-a', { event: 'event-1' });
    expect(JSON.parse(calls[0]!.body)).toEqual({ event_id: 'event-1' });
  });
});

// --- resolving is not removing ---

describe('resolving and removing are two acts with two words', () => {
  it('resolves through the amendment path, keeping the item in the record', () => {
    // "She had this and no longer does" is a clinical fact. A list that hid it would make
    // every follow-up look like a first visit.
    expect(toResolution(ids)).toEqual({
      event_id: 'event-1',
      visit_id: 'visit-1',
      status: 'RESOLVED',
    });
    // "She has it again" is the other half of the same control, and still not a removal.
    expect(toReactivation({ event: 'event-2' })).toEqual({ event_id: 'event-2', status: 'ACTIVE' });
    expect(toReactivation(ids)).toEqual({
      event_id: 'event-1',
      visit_id: 'visit-1',
      status: 'ACTIVE',
    });
  });

  it('removes through a different path, and will not build a removal without a reason', () => {
    expect(removalRefused('')).toBe(true);
    expect(removalRefused('   ')).toBe(true);
    expect(toRemoval('  ', ids)).toBeNull();
    expect(removalRefused('Recorded on the wrong patient.')).toBe(false);
    expect(toRemoval('  Recorded on the wrong patient.  ', ids)).toEqual({
      event_id: 'event-1',
      visit_id: 'visit-1',
      reason: 'Recorded on the wrong patient.',
    });
    expect(toRemoval('wrong patient', { event: 'event-3' })).toEqual({
      event_id: 'event-3',
      reason: 'wrong patient',
    });
  });

  it('never lets a removal be mistaken for a resolution', () => {
    // Two payloads, two endpoints, no shared shape. A removal carries a reason and no
    // status; a resolution carries a status and no reason.
    const resolution = toResolution(ids) as unknown as Record<string, unknown>;
    const removal = toRemoval('never had this', ids) as unknown as Record<string, unknown>;
    expect(resolution.status).toBe('RESOLVED');
    expect(resolution.reason).toBeUndefined();
    expect(removal.reason).toBe('never had this');
    expect(removal.status).toBeUndefined();
  });

  it('sends them to two different endpoints, with two different verbs', async () => {
    const calls = stubFetch(
      respond({ item: item({ status: 'RESOLVED' }) }),
      respond(null, { status: 204 }),
    );
    await amendItem('item-a', toResolution(ids));
    await removeItem('item-a', toRemoval('Recorded on the wrong patient.', ids)!);
    expect(calls[0]?.method).toBe('PATCH');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/history/items/item-a');
    expect(calls[1]?.method).toBe('POST');
    expect(new URL(calls[1]!.url).pathname).toBe('/v1/history/items/item-a/remove');
    expect(JSON.parse(calls[1]!.body)).toMatchObject({ reason: 'Recorded on the wrong patient.' });
  });
});

// --- what the server would refuse, refused here first ---

describe('the refusals mirror the server, in the server’s order', () => {
  const complaint = () => edit(setKind(emptyDraft(), COMPLAINT), { said: 'chest pain', duration: '7' }); // prettier-ignore

  it('lets a complete draft through', () => {
    expect(problemsWith(complaint(), COMPLAINT)).toEqual([]);
    expect(canRecord(complaint(), COMPLAINT)).toBe(true);
  });

  it('refuses an item with neither a coding nor words', () => {
    // It would assert only that the patient has *something*.
    const draft = edit(complaint(), { said: '   ' });
    expect(problemsWith(draft, COMPLAINT).map((r) => r.problem)).toEqual(['nothingSaid']);
    expect(canRecord(emptyDraft('COMPLAINT'), COMPLAINT)).toBe(false);
  });

  it('refuses a family history with no relative, and a relative on anything else', () => {
    const family = edit(setKind(emptyDraft(), FAMILY_HISTORY), { said: 'diabetes' });
    expect(problemsWith(family, FAMILY_HISTORY).map((r) => r.problem)).toEqual(['needsRelation']);
    expect(canRecord(edit(family, { relation: 'MOTHER' }), FAMILY_HISTORY)).toBe(true);

    const complaintWithRelative = edit(complaint(), { relation: 'MOTHER' });
    expect(problemsWith(complaintWithRelative, COMPLAINT).map((r) => r.problem)).toEqual([
      'noRelation',
    ]);
  });

  it('refuses a complaint with no duration, and a duration that is not one', () => {
    expect(problemsWith(edit(complaint(), { duration: '' }), COMPLAINT).map((r) => r.problem)).toEqual(['needsDuration']); // prettier-ignore
    for (const text of ['two weeks', '3.5', '-1']) {
      expect(problemsWith(edit(complaint(), { duration: text }), COMPLAINT).map((r) => r.problem), text).toEqual(['notADuration']); // prettier-ignore
    }
    // Zero is a real answer — it started today — so it is not the anthropometry rule.
    expect(parsedDuration('0')).toBe(0);
    expect(parsedDuration(' 14 ')).toBe(14);
    expect(parsedDuration('')).toBeNull();
    expect(parsedDuration('later')).toBeNull();
  });

  it('refuses a severity, an onset and a dose on kinds that carry none', () => {
    const vaccination = { ...emptyDraft('VACCINATION'), said: 'tetanus' };
    expect(problemsWith({ ...vaccination, severity: 'mild' }, VACCINATION).map((r) => r.problem)).toEqual(['noSeverity']); // prettier-ignore
    expect(problemsWith({ ...emptyDraft('COMPLAINT'), said: 'x', duration: '1', onsetOn: '2026-01-01', onsetPrecision: 'day' }, COMPLAINT).map((r) => r.problem)).toEqual(['noOnset']); // prettier-ignore
    expect(problemsWith({ ...vaccination, dose: '1 tablet' }, VACCINATION).map((r) => r.problem)).toEqual(['noDose']); // prettier-ignore
    expect(problemsWith({ ...vaccination, frequency: 'twice a day' }, VACCINATION).map((r) => r.problem)).toEqual(['noDose']); // prettier-ignore
  });

  it('refuses an onset date without its precision, and a precision without a date', () => {
    // A patient who says "about two years ago" has given a real answer, and storing it as an
    // exact date makes a guess look like a measurement.
    const base = { ...emptyDraft('COMORBIDITY'), said: 'high blood pressure' };
    expect(problemsWith({ ...base, onsetOn: '2024-01-01' }, COMORBIDITY).map((r) => r.problem)).toEqual(['onsetPartial']); // prettier-ignore
    expect(problemsWith({ ...base, onsetPrecision: 'year' }, COMORBIDITY).map((r) => r.problem)).toEqual(['onsetPartial']); // prettier-ignore
    const whole = setOnset(base, '2024-01-01', 'year');
    expect(problemsWith(whole, COMORBIDITY)).toEqual([]);
  });

  it('puts each refusal on the field it belongs to', () => {
    const broken = { ...emptyDraft('FAMILY_HISTORY'), onsetOn: '2024-01-01' };
    const refusals = problemsWith(broken, FAMILY_HISTORY);
    expect(refusalOn(refusals, 'said')).toBe('nothingSaid');
    expect(refusalOn(refusals, 'relation')).toBe('needsRelation');
    expect(refusalOn(refusals, 'onset')).toBe('onsetPartial');
    expect(refusalOn(refusals, 'dose')).toBeNull();
  });

  it('refuses to build a body at all for a draft the server would refuse', () => {
    expect(toRecording(emptyDraft('COMPLAINT'), COMPLAINT, ids)).toBeNull();
  });

  it('does not correct an officer who has not started', () => {
    // A refusal on screen before the first keystroke is furniture by the time it means
    // something. The draft is still refused — it just is not shouted at.
    expect(isPristine(emptyDraft('COMPLAINT'))).toBe(true);
    expect(canRecord(emptyDraft('COMPLAINT'), COMPLAINT)).toBe(false);
    expect(isPristine(edit(emptyDraft('COMPLAINT'), { said: 'cough' }))).toBe(false);
    expect(isPristine(edit(emptyDraft('COMPLAINT'), { duration: '7' }))).toBe(false);
    expect(isPristine(setOnset(emptyDraft('COMORBIDITY'), '2024-01-01', 'year'))).toBe(false);
    expect(
      isPristine(
        setCoding(setKind(emptyDraft(), COMORBIDITY), COMORBIDITY, {
          system: 'ICD10',
          version: '2019',
          code: 'E11.9',
        }),
      ),
    ).toBe(false);
    // Whitespace is not a start.
    expect(isPristine(edit(emptyDraft('COMPLAINT'), { said: '   ', dose: ' ' }))).toBe(true);
  });
});

// --- what gets sent ---

describe('one item, one request', () => {
  it('writes the coding as three fields or none at all', () => {
    const draft = setCoding(setKind(emptyDraft(), COMORBIDITY), COMORBIDITY, {
      system: 'ICD10',
      version: '2019',
      code: 'E11.9',
    });
    const body = toRecording(edit(draft, { said: 'sugar since the flood' }), COMORBIDITY, ids);
    expect(body).toEqual({
      event_id: 'event-1',
      visit_id: 'visit-1',
      kind: 'COMORBIDITY',
      code_system: 'ICD10',
      code_version: '2019',
      code: 'E11.9',
      said: 'sugar since the flood',
    });
  });

  it('sends only the fields the kind asks for, however the draft was filled', () => {
    // Belt as well as braces: `setKind` clears what a kind does not ask for, and this refuses
    // to write it even if something else put it back.
    const draft = {
      ...emptyDraft('VACCINATION'),
      said: 'tetanus, at the union clinic',
      relation: 'MOTHER',
      duration: '30',
      severity: 'mild' as const,
      dose: '0.5 mL',
      frequency: 'once',
    };
    // The draft is refused first, which is the honest answer; with the offending fields gone
    // what is left is exactly what a vaccination carries.
    expect(canRecord(draft, VACCINATION)).toBe(false);
    const cleaned = setKind(draft, VACCINATION);
    expect(cleaned).toMatchObject({ relation: '', duration: '', severity: '', dose: '' });
    expect(toRecording(cleaned, VACCINATION, { event: 'event-9' })).toEqual({
      event_id: 'event-9',
      kind: 'VACCINATION',
      said: 'tetanus, at the union clinic',
    });
  });

  it('writes a medicine’s dose and frequency, and a complaint’s duration and severity', () => {
    const medicine = {
      ...emptyDraft('MEDICATION'),
      said: 'metformin',
      dose: ' 1 tablet ',
      frequency: 'twice a day after food',
    };
    expect(toRecording(medicine, MEDICATION, { event: 'e' })).toMatchObject({
      dose: '1 tablet',
      frequency: 'twice a day after food',
    });

    const complaint = {
      ...emptyDraft('COMPLAINT'),
      said: 'pain in both feet',
      duration: '14',
      severity: 'moderate' as const,
    };
    expect(toRecording(complaint, COMPLAINT, { event: 'e' })).toMatchObject({
      duration_days: 14,
      severity: 'moderate',
    });
  });

  it('writes an onset date only together with the precision that says how exact it is', () => {
    const dated = setOnset(
      { ...emptyDraft('COMORBIDITY'), said: 'high blood pressure' },
      ' 2024-01-01 ',
      'year',
    );
    expect(toRecording(dated, COMORBIDITY, { event: 'e' })).toEqual({
      event_id: 'e',
      kind: 'COMORBIDITY',
      said: 'high blood pressure',
      onset_on: '2024-01-01',
      onset_precision: 'year',
    });
  });

  it('drops the visit when there is none, rather than sending an empty one', () => {
    const draft = edit(setKind(emptyDraft(), COMPLAINT), { said: 'cough', duration: '3' });
    expect(toRecording(draft, COMPLAINT, { event: 'e', visit: '' })?.visit_id).toBeUndefined();
    expect(toRecording(draft, COMPLAINT, { event: 'e' })?.visit_id).toBeUndefined();
  });

  it('clears the coding when the kind changes, because each kind has its own catalogue', () => {
    const comorbidity = setCoding(setKind(emptyDraft(), COMORBIDITY), COMORBIDITY, {
      system: 'ICD10',
      version: '2019',
      code: 'E11.9',
    });
    expect(comorbidity.coding).not.toBeNull();
    // "Actually this is a medicine, not a condition" is a different item, not an edited one.
    expect(setKind(comorbidity, MEDICATION).coding).toBeNull();
  });

  it('keeps the onset date and its precision together through a change of kind', () => {
    const dated = setOnset({ ...emptyDraft('COMORBIDITY'), said: 'x' }, '2024-01-01', 'year');
    expect(setKind(dated, COMPLAINT)).toMatchObject({ onsetOn: '', onsetPrecision: '' });
    expect(setKind(dated, SURGICAL_HISTORY)).toMatchObject({
      onsetOn: '2024-01-01',
      onsetPrecision: 'year',
    });
  });
});

// --- what belongs to the lifestyle station ---

describe('what the lifestyle station answered is shown and never asked again', () => {
  it('reads the newest answer for each code the server names', () => {
    const rows = lifestyleRows(
      ['SMOKING_STATUS', 'ALCOHOL_USE'],
      [
        { code: 'SMOKING_STATUS', value_code: 'former' },
        { code: 'SMOKING_STATUS', value_code: 'current' },
      ],
    );
    // Newest first from the API, so the first sighting of a code is the current answer.
    expect(rows[0]).toEqual({ code: 'SMOKING_STATUS', valueCode: 'former', known: true });
    // A code nobody has answered is shown as unasked, never as a blank.
    expect(rows[1]).toEqual({ code: 'ALCOHOL_USE', valueCode: '', known: false });
  });

  it('says nothing at all when the server names no lifestyle codes', () => {
    expect(lifestyleRows([], undefined)).toEqual([]);
    expect(lifestyleRows(['SMOKING_STATUS'], undefined)[0]?.known).toBe(false);
    expect(lifestyleRows(['SMOKING_STATUS'], [{ code: 'SMOKING_STATUS' }])[0]?.known).toBe(false);
  });

  it('offers no way to record one of them', () => {
    // Station 4 shows these and never re-enters them: a second copy would be two answers to
    // one question with no way to tell which is current. Nothing in this feature writes an
    // observation at all — every write here is a history item.
    const writers = Object.keys(historyApi).filter((name) => /^(record|amend|remove|confirm)/.test(name)); // prettier-ignore
    expect(writers.sort()).toEqual(['amendItem', 'confirmItem', 'recordItem', 'removeItem']);
    expect(Object.keys(historyForm).filter((name) => /lifestyle/i.test(name))).toEqual([
      'lifestyleRows',
    ]);
  });
});

// --- the calls ---

describe('the eight calls', () => {
  it('fetches the kinds, the relations and what belongs to another station', async () => {
    stubFetch(
      respond({ kinds: KINDS, relations: RELATIONS, from_lifestyle_station: ['SMOKING_STATUS'] }),
    );
    const body = await listKinds();
    expect(body.kinds.map((kind) => kind.kind)).toContain('COMPLAINT');
    expect(body.relations).toHaveLength(3);
    expect(body.from_lifestyle_station).toEqual(['SMOKING_STATUS']);
  });

  it('counts the uncoded items by kind', async () => {
    stubFetch(respond({ uncoded: { COMPLAINT: 4 } }));
    expect(await countUncoded()).toEqual({ COMPLAINT: 4 });
  });

  it('lists a patient’s history', async () => {
    const calls = stubFetch(respond({ items: [item(), codedItem({ id: 'item-2' })] }));
    const items = await listMedicalHistory('patient-1');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/patients/patient-1/medical-history');
    expect(items.map((row) => row.id)).toEqual(['item-1', 'item-2']);
  });

  it('records one item, with the forgery guard on it', async () => {
    const calls = stubFetch(respond({ item: item() }, { status: 201 }));
    const draft = edit(setKind(emptyDraft(), COMPLAINT), { said: 'cough', duration: '3' });
    const written = await recordItem('patient-1', toRecording(draft, COMPLAINT, ids)!);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/patients/patient-1/medical-history');
    expect(calls[0]?.method).toBe('POST');
    expect(calls[0]?.requestedWith).toBe('DTHCMS');
    expect(JSON.parse(calls[0]!.body)).toMatchObject({ kind: 'COMPLAINT', duration_days: 3 });
    expect(written.id).toBe('item-1');
  });

  it('fetches one item, removed ones included', async () => {
    const calls = stubFetch(respond({ item: item() }));
    expect((await getItem('item-1')).id).toBe('item-1');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/history/items/item-1');
  });

  it('throws the shared error classes, so the trouble mapping has something to read', async () => {
    stubFetch(
      respond(
        {
          error: {
            code: 'VALIDATION_FAILED',
            kind: 'validation',
            message: 'A family history is about a relative.',
            message_bn: 'পারিবারিক ইতিহাস কোনো আত্মীয়কে নিয়ে।',
            fields: { relation: 'A family history is about a relative.' },
          },
        },
        { status: 422 },
      ),
    );
    const error = await recordItem('patient-1', { kind: 'FAMILY_HISTORY' }).catch(
      (e: unknown) => e,
    );
    expect(error).toBeInstanceOf(ApiError);
    expect(troubleOf(error, 'en')).toEqual({
      kind: 'refused',
      field: 'relation',
      message: 'A family history is about a relative.',
    });
  });
});

describe('what went wrong, in the three ways it can', () => {
  const apiError = (over: {
    status: number;
    fields?: Record<string, string>;
    fieldsBN?: Record<string, string>;
    messageEN?: string;
    messageBN?: string;
  }) =>
    new ApiError({
      status: over.status,
      code: 'VALIDATION_FAILED',
      kind: 'validation',
      messageEN: over.messageEN ?? 'That request cannot be answered.',
      messageBN: over.messageBN ?? 'ওই অনুরোধের উত্তর দেওয়া যাচ্ছে না।',
      fields: over.fields ?? {},
      fieldsBN: over.fieldsBN ?? {},
      correlationID: 'req-1',
    });

  it('reports a request that never left the tablet as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Network request failed')));
    const error = await listKinds().catch((e: unknown) => e);
    expect(troubleOf(error, 'en')).toEqual({ kind: 'unreachable' });
  });

  it('shows a refusal in the server’s own words, in the reader’s language', () => {
    const error = apiError({
      status: 422,
      fields: { duration_days: 'A complaint says how long.' },
      fieldsBN: { duration_days: 'সমস্যাটি কতদিন ধরে চলছে তা লিখতে হবে।' },
    });
    expect(troubleOf(error, 'en')).toMatchObject({ field: 'duration_days' });
    expect(troubleOf(error, 'bn')).toEqual({
      kind: 'refused',
      field: 'duration_days',
      message: 'সমস্যাটি কতদিন ধরে চলছে তা লিখতে হবে।',
    });
  });

  it('reports the field the officer can act on first when a refusal names two', () => {
    const error = apiError({
      status: 422,
      fields: { severity: 'no severity here', relation: 'name the relation' },
    });
    expect(troubleOf(error, 'en')).toMatchObject({ field: 'relation' });
  });

  it('shows a field this build has never heard of rather than swallowing it', () => {
    // A newer server naming a parameter this tablet does not know still produces a sentence
    // somebody can quote down a phone line. Sorted, so two operators read the same one.
    const error = apiError({ status: 422, fields: { zeta: 'last', alpha: 'first' } });
    expect(troubleOf(error, 'en')).toMatchObject({ field: 'alpha', message: 'first' });
  });

  it('still says something when a refusal names no field at all', () => {
    const error = apiError({ status: 422, messageEN: 'That item was removed.' });
    expect(troubleOf(error, 'en')).toEqual({
      kind: 'refused',
      field: '',
      message: 'That item was removed.',
    });
  });

  it('answers in Bangla when the server wrote a Bangla sentence for a failure', () => {
    const error = apiError({ status: 503, messageBN: 'সার্ভার এখন সাড়া দিচ্ছে না।' });
    expect(troubleOf(error, 'bn')).toEqual({
      kind: 'failed',
      message: 'সার্ভার এখন সাড়া দিচ্ছে না।',
    });
  });

  it('separates a server that refused from one that could not answer', () => {
    expect(troubleOf(apiError({ status: 500 }), 'en')).toMatchObject({ kind: 'failed' });
    expect(troubleOf(apiError({ status: 409, messageBN: '' }), 'bn')).toEqual({
      kind: 'failed',
      message: 'That request cannot be answered.',
    });
    // Something that is not an error this app throws. The screen supplies the sentence.
    expect(troubleOf(new Error('surprise'), 'en')).toEqual({ kind: 'failed', message: '' });
  });
});

// --- the words ---

describe('every label this station can produce exists in both languages', () => {
  const messages = { en: en as Record<string, Record<string, unknown>>, bn: bn as Record<string, Record<string, unknown>> }; // prettier-ignore

  const lookup = (language: 'en' | 'bn', path: string): unknown =>
    path
      .split('.')
      .reduce<unknown>(
        (node, key) =>
          node !== null && typeof node === 'object'
            ? (node as Record<string, unknown>)[key]
            : undefined,
        messages[language],
      );

  const present = (path: string) => {
    for (const language of ['en', 'bn'] as const) {
      expect(typeof lookup(language, path), `${language}: ${path}`).toBe('string');
    }
  };

  it('has a sentence for every refusal this form can produce', () => {
    // The screen looks the key up from the refusal, so a rule added without a sentence
    // written for it would show the officer a raw identifier where the explanation belongs.
    expect(PROBLEMS).toHaveLength(11);
    for (const problem of PROBLEMS) present(`history.problem.${problem}`);
  });

  it('has a sentence for every reason an item is waiting on somebody', () => {
    expect(CARRY_REASONS).toHaveLength(2);
    for (const reason of CARRY_REASONS) present(`history.why.${reason}`);
    present('history.confirmedToday');
    present('history.confirm');
    present('history.outstanding');
    present('history.allConfirmed');
    // The sentence that says out loud why there is no confirm-all button. Without it the
    // absence of a convenience reads as an oversight and somebody adds one.
    present('history.oneAtATime');
  });

  it('has a label for every field a kind can ask for', () => {
    expect(HISTORY_FIELDS).toHaveLength(7);
    for (const field of HISTORY_FIELDS) present(`history.field.${field}`);
    // And for every field any of the six kinds actually asks for, which is the same set read
    // from the other end.
    for (const kind of KINDS) {
      for (const ask of fieldsFor(kind)) present(`history.field.${ask.field}`);
    }
    present('history.required');
  });

  it('has a word for every severity, every precision and every duration preset', () => {
    for (const severity of SEVERITIES) present(`history.severity.${severity}`);
    for (const precision of ONSET_PRECISIONS) present(`history.onsetPrecision.${precision}`);
    expect(DURATION_PRESETS).toHaveLength(6);
    for (const preset of DURATION_PRESETS) present(`history.preset.${preset.key}`);
    present('history.durationDays');
    present('history.onsetOn');
  });

  it('has a word for both statuses, and two different words for the two acts', () => {
    for (const status of ['ACTIVE', 'RESOLVED']) present(`history.status.${status}`);
    // Never one control. Resolving keeps the item; removing says it should not have been
    // recorded and takes a reason.
    present('history.resolve');
    present('history.reactivate');
    present('history.resolveHint');
    present('history.removeOpen');
    present('history.removeHint');
    present('history.removeReason');
    present('history.remove');
    present('history.removeCancel');
    for (const language of ['en', 'bn'] as const) {
      expect(lookup(language, 'history.resolve')).not.toBe(lookup(language, 'history.removeOpen'));
    }
  });

  it('says that an item may be uncoded, and carries all three parts of a coding', () => {
    present('history.uncoded');
    present('history.uncodedHint');
    present('history.uncodedInClinic');
    present('history.said');
    for (const language of ['en', 'bn'] as const) {
      const coding = lookup(language, 'history.coding') as string;
      for (const placeholder of ['{system}', '{code}', '{version}']) {
        expect(coding, `${language} coding`).toContain(placeholder);
      }
    }
  });

  it('has the rest of the screen, including what belongs to the lifestyle station', () => {
    for (const key of [
      'station',
      'noPatient',
      'lifestyle',
      'lifestyleHint',
      'lifestyleUnknown',
      'carried',
      'carriedHint',
      'nothingCarried',
      'add',
      'chooseKind',
      'record',
      'recorded',
      'none',
      'unknownKind',
      'troubleHint',
      'retry',
      'trouble.refused',
      'trouble.unreachable',
      'trouble.failed',
    ]) {
      present(`history.${key}`);
    }
    present('screen.history');
  });
});
