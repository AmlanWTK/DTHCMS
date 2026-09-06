/**
 * What an event means for what the screens show (CP64, §13.3, §13.6).
 *
 * # One projector, two directions
 *
 * The same function is used for an event this device wrote and for the same event pulled back down
 * from the clinic later. That is not tidiness: it is what makes reconciliation safe. A station
 * records a weight offline, the event syncs, and tomorrow the device pulls the ledger from a
 * cursor that includes it — if the two paths projected differently, a value would change on screen
 * for no reason a person could see, and the device would disagree with the clinic about a number
 * nobody had touched.
 *
 * # The registry is a declaration, not a filter
 *
 * Same discipline as `internal/offline/pullable.go`: an event type gets a line here or it gets
 * nothing. An unknown type is **recorded and not projected** — never dropped, because a device
 * running an older build than the clinic is an ordinary state during a rollout, and the events it
 * cannot draw yet are still events it must keep so that a later build can.
 *
 * # Last writer by `occurred_at`, and both values kept
 *
 * §13.6's answer to the two-device question. `replaces()` in `state.ts` is the whole rule; here it
 * is only decided *what* is keyed by what. Nothing in this file deletes a projection, and
 * `local_events` keeps every event whether or not it changed the current value — which is what
 * makes "both values remain visible with full attribution" true on the device as well as the
 * server.
 */

/** An event, in the shape both the command handler and the reconciler hold it. */
export interface AppliedEvent {
  eventId: string;
  aggregateType: string;
  aggregateId: string;
  patientId: string | null;
  visitId: string | null;
  eventType: string;
  eventVersion: number;
  occurredAt: string;
  /** The clinic's arrival time. Null for an event this device has not yet delivered. */
  recordedAt: string | null;
  payload: Record<string, unknown>;
  globalSeq: number | null;
  /**
   * Who the clinic says recorded it, for the events that came down from the clinic.
   *
   * Absent for an event this device wrote and has not yet delivered, and that is the honest
   * state: attribution is assigned by the server from an unforgeable actor, so a device that
   * filled it in locally would be asserting something it cannot know. The screen draws such a
   * value as pending (§13.9), which is what it is.
   */
  actor?: { userId?: string; role?: string; station?: string; source?: string } | undefined;
}

/** One row a projector wants written. */
export interface ProjectionWrite {
  kind: string;
  key: string;
  patientId: string | null;
  document: Record<string, unknown>;
}

export type Projector = (event: AppliedEvent) => ProjectionWrite[];

function stringField(payload: Record<string, unknown>, name: string): string | null {
  const value = payload[name];
  return typeof value === 'string' && value !== '' ? value : null;
}

/**
 * A measurement, keyed by patient and code.
 *
 * The code comes from the payload when it carries one and from the event type when it does not —
 * `WEIGHT_RECORDED` is its own code. Keying by patient and code is what makes the second reading
 * of a height replace the first *as the current value* while both stay in the event log.
 */
const observation: Projector = (event) => {
  if (!event.patientId) return [];
  const code = stringField(event.payload, 'code') ?? event.eventType;
  return [
    {
      kind: 'observation',
      key: `${event.patientId}/${code}`,
      patientId: event.patientId,
      document: {
        code,
        value: event.payload.value ?? null,
        unit: event.payload.unit ?? null,
        value_code: event.payload.value_code ?? null,
        // When it was true, which is not when it was written down. The station's "last
        // recorded" line reads this one.
        effective_at: event.payload.effective_at ?? event.occurredAt,
        visit_id: event.visitId,
        event_type: event.eventType,
        // CP61: a value is one interaction away from naming who entered it. Null until the
        // clinic has told us, because until then nobody has established it.
        recorded_by: event.actor?.userId ?? null,
        recorded_role: event.actor?.role ?? null,
        station_code: event.actor?.station ?? null,
        recorded_at: event.recordedAt,
        source: event.actor?.source ?? null,
      },
    },
  ];
};

/**
 * A place in a station's queue (CP39).
 *
 * Keyed by the entry rather than the patient: a patient can be in two stations' queues at once,
 * and the call, the entry and the departure all name the same entry id so that they land on the
 * same row.
 */
const queue: Projector = (event) => {
  const entry = stringField(event.payload, 'entry_id') ?? event.aggregateId;
  const station = stringField(event.payload, 'station_code');
  if (!station) return [];
  return [
    {
      kind: 'queue',
      key: entry,
      patientId: event.patientId ?? stringField(event.payload, 'patient_id'),
      document: {
        id: entry,
        station_code: station,
        patient_id: event.patientId ?? stringField(event.payload, 'patient_id'),
        visit_id: event.visitId ?? stringField(event.payload, 'visit_id'),
        position: event.payload.position ?? 0,
        priority: event.payload.priority ?? 0,
        priority_reason: event.payload.priority_reason ?? null,
        status: queueStatusOf(event.eventType),
        entered_at: event.occurredAt,
      },
    },
  ];
};

