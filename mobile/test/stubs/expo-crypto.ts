import { randomBytes } from 'node:crypto';

/**
 * `expo-crypto` for the tests: Node's CSPRNG behind the same name. The real module pulls
 * in the whole React Native import graph, which vitest cannot parse and does not need.
 */
export function getRandomBytes(byteCount: number): Uint8Array {
  return new Uint8Array(randomBytes(byteCount));
}
