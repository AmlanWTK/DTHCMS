import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import type { components } from '@dthcms/api-client';
import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import {
  ValueWithAttribution,
  alertAttribution,
  allergyAttribution,
  allergyChangeAttribution,
  assertionAttribution,
  buildLookup,
  historyItemAttribution,
  observationAttribution,
  type Directory,
} from '@/features/attribution';

import { renderWithProviders } from './render';

/**
 * Attribution everywhere (CP61, §4.2).
 *
 * §4.2 in full: *any reviewer sees who entered a value **instantly, without digging***. The
 * manual verification is a physician pointing at a number and asking who put it there. What
 * can be proven here is whether the interface makes that possible — and every way it fails
 * is quiet.
 *
 *  - **A uuid where a name should be.** The commonest failure and the one that looks like it
 *    is working: the field is populated, the screen renders it, and the answer is unreadable
 *    to everybody who needed it. There is a named test below whose whole job is that no
 *    screen in this application renders one.
 *  - **Hover only.** A reveal that works for a physician at a desk and refuses a keyboard
 *    user, a screen reader and — the one that matters here — a tablet, which is what the
 *    clinic's floor actually reads screens on. Three named tests.
 *  - **A directory failure taken as an answer.** "Nobody entered this" and "we could not
 *    read the names" are different sentences, and only one of them is safe to imply. A
 *    failure must not blank a value either: the record is the record, and the names are a
 *    convenience laid over it.
 *  - **OCR drawn like a station reading.** A number a scanner lifted off a photograph of a
 *    paper chart and a number an operator read off a calibrated scale are different
 *    evidence. The distinction has to survive greyscale, a tablet in sunlight and a
 *    photograph of the screen, which means it is a word before it is anything else.
 *  - **A correction that hides its author, or hides the original one.** Both people are on
 *    the record and both must be on the screen; and where the contract names neither, the
 *    screen says so rather than leaving a gap that reads as a rendering fault.
 *  - **A panel that reads as an accusation.** A staff name is not PHI, but it is a person.
 *    This says who, never who is at fault.
 */

const RINA = '0190a8f2-0000-7000-8000-00000000aa01';
const KAMAL = '0190a8f2-0000-7000-8000-00000000aa02';
const STRANGER = '0190a8f2-0000-7000-8000-00000000aa99';
const TABLET = '0190a8f2-0000-7000-8000-00000000bb01';
const PATIENT = '0190a8f2-0000-7000-8000-0000000000a1';

/** A uuid anywhere on a screen. The thing no attribution surface may ever show. */
const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

function directory(over: Partial<Directory> = {}): Directory {
  return {
    staff: [
      {
        id: RINA,
        code: 'C001',
        name_en: 'Rina Akter',
        name_bn: 'রিনা আক্তার',
        status: 'active',
      },
      {
        // Somebody who has left. The server keeps them listed on purpose: most of what a
        // reviewer asks about is a value from weeks ago.
        id: KAMAL,
        code: 'A014',
        name_en: 'Kamal Hossain',
        name_bn: 'কামাল হোসেন',
        status: 'deactivated',
      },
    ],
    devices: [{ id: TABLET, name: 'Station 3 tablet', kind: 'tablet', status: 'active' }],
    stations: [
      {
        code: 'STN_ANTHROPOMETRY',
        name_en: 'Anthropometry',
        name_bn: 'দেহ পরিমাপ',
        sequence: 2,
      },
    ],
    as_of: '2026-09-01T00:00:00Z',
    ...over,
  };
}

describe('the directory lookup', () => {
  it('names somebody in the reader’s own language', () => {
    expect(buildLookup(directory(), 'en', 'ready').person(RINA)?.name).toBe('Rina Akter');
    expect(buildLookup(directory(), 'bn', 'ready').person(RINA)?.name).toBe('রিনা আক্তার');
  });

  it('keeps somebody who has left the clinic, and says that they have', () => {
    // A directory of current staff only would render a blank for exactly the person the
    // question is about: attribution on last March's value names whoever took it.
    const person = buildLookup(directory(), 'en', 'ready').person(KAMAL);
    expect(person?.name).toBe('Kamal Hossain');
    expect(person?.status).toBe('deactivated');
  });

  it('answers nothing — not the id — for somebody it has never heard of', () => {
    // Handing the uuid back would let a caller print it as if it were a name.
    expect(buildLookup(directory(), 'en', 'ready').person(STRANGER)).toBeNull();
  });

  it('names a station, and falls back to the code rather than to a blank', () => {
    const lookup = buildLookup(directory(), 'en', 'ready');
    expect(lookup.station('STN_ANTHROPOMETRY')).toBe('Anthropometry');
    // A code the directory has never seen is not this lookup's to invent. The component
    // renders the code, which is what the queue and the board call that room anyway.
    expect(lookup.station('STN_MADE_UP')).toBeNull();
  });

  it('answers every question without throwing when there is no directory at all', () => {
    const lookup = buildLookup(null, 'en', 'unavailable');
    expect(lookup.state).toBe('unavailable');
    expect(lookup.person(RINA)).toBeNull();
    expect(lookup.device(TABLET)).toBeNull();
    expect(lookup.station('STN_ANTHROPOMETRY')).toBeNull();
    expect(lookup.asOf).toBeNull();
  });
});

/** The shape a station-entered observation arrives in. */
type Observation = components['schemas']['Observation'];

