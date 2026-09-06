import { ApiError, type components } from '@dthcms/api-client';
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import en from '../messages/en.json';
import bn from '../messages/bn.json';

import { renderWithProviders } from './render';

/**
 * The background queue's operator view (CP69, ADR-0031, §7.1, §8.5).
 *
 * The manual verification is that somebody who suspects background work has stopped can tell
 * from this page whether it has. What can be proven from a screen is whether it makes that
 * answer easier or harder to reach — and every way a queue dashboard gets it wrong is quiet,
 * plausible, and looks exactly like a working feature:
 *
 *  - **A kind with nothing in flight is dropped from the table.** The most natural rendering
 *    in the world: iterate what came back from the queue. It hides the single failure this
 *    page exists to catch — a job type that has stopped being enqueued at all — and it hides
 *    it by making the row *absent*, which nobody notices. ADR-0031 made `ops.job_kind` a
 *    registered catalogue for this reason, and there is a named test below that renders nine
 *    kinds of which seven are silent and insists on all nine.
 *  - **A null rate drawn as 0%.** "No failures" and "nothing ran" are different states, and
 *    the second one wearing the first one's number is a stopped queue behind the healthiest
 *    figure on the page. There are tests for both rates, for the badge that must not be
 *    green, and for `seconds_since_last_finish: -1`, which must read as *never* and never as
 *    a duration.
 *  - **Depth drawn larger than age.** A queue of two that has not moved in an hour is worse
 *    than a queue of four hundred that is draining. Position and size are claims; the test
 *    checks the age is the element the row leads with.
 *  - **A paused kind that reads as a quiet one.** Paused work is work that has stopped
 *    happening on purpose and its row is numerically identical to a healthy idle row. It has
 *    to be unmistakable in more than colour.
 *  - **A manage control drawn for somebody who holds only the read.** The physician and QA
 *    hold `ops.jobs.read` and not `ops.jobs.manage`. A button that exists in order to be
 *    refused teaches people the software is unreliable.
 *  - **A 409 shown as a failure.** All four of them mean a colleague changed something while
 *    the screen was open. Reported as an error, they send an operator looking for a bug.
 *  - **An enqueue control.** There is deliberately no endpoint, and the audit below fails if
 *    this feature ever grows one.
 */

type JobQueueHealth = components['schemas']['JobQueueHealth'];
type JobKind = components['schemas']['JobKind'];
type Job = components['schemas']['Job'];

const JOB_ID = '0190a8f2-0000-7000-8000-0000000000a1';
const OTHER_JOB = '0190a8f2-0000-7000-8000-0000000000a2';
const ADMIN = '0190a8f2-0000-7000-8000-0000000000d1';

const getQueueHealth = vi.hoisted(() => vi.fn());
const listKinds = vi.hoisted(() => vi.fn());
const listJobs = vi.hoisted(() => vi.fn());
const getJob = vi.hoisted(() => vi.fn());
const retryJob = vi.hoisted(() => vi.fn());
const cancelJob = vi.hoisted(() => vi.fn());
const pauseKind = vi.hoisted(() => vi.fn());
const resumeKind = vi.hoisted(() => vi.fn());

/*
 * Partial: the network calls are stubbed, the rules are not. `severityOf`, `rowsInOrder`,
 * `rateIsAvailable`, `ageParts` and `conflictOf` are what this screen *is* — which word a row
 * gets, what order they are read in, whether there is a rate to draw at all, and whether a
 * refusal is a colleague or a fault. A test that stubbed them would prove the components call
 * a function rather than that the right sentence reaches the person reading the page.
 */
vi.mock('@/features/jobs/api/jobs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/features/jobs/api/jobs')>()),
  getQueueHealth,
  listKinds,
  listJobs,
  getJob,
  retryJob,
  cancelJob,
  pauseKind,
  resumeKind,
}));

const { JobsConsole } = await import('@/features/jobs/components/JobsConsole');
// The barrel, imported as a caller would.
const surface = await import('@/features/jobs');
const { useSessionStore } = await import('@/stores/session');

/* ------------------------------------------------------------------------- */
/* The catalogue, as the migration seeds it                                   */
/* ------------------------------------------------------------------------- */

/** The nine registered kinds. Seven of them have nothing in flight, which is the point. */
const CATALOGUE: { kind: string; cls: JobKind['job_class']; queue: string; sla: number | null }[] =
  [
    { kind: 'clinical.synthesis', cls: 'CLINICAL_CRITICAL', queue: 'clinical', sla: 300 },
    { kind: 'clinical.alert_escalation', cls: 'CLINICAL_CRITICAL', queue: 'clinical', sla: 60 },
    { kind: 'patient.sms', cls: 'PATIENT_FACING', queue: 'patient', sla: null },
    { kind: 'patient.prescription_pdf', cls: 'PATIENT_FACING', queue: 'patient', sla: null },
    { kind: 'pipeline.ocr', cls: 'PIPELINE', queue: 'pipeline', sla: null },
    { kind: 'analytical.research_extract', cls: 'ANALYTICAL', queue: 'analytical', sla: null },
    { kind: 'maintenance.idempotency_purge', cls: 'MAINTENANCE', queue: 'maintenance', sla: null },
    { kind: 'maintenance.ledger_verify', cls: 'MAINTENANCE', queue: 'maintenance', sla: null },
    { kind: 'maintenance.lease_reap', cls: 'MAINTENANCE', queue: 'maintenance', sla: null },
  ];

function kinds(over: Partial<Record<string, Partial<JobKind>>> = {}): JobKind[] {
  return CATALOGUE.map((entry) => ({
    kind: entry.kind,
    job_class: entry.cls,
    queue: entry.queue,
    priority: 500,
    max_attempts: 5,
    backoff_seconds: 15,
    backoff_cap_seconds: 300,
    sla_seconds: entry.sla,
    description_en: `Do the ${entry.kind} work`,
    description_bn: `${entry.kind} কাজটি করা`,
    paused: false,
    ...(over[entry.kind] ?? {}),
  }));
}

/**
 * A row that has never had anything in it: nothing waiting, nothing running, nothing
 * finished. Both rates null and `seconds_since_last_finish` at the never sentinel.
 */
function silent(kind: string, over: Partial<JobQueueHealth> = {}): JobQueueHealth {
  const entry = CATALOGUE.find((c) => c.kind === kind);
  return {
    kind,
    job_class: entry?.cls ?? 'MAINTENANCE',
    queue: entry?.queue ?? 'maintenance',
    paused: false,
    sla_seconds: entry?.sla ?? null,
    available: 0,
    running: 0,
    oldest_available_seconds: 0,
    oldest_due_seconds: 0,
    succeeded: 0,
    discarded: 0,
    cancelled: 0,
    dead_letters: 0,
    failure_rate: null,
    sla_measured: 0,
    sla_met: 0,
    sla_attainment: null,
    seconds_since_last_finish: -1,
    ...over,
  };
}

