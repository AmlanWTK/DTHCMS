#!/usr/bin/env node
/**
 * Builds the API documentation page from api/openapi.yaml.
 *
 * Redocly's own output links Redoc's JavaScript from a CDN and its fonts from Google.
 * Neither is acceptable here, for the same reason CP10 self-hosts the application's fonts:
 * a page that silently fails when a third-party host is unreachable is a page nobody can
 * rely on, and the whole point of this one is that it is the reference an engineer opens
 * when something is already wrong.
 *
 * So the bundle is inlined from node_modules and the font link is dropped. What comes out
 * is one file that works from a memory stick, on a plane, or behind whatever the clinic's
 * connection is doing today.
 */

import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const output = resolve(root, 'api/docs.html');

execFileSync(
  'pnpm',
  [
    'exec',
    'redocly',
    'build-docs',
    'api/openapi.yaml',
    '-o',
    'api/docs.html',
    '--disableGoogleFont',
  ],
  { cwd: root, stdio: 'inherit' },
);

let html = readFileSync(output, 'utf8');

// The tag carries integrity and crossorigin attributes as well as src, and their order is
// not guaranteed — so match the whole element rather than an exact attribute list.
const cdnScript = /<script[^>]*\bsrc="https:\/\/cdn\.redocly\.com\/[^"]*"[^>]*><\/script>/;
if (!cdnScript.test(html)) {
  // Redocly changed its output template. Failing loudly beats shipping a page that looks
  // fine until the day the CDN is unreachable, which is the day it is needed.
  throw new Error(
    'build-api-docs: no CDN script tag found in the generated page.\n' +
      'Redocly’s output template has changed; update the pattern in scripts/build-api-docs.mjs\n' +
      'rather than removing this check.',
  );
}

const PLACEHOLDER = '<!--DTHCMS_REDOC_BUNDLE-->';
html = html.replace(cdnScript, PLACEHOLDER);

/*
 * The guard runs on the markup *before* the bundle goes in.
 *
 * Redoc's own source contains those hostnames in string literals, so scanning the
 * finished 1MB file would report a failure that is not one. What matters is whether the
 * page *loads* anything from outside — a src or a stylesheet link — not whether the text
 * appears somewhere inside a minified bundle.
 */
const externalResource = /(?:\bsrc="https?:|<link\b[^>]*\bhref="https?:)/i;
const offending = html.match(externalResource);
if (offending) {
  throw new Error(`build-api-docs: the page still loads a resource from outside — ${offending[0]}`);
}

// The bundle contains `</script>` inside string literals; splitting on it stops the first
// of them from ending the tag early.
const bundle = readFileSync(require.resolve('redoc/bundles/redoc.standalone.js'), 'utf8');
html = html.replace(
  PLACEHOLDER,
  `<script>${bundle.split('</script>').join('<\\/script>')}</script>`,
);

writeFileSync(output, html);

const kb = Math.round(Buffer.byteLength(html) / 1024);
console.log(`api/docs.html — ${kb} KiB, self-contained (Redoc inlined, no external loads)`);
