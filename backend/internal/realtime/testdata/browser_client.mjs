// A real browser opening a real WebSocket to the gateway.
//
// The frame layer is verified against hand-written bytes in ws_test.go, and the gateway
// against this repository's own client. Neither proves the thing that actually matters:
// that Chromium's WebSocket implementation — the only client the web application has —
// accepts this handshake and these frames. Autobahn cannot run here; a browser can.
//
// Usage: node browser_client.mjs <page-url> <ws-url> <user-id> <role> <topic>
//
// The page is loaded first so that the browser has a real origin: a WebSocket opened from
// `about:blank` sends `Origin: null`, which the gateway refuses — correctly, and which the
// companion test asserts separately.
// Prints one JSON line: {"ok":true,"envelopes":[...]} or {"ok":false,"error":"..."}

import { chromium } from 'playwright';

const [pageURL, url, userId, role, topic] = process.argv.slice(2);

// PLAYWRIGHT_CHROMIUM_EXECUTABLE lets the caller point at a browser that is already on the
// machine, which is how this runs where Playwright's own download is pinned to a different
// build than the one installed.
const launch = { args: ['--no-sandbox'] };
if (process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE) {
  launch.executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
}

const browser = await chromium.launch(launch);
try {
  const page = await browser.newPage();
  await page.goto(pageURL);

  // The handshake needs headers a browser cannot set on `new WebSocket`, so the test
  // server reads them from the query string. That is a test affordance and never the
  // production path: the real gateway takes the session cookie the browser sends by
  // itself, and a token in a URL is a token in an access log.
  const result = await page.evaluate(
    ([url, userId, role, topic]) =>
      new Promise((resolve) => {
        const envelopes = [];
        const socket = new WebSocket(
          `${url}?user=${encodeURIComponent(userId)}&role=${encodeURIComponent(role)}`,
        );
        const done = (extra) => {
          try {
            socket.close(1000, 'done');
          } catch {
            /* already closing */
          }
          resolve({ envelopes, protocol: socket.protocol, ...extra });
        };
        const failure = setTimeout(() => done({ ok: false, error: 'timed out' }), 15000);

        socket.onerror = () => done({ ok: false, error: 'the browser reported a socket error' });
        socket.onopen = () => {
          socket.send(JSON.stringify({ type: 'subscribe', topics: [topic] }));
          socket.send(JSON.stringify({ type: 'ping' }));
          // A payload that spans the 16-bit length encoding, and multi-byte UTF-8, so the
          // browser's framing and ours have to agree on both.
          socket.send(JSON.stringify({ type: 'unknown', note: 'রোগী'.repeat(200) }));
        };
        socket.onmessage = (event) => {
          const envelope = JSON.parse(event.data);
          envelopes.push(envelope);
          if (envelope.type === 'message') {
            clearTimeout(failure);
            done({ ok: true });
          }
        };
        socket.onclose = (event) => {
          clearTimeout(failure);
          if (!envelopes.some((e) => e.type === 'message')) {
            done({ ok: false, error: `closed early: ${event.code} ${event.reason}` });
          }
        };
      }),
    [url, userId, role, topic],
  );

  process.stdout.write(JSON.stringify(result) + '\n');
} catch (error) {
  process.stdout.write(JSON.stringify({ ok: false, error: String(error) }) + '\n');
} finally {
  await browser.close();
}
