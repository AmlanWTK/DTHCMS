import { describe, expect, it } from 'vitest';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import {
  EXAM_FIELDS,
  EXAM_GROUPS,
  MAX_BATCH,
  MONOFILAMENT_SITES,
  SIDES,
  answersFor,
  answeredSites,
  canSave,
  emptyExam,
  emptyMonofilament,
  fieldsIn,
  footRiskFrom,
  groupOf,
  interruptedFeet,
  isBlank,
  markAllFelt,
  missedSites,
  monofilamentInterrupted,
  monofilamentPayload,
  normalAnswerFor,
  parsedScore,
  scoreOutOfRange,
  setCoded,
  setFlag,
  setNotTested,
  setNotTestedReason,
  setScore,
  siteState,
  stillBlank,
  tapSite,
  toExamBatch,
  type Answer,
  type ExamForm,
  type MonofilamentSite,
} from '../src/features/examination/form';
import {
  PROMPT_RULES,
  priorLossOfProtectiveSensation,
  priorSightThreateningRetinopathy,
  priorUlcerOrAmputation,
  promptsFor,
  retinopathyScreeningDue,
  type HistoryRow,
} from '../src/features/examination/prompts';

/**
 * Station 5's structured examination (CP51).
 *
 * The foot diagram is verified by a clinician doing an examination with it on a real phone.
 * What is checked here is everything that decides what the record ends up holding: that a
 * foot the examiner half-finished is refused rather than written as a whole one, that
 * "left" never arrives attached to the right foot's findings, that an untouched form posts
 * nothing at all, and that each history-driven prompt fires for the patient it is for and
 * stays quiet for the patient it is not.
 */

const ANSWERS: Answer[] = [
  // Deliberately in the server's clinical order — present, then diminished, then absent —
  // and deliberately not in alphabetical order, which is the order a careless client would
  // impose and the order that would put "absent" first on every screen.
  { code: 'DP_PULSE_LEFT', value_code: 'present', display_en: 'Present', display_bn: 'স্পষ্ট', ordering: 1, is_normal: true }, // prettier-ignore
  { code: 'DP_PULSE_LEFT', value_code: 'diminished', display_en: 'Diminished', display_bn: 'ক্ষীণ', ordering: 2, is_normal: false }, // prettier-ignore
  { code: 'DP_PULSE_LEFT', value_code: 'absent', display_en: 'Absent', display_bn: 'পাওয়া যায়নি', ordering: 3, is_normal: false }, // prettier-ignore
  { code: 'MURMUR', value_code: 'none', display_en: 'None', display_bn: 'নেই', ordering: 1, is_normal: true }, // prettier-ignore
  { code: 'MURMUR', value_code: 'systolic', display_en: 'Systolic', display_bn: 'সিস্টোলিক', ordering: 2, is_normal: false }, // prettier-ignore
];

function feltEverywhere(form: ExamForm, side: 'LEFT' | 'RIGHT'): ExamForm {
  return markAllFelt(form, side);
}

function tapUntil(
  form: ExamForm,
  side: 'LEFT' | 'RIGHT',
  site: MonofilamentSite,
  times: number,
): ExamForm {
  let next = form;
  for (let i = 0; i < times; i += 1) next = tapSite(next, side, site);
  return next;
}

const ids = {
  batch: (group: string) => `batch-${group}`,
  patient: 'patient-1',
  perValue: (code: string) => `event-${code}`,
};

// --- the form ---

describe('the fields are the examination, in the order it is performed', () => {
  it('does both feet before anything else', () => {
    const sections = EXAM_FIELDS.map((field) => field.section);
    expect(sections.slice(0, 16)).toEqual(Array<string>(16).fill('foot'));
    expect([...new Set(sections)]).toEqual(['foot', 'neuropathy', 'retinopathy', 'cardiovascular']);
  });

  it('starts each foot with the monofilament, while the patient still has their eyes shut', () => {
    expect(fieldsIn('foot', 'LEFT').map((field) => field.code)).toEqual([
      'MONOFILAMENT_LEFT',
      'VIBRATION_LEFT',
      'ANKLE_REFLEX_LEFT',
      'DP_PULSE_LEFT',
      'PT_PULSE_LEFT',
      'FOOT_DEFORMITY_LEFT',
      'FOOT_SKIN_LEFT',
      'FOOT_ULCER_LEFT',
    ]);
  });

  it('carries the side in the code rather than beside it', () => {
    // A finding whose side lives in a separate field is a finding that can be filed against
    // the wrong foot. There is no moment here where the two are apart.
    for (const field of EXAM_FIELDS) {
      if (field.side === undefined) continue;
      expect(field.code.endsWith(`_${field.side}`), field.code).toBe(true);
    }
  });

  it('asks when the eyes were last looked at before asking what was seen', () => {
    expect(fieldsIn('retinopathy')[0]?.code).toBe('RETINOPATHY_SCREEN');
  });

  it('narrows a section to one side when asked, and gives the whole section otherwise', () => {
    expect(fieldsIn('cardiovascular').length).toBe(6);
    expect(fieldsIn('cardiovascular', 'RIGHT').map((f) => f.code)).toEqual(['CAROTID_BRUIT_RIGHT']);
  });
});

