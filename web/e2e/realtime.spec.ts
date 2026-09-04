import { expect } from '@playwright/test';

import { test } from './fixtures';

/**
 * CP27 in a real browser: the connection indicator, and what a network loss looks like to
 * the person in front of the screen.
 *
 * The gateway itself is proven on the Go side — including against a real Chromium
 * WebSocket client — so what is checked here is the half that only exists in this
 * application: that the state reaches the top bar, in both languages, and that a drop and
 * a recovery are visible without a refresh.
 *
 * `WebSocket` is replaced before the page loads. A real socket would need a real gateway,
 * a real session and a real Redis to run the browser suite, and the suite deliberately
 * runs without a backend.
 */

const FAKE_SOCKET = `
  window.__sockets = [];
  window.__refuse = false;
  class FakeWS {
    constructor(url) {
      this.url = url; this.sent = []; this.closed = false;
      this.onopen = null; this.onclose = null; this.onerror = null; this.onmessage = null;
      window.__sockets.push(this);
      setTimeout(() => {
        if (this.closed) return;
        if (window.__refuse) { this.closed = true; this.onclose && this.onclose({ code: 1006 }); return; }
        this.onopen && this.onopen({});
      }, 20);
    }
    send(data) { this.sent.push(data); }
    close() { this.closed = true; this.onclose && this.onclose({ code: 1000 }); }
  }
  window.WebSocket = FakeWS;
  window.__drop = () => {
    const socket = window.__sockets[window.__sockets.length - 1];
    if (socket) { socket.closed = true; socket.onclose && socket.onclose({ code: 1006 }); }
  };
`;

test.describe('CP27: the realtime connection', () => {
  test('says the screen is live, and says so quietly', async ({ signedIn: page }) => {
    await page.addInitScript(FAKE_SOCKET);
    await page.goto('/');

    const indicator = page.locator('[data-status]').filter({ hasText: 'Live' });
    await expect(indicator).toBeVisible();
    await expect(indicator).toHaveAttribute('data-status', 'live');

    // The socket carried no credential in its URL: the browser's credential is the
    // session cookie, which it attaches to the handshake itself (ADR-0010).
    const url = await page.evaluate(() => window.__sockets[0]?.url as string);
    expect(url).toContain('/v1/realtime');
    expect(url).not.toMatch(/token|bearer|access/i);
  });

  // Criterion 1 and 3, from the operator's side: the wifi drops, and they can see it.
  test('shows the connection dropping and coming back, without a refresh', async ({
    signedIn: page,
  }) => {
    await page.addInitScript(FAKE_SOCKET);
    await page.goto('/');
    await expect(page.locator('[data-status="live"]')).toBeVisible();

    await page.evaluate(() => window.__drop());
    await expect(page.locator('[data-status="reconnecting"]')).toBeVisible();
    await expect(page.getByText('Reconnecting')).toBeVisible();

    // The client reconnects by itself. Nothing here presses anything.
    await expect(page.locator('[data-status="live"]')).toBeVisible({ timeout: 15_000 });
  });

  // "Reconnecting" stops being an honest word after a while, and the operator should know
  // that what is on the screen is a snapshot.
  test('stops calling it a blip once the connection is really gone', async ({ signedIn: page }) => {
    await page.addInitScript(FAKE_SOCKET);
    await page.goto('/');
    await expect(page.locator('[data-status="live"]')).toBeVisible();

    await page.evaluate(() => {
      window.__refuse = true;
      window.__drop();
    });

    // 1s + 2s + 4s + 8s of backoff before the client gives up calling it temporary.
    await expect(page.locator('[data-status="offline"]')).toBeVisible({ timeout: 45_000 });
    await expect(page.getByText('Not live')).toBeVisible();
  });

  test('is in Bangla when the interface is', async ({ bangla: page }) => {
    await page.addInitScript(FAKE_SOCKET);
    await page.goto('/');
    await expect(page.getByText('লাইভ')).toBeVisible();
  });

  // A station tablet is 820 points wide in portrait, and the top bar is already carrying a
  // role switcher and a language toggle. The indicator must fit beside them.
  test('survives a tablet-width top bar', async ({ signedIn: page }) => {
    await page.setViewportSize({ width: 820, height: 1180 });
    await page.addInitScript(FAKE_SOCKET);
    await page.goto('/');
    const indicator = page.locator('[data-status="live"]');
    await expect(indicator).toBeVisible();

    // Inside the viewport, not pushed off the edge by the role switcher beside it.
    const box = await indicator.boundingBox();
    const width = page.viewportSize()?.width ?? 0;
    expect(box).not.toBeNull();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
  });
});

declare global {
  interface Window {
    __sockets: Array<{ url: string; sent: string[] }>;
    __refuse: boolean;
    __drop: () => void;
  }
}
