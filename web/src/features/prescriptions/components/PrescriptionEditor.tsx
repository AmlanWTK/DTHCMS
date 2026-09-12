'use client';

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { Button, Icon } from '@dthcms/ui';

import { api, unwrap } from '@/lib/api';
import type { Locale } from '@/lib/i18n/config';

import { MedicineCombobox, type MedicineChoice } from '@/features/formulary';

import {
  addItem,
  createDraft,
  getPrescription,
  getPrintModel,
  listInstructionTemplates,
  listPatientPrescriptions,
  listPrescribingDefaults,
  modifyItem,
  prescriptionKey,
  printModelKey,
  removeItem,
  resolveDefault,
  runSafetyCheck,
  safetyKey,
  type PrescribingDefault,
  type Prescription,
  type PrescriptionItem,
  type SafetyResult,
} from '../api/prescriptions';
import { PrintPreview } from './PrintPreview';
import { RenalIndicator } from './RenalIndicator';
import { SafetyPanel } from './SafetyPanel';
import { ShortcutHelp } from './ShortcutHelp';
import { usePrescriptionShortcuts } from './usePrescriptionShortcuts';

/**
 * The screen where Dr. Nahid actually prescribes (CP81).
 *
 * > *It must be faster than writing on paper. If prescribing is slower than paper, the system
 * > fails at its central promise regardless of everything else.*
 *
 * Everything below follows from that sentence and from four decisions, each of which cost
 * something somewhere else.
 *
 * # 1. One line at a time, and the keyboard never leaves the row it is in
 *
 * The alternative — a table of empty rows you tab through — looks more like a prescription pad and
 * is slower, because every row costs a decision about which cell to be in. Here there is exactly
 * one place to type at any moment:
 *
 *	two letters → ↓ / ↑ pick the brand, ← / → pick the strength, **Enter** takes it
 *	→ focus lands on the dose, already filled in with the suggestion
 *	→ **Enter** writes the line to the server and focus returns to the search box
 *
 * Four medicines is therefore four cycles of *type, Enter, Enter*. Tab reaches every field in the
 * row for the cases where the suggestion is wrong, and Escape abandons the row and goes back to
 * the search box.
 *
 * # 2. The suggestion fills the fields, and is marked while it does
 *
 * This is the decision I am least comfortable with and the first thing to put in front of Dr.
 * Nahid. A default that has to be retyped saves nothing, so the fields are filled — but a default
 * a machine wrote must never look like one he set, so the filled row sits inside a dashed block
 * that names the guidance it came from and says, in both languages, that nobody at this clinic has
 * checked it. The moment he presses Enter the values become his: CP80 records the line with his
 * attribution, and the suggestion is not mentioned in the ledger at all.
 *
 * The risk this accepts is that a tired physician presses Enter twice without reading. The
 * mitigation is that the dose field is where focus lands, so the suggested value is under the
 * cursor rather than somewhere on the page. Whether that is enough is a question for somebody who
 * prescribes for a living.
 *
 * # 3. Autosave is not a timer — a completed line is a server write
 *
 * There is no local draft buffer anywhere in this feature. Pressing Enter on a line posts
 * `PRESCRIPTION_ITEM_ADDED` through CP80's event path, and that is the save. A browser crash
 * therefore loses **the line being typed and nothing else**, which the footer says in words rather
 * than implying with a spinner. It also means no token, no draft and no patient data is ever put
 * in `localStorage` — ADR-0010 holds by construction here rather than by care.
 *
 * # 4. The safety check runs on the lines, not on the submit
 *
 * Every successful write schedules a check, debounced by 250ms so that adding three medicines in
 * six seconds does not fire three overlapping requests. The panel shows the result for the lines
 * that are actually on the sheet — `safetyKey` carries a revision, so a stale result cannot be
 * drawn beside a changed list.
 *
 * **What the panel shows today is that nothing has been checked**, because none of this clinic's
 * forty-eight rules is approved. That state is rendered as the loudest thing on the screen; see
 * `SafetyPanel.tsx`, which is where the reasoning belongs.
 */

export interface PrescriptionEditorProps {
  patientId: string;
  /** The visit this prescription is written at. Resolved by the page from the patient's open visit. */
  visitId?: string;
}

interface DraftLine {
  dose: string;
  frequency: string;
  durationDays: string;
  instructionCode: string;
}

const EMPTY_LINE: DraftLine = { dose: '', frequency: '', durationDays: '', instructionCode: '' };

