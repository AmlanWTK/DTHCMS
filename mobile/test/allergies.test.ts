import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@dthcms/api-client';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import {
  ALLERGEN_SYSTEM,
  ALLERGY_FIELDS,
  ALLERGY_STATUSES,
  ANSWERS,
  ASSERTION_KINDS,
  CERTAINTIES,
  PROBLEMS,
  SEVERITIES,
  allergyLabel,
  allergyRows,
  anyEmergency,
  asked,
  assertionProblem,
  canAssert,
  canRecord,
  changeRows,
  coded,
  codingFrom,
  edit,
  emergencyOf,
  emptyDraft,
  isPristine,
  knownReaction,
  mayAdvance,
  missingFrom,
  needsReason,
  partialCoding,
  problemsWith,
  reactionLabel,
  reactionNamed,
  reactionTextOf,
  reactionsInOrder,
  readingOf,
  refusalOn,
  setCoding,
  statusKnown,
  toAssertion,
  toRecording,
  toWithdrawal,
  toneFor,
  wholeCoding,
  withdrawalRefused,
  type Allergy,
  type AllergyAssertion,
  type AllergyChange,
  type AllergyReaction,
  type AllergyState,
  type AllergyStatus,
} from '../src/features/allergies/state';

/*
 * The station binding reaches the Keystore through lib/credentials, and the native module
 * cannot load under Node. Mocked exactly as history.test.ts and terminology.test.ts do;
 * nothing here exercises it.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const allergyApi = await import('../src/features/allergies/api');
const allergyState = await import('../src/features/allergies/state');
const {
  assertAllergyStatus,
  getAllergyState,
  listAllergyChanges,
  listReactions,
  recordAllergy,
  troubleOf,
  withdrawAllergy,
  withdrawAllergyAssertion,
} = allergyApi;

/**
 * The allergy hard stop, at station 4 (CP54).
 *
 * The screen is a React Native component and is judged on the clinic's tablet by a history
 * officer with a queue behind them. What is checked here is every decision behind it, and
 * four of these tests matter more than the rest.
 *
 * **An empty list means two opposite things.** `NONE_RECORDED` and `NO_KNOWN_ALLERGY` both
 * arrive with nothing on them, and rendering either as "no allergies" is a lie in the
 * safe-looking direction — which is the exact failure this checkpoint exists to prevent.
 *
 * **Nothing here satisfies the gate without a real answer.** There are three ways, they are
 * named, and the tests assert on the exported names as well as on the behaviour: no helper
 * produces a satisfied state from nothing, nothing in this feature builds an `AllergyState` at
 * all, and there is no export whose name suggests a way past.
 *
 * **`UNABLE_TO_ASSESS` is not reassurance.** It takes a reason, it satisfies the gate, and it
 * is not the status `reassuring` is true for.
 *
 * **Recording is as cheap as asserting.** Four taps and no typing, counted here, because if it
 * were twenty the record would fill with claims instead of findings.
 */

// --- the fixtures: the reaction vocabulary as the clinic seeds it ---

const reaction = (over: Partial<AllergyReaction> & { reaction: string }): AllergyReaction =>
  ({
    display_en: over.reaction,
    display_bn: `বাংলা ${over.reaction}`,
    is_emergency: false,
    ordering: 9,
    ...over,
  }) as AllergyReaction;

const ANAPHYLAXIS = reaction({
  reaction: 'ANAPHYLAXIS',
  display_en: 'Collapse or anaphylaxis',
  display_bn: 'অজ্ঞান হয়ে পড়া',
  is_emergency: true,
  ordering: 1,
});
const BREATHING = reaction({
  reaction: 'BREATHING',
  display_en: 'Trouble breathing',
  display_bn: 'শ্বাস নিতে কষ্ট',
  is_emergency: true,
  ordering: 2,
});
const RASH = reaction({
  reaction: 'RASH',
  display_en: 'Rash',
  display_bn: 'ফুসকুড়ি',
  ordering: 4,
});
const ITCHING = reaction({
  reaction: 'ITCHING',
  display_en: 'Itching',
  display_bn: 'চুলকানি',
  ordering: 5,
});

const REACTIONS: AllergyReaction[] = [ANAPHYLAXIS, BREATHING, RASH, ITCHING];

const allergy = (over: Partial<Allergy> = {}): Allergy => ({
  id: 'allergy-1',
  patient_id: 'patient-1',
  reaction: 'RASH',
  severity: 'mild',
  certainty: 'suspected',
  recorded_at: '2026-03-11T10:15:00.000Z',
  recorded_by: 'officer-1',
  ...over,
});

const codedAllergy = (over: Partial<Allergy> = {}): Allergy =>
  allergy({
    code_system: 'DTHC',
    code_version: '1.0',
    code: 'ALLERGEN_PENICILLIN',
    display_en: 'Penicillins',
    display_bn: 'পেনিসিলিন',
    ...over,
  });

const assertion = (over: Partial<AllergyAssertion> = {}): AllergyAssertion => ({
  id: 'assertion-1',
  patient_id: 'patient-1',
  kind: 'NO_KNOWN_ALLERGY',
  asserted_at: '2026-09-04T08:41:00.000Z',
  asserted_by: 'officer-1',
  ...over,
});

/** The four statuses as the server actually sends them. */
const NOBODY_ASKED: AllergyState = {
  status: 'NONE_RECORDED',
  satisfied: false,
  allergies: [],
};
const NO_KNOWN: AllergyState = {
  status: 'NO_KNOWN_ALLERGY',
  satisfied: true,
  allergies: [],
  assertion: assertion(),
};
const UNABLE: AllergyState = {
  status: 'UNABLE_TO_ASSESS',
  satisfied: true,
  allergies: [],
  assertion: assertion({
    id: 'assertion-2',
    kind: 'UNABLE_TO_ASSESS',
    reason: 'Patient is drowsy and no attendant present.',
  }),
};
const RECORDED: AllergyState = {
  status: 'ALLERGIES_RECORDED',
  satisfied: true,
  allergies: [codedAllergy()],
};

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
  idempotencyKey: string | null;
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
      idempotencyKey: request.headers.get('Idempotency-Key'),
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

