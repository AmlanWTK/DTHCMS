import { delinearize, fromHex, linearize, perceptualDistance, toHex, type RGB } from './space.js';

/**
 * Colour-vision-deficiency simulation, using the Machado, Oliveira and Fernandes (2009)
 * matrices at full severity.
 *
 * Why this is in a clinical system rather than a nice-to-have: red–green deficiency
 * affects roughly one man in twelve. DTHC's stations will be operated by whoever is on
 * shift, and the physician reading a dashboard is one person. If "high" and "normal" are
 * distinguished only by red and green, then for one operator in twelve they are not
 * distinguished at all — and nothing about the interface reveals that. It looks like it
 * is working.
 *
 * The matrices operate on linear RGB, which is why the callers linearise first. Applying
 * them to gamma-encoded values — an easy mistake — produces a simulation that is roughly
 * the right hue and quite wrong about how similar two colours become.
 */

export type VisionType = 'protanopia' | 'deuteranopia' | 'tritanopia';

type Matrix = readonly [number, number, number, number, number, number, number, number, number];

const MATRICES: Record<VisionType, Matrix> = {
  // No functioning long-wavelength cones. Reds darken sharply.
  protanopia: [
    0.152286, 1.052583, -0.204868, 0.114503, 0.786281, 0.099216, -0.003882, -0.048116, 1.051998,
  ],
  // No functioning medium-wavelength cones. The most common form.
  deuteranopia: [
    0.367322, 0.860646, -0.227968, 0.280085, 0.672501, 0.047413, -0.01182, 0.04294, 0.968881,
  ],
  // No functioning short-wavelength cones. Rare, and affects blue–yellow.
  tritanopia: [
    1.255528, -0.076749, -0.178779, -0.078411, 0.930809, 0.147602, 0.004733, 0.691367, 0.3039,
  ],
};

export function simulate(color: RGB, vision: VisionType): RGB {
  const m = MATRICES[vision];
  const { r, g, b } = linearize(color);

  return delinearize({
    r: Math.min(1, Math.max(0, m[0] * r + m[1] * g + m[2] * b)),
    g: Math.min(1, Math.max(0, m[3] * r + m[4] * g + m[5] * b)),
    b: Math.min(1, Math.max(0, m[6] * r + m[7] * g + m[8] * b)),
  });
}

export function simulateHex(hex: string, vision: VisionType): string {
  return toHex(simulate(fromHex(hex), vision));
}

export const VISION_TYPES: readonly VisionType[] = ['protanopia', 'deuteranopia', 'tritanopia'];

/**
 * The distance at which two colours are considered tellable apart.
 *
 * 0.10 in Oklab, not the 0.02 of a just-noticeable difference. Two colours a hair apart
 * are "different" in a laboratory and identical on a tablet held at arm's length in a
 * corridor. This threshold is about whether an operator glancing at a queue can tell two
 * statuses apart, not whether a colorimeter can.
 */
export const DISTINGUISHABLE = 0.1;

export interface Confusion {
  vision: VisionType;
  a: string;
  b: string;
  distance: number;
}

/**
 * Finds pairs of colours that collapse together for someone with a colour vision
 * deficiency.
 *
 * Note what this function is for. It is *not* a gate that the palette must pass — under
 * deuteranopia, red and green genuinely converge, and a palette that avoided every such
 * pair could not use red for critical values, which would be a worse interface for the
 * other eleven operators in twelve. It exists to produce the list of pairs that must
 * therefore be separated by something other than colour, and the test suite asserts that
 * every pair it finds has different icons and different labels.
 *
 * That inverts the usual accessibility check from "does the palette pass" to "is every
 * failure covered by a non-colour signal", which is the guarantee that actually matters.
 */
export function findConfusions(colors: Record<string, string>): Confusion[] {
  const names = Object.keys(colors).sort();
  const confusions: Confusion[] = [];

  for (const vision of VISION_TYPES) {
    for (let i = 0; i < names.length; i += 1) {
      for (let j = i + 1; j < names.length; j += 1) {
        const nameA = names[i];
        const nameB = names[j];
        if (nameA === undefined || nameB === undefined) continue;

        const hexA = colors[nameA];
        const hexB = colors[nameB];
        if (hexA === undefined || hexB === undefined) continue;

        const distance = perceptualDistance(
          simulate(fromHex(hexA), vision),
          simulate(fromHex(hexB), vision),
        );

        if (distance < DISTINGUISHABLE) {
          confusions.push({ vision, a: nameA, b: nameB, distance });
        }
      }
    }
  }

  return confusions;
}
