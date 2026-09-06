import { ScreenShell } from '@/components/ScreenShell';
import { CorrectionInbox } from '@/features/corrections';
import { MyRecordLink } from '@/features/quality';

/**
 * The values somebody has asked me to look at again (CP62, §4.3).
 *
 * # Why this sits in the station group
 *
 * A correction request is answered where the value was taken: the operator is at their
 * station, the patient is often still in front of them, and the act is to read the tape again
 * and retype a number. It is not a review surface and it is not an inbox with a workflow — it
 * is the station's other job, so it lives beside `station.tsx` rather than in a group of its
 * own.
 *
 * # How anybody gets here
 *
 * The shell's own notice. `CorrectionNotice` sits in `ScreenShell`, so a flag reaches an
 * operator wherever they are — mid-vitals, on the queue — and one press brings them here.
 * That is acceptance criterion 4: the author is told on their device. A screen reachable only
 * by somebody who already knew to look for it would satisfy the words and none of the point.
 *
 * # There is nothing here but the panel
 *
 * No patient header, no station header, no queue. The requests carry what they are about, the
 * value carries who entered it, and a patient banner above them would invite an operator to
 * read the flags of whoever is currently in the chair — which is not what this queue is.
 *
 * # One link, below the panel (CP63)
 *
 * The operator's own quality record is the adjacent question — *what have I been asked to look
 * at again* and *what does my own record say* are one subject — and this is the one screen
 * where somebody is already thinking about it. It is below the queue rather than above it,
 * because the queue is the thing to do and the record is the thing to read; and it is here
 * whether or not anything has been written on that record, because a record reachable only on
 * the day somebody raises a note about you is one you read as being kept from you until it is
 * used against you (ADR-0029).
 */
export default function CorrectionsScreen() {
  return (
    <ScreenShell titleKey="screen.corrections">
      <CorrectionInbox />
      <MyRecordLink />
    </ScreenShell>
  );
}