// --- the four statuses ---

describe('an empty list means two opposite things, and never one of them', () => {
  it('gives NONE_RECORDED and NO_KNOWN_ALLERGY different display state despite both having an empty list', () => {
    // The test this whole feature exists for. Both come back with nothing on them: nobody has
    // asked, and somebody asked and was told there are none. Drawing them the same way is a
    // lie in the safe-looking direction.
    expect(NOBODY_ASKED.allergies).toEqual([]);
    expect(NO_KNOWN.allergies).toEqual([]);

    const nobody = readingOf(NOBODY_ASKED, REACTIONS);
    const none = readingOf(NO_KNOWN, REACTIONS);

    // Every part of what a screen draws differs — the headline, the sentence under it, the
    // sentence in the empty space, the tone, and whether the patient may be sent on.
    expect(nobody.headline).not.toBe(none.headline);
    expect(nobody.meaning).not.toBe(none.meaning);
    expect(nobody.empty).not.toBe(none.empty);
    expect(nobody.tone).not.toBe(none.tone);
    expect(nobody.blocked).toBe(true);
    expect(none.blocked).toBe(false);
    expect(nobody.asked).toBe(false);
    expect(none.asked).toBe(true);

    // And the one that would be read as reassurance is true for exactly one of them.
    expect(nobody.reassuring).toBe(false);
    expect(none.reassuring).toBe(true);
  });

  it('chooses the empty-list sentence by the status, never by the length of the list', () => {
    // Four statuses, four sentences, and no path that reads a list and decides what it means.
    const keys = ALLERGY_STATUSES.map(
      (status) => readingOf({ status, satisfied: true, allergies: [] }).empty,
    );
    expect(new Set(keys).size).toBe(ALLERGY_STATUSES.length);
    expect(keys).toEqual([
      'empty.NONE_RECORDED',
      'empty.ALLERGIES_RECORDED',
      'empty.NO_KNOWN_ALLERGY',
      'empty.UNABLE_TO_ASSESS',
    ]);
  });

  it('does not read as reassurance for “unable to assess”', () => {
    // Somebody looked and could not get an answer. It satisfies the gate and it is emphatically
    // not a claim that there are none — the medication safety engine treats the two very
    // differently, and it can only do that because they are different facts.
    const unable = readingOf(UNABLE, REACTIONS);
    expect(unable.satisfied).toBe(true);
    expect(unable.blocked).toBe(false);
    expect(unable.asked).toBe(true);
    expect(unable.reassuring).toBe(false);
    expect(unable.tone).not.toBe(readingOf(NO_KNOWN).tone);
    // The reason travels with it, because the third state is worth having only while it is
    // reviewable.
    expect(unable.assertion?.reason).toBe('Patient is drowsy and no attendant present.');
  });

  it('reads a status this build has never heard of as unknown rather than as calm', () => {
    // A tablet a version behind the server must not render a fifth answer as reassurance.
    const future = { status: 'DECLINED' as AllergyStatus, satisfied: false, allergies: [] };
    const reading = readingOf(future);
    expect(statusKnown('DECLINED')).toBe(false);
    expect(reading.known).toBe(false);
    expect(reading.headline).toBe('status.unknown');
    expect(reading.meaning).toBe('meaning.unknown');
    expect(reading.empty).toBe('empty.unknown');
    expect(reading.tone).toBe('critical');
    expect(reading.reassuring).toBe(false);
  });

  it('gives each status a tone, and never gives the two empty ones the same one', () => {
    expect(toneFor('NONE_RECORDED')).toBe('critical');
    expect(toneFor('ALLERGIES_RECORDED')).toBe('critical');
    expect(toneFor('NO_KNOWN_ALLERGY')).toBe('normal');
    expect(toneFor('UNABLE_TO_ASSESS')).toBe('borderline');
    for (const status of ALLERGY_STATUSES) expect(statusKnown(status)).toBe(true);
    expect(asked(NOBODY_ASKED)).toBe(false);
    expect(asked(RECORDED)).toBe(true);
  });
});

// --- the gate ---

