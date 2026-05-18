// M8 session-3 smoke: multi-list switcher, fractional-indexing
// reorder, qty inline edit. Runs in demo mode (no Hub required).

import { test, expect } from '@playwright/test';

test.describe.configure({ mode: 'serial' });

test('roster session 3 / multi-list switcher creates and switches', async ({ page }) => {
  await page.goto('/pages/roster.html');
  await expect(page.locator('#list-title')).toHaveText('Groceries');

  // Create a second list via the test hook (avoids the prompt() call).
  await page.evaluate(() => window.__ROSTER__.createList('Hardware store'));
  await expect(page.locator('#list-title')).toHaveText('Hardware store');

  // New list starts empty + 1 metadata event.
  const items = page.locator('li.item');
  await expect(items).toHaveCount(0);
  const stateA = await page.evaluate(() => window.__ROSTER__.getState());
  expect(stateA.knownLists.length).toBe(2);
  expect(stateA.currentListKey).toMatch(/^demo:/);

  // Add an item to the new list.
  await page.fill('#add-text', 'Drill bits');
  await page.click('button[type="submit"]');
  await expect(items).toHaveCount(1);
  await expect(page.locator('li.item', { hasText: 'Drill bits' })).toHaveCount(1);

  // Switch back to Groceries via the switcher dropdown.
  await page.click('#title-btn');
  await expect(page.locator('#switcher')).toHaveAttribute('aria-expanded', 'true');
  const groceriesBtn = page.locator('#switcher button.list-row', { hasText: 'Groceries' });
  await expect(groceriesBtn).toHaveCount(1);
  await groceriesBtn.click();
  await expect(page.locator('#list-title')).toHaveText('Groceries');
  await expect(items).toHaveCount(14);

  // Hardware-store items should NOT leak into Groceries.
  await expect(page.locator('li.item', { hasText: 'Drill bits' })).toHaveCount(0);
});

test('roster session 3 / reorder moves an item up via arrow button', async ({ page }) => {
  await page.goto('/pages/roster.html');

  // Capture the first three rows in original order.
  const initial = await page.evaluate(() =>
    window.__ROSTER__.getItems().slice(0, 3).map((i) => i.text)
  );
  expect(initial[0]).toBe('Milk');

  // Move "Atta" (row 2) up to position 1 via the up-arrow.
  const atta = page.locator('li.item', { hasText: 'Atta' });
  await atta.hover();
  await atta.locator('button[aria-label="Move Atta up"]').click();

  const after = await page.evaluate(() =>
    window.__ROSTER__.getItems().slice(0, 3).map((i) => i.text)
  );
  expect(after[0]).toBe('Atta');
  expect(after[1]).toBe('Milk');

  // The materializer should have produced a list:item-reordered event.
  const events = await page.evaluate(() => window.__ROSTER__.getEvents());
  expect(events.some((e) => e.kind === 'list:item-reordered')).toBe(true);
});

test('roster session 3 / qty inline edit produces a list:item-edited event with qty', async ({ page }) => {
  await page.goto('/pages/roster.html');

  // Find Milk's id, click its qty cell, type a new qty, press Enter.
  const milkId = await page.evaluate(() => {
    const it = window.__ROSTER__.getItems().find((i) => i.text === 'Milk');
    return it.item_id;
  });
  const row = page.locator(`li.item[data-item-id="${milkId}"]`);
  const qty = row.locator('.qty');
  await expect(qty).toHaveText('2L');
  await qty.click();
  const edit = row.locator('.qty input.edit');
  await expect(edit).toBeVisible();
  await edit.fill('1L');
  await edit.press('Enter');

  await expect(page.locator(`li.item[data-item-id="${milkId}"] .qty`)).toHaveText('1L');

  const edited = await page.evaluate(() => {
    return window.__ROSTER__.getEvents()
      .filter((e) => e.kind === 'list:item-edited')
      .map((e) => e.payload);
  });
  expect(edited.some((p) => p.qty === '1L')).toBe(true);
});

test('roster session 3 / fractional indexing produces strictly-between keys', async ({ page }) => {
  await page.goto('/pages/roster.html');
  const results = await page.evaluate(() => {
    const fi = window.__ROSTER__.fiBetween;
    const cases = [
      ['a', 'b'],
      ['an', 'ao'],
      ['m', 'n'],
      ['a', 'c'],
      [null, 'g'],
      ['m', null],
      [null, null],
    ];
    return cases.map(([a, b]) => {
      const c = fi(a, b);
      const okLower = a === null || c > a;
      const okUpper = b === null || c < b;
      return { a, b, c, okLower, okUpper };
    });
  });
  for (const r of results) {
    expect(r.okLower, `${JSON.stringify(r)} not strictly greater than lower bound`).toBe(true);
    expect(r.okUpper, `${JSON.stringify(r)} not strictly less than upper bound`).toBe(true);
  }
});
