import { afterEach, describe, expect, it, vi } from 'vitest';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';
import { CLINIC_UTC_OFFSET_MINUTES as COUNSELING_CLINIC_OFFSET } from '../src/features/counseling/state';
import {
  CLINIC_UTC_OFFSET_MINUTES,
  KNOWN_ROLE_CODES,
  NO_DIRECTORY,
  NO_PROVENANCE,
  SOURCES,
  clinicDay,
  clockTime,
  deviceOf,
  indexDirectory,
  observationFor,
  ofAllergy,
  ofAllergyAssertion,
  ofCounselingTick,
  ofCriticalAlert,
  ofHistoryItem,
  ofInstrumentResponse,
  ofObservation,
  personOf,
  readingFor,
  readingOf,
  roleOf,
  sourceReading,
  stationOf,
  whenOf,
  type Directory,
  type Reader,
} from '../src/features/attribution/state';

/*
 * The directory binding reaches the Keystore through lib/credentials, and the native module
 * cannot load under Node. Mocked exactly as the counselling suites do; nothing here exercises
 * it. The import is dynamic for the same reason: the mock has to be in place first.
 */
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async () => undefined),
  getItemAsync: vi.fn(async () => null),
  deleteItemAsync: vi.fn(async () => undefined),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const { DIRECTORY_QUERY_KEY, DIRECTORY_STALE_MS, getDirectory } =
  await import('../src/features/attribution/api');

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/**
 * "Entered by", on the operator's phone (CP61, §4.2, [R-03]).
 *
 * The component is a React Native component and is judged in a clinic corridor by somebody
 * holding a phone. What is checked here is every decision behind it, and six of these tests
 * matter more than the rest.
 *
 * **A uuid never reaches a screen, and neither does a blank.** `personOf` walks four answers
 * and finishes with a sentence, and the tests walk all five rungs of that ladder including the
 * one where the directory is missing entirely. The failure being prevented is the pair of
 * failures §4.2 exists to remove: a reviewer shown `9f2c…` goes and asks somebody, and a
 * reviewer shown nothing assumes the record does not know.
 *
 * **A missing directory does not blank the screen.** Every reading is run against
 * `NO_DIRECTORY` as well as against a loaded one, and the assertion is that the role, the
 * station code, the time and the source all still come through. A tablet whose one extra
 * request failed still holds everything the value itself carries.
 *
 * **Somebody else's entry is never "you".** `mine` is true only when the reader's id and the
 * author's id are both known and equal, and an empty reader — the ordinary state for the first
 * moments after start-up — is never a match.
 *
 * **A source is a word, and an absent source is its own word.** All five codes, a code this
 * build has never heard of, and no code at all: four different answers, and `STATION` is not
 * the answer to any of the last two. A value whose payload forgot to say where it came from
 * reading as an operator's own measurement is the safe-looking lie criterion 3 is about.
 *
 * **Both people appear where the payload carries both, and not otherwise.** A history item
 * names its amender; a counselling tick names whoever withdrew it; an observation carries only
 * the fact that it was corrected and the id of the row that replaced it, so no second name is
 * produced. A name invented on that last case would be the name of the person being corrected.
 *
 * **The clinic's clock is stated twice in this application and cannot drift.** The offset here
 * and the offset in the counselling feature are asserted equal, and both are asserted against
 * `Asia/Dhaka`.
 */

// --- fixtures: the clinic, as the directory describes it ---

const RAHIM = '11111111-1111-4111-8111-111111111111';
const NASRIN = '22222222-2222-4222-8222-222222222222';
const DEPARTED = '33333333-3333-4333-8333-333333333333';
const SUSPENDED = '44444444-4444-4444-8444-444444444444';
const STRANGER = '55555555-5555-4555-8555-555555555555';
const TABLET = '66666666-6666-4666-8666-666666666666';

const DIRECTORY: Directory = {
  staff: [
    {
      id: RAHIM,
      code: 'C001',
      name_en: 'Md Rahim Uddin',
      name_bn: 'মোঃ রহিম উদ্দিন',
      status: 'active',
    },
    // Named in English only. Publishing does not police staff names the way it polices item
    // text, so a half-filled record is an ordinary case rather than an edge one.
    { id: NASRIN, code: 'C002', name_en: 'Nasrin Akter', name_bn: '', status: 'active' },
    {
      id: DEPARTED,
      code: 'C003',
      name_en: 'Shahida Begum',
      name_bn: 'শাহিদা বেগম',
      status: 'deactivated',
    },
    { id: SUSPENDED, code: 'C004', name_en: 'Kamal Hossain', name_bn: '', status: 'suspended' },
  ],
  devices: [{ id: TABLET, name: 'Station 3 tablet', kind: 'tablet', status: 'active' }],
  stations: [
    { code: 'STN_VITALS', name_en: 'Vitals', name_bn: 'ভাইটালস', sequence: 5 },
    { code: 'STN_HISTORY', name_en: 'History', name_bn: 'ইতিহাস', sequence: 4 },
  ],
  as_of: '2026-03-14T05:00:00Z',
};