describe('nothing in this feature satisfies the gate without a real answer', () => {
  it('has three ways to answer, named, and no fourth', () => {
    // The whole checkpoint rests on this list being exactly this long. A fourth answer cannot
    // be added by a screen: it would have to be added in `state.ts`, next to the sentence
    // saying why there is not one.
    expect(ANSWERS).toEqual(['ALLERGY', 'NO_KNOWN_ALLERGY', 'UNABLE_TO_ASSESS']);
    expect(ASSERTION_KINDS).toEqual(['NO_KNOWN_ALLERGY', 'UNABLE_TO_ASSESS']);
  });

  it('exports nothing whose name is a way past the checkpoint', () => {
    const exported = [...Object.keys(allergyState), ...Object.keys(allergyApi)];
    expect(
      exported.filter((name) =>
        /skip|override|bypass|force|anyway|proceed|advance(?!$)|waive|dismiss|clear.*gate|gate.*clear/i.test(
          name,
        ),
      ),
      'no export offers a way round the hard stop',
    ).toEqual([]);
    // Every write this feature can make, by name. Three answers and two ways to take one
    // back, and nothing else in `api.ts` sends anything at all.
    expect(
      Object.keys(allergyApi)
        .filter((name) => !/^(list|get|troubleOf)/.test(name))
        .sort(),
    ).toEqual([
      // prettier-ignore
      'assertAllergyStatus',
      'recordAllergy',
      'withdrawAllergy',
      'withdrawAllergyAssertion',
    ]);
  });

  it('produces no satisfied state from nothing, because it produces no state at all', () => {
    // `mayAdvance` is the server's own answer, mirrored. Nothing here computes it from a
    // status, a list, or a draft — the enforcement is a trigger on the queue table, and a
    // second account of it in a client would eventually disagree.
    expect(mayAdvance(NOBODY_ASKED)).toBe(false);
    expect(readingOf(NOBODY_ASKED).blocked).toBe(true);
    expect(readingOf(NOBODY_ASKED).satisfied).toBe(false);

    // Every body this feature can build is a *request*. None of them carries a status or a
    // satisfied flag, so nothing here can hand a screen a cleared gate.
    const built = [
      toRecording(complete(), REACTIONS, ids),
      toAssertion('NO_KNOWN_ALLERGY', '', ids),
      toAssertion('UNABLE_TO_ASSESS', 'no attendant', ids),
      toWithdrawal('wrong patient', ids),
    ];
    for (const body of built) {
      expect(body).not.toBeNull();
      expect(body).not.toHaveProperty('satisfied');
      expect(body).not.toHaveProperty('status');
      expect(body).not.toHaveProperty('allergies');
    }
  });

  it('will not build an assertion for a kind that is not one of the two', () => {
    // A caller that invented a kind gets null rather than a request the server has to refuse
    // — which is the same guarantee expressed one layer earlier.
    expect(toAssertion('OVERRIDE' as never, 'because the queue is long', ids)).toBeNull();
    expect(toAssertion('' as never, '', ids)).toBeNull();
  });

  it('is not asked about anywhere except the one place that reads the server’s answer', () => {
    // `satisfied` is read in `state.ts` and nowhere else in the feature: no screen and no call
    // may reach around `mayAdvance` to decide the gate for itself.
    const dir = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'features', 'allergies');
    for (const file of readdirSync(dir)) {
      if (file === 'state.ts') continue;
      const source = readFileSync(join(dir, file), 'utf8');
      expect(/\bsatisfied\b\s*[:=]/.test(source), file).toBe(false);
    }
  });
});

// --- the two assertions ---

describe('the two assertions are mirror images, and both are refused without their rule', () => {
  it('requires a reason for “unable to assess”', () => {
    // The point of the third state is that it is reviewable. "We could not ask" with nothing
    // after it is a silent gap wearing a label.
    expect(assertionProblem('UNABLE_TO_ASSESS', '')).toBe('needsReason');
    expect(assertionProblem('UNABLE_TO_ASSESS', '   ')).toBe('needsReason');
    expect(canAssert('UNABLE_TO_ASSESS', '')).toBe(false);
    expect(toAssertion('UNABLE_TO_ASSESS', '  ', ids)).toBeNull();
    expect(needsReason('UNABLE_TO_ASSESS')).toBe(true);

    expect(assertionProblem('UNABLE_TO_ASSESS', 'Patient is drowsy.')).toBeNull();
    expect(toAssertion('UNABLE_TO_ASSESS', '  Patient is drowsy.  ', ids)).toEqual({
      event_id: 'event-1',
      visit_id: 'visit-1',
      kind: 'UNABLE_TO_ASSESS',
      reason: 'Patient is drowsy.',
    });
  });

  it('refuses a reason on “no known allergies”', () => {
    // The mirror rule: text nobody will ever read, answering a question nobody asked.
    expect(assertionProblem('NO_KNOWN_ALLERGY', 'she said so')).toBe('reasonRefused');
    expect(canAssert('NO_KNOWN_ALLERGY', 'she said so')).toBe(false);
    expect(toAssertion('NO_KNOWN_ALLERGY', 'she said so', ids)).toBeNull();
    expect(needsReason('NO_KNOWN_ALLERGY')).toBe(false);

    expect(canAssert('NO_KNOWN_ALLERGY', '')).toBe(true);
    expect(canAssert('NO_KNOWN_ALLERGY', '   ')).toBe(true);
    const body = toAssertion('NO_KNOWN_ALLERGY', '   ', { event: 'event-2' });
    expect(body).toEqual({ event_id: 'event-2', kind: 'NO_KNOWN_ALLERGY' });
    // Not an empty string either: an empty reason is a field the server would refuse.
    expect(body).not.toHaveProperty('reason');
  });
});

// --- a coding is three fields ---

describe('a partial coding never reaches the server', () => {
  it('refuses two of the three, and says so as a refusal rather than repairing it', () => {
    expect(codingFrom({ code_system: 'DTHC', code: 'ALLERGEN_PENICILLIN' })).toBeNull();
    expect(partialCoding({ code_system: 'DTHC', code: 'ALLERGEN_PENICILLIN' })).toBe(true);
    expect(partialCoding({ code: 'ALLERGEN_PENICILLIN' })).toBe(true);
    expect(partialCoding({})).toBe(false);
    expect(partialCoding({ code_system: ' ', code_version: '', code: '' })).toBe(false);
  });

  it('hands back all three, trimmed, when all three are there', () => {
    expect(
      codingFrom({ code_system: ' DTHC ', code_version: '1.0', code: ' ALLERGEN_PENICILLIN' }),
    ).toEqual({ system: 'DTHC', version: '1.0', code: 'ALLERGEN_PENICILLIN' });
    expect(coded(codedAllergy())).toBe(true);
    expect(coded(allergy())).toBe(false);
  });

  it('refuses a hand-built half coding before it can be recorded', () => {
    // `setCoding` will not store one, so this is the second lock: a draft that acquired one
    // some other way is refused with the server's own rule rather than sent.
    const half = { system: 'DTHC', version: '', code: 'ALLERGEN_PENICILLIN' };
    expect(wholeCoding(half)).toBe(false);
    expect(wholeCoding(null)).toBe(true);
    expect(setCoding(emptyDraft(), half).coding, 'a half coding is not stored at all').toBeNull();

    const draft = { ...complete(), coding: half };
    expect(problemsWith(draft, REACTIONS).map((refusal) => refusal.problem)).toEqual([
      'partialCoding',
    ]);
    expect(canRecord(draft, REACTIONS)).toBe(false);
    expect(toRecording(draft, REACTIONS, ids)).toBeNull();
  });

  it('codes an allergen from the clinic’s own dictionary', () => {
    // ICD codes diseases, not substances. `DTHC` is where the clinic keeps what ICD has no
    // code for, which is the catalogue the picker opens on.
    expect(ALLERGEN_SYSTEM).toBe('DTHC');
    const coding = { system: 'DTHC', version: '1.0', code: 'ALLERGEN_PENICILLIN' };
    expect(setCoding(emptyDraft(), coding).coding).toEqual(coding);
    expect(setCoding(setCoding(emptyDraft(), coding), null).coding).toBeNull();
  });
});

