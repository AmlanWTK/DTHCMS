/**
 * `dashboard` — §8's three panels, the screen Dr. Nahid spends the working day in (CP73).
 *
 * # What this feature is, and what it deliberately is not
 *
 * It is a **composition**. Almost nothing clinical is drawn here for the first time: the
 * allergy strip is CP54's, the percentile card is CP48's, every value goes through CP61's
 * attribution component and CP44's dual-unit one, and the summary's six states are CP71's own
 * API shape rendered honestly. What this feature adds is the layout, the AI marking, the
 * sparklines, the accept/edit/reject controls, and the one request that feeds all of it.
 *
 * Rebuilding any of those would have been the mistake this checkpoint is most exposed to: a
 * dashboard with its own allergy strip is a dashboard where CP54's distinction between "nobody
 * asked" and "somebody asked and there are none" has to be got right a second time.
 *
 * # The four properties worth knowing before changing anything here
 *
 * **One request.** `GET /v1/patients/{id}/dashboard` returns every panel, and the components
 * that fetch for themselves read caches this feature primes. Adding a `useQuery` to a panel is
 * how the acceptance criterion quietly stops being true.
 *
 * **AI content is enclosed, not labelled.** `AiMarked` gives a machine-written region its own
 * ground, a sticky header and a repeating gutter, because a chip at the top of a page of prose
 * is invisible after one scroll and absent from a photograph of the screen. The right panel is
 * a *mixture* — a model's proposals and the assembler's own arithmetic — so the enclosure goes
 * around the model's half only, and every item additionally says which it is in a word.
 *
 * **Attribution on every value.** Nothing draws a clinical number except through
 * `ValueWithAttribution`, including the five points inside each sparkline. That is criterion 5
 * and it is why the payload carries whole observations rather than formatted numbers.
 *
 * **A withheld panel is not an empty one.** `WithheldNote` draws the difference. "No active
 * conditions" and "you were not shown the active conditions" are opposite facts, and the one
 * that looks reassuring is the wrong one.
 */
export { PhysicianDashboard, type PhysicianDashboardProps } from './components/PhysicianDashboard';
export { PatientPicker } from './components/PatientPicker';
export { SnapshotPanel, type SnapshotPanelProps } from './components/SnapshotPanel';
export { SummaryPanel, type SummaryPanelProps } from './components/SummaryPanel';
export { AssistantPanel, type AssistantPanelProps } from './components/AssistantPanel';
export { SuggestionCard, type SuggestionCardProps } from './components/SuggestionCard';
export { Sparkline, type SparklineProps } from './components/Sparkline';
export { AiMarked, OriginMark, type AiMarkedProps } from './components/AiMarked';
export { WithheldNote, type WithheldNoteProps } from './components/WithheldNote';
export { ShortcutHelp, type ShortcutHelpProps } from './components/ShortcutHelp';
export {
  DASHBOARD_SHORTCUTS,
  useDashboardShortcuts,
  type DashboardShortcutHandlers,
} from './components/useDashboardShortcuts';
export { useDashboardLayout, type DashboardLayout } from './components/useDashboardLayout';
export { bmiClassLabel, codeLabel, gapSentence, gapTone } from './components/dashboardText';
export {
  dashboardKey,
  decideSuggestion,
  decidedAgainstAnOlderRun,
  listTodaysPatients,
  omissionFor,
  primeDashboardCaches,
  readDashboard,
  suggestionsInWorkingOrder,
  todaysPatientsKey,
  type DashboardAccess,
  type DashboardAssistant,
  type DashboardBodyMass,
  type DashboardIdentity,
  type DashboardOmission,
  type DashboardSuggestion,
  type DashboardSummary,
  type DashboardTrend,
  type DashboardVisit,
  type DecisionInput,
  type PhysicianDashboard as PhysicianDashboardView,
  type SuggestionDecision,
  type SuggestionDecisionKind,
  type SuggestionKind,
  type SuggestionOrigin,
} from './api/dashboard';
export { requestSummary } from './api/summary';
