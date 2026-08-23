/**
 * DTHCMS web primitives.
 *
 * Eleven components, no clinical knowledge. A component here knows what a status looks
 * like; it does not know what makes a glucose reading high. That distinction is what
 * keeps the primitive layer usable by every station without becoming a dependency of all
 * of them.
 */

export { Icon, ICON_PATHS, type IconName, type IconProps } from './components/Icon.js';
export {
  Button,
  type ButtonProps,
  type ButtonSize,
  type ButtonVariant,
} from './components/Button.js';
export { Field, type FieldOwnProps, type FieldRenderArgs } from './components/Field.js';
export { Input, type InputProps } from './components/Input.js';
export {
  NumericInput,
  formatClinicalValue,
  type NumericInputProps,
  type NumericRange,
} from './components/NumericInput.js';
export { Select, type SelectOption, type SelectProps } from './components/Select.js';
export { Card, type CardElevation, type CardProps } from './components/Card.js';
export { Badge, type BadgeProps, type BadgeTone } from './components/Badge.js';
export { StatusPill, type StatusPillProps } from './components/StatusPill.js';
export { AlertBanner, type AlertBannerProps, type AlertTone } from './components/AlertBanner.js';
export { Skeleton, type SkeletonProps } from './components/Skeleton.js';
export { EmptyState, type EmptyStateProps } from './components/EmptyState.js';
export { ErrorState, type ErrorStateProps } from './components/ErrorState.js';

export { LanguageProvider, useLanguage, type LanguageValue } from './lib/language.js';
export { cx } from './lib/cx.js';