// --- an uncoded allergy is a real allergy ---

describe('an allergy the catalogue has nothing for', () => {
  it('keeps what the patient said and is marked uncoded', () => {
    // The escape hatch matters more here than anywhere else in the system: an allergy nobody
    // could code is far more dangerous in a note field than it is here, marked and visible.
    const uncoded = allergy({ said: 'the yellow tablet from the pharmacy near the bridge' });
    const [row] = allergyRows([uncoded], REACTIONS, 'en');
    expect(row?.coded).toBe(false);
    expect(row?.coding).toBeNull();
    expect(row?.said).toBe('the yellow tablet from the pharmacy near the bridge');
    // The words are what the row reads as, so an uncoded allergy is never a blank line.
    expect(row?.label).toBe('the yellow tablet from the pharmacy near the bridge');
    expect(allergyLabel(uncoded, 'bn')).toBe('the yellow tablet from the pharmacy near the bridge');
  });

  it('is recorded rather than refused, as long as it says what the reaction was to', () => {
    const draft = edit(emptyDraft(), {
      said: 'the yellow tablet',
      reaction: 'RASH',
      severity: 'mild',
      certainty: 'suspected',
    });
    expect(canRecord(draft, REACTIONS)).toBe(true);
    const body = toRecording(draft, REACTIONS, ids);
    expect(body).toMatchObject({ said: 'the yellow tablet', reaction: 'RASH' });
    expect(body?.code).toBeUndefined();
    expect(body?.code_system).toBeUndefined();
    expect(body?.code_version).toBeUndefined();
  });

  it('keeps the patient’s words on a coded allergy too', () => {
    const both = codedAllergy({ said: 'the yellow tablet' });
    const [row] = allergyRows([both], REACTIONS, 'bn');
    expect(row?.coded).toBe(true);
    expect(row?.said).toBe('the yellow tablet');
    expect(row?.label).toBe('পেনিসিলিন');
    expect(allergyLabel(both, 'en')).toBe('Penicillins');
  });

  it('never reads as an empty row', () => {
    expect(allergyLabel(codedAllergy({ display_bn: '  ' }), 'bn')).toBe('Penicillins');
    expect(allergyLabel(allergy({ code: 'ALLERGEN_DUST', display_en: '' }), 'en')).toBe(
      'ALLERGEN_DUST',
    );
    expect(allergyLabel(allergy(), 'en')).toBe('');
  });
});

// --- the list ---

describe('the list is drawn in the order the server sent it', () => {
  it('never re-sorts, because the server already leads with the worst', () => {
    // Re-sorting here — even by severity, even innocently — would eventually put a rash above
    // an anaphylaxis because the two orderings disagreed.
    const worstFirst = [
      allergy({ id: 'a', reaction: 'ANAPHYLAXIS', severity: 'life_threatening' }),
      allergy({ id: 'b', reaction: 'RASH', severity: 'severe' }),
      allergy({ id: 'c', reaction: 'ITCHING', severity: 'mild' }),
    ];
    expect(allergyRows(worstFirst, REACTIONS, 'en').map((row) => row.allergy.id)).toEqual([
      'a',
      'b',
      'c',
    ]);
    // Even when the server sends them in an order this app would not have chosen.
    const asSent = [worstFirst[2]!, worstFirst[0]!];
    expect(allergyRows(asSent, REACTIONS, 'en').map((row) => row.allergy.id)).toEqual(['c', 'a']);
    expect(readingOf({ ...RECORDED, allergies: asSent }).allergies).toBe(asSent);
  });

  it('reads the emergency flag off the reaction rather than off the severity', () => {
    // Anaphylaxis is an emergency whatever was ticked beside it, and a mild rash is not one
    // however alarmed somebody was.
    const mildAnaphylaxis = allergy({ reaction: 'ANAPHYLAXIS', severity: 'mild' });
    expect(emergencyOf(mildAnaphylaxis, REACTIONS)).toBe(true);
    expect(
      emergencyOf(allergy({ reaction: 'RASH', severity: 'life_threatening' }), REACTIONS),
    ).toBe(false);
    // The server's own flag on the row is believed when it is there, whatever this build's
    // copy of the vocabulary says.
    expect(emergencyOf(allergy({ reaction: 'RASH', is_emergency: true }), REACTIONS)).toBe(true);
    expect(emergencyOf(allergy({ reaction: 'NOT_A_REACTION' }), REACTIONS)).toBe(false);
    expect(emergencyOf(allergy({ reaction: 'ANAPHYLAXIS' }))).toBe(false);

    expect(anyEmergency([allergy(), mildAnaphylaxis], REACTIONS)).toBe(true);
    expect(anyEmergency([allergy()], REACTIONS)).toBe(false);
    expect(readingOf({ ...RECORDED, allergies: [mildAnaphylaxis] }, REACTIONS).emergency).toBe(
      true,
    );
  });

  it('says what the reaction was, in the reader’s language, and never blankly', () => {
    const named = allergy({ reaction_en: 'Rash', reaction_bn: 'ফুসকুড়ি' });
    expect(reactionTextOf(named, REACTIONS, 'en')).toBe('Rash');
    expect(reactionTextOf(named, REACTIONS, 'bn')).toBe('ফুসকুড়ি');
    // The server sent no wording, so this build's own vocabulary answers.
    expect(reactionTextOf(allergy({ reaction: 'ITCHING' }), REACTIONS, 'bn')).toBe('চুলকানি');
    expect(reactionTextOf(allergy({ reaction: 'ITCHING' }), REACTIONS, 'en')).toBe('Itching');
    // And a reaction this build has never heard of still shows its code rather than a gap.
    expect(reactionTextOf(allergy({ reaction: 'OTHER' }), REACTIONS, 'en')).toBe('OTHER');
  });

  it('draws the vocabulary in the clinic’s order, emergencies first', () => {
    expect(reactionsInOrder([RASH, ANAPHYLAXIS, ITCHING]).map((r) => r.reaction)).toEqual([
      'ANAPHYLAXIS',
      'RASH',
      'ITCHING',
    ]);
    expect(reactionNamed(REACTIONS, 'RASH')?.ordering).toBe(4);
    expect(reactionNamed(REACTIONS, 'NOPE')).toBeNull();
    expect(reactionLabel(RASH, 'bn')).toBe('ফুসকুড়ি');
    expect(reactionLabel(RASH, 'en')).toBe('Rash');
    expect(reactionLabel({ ...RASH, display_bn: '  ' }, 'bn')).toBe('Rash');
    expect(reactionLabel({ ...RASH, display_en: '' }, 'en')).toBe('RASH');
  });

  it('carries the severity and the certainty onto the row without collapsing them', () => {
    // A suspected reaction thirty years ago and a confirmed anaphylaxis are both worth
    // recording and they are not the same warning.
    const [row] = allergyRows(
      [codedAllergy({ severity: 'life_threatening', certainty: 'confirmed' })],
      REACTIONS,
      'en',
    );
    expect(row?.severity).toBe('life_threatening');
    expect(row?.certainty).toBe('confirmed');
    expect(row?.coding).toEqual({ system: 'DTHC', version: '1.0', code: 'ALLERGEN_PENICILLIN' });
  });
});

