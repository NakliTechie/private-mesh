// M5 gate: load /sandbox.html with the gate context, wait for the SDK to
// finish its append + read round-trip, assert success.

import { readFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

const gateConfigPath = process.env.SDK_GATE_CONFIG ?? './browser-test/gate-config.json';

test('M5 gate / vault append + read round-trips through the SDK', async ({ page }) => {
  const cfg = JSON.parse(await readFile(gateConfigPath, 'utf8'));
  await page.addInitScript((gate) => {
    window.__GATE = gate;
  }, cfg);

  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e)));
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text());
  });

  await page.goto('/sandbox.html');
  const result = await page.waitForFunction(() => window.__GATE_RESULT, null, { timeout: 30_000 });
  const value = await result.jsonValue();
  expect(value.ok, `gate failed: ${value.error}`).toBe(true);
  expect(value.eventId).toMatch(/^[0-9A-Z]{20,}/);
  expect(value.sequenceNumber).toBe(1);
  expect(value.payload).toEqual({ item: 'milk', qty: 2, from: 'browser' });
  expect(errors, `page errors: ${errors.join('\n')}`).toEqual([]);
});
