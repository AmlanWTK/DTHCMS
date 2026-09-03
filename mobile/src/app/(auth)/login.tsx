import { useRouter } from 'expo-router';
import { useEffect, useState } from 'react';
import { TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { ApiError, NetworkError } from '@dthcms/api-client';

import { AppButton } from '@/components/AppButton';
import { AppText } from '@/components/AppText';
import { ScreenShell } from '@/components/ScreenShell';
import { deviceIdentity } from '@/lib/device';
import { theme, useTokens } from '@/lib/tokens';
import { usePreferences } from '@/stores/preferences';
import { useSession, type Proof } from '@/stores/session';

/**
 * The sign-in screen.
 *
 * Deliberately plain, and the same three sentences as the web's: the server's one
 * refusal message, "cannot reach the clinic server", or "something broke, quote this".
 * Every refusal from the server is the same 401 whatever the cause (docs/identity.md
 * §7.3), and the screen shows that message and nothing more — it does not know whether
 * the code exists, and it must not look as if it does.
 *
 * The employee code field capitalises as typed: codes are upper-case on the roster, and a
 * tablet keyboard defaults to lower.
 */

type Refusal =
  | { kind: 'credentials'; message: string }
  | { kind: 'offline' }
  | { kind: 'server'; message: string; correlationID: string }
  | null;

export default function LoginScreen() {
  const t = useTranslations('login');
  const tScreen = useTranslations('screen');
  const language = usePreferences((state) => state.language);
  const { colors, status } = useTokens();
  const router = useRouter();

  const tSecondFactor = useTranslations('secondFactor');
  const tDevice = useTranslations('device');
  // Whether this tablet is enrolled (CP18). Shown, not enforced: an unenrolled tablet can
  // sign a person in; the server refuses the clinical write later, with a reason.
  const [deviceName, setDeviceName] = useState<string | null | undefined>(undefined);
  useEffect(() => {
    let cancelled = false;
    void deviceIdentity().then((identity) => {
      if (!cancelled) setDeviceName(identity?.name ?? null);
    });
    return () => {
      cancelled = true;
    };
  }, []);
  const sessionStatus = useSession((state) => state.status);
  const signIn = useSession((state) => state.signIn);
  const completeSecondFactor = useSession((state) => state.completeSecondFactor);

  const [employeeCode, setEmployeeCode] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [refusal, setRefusal] = useState<Refusal>(null);
  // The second step: set when the password was right and a code is owed.
  const [challenge, setChallenge] = useState<string | null>(null);
  const [proofMode, setProofMode] = useState<'code' | 'recovery'>('code');
  const [proofValue, setProofValue] = useState('');

  // Somebody who is already signed in has no business here.
  useEffect(() => {
    if (sessionStatus === 'authenticated') router.replace('/queue');
  }, [sessionStatus, router]);

  async function submit() {
    if (busy) return;
    setBusy(true);
    setRefusal(null);
    try {
      const result = await signIn(employeeCode.trim().toUpperCase(), password);
      if (result.kind === 'second-factor') {
        setPassword('');
        setChallenge(result.challenge);
        return;
      }
      router.replace('/queue');
    } catch (error) {
      setPassword('');
      if (error instanceof NetworkError) {
        setRefusal({ kind: 'offline' });
      } else if (error instanceof ApiError && error.status === 401) {
        setRefusal({
          kind: 'credentials',
          message: language === 'bn' ? error.messageBN : error.messageEN,
        });
      } else if (error instanceof ApiError) {
        setRefusal({
          kind: 'server',
          message: language === 'bn' ? error.messageBN : error.messageEN,
          correlationID: error.correlationID,
        });
      } else {
        setRefusal({ kind: 'server', message: t('unexpected'), correlationID: '' });
      }
    } finally {
      setBusy(false);
    }
  }

  async function submitProof() {
    if (!challenge || busy) return;
    const proof: Proof =
      proofMode === 'code' ? { code: proofValue.trim() } : { recoveryCode: proofValue.trim() };
    setBusy(true);
    setRefusal(null);
    try {
      await completeSecondFactor(challenge, proof);
      router.replace('/queue');
    } catch (error) {
      setProofValue('');
      if (error instanceof NetworkError) {
        setRefusal({ kind: 'offline' });
      } else if (error instanceof ApiError && error.status === 401) {
        setRefusal({ kind: 'credentials', message: tSecondFactor('refused') });
      } else if (error instanceof ApiError) {
        setRefusal({
          kind: 'server',
          message: language === 'bn' ? error.messageBN : error.messageEN,
          correlationID: error.correlationID,
        });
      } else {
        setRefusal({ kind: 'server', message: t('unexpected'), correlationID: '' });
      }
    } finally {
      setBusy(false);
    }
  }

  const canSubmit = employeeCode.trim() !== '' && password !== '' && !busy;
  const proofReady =
    proofMode === 'code'
      ? /^\d{6}$/.test(proofValue.trim())
      : proofValue.replace(/[\s-]/g, '').length >= 16;

  const field = {
    minHeight: theme.size.touchTarget,
    borderRadius: theme.borderRadius.md,
    borderWidth: 1,
    borderColor: colors.border.control,
    backgroundColor: colors.surface.raised,
    paddingHorizontal: theme.spacing['4'],
    color: colors.text.primary,
    fontSize: theme.fontSize.base,
  };

  const banner = (tone: 'critical' | 'stale', title: string, body?: string) => (
    <View
      accessibilityRole="alert"
      accessibilityLiveRegion="assertive"
      style={{
        backgroundColor: status[tone].surface,
        borderColor: status[tone].border,
        borderWidth: 1,
        borderRadius: theme.borderRadius.md,
        padding: theme.spacing['4'],
        gap: theme.spacing['1'],
      }}
    >
      <AppText weight="semibold" style={{ color: status[tone].text }}>
        {title}
      </AppText>
      {body ? (
        <AppText size="sm" style={{ color: status[tone].text }}>
          {body}
        </AppText>
      ) : null}
    </View>
  );

  if (challenge) {
    return (
      <ScreenShell titleKey="screen.login">
        <View style={{ gap: theme.spacing['4'] }}>
          <AppText weight="semibold" size="lg">
            {tSecondFactor('loginTitle')}
          </AppText>
          <AppText style={{ color: colors.text.secondary }}>{tSecondFactor('loginBody')}</AppText>

          {refusal?.kind === 'credentials' && banner('critical', refusal.message)}
          {refusal?.kind === 'offline' && banner('stale', t('offlineTitle'), t('offlineBody'))}
          {refusal?.kind === 'server' && banner('critical', refusal.message)}

          <View style={{ gap: theme.spacing['1'] }}>
            <AppText size="sm" weight="medium">
              {proofMode === 'code' ? tSecondFactor('codeLabel') : tSecondFactor('recoveryLabel')}
            </AppText>
            <TextInput
              style={field}
              accessibilityLabel={
                proofMode === 'code' ? tSecondFactor('codeLabel') : tSecondFactor('recoveryLabel')
              }
              value={proofValue}
              onChangeText={(v) => setProofValue(proofMode === 'code' ? v.replace(/\D/g, '') : v)}
              keyboardType={proofMode === 'code' ? 'number-pad' : 'default'}
              autoCapitalize={proofMode === 'code' ? 'none' : 'characters'}
              autoCorrect={false}
              autoComplete={proofMode === 'code' ? 'one-time-code' : 'off'}
              textContentType={proofMode === 'code' ? 'oneTimeCode' : 'none'}
              maxLength={proofMode === 'code' ? 6 : 24}
              editable={!busy}
              returnKeyType="go"
              onSubmitEditing={() => void submitProof()}
              autoFocus
            />
            <AppText size="xs" style={{ color: colors.text.muted }}>
              {proofMode === 'code' ? tSecondFactor('codeHint') : tSecondFactor('recoveryHint')}
            </AppText>
          </View>

          <AppButton
            label={busy ? t('signingIn') : tSecondFactor('continue')}
            disabled={!proofReady || busy}
            onPress={() => void submitProof()}
          />
          <AppButton
            variant="secondary"
            label={proofMode === 'code' ? tSecondFactor('useRecovery') : tSecondFactor('useCode')}
            disabled={busy}
            onPress={() => {
              setProofMode(proofMode === 'code' ? 'recovery' : 'code');
              setProofValue('');
            }}
          />
          <AppButton
            variant="secondary"
            label={tSecondFactor('startOver')}
            disabled={busy}
            onPress={() => {
              setChallenge(null);
              setProofValue('');
              setRefusal(null);
            }}
          />
        </View>
      </ScreenShell>
    );
  }

  return (
    <ScreenShell titleKey="screen.login">
      <View style={{ gap: theme.spacing['4'] }}>
        <AppText style={{ color: colors.text.secondary }}>{t('subtitle')}</AppText>

        {refusal?.kind === 'credentials' && banner('critical', refusal.message)}
        {refusal?.kind === 'offline' && banner('stale', t('offlineTitle'), t('offlineBody'))}
        {refusal?.kind === 'server' &&
          banner(
            'critical',
            refusal.message,
            refusal.correlationID ? t('reference', { id: refusal.correlationID }) : undefined,
          )}

        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="sm" weight="medium">
            {t('employeeCode')}
          </AppText>
          <TextInput
            style={field}
            accessibilityLabel={t('employeeCode')}
            value={employeeCode}
            onChangeText={setEmployeeCode}
            autoCapitalize="characters"
            autoCorrect={false}
            autoComplete="username"
            textContentType="username"
            editable={!busy}
            returnKeyType="next"
          />
        </View>

        <View style={{ gap: theme.spacing['1'] }}>
          <AppText size="sm" weight="medium">
            {t('password')}
          </AppText>
          <TextInput
            style={field}
            accessibilityLabel={t('password')}
            value={password}
            onChangeText={setPassword}
            secureTextEntry
            autoCapitalize="none"
            autoCorrect={false}
            autoComplete="current-password"
            textContentType="password"
            editable={!busy}
            returnKeyType="go"
            onSubmitEditing={() => void submit()}
          />
        </View>

        <AppButton
          label={busy ? t('signingIn') : tScreen('login')}
          disabled={!canSubmit}
          onPress={() => void submit()}
        />

        {deviceName !== undefined && (
          <View
            className="flex-row items-center justify-between"
            style={{ marginTop: theme.spacing['4'] }}
          >
            <AppText size="sm" style={{ color: colors.text.muted, flexShrink: 1 }}>
              {tDevice('loginLine', {
                state: deviceName
                  ? tDevice('stateEnrolled', { name: deviceName })
                  : tDevice('stateNotEnrolled'),
              })}
            </AppText>
            <AppButton
              variant="secondary"
              label={tDevice('manage')}
              onPress={() => router.push('/device')}
            />
          </View>
        )}
      </View>
    </ScreenShell>
  );
}
