import { ApiError, NetworkError } from '@dthcms/api-client';

import { seeded, type FakeClinic } from '../fake-sync-server';

import type { SyncTransport } from '../../src/lib/sync/transport';

/**
 * The chaos harness (CP68): a clinic that answers badly, on purpose, reproducibly.
 *
 * # Why reproducibility is the first requirement and not a nicety
 *
 * Randomised failure injection that cannot be replayed produces a red build with a stack trace
 * nobody can reach twice. The engineer's only options are to stare at it or to re-run until it goes
 * green, and the second is what actually happens — after which the suite has taught everybody that
 * red means "try again", which is worse than having no chaos test at all. CP68's own risk note
 * names flaky device tests eroding trust; this is the same erosion arriving through the server.
 *
 * So **everything** random in a chaotic run comes from one integer. Three streams are derived from
 * it — what the network does, what the clinic decides about individual events, and the engine's own
 * backoff jitter — so that adding a rejection does not shift the network's decisions and make a
 * previously-recorded failure unreachable. Nothing consults `Math.random`, nothing consults the
 * wall clock, and a failed run carries the sentence needed to repeat it.
 *
 * # Slowness is charged to the injected clock, never to a real timer
 *
 * A three-second latency implemented with a real `setTimeout` would put ten real minutes into the
 * §13.10 matrix and would make every timing assertion a race against the CI machine's load. The
 * engine takes its clock as a dependency for exactly this reason, so a slow answer advances that
 * clock and returns immediately. What the engine experiences is indistinguishable — time passed
 * during the request, its backoff arithmetic sees it — and the suite stays deterministic and fast.
 *
 * That is also why there is nothing here that sleeps: if a test in this directory ever needs a real
 * delay, something has stopped taking its clock as a dependency, and that is the bug.
 *
 * # What it can do to a request
 *
 * The five ways a clinic can answer badly, and each one is a case the engine has a written rule
 * for:
 *
 *   - **the request never arrives** — a corridor. The rows stay in flight and are asked about;
 *   - **the answer is lost** — the clinic did the work and the response died on the way back. The
 *     case `batch_id` and the receipt endpoint exist for, and the one a client that only survives
 *     the first kind has never been tested against;
 *   - **the connection is cut mid-batch** — the clinic opened the batch row, processed some of it
 *     and never closed it. A receipt that reports nothing and says `closed: false`;
 *   - **a server error, or a limiter's 429 with a `Retry-After`** — retryable, but one of them
 *     carries a number the client must obey rather than guess around;
 *   - **a slow answer** — three seconds of a bad connection, charged to the clock.
 *
 * And one thing it does to the clinic rather than to the network: **random per-event rejections**,
 * which is §13.10's "one event in fifty" turned into a rate.
 */

/** How badly the clinic behaves. Every field is a fraction of requests except the two named ms. */
export interface ChaosProfile {
  /** The request never arrives. §13.10's ten per cent, by default. */
  loss: number;
  /** The clinic does the work and the answer dies on the way back. */
  lostAnswer: number;
  /** A push is cut off part way through, leaving the batch open at the clinic. */
  cutMidBatch: number;
  /** A 5xx. */
  serverError: number;
  /** A 429 carrying a `Retry-After` the client must obey. */
  throttle: number;
  /** How often an answer is slow. */
  slow: number;
  /** How slow, in milliseconds. §13.10 asks for three seconds. */
  latencyMs: number;
  /** The share of events the clinic refuses outright. §13.10's one in fifty is 0.02. */
  rejectRate: number;
}

/** §13.10's own numbers: ten per cent packet loss, three seconds of latency, one refusal in fifty. */
export const FLAKY_NETWORK: ChaosProfile = {
  loss: 0.05,
  lostAnswer: 0.05,
  cutMidBatch: 0.02,
  serverError: 0.03,
  throttle: 0.02,
  slow: 0.3,
  latencyMs: 3_000,
  rejectRate: 0,
};

/** Nothing goes wrong. The baseline every scenario runs against unless it asks for weather. */
export const CALM: ChaosProfile = {
  loss: 0,
  lostAnswer: 0,
  cutMidBatch: 0,
  serverError: 0,
  throttle: 0,
  slow: 0,
  latencyMs: 0,
  rejectRate: 0,
};

export interface ChaosClock {
  advance(ms: number): void;
}

export interface ChaosOptions {
  seed: number;
  profile?: Partial<ChaosProfile>;
  /** The clock a slow answer is charged to. */
  clock: ChaosClock;
}

