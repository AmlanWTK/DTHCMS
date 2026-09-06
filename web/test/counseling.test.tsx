import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@/lib/api';
import type {
  CounselingItem,
  CounselingRoom,
  CounselingTemplate,
  CounselingVersion,
} from '@/features/counseling/api/counseling';

import { renderWithProviders } from './render';

/**
 * The counselling authoring screens (CP55, §5.1, [R-07]).
 *
 * The manual verification for this checkpoint is Dr. Nahid creating a thyroid checklist,
 * publishing it, and it reaching a counsellor's phone with no developer involved. What can
 * only be proven here is whether the physician doing that ends up believing the right things
 * — and every way that fails is quiet.
 *
 *  - **A published version offering somewhere to type.** The version is frozen by a database
 *    trigger and an edit answers 409, so nothing here is the enforcement. What an editor with
 *    disabled inputs *would* do is teach an author that the block is temporary and set them
 *    hunting for the state in which it lifts. There is a named test below whose entire job is
 *    to fail if a textbox, a combobox or a checkbox ever appears inside a published version.
 *  - **Publishing by accident.** Saving is cheap and publishing puts a checklist on every
 *    phone on the floor and freezes it forever. The first press must call nothing; the
 *    confirmation must say what will happen, including the part people are surprised by —
 *    that the current version is retired.
 *  - **A form that fights the person writing in it.** An item with English and no Bangla is
 *    what a half-written draft looks like. It saves. The gap is named as guidance, and it is
 *    named again — against the item, which the server's own 422 does not do — when the
 *    publish is refused.
 *  - **A proposal drawn as a clinician's list.** D-53 is open: the seeded diabetes items are
 *    transcribed from the plan and their Bangla is an engineer's. That checklist is live *and*
 *    unapproved, and both facts have to be readable at once.
 *  - **A migration drawn as a missing author.** `published_source: MIGRATION` means no person
 *    published it. A blank reads as data missing; this must read as a fact.
 *  - **An order nobody can change without a mouse.** Reordering is content, not decoration,
 *    and it has to work from a keyboard.
 */

const listCounselingRooms = vi.hoisted(() => vi.fn());
const listCounselingTemplates = vi.hoisted(() => vi.fn());
const listCounselingVersions = vi.hoisted(() => vi.fn());
const getCounselingVersion = vi.hoisted(() => vi.fn());
const createCounselingTemplate = vi.hoisted(() => vi.fn());
const draftCounselingVersion = vi.hoisted(() => vi.fn());
const saveCounselingDraft = vi.hoisted(() => vi.fn());
const publishCounselingVersion = vi.hoisted(() => vi.fn());

// Partial: the network calls are stubbed, the pure rules are not. `groupByRoom`,
// `publishBlockers`, `saveBlockers`, `moveRow` and `draftFrom` are what these screens
// actually do, and a test that stubbed them would prove the components call a function
// rather than that the right words reach the physician.
vi.mock('@/features/counseling/api/counseling', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/counseling/api/counseling')>()),
  listCounselingRooms,
  listCounselingTemplates,
  listCounselingVersions,
  getCounselingVersion,
  createCounselingTemplate,
  draftCounselingVersion,
  saveCounselingDraft,
  publishCounselingVersion,
}));

const requestStepUp = vi.hoisted(() => vi.fn());

vi.mock('@/features/auth', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/auth')>()),
  useStepUp: () => requestStepUp,
}));

const { TemplateList } = await import('@/features/counseling/components/TemplateList');
const { TemplateWorkspace } = await import('@/features/counseling/components/TemplateWorkspace');
const { VersionView } = await import('@/features/counseling/components/VersionView');
const { CounsellorPreview } = await import('@/features/counseling/components/CounsellorPreview');
// The barrel, imported as a caller would.
const surface = await import('@/features/counseling');
const { useSessionStore } = await import('@/stores/session');

const TEMPLATE = '0190a8f2-0000-7000-8000-0000000000d1';
const THYROID = '0190a8f2-0000-7000-8000-0000000000d2';
const PHYSICIAN = '0190a8f2-0000-7000-8000-0000000000c9';

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

/** The seeded diabetes checklist: published by a migration, and not approved by anybody. */
const SEEDED: CounselingVersion = {
  template_id: TEMPLATE,
  version: 1,
  status: 'PUBLISHED',
  created_at: '2026-01-01T00:00:00Z',
  published_at: '2026-01-01T00:00:00Z',
  published_source: 'MIGRATION',
  notes: 'Transcribed from the clinic plan.',
  items: [
    item({ item_code: 'INSULIN_TECHNIQUE', room: 'INSULIN_CORNER', ordering: 5 }),
    item({
      item_code: 'PORTION',
      room: 'NUTRITION_ROOM',
      ordering: 3,
      text_en: 'Portion sizes',
      text_bn: 'খাবারের পরিমাণ',
    }),
    item({ item_code: 'DIET', room: 'COUNSELING_ROOM', ordering: 2 }),
    item({
      item_code: 'FOOT_CARE',
      room: 'COUNSELING_ROOM',
      ordering: 1,
      text_en: 'Foot care',
      text_bn: 'পায়ের যত্ন',
      mandatory: false,
    }),
  ],
};

/** A draft of the thyroid checklist, one item of which has no Bangla yet. */
const HALF_WRITTEN: CounselingVersion = {
  template_id: THYROID,
  version: 1,
  status: 'DRAFT',
  created_at: '2026-09-02T04:00:00Z',
  items: [
    item({
      item_code: 'TAKE_ON_EMPTY_STOMACH',
      ordering: 1,
      text_en: 'Take levothyroxine on an empty stomach',
      text_bn: 'খালি পেটে লেভোথাইরক্সিন খাবেন',
      room: 'COUNSELING_ROOM',
    }),
    item({
      item_code: 'IODINE_FOODS',
      ordering: 2,
      text_en: 'Iodine-rich foods',
      text_bn: '',
      room: 'NUTRITION_ROOM',
      mandatory: false,
    }),
  ],
};

function template(over: Partial<CounselingTemplate> = {}): CounselingTemplate {
  return {
    id: TEMPLATE,
    code: 'DIABETES',
    title_en: 'Diabetes counselling',
    title_bn: 'ডায়াবেটিস কাউন্সেলিং',
    published_version: 1,
    published_at: '2026-01-01T00:00:00Z',
    latest_version: 1,
    draft_count: 0,
    ...over,
  };
}