export function PrescriptionEditor({ patientId, visitId }: PrescriptionEditorProps) {
  const t = useTranslations('prescriptions');
  const locale = useLocale() as Locale;
  const queryClient = useQueryClient();

  const [prescriptionId, setPrescriptionId] = useState<string | null>(null);
  const [chosen, setChosen] = useState<MedicineChoice | null>(null);
  const [line, setLine] = useState<DraftLine>(EMPTY_LINE);
  const [suggestion, setSuggestion] = useState<PrescribingDefault | undefined>();
  const [comboboxGeneration, setComboboxGeneration] = useState(0);
  const [revision, setRevision] = useState(0);
  const [showPreview, setShowPreview] = useState(true);
  const [showHelp, setShowHelp] = useState(false);
  const [removing, setRemoving] = useState<string | null>(null);
  const [removeReason, setRemoveReason] = useState('');
  const [lastSavedAt, setLastSavedAt] = useState<Date | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  const doseRef = useRef<HTMLInputElement>(null);
  const searchRef = useRef<HTMLDivElement>(null);
  const linesRef = useRef<HTMLOListElement>(null);
  const safetyRef = useRef<HTMLDivElement>(null);

  /* ----------------------------------------------------------------------- */
  /* What the editor is working on                                            */
  /* ----------------------------------------------------------------------- */

  const previous = useQuery({
    queryKey: ['patients', patientId, 'prescriptions'],
    queryFn: () => listPatientPrescriptions(patientId),
  });

  const defaults = useQuery({
    queryKey: ['prescribing-defaults'],
    queryFn: listPrescribingDefaults,
    // The whole set, once. Twenty-eight rows answering a keystroke from memory; see the API
    // module for why this is not a per-line lookup.
    staleTime: 5 * 60_000,
  });

  const templates = useQuery({
    queryKey: ['instruction-templates'],
    queryFn: listInstructionTemplates,
    staleTime: 5 * 60_000,
  });

  const sheet = useQuery({
    queryKey: prescriptionId ? prescriptionKey(prescriptionId) : ['prescriptions', 'none'],
    queryFn: () => getPrescription(prescriptionId as string),
    enabled: Boolean(prescriptionId),
  });

  const preview = useQuery({
    queryKey: prescriptionId ? printModelKey(prescriptionId) : ['print-model', 'none'],
    queryFn: () => getPrintModel(prescriptionId as string),
    enabled: Boolean(prescriptionId),
  });

  const prescription = sheet.data?.prescription as Prescription | undefined;
  const liveItems = useMemo(
    () => (prescription?.items ?? []).filter((item) => !item.removed_at),
    [prescription],
  );

  /* ----------------------------------------------------------------------- */
  /* The safety check                                                         */
  /* ----------------------------------------------------------------------- */

  const [safety, setSafety] = useState<SafetyResult | undefined>();
  const [checking, setChecking] = useState(false);
  const [checkFailed, setCheckFailed] = useState(false);

  useEffect(() => {
    if (!prescriptionId || liveItems.length === 0) {
      setSafety(undefined);
      return;
    }
    let cancelled = false;
    setChecking(true);
    // 250ms. Long enough that three medicines added in six seconds do not produce three
    // overlapping requests; short enough that criterion 2 — findings within 500ms of adding an
    // item — has most of its budget left for the round trip.
    const timer = setTimeout(() => {
      runSafetyCheck(prescriptionId)
        .then((result) => {
          if (cancelled) return;
          setSafety(result);
          setCheckFailed(false);
          queryClient.setQueryData(safetyKey(prescriptionId, revision), result);
        })
        .catch(() => {
          if (cancelled) return;
          // The previous result is dropped rather than left on screen. Findings for a
          // prescription that has since changed are worse than no findings.
          setSafety(undefined);
          setCheckFailed(true);
        })
        .finally(() => {
          if (!cancelled) setChecking(false);
        });
    }, 250);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [prescriptionId, revision, liveItems.length, queryClient]);

  /* ----------------------------------------------------------------------- */
  /* Resuming, which is what crash recovery actually is                        */
  /* ----------------------------------------------------------------------- */

  /**
   * A draft already open for this visit is adopted rather than a second one started.
   *
   * This is the whole of criterion 4, and it is a consequence of the autosave design rather than
   * a feature beside it. Because every completed line is already an event in the ledger, "recover
   * after a crash" reduces to "find the draft again" — and the draft is found by asking the
   * server which prescription this visit has, not by remembering an id in the browser.
   *
   * Matching on the visit and not only on the patient: a draft written at last month's visit is
   * not this consultation's prescription, and silently continuing it would put today's medicines
   * on a sheet dated four weeks ago.
   */
  useEffect(() => {
    if (prescriptionId || !visitId || !previous.data) return;
    const open = previous.data
      .map((entry) => entry.prescription)
      .find((sheet) => sheet.status === 'DRAFT' && sheet.visit_id === visitId);
    if (open) {
      setPrescriptionId(open.id);
      setRevision((n) => n + 1);
    }
  }, [prescriptionId, visitId, previous.data]);

  /* ----------------------------------------------------------------------- */
  /* Starting the prescription                                               */
  /* ----------------------------------------------------------------------- */

  const start = useMutation({
    mutationFn: (carryForwardFrom?: string) =>
      createDraft({ patientId, visitId: visitId as string, carryForwardFrom }),
    onSuccess: (created) => {
      setPrescriptionId(created.prescription.id);
      setRevision((n) => n + 1);
      setLastSavedAt(new Date());
      setProblem(null);
    },
    onError: (error: Error) => setProblem(error.message),
  });

  /* ----------------------------------------------------------------------- */
  /* Writing a line                                                           */
  /* ----------------------------------------------------------------------- */

  const commit = useMutation({
    mutationFn: async () => {
      if (!prescriptionId || !chosen) throw new Error('nothing to write');
      const template = (templates.data?.templates ?? []).find(
        (candidate) => candidate.code === line.instructionCode,
      );
      return addItem(prescriptionId, {
        productId: chosen.productId,
        label: `${chosen.tradeName}`,
        dose: line.dose.trim(),
        dailyDose: suggestion?.daily_dose ?? undefined,
        doseUnit: suggestion?.dose_unit || undefined,
        frequency: line.frequency.trim(),
        durationDays: line.durationDays ? Number(line.durationDays) : undefined,
        route: suggestion?.route || undefined,
        instructionsEn: template?.text_en,
        instructionsBn: template?.text_bn,
      });
    },
    onSuccess: async () => {
      setLastSavedAt(new Date());
      setProblem(null);
      setChosen(null);
      setSuggestion(undefined);
      setLine(EMPTY_LINE);
      // Remounting the combobox is how it is cleared and refocused: it owns its own typed
      // text and exposes no reset. A key bump is honest about that rather than reaching into
      // its state.
      setComboboxGeneration((n) => n + 1);
      setRevision((n) => n + 1);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: prescriptionKey(prescriptionId as string) }),
        queryClient.invalidateQueries({ queryKey: printModelKey(prescriptionId as string) }),
      ]);
    },
    onError: (error: Error) => setProblem(error.message),
  });

  const drop = useMutation({
    mutationFn: ({ itemId, reason }: { itemId: string; reason: string }) =>
      removeItem(prescriptionId as string, itemId, reason),
    onSuccess: async () => {
      setRemoving(null);
      setRemoveReason('');
      setLastSavedAt(new Date());
      setRevision((n) => n + 1);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: prescriptionKey(prescriptionId as string) }),
        queryClient.invalidateQueries({ queryKey: printModelKey(prescriptionId as string) }),
      ]);
    },
    onError: (error: Error) => setProblem(error.message),
  });

  /**
   * Reorder by rewriting both line numbers.
   *
   * Two PATCHes rather than one "move" call, because CP80 has no move: an item's position is
   * `line_no` and a modification is an event like any other. The whole line is resent because
   * `PRESCRIPTION_ITEM_MODIFIED` replaces the fields it carries — sending only `line_no` would
   * blank the dose.
   */
  const reorder = useMutation({
    mutationFn: async ({ index, delta }: { index: number; delta: number }) => {
      const target = index + delta;
      if (target < 0 || target >= liveItems.length) return;
      const a = liveItems[index];
      const b = liveItems[target];
      if (!a || !b) return;
      await modifyItem(prescriptionId as string, a.id, lineOf(b.line_no ?? target, a));
      await modifyItem(prescriptionId as string, b.id, lineOf(a.line_no ?? index, b));
    },
    onSuccess: async () => {
      setLastSavedAt(new Date());
      setRevision((n) => n + 1);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: prescriptionKey(prescriptionId as string) }),
        queryClient.invalidateQueries({ queryKey: printModelKey(prescriptionId as string) }),
      ]);
    },
    onError: (error: Error) => setProblem(error.message),
  });

  /* ----------------------------------------------------------------------- */
  /* Picking a medicine                                                       */
  /* ----------------------------------------------------------------------- */

  const onChoose = useCallback(
    (choice: MedicineChoice) => {
      setChosen(choice);
      const found = resolveDefault(
        defaults.data?.defaults ?? [],
        choice.genericName,
        choice.strength,
      );
      setSuggestion(found);
      setLine({
        dose: found?.dose ?? '',
        frequency: found?.frequency ?? '',
        durationDays: found?.duration_days ? String(found.duration_days) : '',
        instructionCode: found?.instruction_code ?? '',
      });
      // The dose is where the decision is, so it is where the cursor goes. A row that filled
      // itself and left focus in the search box would be a row somebody submits without
      // looking at.
      window.setTimeout(() => doseRef.current?.select(), 0);
    },
    [defaults.data],
  );

  const readyToCommit = Boolean(chosen && line.dose.trim() && line.frequency.trim());

  function onRowKeyDown(event: React.KeyboardEvent) {
    if (event.key === 'Enter' && readyToCommit && !commit.isPending) {
      event.preventDefault();
      commit.mutate();
      return;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      setChosen(null);
      setSuggestion(undefined);
      setLine(EMPTY_LINE);
      setComboboxGeneration((n) => n + 1);
    }
  }

  usePrescriptionShortcuts({
    focusSearch: () => {
      setChosen(null);
      setComboboxGeneration((n) => n + 1);
    },
    focusLines: () => linesRef.current?.focus(),
    focusSafety: () => safetyRef.current?.focus(),
    togglePreview: () => setShowPreview((open) => !open),
    toggleHelp: () => setShowHelp((open) => !open),
    carryForward: () => {
      const source = previous.data?.[0]?.prescription;
      if (source && !prescriptionId) start.mutate(source.id);
    },
  });

  /* ----------------------------------------------------------------------- */
  /* Render                                                                   */
  /* ----------------------------------------------------------------------- */

  if (!visitId) {
    return (
      <section className="app-card app-prescribe__blocked">
        <h2>{t('editor.noVisitTitle')}</h2>
        <p>{t('editor.noVisitBody')}</p>
      </section>
    );
  }

  if (!prescriptionId) {
    // The list is a list of envelopes — each entry is `{ prescription, available_transitions }`
    // — because the server decorates every prescription with what may happen to it next.
    const source = previous.data?.[0]?.prescription;
    const carried = source?.items?.filter((item) => !item.removed_at).length ?? 0;
    return (
      <section className="app-card app-prescribe__start">
        <h2>{t('editor.startTitle')}</h2>
        <p>{t('editor.startBody')}</p>
        <div className="app-prescribe__start-actions">
          <Button onClick={() => start.mutate(undefined)} disabled={start.isPending}>
            {t('editor.startBlank')}
          </Button>
          {source && carried > 0 ? (
            // One click, and the confirmation is the click — CP80 refuses a carry-forward
            // without an explicit yes, and this button is that yes. The count is on the
            // button so the yes is to something specific rather than to "last time".
            <Button
              variant="secondary"
              onClick={() => start.mutate(source.id)}
              disabled={start.isPending}
              data-testid="carry-forward"
            >
              {t('editor.carryForward', { count: carried })}
            </Button>
          ) : null}
        </div>
        {previous.data && previous.data.length === 0 ? (
          <p className="app-prescribe__note">{t('editor.noPrevious')}</p>
        ) : null}
        {problem ? <p className="app-prescribe__problem">{problem}</p> : null}
      </section>
    );
  }

  return (
    <div className={showPreview ? 'app-prescribe app-prescribe--split' : 'app-prescribe'}>
      <div className="app-prescribe__work">
        {/* CP79's criterion: a visible renal status indicator on the prescription editor. It is
            above the search box rather than beside the findings because it is read *before* a
            drug is chosen — "what is this patient's kidney function" is the question that
            decides whether metformin is on the list at all. */}
        <RenalIndicator patientId={patientId} />

        <section className="app-card app-prescribe__entry" ref={searchRef}>
          <h2 className="app-prescribe__heading">{t('editor.addTitle')}</h2>
          <MedicineCombobox key={comboboxGeneration} onChoose={onChoose} autoFocus />

          {chosen ? (
            <div
              className="app-prescribe__row"
              onKeyDown={onRowKeyDown}
              data-testid="dose-row"
              data-suggested={suggestion ? 'true' : undefined}
            >
              <p className="app-prescribe__chosen">
                <strong>
                  {chosen.tradeName} {chosen.strength}
                </strong>
                <span>{chosen.genericName}</span>
              </p>

              {suggestion ? (
                <div className="app-prescribe__suggestion" data-testid="suggestion-note">
                  <p className="app-prescribe__suggestion-head">
                    <Icon name="alert-triangle" size={14} />
                    {t('editor.suggestionUnchecked')}
                  </p>
                  <p className="app-prescribe__suggestion-why">
                    {locale === 'bn' ? suggestion.rationale_bn : suggestion.rationale_en}
                  </p>
                  <p className="app-prescribe__suggestion-source">
                    {t('editor.suggestionSource', {
                      source: suggestion.approval.source_citation,
                    })}
                  </p>
                </div>
              ) : (
                <p className="app-prescribe__suggestion-none">{t('editor.noSuggestion')}</p>
              )}

              <div className="app-prescribe__fields">
                <label className="app-prescribe__field">
                  <span>{t('editor.dose')}</span>
                  <input
                    ref={doseRef}
                    className="app-input"
                    value={line.dose}
                    autoComplete="off"
                    onChange={(e) => setLine({ ...line, dose: e.target.value })}
                    data-testid="dose"
                  />
                </label>
                <label className="app-prescribe__field">
                  <span>{t('editor.frequency')}</span>
                  <input
                    className="app-input"
                    list="dthcms-frequencies"
                    value={line.frequency}
                    autoComplete="off"
                    onChange={(e) => setLine({ ...line, frequency: e.target.value })}
                    data-testid="frequency"
                  />
                </label>
                <label className="app-prescribe__field app-prescribe__field--narrow">
                  <span>{t('editor.duration')}</span>
                  <input
                    className="app-input"
                    inputMode="numeric"
                    value={line.durationDays}
                    autoComplete="off"
                    onChange={(e) => setLine({ ...line, durationDays: e.target.value })}
                    data-testid="duration"
                  />
                </label>
                <label className="app-prescribe__field">
                  <span>{t('editor.instruction')}</span>
                  <select
                    className="app-input"
                    value={line.instructionCode}
                    onChange={(e) => setLine({ ...line, instructionCode: e.target.value })}
                    data-testid="instruction"
                  >
                    <option value="">{t('editor.noInstruction')}</option>
                    {(templates.data?.templates ?? []).map((template) => (
                      <option key={template.id} value={template.code}>
                        {locale === 'bn' ? template.label_bn : template.label_en}
                        {template.approval.approved ? '' : ` — ${t('editor.uncheckedShort')}`}
                      </option>
                    ))}
                  </select>
                </label>
              </div>

              <div className="app-prescribe__row-actions">
                <Button
                  onClick={() => commit.mutate()}
                  disabled={!readyToCommit || commit.isPending}
                  data-testid="commit-line"
                >
                  {t('editor.addLine')}
                </Button>
                <span className="app-prescribe__hint">{t('editor.enterHint')}</span>
              </div>
            </div>
          ) : null}

          {/* The frequency vocabulary migration 00064 seeds defaults in. A datalist rather than
              a select, because the set of real frequencies is open — a physician typing
              "every third day" must not be stopped by a list somebody finished in 2026. */}
          <datalist id="dthcms-frequencies">
            <option value="once daily" />
            <option value="twice daily" />
            <option value="three times daily" />
            <option value="four times daily" />
            <option value="once weekly" />
            <option value="at night" />
            <option value="as needed" />
          </datalist>
        </section>

        <section className="app-card">
          <h2 className="app-prescribe__heading">
            {t('editor.linesTitle', { count: liveItems.length })}
          </h2>
          {liveItems.length === 0 ? (
            <p className="app-prescribe__note">{t('editor.noLines')}</p>
          ) : (
            <ol className="app-prescribe__lines" ref={linesRef} tabIndex={-1}>
              {liveItems.map((item, index) => (
                <li key={item.id} className="app-prescribe__line" data-testid="line">
                  <div className="app-prescribe__line-body">
                    <p className="app-prescribe__line-medicine">
                      <strong>
                        {item.product_label} {item.strength}
                      </strong>
                      <span>{item.generic_name}</span>
                    </p>
                    <p className="app-prescribe__line-directions">
                      {[
                        item.dose,
                        item.frequency,
                        item.duration_days ? `${item.duration_days} d` : '',
                      ]
                        .filter(Boolean)
                        .join(' · ')}
                    </p>
                  </div>
                  <div className="app-prescribe__line-actions">
                    <button
                      type="button"
                      className="app-icon-button"
                      aria-label={t('editor.moveUp', { medicine: item.product_label })}
                      disabled={index === 0 || reorder.isPending}
                      onClick={() => reorder.mutate({ index, delta: -1 })}
                    >
                      <Icon name="arrow-up" size={16} />
                    </button>
                    <button
                      type="button"
                      className="app-icon-button"
                      aria-label={t('editor.moveDown', { medicine: item.product_label })}
                      disabled={index === liveItems.length - 1 || reorder.isPending}
                      onClick={() => reorder.mutate({ index, delta: 1 })}
                    >
                      <Icon name="arrow-down" size={16} />
                    </button>
                    <button
                      type="button"
                      className="app-icon-button"
                      aria-label={t('editor.remove', { medicine: item.product_label })}
                      onClick={() => {
                        setRemoving(item.id);
                        setRemoveReason('');
                      }}
                      data-testid="remove"
                    >
                      <Icon name="x" size={16} />
                    </button>
                  </div>
                  {removing === item.id ? (
                    // The reason is required by the server and is not defaulted here. "What
                    // was on this prescription at 14:05" stays answerable only if the row
                    // that came off at 14:06 says why.
                    <form
                      className="app-prescribe__remove"
                      onSubmit={(event) => {
                        event.preventDefault();
                        if (removeReason.trim())
                          drop.mutate({ itemId: item.id, reason: removeReason.trim() });
                      }}
                    >
                      <label className="app-prescribe__field">
                        <span>{t('editor.removeReason')}</span>
                        <input
                          className="app-input"
                          value={removeReason}
                          autoFocus
                          onChange={(e) => setRemoveReason(e.target.value)}
                          data-testid="remove-reason"
                        />
                      </label>
                      <Button type="submit" disabled={!removeReason.trim() || drop.isPending}>
                        {t('editor.removeConfirm')}
                      </Button>
                      <Button type="button" variant="secondary" onClick={() => setRemoving(null)}>
                        {t('editor.cancel')}
                      </Button>
                    </form>
                  ) : null}
                </li>
              ))}
            </ol>
          )}
        </section>

        <div ref={safetyRef} tabIndex={-1}>
          <SafetyPanel
            result={safety}
            checking={checking}
            failed={checkFailed}
            itemCount={liveItems.length}
          />
        </div>

        <footer className="app-prescribe__footer">
          <p data-testid="save-state">
            {lastSavedAt
              ? t('editor.savedAt', { time: lastSavedAt.toLocaleTimeString(locale) })
              : t('editor.nothingSaved')}
          </p>
          {/* Said in words rather than implied by a spinner: what a crash costs is the line
              being typed, because every completed line is already an event in the ledger. */}
          <p className="app-prescribe__crash">{t('editor.crashNote')}</p>
          {problem ? (
            <p className="app-prescribe__problem" role="alert">
              {problem}
            </p>
          ) : null}
        </footer>
      </div>

      {showPreview ? (
        <aside className="app-prescribe__preview">
          <PrintPreview model={preview.data} />
        </aside>
      ) : null}

      {showHelp ? <ShortcutHelp onClose={() => setShowHelp(false)} /> : null}
    </div>
  );
}

/** The whole line, for a modification that only means to move it. See `reorder`. */
function lineOf(lineNo: number, item: PrescriptionItem) {
  return {
    lineNo,
    dose: item.dose,
    dailyDose: item.daily_dose ?? undefined,
    doseUnit: item.dose_unit || undefined,
    frequency: item.frequency,
    durationDays: item.duration_days ?? undefined,
    route: item.route || undefined,
    instructionsEn: item.instructions_en || undefined,
    instructionsBn: item.instructions_bn || undefined,
  };
}

/** Re-exported so the page can resolve the visit without a second API module. */
export async function openVisitOf(patientId: string): Promise<string | undefined> {
  const body = await unwrap(
    api.GET('/v1/patients/{id}/visits', { params: { path: { id: patientId } } }),
  );
  const visits = body.visits ?? [];
  return visits.find((visit) => visit.status === 'open')?.id;
}