/** The whole catalogue as health rows, with named rows overridden. */
function health(over: Record<string, Partial<JobQueueHealth>> = {}) {
  return {
    queues: CATALOGUE.map((entry) => silent(entry.kind, over[entry.kind] ?? {})),
    window_seconds: 3600,
    as_of: '2026-09-05T09:14:00Z',
  };
}

function job(over: Partial<Job> = {}): Job {
  return {
    id: JOB_ID,
    kind: 'clinical.synthesis',
    queue: 'clinical',
    args: { patient_id: '0190a8f2-0000-7000-8000-00000000beef', visit_id: 'v-7' },
    priority: 900,
    attempt: 5,
    max_attempts: 5,
    status: 'DISCARDED',
    run_at: '2026-09-05T08:00:00Z',
    enqueued_at: '2026-09-05T07:55:00Z',
    finished_at: '2026-09-05T08:04:00Z',
    sla_deadline: '2026-09-05T08:00:00Z',
    met_sla: false,
    last_error: 'inference gateway timed out',
    description_en: 'Do the clinical.synthesis work',
    description_bn: 'clinical.synthesis কাজটি করা',
    attempts: [
      {
        attempt: 1,
        worker: 'worker-pod-3',
        failed_at: '2026-09-05T07:56:00Z',
        ran_for_ms: 42,
        error: 'inference gateway refused the connection',
      },
      {
        attempt: 2,
        worker: 'worker-pod-3',
        failed_at: '2026-09-05T08:00:00Z',
        ran_for_ms: 240_000,
        error: 'inference gateway timed out',
      },
    ],
    ...over,
  };
}

const initialSession = useSessionStore.getInitialState();

/** The administrator: the only role the server grants `ops.jobs.manage`. */
const MANAGER = ['ops.jobs.read', 'ops.jobs.manage', 'audit.read'];
/** The physician and QA: they may read the queue and may not touch it. */
const READER = ['ops.jobs.read', 'audit.read'];

