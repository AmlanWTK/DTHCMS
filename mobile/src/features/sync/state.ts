import {
  OPERATOR_ACTIONABLE_STATES,
  REASON_CODES,
  reasonKey,
  referenceOf,
  statusOf,
  toneOf,
  type OutboxRow,
  type SyncMetrics,
  type SyncStatus,
  type SyncTone,
} from '@/lib/sync';

/**
 * What the operator is shown about their own data, as data (CP67, §13.9).
 *
 * # Why none of this is in a component
 *
 * The same rule every station in this application follows, and here it has a sharper edge than
 * usual. §13.9's whole argument is that *a sync indicator that lies destroys trust permanently*,
 * and an indicator computed inside a `.tsx` file is one nobody can check — this container cannot
 * render React Native at all, so a status assembled in a component is a status with no test. So
 * the pill's status comes from `statusOf` in `lib/sync`, its prominence from `toneOf` beside it,
 * and everything below is the arrangement of those two into sentences and lists. `SyncPill.tsx`,
 * `SyncPanel.tsx` and `SyncItems.tsx` between them hold no rule at all.
 *
 * # The security line, and why it ends where it does
 *
 * The checkpoint's own words: *failure reasons must not leak information the operator's role
 * cannot see.* A rejection carries two things — a `reason_code`, which is a fixed vocabulary from
 * the contract, and a `reason`, which is free prose the server wrote. The code is safe by
 * construction: there are eight of them, they are enumerated in `REASON_CODES`, and each maps to a
 * sentence written here, in both languages, about what to do. The prose is not safe by
 * construction and cannot be made so — it is written for whoever reads the server's log, a future
 * server can put anything in it, and "anything" on this path plausibly includes a field name, a
 * validation message quoting a value, or a clinical number.
 *
 * So `reasonFor` returns the code's sentence to everybody and the server's prose only to a reader
 * who holds `sync.quarantine.read` — the permission the API itself requires before it will show
 * anybody the contents of a held event, and the one the contract calls sensitive in as many words.
 * The rule is therefore not a guess about what is probably harmless: it is the server's own
 * decision about who may read a refused event's content, applied to the copy of that content that
 * happens to be sitting on the tablet. A clinical assistant sees the same eight sentences whatever
 * the clinic wrote; a physician holding the tablet sees what a physician could have read anyway.
 *
 * Two things this deliberately does **not** do. It does not treat the tablet's local permission
 * list as a security boundary — the server decides, always, and this only decides what to draw.
 * And it does not fall back to the prose when a code is unrecognised: an unknown code gets
 * `reasonRefused`, because "we do not know why" is a true sentence and showing the server's words
 * to somebody who may not read them in order to be more helpful is precisely the trade this line
 * exists to refuse.
 */

// --- who is reading ---

/**
 * The permission that decides whether the clinic's own words may be drawn.
 *
 * `GET /v1/sync/quarantine/{id}` requires it, and the contract's note on that route says why it is
 * sensitive: the single-event read is where a refused event's *content* lives, deliberately split
 * from the list so that a screen showing counts never shows a measurement. This is the same
 * question about the same content, asked on a device instead of a server.
 */
export const CLINIC_PROSE_PERMISSION = 'sync.quarantine.read';

export interface Viewer {
  /** The permissions the session says this operator holds. Absent is treated as none. */
  permissions?: readonly string[];
}

export function mayReadClinicProse(viewer: Viewer | undefined): boolean {
  return viewer?.permissions?.includes(CLINIC_PROSE_PERMISSION) === true;
}

// --- the reason one entry did not go ---

export interface ReasonView {
  /** The message key, under `sync`. Always set, in both languages. */
  key: string;
  /** The contract's code, for a support call. Never a sentence. */
  code: string;
  /**
   * The clinic's own words, or null.
   *
   * Null for every reader who could not have read the event's content on the server. That is the
   * common case on a station tablet and is not a degraded one: the sentence behind `key` says what
   * happened and what to do, which is what the operator needs.
   */
  prose: string | null;
}

