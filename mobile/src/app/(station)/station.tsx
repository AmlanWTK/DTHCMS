import { PlaceholderScreen } from '@/components/PlaceholderScreen';
import { ScreenShell } from '@/components/ScreenShell';

export default function Screen() {
  return (
    <ScreenShell titleKey="screen.station">
      <PlaceholderScreen labelKey="screen.station" checkpoint="CP45" />
    </ScreenShell>
  );
}
