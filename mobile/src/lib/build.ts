/**
 * What this build is, for the device record and the crash report.
 *
 * A constant rather than a read of `expo-constants`: that module drags the whole React
 * Native import graph behind it, which the tests cannot parse and do not need. The value
 * must match `app.json`, and `test/build.test.ts` fails the suite when it does not — so
 * bumping the version is a two-line change that cannot be half done.
 */
export const APP_VERSION = '0.1.0';
