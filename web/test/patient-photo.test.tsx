import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { renderWithProviders } from './render';

const photoURL = vi.hoisted(() => vi.fn());
const uploadTicket = vi.hoisted(() => vi.fn());
const attachPhoto = vi.hoisted(() => vi.fn());

vi.mock('@/features/patients/api/patients', async () => {
  const actual = await vi.importActual<typeof import('@/features/patients/api/patients')>(
    '@/features/patients/api/patients',
  );
  return { ...actual, photoURL, uploadTicket, attachPhoto };
});

const { PatientPhoto } = await import('@/features/patients/components/PatientPhoto');
const { MAX_BYTES, preparePhoto } = await import('@/features/patients/lib/photo');

/**
 * The patient's photograph, from the camera to the frame on screen (CP34).
 *
 * Three rules govern this and none of them are visible in a screenshot.
 *
 * **The bytes never enter the API.** They are PUT straight to storage against a pre-signed
 * URL, and only the server's own object key comes back to the API afterwards. A photograph
 * that passes through the API process is a photograph that can turn up in a request log, a
 * crash dump or a proxy's buffer — which is where PHI images appear in incident reports.
 *
 * **Nothing is public and no URL is kept.** The displayed link is signed, minted per
 * request, and dies within fifteen minutes. A URL cached in a component outlives its
 * signature and then renders as a broken image with nothing on screen to say why, so the
 * screen has to ask again rather than remember.
 *
 * **The resize happens before the upload, and not for bandwidth.** A clinic's uplink is
 * shared with every other station; a four-megabyte camera photograph takes long enough on
 * it that the operator gives up and the patient is registered without one.
 */

const PATIENT = '5f1d3e2a-0000-4000-8000-000000000001';

const ticket = {
  object_key: 'patients/5f1d/photo/01926c.jpg',
  upload_url: 'https://storage.example.invalid/put?sig=abc',
  expires_at: '2026-09-04T10:15:00Z',
  max_bytes: MAX_BYTES,
  content_types: ['image/jpeg'],
};

const SIGNED = 'https://storage.example.invalid/get?sig=xyz';

/** A decoded photograph, without a decoder. `close` is asserted on: it frees the bitmap. */
function bitmap(width: number, height: number) {
  return { width, height, close: vi.fn() };
}

/**
 * jsdom has no canvas, so the two calls that need one are scripted. What is being tested is
 * the arithmetic and the refusals around them, not whether a browser can draw.
 */
function stubCanvas(options: { context?: boolean; blob?: Blob | null } = {}) {
  const drawImage = vi.fn();
  const toBlob = vi.fn();
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() =>
    options.context === false ? null : ({ drawImage } as unknown as CanvasRenderingContext2D),
  );
  vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((callback, type, quality) => {
    toBlob(type, quality);
    callback(
      options.blob === undefined
        ? new Blob([new Uint8Array(2048)], { type: 'image/jpeg' })
        : options.blob,
    );
  });
  return { drawImage, toBlob };
}

function file(name = 'face.jpg', type = 'image/jpeg', size?: number): File {
  const made = new File([new Uint8Array(64)], name, { type });
  // Defined rather than allocated: an eight-megabyte buffer in a unit test buys nothing.
  if (size !== undefined) Object.defineProperty(made, 'size', { value: size });
  return made;
}