// --- what the server would refuse, refused here first ---

const complete = () =>
  edit(emptyDraft(), {
    said: 'penicillin',
    reaction: 'RASH',
    severity: 'moderate',
    certainty: 'confirmed',
  });

describe('the refusals mirror the server, in the server’s order', () => {
  it('lets a complete draft through', () => {
    expect(problemsWith(complete(), REACTIONS)).toEqual([]);
    expect(canRecord(complete(), REACTIONS)).toBe(true);
  });

  it('refuses an allergy with neither a coding nor words', () => {
    // It would assert only that the patient reacts to *something*, which is the one row a
    // prescriber cannot act on.
    const draft = edit(complete(), { said: '  ' });
    expect(problemsWith(draft, REACTIONS).map((refusal) => refusal.problem)).toEqual([
      'nothingSaid',
    ]);
    expect(
      problemsWith(
        setCoding(draft, { system: 'DTHC', version: '1.0', code: 'ALLERGEN_PENICILLIN' }),
        REACTIONS,
      ),
    ).toEqual([]);
  });

  it('refuses a reaction that is not in the vocabulary, and one that is missing', () => {
    // A row a header cannot render is worse than an allergy nobody recorded: the blank line
    // reads as "checked, nothing found".
    expect(
      problemsWith(edit(complete(), { reaction: '' }), REACTIONS).map((r) => r.problem),
    ).toEqual(['needsReaction']);
    expect(
      problemsWith(edit(complete(), { reaction: 'SNEEZING' }), REACTIONS).map((r) => r.problem),
    ).toEqual(['unknownReaction']);
    expect(knownReaction(REACTIONS, 'RASH')).toBe(true);
    expect(knownReaction(REACTIONS, 'SNEEZING')).toBe(false);
    // An empty vocabulary refuses everything rather than waving it through: a reaction must
    // never be written against a list this tablet has not got.
    expect(knownReaction([], 'RASH')).toBe(false);
    expect(canRecord(complete(), [])).toBe(false);
  });

  it('refuses a draft with no severity and one with no certainty', () => {
    expect(
      problemsWith(edit(complete(), { severity: '' }), REACTIONS).map((r) => r.problem),
    ).toEqual(['needsSeverity']);
    expect(
      problemsWith(edit(complete(), { certainty: '' }), REACTIONS).map((r) => r.problem),
    ).toEqual(['needsCertainty']);
    // Nothing is pre-selected: a severity nobody chose is a finding the form invented.
    expect(emptyDraft().severity).toBe('');
    expect(emptyDraft().certainty).toBe('');
  });

  it('puts each refusal on the control it belongs to', () => {
    const refusals = problemsWith(emptyDraft(), REACTIONS);
    expect(refusalOn(refusals, 'substance')).toBe('nothingSaid');
    expect(refusalOn(refusals, 'reaction')).toBe('needsReaction');
    expect(refusalOn(refusals, 'severity')).toBe('needsSeverity');
    expect(refusalOn(refusals, 'certainty')).toBe('needsCertainty');
    expect(refusalOn(refusals, 'note')).toBeNull();
    expect(refusalOn(refusals, 'reason')).toBeNull();
  });

  it('does not correct an officer who has not started', () => {
    expect(isPristine(emptyDraft())).toBe(true);
    expect(canRecord(emptyDraft(), REACTIONS)).toBe(false);
    expect(isPristine(edit(emptyDraft(), { said: 'penicillin' }))).toBe(false);
    expect(isPristine(edit(emptyDraft(), { reaction: 'RASH' }))).toBe(false);
    expect(isPristine(edit(emptyDraft(), { severity: 'mild' }))).toBe(false);
    expect(isPristine(edit(emptyDraft(), { certainty: 'confirmed' }))).toBe(false);
    expect(isPristine(edit(emptyDraft(), { note: 'x' }))).toBe(false);
    expect(isPristine(setCoding(emptyDraft(), { system: 'DTHC', version: '1.0', code: 'A' }))).toBe(
      false,
    );
    // Whitespace is not a start.
    expect(isPristine(edit(emptyDraft(), { said: '   ', note: ' ' }))).toBe(true);
  });
});

