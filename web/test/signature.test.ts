import { describe, expect, it, vi } from 'vitest';

import {
  MIN_POINTS,
  bounds,
  digestOf,
  draw,
  hasMark,
  type Stroke,
} from '@/features/consent/lib/signature';

/**
 * The mark a patient makes when they consent (CP36).
 *
 * This is the only part of a consent record that is evidence rather than metadata, and it is
 * the part a lawyer would look at years later. Four things go wrong here and none of them is
 * loud:
 *
 *  - a **tap** recorded as a signature. A finger brushing a tablet on a crowded desk leaves
 *    two or three points; if that counts as a mark, the clinic has a consent nobody gave.
 *  - a mark drawn but **not rendered** — an empty stroke, or a single point that a
 *    zero-length line silently draws as nothing — producing a blank PNG filed as a signature.
 *  - the ink measured as the **canvas** rather than as the strokes, which crops or stretches
 *    the signature when it is redrawn at a different pixel ratio.
 *  - a **digest** that does not match the bytes, which is what the server checks its own read
 *    against; a wrong one rejects a signature that arrived perfectly.
 *
 * So: what counts as a mark, where the ink is, what actually reaches the canvas, and whether
 * the hex is the hex.
 */

/** jsdom's canvas is not implemented, and a recorder says more than a real context would. */
function recordingContext() {
  const ctx = {
    calls: [] as string[],
    lineWidth: 0,
    lineCap: '',
    lineJoin: '',
    strokeStyle: '',
    fillStyle: '',
    clearRect: vi.fn((...args: number[]) => ctx.calls.push(`clearRect(${args.join(',')})`)),
    beginPath: vi.fn(() => ctx.calls.push('beginPath')),
    moveTo: vi.fn((x: number, y: number) => ctx.calls.push(`moveTo(${x},${y})`)),
    lineTo: vi.fn((x: number, y: number) => ctx.calls.push(`lineTo(${x},${y})`)),
    arc: vi.fn((x: number, y: number, r: number) => ctx.calls.push(`arc(${x},${y},${r})`)),
    fill: vi.fn(() => ctx.calls.push('fill')),
    stroke: vi.fn(() => ctx.calls.push('stroke')),
  };
  return ctx;
}

type Recorder = ReturnType<typeof recordingContext>;

function drawOn(ctx: Recorder, strokes: Stroke[], lineWidth?: number) {
  draw(ctx as unknown as CanvasRenderingContext2D, strokes, {
    width: 400,
    height: 160,
    colour: '#101828',
    ...(lineWidth === undefined ? {} : { lineWidth }),
  });
}

const line = (points: number) => Array.from({ length: points }, (_, i) => ({ x: i, y: i * 2 }));

describe('what counts as a mark', () => {
  it.each([
    { what: 'nothing drawn at all', strokes: [], marked: false },
    { what: 'a stroke that was started and abandoned', strokes: [[]], marked: false },
    { what: 'a single tap', strokes: [line(1)], marked: false },
    {
      what: 'a brush of the hand, one point short',
      strokes: [line(MIN_POINTS - 1)],
      marked: false,
    },
    { what: 'exactly the shortest deliberate mark', strokes: [line(MIN_POINTS)], marked: true },
    {
      what: 'a name written in several strokes',
      strokes: [line(3), line(3), line(4)],
      marked: true,
    },
    { what: 'a full signature', strokes: [line(60)], marked: true },
  ])('reads $what as marked=$marked', ({ strokes, marked }) => {
    expect(hasMark(strokes)).toBe(marked);
  });

  it('counts points across strokes, not strokes', () => {
    // A signature is one gesture to the person making it and several strokes to the tablet.
    // Counting strokes would refuse "S. Rahman" written with a lifted pen.
    expect(hasMark([line(4), line(4)])).toBe(true);
    expect(hasMark([line(4), line(3)])).toBe(false);
  });
});

describe('where the ink is', () => {
  it('has no bounds when there is no ink', () => {
    expect(bounds([])).toBeNull();
    expect(bounds([[], []])).toBeNull();
  });

  it('measures the strokes rather than the box they were drawn in', () => {
    // The box is 400×160. The signature is not, and a redraw that assumed it was would
    // stretch a name across the whole card.
    expect(
      bounds([
        [
          { x: 10, y: 20 },
          { x: 40, y: 60 },
        ],
      ]),
    ).toEqual({ x: 10, y: 20, w: 30, h: 40 });
  });

  it('spans every stroke, in whatever order they were drawn', () => {
    // The dot of an "i" is often the last stroke and the highest point on the card.
    expect(
      bounds([
        [
          { x: 50, y: 50 },
          { x: 60, y: 55 },
        ],
        [{ x: 12, y: 4 }],
        [{ x: 90, y: 70 }],
      ]),
    ).toEqual({ x: 12, y: 4, w: 78, h: 66 });
  });

  it('gives a zero-sized box for a mark made in one place', () => {
    // Not null: there *is* ink. A caller cropping to the bounds has to cope with a dot.
    expect(bounds([[{ x: 7, y: 9 }]])).toEqual({ x: 7, y: 9, w: 0, h: 0 });
  });
});

