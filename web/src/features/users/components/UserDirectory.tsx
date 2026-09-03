'use client';

import Link from 'next/link';
import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useMemo, useState } from 'react';

import { ApiError, NetworkError } from '@dthcms/api-client';
import { AlertBanner, Badge, Button, Card, Input, Select, Skeleton } from '@dthcms/ui';

import { Can } from '@/components/Can';

import {
  listRoles,
  listUsers,
  type AdminAccount,
  type AdminUser,
  type RoleDefinition,
  type UserStatus,
} from '../api/users';
import { InviteForm } from './InviteForm';

/**
 * Administration → Users: everyone with an account at this clinic (CP21).
 *
 * The list answers the questions an administrator actually arrives with — "does she have
 * an account", "why can't he sign in", "who still hasn't set up an authenticator" — so it
 * shows the status, the roles, the second factor and the last sign-in, and lets the list be
 * narrowed by status or by typing part of a name or code. Everything else is on the
 * account page.
 */

const STATUSES: UserStatus[] = ['invited', 'active', 'suspended', 'deactivated'];

export const STATUS_TONE: Record<UserStatus, 'neutral' | 'brand' | 'info'> = {
  invited: 'info',
  active: 'brand',
  suspended: 'neutral',
  deactivated: 'neutral',
};

export function useExplain() {
  const t = useTranslations('users');
  const locale = useLocale();
  return useCallback(
    (error: unknown): string => {
      if (error instanceof NetworkError) return t('offline');
      if (error instanceof ApiError) return locale === 'bn' ? error.messageBN : error.messageEN;
      return t('unexpected');
    },
    [t, locale],
  );
}

