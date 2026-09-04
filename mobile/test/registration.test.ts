import { randomUUID } from 'node:crypto';

import { describe, expect, it, vi } from 'vitest';

import { ageOn, isComplete, normalisePhone, readDate, requiredState } from '@dthcms/shared-schemas';

import {
  BLANK,
  STEPS,
  canAdvance,
  complete,
  required,
  toRegistration,
  type RegistrationValues,
} from '../src/features/registration/state';

const keystore = new Map<string, string>();
vi.mock('expo-secure-store', () => ({
  setItemAsync: vi.fn(async (key: string, value: string) => {
    keystore.set(key, value);
  }),
  getItemAsync: vi.fn(async (key: string) => keystore.get(key) ?? null),
  deleteItemAsync: vi.fn(async (key: string) => {
    keystore.delete(key);
  }),
  WHEN_UNLOCKED_THIS_DEVICE_ONLY: 'WHEN_UNLOCKED_THIS_DEVICE_ONLY',
}));

const { DRAFT_TTL_MS, clearDraft, isStale, loadDraft, saveDraft } =
  await import('../src/lib/registration-draft');

/**
 * Registration on the station app (CP33).
 *
 * The three criteria, and the one that earns its place: the validation rules must be
 * *identical* to the web desk's, proven rather than asserted.
 */

const filled: RegistrationValues = {
  ...BLANK,
  nameEN: 'Rahima Begum',
  nameBN: 'রহিমা বেগম',
  sex: 'female',
  date: { day: '14', month: '06', year: '1985' },
  dobSource: 'national_id',
  phone: '01712345678',
  consentReference: 'consent_2026_0001',
};

describe('the rules are the web desk’s rules', () => {
  // Criterion 2. Not "the two behave the same" — the same code, which is why these
  // assertions are about *identity* rather than about behaviour.
  it('uses the shared module rather than a second copy', async () => {
    const { readFileSync } = await import('node:fs');
    const { dirname, join } = await import('node:path');
    const here = dirname(import.meta.url.replace('file://', ''));
    const source = readFileSync(
      join(here, '..', 'src', 'features', 'registration', 'state.ts'),
      'utf8',
    );

    expect(source).toContain("from '@dthcms/shared-schemas'");
    // A local re-implementation of any of these is the failure this test exists to catch.
    for (const rule of ['normalisePhone', 'readDate', 'requiredState', 'isComplete']) {
      expect(source).toContain(rule);
      expect(source).not.toMatch(new RegExp(`function ${rule}\\\\b`));
    }
  });

  it('agrees with the shared module on what is required', () => {
    expect(required(filled)).toEqual(
      requiredState({
        nameEN: filled.nameEN,
        sex: filled.sex,
        date: filled.date,
        dobSource: filled.dobSource,
        phone: filled.phone,
        consentReference: filled.consentReference,
      }),
    );
    expect(complete(filled)).toBe(isComplete(required(filled)));
  });

  it('normalises a telephone number exactly as the desk does', () => {
    for (const typed of ['01712345678', '+8801712345678', '8801712345678', '017 1234 5678']) {
      const body = toRegistration({ ...filled, phone: typed }, randomUUID());
      expect(body.phone_primary).toBe(normalisePhone(typed));
      expect(body.phone_primary).toBe('+8801712345678');
    }
  });

  it('reads a date exactly as the desk does, including the precision', () => {
    for (const date of [
      { day: '14', month: '06', year: '1985' },
      { day: '', month: '06', year: '1985' },
      { day: '', month: '', year: '1958' },
    ]) {
      const body = toRegistration({ ...filled, date }, randomUUID());
      expect(body.birth_date).toBe(readDate(date)?.iso);
      expect(body.dob_precision).toBe(readDate(date)?.precision);
    }
  });
});

