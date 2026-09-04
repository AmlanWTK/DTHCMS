import { act, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError, NetworkError } from '@/lib/api';
import type { Concept, ConceptList } from '@/features/terminology/api/terminology';

import { renderWithProviders } from './render';

/**
 * The coded terminology picker (CP52, §4.6).
 *
 * The search itself is proven on the server: which tier a row lands in, how a misspelling is
 * scored, what the twenty favourites are. What can only be proven here is whether the person
 * holding the tablet ends up with a *coding* rather than a string — and the ways that fails
 * are all quiet ones.
 *
 *  - **A coding recorded without its version.** `E11.9` is a different disease in ICD-11 and
 *    a narrower one in ICD-10 2016. Nobody notices for years, and then everybody does at
 *    once. This is acceptance criterion 2 and it has its own named test below.
 *  - **A stale reply overwriting a fresh one.** A station operator types faster than a shared
 *    clinic connection answers, and the row a slow `dia` puts on screen after a fast `diab`
 *    is a row nobody asked for and everybody trusts.
 *  - **A picker that only works with a mouse.** This is opened forty times a day by somebody
 *    whose other hand is on a patient's file. If the arrow keys do not move a highlight that
 *    a screen reader announces, the field is unusable by half the people who need it.
 *  - **A refusal drawn as an empty list.** "No results" and "SNOMED CT is not licensed here"
 *    send a clinician to two different places, and one of those places is a licence decision
 *    that is already written down.
 *  - **A network failure drawn as silence.** The useful half of that message is not "try
 *    again"; it is that the diagnosis may be written in words now and coded later.
 */

const listFavourites = vi.hoisted(() => vi.fn());
const searchConcepts = vi.hoisted(() => vi.fn());

// Partial: the two network calls are stubbed, the pure helpers are not. `conceptLabel`,
// `selectionOf` and `tierReason` are part of what the picker does, and a test that stubbed
// them would prove the component calls a function rather than that the right words and the
// right three fields reach the caller.
vi.mock('@/features/terminology/api/terminology', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/terminology/api/terminology')>()),
  listFavourites,
  searchConcepts,
}));

const { ConceptPicker } = await import('@/features/terminology/components/ConceptPicker');
const { ConceptChip } = await import('@/features/terminology/components/ConceptChip');
// The barrel, imported as a caller would import it. CP53 and everything after it reach this
// feature through here, and an export left off the index is a boundary violation nobody sees
// until the next screen needs it.
const surface = await import('@/features/terminology');

function concept(over: Partial<Concept> = {}): Concept {
  return {
    system: 'ICD10',
    version: '2019',
    code: 'E11.9',
    display_en: 'Type 2 diabetes mellitus without complications',
    display_bn: 'টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন',
    heading: 'Endocrine, nutritional and metabolic diseases',
    ...over,
  };
}

/** The clinic's own three, ranked, as the favourites endpoint returns them: no tier. */
const FAVOURITES: ConceptList = {
  system: 'ICD10',
  version: '2019',
  concepts: [
    concept({ favourite_rank: 1 }),
    concept({
      code: 'E03.9',
      display_en: 'Hypothyroidism, unspecified',
      display_bn: 'হাইপোথাইরয়েডিজম, অনির্দিষ্ট',
      favourite_rank: 2,
    }),
    concept({
      code: 'I10',
      display_en: 'Essential hypertension',
      display_bn: 'প্রাথমিক উচ্চ রক্তচাপ',
      favourite_rank: 3,
    }),
  ],
};