describe('an untouched form', () => {
  it('holds nothing worth saving', () => {
    const form = emptyExam();
    expect(isBlank(form)).toBe(true);
    expect(stillBlank(form)).toHaveLength(EXAM_FIELDS.length);
    expect(canSave(form)).toBe(false);
  });

  it('posts nothing at all, rather than a set of normal answers', () => {
    // The whole reason nothing is pre-selected. A default that reached the record would be
    // a finding nobody made, indistinguishable afterwards from one somebody did.
    expect(toExamBatch(emptyExam(), ids)).toEqual([]);
  });

  it('reports every site as unanswered, which is not the same as "not felt"', () => {
    const state = emptyMonofilament();
    expect(siteState(state, 'hallux')).toBe('unknown');
    expect(answeredSites(state)).toBe(0);
    expect(missedSites(state)).toEqual([]);
  });
});

describe('tapping a site', () => {
  it('offers the common answer first and an escape from a mis-tap', () => {
    let form = emptyExam();
    form = tapSite(form, 'LEFT', 'hallux');
    expect(siteState(form.monofilament.LEFT, 'hallux')).toBe('felt');
    form = tapSite(form, 'LEFT', 'hallux');
    expect(siteState(form.monofilament.LEFT, 'hallux')).toBe('not_felt');
    // The third tap clears the site rather than looping back to "felt": without an escape a
    // mis-tap can only be corrected by tapping past the wrong answer again.
    form = tapSite(form, 'LEFT', 'hallux');
    expect(siteState(form.monofilament.LEFT, 'hallux')).toBe('unknown');
  });

  it('never touches the other foot', () => {
    const form = tapSite(emptyExam(), 'LEFT', 'heel');
    expect(answeredSites(form.monofilament.LEFT)).toBe(1);
    expect(answeredSites(form.monofilament.RIGHT)).toBe(0);
  });

  it('takes a foot back out of "could not test", because answering a site is testing it', () => {
    let form = setNotTested(emptyExam(), 'RIGHT', true);
    form = setNotTestedReason(form, 'RIGHT', 'dressing in place');
    form = tapSite(form, 'RIGHT', 'heel');
    expect(form.monofilament.RIGHT.notTested).toBe(false);
    expect(form.monofilament.RIGHT.reason).toBe('');
  });

  it('records the whole foot in one press when every site was felt', () => {
    const form = feltEverywhere(emptyExam(), 'LEFT');
    expect(answeredSites(form.monofilament.LEFT)).toBe(MONOFILAMENT_SITES.length);
    expect(missedSites(form.monofilament.LEFT)).toEqual([]);
    expect(monofilamentInterrupted(form.monofilament.LEFT)).toBe(false);
  });

  it('names which sites went unfelt, not merely how many', () => {
    // Early neuropathy at the hallux and a forefoot that has lost protective sensation are
    // different appointments, and a single count loses the difference permanently.
    let form = feltEverywhere(emptyExam(), 'LEFT');
    form = tapUntil(form, 'LEFT', 'mth_1', 1);
    form = tapUntil(form, 'LEFT', 'mth_3', 1);
    expect(missedSites(form.monofilament.LEFT)).toEqual(['mth_1', 'mth_3']);
  });
});

describe('a foot that could not be tested', () => {
  it('travels alone, because a foot nobody tested has no sites', () => {
    let form = feltEverywhere(emptyExam(), 'LEFT');
    form = setNotTested(form, 'LEFT', true);
    expect(form.monofilament.LEFT.felt).toEqual({});
    expect(monofilamentPayload(form.monofilament.LEFT)).toEqual({ not_tested: true });
    expect(JSON.stringify(monofilamentPayload(form.monofilament.LEFT))).not.toContain('felt');
  });

  it('keeps the reason across the toggle, so a mis-tap does not lose what was typed', () => {
    let form = setNotTested(emptyExam(), 'LEFT', true);
    form = setNotTestedReason(form, 'LEFT', 'amputated below the knee');
    form = setNotTested(form, 'LEFT', true);
    expect(form.monofilament.LEFT.reason).toBe('amputated below the knee');
  });

  it('stops being untestable when the examiner says so', () => {
    let form = setNotTested(emptyExam(), 'LEFT', true);
    form = setNotTested(form, 'LEFT', false);
    expect(form.monofilament.LEFT.notTested).toBe(false);
    expect(monofilamentPayload(form.monofilament.LEFT)).toBeNull();
  });

  it('is not an interrupted foot', () => {
    const form = setNotTested(emptyExam(), 'LEFT', true);
    expect(monofilamentInterrupted(form.monofilament.LEFT)).toBe(false);
    expect(interruptedFeet(form)).toEqual([]);
  });
});

