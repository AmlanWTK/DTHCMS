import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  ASSERTION_KINDS,
  CERTAINTIES,
  REASON_MIN,
  SEVERITIES,
  allergyAssertionRates,
  allergyChangesKey,
  allergyCoding,
  allergyStateKey,
  assertAllergyStatus,
  assertionRequestFrom,
  emptyAllergyDraft,
  gateSatisfied,
  getAllergyState,
  hasEmergency,
  isEmergency,
  isReassuring,
  isUncoded,
  isWithdrawn,
  listAllergyChanges,
  listAllergyReactions,
  missingAllergyFields,
  missingAssertionFields,
  newEventId,
  reactionsInOrder,
  reasonAcceptable,
  recordAllergy,
  recordRequestFrom,
  statusOf,
  withdrawAllergy,
  withdrawAllergyAssertion,
  type Allergy,
  type AllergyDraft,
  type AllergyReaction,
  type AllergyState,
} from '@/features/allergies/api/allergies';
import { API_BASE_URL, ApiError, NetworkError } from '@/lib/api';

/**
 * The client half of the allergy hard stop (CP54).
 *
 * Most of this is ordinary — a path, an envelope unwrapped, a refusal that arrives as an
 * ApiError rather than as an empty list. Four things are not, and they are why the file
 * exists.
 *
 *  - **There is no override and no export that could become one.** Three acts satisfy the
 *    gate and every one of them has a person behind it. There is a named test below whose
 *    entire job is to fail if a fourth ever appears on this module or on the barrel.
 *  - **An empty list means two opposite things.** `NONE_RECORDED` is nobody having asked;
 *    `NO_KNOWN_ALLERGY` is somebody having asked and been told there are none. Nothing here
 *    derives a status from `allergies.length`, and `isReassuring` answers `true` for
 *    exactly one of the four.
 *  - **A coding is three fields or none.** Two of the three is a request the server is
 *    right to refuse, and an allergy the catalogue has no word for belongs here — marked —
 *    rather than in a note field nothing can search.
 *  - **The reason travels with exactly one assertion.** Required on "unable to assess",
 *    which is what makes the third state reviewable rather than an override with a longer
 *    name; refused on "no known allergies", and dropped here so the refused request cannot
 *    be made by leaving a box filled in.
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
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_allergy_1' },
  });
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const PATIENT_ID = '0190a8f2-0000-7000-8000-0000000000a1';
const ALLERGY_ID = '0190a8f2-0000-7000-8000-0000000000b1';
const ASSERTION_ID = '0190a8f2-0000-7000-8000-0000000000d1';

function allergy(over: Partial<Allergy> = {}): Allergy {
  return {
    id: ALLERGY_ID,
    patient_id: PATIENT_ID,
    reaction: 'RASH',
    reaction_en: 'Rash',
    reaction_bn: 'ফুসকুড়ি',
    severity: 'mild',
    certainty: 'suspected',
    recorded_at: '2026-09-01T04:00:00Z',
    recorded_by: '0190a8f2-0000-7000-8000-0000000000c1',
    ...over,
  };
}

function state(over: Partial<AllergyState> = {}): AllergyState {
  return { status: 'NONE_RECORDED', satisfied: false, allergies: [], ...over };
}

function draft(over: Partial<AllergyDraft> = {}): AllergyDraft {
  return { ...emptyAllergyDraft(), ...over };
}

const CONCEPT = {
  system: 'DTHC',
  version: '1.0',
  code: 'ALLERGEN_PENICILLIN',
  display_en: 'Penicillin',
  display_bn: 'পেনিসিলিন',
};

function reaction(over: Partial<AllergyReaction> = {}): AllergyReaction {
  return {
    reaction: 'RASH',
    display_en: 'Rash',
    display_bn: 'ফুসকুড়ি',
    is_emergency: false,
    ordering: 4,
    ...over,
  };
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('reading the status and the list', () => {
  it('hands back the whole state, not only the allergies', async () => {
    // The status is an answer the list cannot be asked for. A caller given the array alone
    // could not tell "nobody has asked" from "asked, and there are none".
    const seen = server({
      [`GET /v1/patients/${PATIENT_ID}/allergies`]: () =>
        respond(state({ status: 'NO_KNOWN_ALLERGY', satisfied: true })),
    });

    const answer = await getAllergyState(PATIENT_ID);

    expect(seen[0]!.url.startsWith(API_BASE_URL)).toBe(true);
    expect(new URL(seen[0]!.url).pathname).toBe(`/v1/patients/${PATIENT_ID}/allergies`);
    expect(answer.status).toBe('NO_KNOWN_ALLERGY');
    expect(answer.satisfied).toBe(true);
    expect(answer.allergies).toEqual([]);
  });

  it('reads the reaction vocabulary out of its envelope', async () => {
    const seen = server({
      'GET /v1/allergies/reactions': () =>
        respond({ reactions: [reaction({ reaction: 'ANAPHYLAXIS', is_emergency: true })] }),
    });

    const reactions = await listAllergyReactions();

    expect(new URL(seen[0]!.url).pathname).toBe('/v1/allergies/reactions');
    expect(reactions[0]!.is_emergency).toBe(true);
  });

  it('reads the change history out of its envelope, withdrawn rows included', async () => {
    server({
      [`GET /v1/patients/${PATIENT_ID}/allergies/history`]: () =>
        respond({
          changes: [
            {
              kind: 'ALLERGY',
              id: ALLERGY_ID,
              said: 'penicillin',
              at: '2026-08-01T04:00:00Z',
              by: 'officer-1',
              undone_at: '2026-08-02T04:00:00Z',
              undone_why: 'Wrong patient.',
            },
          ],
        }),
    });

    const changes = await listAllergyChanges(PATIENT_ID);

    expect(changes).toHaveLength(1);
    // The withdrawn row is the reason the endpoint exists; a client that filtered it would
    // hide the half a prescriber most needs.
    expect(isWithdrawn(changes[0]!)).toBe(true);
  });

  it('raises the shared ApiError when the role may not read allergies', async () => {
    server({
      [`GET /v1/patients/${PATIENT_ID}/allergies`]: () =>
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

    await expect(getAllergyState(PATIENT_ID)).rejects.toBeInstanceOf(ApiError);
  });

  it('raises a NetworkError when the request never arrived', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))),
    );

    await expect(getAllergyState(PATIENT_ID)).rejects.toBeInstanceOf(NetworkError);
  });

  it('asks QA’s rate view for a window, and omits what it was not given', async () => {
    const seen = server({
      'GET /v1/allergies/assertion-rates': () =>
        respond({
          from: '2026-08-05T00:00:00Z',
          to: '2026-09-04T00:00:00Z',
          operators: [{ asserted_by: 'officer-1', no_known: 40, unable: 1, asserted: 41 }],
        }),
    });

    await allergyAssertionRates({ from: '2026-08-05T00:00:00Z' });
    await allergyAssertionRates();
    await allergyAssertionRates({ to: '2026-09-04T00:00:00Z' });

    expect(new URL(seen[0]!.url).searchParams.get('from')).toBe('2026-08-05T00:00:00Z');
    expect(new URL(seen[0]!.url).searchParams.has('to')).toBe(false);
    // No window at all is the normal case: the server defaults to the last thirty days,
    // and a client that invented its own would flatten the change it exists to show.
    expect([...new URL(seen[1]!.url).searchParams.keys()]).toEqual([]);
    expect([...new URL(seen[2]!.url).searchParams.keys()]).toEqual(['to']);
  });
});

describe('the three ways to satisfy the gate, and the absence of a fourth', () => {
  it('there is no override anywhere on this module', async () => {
    /*
     * The named test the checkpoint rests on.
     *
     * Every design that satisfies "no patient advances past the history station without
     * allergy status" satisfies it right up until somebody adds the kindness: a skip for
     * the unconscious patient, a "proceed anyway" with a note, a helper that marks the
     * question asked. The plan already names what that produces — a gate with a way past
     * it is a gate people learn the shape of, and within a month the override is the
     * normal path — so the third state exists instead, with a person's name and a reason
     * on it.
     *
     * This asserts the absence rather than trusting a comment. Anything on this module or
     * on the barrel whose name suggests a way past fails it, and the three writes that do
     * exist are checked to be the only ones that reach the server.
     */
    const forbidden = /(skip|override|bypass|proceed|waive|unblock|force|dismissgate|ignore)/i;

    const surface = await import('@/features/allergies/api/allergies');
    const suspicious = Object.keys(surface).filter((name) => forbidden.test(name));
    expect(suspicious, `Exports that look like a way past: ${suspicious.join(', ')}`).toEqual([]);

    // The barrel too — a caller reaches this feature through there, and an export added
    // for one screen's convenience is how the discipline is lost.
    const barrel = await import('@/features/allergies');
    const barrelSuspicious = Object.keys(barrel).filter((name) => forbidden.test(name));
    expect(barrelSuspicious).toEqual([]);

    // And there are exactly two assertion kinds. A third enum value is the other shape an
    // override would arrive in, wearing the vocabulary's clothes.
    expect(ASSERTION_KINDS).toEqual(['NO_KNOWN_ALLERGY', 'UNABLE_TO_ASSESS']);
  });

  it('records one allergy with the forgery guard, a fresh key and an event id', async () => {
    const seen = server({
      [`POST /v1/patients/${PATIENT_ID}/allergies`]: () =>
        respond(
          state({ status: 'ALLERGIES_RECORDED', satisfied: true, allergies: [allergy()] }),
          201,
        ),
    });

    const first = await recordAllergy(PATIENT_ID, {
      reaction: 'RASH',
      severity: 'mild',
      certainty: 'suspected',
    });
    await recordAllergy(PATIENT_ID, {
      reaction: 'ITCHING',
      severity: 'mild',
      certainty: 'suspected',
    });

    for (const request of seen) {
      expect(request.headers.get('X-Requested-With')).toBe('DTHCMS');
      expect(request.headers.get('Idempotency-Key')).toMatch(UUID);
    }
    // Two allergies are two records. A shared key would be answered from the store, which
    // looks like success and quietly records one.
    expect(seen[0]!.headers.get('Idempotency-Key')).not.toBe(
      seen[1]!.headers.get('Idempotency-Key'),
    );
    expect((await seen[0]!.json()).event_id).toMatch(UUID);
    // The whole state comes back, because recording moved the gate.
    expect(first.satisfied).toBe(true);
  });

  it('asserts "no known allergies" with no reason, so the refused request cannot be made', async () => {
    const seen = server({
      [`POST /v1/patients/${PATIENT_ID}/allergies/assert`]: () =>
        respond(state({ status: 'NO_KNOWN_ALLERGY', satisfied: true }), 201),
    });

    // Even given one. The server refuses a reason on this kind — text nobody will read,
    // answering a question nobody asked — and a form that kept the box filled in while the
    // operator changed their mind would otherwise send it.
    await assertAllergyStatus(
      PATIENT_ID,
      assertionRequestFrom('NO_KNOWN_ALLERGY', 'she seemed fine'),
    );

    const body = (await seen[0]!.json()) as Record<string, unknown>;
    expect(body.kind).toBe('NO_KNOWN_ALLERGY');
    expect(body).not.toHaveProperty('reason');
    expect(new URL(seen[0]!.url).pathname).toBe(`/v1/patients/${PATIENT_ID}/allergies/assert`);
  });

  it('asserts "unable to assess" with the reason that makes it reviewable', async () => {
    const seen = server({
      [`POST /v1/patients/${PATIENT_ID}/allergies/assert`]: () =>
        respond(state({ status: 'UNABLE_TO_ASSESS', satisfied: true }), 201),
    });

    const answer = await assertAllergyStatus(
      PATIENT_ID,
      assertionRequestFrom('UNABLE_TO_ASSESS', '  Patient is drowsy and no attendant present.  '),
    );

    expect(await seen[0]!.json()).toMatchObject({
      kind: 'UNABLE_TO_ASSESS',
      reason: 'Patient is drowsy and no attendant present.',
    });
    // It satisfies the gate, and it is emphatically not a claim that there are none.
    expect(answer.satisfied).toBe(true);
    expect(isReassuring(answer.status)).toBe(false);
  });

  it('names the reason as missing on the third state and never on the first', () => {
    expect(missingAssertionFields('UNABLE_TO_ASSESS', '   ')).toEqual(['reason']);
    expect(missingAssertionFields('UNABLE_TO_ASSESS', 'No attendant.')).toEqual([]);
    // A reason is not asked for here, so its absence can never block the act.
    expect(missingAssertionFields('NO_KNOWN_ALLERGY', '')).toEqual([]);
  });

  it('carries the visit on a write when there is one, and nothing when there is not', () => {
    expect(assertionRequestFrom('NO_KNOWN_ALLERGY', '', 'visit-1')).toEqual({
      kind: 'NO_KNOWN_ALLERGY',
      visit_id: 'visit-1',
    });
    expect(Object.keys(assertionRequestFrom('NO_KNOWN_ALLERGY', ''))).toEqual(['kind']);

    const request = recordRequestFrom(
      draft({ said: 'penicillin', reaction: 'RASH', severity: 'mild', certainty: 'confirmed' }),
      'visit-1',
    );
    expect(request.visit_id).toBe('visit-1');
  });
});