function observation(over: Partial<Observation> = {}): Observation {
  return {
    id: 'obs-1',
    patient_id: PATIENT,
    code: 'BODY_WEIGHT',
    category: 'ANTHRO',
    value_type: 'numeric',
    value: 69.5,
    unit: 'kg',
    effective_at: '2026-09-01T03:05:00Z',
    recorded_at: '2026-09-01T03:20:00Z',
    source: 'STATION',
    status: 'ACTIVE',
    recorded_by: RINA,
    recorded_role: 'ANTHROPOMETRY',
    station_code: 'STN_ANTHROPOMETRY',
    device_id: TABLET,
    ...over,
  };
}

function renderValue(
  over: Partial<Observation> = {},
  options: Parameters<typeof renderWithProviders>[1] = {},
) {
  return renderWithProviders(
    <ValueWithAttribution attribution={observationAttribution(observation(over))} label="Weight">
      <span>69.5 kg</span>
    </ValueWithAttribution>,
    { directory: directory(), ...options },
  );
}

describe('one interaction, whoever is asking', () => {
  it('offers a control named after the value it is about', async () => {
    // A list of six values otherwise offers a screen reader six buttons all called "Who
    // entered this value", which is six ways of not answering.
    renderValue();
    expect(await screen.findByRole('button', { name: 'Who entered Weight' })).toBeInTheDocument();
  });

  it('describes the control with the panel, so a screen reader hears it on focus', async () => {
    /*
     * This is criterion 1 for somebody who cannot see the screen, and it is the reason the
     * panel is always in the DOM. `aria-describedby` pointing at an element React has not
     * rendered describes nothing, and the failure is silent: the attribution simply never
     * reaches the person who most needs it read aloud.
     */
    renderValue();
    const trigger = await screen.findByTestId('attribution-trigger');
    const panel = screen.getByTestId('attribution-panel');

    expect(trigger.getAttribute('aria-describedby')).toBe(panel.getAttribute('id'));
    expect(panel).toHaveTextContent('Rina Akter');
  });

  it('is reachable by the keyboard', async () => {
    const user = userEvent.setup();
    renderValue();
    const trigger = await screen.findByTestId('attribution-trigger');

    await user.tab();
    expect(trigger).toHaveFocus();
  });

  it('opens on a tap and closes on Escape, for a tablet with no hover at all', async () => {
    // The clinic reads screens on cheap Android tablets. A component whose only reveal was
    // hover would show nothing at all on the hardware the floor actually uses.
    const user = userEvent.setup();
    renderValue();

    const wrapper = await screen.findByTestId('value-attribution');
    expect(wrapper).toHaveAttribute('data-open', 'false');

    await user.click(screen.getByTestId('attribution-trigger'));
    expect(wrapper).toHaveAttribute('data-open', 'true');

    await user.keyboard('{Escape}');
    expect(wrapper).toHaveAttribute('data-open', 'false');
  });

  it('reveals on hover and on focus in the stylesheet, not only on a click', () => {
    /*
     * jsdom has no layout engine and no hover, so the reveal cannot be exercised here. What
     * can be read is the rule that produces it — the same discipline `styles.test.ts` uses —
     * and the thing worth catching is a stylesheet that lost `:focus-within`, which would
     * leave a keyboard user with a button that appears to do nothing.
     */
    const css = readFileSync(join(webRoot, 'src', 'styles', 'globals.css'), 'utf8');
    expect(css).toContain('.app-attrib:hover .app-attrib__panel');
    expect(css).toContain('.app-attrib:focus-within .app-attrib__panel');
    expect(css).toContain(".app-attrib[data-open='true'] .app-attrib__panel");
  });

  it('puts the name on screen without any interaction in the compact variant', async () => {
    // For a dense view — the allergy strip on every patient header — where a reviewer is
    // scanning rather than asking about one value.
    renderWithProviders(
      <ValueWithAttribution
        attribution={observationAttribution(observation())}
        label="Weight"
        variant="compact"
      >
        <span>69.5 kg</span>
      </ValueWithAttribution>,
      { directory: directory() },
    );

    expect(await screen.findByTestId('attribution-summary')).toHaveTextContent('Rina Akter');
  });
});

