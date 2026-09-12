import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';
import type {
  FormularyCatalogue,
  FormularyImport,
  FormularyPrice,
  FormularyProduct,
  FormularyReviewState,
} from '@/features/formulary';
import type { SessionUser } from '@/stores/session';

/**
 * The medicine formulary's screen (CP75, §10, §16.1, D-56).
 *
 * What is checked here is the set of things that would be quietly wrong rather than visibly
 * broken, which is the whole failure mode of a price screen:
 *
 *  - a price nobody has checked must not look like a price somebody approved. All 250 seeded
 *    prices are published MRP read off medex.com.bd, and a column of plausible numbers with
 *    no state beside them is a screen that reads as finished;
 *  - a medicine nobody has priced must not render as zero. Zero is a price;
 *  - the as-of box must answer with the price that was in force **then**, not today's;
 *  - a bulk import's rejections must name the line number from the person's own spreadsheet,
 *    the column, and the reason — in the language the screen is being read in;
 *  - the good lines of a part-failed import must be reported as having gone in, or the
 *    pharmacist re-uploads the whole file;
 *  - a trade name stays in Latin script in both interfaces. A transliterated brand is a
 *    different medicine at the pharmacy counter.
 */

const getCatalogue = vi.hoisted(() => vi.fn());
const listProducts = vi.hoisted(() => vi.fn());
const getPriceHistory = vi.hoisted(() => vi.fn());
const getPriceOn = vi.hoisted(() => vi.fn());
const getReview = vi.hoisted(() => vi.fn());
const recordPrice = vi.hoisted(() => vi.fn());
const completeReview = vi.hoisted(() => vi.fn());
const runImport = vi.hoisted(() => vi.fn());

vi.mock('@/features/formulary/api/formulary', async () => {
  const real = await vi.importActual<typeof import('@/features/formulary/api/formulary')>(
    '@/features/formulary/api/formulary',
  );
  return {
    ...real,
    getCatalogue,
    listProducts,
    getPriceHistory,
    getPriceOn,
    getReview,
    recordPrice,
    completeReview,
    runImport,
  };
});

const { FormularyConsole } = await import('@/features/formulary/components/FormularyConsole');
const { PriceHistoryPanel } = await import('@/features/formulary/components/PriceHistoryPanel');
const { ImportPanel } = await import('@/features/formulary/components/ImportPanel');
const { isConfirmed, isSeeded, daysOverdue, rejectedRows, acceptedRows, unconfirmedCount } =
  await import('@/features/formulary/api/formulary');
const { useSessionStore } = await import('@/stores/session');

const catalogue: FormularyCatalogue = {
  classes: [
    { code: 'BIGUANIDE', name_en: 'Biguanide', name_bn: 'বাইগুয়ানাইড', ordering: 10 },
    {
      code: 'GLP_1_RECEPTOR_AGONIST',
      name_en: 'GLP-1 receptor agonist',
      name_bn: 'জিএলপি-১ রিসেপ্টর অ্যাগোনিস্ট',
      ordering: 50,
    },
  ],
  forms: [{ code: 'TABLET', name_en: 'Tablet', name_bn: 'ট্যাবলেট' }],
  units: [{ code: 'tablet', name_en: 'tablet', name_bn: 'ট্যাবলেট' }],
};

const seededPrice: FormularyPrice = {
  id: '0190a8f2-0000-7000-8000-0000000000b1',
  product_id: '0190a8f2-0000-7000-8000-0000000000c1',
  amount_poisha: 500,
  amount_bdt: '5.00',
  effective_from: '2026-09-08',
  verification: 'PROVISIONAL',
  origin: 'SEED',
  recorded_at: '2026-09-08T00:00:00Z',
};

const confirmedPrice: FormularyPrice = {
  ...seededPrice,
  id: '0190a8f2-0000-7000-8000-0000000000b2',
  amount_poisha: 725,
  amount_bdt: '7.25',
  effective_from: '2026-09-11',
  verification: 'VERIFIED',
  origin: 'REVIEW',
  recorded_by: '0190a8f2-0000-7000-8000-0000000000d1',
  recorded_by_name_en: 'Rakib Hasan',
  recorded_by_name_bn: 'রাকিব হাসান',
};