function signedInWith(permissions: string[], role = 'ADMIN') {
  useSessionStore.setState({
    ...initialSession,
    status: 'authenticated',
    user: {
      id: ADMIN,
      employeeCode: 'E900',
      nameEN: 'Ops Administrator',
      nameBN: 'প্রশাসক',
      facilityId: '11111111-1111-4111-8111-111111111111',
      roles: [role],
      grants: { [role]: permissions },
      permissions,
      secondFactor: { required: false, enrolled: true, pending: false, recoveryCodesLeft: 8 },
    },
    activeRole: role,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  useSessionStore.setState(initialSession, true);
  signedInWith(MANAGER);
  getQueueHealth.mockResolvedValue(health());
  listKinds.mockResolvedValue(kinds());
  listJobs.mockResolvedValue([]);
  getJob.mockResolvedValue(job());
  retryJob.mockResolvedValue(undefined);
  cancelJob.mockResolvedValue(undefined);
  pauseKind.mockResolvedValue(kinds()[0]);
  resumeKind.mockResolvedValue(kinds()[0]);
});

afterEach(() => {
  vi.restoreAllMocks();
});

/* ------------------------------------------------------------------------- */

describe('every registered kind is a row', () => {
  it('draws all nine even when seven of them have never had anything in them', async () => {
    /*
     * The failure this catches is the whole reason the endpoint returns the catalogue
     * left-joined to the queue. A table built from what is currently queued would show two
     * rows here and look completely correct, and the seven kinds that have stopped being
     * enqueued would be invisible — not wrong, *absent*, which is the one defect a reader
     * cannot see.
     */
    getQueueHealth.mockResolvedValue(
      health({
        'pipeline.ocr': {
          available: 3,
          oldest_available_seconds: 40,
          oldest_due_seconds: 40,
          running: 1,
        },
        'patient.sms': { succeeded: 20, failure_rate: 0, seconds_since_last_finish: 30 },
      }),
    );

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    for (const entry of CATALOGUE) {
      expect(screen.getByTestId(`kind-${entry.kind}`), entry.kind).toBeInTheDocument();
    }
    expect(screen.getAllByTestId(/^kind-/)).toHaveLength(CATALOGUE.length);
  });

  it('has no control anywhere that hides a row', async () => {
    // The most requested feature on a page like this, and the one that breaks it. If a
    // filter is ever added, this fails and whoever added it has to argue with the ADR.
    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    const hiders = screen
      .getAllByRole('button')
      .map((button) => button.textContent ?? '')
      .filter((label) => /hide|only failing|only busy|লুকান/i.test(label));
    expect(hiders, `Controls that would drop a row: ${hiders.join(', ')}`).toEqual([]);
  });

  it('says how many need a look, out of how many there are', async () => {
    getQueueHealth.mockResolvedValue(health({ 'clinical.synthesis': { paused: true } }));

    renderWithProviders(<JobsConsole />);

    // Eight of nine are silent and one is paused; the silent ones have never finished
    // anything, so this is a nine-out-of-nine morning. The point of the assertion is the
    // denominator: a count of what is wrong without a count of what exists is not a fact.
    const summary = await screen.findByTestId('attention-summary');
    expect(summary).toHaveTextContent('9');
  });
});

describe('a rate that could not be computed is words, never a zero', () => {
  it('says nothing finished rather than 0% when nothing finished in the window', async () => {
    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    const cell = screen.getByTestId('failures-pipeline.ocr');
    expect(cell).toHaveTextContent(en.jobs.nothingFinished);
    expect(cell).not.toHaveTextContent('0%');
  });

  it('draws no percentage at all on a page where nothing has run', async () => {
    // Belt and braces, and it is the assertion that would have caught the bug: a single
    // `rate ?? 0` anywhere in the feature puts a "0%" on this screen.
    const view = renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    expect(view.container.textContent).not.toMatch(/0(\.0)?%/);
  });

  it('draws the real rate when the server computed one', async () => {
    getQueueHealth.mockResolvedValue(
      health({
        'pipeline.ocr': {
          succeeded: 39,
          discarded: 1,
          failure_rate: 2.5,
          seconds_since_last_finish: 120,
        },
      }),
    );

    renderWithProviders(<JobsConsole />);
    const cell = (await screen.findByTestId('failures-pipeline.ocr')).closest('td');

    // The percentage and what it is a percentage *of*, together. A bare "2.5%" in a table
    // cell is a number two readers take to mean two different things, and a numerator with
    // no denominator beside it is the way this page would start lying quietly.
    expect(cell).toHaveTextContent('2.5%');
    expect(cell).toHaveTextContent('1 of 40 finished jobs');
  });

  it('says nothing measured for a deadline nothing was measured against', async () => {
    renderWithProviders(<JobsConsole />);
    const cell = await screen.findByTestId('sla-clinical.synthesis');

    expect(cell).toHaveTextContent(en.jobs.nothingMeasured);
    expect(cell).not.toHaveTextContent('100%');
    expect(cell).not.toHaveTextContent('0%');
  });

  it('distinguishes a kind with no deadline from one whose deadline was not measured', async () => {
    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    // `pipeline.ocr` carries no SLA at all, so it has no cell to fill: saying "nothing
    // measured" there would imply a promise that was never made.
    expect(screen.queryByTestId('sla-pipeline.ocr')).not.toBeInTheDocument();
    expect(screen.getByTestId('kind-pipeline.ocr')).toHaveTextContent(en.jobs.noDeadline);
  });

  it('reads -1 as never, and never as a duration', async () => {
    renderWithProviders(<JobsConsole />);
    const cell = await screen.findByTestId('last-finish-clinical.synthesis');

    expect(cell).toHaveTextContent(en.jobs.neverFinished);
    // The sentinel is not a number of seconds and must never be rendered as one.
    expect(cell).not.toHaveTextContent('-1');
    expect(cell).not.toHaveTextContent('1 second');
  });

  it('does not call a kind that has never finished anything healthy', async () => {
    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    const row = screen.getByTestId('kind-maintenance.lease_reap');
    expect(within(row).queryByTestId('state-healthy')).not.toBeInTheDocument();
    expect(within(row).getByTestId('state-never')).toBeInTheDocument();
  });

  it('draws dead letters even when the window says nothing was given up on', async () => {
    /*
     * The trap the un-windowed count exists to close. Forty jobs were given up on last
     * night; nothing has failed since. `discarded` over the last hour is zero and
     * `failure_rate` is null because nothing finished — so on the windowed numbers alone
     * this row is indistinguishable from a kind with nothing wrong, and those forty jobs
     * will not run again until a person presses retry.
     */
    getQueueHealth.mockResolvedValue(
      health({ 'pipeline.ocr': { dead_letters: 40, seconds_since_last_finish: 26_000 } }),
    );

    renderWithProviders(<JobsConsole />);
    const row = await screen.findByTestId('kind-pipeline.ocr');

    expect(within(row).getByTestId('dead-pipeline.ocr')).toHaveTextContent('40');
    // And the way through to them does not depend on the window either.
    expect(within(row).getByTestId('open-dead-pipeline.ocr')).toBeInTheDocument();
    // The windowed cell is still honest about its own question.
    expect(within(row).getByTestId('failures-pipeline.ocr')).toHaveTextContent(
      en.jobs.nothingFinished,
    );
  });

  it('gives a row with dead letters a state of its own, and counts it as needing a look', () => {
    const abandoned = silent('pipeline.ocr', {
      succeeded: 12,
      failure_rate: 0,
      dead_letters: 3,
      seconds_since_last_finish: 40,
    });
    // Not `healthy`, which is what it would be on the windowed numbers alone, and not
    // `failing`, which asks for a different act: nothing is going wrong at this moment.
    expect(surface.severityOf(abandoned)).toBe('abandoned');
    expect(surface.needsAttention(abandoned)).toBe(true);
  });

  it('does not call a quiet kind healthy either, and does not raise an alarm about it', async () => {
    // A kind that finished something recently and has nothing in the window is *quiet*: the
    // shape of a six-hourly maintenance job and equally the shape of a stopped queue. Grey,
    // and not counted as something to look at — an alarm that is usually wrong takes the
    // real ones down with it.
    expect(
      surface.severityOf(silent('maintenance.ledger_verify', { seconds_since_last_finish: 4000 })),
    ).toBe('quiet');
    expect(
      surface.needsAttention(
        silent('maintenance.ledger_verify', { seconds_since_last_finish: 4000 }),
      ),
    ).toBe(false);
  });
});

describe('age is the alarm, not depth', () => {
  it('calls a shallow queue that has not moved stuck, and a deep one that is moving working', async () => {
    const stuck = silent('patient.sms', {
      available: 2,
      oldest_available_seconds: 3600,
      oldest_due_seconds: 3600,
    });
    const draining = silent('pipeline.ocr', {
      available: 400,
      oldest_available_seconds: 20,
      oldest_due_seconds: 20,
      running: 8,
      succeeded: 900,
      failure_rate: 0,
      seconds_since_last_finish: 5,
    });

    expect(surface.severityOf(stuck)).toBe('stuck');
    expect(surface.severityOf(draining)).toBe('working');
  });

  it('measures a kind with a deadline against its own deadline', async () => {
    // §7.1's five minutes. A synthesis job still waiting after six is already late, and a
    // general fifteen-minute threshold would notice nine minutes after the promise did.
    const synthesis = silent('clinical.synthesis', {
      available: 1,
      oldest_available_seconds: 360,
      oldest_due_seconds: 360,
    });
    expect(surface.stallThresholdFor(synthesis)).toBe(300);
    expect(surface.severityOf(synthesis)).toBe('stuck');

    // The same six minutes on a kind with no deadline is a queue that is simply busy.
    const ocr = silent('pipeline.ocr', {
      available: 1,
      oldest_available_seconds: 360,
      oldest_due_seconds: 360,
      succeeded: 4,
      failure_rate: 0,
      seconds_since_last_finish: 60,
    });
    expect(surface.stallThresholdFor(ocr)).toBe(surface.DEFAULT_STALL_SECONDS);
    expect(surface.severityOf(ocr)).toBe('working');
  });

  it('leads the row with the age and puts the depth under it', async () => {
    getQueueHealth.mockResolvedValue(
      health({
        'patient.sms': {
          available: 2,
          oldest_available_seconds: 3600,
          oldest_due_seconds: 3600,
        },
      }),
    );

    renderWithProviders(<JobsConsole />);
    const age = await screen.findByTestId('age-patient.sms');

    expect(age).toHaveTextContent('1 hour');
    // Both facts are present, and the age is the one drawn as the row's headline. Position
    // and size are claims about which number matters.
    const cell = age.closest('td');
    expect(cell).not.toBeNull();
    expect(cell?.firstElementChild).toBe(age);
    expect(cell).toHaveTextContent('2 jobs waiting');
  });

  it('does not call a queue stuck when what is waiting is waiting on purpose', () => {
    /*
     * The distinction the payload draws and the reason it draws it. One SMS that failed and
     * is sitting out a half-hour backoff has existed for 38 minutes — total age says so
     * honestly — and nothing about it is due. Keying the alarm off total age would light
     * this row up while the retry policy behaved exactly as designed, and an alarm that is
     * usually wrong is one people stop reading.
     */
    const backingOff = silent('patient.sms', {
      available: 1,
      oldest_available_seconds: 2280,
      oldest_due_seconds: 0,
      succeeded: 30,
      failure_rate: 0,
      seconds_since_last_finish: 60,
    });
    expect(surface.severityOf(backingOff)).toBe('working');
    expect(surface.someWaitingIsDeliberate(backingOff)).toBe(true);

    // The same total age with the work actually due is the wedged worker this page is for.
    expect(surface.severityOf({ ...backingOff, oldest_due_seconds: 2280 })).toBe('stuck');
  });

  it('draws both ages, and says which is which, whenever they differ', async () => {
    getQueueHealth.mockResolvedValue(
      health({
        'patient.sms': {
          available: 1,
          oldest_available_seconds: 2280,
          oldest_due_seconds: 0,
          succeeded: 30,
          failure_rate: 0,
          seconds_since_last_finish: 60,
        },
      }),
    );

    renderWithProviders(<JobsConsole />);

    // Total age still leads the row: "waiting 38 minutes" is true and worth knowing.
    expect(await screen.findByTestId('age-patient.sms')).toHaveTextContent('38 minutes');
    // And the second line is what stops a reader concluding a worker is wedged.
    expect(screen.getByTestId('due-patient.sms')).toHaveTextContent(en.jobs.noneDue);
  });

  it('says how much is overdue when only some of the wait was deliberate', async () => {
    getQueueHealth.mockResolvedValue(
      health({
        'pipeline.ocr': {
          available: 4,
          oldest_available_seconds: 3600,
          oldest_due_seconds: 600,
          succeeded: 12,
          failure_rate: 0,
          seconds_since_last_finish: 30,
        },
      }),
    );

    renderWithProviders(<JobsConsole />);
    expect(await screen.findByTestId('age-pipeline.ocr')).toHaveTextContent('1 hour');
    expect(screen.getByTestId('due-pipeline.ocr')).toHaveTextContent('10 minutes');
  });

  it('does not print the same number twice when the two ages agree', async () => {
    // The common case. A second line repeating the first teaches a reader to stop reading
    // it, which is where the interesting case lives.
    getQueueHealth.mockResolvedValue(
      health({
        'pipeline.ocr': {
          available: 4,
          oldest_available_seconds: 120,
          oldest_due_seconds: 120,
          succeeded: 12,
          failure_rate: 0,
          seconds_since_last_finish: 30,
        },
      }),
    );

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('age-pipeline.ocr');
    expect(screen.queryByTestId('due-pipeline.ocr')).not.toBeInTheDocument();
  });

  it('sorts the loudest rows to the top', async () => {
    const rows = [
      silent('maintenance.lease_reap', {
        succeeded: 4,
        failure_rate: 0,
        seconds_since_last_finish: 9,
      }),
      // Two waiting for an hour and every one of them due: nothing is taking this work.
      silent('patient.sms', {
        available: 2,
        oldest_available_seconds: 3600,
        oldest_due_seconds: 3600,
      }),
      silent('pipeline.ocr', {
        succeeded: 8,
        discarded: 2,
        dead_letters: 2,
        failure_rate: 20,
        seconds_since_last_finish: 10,
      }),
      // Given up on last night, nothing failing since. Below the two above and above the
      // ones that are fine — those jobs will not run again until somebody presses retry.
      silent('patient.prescription_pdf', {
        succeeded: 12,
        failure_rate: 0,
        dead_letters: 3,
        seconds_since_last_finish: 40,
      }),
    ];
    expect(surface.rowsInOrder(rows).map((row) => row.kind)).toEqual([
      'patient.sms',
      'pipeline.ocr',
      'patient.prescription_pdf',
      'maintenance.lease_reap',
    ]);
  });
});

describe('a paused kind is unmistakable', () => {
  it('outranks every other state, because by the numbers it looks idle and healthy', () => {
    const paused = silent('patient.sms', {
      paused: true,
      available: 2,
      oldest_available_seconds: 7200,
      oldest_due_seconds: 7200,
      succeeded: 4,
      discarded: 4,
      dead_letters: 4,
      failure_rate: 50,
      seconds_since_last_finish: 30,
    });
    expect(surface.severityOf(paused)).toBe('paused');
  });

  it('says so in a word and in a sentence, not only in a colour', async () => {
    getQueueHealth.mockResolvedValue(health({ 'patient.sms': { paused: true } }));
    listKinds.mockResolvedValue(kinds({ 'patient.sms': { paused: true } }));

    renderWithProviders(<JobsConsole />);
    const row = await screen.findByTestId('kind-patient.sms');

    // The word.
    expect(within(row).getByTestId('state-paused')).toHaveTextContent(en.jobs.state.paused);
    // The sentence, which is what a reader who has not learned the colours takes from it.
    expect(within(row).getByTestId('paused-patient.sms')).toBeInTheDocument();
    // And the row carries the state as data, which is what the rail is drawn from — so a
    // photograph of this screen still says which row it was.
    expect(row).toHaveAttribute('data-state', 'paused');
  });

  it('names who paused it and when', async () => {
    /*
     * The half that was missing. The operator reading this at nine on Thursday is not the
     * one who paused it during Tuesday's incident, and "who do I ask before turning this
     * back on" is the question they are holding — answerable, until now, only from the log.
     */
    getQueueHealth.mockResolvedValue(
      health({
        'patient.sms': {
          paused: true,
          paused_at: '2026-09-03T08:40:00Z',
          paused_by_name_en: 'Dr Nahid Rahman',
          paused_by_name_bn: 'ডা. নাহিদ রহমান',
        },
      }),
    );

    renderWithProviders(<JobsConsole />);
    const note = await screen.findByTestId('paused-patient.sms');

    expect(note).toHaveTextContent('Dr Nahid Rahman');
    expect(note).toHaveTextContent('Sep 3, 2026');
    // And still says what a pause *means*, for the reader who has not seen one before.
    expect(note).toHaveTextContent(en.jobs.pausedNote);
  });

  it('still says a kind is paused when the server sent no attribution', async () => {
    // Degrading rather than disappearing: losing the whole notice to protect a detail
    // would drop the most important fact on the row.
    getQueueHealth.mockResolvedValue(health({ 'patient.sms': { paused: true } }));

    renderWithProviders(<JobsConsole />);
    const note = await screen.findByTestId('paused-patient.sms');

    expect(note).toHaveTextContent(en.jobs.pausedNote);
    expect(note.textContent ?? '').not.toMatch(/Paused by\s*[.,]/);
  });

  it('names the person in the reader’s language, falling back rather than blanking', () => {
    // Bangla reader, Bangla name.
    expect(
      surface.pausedAttribution(
        { paused_by_name_en: 'Dr Nahid Rahman', paused_by_name_bn: 'ডা. নাহিদ রহমান' },
        'bn',
      )?.values.name,
    ).toBe('ডা. নাহিদ রহমান');
    // Bangla reader, only an English name on the record: answered, not blanked.
    expect(
      surface.pausedAttribution({ paused_by_name_en: 'Dr Nahid Rahman' }, 'bn')?.values.name,
    ).toBe('Dr Nahid Rahman');
    // A time and no name, and a name and no time, each say the half they have.
    expect(surface.pausedAttribution({ paused_at: '2026-09-03T08:40:00Z' }, 'en')?.key).toBe(
      'pausedAt',
    );
    expect(surface.pausedAttribution({ paused_by_name_en: 'A' }, 'en')?.key).toBe('pausedBy');
    // Neither: no sentence at all, rather than one with a hole in it.
    expect(surface.pausedAttribution({}, 'en')).toBeNull();
  });

  it('keeps drawing what is queued behind a paused kind', async () => {
    // Pausing does not consume what is already there: those jobs stay AVAILABLE and are
    // skipped rather than claimed. A row that blanked its depth would tell an operator the
    // queue had drained.
    getQueueHealth.mockResolvedValue(
      health({
        'patient.sms': {
          paused: true,
          available: 6,
          oldest_available_seconds: 900,
          oldest_due_seconds: 900,
        },
      }),
    );

    renderWithProviders(<JobsConsole />);
    const row = await screen.findByTestId('kind-patient.sms');
    expect(row).toHaveTextContent('6 jobs waiting');
  });
});

describe('the manage controls are absent without the permission', () => {
  it('draws no pause, no retry and no cancel for somebody who holds only the read', async () => {
    signedInWith(READER, 'PHYSICIAN');
    listJobs.mockResolvedValue([job()]);

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');
    await screen.findByTestId(`job-${JOB_ID}`);

    for (const entry of CATALOGUE) {
      expect(screen.queryByTestId(`pause-${entry.kind}`), entry.kind).not.toBeInTheDocument();
    }
    expect(screen.queryByTestId(`retry-${JOB_ID}`)).not.toBeInTheDocument();
    expect(screen.queryByTestId('start-cancel')).not.toBeInTheDocument();
    // And the column heading goes with them. A column of empty cells is its own kind of
    // confusing.
    expect(screen.queryByText(en.jobs.column.controls)).not.toBeInTheDocument();
  });

  it('draws them for the administrator, who is the one role granted the manage permission', async () => {
    listJobs.mockResolvedValue([job()]);

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    expect(screen.getByTestId('pause-clinical.synthesis')).toBeInTheDocument();
    expect(await screen.findByTestId(`retry-${JOB_ID}`)).toBeInTheDocument();
  });

  it('asks the interface actions the server’s own two questions', async () => {
    const { requirementsOf } = await import('@/lib/permissions');
    expect(requirementsOf('admin.jobs.view')).toEqual(['ops.jobs.read']);
    // Emphatically its own permission. Folding the manage into the read would hand the
    // floor supervisor the ability to stop the synthesis §7.1 promises.
    expect(requirementsOf('admin.jobs.manage')).toEqual(['ops.jobs.manage']);
  });
});

describe('pausing a kind', () => {
  it('confirms before stopping work the clinic is relying on', async () => {
    const user = userEvent.setup();
    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    await user.click(screen.getByTestId('pause-clinical.synthesis'));
    expect(pauseKind).not.toHaveBeenCalled();

    await user.click(screen.getByTestId('confirm-pause-clinical.synthesis'));
    await waitFor(() => expect(pauseKind).toHaveBeenCalledWith('clinical.synthesis'));
  });

  it('offers resume rather than pause on a kind that is already paused', async () => {
    getQueueHealth.mockResolvedValue(health({ 'patient.sms': { paused: true } }));
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    await user.click(screen.getByTestId('pause-patient.sms'));
    await user.click(screen.getByTestId('confirm-pause-patient.sms'));
    await waitFor(() => expect(resumeKind).toHaveBeenCalledWith('patient.sms'));
    expect(pauseKind).not.toHaveBeenCalled();
  });
});

describe('a 409 is a colleague, not a fault', () => {
  function conflict(code: string) {
    return new ApiError({
      status: 409,
      code,
      kind: 'conflict',
      messageEN: 'from the server',
      messageBN: 'সার্ভার থেকে',
      correlationID: 'req_test',
    });
  }

  it('explains an already-paused kind and reads the page again', async () => {
    pauseKind.mockRejectedValue(conflict('JOB_KIND_ALREADY_PAUSED'));
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');
    const readsBefore = getQueueHealth.mock.calls.length;

    await user.click(screen.getByTestId('pause-clinical.synthesis'));
    await user.click(screen.getByTestId('confirm-pause-clinical.synthesis'));

    // The sentence names what happened rather than saying "could not pause": an operator
    // told the first goes back to work, and one told the second goes looking for a bug.
    expect(await screen.findByText(en.jobs.conflict.JOB_KIND_ALREADY_PAUSED)).toBeInTheDocument();
    expect(screen.getByText(en.jobs.conflict.body)).toBeInTheDocument();
    // And the screen is current again, so the row under the sentence agrees with it.
    await waitFor(() => expect(getQueueHealth.mock.calls.length).toBeGreaterThan(readsBefore));
  });

  it('explains a kind somebody else has already resumed', async () => {
    getQueueHealth.mockResolvedValue(health({ 'patient.sms': { paused: true } }));
    resumeKind.mockRejectedValue(conflict('JOB_KIND_NOT_PAUSED'));
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    await user.click(screen.getByTestId('pause-patient.sms'));
    await user.click(screen.getByTestId('confirm-pause-patient.sms'));

    expect(await screen.findByText(en.jobs.conflict.JOB_KIND_NOT_PAUSED)).toBeInTheDocument();
  });

  it('explains a job a colleague has already retried, and reads the list again', async () => {
    listJobs.mockResolvedValue([job()]);
    retryJob.mockRejectedValue(conflict('JOB_NOT_RETRYABLE'));
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId(`job-${JOB_ID}`);
    const readsBefore = listJobs.mock.calls.length;

    await user.click(screen.getByTestId(`retry-${JOB_ID}`));

    expect(await screen.findByText(en.jobs.conflict.JOB_NOT_RETRYABLE)).toBeInTheDocument();
    await waitFor(() => expect(listJobs.mock.calls.length).toBeGreaterThan(readsBefore));
  });

  it('explains a job a worker has already started, when somebody tries to cancel it', async () => {
    getJob.mockResolvedValue(job({ status: 'AVAILABLE', attempt: 0, met_sla: null }));
    listJobs.mockResolvedValue([job({ status: 'AVAILABLE', attempt: 0, met_sla: null })]);
    cancelJob.mockRejectedValue(conflict('JOB_NOT_CANCELLABLE'));
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId(`open-${JOB_ID}`));
    await user.click(await screen.findByTestId('start-cancel'));
    await user.type(screen.getByTestId('cancel-reason'), 'duplicate of an earlier request');
    await user.click(screen.getByTestId('confirm-cancel'));

    expect(await screen.findByText(en.jobs.conflict.JOB_NOT_CANCELLABLE)).toBeInTheDocument();
  });

  it('tells the four conflicts apart from an ordinary failure', () => {
    for (const code of surface.CONFLICT_CODES) {
      expect(surface.conflictOf(conflict(code))).toBe(code);
    }
    // An idempotency clash is also a 409 and is a client bug rather than a colleague, so it
    // must not be reported as one.
    expect(surface.conflictOf(conflict('IDEMPOTENCY_KEY_REUSED'))).toBeNull();
    expect(surface.conflictOf(new Error('network'))).toBeNull();
  });

  it('reports a real failure as a failure', async () => {
    pauseKind.mockRejectedValue(new Error('the server did not answer'));
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    await user.click(screen.getByTestId('pause-clinical.synthesis'));
    await user.click(screen.getByTestId('confirm-pause-clinical.synthesis'));

    expect(await screen.findByText(en.jobs.controlFailed)).toBeInTheDocument();
  });
});

