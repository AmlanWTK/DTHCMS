/**
 * What an operator is shown about their own data (CP66's metrics, §13.9; CP67 owns the rest).
 *
 * The logic this draws lives in `lib/sync`, which is the application's write path and is imported
 * by every station. What is here is a screen and the provider that decides when the engine runs —
 * both React Native, both untestable outside a device, and both deliberately holding no decisions
 * of their own.
 */

export { SyncPanel, type SyncPanelProps } from './SyncPanel';
export { SyncProvider, useSyncEngine } from './SyncProvider';
