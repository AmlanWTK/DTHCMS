/**
 * Counselling ticking, on the floor (CP56, §5.3, [R-07]).
 *
 * A station reaches this feature through here and nowhere else. What it gets is one checklist
 * for one visit — the items the session froze when it opened, every tick made on them with the
 * name and the moment behind each, and the two numbers a counsellor says out loud when
 * somebody asks how far they have got.
 *
 * The public surface is in three parts: `CounselingStation`, the screen; `state.ts`, where
 * every decision lives — what one tap asserts, what an un-tick costs, what finishing early
 * means; and the calls in `api.ts`.
 *
 * Five things in this list are deliberate, and four of them are absences:
 *
 *   - **Nothing here ticks a list.** `toTick` builds a body for one item code, `oneItemCode`
 *     refuses a string that is secretly a list, and there is no function in `api.ts` with an
 *     array parameter. Criterion 1 is only as strong as the smallest act a client can send,
 *     and §5.4's method — the physician asking the patient what they were told about injection
 *     sites, and then looking at who told them — has no answer if seven acts share one press.
 *   - **There is no confirmation step and no navigation between rooms.** The rooms are
 *     headings on one list. `tapsToComplete` is eight for the seeded seven-item checklist, and
 *     it is asserted, so a redesign that adds a step fails a test rather than a stopwatch.
 *   - **There is no percentage.** `Progress` carries integers, `mandatory` and `covered`, and
 *     the covered figure comes from the server's own outstanding list — the same database
 *     function CP57's gate reads. A percentage would round away the difference between
 *     finished and nearly finished; a second implementation of "what is missing" would put a
 *     green tick on a phone while a gate refuses the patient in front of it.
 *   - **Nothing here refuses to finish.** `finishRefused` is true for one reason — a session
 *     somebody already closed — and never because something is uncovered. `Tone` has no
 *     failure value, so the situation cannot be drawn as an error. Whether such a visit
 *     reaches the physician is CP57's gate to decide, and it can only decide it because this
 *     screen did not make the situation unrecordable.
 *   - **An un-tick always costs a reason.** `untickRefused` is what an empty one meets before
 *     it becomes a request, and it is the sentence a person reads rather than the enforcement
 *     — the server refuses it too, in its own validation and by a database constraint.
 *
 * Two things this list now *does* have, and both are the server answering a question this app
 * must never answer for itself:
 *
 *   - **Starting a checklist**, offered only for a checklist the server named for this visit,
 *     with the coded condition that called for it beside the control. There is no template
 *     picker: assignment is a rule keyed on a coding (§5.1), and a counsellor choosing from a
 *     catalogue of every checklist in the clinic would be making that assignment by hand.
 *   - **Resuming**, because the same answer names the session already open. A phone that opened
 *     a second session on a list a colleague is halfway down is how two people each cover half
 *     of it and each believe the other did the rest.
 *
 * And one thing it shows to somebody who cannot write on it at all. The checklist answer reads
 * with `counseling.session.read` as well as `counseling.tick`, so a reviewer or a physician's
 * panel sees the checklists this visit *called for* and not only the ones somebody walked —
 * which is the half a gate refusal turns on, and which used to be a 403 and an empty screen.
 * Only the control that opens one is a counsellor's.
 *
 * # The checkpoint (CP57, §5.5)
 *
 * `CounselingGateStep` is the second screen in this feature, and it is what a patient who cannot
 * be sent on to the physician looks like. Everything about it is a consequence of criterion 2 —
 * *the blocked message names exactly which items are missing* — and of the observation that a
 * refusal naming nothing sends an operator to whoever is quickest to ask rather than whoever is
 * right. So it names the items, groups them under the rooms they are covered in, and offers one
 * tap per remediation.
 *
 * Three absences hold it up, and each of them is a way this could quietly go wrong:
 *
 *   - **Nothing here decides the gate.** `gateStateOf` reads two booleans the server sets and
 *     `remedyFor` reads whether the server sent a `session_id`; there is no argument to
 *     `gateReadingOf` through which a session, a tick or a progress figure could arrive to be
 *     counted. A client-side "looks finished to me" is exactly how a green tick appears on a
 *     phone while the queue refuses the patient standing in front of it.
 *   - **There is no override anywhere in this feature.** Not a function, not a call, not a
 *     control. It needs `counseling.gate.override`, which a station operator does not hold, so a
 *     button would answer 403 with a patient waiting. The screen names who can instead.
 *   - **There are two remedies and they are never drawn as one.** A half-walked checklist is
 *     resumed; a checklist nobody opened is started, and that is a different room and a
 *     different person. Collapsing them would send half the patients to the wrong one.
 *
 * The refusal itself is recognised by `gateRefused` — the server's `VISIT_GATE_BLOCKED`, which is
 * the only part of a queue refusal a client may branch on. What a caller does with it is read the
 * gate: the refusal's own sentence names the items, but it cannot name the room, and the room is
 * where the patient has to be walked.
 */

