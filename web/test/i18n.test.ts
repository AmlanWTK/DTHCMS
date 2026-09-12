import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import en from '../messages/en.json';
import bn from '../messages/bn.json';

/**
 * CP10 acceptance criterion 2: language switching is complete — no untranslated strings,
 * verified automatically.
 *
 * "Verified automatically" is the load-bearing half. A bilingual interface does not fail
 * loudly; it fails as one English word in a Bangla screen, in a place the person who
 * reads Bangla notices and the person who wrote it never looks.
 *
 * Three failures are worth catching, and they are different from each other:
 *
 *   1. A key in one file and not the other — the string renders as its own key.
 *   2. A key present in both, with the English text copied into the Bangla file — the
 *      commonest way a translation goes missing, and invisible to a key-set comparison.
 *   3. A key used in the code and present in neither file.
 */

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const srcDir = join(webRoot, 'src');

type Tree = Record<string, unknown>;

function flatten(tree: Tree, prefix = ''): Map<string, string> {
  const out = new Map<string, string>();
  for (const [key, value] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object') {
      for (const [k, v] of flatten(value as Tree, path)) out.set(k, v);
    } else {
      out.set(path, String(value));
    }
  }
  return out;
}

const english = flatten(en as Tree);
const bangla = flatten(bn as Tree);

/**
 * Keys whose two languages are identical, each with the reason.
 *
 * Same idea as the design system's contrast contract: an exemption is allowed, and it has
 * to be argued for in the file. An unexplained one is indistinguishable from a
 * translation somebody forgot.
 */
const IDENTICAL_BY_DESIGN: Record<string, string> = {
  'app.name': 'A product name. Transliterating it would give two different names for one system.',
  'app.clinic': "The clinic's own name as it appears on its signage and its prescriptions.",
  'systemStatus.dependency':
    'Two placeholders and a colon — "{name}: {state}" — with no words to translate. The values it interpolates are already localised.',
  'board.moveFromTo':
    'Three placeholders, a colon and an arrow — "{label}: {from} → {to}". The station names it interpolates are already localised; there is nothing here to translate.',
  'growth.zScore':
    'A symbol and a number — "z = {z}". The letter z is the notation itself, the same in every paediatric chart printed in Bangla; transliterating it would make the value unrecognisable to the clinician reading it.',
  'dashboard.assistant.decidedBy':
    'Two placeholders and a comma — "{who}, {at}". The name is a colleague\'s own and is never translated; the time is already formatted in the reader\'s locale before it reaches this message.',
  'formulary.review.cycle':
    'Two placeholders, a middle dot and nothing else — "{month} · {status}". Both values are localised before they reach it: the month by the date formatter, the status by its own message.',
  'formulary.import.columns':
    'The CSV header this import expects, letter for letter. It is what the person must type into the first row of their file, so translating it would make the instruction wrong — the parser matches `unit_price_bdt`, not its Bangla name.',
  'medicationRules.sandbox.egfr':
    'The notation itself. eGFR is written "eGFR" on every Bangladeshi lab report and in every ' +
    'Bangla-language renal guideline; transliterating it would make the field unrecognisable to ' +
    'the physician filling it in.',
  'prescriptions.safety.summary':
    'A single placeholder — "{summary}" — and nothing else. The sentence it interpolates is ' +
    'written by the safety engine in both languages and picked before it reaches this message; ' +
    'a second wording here would be a second author of clinical text.',
  'prescriptions.preview.price':
    'The taka sign and a placeholder — "৳ {amount}". The sign is the same character in both ' +
    "interfaces and the amount is an ASCII figure by the design system's rule, so that a price " +
    'read back over a phone or copied onto a paper chart cannot be transcribed wrongly.',
  'attribution.byPerson':
    'Two placeholders and a dash — "{name} — {role}". The name is a person\'s own and is never translated; the role is already localised before it reaches this message. There is nothing here to put into Bangla.',
};

describe('the two message files agree on what exists', () => {
  it('has the same keys in both languages', () => {
    const missingBangla = [...english.keys()].filter((key) => !bangla.has(key));
    const missingEnglish = [...bangla.keys()].filter((key) => !english.has(key));

    expect(missingBangla, `Keys with no Bangla: ${missingBangla.join(', ')}`).toEqual([]);
    expect(missingEnglish, `Keys with no English: ${missingEnglish.join(', ')}`).toEqual([]);
  });

  it('has something to translate', () => {
    // Guards the two tests above from passing vacuously if the files were ever emptied.
    expect(english.size).toBeGreaterThan(50);
  });
});

describe('nothing is left in English inside the Bangla file', () => {
  it('has no untranslated value', () => {
    const untranslated = [...english.entries()]
      .filter(([key, value]) => bangla.get(key) === value)
      .map(([key]) => key)
      .filter((key) => !(key in IDENTICAL_BY_DESIGN));

    expect(
      untranslated,
      `Identical in both languages with no recorded reason: ${untranslated.join(', ')}. ` +
        `If a value is genuinely the same in both, add it to IDENTICAL_BY_DESIGN and say why.`,
    ).toEqual([]);
  });

  it('has no exemption that is no longer identical', () => {
    // A stale exemption is a permission nobody is using and everybody trusts.
    const stale = Object.keys(IDENTICAL_BY_DESIGN).filter(
      (key) => english.get(key) !== bangla.get(key),
    );
    expect(stale, `No longer identical, so the exemption should go: ${stale.join(', ')}`).toEqual(
      [],
    );
  });

  it('writes Bangla in Bengali script', () => {
    // A spot check that catches a file pasted in from the wrong source: at least four
    // fifths of the translated values should contain Bengali characters.
    const translated = [...bangla.entries()].filter(([key]) => !(key in IDENTICAL_BY_DESIGN));
    const bengali = translated.filter(([, value]) => /[ঀ-৿]/.test(value));
    expect(bengali.length / translated.length).toBeGreaterThan(0.8);
  });
});

