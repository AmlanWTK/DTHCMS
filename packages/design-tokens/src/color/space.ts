/**
 * Colour space conversions, implemented here rather than taken from a library.
 *
 * Three reasons. The contrast and colour-blindness guarantees in this package are
 * assertions about patient safety, and an assertion is only as trustworthy as the code
 * behind it — this is eighty lines that can be read in full. The brand ramp is generated
 * rather than hand-picked, which needs OKLCH available at build time with no runtime
 * dependency. And a design token package that pulls in a colour library exports that
 * library's version conflicts to every application that consumes it.
 *
 * Everything is sRGB. Wide-gamut displays are not a consideration: the output has to be
 * legible on a low-end Android tablet in a clinic with fluorescent lighting, and on a
 * monochrome laser printer.
 */

export interface RGB {
  /** 0–1 */
  r: number;
  g: number;
  b: number;
}

export interface OKLCH {
  /** Perceptual lightness, 0–1. */
  l: number;
  /** Chroma, 0–~0.4 in sRGB. */
  c: number;
  /** Hue in degrees, 0–360. */
  h: number;
}

export interface OKLab {
  l: number;
  a: number;
  b: number;
}

const clamp01 = (value: number): number => Math.min(1, Math.max(0, value));

/** sRGB gamma decode: display value to light intensity. */
export function toLinear(channel: number): number {
  return channel <= 0.04045 ? channel / 12.92 : Math.pow((channel + 0.055) / 1.055, 2.4);
}

/** sRGB gamma encode: light intensity back to display value. */
export function toGamma(channel: number): number {
  return channel <= 0.0031308 ? channel * 12.92 : 1.055 * Math.pow(channel, 1 / 2.4) - 0.055;
}

export function linearize({ r, g, b }: RGB): RGB {
  return { r: toLinear(r), g: toLinear(g), b: toLinear(b) };
}

export function delinearize({ r, g, b }: RGB): RGB {
  return { r: toGamma(r), g: toGamma(g), b: toGamma(b) };
}

/**
 * Oklab, from Björn Ottosson's 2020 derivation.
 *
 * Used rather than CIELAB because its lightness axis matches perception closely enough
 * that a ramp built by stepping L looks evenly spaced, which is exactly what a token
 * ramp needs.
 */
export function linearRgbToOklab({ r, g, b }: RGB): OKLab {
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);

  return {
    l: 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    a: 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    b: 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  };
}

export function oklabToLinearRgb({ l, a, b }: OKLab): RGB {
  const lp = l + 0.3963377774 * a + 0.2158037573 * b;
  const mp = l - 0.1055613458 * a - 0.0638541728 * b;
  const sp = l - 0.0894841775 * a - 1.291485548 * b;

  const lc = lp * lp * lp;
  const mc = mp * mp * mp;
  const sc = sp * sp * sp;

  return {
    r: 4.0767416621 * lc - 3.3077115913 * mc + 0.2309699292 * sc,
    g: -1.2684380046 * lc + 2.6097574011 * mc - 0.3413193965 * sc,
    b: -0.0041960863 * lc - 0.7034186147 * mc + 1.707614701 * sc,
  };
}

export function oklchToOklab({ l, c, h }: OKLCH): OKLab {
  const radians = (h * Math.PI) / 180;
  return { l, a: c * Math.cos(radians), b: c * Math.sin(radians) };
}

export function oklabToOklch({ l, a, b }: OKLab): OKLCH {
  const c = Math.sqrt(a * a + b * b);
  let h = (Math.atan2(b, a) * 180) / Math.PI;
  if (h < 0) h += 360;
  return { l, c, h };
}

function inGamut({ r, g, b }: RGB): boolean {
  const epsilon = 1e-5;
  return (
    r >= -epsilon &&
    r <= 1 + epsilon &&
    g >= -epsilon &&
    g <= 1 + epsilon &&
    b >= -epsilon &&
    b <= 1 + epsilon
  );
}

/**
 * Convert OKLCH to sRGB, reducing chroma until the colour fits.
 *
 * Clipping each channel independently — the obvious approach — shifts hue, so a ramp
 * generated from one hue would drift as it saturates. Reducing chroma while holding
 * lightness and hue keeps the ramp recognisably one colour, which is the whole point of
 * generating it from a single hue value.
 */
export function oklchToRgb(color: OKLCH): RGB {
  const direct = oklabToLinearRgb(oklchToOklab(color));
  if (inGamut(direct)) {
    return delinearize({ r: clamp01(direct.r), g: clamp01(direct.g), b: clamp01(direct.b) });
  }

  let low = 0;
  let high = color.c;
  let result = direct;

  // Twenty halvings resolves chroma far below any perceptible step.
  for (let i = 0; i < 20; i += 1) {
    const mid = (low + high) / 2;
    const candidate = oklabToLinearRgb(oklchToOklab({ ...color, c: mid }));
    if (inGamut(candidate)) {
      low = mid;
      result = candidate;
    } else {
      high = mid;
    }
  }

  return delinearize({ r: clamp01(result.r), g: clamp01(result.g), b: clamp01(result.b) });
}

export function rgbToOklch(rgb: RGB): OKLCH {
  return oklabToOklch(linearRgbToOklab(linearize(rgb)));
}

export function toHex({ r, g, b }: RGB): string {
  const channel = (value: number): string =>
    Math.round(clamp01(value) * 255)
      .toString(16)
      .padStart(2, '0');
  return `#${channel(r)}${channel(g)}${channel(b)}`;
}

export function fromHex(hex: string): RGB {
  const normalised = hex.trim().replace(/^#/, '');
  const expanded =
    normalised.length === 3
      ? normalised
          .split('')
          .map((c) => c + c)
          .join('')
      : normalised;

  if (!/^[0-9a-fA-F]{6}$/.test(expanded)) {
    throw new Error(`"${hex}" is not a six-digit hex colour`);
  }

  return {
    r: parseInt(expanded.slice(0, 2), 16) / 255,
    g: parseInt(expanded.slice(2, 4), 16) / 255,
    b: parseInt(expanded.slice(4, 6), 16) / 255,
  };
}

/**
 * Perceptual distance in Oklab.
 *
 * Used to ask whether two clinical status colours remain distinguishable after
 * colour-blindness simulation. Roughly: 0.02 is a just-noticeable difference under good
 * conditions, 0.10 is comfortably different, 0.25 is obviously a different colour.
 */
export function perceptualDistance(a: RGB, b: RGB): number {
  const first = linearRgbToOklab(linearize(a));
  const second = linearRgbToOklab(linearize(b));
  return Math.sqrt(
    (first.l - second.l) ** 2 + (first.a - second.a) ** 2 + (first.b - second.b) ** 2,
  );
}
