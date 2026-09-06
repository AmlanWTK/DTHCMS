/**
 * `counseling` — the checklist a counsellor works through, authored by a physician rather
 * than by a release (CP55, §5.1, [R-07]).
 *
 * Acceptance criterion 1 is the whole surface: **a new template can be authored and published
 * without a code change.** Dr. Nahid needs a thyroid checklist; she opens `TemplateList`,
 * starts one, writes its items in both languages in `DraftEditor`, checks it in
 * `CounsellorPreview` in the language she does not work in, and publishes it through
 * `PublishPanel` — and nobody writes any code, at any point.
 *
 * **A published version is frozen, and nothing here offers to edit one.** The decision is
 * enforced by a database trigger and answered as `409` by the API, and this module's answer
 * to it is structural rather than defensive: `TemplateWorkspace` renders `DraftEditor` for a
 * draft and `VersionView` — which contains no input of any kind — for everything else. There
 * is no third state where a published version renders a disabled form. A disabled field
 * teaches "not right now" and invites hunting for the state in which it would work; the model
 * that is true is *published means done, revise by drafting*, and `VersionView`'s only control
 * is the one that does that. `counseling.test.tsx` has a named test that fails if a textbox,
 * a combobox or a checkbox ever appears inside a published version.
 *
 * **Saving and publishing are not built by the same component.** `DraftEditor` owns exactly
 * one write and it is the save; `PublishPanel` owns the other, and takes three deliberate
 * acts to reach — press, confirm against a statement of consequences, then a step-up minted
 * for `counseling.publish` and nothing else. They are separated by construction rather than
 * by layout, because a physician fixing a typo at the end of a clinic day must not be able to
 * put a checklist on every phone on the floor by pressing the nearer button.
 *
 * **Half-written drafts are legitimate and nothing here refuses one.** `saveBlockers` names
 * only what the server itself rejects — a blank or duplicated item code, a row with no text
 * in either language, an unknown room. The bilingual requirement is criterion 4, is checked
 * at the publish transition by the API and by a trigger, and appears here as *guidance* from
 * `publishBlockers`: which item, which language, before the author tries. It never blocks a
 * save, and it is also what turns the server's 422 — one message against the field `items` —
 * into something a person can act on.
 *
 * **`approved_at` and `published_source` are reported, never rounded up.** D-53 is open: the
 * seeded diabetes items are transcribed from §5.1 and their Bengali and guidance are an
 * engineer's, so `contentApproved` is false for them and every surface says so in words —
 * a checklist can be live and still be a proposal. `publishedBySystem` is the other half:
 * that version says `MIGRATION` and carries no `published_by`, so it renders as "published
 * with the system" rather than as a blank author, because no person published it and an
 * invented id would be the only attribution in this system naming somebody who did not do
 * the thing.
 *
 * The pure helpers are public for the same reason terminology's, history's and allergies'
 * are: a station app or a second screen that needs to know a version's room order, or whether
 * a checklist is ready for the floor, must get the same answer these screens give rather than
 * a second implementation that decides differently about an item with one language.
 *
 * # The other half: the floor, and the checkpoint in front of the physician (CP57)
 *
 * `CounselingPanel` is the physician's surface — §5.5's gate on one visit, and every checklist
 * walked on it, item by item. It is a **read surface plus exactly one write**, and the write
 * is the override.
 *
 * **Nothing here ticks anything.** There is no tick, un-tick, complete or start anywhere in
 * `api/gate.ts` or in any component built on it, and there must never be one. §5.4's method is
 * the physician asking the patient what they were told about injection sites and then looking
 * at who told them; a physician who could tick a counsellor's item from this panel could close
 * the gate holding their own patient, in a colleague's name, having said nothing to anybody.
 * `counseling-panel.test.tsx` has two named tests for this — one on the module's exports, one
 * on the rendered screen.
 *
 * **Attribution is per item, never per session.** One session walks three rooms and the
 * nutritionist holds `counseling.tick` because the nutrition items are hers. "Counselled by
 * Rina" would be a true sentence answering the wrong question.
 *
 * **Time per item is elapsed time between ticks, and the screen says so.** It is the gap
 * between one press and the next, because that is all that is recorded — not attention. A
 * counsellor who teaches for eleven minutes and ticks four items at the end shows three fast
 * items and one slow one, and the panel must not present that as a fact about the teaching.
 *
 * **`overridden` is not `covered`.** `gateState` answers with three values and nothing here
 * collapses them into two: a patient somebody waved past has an unfinished checklist, and a
 * screen that drew that like a completed one would be telling a physician the counselling was
 * done.
 */
export { TemplateList } from './components/TemplateList';
export { TemplateWorkspace, type TemplateWorkspaceProps } from './components/TemplateWorkspace';
export { VersionView, type VersionViewProps } from './components/VersionView';
export { DraftEditor, type DraftEditorProps } from './components/DraftEditor';
export { PublishPanel, type PublishPanelProps } from './components/PublishPanel';
export { CounsellorPreview, type CounsellorPreviewProps } from './components/CounsellorPreview';
export { NewTemplateForm, type NewTemplateFormProps } from './components/NewTemplateForm';
export { ItemLines } from './components/ItemLines';
export { CounselingPanel, type CounselingPanelProps } from './components/CounselingPanel';
export { GateState, type GateStateProps } from './components/GateState';
export { OverrideGate, type OverrideGateProps } from './components/OverrideGate';
export { SessionItems, type SessionItemsProps } from './components/SessionItems';
export { SessionList, type SessionListProps } from './components/SessionList';
export {
  itemLabel,
  missingChecklistTitle,
  missingLabel,
  missingRoomName,
  sessionTitle,
  staffLabel,
  type BilingualText,
  type StaffFields,
  type StaffLabel,
} from './components/panelText';
export {
  OVERRIDE_CONFLICT_CODE,
  OVERRIDE_REASON_MIN,
  counselingGateKey,
  counselingOverridesKey,
  counselingSessionKey,
  counselingVisitSessionsKey,
  coveredCount,
  elapsedParts,
  gateState,
  getCounselingGate,
  getCounselingSession,
  isFinished,
  isWithdrawn,
  itemProgress,
  listCounselingGateOverrides,
  listCounselingSessionsForVisit,
  missingByRemediation,
  overrideAlreadyStands,
  overrideCounselingGate,
  overrideReasonAcceptable,
  skippedAtGrant,
  tickFor,
  ticksInOrder,
  type CounselingGate,
  type CounselingGateOverride,
  type CounselingMissingItem,
  type CounselingSession,
  type CounselingTick,
  type ElapsedParts,
  type GateState as CounselingGateState,
  type ItemProgress,
  type MissingSplit,
  type SkippedAtGrant,
} from './api/gate';
export { itemGuidance, itemText, roomName, templateTitle } from './components/counselingText';
export {
  COUNSELING_ROOMS_KEY,
  COUNSELING_TEMPLATES_KEY,
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
  type CounselingAssignment,
  type CounselingItem,
  type CounselingItemDraft,
  type CounselingMatch,
  type CounselingRoom,
  type CounselingTemplate,
  type CounselingVersion,
  type ItemDraftRow,
  type PublishBlocker,
  type PublishedSource,
  type RoomCode,
  type RoomGroup,
  type SaveBlocker,
  type VersionStatus,
} from './api/counseling';