describe('a monofilament the examiner did not finish', () => {
  it('is refused rather than written, at every count from one site to nine', () => {
    // Nine sites is refused by the server, and rightly: a missing site recorded as "not
    // felt" invents a finding and recorded as "felt" hides one.
    for (let count = 1; count < MONOFILAMENT_SITES.length; count += 1) {
      let form = emptyExam();
      for (const site of MONOFILAMENT_SITES.slice(0, count)) form = tapSite(form, 'LEFT', site);
      expect(monofilamentInterrupted(form.monofilament.LEFT), `${count} sites`).toBe(true);
      expect(monofilamentPayload(form.monofilament.LEFT), `${count} sites`).toBeNull();
    }
  });

  it('sends the examiner back to the foot instead of saving the rest', () => {
    let form = tapSite(emptyExam(), 'LEFT', 'hallux');
    form = setCoded(form, 'MURMUR', 'none');
    expect(interruptedFeet(form)).toEqual(['LEFT']);
    expect(canSave(form)).toBe(false);
  });

  it('names each unfinished foot separately', () => {
    let form = tapSite(emptyExam(), 'LEFT', 'hallux');
    form = tapSite(form, 'RIGHT', 'heel');
    expect(interruptedFeet(form)).toEqual(['LEFT', 'RIGHT']);
  });
});

describe('the ten sites', () => {
  it('are written in the server order however the examiner tapped around the foot', () => {
    let form = emptyExam();
    for (const site of [...MONOFILAMENT_SITES].reverse()) form = tapSite(form, 'RIGHT', site);
    const payload = monofilamentPayload(form.monofilament.RIGHT);
    expect(payload).not.toBeNull();
    expect(Object.keys(payload as object)).toEqual(['felt']);
    const felt = (payload as { felt: Record<string, boolean> }).felt;
    expect(Object.keys(felt)).toEqual([...MONOFILAMENT_SITES]);
  });

  it('are all ten, always, because nine is refused', () => {
    const form = feltEverywhere(emptyExam(), 'LEFT');
    const payload = monofilamentPayload(form.monofilament.LEFT) as {
      felt: Record<string, boolean>;
    };
    expect(Object.values(payload.felt).every((value) => value === true)).toBe(true);
    expect(Object.keys(payload.felt)).toHaveLength(10);
  });
});

describe('the neuropathy symptom score', () => {
  it('accepts zero, because no symptoms is an answer and not a refusal', () => {
    // The anthropometry rule — a non-positive number means "not measured" — would silently
    // discard the commonest score in the clinic.
    expect(parsedScore('0')).toBe(0);
    expect(scoreOutOfRange(setScore(emptyExam(), '0'))).toBe(false);
    expect(stillBlank(setScore(emptyExam(), '0'))).not.toContain('NEUROPATHY_SYMPTOM_SCORE');
  });

  it('treats nothing typed, and half-typed nonsense, as no answer', () => {
    expect(parsedScore('')).toBeNull();
    expect(parsedScore('   ')).toBeNull();
    expect(parsedScore('seven')).toBeNull();
    expect(scoreOutOfRange(emptyExam())).toBe(false);
  });

  it('refuses a score the instrument cannot produce, while the patient is still in the chair', () => {
    expect(scoreOutOfRange(setScore(emptyExam(), '14'))).toBe(true);
    expect(scoreOutOfRange(setScore(emptyExam(), '-1'))).toBe(true);
    expect(scoreOutOfRange(setScore(emptyExam(), '6.5'))).toBe(true);
    expect(scoreOutOfRange(setScore(emptyExam(), '13'))).toBe(false);
    expect(canSave(setScore(emptyExam(), '14'))).toBe(false);
  });
});