describe('the dead letters, and what somebody opens them to read', () => {
  it('opens on what has been given up on rather than on the whole queue', async () => {
    listJobs.mockResolvedValue([job()]);

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('job-list-DISCARDED');

    expect(listJobs).toHaveBeenCalledWith('DISCARDED', expect.objectContaining({ kind: null }));
  });

  it('says the list is not bounded by the window the rates are', async () => {
    // The trap: a health row can read "nothing given up on in the last hour" while forty
    // dead letters from last night are sitting below it. Both are true, and a reader who
    // believed they were the same set would conclude the screen was lying.
    listJobs.mockResolvedValue([job()]);
    renderWithProviders(<JobsConsole />);

    expect(await screen.findByText(en.jobs.deadLetters.lead)).toBeInTheDocument();
  });

  it('shows every attempt with its error and how long it ran', async () => {
    listJobs.mockResolvedValue([job()]);
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId(`open-${JOB_ID}`));

    const attempts = await screen.findByTestId('attempts');
    expect(within(attempts).getAllByRole('listitem')).toHaveLength(2);
    expect(attempts).toHaveTextContent('inference gateway refused the connection');
    expect(attempts).toHaveTextContent('inference gateway timed out');
    // Which pod held it — a lease held by "worker" tells nobody which of six is wedged.
    expect(attempts).toHaveTextContent('worker-pod-3');
    // And how long each ran, which is the fact that separates "failed before it started"
    // from "failed part-way through". Milliseconds survive below a second.
    expect(attempts).toHaveTextContent('42 ms');
    expect(attempts).toHaveTextContent('4 min');
  });

  it('draws a missed deadline as a miss', async () => {
    listJobs.mockResolvedValue([job()]);
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId(`open-${JOB_ID}`));

    expect(await screen.findByTestId('met-sla')).toHaveTextContent(en.jobs.fact.metNo);
  });

  it('does not draw a met-or-missed answer while there is not one', async () => {
    // Null while unfinished and for a kind with no deadline, and neither of those is a
    // miss. A screen that rendered null as "no" would report a breach that never happened.
    getJob.mockResolvedValue(job({ status: 'AVAILABLE', met_sla: null, finished_at: undefined }));
    listJobs.mockResolvedValue([job({ status: 'AVAILABLE', met_sla: null })]);
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId(`open-${JOB_ID}`));
    await screen.findByTestId('job-detail');

    expect(screen.queryByTestId('met-sla')).not.toBeInTheDocument();
  });

  it('labels a cancellation reason as a reason and not as an error', async () => {
    // The server writes the reason into `last_error`. Drawn under the word "error" it would
    // show an operator their own sentence as though the software had complained.
    listJobs.mockResolvedValue([
      job({ status: 'CANCELLED', last_error: 'the clinic closed early' }),
    ]);

    renderWithProviders(<JobsConsole />);
    const row = await screen.findByTestId(`error-${JOB_ID}`);

    expect(row).toHaveTextContent(en.jobs.cancelledBecause);
    expect(row).not.toHaveTextContent(en.jobs.lastError);
  });

  it('retries from the list and reads everything again rather than trusting the response', async () => {
    /*
     * The response body is now the whole job — the handler re-reads the row rather than
     * assembling a struct from the UPDATE's five returned columns. What it still cannot
     * describe is everything *else* a retry moves: the dead-letter list it just left, its
     * kind's depth, and the count of kinds needing a look above the table. Three of the four
     * reads on this page change and the response knows about one, so the screen refetches
     * and there is one path producing what it shows.
     */
    listJobs.mockResolvedValue([job()]);
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId(`job-${JOB_ID}`);
    const readsBefore = listJobs.mock.calls.length;

    await user.click(screen.getByTestId(`retry-${JOB_ID}`));

    await waitFor(() => expect(retryJob).toHaveBeenCalledWith(JOB_ID));
    await waitFor(() => expect(listJobs.mock.calls.length).toBeGreaterThan(readsBefore));
    expect(await screen.findByText(en.jobs.done.retried)).toBeInTheDocument();
  });

  it('will not send a cancellation with nothing said', async () => {
    getJob.mockResolvedValue(job({ status: 'AVAILABLE', met_sla: null }));
    listJobs.mockResolvedValue([job({ status: 'AVAILABLE', met_sla: null })]);
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId(`open-${JOB_ID}`));
    await user.click(await screen.findByTestId('start-cancel'));

    // The server refuses an empty reason with a 422, and a form that lets somebody press the
    // button and then shows them a validation error has taught them nothing and cost them a
    // round trip on a clinic's connection.
    expect(screen.getByTestId('confirm-cancel')).toBeDisabled();
    await user.type(screen.getByTestId('cancel-reason'), '   ');
    expect(screen.getByTestId('confirm-cancel')).toBeDisabled();

    await user.type(screen.getByTestId('cancel-reason'), 'the clinic closed early');
    expect(screen.getByTestId('confirm-cancel')).toBeEnabled();
    await user.click(screen.getByTestId('confirm-cancel'));
    await waitFor(() =>
      expect(cancelJob).toHaveBeenCalledWith(JOB_ID, expect.stringContaining('closed early')),
    );
  });

  it('reaches one kind’s dead letters from its row', async () => {
    getQueueHealth.mockResolvedValue(
      health({
        'pipeline.ocr': {
          succeeded: 9,
          discarded: 1,
          dead_letters: 1,
          failure_rate: 10,
          seconds_since_last_finish: 60,
        },
      }),
    );
    listJobs.mockResolvedValue([job({ id: OTHER_JOB, kind: 'pipeline.ocr' })]);
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId('open-dead-pipeline.ocr'));

    await waitFor(() =>
      expect(listJobs).toHaveBeenCalledWith(
        'DISCARDED',
        expect.objectContaining({ kind: 'pipeline.ocr' }),
      ),
    );
  });

  it('opens the jobs behind the attainment figure', async () => {
    /*
     * The last dead end on the row, and the one that mattered most. "63.6% on time, 14 of 22
     * met it" is §7.1 measured rather than asserted — but a physician saying the summary was
     * not ready at 10:15 needs one of the eight, and they are reachable no other way: they
     * succeeded, so they are not among the dead letters, and they finished, so they are not
     * waiting. The list asks for `sla=missed` with **no status** for exactly that reason.
     */
    getQueueHealth.mockResolvedValue(
      health({
        'clinical.synthesis': {
          succeeded: 22,
          failure_rate: 0,
          sla_measured: 22,
          sla_met: 14,
          sla_attainment: 63.6,
          seconds_since_last_finish: 60,
        },
      }),
    );
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId('open-missed-clinical.synthesis'));

    await waitFor(() =>
      expect(listJobs).toHaveBeenCalledWith(
        'MISSED',
        expect.objectContaining({ kind: 'clinical.synthesis' }),
      ),
    );
    expect(await screen.findByText(en.jobs.missed.title)).toBeInTheDocument();
  });

  it('sends sla=missed and no status, because a miss crosses statuses', async () => {
    // Narrowing by status would drop half the answer: a job that missed its deadline may
    // have succeeded or been given up on.
    const { listJobs: realListJobs } = await vi.importActual<
      typeof import('@/features/jobs/api/jobs')
    >('@/features/jobs/api/jobs');
    const get = vi
      .fn()
      .mockResolvedValue({ data: { jobs: [] }, response: new Response(null, { status: 200 }) });
    const { api } = await import('@/lib/api');
    const spy = vi.spyOn(api, 'GET').mockImplementation(get);

    await realListJobs('MISSED', { kind: 'clinical.synthesis', limit: 50 });

    expect(get).toHaveBeenCalledWith(
      '/v1/ops/jobs',
      expect.objectContaining({
        params: { query: { sla: 'missed', kind: 'clinical.synthesis', limit: 50 } },
      }),
    );
    spy.mockRestore();
  });

  it('offers no way through when every measured job met its deadline', async () => {
    getQueueHealth.mockResolvedValue(
      health({
        'clinical.synthesis': {
          succeeded: 22,
          failure_rate: 0,
          sla_measured: 22,
          sla_met: 22,
          sla_attainment: 100,
          seconds_since_last_finish: 60,
        },
      }),
    );

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('sla-clinical.synthesis');
    expect(screen.queryByTestId('open-missed-clinical.synthesis')).not.toBeInTheDocument();
    expect(
      surface.missedDeadlines(silent('clinical.synthesis', { sla_measured: 22, sla_met: 22 })),
    ).toBe(0);
  });

  it('marks a job that missed, since a successful late job looks like an on-time one', async () => {
    listJobs.mockResolvedValue([
      job({ status: 'SUCCEEDED', met_sla: false, last_error: undefined, attempt: 1 }),
    ]);

    renderWithProviders(<JobsConsole />);
    const row = await screen.findByTestId(`job-${JOB_ID}`);

    expect(within(row).getByTestId(`missed-${JOB_ID}`)).toHaveTextContent(
      en.jobs.missedItsDeadline,
    );
    // And it carries the fact as data too, which is what the rail is drawn from.
    expect(row).toHaveAttribute('data-missed', 'true');
  });

  it('reaches what is running for one kind from the running count', async () => {
    /*
     * The depth had a way through from the first version of this page and the running count
     * did not, which made the busiest column on a failing row the only dead end on it. "Two
     * running" is a number an operator can do nothing with; the two jobs, with how long each
     * has been held and by which worker, is the difference between a hard job and a wedged
     * pod.
     */
    getQueueHealth.mockResolvedValue(health({ 'pipeline.ocr': { running: 2 } }));
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId('open-running-pipeline.ocr'));

    await waitFor(() =>
      expect(listJobs).toHaveBeenCalledWith(
        'RUNNING',
        expect.objectContaining({ kind: 'pipeline.ocr' }),
      ),
    );
    expect(await screen.findByText(en.jobs.running.title)).toBeInTheDocument();
  });

  it('offers no way through on a count of zero', async () => {
    // A control that opens an empty list is a control that wastes a press during an
    // incident. Nothing running is drawn as a number and nothing else.
    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');
    expect(screen.queryByTestId('open-running-pipeline.ocr')).not.toBeInTheDocument();
  });

  it('reaches what is waiting for one kind from its depth', async () => {
    getQueueHealth.mockResolvedValue(
      health({
        'patient.sms': { available: 4, oldest_available_seconds: 200, oldest_due_seconds: 200 },
      }),
    );
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await user.click(await screen.findByTestId('open-queue-patient.sms'));

    await waitFor(() =>
      expect(listJobs).toHaveBeenCalledWith(
        'AVAILABLE',
        expect.objectContaining({ kind: 'patient.sms' }),
      ),
    );
  });
});