// --- recording is as cheap as asserting ---

describe('recording an allergy costs about what asserting there are none costs', () => {
  it('counts down from four answers, and every one of them is a tap', () => {
    // The plan's risk is reflexive NKA. If recording were twenty presses and asserting were
    // one, the record would fill with claims rather than findings — so the honest answer is
    // four taps from a picker that opens on the clinic's own favourites, and this is the
    // countdown the screen puts in front of the officer before they start.
    let draft = emptyDraft();
    expect(missingFrom(draft, REACTIONS)).toEqual([
      'substance',
      'reaction',
      'severity',
      'certainty',
    ]);

    draft = setCoding(draft, { system: 'DTHC', version: '1.0', code: 'ALLERGEN_PENICILLIN' });
    expect(missingFrom(draft, REACTIONS)).toHaveLength(3);
    draft = edit(draft, { reaction: 'ANAPHYLAXIS' });
    expect(missingFrom(draft, REACTIONS)).toHaveLength(2);
    draft = edit(draft, { severity: 'life_threatening' });
    expect(missingFrom(draft, REACTIONS)).toHaveLength(1);
    draft = edit(draft, { certainty: 'confirmed' });

    expect(missingFrom(draft, REACTIONS)).toEqual([]);
    expect(canRecord(draft, REACTIONS)).toBe(true);
    // Four taps, and not one keystroke: the note is never required.
    expect(draft.said).toBe('');
    expect(draft.note).toBe('');
  });

  it('takes the patient’s own words instead of a coding, at the same cost', () => {
    const spoken = edit(emptyDraft(), { said: 'the yellow tablet' });
    expect(missingFrom(spoken, REACTIONS)).toEqual(['reaction', 'severity', 'certainty']);
  });
});

// --- what gets sent ---

describe('one allergy, one request', () => {
  it('writes the coding as three fields or none at all', () => {
    const draft = edit(
      setCoding(emptyDraft(), { system: 'DTHC', version: '1.0', code: 'ALLERGEN_PENICILLIN' }),
      {
        said: '  the yellow tablet  ',
        reaction: ' RASH ',
        severity: 'severe',
        certainty: 'confirmed',
        note: '  swelling lasted two days  ',
      },
    );
    expect(toRecording(draft, REACTIONS, ids)).toEqual({
      event_id: 'event-1',
      visit_id: 'visit-1',
      reaction: 'RASH',
      severity: 'severe',
      certainty: 'confirmed',
      code_system: 'DTHC',
      code_version: '1.0',
      code: 'ALLERGEN_PENICILLIN',
      said: 'the yellow tablet',
      note: 'swelling lasted two days',
    });
  });

  it('leaves out the empty boxes rather than sending them empty', () => {
    const body = toRecording(complete(), REACTIONS, { event: 'event-9' });
    expect(body).toEqual({
      event_id: 'event-9',
      reaction: 'RASH',
      severity: 'moderate',
      certainty: 'confirmed',
      said: 'penicillin',
    });
    expect(body).not.toHaveProperty('note');
    expect(body).not.toHaveProperty('visit_id');
    expect(toRecording(complete(), REACTIONS, { event: 'e', visit: '' })?.visit_id).toBeUndefined();
  });

  it('refuses to build a body at all for a draft the server would refuse', () => {
    expect(toRecording(emptyDraft(), REACTIONS, ids)).toBeNull();
  });
});

// --- withdrawal ---

describe('taking something back is never a deletion, and never without a reason', () => {
  it('will not build a withdrawal without one', () => {
    expect(withdrawalRefused('')).toBe(true);
    expect(withdrawalRefused('   ')).toBe(true);
    expect(toWithdrawal('  ', ids)).toBeNull();
    expect(withdrawalRefused('Recorded on the wrong patient.')).toBe(false);
    expect(toWithdrawal('  Recorded on the wrong patient.  ', ids)).toEqual({
      event_id: 'event-1',
      visit_id: 'visit-1',
      reason: 'Recorded on the wrong patient.',
    });
    expect(toWithdrawal('wrong patient', { event: 'event-3' })).toEqual({
      event_id: 'event-3',
      reason: 'wrong patient',
    });
  });

  it('keeps both halves in the change history, which is why the history exists', () => {
    const changes: AllergyChange[] = [
      {
        kind: 'ALLERGY',
        id: 'allergy-1',
        said: 'penicillin',
        detail: 'confirmed',
        at: '2026-06-02T10:00:00.000Z',
        by: 'officer-2',
        undone_at: '2026-06-02T10:30:00.000Z',
        undone_by: 'officer-3',
        undone_why: 'Recorded on the wrong patient.',
      },
      {
        kind: 'NO_KNOWN_ALLERGY',
        id: 'assertion-1',
        at: '2026-03-11T09:00:00.000Z',
        by: 'officer-1',
      },
      { kind: 'ALLERGY', id: 'allergy-2', code: 'ALLERGEN_DUST', at: '2026-01-01T09:00:00.000Z', by: 'officer-1' }, // prettier-ignore
    ];
    const rows = changeRows(changes);
    // Newest first, as the server sent it. Never re-sorted and never filtered.
    expect(rows.map((row) => row.change.id)).toEqual(['allergy-1', 'assertion-1', 'allergy-2']);
    expect(rows[0]).toMatchObject({
      label: 'penicillin',
      detail: 'confirmed',
      withdrawn: true,
      why: 'Recorded on the wrong patient.',
    });
    // Somebody believed it and somebody else disagreed, and both halves are on the row.
    expect(rows[1]).toMatchObject({
      kind: 'NO_KNOWN_ALLERGY',
      label: '',
      withdrawn: false,
      why: '',
    });
    expect(rows[2]?.label).toBe('ALLERGEN_DUST');
    expect(changeRows([])).toEqual([]);
  });
});

