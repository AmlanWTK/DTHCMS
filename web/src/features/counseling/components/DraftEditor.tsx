'use client';

import { useMutation } from '@tanstack/react-query';
import { useLocale, useTranslations } from 'next-intl';
import { useEffect, useMemo, useRef, useState } from 'react';

import { ApiError, fieldMessages } from '@dthcms/api-client';
import { AlertBanner, Button, Card, Input, Select } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import {
  draftFrom,
  emptyItemRow,
  moveRow,
  publishBlockers,
  removeRow,
  rowsFrom,
  saveBlockers,
  saveCounselingDraft,
  suggestItemCode,
  type CounselingRoom,
  type CounselingVersion,
  type ItemDraftRow,
  type SaveBlocker,
} from '../api/counseling';

import { roomName } from './counselingText';

/**
 * Writing a checklist (CP55, §5.1, criterion 1).
 *
 * # Saving is not publishing, and they are not adjacent
 *
 * This component owns exactly one write: `saveCounselingDraft`. There is no publish button
 * in this file. Publishing lives in its own panel, below, owned by the workspace — because a
 * physician who meant to fix a typo must not be able to do the other thing by pressing the
 * nearer control, and the cheapest guarantee of that is that the two controls are not built
 * by the same component and never sit side by side.
 *
 * # Half-written saves
 *
 * An item with English and no Bengali saves without complaint. That is what writing looks
 * like, and a form that refused it would fight the person using it — they would keep the
 * missing half in their head, or in a notebook, which is worse than keeping it in a draft.
 * The bilingual rule is criterion 4 and it is checked at the publish transition, by the API
 * and by a database trigger.
 *
 * So the gap is shown as **guidance**: a list, before the save button, naming which items are
 * not ready for the floor yet. It never blocks the save and never disables it. What does
 * block a save is only what the server itself refuses — a blank item code, a code used
 * twice, a row with no text in either language — mirrored so the author is told by the form
 * rather than by a round trip.
 *
 * # Reordering is buttons, and drag is not implemented
 *
 * The order is the order a counsellor works through, so it is content rather than
 * decoration. Up and down are ordinary buttons with names that say which item they move:
 * they work from a keyboard, they work with a screen reader, they work with one thumb on a
 * tablet, and they work for somebody with a tremor. A pointer drag has none of those
 * properties, and a drag surface with a keyboard fallback bolted on is two implementations
 * of one behaviour that drift. The plan called drag "nice"; it is not, at the cost of the
 * people who cannot use it.
 *
 * # Why the item code is editable, and warned about
 *
 * It is stable across versions on purpose: it is what a tick references, so rewording an item
 * keeps its history and renaming its code does not. An author has to be able to set one on a
 * new item, so the field is here, suggested from the English text and editable. The hint says
 * what changing it costs, because the cost is entirely invisible — the screen looks identical
 * afterwards and six months of counselling records quietly stop matching.
 */
export interface DraftEditorProps {
  templateId: string;
  version: CounselingVersion;
  rooms: readonly CounselingRoom[];
  /** Called with the saved version. The workspace re-reads what it will publish. */
  onSaved?: (version: CounselingVersion) => void;
  /**
   * Whether the screen holds changes the server has not got.
   *
   * Lifted out because the publish card needs it: publishing puts the **saved** version on
   * the floor, not what is on this screen, and a physician who edited and then published
   * without saving would put the previous text on every phone while reading the new text.
   */
  onDirtyChange?: (dirty: boolean) => void;
}