describe('the window', () => {
  it('is stated in words rather than left to the reader to assume', async () => {
    renderWithProviders(<JobsConsole />);
    const sentence = await screen.findByTestId('window-sentence');
    expect(sentence).toHaveTextContent('1 hour');
  });

  it('is captioned with the window the server used, not the one the screen asked for', async () => {
    /*
     * The endpoint refuses an out-of-range window with a 422 now rather than clamping it, so
     * this is no longer guarding against a sharp edge in the API. It is kept because the
     * property is worth having on its own: a caption rendered from the request rather than
     * from the response is correct numbers under a heading nobody computed, and that is the
     * kind of wrong nobody notices. The picker cannot produce the 422 either — see below.
     */
    getQueueHealth.mockResolvedValue({ ...health(), window_seconds: 3600 });
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />);
    await screen.findByTestId('queue-health');

    await user.click(screen.getByTestId('window-900'));
    await waitFor(() => expect(getQueueHealth).toHaveBeenCalledWith(900));

    expect(await screen.findByTestId('window-sentence')).toHaveTextContent('1 hour');
  });

  it('offers nothing the server would refuse', () => {
    for (const choice of surface.WINDOW_CHOICES) {
      expect(surface.windowIsAcceptable(choice), String(choice)).toBe(true);
    }
    expect(surface.windowIsAcceptable(30)).toBe(false);
    expect(surface.windowIsAcceptable(90_000)).toBe(false);
  });

  it('reads a duration as the largest unit that still says something', () => {
    expect(surface.ageParts(0)).toEqual({ unit: 'seconds', value: 0 });
    expect(surface.ageParts(59)).toEqual({ unit: 'seconds', value: 59 });
    expect(surface.ageParts(3599)).toEqual({ unit: 'minutes', value: 59 });
    expect(surface.ageParts(7200)).toEqual({ unit: 'hours', value: 2 });
    expect(surface.ageParts(180_000)).toEqual({ unit: 'days', value: 2 });
    // The never sentinel is not a duration and is never passed here, but a negative must
    // not become a negative age if it ever is.
    expect(surface.ageParts(-1)).toEqual({ unit: 'seconds', value: 0 });
  });

  it('keeps milliseconds below a second, because that is the diagnostic difference', () => {
    expect(surface.ranForParts(42)).toEqual({ unit: 'ms', value: 42 });
    expect(surface.ranForParts(4200)).toEqual({ unit: 'seconds', value: 4.2 });
    expect(surface.ranForParts(240_000)).toEqual({ unit: 'minutes', value: 4 });
  });
});