export function reasonFor(row: OutboxRow, viewer?: Viewer): ReasonView {
  const code = row.reasonCode ?? '';
  const prose = row.reason ?? '';
  return {
    key: reasonKey(code),
    code: code === '' ? REASON_CODES.refused : code,
    prose: prose !== '' && mayReadClinicProse(viewer) ? prose : null,
  };
}

// --- what a person can do about one entry ---

/**
 * The acts CP67 gives an operator, and the states in which each is honest.
 *
 *   - `correct` — restate the measurement. Offered only where restating it could plausibly change
 *     the answer, which is a property of the refusal rather than of the entry.
 *   - `escalate` — hand it to somebody who can act. Offered on every refusal without exception,
 *     including the ones that are also correctable: an operator who does not know what was wrong
 *     with an entry must never be cornered into either guessing at a value or leaving the screen.
 *   - `wait` — nothing to do here. The entry is at the clinic and a physician has it.
 */
export const ACTS = ['correct', 'escalate', 'wait'] as const;
export type Act = (typeof ACTS)[number];

/**
 * Which refusals an operator can answer by looking at the entry again.
 *
 * Two, and the shortness of the list is the point. `INVALID_PAYLOAD` means the clinic would not
 * take the values as sent, and `SEQUENCE_CONFLICT` means the record moved underneath the entry —
 * in both, a person at the station with the patient's chart can look and restate.
 *
 * Everything else is deliberately outside it. `UNKNOWN_EVENT_TYPE` is a clinic running an older
 * server than this app: no number the operator types changes it, and offering a correction would
 * be asking somebody to retype a perfectly good blood pressure until the clinic upgrades.
 * `DEVICE_REVOKED` and `DEVICE_SUSPENDED` are about the tablet. `CLOCK_IMPLAUSIBLE` is about the
 * tablet's clock, which no station screen can set. An unrecognised code — a newer server saying
 * something this build has never heard of — is outside it too, and that is the safe direction: a
 * correction offered for an unknown reason is an invitation to overwrite a real measurement in
 * response to a message nobody has read.
 */
const CORRECTABLE_CODES: readonly string[] = [
  REASON_CODES.invalidPayload,
  REASON_CODES.sequenceConflict,
];

/**
 * Whether the payload is one this application knows how to restate.
 *
 * The second half of `correctable`, and separate from the first because they fail for different
 * reasons and a screen has to say so differently. A refusal can be correctable in principle while
 * the entry is of a kind — an allergy, a counselling tick, a queue movement — whose correction
 * belongs to the station that owns that form and not to a sync screen. Only a measurement carrying
 * a numeric value and a unit is restated here.
 */
export function hasRestateableValue(row: OutboxRow): boolean {
  try {
    const payload = JSON.parse(row.payload) as Record<string, unknown>;
    return typeof payload.value === 'number' && typeof payload.unit === 'string';
  } catch {
    // A payload that will not parse is a row this build cannot reason about at all. It is still
    // listed, still counted and still escalatable; it is simply not offered a value editor.
    return false;
  }
}

export function actsFor(row: OutboxRow): Act[] {
  // Deliberately not a function of the viewer. Answering a refusal of your own entry needs no
  // permission — the same argument `features/corrections` records for the same act — and a
  // client-side gate here would be a locked control in front of the one person who can press it.
  // What the viewer's permissions decide is what the *reason* may say, and that is `reasonFor`.
  if (!(OPERATOR_ACTIONABLE_STATES as readonly string[]).includes(row.state)) return ['wait'];
  const acts: Act[] = [];
  if (CORRECTABLE_CODES.includes(row.reasonCode ?? '') && hasRestateableValue(row)) {
    acts.push('correct');
  }
  // Not offered again on an entry that has already been escalated: the act is telling a person,
  // and it has been done. Correcting one still is — an operator who has since worked out what was
  // wrong should not have to undo an escalation first — which is why this is the only act that
  // drops off rather than the whole row becoming inert.
  if (row.state !== 'ESCALATED') acts.push('escalate');
  return acts;
}

/** The measurement as it was refused, for the field the operator retypes it in. */
export interface RefusedValue {
  value: number;
  unit: string;
  /** The observation code, so the screen can name the field rather than print an LOINC number. */
  code: string;
}

