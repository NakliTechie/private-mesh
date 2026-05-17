// M8 session-1 smoke: open saanjha.html, exercise the local-state CRUD,
// confirm the materialization shows up. Loads the file via the test
// server's /pages/ route (Playwright doesn't follow CORS for file:// JS
// modules consistently across all three projects).

import { test, expect } from '@playwright/test';

test.describe.configure({ mode: 'serial' });

test('saanjha session 1 / mock list renders, add + check + filter all work', async ({ page }) => {
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e)));
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text());
  });

  await page.goto('/pages/saanjha.html');
  await expect(page.locator('#list-title')).toHaveText('Groceries');

  // Initial mock dataset has 14 items, 2 checked.
  const items = page.locator('li.item');
  await expect(items).toHaveCount(14);
  await expect(page.locator('#list-meta')).toHaveText('12 open');

  // Add an item.
  await page.fill('#add-text', 'Ginger');
  await page.fill('#add-qty', '100g');
  await page.click('button[type="submit"]');
  await expect(items).toHaveCount(15);
  await expect(page.locator('#list-meta')).toHaveText('13 open');

  // Check off the new item.
  const ginger = items.filter({ hasText: 'Ginger' });
  await expect(ginger).toHaveCount(1);
  await ginger.locator('input[type="checkbox"]').check();
  await expect(ginger).toHaveClass(/checked/);
  await expect(page.locator('#list-meta')).toHaveText('12 open');

  // Filter to checked-only — 3 items (2 mocked + new Ginger).
  await page.click('button[data-filter="checked"]');
  await expect(page.locator('button[data-filter="checked"]')).toHaveAttribute('aria-pressed', 'true');
  await expect(items).toHaveCount(3);
  await expect(page.locator('#filter-meta')).toContainText('Showing done (3 of 15)');

  // Filter to open-only — 12 items.
  await page.click('button[data-filter="open"]');
  await expect(items).toHaveCount(12);

  // Back to all.
  await page.click('button[data-filter="all"]');
  await expect(items).toHaveCount(15);

  // Materialization function is callable from the page (helps session 2's
  // tests when we swap mock for fabric events).
  const itemCount = await page.evaluate(() => window.__SAANJHA__.getItems().length);
  expect(itemCount).toBe(15);

  // No console errors and no uncaught page errors.
  expect(errors, `page errors: ${errors.join('\n')}`).toEqual([]);
});

test('saanjha session 1 / inline edit a row', async ({ page }) => {
  await page.goto('/pages/saanjha.html');

  // Pin the row by its item id once. Once edit mode flips the visible
  // text into an input value (not a text node), `hasText` would stop
  // matching the row — so we capture the id first and hold it.
  const milkId = await page.evaluate(() => {
    const items = window.__SAANJHA__.getItems();
    return items.find((i) => i.text === 'Milk').item_id;
  });
  expect(milkId, 'mock data should contain Milk').toBeTruthy();
  const row = page.locator(`li.item[data-item-id="${milkId}"]`);
  await expect(row).toHaveCount(1);

  await row.locator('.text').click();
  const editor = row.locator('input.edit');
  await expect(editor).toBeVisible();
  await editor.fill('Whole milk');
  await editor.press('Enter');

  await expect(page.locator('li.item', { hasText: 'Whole milk' })).toHaveCount(1);
  await expect(page.locator('li.item', { hasText: /^Milk\s/ })).toHaveCount(0);
});