describe('the answer vocabulary', () => {
  it('keeps the server order, because present-diminished-absent is a scale', () => {
    expect(answersFor(ANSWERS, 'DP_PULSE_LEFT').map((answer) => answer.value_code)).toEqual([
      'present',
      'diminished',
      'absent',
    ]);
  });

  it('takes the first match and nothing from another code', () => {
    expect(answersFor(ANSWERS, 'MURMUR')).toHaveLength(2);
    expect(answersFor(ANSWERS, 'JVP')).toEqual([]);
  });

  it('names the answer that means nothing abnormal, without selecting it', () => {
    expect(normalAnswerFor(ANSWERS, 'DP_PULSE_LEFT')?.value_code).toBe('present');
    expect(normalAnswerFor(ANSWERS, 'JVP')).toBeNull();
    // Nothing on the form has been answered by knowing what normal is.
    expect(isBlank(emptyExam())).toBe(true);
  });
});

describe('what is still blank', () => {
  it('lists the codes in examination order, so the examiner is sent back in order', () => {
    let form = feltEverywhere(emptyExam(), 'LEFT');
    form = setCoded(form, 'VIBRATION_LEFT', 'felt');
    const blank = stillBlank(form);
    expect(blank).not.toContain('MONOFILAMENT_LEFT');
    expect(blank).not.toContain('VIBRATION_LEFT');
    expect(blank[0]).toBe('ANKLE_REFLEX_LEFT');
  });

  it('counts a yes/no finding answered "no" as answered', () => {
    // Absent is not the same statement as unanswered, and a form that conflated them would
    // report a carotid nobody listened to as a carotid with no bruit.
    const form = setFlag(emptyExam(), 'CAROTID_BRUIT_LEFT', false);
    expect(stillBlank(form)).not.toContain('CAROTID_BRUIT_LEFT');
    expect(isBlank(form)).toBe(false);
  });

  it('counts an empty coded answer as unanswered', () => {
    const form = setCoded(emptyExam(), 'MURMUR', '');
    expect(stillBlank(form)).toContain('MURMUR');
  });

  it('does not stop the examination being saved', () => {
    // A screening clinic examines what it can reach. A form that demanded every field is a
    // form somebody fills with whatever clears it.
    const form = setCoded(emptyExam(), 'MURMUR', 'none');
    expect(stillBlank(form).length).toBeGreaterThan(20);
    expect(canSave(form)).toBe(true);
  });
});

// --- what gets sent ---