const INDEX = indexDirectory(DIRECTORY);

/** Half past eleven on the fourteenth of March, clinic time. */
const MARCH_MORNING = '2026-03-14T05:32:00Z';
const TODAY = '2026-03-14';

const reader = (over: Partial<Reader> = {}): Reader => ({
  locale: 'en',
  me: NASRIN,
  today: TODAY,
  ...over,
});

// --- the clinic's clock ---

describe('the clinic clock is one clock, stated in two places', () => {
  it('agrees with the counselling feature', () => {
    // Two copies on purpose — see the note beside the constant. This is the seam that keeps
    // them from drifting apart quietly, which they would, because nothing else connects them.
    expect(CLINIC_UTC_OFFSET_MINUTES).toBe(COUNSELING_CLINIC_OFFSET);
  });

  it('agrees with Asia/Dhaka', () => {
    const at = new Date('2026-03-14T05:32:00Z');
    const dhaka = new Intl.DateTimeFormat('en-GB', {
      timeZone: 'Asia/Dhaka',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    }).format(at);
    expect(clockTime(MARCH_MORNING)).toBe(dhaka);
  });

  it('reads a timestamp as a clinic time and a clinic day', () => {
    expect(clockTime(MARCH_MORNING)).toBe('11:32');
    expect(clinicDay(MARCH_MORNING)).toBe('2026-03-14');
  });

  it('rolls the day over in the clinic hours, not in UTC', () => {
    // Ten to seven in the evening UTC is ten to one in the morning of the next day here. A
    // value taken at that moment belongs to the following clinic day, and a screen that dated
    // it by UTC would file the last patient of an evening under the wrong date.
    expect(clinicDay('2026-03-14T18:50:00Z')).toBe('2026-03-15');
    expect(clockTime('2026-03-14T18:50:00Z')).toBe('00:50');
  });

  it('says nothing rather than Invalid Date', () => {
    // A clinical row that says a thing happened at Invalid Date is worse than one that leaves
    // the time out: the first is read as data.
    for (const bad of ['', '   ', 'yesterday', 'not-a-time']) {
      expect(clockTime(bad)).toBe('');
      expect(clinicDay(bad)).toBe('');
    }
  });
});

// --- the directory ---

describe('the directory is a lookup, and its absence is a value', () => {
  it('indexes staff by id, devices by id and stations by code', () => {
    expect(INDEX.loaded).toBe(true);
    expect(INDEX.staff.get(RAHIM)?.code).toBe('C001');
    expect(INDEX.devices.get(TABLET)?.name).toBe('Station 3 tablet');
    expect(INDEX.stations.get('STN_VITALS')?.name_bn).toBe('ভাইটালস');
    expect(INDEX.asOf).toBe('2026-03-14T05:00:00Z');
  });

  it('treats a missing directory as an empty one that says it is missing', () => {
    for (const nothing of [null, undefined]) {
      const index = indexDirectory(nothing);
      expect(index.loaded).toBe(false);
      expect(index.staff.size).toBe(0);
    }
    expect(NO_DIRECTORY.loaded).toBe(false);
  });

  it('keeps deactivated staff and retired devices listed', () => {
    // The whole reason this endpoint exists rather than a join per value. Most of what a
    // reviewer asks about is a value from months ago, and a directory of current staff only
    // would render a blank for exactly the person the question is about.
    expect(INDEX.staff.get(DEPARTED)?.name_en).toBe('Shahida Begum');
  });
});

// --- who ---