const comet: FormularyProduct = {
  id: '0190a8f2-0000-7000-8000-0000000000c1',
  generic_id: '0190a8f2-0000-7000-8000-0000000000e1',
  trade_name: 'Comet',
  strength: '500 mg',
  manufacturer: 'Square Pharmaceuticals PLC',
  generic_name: 'Metformin hydrochloride',
  class_code: 'BIGUANIDE',
  class_name_en: 'Biguanide',
  class_name_bn: 'বাইগুয়ানাইড',
  form_code: 'TABLET',
  form_name_en: 'Tablet',
  form_name_bn: 'ট্যাবলেট',
  dispense_unit: 'tablet',
  unit_name_en: 'tablet',
  unit_name_bn: 'ট্যাবলেট',
  is_active: true,
  price: seededPrice,
};

const unpriced: FormularyProduct = {
  ...comet,
  id: '0190a8f2-0000-7000-8000-0000000000c2',
  trade_name: 'Novomet',
  price: undefined,
};

const review: FormularyReviewState = {
  owner: {
    owner_role: 'PHARMACIST',
    owner_role_name_en: 'Pharmacist',
    owner_role_name_bn: 'ফার্মাসিস্ট',
    due_day_of_month: 1,
  },
  current: {
    id: '0190a8f2-0000-7000-8000-0000000000f1',
    period_month: '2026-09-01',
    status: 'OPEN',
    owner_role: 'PHARMACIST',
    owner_role_name_en: 'Pharmacist',
    owner_role_name_bn: 'ফার্মাসিস্ট',
    opened_at: '2026-09-11T13:05:00Z',
    due_on: '2026-09-01',
    reminded_at: '2026-09-11T13:05:00Z',
    products_at_open: 250,
    provisional_at_open: 250,
  },
  products: 250,
  unverified: 250,
  oldest_price_days: 3,
};

const admin: SessionUser = {
  id: '0190a8f2-0000-7000-8000-0000000000d1',
  employee_code: 'ADM01',
  name_en: 'Local Administrator',
  name_bn: 'স্থানীয় প্রশাসক',
  roles: ['ADMIN'],
  permissions: ['formulary.read', 'formulary.write', 'formulary.price.review'],
  facility_id: '0190a000-0000-7000-8000-000000000001',
  grants: {},
} as unknown as SessionUser;

beforeEach(() => {
  vi.clearAllMocks();
  useSessionStore.setState({ user: admin, activeRole: 'ADMIN' } as never);
  getCatalogue.mockResolvedValue(catalogue);
  listProducts.mockResolvedValue({ items: [comet, unpriced], total: 2 });
  getReview.mockResolvedValue(review);
  getPriceHistory.mockResolvedValue([
    confirmedPrice,
    { ...seededPrice, effective_to: '2026-09-11' },
  ]);
  getPriceOn.mockResolvedValue(seededPrice);
});

describe('what the list says about a price', () => {
  it('marks a seeded price as unchecked rather than drawing it like any other number', async () => {
    renderWithProviders(<FormularyConsole />);

    const row = (await screen.findByText('Comet')).closest('tr');
    expect(row).not.toBeNull();
    // The number and the fact that nobody has checked it, together. A screen showing only
    // the first would read as a formulary somebody had been through.
    expect(within(row as HTMLElement).getByText('Tk 5.00')).toBeInTheDocument();
    expect(within(row as HTMLElement).getByText('Not checked')).toBeInTheDocument();
  });

  it('says a medicine has never been priced rather than showing a zero', async () => {
    renderWithProviders(<FormularyConsole />);

    const row = (await screen.findByText('Novomet')).closest('tr');
    expect(within(row as HTMLElement).getByText('Never priced')).toBeInTheDocument();
    // Zero is a price. "Tk 0.00" on this screen would be telling somebody a medicine is free.
    expect(within(row as HTMLElement).queryByText('Tk 0.00')).toBeNull();
  });

  it('keeps a trade name in Latin script', async () => {
    renderWithProviders(<FormularyConsole />);

    const name = await screen.findByText('Comet');
    expect(name).toHaveAttribute('lang', 'en');
  });
});