beforeEach(() => {
  photoURL.mockReset();
  uploadTicket.mockReset();
  attachPhoto.mockReset();
  photoURL.mockResolvedValue({ url: SIGNED, expires_at: ticket.expires_at });
  uploadTicket.mockResolvedValue(ticket);
  attachPhoto.mockResolvedValue({ object_key: ticket.object_key, url: SIGNED });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('preparing a photograph before it is uploaded', () => {
  it('refuses a file that is not a photograph before anything is decoded', async () => {
    const decode = vi.fn();
    vi.stubGlobal('createImageBitmap', decode);

    await expect(preparePhoto(file('scan.pdf', 'application/pdf'))).rejects.toThrow(
      /application\/pdf is not a photograph/,
    );
    // Nothing was decoded, so nothing large was held in memory on a tablet.
    expect(decode).not.toHaveBeenCalled();
  });

  it('names the file rather than the empty string when the type is unknown', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn());
    await expect(preparePhoto(file('mystery', ''))).rejects.toThrow(
      /that file is not a photograph/,
    );
  });

  it('brings a camera photograph down to the stored size, keeping the shape', async () => {
    const decoded = bitmap(4000, 3000);
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(decoded));
    stubCanvas();

    const prepared = await preparePhoto(file());

    expect(prepared.width).toBe(640);
    expect(prepared.height).toBe(480);
    expect(decoded.close).toHaveBeenCalled();
  });

  it('re-encodes a PNG as JPEG, because one output format is one thing to test', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(bitmap(800, 800)));
    const canvas = stubCanvas();

    const prepared = await preparePhoto(file('face.png', 'image/png'));

    expect(prepared.contentType).toBe('image/jpeg');
    expect(prepared.blob.type).toBe('image/jpeg');
    // 0.85 is where JPEG stops being visibly lossy on a face; asserted so a later tidy-up
    // cannot quietly ship every clinic's photographs at full quality over a shared uplink.
    expect(canvas.toBlob).toHaveBeenCalledWith('image/jpeg', 0.85);
  });

  it('does not enlarge a photograph that is already small', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(bitmap(200, 150)));
    stubCanvas();

    const prepared = await preparePhoto(file());
    expect({ width: prepared.width, height: prepared.height }).toEqual({ width: 200, height: 150 });
  });

  it('says so plainly when the browser cannot give it a canvas', async () => {
    const decoded = bitmap(4000, 3000);
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(decoded));
    stubCanvas({ context: false });

    await expect(preparePhoto(file())).rejects.toThrow('this browser cannot prepare a photograph');
    // Released even on the way out: a bitmap left open on a tablet with several patients
    // registered in a row is a tab that dies mid-registration.
    expect(decoded.close).toHaveBeenCalled();
  });

  it('releases the decoded bitmap when the encode yields nothing', async () => {
    const decoded = bitmap(4000, 3000);
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(decoded));
    stubCanvas({ blob: null });

    await expect(preparePhoto(file())).rejects.toThrow('this browser cannot prepare a photograph');
    expect(decoded.close).toHaveBeenCalled();
  });
});

