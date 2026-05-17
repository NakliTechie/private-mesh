// M8 session-4 smoke: operator side-drawer + .naklilist export. Runs
// in demo mode (no Hub) so the test is fast and deterministic.

import { test, expect } from '@playwright/test';

test.describe.configure({ mode: 'serial' });

test('saanjha session 4 / drawer opens and surfaces sync/members/lists/about', async ({ page }) => {
  await page.goto('/pages/saanjha.html');

  // Drawer starts closed.
  await expect(page.locator('#drawer')).toHaveAttribute('aria-hidden', 'true');
  await page.click('#menu-btn');
  await expect(page.locator('#drawer')).toHaveAttribute('aria-hidden', 'false');

  // Panels visible: Sync status, Members, Lists, Conflicts, Export, About.
  const drawer = page.locator('#drawer');
  for (const heading of ['Sync status', 'Members', 'Lists', 'Conflicts', 'Export', 'About']) {
    await expect(drawer.locator('h3', { hasText: heading })).toHaveCount(1);
  }

  // Demo mode → Mode shows "Demo (local)" with warn tint.
  await expect(drawer.locator('section', { hasText: 'Sync status' })).toContainText('Demo (local)');

  // Members panel has at least the four mock principals (3 humans + agent).
  const memberRows = drawer.locator('section', { hasText: 'Members' }).locator('.row');
  expect(await memberRows.count()).toBeGreaterThanOrEqual(4);

  // About panel reports a session-5 version tag and "(not loaded — demo mode)" for the SDK.
  await expect(drawer.locator('section', { hasText: 'About' })).toContainText('saanjha-session-');
  await expect(drawer.locator('section', { hasText: 'About' })).toContainText('demo mode');

  // Escape closes the drawer.
  await page.keyboard.press('Escape');
  await expect(page.locator('#drawer')).toHaveAttribute('aria-hidden', 'true');
});

test('saanjha session 4 / export produces a valid .naklilist payload', async ({ page }) => {
  await page.goto('/pages/saanjha.html');

  // Drive export via the test hook so we can introspect the doc directly
  // (the user-facing path is a <a download> click; the data shape is the
  // contract that matters).
  const doc = await page.evaluate(() => window.__SAANJHA__.exportList());
  expect(doc.format).toBe('naklilist/1.0');
  expect(doc.exported_at).toMatch(/^\d{4}-\d{2}-\d{2}T/);
  expect(doc.list).toBeTruthy();
  expect(doc.list.namespace).toBe('saanjha');
  expect(doc.list.events.length).toBeGreaterThan(10);
  expect(doc.list.metadata.name).toBe('Groceries');

  // Every event has the wire-spec fields.
  for (const ev of doc.list.events) {
    expect(ev).toHaveProperty('event_id');
    expect(ev).toHaveProperty('kind');
    expect(ev).toHaveProperty('payload');
    expect(ev).toHaveProperty('appended_by_principal');
  }
});

test('saanjha session 4 / focus + a11y essentials present', async ({ page }) => {
  await page.goto('/pages/saanjha.html');

  // Skip-link is in DOM (visually-hidden until focused).
  const skip = page.locator('a.skip');
  await expect(skip).toHaveAttribute('href', '#items');

  // Every interactive control that's visible has an accessible name (via
  // aria-label, aria-labelledby, an associated <label>, visible text,
  // placeholder, or title). Controls inside aria-hidden subtrees (the
  // setup modal + drawer when closed) don't count toward this audit.
  const missing = await page.evaluate(() => {
    const out = [];
    for (const el of document.querySelectorAll('button, [role="button"], a, input')) {
      if (el.closest('[hidden]')) continue;
      if (el.closest('[aria-hidden="true"]')) continue;
      const associatedLabel = el.id ? document.querySelector(`label[for="${el.id}"]`) : null;
      const labelled = el.getAttribute('aria-label')
        || el.getAttribute('aria-labelledby')
        || (associatedLabel && associatedLabel.textContent.trim())
        || (el.textContent ?? '').trim()
        || el.getAttribute('placeholder')
        || el.getAttribute('title');
      if (!labelled) out.push(el.outerHTML.slice(0, 120));
    }
    return out;
  });
  expect(missing, `Unlabelled controls:\n${missing.join('\n')}`).toEqual([]);

  // Filter chips are toggled exclusively via aria-pressed.
  await page.click('button[data-filter="open"]');
  await expect(page.locator('button[data-filter="open"]')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('button[data-filter="all"]')).toHaveAttribute('aria-pressed', 'false');
});
