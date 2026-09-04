import { Pressable, TextInput, View } from 'react-native';
import { useTranslations } from 'use-intl';

import { AppText } from '@/components/AppText';
import { theme, useTokens } from '@/lib/tokens';

import {
  MONOFILAMENT_SITES,
  answeredSites,
  missedSites,
  siteState,
  type MonofilamentSite,
  type MonofilamentState,
  type Side,
  type SiteState,
} from './form';

/**
 * One foot's monofilament test, as a foot (CP51 criterion 1).
 *
 * # Why a picture rather than ten rows
 *
 * The examiner is holding a filament in one hand and a foot in the other, and their eyes are
 * on the foot. Ten labelled rows would make them read "third metatarsal head" and then work
 * out where that is on the patient in front of them — twenty times, once per foot. A
 * diagram is looked at the way the foot is looked at, so the tap lands where the filament
 * just was and the screen never becomes the thing being examined.
 *
 * # Why it is built out of Views
 *
 * There is no SVG in this app and there will not be one for a foot outline. A drawing
 * library would be a new dependency, a new licence, a new thing that breaks on an OEM
 * Android, and a bundle every clinic tablet downloads over the same connection that already
 * struggles — all to draw a shape that is, at the fidelity anybody needs here, four rounded
 * rectangles. The sites are absolutely positioned round targets on that ground; the ground
 * is scenery and the targets are the interface.
 *
 * # Why colour is never the whole answer
 *
 * Roughly one man in twelve who will work in this clinic cannot rely on red against green,
 * and a tablet held under the window in Dhaka in April flattens both regardless. So felt is
 * a circle with a tick and not-felt is a square with a cross: the shape carries the finding,
 * the symbol repeats it, and the colour is the third copy rather than the only one.
 *
 * # Why "all ten felt" is a button
 *
 * Most feet in a screening clinic are normal, and ten taps to say so is where the two-minute
 * budget goes. It is an assertion the examiner makes after testing the foot, never a
 * default: an untouched diagram says nothing, and nothing is what the record then holds.
 */

/** How big the drawing is. Wide enough for a 48dp target at every site with room between. */
const GROUND = { width: 268, height: 392 };

/** Bigger than the 48dp floor, because these are tapped with a filament in the other hand. */
const TARGET = 54;

/**
 * Where each site sits, as a fraction of the ground.
 *
 * Written for a left foot and mirrored for a right one, rather than flipping the whole view
 * with a transform — a mirrored transform would flip the ticks and crosses too, and a
 * backwards tick is a symbol somebody has to stop and decode.
 *
 * `dorsum` is the one site on the other surface of the foot, and it is drawn over the instep
 * rather than out in the arch: putting it among the plantar sites would be a diagram that
 * lies about where the filament goes.
 */
const SITE_POSITIONS: Record<MonofilamentSite, { x: number; y: number }> = {
  hallux: { x: 0.22, y: 0.07 },
  toe_3: { x: 0.47, y: 0.04 },
  toe_5: { x: 0.71, y: 0.1 },
  mth_1: { x: 0.22, y: 0.27 },
  mth_3: { x: 0.47, y: 0.25 },
  mth_5: { x: 0.72, y: 0.29 },
  dorsum: { x: 0.47, y: 0.45 },
  medial_arch: { x: 0.23, y: 0.61 },
  lateral_arch: { x: 0.73, y: 0.63 },
  heel: { x: 0.47, y: 0.84 },
};

