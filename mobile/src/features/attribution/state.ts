import type { components } from '@dthcms/api-client';

/**
 * Who entered a clinical value, as data (CP61, §4.2, [R-03]).
 *
 * # Why every decision is in here and none of it is in the component
 *
 * A React Native component cannot be rendered outside a device, so anything it decides is a
 * decision nobody checks. What this feature decides is not layout. It decides **whose name
 * goes next to a number in a clinical record** — which is the one thing on the screen that
 * can be wrong about a person rather than about a patient — and it decides what a reviewer is
 * told when the answer is not available. Both of those live in pure functions with tests
 * beside them; `EnteredBy.tsx` is arrangement and holds no rule at all.
 *
 * # A lookup, not a property of the value
 *
 * Clinical reads carry ids: `recorded_by`, `recorded_role`, `station_code`, `source`. The
 * server deliberately does not join a name onto every value (`backend/internal/auth/
 * directory.go`) — a patient's timeline is hundreds of values written by a dozen people, and
 * copying the same twelve names into every payload all day is both waste and a join every
 * future clinical read has to remember. So the name is resolved here, once per session,
 * against `GET /v1/directory`.
 *
 * That choice has a consequence this file is responsible for: **the directory can be absent.**
 * A tablet that lost the connection between sign-in and the first patient has ids and no
 * names. `NO_DIRECTORY` is a first-class value and every reading is defined against it —
 * because attribution that blanks out the moment a lookup fails is attribution a reviewer
 * learns not to trust, and the role, the station, the time and the source are all still on the
 * value itself and all still worth saying.
 *
 * # Nothing here ever renders a uuid, and nothing here ever renders a blank
 *
 * `personOf` walks four answers in order: the name the payload already carried, the name the
 * directory holds, the staff code, the role. Only when all four are empty does it hand back a
 * message key, and the sentence behind that key says the tablet cannot name the person —
 * which is a different statement from an empty line, and the difference is whether the reader
 * goes and asks somebody. A raw uuid would be worse than either: it is unreadable, it looks
 * like a defect, and it is the one form of the answer that cannot be spoken out loud.
 *
 * # A departed colleague is named, and said to be departed
 *
 * The directory lists deactivated staff and retired devices on purpose. Most of what a
 * reviewer asks about is a value from months ago, and a directory of current staff only would
 * render a blank for exactly the person the question is about. `standing` carries the
 * directory's own word so the screen can say *no longer at the clinic* rather than sending
 * somebody down a corridor after a colleague who has left.
 *
 * # Source is a word, never only a tint (criterion 3)
 *
 * A number the machine read off a photograph of paper and a number an operator measured on a
 * calibrated scale are different evidence, and the difference has to survive a greyscale
 * screenshot, direct sun on a tablet, and the roughly one man in twelve who will work here and
 * cannot rely on colour. So `sourceReading` always produces a **word**; the tone is decoration
 * on top of it. It also has a value for *no source recorded*, and that is not folded into
 * `STATION`: absence reading as "an operator typed this" is the safe-looking lie this
 * criterion exists to prevent.
 *
 * # Corrections are rendered from what the payload actually carries (criterion 2)
 *
 * Four different things a record can say about a value having been changed, and they are not
 * interchangeable. A history item names its amender (`amended_by`, `amended_at`) and so both
 * people appear. A counselling tick names whoever took it back. An observation says only that
 * it was `CORRECTED` (it was wrong) or `SUPERSEDED` (it was right and re-measured) and points
 * at the row that replaced it — the person who made that correction is the *replacement's*
 * author, and this record does not contain them. So the reading says the value was corrected
 * and does not name a corrector, because inventing one is how a name ends up beside a change
 * that person did not make. CP62 builds the chain that fetches the other row.
 */

// --- what the contract gives us ---

export type Directory = components['schemas']['Directory'];
export type DirectoryPerson = components['schemas']['DirectoryPerson'];
export type DirectoryDevice = components['schemas']['DirectoryDevice'];
export type DirectoryStation = components['schemas']['DirectoryStation'];

/** The interface language. Local rather than imported: this file must stay renderer-free. */
export type Locale = 'en' | 'bn';

/**
 * Who is reading, in which language, on which day.
 *
 * `me` is what keeps somebody else's entry from reading as yours; an empty `me` means the
 * session store has not answered yet, and an unknown reader is never "you".
 *
 * `today` is the clinic day as `YYYY-MM-DD`, supplied by the caller rather than read from the
 * clock in here. It decides one thing: whether the collapsed line shows a time or a date. A
 * value entered last March showing "11:02" and nothing else is a value a reviewer reads as
 * this morning's, which is the single cheapest way for this component to mislead somebody.
 */
export interface Reader {
  locale: Locale;
  me: string;
  today: string;
}

// --- the clinic's clock ---

/**
 * Bangladesh Standard Time, as minutes east of UTC.
 *
 * A second copy of the number `features/counseling/state.ts` holds, and deliberately a copy
 * rather than an import: attribution is rendered on every screen in the application, and
 * importing it from the counselling feature would drag seventeen hundred lines of checklist
 * logic into every station's bundle to obtain one integer. The seam is a test —
 * `attribution.test.ts` asserts this constant equals the counselling module's *and* agrees
 * with `Asia/Dhaka` — so the two statements of the clinic's clock cannot drift apart quietly.
 *
 * A fixed offset rather than `Intl.DateTimeFormat` with the zone name, for the reason recorded
 * beside the other copy: Hermes ships a cut-down ICU on some builds, and a timestamp that
 * silently renders in the tablet's own zone is one record two people read as two different
 * times. Bangladesh has kept a single offset with no daylight saving since 2009, so the
 * arithmetic here is exact. **If that ever changes**, every time on every attribution is wrong
 * by an hour for part of the year, nothing throws, and nothing looks broken.
 */
