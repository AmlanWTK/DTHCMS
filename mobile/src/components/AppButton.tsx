import { Pressable, type PressableProps } from 'react-native';

import { theme } from '@/lib/tokens';
import { useTokens } from '@/lib/tokens';
import { AppText } from '@/components/AppText';

/**
 * The button, sized for a thumb.
 *
 * `size.touchTarget` is the same 48 the web stylesheet asserts — one token, three
 * surfaces. There is deliberately no small variant here at all: the web's `sm` exists
 * for pointer-driven density, and nothing on a station tablet is pointer-driven.
 */

export interface AppButtonProps extends Omit<PressableProps, 'children' | 'style'> {
  label: string;
  variant?: 'primary' | 'secondary';
}

export function AppButton({ label, variant = 'primary', disabled, ...rest }: AppButtonProps) {
  const { colors } = useTokens();

  const background = disabled
    ? colors.state.disabledSurface
    : variant === 'primary'
      ? colors.brand.solid
      : colors.surface.raised;
  const foreground = disabled
    ? colors.state.disabledText
    : variant === 'primary'
      ? colors.text.onBrand
      : colors.text.primary;

  return (
    <Pressable
      accessibilityRole="button"
      accessibilityState={{ disabled: disabled === true }}
      disabled={disabled}
      {...rest}
      style={({ pressed }) => ({
        minHeight: theme.size.touchTarget,
        borderRadius: theme.borderRadius.md,
        paddingHorizontal: theme.spacing['5'],
        alignItems: 'center',
        justifyContent: 'center',
        backgroundColor: background,
        // A disabled primary button sits on the same grey as the screen behind it; the
        // border is what keeps it visible as a button that will come back.
        borderWidth: variant === 'secondary' || disabled ? 1 : 0,
        borderColor: disabled ? colors.state.disabledBorder : colors.border.control,
        opacity: pressed ? 0.85 : 1,
      })}
    >
      <AppText weight="semibold" style={{ color: foreground }}>
        {label}
      </AppText>
    </Pressable>
  );
}
