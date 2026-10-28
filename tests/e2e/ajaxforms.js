// End-to-end test of background form submits and form windows:
// - a website is created and a cron job added in its "Add cron job" window
//   without a full page reload (a marker on window survives), the new row
//   appears and the result is shown
// - a refused save reopens the window with the field error, still without
//   a reload
// - delete asks for confirmation first, then removes the row in place
// - #new-cron in the address opens the window; Back after a background
//   submit loads the previous page
// - the file manager is untouched and pages with their own scripts
//   (profile: security keys) submit forms normally
// No script or CSP errors.
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
  const stamp = Date.now().toString(36);
  const mark = () => page.evaluate(() => { window.__nimMark = 1; });
  const same = async () => (await page.evaluate(() => window.__nimMark)) === 1;
  // A form submitted in the background: wait for the page swap.
  const swap = async (click) => Promise.all([page.waitForNavigation(), click()]);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    await page.goto(BASE + '/websites/new');
    await mark();
    await page.fill('#f-domain', 'ajax' + stamp + '.example.test');
    await swap(() => page.click('main form[action="/websites/new"] button[type=submit]'));
    if (!await same()) { throw new Error('creating a website reloaded the page'); }
    const site = new URL(page.url()).pathname;
    if (!/^\/sites\/\d+$/.test(site)) { throw new Error('not on the new website: ' + page.url()); }
    step('website created in the background: ' + site);

    await page.goto(BASE + site + '/cron');
    await mark();
    await page.click('button[data-bs-target="#new-cron"]');
    await page.waitForSelector('#new-cron.show');
    await page.waitForFunction(() => document.activeElement && document.activeElement.closest('#new-cron'));
    await page.screenshot({ path: OUT + '/ajax-window.png' });
    // The browser would stop an empty field itself; test the server's refusal.
    await page.$eval('#c-cmd', el => el.removeAttribute('required'));
    await page.fill('#c-cmd', '');
    await swap(() => page.click('#new-cron button[type=submit]'));
    if (!await same()) { throw new Error('a refused save reloaded the page'); }
    await page.waitForSelector('#new-cron.show');
    if (!(await page.$('#new-cron .is-invalid'))) { throw new Error('the refused field is not marked'); }
    step('refused save: the window reopens with the error, no reload');

    await page.fill('#c-cmd', 'echo ajax-' + stamp);
    await swap(() => page.click('#new-cron button[type=submit]'));
    if (!await same()) { throw new Error('adding a cron job reloaded the page'); }
    await page.waitForSelector('#new-cron', { state: 'hidden' });
    if (!(await page.textContent('main')).includes('echo ajax-' + stamp)) { throw new Error('new job not listed'); }
    if (await page.locator('.modal-backdrop').count() !== 0) { throw new Error('a window backdrop was left behind'); }
    await page.screenshot({ path: OUT + '/ajax-cron.png' });
    step('cron job added in its window; the list updated in place');

    await page.click(`form[data-nim-confirm="Delete this cron job?"] button`);
    await page.waitForSelector('#nim-confirm.show');
    await swap(() => page.click('#nim-confirm [data-nim-confirm-ok]'));
    if (!await same()) { throw new Error('deleting reloaded the page'); }
    if ((await page.textContent('main')).includes('echo ajax-' + stamp)) { throw new Error('job still listed after delete'); }
    step('delete after confirmation, in place');

    await page.goto(BASE + site + '/cron#new-cron');
    await page.waitForSelector('#new-cron.show');
    await page.keyboard.press('Escape');
    step('#new-cron opens the window');

    await page.goto(BASE + site);
    await page.goto(BASE + site + '/cron');
    await page.click('button[data-bs-target="#new-cron"]');
    await page.waitForSelector('#new-cron.show');
    await page.fill('#c-cmd', 'echo back-' + stamp);
    await swap(() => page.click('#new-cron button[type=submit]'));
    await Promise.all([page.waitForNavigation(), page.goBack()]);
    await page.waitForLoadState('load');
    if (!(await page.textContent('main')).includes('Cron jobs')) { throw new Error('Back did not show the page: ' + page.url()); }
    step('Back after a background submit');

    await page.goto(BASE + '/settings#dns');
    await page.waitForSelector('#pane-dns.show');
    await mark();
    await page.fill('[name=dns_hostmaster]', 'hostmaster@example.test');
    await swap(() => page.click('#pane-dns button[type=submit]'));
    if (!await same()) { throw new Error('saving settings reloaded the page'); }
    await page.waitForSelector('#pane-dns.show');
    if (await page.inputValue('[name=dns_hostmaster]') !== 'hostmaster@example.test') { throw new Error('setting not saved'); }
    step('settings: saved in the background, back on the same tab');

    // A button with its own formaction posts there.
    await page.goto(BASE + '/settings#email');
    const test = page.locator('button[formaction$="/settings/mail/test"]');
    await test.evaluate(b => b.removeAttribute('disabled'));
    const [req] = await Promise.all([page.waitForRequest(r => r.method() === 'POST'), test.click()]);
    if (!req.url().endsWith('/settings/mail/test')) { throw new Error('formaction ignored: ' + req.url()); }
    await page.waitForLoadState('load');
    step('a button\'s formaction is honoured');

    await page.goto(BASE + site + '/files');
    if (await page.$('.nim-content')) { throw new Error('the file manager is now inside the page layout'); }
    await page.goto(BASE + '/profile');
    if (await page.getAttribute('body', 'data-nim-ajax') !== 'off') { throw new Error('a page with its own scripts is not left to normal submits'); }
    step('file manager untouched; pages with their own scripts keep normal submits');
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK ajax forms');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/ajaxforms-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
