import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { STEP_UP_HEADER } from '@/features/auth';
import { api, unwrap } from '@/lib/api';

/**
 * The counselling checklist, authored rather than released (CP55, §5.1, [R-07]).
 *
 * Nine calls and a set of pure rules. Five of the rules are the checkpoint, and each one is
 * a place where the obvious convenience would break the thing the versioning exists for.
 *
 * **A published version is frozen, and nothing here pretends otherwise.** `saveDraftItems`
 * against a published version answers `409`, and that is not an error to route around: a
 * completed session retains the version it used, so editing a published checklist rewrites
 * what every counsellor was asked to cover last October and nothing afterwards looks wrong.
 * `isEditable` is the single answer to "may this be changed", and the screens ask it rather
 * than reading `status` themselves — because the tempting reading, "not RETIRED", is wrong
 * in exactly the case that matters.
 *
 * **Saving and publishing are two acts and are two functions.** They do not share a
 * signature: `publishVersion` cannot be called without a step-up token, so a caller cannot
 * arrive at it by adding a flag to a save. Publishing puts a checklist on every phone on the
 * floor within seconds, freezes it forever and retires the current one; a physician fixing a
 * typo must not be able to do the other thing by pressing the nearer button.
 *
 * **Half-written is a legitimate draft, so nothing here refuses one.** `saveBlockers` names
 * only what the *server* would refuse a save for — a missing item code, a duplicated one, a
 * row with no text in either language, an unknown room. An item with English and no Bengali
 * is not on that list, because that is what writing looks like. It is on `publishBlockers`,
 * which is the same rule the publish endpoint and the database trigger apply, mirrored so a
 * screen can name the offending item before the author spends a round trip finding out.
 *
 * **The server's publish refusal names `items`, not an item.** `ErrNotBilingual` comes back
 * as one field message against the whole list, which is correct for an API and useless on a
 * screen with nine rows. So `publishBlockers` is what turns it into "item 4, Bengali
 * missing" — the refusal is the server's, the attribution is ours, and the two are shown
 * together rather than one instead of the other.
 *
 * **`approved_at` and `published_source` are reported, never rounded up.** The seeded
 * diabetes version has a null `approved_at` (D-53 is open: those seven items are transcribed
 * from §5.1, and their Bengali and guidance are an engineer's) and a `published_source` of
 * `MIGRATION`, because no person published it. `contentApproved` and `publishedBySystem`
 * exist so that no screen answers either question by testing a timestamp for truthiness and
 * quietly drawing a proposal as a clinician's approved list, or a migration as a blank author.
 */

export type CounselingRoom = components['schemas']['CounselingRoom'];
export type CounselingItem = components['schemas']['CounselingItem'];
export type CounselingItemDraft = components['schemas']['CounselingItemDraft'];
export type CounselingVersion = components['schemas']['CounselingVersion'];
export type CounselingTemplate = components['schemas']['CounselingTemplate'];
export type CounselingAssignment = components['schemas']['CounselingAssignment'];
export type CounselingMatch = components['schemas']['CounselingMatch'];

/** DRAFT → PUBLISHED → RETIRED, and nowhere else. There is no un-publishing. */
export type VersionStatus = CounselingVersion['status'];
/** The three rooms §5.2 walks through. The vocabulary is the server's; this is its shape. */
export type RoomCode = CounselingRoom['room'];
/** Whether a person published a version, or a migration did. Never inferred from a blank. */
export type PublishedSource = NonNullable<CounselingVersion['published_source']>;

/**
 * The cache keys, held beside the calls.
 *
 * Publishing writes three of them at once — the version, the template's version list, and
 * the template listing whose `published_version` column just changed — and the screens that
 * read them are in different files. That is exactly the situation where two spellings of one
 * key leaves a physician looking at a list that still says version 2 is live.
 */
