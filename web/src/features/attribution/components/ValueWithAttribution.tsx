'use client';

import { useLocale, useTranslations } from 'next-intl';
import { useId, useState, type KeyboardEvent, type ReactNode } from 'react';

import { Icon, cx } from '@dthcms/ui';

import { formatDateTime } from '@/lib/formatters';
import type { Locale } from '@/lib/i18n/config';
import { isKnownRole } from '@/lib/permissions';

import { useDirectory } from '../api/useDirectory';
import { isKnownSource, type ValueAttribution, type ValueCorrection } from '../lib/attribution';
import type { DirectoryLookup } from '../lib/lookup';

/**
 * A clinical value, with who entered it one interaction away (CP61, §4.2).
 *
 * §4.2 asks for one thing: *any reviewer sees who entered a value **instantly, without
 * digging***. Everything below follows from taking "instantly" and "any reviewer" literally.
 *
 * # Why hover is not enough, and why this is a button
 *
 * A reveal on hover alone answers the question for a physician with a mouse and refuses it
 * to everybody else — a keyboard user, a screen reader, a tablet. The tablet is not
 * hypothetical: this clinic reads screens on cheap Android tablets where there is no hover
 * at all, and a component whose entire job is showing attribution that shows nothing on the
 * floor's own hardware has failed at its only job.
 *
 * So there are three ways in and they are the same interaction count:
 *
 *   - **Hover** the value: CSS reveals the panel.
 *   - **Tab** to the control: CSS reveals it through `:focus-within`, *and* the panel is the
 *     control's `aria-describedby`, so a screen reader speaks the whole attribution on
 *     focus rather than announcing a button whose contents are invisible to it.
 *   - **Tap or click** the control: the panel pins open until Escape or a second tap.
 *
 * There is deliberately **no `aria-expanded`**. Hover and `:focus-within` change what is on
 * screen without React being told, so a boolean that claimed to describe the panel's
 * visibility would be wrong for two of the three ways in — and an ARIA attribute that is
 * wrong is worse than one that is absent. The description relationship is the honest one:
 * it is true whether the panel is drawn or not.
 *
 * # Why the panel is always in the DOM
 *
 * `aria-describedby` pointing at an element React has not rendered yet describes nothing,
 * and the failure is silent — the attribution simply never reaches the person who most needs
 * it read aloud. The panel is therefore always rendered and hidden by CSS.
 *
 * # Why the source is a word and never only a tint (criterion 3)
 *
 * A number lifted off a photograph of a paper chart by the scanner and a number an operator
 * read off a calibrated scale are different evidence, and telling them apart is the whole of
 * criterion 3. The mark beside the value is a **word** — "Scanned", "Station" — and the
 * stylesheet additionally gives a machine-read value a dashed rule rather than a solid one.
 * Two signals, neither of them hue: roughly one man in twelve working in this clinic cannot
 * use colour, a tablet held near a window flattens it for everybody, and a photograph of a
 * screen sent to a consultant has no colour worth relying on either.
 *
 * The word is drawn for **every** source, not only for OCR. A distinction carried by the
 * absence of a mark is a distinction that cannot survive being cropped, and "no mark" and
 * "mark I did not notice" look identical.
 *
 * # Why both authors show on a corrected value (criterion 2)
 *
 * A correction does not replace the person who entered the original — it adds a second
 * person to the record — and a panel that showed only the most recent one would let a
 * reviewer attribute the first author's value to whoever touched it last. So the heading
 * changes to *Originally entered by* and the correction is a separate block with its own
 * name in it.
 *
 * Where the contract does not name the corrector — an observation carries `status:
 * CORRECTED` and sometimes the id of its replacement, and no `corrected_by` at all — this
 * says so in a sentence. An empty line where a name should be reads as a rendering fault;
 * "the record does not name who made this change" is the fact, and it is the fact CP62 is
 * for. Nothing here invents a field.
 *
 * # Why nothing here reads as an accusation
 *
 * A staff name is not PHI, but it is a person, and this panel will be read over somebody's
 * shoulder on a bad morning. Every sentence in it says **who**, never who is at fault:
 * "Entered by", "Corrected by", "No longer at the clinic". There is no word for wrong, no
 * word for responsible, and no control on this surface that does anything to the value.
 *
 * # What it does when the directory is not there
 *
 * The names are a lookup laid over the record; the record is the record. A directory that
 * has not arrived, or failed to, must never blank out a clinical value — so the value is
 * always drawn, the panel falls back to the role, the station and the time that came with
 * the value itself, and it says plainly that the names could not be read.
 */

export type AttributionVariant = 'disclosure' | 'compact';