describe('the monthly review', () => {
  it('leads with how many prices nobody has checked, and says where they came from', async () => {
    renderWithProviders(<FormularyConsole />);

    expect(await screen.findByText('prices nobody has checked, of 250')).toBeInTheDocument();
    expect(screen.getByText('These prices have not been checked')).toBeInTheDocument();
  });

  it('says plainly when only a role owns it and no person is named', async () => {
    renderWithProviders(<FormularyConsole />);

    expect(await screen.findByText('— no particular person is named')).toBeInTheDocument();
  });

  it('names the person when there is one', async () => {
    getReview.mockResolvedValue({
      ...review,
      owner: { ...review.owner, owner_name_en: 'Rakib Hasan', owner_name_bn: 'রাকিব হাসান' },
    });

    renderWithProviders(<FormularyConsole />);

    expect(await screen.findByText('Rakib Hasan')).toBeInTheDocument();
  });

  it('says nobody has been reminded when nobody has', async () => {
    getReview.mockResolvedValue({
      ...review,
      current: { ...review.current!, reminded_at: undefined },
    });

    renderWithProviders(<FormularyConsole />);

    // An open cycle nobody has been told about is the failure criterion 4 is about, so it is
    // a banner rather than an absence.
    expect(await screen.findByText('Nobody has been reminded')).toBeInTheDocument();
  });
});

describe('the price history', () => {
  it('names nobody for a seeded price, in words rather than as a blank', async () => {
    renderWithProviders(<PriceHistoryPanel product={comet} onClose={() => {}} />);

    expect(await screen.findByText('Nobody — published list price')).toBeInTheDocument();
    expect(screen.getByText('Rakib Hasan')).toBeInTheDocument();
  });

  it('answers the as-of question with the price that was in force then', async () => {
    const user = userEvent.setup();
    renderWithProviders(<PriceHistoryPanel product={comet} onClose={() => {}} />);

    await user.type(await screen.findByLabelText('What did this cost on…'), '2026-09-09');

    await waitFor(() => {
      expect(getPriceOn).toHaveBeenCalledWith(comet.id, '2026-09-09');
    });
    // 5.00, the price that was in force on the 9th — not 7.25, which is today's.
    expect(await screen.findByText(/this cost Tk 5.00/)).toBeInTheDocument();
  });

  it('says the clinic had not priced a medicine, rather than showing nothing', async () => {
    getPriceOn.mockResolvedValue(null);
    const user = userEvent.setup();
    renderWithProviders(<PriceHistoryPanel product={comet} onClose={() => {}} />);

    await user.type(await screen.findByLabelText('What did this cost on…'), '2020-01-01');

    expect(await screen.findByText(/had not priced this medicine/)).toBeInTheDocument();
  });

  it('offers no price form for a withdrawn product', async () => {
    renderWithProviders(
      <PriceHistoryPanel
        product={{ ...comet, is_active: false, withdrawn_at: '2026-09-10T00:00:00Z' }}
        onClose={() => {}}
      />,
    );

    await screen.findByText('Price history');
    expect(screen.queryByText('Record a new price')).toBeNull();
  });
});