describe('every key the code asks for exists', () => {
  const files: string[] = [];
  (function walk(dir: string) {
    for (const entry of readdirSync(dir)) {
      const path = join(dir, entry);
      if (statSync(path).isDirectory()) walk(path);
      else if (/\.tsx?$/.test(entry)) files.push(path);
    }
  })(srcDir);

  /*
   * A static scan, and honest about its limits: it finds literal `t('a.b')` calls and the
   * namespace given to `useTranslations('x')`. A key built at runtime — `t(item.labelKey)`
   * in the sidebar — cannot be seen from here, which is exactly why those keys are held
   * in navigation.ts and checked separately below.
   */
  const used = new Set<string>();
  for (const file of files) {
    const source = readFileSync(file, 'utf8');
    const namespace = /useTranslations\('([^']+)'\)/.exec(source)?.[1] ?? '';
    for (const match of source.matchAll(/\bt\('([a-zA-Z][\w.]*)'/g)) {
      const key = match[1];
      if (key === undefined) continue;
      used.add(namespace ? `${namespace}.${key}` : key);
    }
  }

  it('finds keys to check', () => {
    expect(used.size).toBeGreaterThan(20);
  });

  it('has every literal key in both files', () => {
    const missing = [...used].filter((key) => !english.has(key) || !bangla.has(key));
    expect(missing, `Used in code, missing from a message file: ${missing.join(', ')}`).toEqual([]);
  });
});

describe('the keys navigation supplies at runtime exist', () => {
  it('has a label for every group and item', async () => {
    // The static scan above cannot see these, and they are the labels on every element of
    // the sidebar — the most visible strings in the application.
    const { ROUTE_GROUPS } = await import('@/lib/navigation');

    const keys = ROUTE_GROUPS.flatMap((group) => [
      group.labelKey,
      ...group.items.map((item) => item.labelKey),
    ]);

    for (const key of keys) {
      expect(english.has(key), `${key} in English`).toBe(true);
      expect(bangla.has(key), `${key} in Bangla`).toBe(true);
    }
  });

  it('has a label for every patient sub-route', async () => {
    // These reuse each screen's own header key rather than inventing a second name, so this
    // is really asserting that the key each screen renders from still exists.
    const { PATIENT_SUBROUTES } = await import('@/lib/navigation');

    for (const route of PATIENT_SUBROUTES) {
      expect(english.has(route.labelKey), `${route.labelKey} in English`).toBe(true);
      expect(bangla.has(route.labelKey), `${route.labelKey} in Bangla`).toBe(true);
    }
  });

  it('has a name for every role the server can report', async () => {
    const { ROLE_CODES } = await import('@/lib/permissions');
    for (const role of ROLE_CODES) {
      expect(english.has(`role.${role}`), `${role} in English`).toBe(true);
      expect(bangla.has(`role.${role}`), `${role} in Bangla`).toBe(true);
    }
  });

  it('has a title and a description for every page in navigation', async () => {
    const { ROUTE_GROUPS } = await import('@/lib/navigation');
    // Every nav item's label key is `nav.x`; the page keys mirror it as `page.x.*`.
    for (const group of ROUTE_GROUPS) {
      for (const item of group.items) {
        const name = item.labelKey.replace(/^nav\./, '');
        expect(english.has(`page.${name}.title`), `page.${name}.title`).toBe(true);
        expect(bangla.has(`page.${name}.description`), `page.${name}.description`).toBe(true);
      }
    }
  });
});

describe('ICU messages are usable in both languages', () => {
  it('keeps the plural argument in the Bangla plural', () => {
    // Bengali has one plural category in CLDR where English has two. The Bangla message
    // is therefore allowed to omit `one` — but it must still be a plural on the same
    // argument name, or the count vanishes.
    const key = 'shell.patientsWaiting';
    expect(english.get(key)).toContain('{count, plural');
    expect(bangla.get(key)).toContain('{count, plural');
  });

  it('uses the same placeholders on both sides of every message', () => {
    /*
     * `{name}` and `{name, plural, ...}` are placeholders. `{No patients waiting}` is a
     * plural branch body and is not — which a naive `\{(\w+)` would have called one, and
     * did, on the first run of this test.
     *
     * The comparison is of the *set* of arguments and deliberately not of how many times each
     * appears. Bengali has one plural category where English has two — the test above says so
     * — which means an English plural whose branches interpolate a second argument writes that
     * argument once per branch while the Bangla writes it once in a single sentence:
     *
     *     en: {part, plural, one {# of {whole, number} was …} other {# of {whole, number} were …}}
     *     bn: {whole, number}টির মধ্যে {part, number}টি …
     *
     * Both use `part` and `whole` and neither is missing anything. Counting occurrences would
     * fail every message of that shape, which is an ordinary ICU one, and the pressure would be
     * to write worse English rather than to fix the test. What matters — an argument a screen
     * passes that one language silently drops — is still caught, in both directions.
     */
    const placeholders = (text: string) =>
      [...new Set([...text.matchAll(/\{(\w+)\s*[},]/g)].map((match) => match[1]))].sort();

    for (const [key, value] of english) {
      const other = bangla.get(key);
      if (other === undefined) continue;
      expect(placeholders(other), `${key} placeholders`).toEqual(placeholders(value));
    }
  });
});
