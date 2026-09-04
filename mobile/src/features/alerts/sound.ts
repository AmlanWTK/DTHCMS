import { createAudioPlayer, setAudioModeAsync, type AudioPlayer } from 'expo-audio';

import alarmTone from '@/assets/sounds/critical-alert.wav';

/**
 * The audible half of criterion 1 (CP50).
 *
 * # Why there is a sound file at all
 *
 * A visual alert on a phone in a busy clinic is an alert nobody sees. The operator is looking
 * at a patient, a cuff and a tape measure; the screen is face-up on a trolley two feet away.
 * §3 step 5 asks for "visual and audible warning" and means it.
 *
 * # What the sound is
 *
 * Five pulses, then a gap, then the same again — the rhythm IEC 60601-1-8 gives a
 * high-priority medical alarm, which is what makes an alarm identifiable as an alarm across a
 * room full of other noises. It is not a conformance claim: the standard also specifies
 * levels and spectra that depend on the device, and the plan's own risk note says the sound
 * design has to be validated on the floor. That is on the manual verification list.
 *
 * # Why it repeats rather than plays once
 *
 * Because the failure mode is somebody in the next room. A single chime while an operator is
 * washing their hands is a chime that never happened.
 *
 * # Why nothing here throws
 *
 * A phone on silent, a broken speaker, an audio session another app has taken — none of them
 * may stop a critical value being shown. The sound is the second channel, not the first, and
 * a screen that crashed because it could not make a noise would have removed the first one
 * too.
 */

let player: AudioPlayer | null = null;
let prepared = false;

/**
 * Prepare the audio session once, at station start-up.
 *
 * `playsInSilentMode` on purpose: an alarm that a switch on the side of the phone can silence
 * is an alarm the clinic will silence on the first day and never restore.
 */
export async function prepareAlarm(): Promise<void> {
  if (prepared) return;
  prepared = true;
  try {
    await setAudioModeAsync({ playsInSilentMode: true, shouldPlayInBackground: false });
    player = createAudioPlayer(alarmTone);
    player.loop = true;
  } catch {
    // Deliberately silent. See the note above: the visual alert must survive this.
    player = null;
  }
}

/** Start the alarm, and keep it going until stopAlarm. */
export function startAlarm(): void {
  try {
    if (player === null) return;
    player.seekTo(0);
    player.play();
  } catch {
    /* the visual alert is still on screen */
  }
}

/** Stop it. Called when the operator says they have seen the alert, and never on a timer. */
export function stopAlarm(): void {
  try {
    player?.pause();
  } catch {
    /* nothing to do */
  }
}

/** Release the player when the station screen goes away. */
export function releaseAlarm(): void {
  try {
    player?.remove();
  } catch {
    /* nothing to do */
  }
  player = null;
  prepared = false;
}