describe('who entered it, walked down four answers and never a uuid', () => {
  it('prefers the name the payload already carried', () => {
    const person = personOf(
      STRANGER,
      'COUNSELOR',
      { code: 'C009', nameEN: 'Ayesha Siddika', nameBN: 'আয়েশা সিদ্দিকা' },
      NO_DIRECTORY,
      reader(),
    );
    // Present with no directory at all, and joined from the same staff record the directory is
    // built from. A counselling tick is the one value that must never fall back to a hat.
    expect(person.kind).toBe('named');
    expect(person.text).toBe('Ayesha Siddika');
  });

  it('falls back to the directory, then the staff code, then the role', () => {
    expect(personOf(RAHIM, 'COUNSELOR', null, INDEX, reader()).text).toBe('Md Rahim Uddin');

    // Listed, but with no name in either language: the code identifies a person where a role
    // only identifies a hat, so it comes first.
    const codeOnly = indexDirectory({
      ...DIRECTORY,
      staff: [{ id: RAHIM, code: 'C001', name_en: '', name_bn: '', status: 'active' }],
    });
    const byCode = personOf(RAHIM, 'COUNSELOR', null, codeOnly, reader());
    expect(byCode.kind).toBe('coded');
    expect(byCode.text).toBe('C001');

    const byRole = personOf(STRANGER, 'COUNSELOR', null, INDEX, reader());
    expect(byRole.kind).toBe('role');
    expect(byRole.key).toBe('person.roleOnly');
  });

  it('says so in a sentence when nothing names anybody', () => {
    const nobody = personOf('', '', null, INDEX, reader());
    expect(nobody.kind).toBe('nobody');
    expect(nobody.key).toBe('person.nobody');
    expect(nobody.text).toBe('');
  });

  it('never puts a uuid on a screen', () => {
    // The one form of the answer that cannot be read aloud, looks like a defect, and sends a
    // reviewer to ask a colleague instead of reading. Checked on every rung of the ladder,
    // with and without a directory.
    for (const index of [INDEX, NO_DIRECTORY]) {
      for (const role of ['COUNSELOR', '']) {
        const person = personOf(STRANGER, role, null, index, reader());
        expect(person.text).not.toContain(STRANGER);
        expect(person.code).not.toContain(STRANGER);
      }
    }
  });

  it('reads a name in the language the reader is using, and falls back to the other spelling', () => {
    expect(personOf(RAHIM, '', null, INDEX, reader({ locale: 'bn' })).text).toBe('মোঃ রহিম উদ্দিন');
    // No Bangla spelling on file. The English one, rather than a blank where a person belongs.
    expect(personOf(NASRIN, '', null, INDEX, reader({ locale: 'bn' })).text).toBe('Nasrin Akter');
  });

  it('says a departed colleague has left, and does not say it of a suspended one', () => {
    const gone = personOf(DEPARTED, '', null, INDEX, reader());
    expect(gone.text).toBe('Shahida Begum');
    expect(gone.departed).toBe(true);
    expect(gone.standingKey).toBe('standing.deactivated');

    // Still here. Sending a reviewer away from somebody who is at their desk is a different
    // wrong answer, not a milder one.
    const held = personOf(SUSPENDED, '', null, INDEX, reader());
    expect(held.departed).toBe(false);
    expect(held.standingKey).toBe('standing.suspended');

    expect(personOf(RAHIM, '', null, INDEX, reader()).standingKey).toBeNull();
  });

  it('is "you" only when both ids are known and equal', () => {
    expect(personOf(NASRIN, '', null, INDEX, reader()).mine).toBe(true);
    expect(personOf(RAHIM, '', null, INDEX, reader()).mine).toBe(false);
    // The session store has not answered yet. An unknown reader is never "you", because that
    // sentence against somebody else's work is the one this must never produce.
    expect(personOf(NASRIN, '', null, INDEX, reader({ me: '' })).mine).toBe(false);
    expect(personOf('', '', null, INDEX, reader({ me: '' })).mine).toBe(false);
  });
});

// --- the hat ---

describe('the role is named where this build knows it and shown as its code where it does not', () => {
  it('names every role the message files carry', () => {
    for (const code of KNOWN_ROLE_CODES) {
      expect(roleOf(code).key, code).toBe(`codes.${code}`);
    }
  });

  it('shows an unknown role as its own code rather than hiding it', () => {
    // A tablet a version behind the server. `PROSTHETIST` is ugly on screen and it is
    // readable; a blank where the role belongs turns an attribution with no name into an
    // attribution with nothing at all.
    const unknown = roleOf('PROSTHETIST');
    expect(unknown.code).toBe('PROSTHETIST');
    expect(unknown.key).toBeNull();
  });

  it('has nothing to say when the payload named no role', () => {
    expect(roleOf('  ')).toEqual({ code: '', key: null });
  });
});

// --- what kind of evidence ---

describe('the source is a word before it is a colour', () => {
  it('has a word and a meaning for every source the contract enumerates', () => {
    for (const code of SOURCES) {
      const source = sourceReading(code);
      expect(source.known, code).toBe(true);
      expect(source.key, code).toBe(`source.${code}`);
      expect(source.meaningKey, code).toBe(`sourceMeaning.${code}`);
    }
  });

  it('marks OCR and only OCR', () => {
    expect(sourceReading('OCR').ocr).toBe(true);
    expect(sourceReading('OCR').tone).toBe('transcribed');
    for (const other of SOURCES.filter((code) => code !== 'OCR')) {
      expect(sourceReading(other).ocr, other).toBe(false);
    }
  });

  it('draws a station entry plainly and everything else as transcribed', () => {
    // Tinting the ordinary case makes the uncommon one harder to spot, which is the opposite
    // of the point. The word is there either way.
    expect(sourceReading('STATION').tone).toBe('plain');
    for (const elsewhere of ['OCR', 'FIELD', 'DEVICE', 'PATIENT']) {
      expect(sourceReading(elsewhere).tone, elsewhere).toBe('transcribed');
    }
  });

  it('does not read an absent source as a station measurement', () => {
    // The failure criterion 3 exists to prevent: a transcribed number that looks measured.
    const missing = sourceReading('');
    expect(missing.key).toBe('source.unrecorded');
    expect(missing.tone).toBe('unrecorded');
    expect(missing.ocr).toBe(false);
  });

  it('shows a source this build has never heard of as its own word', () => {
    const future = sourceReading('LAB_FEED');
    expect(future.known).toBe(false);
    expect(future.key).toBeNull();
    expect(future.code).toBe('LAB_FEED');
    // Not `plain`. A future source silently rendering as a station entry is the same failure
    // as an absent one rendering that way.
    expect(future.tone).toBe('unrecorded');
  });

  it('is not confused by case or padding', () => {
    expect(sourceReading('  ocr ').key).toBe('source.OCR');
  });
});