describe('both languages', () => {
  it('renders the table in Bangla, including the states and the absent rates', async () => {
    getQueueHealth.mockResolvedValue(health({ 'patient.sms': { paused: true } }));
    listKinds.mockResolvedValue(kinds({ 'patient.sms': { paused: true } }));

    renderWithProviders(<JobsConsole />, { locale: 'bn' });
    await screen.findByTestId('queue-health');

    expect(screen.getByText(bn.jobs.column.kind)).toBeInTheDocument();
    expect(screen.getByTestId('state-paused')).toHaveTextContent(bn.jobs.state.paused);
    expect(screen.getByTestId('paused-patient.sms')).toHaveTextContent(bn.jobs.pausedNote);
    expect(screen.getByTestId('failures-pipeline.ocr')).toHaveTextContent(bn.jobs.nothingFinished);
    expect(screen.getByTestId('last-finish-clinical.synthesis')).toHaveTextContent(
      bn.jobs.neverFinished,
    );
  });

  it('renders a dead letter and its attempts in Bangla', async () => {
    listJobs.mockResolvedValue([job()]);
    const user = userEvent.setup();

    renderWithProviders(<JobsConsole />, { locale: 'bn' });
    expect(await screen.findByText(bn.jobs.deadLetters.title)).toBeInTheDocument();

    await user.click(await screen.findByTestId(`open-${JOB_ID}`));
    expect(await screen.findByText(bn.jobs.attempts.title)).toBeInTheDocument();
    // The description comes from the server in both languages; the Bangla screen shows the
    // Bangla one rather than falling back to the identifier.
    expect(screen.getByTestId('job-detail')).toHaveTextContent('কাজটি করা');
  });

  it('writes every count on the Bangla page in Bengali numerals', async () => {
    /*
     * The defect this catches is the one a Bengali reader sees before anything else: a
     * sentence with two numeral systems in it. It happens because ICU formats the `#` of a
     * plural for the locale and leaves a bare `{part}` alone, so "৩৮ মিনিট" and "22টির মধ্যে
     * 14টি" ended up side by side on the same row. Every count here is `{x, number}` for
     * that reason.
     *
     * Dates are deliberately exempt and are not asserted on: `formatters.ts` keeps them in
     * ASCII in both languages, because a timestamp on this page is read next to a log line.
     */
    getQueueHealth.mockResolvedValue(
      health({
        'pipeline.ocr': {
          available: 12,
          oldest_available_seconds: 2280,
          oldest_due_seconds: 2280,
          running: 2,
          succeeded: 30,
          discarded: 1,
          dead_letters: 1,
          failure_rate: 3.2,
          seconds_since_last_finish: 15,
        },
      }),
    );

    renderWithProviders(<JobsConsole />, { locale: 'bn' });
    await screen.findByTestId('queue-health');

    for (const testId of ['attention-summary', 'age-pipeline.ocr', 'failures-pipeline.ocr']) {
      const text = screen.getByTestId(testId).textContent ?? '';
      expect(text, `${testId} should carry Bengali numerals`).toMatch(/[০-৯]/);
      expect(text, `${testId} should carry no Latin numerals`).not.toMatch(/[0-9]/);
    }
  });

  it('has no English left anywhere in the namespace', () => {
    const untranslated = Object.keys(flatten(en.jobs as Record<string, unknown>)).filter(
      (key) =>
        flatten(en.jobs as Record<string, unknown>)[key] ===
        flatten(bn.jobs as Record<string, unknown>)[key],
    );
    expect(untranslated, `Identical in both languages: ${untranslated.join(', ')}`).toEqual([]);
  });
});

