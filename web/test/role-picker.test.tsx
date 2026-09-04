import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useState } from 'react';

import { renderWithProviders } from './render';
import { RolePicker } from '@/features/users/components/RolePicker';
import type { RoleDefinition } from '@/features/users';

/**
 * Choosing roles, with the consequence printed beside the choice (CP21, criterion 2).
 *
 * An administrator ticking "Clinical nutritionist" is handing somebody the right to write
 * into patients' records. Nobody has the permission list of eighteen roles memorised, so
 * the picker prints what a tick would confer, and outlines the part that is new. A preview
 * that lagged behind the ticks — or quietly counted a role already held as an addition —
 * would be worse than no preview: it would be a wrong answer to "what am I about to give
 * this person", believed because it was shown.
 *
 * The other assertion here is that a role already held is locked. Revoking is a separate
 * act that records a reason in the grant history; if it could be done by unticking a box,
 * a permission would disappear from a colleague's account with nothing on record saying
 * who removed it or why.
 */

function role(
  code: string,
  is_clinical: boolean,
  permissions: string[],
  names: { en: string; bn: string } = { en: code, bn: code },
): RoleDefinition {
  return {
    code,
    name_en: names.en,
    name_bn: names.bn,
    is_clinical,
    station: '',
    permissions,
  };
}

const NUTRITIONIST = role(
  'NUTRITIONIST',
  true,
  ['observation.write.nutrition', 'patient.read.demographics'],
  {
    en: 'Clinical nutritionist',
    bn: 'পুষ্টিবিদ',
  },
);
const EXERCISE = role('EXERCISE', true, ['observation.write.exercise'], {
  en: 'Exercise specialist',
  bn: 'ব্যায়াম বিশেষজ্ঞ',
});
const COUNSELOR = role('COUNSELOR', true, ['patient.read.demographics'], {
  en: 'Clinical counselor',
  bn: 'ক্লিনিক্যাল কাউন্সেলর',
});
const HR = role('HR', false, ['user.read'], { en: 'Human resources officer', bn: 'এইচআর' });
/** A role the catalogue has no name for — the shared `role` messages are the fallback. */
const ADMIN = role('ADMIN', false, ['user.invite', 'role.grant'], { en: '', bn: '' });

const CATALOGUE = [HR, NUTRITIONIST, ADMIN, EXERCISE, COUNSELOR];

/** The picker is controlled, so a test that ticks anything has to hold the choice. */
function Picker({
  catalogue = CATALOGUE,
  held,
  initial = [],
  disabled,
}: {
  catalogue?: RoleDefinition[];
  held?: string[];
  initial?: string[];
  disabled?: boolean;
}) {
  const [chosen, setChosen] = useState<string[]>(initial);
  return (
    <RolePicker
      catalogue={catalogue}
      chosen={chosen}
      held={held}
      onChange={setChosen}
      disabled={disabled}
    />
  );
}

function tickboxes(): HTMLInputElement[] {
  return screen.getAllByRole('checkbox') as HTMLInputElement[];
}

function permissionItem(permission: string): HTMLElement {
  return screen.getByText(permission).closest('li')!;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the catalogue as the picker draws it', () => {
  it('puts the clinical roles first, because most of the clinic holds one', () => {
    renderWithProviders(<Picker />);

    expect(tickboxes().map((box) => box.value)).toEqual([
      'NUTRITIONIST',
      'EXERCISE',
      'COUNSELOR',
      'HR',
      'ADMIN',
    ]);
  });

  it('names each role in the language on screen', () => {
    renderWithProviders(<Picker />, { locale: 'bn' });

    expect(screen.getByText('পুষ্টিবিদ')).toBeInTheDocument();
    expect(screen.queryByText('Clinical nutritionist')).not.toBeInTheDocument();
  });

  it('falls back to the shared role name when the catalogue carries none', () => {
    // An unnamed checkbox is a permission granted by a box whose label is blank.
    renderWithProviders(<Picker />);

    expect(screen.getByText('System administrator')).toBeInTheDocument();
  });

  it('counts the permissions beside each code, and marks the clinical roles', () => {
    renderWithProviders(<Picker />);

    expect(screen.getByText('NUTRITIONIST · 2 permissions')).toBeInTheDocument();
    expect(screen.getByText('EXERCISE · 1 permission')).toBeInTheDocument();
    expect(screen.getAllByText('Clinical')).toHaveLength(3);
  });

  it('groups the boxes under one labelled legend', () => {
    renderWithProviders(<Picker />);

    expect(
      within(screen.getByRole('group', { name: 'Roles' })).getAllByRole('checkbox'),
    ).toHaveLength(CATALOGUE.length);
  });
});

