// Server administration pages in tabs and windows: Server (System, Disks,
// Network interfaces, Server settings with one window per setting),
// Resource limits (Limited accounts, Disk quotas; "Apply the plans" at the
// top right), IP addresses ("Add an address", "Add an IPv6 range" windows)
// and Servers ("Connect a server" window). No script or CSP errors; phones
// do not scroll sideways.
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await (await browser.newContext({ viewport: { width: 1300, height: 900 } })).newPage();
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')'));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text() + ' (' + page.url() + ')'); });
  const step = s => console.log('STEP', s);
  const tabs = async () => page.$$eval('.wcp-subtabs [role=tab]', b => b.map(x => x.textContent.replace(/\s+\d+\s*$/, '').replace(/\s+/g, ' ').trim()));
  const openModal = async (target, check) => {
    await page.click(`button[data-bs-target="${target}"]`);
    await page.waitForSelector(`${target}.show`);
    if (check && !(await page.textContent(target)).includes(check)) { throw new Error(target + ' lacks ' + check); }
    await page.click(`${target} .btn-close`);
    await page.waitForSelector(target, { state: 'hidden' });
  };
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    await page.goto(BASE + '/server');
    const st = await tabs();
    if (JSON.stringify(st.map(t => t.replace(/ restart$/, ''))) !== JSON.stringify(['System', 'Disks', 'Network interfaces', 'Server settings'])) { throw new Error('server tabs: ' + JSON.stringify(st)); }
    await page.click('[data-bs-target="#settings"]');
    await page.waitForSelector('#settings.active');
    await openModal('#set-hostname', 'Host name');
    await openModal('#set-timezone', 'Time zone');
    await openModal('#root-password', 'Your panel password');
    if (await page.$('button[data-bs-target="#graphical-login"]')) { await openModal('#graphical-login', 'GNOME'); }
    if (await page.$('button[data-bs-target="#server-restart"]')) { await openModal('#server-restart', 'Type the host name'); }
    // A saved setting comes back to the Server settings tab.
    await page.click('button[data-bs-target="#set-timezone"]');
    await page.waitForSelector('#set-timezone.show');
    await Promise.all([page.waitForLoadState('networkidle'), page.click('#set-timezone button.btn-primary')]);
    await page.waitForSelector('#settings.active', { timeout: 15000 });
    step('Server: tabs; host name, time zone, root password, graphical login, restart in windows; back on the settings tab after saving');

    await page.goto(BASE + '/limits');
    if (JSON.stringify((await tabs()).map(t => t.replace(/ (on|off)$/, ''))) !== JSON.stringify(['Limited accounts', 'Disk quotas'])) { throw new Error('limits tabs: ' + JSON.stringify(await tabs())); }
    if (!(await page.$('.wcp-page-actions button:has-text("Apply the plans now")'))) { throw new Error('Apply the plans not at the top right'); }
    await page.goto(BASE + '/limits#disk-quotas');
    await page.waitForSelector('#disk-quotas.active');
    step('Resource limits: tabs, Apply the plans at the top right, #disk-quotas opens its tab');

    await page.goto(BASE + '/ips');
    await openModal('#ip-add', 'must already be configured');
    await page.goto(BASE + '/ips/ranges');
    await openModal('#range-add', 'routes to this server');
    step('IP addresses: add an address / an IPv6 range in windows');

    await page.goto(BASE + '/servers');
    await openModal('#server-connect', 'Certificate pin');
    step('Servers: Connect a server in a window');

    await page.setViewportSize({ width: 390, height: 844 });
    for (const u of ['/server', '/limits', '/ips', '/servers', '/packages']) {
      await page.goto(BASE + u);
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      if (over > 1) { throw new Error(u + ': horizontal scroll on a phone: ' + over + 'px'); }
    }
    step('phones: no sideways scrolling');
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK server pages');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/serverpages-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
