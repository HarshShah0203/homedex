// Captures README screenshots from a running seeded instance (default :7377).
// Usage: node scripts/capture-screenshots.mjs [baseURL] [outDir]
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';

const base = process.argv[2] ?? 'http://127.0.0.1:7377';
const out = process.argv[3] ?? '../docs/screenshots';
mkdirSync(out, { recursive: true });

const shots = [
  { path: '/', wait: '.service-row', file: 'services.png' },
  { path: '/routes', wait: '.register-row', file: 'routes.png' },
  { path: '/expiry', wait: '.register-row', file: 'expiry.png' },
  { path: '/changes', wait: '.register-row', file: 'changes.png' }
];

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 860 }, deviceScaleFactor: 2 });
for (const shot of shots) {
  await page.goto(base + shot.path, { waitUntil: 'networkidle' });
  await page.waitForSelector(shot.wait, { timeout: 10000 });
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${out}/${shot.file}` });
  console.log(`captured ${shot.file}`);
}
await browser.close();
