import { PlaceholderScreen } from '@/components/PlaceholderScreen';
import { ScreenShell } from '@/components/ScreenShell';

export default function Screen() {
  return (
    <ScreenShell titleKey="screen.sync">
      <PlaceholderScreen labelKey="screen.sync" checkpoint="CP64" />
    </ScreenShell>
  );
}
