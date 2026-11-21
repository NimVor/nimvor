// End-to-end test of the dashboard's widgets and service widget:
// - the dashboard shows the Disk and Backups widgets and no longer has the tools grid
// - the Services widget has start/stop and restart buttons that work
//   without leaving the page (one row per service)
// No script or CSP errors.
const path = require('path');
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')\n' + (e.stack || '')));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    // A layout left over from an earlier run is put back to the default first.
    if (await page.locator('button:has-text("Restore default")').count()) {
      await page.click('button:has-text("Restore default")');
      await page.waitForSelector('#nim-confirm.show [data-nim-confirm-ok]', { timeout: 5000 });
      await Promise.all([page.waitForNavigation(), page.click('#nim-confirm [data-nim-confirm-ok]')]);
    }

    if (await page.locator('.nim-tools, .nim-tool-card').count() !== 0) { throw new Error('the tools grid is still on the dashboard'); }
    for (const w of ['disk', 'backups']) {
      if (await page.locator('[data-widget="' + w + '"]').count() === 0) { throw new Error('widget ' + w + ' missing'); }
    }
    step('widgets: disk and backups, no tools grid');

    // A measured widget is loaded after the page and ends in a real state, never stuck on "Loading".
    await page.waitForSelector('[data-widget="top_processes"]:not([data-state="loading"])', { timeout: 30000 });
    const tpState = await page.getAttribute('[data-widget="top_processes"]', 'data-state');
    if (!['ok', 'empty', 'unavailable', 'error'].includes(tpState)) { throw new Error('top processes widget state: ' + tpState); }
    step('top processes widget loaded (' + tpState + ')');

    // Customize: hide the Disk widget, then restore the default layout.
    await page.click('button:has-text("Customize")');
    await page.waitForSelector('#nim-customize.show', { timeout: 5000 });
    const firstId = await page.$eval('#nim-customize [data-customize-row] input[name="order[]"]', i => i.value);
    await page.click('#nim-customize [data-customize-row]:first-child [data-customize-move="1"]');
    const afterMove = await page.$eval('#nim-customize [data-customize-row] input[name="order[]"]', i => i.value);
    if (afterMove === firstId) { throw new Error('the row did not move'); }
    await page.uncheck('#cw-disk');
    await Promise.all([page.waitForNavigation(), page.click('#nim-customize button[type=submit]:has-text("Save")')]);
    if (await page.locator('[data-widget-host="disk"]').count() !== 0) { throw new Error('a hidden widget is still shown'); }
    step('customize: moved a widget and hid Disk');
    await page.click('button:has-text("Restore default")');
    await page.waitForSelector('#nim-confirm.show [data-nim-confirm-ok]', { timeout: 5000 });
    await Promise.all([page.waitForNavigation(), page.click('#nim-confirm [data-nim-confirm-ok]')]);
    await page.waitForSelector('[data-widget-host="disk"]', { timeout: 10000 });
    step('customize: default layout restored');

    await page.waitForSelector('[data-services] > li:not(:has(.placeholder))', { timeout: 20000 });
    if (await page.locator('[data-services] .nim-service-btn').count() === 0) {
      // The development instance simulates systemd: no services to list.
      step('services widget: no services here (simulated system)');
      if (errors.length) { throw new Error(errors.join('\n')); }
      console.log('OK dashboard tools');
      return;
    }
    const units = await page.$$eval('[data-services] > li', lis => lis.map(li => li.firstChild.textContent.trim()));
    if (new Set(units).size !== units.length) { throw new Error('a service is listed twice: ' + units.join(', ')); }
    const url = page.url();
    const btn = page.locator('[data-services] button[aria-label^="Restart"]').first();
    const label = await btn.getAttribute('aria-label');
    await btn.click();
    await page.waitForSelector('.toast, [role=alert], [role=status]', { timeout: 15000 });
    if (page.url() !== url) { throw new Error('the restart left the page'); }
    await page.screenshot({ path: path.join(OUT, 'dashboard-services.png') });
    step('services widget: ' + label + ' without leaving the page');

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK dashboard tools');
  } catch (e) {
    await page.screenshot({ path: path.join(OUT, 'dashboardtools-failure.png'), fullPage: true }).catch(() => {});
    console.error(e.message);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
