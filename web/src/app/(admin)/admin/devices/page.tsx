import { useTranslations } from 'next-intl';

import { PageHeader } from '@/components/PageHeader';
import { DeviceConsole } from '@/features/devices';

/**
 * Administration → Devices (CP18): the clinic's tablets — register, enrol, suspend,
 * revoke. The rest of the administration console arrives at CP21.
 */
export default function DevicesPage() {
  const t = useTranslations('devices');

  return (
    <div className="app-stack">
      <PageHeader title={t('title')} description={t('description')} />
      <DeviceConsole />
    </div>
  );
}
