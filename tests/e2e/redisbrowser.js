// End-to-end test of the Redis key browser (needs E2E_REDIS=1: run.sh
// starts a Redis on port 6390).
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';
if (process.env.E2E_REDIS !== '1') { console.log('redis browser: skipped (no redis-server)'); process.exit(0); }
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  await page.goto(BASE + '/login');
  await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
  await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

  await page.goto(BASE + '/redis/browser');
  await page.waitForSelector('[data-rb-count]:not(:empty)');
  step('browser loaded: ' + await page.textContent('[data-rb-count]'));

  // Create a string key with a multi-line value.
  await page.click('[data-rb-cmd="newkey"]');
  const inputs = page.locator('#rb-dialog .modal-body').locator('input, select, textarea');
  await inputs.nth(0).fill('e2e:greeting');
  await inputs.nth(3).fill('hello\nworld');
  await page.click('[data-rb-dialog-ok]');
  await page.waitForSelector('.wcp-rb-text');
  const v = await page.inputValue('.wcp-rb-text');
  if (v !== 'hello\nworld') { throw new Error('value = ' + JSON.stringify(v)); }
  step('string key created and shown');

  // Edit and save the value.
  await page.fill('.wcp-rb-text', '{"a":1}');
  await page.click('text=Save value');
  await page.waitForTimeout(500);
  await page.click('text=Format JSON');
  step('saved; formatted = ' + JSON.stringify(await page.inputValue('.wcp-rb-text')));

  // Hash key with fields.
  await page.click('[data-rb-cmd="newkey"]');
  await inputs.nth(0).fill('e2e:user');
  await inputs.nth(1).selectOption('hash');
  await inputs.nth(2).fill('name');
  await inputs.nth(3).fill('Ada');
  await page.click('[data-rb-dialog-ok]');
  await page.waitForSelector('.wcp-rb-table');
  await page.click('text=Add field');
  const fi = page.locator('#rb-dialog .modal-body').locator('input, textarea');
  await fi.nth(0).fill('email'); await fi.nth(1).fill('ada@example.com');
  await page.click('[data-rb-dialog-ok]');
  await page.waitForFunction(() => document.querySelectorAll('.wcp-rb-table tbody tr').length === 2);
  step('hash has 2 fields');

  // Scan with a pattern, set a TTL, rename, delete.
  await page.fill('[data-rb-pattern]', 'e2e:*');
  await page.click('[data-rb-search] button[type=submit]');
  await page.waitForFunction(() => document.querySelectorAll('.wcp-rb-key').length === 2);
  step('pattern scan: 2 keys');
  await page.click('.wcp-rb-key:has-text("e2e:greeting")');
  await page.waitForSelector('.wcp-rb-text');
  await page.click('button:has-text("TTL")');
  await page.fill('#rb-dialog input', '600');
  await page.click('[data-rb-dialog-ok]');
  await page.waitForSelector('.wcp-rb-head:has-text("10 min")');
  step('TTL set');
  await page.click('button:has-text("Rename")');
  await page.fill('#rb-dialog input', 'e2e:hello');
  await page.click('[data-rb-dialog-ok]');
  await page.waitForSelector('.wcp-rb-key:has-text("e2e:hello")');
  step('renamed');
  await page.screenshot({ path: OUT + '/redis-browser.png' });
  await page.click('.wcp-rb-head button:has-text("Delete")');
  await page.click('[data-rb-dialog-ok]');
  await page.waitForFunction(() => document.querySelectorAll('.wcp-rb-key').length === 1);
  step('deleted');

  await browser.close();
  if (errors.length) { console.error('JavaScript errors:', errors); process.exit(1); }
  console.log('redis browser: ok');
})().catch(e => { console.error('FAILED', e.message); process.exit(1); });