export const COUNSELING_ROOMS_KEY = ['counseling', 'rooms'] as const;
export const COUNSELING_TEMPLATES_KEY = ['counseling', 'templates'] as const;

export function counselingVersionsKey(templateId: string) {
  return ['counseling', 'versions', templateId] as const;
}

export function counselingVersionKey(templateId: string, version: number) {
  return ['counseling', 'version', templateId, version] as const;
}

export function counselingAssignmentsKey(coding?: {
  system?: string;
  version?: string;
  code?: string;
}) {
  return ['counseling', 'assignments', coding ?? null] as const;
}

/**
 * The rooms, in the configured sequence.
 *
 * §5.2's flow as data. A screen groups by these rather than by a list of its own, because
 * the grouping *is* the flow — a counsellor works through one room, walks the patient to the
 * next, and a room order invented in a component is one that stops matching the corridor the
 * first time the clinic moves the insulin corner.
 */
export async function listCounselingRooms(): Promise<CounselingRoom[]> {
  const body = await unwrap(api.GET('/v1/counseling/rooms'));
  return body.rooms;
}

/**
 * Every checklist, with the version a new session would get.
 *
 * Templates with nothing published are in the list too. They are work in progress, and an
 * author who cannot see their own unpublished template has no way back to it.
 */
export async function listCounselingTemplates(): Promise<CounselingTemplate[]> {
  const body = await unwrap(api.GET('/v1/counseling/templates'));
  return body.templates;
}

/**
 * Which diagnosis calls for which checklist — and, given a coding, what it would get.
 *
 * The authoring screen asks the second question to let an author check their own rule
 * against a real code before a counsellor meets it. Only published versions come back: a
 * draft is not something to hand somebody on the floor.
 */
export async function listCounselingAssignments(
  coding: { system?: string; version?: string; code?: string } = {},
): Promise<{ assignments: CounselingAssignment[]; matches: CounselingMatch[] }> {
  const body = await unwrap(
    api.GET('/v1/counseling/assignments', {
      params: {
        query: {
          ...(coding.system === undefined ? {} : { system: coding.system }),
          ...(coding.version === undefined ? {} : { version: coding.version }),
          ...(coding.code === undefined ? {} : { code: coding.code }),
        },
      },
    }),
  );
  return { assignments: body.assignments ?? [], matches: body.matches ?? [] };
}

/**
 * Start a new checklist. Criterion 1, in one call.
 *
 * The server creates the first draft version alongside the template, so the caller never
 * has to hold a template that cannot be edited or published.
 */
export async function createCounselingTemplate(input: {
  code: string;
  title_en: string;
  title_bn: string;
}): Promise<CounselingTemplate> {
  const body = await unwrap(
    api.POST('/v1/counseling/templates', {
      params: writing(),
      // Upper-cased here as well as on the server, so the code the author sees on the
      // confirmation is the code the assignment rules and the audit entries will name.
      body: { ...input, code: input.code.trim().toUpperCase() },
    }),
  );
  return body.template;
}

/** Every revision of one checklist, newest first, as the server orders them. */
export async function listCounselingVersions(templateId: string): Promise<CounselingVersion[]> {
  const body = await unwrap(
    api.GET('/v1/counseling/templates/{templateId}', { params: { path: { templateId } } }),
  );
  return body.versions;
}

/** One revision with its items, in working order. */
export async function getCounselingVersion(
  templateId: string,
  version: number,
): Promise<CounselingVersion> {
  const body = await unwrap(
    api.GET('/v1/counseling/templates/{templateId}/versions/{version}', {
      params: { path: { templateId, version } },
    }),
  );
  return body.version;
}

/**
 * Open a new draft — the only way to change a published checklist.
 *
 * The server copies the current published version's items, which is what an author almost
 * always wants: a new version exists to change one thing. The notes say why it exists, and
 * are read by whoever compares two versions a year from now.
 */