// --- when ---

describe('when it was entered, in the words the collapsed line can hold', () => {
  it('shows a time for today and a date for anything older', () => {
    // A value entered last March showing "11:32" and nothing else is a value a reviewer reads
    // as this morning's. The exact time is still one tap away.
    expect(whenOf(MARCH_MORNING, TODAY).text).toBe('11:32');
    expect(whenOf(MARCH_MORNING, TODAY).today).toBe(true);
    expect(whenOf(MARCH_MORNING, '2026-09-04').text).toBe('2026-03-14');
    expect(whenOf(MARCH_MORNING, '2026-09-04').today).toBe(false);
  });

  it('carries the exact date and time whatever the collapsed line shows', () => {
    const when = whenOf(MARCH_MORNING, '2026-09-04');
    expect(when.date).toBe('2026-03-14');
    expect(when.time).toBe('11:32');
    expect(when.iso).toBe(MARCH_MORNING);
  });

  it('never guesses that an unreadable timestamp is today', () => {
    const broken = whenOf('whenever', TODAY);
    expect(broken.known).toBe(false);
    expect(broken.today).toBe(false);
    expect(broken.key).toBe('whenUnknown');
  });

  it('is not today when the day the reader is having is unknown', () => {
    expect(whenOf(MARCH_MORNING, '').today).toBe(false);
  });
});

// --- where, and on what ---

describe('the station and the device', () => {
  it('names a station the directory knows, in the language the reader is using', () => {
    expect(stationOf('STN_VITALS', INDEX, 'en')?.text).toBe('Vitals');
    expect(stationOf('STN_VITALS', INDEX, 'bn')?.text).toBe('ভাইটালস');
  });

  it('shows a station the directory does not list as its own code', () => {
    const stray = stationOf('STN_PHARMACY', INDEX, 'en');
    expect(stray?.text).toBe('STN_PHARMACY');
    expect(stray?.named).toBe(false);
  });

  it('has nothing to draw when the value names no station', () => {
    expect(stationOf('', INDEX, 'en')).toBeNull();
    expect(deviceOf('', INDEX)).toBeNull();
  });

  it('names a device and says when it is out of service', () => {
    expect(deviceOf(TABLET, INDEX)?.text).toBe('Station 3 tablet');
    expect(deviceOf(TABLET, INDEX)?.standingKey).toBeNull();

    const retired = indexDirectory({
      ...DIRECTORY,
      devices: [{ id: TABLET, name: 'Station 3 tablet', kind: 'tablet', status: 'revoked' }],
    });
    expect(deviceOf(TABLET, retired)?.standingKey).toBe('deviceStanding.retired');
  });
});

// --- the payloads ---

