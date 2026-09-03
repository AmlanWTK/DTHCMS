/**
 * `auth` — signing in and out, and the second factor.
 *
 * The session itself lives in `@/stores/session`, because the whole shell reads it. What
 * this feature owns is the screens that establish one, and the prompt that asks for a fresh
 * second factor before a privileged action.
 */
export { LoginForm, safeNext } from './components/LoginForm';
export { SecuritySettings } from './components/SecuritySettings';
export { StepUpProvider, useStepUp } from './components/StepUpProvider';
export { StepUpCancelled } from './components/StepUpDialog';
export { SecondFactorNudge } from './components/SecondFactorNudge';
export { STEP_UP_HEADER, isStepUpRequired, type StepUpPurpose } from './api/secondFactor';
