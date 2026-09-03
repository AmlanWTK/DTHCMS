import { REQUESTED_WITH_HEADER, REQUESTED_WITH_VALUE } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';
import { getRandomBytes } from 'expo-crypto';

import { deleteSecureItem, getSecureItem, setSecureItem } from '@/lib/secure-storage';
import { base64, bytesToHex, hexToBytes, publicKeyOf, signedHeaders } from '@/lib/device-signing';

/**
 * This device's identity (CP18, D-46).
 *
 * One record in the Keystore: the device id the server assigned and the 32-byte seed the
 * app generated. The seed is made here, on this hardware, and is never sent anywhere —
 * only the public key derived from it leaves, once, at enrolment. Every request after that
 * is signed with it (`signRequest`), and the session a sign-in opens is bound to it: the
 * access token is useless from anywhere else.
 *
 * Reinstalling the app empties the Keystore, which is the documented reason a tablet has
 * to be re-enrolled: `docs/staff/devices.md`.
 *
 * Not the Android Keystore's own non-exportable key. Expo has no module that generates an
 * Ed25519 key inside the hardware; the seed is generated in software and kept in
 * `expo-secure-store`, which the Keystore encrypts at rest. That is the honest description
 * of D-46 as implemented, and it is recorded as such in `docs/identity.md` §9.
 */

export type DeviceView = components['schemas']['Device'];

interface StoredIdentity {
  device_id: string;
  /** What the administrator called it, for the device screen. */
  name: string;
  /** 32-byte seed, hex. */
  seed: string;
}

let cached: StoredIdentity | null | undefined;

async function load(): Promise<StoredIdentity | null> {
  if (cached !== undefined) return cached;
  try {
    const raw = await getSecureItem('deviceKey');
    cached = raw ? (JSON.parse(raw) as StoredIdentity) : null;
  } catch {
    cached = null;
  }
  return cached;
}

/** Whether this device holds an enrolment. */
export async function isEnrolled(): Promise<boolean> {
  return (await load()) !== null;
}

/** The device id the server knows this hardware by, or null before enrolment. */
export async function deviceId(): Promise<string | null> {
  return (await load())?.device_id ?? null;
}

/** What this device is called, or null before enrolment. */
export async function deviceIdentity(): Promise<{ id: string; name: string } | null> {
  const identity = await load();
  return identity ? { id: identity.device_id, name: identity.name } : null;
}

/** Forget the enrolment. After a revocation, or on purpose from the device screen. */
export async function forgetDevice(): Promise<void> {
  cached = null;
  try {
    await deleteSecureItem('deviceKey');
  } catch {
    // Nothing to do; the next load finds nothing either way.
  }
}

export interface EnrolOptions {
  baseUrl: string;
  code: string;
  model: string;
  osVersion: string;
  appVersion: string;
  fetch?: typeof globalThis.fetch;
  /** Injected for tests; the app uses expo-crypto. */
  randomBytes?: (n: number) => Uint8Array;
}

export type EnrolResult =
  | { kind: 'enrolled'; device: DeviceView }
  | { kind: 'refused'; status: number }
  | { kind: 'offline' };

/**
 * Enrol: generate a fresh seed, send the code with the public key, keep the identity the
 * server answers with. The seed is only written to the Keystore once the server has
 * accepted its public half; a refused code leaves nothing behind.
 */
export async function enrolDevice(options: EnrolOptions): Promise<EnrolResult> {
  const random = options.randomBytes ?? getRandomBytes;
  const doFetch = options.fetch ?? ((input: Request) => globalThis.fetch(input));

  const seed = random(32);
  const publicKey = base64(publicKeyOf(seed));

  let response: Response;
  try {
    response = await doFetch(
      new Request(`${options.baseUrl}/v1/auth/device/enrol`, {
        method: 'POST',
        credentials: 'omit',
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
          [REQUESTED_WITH_HEADER]: REQUESTED_WITH_VALUE,
        },
        body: JSON.stringify({
          code: options.code.trim(),
          public_key: publicKey,
          model: options.model,
          os_version: options.osVersion,
          app_version: options.appVersion,
        }),
      }),
    );
  } catch {
    return { kind: 'offline' };
  }
  if (!response.ok) return { kind: 'refused', status: response.status };

  const body = (await response.json()) as components['schemas']['DeviceEnrolResponse'];
  const identity: StoredIdentity = {
    device_id: body.device.id,
    name: body.device.name,
    seed: bytesToHex(seed),
  };
  await setSecureItem('deviceKey', JSON.stringify(identity));
  cached = identity;
  return { kind: 'enrolled', device: body.device };
}

/**
 * The device headers for one request, or none when this device is not enrolled — an
 * unenrolled tablet can still sign a person in; it just cannot write a clinical record.
 */
export async function signRequest(
  method: string,
  url: string,
  body: string | Uint8Array | null | undefined,
  appVersion: string,
  randomBytes: (n: number) => Uint8Array = getRandomBytes,
): Promise<Record<string, string>> {
  const identity = await load();
  if (!identity) return {};
  // Not URL.pathname: React Native's URL does not implement it on every version, and a
  // path is easy to cut by hand.
  const path = url.replace(/^[a-z][a-z0-9+.-]*:\/\/[^/]+/i, '').split('?')[0] ?? '/';
  return signedHeaders(
    hexToBytes(identity.seed),
    { method, path, body, deviceId: identity.device_id },
    { randomBytes, appVersion },
  );
}

/**
 * An authorizer for `createRefreshingFetch`: signs the request when this device is
 * enrolled. Composed after the bearer authorizer, so the retry after a refresh is
 * re-signed with a fresh nonce — a nonce is remembered by the server, and the copy taken
 * before the first send must not carry the one already spent.
 */
export function deviceAuthorizer(appVersion: string) {
  return async (request: Request): Promise<Request> => {
    if (!(await isEnrolled())) return request;
    const hasBody = request.method !== 'GET' && request.method !== 'HEAD';
    const body = hasBody ? await request.clone().text() : null;
    const signed = await signRequest(request.method, request.url, body || null, appVersion);
    const headers = new Headers(request.headers);
    for (const [name, value] of Object.entries(signed)) headers.set(name, value);
    return new Request(request, { headers });
  };
}
