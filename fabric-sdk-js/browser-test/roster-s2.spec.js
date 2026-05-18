// M8 session-2 gate: roster.html boots into Fabric mode via
// window.__GATE, writes through a real Hub, re-reads after reload,
// and surfaces freshness. The harness (scripts/roster-fabric-gate.sh)
// stands up nakli-hub on a free port and mints a wildcard Grant
// before this test runs.

import { readFile } from 'node:fs/promises';
import { test, expect } from '@playwright/test';

const gateConfigPath = process.env.ROSTER_GATE_CONFIG ?? './browser-test/roster-gate-config.json';

test.describe.configure({ mode: 'serial' });

let GATE;
test.beforeAll(async ({}, testInfo) => {
  const base = JSON.parse(await readFile(gateConfigPath, 'utf8'));
  // Stream IDs must be unique per browser project so Chromium / Firefox /
  // WebKit don't pollute each other's reads. The Crockford ULID alphabet
  // includes C, F, W so picking the first char of the project name as the
  // trailing differentiator keeps the ID a valid ULID.
  const proj = testInfo.project.name.toUpperCase()[0]; // C / F / W
  GATE = { ...base, streamId: base.streamId.slice(0, 25) + proj };
});

test('roster session 2 / boots into Fabric mode against the Hub', async ({ page }) => {
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e)));
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text());
  });

  await page.addInitScript((gate) => { window.__GATE = gate; }, GATE);
  await page.goto('/pages/roster.html');

  // The boot path appends a list:metadata event, then a poll runs. The
  // banner switches to "Connected to ..." when activateFabricMode finishes.
  await expect(page.locator('#banner')).toContainText('Connected to', { timeout: 10_000 });
  await expect(page.locator('#banner')).toContainText(GATE.hubUrl);

  // Initial state: empty list (this is a fresh stream id).
  await expect(page.locator('#list-title')).toHaveText('Groceries');
  await expect(page.locator('li.item')).toHaveCount(0);

  const mode = await page.evaluate(() => window.__ROSTER__.getState().mode);
  expect(mode).toBe('fabric');

  // Add three items through the UI.
  for (const [text, qty] of [['Milk', '2L'], ['Bread', '1'], ['Apples', '6']]) {
    await page.fill('#add-text', text);
    await page.fill('#add-qty', qty);
    await page.click('button[type="submit"]');
    await expect(page.locator('li.item', { hasText: text })).toHaveCount(1, { timeout: 5_000 });
  }
  await expect(page.locator('li.item')).toHaveCount(3);
  await expect(page.locator('#list-meta')).toHaveText('3 open');

  // Freshness indicator is populated and shows a small staleness.
  const freshTxt = await page.locator('#freshness').innerText();
  expect(freshTxt.length).toBeGreaterThan(0);

  expect(errors, `page errors: ${errors.join('\n')}`).toEqual([]);
});

test('roster session 2 / items survive a reload (round-tripped through the Hub)', async ({ page }) => {
  await page.addInitScript((gate) => { window.__GATE = gate; }, GATE);
  await page.goto('/pages/roster.html');
  await expect(page.locator('#banner')).toContainText('Connected to', { timeout: 10_000 });

  // After Fabric boot, read() pulls the three previously-appended items.
  await expect(page.locator('li.item')).toHaveCount(3, { timeout: 10_000 });
  await expect(page.locator('li.item', { hasText: 'Milk' })).toHaveCount(1);
  await expect(page.locator('li.item', { hasText: 'Bread' })).toHaveCount(1);
  await expect(page.locator('li.item', { hasText: 'Apples' })).toHaveCount(1);

  // Check off Milk — write must hit the Hub.
  const milk = page.locator('li.item', { hasText: 'Milk' });
  await milk.locator('input[type="checkbox"]').check();
  await expect(milk).toHaveClass(/checked/);

  // The store should hold 5 events for this stream (1 metadata + 3 add + 1 check).
  const evCount = await page.evaluate(() => window.__ROSTER__.getEvents().length);
  expect(evCount).toBeGreaterThanOrEqual(5);
});

test('roster session 2 / new check survives another reload', async ({ page }) => {
  await page.addInitScript((gate) => { window.__GATE = gate; }, GATE);
  await page.goto('/pages/roster.html');
  await expect(page.locator('#banner')).toContainText('Connected to', { timeout: 10_000 });

  await expect(page.locator('li.item')).toHaveCount(3, { timeout: 10_000 });
  const milk = page.locator('li.item', { hasText: 'Milk' });
  await expect(milk).toHaveClass(/checked/);
  await expect(page.locator('#list-meta')).toHaveText('2 open');
});