describe('who entered it', () => {
  it('names the person, with the role they were wearing beside them', async () => {
    // The role tells a counsellor from a nutritionist and does not tell two counsellors
    // apart. §4.2 is a question about a person, so the name leads and the role stays.
    renderValue();
    const person = await screen.findByTestId('attribution-person');
    expect(person).toHaveTextContent('Rina Akter');
    expect(person).toHaveTextContent('Anthropometry officer');
  });

  it('shows the staff code, which is what does not move when a name does', async () => {
    renderValue();
    expect(await screen.findByTestId('attribution-code')).toHaveTextContent('C001');
  });

  it('says when the author is no longer at the clinic', async () => {
    // So a reviewer is not sent down the corridor to ask somebody who has gone.
    renderValue({ recorded_by: KAMAL });
    expect(await screen.findByTestId('attribution-presence')).toHaveTextContent(
      'No longer at the clinic.',
    );
  });

  it('falls back to the role, and says why there is no name', async () => {
    renderValue({ recorded_by: STRANGER });

    expect(await screen.findByTestId('attribution-person')).toHaveTextContent(
      'Anthropometry officer',
    );
    expect(screen.getByTestId('attribution-aside')).toHaveTextContent(
      'This person is not in the staff directory.',
    );
  });

  it('never renders a uuid, whatever it could not resolve', async () => {
    /*
     * The named test this checkpoint rests on.
     *
     * A uuid answers a different question from the one being asked, it cannot be read
     * aloud, and a reviewer who copies one down has copied down something no colleague on
     * the floor can use. There is no fallback path in this component that reaches for one.
     */
    const { container } = renderValue({ recorded_by: STRANGER });
    await screen.findByTestId('attribution-person');

    expect(container.textContent ?? '').not.toMatch(UUID);
  });

  it('keeps the value on screen when the directory could not be read', async () => {
    /*
     * The record is the record; the names are a convenience laid over it. A screen that
     * blanked every value because one small request failed would have turned a cosmetic
     * failure into a clinical one.
     */
    renderWithProviders(
      <ValueWithAttribution
        attribution={observationAttribution(observation())}
        label="Weight"
        variant="compact"
      >
        <span>69.5 kg</span>
      </ValueWithAttribution>,
      { directory: null },
    );

    expect(screen.getByText('69.5 kg')).toBeInTheDocument();
    expect(screen.getByTestId('attribution-person')).toHaveTextContent('Anthropometry officer');
    expect(screen.getByTestId('attribution-station')).toHaveTextContent('STN_ANTHROPOMETRY');
  });

  it('says the record itself names nobody, when it names nobody', async () => {
    // Distinct from a name that could not be resolved. A growth percentile carries no
    // author at all, and pretending otherwise would hide a gap in the contract.
    renderWithProviders(
      <ValueWithAttribution attribution={{ effectiveAt: '2026-09-01T03:05:00Z' }}>
        <span>16.4 kg/m²</span>
      </ValueWithAttribution>,
      { directory: directory() },
    );

    expect(screen.getByTestId('attribution-person')).toHaveTextContent(
      'This record does not say who entered the value.',
    );
  });
});

describe('where and when', () => {
  it('names the station, and shows an unknown code as itself', async () => {
    renderValue();
    expect(await screen.findByTestId('attribution-station')).toHaveTextContent('Anthropometry');

    renderValue({ station_code: 'STN_MADE_UP' });
    expect(screen.getAllByTestId('attribution-station')[1]).toHaveTextContent('STN_MADE_UP');
  });

  it('separates when the value was true from when it was written down', async () => {
    // A reading taken at 09:05 and entered at 09:20 has both, and a reviewer asking whether
    // a glucose was before or after the insulin is asking about the first.
    renderValue();
    expect(await screen.findByTestId('attribution-effective')).toHaveTextContent('Measured');
    expect(screen.getByTestId('attribution-when')).toHaveTextContent('Written down');
  });

  it('collapses the two into one line when they are the same instant', async () => {
    renderValue({ effective_at: '2026-09-01T03:20:00Z' });
    await screen.findByTestId('attribution-when');
    expect(screen.queryByTestId('attribution-effective')).toBeNull();
  });

  it('says a timestamp could not be read rather than drawing an empty line', async () => {
    renderValue({ recorded_at: 'not a date', effective_at: 'not a date either' });
    const lines = await screen.findAllByText('The time on this record could not be read.');
    expect(lines.length).toBeGreaterThan(0);
  });

  it('names the tablet a value was typed on', async () => {
    // `device_id` was stored all along and reached the contract at CP61. A reviewer asking
    // which tablet produced an odd run of readings is asking a real question, and the answer
    // is a name from the directory rather than the uuid on the record.
    renderValue();
    expect(await screen.findByTestId('attribution-device')).toHaveTextContent('Station 3 tablet');
  });

  it('marks a device that has since been retired', async () => {
    // Retired devices stay in the directory for the same reason departed staff do: most of
    // what a reviewer asks about is a value from weeks ago.
    renderValue(
      {},
      {
        directory: directory({
          devices: [{ id: TABLET, name: 'Station 3 tablet', kind: 'tablet', status: 'retired' }],
        }),
      },
    );
    expect(await screen.findByTestId('attribution-device')).toHaveTextContent('since retired');
  });

  it('says nothing about a device on a value typed on the web', async () => {
    // There was no tablet, so there is no tablet to name. An honest absence rather than a
    // gap — and a line reading "device not recorded" beside every value a physician enters
    // at a desk would be noise standing in for a fact that is already true.
    renderValue({ device_id: undefined });
    await screen.findByTestId('attribution-person');
    expect(screen.queryByTestId('attribution-device')).toBeNull();
  });

  it('says nothing about a device the record left empty', async () => {
    // A row written before the migration carries an empty string rather than no field, and
    // the two mean the same thing about a device: nobody knows which one, and there is
    // nothing to name.
    renderValue({ device_id: '' });
    await screen.findByTestId('attribution-person');
    expect(screen.queryByTestId('attribution-device')).toBeNull();
  });
});