describe('the record a phone produces', () => {
  it('is the same shape the desk sends', () => {
    // Criterion 1. `device_id` is not in this body at all — it comes from the envelope the
    // server builds from the verified device, which is precisely why a phone's record and a
    // desk's record differ only there.
    const body = toRegistration(filled, '0190a8f2-0000-7000-8000-00000000000f');
    expect(Object.keys(body).sort()).toEqual(
      [
        'address_line',
        'birth_date',
        'consent_reference',
        'district',
        'division',
        'dob_precision',
        'dob_source',
        'education_level',
        'emergency_name',
        'emergency_phone',
        'emergency_relation',
        'event_id',
        'household_size',
        'identifiers',
        'income_band',
        'medicine_payer',
        'name_bn',
        'name_en',
        'occupation_category',
        'phone_primary',
        'phone_secondary',
        'postcode',
        'residence_type',
        'sex',
        'upazila',
      ].sort(),
    );
    expect(body).not.toHaveProperty('device_id');
    expect(body.name_en).toBe('Rahima Begum');
  });

  it('carries the age echo the desk shows, from the same function', () => {
    const today = new Date('2026-09-03T00:00:00Z');
    expect(ageOn('1985-06-14', 'day', today)).toEqual({ years: 41, months: 2, approximate: false });
  });
});

describe('stepping through', () => {
  it('blocks a required step on its own fields alone', () => {
    // Blocking the whole flow on a field three screens away is how a step-by-step form
    // becomes a maze.
    expect(canAdvance('identity', BLANK)).toBe(false);
    expect(canAdvance('identity', { ...BLANK, nameEN: 'Rahima Begum', sex: 'female' })).toBe(true);
    // …even though nothing else is filled in.
    expect(complete({ ...BLANK, nameEN: 'Rahima Begum', sex: 'female' })).toBe(false);
  });

  it('lets the optional steps be skipped', () => {
    for (const step of STEPS.filter((s) => !s.required)) {
      expect(canAdvance(step.id, BLANK)).toBe(true);
    }
  });

  it('holds the last step until everything required is there', () => {
    expect(canAdvance('review', BLANK)).toBe(false);
    expect(canAdvance('review', filled)).toBe(true);
  });
});

describe('a registration interrupted', () => {
  // Criterion 3. On a phone an interruption is not an edge case: a call comes in, the
  // screen locks, Android reclaims memory.
  it('comes back with everything that was typed', async () => {
    keystore.clear();
    await saveDraft({ eventID: 'e-1', step: 3, savedAt: Date.now(), values: filled });

    const restored = await loadDraft<RegistrationValues>();
    expect(restored?.values).toEqual(filled);
    expect(restored?.step).toBe(3);
    // The same event id, so a resumed registration is still *one* registration however
    // many times it was interrupted.
    expect(restored?.eventID).toBe('e-1');
  });

  it('lives in the Keystore, not in plain storage', async () => {
    keystore.clear();
    await saveDraft({ eventID: 'e-1', step: 1, savedAt: Date.now(), values: filled });
    // A draft holds a name, a telephone number and a date of birth — the same data the
    // finished record holds. The allowlist in secure-keys.ts is what keeps it out of
    // AsyncStorage, and a key it does not know throws.
    expect([...keystore.keys()]).toEqual(['dthcms.registration-draft']);
  });

  it('forgets a draft older than the clinic day', async () => {
    keystore.clear();
    const yesterday = Date.now() - DRAFT_TTL_MS - 1000;
    await saveDraft({ eventID: 'e-1', step: 1, savedAt: yesterday, values: filled });

    expect(isStale({ savedAt: yesterday })).toBe(true);
    expect(await loadDraft()).toBeNull();
    // …and it is gone, not merely hidden: a name in the Keystore of a phone in a drawer is
    // the kind of residue nobody thinks to look for.
    expect(keystore.size).toBe(0);
  });

  it('discards a draft it cannot read rather than offering to resume it', async () => {
    keystore.clear();
    keystore.set('dthcms.registration-draft', 'not json');
    expect(await loadDraft()).toBeNull();
    expect(keystore.size).toBe(0);
  });

  it('is cleared when the registration lands', async () => {
    keystore.clear();
    await saveDraft({ eventID: 'e-1', step: 8, savedAt: Date.now(), values: filled });
    await clearDraft();
    expect(await loadDraft()).toBeNull();
  });
});