const THYROID_TEMPLATE = template({
  id: THYROID,
  code: 'THYROID',
  title_en: 'Thyroid counselling',
  title_bn: 'থাইরয়েড কাউন্সেলিং',
  draft_count: 1,
});
delete (THYROID_TEMPLATE as { published_version?: number }).published_version;
delete (THYROID_TEMPLATE as { published_at?: string }).published_at;

const initialSession = useSessionStore.getInitialState();

/** A physician: may read, write and publish a checklist. */
function holding(permissions: string[]) {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: PHYSICIAN,
      employeeCode: 'E010',
      nameEN: 'Dr Nahid',
      nameBN: 'ডা. নাহিদ',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: ['PHYSICIAN'],
      grants: { PHYSICIAN: permissions },
      permissions,
      secondFactor: { required: false, enrolled: true, pending: false, recoveryCodesLeft: 8 },
    },
    activeRole: 'PHYSICIAN',
  });
}

const ALL = [
  'counseling.template.read',
  'counseling.template.write',
  'counseling.template.publish',
];

beforeEach(() => {
  // jsdom has no <dialog>.showModal. Give it one that toggles `open`, which is all the
  // publish confirmation relies on.
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function close() {
    this.removeAttribute('open');
  };

  vi.clearAllMocks();
  holding(ALL);
  requestStepUp.mockResolvedValue('step-up-token');
  listCounselingRooms.mockResolvedValue(ROOMS);
  listCounselingTemplates.mockResolvedValue([template(), THYROID_TEMPLATE]);
  listCounselingVersions.mockResolvedValue([SEEDED]);
  getCounselingVersion.mockResolvedValue(SEEDED);
});

afterEach(() => {
  useSessionStore.setState(initialSession, true);
  vi.restoreAllMocks();
});

async function openWorkspace(
  templateId = TEMPLATE,
  locale: 'en' | 'bn' = 'en',
  initialVersion?: number,
) {
  renderWithProviders(
    <TemplateWorkspace
      templateId={templateId}
      {...(initialVersion === undefined ? {} : { initialVersion })}
    />,
    { locale },
  );
  return screen.findByTestId('template-workspace');
}

describe('the list of checklists', () => {
  it('says which version is live and how many drafts are open', async () => {
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');

    expect(screen.getByTestId('template-state-DIABETES')).toHaveTextContent('Version 1 is live');
    expect(screen.getByTestId('template-drafts-DIABETES')).toHaveTextContent('No draft open');
    // A template with nothing published is work in progress, not a broken row. An author who
    // cannot see their own unpublished work has no way back to it.
    expect(screen.getByTestId('template-unpublished-THYROID')).toBeInTheDocument();
    expect(screen.getByTestId('template-drafts-THYROID')).toHaveTextContent('1 draft open');
  });

  it('marks the seeded checklist as content nobody has approved (D-53)', async () => {
    // The named test for the open decision. That checklist is live and it is a proposal:
    // the items are transcribed from §5.1 as a launch minimum, and their Bangla and their
    // guidance were written by the engineer. A list that showed only "live" would present it
    // as settled.
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');

    expect(screen.getByTestId('template-unapproved-DIABETES')).toHaveTextContent(
      'Content not approved by a clinician',
    );
    // And both facts are readable at once, rather than one replacing the other.
    expect(screen.getByTestId('template-state-DIABETES')).toHaveTextContent('Version 1 is live');
  });

  it('offers to start a new checklist only to somebody who may write one', async () => {
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');
    expect(screen.getByRole('button', { name: 'Start a new checklist' })).toBeInTheDocument();
  });

  it('does not offer to start one to a counsellor who may only read', async () => {
    holding(['counseling.template.read']);
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');
    expect(screen.queryByRole('button', { name: 'Start a new checklist' })).toBeNull();
  });

  it('says so loudly when the list cannot be read', async () => {
    // Not a quiet empty state. "No checklists" and "the checklists could not be read" look
    // identical on screen and mean opposite things, and one of them sends an author off to
    // write a second copy of a list that already exists.
    listCounselingTemplates.mockRejectedValue(new Error('down'));
    renderWithProviders(<TemplateList />);
    expect(await screen.findByText('The counselling checklists could not be read.')).toBeVisible();
  });

  it('creates a thyroid checklist without anybody writing code (criterion 1)', async () => {
    const user = userEvent.setup();
    createCounselingTemplate.mockResolvedValue(THYROID_TEMPLATE);
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');

    await user.click(screen.getByRole('button', { name: 'Start a new checklist' }));
    await user.type(screen.getByTestId('new-template-title-en'), 'Thyroid counselling');
    await user.type(screen.getByTestId('new-template-title-bn'), 'থাইরয়েড কাউন্সেলিং');
    await user.click(screen.getByTestId('new-template-create'));

    await waitFor(() => expect(createCounselingTemplate).toHaveBeenCalled());
    // The code is suggested from the English name and sent upper-cased, so a physician does
    // not have to invent an identifier — and it is the identifier an assignment rule and an
    // audit entry will name.
    expect(createCounselingTemplate.mock.calls[0]![0]).toMatchObject({
      code: 'THYROID_COUNSELLING',
      title_en: 'Thyroid counselling',
      title_bn: 'থাইরয়েড কাউন্সেলিং',
    });
  });

  it('asks for the Bangla name now rather than at publication', async () => {
    const user = userEvent.setup();
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');

    await user.click(screen.getByRole('button', { name: 'Start a new checklist' }));
    await user.type(screen.getByTestId('new-template-title-en'), 'Thyroid');
    await user.click(screen.getByTestId('new-template-create'));

    expect(createCounselingTemplate).not.toHaveBeenCalled();
    expect(screen.getByText('A name in Bangla is required.')).toBeVisible();
  });
});