export interface ValueWithAttributionProps {
  /** The value itself. Whatever the screen would have rendered without this component. */
  children: ReactNode;
  attribution: ValueAttribution;
  /**
   * What this value is — "Height", "Penicillin", "Oxygen saturation".
   *
   * Used to name the control. A list of six values otherwise offers a screen reader six
   * buttons all called "Who entered this value", which is six ways of not answering.
   */
  label?: string;
  /**
   * `compact` puts the person's name and the source on screen without any interaction, for
   * dense views — a station list, the patient header's allergy strip — where a reviewer is
   * scanning rather than asking about one value. The panel is still there for the rest.
   */
  variant?: AttributionVariant;
  testId?: string;
  className?: string;
}

export function ValueWithAttribution({
  children,
  attribution,
  label,
  variant = 'disclosure',
  testId,
  className,
}: ValueWithAttributionProps) {
  const t = useTranslations('attribution');
  const tRole = useTranslations('role');
  const locale = useLocale() as Locale;
  const lookup = useDirectory();

  const panelId = useId();
  const [pinned, setPinned] = useState(false);

  const who = describePerson(attribution.recordedBy, attribution.recordedRole, lookup, t, tRole);
  const corrected = attribution.correction !== undefined;

  /*
   * The source, in the three states `ValueAttribution` distinguishes.
   *
   * `carries` is whether this kind of record has the field at all; `source` is what it says
   * when it does. The middle state — the field exists and is empty — is the one CP61's
   * migration created and the one worth being careful about: every row written before it
   * comes back blank, and a blank drawn as nothing is indistinguishable on screen from a
   * value somebody typed at a station. So it gets a word of its own.
   */
  const carriesSource = attribution.source !== undefined;
  const source = attribution.source ?? null;

  return (
    <span
      className={cx('app-attrib', className)}
      data-testid={testId ?? 'value-attribution'}
      data-variant={variant}
      // Read by the stylesheet to give a machine-read value a rule of a different shape.
      // The word beside the value is the primary signal; this is the second one.
      data-source={!carriesSource ? 'absent' : (source ?? 'unrecorded')}
      data-corrected={corrected}
      data-open={pinned}
      onKeyDown={(event: KeyboardEvent<HTMLSpanElement>) => {
        // Escape closes a pinned panel. A panel that could only be dismissed by finding the
        // same small control again is a panel that covers the next value on the list.
        if (event.key === 'Escape' && pinned) {
          setPinned(false);
          event.stopPropagation();
        }
      }}
    >
      <span className="app-attrib__value">{children}</span>

      {carriesSource && (
        <span className="app-attrib__source" data-testid="attribution-source">
          {/* Three answers and never a blank. A source this build has no word for renders as
              the server's own code, which is a thing somebody can quote when they ask why; a
              record that carries the field empty says so. A blank mark would make a value of
              unknown provenance look like an ordinary station reading, which is the exact
              confusion criterion 3 exists to prevent. */}
          {source === null
            ? t('mark.unrecorded')
            : isKnownSource(source)
              ? t(`mark.${source}`)
              : source}
        </span>
      )}

      {variant === 'compact' && (
        // Criterion 1 costs one interaction; a dense list is read without any. The summary
        // is who, and nothing else — the station, the device and the times stay in the
        // panel, because a strip that carried all of them would be unreadable.
        <span className="app-attrib__summary" data-testid="attribution-summary">
          {who.summary}
        </span>
      )}

      <button
        type="button"
        className="app-attrib__trigger"
        // Not aria-expanded — see the note at the top of this file. The panel is described,
        // which is true however it came to be on screen.
        aria-describedby={panelId}
        aria-label={label === undefined ? t('trigger') : t('triggerNamed', { what: label })}
        data-testid="attribution-trigger"
        onClick={() => setPinned((open) => !open)}
      >
        <Icon name="user" size={16} />
      </button>

      <span
        role="tooltip"
        id={panelId}
        className="app-attrib__panel"
        data-testid="attribution-panel"
      >
        <span className="app-attrib__heading">
          {corrected ? t('headingOriginal') : t('heading')}
        </span>

        <span className="app-attrib__person" data-testid="attribution-person">
          {who.sentence}
        </span>

        {who.aside !== null && (
          // Why there is no name: still loading, looked up and not listed, or the directory
          // itself could not be read. Three different situations, and a reviewer deciding
          // whether to go and ask somebody needs to know which one this is.
          <span className="app-attrib__aside" data-testid="attribution-aside">
            {who.aside}
          </span>
        )}

        {who.presence !== null && (
          // "No longer at the clinic" rather than sending somebody to ask a colleague who
          // has gone. It is a fact about the roster, not about the value or the person.
          <span className="app-attrib__presence" data-testid="attribution-presence">
            {who.presence}
          </span>
        )}

        {who.code !== null && (
          <span className="app-attrib__code" data-testid="attribution-code">
            {t('staffCode')} <code>{who.code}</code>
          </span>
        )}

        <Where attribution={attribution} lookup={lookup} />
        <When attribution={attribution} locale={locale} />

        {carriesSource && (
          <span className="app-attrib__evidence" data-testid="attribution-evidence">
            {source === null
              ? t('source.unrecorded')
              : isKnownSource(source)
                ? t(`source.${source}`)
                : t('source.other', { source })}
          </span>
        )}

        {attribution.correction !== undefined && (
          <Correction correction={attribution.correction} lookup={lookup} locale={locale} />
        )}

        {lookup.state === 'unavailable' && attribution.recordedBy !== undefined && (
          <span className="app-attrib__degraded" data-testid="attribution-degraded">
            {t('directoryUnavailable')}
          </span>
        )}
      </span>
    </span>
  );
}