describe('what gets sent', () => {
  it('keeps each foot in its own transaction, with its own derived risk', () => {
    // The category is derived from that foot's findings. A record holding four of a foot's
    // eight would carry a category computed from half an examination.
    let form = feltEverywhere(emptyExam(), 'LEFT');
    form = feltEverywhere(form, 'RIGHT');
    form = setCoded(form, 'MURMUR', 'none');
    const batches = toExamBatch(form, ids);
    expect(batches.map((batch) => batch.event_id)).toEqual([
      'batch-FOOT_LEFT',
      'batch-FOOT_RIGHT',
      'batch-GENERAL',
    ]);
    expect(batches[0]!.derive).toEqual(['FOOT_RISK_LEFT']);
    expect(batches[1]!.derive).toEqual(['FOOT_RISK_RIGHT']);
    expect(batches[2]!.derive).toBeUndefined();
  });

  it('never puts one foot’s finding in the other foot’s batch', () => {
    let form = setCoded(emptyExam(), 'DP_PULSE_LEFT', 'absent');
    form = setCoded(form, 'DP_PULSE_RIGHT', 'present');
    const batches = toExamBatch(form, ids);
    const left = batches.find((batch) => batch.event_id === 'batch-FOOT_LEFT');
    const right = batches.find((batch) => batch.event_id === 'batch-FOOT_RIGHT');
    expect(left!.observations.map((o) => o.code)).toEqual(['DP_PULSE_LEFT']);
    expect(right!.observations.map((o) => o.code)).toEqual(['DP_PULSE_RIGHT']);
    expect(left!.observations[0]!.value_code).toBe('absent');
  });

  it('names the risk category and never sends one', () => {
    // A structured foot examination exists so the category falls out of the findings. An
    // examiner who could type it would be back to an opinion with a dropdown in front of it.
    const form = feltEverywhere(emptyExam(), 'LEFT');
    const body = JSON.stringify(toExamBatch(form, ids));
    expect(body).toContain('FOOT_RISK_LEFT');
    expect(body).not.toContain('very_low');
    expect(body).not.toContain('moderate');
  });

  it('stays inside the ceiling one transaction may carry, with everything filled in', () => {
    let form = emptyExam();
    for (const side of SIDES) form = feltEverywhere(form, side);
    for (const field of EXAM_FIELDS) {
      if (field.kind === 'coded') form = setCoded(form, field.code, 'none');
      if (field.kind === 'boolean') form = setFlag(form, field.code, true);
    }
    form = setScore(form, '7');
    const batches = toExamBatch(form, ids);
    expect(batches).toHaveLength(EXAM_GROUPS.length);
    for (const batch of batches) {
      const size = batch.observations.length + (batch.derive?.length ?? 0);
      expect(size, batch.event_id).toBeLessThanOrEqual(MAX_BATCH);
    }
    expect(batches.flatMap((batch) => batch.observations)).toHaveLength(EXAM_FIELDS.length);
  });

  it('sends the score with the unit its registry entry requires', () => {
    // A unit-bearing code sent without one is refused, and a score is unit-bearing in the
    // same sense a ratio is.
    const batches = toExamBatch(setScore(emptyExam(), '9'), ids);
    expect(batches[0]!.observations[0]).toEqual({
      event_id: 'event-NEUROPATHY_SYMPTOM_SCORE',
      code: 'NEUROPATHY_SYMPTOM_SCORE',
      value: 9,
      unit: '1',
    });
  });

  it('sends a yes/no finding as a boolean, including when the answer is no', () => {
    let form = setFlag(emptyExam(), 'MACULOPATHY_LEFT', false);
    form = setFlag(form, 'CAROTID_BRUIT_RIGHT', true);
    const written = toExamBatch(form, ids)[0]!.observations;
    expect(written).toEqual([
      { event_id: 'event-MACULOPATHY_LEFT', code: 'MACULOPATHY_LEFT', value_bool: false },
      { event_id: 'event-CAROTID_BRUIT_RIGHT', code: 'CAROTID_BRUIT_RIGHT', value_bool: true },
    ]);
  });

  it('carries the reason a foot could not be tested with the finding itself', () => {
    let form = setNotTested(emptyExam(), 'RIGHT', true);
    form = setNotTestedReason(form, 'RIGHT', '  dressing in place  ');
    const written = toExamBatch(form, ids)[0]!.observations[0]!;
    expect(written.value_json).toEqual({ not_tested: true });
    expect(written.note).toBe('dressing in place');
  });

  it('does not invent a note out of whitespace', () => {
    let form = setNotTested(emptyExam(), 'RIGHT', true);
    form = setNotTestedReason(form, 'RIGHT', '   ');
    expect(toExamBatch(form, ids)[0]!.observations[0]!.note).toBeUndefined();
  });

  it('leaves the reason off a foot that was tested', () => {
    let form = setNotTestedReason(emptyExam(), 'LEFT', 'never used');
    form = feltEverywhere(form, 'LEFT');
    expect(toExamBatch(form, ids)[0]!.observations[0]!.note).toBeUndefined();
  });

  it('omits an unfinished monofilament from the batch entirely', () => {
    let form = tapSite(emptyExam(), 'LEFT', 'hallux');
    form = setCoded(form, 'VIBRATION_LEFT', 'felt');
    const batch = toExamBatch(form, ids)[0]!;
    expect(batch.observations.map((o) => o.code)).toEqual(['VIBRATION_LEFT']);
  });

  it('gives every value a stable id, so a retry writes the same set', () => {
    const form = setCoded(emptyExam(), 'JVP', 'raised');
    const first = toExamBatch(form, ids);
    const second = toExamBatch(form, ids);
    expect(first).toEqual(second);
  });

  it('carries the visit when there is one, and says nothing when there is not', () => {
    const form = setCoded(emptyExam(), 'JVP', 'raised');
    expect(toExamBatch(form, ids)[0]!.visit_id).toBeUndefined();
    expect(toExamBatch(form, { ...ids, visit: 'visit-1' })[0]!.visit_id).toBe('visit-1');
  });

  it('puts every non-foot finding in one group', () => {
    for (const field of EXAM_FIELDS) {
      const expected =
        field.section !== 'foot' ? 'GENERAL' : field.side === 'RIGHT' ? 'FOOT_RIGHT' : 'FOOT_LEFT';
      expect(groupOf(field), field.code).toBe(expected);
    }
  });
});

describe('the risk category the server derived', () => {
  it('is read back per foot, newest first', () => {
    expect(
      footRiskFrom([
        { code: 'FOOT_RISK_LEFT', value_code: 'high' },
        { code: 'FOOT_RISK_LEFT', value_code: 'low' },
        { code: 'FOOT_RISK_RIGHT', value_code: 'very_low' },
      ]),
    ).toEqual({ LEFT: 'high', RIGHT: 'very_low' });
  });

  it('says nothing rather than guessing when there is no category on record', () => {
    expect(footRiskFrom(undefined)).toEqual({});
    expect(footRiskFrom([])).toEqual({});
    expect(footRiskFrom([{ code: 'FOOT_RISK_LEFT' }])).toEqual({});
    expect(footRiskFrom([{ code: 'FOOT_RISK_LEFT', value_code: null }])).toEqual({});
    // A category this app does not know is not a category it will draw.
    expect(footRiskFrom([{ code: 'FOOT_RISK_LEFT', value_code: 'catastrophic' }])).toEqual({});
  });
});

