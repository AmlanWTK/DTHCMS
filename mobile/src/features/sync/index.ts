/**
 * What an operator is shown about their own data (CP66's metrics, CP67's ladder; §13.9).
 *
 * The logic this draws lives in two places, and the split is deliberate. `lib/sync` is the
 * application's write path and the engine — imported by every station, and the only thing that
 * touches the outbox. `state.ts` here is the presentation half: which sentence, how loudly, what a
 * person may be shown and what they may do about it. Both are pure and both are tested; the four
 * `.tsx` files are arrangement and hold no rule at all.
 */

export { SyncItems } from './SyncItems';
export { SyncPanel, type SyncPanelProps } from './SyncPanel';
export { SyncProvider, useSyncEngine, useSyncMetrics, type SyncMetricsView } from './SyncProvider';
export {
  ACTS,
  CLINIC_PROSE_PERMISSION,
  OFFLINE_FACTS,
  actsFor,
  entryKey,
  hasRestateableValue,
  itemOf,
  itemsOf,
  mayReadClinicProse,
  pillFor,
  reasonFor,
  refusedValueOf,
  type Act,
  type OfflineFact,
  type PillView,
  type ReasonView,
  type RefusedValue,
  type SyncItem as SyncItemView,
  type SyncItems as SyncItemGroups,
  type Viewer,
} from './state';
