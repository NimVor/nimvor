// End-to-end test of the admin and reseller shell:
// - the dashboard's welcome strip, quick actions and "Tools & settings" grid
// - Ctrl+K opens the global search; typing filters it; Enter opens the match
// - a reseller gets their own dashboard (customers, websites, plan), no
//   server details and no server tools in the search
// - phones: no horizontal scrolling. No script or CSP errors.
const path = require('path');
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')\n' + (e.stack || '')));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  const login = async (u, pw) => {
    await page.goto(BASE + '/login'); await page.fill('#username', u); await page.fill('#password', pw);
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
  };
  const search = async (text) => {
    await page.keyboard.press('Control+k');
    await page.waitForSelector('#wcp-palette.show');
    await page.waitForFunction(() => document.activeElement && document.activeElement.matches('[data-wcp-palette-input]'));
    await page.keyboard.type(text);
  };
  try {
    await login('admin', 'dev-password-change-me');
    const main = await page.textContent('main');
    for (const s of ['Tools & settings', 'Create website', 'System accounts', 'Audit log']) {
      if (!main.includes(s)) { throw new Error('dashboard misses ' + s); }
    }
    await page.screenshot({ path: path.join(OUT, 'nav-dashboard.png'), fullPage: true });
    step('dashboard: welcome strip, quick actions, tools grid');

    await search('backup');
    const first = (await page.textContent('[data-wcp-palette-item].is-first')).trim();
    if (!first.startsWith('Backups')) { throw new Error('first match: ' + first); }
    await Promise.all([page.waitForNavigation(), page.keyboard.press('Enter')]);
    if (!page.url().endsWith('/backups')) { throw new Error('Enter opened ' + page.url()); }
    await search('zzzz-nothing');
    if (!(await page.isVisible('[data-wcp-palette-empty]'))) { throw new Error('no "Nothing matches"'); }
    await page.keyboard.press('Escape');
    step('Ctrl+K search filters and Enter opens the first match');

    // A reseller.
    const stamp = Date.now().toString(36);
    await page.goto(BASE + '/users/new');
    await page.fill('[name=username]', 'res' + stamp); await page.fill('[name=email]', 'res' + stamp + '@example.test');
    await page.fill('[name=password]', 'Reseller-pass-' + stamp); await page.fill('[name=password_confirmation]', 'Reseller-pass-' + stamp);
    const rv = await page.$eval('[name=role_id]', s => [...s.options].find(o => /Reseller/.test(o.textContent)).value);
    await page.selectOption('[name=role_id]', rv);
    await Promise.all([page.waitForNavigation(), page.click('main form[action="/users"] button[type=submit]')]);
    await ctx.clearCookies();
    await login('res' + stamp, 'Reseller-pass-' + stamp);
    const rmain = await page.textContent('main');
    for (const s of ['Reseller account', 'Plan use', 'No customers yet', 'New account']) {
      if (!rmain.includes(s)) { throw new Error('reseller dashboard misses ' + s); }
    }
    for (const s of ['Hostname', 'Kernel', 'Load average']) {
      if (rmain.includes(s)) { throw new Error('reseller sees server detail ' + s); }
    }
    await search('packages');
    if (await page.$('[data-wcp-palette-item]:not(.d-none):has-text("Packages")')) { throw new Error('server tools offered to a reseller'); }
    await page.keyboard.press('Escape');
    await page.screenshot({ path: path.join(OUT, 'nav-reseller.png'), fullPage: true });
    step('reseller dashboard without server details or tools');

    await page.setViewportSize({ width: 390, height: 844 });
    for (const u of ['/', '/websites']) {
      await page.goto(BASE + u);
      if (await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)) { throw new Error('horizontal scroll on ' + u); }
    }
    step('phones: no horizontal scroll');
    if (errors.length) { throw new Error('browser errors:\n' + errors.join('\n')); }
    console.log('OK navigation');
  } catch (e) {
    await page.screenshot({ path: path.join(OUT, 'navigation-failure.png'), fullPage: true }).catch(() => {});
    console.error('FAIL', e.message);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