// --- the prompts ---

describe('a patient whose foot has already broken down', () => {
  it('is prompted for both feet after any ulcer above grade 0', () => {
    const prompts = priorUlcerOrAmputation([{ code: 'FOOT_ULCER_LEFT', value_code: 'grade_2' }]);
    expect(prompts).toHaveLength(1);
    expect(prompts[0]!.id).toBe('ulcer_history');
    // Both feet, whichever one the history is about: the other foot of a patient who has
    // ulcerated once is itself a high-risk foot.
    expect(prompts[0]!.fields).toEqual(['FOOT_ULCER_LEFT', 'FOOT_ULCER_RIGHT']);
  });

  it('is prompted after a previous amputation, which is recorded as a deformity', () => {
    expect(
      priorUlcerOrAmputation([{ code: 'FOOT_DEFORMITY_RIGHT', value_code: 'amputation' }]),
    ).toHaveLength(1);
  });

  it('is still prompted years later, when the ulcer has healed', () => {
    // Reading only the current grade would silence this rule the moment the ulcer healed,
    // which is the moment it starts mattering most: a well-healed foot examines normally
    // right up until it ulcerates again.
    expect(
      priorUlcerOrAmputation([
        { code: 'FOOT_ULCER_LEFT', value_code: 'grade_0', effective_at: '2026-09-01T09:00:00Z' },
        { code: 'FOOT_ULCER_LEFT', value_code: 'grade_3', effective_at: '2024-02-01T09:00:00Z' },
      ]),
    ).toHaveLength(1);
  });

  it('is not prompted by a foot that has only ever been intact', () => {
    expect(
      priorUlcerOrAmputation([
        { code: 'FOOT_ULCER_LEFT', value_code: 'grade_0' },
        { code: 'FOOT_DEFORMITY_LEFT', value_code: 'clawed_toes' },
        { code: 'FOOT_SKIN_LEFT', value_code: 'callus' },
      ]),
    ).toEqual([]);
  });

  it('is not prompted by a grade somebody typed wrongly and corrected', () => {
    // Otherwise one slip prompts for an ulcer at every visit for the rest of the patient's
    // life, and a prompt nobody can make go away is a prompt everybody learns to ignore.
    expect(
      priorUlcerOrAmputation([
        { code: 'FOOT_ULCER_LEFT', value_code: 'grade_4', status: 'CORRECTED' },
      ]),
    ).toEqual([]);
  });

  it('is not prompted by a row with no answer in it', () => {
    expect(priorUlcerOrAmputation([{ code: 'FOOT_ULCER_LEFT' }])).toEqual([]);
    expect(priorUlcerOrAmputation([{ code: 'FOOT_ULCER_LEFT', value_code: null }])).toEqual([]);
  });
});