export async function draftCounselingVersion(
  templateId: string,
  notes?: string,
): Promise<CounselingVersion> {
  const trimmed = notes?.trim() ?? '';
  const body = await unwrap(
    api.POST('/v1/counseling/templates/{templateId}/versions', {
      params: { ...writing(), path: { templateId } },
      body: trimmed === '' ? {} : { notes: trimmed },
    }),
  );
  return body.version;
}

/**
 * Save a draft's items, wholesale.
 *
 * The whole list is sent because the server replaces the whole list. A patch would leave the
 * stored version disagreeing with the screen the author is looking at, and the way that
 * surfaces is somebody publishing an item they thought they had deleted, on a checklist a
 * counsellor then reads to a patient.
 *
 * Answers `409` for a published version. The screens never make that call — they offer
 * "draft a new version" instead — but the call is honest about it rather than swallowing it,
 * because the version can be published by somebody else between the screen loading and the
 * author pressing save.
 */
export async function saveCounselingDraft(
  templateId: string,
  version: number,
  draft: { notes?: string; items: CounselingItemDraft[] },
): Promise<CounselingVersion> {
  const notes = draft.notes?.trim() ?? '';
  const body = await unwrap(
    api.PUT('/v1/counseling/templates/{templateId}/versions/{version}', {
      params: { ...writing(), path: { templateId, version } },
      body: { ...(notes === '' ? {} : { notes }), items: draft.items },
    }),
  );
  return body.version;
}

/**
 * Put a checklist on the floor, and freeze it.
 *
 * The token is the first parameter and is not optional, so this cannot be reached by adding
 * an argument to a save. It is minted for `counseling.publish` and for nothing else: a token
 * good for resetting a password must not be spendable on changing what every counsellor asks
 * every patient tomorrow.
 */
export async function publishCounselingVersion(
  stepUpToken: string,
  templateId: string,
  version: number,
): Promise<CounselingVersion> {
  const body = await unwrap(
    api.POST('/v1/counseling/templates/{templateId}/versions/{version}/publish', {
      params: { ...writing({ [STEP_UP_HEADER]: stepUpToken }), path: { templateId, version } },
    }),
  );
  return body.version;
}

/**
 * Whether this version may be changed at all.
 *
 * The one answer to that question, so no screen reaches its own conclusion from `status`.
 * The tempting reading is `status !== 'RETIRED'`, which is wrong in precisely the case the
 * whole checkpoint is about: a published version is live, looks like the current thing, and
 * is the one that must never be edited.
 */
export function isEditable(version: CounselingVersion): boolean {
  return version.status === 'DRAFT';
}

/** Whether this is the version a new session gets. */
export function isPublished(version: CounselingVersion): boolean {
  return version.status === 'PUBLISHED';
}

/**
 * Whether a clinician has signed off on the content.
 *
 * A predicate rather than a truthiness test on the timestamp, because the answer decides
 * whether a screen may present a checklist as settled. **D-53 is open** for the seeded
 * diabetes version: its seven items are transcribed from §5.1 and are the launch minimum,
 * and their Bengali and guidance text are the engineer's. `false` here is a fact worth
 * printing, not an absence to leave blank.
 */
export function contentApproved(subject: { approved_at?: string | null }): boolean {
  const at = subject.approved_at;
  return typeof at === 'string' && at.trim() !== '';
}

/**
 * Whether a migration published this version rather than a person.
 *
 * The seeded diabetes version carries `published_source: 'MIGRATION'` and no `published_by`,
 * because nobody published it — and an invented user id there would be the only attribution
 * in this system naming somebody who did not do the thing. A screen that read only
 * `published_by` would draw the same version with an empty author, which reads as data
 * missing rather than as a fact.
 */
export function publishedBySystem(version: CounselingVersion): boolean {
  return version.published_source === 'MIGRATION';
}

/** The version a new session would get, if any. Never guessed from the highest number. */
export function publishedVersionOf(
  versions: readonly CounselingVersion[],
): CounselingVersion | undefined {
  return versions.find(isPublished);
}

