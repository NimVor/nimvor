// End-to-end test of account administration (dev instance, system commands
// simulated):
// - a feature list without the WordPress Toolkit, a plan that uses it
// - Create a New Account on that plan; the customer then has no WordPress
//   page (menu entry gone, address refused)
// - suspend (with a reason) and unsuspend (with confirmation)
// - plan change page compares the plans
// - an addon domain of the customer's website becomes an account of its own
//   (window lists what moves, typed confirmation), and the addon is then that
//   account's website
// - the new admin pages fit a phone
// No script or CSP errors.
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1300, height: 900 } });
  const page = await ctx.newPage();
  const errors = [];
  const watch = p => {
    p.on('pageerror', e => errors.push('pageerror: ' + e.message));
    p.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  };
  watch(page);
  const step = s => console.log('STEP', s);
  const stamp = Date.now().toString(36).slice(-6);
  const nav = click => Promise.all([page.waitForNavigation(), click()]);
  const main = async () => (await page.textContent('main')).replace(/\s+/g, ' ');
  const confirmOk = async () => {
    await page.waitForSelector('#nim-confirm.show [data-nim-confirm-ok]');
    await nav(() => page.click('#nim-confirm [data-nim-confirm-ok]'));
  };
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await nav(() => page.click('button[type=submit]'));

    // ---- Feature list without the WordPress Toolkit, and a plan with it
    const listName = 'No WP ' + stamp;
    await page.goto(BASE + '/features');
    await page.click('button[data-bs-target="#fl-modal-new"]');
    await page.waitForSelector('#fl-modal-new.show');
    await page.fill('#fl-modal-new input[name=name]', listName);
    await page.uncheck('#fl-modal-new input[name="features[]"][value="wordpress"]');
    await nav(() => page.click('#fl-modal-new button[type=submit], #fl-modal-new .modal-footer button.btn-primary'));
    if (!(await main()).includes(listName)) { throw new Error('feature list not listed: ' + (await main()).slice(0, 300)); }
    step('feature list without the WordPress Toolkit');

    const planName = 'Basic ' + stamp;
    await page.goto(BASE + '/plans');
    await page.click('button[data-bs-target="#plan-new"]');
    await page.waitForSelector('#plan-new.show');
    await page.fill('#pn-name', planName);
    await page.selectOption('#pn-features', { label: listName });
    await nav(() => page.click('#plan-new button.btn-primary'));
    if (!(await main()).includes(planName)) { throw new Error('plan not listed'); }
    // Edit it in its own window.
    await page.click('tr:has-text("' + planName + '") button[data-bs-target^="#plan-edit-"]');
    const editId = await (await page.waitForSelector('.modal.show[id^="plan-edit-"]')).evaluate(m => m.id);
    if ((await page.inputValue('#' + editId + ' input[name=name]')) !== planName) { throw new Error('edit window not filled'); }
    await page.fill('#' + editId + ' input[name=description]', 'edited in a window');
    await nav(() => page.click('#' + editId + ' button.btn-primary'));
    if (!(await main()).includes('edited in a window')) { throw new Error('plan edit not saved'); }
    step('plan created and edited in windows, using the feature list');

    // ---- Create a New Account on that plan
    const user = 'cu' + stamp;
    const domain = 'shop' + stamp + '.example.test';
    await page.goto(BASE + '/accounts/new');
    await page.fill('#a-domain', domain);
    await page.fill('#a-username', user);
    await page.click('[data-nim-genpass="#a-password"]');
    const password = await page.inputValue('#a-password');
    if (password.length < 16 || await page.inputValue('#a-password2') !== password) { throw new Error('generated password'); }
    await page.fill('#a-email', user + '@example.test');
    await page.selectOption('#a-plan', { label: planName + ' — edited in a window' });
    await nav(() => page.click('main form button[type=submit]:has-text("Create account")'));
    const userUrl = page.url();
    if (!/\/users\/\d+\/edit$/.test(userUrl)) { throw new Error('account not created: ' + page.url() + ' ' + (await main()).slice(0, 300)); }
    step('account created with its website: ' + user);

    // ---- The customer: no WordPress page on this plan
    const cust = await (await browser.newContext({ viewport: { width: 1300, height: 900 } })).newPage();
    // (The refused WordPress address below logs a 403: only script errors count here.)
    cust.on('pageerror', e => errors.push('customer pageerror: ' + e.message));
    await cust.goto(BASE + '/login');
    await cust.fill('#username', user); await cust.fill('#password', password);
    await Promise.all([cust.waitForNavigation(), cust.click('button[type=submit]')]);
    await cust.goto(BASE + '/websites');
    const siteHref = await cust.getAttribute(`a[href^="/sites/"]:has-text("${domain}")`, 'href');
    const site = siteHref.replace(/\/$/, '');
    await cust.goto(BASE + site);
    if (await cust.$(`a[href="${site}/wordpress"]`)) { throw new Error('WordPress menu entry shown although the plan has no WordPress Toolkit'); }
    const wp = await cust.goto(BASE + site + '/wordpress');
    if (wp.status() === 200) { throw new Error('WordPress page reachable: ' + wp.status()); }
    step('customer on that plan: no WordPress Toolkit (menu and address)');

    // ---- Suspend and unsuspend
    await page.goto(userUrl);
    await page.click('button[data-bs-target="#suspend-modal"]');
    await page.waitForSelector('#suspend-modal.show');
    await page.fill('#suspend-reason', 'e2e check');
    await nav(() => page.click('#suspend-modal .modal-footer button.btn-danger'));
    if (!(await main()).includes('Suspended')) { throw new Error('not suspended: ' + (await main()).slice(0, 400)); }
    await cust.reload();
    if (!(await cust.textContent('body')).match(/suspended|Sign in/i)) { throw new Error('customer still signed in after suspension'); }
    await page.click('#account-suspension form[action$="/unsuspend"] button');
    await confirmOk();
    if ((await main()).includes('Suspended since')) { throw new Error('still suspended'); }
    step('suspended with a reason (customer signed out), then unsuspended after confirmation');

    // ---- Plan change page compares
    await page.goto(userUrl.replace(/\/edit$/, '/plan'));
    if (!(await main()).includes(planName)) { throw new Error('plan page lacks the current plan'); }
    step('plan change page shows the current plan');

    // ---- Addon domain → account of its own
    const addon = 'addon' + stamp + '.example.test';
    await page.goto(BASE + site + '/addons');
    await page.click('button[data-bs-target="#new-addon"]');
    await page.waitForSelector('#new-addon.show');
    await page.fill('#ad-name', addon);
    await nav(() => page.click('#new-addon button[type=submit]'));
    const row = page.locator(`tr:has-text("${addon}")`);
    await row.locator('button:has-text("Convert to account")').click();
    const win = page.locator('.modal.show[id^="convert-"]');
    await win.waitFor();
    if (!(await win.textContent()).includes('Cron jobs')) { throw new Error('the window does not list what moves'); }
    const newUser = 'ad' + stamp;
    await win.locator('input[name=username]').fill(newUser);
    await win.locator('input[name=email]').fill(newUser + '@example.test');
    await win.locator('input[name=password]').fill('an-e2e-password-123');
    await win.locator('input[name=password_confirmation]').fill('an-e2e-password-123');
    await page.screenshot({ path: OUT + '/convert-window.png' });
    await win.locator('button:has-text("Convert to account")').click();
    await page.waitForSelector('#nim-confirm.show');
    await page.fill('#nim-confirm-input', addon);
    await confirmOk();
    if (!/\/users\/\d+\/edit$/.test(page.url())) { throw new Error('conversion: ' + page.url() + ' ' + (await main()).slice(0, 400)); }
    if (!(await main()).includes('is now the hosting account ' + newUser)) { throw new Error('conversion message: ' + (await main()).slice(0, 300)); }
    await page.goto(BASE + '/websites');
    const addonRow = await page.textContent(`tr:has-text("${addon}")`);
    if (!addonRow.includes(newUser)) { throw new Error('addon not owned by the new account: ' + addonRow.replace(/\s+/g, ' ')); }
    step('addon domain converted into account ' + newUser);

    // ---- Phone widths
    await page.setViewportSize({ width: 390, height: 844 });
    for (const p of ['/features', '/ips', '/limits', userUrl.replace(BASE, '')]) {
      await page.goto(BASE + p);
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      if (over > 1) { throw new Error('horizontal scroll on a phone at ' + p + ': ' + over + 'px'); }
    }
    step('admin pages fit a phone');
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK account administration');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/accountadmin-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