describe('a published version', () => {
  it('offers no edit affordance', async () => {
    // **The named test.** Criterion 2's interface half. A published version is frozen by a
    // database trigger and editing one answers 409, so nothing on this screen is the
    // enforcement — but an editor rendered with its inputs disabled would teach an author
    // that the refusal is temporary, and send them looking for the state in which it lifts.
    // The model that is true is: published means done, and you revise by drafting. If a
    // textbox, a combobox or a checkbox ever appears inside a published version, this fails.
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />);
    const view = within(await screen.findByTestId('version-view'));

    expect(view.queryAllByRole('textbox')).toEqual([]);
    expect(view.queryAllByRole('combobox')).toEqual([]);
    expect(view.queryAllByRole('checkbox')).toEqual([]);
    expect(view.queryAllByRole('spinbutton')).toEqual([]);

    // And the act that *is* available is on the screen, not hidden behind a menu.
    expect(view.getByTestId('draft-new-version')).toBeInTheDocument();
    expect(view.getByText('This version is finished and cannot be changed.')).toBeVisible();
  });

  it('offers no edit affordance through the workspace either', async () => {
    // The same guarantee where a physician actually meets it: the workspace picks the
    // surface, and a published version must never reach the editor.
    await openWorkspace();
    await screen.findByTestId('version-view');
    expect(screen.queryByTestId('draft-editor')).toBeNull();
    expect(screen.queryByTestId('publish-panel')).toBeNull();
  });

  it('opens a new draft rather than unlocking the published one', async () => {
    const user = userEvent.setup();
    draftCounselingVersion.mockResolvedValue({
      ...HALF_WRITTEN,
      template_id: TEMPLATE,
      version: 2,
    });
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />);

    await user.click(await screen.findByTestId('draft-new-version'));
    await waitFor(() => expect(draftCounselingVersion).toHaveBeenCalledWith(TEMPLATE));
  });

  it('does not offer to draft one to somebody who may only read', async () => {
    holding(['counseling.template.read']);
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />);
    await screen.findByTestId('version-view');
    expect(screen.queryByTestId('draft-new-version')).toBeNull();
  });

  it('says a migration published it, rather than leaving the author blank', async () => {
    // The named test for `published_source: MIGRATION`. Nobody published this version; it
    // arrived with the migration that set the clinic up. A blank author reads as data
    // missing, and an invented user id would be the only attribution in this system naming
    // somebody who did not do the thing.
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />);

    const attribution = await screen.findByTestId('version-attribution');
    expect(attribution).toHaveTextContent('Published with the system');
    expect(attribution.textContent?.trim()).not.toBe('');
    expect(screen.getByTestId('version-migration-note')).toHaveTextContent(
      /No person published this version/,
    );
  });

  it('marks its content as unapproved even though it is live (D-53)', async () => {
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />);
    expect(await screen.findByTestId('version-status')).toHaveTextContent('Live');
    expect(screen.getByText('Content not approved by a clinician')).toBeVisible();
  });

  it('groups the items by room, in the room order the server configured', async () => {
    // §5.2's sequence. The grouping *is* the flow: one room's items, then walk the patient
    // to the next — so the rooms come out in their configured order however the items
    // arrived, and the items within a room in theirs.
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />);
    await screen.findByTestId('version-view');

    const headings = screen.getAllByRole('heading', { level: 4 });
    expect(headings.map((node) => node.textContent)).toEqual([
      'Counselling room',
      'Nutrition room',
      'Insulin corner',
      // The revise card's own heading, which follows the checklist.
      'Draft a new version',
    ]);

    const rooms = screen.getByTestId('version-view').querySelectorAll('.app-counseling-room');
    const first = within(rooms[0] as HTMLElement).getAllByRole('listitem');
    expect(first.map((node) => node.getAttribute('data-item'))).toEqual(['FOOT_CARE', 'DIET']);
  });

  it('says which items must be covered, in words rather than only a colour', async () => {
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />);
    await screen.findByTestId('version-view');
    const mandatory = screen
      .getByTestId('version-view')
      .querySelector('[data-item="DIET"]') as HTMLElement;
    expect(within(mandatory).getByText('Must be covered')).toBeVisible();
  });
});

describe('writing a draft', () => {
  beforeEach(() => {
    listCounselingVersions.mockResolvedValue([HALF_WRITTEN]);
    getCounselingVersion.mockResolvedValue(HALF_WRITTEN);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);
    saveCounselingDraft.mockResolvedValue(HALF_WRITTEN);
  });

  it('saves an item whose Bangla is not written yet', async () => {
    // Half-written is what writing looks like. A form that refused it would push the missing
    // half into the author's head or a notebook, which is worse than a draft.
    const user = userEvent.setup();
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    await user.click(screen.getByTestId('save-draft'));
    await waitFor(() => expect(saveCounselingDraft).toHaveBeenCalled());

    const [, , draft] = saveCounselingDraft.mock.calls[0]!;
    expect(draft.items[1]).toMatchObject({ item_code: 'IODINE_FOODS', text_bn: '' });
  });

  it('names the item with no Bangla as guidance, and does not disable the save', async () => {
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    expect(
      screen.getByText('Not ready for the floor yet. This does not stop you saving.'),
    ).toBeVisible();
    expect(within(screen.getByTestId('publish-guidance')).getByText(/IODINE_FOODS/)).toBeVisible();
    expect(screen.getByTestId('save-draft')).toBeEnabled();
  });

  it('refuses a save the server would refuse, and says which row', async () => {
    const user = userEvent.setup();
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    await user.clear(screen.getByTestId('item-code-1'));
    await user.click(screen.getByTestId('save-draft'));

    expect(saveCounselingDraft).not.toHaveBeenCalled();
    expect(
      within(screen.getByTestId('save-blockers')).getByText('Item 2 has no code.'),
    ).toBeVisible();
  });

  it('tells an author to draft again when the version was published under them', async () => {
    const user = userEvent.setup();
    saveCounselingDraft.mockRejectedValue(
      new ApiError({
        status: 409,
        code: 'CONFLICT',
        kind: 'conflict',
        messageEN: 'That version is published and cannot be edited.',
        messageBN: 'ওই সংস্করণটি প্রকাশিত।',
        correlationID: 'c-9',
      }),
    );
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    await user.click(screen.getByTestId('save-draft'));
    // Not "try again". The edits belong in a new version, and retrying would be an attempt
    // to rewrite what past sessions were asked to cover.
    expect(
      await screen.findByText('This version was published while you were writing.'),
    ).toBeVisible();
  });

  it('adds an item in the room the previous one is in', async () => {
    const user = userEvent.setup();
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    await user.click(screen.getByTestId('add-item'));
    expect(screen.getByTestId('room-2')).toHaveValue('NUTRITION_ROOM');
    // And it starts unticked: §5.5's gate reads that flag, and a default there is a clinical
    // decision nobody made.
    expect(screen.getByTestId('mandatory-2')).not.toBeChecked();
  });

  it('suggests a code for a new item and leaves an existing one alone', async () => {
    const user = userEvent.setup();
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    await user.click(screen.getByTestId('add-item'));
    await user.type(screen.getByTestId('text-en-2'), 'Report palpitations');
    expect(screen.getByTestId('item-code-2')).toHaveValue('REPORT_PALPITATIONS');

    // Rewording an item keeps its history; renaming its code does not, so the code of an
    // item that already has one does not follow the text.
    await user.type(screen.getByTestId('text-en-0'), ' daily');
    expect(screen.getByTestId('item-code-0')).toHaveValue('TAKE_ON_EMPTY_STOMACH');
  });

  it('reorders from the keyboard alone', async () => {
    // The required half of the reordering story. The order is what a counsellor works
    // through, so it is content — and a control that needs a pointer is one a physician with
    // a tremor, a keyboard user or somebody holding a tablet in one hand cannot operate.
    const user = userEvent.setup();
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    const down = screen.getByTestId('move-down-0');
    down.focus();
    expect(down).toHaveFocus();
    await user.keyboard('{Enter}');

    expect(screen.getByTestId('item-code-0')).toHaveValue('IODINE_FOODS');
    expect(screen.getByTestId('item-code-1')).toHaveValue('TAKE_ON_EMPTY_STOMACH');

    // Moved, and said so: a reorder is silent to somebody using a screen reader, because the
    // button they pressed is still under their finger.
    expect(screen.getByTestId('reorder-announcement')).toHaveTextContent('is now 2 of 2');

    await user.click(screen.getByTestId('save-draft'));
    await waitFor(() => expect(saveCounselingDraft).toHaveBeenCalled());
    const [, , draft] = saveCounselingDraft.mock.calls[0]!;
    expect(
      draft.items.map((one: { item_code: string; ordering: number }) => [
        one.item_code,
        one.ordering,
      ]),
    ).toEqual([
      ['IODINE_FOODS', 1],
      ['TAKE_ON_EMPTY_STOMACH', 2],
    ]);
  });

  it('names the item each move button moves', async () => {
    // Nine buttons all called "Move up" is the same as none.
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');
    expect(
      screen.getByRole('button', {
        name: 'Move “Take levothyroxine on an empty stomach” down',
      }),
    ).toBeInTheDocument();
  });

  it('removes an item', async () => {
    const user = userEvent.setup();
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    await user.click(screen.getByTestId('remove-1'));
    await user.click(screen.getByTestId('save-draft'));

    await waitFor(() => expect(saveCounselingDraft).toHaveBeenCalled());
    const [, , draft] = saveCounselingDraft.mock.calls[0]!;
    expect(draft.items).toHaveLength(1);
  });
});