/** The drafts, newest first. What an author has open and unfinished. */
export function draftsOf(versions: readonly CounselingVersion[]): CounselingVersion[] {
  return versions.filter(isEditable);
}

/**
 * The version a workspace should open on.
 *
 * The newest draft when there is one, because an author who left something half-written is
 * almost certainly coming back to it; otherwise the published version; otherwise the newest
 * thing there is. Never simply "the first in the list": the list is newest-first, and the
 * newest version of a template somebody published last week is a retired one.
 */
export function versionToOpen(
  versions: readonly CounselingVersion[],
): CounselingVersion | undefined {
  return draftsOf(versions)[0] ?? publishedVersionOf(versions) ?? versions[0];
}

/**
 * One item, as the editor holds it while it is being written.
 *
 * `key` is client-side identity and is never sent. Rows are reordered, inserted and deleted,
 * and React keyed on the item code would lose an input's focus and its cursor the moment the
 * author typed the first letter of a new item's code — on a form whose whole job is typing.
 *
 * Every text field is a string, including the ones the contract calls optional: "" and "not
 * written yet" are the same state on a form, and the difference is made when the request is
 * built.
 */
export interface ItemDraftRow {
  key: string;
  itemCode: string;
  /**
   * Whether the code is still ours to keep in step with the English text.
   *
   * True on a row nobody has given a code to, false the moment an author types one and false
   * on every row read back from a saved version. The distinction is the difference between a
   * guess and a decision: the code is what a tick references, so silently rewriting one the
   * author chose — or one a past session already points at — detaches history that nothing on
   * screen would show as detached.
   */
  codeSuggested: boolean;
  textEN: string;
  textBN: string;
  guidanceEN: string;
  guidanceBN: string;
  mandatory: boolean;
  room: string;
}

let rowCounter = 0;

/** A fresh row's client-side key. Monotonic rather than random: a test can read it. */
function nextRowKey(): string {
  rowCounter += 1;
  return `row-${rowCounter}`;
}

/**
 * A blank item, in the room the author was last working in.
 *
 * Not mandatory by default. Which items are mandatory is a clinical decision that §5.5's gate
 * reads, and a row that arrived pre-ticked would put that decision into a default nobody made
 * — on the flag that decides whether a counsellor may close a session.
 */
export function emptyItemRow(room: string): ItemDraftRow {
  return {
    key: nextRowKey(),
    itemCode: '',
    codeSuggested: true,
    textEN: '',
    textBN: '',
    guidanceEN: '',
    guidanceBN: '',
    mandatory: false,
    room,
  };
}

/** A saved version's items as editable rows, in the order the server stored them. */
export function rowsFrom(version: CounselingVersion): ItemDraftRow[] {
  return itemsInOrder(version.items ?? []).map((item) => ({
    key: nextRowKey(),
    itemCode: item.item_code,
    // A stored code is a decision, and past ticks reference it. Never re-derived.
    codeSuggested: false,
    textEN: item.text_en,
    textBN: item.text_bn,
    guidanceEN: item.guidance_en ?? '',
    guidanceBN: item.guidance_bn ?? '',
    mandatory: item.mandatory,
    room: item.room,
  }));
}

/**
 * The rows as the contract's request body.
 *
 * `ordering` is written from the array position rather than carried, because the array *is*
 * the order the author arranged: a stored ordering carried through a move would put the item
 * back where it was. Empty guidance is dropped rather than sent as "", which is the same
 * distinction the record-an-allergy form makes and for the same reason.
 */
export function draftFrom(rows: readonly ItemDraftRow[]): CounselingItemDraft[] {
  return rows.map((row, index) => ({
    item_code: row.itemCode.trim().toUpperCase(),
    ordering: index + 1,
    text_en: row.textEN.trim(),
    text_bn: row.textBN.trim(),
    ...(row.guidanceEN.trim() === '' ? {} : { guidance_en: row.guidanceEN.trim() }),
    ...(row.guidanceBN.trim() === '' ? {} : { guidance_bn: row.guidanceBN.trim() }),
    mandatory: row.mandatory,
    room: row.room,
  }));
}