describe('what kind of evidence it is', () => {
  it('gives a station reading and a scanned one different words', async () => {
    /*
     * Criterion 3. A number a scanner lifted off a photograph of a paper chart and a number
     * an operator read off a calibrated scale are different evidence, and the difference is
     * a word before it is anything else: a tablet in sunlight flattens every hue, a clinic
     * printer has none, and roughly one man in twelve cannot use it.
     */
    const { unmount } = renderValue();
    expect(await screen.findByTestId('attribution-source')).toHaveTextContent('Station');
    unmount();

    renderValue({ source: 'OCR' });
    expect(await screen.findByTestId('attribution-source')).toHaveTextContent('Scanned');
    expect(screen.getByTestId('attribution-evidence')).toHaveTextContent(
      'Read by the scanner from a photograph of paper. Nobody typed it.',
    );
  });

  it('does not distinguish them by colour alone', () => {
    /*
     * The second signal is a shape, not a hue: a dashed rule under a machine-read value. The
     * word above is the primary one and this is what survives a photocopy — the check is on
     * the stylesheet because jsdom cannot compute either.
     */
    const css = readFileSync(join(webRoot, 'src', 'styles', 'globals.css'), 'utf8');
    const rule = css.slice(css.indexOf(".app-attrib[data-source='OCR']"));
    expect(rule.slice(0, rule.indexOf('}'))).toContain('border-block-end-style: dashed');
  });

  it('marks a value the machine read even before anybody interacts', async () => {
    // A distinction carried only inside the panel would be a distinction nobody sees while
    // scanning a list, which is when it matters.
    renderValue({ source: 'OCR' });
    const wrapper = await screen.findByTestId('value-attribution');
    expect(wrapper).toHaveAttribute('data-source', 'OCR');
    expect(within(wrapper).getByTestId('attribution-source')).toBeInTheDocument();
  });

  it('renders a source this build has never heard of as its own code', async () => {
    // A blank mark would make a value of unknown provenance look like an ordinary station
    // reading, which is the exact confusion this criterion exists to prevent.
    renderValue({ source: 'LABORATORY_FEED' as Observation['source'] });
    expect(await screen.findByTestId('attribution-source')).toHaveTextContent('LABORATORY_FEED');
  });

  it('says nothing about the source on a record that has no source field', async () => {
    // An allergy change line and a critical alert carry none. Inventing "Station" for one
    // would be a claim nobody made.
    renderWithProviders(
      <ValueWithAttribution attribution={{ recordedBy: RINA }}>
        <span>Penicillin</span>
      </ValueWithAttribution>,
      { directory: directory() },
    );
    expect(screen.queryByTestId('attribution-source')).toBeNull();
  });

  it('says "not recorded" on a record that has the field and left it empty', async () => {
    /*
     * The state CP61's migration created, and the one worth being careful about.
     *
     * `station_code`, `source` and `device_id` were added to four tables and backfilled from
     * the event envelope, so every row written before that comes back with an empty source.
     * A blank drawn as nothing is indistinguishable on screen from a value somebody typed at
     * a station — which is the whole confusion criterion 3 exists to prevent — so the empty
     * field gets a word of its own, and the panel says in as many words that it must not be
     * read as a station entry.
     */
    renderValue({ source: '' as Observation['source'] });

    expect(await screen.findByTestId('attribution-source')).toHaveTextContent(
      'Source not recorded',
    );
    expect(screen.getByTestId('value-attribution')).toHaveAttribute('data-source', 'unrecorded');
    expect(screen.getByTestId('attribution-evidence')).toHaveTextContent(
      'must not be read as a station entry',
    );
  });

  it('gives the unknown provenance a third shape, not a third colour', () => {
    // Solid for a station entry, dashed for one nobody typed, dotted for one nobody
    // recorded. Three shapes, so the distinction survives a monochrome printer and a
    // photograph of the screen.
    const css = readFileSync(join(webRoot, 'src', 'styles', 'globals.css'), 'utf8');
    const rule = css.slice(css.indexOf(".app-attrib[data-source='unrecorded']"));
    expect(rule.slice(0, rule.indexOf('}'))).toContain('border-block-end-style: dotted');
  });
});

type HistoryItem = components['schemas']['HistoryItem'];
type CriticalAlert = components['schemas']['CriticalAlert'];