export function UserDirectory() {
  const t = useTranslations('users');
  const locale = useLocale();
  const explain = useExplain();

  const [users, setUsers] = useState<AdminUser[] | null>(null);
  const [catalogue, setCatalogue] = useState<RoleDefinition[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [status, setStatus] = useState<UserStatus | 'all'>('all');
  const [query, setQuery] = useState('');
  const [inviting, setInviting] = useState(false);
  const [created, setCreated] = useState<{ account: AdminAccount; password: string } | null>(null);

  const reload = useCallback(async () => {
    try {
      const [people, roles] = await Promise.all([listUsers(), listRoles()]);
      setUsers(people);
      setCatalogue(roles);
      setLoadError(null);
    } catch (error) {
      setLoadError(explain(error));
    }
  }, [explain]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const shown = useMemo(() => {
    if (!users) return [];
    const needle = query.trim().toLowerCase();
    return users.filter(
      (u) =>
        (status === 'all' || u.status === status) &&
        (!needle ||
          u.employee_code.toLowerCase().includes(needle) ||
          u.name_en.toLowerCase().includes(needle) ||
          u.name_bn.includes(query.trim())),
    );
  }, [users, status, query]);

  const counts = useMemo(() => {
    const out: Record<string, number> = {};
    for (const u of users ?? []) out[u.status] = (out[u.status] ?? 0) + 1;
    return out;
  }, [users]);

  function onInvited(account: AdminAccount, password: string) {
    setInviting(false);
    setCreated({ account, password });
    void reload();
  }

  return (
    <div className="app-stack">
      {created ? (
        <CreatedCard
          account={created.account}
          password={created.password}
          onDone={() => setCreated(null)}
        />
      ) : inviting ? (
        <InviteForm
          catalogue={catalogue}
          explain={explain}
          onInvited={onInvited}
          onCancel={() => setInviting(false)}
        />
      ) : null}

      <Card
        header={
          <div className="app-card__heading">
            <h2 className="app-card__title">{t('list.title')}</h2>
            {!inviting && !created && (
              <Can action="admin.users.manage">
                <Button
                  variant="primary"
                  size="sm"
                  iconStart="users"
                  onClick={() => setInviting(true)}
                  disabled={catalogue.length === 0}
                >
                  {t('list.invite')}
                </Button>
              </Can>
            )}
          </div>
        }
      >
        <div className="app-stack">
          <div className="app-form-row">
            <Input
              label={t('list.search')}
              labelHidden
              name="query"
              type="search"
              placeholder={t('list.searchPlaceholder')}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <Select
              label={t('list.status')}
              labelHidden
              name="status"
              value={status}
              onChange={(event) => setStatus(event.target.value as UserStatus | 'all')}
              options={[
                { value: 'all', label: t('list.allStatuses', { count: users?.length ?? 0 }) },
                ...STATUSES.map((value) => ({
                  value,
                  label: `${t(`status.${value}`)} (${counts[value] ?? 0})`,
                })),
              ]}
            />
          </div>

          {loadError ? (
            <AlertBanner tone="critical" title={loadError}>
              <Button variant="secondary" size="sm" onClick={() => void reload()}>
                {t('retry')}
              </Button>
            </AlertBanner>
          ) : users === null ? (
            <div className="app-stack" aria-busy="true">
              <Skeleton />
              <Skeleton />
              <Skeleton />
            </div>
          ) : shown.length === 0 ? (
            <p className="app-page__description">
              {users.length === 0 ? t('list.empty') : t('list.noMatch')}
            </p>
          ) : (
            <div className="app-table-wrap">
              <table className="app-table">
                <thead>
                  <tr>
                    <th scope="col">{t('list.person')}</th>
                    <th scope="col">{t('list.statusColumn')}</th>
                    <th scope="col">{t('list.roles')}</th>
                    <th scope="col">{t('list.secondFactor')}</th>
                    <th scope="col">{t('list.lastLogin')}</th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((user) => (
                    <UserRow key={user.id} user={user} locale={locale} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}

function UserRow({ user, locale }: { user: AdminUser; locale: string }) {
  const t = useTranslations('users');
  const tRole = useTranslations('role');
  const name = locale === 'bn' && user.name_bn ? user.name_bn : user.name_en;
  const other = locale === 'bn' ? user.name_en : user.name_bn;

  return (
    <tr>
      <th scope="row">
        <Link className="app-table__primary app-link" href={`/admin/users/${user.id}`}>
          {name}
        </Link>
        <div className="app-table__secondary">
          <code>{user.employee_code}</code>
          {other ? ` · ${other}` : ''}
        </div>
      </th>
      <td>
        <Badge tone={STATUS_TONE[user.status]}>{t(`status.${user.status}`)}</Badge>
        {user.status_reason && <div className="app-table__secondary">{user.status_reason}</div>}
      </td>
      <td>
        {user.roles.length === 0 ? (
          <span className="app-table__secondary">{t('list.noRoles')}</span>
        ) : (
          <div className="app-badge-row">
            {user.roles.map((role) => (
              <Badge key={role} tone="neutral">
                {tRole(role as never)}
              </Badge>
            ))}
          </div>
        )}
      </td>
      <td>
        <SecondFactorCell status={user.second_factor} />
      </td>
      <td>
        {user.last_login_at ? (
          <time dateTime={user.last_login_at}>{formatWhen(user.last_login_at, locale)}</time>
        ) : (
          <span className="app-table__secondary">{t('list.never')}</span>
        )}
      </td>
    </tr>
  );
}

export function SecondFactorCell({ status }: { status: AdminUser['second_factor'] }) {
  const t = useTranslations('users');
  if (status.enrolled) return <Badge tone="brand">{t('secondFactor.enrolled')}</Badge>;
  if (status.pending) return <Badge tone="info">{t('secondFactor.pending')}</Badge>;
  if (status.required) return <Badge tone="neutral">{t('secondFactor.missing')}</Badge>;
  return <span className="app-table__secondary">{t('secondFactor.none')}</span>;
}

export function formatWhen(iso: string, locale: string): string {
  return new Date(iso).toLocaleString(locale === 'bn' ? 'bn-BD' : 'en-GB', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// --- after an invitation: the details to hand over, shown once ---

function CreatedCard({
  account,
  password,
  onDone,
}: {
  account: AdminAccount;
  password: string;
  onDone: () => void;
}) {
  const t = useTranslations('users');
  return (
    <Card
      header={<h2 className="app-card__title">{t('created.title', { name: account.name_en })}</h2>}
    >
      <div className="app-stack">
        <p className="app-page__description">{t('created.body')}</p>
        <dl className="app-handover">
          <div>
            <dt>{t('created.code')}</dt>
            <dd>
              <code>{account.employee_code}</code>
            </dd>
          </div>
          <div>
            <dt>{t('created.password')}</dt>
            <dd>
              <code>{password}</code>
            </dd>
          </div>
        </dl>
        <AlertBanner tone="borderline" title={t('created.onceTitle')}>
          {t('created.onceBody')}
        </AlertBanner>
        <div className="app-actions">
          <Button variant="primary" onClick={onDone}>
            {t('created.done')}
          </Button>
          <Link
            className="dthc-button dthc-button--secondary dthc-button--md"
            href={`/admin/users/${account.id}`}
          >
            {t('created.open')}
          </Link>
        </div>
      </div>
    </Card>
  );
}