describe('ticking a role', () => {
  it('reports the role as chosen', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(<RolePicker catalogue={CATALOGUE} chosen={[]} onChange={onChange} />);

    await user.click(screen.getByRole('checkbox', { name: /Clinical nutritionist/ }));

    expect(onChange).toHaveBeenCalledWith(['NUTRITIONIST']);
  });

  it('reports the role as dropped when it is unticked again', async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    renderWithProviders(
      <RolePicker
        catalogue={CATALOGUE}
        chosen={['NUTRITIONIST', 'EXERCISE']}
        onChange={onChange}
      />,
    );

    await user.click(screen.getByRole('checkbox', { name: /Clinical nutritionist/ }));

    expect(onChange).toHaveBeenCalledWith(['EXERCISE']);
  });

  it('says nothing is in effect until something is ticked', () => {
    renderWithProviders(<Picker />);

    expect(screen.getByText('No permissions yet')).toBeInTheDocument();
    expect(screen.getByText('Tick a role to see what it lets the person do.')).toBeInTheDocument();
  });

  it('prints the union of the ticked roles, sorted, without counting a shared permission twice', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Picker />);

    await user.click(screen.getByRole('checkbox', { name: /Clinical nutritionist/ }));
    await user.click(screen.getByRole('checkbox', { name: /Clinical counselor/ }));

    // COUNSELOR brings `patient.read.demographics`, which NUTRITIONIST already carried.
    expect(screen.getByText('2 permissions in effect')).toBeInTheDocument();
    expect(screen.getByText('patient.read.demographics')).toBeInTheDocument();
    expect(screen.getByText('observation.write.nutrition')).toBeInTheDocument();
  });
});

describe('roles the person already holds', () => {
  it('shows a held role ticked and locked, because revoking is not an untick', () => {
    renderWithProviders(<Picker held={['NUTRITIONIST']} />);

    const held = screen.getByRole('checkbox', { name: /Clinical nutritionist/ });
    expect(held).toBeChecked();
    expect(held).toBeDisabled();
    expect(held.closest('label')).toHaveAttribute('data-locked', 'true');
  });

  it('counts what is already held towards the preview', () => {
    renderWithProviders(<Picker held={['NUTRITIONIST']} />);

    expect(screen.getByText('2 permissions in effect')).toBeInTheDocument();
    expect(screen.queryByText(/would be added/)).not.toBeInTheDocument();
  });

  it('outlines only the permissions the new role would actually add', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Picker held={['HR']} />);

    await user.click(screen.getByRole('checkbox', { name: /Clinical nutritionist/ }));

    expect(permissionItem('user.read')).not.toHaveAttribute('data-added');
    expect(permissionItem('observation.write.nutrition')).toHaveAttribute('data-added', 'true');
    expect(permissionItem('patient.read.demographics')).toHaveAttribute('data-added', 'true');
    expect(screen.getByText('2 would be added — shown outlined.')).toBeInTheDocument();
  });

  it('claims nothing would be added when the ticked role confers nothing new', async () => {
    // Granting a second clinical role often adds nothing an administrator has not already
    // given. Saying "1 would be added" there would be a lie about the consequence.
    const user = userEvent.setup();
    renderWithProviders(<Picker held={['NUTRITIONIST']} />);

    await user.click(screen.getByRole('checkbox', { name: /Clinical counselor/ }));

    expect(screen.queryByText(/would be added/)).not.toBeInTheDocument();
    expect(screen.getByText('2 permissions in effect')).toBeInTheDocument();
  });
});

describe('while the invitation is in flight', () => {
  it('lets nothing be ticked or unticked', () => {
    renderWithProviders(<Picker initial={['NUTRITIONIST']} disabled />);

    for (const box of tickboxes()) expect(box).toBeDisabled();
  });
});
