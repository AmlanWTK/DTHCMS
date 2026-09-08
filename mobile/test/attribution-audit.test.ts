import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

/**
 * Criterion 4: no screen renders a clinical value without the attribution component (CP61).
 *
 * # What this test actually is
 *
 * A static scan of `mobile/src`, and it is worth being exact about that, because a test that
 * cannot fail is worse than no test — it is a green tick that stops anybody looking. So the
 * first two cases below are **canaries**: the same checker is run against two hand-written
 * sources, one that renders a clinical payload with no attribution and one that renders it
 * with attribution, and the test asserts the checker separates them. If somebody weakens the
 * rule until it can no longer object to anything, those two fail before the real scan does.
 *
 * # What it can catch
 *
 *   - A screen that renders one of the clinical payload types this application reads back, or
 *     one of the two primitives that draw a clinical number, and does not render `EnteredBy`.
 *     That is the case criterion 4 is about, and it is the one that happens: a station is added
 *     at a later checkpoint and the attribution is the thing nobody remembers.
 *   - A screen that *stops* rendering it. Deleting the component from a station fails here.
 *   - An exemption that has gone stale — the file no longer exists, or no longer renders a
 *     value — so the list cannot silently grow into a place where things are parked.
 *   - An exemption with no reason written next to it.
 *   - The component being hollowed out: the reveal has to go on naming the person, the role,
 *     the station, the moment, the source and the correction, or this fails. "Adopted" and
 *     "adopted and then quietly reduced to a timestamp" are not the same thing.
 *   - A screen assembling a provenance by hand instead of using one of the feature's
 *     extractors — which is how a value ends up attributed to whoever *confirmed* it.
 *
 * # What it cannot catch, and these are real holes
 *
 *   - **A clinical value whose payload type is not on the list below, drawn without reading an
 *     author field off it.** A new endpoint with a new shape is invisible here until somebody
 *     adds its name. The author-field markers narrow this — a screen that prints a value and
 *     the moment somebody recorded it is caught whatever the type is called — but a screen that
 *     draws the number alone is not. This is the largest remaining hole and there is no way to
 *     close it with a scanner; the two lists are the thing a reviewer has to read.
 *   - **A value rendered as a bare string or number.** A screen handed a pre-formatted
 *     `"72.5 kg"` and told to draw it mentions no type and no primitive, and passes.
 *   - **Whether the component is reached at runtime.** It can sit inside a condition that is
 *     never true. Nothing here renders anything: React Native does not run in this container,
 *     which is why every decision was put in `state.ts` in the first place.
 *   - **Whether the attribution is anywhere near the value, legible, or one tap away.** That is
 *     judged by a physician holding the phone, and by the Maestro flow when D-59 names a
 *     device.
 *   - **Whether the provenance handed to the component is the right one for the value beside
 *     it.** Passing last month's row against this month's number would pass this test. The
 *     extractors are pinned by `attribution.test.ts`; the wiring is not pinned by anything but
 *     review.
 *   - **A screen under `src/app` that prints a value inline** rather than through a feature
 *     component. `station.tsx` is exempt for exactly that reason and the exemption is a promise
 *     a person has to keep.
 */

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const srcDir = join(root, 'src');

/**
 * The type names that only appear where a stored clinical value is being handled.
 *
 * The payload types this application reads back off the contract — each of which carries an
 * author, a station, a device and, on all but the counselling tick, a source — and the two
 * primitives that exist to draw a clinical number on a screen. A file that mentions either is
 * holding a value somebody entered. `AUTHOR_FIELDS` below is the second half of the rule.
 *
 * `DualUnitValue` and `MeasurementField` are on the list even though they are components
 * rather than payloads, and that is deliberate: they are the only two things in this
 * application whose entire purpose is to render a clinical number, so any future screen that
 * reaches for one is a future screen that owes a reviewer an attribution beside it.
 */
const CARRIERS = [
  'HistoryItem',
  'LifestyleRow',
  'Allergy',
  'AllergyAssertion',
  'CounselingTick',
  'CriticalAlert',
  'InstrumentResponse',
  'Observation',
  'PreviousValues',
  'PreviousMeasurements',
  'PreviousSources',
  'PreviousVital',
  'RiskObservation',
  'FootRisk',
  'CardPercentile',
  'GrowthPercentile',
  'WeightStatus',
  'DualUnitValue',
  'MeasurementField',
];

/**
 * The field names that only appear where a person's act is being read off a payload.
 *
 * A second class of marker, added once `device_id`, `station_code` and `source` landed on the
 * history, allergy and counselling payloads: a screen that reaches into a clinical row for one
 * of these is reading attribution, whatever it calls the type. No `.tsx` in this application
 * mentions one today — every screen goes through the feature's extractors — so this catches
 * nothing now and is here for the screen that one day prints a value beside a hand-read
 * `recorded_at` instead of drawing the component.
 *
 * `device_id` and `station_code` are deliberately **not** on this list. A device id belongs to
 * enrolment as much as to attribution, and a station code appears in a queue header, so both
 * would eventually flag a screen that is not clinical at all — and an exemption whose reason is
 * "this match is wrong" is how an audit list turns into noise. Everything here names a person
 * or the moment a person acted, and there is no non-clinical reason to read one.
 */