describe('taking something back', () => {
  it('withdraws one allergy with a reason and hands back the resulting status', async () => {
    const seen = server({
      [`POST /v1/allergies/${ALLERGY_ID}/withdraw`]: () =>
        respond(state({ status: 'NONE_RECORDED', satisfied: false })),
    });

    const after = await withdrawAllergy(ALLERGY_ID, 'Recorded on the wrong patient.');

    expect(new URL(seen[0]!.url).pathname).toBe(`/v1/allergies/${ALLERGY_ID}/withdraw`);
    expect(await seen[0]!.json()).toMatchObject({ reason: 'Recorded on the wrong patient.' });
    // Withdrawing the last allergy re-closes the gate, and the caller must not be left to
    // guess: a header still saying "satisfied" over a patient who can no longer be queued
    // is the failure this response shape exists to prevent.
    expect(gateSatisfied(after)).toBe(false);
    expect(statusOf(after)).toBe('NONE_RECORDED');
  });

  it('withdraws a standing assertion on its own path', async () => {
    const seen = server({
      [`POST /v1/allergies/assertions/${ASSERTION_ID}/withdraw`]: () =>
        respond(state({ status: 'NONE_RECORDED', satisfied: false })),
    });

    await withdrawAllergyAssertion(ASSERTION_ID, 'Tapped on the wrong patient.');

    expect(new URL(seen[0]!.url).pathname).toBe(
      `/v1/allergies/assertions/${ASSERTION_ID}/withdraw`,
    );
    expect(seen[0]!.headers.get('X-Requested-With')).toBe('DTHCMS');
  });

  it('mirrors the server’s floor under a withdrawal reason', () => {
    expect(REASON_MIN).toBe(1);
    expect(reasonAcceptable('   ')).toBe(false);
    expect(reasonAcceptable('Wrong patient')).toBe(true);
  });
});

