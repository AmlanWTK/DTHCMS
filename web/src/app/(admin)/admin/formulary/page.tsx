import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { FormularyConsole } from '@/features/formulary';

/**
 * Administration → Medicine formulary (CP75, §10, §16.1, D-56).
 *
 * # Why it is under administration rather than under pharmacy
 *
 * `/pharmacy` is dispensing: what a named patient is owed today. The formulary is the clinic's
 * catalogue and its prices — configuration, with no patient in it, maintained between clinics
 * rather than during one. It sits beside the audit trail and the background queue for the same
 * reason those do: they are the screens somebody opens to ask what the *system* holds, not what a
 * patient does.
 *
 * # Why the permission is `formulary.view` and not `admin.view`
 *
 * The pharmacist owns this (§16.1) and holds none of the seven permissions behind `admin.view`.
 * Filing it under the administrator's umbrella would hide the screen from the one person whose
 * job it is — which is the same mistake CP69 avoided with the queue and the physician.
 */
export default function Page() {
  const t = useTranslations('page.formulary');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <FormularyConsole />
    </div>
  );
}