describe('publishing', () => {
  beforeEach(() => {
    listCounselingVersions.mockResolvedValue([HALF_WRITTEN]);
    getCounselingVersion.mockResolvedValue(HALF_WRITTEN);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);
    saveCounselingDraft.mockResolvedValue(HALF_WRITTEN);
  });

  it('states the consequences and takes a second deliberate act', async () => {
    // The named test for criterion 2's other half. Pressing publish calls nothing: it opens
    // a statement of what will happen, and the confirmation inside it is the second act.
    // A physician who meant to fix a typo must not be able to do this by pressing the
    // nearer button.
    const user = userEvent.setup();
    publishCounselingVersion.mockResolvedValue({ ...HALF_WRITTEN, status: 'PUBLISHED' });
    await openWorkspace(THYROID);
    await screen.findByTestId('publish-panel');

    await user.click(screen.getByTestId('publish-open'));
    expect(publishCounselingVersion).not.toHaveBeenCalled();
    expect(requestStepUp).not.toHaveBeenCalled();

    const consequences = within(screen.getByTestId('publish-consequences'));
    expect(consequences.getByText(/every counsellor’s phone/i)).toBeVisible();
    expect(consequences.getByText(/frozen forever/i)).toBeVisible();
    // The one people are surprised by. Nothing is live for the thyroid checklist yet, so it
    // says that rather than naming a version that does not exist.
    expect(consequences.getByText(/nothing will be retired/i)).toBeVisible();

    await user.click(screen.getByTestId('publish-confirm-action'));
    await waitFor(() => expect(publishCounselingVersion).toHaveBeenCalled());
    // Its own step-up purpose: a token minted to reset a password must not be spendable on
    // changing what every counsellor asks every patient tomorrow.
    expect(requestStepUp).toHaveBeenCalledWith('counseling.publish', expect.any(String));
    expect(publishCounselingVersion).toHaveBeenCalledWith('step-up-token', THYROID, 1);
  });

  it('names the version it will retire when there is one', async () => {
    const user = userEvent.setup();
    const draft = { ...HALF_WRITTEN, version: 4 };
    listCounselingVersions.mockResolvedValue([
      draft,
      { ...SEEDED, template_id: THYROID, version: 3 },
    ]);
    getCounselingVersion.mockResolvedValue(draft);
    await openWorkspace(THYROID);
    await screen.findByTestId('publish-panel');

    await user.click(screen.getByTestId('publish-open'));
    expect(
      within(screen.getByTestId('publish-consequences')).getByText(
        'Version 3, which is live now, will be retired.',
      ),
    ).toBeVisible();
  });

  it('does nothing when the confirmation is cancelled', async () => {
    const user = userEvent.setup();
    await openWorkspace(THYROID);
    await screen.findByTestId('publish-panel');

    await user.click(screen.getByTestId('publish-open'));
    await user.click(screen.getByTestId('publish-cancel'));
    expect(publishCounselingVersion).not.toHaveBeenCalled();
    expect(screen.queryByTestId('publish-confirm')).toBeNull();
  });

  it('does nothing when the second factor prompt is dismissed', async () => {
    const user = userEvent.setup();
    const { StepUpCancelled } = await import('@/features/auth');
    requestStepUp.mockRejectedValue(new StepUpCancelled());
    await openWorkspace(THYROID);
    await screen.findByTestId('publish-panel');

    await user.click(screen.getByTestId('publish-open'));
    await user.click(screen.getByTestId('publish-confirm-action'));

    await waitFor(() => expect(requestStepUp).toHaveBeenCalled());
    expect(publishCounselingVersion).not.toHaveBeenCalled();
    // Changing your mind at the code prompt is not a failure and is not reported as one.
    expect(screen.queryByText('The checklist could not be published.')).toBeNull();
  });

  it('surfaces the server’s refusal against the item it was about', async () => {
    // The server's 422 names the field `items` — right for an API, useless on a screen with
    // several rows. Its sentence is shown because it is the authority; the item it meant is
    // named underneath, because that is what an author can act on.
    const user = userEvent.setup();
    publishCounselingVersion.mockRejectedValue(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Some values need correcting.',
        messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
        fields: { items: 'Every item must read in both languages before publishing.' },
        fieldsBN: { items: 'প্রকাশের আগে প্রতিটি বিষয় দুই ভাষাতেই লিখতে হবে।' },
        correlationID: 'c-4',
      }),
    );

    await openWorkspace(THYROID);
    await screen.findByTestId('publish-panel');
    await user.click(screen.getByTestId('publish-open'));
    await user.click(screen.getByTestId('publish-confirm-action'));

    expect(
      await screen.findByText('Every item must read in both languages before publishing.'),
    ).toBeVisible();
    const named = within(await screen.findByTestId('publish-refusal-items'));
    expect(named.getByText('Item 2 (IODINE_FOODS) has no Bangla.')).toBeVisible();
    // And not against the item that is fine.
    expect(named.queryByText(/TAKE_ON_EMPTY_STOMACH/)).toBeNull();
  });

  it('will not publish over unsaved work', async () => {
    // The one refusal this screen makes on its own, and the only one it can: publishing
    // sends the *saved* version to the floor. Nobody but this client knows the screen has
    // moved on, so nobody but this client can catch it.
    const user = userEvent.setup();
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');

    await user.type(screen.getByTestId('text-bn-1'), 'আয়োডিনযুক্ত খাবার');

    expect(screen.getByTestId('publish-open')).toBeDisabled();
    expect(screen.getByText('Save your changes first.')).toBeVisible();
  });

  it('does not offer publishing to somebody who may write but not publish', async () => {
    // The server grants writing and publishing separately, and a button offered to somebody
    // the server will refuse teaches that the software is unreliable.
    holding(['counseling.template.read', 'counseling.template.write']);
    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');
    expect(screen.queryByTestId('publish-panel')).toBeNull();
  });
});

