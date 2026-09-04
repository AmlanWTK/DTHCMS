import type { DuplicateCandidate } from '../api/patients';

/**
 * A match reason in the reader's language.
 *
 * The server sends both, always. Choosing here rather than server-side keeps the response
 * cacheable and lets one screen show one language while another shows the other — but the
 * choice has to actually be made, and a component that reaches for `.message` unconditionally
 * shows a Bangla-reading registration officer an English sentence at the moment they are
 * deciding whether two records are one person.
 */
export function reasonText(reason: DuplicateCandidate['reasons'][number], locale: string): string {
  return locale === 'bn' && reason.message_bn ? reason.message_bn : reason.message;
}