describe('one provenance from every clinical payload this application reads back', () => {
  it('takes an observation whole, source and station and all', () => {
    const provenance = ofObservation({
      recorded_by: RAHIM,
      recorded_role: 'CLINICAL_ASSISTANT',
      recorded_at: MARCH_MORNING,
      station_code: 'STN_VITALS',
      device_id: TABLET,
      source: 'OCR',
      status: 'ACTIVE',
    });
    expect(provenance).toEqual({
      by: RAHIM,
      role: 'CLINICAL_ASSISTANT',
      at: MARCH_MORNING,
      station: 'STN_VITALS',
      device: TABLET,
      source: 'OCR',
      named: null,
      correction: null,
    });
  });

  it('separates a value that was wrong from one that was re-measured', () => {
    // Two different facts, and a report that conflated them would count re-measurements as an
    // error rate. The server keeps them apart; so does this.
    const wrong = ofObservation({ status: 'CORRECTED', replaced_by: 'obs-2' });
    expect(wrong.correction?.kind).toBe('corrected');
    expect(wrong.correction?.replacedBy).toBe('obs-2');

    const again = ofObservation({ status: 'SUPERSEDED', replaced_by: 'obs-3' });
    expect(again.correction?.kind).toBe('superseded');

    expect(ofObservation({ status: 'ACTIVE' }).correction).toBeNull();
  });

  it('names nobody as the corrector of an observation', () => {
    // The corrector is the author of the row that *replaced* this one, and this payload does
    // not contain it. A name here would be the name of whoever wrote the value being
    // corrected, printed beside the correction. CP62 fetches the other row.
    const corrected = ofObservation({
      recorded_by: RAHIM,
      status: 'CORRECTED',
      replaced_by: 'obs-2',
    });
    expect(corrected.correction?.by).toBe('');
    expect(corrected.correction?.named).toBeNull();

    const reading = readingOf(corrected, INDEX, reader());
    expect(reading.correction?.by).toBeNull();
    expect(reading.person.text).toBe('Md Rahim Uddin');
  });

  it('names both people on an amended history item', () => {
    // Criterion 2, in the one payload that can actually satisfy it.
    const reading = readingOf(
      ofHistoryItem({
        recorded_by: RAHIM,
        recorded_role: 'HISTORY',
        recorded_at: MARCH_MORNING,
        amended_by: DEPARTED,
        amended_at: '2026-03-14T06:00:00Z',
      }),
      INDEX,
      reader(),
    );
    expect(reading.person.text).toBe('Md Rahim Uddin');
    expect(reading.correction?.kind).toBe('amended');
    expect(reading.correction?.by?.text).toBe('Shahida Begum');
    expect(reading.correction?.when.time).toBe('12:00');
  });

  it('takes the station, the source and the device off a history item', () => {
    // All three arrived with the migration that added the columns and filled them from the
    // event envelope. Before it, this shape dropped them and every history item on the screen
    // said "source not recorded", which was true and useless.
    const provenance = ofHistoryItem({
      recorded_by: RAHIM,
      recorded_role: 'HISTORY',
      recorded_at: MARCH_MORNING,
      station_code: 'STN_HISTORY',
      device_id: TABLET,
      source: 'OCR',
    });
    expect(provenance.station).toBe('STN_HISTORY');
    expect(provenance.device).toBe(TABLET);
    expect(sourceReading(provenance.source).ocr).toBe(true);
  });

  it('does not read a row written before the migration as a station entry', () => {
    // The failure that got *easier* the day the column landed: an empty source used to be
    // every history item and is now the exception, which is exactly when somebody decides the
    // exception must mean the ordinary thing. An allergy the machine read off a photograph of
    // paper and one an officer took down with the patient present are different warnings, and
    // an old row is neither — it is a row that does not say.
    for (const old of [
      ofHistoryItem({ recorded_by: RAHIM }),
      ofAllergy({ recorded_by: RAHIM }),
      ofAllergyAssertion({ asserted_by: RAHIM }),
    ]) {
      expect(old.source).toBe('');
      expect(sourceReading(old.source).key).toBe('source.unrecorded');
      expect(sourceReading(old.source).tone).toBe('unrecorded');
    }
  });

  it('takes an allergy and a standing assertion, station and source and device included', () => {
    expect(
      ofAllergy({
        recorded_by: RAHIM,
        recorded_role: 'HISTORY',
        recorded_at: MARCH_MORNING,
        station_code: 'STN_HISTORY',
        device_id: TABLET,
        source: 'STATION',
      }),
    ).toMatchObject({
      by: RAHIM,
      role: 'HISTORY',
      at: MARCH_MORNING,
      station: 'STN_HISTORY',
      device: TABLET,
      source: 'STATION',
    });
    expect(
      ofAllergyAssertion({
        asserted_by: NASRIN,
        asserted_role: 'HISTORY',
        asserted_at: MARCH_MORNING,
        station_code: 'STN_HISTORY',
        device_id: TABLET,
        source: 'STATION',
      }),
    ).toMatchObject({
      by: NASRIN,
      role: 'HISTORY',
      at: MARCH_MORNING,
      station: 'STN_HISTORY',
      device: TABLET,
      source: 'STATION',
    });
  });

  it('uses the names on the counselling tick and reads an un-tick as a correction', () => {
    const provenance = ofCounselingTick({
      ticked_by: RAHIM,
      ticked_role: 'COUNSELOR',
      ticked_at: MARCH_MORNING,
      station_code: 'STN_COUNSELING',
      device_id: TABLET,
      ticked_by_code: 'C001',
      ticked_by_name_en: 'Md Rahim Uddin',
      ticked_by_name_bn: 'মোঃ রহিম উদ্দিন',
      undone_by: NASRIN,
      undone_at: '2026-03-14T05:34:00Z',
      undone_by_code: 'C002',
      undone_by_name_en: 'Nasrin Akter',
      undone_by_name_bn: '',
    });
    // Resolved on the wire, so it works with no directory at all — which is the ordinary state
    // of a phone in this clinic's corridor.
    const reading = readingOf(provenance, NO_DIRECTORY, reader());
    expect(reading.person.text).toBe('Md Rahim Uddin');
    expect(reading.correction?.kind).toBe('withdrawn');
    expect(reading.correction?.by?.text).toBe('Nasrin Akter');
    // The reader took it back themselves, and the record says so.
    expect(reading.correction?.by?.mine).toBe(true);
    // §5.2 walks three rooms and the same counsellor may tick in two of them, so the room is
    // part of the answer to "where was this covered".
    expect(reading.station?.code).toBe('STN_COUNSELING');
    expect(provenance.device).toBe(TABLET);
    // A counselling item is covered by a person talking to a patient. There is no other way
    // for one to arrive, so no source is claimed.
    expect(provenance.source).toBe('');
  });

  it('takes a critical alert, which carries no source of its own', () => {
    const provenance = ofCriticalAlert({
      raised_by: RAHIM,
      raised_role: 'CLINICAL_ASSISTANT',
      raised_at: MARCH_MORNING,
      station_code: 'STN_VITALS',
    });
    expect(provenance.station).toBe('STN_VITALS');
    // Named rather than assumed, and deliberately not copied onto the alert: an alert's number
    // is a snapshot of an observation, and a provenance copied beside a copied value drifts
    // from the original the moment that observation is corrected. Following `observation_id`
    // is the honest fix and it belongs with CP62's correction chain.
    expect(provenance.source).toBe('');
    expect(provenance.device).toBe('');
    // Which is not silence: the panel says *source not recorded* rather than `STATION`, which
    // would be the reassuring version of not knowing.
    expect(sourceReading(provenance.source).key).toBe('source.unrecorded');
  });

  it('gives an empty provenance for a payload that has not arrived', () => {
    for (const nothing of [null, undefined]) {
      expect(ofObservation(nothing)).toEqual(NO_PROVENANCE);
      expect(ofHistoryItem(nothing)).toEqual(NO_PROVENANCE);
      expect(ofAllergy(nothing)).toEqual(NO_PROVENANCE);
      expect(ofAllergyAssertion(nothing)).toEqual(NO_PROVENANCE);
      expect(ofCounselingTick(nothing)).toEqual(NO_PROVENANCE);
      expect(ofCriticalAlert(nothing)).toEqual(NO_PROVENANCE);
      expect(ofInstrumentResponse(nothing)).toEqual(NO_PROVENANCE);
    }
  });

  it('reads a questionnaire response, with the names the server already resolved (CP58)', () => {
    const provenance = ofInstrumentResponse({
      recorded_by: RAHIM,
      recorded_role: 'COUNSELOR',
      recorded_at: '2026-09-05T04:12:00Z',
      recorded_by_code: 'STF-014',
      recorded_by_name_en: 'Rahim Uddin',
      recorded_by_name_bn: 'রহিম উদ্দিন',
      station_code: 'STN_COUNSELING',
      device_id: 'device-3',
      source: 'MOBILE_ONLINE',
      status: 'ACTIVE',
    });
    expect(provenance.by).toBe(RAHIM);
    expect(provenance.station).toBe('STN_COUNSELING');
    expect(provenance.named?.nameBN).toBe('রহিম উদ্দিন');
    expect(provenance.correction).toBeNull();
  });

  it('reads a superseded response as a correction with no corrector named', () => {
    // Answering again supersedes rather than edits, and the person who answered again is the
    // author of the response that *replaced* this one. This payload does not carry them, and a
    // name here would be the name of whoever gave the earlier answers.
    const provenance = ofInstrumentResponse({ recorded_by: RAHIM, status: 'SUPERSEDED' });
    expect(provenance.correction).toEqual({
      kind: 'superseded',
      by: '',
      named: null,
      role: '',
      at: '',
      replacedBy: '',
    });
  });
});

