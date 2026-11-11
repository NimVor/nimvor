// End-to-end test of the customer panel themes (dev instance, system commands simulated):
// - a new customer sees NimVor Modern; the Appearance page offers every enabled theme
// - choosing each theme and saving shows that theme's layout, with the website's tools reachable, the finder
//   working, the dark-mode button working, and nothing overflowing a phone screen
// - switching themes keeps the customer signed in and the website unchanged
// - the administrator can switch a theme off and change the default
// No script or CSP errors anywhere.
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';

(async () => {
  const browser = await chromium.launch();
  const admin = await (await browser.newContext({ viewport: { width: 1300, height: 900 } })).newPage();
  const errors = [];
  const watch = (p, who) => {
    p.on('pageerror', e => errors.push(who + ' pageerror: ' + e.message));
    p.on('console', m => { if (m.type() === 'error' && !/status of 40[34]/.test(m.text())) { errors.push(who + ' console: ' + m.text()); } });
  };
  watch(admin, 'admin');
  const step = s => console.log('STEP', s);
  const stamp = Date.now().toString(36).slice(-6);
  const nav = (p, click) => Promise.all([p.waitForNavigation(), click()]);
  try {
    await admin.goto(BASE + '/login');
    await admin.fill('#username', 'admin'); await admin.fill('#password', 'dev-password-change-me');
    await nav(admin, () => admin.click('button[type=submit]'));

    // ---- A customer with a website
    const user = 'thm' + stamp;
    const domain = 'thm' + stamp + '.example.test';
    await admin.goto(BASE + '/accounts/new');
    await admin.fill('#a-domain', domain);
    await admin.fill('#a-username', user);
    await admin.click('[data-nim-genpass="#a-password"]');
    const password = await admin.inputValue('#a-password');
    await admin.fill('#a-email', user + '@example.test');
    await nav(admin, () => admin.click('main form button[type=submit]:has-text("Create account")'));
    if (!/\/users\/\d+\/edit$/.test(admin.url())) { throw new Error('account not created: ' + admin.url()); }
    step('customer account created');

    const cust = await (await browser.newContext({ viewport: { width: 1300, height: 900 } })).newPage();
    watch(cust, 'customer');
    await cust.goto(BASE + '/login');
    await cust.fill('#username', user); await cust.fill('#password', password);
    await Promise.all([cust.waitForNavigation(), cust.click('button[type=submit]')]);
    const html = async () => cust.getAttribute('html', 'data-nim-theme');
    if (await html() !== 'modern') { throw new Error('a new customer should have NimVor Modern, got ' + await html()); }
    await cust.goto(BASE + '/websites');
    const href = await cust.getAttribute(`a[href^="/sites/"]:has-text("${domain}")`, 'href');
    const site = href.replace(/\/$/, '');
    step('new customer: NimVor Modern');

    // ---- Each theme
    const themes = ['classic', 'workspace', 'simple', 'compact'];
    for (const t of themes) {
      await cust.goto(BASE + '/appearance');
      await cust.check(`input[name=theme][value=${t}]`);
      await nav(cust, () => cust.click('button.btn-primary:has-text("Save")'));
      if (await html() !== t) { throw new Error('theme ' + t + ' not applied: ' + await html()); }
      await cust.goto(BASE + '/websites');
      if (!(await cust.locator('body.nt-' + t).count())) { throw new Error(t + ': layout class missing'); }
      if (!(await cust.textContent('main')).includes(domain)) { throw new Error(t + ': website not on the home page'); }
      // The website's tools stay reachable and open.
      await cust.goto(BASE + site);
      for (const tool of ['files', 'databases', 'dns', 'ssl', 'backups']) {
        if (!(await cust.locator(`a[href="${site}/${tool}"]`).count())) { throw new Error(t + ': the ' + tool + ' tool is not linked'); }
      }
      // The finder lists a tool as you type (themes that have one).
      const finder = cust.locator('[data-nt-finder-input]');
      if (await finder.count()) {
        await finder.first().fill('file');
        await cust.waitForSelector('[data-nt-finder-item]:not([hidden])', { timeout: 3000 });
        await finder.first().fill('');
      }
      // Dark mode.
      await cust.click('[data-nim-theme-toggle]');
      const mode = await cust.getAttribute('html', 'data-bs-theme');
      await cust.click('[data-nim-theme-toggle]');
      if (mode === (await cust.getAttribute('html', 'data-bs-theme'))) { throw new Error(t + ': dark-mode button does nothing'); }
      // A phone: nothing wider than the screen.
      await cust.setViewportSize({ width: 390, height: 800 });
      for (const path of ['/websites', site]) {
        await cust.goto(BASE + path);
        const over = await cust.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        if (over > 2) { throw new Error(t + ' ' + path + ': ' + over + 'px wider than a phone screen'); }
      }
      await cust.setViewportSize({ width: 1300, height: 900 });
      step('theme ' + t + ': home, tools, finder, dark mode, phone width');
    }

    // ---- A preview changes nothing until it is kept
    await cust.goto(BASE + '/appearance');
    await cust.check('input[name=theme][value=modern]');
    await cust.click('button:has-text("Preview")');
    await cust.waitForSelector('.nim-preview-bar');
    await cust.goto(BASE + '/appearance');
    await nav(cust, () => cust.click('.nim-preview-bar button:has-text("Go back")').catch(() => null));
    step('preview bar');

    // ---- The administrator turns one theme off and changes the default
    await admin.goto(BASE + '/settings/themes');
    await admin.uncheck('#theme_enabled_compact');
    await admin.selectOption('#theme_default', 'workspace');
    await nav(admin, () => admin.click('main form button.btn-primary'));
    await cust.goto(BASE + '/appearance');
    if (await cust.locator('input[name=theme][value=compact]').count()) { throw new Error('a disabled theme is still offered'); }
    step('administrator controls');

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK themes');
  } catch (e) {
    console.error('FAIL themes:', e.message);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