describe('a bulk import', () => {
  const partFailed: FormularyImport = {
    id: '0190a8f2-0000-7000-8000-000000000101',
    filename: 'september.csv',
    mode: 'DRY_RUN',
    rows_total: 4,
    rows_accepted: 2,
    rows_rejected: 2,
    products_created: 0,
    products_updated: 2,
    prices_recorded: 0,
    imported_at: '2026-09-11T13:00:00Z',
    rows: [
      {
        line: 3,
        outcome: 'REJECTED',
        field: 'strength',
        message_en: 'Strength is required and this row leaves it empty.',
        message_bn: 'মাত্রা লাগবেই, এই সারিতে সেটি ফাঁকা আছে।',
        trade_name: 'Comet',
      },
      {
        line: 5,
        outcome: 'REJECTED',
        field: 'unit_price_bdt',
        message_en: '"0.335" has more precision than one poisha.',
        message_bn: '"0.335"-তে পয়সার চেয়ে বেশি ভগ্নাংশ আছে।',
        trade_name: 'Comet XR',
      },
      { line: 2, outcome: 'UPDATED', trade_name: 'Comet' },
      { line: 4, outcome: 'UPDATED', trade_name: 'Comprid' },
    ],
  };

  async function upload(user: ReturnType<typeof userEvent.setup>) {
    const file = new File(['generic,trade_name\n'], 'september.csv', { type: 'text/csv' });
    await user.upload(screen.getByLabelText('Choose a CSV file'), file);
    await user.click(screen.getByRole('button', { name: 'Check the file' }));
  }

  it('names the line, the column and the reason for every refused row', async () => {
    runImport.mockResolvedValue(partFailed);
    const user = userEvent.setup();
    renderWithProviders(<ImportPanel />);

    await upload(user);

    // The line number from the person's own spreadsheet, not the index among rows that parsed.
    expect(await screen.findByText('3')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('strength')).toBeInTheDocument();
    expect(
      screen.getByText('Strength is required and this row leaves it empty.'),
    ).toBeInTheDocument();
    expect(screen.getByText('"0.335" has more precision than one poisha.')).toBeInTheDocument();
  });

  it('says the good lines are fine, so nobody re-uploads the whole file', async () => {
    runImport.mockResolvedValue(partFailed);
    const user = userEvent.setup();
    renderWithProviders(<ImportPanel />);

    await upload(user);

    expect(
      await screen.findByText(
        'The other 2 lines are fine and will import when you apply this file.',
      ),
    ).toBeInTheDocument();
  });

  it('defaults to a dry run and only offers to apply after one', async () => {
    runImport.mockResolvedValue(partFailed);
    const user = userEvent.setup();
    renderWithProviders(<ImportPanel />);

    // Before a check there is no apply button at all: an upload control whose first button
    // wrote 250 prices would make the dry run something somebody had to remember to ask for.
    expect(screen.queryByRole('button', { name: /Import/ })).toBeNull();

    await upload(user);

    expect(runImport).toHaveBeenCalledWith(expect.objectContaining({ apply: false }));
    expect(await screen.findByRole('button', { name: 'Import 2 lines' })).toBeInTheDocument();
  });
});

describe('the predicates the screen reasons with', () => {
  it('treats an absent price as unconfirmed rather than as an exception', () => {
    // "Nobody has checked this" and "nobody has priced this" are both *not confirmed*, and a
    // caller that had to remember to handle the second separately eventually would not.
    expect(isConfirmed(confirmedPrice)).toBe(true);
    expect(isConfirmed(seededPrice)).toBe(false);
    expect(isConfirmed(undefined)).toBe(false);
    expect(isSeeded(seededPrice)).toBe(true);
    expect(isSeeded(confirmedPrice)).toBe(false);
    expect(unconfirmedCount([comet, unpriced])).toBe(2);
    expect(unconfirmedCount([{ ...comet, price: confirmedPrice }])).toBe(0);
  });

  it('reports overdue as null rather than as a negative number of days', () => {
    const cycle = review.current!;
    expect(daysOverdue(cycle, new Date('2026-09-11T00:00:00Z'))).toBe(10);
    // Not yet due is not "overdue by -3 days". A component branching on the sign of a number
    // is one that eventually renders that sentence.
    expect(daysOverdue(cycle, new Date('2026-08-20T00:00:00Z'))).toBeNull();
    expect(
      daysOverdue({ ...cycle, status: 'COMPLETE' }, new Date('2026-12-01T00:00:00Z')),
    ).toBeNull();
    expect(daysOverdue(undefined, new Date())).toBeNull();
  });

  it('splits an import report into what failed and what did not', () => {
    const report = {
      rows: [
        { line: 2, outcome: 'UPDATED' as const },
        { line: 3, outcome: 'REJECTED' as const, message_en: 'x', message_bn: 'x' },
      ],
    } as FormularyImport;
    expect(rejectedRows(report)).toHaveLength(1);
    expect(acceptedRows(report)).toHaveLength(1);
    expect(rejectedRows({ ...report, rows: undefined })).toEqual([]);
  });
});