describe('there is no way to put work on the queue', () => {
  it('exports nothing that enqueues, schedules or creates a job', () => {
    /*
     * ADR-0031's criterion 1 made transactional enqueue structural rather than remembered:
     * work is enqueued by the code that decided it was needed, inside the transaction that
     * made that decision true. There is no endpoint, and an operator console with a "run
     * this now" button would be the way around it that somebody eventually used because it
     * was convenient — so the absence is asserted rather than left to habit.
     */
    const offenders = Object.keys(surface).filter((name) =>
      /enqueue|schedule|createJob|runNow|trigger/i.test(name),
    );
    expect(offenders, `The queue has grown a way in: ${offenders.join(', ')}`).toEqual([]);
  });

  it('has no message offering to start a job either', () => {
    // A component can be added without touching the API module. The vocabulary is checked
    // as data, the way the corrections and quality features check theirs.
    const offenders = Object.entries(flatten(en.jobs as Record<string, unknown>))
      .filter(([, value]) => /\benqueue\b|run it now|start a job|add a job/i.test(value))
      .map(([key]) => key);
    expect(offenders, `Messages offering to enqueue: ${offenders.join(', ')}`).toEqual([]);
  });

  it('has something to check', () => {
    // Guards both audits from passing vacuously if the barrel or the namespace emptied.
    expect(Object.keys(surface).length).toBeGreaterThan(20);
    expect(Object.keys(flatten(en.jobs as Record<string, unknown>)).length).toBeGreaterThan(50);
  });
});

/* ------------------------------------------------------------------------- */

function flatten(tree: Record<string, unknown>, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object') {
      Object.assign(out, flatten(value as Record<string, unknown>, path));
    } else {
      out[path] = String(value);
    }
  }
  return out;
}
