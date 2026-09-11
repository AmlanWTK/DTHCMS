/**
 * `attribution` — who entered a value, on every screen that shows one (CP61, §4.2).
 *
 * §4.2 in full: *any reviewer sees who entered a value **instantly, without digging***. That
 * is a requirement about every clinical value in the application, which makes it a
 * requirement about a component rather than about a screen — a rule followed on nine screens
 * and forgotten on the tenth is not a rule, and the tenth screen is the one built next month.
 *
 * Three pieces:
 *
 * **`ValueWithAttribution`** is the only way a clinical value is drawn. The value, plus the
 * person, the role, the station, the device, the time and the kind of evidence, one
 * interaction away — hover, keyboard focus, or a tap, because the clinic reads screens on
 * cheap tablets where hover does not exist.
 *
 * **`useDirectory`** resolves the ids every clinical read carries into names, once per
 * session. The server deliberately does not join staff records into clinical queries; the
 * name is a lookup, and this is the client half of that decision.
 *
 * **The adapters** — `observationAttribution` and its four siblings — are where "which field
 * on which schema is the attribution" is decided, once. Five record types spell it five
 * ways, and a screen that reached for the field itself would be the screen that renders a
 * uuid the day a sixth is added.
 *
 * **Nothing here is a control.** There is no button on this surface that changes a value,
 * withdraws one or corrects one. It says who, not who is at fault: a staff name is not PHI,
 * but it is a person, and this panel is read over somebody's shoulder on a bad morning.
 */
export {
  ValueWithAttribution,
  type AttributionVariant,
  type ValueWithAttributionProps,
} from './components/ValueWithAttribution';

export { useDirectory } from './api/useDirectory';
export {
  DIRECTORY_KEY,
  DIRECTORY_STALE_MS,
  readDirectory,
  type Directory,
  type DirectoryDevice,
  type DirectoryPerson,
  type DirectoryStation,
} from './api/directory';

export {
  buildLookup,
  isPresentStaff,
  type DirectoryLookup,
  type DirectoryState,
  type ResolvedDevice,
  type ResolvedPerson,
} from './lib/lookup';

export {
  VALUE_SOURCES,
  alertAttribution,
  allergyAttribution,
  allergyChangeAttribution,
  assertionAttribution,
  historyItemAttribution,
  isKnownSource,
  isMachineRead,
  observationAttribution,
  timelineMarkAttribution,
  timelineSeriesPointAttribution,
  type CorrectionKind,
  type ValueAttribution,
  type ValueCorrection,
  type ValueSource,
} from './lib/attribution';

/**
 * The house's answer to "what do we call this person", promoted here from CP57's counselling
 * panel so that one screen cannot name a colleague differently from another. The counselling
 * feature re-exports it, so everything that already imported it from there still works.
 */
export { staffLabel, type StaffFields, type StaffLabel } from './lib/staff';