export const CLINIC_UTC_OFFSET_MINUTES = 360;

function inClinicTime(iso: string): Date | null {
  const parsed = Date.parse(iso);
  if (Number.isNaN(parsed)) return null;
  return new Date(parsed + CLINIC_UTC_OFFSET_MINUTES * 60_000);
}

/**
 * A timestamp as a clock time, in the clinic's hours.
 *
 * An unparseable timestamp returns an empty string rather than "Invalid Date". A clinical row
 * that says a thing happened at Invalid Date is worse than one that leaves the time out: the
 * first is read as data.
 */
export function clockTime(iso: string): string {
  const local = inClinicTime(iso);
  if (local === null) return '';
  const hours = String(local.getUTCHours()).padStart(2, '0');
  const minutes = String(local.getUTCMinutes()).padStart(2, '0');
  return `${hours}:${minutes}`;
}

/**
 * A timestamp as the clinic's calendar day, `YYYY-MM-DD`.
 *
 * Digits in one order, in both interfaces. A month name would have to be translated, and a
 * numeric order that changes with the language — 03/04 meaning two different days to two
 * people looking at the same record — is exactly the ambiguity a clinical date cannot afford.
 */
export function clinicDay(iso: string): string {
  const local = inClinicTime(iso);
  if (local === null) return '';
  const year = String(local.getUTCFullYear()).padStart(4, '0');
  const month = String(local.getUTCMonth() + 1).padStart(2, '0');
  const day = String(local.getUTCDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

// --- what kind of evidence a value is ---

/**
 * The five sources the contract enumerates, in the contract's order.
 *
 * A list rather than a bare union because the message files are checked against it: a source
 * added to the contract without a sentence written for it fails a test here, rather than
 * arriving on a tablet as a bare identifier where the explanation belongs.
 */
export const SOURCES = ['STATION', 'OCR', 'FIELD', 'DEVICE', 'PATIENT'] as const;
export type SourceCode = (typeof SOURCES)[number];

/**
 * How a source is drawn, in the design tokens' own vocabulary.
 *
 * `plain` is a value an operator typed at a station on this system. `transcribed` is anything
 * that arrived some other way — the machine reading a photograph of paper, a field worker's
 * tablet, an instrument, the patient's own account. `unrecorded` is a value whose payload does
 * not say, which is neither of the other two and must not be drawn as either.
 *
 * Nothing is carried by the tone. Every source is also a word — see this module's own note.
 */
export type SourceTone = 'plain' | 'transcribed' | 'unrecorded';

export interface SourceReading {
  /** The code as the payload sent it, whatever it was. */
  code: string;
  /** False for a code this build has never heard of, and for no code at all. */
  known: boolean;
  /** Criterion 3, as a boolean the component cannot fudge. */
  ocr: boolean;
  tone: SourceTone;
  /** The word. Null only when the code is unknown, where the code itself is the word. */
  key: string | null;
  /** A sentence saying what this kind of evidence is, for the reveal. Null when unknown. */
  meaningKey: string | null;
}

export function sourceKnown(code: string): boolean {
  return (SOURCES as readonly string[]).includes(code);
}

export function sourceReading(raw: string): SourceReading {
  const code = (raw ?? '').trim().toUpperCase();

  if (code === '') {
    // Not folded into STATION. A value whose payload forgot to say where it came from is not
    // evidence that an operator typed it, and drawing it as though it were is the one mistake
    // on this screen that makes a transcribed number look measured.
    return {
      code: '',
      known: false,
      ocr: false,
      tone: 'unrecorded',
      key: 'source.unrecorded',
      meaningKey: 'sourceMeaning.unrecorded',
    };
  }

  if (!sourceKnown(code)) {
    // A tablet a version behind the server. The server's own word is shown rather than hidden
    // — it is readable, and hiding it would leave the value looking station-entered.
    return { code, known: false, ocr: false, tone: 'unrecorded', key: null, meaningKey: null };
  }

  return {
    code,
    known: true,
    ocr: code === 'OCR',
    tone: code === 'STATION' ? 'plain' : 'transcribed',
    key: `source.${code}`,
    meaningKey: `sourceMeaning.${code}`,
  };
}

// --- the roles this build can name ---

/**
 * The role codes this build has a word for.
 *
 * A copy of the keys under `role.codes` in the message files, and the test asserts it is
 * exactly that set in both languages. It exists so that `roleOf` can decide *here* whether a
 * role has a sentence, instead of the component asking `use-intl` for a key that may not exist
 * and drawing the literal string `role.codes.SOMETHING` at a reviewer.
 *
 * A role this build has never heard of is shown as its own code — `RX_EDUCATOR` is ugly and it
 * is readable, and a blank where the role belongs is what turns an attribution with no name
 * into an attribution with nothing at all.
 */
export const KNOWN_ROLE_CODES: readonly string[] = [
  'REGISTRATION',
  'ANTHROPOMETRY',
  'COUNSELOR',
  'HISTORY',
  'CLINICAL_ASSISTANT',
  'JUNIOR_DOCTOR',
  'RECORDS',
  'NUTRITIONIST',
  'EXERCISE',
  'PHYSICIAN',
  'QA',
  'RX_EDUCATOR',
  'PHARMACIST',
  'CRM',
  'RESEARCHER',
  'HR',
  'ADMIN',
  'FIELD_WORKER',
];

export interface RoleReading {
  /** The code as the value carried it. Empty when the payload named no role. */
  code: string;
  /** `codes.X` inside the `role` namespace, or null when this build cannot name it. */
  key: string | null;
}

export function roleOf(raw: string): RoleReading {
  const code = (raw ?? '').trim();
  if (code === '') return { code: '', key: null };
  return { code, key: KNOWN_ROLE_CODES.includes(code) ? `codes.${code}` : null };
}

// --- the directory, indexed ---

export interface DirectoryIndex {
  staff: ReadonlyMap<string, DirectoryPerson>;
  devices: ReadonlyMap<string, DirectoryDevice>;
  stations: ReadonlyMap<string, DirectoryStation>;
  /** When the directory was read, so a screen can say how old its answer is. */
  asOf: string;
  /** False for a directory that has not arrived, or could not be read. */
  loaded: boolean;
}

/**
 * The absence of a directory, as a value.
 *
 * Named and exported rather than left as a null check at every call site, because the failure
 * this guards against is a screen that renders nothing at all when one request did not come
 * back. Every function below is defined against this, and the tests run the whole reading
 * against it.
 */
export const NO_DIRECTORY: DirectoryIndex = {
  staff: new Map(),
  devices: new Map(),
  stations: new Map(),
  asOf: '',
  loaded: false,
};

export function indexDirectory(directory: Directory | null | undefined): DirectoryIndex {
  if (directory === null || directory === undefined) return NO_DIRECTORY;
  const staff = new Map<string, DirectoryPerson>();
  for (const person of directory.staff ?? []) staff.set(person.id, person);
  const devices = new Map<string, DirectoryDevice>();
  for (const device of directory.devices ?? []) devices.set(device.id, device);
  const stations = new Map<string, DirectoryStation>();
  for (const station of directory.stations ?? []) stations.set(station.code, station);
  return { staff, devices, stations, asOf: (directory.as_of ?? '').trim(), loaded: true };
}

// --- one clinical value's provenance, whatever shape it arrived in ---

/** Names a payload already resolved server-side, where it did. */
export interface Named {
  code: string;
  nameEN: string;
  nameBN: string;
}

export type CorrectionKind = 'amended' | 'withdrawn' | 'corrected' | 'superseded';

/** Evidence of a change that the payload actually carries. Never inferred. */
export interface RawCorrection {
  kind: CorrectionKind;
  /** The corrector's staff id, or '' where the payload names no person. */
  by: string;
  named: Named | null;
  role: string;
  at: string;
  /** The value that took this one's place, where the payload names it. */
  replacedBy: string;
}

/**
 * Everything the app knows about where one clinical value came from.
 *
 * One shape for every payload, so that the component takes one prop and every screen adopts
 * the same thing. The `of*` functions below are the only way to build one from a contract
 * type, which is what stops a screen assembling a provenance out of whichever fields were
 * nearest — the failure being a value attributed to whoever *confirmed* it rather than
 * whoever entered it.
 */
export interface Provenance {
  by: string;
  role: string;
  at: string;
  station: string;
  device: string;
  source: string;
  named: Named | null;
  correction: RawCorrection | null;
}

const NOTHING: Provenance = {
  by: '',
  role: '',
  at: '',
  station: '',
  device: '',
  source: '',
  named: null,
  correction: null,
};

function text(value: string | null | undefined): string {
  return (value ?? '').trim();
}

function namedOf(
  code: string | undefined,
  nameEN: string | undefined,
  nameBN: string | undefined,
): Named | null {
  const named = { code: text(code), nameEN: text(nameEN), nameBN: text(nameBN) };
  if (named.code === '' && named.nameEN === '' && named.nameBN === '') return null;
  return named;
}

/**
 * The provenance fields an observation carries.
 *
 * Structural rather than the generated `Observation`, so that a screen holding a narrower row
 * — the lifestyle answers station 4 shows, the previous measurement a station compares
 * against — can pass what it has without inventing the rest of an observation.
 */
export interface ObservationProvenance {
  code?: string | null;
  /**
   * When the value was *true*. `recorded_at` is when it was written down.
   *
   * Nullable as well as optional because that is how the observation rows reach the screens:
   * a shape that refused null here would push a cast into every call site, and a cast is where
   * a wrong field quietly becomes an accepted one.
   */
  effective_at?: string | null;
  recorded_by?: string;
  recorded_role?: string;
  recorded_at?: string;
  station_code?: string;
  device_id?: string;
  source?: string;
  status?: string;
  replaced_by?: string;
}

/**
 * The observation an on-screen number was computed from, or nothing.
 *
 * Some screens are handed a value the server derived — a growth percentile, a risk category —
 * together with the code and the moment of the measurement behind it, but not the measurement's
 * row. Finding that row is what lets the screen name the person who took it.
 *
 * **Both the code and the moment must match.** Matching on the code alone would take the
 * *newest* row of that code, which is very often not the one the server used, and an
 * attribution pointing at the wrong measurement is worse than none: it puts a colleague's name
 * against a number they did not take. A caller with no moment to match on gets nothing back,
 * and the screen says the record does not name an author — which is the truth.
 */
export function observationFor(
  rows: readonly ObservationProvenance[] | undefined,
  wanted: { code: string; effective_at?: string | null },
): ObservationProvenance | null {
  const code = text(wanted.code);
  const at = text(wanted.effective_at);
  if (rows === undefined || code === '' || at === '') return null;
  const moment = Date.parse(at);
  if (Number.isNaN(moment)) return null;
  for (const row of rows) {
    if (text(row.code) !== code) continue;
    const rowAt = Date.parse(text(row.effective_at));
    // Compared as instants rather than as strings: the same moment reaches a client as
    // `...Z` from one endpoint and `...+00:00` from another, and a string comparison would
    // silently stop matching the day one of them changed.
    if (!Number.isNaN(rowAt) && rowAt === moment) return row;
  }
  return null;
}

export function ofObservation(row: ObservationProvenance | null | undefined): Provenance {
  if (row === null || row === undefined) return NOTHING;
  const status = text(row.status).toUpperCase();
  return {
    by: text(row.recorded_by),
    role: text(row.recorded_role),
    at: text(row.recorded_at),
    station: text(row.station_code),
    device: text(row.device_id),
    source: text(row.source),
    named: null,
    correction:
      status === 'CORRECTED' || status === 'SUPERSEDED'
        ? {
            // Deliberately no person. The corrector is the author of the row that *replaced*
            // this one, and this payload does not contain it. A name here would be the name of
            // whoever wrote the value being corrected, printed beside the correction.
            kind: status === 'CORRECTED' ? 'corrected' : 'superseded',
            by: '',
            named: null,
            role: '',
            at: '',
            replacedBy: text(row.replaced_by),
          }
        : null,
  };
}

export interface HistoryItemProvenance {
  recorded_by?: string;
  recorded_role?: string;
  recorded_at?: string;
  station_code?: string;
  device_id?: string;
  source?: string;
  amended_by?: string;
  amended_at?: string;
}

export function ofHistoryItem(item: HistoryItemProvenance | null | undefined): Provenance {
  if (item === null || item === undefined) return NOTHING;
  const amendedAt = text(item.amended_at);
  const amendedBy = text(item.amended_by);
  return {
    by: text(item.recorded_by),
    role: text(item.recorded_role),
    at: text(item.recorded_at),
    station: text(item.station_code),
    device: text(item.device_id),
    // Carried since the migration that added the column. Items recorded **before** it keep an
    // empty source, and that still reads as *source not recorded* rather than as a station
    // entry — an old row silently rendering as something an operator typed is the same failure
    // criterion 3 is about, and it is the one that would now be easy to make, because the
    // ordinary case has stopped being empty.
    source: text(item.source),
    named: null,
    correction:
      amendedAt === '' && amendedBy === ''
        ? null
        : {
            // Criterion 2, in the one place the payload can actually satisfy it: both people
            // are on the item, so both are shown.
            kind: 'amended',
            by: amendedBy,
            named: null,
            role: '',
            at: amendedAt,
            replacedBy: '',
          },
  };
}

export interface AllergyProvenance {
  recorded_by?: string;
  recorded_role?: string;
  recorded_at?: string;
  station_code?: string;
  device_id?: string;
  source?: string;
}

export function ofAllergy(allergy: AllergyProvenance | null | undefined): Provenance {
  if (allergy === null || allergy === undefined) return NOTHING;
  return {
    ...NOTHING,
    by: text(allergy.recorded_by),
    role: text(allergy.recorded_role),
    at: text(allergy.recorded_at),
    station: text(allergy.station_code),
    device: text(allergy.device_id),
    // An allergy read off a photograph of a paper the patient brought is a different warning
    // from one an officer took down with the patient in front of them, and a prescriber acting
    // on it should be able to see which. Empty on rows written before the column existed.
    source: text(allergy.source),
  };
}

export interface AssertionProvenance {
  asserted_by?: string;
  asserted_role?: string;
  asserted_at?: string;
  station_code?: string;
  device_id?: string;
  source?: string;
}

export function ofAllergyAssertion(assertion: AssertionProvenance | null | undefined): Provenance {
  if (assertion === null || assertion === undefined) return NOTHING;
  return {
    ...NOTHING,
    by: text(assertion.asserted_by),
    role: text(assertion.asserted_role),
    at: text(assertion.asserted_at),
    station: text(assertion.station_code),
    device: text(assertion.device_id),
    source: text(assertion.source),
  };
}

export interface TickProvenance {
  ticked_by?: string;
  ticked_role?: string;
  ticked_at?: string;
  station_code?: string;
  device_id?: string;
  ticked_by_code?: string;
  ticked_by_name_en?: string;
  ticked_by_name_bn?: string;
  undone_by?: string;
  undone_at?: string;
  undone_by_code?: string;
  undone_by_name_en?: string;
  undone_by_name_bn?: string;
}

/**
 * A counselling tick, whose names the server already resolved.
 *
 * The payload's own names are used ahead of the directory. Not a preference: they are joined
 * from the same staff record the directory is built from, they are present with no directory
 * at all, and §5.4's question — "who taught you this" — is asked of a person rather than of a
 * hat, so a tick is the one clinical value in the system that must never fall back to a role.
 *
 * An un-tick is a correction with a person on it, so it is read as one.
 */
export function ofCounselingTick(tick: TickProvenance | null | undefined): Provenance {
  if (tick === null || tick === undefined) return NOTHING;
  const undoneAt = text(tick.undone_at);
  const undoneBy = text(tick.undone_by);
  return {
    ...NOTHING,
    by: text(tick.ticked_by),
    role: text(tick.ticked_role),
    at: text(tick.ticked_at),
    // Which room's queue the counsellor was standing in, and which phone it was ticked on.
    // §5.2 walks three rooms and the same person may tick in two of them; the room is part of
    // the answer to "where was this covered".
    station: text(tick.station_code),
    device: text(tick.device_id),
    // No `source` on a tick, and none is claimed: a counselling item is covered by a person
    // talking to a patient, and there is no other way for one to arrive.
    named: namedOf(tick.ticked_by_code, tick.ticked_by_name_en, tick.ticked_by_name_bn),
    correction:
      undoneAt === '' && undoneBy === ''
        ? null
        : {
            kind: 'withdrawn',
            by: undoneBy,
            named: namedOf(tick.undone_by_code, tick.undone_by_name_en, tick.undone_by_name_bn),
            role: '',
            at: undoneAt,
            replacedBy: '',
          },
  };
}

export interface InstrumentResponseProvenance {
  recorded_by?: string;
  recorded_role?: string;
  recorded_at?: string;
  recorded_by_code?: string;
  recorded_by_name_en?: string;
  recorded_by_name_bn?: string;
  station_code?: string;
  device_id?: string;
  source?: string;
  status?: string;
}

/**
 * A questionnaire response, answered by one person at one moment (CP58).
 *
 * Its own extractor rather than `ofObservation` with the fields renamed, even though the two
 * payloads happen to spell their author the same way today. A response is not an observation —
 * it has a version, item rows and a licence behind it — and a shared extractor would make the
 * day either shape changes a day two payloads silently disagree about who answered.
 *
 * The payload's own names are used ahead of the directory where it carries them, for the reason
 * a counselling tick's are: they are joined from the same staff record the directory is built
 * from, and they are present with no directory at all.
 *
 * A superseded response is a **correction with no corrector named**, and that is deliberate: the
 * person who answered again is the author of the response that replaced this one, and this
 * payload does not carry it. A name here would be the name of whoever gave the *earlier*
 * answers, printed beside the note saying they were superseded.
 */
export function ofInstrumentResponse(
  response: InstrumentResponseProvenance | null | undefined,
): Provenance {
  if (response === null || response === undefined) return NOTHING;
  const status = text(response.status).toUpperCase();
  return {
    ...NOTHING,
    by: text(response.recorded_by),
    role: text(response.recorded_role),
    at: text(response.recorded_at),
    station: text(response.station_code),
    device: text(response.device_id),
    source: text(response.source),
    named: namedOf(
      response.recorded_by_code,
      response.recorded_by_name_en,
      response.recorded_by_name_bn,
    ),
    correction:
      status === 'SUPERSEDED'
        ? { kind: 'superseded', by: '', named: null, role: '', at: '', replacedBy: '' }
        : null,
  };
}

export interface DietEntryProvenance {
  recorded_by?: string;
  recorded_role?: string;
  recorded_at?: string;
  recorded_by_code?: string;
  recorded_by_name_en?: string;
  recorded_by_name_bn?: string;
  station_code?: string;
  device_id?: string;
  source?: string;
  withdrawn_at?: string;
  withdrawn_by?: string;
  withdrawn_by_code?: string;
  withdrawn_by_name_en?: string;
  withdrawn_by_name_bn?: string;
}

/**
 * One thing a patient said they ate, whose names the server already resolved (CP59).
 *
 * Its own extractor rather than `ofObservation` with the fields renamed, for the reason a
 * counselling tick has one: a diet entry is not an observation — it is never edited, never
 * superseded and never corrected, only withdrawn — and a shared extractor would make the day
 * either shape changes a day two payloads silently disagree about who recorded what.
 *
 * This is criterion 2's other half made visible. Two assistants build one recall from two
 * tablets, and a recall two people contributed to is only useful if you can tell which half is
 * whose — so the payload's own names are used ahead of the directory, exactly as a tick's are:
 * they are joined from the same staff record the directory is built from, and they are present
 * with no directory at all.
 *
 * A withdrawal is a **correction with a person on it**, and it is read as one — both halves of it:
 * `withdrawn_by` is the staff id and `withdrawn_by_*` the resolved names, so the reveal can say
 * "you took this back" the same way it says "you recorded this". The payload carried only the
 * names for one checkpoint, which meant the one row at this station most likely to have been
 * written by somebody else was the one row that could not tell you whether it was you.
 */
export function ofDietEntry(entry: DietEntryProvenance | null | undefined): Provenance {
  if (entry === null || entry === undefined) return NOTHING;
  const withdrawnAt = text(entry.withdrawn_at);
  const withdrawnById = text(entry.withdrawn_by);
  const withdrawnBy = namedOf(
    entry.withdrawn_by_code,
    entry.withdrawn_by_name_en,
    entry.withdrawn_by_name_bn,
  );
  return {
    ...NOTHING,
    by: text(entry.recorded_by),
    role: text(entry.recorded_role),
    at: text(entry.recorded_at),
    // Which room and which tablet. Two assistants entering one recall are at two devices, and
    // "which of us recorded this" is answered by the device as often as by the name.
    station: text(entry.station_code),
    device: text(entry.device_id),
    source: text(entry.source),
    named: namedOf(entry.recorded_by_code, entry.recorded_by_name_en, entry.recorded_by_name_bn),
    correction:
      withdrawnAt === '' && withdrawnById === '' && withdrawnBy === null
        ? null
        : {
            kind: 'withdrawn',
            // The withdrawer's own id, never the recorder's: at this station they are routinely
            // two different people, and the recorder's id here would name the wrong one.
            by: text(entry.withdrawn_by),
            named: withdrawnBy,
            role: '',
            at: withdrawnAt,
            replacedBy: '',
          },
  };
}

export interface AlertProvenance {
  raised_by?: string;
  raised_role?: string;
  raised_at?: string;
  station_code?: string;
}

/**
 * A critical value, as the alarm screen carries it.
 *
 * The alert payload has no `source` and no `device_id`, and that is a decision rather than an
 * omission: an alert's number is a **snapshot** of an observation, and a provenance copied
 * beside a copied value can drift from the original — the observation gets corrected and the
 * alert goes on claiming the old evidence. The alert carries `observation_id`, and following
 * that link is the honest fix; it belongs with CP62's correction chain.
 *
 * What that costs here, plainly, and it is not nothing: a critical value the machine read off
 * a photograph of paper is one somebody should check against the paper before acting on it,
 * and this screen cannot yet say so. It says *source not recorded*, which is true, rather than
 * assuming `STATION`, which would be the reassuring version of not knowing.
 */
export function ofCriticalAlert(alert: AlertProvenance | null | undefined): Provenance {
  if (alert === null || alert === undefined) return NOTHING;
  return {
    ...NOTHING,
    by: text(alert.raised_by),
    role: text(alert.raised_role),
    at: text(alert.raised_at),
    station: text(alert.station_code),
  };
}

// --- who, resolved ---

export type PersonKind = 'named' | 'coded' | 'role' | 'nobody';

export interface PersonReading {
  kind: PersonKind;
  /** The words to draw. Empty for `role` and `nobody`, where `key` says what to draw. */
  text: string;
  /** A message key, or null when `text` carries the words. Never both empty. */
  key: string | null;
  /** The staff code, from the payload or the directory. What a reviewer asks by. */
  code: string;
  /** The directory's own word: active, suspended, deactivated — or '' when unknown. */
  standing: string;
  /** Message key for that word, or null when the directory did not say. */
  standingKey: string | null;
  /** True only for `deactivated`: this person is no longer at the clinic. */
  departed: boolean;
  /** True only when the reader's id and the author's id are both known and equal. */
  mine: boolean;
}

/** A person's name in the reader's language, falling back to the other spelling. */
function nameIn(named: { nameEN: string; nameBN: string }, locale: Locale): string {
  const own = locale === 'bn' ? named.nameBN : named.nameEN;
  if (own !== '') return own;
  return locale === 'bn' ? named.nameEN : named.nameBN;
}

/**
 * Who entered this, in the best answer available.
 *
 * Four answers in order — the name the payload carried, the name the directory holds, the
 * staff code, the role — and a fifth that is a sentence rather than a blank. The order is not
 * arbitrary: a payload that already resolved the name did so from the same staff record the
 * directory is built from and is available with no directory at all, and a staff code
 * identifies a person where a role only identifies a hat.
 *
 * The uuid is never an answer. It cannot be read aloud, it looks like a defect, and a reviewer
 * who was shown one would go and ask somebody rather than read it — which is the digging §4.2
 * exists to remove.
 */
export function personOf(
  id: string,
  role: string,
  named: Named | null,
  index: DirectoryIndex,
  reader: Reader,
): PersonReading {
  const actor = text(id);
  const me = text(reader.me);
  const mine = me !== '' && actor !== '' && actor === me;

  const listed = actor === '' ? undefined : index.staff.get(actor);
  const standing = text(listed?.status).toLowerCase();
  const standingKey =
    standing === 'suspended' || standing === 'deactivated' ? `standing.${standing}` : null;
  const code = text(named?.code) !== '' ? text(named?.code) : text(listed?.code);

  const common = {
    code,
    standing,
    standingKey,
    // Only `deactivated` is "no longer at the clinic". A suspended colleague is still here and
    // sending a reviewer away from them would be a different wrong answer.
    departed: standing === 'deactivated',
    mine,
  };

  const fromPayload = named === null ? '' : nameIn(named, reader.locale);
  if (fromPayload !== '') return { ...common, kind: 'named', text: fromPayload, key: null };

  if (listed !== undefined) {
    const fromDirectory = nameIn(
      { nameEN: text(listed.name_en), nameBN: text(listed.name_bn) },
      reader.locale,
    );
    if (fromDirectory !== '') return { ...common, kind: 'named', text: fromDirectory, key: null };
  }

  if (code !== '') return { ...common, kind: 'coded', text: code, key: null };

  // The hat is known and the person is not. Two different sentences, because "a counsellor
  // entered this" and "nothing on this value says who or what entered it" send a reviewer to
  // two different places, and only one of them is a place worth going.
  const hat = roleOf(role);
  if (hat.code !== '') return { ...common, kind: 'role', text: '', key: 'person.roleOnly' };

  return { ...common, kind: 'nobody', text: '', key: 'person.nobody' };
}

// --- when, where, on what ---

export interface WhenReading {
  /** The timestamp as it arrived, for anybody who needs the exact instant. */
  iso: string;
  /** `YYYY-MM-DD` in clinic time, or '' when unreadable. */
  date: string;
  /** `HH:MM` in clinic time, or '' when unreadable. */
  time: string;
  /** True when the value was entered on the reader's own clinic day. */
  today: boolean;
  known: boolean;
  /**
   * The words for the collapsed line: a time for today's entries, a date for older ones.
   *
   * A value entered last March showing "11:02" and nothing else is a value a reviewer reads as
   * this morning's. The exact time of an older value is still one tap away, in the reveal.
   */
  text: string;
  /** A message key when there are no words. Null when `text` carries them. */
  key: string | null;
}

export function whenOf(iso: string, today: string): WhenReading {
  const at = text(iso);
  const date = clinicDay(at);
  const time = clockTime(at);
  if (date === '') {
    return {
      iso: at,
      date: '',
      time: '',
      today: false,
      known: false,
      text: '',
      key: 'whenUnknown',
    };
  }
  const isToday = text(today) !== '' && date === text(today);
  return {
    iso: at,
    date,
    time,
    today: isToday,
    known: true,
    text: isToday ? time : date,
    key: null,
  };
}

export interface PlaceReading {
  /** The code as the value carried it. */
  code: string;
  /** The name where the directory has one, the code where it does not. Never empty. */
  text: string;
  /** True when the directory named it; false when this is the bare code. */
  named: boolean;
  /** For a device or station the directory says is retired, its own word. Null otherwise. */
  standingKey: string | null;
}

export function stationOf(
  code: string,
  index: DirectoryIndex,
  locale: Locale,
): PlaceReading | null {
  const wanted = text(code);
  if (wanted === '') return null;
  const listed = index.stations.get(wanted);
  if (listed === undefined) return { code: wanted, text: wanted, named: false, standingKey: null };
  const name = nameIn({ nameEN: text(listed.name_en), nameBN: text(listed.name_bn) }, locale);
  return {
    code: wanted,
    text: name === '' ? wanted : name,
    named: name !== '',
    standingKey: null,
  };
}

/**
 * Which tablet or phone the value was entered on.
 *
 * Every clinical payload this application reads back now carries `device_id`, so this resolves
 * rather than sitting empty. It is worth saying what a reviewer is actually asking when they
 * open this row: not "which serial number", but *is that tablet still in the building* — a
 * value taken on a phone that was revoked the week after is a value somebody may want to look
 * at twice. That is why the directory lists retired devices and why `standingKey` exists.
 *
 * **Null stays a real answer.** A record written on the web has no device, and drawing an
 * empty row for it would turn an honest absence into a field that looks broken. The screen
 * draws this row only when there is an id.
 */
export function deviceOf(id: string, index: DirectoryIndex): PlaceReading | null {
  const wanted = text(id);
  if (wanted === '') return null;
  const listed = index.devices.get(wanted);
  if (listed === undefined) return { code: wanted, text: wanted, named: false, standingKey: null };
  const standing = text(listed.status).toLowerCase();
  const name = text(listed.name);
  return {
    code: wanted,
    text: name === '' ? wanted : name,
    named: name !== '',
    // One sentence for every status that is not `active`, rather than a key per status. The
    // reviewer's question is only ever "can I still go and look at that tablet"; whether it was
    // suspended or revoked is an administrator's distinction and belongs on the device screen.
    standingKey: standing === '' || standing === 'active' ? null : 'deviceStanding.retired',
  };
}

// --- the whole reading ---

export interface CorrectionReading {
  kind: CorrectionKind;
  /**
   * The corrector, where the payload names one.
   *
   * Null for an observation, on purpose: what replaced it is another value, and this record
   * does not carry that value's author. Criterion 2 says show both people **where the payload
   * carries both**, and this is the case where it does not.
   */
  by: PersonReading | null;
  when: WhenReading;
  /** The id of the value that took this one's place, where the payload names it. */
  replacedBy: string;
  /** The sentence for what happened. Four kinds, four sentences, never interchangeable. */
  key: string;
}

export interface Reading {
  person: PersonReading;
  role: RoleReading;
  station: PlaceReading | null;
  device: PlaceReading | null;
  when: WhenReading;
  source: SourceReading;
  correction: CorrectionReading | null;
  /** True when the value is the reader's own entry. */
  mine: boolean;
  /**
   * True when the directory has not arrived. The reveal says so, because a reader who is not
   * told will read a missing name as "the record does not know" rather than "this tablet does
   * not know", and those send them to two different places.
   */
  directoryMissing: boolean;
  /** The collapsed line. The component fills {person}, {role} and {when} from this reading. */
  headline: { key: string };
}

function headlineKeyFor(person: PersonReading): string {
  if (person.mine) return 'headline.byYou';
  if (person.kind === 'named' || person.kind === 'coded') return 'headline.byPerson';
  if (person.kind === 'role') return 'headline.byRole';
  return 'headline.byNobody';
}

/**
 * One clinical value's attribution, entirely decided.
 *
 * Everything the component draws is in here and nothing is left for it to work out. That is
 * the whole discipline of this feature: the component is rendered on a device nobody can run
 * in a test, so if it decided which of two names to show, or whether a source counts as
 * transcribed, that decision would ship unchecked.
 */
export function readingOf(provenance: Provenance, index: DirectoryIndex, reader: Reader): Reading {
  const person = personOf(provenance.by, provenance.role, provenance.named, index, reader);
  const raw = provenance.correction;

  return {
    person,
    role: roleOf(provenance.role),
    station: stationOf(provenance.station, index, reader.locale),
    device: deviceOf(provenance.device, index),
    when: whenOf(provenance.at, reader.today),
    source: sourceReading(provenance.source),
    correction:
      raw === null
        ? null
        : {
            kind: raw.kind,
            by:
              raw.by === '' && raw.named === null
                ? null
                : personOf(raw.by, raw.role, raw.named, index, reader),
            when: whenOf(raw.at, reader.today),
            replacedBy: raw.replacedBy,
            key: `correction.${raw.kind}`,
          },
    mine: person.mine,
    directoryMissing: !index.loaded,
    headline: { key: headlineKeyFor(person) },
  };
}

/**
 * The null-tolerant form, for a screen whose value has not arrived yet.
 *
 * A row rendered while its payload is still loading must not crash and must not disappear: it
 * draws the same component saying nothing on this value names an author, which is what a value
 * with no provenance genuinely is. The alternative — omitting the component until the data
 * lands — is a screen whose attribution is sometimes absent, and criterion 4 is that it never
 * is.
 */
export function readingFor(
  provenance: Provenance | null | undefined,
  index: DirectoryIndex,
  reader: Reader,
): Reading {
  return readingOf(provenance ?? NOTHING, index, reader);
}

export interface ExerciseAssessmentProvenance {
  recorded_by?: string;
  recorded_role?: string;
  recorded_at?: string;
  recorded_by_code?: string;
  recorded_by_name_en?: string;
  recorded_by_name_bn?: string;
  station_code?: string;
  device_id?: string;
  source?: string;
  status?: string;
}

/**
 * What station 8 found, and who found it (CP60).
 *
 * A superseded assessment is a **correction with no corrector named**, exactly as a superseded
 * questionnaire response is: the person who asked the questions again is the author of the
 * assessment that replaced this one, and this payload does not carry them. A name here would be
 * the name of whoever took the *earlier* findings, printed beside the note saying they were
 * superseded — which is the specific misattribution this feature exists to prevent.
 *
 * Its own extractor rather than `ofObservation` with the fields renamed, for the reason a diet
 * entry has one: an assessment is not an observation. It is never edited and never corrected,
 * only superseded by a later one, and a shared extractor would make the day either shape changes
 * a day two payloads silently disagree about who recorded what.
 */
export function ofExerciseAssessment(
  assessment: ExerciseAssessmentProvenance | null | undefined,
): Provenance {
  if (assessment === null || assessment === undefined) return NOTHING;
  const status = text(assessment.status).toUpperCase();
  return {
    ...NOTHING,
    by: text(assessment.recorded_by),
    role: text(assessment.recorded_role),
    at: text(assessment.recorded_at),
    station: text(assessment.station_code),
    device: text(assessment.device_id),
    source: text(assessment.source),
    named: namedOf(
      assessment.recorded_by_code,
      assessment.recorded_by_name_en,
      assessment.recorded_by_name_bn,
    ),
    correction:
      status === 'SUPERSEDED'
        ? { kind: 'superseded', by: '', named: null, role: '', at: '', replacedBy: '' }
        : null,
  };
}

export interface ExercisePlanProvenance {
  issued_by?: string;
  issued_role?: string;
  issued_at?: string;
  issued_by_code?: string;
  issued_by_name_en?: string;
  issued_by_name_bn?: string;
  station_code?: string;
  device_id?: string;
  source?: string;
  status?: string;
}

/**
 * The routine a patient was handed, and who handed it over (CP60).
 *
 * `issued_*` rather than `recorded_*`, and it needs its own extractor for exactly that reason:
 * the findings and the plan are separate acts by possibly separate people — the specialist who
 * asked the questions is often not the one who chose the targets — so a plan attributed to
 * whoever took the assessment would name the wrong person on the sheet the patient took home.
 *
 * Superseded is read the same way an assessment's is, and with the same silence about who
 * replaced it: a follow-up plan is a new act by whoever issued *it*, and this payload does not
 * carry them.
 */
export function ofExercisePlan(plan: ExercisePlanProvenance | null | undefined): Provenance {
  if (plan === null || plan === undefined) return NOTHING;
  const status = text(plan.status).toUpperCase();
  return {
    ...NOTHING,
    by: text(plan.issued_by),
    role: text(plan.issued_role),
    at: text(plan.issued_at),
    station: text(plan.station_code),
    device: text(plan.device_id),
    source: text(plan.source),
    named: namedOf(plan.issued_by_code, plan.issued_by_name_en, plan.issued_by_name_bn),
    correction:
      status === 'SUPERSEDED'
        ? { kind: 'superseded', by: '', named: null, role: '', at: '', replacedBy: '' }
        : null,
  };
}

/** The empty provenance, for a value whose payload names nobody. Exported for tests. */
export const NO_PROVENANCE: Provenance = NOTHING;