// --- finding the row a derived value came off ---

describe('matching a derived value back to the measurement it was computed from', () => {
  const rows = [
    { code: 'BODY_WEIGHT', effective_at: '2026-03-14T05:00:00Z', recorded_by: NASRIN },
    { code: 'BODY_WEIGHT', effective_at: '2025-09-01T05:00:00Z', recorded_by: RAHIM },
    { code: 'BODY_HEIGHT', effective_at: '2026-03-14T05:00:00Z', recorded_by: RAHIM },
  ];

  it('matches on the code and the moment together', () => {
    expect(
      observationFor(rows, { code: 'BODY_WEIGHT', effective_at: '2025-09-01T05:00:00Z' })
        ?.recorded_by,
    ).toBe(RAHIM);
  });

  it('refuses to guess when the moment is missing', () => {
    // Matching on the code alone would take the newest row, which is very often not the one
    // the server used. An attribution pointing at the wrong measurement puts a colleague's
    // name against a number they did not take, which is worse than no attribution.
    expect(observationFor(rows, { code: 'BODY_WEIGHT' })).toBeNull();
    expect(observationFor(rows, { code: 'BODY_WEIGHT', effective_at: '' })).toBeNull();
    expect(observationFor(rows, { code: '', effective_at: '2026-03-14T05:00:00Z' })).toBeNull();
    expect(observationFor(undefined, { code: 'BODY_WEIGHT', effective_at: MARCH_MORNING })).toBe(
      null,
    );
  });

  it('compares moments as instants, not as strings', () => {
    // The same moment reaches a client as `...Z` from one endpoint and `+00:00` from another.
    expect(
      observationFor(rows, { code: 'BODY_HEIGHT', effective_at: '2026-03-14T05:00:00+00:00' })
        ?.recorded_by,
    ).toBe(RAHIM);
  });

  it('returns nothing for a measurement that is not in the list', () => {
    expect(observationFor(rows, { code: 'BODY_WEIGHT', effective_at: MARCH_MORNING })).toBeNull();
  });
});

