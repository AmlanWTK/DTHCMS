import { useCallback, useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { ScreenShell } from '@/components/ScreenShell';
import { StationQueue, type QueueRow } from '@/features/queue';
import { api } from '@/lib/api';

/**
 * My station's queue (CP39) — the screen a station operator lives in for a morning.
 *
 * The station comes from the session's active role, not from a picker: an operator working at
 * Anthropometry is at Anthropometry, and a screen that lets them choose is a screen where
 * somebody calls a patient to the wrong room.
 */
export default function QueueScreen() {
  const t = useTranslations('queue');
  const [rows, setRows] = useState<QueueRow[]>([]);
  const [station, setStation] = useState('');
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  const load = useCallback(async (code: string) => {
    const result = await api.GET('/v1/stations/{station}/queue', {
      params: { path: { station: code } },
    });
    if (result.data) setRows(result.data.entries as QueueRow[]);
  }, []);

  useEffect(() => {
    let live = true;
    api
      .GET('/v1/auth/me')
      .then((result) => {
        // The station comes from the role the operator holds, not from a picker. An operator
        // working at Anthropometry is at Anthropometry, and a screen that lets them choose is
        // a screen where somebody calls a patient to the wrong room.
        const code = result.data?.grants.find((grant) => grant.station)?.station ?? '';
        if (!live || !code) return;
        setStation(code);
        return load(code);
      })
      .catch(() => {
        if (live) setFailed(true);
      });
    return () => {
      live = false;
    };
  }, [load]);

  return (
    <ScreenShell titleKey="screen.queue">
      {failed ? (
        <AppText>{t('emptyDetail')}</AppText>
      ) : (
        <StationQueue
          stationName={station}
          rows={rows}
          busy={busy}
          onCallNext={async () => {
            setBusy(true);
            try {
              await api.POST('/v1/stations/{station}/call-next', {
                params: {
                  path: { station },
                  header: {
                    'X-Requested-With': 'DTHCMS',
                    'Idempotency-Key': crypto.randomUUID(),
                  },
                },
                body: { event_id: crypto.randomUUID() },
              });
              await load(station);
            } finally {
              setBusy(false);
            }
          }}
          onStart={() => {
            // The encounter is opened by the station's own capture screen, which is the
            // checkpoint that owns the measurement. Refreshing here keeps the list honest.
            void load(station);
          }}
        />
      )}
    </ScreenShell>
  );
}