export function DraftEditor({
  templateId,
  version,
  rooms,
  onSaved,
  onDirtyChange,
}: DraftEditorProps) {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;

  const ordered = useMemo(() => [...rooms].sort((a, b) => a.ordering - b.ordering), [rooms]);
  const firstRoom = ordered[0]?.room ?? '';

  const [rows, setRows] = useState<ItemDraftRow[]>(() => rowsFrom(version));
  const [notes, setNotes] = useState(version.notes ?? '');
  const [dirty, setDirty] = useState(false);
  const [blockers, setBlockers] = useState<SaveBlocker[]>([]);
  const [refusal, setRefusal] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);

  // Announced rather than only moved. A reorder changes nothing visible to somebody using a
  // screen reader — the button they pressed is still under their finger and the list around
  // it is silent — so the new position is spoken.
  const [announcement, setAnnouncement] = useState('');

  const report = useRef(onDirtyChange);
  report.current = onDirtyChange;
  useEffect(() => {
    report.current?.(dirty);
  }, [dirty]);

  function edit(next: ItemDraftRow[]) {
    setRows(next);
    setDirty(true);
    setBlockers([]);
    setRefusal(null);
  }

  function setRow(index: number, patch: Partial<ItemDraftRow>) {
    edit(rows.map((row, position) => (position === index ? { ...row, ...patch } : row)));
  }

  const save = useMutation({
    mutationFn: () =>
      saveCounselingDraft(templateId, version.version, { notes, items: draftFrom(rows) }),
    onSuccess: (saved) => {
      setDirty(false);
      setRefusal(null);
      setConflict(false);
      onSaved?.(saved);
    },
    onError: (error: unknown) => {
      if (error instanceof ApiError) {
        // The version was published by somebody else while this screen was open. Not an
        // error to retry: the edits on screen belong in a new version, and saying so is the
        // whole of the frozen model.
        if (error.status === 409) {
          setConflict(true);
          setRefusal(null);
          return;
        }
        const named = fieldMessages(error, locale);
        const message = Object.values(named)[0];
        if (message !== undefined) {
          setRefusal(message);
          return;
        }
        setRefusal(locale === 'bn' ? error.messageBN : error.messageEN);
        return;
      }
      setRefusal(t('editor.saveFailed'));
    },
  });

  function submitSave() {
    const found = saveBlockers(rows, ordered);
    if (found.length > 0) {
      setBlockers(found);
      setRefusal(null);
      return;
    }
    setBlockers([]);
    save.mutate();
  }

  function move(index: number, delta: number) {
    const next = moveRow(rows, index, delta);
    edit(next);
    const row = rows[index];
    if (row) {
      setAnnouncement(
        t('editor.moved', {
          item: row.textEN.trim() || row.textBN.trim() || row.itemCode || String(index + 1),
          position: index + delta + 1,
          total: rows.length,
        }),
      );
    }
  }

  function addRow() {
    // In the room the last item is in. An author writing the nutrition room's items should
    // not have to reselect the room on every one of them.
    const room = rows[rows.length - 1]?.room ?? firstRoom;
    edit([...rows, emptyItemRow(room)]);
  }

  const notReady = publishBlockers(draftFrom(rows));
  const codes = rows.map((row) => row.itemCode);

  return (
    <section
      className="app-counseling-editor"
      aria-label={t('editor.title', { version: version.version })}
      data-testid="draft-editor"
      data-editable="true"
    >
      <h3 className="app-counseling-version__title">
        {t('editor.title', { version: version.version })}
      </h3>

      {conflict && (
        <AlertBanner tone="critical" title={t('editor.conflict')}>
          {t('editor.conflictBody')}
        </AlertBanner>
      )}
      {refusal && <AlertBanner tone="critical" title={refusal} />}

      {blockers.length > 0 && (
        <AlertBanner tone="critical" title={t('editor.cannotSave')}>
          <ul data-testid="save-blockers">
            {blockers.map((blocker) => (
              <li key={`${blocker.kind}-${blocker.index}`}>
                {t(`editor.blocker.${blocker.kind}`, {
                  position: blocker.index + 1,
                  code: 'itemCode' in blocker ? blocker.itemCode : '',
                  room: 'room' in blocker ? blocker.room : '',
                })}
              </li>
            ))}
          </ul>
        </AlertBanner>
      )}

      <Input
        label={t('editor.notes')}
        description={t('editor.notesHint')}
        value={notes}
        data-testid="draft-notes"
        onChange={(event) => {
          setNotes(event.target.value);
          setDirty(true);
        }}
      />

      <ol className="app-counseling-editor__rows">
        {rows.map((row, index) => (
          <li key={row.key} data-testid={`item-row-${index}`}>
            <ItemEditor
              row={row}
              index={index}
              total={rows.length}
              rooms={ordered}
              locale={locale}
              otherCodes={codes.filter((_, position) => position !== index)}
              onChange={(patch) => setRow(index, patch)}
              onMove={(delta) => move(index, delta)}
              onRemove={() => edit(removeRow(rows, index))}
            />
          </li>
        ))}
      </ol>

      {/* Politely, not assertively: a reorder is not an emergency and a list that interrupted
          on every press would be a list nobody could use. */}
      <p
        className="dthc-visually-hidden"
        role="status"
        aria-live="polite"
        data-testid="reorder-announcement"
      >
        {announcement}
      </p>

      <div className="app-counseling-editor__add">
        <Button variant="secondary" onClick={addRow} data-testid="add-item">
          {t('editor.addItem')}
        </Button>
      </div>

      {/* Guidance, before the save button and never in its way. Naming the item is the point:
          the server's own refusal, when it comes, names the field `items`, which on a
          nine-row screen tells an author nothing they can act on. */}
      {notReady.length > 0 && (
        <AlertBanner tone="borderline" title={t('editor.notReady')}>
          <ul data-testid="publish-guidance">
            {notReady.map((blocker) => (
              <li
                key={
                  blocker.kind === 'no_items'
                    ? 'no_items'
                    : `${blocker.itemCode}-${blocker.missing}`
                }
              >
                {blocker.kind === 'no_items'
                  ? t('publish.blocker.no_items')
                  : t(`publish.blocker.${blocker.missing}`, {
                      position: blocker.ordering,
                      code: blocker.itemCode,
                    })}
              </li>
            ))}
          </ul>
        </AlertBanner>
      )}

      <div className="app-actions">
        <Button
          variant="primary"
          loading={save.isPending}
          data-testid="save-draft"
          onClick={submitSave}
        >
          {t('editor.save')}
        </Button>
        <span className="app-counseling-editor__saved" data-testid="draft-dirty" data-dirty={dirty}>
          {dirty ? t('editor.unsaved') : t('editor.saved')}
        </span>
      </div>
    </section>
  );
}