// --- the whole reading ---

describe('the reading a screen draws', () => {
  const observation = ofObservation({
    recorded_by: RAHIM,
    recorded_role: 'CLINICAL_ASSISTANT',
    recorded_at: MARCH_MORNING,
    station_code: 'STN_VITALS',
    device_id: TABLET,
    source: 'OCR',
  });

  it('resolves everything the panel shows', () => {
    const reading = readingOf(observation, INDEX, reader());
    expect(reading.person.text).toBe('Md Rahim Uddin');
    expect(reading.person.code).toBe('C001');
    expect(reading.role.key).toBe('codes.CLINICAL_ASSISTANT');
    expect(reading.station?.text).toBe('Vitals');
    expect(reading.when.date).toBe('2026-03-14');
    expect(reading.source.ocr).toBe(true);
    expect(reading.directoryMissing).toBe(false);
    expect(reading.device?.text).toBe('Station 3 tablet');
  });

  it('draws no device row for a record that genuinely has none', () => {
    // A record written on the web has no device, and an empty row for it would turn an honest
    // absence into a field that looks broken — which is how operators learn that the reveal
    // has a part nobody fills in and stop reading the rest of it.
    const web = ofObservation({ recorded_by: RAHIM, recorded_at: MARCH_MORNING });
    expect(readingOf(web, INDEX, reader()).device).toBeNull();
  });

  it('says a device is out of service rather than sending somebody to look for it', () => {
    // The question a reviewer is actually asking when they open this row is whether the tablet
    // is still in the building. The directory keeps retired devices for the same reason it
    // keeps departed staff.
    const retired = indexDirectory({
      ...DIRECTORY,
      devices: [{ id: TABLET, name: 'Station 3 tablet', kind: 'tablet', status: 'revoked' }],
    });
    const reading = readingOf(observation, retired, reader());
    expect(reading.device?.text).toBe('Station 3 tablet');
    expect(reading.device?.standingKey).toBe('deviceStanding.retired');
  });

  it('keeps everything the value itself carries when the directory never arrived', () => {
    // The failure this prevents: one request that did not come back blanking the attribution
    // on every value on the screen, which is what teaches people to ignore attribution.
    const reading = readingOf(observation, NO_DIRECTORY, reader());
    expect(reading.directoryMissing).toBe(true);
    expect(reading.role.key).toBe('codes.CLINICAL_ASSISTANT');
    expect(reading.station?.text).toBe('STN_VITALS');
    expect(reading.when.date).toBe('2026-03-14');
    expect(reading.source.ocr).toBe(true);
    // The device id survives too, as its own bare id — unreadable, and still better than
    // dropping the one fact that says this value came off a particular tablet.
    expect(reading.device?.code).toBe(TABLET);
    // No name, and not a uuid and not a blank either.
    expect(reading.person.kind).toBe('role');
    expect(reading.person.key).toBe('person.roleOnly');
  });

  it('chooses one of four headlines and no fifth', () => {
    expect(readingOf(observation, INDEX, reader()).headline.key).toBe('headline.byPerson');
    expect(readingOf(observation, INDEX, reader({ me: RAHIM })).headline.key).toBe(
      'headline.byYou',
    );
    expect(readingOf(observation, NO_DIRECTORY, reader()).headline.key).toBe('headline.byRole');
    expect(readingOf(NO_PROVENANCE, INDEX, reader()).headline.key).toBe('headline.byNobody');
  });

  it('draws something for a value whose payload has not arrived', () => {
    // Criterion 4 is that no screen renders a clinical value without this component, so a row
    // that is still loading must render it saying nothing names an author — not omit it.
    const reading = readingFor(null, INDEX, reader());
    expect(reading.person.kind).toBe('nobody');
    expect(reading.source.key).toBe('source.unrecorded');
    expect(reading.when.key).toBe('whenUnknown');
  });
});