describe('a corrected value shows both people', () => {
  it('names the original author and the person who amended it', async () => {
    /*
     * Criterion 2. A correction does not replace the person who entered the original — it
     * adds a second person to the record — and a panel that showed only the most recent one
     * would let a reviewer attribute the first author's value to whoever touched it last.
     */
    const item: HistoryItem = {
      id: 'item-1',
      patient_id: PATIENT,
      kind: 'COMPLAINT',
      status: 'ACTIVE',
      recorded_at: '2026-08-02T04:00:00Z',
      recorded_by: RINA,
      recorded_role: 'HISTORY',
      amended_at: '2026-08-09T06:00:00Z',
      amended_by: KAMAL,
    };

    renderWithProviders(
      <ValueWithAttribution attribution={historyItemAttribution(item)} label="Chest pain">
        <span>Burning chest pain</span>
      </ValueWithAttribution>,
      { directory: directory() },
    );

    // The heading changes, so the first name is not read as the current author.
    expect(screen.getByText('Originally entered by')).toBeInTheDocument();
    expect(screen.getByTestId('attribution-person')).toHaveTextContent('Rina Akter');

    const correction = screen.getByTestId('attribution-correction');
    expect(correction).toHaveTextContent('A detail of this record was changed later.');
    expect(within(correction).getByTestId('attribution-corrector')).toHaveTextContent(
      'Kamal Hossain',
    );
  });

  it('says plainly when the record does not name who corrected it', async () => {
    /*
     * An observation carries `status: CORRECTED` and sometimes the id of its replacement,
     * and no `corrected_by` at all. An empty line where a name should be reads as a
     * rendering fault; this is the fact, and it is the fact CP62 exists to fix.
     */
    renderValue({ status: 'CORRECTED', replaced_by: 'obs-2' });

    const correction = await screen.findByTestId('attribution-correction');
    expect(correction).toHaveTextContent('This value was corrected later.');
    expect(within(correction).getByTestId('attribution-corrector')).toHaveTextContent(
      'The record does not name who made the change.',
    );
    // And still no uuid, including the id of the value that replaced this one.
    expect(correction.textContent ?? '').not.toMatch(UUID);
  });

  it('names both halves of a withdrawn allergy', async () => {
    // Somebody recorded it, somebody else took it back, and the next clinician needs both.
    renderWithProviders(
      <ValueWithAttribution
        attribution={allergyChangeAttribution({
          kind: 'ALLERGY',
          id: 'change-1',
          at: '2026-08-02T04:00:00Z',
          by: RINA,
          by_role: 'HISTORY',
          undone_at: '2026-08-03T04:00:00Z',
          undone_by: KAMAL,
        })}
      >
        <span>the red syrup</span>
      </ValueWithAttribution>,
      { directory: directory() },
    );

    expect(screen.getByTestId('attribution-person')).toHaveTextContent('Rina Akter');
    expect(screen.getByTestId('attribution-corrector')).toHaveTextContent('Kamal Hossain');
  });

  it('does not draw a resolved history item as a correction', () => {
    // "She had this and no longer does" is a clinical fact about the patient, not a change
    // to who said what. Drawing it as one would put a colleague's name against a revision
    // nobody made.
    const resolved = historyItemAttribution({
      id: 'item-2',
      patient_id: PATIENT,
      kind: 'COMPLAINT',
      status: 'RESOLVED',
      recorded_at: '2026-08-02T04:00:00Z',
      recorded_by: RINA,
    });
    expect(resolved.correction).toBeUndefined();
  });

  it('does not draw whoever acknowledged an alert as its author or its corrector', () => {
    // Answering an alert is not recording a value.
    const alert: CriticalAlert = {
      id: 'alert-1',
      patient_id: PATIENT,
      observation_id: 'obs-1',
      code: 'SPO2',
      value: 88,
      breached: 'low',
      threshold: 92,
      raised_at: '2026-09-01T03:20:00Z',
      raised_by: RINA,
      raised_role: 'CLINICAL_ASSISTANT',
      station_code: 'STN_ANTHROPOMETRY',
      status: 'ACKNOWLEDGED',
      acknowledged_by: KAMAL,
      escalation_step: 1,
      delivered: true,
      recipients: 0,
    };

    const attribution = alertAttribution(alert);
    expect(attribution.recordedBy).toBe(RINA);
    expect(attribution.correction).toBeUndefined();
  });
});

describe('the adapters read the field each schema actually has', () => {
  it('maps an observation, device and source and all', () => {
    const mapped = observationAttribution(observation());
    expect(mapped).toMatchObject({
      recordedBy: RINA,
      recordedRole: 'ANTHROPOMETRY',
      stationCode: 'STN_ANTHROPOMETRY',
      deviceId: TABLET,
      recordedAt: '2026-09-01T03:20:00Z',
      effectiveAt: '2026-09-01T03:05:00Z',
      source: 'STATION',
    });
    expect(mapped.correction).toBeUndefined();
  });

  it('maps an allergy’s station, device and source, which it did not used to have', () => {
    // The fields arrived on this schema at CP61, and they are what puts criterion 3's
    // distinction on the one record a prescriber reads before writing.
    const mapped = allergyAttribution({
      id: 'allergy-1',
      patient_id: PATIENT,
      reaction: 'RASH',
      severity: 'mild',
      certainty: 'suspected',
      recorded_at: '2026-08-02T04:00:00Z',
      recorded_by: RINA,
      recorded_role: 'HISTORY',
      station_code: 'STN_HISTORY',
      device_id: TABLET,
      source: 'OCR',
    });
    expect(mapped).toMatchObject({
      recordedBy: RINA,
      stationCode: 'STN_HISTORY',
      deviceId: TABLET,
      source: 'OCR',
    });
    // Still no correction fields on the allergy itself, and none is invented: an allergy
    // somebody disagreed with is withdrawn, and the withdrawal is a change-history row.
    expect(mapped.correction).toBeUndefined();
  });

  it('reads a row written before the migration as empty rather than as a station entry', () => {
    // The columns are defaulted, so an older row comes back with empty strings. `null`
    // rather than `undefined`, because the record *has* the field — which is the difference
    // between "we do not know" and "this kind of record has no such thing".
    const mapped = allergyAttribution({
      id: 'allergy-2',
      patient_id: PATIENT,
      reaction: 'RASH',
      severity: 'mild',
      certainty: 'suspected',
      recorded_at: '2024-01-02T04:00:00Z',
      recorded_by: RINA,
      station_code: '',
      device_id: '',
      source: '',
    });
    expect(mapped.source).toBeNull();
    expect(mapped.stationCode).toBeUndefined();
    expect(mapped.deviceId).toBeUndefined();
  });

  it('maps a history item’s station, device and source beside both of its people', () => {
    const mapped = historyItemAttribution({
      id: 'item-3',
      patient_id: PATIENT,
      kind: 'MEDICATION',
      status: 'ACTIVE',
      recorded_at: '2026-08-02T04:00:00Z',
      recorded_by: RINA,
      recorded_role: 'HISTORY',
      station_code: 'STN_HISTORY',
      device_id: TABLET,
      source: 'OCR',
      amended_at: '2026-08-09T06:00:00Z',
      amended_by: KAMAL,
    });
    expect(mapped).toMatchObject({
      recordedBy: RINA,
      stationCode: 'STN_HISTORY',
      deviceId: TABLET,
      source: 'OCR',
    });
    expect(mapped.correction).toMatchObject({ kind: 'AMENDED', by: KAMAL });
  });

  it('maps an assertion, which is a claim with an author like any other', () => {
    const mapped = assertionAttribution({
      id: 'assertion-1',
      patient_id: PATIENT,
      kind: 'NO_KNOWN_ALLERGY',
      asserted_at: '2026-09-01T05:00:00Z',
      asserted_by: RINA,
      asserted_role: 'HISTORY',
      station_code: 'STN_HISTORY',
      device_id: TABLET,
      source: 'STATION',
    });
    expect(mapped).toMatchObject({
      recordedBy: RINA,
      recordedRole: 'HISTORY',
      stationCode: 'STN_HISTORY',
      deviceId: TABLET,
      source: 'STATION',
    });
  });

  it('asks an alert for no source, because its number is a copy of an observation', () => {
    // Where the number came from is a property of the observation, and the honest way to say
    // so is a link to it rather than a second copy of its provenance that can disagree with
    // the first. `observation_id` is on the schema; nothing follows it yet.
    const mapped = alertAttribution({
      id: 'alert-2',
      patient_id: PATIENT,
      observation_id: 'obs-1',
      code: 'SPO2',
      value: 88,
      breached: 'low',
      threshold: 92,
      raised_at: '2026-09-01T03:20:00Z',
      raised_by: RINA,
      status: 'OPEN',
      escalation_step: 1,
      delivered: true,
      recipients: 0,
    });
    expect(mapped.source).toBeUndefined();
  });
});

