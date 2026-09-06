import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { API_BASE_URL, ApiError } from '@/lib/api';
import {
  contentApproved,
  counselingAssignmentsKey,
  counselingVersionKey,
  counselingVersionsKey,
  createCounselingTemplate,
  draftCounselingVersion,
  draftFrom,
  draftsOf,
  emptyItemRow,
  getCounselingVersion,
  groupByRoom,
  isEditable,
  isPublished,
  itemsInOrder,
  listCounselingAssignments,
  listCounselingRooms,
  listCounselingTemplates,
  listCounselingVersions,
  mandatoryItems,
  moveRow,
  publishBlockers,
  publishCounselingVersion,
  publishedBySystem,
  publishedVersionOf,
  readyToPublish,
  removeRow,
  rowsFrom,
  saveBlockers,
  saveCounselingDraft,
  suggestItemCode,
  versionToOpen,
  type CounselingItem,
  type CounselingRoom,
  type CounselingVersion,
  type ItemDraftRow,
} from '@/features/counseling/api/counseling';

/**
 * The client half of the counselling checklist (CP55, §5.1, [R-07]).
 *
 * The calls themselves are ordinary — a path, an envelope unwrapped, a refusal that arrives
 * as an `ApiError` rather than as an empty list. Six rules are not, and they are why this
 * file exists.
 *
 *  - **`isEditable` answers `status === 'DRAFT'` and nothing looser.** The tempting reading
 *    is "not retired", which is wrong in precisely the case the whole checkpoint is about: a
 *    published version is live, looks current, and is the one that must never be edited.
 *  - **Publishing cannot be reached without a token.** `publishCounselingVersion` takes the
 *    step-up token as its first parameter, so no caller arrives at it by adding an argument
 *    to a save. There is a named test below that fails if a token-free publish ever appears.
 *  - **`saveBlockers` does not contain the bilingual rule.** A draft with English and no
 *    Bangla saves, because that is what writing looks like. Only what the server rejects a
 *    *save* for is listed.
 *  - **`publishBlockers` names the item, where the server names the list.** A 422 comes back
 *    as one message against `items`; this is what turns it into "item 2, no Bangla".
 *  - **Ordering is written from the array position.** The array is the order the author
 *    arranged, and a stored ordering carried through a move puts the item back where it was.
 *  - **`contentApproved` and `publishedBySystem` are predicates, not truthiness.** D-53 is
 *    open and the seeded version says `MIGRATION`; both facts are reported rather than
 *    rounded up to the comfortable answer.
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
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_counseling_1' },
  });
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const TEMPLATE = '0190a8f2-0000-7000-8000-0000000000d1';

const ROOMS: CounselingRoom[] = [
  {
    room: 'COUNSELING_ROOM',
    display_en: 'Counselling room',
    display_bn: 'কাউন্সেলিং কক্ষ',
    station_code: 'COUNSELING',
    ordering: 1,
  },
  {
    room: 'NUTRITION_ROOM',
    display_en: 'Nutrition room',
    display_bn: 'পুষ্টি কক্ষ',
    station_code: 'NUTRITION',
    ordering: 2,
  },
  {
    room: 'INSULIN_CORNER',
    display_en: 'Insulin corner',
    display_bn: 'ইনসুলিন কর্নার',
    station_code: 'COUNSELING',
    ordering: 3,
  },
];

function item(over: Partial<CounselingItem> = {}): CounselingItem {
  return {
    item_code: 'DIET',
    ordering: 1,
    text_en: 'Diet advice',
    text_bn: 'খাদ্য পরামর্শ',
    mandatory: true,
    room: 'COUNSELING_ROOM',
    ...over,
  };
}

function version(over: Partial<CounselingVersion> = {}): CounselingVersion {
  return {
    template_id: TEMPLATE,
    version: 1,
    status: 'DRAFT',
    created_at: '2026-09-01T04:00:00Z',
    ...over,
  };
}

function row(over: Partial<ItemDraftRow> = {}): ItemDraftRow {
  return {
    key: 'k1',
    itemCode: 'DIET',
    codeSuggested: false,
    textEN: 'Diet advice',
    textBN: 'খাদ্য পরামর্শ',
    guidanceEN: '',
    guidanceBN: '',
    mandatory: false,
    room: 'COUNSELING_ROOM',
    ...over,
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('reading', () => {
  it('unwraps the rooms', async () => {
    server({ 'GET /v1/counseling/rooms': () => respond({ rooms: ROOMS }) });
    await expect(listCounselingRooms()).resolves.toHaveLength(3);
  });

  it('unwraps the templates', async () => {
    server({
      'GET /v1/counseling/templates': () =>
        respond({
          templates: [
            {
              id: TEMPLATE,
              code: 'DIABETES',
              title_en: 'Diabetes',
              title_bn: 'ডায়াবেটিস',
              latest_version: 2,
            },
          ],
        }),
    });
    const templates = await listCounselingTemplates();
    expect(templates[0]?.code).toBe('DIABETES');
  });

  it('unwraps the versions', async () => {
    server({
      [`GET /v1/counseling/templates/${TEMPLATE}`]: () =>
        respond({ versions: [version({ version: 2 }), version({ status: 'PUBLISHED' })] }),
    });
    await expect(listCounselingVersions(TEMPLATE)).resolves.toHaveLength(2);
  });

  it('unwraps one version with its items', async () => {
    server({
      [`GET /v1/counseling/templates/${TEMPLATE}/versions/3`]: () =>
        respond({ version: version({ version: 3, items: [item()] }) }),
    });
    const one = await getCounselingVersion(TEMPLATE, 3);
    expect(one.items).toHaveLength(1);
  });

  it('answers with empty lists when the assignments endpoint sends neither key', async () => {
    // The contract makes both properties optional, and a screen that read
    // `body.assignments.length` would crash on the perfectly legal empty answer.
    server({ 'GET /v1/counseling/assignments': () => respond({}) });
    await expect(listCounselingAssignments()).resolves.toEqual({ assignments: [], matches: [] });
  });

  it('asks what a coding would get, dropping the parts it was not given', async () => {
    const seen = server({ 'GET /v1/counseling/assignments': () => respond({ matches: [] }) });
    await listCounselingAssignments({ system: 'ICD10', code: 'E11.9' });
    const url = new URL(seen[0]!.url);
    expect(url.searchParams.get('system')).toBe('ICD10');
    expect(url.searchParams.get('code')).toBe('E11.9');
    expect(url.searchParams.has('version')).toBe(false);
  });

  it('raises the server’s refusal rather than answering with nothing', async () => {
    server({
      'GET /v1/counseling/templates': () =>
        respond(
          {
            error: {
              code: 'FORBIDDEN',
              kind: 'authorization',
              message: 'Not for this role.',
              message_bn: 'এই ভূমিকার জন্য নয়।',
              correlation_id: 'c-1',
            },
          },
          403,
        ),
    });
    await expect(listCounselingTemplates()).rejects.toBeInstanceOf(ApiError);
  });
});

describe('writing', () => {
  it('creates a template, upper-casing the code it sends', async () => {
    const seen = server({
      'POST /v1/counseling/templates': () =>
        respond(
          {
            template: {
              id: TEMPLATE,
              code: 'THYROID',
              title_en: 'Thyroid',
              title_bn: 'থাইরয়েড',
              latest_version: 1,
            },
          },
          201,
        ),
    });

    await createCounselingTemplate({
      code: ' thyroid ',
      title_en: 'Thyroid',
      title_bn: 'থাইরয়েড',
    });

    const body = await seen[0]!.clone().json();
    expect(body.code).toBe('THYROID');
    // Every state-changing call carries an attempt key. A clinic's connection drops
    // mid-save routinely, and a retried create must not make two checklists.
    expect(seen[0]!.headers.get('Idempotency-Key')).toMatch(UUID);
    expect(seen[0]!.headers.get('X-Requested-With')).toBe('DTHCMS');
  });

  it('opens a draft, sending notes only when there are some', async () => {
    const seen = server({
      [`POST /v1/counseling/templates/${TEMPLATE}/versions`]: () =>
        respond({ version: version({ version: 2 }) }, 201),
    });

    await draftCounselingVersion(TEMPLATE, '   ');
    expect(await seen[0]!.clone().json()).toEqual({});

    await draftCounselingVersion(TEMPLATE, '  Fix the injection-site wording  ');
    expect(await seen[1]!.clone().json()).toEqual({ notes: 'Fix the injection-site wording' });
  });

  it('saves a draft whose Bangla is missing, because half-written is a real state', async () => {
    const seen = server({
      [`PUT /v1/counseling/templates/${TEMPLATE}/versions/2`]: () =>
        respond({ version: version({ version: 2 }) }),
    });

    await saveCounselingDraft(TEMPLATE, 2, {
      items: draftFrom([row({ textBN: '' })]),
    });

    const body = await seen[0]!.clone().json();
    expect(body.items[0]).toMatchObject({ item_code: 'DIET', text_en: 'Diet advice', text_bn: '' });
  });

  it('surfaces the 409 a published version answers with, rather than swallowing it', async () => {
    server({
      [`PUT /v1/counseling/templates/${TEMPLATE}/versions/1`]: () =>
        respond(
          {
            error: {
              code: 'CONFLICT',
              kind: 'conflict',
              message: 'That version is published and cannot be edited.',
              message_bn: 'ওই সংস্করণটি প্রকাশিত, সম্পাদনা করা যাবে না।',
              correlation_id: 'c-2',
            },
          },
          409,
        ),
    });

    // Not an error to route around. The edits belong in a new version, and a client that
    // retried would be trying to rewrite what past sessions were asked to cover.
    await expect(
      saveCounselingDraft(TEMPLATE, 1, { items: draftFrom([row()]) }),
    ).rejects.toMatchObject({ status: 409 });
  });

  it('sends the step-up token on the publish, rather than retrying after a 403', async () => {
    const seen = server({
      [`POST /v1/counseling/templates/${TEMPLATE}/versions/2/publish`]: () =>
        respond({ version: version({ version: 2, status: 'PUBLISHED' }) }),
    });

    await publishCounselingVersion('step-up-token', TEMPLATE, 2);

    expect(seen).toHaveLength(1);
    expect(seen[0]!.headers.get('X-Step-Up-Token')).toBe('step-up-token');
  });

  it('has no way to publish without a token', () => {
    // The named test. Publishing puts a checklist on every phone on the floor and freezes it
    // forever; saving is cheap and reversible. The token is the first parameter and is not
    // optional, so nobody arrives at publishing by adding an argument to a save.
    expect(publishCounselingVersion).toHaveLength(3);

    // And there is exactly one call to the endpoint in the whole module, carrying exactly
    // one step-up header. A convenience wrapper that made the dangerous act look like the
    // cheap one would add a second of either, and this fails when it does.
    const source = readFileSync(
      join(
        dirname(fileURLToPath(import.meta.url)),
        '..',
        'src',
        'features',
        'counseling',
        'api',
        'counseling.ts',
      ),
      'utf8',
    );
    expect(source.match(/versions\/\{version\}\/publish/g)).toHaveLength(1);
    expect(source.match(/STEP_UP_HEADER/g)).toHaveLength(2); // the import, and the one use
  });
});

describe('what may be changed', () => {
  it('calls only a draft editable', () => {
    expect(isEditable(version({ status: 'DRAFT' }))).toBe(true);
    // The two that matter. "Not retired" would have called the first of these editable,
    // which is the single mistake the frozen model exists to prevent.
    expect(isEditable(version({ status: 'PUBLISHED' }))).toBe(false);
    expect(isEditable(version({ status: 'RETIRED' }))).toBe(false);
  });

  it('finds the live version rather than the highest number', () => {
    const versions = [
      version({ version: 4, status: 'DRAFT' }),
      version({ version: 3, status: 'PUBLISHED' }),
      version({ version: 2, status: 'RETIRED' }),
    ];
    expect(publishedVersionOf(versions)?.version).toBe(3);
    expect(isPublished(versions[1]!)).toBe(true);
    expect(draftsOf(versions).map((one) => one.version)).toEqual([4]);
  });

  it('opens on the newest draft, then on what is live, then on whatever exists', () => {
    expect(
      versionToOpen([version({ version: 3, status: 'DRAFT' }), version({ status: 'PUBLISHED' })])
        ?.version,
    ).toBe(3);
    expect(
      versionToOpen([version({ version: 3, status: 'RETIRED' }), version({ status: 'PUBLISHED' })])
        ?.version,
    ).toBe(1);
    expect(versionToOpen([version({ version: 9, status: 'RETIRED' })])?.version).toBe(9);
    expect(versionToOpen([])).toBeUndefined();
  });
});

describe('what is reported rather than rounded up', () => {
  it('calls unapproved content unapproved, however it is absent', () => {
    // D-53 is open. Null, missing and empty all mean the same thing and none of them means
    // a clinician signed off.
    expect(contentApproved({})).toBe(false);
    expect(contentApproved({ approved_at: null })).toBe(false);
    expect(contentApproved({ approved_at: '  ' })).toBe(false);
    expect(contentApproved({ approved_at: '2026-09-02T04:00:00Z' })).toBe(true);
  });

  it('knows a migration published a version, without inventing a person', () => {
    const seeded = version({
      status: 'PUBLISHED',
      published_source: 'MIGRATION',
      published_at: '2026-01-01T00:00:00Z',
    });
    expect(publishedBySystem(seeded)).toBe(true);
    expect(seeded.published_by).toBeUndefined();
    expect(publishedBySystem(version({ status: 'PUBLISHED', published_source: 'USER' }))).toBe(
      false,
    );
  });
});

describe('what the server would refuse a save for', () => {
  it('allows an item with one language, because that is what writing looks like', () => {
    expect(saveBlockers([row({ textBN: '' })], ROOMS)).toEqual([]);
    expect(saveBlockers([row({ textEN: '' })], ROOMS)).toEqual([]);
  });

  it('names a blank code, a repeated one, an empty row and an unknown room', () => {
    const blockers = saveBlockers(
      [
        row({ key: 'a', itemCode: '   ' }),
        row({ key: 'b', itemCode: 'DIET' }),
        row({ key: 'c', itemCode: 'diet' }),
        row({ key: 'd', itemCode: 'EMPTY', textEN: ' ', textBN: '' }),
        row({ key: 'e', itemCode: 'ELSEWHERE', room: 'CORRIDOR' }),
      ],
      ROOMS,
    );

    expect(blockers).toContainEqual({ kind: 'no_code', index: 0 });
    // Case-insensitively: the server upper-cases before it checks, and two items sharing a
    // code make a tick ambiguous in a way that only surfaces as a gate that never closes.
    expect(blockers).toContainEqual({ kind: 'duplicate_code', index: 2, itemCode: 'DIET' });
    expect(blockers).toContainEqual({ kind: 'no_text', index: 3 });
    expect(blockers).toContainEqual({ kind: 'unknown_room', index: 4, room: 'CORRIDOR' });
  });
});

describe('what the publish endpoint would refuse', () => {
  it('names the item and the missing language, where the server names the list', () => {
    const blockers = publishBlockers(
      draftFrom([row({ itemCode: 'DIET' }), row({ key: 'k2', itemCode: 'FEET', textBN: '' })]),
    );
    expect(blockers).toEqual([
      { kind: 'one_language', itemCode: 'FEET', missing: 'bn', ordering: 2 },
    ]);
  });

  it('refuses an empty checklist, which reads as finished the moment it opens', () => {
    expect(publishBlockers([])).toEqual([{ kind: 'no_items' }]);
    expect(readyToPublish([])).toBe(false);
  });

  it('accepts a fully bilingual list', () => {
    expect(readyToPublish(draftFrom([row()]))).toBe(true);
  });

  it('reads a saved version’s items as well as a draft’s', () => {
    // The same rule against both shapes, so the guidance an author sees before publishing and
    // the attribution shown after a 422 cannot disagree.
    expect(publishBlockers([item({ text_bn: '' })])).toEqual([
      { kind: 'one_language', itemCode: 'DIET', missing: 'bn', ordering: 1 },
    ]);
  });
});

describe('the editor’s rows', () => {
  it('writes ordering from the array position, not from what was stored', () => {
    const stored = version({
      items: [item({ item_code: 'FEET', ordering: 9 }), item({ item_code: 'DIET', ordering: 1 })],
    });
    const rows = rowsFrom(stored);
    // Read back in the server's order…
    expect(rows.map((one) => one.itemCode)).toEqual(['DIET', 'FEET']);
    // …then renumbered from the array, so a move actually moves.
    const moved = moveRow(rows, 0, 1);
    expect(draftFrom(moved).map((one) => [one.item_code, one.ordering])).toEqual([
      ['FEET', 1],
      ['DIET', 2],
    ]);
  });

  it('gives each row its own key, so typing in a new one does not lose focus', () => {
    const rows = rowsFrom(version({ items: [item(), item({ item_code: 'FEET' })] }));
    expect(new Set(rows.map((one) => one.key)).size).toBe(2);
    expect(emptyItemRow('NUTRITION_ROOM').key).not.toBe(rows[0]?.key);
  });

  it('starts a new item unticked, because mandatory is a clinical decision', () => {
    // §5.5's gate reads this flag. A row that arrived pre-ticked would put that decision
    // into a default nobody made.
    expect(emptyItemRow('COUNSELING_ROOM').mandatory).toBe(false);
    expect(emptyItemRow('COUNSELING_ROOM').room).toBe('COUNSELING_ROOM');
  });

  it('refuses to wrap a move off either end', () => {
    const rows = [row({ key: 'a' }), row({ key: 'b' })];
    expect(moveRow(rows, 0, -1).map((one) => one.key)).toEqual(['a', 'b']);
    expect(moveRow(rows, 1, 1).map((one) => one.key)).toEqual(['a', 'b']);
    expect(moveRow(rows, 5, 1).map((one) => one.key)).toEqual(['a', 'b']);
    expect(moveRow(rows, 0, 1).map((one) => one.key)).toEqual(['b', 'a']);
  });

  it('removes by position and leaves an out-of-range list alone', () => {
    const rows = [row({ key: 'a' }), row({ key: 'b' })];
    expect(removeRow(rows, 0).map((one) => one.key)).toEqual(['b']);
    expect(removeRow(rows, 7)).toHaveLength(2);
  });

  it('drops empty guidance rather than sending it as an empty string', () => {
    const [sent] = draftFrom([row({ guidanceEN: '  ', guidanceBN: 'কীভাবে বলবেন' })]);
    expect(sent).not.toHaveProperty('guidance_en');
    expect(sent?.guidance_bn).toBe('কীভাবে বলবেন');
  });

  it('suggests a code from the English text and steps around one already taken', () => {
    expect(suggestItemCode('Insulin injection technique')).toBe('INSULIN_INJECTION_TECHNIQUE');
    expect(suggestItemCode('Foot care', ['FOOT_CARE'])).toBe('FOOT_CARE_2');
    expect(suggestItemCode('  ')).toBe('');
    // Four words at most: a code is an identifier somebody reads in an audit entry, not a
    // sentence.
    expect(suggestItemCode('one two three four five six')).toBe('ONE_TWO_THREE_FOUR');
  });
});

describe('grouping by room', () => {
  const items = [
    item({ item_code: 'INSULIN', room: 'INSULIN_CORNER', ordering: 5 }),
    item({ item_code: 'PORTION', room: 'NUTRITION_ROOM', ordering: 3 }),
    item({ item_code: 'DIET', room: 'COUNSELING_ROOM', ordering: 2 }),
    item({ item_code: 'FEET', room: 'COUNSELING_ROOM', ordering: 1 }),
  ];

  it('puts the rooms in the configured sequence, whatever order the items arrive in', () => {
    // The grouping is §5.2's flow: one room's items, then walk the patient to the next.
    expect(groupByRoom(items, ROOMS).map((group) => group.code)).toEqual([
      'COUNSELING_ROOM',
      'NUTRITION_ROOM',
      'INSULIN_CORNER',
    ]);
  });

  it('orders the items within a room by their ordering', () => {
    const [first] = groupByRoom(items, ROOMS);
    expect(first?.items.map((one) => one.item_code)).toEqual(['FEET', 'DIET']);
  });

  it('gives an empty room no heading', () => {
    const groups = groupByRoom([item({ room: 'COUNSELING_ROOM' })], ROOMS);
    expect(groups).toHaveLength(1);
  });

  it('still shows an item whose room the vocabulary does not list', () => {
    // An item that silently vanished from the screen is an item nobody covers and nobody
    // misses. It comes last, marked, rather than being dropped.
    const groups = groupByRoom([...items, item({ item_code: 'ODD', room: 'CORRIDOR' })], ROOMS);
    expect(groups.at(-1)).toMatchObject({ code: 'CORRIDOR', room: undefined });
    expect(groups.at(-1)?.items).toHaveLength(1);
  });

  it('sorts items without regrouping them', () => {
    expect(itemsInOrder(items).map((one) => one.item_code)).toEqual([
      'FEET',
      'DIET',
      'PORTION',
      'INSULIN',
    ]);
  });

  it('reads the mandatory flag off the column rather than a list here', () => {
    expect(
      mandatoryItems([item({ mandatory: false }), item({ item_code: 'X', mandatory: true })]),
    ).toHaveLength(1);
  });
});

describe('cache keys', () => {
  it('spells each one the same way for every caller', () => {
    // Publishing moves three of these at once and the screens that read them are in
    // different files — the situation where two spellings leave a physician looking at a
    // list that still says version 2 is live.
    expect(counselingVersionsKey(TEMPLATE)).toEqual(['counseling', 'versions', TEMPLATE]);
    expect(counselingVersionKey(TEMPLATE, 3)).toEqual(['counseling', 'version', TEMPLATE, 3]);
    expect(counselingAssignmentsKey()).toEqual(['counseling', 'assignments', null]);
    expect(counselingAssignmentsKey({ code: 'E11' })).toEqual([
      'counseling',
      'assignments',
      { code: 'E11' },
    ]);
  });

  it('points at this deployment’s API', () => {
    expect(API_BASE_URL).toBeTruthy();
  });
});