describe('an empty list means two opposite things', () => {
  it('reads the status off the state and never off the length of the list', () => {
    const nobodyAsked = state({ status: 'NONE_RECORDED', satisfied: false });
    const askedAndNone = state({ status: 'NO_KNOWN_ALLERGY', satisfied: true });

    // Identical lists. Opposite facts.
    expect(nobodyAsked.allergies).toEqual(askedAndNone.allergies);
    expect(statusOf(nobodyAsked)).not.toBe(statusOf(askedAndNone));
    expect(gateSatisfied(nobodyAsked)).toBe(false);
    expect(gateSatisfied(askedAndNone)).toBe(true);
  });

  it('calls exactly one of the four statuses reassuring', () => {
    expect(isReassuring('NO_KNOWN_ALLERGY')).toBe(true);
    expect(isReassuring('NONE_RECORDED')).toBe(false);
    // Somebody looked at an unconscious patient and could not find out. That is not the
    // same sentence as "there are none", and everything downstream depends on it not being
    // rounded up to one.
    expect(isReassuring('UNABLE_TO_ASSESS')).toBe(false);
    expect(isReassuring('ALLERGIES_RECORDED')).toBe(false);
  });
});

describe('what leads, and what is coded', () => {
  it('calls an emergency reaction an emergency whatever severity was ticked', () => {
    // `is_emergency` is a property of the reaction. Anaphylaxis with "mild" beside it is
    // still the thing that stops a heart.
    expect(isEmergency(allergy({ reaction: 'ANAPHYLAXIS', is_emergency: true }))).toBe(true);
    // And a severity that says it could kill is enough on its own.
    expect(isEmergency(allergy({ severity: 'life_threatening' }))).toBe(true);
    expect(isEmergency(allergy())).toBe(false);

    expect(hasEmergency(state({ allergies: [allergy(), allergy({ is_emergency: true })] }))).toBe(
      true,
    );
    expect(hasEmergency(state({ allergies: [allergy()] }))).toBe(false);
  });

  it('reads an allergy’s coding whole or not at all', () => {
    expect(
      allergyCoding(
        allergy({ code_system: 'DTHC', code_version: '1.0', code: 'ALLERGEN_PENICILLIN' }),
      ),
    ).toEqual({
      system: 'DTHC',
      version: '1.0',
      code: 'ALLERGEN_PENICILLIN',
      display_en: 'ALLERGEN_PENICILLIN',
    });

    // Half a coding is not a coding. A chip rendered from this would show a code with no
    // version, which is the one failure CP52 exists to prevent.
    expect(allergyCoding(allergy({ code_system: 'DTHC', code: 'ALLERGEN_PENICILLIN' }))).toBeNull();
    expect(isUncoded(allergy({ code_system: 'DTHC', code: 'ALLERGEN_PENICILLIN' }))).toBe(true);
    expect(isUncoded(allergy({ said: 'the yellow tablet' }))).toBe(true);
    expect(
      isUncoded(allergy({ code_system: 'DTHC', code_version: '1.0', code: 'ALLERGEN_X' })),
    ).toBe(false);
  });

  it('keeps both displays on the coding it hands to a chip', () => {
    expect(
      allergyCoding(
        allergy({
          code_system: 'DTHC',
          code_version: '1.0',
          code: 'ALLERGEN_PENICILLIN',
          display_en: 'Penicillin',
          display_bn: 'পেনিসিলিন',
        }),
      ),
    ).toMatchObject({ display_en: 'Penicillin', display_bn: 'পেনিসিলিন' });
  });

  it('offers the emergency reactions first, then the server’s own order', () => {
    const ordered = reactionsInOrder([
      reaction({ reaction: 'RASH', ordering: 4 }),
      reaction({ reaction: 'ANAPHYLAXIS', is_emergency: true, ordering: 1 }),
      reaction({ reaction: 'ITCHING', ordering: 5 }),
      reaction({ reaction: 'BREATHING', is_emergency: true, ordering: 2 }),
    ]);

    expect(ordered.map((one) => one.reaction)).toEqual([
      'ANAPHYLAXIS',
      'BREATHING',
      'RASH',
      'ITCHING',
    ]);
  });
});