/** Where it was entered: which station, and which tablet or phone. */
function Where({
  attribution,
  lookup,
}: {
  attribution: ValueAttribution;
  lookup: DirectoryLookup;
}) {
  const t = useTranslations('attribution');

  const { stationCode, deviceId } = attribution;
  const device = lookup.device(deviceId);

  return (
    <>
      {stationCode !== undefined && (
        <span className="app-attrib__station" data-testid="attribution-station">
          {/* The code where the directory has no name for it. `STN_COUNSELING` is what the
              queue, the board and every assignment rule call that room, so it is a worse
              label than the name and a far better one than a blank. */}
          {t('station', { station: lookup.station(stationCode) ?? stationCode })}
        </span>
      )}

      {deviceId !== undefined && (
        // Nothing is said when there is no device id, which is the ordinary case for a value
        // typed on the web: there was no tablet, so there is no tablet to name. That is an
        // honest absence rather than a gap, and a line reading "device not recorded" beside
        // every value a physician enters at a desk would be noise standing in for a fact
        // that is already true.
        <span className="app-attrib__device" data-testid="attribution-device">
          {device === null
            ? t('deviceUnlisted')
            : device.status === 'active'
              ? t('device', { device: device.name })
              : t('deviceRetired', { device: device.name })}
        </span>
      )}
    </>
  );
}

/**
 * When it was true, and when it was written down.
 *
 * Two facts, and they are not the same one: a reading taken at 09:05 and entered at 09:20
 * has both, and a reviewer asking whether a glucose was before or after the insulin is
 * asking about the first. They collapse to one line when they are the same instant, because
 * printing an identical time twice under two different labels invites a reader to look for a
 * difference that is not there.
 */
function When({ attribution, locale }: { attribution: ValueAttribution; locale: Locale }) {
  const t = useTranslations('attribution');

  const effective = instant(attribution.effectiveAt);
  const recorded = instant(attribution.recordedAt);

  if (effective !== null && recorded !== null && effective === recorded) {
    return (
      <span className="app-attrib__when" data-testid="attribution-when">
        {t('recordedAt', { at: formatDateTime(recorded, locale) })}
      </span>
    );
  }

  return (
    <>
      {attribution.effectiveAt !== undefined && (
        <span className="app-attrib__when" data-testid="attribution-effective">
          {effective === null
            ? // A timestamp that will not parse is a fault in the record, and saying so is
              // more use to whoever has to fix it than an empty line that reads as a value
              // nobody stamped.
              t('timeUnreadable')
            : t('measuredAt', { at: formatDateTime(effective, locale) })}
        </span>
      )}

      {attribution.recordedAt !== undefined && (
        <span className="app-attrib__when" data-testid="attribution-when">
          {recorded === null
            ? t('timeUnreadable')
            : t('recordedAt', { at: formatDateTime(recorded, locale) })}
        </span>
      )}
    </>
  );
}

/** A timestamp as milliseconds, or `null` when it will not parse. Never `NaN` downstream. */
function instant(value: string | undefined): number | null {
  if (value === undefined) return null;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : null;
}

/**
 * The second person on the record (criterion 2).
 *
 * Its own block, below the original author rather than instead of them. The kind says what
 * happened in the ledger's own words; the name says who, where there is one to say.
 */
