'use client';

import { useTranslations } from 'next-intl';

import { Icon } from '@dthcms/ui';

import type { SignatureImageCaveat } from '../api/signing';

/**
 * The handwritten signature image, and the sentence that has to be beside it (`docs/signing.md` §6).
 *
 * # This block exists so the two can never be confused on screen
 *
 * A prescription carries two things people call "the signature": a picture of the physician's
 * handwriting, and an Ed25519 signature over a canonical serialisation of what he decided. Only one
 * of them proves anything. A pasted image is trivially forged by anybody with a scanner; the
 * cryptographic signature is not forgeable at all without the private key.
 *
 * The danger is not that somebody will write code that confuses them — the Go type for the picture
 * has no bytes and no `verify`, which makes that hard. The danger is that a **reader** will, because
 * a handwritten signature is what four hundred years of paper has taught everybody to look for. So
 * this block:
 *
 *   - sits in its own frame, under its own heading, visually unlike the verification banner;
 *   - is drawn in a quiet, neutral treatment and never in the green a verified signature gets, so
 *     that nothing about it reads as a verdict;
 *   - carries the caveat the **server** composed, in both languages, rather than a sentence this
 *     component wrote — one wording, on the screen and on the paper, from `TheImageIsNotTheSignature()`;
 *   - says what does prove it, in the same breath, because "this is not proof" without "that is"
 *     leaves a pharmacist with nothing to do.
 *
 * # There is no image here yet
 *
 * CP84 stores none — CP89 owns the paper. What is drawn is the space and the caveat, so that
 * whoever adds the picture adds it to a block that already says what it is, rather than adding the
 * picture first and the sentence later.
 */
export function SignatureImageNote({ caveat, bn }: { caveat: SignatureImageCaveat; bn: boolean }) {
  const t = useTranslations('signing');

  return (
    <aside className="app-sign__image" data-testid="signature-image-note">
      <p className="app-sign__image-head">
        <Icon name="help-circle" size={14} /> {t('image.title')}
      </p>
      <div className="app-sign__image-space" aria-hidden="true">
        <span>{t('image.emptySpace')}</span>
      </div>
      <p className="app-sign__image-caveat">{bn ? caveat.caveat_bn : caveat.caveat_en}</p>
      {caveat.present_on_file ? null : (
        <p className="app-sign__image-none">{t('image.noneOnFile')}</p>
      )}
    </aside>
  );
}
