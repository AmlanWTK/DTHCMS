import { writing } from '@dthcms/api-client';
import type { components } from '@dthcms/api-client';

import { STEP_UP_HEADER } from '@/features/auth';
import { api, unwrap } from '@/lib/api';

/**
 * The medication safety rule library, typed against the contract (CP77, §6.3, D-22).
 *
 * # Who opens this screen
 *
 * One person: Dr. Nahid. D-22 made the physician the author of every clinical rule, and the
 * permission to write or publish is granted to his role alone. Everything here is arranged around
 * that — the forms use his words, the preview says the rule back in a sentence, and the sandbox
 * lets him try it before anybody's prescription meets it.
 *
 * # An unapproved rule is not a rule
 *
 * Forty rules ship drafted from published guidance. Every one is `approved: false`, every one
 * cites where it came from, and none of them does anything to any prescription. There is
 * deliberately no helper here that hides that: `RuleSummary` carries `approved` and `origin`
 * together, so a component cannot draw a rule without having been handed the fact that nobody at
 * this clinic has agreed to it.
 *
 * # The preview is not computed here
 *
 * It would be one fewer round trip and it is the wrong place. The preview is the physician's
 * check that what the system understood is what he meant, and a sentence composed from the form's
 * own state can only ever agree with the form. The server renders it from the canonical condition
 * it will actually evaluate — so it can disagree, which is the case worth catching.
 */

export type MedicationRule = components['schemas']['MedicationRule'];
export type RuleSummary = components['schemas']['MedicationRuleSummary'];
export type RuleVersion = components['schemas']['MedicationRuleVersion'];
export type RuleCondition = components['schemas']['MedicationRuleCondition'];
export type RulePredicate = components['schemas']['MedicationRulePredicate'];
export type RuleTarget = components['schemas']['MedicationRuleTarget'];
export type RulePlain = components['schemas']['MedicationRulePlain'];
export type RuleVocabulary = components['schemas']['MedicationRuleVocabulary'];
export type RuleDraft = components['schemas']['MedicationRuleDraft'];
export type AllergenMap = components['schemas']['AllergenMap'];
export type SandboxResult = components['schemas']['MedicationRuleSandboxResult'];
export type SandboxContext = components['schemas']['MedicationRuleContext'];
export type RuleFinding = components['schemas']['MedicationRuleFinding'];
export type RuleExport = components['schemas']['MedicationRuleExport'];
export type ImportReport = components['schemas']['MedicationRuleImportReport'];

export type RuleType = MedicationRule['type'];
export type Severity = RuleSummary['severity'];

export const RULES_KEY = ['medication-rules'] as const;
export const VOCABULARY_KEY = ['medication-rules', 'vocabulary'] as const;
export const ALLERGENS_KEY = ['medication-rules', 'allergens'] as const;

export interface RuleFilter {
  type: RuleType | '';
  unapprovedOnly: boolean;
}

export function rulesKey(filter: RuleFilter) {
  return ['medication-rules', 'list', filter.type, filter.unapprovedOnly] as const;
}

export function ruleKey(id: string) {
  return ['medication-rules', 'rule', id] as const;
}

/* ------------------------------------------------------------------------- */
/* Reads                                                                      */
/* ------------------------------------------------------------------------- */

export async function listRules(filter: RuleFilter) {
  return unwrap(
    api.GET('/v1/medication-rules', {
      params: {
        query: {
          type: filter.type || undefined,
          unapproved: filter.unapprovedOnly || undefined,
          limit: 200,
        },
      },
    }),
  );
}

export async function getRule(id: string) {
  return unwrap(api.GET('/v1/medication-rules/{ruleId}', { params: { path: { ruleId: id } } }));
}

export async function getVocabulary(): Promise<RuleVocabulary> {
  return unwrap(api.GET('/v1/medication-rules/vocabulary', {}));
}

export async function getAllergens(): Promise<AllergenMap> {
  return unwrap(api.GET('/v1/medication-rules/allergens', {}));
}