describe('it says who, never who is at fault', () => {
  it('has no word of blame in either language', () => {
    /*
     * A staff name is not PHI, but it is a person, and this panel is read over somebody's
     * shoulder on a bad morning. The wording is a decision, so it is a test: "Entered by",
     * "Corrected by", "No longer at the clinic" — and nothing that assigns a cause.
     */
    const blame = /\b(fault|blame|responsible|culprit|negligen|mistake|failed to)\b/i;
    const banglaBlame = /(দোষ|ত্রুটি|অবহেলা)/;

    const en = JSON.stringify(
      JSON.parse(readFileSync(join(webRoot, 'messages', 'en.json'), 'utf8')).attribution,
    );
    const bn = JSON.stringify(
      JSON.parse(readFileSync(join(webRoot, 'messages', 'bn.json'), 'utf8')).attribution,
    );

    expect(en).not.toMatch(blame);
    expect(bn).not.toMatch(banglaBlame);
  });

  it('offers nothing that changes a value', async () => {
    // One control on this surface, and it reveals. A panel that could correct a colleague's
    // value from behind a hover would be a way to alter a record without a word said.
    renderValue();
    const wrapper = await screen.findByTestId('value-attribution');
    const controls = within(wrapper).getAllByRole('button');
    expect(controls).toHaveLength(1);
    expect(controls[0]).toHaveAttribute('data-testid', 'attribution-trigger');
  });
});

/* ------------------------------------------------------------------------- */

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

/**
 * Criterion 4: **no screen renders a clinical value without the component.**
 *
 * Implemented as a build failure rather than as a review checklist item, for the reason
 * CP44's dual-unit audit gives: a checklist is followed on nine screens and forgotten on the
 * tenth, and the tenth is the one built next month.
 *
 * # What it actually checks
 *
 * Three signals that a `.tsx` file under `src` is showing a clinical value, any one of which
 * obliges it to go through `ValueWithAttribution`:
 *
 *   1. It imports one of the contract's clinical record types — `Observation`, `Allergy`,
 *      `HistoryItem`, `CriticalAlert`, a growth shape. This is the strongest of the three:
 *      a new screen that reads clinical data has to name its type somewhere.
 *   2. It names one of the fields that identify who entered a value — `recorded_by`,
 *      `asserted_by`, `raised_by`, `amended_by`. This catches the specific failure this
 *      checkpoint exists to end: a uuid printed on a screen.
 *   3. It renders `<DualUnitValue`, which by CP44's own audit is the only way a clinical
 *      measurement is drawn at all.
 *
 * # What it cannot catch, said plainly
 *
 * A test that cannot fail is worse than no test, so here is the honest boundary of this one.
 *
 *   - **It is a text scan, not a compiler.** It cannot tell a JSX interpolation from an
 *      object literal, so it errs towards flagging — which is why the exemption list below
 *      exists and why every entry on it carries a reason.
 *   - **It cannot see a value that arrives without a type.** A screen that fetched an
 *      observation into an untyped shape, or received one already formatted as a string from
 *      a helper, is invisible to all three signals.
 *   - **It cannot judge whether the attribution is *right*.** It proves the component is
 *      there, never that the ids handed to it belong to the value beside them. Only the
 *      per-screen tests in `allergies.test.tsx`, `history.test.tsx` and this file do that.
 *   - **It says nothing about the station app.** `mobile/` has its own surfaces and its own
 *      audit; this scan stops at `web/src`.
 *   - **It cannot see a value drawn as a picture.** `GrowthChart` plots points as circles in
 *      an SVG with no text on them, and is exempted below for that reason.
 *
 * The matchers are ordinary functions and there is a `describe` block below that feeds them
 * hand-written source strings, so that this audit is itself known to be able to fail.
 */

