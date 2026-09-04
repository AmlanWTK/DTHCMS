import { useState } from 'react';
import { Modal, Pressable, ScrollView, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { ApiError } from '@dthcms/api-client';

import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';
import { useSession } from '@/stores/session';

/**
 * In-session role switching (CP41, [R-02]).
 *
 * The blueprint names the staffing reality: "the same assistant enters BP, then switches to
 * anthropometry entry, from the same phone." A clinic with nine staff and twelve stations
 * cannot afford a logout between hats.
 *
 * # The band is the indicator, not the switcher
 *
 * Criterion 3 asks that the active role be *unmistakable*. So the band is always on screen,
 * always says the role in words, and is the thing you tap to change it — two taps, which is
 * criterion 1. An operator cannot be wearing a hat they cannot see, because the only way to
 * change hats is through the thing that displays the current one.
 *
 * # About the colour
 *
 * The band is tinted per role from the design tokens' hue families. Those families are
 * named for clinical status — `normal`, `borderline`, `high` — and using them for chrome is
 * an overload worth naming: the alternative was a colour literal, which `mobile/src` forbids
 * and a test greps for, or a new palette in the token package, which is a CP09 change and
 * not this checkpoint's. It is safe in practice because clinical hues in this app only ever
 * colour *values*, and a full-width band under the title is not somewhere a reading appears.
 *
 * The colour is never the only signal. The role is written in the band in words, at the
 * size the title is, and a switch that changed only a hue would fail for the roughly one in
 * twelve men who will work in this clinic.
 */

/**
 * Role → hue family. Nine hats, distinct at a glance; the administrative roles fall through
 * to neutral because they work no station and nobody switches into one mid-clinic.
 */
const ROLE_HUE: Record<
  string,
  'brand' | 'normal' | 'low' | 'borderline' | 'high' | 'critical' | 'unknown' | 'stale' | 'neutral'
> = {
  REGISTRATION: 'brand',
  ANTHROPOMETRY: 'normal',
  COUNSELOR: 'low',
  HISTORY: 'unknown',
  CLINICAL_ASSISTANT: 'borderline',
  JUNIOR_DOCTOR: 'high',
  PHYSICIAN: 'high',
  RECORDS: 'stale',
  NUTRITIONIST: 'low',
  EXERCISE: 'normal',
  QA: 'critical',
  RX_EDUCATOR: 'brand',
  PHARMACIST: 'unknown',
  CRM: 'stale',
  FIELD_WORKER: 'borderline',
};

function hueFor(role: string | null): { band: string; edge: string } {
  const family = (role && ROLE_HUE[role]) || 'neutral';
  const ramp = theme.colors[family] as Record<string, string>;
  return { band: ramp['100'] ?? ramp['50'] ?? '', edge: ramp['500'] ?? ramp['600'] ?? '' };
}

export function RoleSwitcher() {
  const t = useTranslations('role');
  const { colors } = useTokens();
  const operator = useSession((state) => state.operator);
  const activeRole = useSession((state) => state.activeRole);
  const switchRole = useSession((state) => state.switchRole);

  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  if (!operator || operator.roleCodes.length === 0) return null;

  const hue = hueFor(activeRole);
  // One hat and nothing to switch to: the band still says which, because "acting as" is
  // information even when it is not a choice. It just does not pretend to be a button.
  const switchable = operator.roleCodes.length > 1;

  async function choose(role: string) {
    setBusy(role);
    setFailed(null);
    try {
      await switchRole(role);
      setOpen(false);
    } catch (error) {
      setFailed(error instanceof ApiError ? error.messageEN : t('switchFailed'));
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <Pressable
        accessibilityRole={switchable ? 'button' : 'text'}
        accessibilityLabel={t('actingAs', { role: t(`codes.${activeRole}` as never) })}
        accessibilityHint={switchable ? t('tapToSwitch') : undefined}
        disabled={!switchable}
        onPress={() => setOpen(true)}
        testID="role-band"
        style={{
          backgroundColor: hue.band,
          borderLeftWidth: theme.spacing['1'],
          borderLeftColor: hue.edge,
          borderRadius: theme.borderRadius.md,
          paddingHorizontal: theme.spacing['3'],
          paddingVertical: theme.spacing['2'],
          minHeight: theme.size.touchTarget,
          flexDirection: 'row',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: theme.spacing['2'],
        }}
      >
        <View>
          <AppText size="xs" style={{ color: colors.text.secondary }}>
            {t('actingAsLabel')}
          </AppText>
          <AppText size="lg" weight="bold" style={{ color: colors.text.primary }}>
            {t(`codes.${activeRole}` as never)}
          </AppText>
        </View>
        {switchable && (
          <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
            {t('change')}
          </AppText>
        )}
      </Pressable>

      <Modal visible={open} transparent animationType="fade" onRequestClose={() => setOpen(false)}>
        <Pressable
          onPress={() => setOpen(false)}
          accessibilityLabel={t('close')}
          style={{
            flex: 1,
            backgroundColor: colors.surface.overlay,
            justifyContent: 'flex-end',
          }}
        >
          <View
            style={{
              backgroundColor: colors.surface.raised,
              borderTopLeftRadius: theme.borderRadius.xl,
              borderTopRightRadius: theme.borderRadius.xl,
              padding: theme.spacing['4'],
              gap: theme.spacing['2'],
            }}
          >
            <AppText size="lg" weight="bold">
              {t('chooseTitle')}
            </AppText>
            {/* No re-authentication, which is the requirement — and the reason the list is
                only the hats this person actually holds. There is nothing here to refuse. */}
            <AppText size="sm" style={{ color: colors.text.muted }}>
              {t('noPasswordNeeded')}
            </AppText>
            {failed && (
              <AppText size="sm" style={{ color: colors.text.primary }}>
                {failed}
              </AppText>
            )}
            <ScrollView style={{ maxHeight: theme.spacing['24'] * 3 }}>
              {operator.roleCodes.map((role) => {
                const selected = role === activeRole;
                const roleHue = hueFor(role);
                return (
                  <Pressable
                    key={role}
                    accessibilityRole="radio"
                    accessibilityState={{ selected, disabled: busy !== null }}
                    disabled={busy !== null}
                    onPress={() => void choose(role)}
                    testID={`role-option-${role}`}
                    style={{
                      minHeight: theme.size.touchTarget,
                      paddingHorizontal: theme.spacing['3'],
                      paddingVertical: theme.spacing['3'],
                      marginBottom: theme.spacing['2'],
                      borderRadius: theme.borderRadius.md,
                      borderWidth: 1,
                      borderColor: selected ? roleHue.edge : colors.border.subtle,
                      borderLeftWidth: theme.spacing['1'],
                      borderLeftColor: roleHue.edge,
                      backgroundColor: selected ? roleHue.band : colors.surface.base,
                      flexDirection: 'row',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                    }}
                  >
                    <AppText weight={selected ? 'bold' : 'regular'}>
                      {t(`codes.${role}` as never)}
                    </AppText>
                    {selected && (
                      <AppText size="sm" style={{ color: colors.text.secondary }}>
                        {t('current')}
                      </AppText>
                    )}
                  </Pressable>
                );
              })}
            </ScrollView>
          </View>
        </Pressable>
      </Modal>
    </>
  );
}
