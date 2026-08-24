import { PlaceholderScreen } from '@/components/PlaceholderScreen';
import { ScreenShell } from '@/components/ScreenShell';

export default function Screen() {
  return (
    <ScreenShell titleKey="screen.patient">
      <PlaceholderScreen labelKey="screen.patient" checkpoint="CP42" />
    </ScreenShell>
  );
}