/** Contract types whose presence in a file's imports means the file handles clinical data. */
const CLINICAL_TYPES = [
  'Allergy',
  'AllergyAssertion',
  'AllergyChange',
  'AllergyState',
  'HistoryItem',
  'Observation',
  'CriticalAlert',
  'Growth',
  'GrowthPercentile',
  'GrowthPoint',
  'GrowthResponse',
];

/**
 * The fields on those records that identify a person or a device. Rendering one raw is the
 * failure this checkpoint exists to end — a uuid on a screen, answering a question nobody
 * asked.
 *
 * `device_id` joined the list when CP61's migration put it on four more schemas: it is a
 * uuid, it names where a value came from, and a screen printing it raw fails in exactly the
 * way `recorded_by` used to.
 *
 * `station_code` and `source` joined those same schemas and are deliberately **not** here.
 * Both words are used for something else elsewhere in this application — the queue board's
 * columns are stations, and a patient's date of birth has a `source` meaning which document
 * it came from — so adding them would flag two files that have nothing to do with
 * attribution, and an exemption list padded with false positives is a list nobody reads. A
 * screen rendering a clinical record's station or source raw necessarily imports that
 * record's type, which signal 1 already catches.
 */
const IDENTITY_FIELDS = [
  'recorded_by',
  'recorded_role',
  'asserted_by',
  'asserted_role',
  'raised_by',
  'raised_role',
  'amended_by',
  'confirmed_by',
  'undone_by',
  'ticked_by',
  'by_role',
  'device_id',
];

// Leading whitespace is tolerated on purpose. Every import in this repository sits at
// column zero, and a scan that quietly stopped matching because one file was indented
// would be an audit that had switched itself off — which the synthetic sources below
// found on their first run.
const IMPORT_STATEMENT = /^[ \t]*import[\s\S]*?;/gm;
const CLINICAL_TYPE = new RegExp(`\\b(${CLINICAL_TYPES.join('|')})\\b`);
const IDENTITY_FIELD = new RegExp(`\\{[^{}]*\\.(${IDENTITY_FIELDS.join('|')})\\b[^{}]*\\}`);

function importsAClinicalRecord(source: string): boolean {
  const imports = (source.match(IMPORT_STATEMENT) ?? []).join('\n');
  return CLINICAL_TYPE.test(imports);
}

function namesAnAuthorField(source: string): boolean {
  return IDENTITY_FIELD.test(source);
}

function drawsAMeasurement(source: string): boolean {
  return source.includes('<DualUnitValue');
}

/**
 * A fourth signal, added at CP62: rendering an observation through the feature's own component.
 *
 * `ObservationValue` is `DualUnitValue` nested inside `ValueWithAttribution`, plus the switch
 * on `value_type` that draws a text finding or a yes/no. It exists because CP62's chain view
 * draws the same value in four places, and four call sites each remembering to nest one
 * component inside the other is three chances to draw a number with nobody's name against it.
 *
 * It is listed here rather than left invisible to the scan: a file rendering `<ObservationValue`
 * **is** showing a clinical value, and this audit should say so. It counts as satisfying the
 * rule below for the same reason — the component is attributed by construction, and the file
 * that builds it is checked like every other.
 */
function drawsAnObservation(source: string): boolean {
  return source.includes('<ObservationValue');
}

function mustBeAttributed(source: string): boolean {
  return (
    importsAClinicalRecord(source) ||
    namesAnAuthorField(source) ||
    drawsAMeasurement(source) ||
    drawsAnObservation(source)
  );
}

function usesTheComponent(source: string): boolean {
  return source.includes('ValueWithAttribution') || drawsAnObservation(source);
}

/**
 * Files that handle clinical data and legitimately draw no value themselves.
 *
 * Same discipline as `i18n.test.ts`'s identical-strings list: an exemption is allowed, and
 * it has to be argued for in the file. An unexplained one is indistinguishable from a rule
 * somebody switched off. The staleness test below deletes the argument for any entry that no
 * longer trips the scan.
 */
const EXEMPT: Record<string, string> = {
  'src/features/history/components/MedicalHistory.tsx':
    'The station screen around the list. Every item is rendered by HistoryItemCard, which is attributed; this file draws headings, counts and an empty state.',
  'src/features/growth/components/GrowthScreen.tsx':
    'The card and the chart together. It draws neither — PercentileCard is attributed and GrowthChart is exempted below — and holds the indicator tabs.',
  'src/features/growth/components/GrowthChart.tsx':
    'The trajectory is one picture: role="img" with a label, and the points are circles with no text on them. There is nothing on this surface a reviewer can point at and no HTML disclosure can live inside the SVG. The same numbers are attributed on the card directly above it, and GrowthPoint carries no author to show anyway — see the note on PercentileCard.',
  'src/features/counseling/components/SessionItems.tsx':
    'CP57 attributes every counselling tick per item already, and does it from a read that carries the resolved names on the row itself rather than through the directory. It shares this checkpoint’s staffLabel and shows the name, the role, the staff code and both people on a withdrawn tick. Converting it would be a rewrite of a working surface to satisfy a scan. What it does not yet say is the two fields CP61’s migration added to CounselingTick — device_id and station_code — so a reviewer cannot ask which tablet a tick came from; the room is already on the row from the item itself, and the device is the real omission.',
};

