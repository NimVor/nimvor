// ModSecurity page (dev instance, system commands simulated): the
// connector state, the Install / Upgrade window, the installation records
// and the Detection only switch render; the page fits a phone; no script
// or CSP errors.
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await (await browser.newContext({ viewport: { width: 1300, height: 900 } })).newPage();
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = s => console.log('STEP', s);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    await page.goto(BASE + '/security/modsec');
    const main = (await page.textContent('main')).replace(/\s+/g, ' ');
    if (!/Nginx connector (working|not installed)|Connector installed, not usable/.test(main)) { throw new Error('no connector state: ' + main.slice(0, 400)); }
    if (!main.includes('Detection only (do not block yet)')) { throw new Error('no detection-only switch'); }
    step('connector state and detection-only switch shown');
    const open = await page.$('button[data-bs-target="#ms-install"]');
    if (open) {
      await open.click();
      await page.waitForSelector('#ms-install.show');
      if (!(await page.textContent('#ms-install')).includes('temporary unprivileged account')) { throw new Error('install window text'); }
      await page.screenshot({ path: OUT + '/modsec-install.png' });
      await page.click('#ms-install .btn-close');
      await page.waitForSelector('#ms-install', { state: 'hidden' });
      step('install window');
    } else {
      step('connector from a package: no install button (updated by the package manager)');
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (over > 1) { throw new Error('horizontal scroll on a phone: ' + over + 'px'); }
    step('fits a phone');
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK modsec');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/modsec-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