export function refusedValueOf(row: OutboxRow): RefusedValue | null {
  try {
    const payload = JSON.parse(row.payload) as Record<string, unknown>;
    if (typeof payload.value !== 'number' || typeof payload.unit !== 'string') return null;
    return {
      value: payload.value,
      unit: payload.unit,
      code: typeof payload.code === 'string' ? payload.code : row.eventType,
    };
  } catch {
    return null;
  }
}

// --- the list ---

/**
 * The message key naming what kind of entry a row is.
 *
 * A list that said `OBSERVATION_RECORDED` would fail acceptance criterion 4 by itself — the
 * criterion is that a non-technical staff member can interpret every state, and an event type is
 * the most technical thing in the system. The mapping is by observation code where the payload
 * carries one, because "blood pressure" and "weight" are the same event type and are not the same
 * thing to the person deciding whether to chase it.
 *
 * Unknown codes fall back to `entryMeasurement` — "a measurement" — rather than to the raw string.
 * A build older than the clinic's will meet codes it has never seen, and the honest answer there
 * is a vaguer noun, not a token.
 *
 * The codes are written out here rather than imported from `features/vitals` and
 * `features/anthropometry`, and the duplication is the deliberate kind this codebase already keeps
 * for the clinic's UTC offset: a feature reaching into two other features' internals to build a
 * label would drag two stations' forms — and, through their indexes, two React Native screens —
 * into the one module the sync pill imports on every screen in the application. The seam is a
 * test: `sync-attention.test.ts` asserts this map covers every code those two stations can
 * actually send, so a station that adds a field cannot quietly leave this list showing "a
 * measurement" for it.
 */
const ENTRY_BY_CODE: Record<string, string> = {
  BP_SYSTOLIC: 'entrySystolic',
  BP_DIASTOLIC: 'entryDiastolic',
  HEART_RATE: 'entryPulse',
  SPO2: 'entrySpo2',
  BODY_TEMP: 'entryTemperature',
  RESP_RATE: 'entryRespiratory',
  BODY_HEIGHT: 'entryHeight',
  BODY_WEIGHT: 'entryWeight',
  WAIST_CIRC: 'entryWaist',
  HIP_CIRC: 'entryHip',
  BODY_FAT_PCT: 'entryBodyFat',
  MUSCLE_MASS: 'entryMuscle',
};

const ENTRY_BY_EVENT: Record<string, string> = {
  OBSERVATION_RECORDED: 'entryMeasurement',
  ALLERGY_RECORDED: 'entryAllergy',
  ALLERGY_WITHDRAWN: 'entryAllergy',
  ALLERGY_STATUS_ASSERTED: 'entryAllergy',
  COUNSELING_ITEM_TICKED: 'entryCounseling',
  COUNSELING_ITEM_UNTICKED: 'entryCounseling',
  QUEUE_ENTERED: 'entryQueue',
  QUEUE_CALLED: 'entryQueue',
  QUEUE_LEFT: 'entryQueue',
  PATIENT_REGISTERED: 'entryRegistration',
  PATIENT_DEMOGRAPHICS_CORRECTED: 'entryRegistration',
  VISIT_OPENED: 'entryVisit',
  VISIT_CLOSED: 'entryVisit',
};

export function entryKey(row: OutboxRow): string {
  try {
    const payload = JSON.parse(row.payload) as Record<string, unknown>;
    const code = typeof payload.code === 'string' ? payload.code : '';
    const named = ENTRY_BY_CODE[code];
    if (named !== undefined) return named;
  } catch {
    // Fall through to the event type, which is always present.
  }
  return ENTRY_BY_EVENT[row.eventType] ?? 'entryOther';
}

/** One row as the detail screen draws it. */
export interface SyncItem {
  eventId: string;
  /** What a person calls this entry. A message key under `sync`. */
  entryKey: string;
  /** When the measurement was taken, ISO 8601, exactly as recorded. */
  occurredAt: string;
  state: OutboxRow['state'];
  /** The message key for the line under the title. Always set. */
  statusKey: string;
  reason: ReasonView | null;
  acts: Act[];
  /** The short name an operator reads out. */
  reference: string;
}

