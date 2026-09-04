import { describe, expect, it } from 'vitest';

import { MAX_BYTES, MAX_EDGE, fitWithin, isAccepted } from '@/features/patients';

/**
 * Preparing a photograph in the browser (CP34).
 *
 * The resize itself needs a canvas, so what is tested here is the arithmetic and the
 * refusals — the parts that decide whether a four-megabyte camera photograph reaches a
 * clinic's shared uplink at all.
 */

describe('fitting a photograph', () => {
  it('never enlarges a small one', () => {
    // Upscaling a 200px photograph to 640 makes the file larger and the face no clearer.
    expect(fitWithin(200, 150)).toEqual({ width: 200, height: 150 });
  });

  it('brings the longest edge down to the limit, keeping the shape', () => {
    expect(fitWithin(4000, 3000)).toEqual({ width: MAX_EDGE, height: 480 });
    expect(fitWithin(3000, 4000)).toEqual({ width: 480, height: MAX_EDGE });
    expect(fitWithin(4000, 4000)).toEqual({ width: MAX_EDGE, height: MAX_EDGE });
  });

  it('handles a very wide photograph without rounding to zero', () => {
    const fitted = fitWithin(6000, 100);
    expect(fitted.width).toBe(MAX_EDGE);
    expect(fitted.height).toBeGreaterThan(0);
  });
});

describe('what the clinic stores', () => {
  it('accepts the three image types the server accepts', () => {
    for (const type of ['image/jpeg', 'image/png', 'image/webp']) {
      expect(isAccepted(type)).toBe(true);
    }
  });

  it('refuses everything else before a byte is uploaded', () => {
    for (const type of ['application/pdf', 'image/gif', 'video/mp4', '']) {
      expect(isAccepted(type)).toBe(false);
    }
  });

  it('states the same ceiling the server enforces', () => {
    // Mirrored so a file can be refused before a clinic's uplink is spent on it. The
    // server refuses it again, because a rule enforced only in a browser is not a rule.
    expect(MAX_BYTES).toBe(8 * 1024 * 1024);
  });
});
