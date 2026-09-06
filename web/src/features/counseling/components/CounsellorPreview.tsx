'use client';

import { useTranslations } from 'next-intl';
import { useState } from 'react';

import { Button, Card } from '@dthcms/ui';

import type { Locale } from '@/lib/i18n/config';

import { groupByRoom, type CounselingRoom, type CounselingVersion } from '../api/counseling';

import { itemGuidance, itemText, roomName } from './counselingText';

/**
 * What a counsellor will see on their phone (CP55, §5.2).
 *
 * # Why the preview has its own language switch
 *
 * The author is usually a physician working in English, and the half of this clinic that
 * counsels in Bangla is exactly the half they cannot check by reading their own screen. A
 * preview that followed the interface language would show a physician the English checklist,
 * every time, and the Bengali would go to the floor unread by anybody who reads Bengali.
 *
 * So the language of the preview is a control, and it is set independently of the interface.
 * It is the cheapest way to make criterion 4 something an author can actually verify rather
 * than something the server refuses them for later.
 *
 * # Why a missing translation is drawn as a gap
 *
 * `itemText` answers `null` rather than falling back to the other language — see the note in
 * `counselingText.ts`. A preview that quietly substituted the English would show a checklist
 * that reads completely and is not, which is the single thing this screen exists to prevent.
 *
 * # The tick boxes are inert and look it
 *
 * They are drawn because the shape of the screen is part of what is being previewed — a
 * counsellor sees a list to work down, not a page to read — and they are `disabled`, which
 * on a preview is the honest state: there is no session here and nothing to record against.
 */
export interface CounsellorPreviewProps {
  version: CounselingVersion;
  rooms: readonly CounselingRoom[];
  /** The language to preview in first. The interface language is a reasonable start. */
  initialLanguage?: Locale;
}

export function CounsellorPreview({
  version,
  rooms,
  initialLanguage = 'en',
}: CounsellorPreviewProps) {
  const t = useTranslations('counseling');
  const [language, setLanguage] = useState<Locale>(initialLanguage);

  const groups = groupByRoom(version.items ?? [], rooms);

  return (
    <section
      className="app-counseling-preview"
      aria-label={t('preview.title')}
      data-testid="counsellor-preview"
      data-language={language}
    >
      <div className="app-counseling-preview__head">
        <h3 className="app-counseling-preview__title">{t('preview.title')}</h3>
        <p className="app-page__description">{t('preview.body')}</p>
        <div
          className="app-counseling-preview__languages"
          role="group"
          aria-label={t('preview.language')}
        >
          <Button
            variant={language === 'en' ? 'primary' : 'quiet'}
            size="sm"
            aria-pressed={language === 'en'}
            data-testid="preview-in-en"
            onClick={() => setLanguage('en')}
          >
            {t('preview.inEnglish')}
          </Button>
          <Button
            variant={language === 'bn' ? 'primary' : 'quiet'}
            size="sm"
            aria-pressed={language === 'bn'}
            data-testid="preview-in-bn"
            onClick={() => setLanguage('bn')}
          >
            {t('preview.inBangla')}
          </Button>
        </div>
      </div>

      <Card elevation="raised" className="app-counseling-phone">
        {groups.length === 0 ? (
          <p className="app-counseling-phone__empty">{t('preview.empty')}</p>
        ) : (
          <ol className="app-counseling-phone__rooms">
            {groups.map((group, step) => (
              <li key={group.code} className="app-counseling-phone__room" data-room={group.code}>
                {/* The step number is the flow: one room's items, then walk the patient to
                    the next. A counsellor reads it as a place, not as a heading. */}
                <p className="app-counseling-phone__room-title">
                  {t('preview.step', {
                    step: step + 1,
                    room: roomName(group.room, group.code, language),
                  })}
                </p>
                <ul className="app-counseling-phone__items">
                  {group.items.map((item) => {
                    const text = itemText(item, language);
                    const guidance = itemGuidance(item, language);
                    return (
                      <li
                        key={item.item_code}
                        className="app-counseling-phone__item"
                        data-item={item.item_code}
                        data-untranslated={text === null || undefined}
                      >
                        <label>
                          <input type="checkbox" disabled aria-hidden="true" tabIndex={-1} />
                          <span lang={language}>
                            {text ?? (
                              <span
                                className="app-counseling-phone__missing"
                                data-testid={`preview-missing-${item.item_code}`}
                              >
                                {language === 'bn' ? t('item.missingBN') : t('item.missingEN')}
                              </span>
                            )}
                          </span>
                        </label>
                        {item.mandatory && (
                          <span className="app-counseling-phone__mandatory">
                            {t('item.mandatory')}
                          </span>
                        )}
                        {guidance && (
                          <p className="app-counseling-phone__guidance" lang={language}>
                            {guidance}
                          </p>
                        )}
                      </li>
                    );
                  })}
                </ul>
              </li>
            ))}
          </ol>
        )}
      </Card>
    </section>
  );
}