describe('the preview', () => {
  it('shows what a counsellor walks through, room by room', async () => {
    renderWithProviders(<CounsellorPreview version={SEEDED} rooms={ROOMS} />);
    const preview = within(await screen.findByTestId('counsellor-preview'));

    expect(preview.getByText('Step 1: Counselling room')).toBeVisible();
    expect(preview.getByText('Step 2: Nutrition room')).toBeVisible();
    expect(preview.getByText('Step 3: Insulin corner')).toBeVisible();
  });

  it('previews in the language the author does not work in', async () => {
    // The whole point of the control. A physician working in English cannot check the Bangla
    // by reading their own screen, and the Bangla is the half this clinic counsels in.
    const user = userEvent.setup();
    renderWithProviders(<CounsellorPreview version={SEEDED} rooms={ROOMS} />);
    await screen.findByTestId('counsellor-preview');

    expect(screen.getByText('Foot care')).toBeVisible();
    await user.click(screen.getByTestId('preview-in-bn'));
    expect(screen.getByText('পায়ের যত্ন')).toBeVisible();
    expect(screen.queryByText('Foot care')).toBeNull();
  });

  it('draws a missing translation as a gap rather than falling back', async () => {
    // A preview that quietly substituted the English would show a checklist that reads
    // completely and is not — which is the single thing this screen exists to prevent.
    const user = userEvent.setup();
    renderWithProviders(<CounsellorPreview version={HALF_WRITTEN} rooms={ROOMS} />);
    await screen.findByTestId('counsellor-preview');

    await user.click(screen.getByTestId('preview-in-bn'));
    expect(screen.getByTestId('preview-missing-IODINE_FOODS')).toHaveTextContent(
      'Bangla not written yet',
    );
    expect(screen.queryByText('Iodine-rich foods')).toBeNull();
  });

  it('offers nothing to tick, because there is no session behind it', async () => {
    renderWithProviders(<CounsellorPreview version={SEEDED} rooms={ROOMS} />);
    const preview = await screen.findByTestId('counsellor-preview');
    for (const box of preview.querySelectorAll('input[type="checkbox"]')) {
      expect(box).toBeDisabled();
    }
  });
});

describe('the workspace', () => {
  it('opens on the newest draft rather than on the newest version', async () => {
    // An author who left something half-written is almost certainly coming back to it — and
    // the newest version of a template published last week is a retired one.
    const draft = { ...HALF_WRITTEN, template_id: TEMPLATE, version: 3 };
    listCounselingVersions.mockResolvedValue([
      { ...SEEDED, version: 4, status: 'RETIRED' },
      draft,
      SEEDED,
    ]);
    getCounselingVersion.mockResolvedValue(draft);

    await openWorkspace();
    await screen.findByTestId('draft-editor');
    expect(getCounselingVersion).toHaveBeenCalledWith(TEMPLATE, 3);
  });

  it('lets an author move between versions, and says what each one is', async () => {
    const user = userEvent.setup();
    listCounselingVersions.mockResolvedValue([
      { ...HALF_WRITTEN, template_id: TEMPLATE, version: 2 },
      SEEDED,
    ]);
    await openWorkspace();
    await screen.findByTestId('version-history');

    expect(screen.getByTestId('version-tab-1')).toHaveTextContent('Version 1 — Live');
    expect(screen.getByTestId('version-tab-2')).toHaveTextContent('Version 2 — Draft');

    await user.click(screen.getByTestId('version-tab-1'));
    await waitFor(() => expect(getCounselingVersion).toHaveBeenCalledWith(TEMPLATE, 1));
  });

  it('does not carry one draft’s items onto another draft’s screen', async () => {
    // The editor holds its rows as state. Moving between two drafts without remounting it
    // would leave version 2's items under version 3's heading — and a save would then write
    // them over the draft the author believes they are editing.
    const user = userEvent.setup();
    const second: CounselingVersion = {
      ...HALF_WRITTEN,
      template_id: TEMPLATE,
      version: 2,
      items: [item({ item_code: 'SECOND_DRAFT_ONLY', text_en: 'Repeat the TSH in six weeks' })],
    };
    listCounselingVersions.mockResolvedValue([second, { ...HALF_WRITTEN, template_id: TEMPLATE }]);
    getCounselingVersion.mockImplementation((_id: string, wanted: number) =>
      Promise.resolve(wanted === 2 ? second : { ...HALF_WRITTEN, template_id: TEMPLATE }),
    );

    await openWorkspace();
    await screen.findByTestId('draft-editor');
    expect(screen.getByTestId('item-code-0')).toHaveValue('SECOND_DRAFT_ONLY');

    await user.click(screen.getByTestId('version-tab-1'));
    await waitFor(() =>
      expect(screen.getByTestId('item-code-0')).toHaveValue('TAKE_ON_EMPTY_STOMACH'),
    );

    // And back again, which is the case a loading state does not cover for us: both versions
    // are cached now, so the new one arrives in the same render and nothing unmounts on its
    // own. Only the key makes the editor start again from the version being shown.
    await user.click(screen.getByTestId('version-tab-2'));
    await waitFor(() => expect(screen.getByTestId('item-code-0')).toHaveValue('SECOND_DRAFT_ONLY'));
  });

  it('shows a draft read-only to somebody who may not write', async () => {
    // Reading reaches the counsellor about to work through the checklist. They get the
    // version, not the form.
    holding(['counseling.template.read']);
    listCounselingVersions.mockResolvedValue([{ ...HALF_WRITTEN, template_id: TEMPLATE }]);
    getCounselingVersion.mockResolvedValue({ ...HALF_WRITTEN, template_id: TEMPLATE });

    await openWorkspace();
    await screen.findByTestId('version-view');
    expect(screen.queryByTestId('draft-editor')).toBeNull();
  });

  it('says so when the workspace cannot be read', async () => {
    listCounselingVersions.mockRejectedValue(new Error('down'));
    renderWithProviders(<TemplateWorkspace templateId={TEMPLATE} />);
    expect(await screen.findByText('The counselling checklists could not be read.')).toBeVisible();
  });
});

