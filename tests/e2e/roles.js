// Users and Roles as separate pages; Logs sub-menu; role editor: every
// permission lists the pages it opens, built-in roles are editable
// (Administrator stays full access), own-scope roles cannot take
// server-wide permissions, reset to WhaCP defaults.
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
  const main = async () => (await page.textContent('main')).replace(/\s+/g, ' ');
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    const side = await page.$$eval('.wcp-sidebar a', a => a.map(x => x.textContent.replace(/\s+/g, ' ').trim()));
    for (const want of ['Users', 'Roles', 'Bandwidth Limits', 'All server logs', 'Web server', 'Mail server', 'Security', 'WhaCP', 'Audit log', 'ModSecurity events', 'Failed logins']) {
      if (!side.includes(want)) { throw new Error('sidebar lacks ' + want + ': ' + side.join(' | ')); }
    }
    await page.goto(BASE + '/logs?view=web');
    if ((await page.textContent('h1')).trim() !== 'Web server logs') { throw new Error('logs category title'); }
    await page.goto(BASE + '/security/modsec?tab=events');
    if (!(await page.$('#events.active'))) { throw new Error('?tab=events did not open the tab'); }
    step('sidebar: Users, Roles, Bandwidth Limits, Logs sub-menu (categories and other logs)');

    await page.goto(BASE + '/roles');
    if ((await page.textContent('h1')).trim() !== 'Roles') { throw new Error('roles page'); }
    await page.click('main tr:has-text("Administrator") a');
    await page.waitForSelector('#perm-list');
    if (!(await page.$eval('fieldset', f => f.disabled))) { throw new Error('Administrator must stay read-only'); }
    step('Administrator is read-only');

    await page.goto(BASE + '/roles');
    await page.click('main tr:has-text("Viewer") a:has-text("Edit")');
    await page.waitForSelector('#perm-list');
    const opens = await page.textContent('#perm-list');
    for (const want of ['ModSecurity', 'Processes', 'IP addresses', 'Bandwidth Limits']) {
      if (!opens.includes(want)) { throw new Error('checklist does not say what opens ' + want); }
    }
    await page.fill('.wcp-perm-search', 'modsecurity');
    const shown = await page.$$eval('[data-wcp-filter-item]:not([hidden]) input', i => i.map(x => x.value));
    if (!shown.includes('security.manage') || shown.length > 3) { throw new Error('search: ' + shown); }
    await page.fill('.wcp-perm-search', '');
    await page.check('#perm-audit\\.view');
    await Promise.all([page.waitForLoadState('networkidle'), page.click('button:has-text("Save role")')]);
    await page.waitForSelector('main tr:has-text("Viewer") .badge:has-text("changed")', { timeout: 15000 });
    step('built-in Viewer edited (pages listed per permission, search)');

    await page.click('main tr:has-text("Viewer") a:has-text("Edit")');
    await page.waitForSelector('#perm-list');
    if (!(await page.isChecked('#perm-audit\\.view'))) { throw new Error('change not saved'); }
    page.once('dialog', d => d.accept());
    await page.click('button:has-text("Reset to WhaCP defaults")');
    const ok = await page.waitForSelector('#wcp-confirm.show [data-wcp-confirm-ok]', { timeout: 3000 }).catch(() => null);
    if (ok) { await Promise.all([page.waitForLoadState('networkidle'), ok.click()]); }
    await page.waitForFunction(() => document.querySelector('#perm-audit\\.view') && !document.querySelector('#perm-audit\\.view').checked, null, { timeout: 15000 });
    step('reset to WhaCP defaults');

    await page.goto(BASE + '/roles');
    await page.click('main tr:has-text("Hosting customer") a:has-text("Edit")');
    await page.waitForSelector('#perm-list');
    if (!(await page.$eval('#perm-server\\.manage', e => e.disabled))) { throw new Error('a customer role offers server.manage'); }
    step('own-scope role: server-wide permissions locked');

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (over > 1) { throw new Error('horizontal scroll on a phone: ' + over + 'px'); }
    step('fits a phone');
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK roles');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/roles-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
