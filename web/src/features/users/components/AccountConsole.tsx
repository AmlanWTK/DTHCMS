'use client';

import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useState } from 'react';

import { AlertBanner, Badge, Button, Card, Skeleton } from '@dthcms/ui';

import { Can } from '@/components/Can';
import { PageHeader } from '@/components/PageHeader';
import { StepUpCancelled, useStepUp } from '@/features/auth';
import { useSessionStore } from '@/stores/session';

import {
  changeStatus,
  endSessions,
  getUser,
  grantRole,
  listRoles,
  passwordAcceptable,
  PASSWORD_MIN,
  reasonRequiredFor,
  resetSecondFactor,
  revokeRole,
  setPassword,
  transitionsFor,
  type AdminAccount,
  type RoleDefinition,
  type TargetStatus,
} from '../api/users';
import { ConfirmDialog, type ConfirmRequest } from './ConfirmDialog';
import { generatePassword } from './InviteForm';
import { RolePicker } from './RolePicker';
import { formatWhen, SecondFactorCell, STATUS_TONE, useExplain } from './UserDirectory';

/**
 * One account, and everything an administrator may do to it (CP21).
 *
 * Four cards: the account itself (status, with the moves the lifecycle allows), the roles
 * (revoke with a reason, grant with the permission preview), the credentials (sessions,
 * password, authenticator — the resets CP17 left to this checkpoint), and the history of
 * every grant the person ever had. Each write goes: confirm-with-reason → step-up → call →
 * reload, and a refusal lands as a banner rather than a blank.
 */