describe('in Bangla', () => {
  it('reads the list in Bangla', async () => {
    renderWithProviders(<TemplateList />, { locale: 'bn' });
    await screen.findByTestId('template-list');

    expect(screen.getByText('ডায়াবেটিস কাউন্সেলিং')).toBeVisible();
    expect(screen.getByTestId('template-unapproved-DIABETES')).toHaveTextContent(
      'বিষয়বস্তু কোনো চিকিৎসক অনুমোদন করেননি',
    );
    expect(screen.getByRole('button', { name: 'নতুন তালিকা শুরু করুন' })).toBeInTheDocument();
  });

  it('reads a published version in Bangla, migration attribution and all', async () => {
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />, {
      locale: 'bn',
    });
    await screen.findByTestId('version-view');

    expect(screen.getByTestId('version-status')).toHaveTextContent('চালু');
    expect(screen.getByTestId('version-attribution')).toHaveTextContent(
      'সিস্টেমের সঙ্গেই প্রকাশিত',
    );
    expect(screen.getByText('এই সংস্করণটি সম্পূর্ণ, এটি আর বদলানো যাবে না।')).toBeVisible();
    // The room headings come from the server's Bangla, not from a table in the client.
    expect(screen.getByTestId('room-heading-INSULIN_CORNER')).toHaveTextContent('ইনসুলিন কর্নার');
  });

  // Both languages are on this screen whatever the interface language — that is what makes a
  // missing half visible before publication, and there is a test above for it. What this pins
  // is the order: a physician working in Bangla reads the Bangla line first. English first for
  // everyone would say, quietly and on every item, which half of this clinic's work is real.
  it('puts the reader’s own language first, without dropping the other', async () => {
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />, {
      locale: 'bn',
    });
    await screen.findByTestId('version-view');

    const item = document.querySelector('[data-item="DIET"]') as HTMLElement;
    const halves = within(item)
      .getAllByText(/Diet advice|খাদ্য পরামর্শ/)
      .map((node) => node.getAttribute('lang'));
    expect(halves).toEqual(['bn', 'en']);
  });

  it('states the publishing consequences in Bangla', async () => {
    const user = userEvent.setup();
    listCounselingVersions.mockResolvedValue([HALF_WRITTEN]);
    getCounselingVersion.mockResolvedValue(HALF_WRITTEN);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);

    await openWorkspace(THYROID, 'bn');
    await screen.findByTestId('publish-panel');
    await user.click(screen.getByTestId('publish-open'));

    const consequences = within(screen.getByTestId('publish-consequences'));
    expect(consequences.getByText(/চিরতরে আটকে যাবে/)).toBeVisible();
    expect(screen.getByTestId('publish-confirm-action')).toHaveTextContent(
      'হ্যাঁ, সংস্করণ 1 প্রকাশ করুন',
    );
  });
});