export interface Chaos {
  readonly seed: number;
  readonly profile: ChaosProfile;
  /** The engine's jitter, from the same seed, so a whole run is one integer. */
  readonly random: () => number;
  /**
   * Point the weather at a clinic, and wrap its transport.
   *
   * The clinic is needed as well as the transport because two of the five failures are the
   * clinic's rather than the wire's: a refusal is a decision about an event, and a connection cut
   * half way through a batch has to leave the batch row **open** at the clinic — a wrapper that
   * threw on its own would leave no batch row at all, which is a different failure the client is
   * already right about.
   */
  attach(clinic: FakeClinic): SyncTransport;
  /** What actually happened, in order. Short strings; a failure prints the first few. */
  readonly log: string[];
  /** The sentence a failing assertion carries. Everything needed to see this run again. */
  replay(): string;
}

export function createChaos(options: ChaosOptions): Chaos {
  const profile: ChaosProfile = { ...CALM, ...options.profile };
  // Three streams from one seed. Mixing them would mean that changing the rejection rate silently
  // changed which requests were dropped, so a run recorded against one profile could never be
  // reproduced after somebody adjusted an unrelated number.
  const network = seeded(options.seed);
  const clinicStream = seeded(options.seed ^ 0x9e3779b9);
  const jitter = seeded(options.seed ^ 0x85ebca6b);
  const log: string[] = [];
  let requests = 0;

  function chance(rate: number): boolean {
    return rate > 0 && network() < rate;
  }

  const chaos: Chaos = {
    seed: options.seed,
    profile,
    random: jitter,
    log,

    attach(clinic) {
      if (profile.rejectRate > 0) {
        clinic.rejectWhen = (event) => {
          if (clinicStream() >= profile.rejectRate) return null;
          log.push(`refuse ${event.event_id}`);
          return {
            code: 'INVALID_PAYLOAD',
            reason: 'the clinic would not take that value as sent',
          };
        };
      }
      const base = clinic.transport();
      async function weather<T>(phase: string, call: () => Promise<T>): Promise<T> {
        requests += 1;
        const at = `${phase}#${requests}`;

        if (chance(profile.loss)) {
          log.push(`${at} never arrived`);
          throw new NetworkError(new Error('the request never arrived'));
        }
        if (chance(profile.slow)) {
          // Charged to the clock rather than to the wall. See the header: a real sleep here would
          // put minutes into the matrix and make every timing assertion a race.
          log.push(`${at} slow by ${profile.latencyMs}ms`);
          options.clock.advance(profile.latencyMs);
        }
        if (chance(profile.serverError)) {
          log.push(`${at} 503`);
          throw refusal(503, 'INTERNAL', 'the clinic is having a bad morning', null);
        }
        if (chance(profile.throttle)) {
          log.push(`${at} 429 for 30s`);
          throw refusal(429, 'RATE_LIMITED', 'too many requests from this device', 30);
        }
        if (chance(profile.lostAnswer)) {
          // The work happens and the answer dies. Everything about `batch_id` and the receipt
          // endpoint exists for this case, and a client that has only met the other kind has not
          // been tested.
          await call();
          log.push(`${at} answer lost`);
          throw new NetworkError(new Error('the response was lost'));
        }
        return call();
      }

      return {
        push: (body) =>
          weather('push', () => {
            if (chance(profile.cutMidBatch) && body.events.length > 1) {
              // Half the batch, and the row left open at the clinic. The receipt will say
              // `closed: false`, which is the client's instruction to send it again under the same
              // id rather than start a fresh one.
              clinic.crashAfter = Math.max(1, Math.floor(body.events.length / 2));
              log.push(`push#${requests} cut mid-batch`);
            }
            return base.push(body);
          }),
        receipt: (batchId) => weather('receipt', () => base.receipt(batchId)),
        pull: (since, limit) => weather('pull', () => base.pull(since, limit)),
        reference: () => weather('reference', () => base.reference()),
        state: () => weather('state', () => base.state()),
      };
    },

    replay() {
      const tail = log.slice(-6).join(' · ');
      return (
        `chaos seed ${options.seed} (${requests} requests). ` +
        `Repeat this exact run with DTHCMS_CHAOS_SEED=${options.seed}. Last events: ${tail}`
      );
    },
  };

  return chaos;
}

function refusal(
  status: number,
  code: string,
  message: string,
  retryAfterSeconds: number | null,
): ApiError {
  return new ApiError({
    status,
    code,
    kind: 'technical',
    messageEN: message,
    messageBN: message,
    correlationID: 'req_chaos',
    retryAfterSeconds,
  });
}
