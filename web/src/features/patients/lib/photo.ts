/**
 * Preparing a photograph in the browser before it is uploaded (CP34).
 *
 * The resize happens on the client, and the reason is not bandwidth. A clinic's uplink is
 * shared with every other station, and a four-megabyte photograph from a modern phone camera
 * takes long enough on it that the operator gives up and the patient is registered without
 * one. Six hundred and forty pixels is more than a face needs on any screen this system has.
 *
 * Everything here is Canvas and the File API — no library. A dependency for a resize is a
 * dependency to keep patched, and this is thirty lines.
 */

/** The longest edge of a stored photograph. A face at 640 is legible on a wall display. */
export const MAX_EDGE = 640;

/** What the API will accept. */
export const ACCEPTED = ['image/jpeg', 'image/png', 'image/webp'] as const;

/** Eight megabytes, matching the server's ceiling — stated so a file can be refused early. */
export const MAX_BYTES = 8 * 1024 * 1024;

export interface PreparedPhoto {
  blob: Blob;
  contentType: string;
  width: number;
  height: number;
}

export function isAccepted(type: string): boolean {
  return (ACCEPTED as readonly string[]).includes(type);
}

/** The dimensions a photograph will be stored at, keeping the aspect ratio. */
export function fitWithin(width: number, height: number, edge = MAX_EDGE) {
  const scale = Math.min(1, edge / Math.max(width, height));
  return { width: Math.round(width * scale), height: Math.round(height * scale) };
}

/**
 * Resize and re-encode.
 *
 * Always re-encoded as JPEG, even from a PNG: a phone's PNG of a face is several times the
 * size for no visible gain, and one output format means one thing to test.
 */
export async function preparePhoto(file: File): Promise<PreparedPhoto> {
  if (!isAccepted(file.type)) {
    throw new Error(`${file.type || 'that file'} is not a photograph this clinic stores`);
  }
  const bitmap = await createImageBitmap(file);
  try {
    const { width, height } = fitWithin(bitmap.width, bitmap.height);

    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('this browser cannot prepare a photograph');
    context.drawImage(bitmap, 0, 0, width, height);

    const blob = await new Promise<Blob | null>((resolve) =>
      // 0.85 is where JPEG stops being visibly lossy on a face and the file is about a
      // tenth of the original.
      canvas.toBlob(resolve, 'image/jpeg', 0.85),
    );
    if (!blob) throw new Error('this browser cannot prepare a photograph');
    return { blob, contentType: 'image/jpeg', width, height };
  } finally {
    bitmap.close();
  }
}