describe('the states that are easy to leave undrawn', () => {
  it('names a person’s publication differently from a migration’s', async () => {
    // The other side of the MIGRATION test. A version somebody actually published must not
    // read the same way, or "published with the system" stops meaning anything.
    const byPerson: CounselingVersion = {
      ...SEEDED,
      published_source: 'USER',
      published_by: PHYSICIAN,
      approved_at: '2026-02-01T00:00:00Z',
      approved_by: PHYSICIAN,
      notes: '',
    };
    renderWithProviders(<VersionView templateId={TEMPLATE} version={byPerson} rooms={ROOMS} />);

    expect(await screen.findByTestId('version-attribution')).toHaveTextContent(
      'A member of the clinic’s staff',
    );
    expect(screen.queryByTestId('version-migration-note')).toBeNull();
    // Approved content says nothing rather than claiming something; the warning is the
    // exceptional state, not the normal one.
    expect(screen.queryByText('Content not approved by a clinician')).toBeNull();
    // An empty note is a note nobody left, and saying so beats an empty cell.
    expect(screen.getByTestId('version-notes')).toHaveTextContent(
      'No note was left with this version.',
    );
  });

  it('says a version with no items cannot go to the floor', async () => {
    renderWithProviders(
      <VersionView templateId={TEMPLATE} version={{ ...SEEDED, items: [] }} rooms={ROOMS} />,
    );
    expect(await screen.findByText('This version has no items yet')).toBeVisible();
  });

  it('draws an item’s missing English, its guidance and an unknown room', async () => {
    // Both languages are on this screen because the author is reviewing, not counselling —
    // an author working in Bangla must see that the English is missing, and vice versa.
    const odd: CounselingVersion = {
      ...SEEDED,
      items: [
        item({
          item_code: 'CORRIDOR_ITEM',
          room: 'CORRIDOR',
          text_en: '',
          guidance_en: 'Ask where they keep the insulin at home.',
          guidance_bn: 'বাড়িতে ইনসুলিন কোথায় রাখেন জিজ্ঞেস করুন।',
        }),
      ],
    };
    renderWithProviders(<VersionView templateId={TEMPLATE} version={odd} rooms={ROOMS} />);
    await screen.findByTestId('version-view');

    expect(screen.getByTestId('missing-en-CORRIDOR_ITEM')).toHaveTextContent(
      'English not written yet',
    );
    expect(screen.getByText('What to say')).toBeVisible();
    expect(screen.getByText('Ask where they keep the insulin at home.')).toBeVisible();
    // The item is still drawn. One that vanished is one nobody covers and nobody misses.
    expect(screen.getByText(/CORRIDOR/, { selector: 'p' })).toBeVisible();
  });

  it('says the list is empty when there are no checklists at all', async () => {
    listCounselingTemplates.mockResolvedValue([]);
    renderWithProviders(<TemplateList />);
    expect(await screen.findByText('No checklist has been written yet')).toBeVisible();
  });

  it('names the live version even when the publication date is missing', async () => {
    const noDate = template();
    delete (noDate as { published_at?: string }).published_at;
    listCounselingTemplates.mockResolvedValue([noDate]);
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');
    expect(screen.getByTestId('template-state-DIABETES')).toHaveTextContent('Version 1 is live');
  });

  it('shows an empty preview rather than an empty frame', async () => {
    renderWithProviders(<CounsellorPreview version={{ ...SEEDED, items: [] }} rooms={ROOMS} />);
    expect(await screen.findByText('Nothing to show yet.')).toBeVisible();
  });

  it('carries the guidance onto the counsellor’s phone', async () => {
    // §5.4: the physician spot-questions the patient afterwards, and two counsellors who
    // covered one item differently make that check useless.
    const guided: CounselingVersion = {
      ...SEEDED,
      items: [item({ guidance_en: 'Show the plate, not the calorie count.' })],
    };
    renderWithProviders(<CounsellorPreview version={guided} rooms={ROOMS} />);
    expect(await screen.findByText('Show the plate, not the calorie count.')).toBeVisible();
  });

  it('says a draft with nothing in it cannot be published', async () => {
    const user = userEvent.setup();
    const bare: CounselingVersion = { ...HALF_WRITTEN, items: [] };
    listCounselingVersions.mockResolvedValue([bare]);
    getCounselingVersion.mockResolvedValue(bare);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);
    publishCounselingVersion.mockRejectedValue(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Some values need correcting.',
        messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
        fields: { items: 'A checklist with no items cannot be published.' },
        fieldsBN: { items: 'বিষয়বিহীন তালিকা প্রকাশ করা যায় না।' },
        correlationID: 'c-5',
      }),
    );

    await openWorkspace(THYROID);
    await screen.findByTestId('publish-panel');
    // An empty checklist reads as finished the moment it opens, so it is named before the
    // attempt as well as after it.
    expect(within(screen.getByTestId('publish-blockers')).getByText(/no items/i)).toBeVisible();

    await user.click(screen.getByTestId('publish-open'));
    await user.click(screen.getByTestId('publish-confirm-action'));
    expect(await screen.findByText('A checklist with no items cannot be published.')).toBeVisible();
    expect(
      within(screen.getByTestId('publish-refusal-items')).getByText(/no items/i),
    ).toBeVisible();
  });

  it('reports a publish that failed for a reason the server did not name', async () => {
    const user = userEvent.setup();
    listCounselingVersions.mockResolvedValue([HALF_WRITTEN]);
    getCounselingVersion.mockResolvedValue(HALF_WRITTEN);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);
    publishCounselingVersion.mockRejectedValue(new Error('boom'));

    await openWorkspace(THYROID);
    await user.click(await screen.findByTestId('publish-open'));
    await user.click(screen.getByTestId('publish-confirm-action'));
    expect(await screen.findByText('The checklist could not be published.')).toBeVisible();
  });

  it('lands the server’s refusal of a new template on the field it names', async () => {
    const user = userEvent.setup();
    createCounselingTemplate.mockRejectedValue(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Some values need correcting.',
        messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
        fields: { code: 'A template with that code already exists.' },
        fieldsBN: { code: 'ওই কোডে একটি টেমপ্লেট আগে থেকেই আছে।' },
        correlationID: 'c-6',
      }),
    );
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');

    await user.click(screen.getByRole('button', { name: 'Start a new checklist' }));
    await user.type(screen.getByTestId('new-template-title-en'), 'Diabetes counselling');
    await user.type(screen.getByTestId('new-template-title-bn'), 'ডায়াবেটিস কাউন্সেলিং');
    await user.click(screen.getByTestId('new-template-create'));

    // Against the code field, where the operator can fix it, rather than as a banner.
    expect(await screen.findByText('A template with that code already exists.')).toBeVisible();
  });

  it('reports a create that failed for a reason the server did not name', async () => {
    const user = userEvent.setup();
    createCounselingTemplate.mockRejectedValue(new Error('boom'));
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');

    await user.click(screen.getByRole('button', { name: 'Start a new checklist' }));
    await user.type(screen.getByTestId('new-template-title-en'), 'Thyroid');
    await user.type(screen.getByTestId('new-template-title-bn'), 'থাইরয়েড');
    await user.click(screen.getByTestId('new-template-create'));

    expect(await screen.findByText('The checklist could not be created.')).toBeVisible();
  });

  it('lets an author back out of starting a checklist', async () => {
    const user = userEvent.setup();
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');

    await user.click(screen.getByRole('button', { name: 'Start a new checklist' }));
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.getByRole('button', { name: 'Start a new checklist' })).toBeInTheDocument();
  });

  it('keeps the code an author typed, whatever they do to the English title afterwards', async () => {
    const user = userEvent.setup();
    createCounselingTemplate.mockResolvedValue(THYROID_TEMPLATE);
    renderWithProviders(<TemplateList />);
    await screen.findByTestId('template-list');

    await user.click(screen.getByRole('button', { name: 'Start a new checklist' }));
    await user.type(screen.getByTestId('new-template-code'), 'thyroid');
    await user.type(screen.getByTestId('new-template-title-en'), 'Thyroid counselling');
    expect(screen.getByTestId('new-template-code')).toHaveValue('THYROID');
  });

  it('says so when a new draft could not be opened', async () => {
    const user = userEvent.setup();
    draftCounselingVersion.mockRejectedValue(new Error('boom'));
    renderWithProviders(<VersionView templateId={TEMPLATE} version={SEEDED} rooms={ROOMS} />);

    await user.click(await screen.findByTestId('draft-new-version'));
    expect(await screen.findByText('A new draft could not be opened.')).toBeVisible();
  });

  it('says so when the version itself cannot be read', async () => {
    getCounselingVersion.mockRejectedValue(new Error('boom'));
    renderWithProviders(<TemplateWorkspace templateId={TEMPLATE} />);
    expect(await screen.findByText('This version could not be read.')).toBeVisible();
  });

  it('records the notes an author leaves with a draft', async () => {
    const user = userEvent.setup();
    listCounselingVersions.mockResolvedValue([HALF_WRITTEN]);
    getCounselingVersion.mockResolvedValue(HALF_WRITTEN);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);
    saveCounselingDraft.mockResolvedValue(HALF_WRITTEN);

    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');
    await user.type(screen.getByTestId('draft-notes'), 'Endocrine team asked for the iodine line.');
    await user.click(screen.getByTestId('save-draft'));

    await waitFor(() => expect(saveCounselingDraft).toHaveBeenCalled());
    expect(saveCounselingDraft.mock.calls[0]![2].notes).toBe(
      'Endocrine team asked for the iodine line.',
    );
  });

  it('shows a refusal the server names no field for', async () => {
    const user = userEvent.setup();
    listCounselingVersions.mockResolvedValue([HALF_WRITTEN]);
    getCounselingVersion.mockResolvedValue(HALF_WRITTEN);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);
    saveCounselingDraft.mockRejectedValue(
      new ApiError({
        status: 500,
        code: 'INTERNAL',
        kind: 'internal',
        messageEN: 'Something went wrong at our end.',
        messageBN: 'আমাদের দিকে কিছু একটা সমস্যা হয়েছে।',
        correlationID: 'c-7',
      }),
    );

    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');
    await user.click(screen.getByTestId('save-draft'));
    expect(await screen.findByText('Something went wrong at our end.')).toBeVisible();
  });

  it('lands a save refusal on the field the server names', async () => {
    const user = userEvent.setup();
    listCounselingVersions.mockResolvedValue([HALF_WRITTEN]);
    getCounselingVersion.mockResolvedValue(HALF_WRITTEN);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);
    saveCounselingDraft.mockRejectedValue(
      new ApiError({
        status: 422,
        code: 'VALIDATION_FAILED',
        kind: 'validation',
        messageEN: 'Some values need correcting.',
        messageBN: 'কিছু তথ্য সংশোধন করতে হবে।',
        fields: { room: 'That is not one of the counselling rooms.' },
        fieldsBN: { room: 'এটি কাউন্সেলিং রুমগুলোর একটি নয়।' },
        correlationID: 'c-8',
      }),
    );

    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');
    await user.click(screen.getByTestId('save-draft'));
    // The named message, not the generic envelope sentence: "Some values need correcting"
    // tells an author nothing they can act on.
    expect(await screen.findByText('That is not one of the counselling rooms.')).toBeVisible();
  });

  it('says so when a save fails for a reason that is not an answer', async () => {
    const user = userEvent.setup();
    listCounselingVersions.mockResolvedValue([HALF_WRITTEN]);
    getCounselingVersion.mockResolvedValue(HALF_WRITTEN);
    listCounselingTemplates.mockResolvedValue([THYROID_TEMPLATE]);
    saveCounselingDraft.mockRejectedValue(new Error('boom'));

    await openWorkspace(THYROID);
    await screen.findByTestId('draft-editor');
    await user.click(screen.getByTestId('save-draft'));
    expect(await screen.findByText('The draft could not be saved.')).toBeVisible();
  });

  it('opens on the version an author asked for', async () => {
    listCounselingVersions.mockResolvedValue([
      { ...HALF_WRITTEN, template_id: TEMPLATE, version: 2 },
      SEEDED,
    ]);
    await openWorkspace(TEMPLATE, 'en', 1);
    await screen.findByTestId('version-view');
    expect(getCounselingVersion).toHaveBeenCalledWith(TEMPLATE, 1);
  });

  it('names a template it cannot find in the listing without pretending to know it', async () => {
    listCounselingTemplates.mockResolvedValue([]);
    await openWorkspace();
    expect(await screen.findByText('This checklist')).toBeVisible();
  });
});

