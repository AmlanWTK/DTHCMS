/**
 * Capturing a signature or a thumbprint (CP36).
 *
 * The strokes are held as points and rendered to a canvas only when the consent is taken.
 * Keeping the geometry rather than only the pixels means "clear the last stroke" is possible
 * and a redraw at a different pixel ratio is not a resample of a resample — on a tablet where
 * a signature is drawn with a finger, both matter.
 *
 * PNG, always. A signature is line art on a transparent ground; JPEG artefacts around thin
 * strokes are exactly what makes a signature arguable later.
 */

export interface Point {
  x: number;
  y: number;
}

export type Stroke = Point[];

/** A signature with fewer points than this is a tap, not a mark. */
export const MIN_POINTS = 8;

export function hasMark(strokes: Stroke[]): boolean {
  return strokes.reduce((total, stroke) => total + stroke.length, 0) >= MIN_POINTS;
}

/** The ink's bounding box, or null when there is none. */
export function bounds(strokes: Stroke[]): { x: number; y: number; w: number; h: number } | null {
  const points = strokes.flat();
  if (points.length === 0) return null;
  const xs = points.map((p) => p.x);
  const ys = points.map((p) => p.y);
  const x = Math.min(...xs);
  const y = Math.min(...ys);
  return { x, y, w: Math.max(...xs) - x, h: Math.max(...ys) - y };
}

/**
 * Draw the strokes onto a canvas context.
 *
 * Round caps and joins, because a signature drawn with square ends looks like a diagram and
 * a clinician asked to confirm one wants it to look like a signature.
 */
export function draw(
  ctx: CanvasRenderingContext2D,
  strokes: Stroke[],
  options: { width: number; height: number; colour: string; lineWidth?: number },
): void {
  ctx.clearRect(0, 0, options.width, options.height);
  ctx.lineWidth = options.lineWidth ?? 2.5;
  ctx.lineCap = 'round';
  ctx.lineJoin = 'round';
  ctx.strokeStyle = options.colour;
  for (const stroke of strokes) {
    if (stroke.length === 0) continue;
    ctx.beginPath();
    // A single point is a dot, which a stroke of zero length would not draw at all.
    if (stroke.length === 1) {
      const [only] = stroke;
      if (!only) continue;
      ctx.arc(only.x, only.y, (options.lineWidth ?? 2.5) / 2, 0, Math.PI * 2);
      ctx.fillStyle = options.colour;
      ctx.fill();
      continue;
    }
    const [first, ...rest] = stroke;
    if (!first) continue;
    ctx.moveTo(first.x, first.y);
    for (const point of rest) ctx.lineTo(point.x, point.y);
    ctx.stroke();
  }
}

/** The hex SHA-256 of a blob, computed in the browser so the server can check its own read. */
export async function digestOf(blob: Blob): Promise<string> {
  const buffer = await blob.arrayBuffer();
  const hash = await crypto.subtle.digest('SHA-256', buffer);
  return Array.from(new Uint8Array(hash))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('');
}