export { CounselingGateStep } from './CounselingGateStep';
export { CounselingStation } from './CounselingStation';
export {
  completeCounselingSession,
  getCounselingGate,
  getCounselingSession,
  listCounselingChecklistsForVisit,
  listCounselingSessionsForVisit,
  startCounselingSession,
  tickCounselingItem,
  troubleOf,
  untickCounselingItem,
} from './api';
export {
  ACTS,
  ADVICE,
  ATTEMPTS,
  CLINIC_UTC_OFFSET_MINUTES,
  CODE_ALREADY_COVERED,
  CODE_GATE_BLOCKED,
  GATE_STATES,
  ITEM_CODE,
  NOTE_MAX,
  PERM_TICK,
  PROBLEMS,
  REASON_MAX,
  REMEDIES,
  TAPS_PER_ITEM,
  TAPS_TO_FINISH,
  TAPS_TO_START,
  TONES,
  adviceFor,
  alreadyCovered,
  attemptKey,
  attributionKey,
  attributionOf,
  checklistTitle,
  choicesOf,
  clockTime,
  completionOf,
  coveredBy,
  eventFor,
  finishRefused,
  gateReadingOf,
  gateRefused,
  gateStateOf,
  gateToneFor,
  grantedBy,
  historyOf,
  holdEvent,
  keepsItsEvent,
  mayTick,
  missingRoomsOf,
  missingRowOf,
  needsReason,
  noteOpenable,
  onTheList,
  oneItemCode,
  progressOf,
  readsBack,
  releaseEvent,
  remedyFor,
  resumeOf,
  roomsOf,
  rowsOf,
  sessionOpen,
  sessionTitle,
  startableOf,
  tapsToComplete,
  tickFor,
  tickIsLive,
  tickProblem,
  toCompletion,
  toStart,
  toTick,
  toUntick,
  toneFor,
  troubleKey,
  untickProblem,
  untickRefused,
  wordingOf,
  type Act,
  type Advice,
  type Attempt,
  type Attribution,
  type ChecklistChoice,
  type Completion,
  type CompleteRequest,
  type CounselingChecklist,
  type CounselingGate,
  type CounselingGateOverride,
  type CounselingItem,
  type CounselingMissingItem,
  type CounselingSession,
  type CounselingTick,
  type GateReading,
  type GateState,
  type GateTone,
  type HeldEvents,
  type ItemRow,
  type Locale,
  type MissingChecklistGroup,
  type MissingRoomGroup,
  type MissingRow,
  type Problem,
  type Progress,
  type Remedy,
  type Reader,
  type RoomGroup,
  type StartRequest,
  type TickHistory,
  type TickRequest,
  type Tone,
  type Trouble,
  type UntickRequest,
  type Wording,
  type WriteIDs,
} from './state';