describe('the words a checklist is read in', () => {
  it('falls back for a room name, because reference data is always bilingual', () => {
    expect(surface.roomName(ROOMS[0], 'COUNSELING_ROOM', 'bn')).toBe('কাউন্সেলিং কক্ষ');
    expect(surface.roomName(ROOMS[0], 'COUNSELING_ROOM', 'en')).toBe('Counselling room');
    // No row for the code: the code itself, which is a visible defect somebody will report.
    expect(surface.roomName(undefined, 'CORRIDOR', 'bn')).toBe('CORRIDOR');
    // Bangla missing on the row: an English word beats a blank space.
    expect(surface.roomName({ ...ROOMS[0]!, display_bn: '' }, 'COUNSELING_ROOM', 'bn')).toBe(
      'Counselling room',
    );
    expect(surface.roomName({ ...ROOMS[0]!, display_en: '', display_bn: '' }, 'X', 'en')).toBe('X');
  });

  it('does not fall back for an item’s own text', () => {
    // The opposite rule, and the reason it is opposite is criterion 4: a preview that
    // substituted the English would show a checklist that reads completely and is not.
    expect(surface.itemText(item(), 'bn')).toBe('খাদ্য পরামর্শ');
    expect(surface.itemText(item({ text_bn: '  ' }), 'bn')).toBeNull();
    expect(surface.itemText(item({ text_en: '' }), 'en')).toBeNull();
  });

  it('treats absent guidance as nothing rather than as a gap', () => {
    // Guidance is genuinely optional, so a missing one is not a defect to report.
    expect(surface.itemGuidance(item(), 'en')).toBeNull();
    expect(surface.itemGuidance(item({ guidance_bn: 'ধীরে বলুন' }), 'bn')).toBe('ধীরে বলুন');
    expect(surface.itemGuidance(item({ guidance_en: ' ' }), 'en')).toBeNull();
  });

  it('names a template in the reader’s language, and by its code as a last resort', () => {
    expect(surface.templateTitle(template(), 'bn')).toBe('ডায়াবেটিস কাউন্সেলিং');
    expect(surface.templateTitle(template({ title_bn: '' }), 'bn')).toBe('Diabetes counselling');
    expect(surface.templateTitle(template({ title_en: '', title_bn: '' }), 'en')).toBe('DIABETES');
  });
});

describe('the module’s public surface', () => {
  it('exports the frozen-state predicate every screen must ask', () => {
    // A second implementation of "may this be edited" somewhere else is how a screen comes
    // to read `status !== 'RETIRED'` and offer a form over a live checklist.
    expect(typeof surface.isEditable).toBe('function');
    expect(surface.isEditable({ ...SEEDED })).toBe(false);
    expect(surface.isEditable({ ...HALF_WRITTEN })).toBe(true);
  });

  it('exports both facts that must never be rounded up', () => {
    expect(surface.contentApproved(SEEDED)).toBe(false);
    expect(surface.publishedBySystem(SEEDED)).toBe(true);
  });
});
