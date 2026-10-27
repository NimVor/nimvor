// PHP Manager: the versions and extensions of the server, a review of what
// changes, apply (one job), the profile kept and exported. (Dev instance:
// system commands are simulated.)
const { chromium } = require('playwright');
const fs = require('fs');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ acceptDownloads: true, viewport: { width: 1300, height: 900 } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  const nav = click => Promise.all([page.waitForNavigation(), click()]);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await nav(() => page.click('button[type=submit]'));
    await page.goto(BASE + '/php');
    await nav(() => page.click('a[href="/php/manager"]'));
    const checked = await page.$$eval('tr[data-version] input:checked', l => l.map(i => i.value));
    if (checked.length === 0) { throw new Error('no installed version ticked'); }
    await page.click('[data-bs-target="#pm-extensions"]');
    await page.waitForSelector('#pm-extensions.active');
    await page.check('#px-redis');
    await nav(() => page.click('button:has-text("Review changes")'));
    const review = (await page.textContent('main')).replace(/\s+/g, ' ');
    if (!/To install \(\d+\)/.test(review) || !review.includes('redis')) { throw new Error('review: ' + review.slice(0, 300)); }
    await nav(() => page.click('form button:has-text("Apply")'));
    if (!(await page.textContent('main')).includes('Applying the PHP profile')) { throw new Error('not applied'); }
    if (await page.waitForSelector('#nim-job.show', { timeout: 3000 }).catch(() => null)) {
      await page.click('#nim-job .btn-close');
      await page.waitForSelector('#nim-job', { state: 'hidden' });
    }
    const [dl] = await Promise.all([page.waitForEvent('download'), page.click('a:has-text("Export profile")')]);
    const prof = JSON.parse(fs.readFileSync(await dl.path(), 'utf8'));
    if (prof.format !== 'nimvor-php-profile' || !prof.extensions.includes('redis')) { throw new Error('export: ' + JSON.stringify(prof)); }
    await page.setViewportSize({ width: 390, height: 844 });
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (over > 1) { throw new Error('horizontal scroll on a phone: ' + over + 'px'); }
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK php manager');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/phpmanager-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