function queueStatusOf(eventType: string): string {
  switch (eventType) {
    case 'QUEUE_CALLED':
      return 'in_service';
    case 'QUEUE_LEFT':
      return 'left';
    default:
      return 'waiting';
  }
}

/** Who the patient is. Without it a station app has an identifier and no name to put on a screen. */
const patient: Projector = (event) => {
  const id = event.patientId ?? event.aggregateId;
  return [{ kind: 'patient', key: id, patientId: id, document: { ...event.payload } }];
};

/** The visit being worked within, and whether it is still open. */
const visit: Projector = (event) => {
  const id = event.visitId ?? event.aggregateId;
  return [
    {
      kind: 'visit',
      key: id,
      patientId: event.patientId,
      document: { ...event.payload, status: statusOfVisit(event.eventType) },
    },
  ];
};

function statusOfVisit(eventType: string): string {
  switch (eventType) {
    case 'VISIT_CLOSED':
      return 'closed';
    case 'VISIT_ABANDONED':
      return 'abandoned';
    default:
      return 'open';
  }
}

/**
 * An allergy.
 *
 * On the device because CP54's third criterion says it has to reach everyone who meets the
 * patient, and a phone that had not been told is a phone at the one station where it matters most.
 * Keyed by the allergy's own id rather than by substance: a withdrawal has to land on the row it
 * withdraws, and two allergies to the same substance recorded by two people are two rows until
 * somebody decides otherwise.
 */
const allergy: Projector = (event) => {
  const id = stringField(event.payload, 'allergy_id') ?? event.aggregateId;
  if (!event.patientId) return [];
  return [
    {
      kind: 'allergy',
      key: `${event.patientId}/${id}`,
      patientId: event.patientId,
      document: {
        ...event.payload,
        withdrawn: event.eventType === 'ALLERGY_WITHDRAWN' ? 1 : 0,
      },
    },
  ];
};

/** One tick on a counselling checklist. */
const counseling: Projector = (event) => {
  const item = stringField(event.payload, 'item_code');
  if (!item) return [];
  return [
    {
      kind: 'counseling',
      key: `${event.aggregateId}/${item}`,
      patientId: event.patientId,
      document: { item_code: item, ticked: event.eventType === 'COUNSELING_ITEM_TICKED' ? 1 : 0 },
    },
  ];
};

/** A critical value somebody has to acknowledge, on the phone of somebody who may be walking. */
const alert: Projector = (event) => [
  {
    kind: 'alert',
    key: event.aggregateId,
    patientId: event.patientId,
    document: {
      ...event.payload,
      acknowledged: event.eventType === 'CRITICAL_VALUE_ACKNOWLEDGED' ? 1 : 0,
    },
  },
];

/**
 * Every event type this device draws, and what it draws it as.
 *
 * The list is the intersection of what the clinic will send (`pullable.go`) and what a station
 * screen reads. Adding one is a line here with a reason, and an event type that is not in it is
 * kept and not drawn — which is the honest behaviour for a build that is older than the clinic's.
 */
export const PROJECTORS: Record<string, Projector> = {
  PATIENT_REGISTERED: patient,
  PATIENT_DEMOGRAPHICS_CORRECTED: patient,

  VISIT_OPENED: visit,
  VISIT_CLOSED: visit,
  VISIT_REOPENED: visit,
  VISIT_ABANDONED: visit,

  OBSERVATION_RECORDED: observation,
  HEIGHT_RECORDED: observation,
  HEIGHT_CORRECTED: observation,
  WEIGHT_RECORDED: observation,
  WEIGHT_CORRECTED: observation,
  WAIST_RECORDED: observation,
  HIP_RECORDED: observation,
  BP_RECORDED: observation,
  BP_CORRECTED: observation,
  PULSE_RECORDED: observation,
  SPO2_RECORDED: observation,
  TEMP_RECORDED: observation,

  ALLERGY_RECORDED: allergy,
  ALLERGY_WITHDRAWN: allergy,
  ALLERGY_STATUS_ASSERTED: allergy,

  QUEUE_ENTERED: queue,
  QUEUE_CALLED: queue,
  QUEUE_LEFT: queue,

  COUNSELING_ITEM_TICKED: counseling,
  COUNSELING_ITEM_UNTICKED: counseling,

  CRITICAL_VALUE_ALERTED: alert,
  CRITICAL_VALUE_ACKNOWLEDGED: alert,
};

/** What this event changes on screen. Empty for a type this build does not draw. */
export function projectionsFor(event: AppliedEvent): ProjectionWrite[] {
  const projector = PROJECTORS[event.eventType];
  return projector ? projector(event) : [];
}