// --- the words behind every key this module can produce ---

type Tree = Record<string, unknown>;

function flatten(tree: Tree, prefix = ''): Map<string, string> {
  const out = new Map<string, string>();
  for (const [key, value] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object') {
      for (const [k, v] of flatten(value as Tree, path)) out.set(k, v);
    } else {
      out.set(path, String(value));
    }
  }
  return out;
}

const english = flatten(en as Tree);
const bangla = flatten(bn as Tree);

describe('every key this feature can produce has a sentence in both languages', () => {
  /*
   * The i18n suite checks the literal `t('…')` calls in the component. It cannot see these:
   * they are values, produced here and handed to the component as data. So they are listed
   * out, and the list is what turns "a source added to the contract" into a test failure here
   * rather than a bare identifier on a tablet where an explanation belongs.
   */
  const keys = [
    'person.roleOnly',
    'person.nobody',
    'standing.suspended',
    'standing.deactivated',
    'deviceStanding.retired',
    'whenUnknown',
    'headline.byYou',
    'headline.byPerson',
    'headline.byRole',
    'headline.byNobody',
    'correction.amended',
    'correction.withdrawn',
    'correction.corrected',
    'correction.superseded',
    'source.unrecorded',
    'sourceMeaning.unrecorded',
    ...SOURCES.map((code) => `source.${code}`),
    ...SOURCES.map((code) => `sourceMeaning.${code}`),
  ];

  it('has each of them in English and in Bangla', () => {
    for (const key of keys) {
      expect(english.has(`attribution.${key}`), `${key} in English`).toBe(true);
      expect(bangla.has(`attribution.${key}`), `${key} in Bangla`).toBe(true);
    }
  });

  it('has a word for every role code this build claims to know', () => {
    for (const code of KNOWN_ROLE_CODES) {
      expect(english.has(`role.codes.${code}`), `${code} in English`).toBe(true);
      expect(bangla.has(`role.codes.${code}`), `${code} in Bangla`).toBe(true);
    }
  });

  it('claims to know exactly the roles the message files carry', () => {
    // Both directions. A role added to the messages and not here would be shown as its bare
    // code beside a sentence that exists; a role removed from the messages and left here would
    // be shown as the literal key `role.codes.SOMETHING` at a reviewer.
    const inMessages = [...english.keys()]
      .filter((key) => key.startsWith('role.codes.'))
      .map((key) => key.slice('role.codes.'.length))
      // The switcher's own sentence for "no role at all", which is not a role.
      .filter((code) => code !== 'null');
    expect([...KNOWN_ROLE_CODES].sort()).toEqual(inMessages.sort());
  });

  it('says the same thing about the four corrections in four different sentences', () => {
    // Four kinds because they are four different facts. Sharing a sentence between "this was
    // wrong" and "this was re-measured" is how a re-measurement gets counted as an error.
    const sentences = ['amended', 'withdrawn', 'corrected', 'superseded'].map((kind) =>
      english.get(`attribution.correction.${kind}`),
    );
    expect(new Set(sentences).size).toBe(4);
  });
});

// --- the one request a session makes for it ---

describe('the directory is fetched once and cached for a clinic session', () => {
  function stubFetch(response: Response): { urls: string[] } {
    const urls: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (request: Request) => {
        urls.push(request.url);
        return response.clone();
      }),
    );
    return { urls };
  }

  it('asks the one endpoint and hands back what it said', async () => {
    const calls = stubFetch(
      new Response(JSON.stringify(DIRECTORY), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const directory = await getDirectory();
    expect(calls.urls[0]).toContain('/v1/directory');
    expect(indexDirectory(directory).staff.get(RAHIM)?.code).toBe('C001');
  });

  it('throws on a refusal rather than pretending the clinic has no staff', async () => {
    // A directory that came back empty and a directory that could not be read are different
    // facts. The first would name nobody and say the record does not know; the second has to
    // reach the caller so the panel can say this tablet could not read the staff list.
    stubFetch(
      new Response(JSON.stringify({ code: 'internal', message_en: 'no' }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    await expect(getDirectory()).rejects.toThrow();
  });

  it('is keyed and held for the length of a clinic session', () => {
    // One key, so React Query answers the second and the hundredth caller from cache and
    // coalesces simultaneous first callers into one request. Forty values on one screen is one
    // fetch; the alternative is forty, on a link that drops for stretches of a morning.
    expect(DIRECTORY_QUERY_KEY).toEqual(['directory']);
    // Long enough that a morning does not re-fetch it, because staff are not enrolled and
    // stations are not renamed during a clinic session — and the response carries `as_of` so a
    // screen can say how old its answer is rather than presenting it as live.
    expect(DIRECTORY_STALE_MS).toBeGreaterThanOrEqual(4 * 60 * 60_000);
  });
});