describe('the photograph on the patient screen', () => {
  it('shows a loading frame rather than an empty one while the URL is minted', () => {
    photoURL.mockReturnValue(new Promise(() => {}));
    renderWithProviders(<PatientPhoto patientID={PATIENT} />);

    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByRole('img')).toBeNull();
  });

  it('renders the signed storage URL itself, not a proxied copy of it', async () => {
    renderWithProviders(<PatientPhoto patientID={PATIENT} />);

    const image = await screen.findByRole('img', { name: "The patient's photograph" });
    // A plain <img>: next/image would proxy and cache the bytes on this origin, which is
    // exactly what a fifteen-minute signature exists to prevent.
    expect(image).toHaveAttribute('src', SIGNED);
    expect(image.getAttribute('src')).not.toContain('/_next/image');
    expect(await screen.findByRole('button', { name: 'Replace the photograph' })).toBeEnabled();
  });

  it('says there is no photograph rather than showing a broken frame', async () => {
    photoURL.mockRejectedValue(new Error('no photograph on file'));
    renderWithProviders(<PatientPhoto patientID={PATIENT} />);

    expect(await screen.findByText('No photograph')).toBeInTheDocument();
    expect(screen.queryByRole('img')).toBeNull();
    expect(screen.getByRole('button', { name: 'Take a photograph' })).toBeInTheDocument();
  });

  it('tells the operator the link expires and the photograph is never public', async () => {
    renderWithProviders(<PatientPhoto patientID={PATIENT} />);
    expect(await screen.findByText(/never public and the link expires/i)).toBeInTheDocument();
  });

  it('opens the camera from the visible button, not from a file field on the page', async () => {
    // The input is hidden and carries `capture="user"`, which is what makes a phone open the
    // camera rather than the gallery — the difference between taking a photograph of the
    // patient in front of you and finding one of somebody else.
    const user = userEvent.setup();
    const opened = vi.spyOn(HTMLInputElement.prototype, 'click').mockImplementation(() => {});

    renderWithProviders(<PatientPhoto patientID={PATIENT} />);
    await screen.findByRole('img');
    await user.click(screen.getByRole('button', { name: 'Replace the photograph' }));

    expect(opened).toHaveBeenCalledTimes(1);
    const input = screen.getByTestId('photo-input');
    expect(input).toHaveAttribute('capture', 'user');
    expect(input).toHaveAttribute('accept', 'image/jpeg,image/png,image/webp');
  });

  it('will not take a second photograph while the first is still going up', async () => {
    const user = userEvent.setup();
    let finish: (response: Response) => void = () => {};
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>((resolve) => (finish = resolve))),
    );
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(bitmap(1200, 900)));
    stubCanvas();

    renderWithProviders(<PatientPhoto patientID={PATIENT} />);
    await screen.findByRole('img');
    await user.upload(screen.getByTestId('photo-input'), file());

    // A second tap on a slow uplink is how one patient ends up with two objects and the
    // operator ends up unsure which face was stored.
    const button = await screen.findByRole('button', { name: 'Uploading…' });
    expect(button).toBeDisabled();

    finish(new Response(null, { status: 200 }));
    await waitFor(() => expect(attachPhoto).toHaveBeenCalled());
  });

  it('PUTs the bytes to storage and sends the API nothing but the key', async () => {
    const user = userEvent.setup();
    const put = vi.fn().mockResolvedValue(new Response(null, { status: 200 }));
    vi.stubGlobal('fetch', put);
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(bitmap(4000, 3000)));
    stubCanvas();

    renderWithProviders(<PatientPhoto patientID={PATIENT} />);
    await screen.findByRole('img');
    await user.upload(screen.getByTestId('photo-input'), file());

    await waitFor(() => expect(attachPhoto).toHaveBeenCalled());

    const [url, init] = put.mock.calls[0] as [string, RequestInit];
    expect(url).toBe(ticket.upload_url);
    expect(init.method).toBe('PUT');
    expect(init.headers).toEqual({ 'Content-Type': 'image/jpeg' });
    expect(init.body).toBeInstanceOf(Blob);
    // Every request the browser made itself went to storage. Nothing carried the image to
    // the API, which is the whole point of the pre-signed URL.
    expect(put.mock.calls.every(([target]) => String(target).startsWith('https://storage.'))).toBe(
      true,
    );

    // The ticket's key, unchanged. A key a client could choose is a key that can be pointed
    // at somebody else's photograph.
    expect(attachPhoto).toHaveBeenCalledWith(PATIENT, {
      object_key: ticket.object_key,
      content_type: 'image/jpeg',
      width: 640,
      height: 480,
    });
    expect(uploadTicket).toHaveBeenCalledWith(PATIENT, 'image/jpeg');
  });

  it('asks for a fresh signed URL after a replacement rather than reusing the one on screen', async () => {
    const user = userEvent.setup();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 200 })));
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(bitmap(1200, 900)));
    stubCanvas();

    renderWithProviders(<PatientPhoto patientID={PATIENT} />);
    await screen.findByRole('img');
    expect(photoURL).toHaveBeenCalledTimes(1);

    await user.upload(screen.getByTestId('photo-input'), file());

    // A replacement is a new object behind a new signature; the URL already on screen
    // points at the previous face.
    await waitFor(() => expect(photoURL).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('refuses a file over the ceiling without spending the clinic uplink on it', async () => {
    const user = userEvent.setup();
    const network = vi.fn();
    vi.stubGlobal('fetch', network);
    vi.stubGlobal('createImageBitmap', vi.fn());

    renderWithProviders(<PatientPhoto patientID={PATIENT} />);
    await screen.findByRole('img');
    await user.upload(
      screen.getByTestId('photo-input'),
      file('huge.jpg', 'image/jpeg', MAX_BYTES + 1),
    );

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent(/too large/i);
    expect(uploadTicket).not.toHaveBeenCalled();
    expect(network).not.toHaveBeenCalled();
  });

  it('says the upload failed when storage refuses, and does not tell the API otherwise', async () => {
    const user = userEvent.setup();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('denied', { status: 403 })));
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(bitmap(1200, 900)));
    stubCanvas();

    renderWithProviders(<PatientPhoto patientID={PATIENT} />);
    await screen.findByRole('img');
    await user.upload(screen.getByTestId('photo-input'), file());

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('The photograph could not be uploaded. Try again.');
    // A record pointing at an object that was never stored renders as a broken frame for
    // everyone who opens the chart afterwards.
    expect(attachPhoto).not.toHaveBeenCalled();
  });

  it('surfaces a refusal from the API in the operator’s own words', async () => {
    const user = userEvent.setup();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 200 })));
    vi.stubGlobal('createImageBitmap', vi.fn().mockResolvedValue(bitmap(1200, 900)));
    stubCanvas();
    attachPhoto.mockRejectedValue(new Error('That patient record has been merged away.'));

    renderWithProviders(<PatientPhoto patientID={PATIENT} />);
    await screen.findByRole('img');
    await user.upload(screen.getByTestId('photo-input'), file());

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'That patient record has been merged away.',
    );
  });

  it('reads in Bangla, which is what the registration desk reads', async () => {
    photoURL.mockRejectedValue(new Error('none'));
    renderWithProviders(<PatientPhoto patientID={PATIENT} />, { locale: 'bn' });

    expect(await screen.findByText('কোনো ছবি নেই')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'ছবি তুলুন' })).toBeInTheDocument();
  });
});