describe('what reaches the canvas', () => {
  it('wipes the whole card before redrawing it', () => {
    // Without this, "clear the last stroke" leaves the last stroke on screen.
    const ctx = recordingContext();
    drawOn(ctx, [line(3)]);
    expect(ctx.clearRect).toHaveBeenCalledWith(0, 0, 400, 160);
    expect(ctx.calls[0]).toBe('clearRect(0,0,400,160)');
  });

  it('draws with round caps and joins, so a signature looks like a signature', () => {
    // Square ends read as a diagram. A clinician asked to confirm a mark should be looking
    // at something that looks like ink.
    const ctx = recordingContext();
    drawOn(ctx, [line(3)]);
    expect(ctx.lineCap).toBe('round');
    expect(ctx.lineJoin).toBe('round');
    expect(ctx.strokeStyle).toBe('#101828');
    expect(ctx.lineWidth).toBe(2.5);
  });

  it('honours a thicker nib when one is asked for', () => {
    const ctx = recordingContext();
    drawOn(ctx, [line(3)], 6);
    expect(ctx.lineWidth).toBe(6);
  });

  it('joins the points of a stroke in the order they were made', () => {
    const ctx = recordingContext();
    drawOn(ctx, [
      [
        { x: 1, y: 2 },
        { x: 3, y: 4 },
        { x: 5, y: 6 },
      ],
    ]);
    expect(ctx.calls).toEqual([
      'clearRect(0,0,400,160)',
      'beginPath',
      'moveTo(1,2)',
      'lineTo(3,4)',
      'lineTo(5,6)',
      'stroke',
    ]);
  });

  it('draws a single point as a dot rather than as nothing', () => {
    // A zero-length line strokes no pixels. The dot on an "i" and a thumbprint's first
    // contact are both single points, and a signature missing them is a signature altered.
    const ctx = recordingContext();
    drawOn(ctx, [[{ x: 20, y: 30 }]], 5);
    expect(ctx.calls).toEqual(['clearRect(0,0,400,160)', 'beginPath', 'arc(20,30,2.5)', 'fill']);
    expect(ctx.fillStyle).toBe('#101828');
    expect(ctx.stroke).not.toHaveBeenCalled();
  });

  it('skips a stroke that was started and abandoned', () => {
    // An empty stroke happens whenever a pointerdown is followed by a pointerup with no
    // movement in between — every hesitation on a tablet.
    const ctx = recordingContext();
    drawOn(ctx, [[], [{ x: 1, y: 1 }]]);
    expect(ctx.beginPath).toHaveBeenCalledTimes(1);
  });

  it('draws every stroke of a multi-stroke name', () => {
    const ctx = recordingContext();
    drawOn(ctx, [line(2), line(2), [{ x: 9, y: 9 }]]);
    expect(ctx.beginPath).toHaveBeenCalledTimes(3);
    expect(ctx.stroke).toHaveBeenCalledTimes(2);
    expect(ctx.fill).toHaveBeenCalledTimes(1);
  });
});

describe('the digest the server checks its own read against', () => {
  /*
   * jsdom's Blob has no arrayBuffer(), so the bytes come from Node's. What is under test is
   * the hex, not the Blob — the browser supplies a real one.
   */
  async function blobOf(text: string): Promise<Blob> {
    const { Blob: NodeBlob } = await import('node:buffer');
    return new NodeBlob([new TextEncoder().encode(text)], {
      type: 'image/png',
    }) as unknown as Blob;
  }

  it('is the SHA-256 of the bytes, in lower-case hex', async () => {
    // The published digest of "abc". If this ever disagrees, the server rejects a signature
    // that arrived intact — and the patient is asked to sign again for no reason.
    await expect(digestOf(await blobOf('abc'))).resolves.toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    );
  });

  it('pads a byte below sixteen rather than dropping its zero', async () => {
    // The 0x01 in the digest above is the case that catches it: an unpadded join produces
    // 63 characters, which is not a SHA-256 of anything.
    const hex = await digestOf(await blobOf('abc'));
    expect(hex).toHaveLength(64);
    expect(hex).toMatch(/^[0-9a-f]{64}$/);
  });

  it('is stable for the same bytes and different for different ones', async () => {
    const [first, again, other] = await Promise.all([
      digestOf(await blobOf('signature')),
      digestOf(await blobOf('signature')),
      digestOf(await blobOf('signaturf')),
    ]);
    expect(first).toBe(again);
    expect(first).not.toBe(other);
  });

  it('has a digest for an empty image too', async () => {
    // An empty PNG is a bug elsewhere, but it must still hash rather than throw: the upload
    // fails on the server's own check, with a message, instead of on a client exception.
    await expect(digestOf(await blobOf(''))).resolves.toBe(
      'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    );
  });
});
