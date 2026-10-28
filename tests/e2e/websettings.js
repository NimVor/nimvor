// Basic WebHost Settings and Tweak Settings: tabs, search across tabs, a
// change marked as changed from the default, a reset, and the defaults
// for new accounts used by the account form. (Dev instance.)
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1300, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('console', m => { if (m.type() === 'error' && !/status of 4\d\d/.test(m.text())) { errors.push('console: ' + m.text()); } });
  const nav = click => Promise.all([page.waitForNavigation(), click()]);
  const step = s => console.log('STEP', s);
  const visible = () => page.$$eval('[data-setting]', l => l.filter(x => x.offsetParent !== null).map(x => x.getAttribute('data-setting')));
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await nav(() => page.click('button[type=submit]'));
    await page.goto(BASE + '/settings');
    await nav(() => page.click('a[href="/settings/tweak"]'));

    // Search finds settings on every tab at once.
    await page.fill('[data-nim-tweak-search]', 'timeout');
    const found = await visible();
    if (!found.includes('session_idle_minutes') || !found.includes('terminal_idle_minutes') || found.length > 4) { throw new Error('search: ' + found); }
    await page.fill('[data-nim-tweak-search]', 'zzzz-nothing');
    if (!(await page.isVisible('[data-nim-tweak-empty]'))) { throw new Error('no "no match" message'); }
    await page.fill('[data-nim-tweak-search]', '');
    step('search across tabs');

    // Change a setting: marked as changed; then reset it.
    await page.click('#tw-tab-general');
    await page.fill('#tw-dashboard_refresh_seconds', '25');
    await nav(() => page.click('#general button.btn-primary'));
    if (!(await page.textContent('main')).includes('Saved: Dashboard refresh interval')) { throw new Error('not saved'); }
    if (await page.getAttribute('[data-setting="dashboard_refresh_seconds"]', 'data-changed') !== '1') { throw new Error('not marked changed'); }
    await page.check('[data-nim-tweak-changed]');
    if (!(await visible()).includes('dashboard_refresh_seconds')) { throw new Error('changed filter'); }
    await page.uncheck('[data-nim-tweak-changed]');
    step('changed and marked');
    await page.click('#tw-tab-general');
    await page.click('[data-setting="dashboard_refresh_seconds"] button[name="reset"]');
    await page.waitForSelector('#nim-confirm.show');
    await nav(() => page.click('[data-nim-confirm-ok]'));
    if (await page.inputValue('#tw-dashboard_refresh_seconds') !== '10') { throw new Error('not reset'); }
    step('reset to default');

    // Settings with their own page are read-only here.
    await page.click('#tw-tab-hulk');
    if (await page.$('[name="hulk_enabled"]')) { throw new Error('hulk_enabled editable'); }
    step('lock-out settings link to their page');

    // Basic WebHost Settings: defaults for new accounts reach the form.
    await page.goto(BASE + '/settings/basic#accounts');
    await page.waitForSelector('#accounts.active');
    await page.selectOption('#b-account_mail_routing', 'backup');
    await nav(() => page.click('#accounts button.btn-primary'));
    await page.goto(BASE + '/accounts/new');
    if (!(await page.isChecked('input[name="mail_routing"][value="backup"]'))) { throw new Error('account form ignores the default'); }
    await page.goto(BASE + '/settings/basic#accounts');
    await page.waitForSelector('#accounts.active');
    await page.selectOption('#b-account_mail_routing', 'auto');
    await nav(() => page.click('#accounts button.btn-primary'));
    step('new account defaults used');

    await page.goto(BASE + '/settings/basic#nameservers');
    await page.waitForSelector('#nameservers.active');
    await page.setViewportSize({ width: 390, height: 844 });
    for (const url of ['/settings/basic', '/settings/tweak']) {
      await page.goto(BASE + url);
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      if (over > 1) { throw new Error(url + ': horizontal scroll on a phone: ' + over + 'px'); }
    }
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK web host settings');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/websettings-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
