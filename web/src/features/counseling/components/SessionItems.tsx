'use client';

import { useLocale, useTranslations } from 'next-intl';

import { formatCount, formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { isKnownRole } from '@/lib/permissions';

import type { CounselingItem } from '../api/counseling';
import {
  elapsedParts,
  itemProgress,
  type CounselingSession,
  type GateState,
  type ItemProgress,
} from '../api/gate';

import { itemLabel, staffLabel, type StaffLabel } from './panelText';

/**
 * One session's items, each with who covered it, when, and how long since the item before
 * (CP57 criterion 4, §5.4).
 *
 * # Attribution is per item, and that is the whole component
 *
 * A panel that said "counselled by Rina" would answer the wrong question. One session walks
 * three rooms — the counselling room, the nutrition room, the insulin corner — and the
 * nutritionist holds `counseling.tick` precisely because the nutrition items are hers. Two
 * people, sometimes three, cover one checklist between them. §5.4's method is the physician
 * asking the patient *what were you told about injection sites* and then looking at who told
 * them, and the answer to that is on the row for that item or it does not exist.
 *
 * So every covered row carries its own actor and its own timestamp, and the session-level
 * facts (who opened it, when it closed) are drawn as what they are: facts about the session,
 * not about any item.
 *
 * # Why times are shown, and what the sentence beside them is for
 *
 * The duration is the gap between one tick and the next — from the session's start for the
 * first — because that is the only thing recorded. It is **not attention**. A counsellor who
 * teaches for eleven minutes and then ticks four items in a row produces three one-second
 * items and one eleven-minute item, and a physician reading that as "she spent one second on
 * insulin technique" would be wrong about a colleague on the strength of an interface's
 * arithmetic. The caveat is therefore on the screen, in words, above the column — not in a
 * tooltip, not only in this comment. What the number is genuinely good for is the other
 * shape: a whole session of one-second gaps is a checklist that was ticked through, and that
 * is exactly what the spot-questioning exists to catch.
 *
 * # Why an uncovered item has to know what the checkpoint is doing
 *
 * An item that nobody covered is uncovered whatever the gate says, and this component marks it
 * exactly as strongly either way — the whole point of the override record is that what was
 * skipped stays visible. What changes is the *consequence*. "The checkpoint will hold this
 * patient until it is covered" is true while the gate is holding them and false the moment
 * somebody sends them past, and a panel whose banner says the patient was let through while
 * every row underneath says they are being held is a panel a physician stops reading.
 *
 * So the state travels in as a prop rather than being fetched here: this component's contract
 * is "give me a session, I will render it", and a leaf that issued a network request of its own
 * would surprise every caller of it. It is **optional**, and when it is absent the row says
 * only that the item is outstanding and claims no consequence at all — which is the honest
 * answer when nobody has told it what the gate is doing.
 *
 * # A withdrawn tick is history, never an absence
 *
 * `undone_at` does not remove a row and does not remove a line from this screen. The item
 * reads as not covered — because it is not — *and* says that somebody ticked it at 11:02 and
 * took it back at 11:04, with the reason they gave. `undo_count` is shown even on an item
 * that is covered now, because it is remembered across re-ticks: "ticked and taken back three
 * times" is precisely what a quality review is looking for, and it is invisible in the
 * current state.
 *
 * # Nothing here is a control
 *
 * There is no checkbox, no button, no input in this component and there must never be one. A
 * physician who could tick a counsellor's item from this panel would be able to close the
 * gate holding their own patient, in a colleague's name, without a word said to anybody.
 * `counseling-panel.test.tsx` has a named test whose entire job is to fail if a tick control
 * ever appears on this surface.
 */

export interface SessionItemsProps {
  session: CounselingSession;
  /**
   * What the checkpoint is doing about this visit, where the caller knows.
   *
   * Decides only what an outstanding item says will *happen* — never how strongly it is
   * marked. Absent means "not told", and the rows then claim no consequence rather than
   * guessing one.
   */
  gate?: GateState;
}

export function SessionItems({ session, gate }: SessionItemsProps) {
  const t = useTranslations('counseling');
  const locale = useLocale() as Locale;

  const rows = itemProgress(session);

  if (rows.length === 0) {
    // The detail response carries the frozen list, so an empty one means the version has no
    // items — which cannot be published and therefore cannot be walked. Said rather than
    // drawn as a finished checklist with nothing on it.
    return (
      <p className="app-counseling-panel__empty" data-testid="session-no-items">
        {t('panel.sessions.noItems')}
      </p>
    );
  }

  return (
    <>
      <p className="app-counseling-panel__caveat" data-testid="time-caveat">
        {t('panel.time.caveat')}
      </p>

      <ol className="app-counseling-panel__items">
        {rows.map((row) => (
          <ItemRow key={row.item.item_code} row={row} locale={locale} gate={gate} />
        ))}
      </ol>
    </>
  );
}

/** What this room is called, from the item itself — the server sends both languages on it. */
function roomOf(item: CounselingItem, locale: Locale): string {
  if (locale === 'bn' && item.room_bn) return item.room_bn;
  return item.room_en || item.room_bn || item.room;
}

function ItemRow({
  row,
  locale,
  gate,
}: {
  row: ItemProgress;
  locale: Locale;
  gate: GateState | undefined;
}) {
  const t = useTranslations('counseling');

  const { item, tick } = row;
  const label = itemLabel(item, locale);
  const code = item.item_code;

  /*
   * Who covered this item, and who took the tick back.
   *
   * Two people, deliberately kept apart. The counsellor who ticked and the physician who
   * un-ticked are different acts by different people, and a row that named only one of them
   * would hide the more interesting half.
   */
  const coveredBy = tick
    ? staffLabel(
        {
          code: tick.ticked_by_code,
          name_en: tick.ticked_by_name_en,
          name_bn: tick.ticked_by_name_bn,
          role: tick.ticked_role,
          id: tick.ticked_by,
        },
        locale,
      )
    : null;

  const takenBackBy = tick
    ? staffLabel(
        {
          code: tick.undone_by_code,
          name_en: tick.undone_by_name_en,
          name_bn: tick.undone_by_name_bn,
          id: tick.undone_by,
        },
        locale,
      )
    : null;

  const undoneCount = tick?.undo_count ?? 0;

  return (
    <li
      className="app-counseling-panel__item"
      data-testid={`panel-item-${code}`}
      data-item={code}
      data-covered={row.covered}
      data-withdrawn={row.withdrawn}
      data-outstanding={row.outstanding}
      data-mandatory={item.mandatory}
    >
      <p className="app-counseling-panel__item-head">
        <span className="app-counseling-item__code">{code}</span>
        <span className="app-counseling-item__mandatory">
          {item.mandatory ? t('item.mandatory') : t('item.optional')}
        </span>
        <span className="app-counseling-panel__room">{roomOf(item, locale)}</span>
      </p>

      {label === null ? (
        // Both languages empty. The code above is then the only thing naming this item, and
        // it stays on screen rather than the row disappearing: an item that vanished is an
        // item nobody covers and nobody misses.
        <p className="app-counseling-item__missing" data-testid={`panel-untitled-${code}`}>
          {t('panel.item.noText')}
        </p>
      ) : (
        <p className="app-counseling-item__text" lang={label.lang}>
          {label.text}
          {!label.translated && (
            // The reader's own language is missing for this item. Said rather than passed
            // off as theirs — the physician is about to ask the patient about this item, and
            // in which language it was written is part of the answer.
            <span className="app-counseling-item__missing" data-testid={`panel-fallback-${code}`}>
              {label.lang === 'en' ? t('item.missingBN') : t('item.missingEN')}
            </span>
          )}
        </p>
      )}

      <p className="app-counseling-panel__state" data-testid={`panel-state-${code}`}>
        {row.covered ? t('panel.item.covered') : t('panel.item.notCovered')}
      </p>

      {row.covered && tick && (
        <div className="app-counseling-panel__attribution">
          <p data-testid={`panel-covered-${code}`}>
            {t('panel.item.coveredAt', {
              when: formatDateTime(Date.parse(tick.ticked_at), locale),
            })}
          </p>
          <p data-testid={`panel-by-${code}`}>
            {/* The person, then the role. This line is §5.4's answer: the physician has just
                asked the patient what they were told about injection sites, and "the clinical
                counselor" does not tell two counsellors apart. The role stays beside the name
                because it is the other half — it says which room this was covered in. */}
            <Named who={coveredBy} />
          </p>
          <StaffIdentifier who={coveredBy} />
          <Elapsed row={row} locale={locale} />
        </div>
      )}

      {tick?.note && tick.note.trim() !== '' && (
        // §5.3's per-item note: what this counsellor wanted the physician to know about this
        // item for this patient. It is the reason the note field exists, so it is shown
        // beside the item rather than gathered into a session-level block.
        <p className="app-counseling-panel__note" data-testid={`panel-note-${code}`}>
          {t('panel.item.note', { note: tick.note })}
        </p>
      )}

      {row.withdrawn && tick && (
        <div className="app-counseling-panel__withdrawn" data-testid={`panel-withdrawn-${code}`}>
          <p>
            {t('panel.item.tickedThenTakenBack', {
              ticked: formatDateTime(Date.parse(tick.ticked_at), locale),
              taken: tick.undone_at
                ? formatDateTime(Date.parse(tick.undone_at), locale)
                : t('panel.item.unknownTime'),
            })}
          </p>
          <p data-testid={`panel-taken-back-by-${code}`}>
            {/* Who un-ticked matters as much as who ticked. A counsellor correcting their own
                mis-tap and a physician striking out a colleague's item are the same row in
                the ledger and very different events on the floor. */}
            {takenBackBy && takenBackBy.name !== null
              ? t('panel.item.takenBackBy', { name: takenBackBy.name })
              : t('panel.item.takenBackByUnknown')}
          </p>
          <p data-testid={`panel-withdrawn-reason-${code}`}>
            {/* Required by the handler, by the event's validation and by a database
                constraint — an un-tick with no reason is indistinguishable from a mis-tap.
                So a blank here is a fact about this record, not a field to leave empty. */}
            {tick.undone_reason && tick.undone_reason.trim() !== ''
              ? t('panel.item.withdrawnReason', { reason: tick.undone_reason })
              : t('panel.item.withdrawnNoReason')}
          </p>
        </div>
      )}

      {undoneCount > 0 && (
        // Remembered across re-ticks, so this appears on covered items too. The current
        // state cannot show it and it is the single most useful number on the row to
        // whoever reviews how a session went.
        <p className="app-counseling-panel__undo" data-testid={`panel-undo-${code}`}>
          {t('panel.item.undoCount', { count: undoneCount })}
        </p>
      )}

      {row.outstanding && (
        <p className="app-counseling-panel__outstanding" data-testid={`panel-outstanding-${code}`}>
          {/* The fact, always. It does not soften when somebody sends the patient past — the
              item is uncovered either way, and this is the line that keeps it visible. */}
          {t('panel.item.outstanding')}{' '}
          {/* The consequence, only where it is true. Nothing is said for a gate that is clear
              — which can only mean the gate and the session were read a moment apart — or for
              a caller who did not say what the gate is doing. */}
          {gate === 'blocked' && t('panel.item.outstandingHeld')}
          {gate === 'overridden' && t('panel.item.outstandingOverridden')}
        </p>
      )}

      {!row.covered && !row.outstanding && !row.withdrawn && (
        <p className="app-counseling-panel__optional" data-testid={`panel-optional-${code}`}>
          {t('panel.item.notCoveredOptional')}
        </p>
      )}
    </li>
  );
}

/**
 * A person, named, with the role they were wearing beside them.
 *
 * Four shapes rather than one, because each says something different and none of them may be
 * a blank: name and role, name alone, role alone when the staff record has gone, and a
 * sentence saying nobody could be identified. The names are joined from the staff record —
 * which is right, so somebody who changes their name reads correctly on last year's work —
 * and that join can come back empty.
 */
function Named({ who }: { who: StaffLabel | null }) {
  const t = useTranslations('counseling');
  const tRole = useTranslations('role');

  const role = who?.role == null ? undefined : isKnownRole(who.role) ? tRole(who.role) : who.role;
  const name = who?.name ?? null;

  if (name !== null && role !== undefined) return <>{t('panel.item.byPerson', { name, role })}</>;
  if (name !== null) return <>{t('panel.item.byName', { name })}</>;
  if (role !== undefined) return <>{t('panel.item.byRole', { role })}</>;
  return <>{t('panel.item.byUnknownRole')}</>;
}

/**
 * The stable identifier for a person, and only one of them.
 *
 * The staff code, because that is what does not move when somebody's name does, and it is
 * what a physician quotes to a colleague. The uuid appears only when there is no code —
 * showing both would put two identifiers for one person on a row that already carries a name.
 */
function StaffIdentifier({ who }: { who: StaffLabel | null }) {
  const t = useTranslations('counseling');
  if (who === null) return null;

  if (who.code !== null) {
    return (
      <p className="app-counseling-panel__staff">
        <span>{t('panel.item.staffCode')}</span> <code>{who.code}</code>
      </p>
    );
  }
  if (who.id !== null) {
    return (
      <p className="app-counseling-panel__staff">
        <span>{t('panel.item.staffId')}</span> <code>{who.id}</code>
      </p>
    );
  }
  return null;
}

/**
 * The gap between this item's tick and the one before it.
 *
 * Two sentences rather than one number: the duration, and what it was measured from. The
 * first item in a session is measured from the session opening and every other from the
 * previous tick, and a column of bare durations with those two meanings mixed together is a
 * column that means nothing.
 */
function Elapsed({ row, locale }: { row: ItemProgress; locale: Locale }) {
  const t = useTranslations('counseling');

  if (row.elapsedMs === null) {
    // No honest number: a tick that claims to precede the session, or a timestamp that would
    // not parse. Both are real — an offline phone carries its own clock — and a negative
    // duration on screen reads as a fault in the record rather than in the clock.
    return (
      <p className="app-counseling-panel__time" data-testid={`panel-time-${row.item.item_code}`}>
        {t('panel.time.unknown')}
      </p>
    );
  }

  const parts = elapsedParts(row.elapsedMs);
  // Formatted here rather than left to the message, so the numeral system is a decision this
  // application made rather than a default: `formatters.ts` puts a count in running text in
  // the reader's own numerals, which is what this is — nobody transcribes a duration onto a
  // paper chart or reads it back down a telephone.
  const minutes = formatCount(parts.minutes, locale);
  const seconds = formatCount(parts.seconds, locale);

  return (
    <p className="app-counseling-panel__time" data-testid={`panel-time-${row.item.item_code}`}>
      <span>
        {parts.minutes > 0
          ? t('panel.time.minutes', { minutes, seconds })
          : t('panel.time.seconds', { seconds })}
      </span>{' '}
      <span className="app-counseling-panel__time-basis">
        {row.fromSessionStart ? t('panel.time.sinceStart') : t('panel.time.sincePrevious')}
      </span>
    </p>
  );
}