describe('a foot that could not feel the filament last time', () => {
  const missing = (count: number) => {
    const felt: Record<string, boolean> = {};
    MONOFILAMENT_SITES.forEach((site, index) => {
      felt[site] = index >= count;
    });
    return felt;
  };

  it('is prompted to be tested again once two sites of ten were missed', () => {
    const prompts = priorLossOfProtectiveSensation([
      { code: 'MONOFILAMENT_LEFT', value_json: { felt: missing(2) } },
    ]);
    expect(prompts).toHaveLength(1);
    expect(prompts[0]!.id).toBe('sensation_lost');
    expect(prompts[0]!.side).toBe('LEFT');
    expect(prompts[0]!.fields).toEqual(['MONOFILAMENT_LEFT']);
  });

  it('is not prompted for one site, which is within the noise of a hurried examination', () => {
    expect(
      priorLossOfProtectiveSensation([
        { code: 'MONOFILAMENT_RIGHT', value_json: { felt: missing(1) } },
      ]),
    ).toEqual([]);
  });

  it('is prompted per foot, and only for the foot the history is about', () => {
    const prompts = priorLossOfProtectiveSensation([
      { code: 'MONOFILAMENT_LEFT', value_json: { felt: missing(4) } },
      { code: 'MONOFILAMENT_RIGHT', value_json: { felt: missing(0) } },
    ]);
    expect(prompts.map((prompt) => prompt.side)).toEqual(['LEFT']);
  });

  it('reads the most recent test, not any test ever', () => {
    // Unlike an ulcer, this is re-measured every visit and re-stratified from what the
    // filament says today.
    expect(
      priorLossOfProtectiveSensation([
        { code: 'MONOFILAMENT_LEFT', value_json: { felt: missing(0) } },
        { code: 'MONOFILAMENT_LEFT', value_json: { felt: missing(6) } },
      ]),
    ).toEqual([]);
  });

  it('says nothing about a foot that was not tested, because nobody knows', () => {
    expect(
      priorLossOfProtectiveSensation([
        { code: 'MONOFILAMENT_LEFT', value_json: { not_tested: true } },
      ]),
    ).toEqual([]);
  });

  it('says nothing about a payload it cannot read, rather than guessing', () => {
    expect(priorLossOfProtectiveSensation([{ code: 'MONOFILAMENT_LEFT' }])).toEqual([]);
    expect(
      priorLossOfProtectiveSensation([{ code: 'MONOFILAMENT_LEFT', value_json: null }]),
    ).toEqual([]);
    expect(priorLossOfProtectiveSensation([{ code: 'MONOFILAMENT_LEFT', value_json: {} }])).toEqual(
      [],
    );
    expect(
      priorLossOfProtectiveSensation([{ code: 'MONOFILAMENT_LEFT', value_json: { felt: null } }]),
    ).toEqual([]);
    expect(
      priorLossOfProtectiveSensation([
        { code: 'MONOFILAMENT_LEFT', value_json: { felt: { hallux: 'no', toe_3: false } } },
      ]),
    ).toEqual([]);
  });

  it('ignores a superseded test', () => {
    expect(
      priorLossOfProtectiveSensation([
        { code: 'MONOFILAMENT_LEFT', value_json: { felt: missing(9) }, status: 'SUPERSEDED' },
      ]),
    ).toEqual([]);
  });
});

describe('an eye that has already been graded sight-threatening', () => {
  it('is prompted after severe non-proliferative disease', () => {
    const prompts = priorSightThreateningRetinopathy([
      { code: 'RETINOPATHY_LEFT', value_code: 'severe_npdr' },
    ]);
    expect(prompts[0]?.id).toBe('retinopathy_severe');
    expect(prompts[0]?.side).toBe('LEFT');
    expect(prompts[0]?.fields).toEqual(['RETINOPATHY_LEFT']);
  });

  it('is prompted after proliferative disease, on the eye it was found in', () => {
    const prompts = priorSightThreateningRetinopathy([
      { code: 'RETINOPATHY_RIGHT', value_code: 'pdr' },
    ]);
    expect(prompts.map((prompt) => prompt.side)).toEqual(['RIGHT']);
  });

  it('is still prompted after treatment has graded it back down', () => {
    expect(
      priorSightThreateningRetinopathy([
        { code: 'RETINOPATHY_RIGHT', value_code: 'moderate_npdr' },
        { code: 'RETINOPATHY_RIGHT', value_code: 'pdr' },
      ]),
    ).toHaveLength(1);
  });

  it('is not prompted by a mild or ungradable eye', () => {
    expect(
      priorSightThreateningRetinopathy([
        { code: 'RETINOPATHY_LEFT', value_code: 'mild_npdr' },
        { code: 'RETINOPATHY_RIGHT', value_code: 'ungradable' },
      ]),
    ).toEqual([]);
  });

  it('is not prompted by a row that holds no grade at all', () => {
    expect(priorSightThreateningRetinopathy([{ code: 'RETINOPATHY_LEFT' }])).toEqual([]);
  });

  it('ignores a corrected grade', () => {
    expect(
      priorSightThreateningRetinopathy([
        { code: 'RETINOPATHY_LEFT', value_code: 'pdr', status: 'CORRECTED' },
      ]),
    ).toEqual([]);
  });
});

describe('eyes nobody has looked at', () => {
  it('are prompted when the record says the screening is due', () => {
    const prompts = retinopathyScreeningDue([{ code: 'RETINOPATHY_SCREEN', value_code: 'due' }]);
    expect(prompts[0]?.id).toBe('retinopathy_due');
    expect(prompts[0]?.fields).toEqual(['RETINOPATHY_SCREEN']);
  });

  it('are prompted when the record says they have never been examined', () => {
    expect(
      retinopathyScreeningDue([{ code: 'RETINOPATHY_SCREEN', value_code: 'never' }]),
    ).toHaveLength(1);
  });

  it('are not prompted once somebody has looked', () => {
    // The current status, because this is a standing state rather than a historical event:
    // a patient screened last month is not due, whatever last year's status said.
    expect(
      retinopathyScreeningDue([
        { code: 'RETINOPATHY_SCREEN', value_code: 'today' },
        { code: 'RETINOPATHY_SCREEN', value_code: 'never' },
      ]),
    ).toEqual([]);
  });

  it('are not prompted when the patient declined, which is an answer', () => {
    expect(
      retinopathyScreeningDue([{ code: 'RETINOPATHY_SCREEN', value_code: 'declined' }]),
    ).toEqual([]);
  });

  it('are not prompted by a status row that holds no status', () => {
    expect(retinopathyScreeningDue([{ code: 'RETINOPATHY_SCREEN' }])).toEqual([]);
  });

  it('are not prompted when the record has never been asked', () => {
    // Absence is not the same statement as "never". A first-visit patient would otherwise
    // arrive under a prompt for every screening the clinic does, which is how a prompt turns
    // into wallpaper.
    expect(retinopathyScreeningDue([])).toEqual([]);
    expect(
      retinopathyScreeningDue([
        { code: 'RETINOPATHY_SCREEN', status: 'CORRECTED', value_code: 'due' },
      ]),
    ).toEqual([]);
  });
});

