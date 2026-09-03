import Constants from 'expo-constants';
import { useRouter } from 'expo-router';
import { useCallback, useEffect, useState } from 'react';
import { Platform, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { ScreenShell } from '@/components/ScreenShell';
import { API_BASE_URL } from '@/lib/api';
import { APP_VERSION } from '@/lib/build';
import { deviceIdentity, enrolDevice, forgetDevice } from '@/lib/device';
import { theme, useTokens } from '@/lib/tokens';

/**
 * This device: enrolled or not, and the one field that changes that (CP18).
 *
 * Reachable signed in or out. The usual moment is before anyone has signed in on a new
 * tablet — an administrator has just issued a code from the console and walks it over.
 * The code is typed here; the tablet makes its key, sends the public half, and from then
 * on every request is signed.
 *
 * Forgetting an enrolment is deliberately quiet and deliberately reversible only by an
 * administrator: the tablet drops its key, and needs a fresh code to get another.
 */

type Identity = { id: string; name: string } | null | 'loading';

export default function DeviceScreen() {
  const t = useTranslations('device');
  const router = useRouter();
  const { colors, status } = useTokens();

  const [identity, setIdentity] = useState<Identity>('loading');
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ tone: 'critical' | 'stale' | 'ok'; text: string } | null>(
    null,
  );

  const reload = useCallback(async () => {
    setIdentity(await deviceIdentity());
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  async function enrol() {
    if (busy || code.replace(/[\s-]/g, '').length < 10) return;
    setBusy(true);
    setNotice(null);
    try {
      const result = await enrolDevice({
        baseUrl: API_BASE_URL,
        code,
        model: Constants.deviceName ?? 'unknown',
        osVersion: `${Platform.OS} ${String(Platform.Version)}`,
        appVersion: APP_VERSION,
      });
      switch (result.kind) {
        case 'enrolled':
          setCode('');
          setNotice({ tone: 'ok', text: t('done') });
          await reload();
          break;
        case 'refused':
          setNotice({ tone: 'critical', text: t('refused') });
          break;
        case 'offline':
          setNotice({ tone: 'stale', text: t('offline') });
          break;
      }
    } catch {
      setNotice({ tone: 'critical', text: t('unexpected') });
    } finally {
      setBusy(false);
    }
  }

  async function forget() {
    await forgetDevice();
    setNotice(null);
    await reload();
  }

  const field = {
    minHeight: theme.size.touchTarget,
    borderRadius: theme.borderRadius.md,
    borderWidth: 1,
    borderColor: colors.border.control,
    backgroundColor: colors.surface.raised,
    paddingHorizontal: theme.spacing['4'],
    color: colors.text.primary,
    fontSize: theme.fontSize.lg,
    letterSpacing: 2,
  };

  const banner = notice && (
    <View
      accessibilityRole="alert"
      style={{
        backgroundColor: notice.tone === 'ok' ? colors.brand.subtle : status[notice.tone].surface,
        borderColor: notice.tone === 'ok' ? colors.brand.border : status[notice.tone].border,
        borderWidth: 1,
        borderRadius: theme.borderRadius.md,
        padding: theme.spacing['4'],
      }}
    >
      <AppText
        weight="semibold"
        style={{ color: notice.tone === 'ok' ? colors.brand.text : status[notice.tone].text }}
      >
        {notice.text}
      </AppText>
    </View>
  );

  if (identity === 'loading') {
    return (
      <ScreenShell titleKey="screen.device">
        <View />
      </ScreenShell>
    );
  }

  if (identity) {
    return (
      <ScreenShell titleKey="screen.device">
        <View style={{ gap: theme.spacing['4'] }}>
          {banner}
          <AppText weight="semibold" size="lg">
            {t('enrolledTitle', { name: identity.name })}
          </AppText>
          <AppText style={{ color: colors.text.secondary }}>{t('enrolledBody')}</AppText>
          <View style={{ gap: theme.spacing['1'] }}>
            <AppText size="sm" weight="medium">
              {t('idLabel')}
            </AppText>
            <AppText size="sm" style={{ color: colors.text.muted, fontVariant: ['tabular-nums'] }}>
              {identity.id}
            </AppText>
          </View>
          <AppButton variant="secondary" label={t('back')} onPress={() => router.back()} />
          <View style={{ gap: theme.spacing['1'] }}>
            <AppButton variant="secondary" label={t('forget')} onPress={() => void forget()} />
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {t('forgetHint')}
            </AppText>
          </View>
        </View>
      </ScreenShell>
    );
  }

  return (
    <ScreenShell titleKey="screen.device">
      <View style={{ gap: theme.spacing['4'] }}>
        {banner}
        <AppText weight="semibold" size="lg">
          {t('notEnrolledTitle')}
        </AppText>
        <AppText style={{ color: colors.text.secondary }}>{t('notEnrolledBody')}</AppText>

        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="sm" weight="medium">
            {t('codeLabel')}
          </AppText>
          <TextInput
            style={field}
            accessibilityLabel={t('codeLabel')}
            value={code}
            onChangeText={(v) => setCode(v.toUpperCase())}
            autoCapitalize="characters"
            autoCorrect={false}
            autoComplete="off"
            maxLength={12}
            editable={!busy}
            returnKeyType="go"
            onSubmitEditing={() => void enrol()}
            placeholder="XXXXX-XXXXX"
            placeholderTextColor={colors.text.muted}
          />
          <AppText size="xs" style={{ color: colors.text.muted }}>
            {t('codeHint')}
          </AppText>
        </View>

        <AppButton
          label={busy ? t('enrolling') : t('enrol')}
          disabled={busy || code.replace(/[\s-]/g, '').length < 10}
          onPress={() => void enrol()}
        />
        <AppButton variant="secondary" label={t('back')} onPress={() => router.back()} />
      </View>
    </ScreenShell>
  );
}
