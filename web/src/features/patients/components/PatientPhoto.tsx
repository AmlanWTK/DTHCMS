'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'next-intl';
import { useRef, useState } from 'react';

import { queryKeys } from '@dthcms/api-client';
import { AlertBanner, Button, Skeleton } from '@dthcms/ui';

import { attachPhoto, photoURL, uploadTicket } from '../api/patients';
import { MAX_BYTES, preparePhoto } from '../lib/photo';

/**
 * The patient's photograph (CP34).
 *
 * The upload does not pass through the API. The browser asks for a pre-signed URL, PUTs the
 * bytes straight to storage, and then tells the API the object is there. What the operator
 * sees is one button.
 *
 * `capture="user"` makes a phone browser open the camera rather than the gallery, which is
 * the difference between "take a photograph" and "find a photograph". On a laptop it is an
 * ordinary file picker, which is what the registration desk has.
 *
 * The displayed URL is short-lived and re-fetched rather than cached, because a signed URL
 * that has expired renders as a broken image and there is nothing on the screen to say why.
 */
export function PatientPhoto({ patientID }: { patientID: string }) {
  const t = useTranslations('patients.photo');
  const queryClient = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [problem, setProblem] = useState<string | null>(null);

  const photo = useQuery({
    queryKey: [...queryKeys.patient(patientID), 'photo'],
    queryFn: () => photoURL(patientID),
    // Re-fetched well before the server's fifteen-minute ceiling: a URL that expires while
    // the screen is open renders as a broken image with nothing to explain it.
    refetchInterval: 10 * 60 * 1000,
    retry: false,
  });

  const upload = useMutation({
    mutationFn: async (file: File) => {
      if (file.size > MAX_BYTES) throw new Error(t('tooLarge'));
      const prepared = await preparePhoto(file);
      const ticket = await uploadTicket(patientID, prepared.contentType);

      const response = await fetch(ticket.upload_url, {
        method: 'PUT',
        headers: { 'Content-Type': prepared.contentType },
        body: prepared.blob,
      });
      if (!response.ok) throw new Error(t('uploadFailed'));

      return attachPhoto(patientID, {
        object_key: ticket.object_key,
        content_type: prepared.contentType,
        width: prepared.width,
        height: prepared.height,
      });
    },
    onSuccess: () => {
      setProblem(null);
      void queryClient.invalidateQueries({ queryKey: [...queryKeys.patient(patientID), 'photo'] });
    },
    onError: (error) => setProblem(error instanceof Error ? error.message : t('uploadFailed')),
  });

  const has = photo.isSuccess && photo.data?.url;

  return (
    <section className="app-photo" aria-label={t('title')}>
      <div className="app-photo__frame" data-empty={has ? undefined : 'true'}>
        {photo.isPending ? (
          <Skeleton shape="block" width="100%" height="100%" />
        ) : has ? (
          // A plain <img>, not next/image. The URL is signed and short-lived on a storage
          // origin, and the optimiser would proxy and cache it — which is exactly what a
          // fifteen-minute TTL exists to prevent.
          <img src={photo.data.url} alt={t('alt')} width={160} height={160} />
        ) : (
          <p className="app-photo__none">{t('none')}</p>
        )}
      </div>

      {problem ? <AlertBanner tone="critical" title={problem} /> : null}

      <input
        ref={input}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        capture="user"
        hidden
        data-testid="photo-input"
        onChange={(event) => {
          const file = event.target.files?.[0];
          event.target.value = '';
          if (file) upload.mutate(file);
        }}
      />
      <Button
        variant="secondary"
        disabled={upload.isPending}
        onClick={() => input.current?.click()}
      >
        {upload.isPending ? t('uploading') : has ? t('replace') : t('take')}
      </Button>
      <p className="app-photo__note">{t('note')}</p>
    </section>
  );
}