export function FootDiagram({
  side,
  state,
  onTapSite,
  onMarkAllFelt,
  onSetNotTested,
  onChangeReason,
}: {
  side: Side;
  state: MonofilamentState;
  onTapSite: (site: MonofilamentSite) => void;
  onMarkAllFelt: () => void;
  onSetNotTested: (notTested: boolean) => void;
  onChangeReason: (text: string) => void;
}) {
  const t = useTranslations('examination');
  const { colors, status } = useTokens();

  const answered = answeredSites(state);
  const missed = missedSites(state).length;

  return (
    <View style={{ gap: theme.spacing['3'] }}>
      <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: theme.spacing['2'] }}>
        <Pressable
          testID={`all-felt-${side}`}
          accessibilityRole="button"
          onPress={onMarkAllFelt}
          style={{
            minHeight: theme.size.touchTarget,
            justifyContent: 'center',
            paddingHorizontal: theme.spacing['4'],
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: status.normal.border,
            backgroundColor: status.normal.surface,
          }}
        >
          <AppText size="sm" weight="semibold" style={{ color: status.normal.text }}>
            {t('allFelt')}
          </AppText>
        </Pressable>

        <Pressable
          testID={`not-tested-${side}`}
          accessibilityRole="switch"
          accessibilityState={{ checked: state.notTested }}
          onPress={() => onSetNotTested(!state.notTested)}
          style={{
            minHeight: theme.size.touchTarget,
            justifyContent: 'center',
            paddingHorizontal: theme.spacing['4'],
            borderRadius: theme.borderRadius.md,
            borderWidth: 1,
            borderColor: state.notTested ? colors.brand.border : colors.border.subtle,
            backgroundColor: state.notTested ? colors.brand.subtle : colors.surface.raised,
          }}
        >
          <AppText
            size="sm"
            weight={state.notTested ? 'semibold' : 'regular'}
            style={{ color: state.notTested ? colors.brand.text : colors.text.secondary }}
          >
            {t('notTested')}
          </AppText>
        </Pressable>
      </View>

      {state.notTested ? (
        // "Not tested" on its own is a hole in the record that nobody can fill afterwards.
        // One line here is the difference between a foot with a dressing on it and a foot
        // somebody skipped.
        <TextInput
          testID={`not-tested-reason-${side}`}
          value={state.reason}
          onChangeText={onChangeReason}
          accessibilityLabel={t('notTestedReason')}
          placeholder={t('notTestedReason')}
          placeholderTextColor={colors.text.muted}
          style={{
            minHeight: theme.size.touchTarget,
            borderWidth: 1,
            borderColor: colors.border.control,
            borderRadius: theme.borderRadius.md,
            paddingHorizontal: theme.spacing['3'],
            backgroundColor: colors.surface.raised,
            color: colors.text.primary,
            fontSize: theme.fontSize.base,
          }}
        />
      ) : (
        <>
          <View
            testID={`foot-${side}`}
            accessibilityRole="none"
            style={{
              width: GROUND.width,
              height: GROUND.height,
              alignSelf: 'center',
            }}
          >
            <FootOutline side={side} />
            {MONOFILAMENT_SITES.map((site) => (
              <SiteTarget
                key={site}
                side={side}
                site={site}
                state={siteState(state, site)}
                label={t(`site.${site}`)}
                onPress={() => onTapSite(site)}
              />
            ))}
          </View>

          {/* The count, not the verdict. How many sites went unfelt is a fact the examiner
              can check against the foot; whether that is loss of protective sensation is the
              server's to decide, and a screen that announced it would be a second opinion
              nobody asked for. */}
          <AppText size="sm" style={{ color: colors.text.secondary, textAlign: 'center' }}>
            {t('sitesAnswered', { answered: String(answered), missed: String(missed) })}
          </AppText>

          <View
            style={{
              flexDirection: 'row',
              flexWrap: 'wrap',
              justifyContent: 'center',
              gap: theme.spacing['4'],
            }}
          >
            <LegendEntry state="felt" label={t('siteState.felt')} />
            <LegendEntry state="not_felt" label={t('siteState.not_felt')} />
            <LegendEntry state="unknown" label={t('siteState.unknown')} />
          </View>
        </>
      )}
    </View>
  );
}

/**
 * The foot itself: a toe pad, a forefoot, a midfoot and a heel, with the arch notched out of
 * the medial side so that left and right are told apart at a glance rather than by reading.
 */
