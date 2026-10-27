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
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')\n' + (e.stack || '')));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = s => console.log('STEP', s);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    await page.goto(BASE + '/security/modsec');
    const main = (await page.textContent('main')).replace(/\s+/g, ' ');
    if (!/Nginx connector (working|not installed)|Connector installed, not usable/.test(main)) { throw new Error('no connector state: ' + main.slice(0, 400)); }
    const tabs = await page.$$eval('.nim-subtabs [role=tab]', b => b.map(x => x.textContent.replace(/\s*\d+\s*$/, '').trim()));
    if (JSON.stringify(tabs) !== JSON.stringify(['Websites', 'Recent events', 'Vendors', 'Custom rules', 'Installation'])) { throw new Error('tabs: ' + JSON.stringify(tabs)); }
    // Server-wide settings in a window.
    await page.click('button[data-bs-target="#ms-settings"]');
    await page.waitForSelector('#ms-settings.show');
    if (!(await page.textContent('#ms-settings')).includes('Detection only (do not block yet)')) { throw new Error('no detection-only switch'); }
    await page.click('#ms-settings .btn-close');
    await page.waitForSelector('#ms-settings', { state: 'hidden' });
    step('tabs in order; connector state; settings window with the detection-only switch');
    // Custom rules: add one in a window, edit it, delete it.
    await page.click('[data-bs-target="#custom"]');
    await page.click('button[data-bs-target="#ms-rule-new"]');
    await page.waitForSelector('#ms-rule-new.show');
    await page.fill('#ms-rule-new-c', 'block test path');
    await page.fill('#ms-rule-new-r', 'SecRule REQUEST_URI "@beginsWith /e2e-test" "id:10077,phase:1,deny,status:403,log,msg:\'e2e\'"');
    await Promise.all([page.waitForLoadState('networkidle'), page.click('#ms-rule-new button.btn-primary')]);
    await page.waitForSelector('#custom.active td:has-text("10077")', { timeout: 15000 });
    if (!(await page.textContent('#custom')).includes('block test path')) { throw new Error('the rule description is not listed'); }
    await page.click('#custom tr:has-text("10077") button:has-text("Edit")');
    const editId = await (await page.waitForSelector('.modal.show')).evaluate(m => m.id);
    await page.fill('#' + editId + '-c', 'block test path (edited)');
    await Promise.all([page.waitForLoadState('networkidle'), page.click('#' + editId + ' button.btn-primary')]);
    await page.waitForSelector('#custom.active td:has-text("(edited)")', { timeout: 15000 });
    page.once('dialog', d => d.accept());
    await page.click('#custom tr:has-text("10077") button:has-text("Delete")');
    const confirmBtn = await page.waitForSelector('.modal.show [data-nim-confirm-ok], .modal.show .btn-danger', { timeout: 5000 }).catch(() => null);
    if (confirmBtn) { await Promise.all([page.waitForLoadState('networkidle'), confirmBtn.click()]); }
    await page.waitForFunction(() => !document.querySelector('#custom td') || !document.querySelector('#custom').textContent.includes('10077'), null, { timeout: 15000 });
    step('custom rule added in a window, edited and deleted');
    await page.click('[data-bs-target="#installs"]');
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