// --- the calls ---

describe('the seven calls', () => {
  it('fetches the reaction vocabulary', async () => {
    const calls = stubFetch(respond({ reactions: REACTIONS }));
    expect((await listReactions()).map((r) => r.reaction)).toContain('ANAPHYLAXIS');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/allergies/reactions');
  });

  it('fetches the status, the list and the standing assertion together', async () => {
    const calls = stubFetch(respond(NO_KNOWN));
    const state = await getAllergyState('patient-1');
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/patients/patient-1/allergies');
    // The status comes back as well as the list, because the two answer different questions.
    expect(state.status).toBe('NO_KNOWN_ALLERGY');
    expect(state.allergies).toEqual([]);
    expect(state.assertion?.kind).toBe('NO_KNOWN_ALLERGY');
  });

  it('fetches everything ever said', async () => {
    const calls = stubFetch(respond({ changes: [] }));
    expect(await listAllergyChanges('patient-1')).toEqual([]);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/patients/patient-1/allergies/history');
  });

  it('records one allergy, guarded and keyed by its own event', async () => {
    const calls = stubFetch(respond(RECORDED, { status: 201 }));
    const body = toRecording(complete(), REACTIONS, ids)!;
    const state = await recordAllergy('patient-1', body);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/patients/patient-1/allergies');
    expect(calls[0]?.method).toBe('POST');
    expect(calls[0]?.requestedWith).toBe('DTHCMS');
    // The key is the event id from the body, so a retry over a bad link is one event.
    expect(calls[0]?.idempotencyKey).toBe('event-1');
    expect(JSON.parse(calls[0]!.body)).toMatchObject({ reaction: 'RASH', severity: 'moderate' });
    // The whole state comes back, because recording can change what the gate says.
    expect(state.satisfied).toBe(true);
  });

  it('asserts a status, and carries the reason only where there is one', async () => {
    const calls = stubFetch(respond(NO_KNOWN, { status: 201 }), respond(UNABLE, { status: 201 }));
    await assertAllergyStatus('patient-1', toAssertion('NO_KNOWN_ALLERGY', '', ids)!);
    await assertAllergyStatus(
      'patient-1',
      toAssertion('UNABLE_TO_ASSESS', 'No attendant present.', { event: 'event-2' })!,
    );
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/patients/patient-1/allergies/assert');
    expect(JSON.parse(calls[0]!.body)).toEqual({
      event_id: 'event-1',
      visit_id: 'visit-1',
      kind: 'NO_KNOWN_ALLERGY',
    });
    expect(JSON.parse(calls[1]!.body)).toEqual({
      event_id: 'event-2',
      kind: 'UNABLE_TO_ASSESS',
      reason: 'No attendant present.',
    });
    expect(calls[1]?.idempotencyKey).toBe('event-2');
  });

  it('withdraws an allergy and an assertion through two different endpoints', async () => {
    const calls = stubFetch(respond(NOBODY_ASKED), respond(NOBODY_ASKED));
    const body = toWithdrawal('Recorded on the wrong patient.', ids)!;
    const afterAllergy = await withdrawAllergy('allergy-1', body);
    const afterAssertion = await withdrawAllergyAssertion('assertion-1', body);
    expect(new URL(calls[0]!.url).pathname).toBe('/v1/allergies/allergy-1/withdraw');
    expect(new URL(calls[1]!.url).pathname).toBe('/v1/allergies/assertions/assertion-1/withdraw');
    for (const call of calls) {
      expect(call.method).toBe('POST');
      expect(call.requestedWith).toBe('DTHCMS');
      expect(JSON.parse(call.body)).toMatchObject({ reason: 'Recorded on the wrong patient.' });
    }
    // Both answer with the resulting status, because a withdrawal can re-close the gate.
    expect(mayAdvance(afterAllergy)).toBe(false);
    expect(mayAdvance(afterAssertion)).toBe(false);
    expect(readingOf(afterAllergy).blocked).toBe(true);
  });

  it('throws the shared error classes, so the trouble mapping has something to read', async () => {
    stubFetch(
      respond(
        {
          error: {
            code: 'VALIDATION_FAILED',
            kind: 'validation',
            message: 'Say why the answer could not be got.',
            message_bn: 'কেন উত্তর পাওয়া যায়নি তা লিখুন।',
            fields: { reason: 'Say why the answer could not be got.' },
          },
        },
        { status: 422 },
      ),
    );
    const error = await assertAllergyStatus('patient-1', {
      event_id: 'event-1',
      kind: 'UNABLE_TO_ASSESS',
    }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(troubleOf(error, 'en')).toEqual({
      kind: 'refused',
      field: 'reason',
      message: 'Say why the answer could not be got.',
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
    const error = await listReactions().catch((e: unknown) => e);
    expect(troubleOf(error, 'en')).toEqual({ kind: 'unreachable' });
  });

  it('shows a refusal in the server’s own words, in the reader’s language', () => {
    const error = apiError({
      status: 422,
      fields: { reaction: 'That reaction is not one this clinic records.' },
      fieldsBN: { reaction: 'এই প্রতিক্রিয়াটি এই ক্লিনিকের তালিকায় নেই।' },
    });
    expect(troubleOf(error, 'en')).toMatchObject({ field: 'reaction' });
    expect(troubleOf(error, 'bn')).toEqual({
      kind: 'refused',
      field: 'reaction',
      message: 'এই প্রতিক্রিয়াটি এই ক্লিনিকের তালিকায় নেই।',
    });
  });

  it('reports the field the officer can act on first when a refusal names two', () => {
    const error = apiError({
      status: 422,
      fields: { severity: 'no severity', said: 'say what it was' },
    });
    expect(troubleOf(error, 'en')).toMatchObject({ field: 'said' });
  });

  it('shows a field this build has never heard of rather than swallowing it', () => {
    const error = apiError({ status: 422, fields: { zeta: 'last', alpha: 'first' } });
    expect(troubleOf(error, 'en')).toMatchObject({ field: 'alpha', message: 'first' });
  });

  it('still says something when a refusal names no field at all', () => {
    const error = apiError({ status: 422, messageEN: 'That assertion was already withdrawn.' });
    expect(troubleOf(error, 'en')).toEqual({
      kind: 'refused',
      field: '',
      message: 'That assertion was already withdrawn.',
    });
  });

  it('separates a server that refused from one that could not answer', () => {
    expect(troubleOf(apiError({ status: 500 }), 'en')).toMatchObject({ kind: 'failed' });
    expect(troubleOf(apiError({ status: 409, messageBN: 'ইতিমধ্যেই ফিরিয়ে নেওয়া হয়েছে।' }), 'bn')).toEqual({ kind: 'failed', message: 'ইতিমধ্যেই ফিরিয়ে নেওয়া হয়েছে।' }); // prettier-ignore
    expect(troubleOf(apiError({ status: 409, messageBN: '' }), 'bn')).toEqual({
      kind: 'failed',
      message: 'That request cannot be answered.',
    });
    // Something that is not an error this app throws. The screen supplies the sentence.
    expect(troubleOf(new Error('surprise'), 'en')).toEqual({ kind: 'failed', message: '' });
  });
});

// --- the words ---

describe('every label this step can produce exists in both languages', () => {
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

  it('has a sentence for every status, its meaning, and its empty list', () => {
    // The screen looks all three up from the reading, so a status added without sentences
    // written for it would show the officer a raw identifier where the explanation belongs.
    expect(ALLERGY_STATUSES).toHaveLength(4);
    for (const key of [...ALLERGY_STATUSES, 'unknown']) {
      present(`allergies.status.${key}`);
      present(`allergies.meaning.${key}`);
      present(`allergies.empty.${key}`);
    }
  });

  it('says two different things about the two empty lists, in both languages', () => {
    // The failure this checkpoint exists to prevent, checked in the words themselves: "nobody
    // has asked" and "asked, and there are none" must not read the same in either language.
    for (const language of ['en', 'bn'] as const) {
      for (const part of ['status', 'meaning', 'empty']) {
        expect(lookup(language, `allergies.${part}.NONE_RECORDED`), `${language} ${part}`).not.toBe(
          lookup(language, `allergies.${part}.NO_KNOWN_ALLERGY`),
        );
        // And "could not be assessed" is not the same sentence as "there are none" either.
        expect(
          lookup(language, `allergies.${part}.UNABLE_TO_ASSESS`),
          `${language} ${part}`,
        ).not.toBe(lookup(language, `allergies.${part}.NO_KNOWN_ALLERGY`));
      }
    }
  });

  it('has a sentence for every refusal this step can produce', () => {
    expect(PROBLEMS).toHaveLength(9);
    for (const problem of PROBLEMS) present(`allergies.problem.${problem}`);
  });

  it('has a label for the three answers, and a sentence for what each of the two claims', () => {
    expect(ANSWERS).toHaveLength(3);
    for (const answer of ANSWERS) present(`allergies.answer.${answer}`);
    for (const kind of ASSERTION_KINDS) present(`allergies.assertHint.${kind}`);
    present('allergies.answer.title');
    present('allergies.answer.hint');
    // The sentence that says out loud whose name the answer goes into. Without it, the
    // absence of a "skip" reads as an oversight and somebody adds one.
    present('allergies.inYourName');
  });

  it('has a label for every field, severity and certainty a recording can carry', () => {
    expect(ALLERGY_FIELDS).toHaveLength(5);
    for (const field of ALLERGY_FIELDS) present(`allergies.field.${field}`);
    expect(SEVERITIES).toHaveLength(4);
    for (const severity of SEVERITIES) present(`allergies.severity.${severity}`);
    expect(CERTAINTIES).toHaveLength(2);
    for (const certainty of CERTAINTIES) present(`allergies.certainty.${certainty}`);
  });

  it('has a word for every kind of change the history can show', () => {
    for (const kind of ['ALLERGY', 'NO_KNOWN_ALLERGY', 'UNABLE_TO_ASSESS']) {
      present(`allergies.changeKind.${kind}`);
    }
  });

  it('carries all three parts of a coding, in both languages', () => {
    for (const language of ['en', 'bn'] as const) {
      const coding = lookup(language, 'allergies.coding') as string;
      for (const placeholder of ['{system}', '{code}', '{version}']) {
        expect(coding, `${language} coding`).toContain(placeholder);
      }
    }
  });

  it('has the rest of the screen, including the refusal and what it costs', () => {
    for (const key of [
      'step',
      'loading',
      'gate.blocked',
      'gate.open',
      'gate.hint',
      'emergency',
      'recorded',
      'standing',
      'assertedAt',
      'assertedBy',
      'withdrawClosesGate',
      'reasonLabel',
      'reasonPlaceholder',
      'assert',
      'stillNeeded',
      'nothingMissing',
      'record',
      'recordedOne',
      'uncoded',
      'uncodedHint',
      'said',
      'withdrawOpen',
      'withdrawHint',
      'withdrawReason',
      'withdraw',
      'withdrawCancel',
      'withdrawn',
      'changes',
      'changesHint',
      'noChanges',
      'trouble.refused',
      'trouble.unreachable',
      'trouble.failed',
      'troubleHint',
      'retry',
    ]) {
      present(`allergies.${key}`);
    }
  });
});