function FootOutline({ side }: { side: Side }) {
  const { colors } = useTokens();
  const skin = {
    position: 'absolute' as const,
    backgroundColor: colors.surface.raised,
    borderWidth: 1,
    borderColor: colors.border.default,
  };
  const medial = side === 'LEFT' ? { left: -18 } : { right: -18 };

  return (
    <View
      style={{ position: 'absolute', top: 0, left: 0, right: 0, bottom: 0 }}
      pointerEvents="none"
    >
      {/* toes */}
      <View
        style={{
          ...skin,
          left: '4%',
          top: 0,
          width: '92%',
          height: '20%',
          borderRadius: theme.borderRadius.full,
        }}
      />
      {/* forefoot */}
      <View
        style={{
          ...skin,
          left: '3%',
          top: '12%',
          width: '94%',
          height: '38%',
          borderRadius: theme.borderRadius['2xl'],
        }}
      />
      {/* midfoot */}
      <View
        style={{
          ...skin,
          left: '12%',
          top: '44%',
          width: '76%',
          height: '32%',
          borderRadius: theme.borderRadius['2xl'],
        }}
      />
      {/* heel */}
      <View
        style={{
          ...skin,
          left: '10%',
          top: '64%',
          width: '80%',
          height: '34%',
          borderRadius: theme.borderRadius.full,
        }}
      />
      {/* the arch, cut out of the medial side */}
      <View
        style={{
          position: 'absolute',
          ...medial,
          top: '48%',
          width: 56,
          height: 92,
          borderRadius: theme.borderRadius.full,
          backgroundColor: colors.surface.sunken,
        }}
      />
    </View>
  );
}

/**
 * One site.
 *
 * The whole target is the button, not an icon inside it: a filament in the other hand and a
 * patient's foot in the way is not a situation for aiming.
 */
function SiteTarget({
  side,
  site,
  state,
  label,
  onPress,
}: {
  side: Side;
  site: MonofilamentSite;
  state: SiteState;
  label: string;
  onPress: () => void;
}) {
  const { colors, status } = useTokens();
  const position = SITE_POSITIONS[site];
  // Mirrored for the right foot, so the great toe is always on the patient's inside edge.
  const x = side === 'LEFT' ? position.x : 1 - position.x;

  const look = appearance(state, { colors, status });

  return (
    <Pressable
      testID={`site-${side}-${site}`}
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityValue={{ text: state }}
      onPress={onPress}
      style={{
        position: 'absolute',
        left: x * GROUND.width - TARGET / 2,
        top: position.y * GROUND.height,
        width: TARGET,
        height: TARGET,
        alignItems: 'center',
        justifyContent: 'center',
        borderWidth: theme.size.borderWidth.thick,
        borderStyle: look.dashed ? 'dashed' : 'solid',
        borderRadius: look.radius,
        borderColor: look.border,
        backgroundColor: look.surface,
      }}
    >
      <AppText size="lg" weight="bold" style={{ color: look.text }}>
        {look.symbol}
      </AppText>
    </Pressable>
  );
}

function LegendEntry({ state, label }: { state: SiteState; label: string }) {
  const { colors, status } = useTokens();
  const look = appearance(state, { colors, status });
  return (
    <View style={{ flexDirection: 'row', alignItems: 'center', gap: theme.spacing['1.5'] }}>
      <View
        style={{
          width: theme.size.icon.lg,
          height: theme.size.icon.lg,
          alignItems: 'center',
          justifyContent: 'center',
          borderWidth: 1,
          borderStyle: look.dashed ? 'dashed' : 'solid',
          borderRadius: look.radius,
          borderColor: look.border,
          backgroundColor: look.surface,
        }}
      >
        <AppText size="2xs" weight="bold" style={{ color: look.text }}>
          {look.symbol}
        </AppText>
      </View>
      <AppText size="xs" style={{ color: colors.text.secondary }}>
        {label}
      </AppText>
    </View>
  );
}

type Palette = Pick<ReturnType<typeof useTokens>, 'colors' | 'status'>;

/**
 * Shape, symbol and colour together, in one place.
 *
 * Kept as one function so the legend and the targets cannot drift apart: a legend that said
 * a cross meant one thing while the diagram drew another would be worse than no legend.
 */
function appearance(state: SiteState, { colors, status }: Palette) {
  switch (state) {
    case 'felt':
      return {
        symbol: '✓',
        radius: theme.borderRadius.full,
        dashed: false,
        border: status.normal.border,
        surface: status.normal.surface,
        text: status.normal.text,
      };
    case 'not_felt':
      return {
        symbol: '✕',
        radius: theme.borderRadius.sm,
        dashed: false,
        border: status.high.border,
        surface: status.high.surface,
        text: status.high.text,
      };
    default:
      return {
        symbol: '?',
        radius: theme.borderRadius.full,
        dashed: true,
        border: colors.border.control,
        surface: colors.surface.base,
        text: colors.text.muted,
      };
  }
}
