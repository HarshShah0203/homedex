import { expect, test } from '@playwright/test';

test('serves the seeded inventory through the embedded production UI and API', async ({ page, request }) => {
  const summaryResponse = await request.get('/api/summary');
  expect(summaryResponse.ok()).toBe(true);
  const summary = await summaryResponse.json();
  expect(summary.counts).toMatchObject({ services: 12, hosts: 3, routes: 10, routes_broken: 1 });
  const routesResponse = await request.get('/api/routes?limit=500');
  expect(routesResponse.ok()).toBe(true);
  const routes = (await routesResponse.json()).items as Array<{ domain: string; status: string }>;
  expect(routes).toEqual(expect.arrayContaining([
    expect.objectContaining({ domain: 'photos.lab.example', status: 'ok' }),
    expect.objectContaining({ domain: 'old.lab.example', status: 'broken' })
  ]));

  const apiFailures: string[] = [];
  page.on('response', (response) => {
    if (response.url().includes('/api/') && response.status() >= 400) {
      apiFailures.push(`${response.status()} ${response.url()}`);
    }
  });

  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Everything, in its place.', level: 1 })).toBeVisible();
  await expect(page.getByText('12 service records across 3 hosts.')).toBeVisible();

  await page.goto('/routes/old.lab.example');
  await expect(page.getByText('Broken · no match')).toBeVisible();

  await page.goto('/copy-my-lab');
  await expect(page.getByText('Mandatory safety receipt')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Homedex lab context', level: 2 })).toBeVisible();
  expect(apiFailures).toEqual([]);
});
