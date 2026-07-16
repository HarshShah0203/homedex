import { expect, test, type Page } from '@playwright/test';

async function expectNoHorizontalOverflow(page: Page) {
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
}

test('renders every approved register and both route evidence outcomes', async ({ page }) => {
  const screens = [
    ['/', 'Everything, in its place.'],
    ['/hosts', 'Where the address book lives.'],
    ['/routes', 'photos.lab.example'],
    ['/routes/old.lab.example', 'old.lab.example'],
    ['/ports', 'Know what is already spoken for.'],
    ['/expiry', 'What needs a date, gets a date.'],
    ['/changes', 'What changed, without the alarm theatre.'],
    ['/copy-my-lab', 'A safe, exact copy of the index.'],
    ['/sources', 'The index starts with declared access.'],
    ['/setup', 'See exactly what Homedex reads.']
  ] as const;

  for (const [path, heading] of screens) {
    await page.goto(path);
    await expect(page.getByRole('heading', { name: heading, level: 1 })).toBeVisible();
    await expectNoHorizontalOverflow(page);
  }

  await page.goto('/routes/old.lab.example');
  await expect(page.getByText('Broken · no match')).toBeVisible();
  await expect(page.getByText('TLS FACT')).toBeVisible();
  await page.goto('/routes/photos.lab.example');
  await expect(page.getByText('Resolved · high confidence')).toBeVisible();
  await expect(page.getByText('JOIN RESULT')).toBeVisible();

  await page.goto('/copy-my-lab');
  await expect(page.getByText('Mandatory safety receipt')).toBeVisible();
  await expect(page.getByText('NEVER INGESTED')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Homedex lab context', level: 2 })).toBeVisible();
  await expect(page.getByText('SCHEMA HOMEDEX.INVENTORY.V1')).toBeVisible();
  await expect(page.locator('.receipt-line').filter({ hasText: 'SHA-256' }).locator('span').last()).toHaveText(/^[A-F0-9]{4} [A-F0-9]{4} [A-F0-9]{4} … [A-F0-9]{4}$/);
});

test('opens grouped cmd-K search with explicit match reasons and mobile fullscreen behavior', async ({ page }) => {
  await page.goto('/');
  await page.keyboard.press('Control+K');
  const dialog = page.getByRole('dialog', { name: 'Search every record' });
  await expect(dialog).toBeVisible();
  await expect(page.getByText('5 results')).toBeVisible();
  for (const reason of [
    'Name starts with ‘immich’',
    'Stack equals ‘immich’',
    'Connected to immich-server',
    'Declared by immich-server',
    'Hosts 2 matching records'
  ]) await expect(page.getByText(reason, { exact: false })).toBeVisible();

  await page.keyboard.press('Escape');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.keyboard.press('Control+K');
  const rect = await dialog.boundingBox();
  expect(rect).not.toBeNull();
  expect(rect!.width).toBeGreaterThanOrEqual(389);
  expect(rect!.height).toBeGreaterThanOrEqual(843);
  await expectNoHorizontalOverflow(page);
});

test('adapts the invoked host inspector from drawer to bottom sheet to fullscreen sheet', async ({ page }) => {
  const modes = [
    { width: 1440, height: 900, mode: 'drawer' },
    { width: 768, height: 900, mode: 'bottom-sheet' },
    { width: 390, height: 844, mode: 'fullscreen' }
  ] as const;

  for (const viewport of modes) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height });
    await page.goto('/hosts');
    await page.getByRole('button', { name: 'Open connected records' }).first().click();
    await expect(page).toHaveURL(/\/hosts\/2$/);
    const inspector = page.getByRole('dialog', { name: 'Connected records for nas-01' });
    await expect(inspector).toBeVisible();
    const rect = await inspector.boundingBox();
    expect(rect).not.toBeNull();
    if (viewport.mode === 'drawer') {
      expect(rect!.width).toBe(390);
      expect(rect!.x + rect!.width).toBe(viewport.width);
      expect(rect!.height).toBe(viewport.height);
    } else if (viewport.mode === 'bottom-sheet') {
      expect(rect!.width).toBe(viewport.width);
      expect(rect!.height).toBeGreaterThan(500);
      expect(rect!.height).toBeLessThan(viewport.height);
      expect(rect!.y + rect!.height).toBe(viewport.height);
    } else {
      expect(rect!.x).toBe(0);
      expect(rect!.y).toBe(0);
      expect(rect!.width).toBe(viewport.width);
      expect(rect!.height).toBe(viewport.height);
    }
    await expectNoHorizontalOverflow(page);
    await page.getByRole('button', { name: 'Close inspector' }).click();
    await expect(page).toHaveURL(/\/hosts$/);
  }
});

test('supports light theme, keyboard focus, reduced motion, and overflow-free responsive registers', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('button', { name: 'Switch to light theme' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

  await page.keyboard.press('Tab');
  const focus = await page.evaluate(() => {
    const active = document.activeElement as HTMLElement;
    const style = getComputedStyle(active);
    return { tag: active.tagName, outline: style.outlineStyle, width: style.outlineWidth };
  });
  expect(focus.tag).not.toBe('BODY');
  expect(focus.outline).toBe('solid');
  expect(focus.width).toBe('2px');

  const hasReducedMotionRule = await page.evaluate(() => [...document.styleSheets].some((sheet) => {
    try {
      return [...sheet.cssRules].some((rule) => rule.cssText.includes('prefers-reduced-motion'));
    } catch {
      return false;
    }
  }));
  expect(hasReducedMotionRule).toBe(true);

  for (const viewport of [{ width: 390, height: 844 }, { width: 768, height: 900 }, { width: 1440, height: 900 }]) {
    await page.setViewportSize(viewport);
    for (const path of ['/', '/routes/old.lab.example', '/ports', '/copy-my-lab', '/sources']) {
      await page.goto(path);
      await expect(page.locator('.page, .setup-page').first()).toBeVisible();
      await expectNoHorizontalOverflow(page);
    }
  }
});
