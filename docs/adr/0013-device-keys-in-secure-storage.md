# ADR-0013 · A device's key is a software Ed25519 seed in Keystore-encrypted storage, not a hardware-bound key

- **Status:** Accepted
- **Date:** 2026-09-03
- **Checkpoint:** CP18
- **Supersedes:** —

## Context

D-46 ratified admin-enrolled devices with "a keypair in the Android Keystore", so that a
clinical event's `device_id` is evidence rather than a claim [R-03]. The strongest reading
of that sentence is a key generated inside the Keystore that can sign but never be read —
hardware-bound on devices with a secure element, software-backed on the rest.

The station app is Expo. No Expo module generates an Ed25519 (or any signing) key inside
the Keystore; `expo-secure-store` stores bytes the Keystore encrypts, which is a different
thing. Getting a non-exportable key means a custom native module (Kotlin, `KeyGenParameterSpec`,
EC P-256 rather than Ed25519 because that is what the Keystore offers), an EAS build
pipeline that does not exist yet (CP11 carried it forward), and a device to test it on
(D-59, still unbought).

## Decision

The device generates a 32-byte Ed25519 seed in software, keeps it in `expo-secure-store`
with `WHEN_UNLOCKED_THIS_DEVICE_ONLY`, and signs every request with it
(`docs/identity.md` §9). Only the public key leaves the device, once, at enrolment. The
server verifies with the Go standard library; no cryptography was written on either side.

The scheme is designed so that the key's _home_ can change without the _protocol_
changing: the server stores an algorithm column beside every public key (`ed25519` today),
the canonical string is independent of the key type, and re-enrolment retires a key in
one statement. A future native module that generates a hardware-bound P-256 key adds an
algorithm value and a verifier branch, not a new enrolment flow.

## Consequences

**What holds.** The key never leaves the device by any path the software offers. A stolen
access or refresh token is useless off the device that opened the session. A forged device
id fails verification. Revocation is effective on the next request. An uninstall, a
factory reset, or a removed screen lock destroys the seed, and the tablet must be
re-enrolled by an administrator — which `docs/staff/devices.md` tells the clinic.

**What does not hold.** An attacker with root on the tablet can read the seed and sign as
that device from elsewhere until it is revoked. A hardware-bound key would refuse that.
This is the "attestation depth" D-46 already deferred; Play Integrity, or a native key
module, closes it, and the security review at CP94 is where the clinic decides whether
its threat model needs it closed.

**What it costs to change.** One native module, one algorithm value, one verifier branch,
and a re-enrolment of every tablet — an afternoon per clinic, once, with the flow that
already exists.
