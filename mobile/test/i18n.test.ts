import { readFileSync, readdirSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import en from '../src/messages/en.json';
import bn from '../src/messages/bn.json';

/**
 * The same bilingual guarantee the web shell carries, applied to the station app —
 * because this is the surface the plan calls primary, used by the operators most likely
 * to be reading Bangla.
 *
 * Same three failures, checked separately: a key in one file and not the other, English
 * copied into the Bangla file, and a key used in code that exists in neither.
 */

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const srcDir = join(root, 'src');

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

const IDENTICAL_BY_DESIGN: Record<string, string> = {
  'app.name': 'A product name. Transliterating it would give two names for one system.',
};

describe('the two message files agree on what exists', () => {
  it('has the same keys in both languages', () => {
    const missingBangla = [...english.keys()].filter((key) => !bangla.has(key));
    const missingEnglish = [...bangla.keys()].filter((key) => !english.has(key));
    expect(missingBangla, `No Bangla: ${missingBangla.join(', ')}`).toEqual([]);
    expect(missingEnglish, `No English: ${missingEnglish.join(', ')}`).toEqual([]);
  });

  it('has something to translate', () => {
    expect(english.size).toBeGreaterThan(15);
  });
});

describe('nothing is left in English inside the Bangla file', () => {
  it('has no untranslated value', () => {
    const untranslated = [...english.entries()]
      .filter(([key, value]) => bangla.get(key) === value)
      .map(([key]) => key)
      .filter((key) => !(key in IDENTICAL_BY_DESIGN));
    expect(untranslated, `Identical with no recorded reason: ${untranslated.join(', ')}`).toEqual(
      [],
    );
  });

  it('writes Bangla in Bengali script', () => {
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

  const used = new Set<string>();
  for (const file of files) {
    const source = readFileSync(file, 'utf8');
    const namespace = /useTranslations\('([^']+)'\)/.exec(source)?.[1] ?? '';
    for (const match of source.matchAll(/\bt(?:Screen)?\('([a-zA-Z][\w.]*)'/g)) {
      const key = match[1];
      if (key === undefined) continue;
      used.add(namespace ? `${namespace}.${key}` : key);
    }
  }

  it('finds keys to check', () => {
    expect(used.size).toBeGreaterThan(8);
  });

  it('has every literal key in both files', () => {
    // tScreen('login') resolves under 'screen', not the file's first namespace, so it is
    // checked under both — present under either is a pass.
    const missing = [...used].filter((key) => {
      const alsoScreen = `screen.${key.split('.').pop() ?? ''}`;
      const present = (k: string) => english.has(k) && bangla.has(k);
      return !present(key) && !present(alsoScreen);
    });
    expect(missing, `Used in code, in neither file: ${missing.join(', ')}`).toEqual([]);
  });

  it('has a label for every route in the navigation definition', async () => {
    const { MOBILE_ROUTES } = await import('../src/lib/navigation');
    for (const route of MOBILE_ROUTES) {
      expect(english.has(route.labelKey), `${route.labelKey} in English`).toBe(true);
      expect(bangla.has(route.labelKey), `${route.labelKey} in Bangla`).toBe(true);
    }
  });
});

describe('placeholders agree across languages', () => {
  it('uses the same ICU arguments on both sides of every message', () => {
    const placeholders = (text: string) =>
      [...text.matchAll(/\{(\w+)\s*[},]/g)].map((match) => match[1]).sort();
    for (const [key, value] of english) {
      const other = bangla.get(key);
      if (other === undefined) continue;
      expect(placeholders(other), `${key} placeholders`).toEqual(placeholders(value));
    }
  });
});
