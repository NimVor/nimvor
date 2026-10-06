// End-to-end test of the dashboard's tools and service widget:
// - "Tools & settings" lists each Security page as its own card
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

    const security = await page.evaluate(() => {
      const head = [...document.querySelectorAll('.wcp-tools-group')].find(h => h.textContent.trim() === 'Security');
      return head ? [...head.nextElementSibling.querySelectorAll('.wcp-tool-card .fw-semibold')].map(e => e.textContent.trim()) : [];
    });
    for (const want of ['Security overview', 'Firewall', 'SSH', 'ModSecurity', 'SMTP Restrictions', 'Outgoing Connections', 'Traceroute Enable/Disable']) {
      if (!security.includes(want)) { throw new Error('Security cards: ' + want + ' missing in ' + JSON.stringify(security)); }
    }
    step('tools: one card per Security page (' + security.length + ')');

    await page.waitForSelector('[data-services] > li:not(:has(.placeholder))', { timeout: 20000 });
    if (await page.locator('[data-services] .wcp-service-btn').count() === 0) {
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