/** One item's fields, its position controls, and the button that takes it out. */
function ItemEditor({
  row,
  index,
  total,
  rooms,
  locale,
  otherCodes,
  onChange,
  onMove,
  onRemove,
}: {
  row: ItemDraftRow;
  index: number;
  total: number;
  rooms: readonly CounselingRoom[];
  locale: Locale;
  otherCodes: readonly string[];
  onChange: (patch: Partial<ItemDraftRow>) => void;
  onMove: (delta: number) => void;
  onRemove: () => void;
}) {
  const t = useTranslations('counseling');
  const label = row.textEN.trim() || row.textBN.trim() || row.itemCode || t('editor.newItem');

  // The field hints are advice about how to write an item, and they are the same advice on
  // every row. Repeated under all four fields of a nine-item checklist they are 36 lines of
  // small print that push the actual text off the screen — the author stops reading them by
  // item two and loses the content they are there to help write. So they are shown against
  // the first item, where an author meets them before writing anything, and the rows below
  // carry only their labels.
  const hint = (text: string) => (index === 0 ? text : undefined);

  function changeEnglish(value: string) {
    // The code follows the English text only while it is still a suggestion. Once an author
    // has typed one — or the row came in from the version this draft was copied from — it
    // stays put, because it is what every past tick references.
    const patch: Partial<ItemDraftRow> = { textEN: value };
    if (row.codeSuggested) patch.itemCode = suggestItemCode(value, otherCodes);
    onChange(patch);
  }

  return (
    <Card elevation="raised" className="app-counseling-editor-item">
      <div className="app-counseling-editor-item__head">
        <span className="app-counseling-editor-item__position">
          {t('editor.position', { position: index + 1, total })}
        </span>
        <div className="app-counseling-editor-item__moves">
          {/* Named after the item they move, not "up" and "down". A screen reader reading
              nine identical "Move up" buttons has given the operator nothing. */}
          <Button
            variant="quiet"
            size="sm"
            iconStart="arrow-up"
            disabled={index === 0}
            data-testid={`move-up-${index}`}
            aria-label={t('editor.moveUp', { item: label })}
            onClick={() => onMove(-1)}
          >
            {t('editor.up')}
          </Button>
          <Button
            variant="quiet"
            size="sm"
            iconStart="arrow-down"
            disabled={index === total - 1}
            data-testid={`move-down-${index}`}
            aria-label={t('editor.moveDown', { item: label })}
            onClick={() => onMove(1)}
          >
            {t('editor.down')}
          </Button>
          <Button
            variant="quiet"
            size="sm"
            iconStart="x"
            data-testid={`remove-${index}`}
            aria-label={t('editor.remove', { item: label })}
            onClick={onRemove}
          >
            {t('editor.removeShort')}
          </Button>
        </div>
      </div>

      {/* Side by side, so the two languages are compared rather than remembered. */}
      <div className="app-counseling-editor-item__pair">
        <Input
          label={t('editor.textEN')}
          lang="en"
          value={row.textEN}
          data-testid={`text-en-${index}`}
          onChange={(event) => changeEnglish(event.target.value)}
        />
        <Input
          label={t('editor.textBN')}
          lang="bn"
          value={row.textBN}
          data-testid={`text-bn-${index}`}
          onChange={(event) => onChange({ textBN: event.target.value })}
        />
      </div>

      <div className="app-counseling-editor-item__pair">
        <Input
          label={t('editor.guidanceEN')}
          description={hint(t('editor.guidanceHint'))}
          lang="en"
          value={row.guidanceEN}
          data-testid={`guidance-en-${index}`}
          onChange={(event) => onChange({ guidanceEN: event.target.value })}
        />
        <Input
          label={t('editor.guidanceBN')}
          description={hint(t('editor.guidanceHint'))}
          lang="bn"
          value={row.guidanceBN}
          data-testid={`guidance-bn-${index}`}
          onChange={(event) => onChange({ guidanceBN: event.target.value })}
        />
      </div>

      <div className="app-counseling-editor-item__pair">
        <Select
          label={t('editor.room')}
          description={hint(t('editor.roomHint'))}
          value={row.room}
          data-testid={`room-${index}`}
          options={rooms.map((room) => ({
            value: room.room,
            label: roomName(room, room.room, locale),
          }))}
          onChange={(event) => onChange({ room: event.target.value })}
        />
        <Input
          label={t('editor.itemCode')}
          description={hint(t('editor.itemCodeHint'))}
          spellCheck={false}
          value={row.itemCode}
          data-testid={`item-code-${index}`}
          onChange={(event) =>
            onChange({ itemCode: event.target.value.toUpperCase(), codeSuggested: false })
          }
        />
      </div>

      {/* A checkbox rather than a select with a default. Mandatory is what §5.5's gate reads,
          and a pre-chosen answer there is a clinical decision nobody made. */}
      <label className="app-counseling-editor-item__mandatory">
        <input
          type="checkbox"
          checked={row.mandatory}
          data-testid={`mandatory-${index}`}
          onChange={(event) => onChange({ mandatory: event.target.checked })}
        />
        <span>
          {t('editor.mandatory')}
          {index === 0 && (
            <span className="app-counseling-editor-item__mandatory-hint">
              {t('editor.mandatoryHint')}
            </span>
          )}
        </span>
      </label>
    </Card>
  );
}
