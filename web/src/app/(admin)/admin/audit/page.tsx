import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { AuditViewer } from '@/features/audit';

/**
 * Administration → Audit trail (CP22): who did what, as sentences; verify the chain;
 * take it away signed.
 */
export default function AuditPage() {
  const t = useTranslations('audit');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <AuditViewer />
    </div>
  );
}
