import { NetworkError, writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * Critical values, typed against the contract (CP50, §4.4).
 *
 * Six calls, and one of them is deliberately not shaped like its neighbours.
 *
 * Everything that reads goes through `unwrap`, which is the house rule: a refusal becomes an
 * ApiError and an unreachable clinic becomes a NetworkError, because those are two different
 * sentences on a screen and the component picks between them by the class it caught.
 *
 * `acknowledgeAlert` cannot use it. The server answers a second acknowledgement with **409
 * and the alert attached**, so that the screen can say who already has it — and an ApiError
 * carries a code and a message, not a body. Passing that response through `unwrap` would
 * throw the one fact the moment needs on the floor, and the physician would be told
 * "something went wrong" about a situation in which nothing went wrong at all: two
 * clinicians reached for the same alert, which is the system working.
 *
 * The pull is the truth. The realtime gateway pushes `critical_value.raised` on the
 * patient's topic, but a board that trusted only the socket would go quiet after a dropped
 * connection and look exactly like a clinic with nothing wrong in it. Callers poll.
 */

export type CriticalAlert = components['schemas']['CriticalAlert'];
export type CriticalValueRule = components['schemas']['CriticalValueRule'];
export type EscalationStep = components['schemas']['EscalationStep'];

/** Everything unacknowledged in this facility. Not paginated — see the contract. */
export async function listOpenAlerts(limit?: number): Promise<CriticalAlert[]> {
  const body = await unwrap(
    api.GET('/v1/alerts', { params: { query: limit === undefined ? {} : { limit } } }),
  );
  return body.alerts;
}

/** One alert, whatever its status. */
export async function readAlert(id: string): Promise<CriticalAlert> {
  const body = await unwrap(api.GET('/v1/alerts/{id}', { params: { path: { id } } }));
  return body.alert;
}

/**
 * One patient's history, acknowledged ones included.
 *
 * The acknowledged ones are the point of the call. A saturation of 90% in somebody who has
 * had three this year is a different conversation from a first one, and an alert that
 * vanished once somebody answered it would make the record say the episode never happened.
 */
export async function listPatientAlerts(
  patientID: string,
  limit?: number,
): Promise<CriticalAlert[]> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/alerts', {
      params: { path: { id: patientID }, query: limit === undefined ? {} : { limit } },
    }),
  );
  return body.alerts;
}

/**
 * The thresholds, in the order the server resolves them.
 *
 * Reference data, and never re-ranked here. A client that sorted these could sound an alarm
 * the server did not raise or — far worse — stay quiet when it did.
 */
export async function listRules(): Promise<CriticalValueRule[]> {
  const body = await unwrap(api.GET('/v1/alerts/rules'));
  return body.rules;
}

/** Who is told when nobody answers, in order. The last step names no role, by design. */
export async function listEscalation(): Promise<EscalationStep[]> {
  const body = await unwrap(api.GET('/v1/alerts/escalation'));
  return body.steps;
}

/**
 * What happened when this clinician tried to take the alert.
 *
 * `taken` is not a failure and is not modelled as one. The alert travels with it so the
 * screen can say what is already being done rather than only that something is.
 */
export type AcknowledgeResult =
  | { outcome: 'acknowledged'; alert: CriticalAlert }
  | { outcome: 'taken'; alert: CriticalAlert | null };

/**
 * Take a critical value, and stop it escalating.
 *
 * The note is required by the server and is not paperwork — see `NOTE_MIN` below.
 */
export async function acknowledgeAlert(id: string, note: string): Promise<AcknowledgeResult> {
  const settled = await api
    .POST('/v1/alerts/{id}/acknowledge', {
      params: { ...writing(), path: { id } },
      body: { note },
    })
    .catch((cause: unknown) => {
      // The same distinction `unwrap` draws, restated because this call does not use it:
      // the request never arrived, which is "you are offline" rather than "the clinic
      // server refused this".
      throw new NetworkError(cause);
    });

  if (settled.response.status === 409) {
    return { outcome: 'taken', alert: alertOnConflict(settled.error) };
  }

  // Everything else — a 403, a 422 on a note somebody trimmed to two characters, a 500 —
  // is a genuine failure, and `unwrap` turns it into the error the screens already catch.
  const body = await unwrap<{ alert: CriticalAlert }>(Promise.resolve(settled));
  return { outcome: 'acknowledged', alert: body.alert };
}

/**
 * The alert the server attached to its 409, if it did.
 *
 * Read defensively rather than cast: this body arrives on the path where something has
 * already gone differently than expected, and a missing field here must degrade to "somebody
 * else has it" rather than to a blank screen.
 */
function alertOnConflict(body: unknown): CriticalAlert | null {
  if (typeof body !== 'object' || body === null) return null;
  const { alert } = body as { alert?: unknown };
  return typeof alert === 'object' && alert !== null ? (alert as CriticalAlert) : null;
}

/**
 * The floor the server puts under an acknowledgement, mirrored here for the form.
 *
 * A form that lets somebody type "ok" and then shows them a validation error has taught
 * them nothing and cost them a second at the worst possible moment.
 */
export const NOTE_MIN = 3;

export function noteAcceptable(note: string): boolean {
  return note.trim().length >= NOTE_MIN;
}

/** Whether the chain has moved past its first step — nobody answered in time. */
export function hasEscalated(alert: CriticalAlert): boolean {
  return alert.escalation_step > 1;
}

/**
 * The board's order: least answered first.
 *
 * Both keys are the server's own facts rather than a severity this client invented. An
 * alert's escalation step is the server saying nobody replied to the last person it told;
 * within one step, the one raised longest ago is the one that has been waiting longest.
 * Sorting by anything clinical — which code is worse than which — would be a second
 * opinion, and the rules table is the only opinion in this system.
 */
export function byUrgency(alerts: readonly CriticalAlert[]): CriticalAlert[] {
  return [...alerts].sort((a, b) => {
    if (a.escalation_step !== b.escalation_step) return b.escalation_step - a.escalation_step;
    return Date.parse(a.raised_at) - Date.parse(b.raised_at);
  });
}

/** Still waiting for somebody. The patient strip shows these; the history shows everything. */
export function stillOpen(alert: CriticalAlert): boolean {
  return alert.status !== 'ACKNOWLEDGED';
}