function Correction({
  correction,
  lookup,
  locale,
}: {
  correction: ValueCorrection;
  lookup: DirectoryLookup;
  locale: Locale;
}) {
  const t = useTranslations('attribution');
  const tRole = useTranslations('role');

  const who = describePerson(correction.by, correction.role, lookup, t, tRole);
  const at = instant(correction.at);

  return (
    <span
      className="app-attrib__correction"
      data-kind={correction.kind}
      data-testid="attribution-correction"
    >
      <span className="app-attrib__correction-kind">{t(`correction.${correction.kind}`)}</span>

      <span className="app-attrib__correction-by" data-testid="attribution-corrector">
        {correction.by === undefined && correction.role === undefined
          ? // The observation ledger's shape today: the status says a value was corrected and
            // no field says by whom. Said out loud rather than left as a gap, because a gap
            // reads as a component that failed to render a name it had.
            t('correction.byUnrecorded')
          : t('correction.by', { who: who.sentence })}
      </span>

      {at !== null && (
        <span className="app-attrib__correction-at">
          {t('correction.at', { at: formatDateTime(at, locale) })}
        </span>
      )}

      {correction.replacedBy !== undefined && (
        // The id of the replacement is not shown. It is a uuid, it names a value rather than
        // a person, and nothing on this screen can navigate to it yet — CP62 builds that
        // chain. What a reviewer can act on now is that a later value exists.
        <span className="app-attrib__correction-replaced">{t('correction.replaced')}</span>
      )}
    </span>
  );
}

/** A translator bound to the `attribution` namespace, as this file uses it. */
type Translate = (key: string, values?: Record<string, string>) => string;

interface PersonDescription {
  /** The full sentence for the panel: name and role, name, role, or an honest absence. */
  sentence: string;
  /** The short form for a dense row. Never longer than a name. */
  summary: string;
  /** Why there is no name, when there is no name. `null` when nothing needs explaining. */
  aside: string | null;
  /** Whether this person is still at the clinic, when the answer is no. */
  presence: string | null;
  /** The staff code, which is the identifier that does not move when a name does. */
  code: string | null;
}

/**
 * A person, as this component names them.
 *
 * # Why the name leads and the role stays beside it
 *
 * The role tells a counsellor from a nutritionist and does not tell two counsellors apart.
 * §4.2 is a question about a person. So the name leads where there is one; the role stays
 * because it is the other half of the answer — it says which hat this person was wearing at
 * the time, which is a property of the value rather than of them — and it is the only half
 * that survives when the staff record cannot be read at all.
 *
 * # Why no uuid ever reaches the screen
 *
 * An unresolved id renders as the role plus a sentence saying the record could not be read.
 * It does not render as the uuid. A uuid answers a different question from the one being
 * asked, it is unreadable aloud, and a reviewer who copies one down has copied down
 * something no colleague on the floor can use. The consequence is accepted deliberately: on
 * a value whose author is not in the directory there is nothing on screen to quote, and the
 * honest reading of that is that the directory is the thing to fix.
 */
function describePerson(
  id: string | undefined,
  role: string | undefined,
  lookup: DirectoryLookup,
  t: Translate,
  tRole: Translate,
): PersonDescription {
  const person = id === undefined ? null : lookup.person(id);
  const roleText = roleLabel(role, tRole);

  const name = person?.name ?? null;
  const code = person?.code ?? null;

  const presence =
    person === null || person.status === 'active'
      ? null
      : person.status === 'deactivated' || person.status === 'suspended'
        ? t(`presence.${person.status}`)
        : // A status this build has not heard of renders as itself. Rounding an unknown one
          // up to "still here" would send somebody looking for a colleague who has left.
          t('presence.other', { status: person.status });

  if (name !== null) {
    return {
      // A name with no role is the name and nothing else. There is no message for it on
      // purpose: a message whose whole body is `{name}` is a translation of nothing, and
      // the i18n contract would have to carry an exemption saying so.
      sentence: roleText === null ? name : t('byPerson', { name, role: roleText }),
      summary: name,
      aside: null,
      presence,
      code,
    };
  }

  // No name. Why not is three different situations and they are not interchangeable: still
  // loading is temporary, not listed is a fact about the staff record, and a directory that
  // could not be read says nothing at all about this person.
  const aside =
    id === undefined
      ? null
      : lookup.state === 'loading'
        ? t('resolving')
        : lookup.state === 'unavailable'
          ? t('namesUnavailable')
          : t('notListed');

  if (roleText !== null) {
    // The role alone, for the same reason. The sentence saying *why* there is no name is
    // the `aside` below it, which does have words in it.
    return { sentence: roleText, summary: roleText, aside, presence, code };
  }

  return {
    // Nobody named and no role either. Distinct from "somebody entered this and we could not
    // name them": this is a record that never carried an author.
    sentence: id === undefined ? t('byNobody') : t('byUnknown'),
    summary: t('summaryUnknown'),
    aside,
    presence,
    code,
  };
}

/**
 * The role, in the reader's language.
 *
 * A role code this build does not know renders as the code. The server's catalogue can grow
 * without this application shipping, and a role that vanished from the panel because nobody
 * had added a translation would silently remove the one piece of attribution that survives
 * when a name cannot be resolved.
 */
function roleLabel(role: string | undefined, tRole: Translate): string | null {
  const code = (role ?? '').trim();
  if (code === '') return null;
  return isKnownRole(code) ? tRole(code) : code;
}
