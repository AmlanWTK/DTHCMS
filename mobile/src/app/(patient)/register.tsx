import { useRouter } from 'expo-router';

import { ScreenShell } from '@/components/ScreenShell';
import { RegistrationFlow } from '@/features/registration';
import { api } from '@/lib/api';

/**
 * Registration on a phone (CP33).
 *
 * The secondary surface, on purpose: registration involves more typing than any other
 * station and a keyboard beats a phone at typing. This exists for the two cases the desk
 * cannot cover — the desk is busy, and the registrar is at an outreach camp with no desk.
 */
export default function Screen() {
  const router = useRouter();

  return (
    <ScreenShell titleKey="screen.register">
      <RegistrationFlow
        newEventID={() => crypto.randomUUID()}
        onSubmit={async (body) => {
          await api.POST('/v1/patients', {
            params: {
              header: {
                'X-Requested-With': 'DTHCMS',
                'Idempotency-Key': crypto.randomUUID(),
              },
            },
            body,
          });
          router.replace('/queue');
        }}
      />
    </ScreenShell>
  );
}