/** One row of each tier, which is what the reasons are drawn from. */
const TIERED: ConceptList = {
  system: 'ICD10',
  version: '2019',
  concepts: [
    concept({ code: 'E11', display_en: 'Type 2 diabetes mellitus', tier: 1 }),
    concept({ tier: 2, favourite_rank: 1 }),
    concept({ code: 'E10.9', display_en: 'Type 1 diabetes mellitus', tier: 3 }),
    concept({ code: 'E13.9', display_en: 'Other specified diabetes mellitus', tier: 4 }),
  ],
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

function refusal(fields: Record<string, string>, fieldsBN: Record<string, string> = {}): ApiError {
  return new ApiError({
    status: 422,
    code: 'validation_failed',
    kind: 'validation',
    messageEN: 'The request could not be processed.',
    messageBN: 'অনুরোধটি প্রক্রিয়া করা যায়নি।',
    fields,
    fieldsBN,
    correlationID: 'req_term_1',
  });
}

/** The box itself. Named by its role so the test fails if the combobox pattern is lost. */
function box(): HTMLElement {
  return screen.getByRole('combobox');
}

async function openPicker(user: ReturnType<typeof userEvent.setup>) {
  await user.click(box());
}

beforeEach(() => {
  vi.clearAllMocks();
  listFavourites.mockResolvedValue(FAVOURITES);
  searchConcepts.mockResolvedValue(TIERED);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the picker opens on the clinic’s own list', () => {
  it('asks the favourites endpoint, not a search with an empty query', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);

    await screen.findByRole('listbox');
    expect(listFavourites).toHaveBeenCalledWith({ system: 'ICD10', version: undefined });
    expect(searchConcepts).not.toHaveBeenCalled();
  });

  it('shows the rank, so it reads as the clinic’s list rather than as some results', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);

    expect(await screen.findByText('No. 1 on the clinic’s list')).toBeInTheDocument();
    expect(screen.getByText('No. 2 on the clinic’s list')).toBeInTheDocument();
    expect(screen.getByRole('listbox')).toHaveAccessibleName('Codes this clinic uses most');
  });

  it('states the resolved system and version above the rows', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);

    expect(await screen.findByText('Searched in ICD10, version 2019.')).toBeInTheDocument();
  });

  it('fetches nothing at all until somebody puts the cursor in the box', () => {
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    expect(listFavourites).not.toHaveBeenCalled();
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(box()).toHaveAttribute('aria-expanded', 'false');
  });
});

describe('typing', () => {
  it('sends one search for a word, not one per keystroke', async () => {
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);
    await screen.findByRole('listbox');
    await user.type(box(), 'diab');

    await waitFor(() => expect(searchConcepts).toHaveBeenCalledTimes(1));
    expect(searchConcepts).toHaveBeenCalledWith({
      system: 'ICD10',
      version: undefined,
      q: 'diab',
      limit: 25,
    });
  });

  it('cannot let a slow reply overwrite a fresh one', async () => {
    /*
     * The reply to `dia` lands after the reply to `diab`, which is the ordinary case on a
     * connection shared with the rest of the building. The query text is in the cache key, so
     * the older reply lands in an entry nobody is looking at — a property of the structure
     * rather than a race that usually resolves the right way.
     */
    const stale = deferred<ConceptList>();
    const fresh = deferred<ConceptList>();
    searchConcepts.mockImplementation(({ q }: { q: string }) =>
      q === 'dia' ? stale.promise : fresh.promise,
    );

    const user = userEvent.setup({ delay: null });
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);
    await screen.findByRole('listbox');

    await user.type(box(), 'dia');
    await waitFor(() =>
      expect(searchConcepts).toHaveBeenCalledWith(expect.objectContaining({ q: 'dia' })),
    );

    await user.type(box(), 'b');
    await waitFor(() =>
      expect(searchConcepts).toHaveBeenCalledWith(expect.objectContaining({ q: 'diab' })),
    );

    await act(async () => {
      fresh.resolve({
        system: 'ICD10',
        version: '2019',
        concepts: [concept({ code: 'E11.2', display_en: 'Diabetic nephropathy', tier: 3 })],
      });
    });
    expect(await screen.findByText('Diabetic nephropathy')).toBeInTheDocument();

    await act(async () => {
      stale.resolve({
        system: 'ICD10',
        version: '2019',
        concepts: [concept({ code: 'Z99.9', display_en: 'A stale answer nobody asked for' })],
      });
    });

    expect(screen.queryByText('A stale answer nobody asked for')).not.toBeInTheDocument();
    expect(screen.getByText('Diabetic nephropathy')).toBeInTheDocument();
  });

  it('says why each row ranked where it did, in words', async () => {
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);
    await user.type(box(), 'dia');

    expect(await screen.findByText('You typed this code')).toBeInTheDocument();
    expect(screen.getByText('On the clinic’s list')).toBeInTheDocument();
    expect(screen.getByText('A word in the name starts with what you typed')).toBeInTheDocument();
    expect(screen.getByText('Close to the spelling you typed')).toBeInTheDocument();
  });
});