/** Every .tsx under src, so the scan cannot miss a screen somebody added. */
function componentFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) {
      out.push(...componentFiles(path));
      continue;
    }
    if (entry.endsWith('.tsx')) out.push(path);
  }
  return out;
}

describe('no screen renders a clinical value without the component', () => {
  const files = componentFiles(join(webRoot, 'src')).map((path) => ({
    relative: path.slice(webRoot.length + 1),
    source: readFileSync(path, 'utf8'),
  }));

  it('scans the whole application', () => {
    expect(files.length).toBeGreaterThan(20);
  });

  const clinical = files.filter((file) => mustBeAttributed(file.source));

  it('finds screens that handle clinical values', () => {
    // Guards every assertion below from passing vacuously if the matchers were ever
    // narrowed to nothing — which is how an audit quietly stops auditing.
    expect(clinical.length).toBeGreaterThan(5);
  });

  for (const file of clinical) {
    if (file.relative in EXEMPT) continue;

    it(`${file.relative} attributes its values`, () => {
      expect(
        usesTheComponent(file.source),
        `${file.relative} shows a clinical value without ValueWithAttribution. §4.2 says any ` +
          `reviewer sees who entered a value instantly, without digging, and this component ` +
          `is the only thing in the application that answers that — with the person, the ` +
          `role, the station, the time and the kind of evidence, one interaction away. If ` +
          `this file genuinely draws no value, add it to EXEMPT with the reason.`,
      ).toBe(true);
    });
  }

  it('has no exemption that no longer applies', () => {
    // A stale exemption is a permission nobody is using and everybody trusts.
    const stale = Object.keys(EXEMPT).filter((relative) => {
      const file = files.find((candidate) => candidate.relative === relative);
      return file === undefined || !mustBeAttributed(file.source);
    });
    expect(
      stale,
      `Exempted but no longer flagged, so the entry should go: ${stale.join(', ')}`,
    ).toEqual([]);
  });
});

describe('the audit can actually fail', () => {
  /*
   * The matchers, fed source that does not exist in the repository. Without this block the
   * loop above is a list of assertions that happen to pass today and would keep passing if
   * a regular expression were quietly broken — which is the failure mode of every scan-based
   * test, and the reason this one is checked against a screen written to be wrong.
   */

  it('reports a screen that prints an author id straight into the markup', () => {
    const pretend = `
      import type { Observation } from '../api/observations';
      export function Vitals({ observation }: { observation: Observation }) {
        return <p>{observation.value} — {observation.recorded_by}</p>;
      }`;
    expect(namesAnAuthorField(pretend)).toBe(true);
    expect(mustBeAttributed(pretend)).toBe(true);
    expect(usesTheComponent(pretend)).toBe(false);
  });

  it('reports a screen that draws a measurement with no attribution', () => {
    const pretend = `
      export function WeightRow() {
        return <DualUnitValue value={69.5} unit="kg" />;
      }`;
    expect(drawsAMeasurement(pretend)).toBe(true);
    expect(usesTheComponent(pretend)).toBe(false);
  });

  it('reports a screen that reads a clinical record and shows none of it attributed', () => {
    const pretend = `
      import type { Allergy } from '../api/allergies';
      export function Row({ allergy }: { allergy: Allergy }) {
        return <li>{allergy.said}</li>;
      }`;
    expect(importsAClinicalRecord(pretend)).toBe(true);
    expect(usesTheComponent(pretend)).toBe(false);
  });

  it('accepts the same screen once it goes through the component', () => {
    const pretend = `
      import type { Allergy } from '../api/allergies';
      import { ValueWithAttribution, allergyAttribution } from '@/features/attribution';
      export function Row({ allergy }: { allergy: Allergy }) {
        return (
          <ValueWithAttribution attribution={allergyAttribution(allergy)}>
            <li>{allergy.said}</li>
          </ValueWithAttribution>
        );
      }`;
    expect(mustBeAttributed(pretend)).toBe(true);
    expect(usesTheComponent(pretend)).toBe(true);
  });

  it('accepts a screen that draws an observation through the attributed component', () => {
    // CP62's chain view and its siblings. `ObservationValue` is DualUnitValue nested inside
    // ValueWithAttribution; a file rendering it is flagged as clinical *and* satisfied, and the
    // component that builds it is checked by the loop above like every other file.
    const pretend = `
      import { ObservationValue } from '@/features/observations';
      export function Row({ observation }) {
        return <ObservationValue observation={observation} label="Height" />;
      }`;
    expect(drawsAnObservation(pretend)).toBe(true);
    expect(mustBeAttributed(pretend)).toBe(true);
    expect(usesTheComponent(pretend)).toBe(true);
  });

  it('leaves a screen with no clinical value on it alone', () => {
    const pretend = `
      import { Button } from '@dthcms/ui';
      export function LanguageToggle() {
        return <Button>বাংলা</Button>;
      }`;
    expect(mustBeAttributed(pretend)).toBe(false);
  });
});