/**
 * A row moved one place, as a new array.
 *
 * The keyboard reorder controls are the required ones and drag is the extra, not the other
 * way round: a pointer drag is unusable by keyboard, unreliable on a touch screen held in
 * one hand, and impossible to describe to a screen reader. So the move is a pure function
 * two buttons call, and it is the same function a drag would end in.
 *
 * A move off either end returns the same array rather than wrapping. Wrapping would send the
 * first item to the bottom of a nine-item checklist on a press somebody made by accident.
 */
export function moveRow(
  rows: readonly ItemDraftRow[],
  index: number,
  delta: number,
): ItemDraftRow[] {
  const target = index + delta;
  if (index < 0 || index >= rows.length || target < 0 || target >= rows.length) return [...rows];
  const next = [...rows];
  const [moved] = next.splice(index, 1);
  if (moved === undefined) return [...rows];
  next.splice(target, 0, moved);
  return next;
}

/** A row removed, as a new array. Out-of-range leaves the list alone. */
export function removeRow(rows: readonly ItemDraftRow[], index: number): ItemDraftRow[] {
  if (index < 0 || index >= rows.length) return [...rows];
  return rows.filter((_, position) => position !== index);
}

/**
 * A stable code suggested from the English text.
 *
 * Suggested, never imposed, and only for a row that has no code yet. The code is what a tick
 * references and what survives a rewording, so it is the author's to decide — but making
 * them invent `INSULIN_TECHNIQUE` by hand for every item is how a checklist ends up with
 * `ITEM1` through `ITEM7`, which tells nobody anything a year later.
 */
export function suggestItemCode(textEN: string, taken: readonly string[] = []): string {
  const base = textEN
    .trim()
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .split('_')
    .filter((word) => word !== '')
    .slice(0, 4)
    .join('_');
  if (base === '') return '';
  const used = new Set(taken.map((code) => code.trim().toUpperCase()));
  if (!used.has(base)) return base;
  for (let suffix = 2; suffix < 100; suffix += 1) {
    const candidate = `${base}_${suffix}`;
    if (!used.has(candidate)) return candidate;
  }
  return base;
}

/**
 * What the *server* would refuse a save for, named as the server names it.
 *
 * Deliberately short, and deliberately missing the bilingual rule. These four are the ones
 * `SaveDraft` rejects: a blank code, a code used twice — which would make a tick ambiguous,
 * and the ambiguity would surface only as a mandatory-item gate that never closes — a row
 * with no text in either language, and a room the vocabulary does not have.
 *
 * Anything else half-written saves, because that is what writing looks like.
 */
export type SaveBlocker =
  | { kind: 'no_code'; index: number }
  | { kind: 'duplicate_code'; index: number; itemCode: string }
  | { kind: 'no_text'; index: number }
  | { kind: 'unknown_room'; index: number; room: string };

export function saveBlockers(
  rows: readonly ItemDraftRow[],
  rooms: readonly CounselingRoom[],
): SaveBlocker[] {
  const known = new Set<string>(rooms.map((room) => room.room));
  const seen = new Set<string>();
  const blockers: SaveBlocker[] = [];

  rows.forEach((row, index) => {
    const code = row.itemCode.trim().toUpperCase();
    if (code === '') blockers.push({ kind: 'no_code', index });
    else if (seen.has(code)) blockers.push({ kind: 'duplicate_code', index, itemCode: code });
    else seen.add(code);

    if (row.textEN.trim() === '' && row.textBN.trim() === '')
      blockers.push({ kind: 'no_text', index });

    // An empty room only when the vocabulary itself failed to load; either way the server
    // would refuse, and saying which row is more use than a bare 422.
    if (!known.has(row.room)) blockers.push({ kind: 'unknown_room', index, room: row.room });
  });

  return blockers;
}