describe('the keyboard', () => {
  it('moves the highlight with the arrows and announces it as an active descendant', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);
    const options = await screen.findAllByRole('option');

    // The first row is highlighted the moment the list opens, so Enter takes the best match.
    expect(options[0]).toHaveAttribute('aria-selected', 'true');
    expect(box()).toHaveAttribute('aria-activedescendant', options[0]!.id);

    await user.keyboard('{ArrowDown}');
    expect(screen.getAllByRole('option')[1]).toHaveAttribute('aria-selected', 'true');
    expect(box()).toHaveAttribute('aria-activedescendant', options[1]!.id);

    await user.keyboard('{ArrowUp}');
    expect(box()).toHaveAttribute('aria-activedescendant', options[0]!.id);

    // Past the top is the bottom. A list that stopped dead would leave the last row of a
    // twenty-five row result reachable only by twenty-four presses.
    await user.keyboard('{ArrowUp}');
    expect(box()).toHaveAttribute('aria-activedescendant', options[2]!.id);
  });

  it('opens the list on an arrow key, without anything having been typed', async () => {
    // Tab into the field, press Down, read the clinic's list. A picker that could only be
    // opened by clicking would be a field a keyboard user cannot see the contents of.
    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await user.tab();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();

    await user.keyboard('{ArrowDown}');

    expect(await screen.findByRole('listbox')).toBeInTheDocument();
  });

  it('selects the highlighted row with Enter', async () => {
    const onSelect = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={onSelect} />);

    await openPicker(user);
    await screen.findAllByRole('option');

    await user.keyboard('{ArrowDown}{Enter}');

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect.mock.calls[0]![0]).toMatchObject({ code: 'E03.9' });
  });

  it('lets Enter reach the form around it when no row is being chosen', async () => {
    // A picker that swallowed every Enter would stop the form it sits in from submitting,
    // and the operator would press it twice, then look for a button that is not there.
    const onSelect = vi.fn();
    const onSubmit = vi.fn((event: { preventDefault: () => void }) => event.preventDefault());
    const user = userEvent.setup();
    renderWithProviders(
      <form onSubmit={onSubmit}>
        <ConceptPicker system="ICD10" onSelect={onSelect} />
        <button type="submit">Save</button>
      </form>,
    );

    await openPicker(user);
    await screen.findByRole('listbox');
    await user.keyboard('{Escape}{Enter}');

    expect(onSelect).not.toHaveBeenCalled();
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('does nothing on an arrow key when the panel is showing a failure', async () => {
    listFavourites.mockRejectedValue(new NetworkError(new TypeError('Failed to fetch')));

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);
    await screen.findByText('The code list cannot be reached.');

    await user.keyboard('{ArrowDown}{Enter}');

    expect(box()).not.toHaveAttribute('aria-activedescendant');
    // And it points at no listbox either. An id reference to an element that was never
    // rendered is a validation failure assistive technology follows anyway.
    expect(box()).not.toHaveAttribute('aria-controls');
    expect(screen.getByText('The code list cannot be reached.')).toBeInTheDocument();
  });

  it('closes on Escape and leaves the box alone, then clears it on a second press', async () => {
    const user = userEvent.setup({ delay: null });
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);
    await user.type(box(), 'dia');
    await screen.findByRole('listbox');

    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
    expect(box()).toHaveAttribute('aria-expanded', 'false');
    expect(box()).toHaveValue('dia');

    await user.keyboard('{Escape}');
    expect(box()).toHaveValue('');
  });

  it('wires the combobox to the listbox it controls', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);
    const listbox = await screen.findByRole('listbox');

    expect(box()).toHaveAttribute('aria-controls', listbox.id);
    expect(box()).toHaveAttribute('aria-expanded', 'true');
    expect(box()).toHaveAttribute('aria-autocomplete', 'list');
  });
});

