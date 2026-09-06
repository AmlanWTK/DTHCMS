'use client';

import { useTranslations } from 'next-intl';

import type { Locale } from '@/lib/i18n/config';

import type { RoomGroup } from '../api/counseling';

import { roomName } from './counselingText';

/**
 * One room's items, as an author reads them back (CP55, §5.2).
 *
 * # Both languages, always, on this screen
 *
 * The counsellor's phone shows one language; this screen shows two, side by side. That is
 * the difference between the preview and the review. An author checking a version needs to
 * see that item four has English and no Bengali — and an interface that showed them only
 * their own interface language would let a physician working in English read a checklist as
 * finished, publish it, and be refused by the server for a gap that was on the screen the
 * whole time and simply not drawn.
 *
 * So a missing half is a **sentence in the place the text would have been**, not a footnote
 * and not a tint. Roughly one man in twelve cannot use hue, and this is read on a tablet in
 * a room with a window.
 *
 * Which of the two comes first follows the interface language. Both are always drawn, so the
 * check the screen exists for is unaffected — but a physician working in Bangla reading every
 * item in English first, with their own language underneath, is being told which half of this
 * clinic's work is the real one.
 *
 * # Why the item code is on screen
 *
 * It is what a tick references, so it survives a rewording and it is what an assignment rule
 * and a counselling record name. An author comparing two versions is comparing codes, and one
 * that never appeared on screen would be invisible right up to the moment somebody changed it.
 */
export function ItemLines({ group, locale }: { group: RoomGroup; locale: Locale }) {
  const t = useTranslations('counseling');
  const heading = roomName(group.room, group.code, locale);

  return (
    <>
      <h4 className="app-counseling-room__title" data-testid={`room-heading-${group.code}`}>
        {heading}
      </h4>
      {/* A room the vocabulary does not list. The items are still drawn — an item that
          vanished from the screen is an item nobody covers and nobody misses — and the fact
          that the room is unknown is said rather than left to look like a naming quirk. */}
      {!group.room && (
        <p className="app-counseling-room__unknown">{t('room.unknown', { code: group.code })}</p>
      )}

      <ol className="app-counseling-items">
        {group.items.map((item) => (
          <li
            key={item.item_code}
            className="app-counseling-item"
            data-item={item.item_code}
            data-mandatory={item.mandatory}
          >
            <p className="app-counseling-item__flags">
              <span className="app-counseling-item__code">{item.item_code}</span>
              <span className="app-counseling-item__mandatory">
                {item.mandatory ? t('item.mandatory') : t('item.optional')}
              </span>
            </p>

            {(locale === 'bn' ? ['bn', 'en'] : ['en', 'bn']).map((half) =>
              half === 'en' ? (
                <p className="app-counseling-item__text" lang="en" key="en">
                  {item.text_en.trim() === '' ? (
                    <span
                      className="app-counseling-item__missing"
                      data-testid={`missing-en-${item.item_code}`}
                    >
                      {t('item.missingEN')}
                    </span>
                  ) : (
                    item.text_en
                  )}
                </p>
              ) : (
                <p className="app-counseling-item__text" lang="bn" key="bn">
                  {item.text_bn.trim() === '' ? (
                    <span
                      className="app-counseling-item__missing"
                      data-testid={`missing-bn-${item.item_code}`}
                    >
                      {t('item.missingBN')}
                    </span>
                  ) : (
                    item.text_bn
                  )}
                </p>
              ),
            )}

            {(item.guidance_en || item.guidance_bn) && (
              <div className="app-counseling-item__guidance">
                <p className="app-counseling-item__guidance-title">{t('item.guidance')}</p>
                {(locale === 'bn' ? ['bn', 'en'] : ['en', 'bn']).map((half) =>
                  half === 'en'
                    ? item.guidance_en && (
                        <p lang="en" key="en">
                          {item.guidance_en}
                        </p>
                      )
                    : item.guidance_bn && (
                        <p lang="bn" key="bn">
                          {item.guidance_bn}
                        </p>
                      ),
                )}
              </div>
            )}
          </li>
        ))}
      </ol>
    </>
  );
}