/**
 * The line that says where one entry is.
 *
 * A key per state rather than per status, because this list is where a person answers "what is
 * happening to *that* one" and the six pill statuses collapse exactly the distinctions they need.
 * `BLOCKED_LOCAL` and `AWAITING_TRIAGE` in particular read identically on a pill and mean two
 * unrelated things here: one waits for an earlier entry on this tablet, the other for a physician
 * at the clinic.
 *
 * The keys are the sync panel's own count labels rather than a second set worded for this screen,
 * and that is a correctness property rather than thrift: two names for one state is how an
 * operator ends up believing there are two states. "Waiting for room at the clinic" on the panel
 * and something else here would be read as two different things happening to two entries.
 */
const ITEM_STATUS_KEYS: Record<string, string> = {
  PENDING: 'queuedLabel',
  IN_FLIGHT: 'inFlightLabel',
  BLOCKED_LOCAL: 'blockedLabel',
  AWAITING_TRIAGE: 'awaitingTriageLabel',
  NEEDS_ATTENTION: 'attentionLabel',
  ESCALATED: 'escalatedLabel',
  HELD: 'heldLabel',
};

export function itemOf(row: OutboxRow, viewer?: Viewer): SyncItem {
  const needsReason =
    row.state === 'NEEDS_ATTENTION' ||
    row.state === 'ESCALATED' ||
    row.state === 'HELD' ||
    row.state === 'AWAITING_TRIAGE';
  return {
    eventId: row.eventId,
    entryKey: entryKey(row),
    occurredAt: row.occurredAt,
    state: row.state,
    // An unknown state gets the honest fallback rather than a blank line, and the fallback is
    // "waiting to be sent" for the same reason `statusOf` ends the way it does: a row nobody has
    // written a case for is still work that has not reached the clinic.
    statusKey: ITEM_STATUS_KEYS[row.state] ?? 'queuedLabel',
    reason: needsReason ? reasonFor(row, viewer) : null,
    acts: actsFor(row),
    reference: referenceOf(row.eventId),
  };
}

export interface SyncItems {
  /** Refusals this operator is being asked to answer. Drawn first, and only these are loud. */
  needsYou: SyncItem[];
  /** Refusals already handed on. Kept visible; not asking for anything. */
  escalated: SyncItem[];
  /** At the clinic, waiting for a physician. Nothing to do here. */
  atClinic: SyncItem[];
  /** Everything still on its way. The ordinary queue. */
  onItsWay: SyncItem[];
}

/**
 * The queue, split by who is next to act.
 *
 * Ordering within each group is oldest first — `seq`, which is the order the operator worked in —
 * and that is a decision rather than a default. Newest first is what a feed does; this is a list
 * of work, and the oldest refusal is both the one most likely to be forgotten and the one whose
 * patient has most likely already left. Same argument the correction queue records for the same
 * reason (`features/corrections/state.ts`).
 */
export function itemsOf(rows: readonly OutboxRow[], viewer?: Viewer): SyncItems {
  const ordered = [...rows].sort((a, b) => a.seq - b.seq);
  const items: SyncItems = { needsYou: [], escalated: [], atClinic: [], onItsWay: [] };
  for (const row of ordered) {
    const item = itemOf(row, viewer);
    if (row.state === 'NEEDS_ATTENTION') items.needsYou.push(item);
    else if (row.state === 'ESCALATED') items.escalated.push(item);
    else if (row.state === 'HELD') items.atClinic.push(item);
    else items.onItsWay.push(item);
  }
  return items;
}

// --- the pill ---

export interface PillView {
  status: SyncStatus;
  tone: SyncTone;
  /** The message key, under `sync`. */
  key: string;
  /** The number the sentence names. Zero where the sentence names none. */
  count: number;
  /**
   * Everything undelivered, always, whatever the sentence says.
   *
   * Carried separately so the pill can put it in the accessibility label even when the headline is
   * about a subset. A screen reader user hearing "3 need you" must be able to learn that eleven
   * other entries are also still on the tablet.
   */
  undelivered: number;
}

