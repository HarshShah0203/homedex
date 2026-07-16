import { createHash } from 'node:crypto';
import { expect, test } from '@playwright/test';

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  const kilobytes = bytes / 1024;
  return `${Number.isInteger(kilobytes) ? kilobytes : kilobytes.toFixed(1)} KB`;
}

test('covers the highest-value seeded production workflows', async ({ page }) => {
  const setupResponse = await page.request.post('/api/setup', { data: { password: 'production-e2e-password' } });
  expect(setupResponse.status()).toBe(200);
  const { csrf } = await setupResponse.json() as { csrf: string };
  expect(csrf).toBeTruthy();
  await page.addInitScript((token) => sessionStorage.setItem('homedex-csrf', token), csrf);

  const summaryResponse = await page.request.get('/api/summary');
  expect(summaryResponse.ok()).toBe(true);
  const summary = await summaryResponse.json();
  expect(summary.counts).toMatchObject({ services: 12, hosts: 3, routes: 10, routes_broken: 1 });
  const routesResponse = await page.request.get('/api/routes?limit=500');
  expect(routesResponse.ok()).toBe(true);
  const routes = (await routesResponse.json()).items as Array<{ id: number; domain: string; path_prefix: string; status: string }>;
  expect(routes).toEqual(expect.arrayContaining([
    expect.objectContaining({ domain: 'photos.lab.example', status: 'ok' }),
    expect.objectContaining({ domain: 'old.lab.example', status: 'broken' })
  ]));
  const brokenRoute = routes.find((route) => route.domain === 'old.lab.example')!;
  // This seed has no duplicate domain/path pair; keep that explicit so a future
  // fixture adding one must extend this gate to assert route-ID navigation.
  expect(new Set(routes.map((route) => `${route.domain}\n${route.path_prefix}`)).size).toBe(routes.length);

  const apiFailures: string[] = [];
  page.on('response', (response) => {
    if (response.url().includes('/api/') && response.status() >= 400) {
      apiFailures.push(`${response.status()} ${response.url()}`);
    }
  });

  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Everything, in its place.', level: 1 })).toBeVisible();
  await expect(page.getByText('12 service records across 3 hosts.')).toBeVisible();

  await page.goto(`/routes/${brokenRoute.id}`);
  await expect(page.getByRole('heading', { name: 'old.lab.example', level: 1 })).toBeVisible();
  await expect(page.getByText('Broken · no match')).toBeVisible();

  const contextResponse = await page.request.get('/api/export/context?include_private=false');
  expect(contextResponse.ok()).toBe(true);
  const contextBody = await contextResponse.body();
  const contextMarkdown = contextBody.toString('utf8');
  const contextHash = createHash('sha256').update(contextBody).digest('hex').toUpperCase();
  const shortHash = `${contextHash.slice(0, 12).match(/.{1,4}/g)!.join(' ')} … ${contextHash.slice(-4)}`;

  await page.goto('/copy-my-lab');
  await expect(page.getByText('Mandatory safety receipt')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Homedex lab context', level: 2 })).toBeVisible();
  await expect(page.locator('.preview-head')).toContainText(`HOMEDEX-CONTEXT.MD · ${formatBytes(contextBody.byteLength)}`);
  await expect.poll(() => page.locator('.markdown-paper pre').textContent()).toBe(contextMarkdown);
  await expect(page.locator('.receipt-line').filter({ hasText: 'SHA-256' }).locator('span').last()).toHaveText(shortHash);
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.getByRole('button', { name: 'Copy exact Markdown' }).click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(contextMarkdown);

  const hosts = (await (await page.request.get('/api/hosts?limit=500')).json()).items as Array<{ id: number; name: string }>;
  const hostID = (name: string) => hosts.find((host) => host.name === name)!.id;
  const gatewayPort = await (await page.request.get(`/api/ports/next-free?host_id=${hostID('gateway')}&start=80&end=81&protocol=tcp`)).json();
  const nasPort = await (await page.request.get(`/api/ports/next-free?host_id=${hostID('nas-01')}&start=80&end=81&protocol=tcp`)).json();
  expect(gatewayPort).toMatchObject({ host_id: hostID('gateway'), port: 81, protocol: 'tcp' });
  expect(nasPort).toMatchObject({ host_id: hostID('nas-01'), port: 80, protocol: 'tcp' });
  await page.goto('/ports');
  const lookupHost = hosts[1];
  const lookupResponse = page.waitForResponse((response) =>
    response.request().method() === 'GET' && response.url().includes(`/api/ports/next-free?host_id=${lookupHost.id}`)
  );
  await page.getByLabel('Select host for port lookup').selectOption(String(lookupHost.id));
  expect((await lookupResponse).ok()).toBe(true);
  await expect(page.locator('[data-component-id="next-free-port"]')).toContainText(`Checked for ${lookupHost.name}`);

  const changes = (await (await page.request.get('/api/changes?limit=500')).json()).items as Array<{ id: number; summary: string; seen: boolean }>;
  const change = changes.find((item) => !item.seen)!;
  await page.goto('/changes');
  const reviewedButton = page.getByRole('button', { name: `Mark ${change.summary} reviewed` });
  await expect(reviewedButton).toHaveAttribute('aria-pressed', 'false');
  const reviewResponse = page.waitForResponse((response) =>
    response.request().method() === 'PATCH' && response.url().endsWith(`/api/changes/${change.id}`)
  );
  await reviewedButton.click();
  expect((await reviewResponse).ok()).toBe(true);
  await expect(reviewedButton).toHaveAttribute('aria-pressed', 'true');
  const persistedChanges = (await (await page.request.get('/api/changes?limit=500')).json()).items as Array<{ id: number; seen: boolean }>;
  expect(persistedChanges.find((item) => item.id === change.id)?.seen).toBe(true);
  await page.reload();
  await expect(reviewedButton).toHaveAttribute('aria-pressed', 'true');

  await page.goto('/setup');
  await expect(page.getByRole('heading', { name: 'See exactly what Homedex reads.', level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Docker metadata, read only', level: 2 })).toBeVisible();
  await expect(page.getByLabel('Read-only endpoint')).toHaveValue('unix:///var/run/docker.sock');
  await expect(page.getByRole('button', { name: 'Test connection' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save and run first scan' })).toBeDisabled();
  expect(apiFailures).toEqual([]);

  const shareResponse = await page.request.post('/api/share', {
    headers: { 'X-Homedex-CSRF': csrf }, data: { name: 'Production E2E share', expires_in_hours: 1 }
  });
  expect(shareResponse.status()).toBe(201);
  const share = await shareResponse.json() as { share_url: string };
  await page.context().clearCookies();
  await page.goto(share.share_url);
  await expect(page.getByRole('status')).toContainText('Read-only shared inventory');
  await expect(page.getByText('12 service records across 3 hosts.')).toBeVisible();
  await page.goto('/sources');
  await expect(page.getByRole('status')).toContainText('Read-only shared inventory');
  await expect(page.getByRole('button', { name: 'Add source' })).toHaveCount(0);
  await expect(page.getByText('SOURCES HIDDEN IN SHARED VIEW')).toBeVisible();
  await page.goto('/changes');
  await expect(page.getByRole('status')).toContainText('Read-only shared inventory');
  await expect(page.getByRole('button', { name: /^Mark .* reviewed$/ })).toHaveCount(0);
  await expect(page.getByText('READ ONLY').first()).toBeVisible();
  expect((await page.request.get('/api/connectors')).status()).toBe(403);
  const blockedReview = await page.request.patch(`/api/changes/${change.id}`, { data: { seen: false } });
  expect(blockedReview.status()).toBe(403);
  expect(await blockedReview.text()).toContain('share tokens are read-only');
});