const AUTHOR_FIELDS = [
  'recorded_by',
  'recorded_at',
  'ticked_by',
  'ticked_at',
  'asserted_by',
  'asserted_at',
  'raised_by',
  'raised_at',
  'amended_by',
  'amended_at',
  'confirmed_by',
  'replaced_by',
];

const carriers = new RegExp(`\\b(${[...CARRIERS, ...AUTHOR_FIELDS].join('|')})`);

/** The component, as it appears in JSX. An import that is never rendered is not adoption. */
const rendersComponent = /<EnteredBy[\s/>]/;

/**
 * The files that render a clinical value and do not draw the component, each with the reason.
 *
 * The rule for adding to this list: **an exemption must say why the value on that screen has
 * no author to name, or where the attribution for it is drawn instead.** "It is awkward there"
 * is not a reason; it is an untested screen with a note on it.
 */
const EXEMPT: Record<string, string> = {
  'app/(station)/station.tsx':
    'Wires each station to its own screen and renders no clinical value itself: every value it ' +
    'holds is passed as a prop to a feature component that draws the attribution beside it. ' +
    'This is the exemption with the least mechanical support behind it — a value printed ' +
    'inline here would pass — so it is the one to check by eye when this file changes.',
  'components/DualUnitValue.tsx':
    'The primitive itself. It is handed a number and a unit and has no payload, no author and ' +
    'no way to look one up; the screen that places it supplies the attribution, and this test ' +
    'is what makes every such screen do so.',
  'components/MeasurementField.tsx':
    'The primitive itself, and the number in it is being typed rather than read back. A draft ' +
    'in an input has no author until it is saved, and putting a name on one would be an ' +
    'assertion nobody has made. The comparison line beside it is a stored value, and the two ' +
    'stations that draw one attribute it.',
  'features/sync/SyncItems.tsx':
    'Draws no stored clinical value at all: the list is entry kind, time, state and reason, ' +
    'deliberately without a measurement or a patient on it. The one number it renders is in a ' +
    'MeasurementField inside the correction sheet (CP67) — a value being retyped by the ' +
    'operator who recorded it, on an entry the clinic refused, which is therefore in no ' +
    'record and has no author to name. It is also the same person: an attribution chip here ' +
    'would print the reader their own name beside their own draft.',
};

interface Screen {
  path: string;
  source: string;
}

function screens(): Screen[] {
  const out: Screen[] = [];
  (function walk(dir: string) {
    for (const entry of readdirSync(dir)) {
      const path = join(dir, entry);
      if (statSync(path).isDirectory()) walk(path);
      else if (entry.endsWith('.tsx')) {
        out.push({
          path: relative(srcDir, path).split(sep).join('/'),
          source: readFileSync(path, 'utf8'),
        });
      }
    }
  })(srcDir);
  return out;
}

/** Does this source render a clinical value without the component? */
function offends(source: string): boolean {
  return carriers.test(source) && !rendersComponent.test(source);
}

const found = screens();

describe('the audit can tell the two cases apart', () => {
  /*
   * The canaries. Without these, a rule that had been weakened to the point of matching
   * nothing would still report a clean scan, which is the failure mode of every audit test
   * ever written.
   */
  it('objects to a screen that draws a clinical payload with no attribution', () => {
    const careless = `
      export function LabScreen({ rows }: { rows: Observation[] }) {
        return rows.map((row) => <AppText key={row.id}>{row.value}</AppText>);
      }
    `;
    expect(offends(careless)).toBe(true);
  });

  it('does not object once the component is rendered', () => {
    const careful = `
      export function LabScreen({ rows }: { rows: Observation[] }) {
        return rows.map((row) => (
          <View key={row.id}>
            <AppText>{row.value}</AppText>
            <EnteredBy compact provenance={ofObservation(row)} />
          </View>
        ));
      }
    `;
    expect(offends(careful)).toBe(false);
  });

  it('objects to a screen that reads an author field off a payload of its own', () => {
    // The case the type names cannot see: a screen handed some shape this list has never heard
    // of, printing a value and the moment somebody wrote it, with no attribution beside it.
    const inventive = `
      export function LabScreen({ rows }: { rows: { value: number; recorded_at: string }[] }) {
        return rows.map((row) => <AppText>{row.value} at {row.recorded_at}</AppText>);
      }
    `;
    expect(offends(inventive)).toBe(true);
  });

  it('is not fooled by an import that is never rendered', () => {
    // The commonest way this rule would rot: somebody adds the import to silence the audit and
    // never places the component. An import is not adoption.
    const pretending = `
      import { EnteredBy, ofObservation } from '@/features/attribution';
      export function LabScreen({ rows }: { rows: Observation[] }) {
        return rows.map((row) => <AppText key={row.id}>{row.value}</AppText>);
      }
    `;
    expect(offends(pretending)).toBe(true);
  });

  it('has screens to scan at all', () => {
    // A walk that silently found nothing would pass every case below.
    expect(found.length).toBeGreaterThan(10);
    expect(found.filter((screen) => carriers.test(screen.source)).length).toBeGreaterThan(6);
  });
});

