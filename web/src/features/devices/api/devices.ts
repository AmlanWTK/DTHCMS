import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { api, unwrap } from '@/lib/api';

/**
 * The device calls, typed against the contract (CP18).
 *
 * Thin on purpose. Which transitions are allowed from which status is the server's rule;
 * the console shows the buttons the status makes sensible and lets the server have the
 * last word.
 */

export type Device = components['schemas']['Device'];
export type DeviceEvent = components['schemas']['DeviceEvent'];
export type DeviceKind = Device['kind'];
export type DeviceStatus = Device['status'];
export type EnrolmentIssued = components['schemas']['DeviceEnrolmentIssued'];

export type Transition = 'suspend' | 'reinstate' | 'revoke' | 'lost';

export async function listDevices(): Promise<Device[]> {
  const result = await unwrap(api.GET('/v1/devices'));
  return result.devices;
}

/**
 * Register a device.
 *
 * One call, two outcomes, which is the server's shape and not a convenience invented here.
 * A tablet or phone comes back with `code` and `expires_at`: a one-time secret to type into
 * the device, which exchanges a key for it. A desktop comes back with `workstation_code`:
 * the label to print and stick to the monitor, because a browser has nowhere to keep a key
 * and so is *named* rather than proved (ADR-0021).
 *
 * `station` is required for a desktop and ignored otherwise. The administrator names the
 * desk — REG, TRIAGE — and never the whole code: the clinic prefix and the ordinal are the
 * database's to decide, so that two people enrolling at once cannot choose the same one.
 */
export function issueEnrolment(
  name: string,
  kind: DeviceKind,
  station = '',
): Promise<EnrolmentIssued> {
  return unwrap(
    api.POST('/v1/devices', {
      params: writing(),
      body: { name, kind, ...(kind === 'desktop' ? { station } : {}) },
    }),
  );
}

export function reissueEnrolment(id: string): Promise<EnrolmentIssued> {
  return unwrap(
    api.POST('/v1/devices/{id}/enrolments', {
      params: { ...writing(), path: { id } },
    }),
  );
}

export async function listDeviceEvents(id: string): Promise<DeviceEvent[]> {
  const result = await unwrap(api.GET('/v1/devices/{id}/events', { params: { path: { id } } }));
  return result.events;
}

export function transitionDevice(id: string, to: Transition, reason: string): Promise<Device> {
  const params = { ...writing(), path: { id } };
  const body = { reason };
  switch (to) {
    case 'suspend':
      return unwrap(api.POST('/v1/devices/{id}/suspend', { params, body }));
    case 'reinstate':
      return unwrap(api.POST('/v1/devices/{id}/reinstate', { params, body }));
    case 'revoke':
      return unwrap(api.POST('/v1/devices/{id}/revoke', { params, body }));
    case 'lost':
      return unwrap(api.POST('/v1/devices/{id}/lost', { params, body }));
  }
}

/** The transitions a status allows. Mirrors the server; the server still decides. */
export function transitionsFor(status: DeviceStatus): Transition[] {
  switch (status) {
    case 'active':
      return ['suspend', 'revoke', 'lost'];
    case 'suspended':
      return ['reinstate', 'revoke', 'lost'];
    case 'pending':
      return ['revoke'];
    default:
      return [];
  }
}