describe('the fields a record needs', () => {
  it('asks for the substance, the reaction, the severity and the certainty', () => {
    // Severity and certainty have no default anywhere: a severity nobody stated is a claim
    // nobody made, sitting in the record a pharmacist reads before handing over a medicine.
    expect(missingAllergyFields(draft())).toEqual(['said', 'reaction', 'severity', 'certainty']);
    expect(
      missingAllergyFields(
        draft({ concept: CONCEPT, reaction: 'RASH', severity: 'mild', certainty: 'suspected' }),
      ),
    ).toEqual([]);
    // The uncoded escape hatch: her own words are enough to identify the substance.
    expect(
      missingAllergyFields(
        draft({
          said: 'the yellow tablet',
          reaction: 'RASH',
          severity: 'mild',
          certainty: 'suspected',
        }),
      ),
    ).toEqual([]);
  });

  it('sends the system, the version and the code together', () => {
    const request = recordRequestFrom(
      draft({ concept: CONCEPT, reaction: 'RASH', severity: 'severe', certainty: 'confirmed' }),
    );

    expect(request).toMatchObject({
      code_system: 'DTHC',
      code_version: '1.0',
      code: 'ALLERGEN_PENICILLIN',
      reaction: 'RASH',
      severity: 'severe',
      certainty: 'confirmed',
    });
  });

  it('sends none of the three when the catalogue had nothing, keeping her words', () => {
    const request = recordRequestFrom(
      draft({
        said: '  the yellow tablet from the pharmacy near the bridge  ',
        reaction: 'SWELLING_FACE',
        severity: 'severe',
        certainty: 'suspected',
        note: '  after the evening meal  ',
      }),
    );

    expect(request).not.toHaveProperty('code');
    expect(request).not.toHaveProperty('code_system');
    expect(request).not.toHaveProperty('code_version');
    expect(request.said).toBe('the yellow tablet from the pharmacy near the bridge');
    expect(request.note).toBe('after the evening meal');
  });

  it('drops an empty note rather than sending one', () => {
    const request = recordRequestFrom(
      draft({ said: 'penicillin', reaction: 'RASH', severity: 'mild', certainty: 'suspected' }),
    );
    expect(request).not.toHaveProperty('note');
  });

  it('raises an ApiError carrying the named field on a 422', () => {
    // The form renders this against the control the server named, which is the difference
    // between "something is wrong" and "say how bad it was".
    server({
      [`POST /v1/patients/${PATIENT_ID}/allergies`]: () =>
        respond(
          {
            error: {
              code: 'validation_failed',
              kind: 'validation',
              message: 'The request could not be processed.',
              message_bn: 'অনুরোধটি প্রক্রিয়া করা যায়নি।',
              fields: { reaction: 'That reaction is not in the vocabulary.' },
            },
          },
          422,
        ),
    });

    return expect(
      recordAllergy(PATIENT_ID, { reaction: 'SNEEZE', severity: 'mild', certainty: 'suspected' }),
    ).rejects.toMatchObject({ fields: { reaction: 'That reaction is not in the vocabulary.' } });
  });

  it('offers the severities and certainties the contract allows', () => {
    // `life_threatening` is here and not in the history feature's list: a complaint can be
    // severe, and only an allergy can be the thing that kills somebody on the way home.
    expect(SEVERITIES).toEqual(['mild', 'moderate', 'severe', 'life_threatening']);
    expect(CERTAINTIES).toEqual(['suspected', 'confirmed']);
  });

  it('spells one patient’s cache keys the same way everywhere', () => {
    // The header reads the state key and the panel writes it, from different files. Two
    // spellings is a header that keeps saying "nobody has asked" after somebody just did.
    expect(allergyStateKey(PATIENT_ID)).toEqual(['allergies', 'state', PATIENT_ID]);
    expect(allergyChangesKey(PATIENT_ID)).toEqual(['allergies', 'changes', PATIENT_ID]);
  });

  it('mints a fresh event id each time', () => {
    expect(newEventId()).toMatch(UUID);
    expect(newEventId()).not.toBe(newEventId());
  });

  it('starts a draft with nothing chosen in it', () => {
    expect(emptyAllergyDraft()).toEqual({
      concept: null,
      said: '',
      reaction: '',
      severity: '',
      certainty: '',
      note: '',
    });
  });
});