describe('the prompts as the examiner reads them', () => {
  it('says nothing at all about a patient with no history', () => {
    expect(promptsFor([])).toEqual([]);
  });

  it('runs every rule that exists', () => {
    // A prompt nobody runs is a prompt that quietly stops firing, so the list the screen
    // uses is the list this test counts.
    expect(PROMPT_RULES).toHaveLength(4);
    for (const rule of PROMPT_RULES) expect(rule([])).toEqual([]);
  });

  it('puts the foot that has already broken down above the eye nobody has seen', () => {
    const history: HistoryRow[] = [
      { code: 'RETINOPATHY_SCREEN', value_code: 'never' },
      { code: 'RETINOPATHY_LEFT', value_code: 'pdr' },
      { code: 'FOOT_ULCER_RIGHT', value_code: 'grade_1' },
      {
        code: 'MONOFILAMENT_RIGHT',
        value_json: { felt: { ...Object.fromEntries(MONOFILAMENT_SITES.map((s) => [s, false])) } },
      },
    ];
    expect(promptsFor(history).map((prompt) => prompt.id)).toEqual([
      'ulcer_history',
      'sensation_lost',
      'retinopathy_severe',
      'retinopathy_due',
    ]);
  });

  it('points every prompt at a field that exists on the form', () => {
    // A prompt pointing at a code no screen draws is a prompt the examiner cannot act on.
    const codes = new Set(EXAM_FIELDS.map((field) => field.code));
    const history: HistoryRow[] = [
      { code: 'FOOT_ULCER_LEFT', value_code: 'grade_1' },
      { code: 'RETINOPATHY_SCREEN', value_code: 'due' },
      { code: 'RETINOPATHY_RIGHT', value_code: 'severe_npdr' },
      { code: 'MONOFILAMENT_LEFT', value_json: { felt: { hallux: false, toe_3: false } } },
    ];
    const prompts = promptsFor(history);
    expect(prompts.length).toBeGreaterThan(0);
    for (const prompt of prompts) {
      for (const field of prompt.fields) expect(codes.has(field), field).toBe(true);
    }
  });
});

// --- the words ---

describe('every label this station needs exists in both languages', () => {
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

  it('has a label for every finding', () => {
    for (const field of EXAM_FIELDS) present(`examination.finding.${field.label}`);
  });

  it('has a name for every monofilament site', () => {
    for (const site of MONOFILAMENT_SITES) present(`examination.site.${site}`);
  });

  it('has a word for every side, and for every state a site can be in', () => {
    for (const side of SIDES) {
      present(`examination.sideName.${side}`);
      present(`examination.footTitle.${side}`);
    }
    for (const state of ['felt', 'not_felt', 'unknown']) {
      present(`examination.siteState.${state}`);
    }
  });

  it('has a name for every risk category the server can return', () => {
    for (const category of ['very_low', 'low', 'moderate', 'high']) {
      present(`examination.risk.${category}`);
    }
  });

  it('has the sentence every prompt names', () => {
    // The screen looks the key up from the prompt, so a rule that fires with a key nobody
    // wrote would show the examiner a raw identifier at the top of the form.
    const history: HistoryRow[] = [
      { code: 'FOOT_ULCER_LEFT', value_code: 'grade_1' },
      { code: 'RETINOPATHY_SCREEN', value_code: 'due' },
      { code: 'RETINOPATHY_RIGHT', value_code: 'pdr' },
      { code: 'MONOFILAMENT_LEFT', value_json: { felt: { hallux: false, toe_3: false } } },
    ];
    const prompts = promptsFor(history);
    expect(prompts).toHaveLength(4);
    for (const prompt of prompts) present(`examination.${prompt.messageKey}`);
  });
});
