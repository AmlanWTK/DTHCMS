/**
 * DTHCMS web primitives.
 *
 * Most of these files carry `'use client'`. React Server Components cannot use state,
 * context, `useId` or `forwardRef`, and every component that reads the language context
 * needs all of that — so the directive marks what is true rather than working around
 * anything. `Icon`, `Badge`, `Card` and `cx` deliberately do not carry it: they are pure,
 * they render on the server, and a design system that made everything a client component
 * would quietly move the whole interface into the browser bundle.
 *
 * Eleven components, no clinical knowledge. A component here knows what a status looks
 * like; it does not know what makes a glucose reading high. That distinction is what
 * keeps the primitive layer usable by every station without becoming a dependency of all
 * of them.
 */

export { Icon, ICON_PATHS, type IconName, type IconProps } from './components/Icon';
export { Button, type ButtonProps, type ButtonSize, type ButtonVariant } from './components/Button';
export { Field, type FieldOwnProps, type FieldRenderArgs } from './components/Field';
export { Input, type InputProps } from './components/Input';
export {
  NumericInput,
  formatClinicalValue,
  type NumericInputProps,
  type NumericRange,
} from './components/NumericInput';
export { Select, type SelectOption, type SelectProps } from './components/Select';
export { Card, type CardElevation, type CardProps } from './components/Card';
export { Badge, type BadgeProps, type BadgeTone } from './components/Badge';
export { StatusPill, type StatusPillProps } from './components/StatusPill';
export { AlertBanner, type AlertBannerProps, type AlertTone } from './components/AlertBanner';
export { Skeleton, type SkeletonProps } from './components/Skeleton';
export { EmptyState, type EmptyStateProps } from './components/EmptyState';
export { ErrorState, type ErrorStateProps } from './components/ErrorState';

export { LanguageProvider, useLanguage, type LanguageValue } from './lib/language';
export { cx } from './lib/cx';