describe('no screen renders a clinical value without the attribution component', () => {
  it('finds every screen adopting it, or exempt with a reason', () => {
    const offenders = found
      .filter((screen) => offends(screen.source))
      .map((screen) => screen.path)
      .filter((path) => !(path in EXEMPT));
    expect(
      offenders,
      `Renders a clinical value with no "entered by": ${offenders.join(', ')}`,
    ).toEqual([]);
  });

  it('has an actual reason written against every exemption', () => {
    for (const [path, reason] of Object.entries(EXEMPT)) {
      // Long enough that it has to be a sentence about that screen rather than a word.
      expect(reason.length, `${path} reason`).toBeGreaterThan(80);
    }
  });

  it('holds no exemption that has gone stale', () => {
    // An exemption for a file that no longer exists, or that no longer renders a value, or
    // that has since adopted the component. Left alone, this list is where screens get parked.
    const stale = Object.keys(EXEMPT).filter((path) => {
      const screen = found.find((candidate) => candidate.path === path);
      return screen === undefined || !offends(screen.source);
    });
    expect(stale, `Exempt for no reason any more: ${stale.join(', ')}`).toEqual([]);
  });

  it('has every station in the application on one side of the line', () => {
    // Named one by one rather than counted, so that a station quietly dropping out of the
    // scan — renamed, moved, or reduced to something the carrier list no longer recognises —
    // fails here rather than shrinking the denominator.
    const adopting = [
      'features/history/HistoryStation.tsx',
      'features/allergies/AllergyStep.tsx',
      'features/anthropometry/AnthropometryStation.tsx',
      'features/vitals/VitalsStation.tsx',
      'features/examination/ExaminationStation.tsx',
      'features/counseling/CounselingStation.tsx',
      'features/growth/PercentileCard.tsx',
      'features/alerts/CriticalAlertModal.tsx',
    ];
    for (const path of adopting) {
      const screen = found.find((candidate) => candidate.path === path);
      expect(screen, `${path} is missing`).toBeDefined();
      expect(rendersComponent.test(screen?.source ?? ''), `${path} draws it`).toBe(true);
    }
  });
});

describe('the attribution a screen draws is one the feature built', () => {
  it('never assembles a provenance at a call site', () => {
    // Every `provenance=` in the application comes out of an `of…` extractor. A screen that
    // built the object itself would be free to take `confirmed_by` for the author, or the
    // amender for the original — which is the whole failure this feature exists to prevent,
    // and it would look perfectly reasonable in a diff.
    const handmade: string[] = [];
    for (const screen of found) {
      for (const match of screen.source.matchAll(/provenance=\{([^\n]{0,20})/g)) {
        const opens = match[1] ?? '';
        if (!/^of[A-Z]/.test(opens)) handmade.push(`${screen.path}: provenance={${opens}`);
      }
    }
    expect(handmade, `Provenance built by hand: ${handmade.join(' | ')}`).toEqual([]);
  });
});

describe('the component still shows everything the reveal is for', () => {
  const component = readFileSync(join(srcDir, 'features/attribution/EnteredBy.tsx'), 'utf8');

  it('draws every part of the reading', () => {
    // Adoption is not worth much if the panel behind the tap has been reduced to a timestamp.
    // Criterion 1 asks for who, and §4.2 asks for it without digging; each of these is a thing
    // a reviewer opens the panel to find.
    for (const part of [
      'reading.person',
      'reading.role',
      'reading.station',
      'reading.device',
      'reading.when',
      'reading.source',
      'reading.correction',
      'reading.directoryMissing',
    ]) {
      expect(component.includes(part), `${part} is drawn`).toBe(true);
    }
  });

  it('opens on a press rather than on a hover, and says so to a screen reader', () => {
    // There is no hover on a phone. A tooltip here would be attribution nobody can reach.
    expect(component).toMatch(/onPress=/);
    expect(component).toMatch(/accessibilityRole="button"/);
    expect(component).toMatch(/accessibilityState=\{\{ expanded/);
  });

  it('sizes its target from the token rather than from a number', () => {
    // 48 is CP09's safety floor, not a style choice, and the compact variant takes the compact
    // token rather than inventing something smaller for a dense list — which is where a mis-tap
    // is most likely, not least.
    expect(component).toMatch(/theme\.size\.touchTarget\b/);
    expect(component).toMatch(/theme\.size\.touchTargetCompact\b/);
  });

  it('names the source in words and not only in a colour', () => {
    // Criterion 3 has to survive a greyscale screenshot and the roughly one man in twelve who
    // cannot rely on the colour. The chip draws `source.key` — a message — and the tone is
    // decoration on top of it.
    expect(component).toMatch(/reading\.source\.key/);
    expect(component).toMatch(/reading\.source\.code/);
  });
});
