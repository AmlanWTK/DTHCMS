import { useEffect } from 'react';
import { Modal, ScrollView, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';

import { startAlarm, stopAlarm } from './sound';
import {
  actionTextOf,
  alarmShouldSound,
  breachLine,
  deliveryOf,
  nameOf,
  ordered,
  type CriticalAlert,
} from './state';

/**
 * A critical value, in the hand of the person who just typed it (CP50 criterion 1).
 *
 * # Why it is a modal, and why it will not go away
 *
 * Everything else this app shows can be scrolled past. This cannot: it covers the screen, it
 * has no close button, and the only way out is a button that says the operator has read it.
 * The plan asks for exactly that — "cannot be dismissed without acknowledgement" — and the
 * reason is the failure it is designed against, which is not a clinician missing an alert but
 * an operator saving a form and moving to the next patient.
 *
 * # What the operator can and cannot do here
 *
 * They cannot answer the alert. That is a clinician's act, they do not hold the permission,
 * and a clinic where the person who typed the number can close the alert about it is a clinic
 * that can clear its own board. What they can do is say they have read it — which stops the
 * alarm — and, when nobody was watching, go and find somebody.
 *
 * # The two states
 *
 *  - **Somebody has it.** A clinician's screen received the alert. The operator is told so,
 *    and told to carry on.
 *  - **Nobody was watching.** The fail-safe. The instruction is to walk, in the largest words
 *    on the screen, because in a building whose Wi-Fi has just failed that is the only
 *    escalation path that still works.
 */
export function CriticalAlertModal({
  alerts,
  seen,
  onSeen,
}: {
  alerts: CriticalAlert[];
  /** True once the operator has said they have read it. Stops the alarm. */
  seen: boolean;
  onSeen: () => void;
}) {
  const t = useTranslations('alerts');
  const { colors, status } = useTokens();
  const language = usePreferences((state) => state.language);

  const sounding = alarmShouldSound(alerts, seen);
  useEffect(() => {
    if (sounding) startAlarm();
    else stopAlarm();
    return stopAlarm;
  }, [sounding]);

  if (alerts.length === 0) return null;

  const shown = ordered(alerts);
  const delivery = deliveryOf(alerts);

  return (
    <Modal
      visible
      animationType="fade"
      transparent={false}
      // Android's back button must not be a way out. The only exit is the button below.
      onRequestClose={() => undefined}
      testID="critical-alert"
    >
      <View style={{ flex: 1, backgroundColor: status.critical.surface }}>
        <ScrollView
          contentContainerStyle={{
            padding: theme.spacing['5'],
            gap: theme.spacing['4'],
            flexGrow: 1,
          }}
        >
          <View style={{ gap: theme.spacing['1'] }}>
            <AppText size="xs" weight="semibold" style={{ color: status.critical.text }}>
              {t('banner')}
            </AppText>
            <AppText size="2xl" weight="bold" style={{ color: status.critical.text }}>
              {t('title')}
            </AppText>
          </View>

          {shown.map((alert) => {
            const line = breachLine(alert);
            const action = actionTextOf(alert, language);
            return (
              <View
                key={alert.id}
                testID={`critical-alert-${alert.code}`}
                style={{
                  backgroundColor: colors.surface.raised,
                  borderRadius: theme.borderRadius.lg,
                  borderWidth: 2,
                  borderColor: status.critical.border,
                  padding: theme.spacing['4'],
                  gap: theme.spacing['2'],
                }}
              >
                <AppText size="sm" weight="semibold" style={{ color: colors.text.secondary }}>
                  {nameOf(alert, language)}
                </AppText>
                {/* The number, then the line it crossed. The operator has just typed the
                    first; what they have not got in their head is the second. */}
                <AppText size="3xl" weight="bold" style={{ color: status.critical.text }}>
                  {line.value}
                </AppText>
                <AppText size="sm" style={{ color: colors.text.secondary }}>
                  {alert.breached === 'low'
                    ? t('belowLimit', { limit: line.limit })
                    : t('aboveLimit', { limit: line.limit })}
                </AppText>
                {action !== '' ? (
                  <AppText size="base" weight="semibold">
                    {action}
                  </AppText>
                ) : null}
              </View>
            );
          })}

          <View
            testID="critical-alert-delivery"
            style={{
              backgroundColor:
                delivery === 'walk' ? status.critical.surface : colors.surface.raised,
              borderRadius: theme.borderRadius.lg,
              borderWidth: 2,
              borderColor: delivery === 'walk' ? status.critical.border : colors.border.subtle,
              padding: theme.spacing['4'],
              gap: theme.spacing['2'],
            }}
          >
            {delivery === 'walk' ? (
              <>
                {/* The fail-safe, in the largest words on the screen. */}
                <AppText size="xl" weight="bold" style={{ color: status.critical.text }}>
                  {t('nobodyWatching')}
                </AppText>
                <AppText size="base">{t('goAndFind')}</AppText>
              </>
            ) : (
              <>
                <AppText size="base" weight="semibold">
                  {t('sent')}
                </AppText>
                <AppText size="sm" style={{ color: colors.text.secondary }}>
                  {t('sentDetail')}
                </AppText>
              </>
            )}
          </View>

          <View style={{ flex: 1 }} />

          <AppButton
            testID="critical-alert-seen"
            label={delivery === 'walk' ? t('seenAndGoing') : t('seen')}
            variant="primary"
            onPress={onSeen}
          />
        </ScrollView>
      </View>
    </Modal>
  );
}