export async function exportRules(): Promise<RuleExport> {
  return unwrap(api.GET('/v1/medication-rules/export', {}));
}

/* ------------------------------------------------------------------------- */
/* Writes                                                                     */
/* ------------------------------------------------------------------------- */

export interface DraftInput extends RuleDraft {
  code?: string;
  type?: RuleType;
}

/**
 * The plain-language preview, and the validation behind it.
 *
 * A half-written rule is the normal state of the authoring screen, so this never throws for an
 * incomplete rule: it answers `valid: false` with the problem in words. A form that showed a red
 * error envelope after every keystroke is one the author stops reading.
 */
export async function previewRule(
  type: RuleType,
  draft: RuleDraft,
): Promise<{ plain: RulePlain; valid: boolean; problem?: string }> {
  return unwrap(
    api.POST('/v1/medication-rules/preview', {
      params: writing(),
      // `code` is required by the create schema, which the preview shares, and is ignored by
      // the preview handler. Sent as a placeholder rather than making the screen hold two
      // shapes of the same rule: two shapes is how the two come to disagree.
      body: { ...draft, type, code: 'PREVIEW' },
    }),
  );
}

export async function createRule(type: RuleType, code: string, draft: RuleDraft) {
  return unwrap(
    api.POST('/v1/medication-rules', {
      params: writing(),
      // Upper-cased here as well as on the server, so the code the author sees on the
      // confirmation is the code every finding will quote.
      body: { ...draft, code: code.trim().toUpperCase(), type },
    }),
  );
}

export async function draftNewVersion(ruleId: string) {
  return unwrap(
    api.POST('/v1/medication-rules/{ruleId}/versions', {
      params: { ...writing(), path: { ruleId } },
    }),
  );
}

export async function saveDraft(ruleId: string, versionId: string, draft: RuleDraft) {
  return unwrap(
    api.PUT('/v1/medication-rules/{ruleId}/versions/{versionId}', {
      params: { ...writing(), path: { ruleId, versionId } },
      body: draft,
    }),
  );
}

/**
 * Publishing. Needs a step-up token, which `useStepUp` obtains — the caller never sees a code.
 */
export async function publishVersion(ruleId: string, versionId: string, stepUpToken: string) {
  return unwrap(
    api.POST('/v1/medication-rules/{ruleId}/versions/{versionId}/publish', {
      params: { ...writing({ [STEP_UP_HEADER]: stepUpToken }), path: { ruleId, versionId } },
    }),
  );
}

export async function withdrawRule(ruleId: string, reason: string, stepUpToken: string) {
  return unwrap(
    api.POST('/v1/medication-rules/{ruleId}/withdraw', {
      params: { ...writing({ [STEP_UP_HEADER]: stepUpToken }), path: { ruleId } },
      body: { reason },
    }),
  );
}

export async function approveCrossReaction(id: string, stepUpToken: string): Promise<AllergenMap> {
  return unwrap(
    api.POST('/v1/medication-rules/allergens/cross-reactions/{id}/approve', {
      params: { ...writing({ [STEP_UP_HEADER]: stepUpToken }), path: { id } },
    }),
  );
}

/**
 * The sandbox. Either a saved version or the rule on screen, never both — one field meaning
 * "the version I have open" and "what I am looking at" is how somebody publishes having tested
 * something else.
 */
export async function runSandbox(input: {
  versionId?: string;
  type?: RuleType;
  rule?: RuleDraft;
  patient: SandboxContext;
}): Promise<SandboxResult> {
  return unwrap(
    api.POST('/v1/medication-rules/sandbox', {
      params: writing(),
      body: {
        version_id: input.versionId,
        type: input.type,
        rule: input.rule,
        patient: input.patient,
      },
    }),
  );
}

export async function importRules(doc: RuleExport, apply: boolean): Promise<ImportReport> {
  return unwrap(
    api.POST('/v1/medication-rules/import', {
      params: { ...writing(), query: { mode: apply ? 'APPLY' : 'DRY_RUN' } },
      body: doc,
    }),
  );
}