describe('acceptance criterion 2: the selection carries system, version and code', () => {
  it('hands the caller all three fields together, never a bare code', async () => {
    const onSelect = vi.fn();
    const user = userEvent.setup();
    // No version named. The server resolves its default and reports which — and that is the
    // one that has to travel.
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={onSelect} />);

    await openPicker(user);
    await user.click(await screen.findByText('Type 2 diabetes mellitus without complications'));

    expect(onSelect).toHaveBeenCalledWith({
      system: 'ICD10',
      version: '2019',
      code: 'E11.9',
      display_en: 'Type 2 diabetes mellitus without complications',
      display_bn: 'টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন',
    });
  });

  it('takes the version from the row the server returned, not from the prop', async () => {
    /*
     * The one that would be discovered years later. A deployment whose picker is configured
     * for 2016 while the loaded content is 2019 must stamp 2019 — what was actually searched
     * — rather than the number somebody put in a prop.
     */
    const onSelect = vi.fn();
    listFavourites.mockResolvedValue({
      system: 'ICD10',
      version: '2019',
      concepts: [concept({ version: '2019', favourite_rank: 1 })],
    });

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" version="2016" onSelect={onSelect} />);

    await openPicker(user);
    await user.click(await screen.findByText('Type 2 diabetes mellitus without complications'));

    expect(onSelect.mock.calls[0]![0]).toMatchObject({ version: '2019' });
  });

  it('shows the chosen coding with its system and version, not only the code', async () => {
    // "E11.9" on its own is what criterion 2 exists to prevent, so the chip states all three.
    renderWithProviders(
      <ConceptPicker
        system="ICD10"
        onSelect={vi.fn()}
        value={{
          system: 'ICD10',
          version: '2019',
          code: 'E11.9',
          display_en: 'Type 2 diabetes mellitus without complications',
        }}
      />,
    );

    const chip = screen.getByTestId('concept-chip');
    expect(within(chip).getByText('E11.9')).toBeInTheDocument();
    expect(within(chip).getByText('ICD10')).toBeInTheDocument();
    expect(within(chip).getByText('2019')).toBeInTheDocument();
  });

  it('lets the chosen coding be taken off again', async () => {
    const onClear = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <ConceptPicker
        system="ICD10"
        onSelect={vi.fn()}
        onClear={onClear}
        value={{
          system: 'ICD10',
          version: '2019',
          code: 'E11.9',
          display_en: 'Type 2 diabetes mellitus without complications',
        }}
      />,
    );

    await user.click(
      screen.getByRole('button', {
        name: 'Remove Type 2 diabetes mellitus without complications',
      }),
    );

    expect(onClear).toHaveBeenCalledTimes(1);
  });
});

describe('when the catalogue answers with a refusal', () => {
  it('renders the server’s own sentence rather than “something went wrong”', async () => {
    // SNOMED CT is registered so the mapping table can name it, and refused until D-24
    // answers. The clinician needs the reason, because the reason is somebody else's job.
    listFavourites.mockRejectedValue(
      refusal({ system: 'SNOMED CT is registered but not licensed for this deployment (D-24).' }),
    );

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="SNOMED" onSelect={vi.fn()} />);

    await openPicker(user);

    expect(
      await screen.findByText(
        'SNOMED CT is registered but not licensed for this deployment (D-24).',
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });

  it('reads the refusal in Bangla to a Bangla reader', async () => {
    listFavourites.mockRejectedValue(
      refusal(
        { version: 'Version 2016 has not been loaded.' },
        { version: '২০১৬ সংস্করণটি এখনও তোলা হয়নি।' },
      ),
    );

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" version="2016" onSelect={vi.fn()} />, {
      locale: 'bn',
    });

    await openPicker(user);

    expect(await screen.findByText('২০১৬ সংস্করণটি এখনও তোলা হয়নি।')).toBeInTheDocument();
  });

  it('falls back to the envelope message when the server named no field', async () => {
    listFavourites.mockRejectedValue(refusal({}));

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);

    expect(await screen.findByText('The request could not be processed.')).toBeInTheDocument();
  });

  it('falls back to its own words when the server sent none at all', async () => {
    // A refusal with nothing in it still has to say that the list is not coming, or the
    // panel is a blank box that reads exactly like a catalogue with no diagnoses in it.
    const silent = refusal({});
    listFavourites.mockRejectedValue(
      new ApiError({
        status: 422,
        code: silent.code,
        kind: silent.kind,
        messageEN: '',
        messageBN: '',
        correlationID: silent.correlationID,
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);

    expect(
      await screen.findByText('This terminology cannot be searched here.'),
    ).toBeInTheDocument();
  });
});

describe('when the catalogue cannot be reached', () => {
  it('says so, and says the diagnosis may still be written in words', async () => {
    // Not swallowed, and not "try again". A clinic with no catalogue still has patients, and
    // a note somebody codes tomorrow is worth more than a field nobody could fill in today.
    listFavourites.mockRejectedValue(new NetworkError(new TypeError('Failed to fetch')));

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);

    expect(await screen.findByText('The code list cannot be reached.')).toBeInTheDocument();
    expect(screen.getByText(/Write the diagnosis in your own words instead/)).toBeInTheDocument();
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  });
});