/**
 * The persistent pill, decided here (CP67, acceptance criterion 1).
 *
 * `statusOf` decides the status and this decides the sentence, which is the split that keeps the
 * criterion checkable: **there is no path through this function that produces the "synced" key
 * while `undelivered` is above zero**, because the key comes from the status and the status
 * cannot be `synced` unless every count is zero. A component that assembled a sentence from the
 * counts directly could — and eventually would, on the day somebody added a state.
 *
 * # Offline changes three sentences and no statuses
 *
 * Being offline is a fact about the device; being undelivered is a fact about the work. They are
 * not the same fact and the pill must not merge them, so `online` never changes the status and
 * never changes the tone — it changes the wording of the three calm states, and only those.
 *
 * The reason is the implicit promise each state makes. "12 waiting to be sent" promises that they
 * are going; with no signal they are not, and the operator deserves to know it is the connection
 * rather than the clinic. "Everything is with the clinic" makes no such promise, and it stays true
 * word for word in a corridor — so the offline version of it says both things rather than
 * retracting the reassurance, because the work really is safe and an operator who reads "offline"
 * over an empty queue at the end of a shift will carry the tablet back to a desk to be sure.
 *
 * The loud states are left alone deliberately. An entry the clinic refused is refused whether or
 * not there is signal now, and prefixing "offline" onto it would add a second thing to think about
 * to the one message on this screen that asks for an action.
 */
export function pillFor(metrics: SyncMetrics, options: { online: boolean }): PillView {
  const status = statusOf(metrics);
  const tone = toneOf(status);
  const undelivered = metrics.undelivered;
  const offline = !options.online;

  switch (status) {
    case 'synced':
      return {
        status,
        tone,
        key: offline ? 'pillOfflineClear' : 'pillSynced',
        count: 0,
        undelivered,
      };
    case 'syncing':
      // Offline while a batch is in flight is the ordinary shape of a connection that dropped
      // mid-request: the rows are still marked in flight and will be asked about when it returns.
      // Saying "sending" over no connection would be the indicator claiming activity it does not
      // have, so the offline wording wins and counts the whole queue rather than the batch.
      return offline
        ? { status, tone, key: 'pillOffline', count: undelivered, undelivered }
        : { status, tone, key: 'pillSyncing', count: metrics.inFlight, undelivered };
    case 'queued':
      return offline
        ? { status, tone, key: 'pillOffline', count: undelivered, undelivered }
        : { status, tone, key: 'pillQueued', count: metrics.queued + metrics.blocked, undelivered };
    case 'stalled':
      return { status, tone, key: 'pillStalled', count: metrics.awaitingTriage, undelivered };
    case 'escalated':
      return { status, tone, key: 'pillEscalated', count: metrics.escalated, undelivered };
    case 'attention':
      return {
        status,
        tone,
        key: 'pillAttention',
        count: metrics.needsAttention + metrics.held,
        undelivered,
      };
    default:
      return { status, tone, key: 'pillHalted', count: undelivered, undelivered };
  }
}

// --- the offline banner ---

/**
 * What still works with no connection, said plainly (CP67).
 *
 * A banner that says only "no connection" leaves an operator to guess, and the two guesses are
 * both expensive: one stops working and writes on paper for an hour, the other carries on and
 * quietly disbelieves the screen for the rest of the day. So the banner names the three things
 * that work and the one that does not, in that order — reassurance first, because the operator is
 * mid-measurement and the question in their head is whether to keep going.
 *
 * A list rather than one long sentence, and a list defined here rather than assembled in the
 * component, so that a test can assert every line has words in both languages. A banner with a
 * blank line in Bangla is the one failure this list can have, and it would appear only on the
 * tablets least able to report it.
 */
export const OFFLINE_FACTS = [
  /** Entry works. First, because it is what they are doing right now. */
  'offlineEntryWorks',
  /** The queue is safe. Second, because it is the fear. */
  'offlineQueueSafe',
  /** Records already synced can still be read. Third: what they can look up. */
  'offlineReadingWorks',
  /** And what does not. Last, and stated as a fact rather than an apology. */
  'offlineServerNeeded',
] as const;

export type OfflineFact = (typeof OFFLINE_FACTS)[number];
