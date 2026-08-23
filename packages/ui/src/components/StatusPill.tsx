import { clinicalStatuses, type ClinicalStatusName } from '@dthcms/design-tokens';

import { cx } from '../lib/cx.js';
import { useLanguage } from '../lib/language.js';
import { Icon, type IconName } from './Icon.js';

export interface StatusPillProps {
  status: ClinicalStatusName;
  /** Filled rather than tinted. For a value that needs to be seen from across a room. */
  emphasis?: 'subtle' | 'solid';
  size?: 'sm' | 'md';
  /** Hides the text, leaving icon and colour. The label stays available to screen readers. */
  labelHidden?: boolean;
  className?: string;
}

/**
 * The component acceptance criterion 4 rests on.
 *
 * It renders three things that all mean the same thing: a colour, an icon, and a word. It
 * is not possible to render only the colour — there is no prop for that — because under
 * deuteranopia several of these colours converge, and the icon and the word are what
 * remain.
 *
 * `labelHidden` hides the word visually but keeps it in the accessibility tree and keeps
 * the icon, so even the most compact form carries two signals rather than one.
 *
 * `data-status` is what the print stylesheet keys off: on paper the status label is
 * appended as text, because the printer in a Bangladeshi clinic is monochrome and colour
 * arrives carrying nothing at all.
 */
export function StatusPill({
  status,
  emphasis = 'subtle',
  size = 'md',
  labelHidden = false,
  className,
}: StatusPillProps) {
  const { t } = useLanguage();
  const token = clinicalStatuses[status];
  const label = t(token.label);

  return (
    <span
      className={cx('dthc-pill', `dthc-pill--${emphasis}`, `dthc-pill--${size}`, className)}
      data-status={status}
    >
      <Icon name={token.icon as IconName} size={size === 'sm' ? 14 : 16} />
      <span className={cx(labelHidden && 'dthc-visually-hidden')}>{label}</span>
    </span>
  );
}