describe('when nothing matches', () => {
  it('says so plainly and suggests the code', async () => {
    searchConcepts.mockResolvedValue({ system: 'ICD10', version: '2019', concepts: [] });

    const user = userEvent.setup({ delay: null });
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);
    await user.type(box(), 'zzzz');

    expect(await screen.findByText('Nothing matched “zzzz”')).toBeInTheDocument();
    expect(screen.getByText(/type the code itself/)).toBeInTheDocument();
  });

  it('says something different when the clinic has no list yet', async () => {
    // An empty favourites list is not a failed search, and telling somebody that nothing
    // matched a query they never typed is how a screen teaches people to distrust it.
    listFavourites.mockResolvedValue({ system: 'ICD10', version: '2019', concepts: [] });

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />);

    await openPicker(user);

    expect(await screen.findByText('This clinic has no list of its own yet')).toBeInTheDocument();
  });
});

describe('both languages', () => {
  it('reads the rows in Bangla to a Bangla reader', async () => {
    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />, { locale: 'bn' });

    await openPicker(user);

    expect(await screen.findByText('টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন')).toBeInTheDocument();
    // The rank is in Bengali numerals, because the house rule says a count in running text
    // follows the language. A code is not a count and stays in ASCII digits — E11.9 read as
    // ই১১.৯ is a code nobody can match against a printed classification.
    expect(screen.getByText('ক্লিনিকের তালিকায় ১ নম্বরে')).toBeInTheDocument();
    expect(screen.getByText('E11.9')).toBeInTheDocument();
  });

  it('shows the English name to a Bangla reader when there is no Bangla one', async () => {
    // A blank row is worse than the wrong language: it cannot be read, and it cannot be
    // selected either.
    listFavourites.mockResolvedValue({
      system: 'ICD10',
      version: '2019',
      concepts: [
        concept({
          code: 'E05.9',
          display_en: 'Thyrotoxicosis, unspecified',
          display_bn: undefined,
        }),
      ],
    });

    const user = userEvent.setup();
    renderWithProviders(<ConceptPicker system="ICD10" onSelect={vi.fn()} />, { locale: 'bn' });

    await openPicker(user);

    expect(await screen.findByText('Thyrotoxicosis, unspecified')).toBeInTheDocument();
  });
});

describe('what the feature offers the screens that will use it', () => {
  it('exports the picker, the chip and the debounce through its index', () => {
    expect(surface.ConceptPicker).toBe(ConceptPicker);
    expect(surface.ConceptChip).toBe(ConceptChip);
    expect(surface.SEARCH_DEBOUNCE_MS).toBe(250);
  });

  it('keys a query on the text searched, which is what makes a stale reply harmless', () => {
    expect(surface.conceptQueryKey('ICD10', undefined, 'dia')).toEqual([
      'terminology',
      'concepts',
      'ICD10',
      'default',
      'dia',
    ]);
    // A pinned version is part of the key too: the same words under two versions are two
    // different lists, and sharing one cache entry between them would show the wrong one.
    expect(surface.conceptQueryKey('ICD10', '2016', 'dia')).toContain('2016');
  });
});

describe('the chip on its own', () => {
  it('names each of the three parts for a screen reader', async () => {
    // "ICD10 2019 E11.9" read as a run of characters is three numbers, and a listener cannot
    // tell which of them is the code.
    renderWithProviders(
      <ConceptChip
        concept={{
          system: 'ICD10',
          version: '2019',
          code: 'E11.9',
          display_en: 'Type 2 diabetes mellitus without complications',
          display_bn: 'টাইপ ২ ডায়াবেটিস মেলিটাস, জটিলতাবিহীন',
        }}
      />,
    );

    const chip = screen.getByTestId('concept-chip');
    expect(within(chip).getByText('Code')).toBeInTheDocument();
    expect(within(chip).getByText('Code system')).toBeInTheDocument();
    expect(within(chip).getByText('Version')).toBeInTheDocument();
  });

  it('has no remove button where removing is not offered', () => {
    renderWithProviders(
      <ConceptChip
        concept={{
          system: 'ICD10',
          version: '2019',
          code: 'E11.9',
          display_en: 'Type 2 diabetes mellitus without complications',
        }}
      />,
    );

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
