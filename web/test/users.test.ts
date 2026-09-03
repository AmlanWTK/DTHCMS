import { describe, expect, it } from 'vitest';

import {
  EMPLOYEE_CODE,
  passwordAcceptable,
  permissionsOf,
  reasonRequiredFor,
  transitionsFor,
  type RoleDefinition,
} from '@/features/users';
import { generatePassword } from '@/features/users/components/InviteForm';

/**
 * The console's own rules (CP21) — the ones it applies before the server does, so a
 * button is offered only where the server would say yes.
 */

const role = (code: string, permissions: string[]): RoleDefinition => ({
  code,
  name_en: code,
  name_bn: code,
  is_clinical: true,
  station: '',
  permissions,
});

describe('the lifecycle the console mirrors', () => {
  it('matches backend/internal/auth/lifecycle.go move for move', () => {
    expect(transitionsFor('invited')).toEqual(['active', 'deactivated']);
    expect(transitionsFor('active')).toEqual(['suspended', 'deactivated']);
    expect(transitionsFor('suspended')).toEqual(['active', 'deactivated']);
    expect(transitionsFor('deactivated')).toEqual(['active']);
  });

  it('demands a reason only for a suspension, like the database CHECK', () => {
    expect(reasonRequiredFor('suspended')).toBe(true);
    expect(reasonRequiredFor('active')).toBe(false);
    expect(reasonRequiredFor('deactivated')).toBe(false);
  });
});

describe('the effective-permission preview', () => {
  const catalogue = [
    role('REGISTRATION', ['patient.read.demographics', 'patient.write.demographics']),
    role('NUTRITIONIST', ['patient.read.demographics', 'observation.write.nutrition']),
  ];

  it('is the union across the chosen roles, sorted, without duplicates', () => {
    expect(permissionsOf(['REGISTRATION', 'NUTRITIONIST'], catalogue)).toEqual([
      'observation.write.nutrition',
      'patient.read.demographics',
      'patient.write.demographics',
    ]);
  });

  it('is empty for no roles and ignores roles the catalogue lacks', () => {
    expect(permissionsOf([], catalogue)).toEqual([]);
    expect(permissionsOf(['GHOST'], catalogue)).toEqual([]);
  });
});

describe('what the invite form checks before the server does', () => {
  it('accepts the employee codes the server accepts', () => {
    for (const ok of ['A1', 'N006', 'JD_01', 'ABCDEFGHIJKLMNOP'])
      expect(EMPLOYEE_CODE.test(ok)).toBe(true);
    for (const bad of ['a1', '1A', 'A', 'ABCDEFGHIJKLMNOPQ', 'N 006', 'N-006']) {
      expect(EMPLOYEE_CODE.test(bad)).toBe(false);
    }
  });

  it('applies the 12–128 character password policy by runes, not bytes', () => {
    expect(passwordAcceptable('short')).toBe(false);
    expect(passwordAcceptable('twelve chars')).toBe(true);
    expect(passwordAcceptable('বারোটি অক্ষরের')).toBe(true);
    expect(passwordAcceptable('x'.repeat(129))).toBe(false);
  });

  it('generates a password that passes the policy and has no look-alike characters', () => {
    for (let i = 0; i < 50; i++) {
      const password = generatePassword();
      expect(passwordAcceptable(password)).toBe(true);
      expect(password).toMatch(/^[a-zA-Z2-9]{4}(-[a-zA-Z2-9]{4}){3}$/);
      expect(password).not.toMatch(/[0O1lI]/);
    }
  });
});