/**
 * What the publish endpoint would refuse, named against the item rather than the list.
 *
 * Criterion 4, mirrored. The server checks the identical rule and so does a database
 * trigger, and the server's answer arrives as one message against the field `items` — which
 * is right for an API and useless on a screen with nine rows, where the author needs to know
 * that it is the fourth one and that the missing half is the Bengali.
 *
 * So this runs twice: once before the button is pressed, as guidance, and once when the 422
 * comes back, to say which item the server meant. It never blocks a *save*.
 */
export type PublishBlocker =
  | { kind: 'no_items' }
  | { kind: 'one_language'; itemCode: string; missing: 'en' | 'bn'; ordering: number };

export function publishBlockers(items: readonly CounselingItemDraft[]): PublishBlocker[] {
  if (items.length === 0) return [{ kind: 'no_items' }];
  const blockers: PublishBlocker[] = [];
  items.forEach((item, index) => {
    const ordering = item.ordering ?? index + 1;
    if (item.text_en.trim() === '')
      blockers.push({ kind: 'one_language', itemCode: item.item_code, missing: 'en', ordering });
    if (item.text_bn.trim() === '')
      blockers.push({ kind: 'one_language', itemCode: item.item_code, missing: 'bn', ordering });
  });
  return blockers;
}

/** Whether this list would be accepted by the publish endpoint. */
export function readyToPublish(items: readonly CounselingItemDraft[]): boolean {
  return publishBlockers(items).length === 0;
}

/** The items in the order a counsellor works through them. */
export function itemsInOrder(items: readonly CounselingItem[]): CounselingItem[] {
  return [...items].sort((a, b) => a.ordering - b.ordering);
}

/**
 * One room's worth of a checklist.
 *
 * `room` is undefined when the server sent an item for a room the vocabulary does not list —
 * which happens if a room is added to the data and the client is a version behind. The group
 * is still returned, with its code, because an item that silently vanished from the screen is
 * an item nobody covers and nobody misses.
 */
export interface RoomGroup {
  code: string;
  room: CounselingRoom | undefined;
  items: CounselingItem[];
}

/**
 * The checklist grouped by room, in the configured sequence.
 *
 * The grouping is §5.2's flow: a counsellor works through one room's items, walks the patient
 * to the next, and the screen should follow them rather than making them hunt. So the rooms
 * come out in the server's `ordering` and the items in theirs.
 *
 * A room with no items gets no heading. This is the opposite of the medical history screen's
 * answer and for a reason that is opposite too: there, an empty group means a question the
 * desk has not asked yet, and hiding it hides work outstanding. Here, a checklist with
 * nothing for the nutrition room is a finished checklist that does not use that room, and an
 * empty heading on a counsellor's phone reads as items that failed to load.
 */
export function groupByRoom(
  items: readonly CounselingItem[],
  rooms: readonly CounselingRoom[],
): RoomGroup[] {
  const ordered = [...rooms].sort((a, b) => a.ordering - b.ordering);
  const groups: RoomGroup[] = [];

  for (const room of ordered) {
    const mine = itemsInOrder(items.filter((item) => item.room === room.room));
    if (mine.length > 0) groups.push({ code: room.room, room, items: mine });
  }

  const known = new Set<string>(ordered.map((room) => room.room));
  const strays = items.filter((item) => !known.has(item.room));
  for (const code of [...new Set(strays.map((item) => item.room))]) {
    groups.push({
      code,
      room: undefined,
      items: itemsInOrder(strays.filter((item) => item.room === code)),
    });
  }

  return groups;
}

/** The items §5.5's gate reads. A clinical decision held in a column, not in a list here. */
export function mandatoryItems(items: readonly CounselingItem[]): CounselingItem[] {
  return items.filter((item) => item.mandatory);
}