export function AccountConsole({ id }: { id: string }) {
  const t = useTranslations('users');
  const locale = useLocale();
  const explain = useExplain();
  const requestStepUp = useStepUp();
  const me = useSessionStore((state) => state.user);

  const [account, setAccount] = useState<AdminAccount | null>(null);
  const [catalogue, setCatalogue] = useState<RoleDefinition[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [notice, setNotice] = useState<{ tone: 'critical' | 'info'; text: string } | null>(null);
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null);
  const [adding, setAdding] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  const reload = useCallback(async () => {
    try {
      const [found, roles] = await Promise.all([getUser(id), listRoles()]);
      setAccount(found);
      setCatalogue(roles);
      setLoadError(null);
    } catch (error) {
      setLoadError(explain(error));
    }
  }, [id, explain]);

  useEffect(() => {
    void reload();
  }, [reload]);

  /** Confirm → step up → do. The dialog stays open on a refusal so the reason is not lost. */
  function run(
    request: Omit<ConfirmRequest, 'onConfirm'>,
    purpose: 'user.manage' | 'credential.reset',
    description: string,
    act: (values: { reason: string; secret: string }, token: string) => Promise<string>,
  ) {
    setConfirm({
      ...request,
      onConfirm: async (values) => {
        let token: string;
        try {
          token = await requestStepUp(purpose, description);
        } catch (error) {
          if (error instanceof StepUpCancelled) return;
          throw new Error(explain(error));
        }
        try {
          const text = await act(values, token);
          setConfirm(null);
          setNotice({ tone: 'info', text });
          await reload();
        } catch (error) {
          throw new Error(explain(error));
        }
      },
    });
  }

  if (loadError) {
    return (
      <AlertBanner tone="critical" title={loadError}>
        <Button variant="secondary" size="sm" onClick={() => void reload()}>
          {t('retry')}
        </Button>
      </AlertBanner>
    );
  }
  if (!account) {
    return (
      <div className="app-stack" aria-busy="true">
        <Skeleton />
        <Skeleton />
        <Skeleton />
      </div>
    );
  }

  const self = me?.id === account.id;
  const name = locale === 'bn' && account.name_bn ? account.name_bn : account.name_en;
  const tRoleLabel = (code: string) => {
    const role = catalogue.find((r) => r.code === code);
    return role ? (locale === 'bn' ? role.name_bn : role.name_en) : code;
  };

  // --- the acts ---

  function move(to: TargetStatus) {
    run(
      {
        title: t(`account.move.${to}.title`, { name }),
        body: t(`account.move.${to}.body`),
        confirmLabel: t(`account.move.${to}.confirm`),
        destructive: to !== 'active',
        reasonRequired: reasonRequiredFor(to),
      },
      'user.manage',
      t(`account.move.${to}.stepUp`, { name }),
      async ({ reason }, token) => {
        await changeStatus(account!.id, to, reason, token);
        return t('account.moved', { name, status: t(`status.${to}`) });
      },
    );
  }

  function revoke(role: string) {
    run(
      {
        title: t('roles.revoke.title', { role: tRoleLabel(role), name }),
        body: t('roles.revoke.body'),
        confirmLabel: t('roles.revoke.confirm'),
        destructive: true,
        reasonRequired: true,
      },
      'user.manage',
      t('roles.revoke.stepUp', { role: tRoleLabel(role), name }),
      async ({ reason }, token) => {
        await revokeRole(account!.id, role, reason, token);
        return t('roles.revoked', { role: tRoleLabel(role), name });
      },
    );
  }

  async function grant() {
    if (adding.length === 0 || busy) return;
    setBusy(true);
    setNotice(null);
    try {
      const labels = adding.map(tRoleLabel).join(', ');
      const token = await requestStepUp(
        'user.manage',
        t('roles.grant.stepUp', { roles: labels, name }),
      );
      // One step-up covers one call; several roles are several calls, so the token is
      // spent on the first and a fresh one asked for each of the rest.
      let spend = token;
      for (const [i, role] of adding.entries()) {
        if (i > 0)
          spend = await requestStepUp(
            'user.manage',
            t('roles.grant.stepUp', { roles: tRoleLabel(role), name }),
          );
        await grantRole(account!.id, role, spend);
      }
      setAdding([]);
      setNotice({ tone: 'info', text: t('roles.granted', { roles: labels, name }) });
      await reload();
    } catch (error) {
      if (!(error instanceof StepUpCancelled))
        setNotice({ tone: 'critical', text: explain(error) });
    } finally {
      setBusy(false);
    }
  }

  function signOutEverywhere() {
    run(
      {
        title: t('credentials.sessions.title', { name }),
        body: t('credentials.sessions.body', { count: account!.sessions.length }),
        confirmLabel: t('credentials.sessions.confirm'),
        destructive: true,
        reasonRequired: true,
      },
      'credential.reset',
      t('credentials.sessions.stepUp', { name }),
      async ({ reason }, token) => {
        const n = await endSessions(account!.id, reason, token);
        return t('credentials.sessions.done', { count: n, name });
      },
    );
  }

  function newPassword() {
    run(
      {
        title: t('credentials.password.title', { name }),
        body: t('credentials.password.body'),
        confirmLabel: t('credentials.password.confirm'),
        reasonRequired: true,
        secret: {
          label: t('credentials.password.label'),
          hint: t('credentials.password.hint', { min: PASSWORD_MIN }),
          acceptable: passwordAcceptable,
          suggest: generatePassword,
        },
      },
      'credential.reset',
      t('credentials.password.stepUp', { name }),
      async ({ reason, secret }, token) => {
        await setPassword(account!.id, secret, reason, token);
        return t('credentials.password.done', { name });
      },
    );
  }

  function resetAuthenticator() {
    run(
      {
        title: t('credentials.authenticator.title', { name }),
        body: t('credentials.authenticator.body'),
        confirmLabel: t('credentials.authenticator.confirm'),
        destructive: true,
        reasonRequired: true,
      },
      'credential.reset',
      t('credentials.authenticator.stepUp', { name }),
      async ({ reason }, token) => {
        await resetSecondFactor(account!.id, reason, token);
        return t('credentials.authenticator.done', { name });
      },
    );
  }

  const moves = transitionsFor(account.status);

  return (
    <div className="app-stack">
      <PageHeader
        title={
          <span className="app-title-row">
            {name}
            <Badge tone={STATUS_TONE[account.status]}>{t(`status.${account.status}`)}</Badge>
            {self && <Badge tone="info">{t('account.you')}</Badge>}
          </span>
        }
        description={
          <>
            <code>{account.employee_code}</code>
            {locale === 'bn' ? account.name_en : account.name_bn ? ` · ${account.name_bn}` : ''}
            {' · '}
            <Link className="app-link" href="/admin/users">
              {t('account.back')}
            </Link>
          </>
        }
      />

      {notice && (
        <AlertBanner tone={notice.tone} title={notice.text} onDismiss={() => setNotice(null)} />
      )}

      {/* --- the account --- */}
      <Card header={<h2 className="app-card__title">{t('account.title')}</h2>}>
        <div className="app-stack">
          <dl className="app-facts">
            <div>
              <dt>{t('account.phone')}</dt>
              <dd>{account.phone || <span className="app-table__secondary">—</span>}</dd>
            </div>
            <div>
              <dt>{t('account.email')}</dt>
              <dd>{account.email || <span className="app-table__secondary">—</span>}</dd>
            </div>
            <div>
              <dt>{t('account.since', { status: t(`status.${account.status}`) })}</dt>
              <dd>
                <time dateTime={account.status_since}>
                  {formatWhen(account.status_since, locale)}
                </time>
                {account.status_reason && (
                  <div className="app-table__secondary">{account.status_reason}</div>
                )}
              </dd>
            </div>
            <div>
              <dt>{t('account.lastLogin')}</dt>
              <dd>
                {account.last_login_at ? (
                  formatWhen(account.last_login_at, locale)
                ) : (
                  <span className="app-table__secondary">{t('list.never')}</span>
                )}
              </dd>
            </div>
            <div>
              <dt>{t('account.created')}</dt>
              <dd>{formatWhen(account.created_at, locale)}</dd>
            </div>
          </dl>
          <Can action="admin.users.manage">
            {self ? (
              <p className="app-table__secondary">{t('account.selfLocked')}</p>
            ) : (
              <div className="app-actions">
                {moves.map((to) => (
                  <Button
                    key={to}
                    variant={
                      to === 'active' ? 'primary' : to === 'deactivated' ? 'danger' : 'secondary'
                    }
                    size="sm"
                    onClick={() => move(to)}
                  >
                    {t(`account.move.${to}.button`)}
                  </Button>
                ))}
              </div>
            )}
          </Can>
        </div>
      </Card>

      {/* --- the roles --- */}
      <Card header={<h2 className="app-card__title">{t('roles.title')}</h2>}>
        <div className="app-stack">
          {account.roles.length === 0 ? (
            <p className="app-page__description">{t('roles.none')}</p>
          ) : (
            <ul className="app-role-list">
              {account.roles.map((role) => (
                <li key={role}>
                  <div>
                    <div className="app-table__primary">{tRoleLabel(role)}</div>
                    <div className="app-table__secondary">{role}</div>
                  </div>
                  <Can action="admin.users.manage">
                    <Button
                      variant="quiet"
                      size="sm"
                      onClick={() => revoke(role)}
                      disabled={self && role === 'ADMIN'}
                    >
                      {t('roles.revoke.button')}
                    </Button>
                  </Can>
                </li>
              ))}
            </ul>
          )}
          <Can action="admin.users.manage">
            <details className="app-disclosure">
              <summary>{t('roles.grant.open')}</summary>
              <div className="app-stack">
                <RolePicker
                  catalogue={catalogue}
                  chosen={adding}
                  held={account.roles}
                  onChange={setAdding}
                  disabled={busy}
                />
                <div className="app-actions">
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={() => void grant()}
                    loading={busy}
                    disabled={adding.length === 0 || busy}
                  >
                    {t('roles.grant.button', { count: adding.length })}
                  </Button>
                </div>
              </div>
            </details>
          </Can>
          <div>
            <div className="app-role-picker__preview-title">
              {t('roles.effective', { count: account.permissions.length })}
            </div>
            {account.permissions.length > 0 && (
              <ul className="app-permission-list">
                {account.permissions.map((permission) => (
                  <li key={permission}>
                    <code>{permission}</code>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </Card>

      {/* --- the credentials --- */}
      <Card header={<h2 className="app-card__title">{t('credentials.title')}</h2>}>
        <div className="app-stack">
          <dl className="app-facts">
            <div>
              <dt>{t('credentials.authenticator.label')}</dt>
              <dd>
                <SecondFactorCell status={account.second_factor} />
                {account.second_factor.enrolled && (
                  <div className="app-table__secondary">
                    {t('credentials.recoveryCodes', {
                      count: account.second_factor.recovery_codes_left,
                    })}
                  </div>
                )}
              </dd>
            </div>
            <div>
              <dt>{t('credentials.sessions.label')}</dt>
              <dd>
                {account.sessions.length === 0 ? (
                  <span className="app-table__secondary">{t('credentials.sessions.none')}</span>
                ) : (
                  <ul className="app-plain-list">
                    {account.sessions.map((s) => (
                      <li key={s.id}>
                        {s.user_agent || t('credentials.sessions.unknownAgent')}
                        <span className="app-table__secondary">
                          {' · '}
                          {t('credentials.sessions.seen', {
                            when: formatWhen(s.last_seen_at, locale),
                          })}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </dd>
            </div>
          </dl>
          <Can action="admin.credentials.reset">
            <div className="app-actions">
              <Button variant="secondary" size="sm" iconStart="key-round" onClick={newPassword}>
                {t('credentials.password.button')}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={resetAuthenticator}
                disabled={
                  self || (!account.second_factor.enrolled && !account.second_factor.pending)
                }
              >
                {t('credentials.authenticator.button')}
              </Button>
              <Button
                variant="quiet"
                size="sm"
                iconStart="log-out"
                onClick={signOutEverywhere}
                disabled={account.sessions.length === 0}
              >
                {t('credentials.sessions.button')}
              </Button>
            </div>
          </Can>
        </div>
      </Card>

      {/* --- the history --- */}
      <Card header={<h2 className="app-card__title">{t('history.title')}</h2>}>
        {account.grant_history.length === 0 ? (
          <p className="app-page__description">{t('history.empty')}</p>
        ) : (
          <div className="app-table-wrap">
            <table className="app-table">
              <thead>
                <tr>
                  <th scope="col">{t('history.role')}</th>
                  <th scope="col">{t('history.granted')}</th>
                  <th scope="col">{t('history.revoked')}</th>
                </tr>
              </thead>
              <tbody>
                {account.grant_history.map((entry, i) => (
                  <tr key={`${entry.role}-${entry.granted_at}-${i}`}>
                    <th scope="row">
                      <div className="app-table__primary">{tRoleLabel(entry.role)}</div>
                      <div className="app-table__secondary">{entry.role}</div>
                    </th>
                    <td>
                      <time dateTime={entry.granted_at}>
                        {formatWhen(entry.granted_at, locale)}
                      </time>
                    </td>
                    <td>
                      {entry.revoked_at ? (
                        <>
                          <time dateTime={entry.revoked_at}>
                            {formatWhen(entry.revoked_at, locale)}
                          </time>
                          {entry.revoke_reason && (
                            <div className="app-table__secondary">{entry.revoke_reason}</div>
                          )}
                        </>
                      ) : (
                        <Badge tone="brand">{t('history.live')}</Badge>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <ConfirmDialog request={confirm} onCancel={() => setConfirm(null)} />
    </div>
  );
}
