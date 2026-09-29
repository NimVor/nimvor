// End-to-end test of the administrators' list pages:
// - Websites table: search filters rows, "Total" follows, the page size
//   is kept, the row menu opens above the table and closes on a click
//   elsewhere
// - Databases: the Users tab, also opened by the #db-users link
// - Security: every tab loads, the firewall switch and rule form exist
// - FTP page loads
// - no horizontal scrolling on a phone
// No script or CSP errors on any page. (The dev instance simulates system commands.)
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  page.on('framenavigated', f => { if (f === page.mainFrame() && process.env.E2E_DEBUG) { console.log('NAV', f.url()); } });
  const stamp = Date.now().toString(36);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    for (const d of ['alpha' + stamp + '.example.test', 'beta' + stamp + '.example.test']) {
      await page.goto(BASE + '/websites/new');
      await page.fill('#f-domain', d);
      await Promise.all([page.waitForNavigation(), page.click('main form[action="/websites/new"] button[type=submit]')]);
    }

    // ---- Websites table
    await page.goto(BASE + '/websites');
    await page.waitForSelector('#wcp-sites-dt [data-wcp-dt-pager] .wcp-pager-total');
    const total = async () => (await page.textContent('#wcp-sites-dt .wcp-pager-total')).trim();
    const all = await total();
    await page.fill('#wcp-sites-dt [data-wcp-dt-search]', 'alpha' + stamp);
    if (await total() !== 'Total 1') { throw new Error('search: ' + await total()); }
    const visible = await page.$$eval('#wcp-sites tr[data-wcp-row]:not(.d-none)', r => r.map(x => x.textContent));
    if (visible.length !== 1 || !visible[0].includes('alpha' + stamp)) { throw new Error('rows after search: ' + visible.length); }
    await page.fill('#wcp-sites-dt [data-wcp-dt-search]', 'no-such-site');
    await page.waitForSelector('#wcp-sites [data-wcp-dt-empty]:not(.d-none)');
    await page.fill('#wcp-sites-dt [data-wcp-dt-search]', '');
    if (await total() !== all) { throw new Error('clearing the search'); }
    step('websites: search and total (' + all + ')');

    await page.selectOption('#wcp-sites-dt [data-wcp-dt-size]', '20');
    await page.reload();
    if (await page.$eval('#wcp-sites-dt [data-wcp-dt-size]', s => s.value) !== '20') { throw new Error('page size not kept'); }
    step('websites: page size kept');

    const row = `#wcp-sites tr:has-text("beta${stamp}")`;
    await page.click(`${row} summary.wcp-op-more`);
    const menu = await page.$(`${row} .wcp-dropmenu`);
    const box = await menu.boundingBox();
    if (!box || box.height < 60) { throw new Error('menu not shown'); }
    const onTop = await page.evaluate(([x, y]) => !!document.elementFromPoint(x, y).closest('.wcp-dropmenu'), [box.x + box.width / 2, box.y + box.height - 8]);
    if (!onTop) { throw new Error('menu is cut off or covered'); }
    await page.click('h1');
    if (await page.$(`${row} details[open]`)) { throw new Error('menu did not close'); }
    step('websites: row menu above the table, closes on a click elsewhere');

    // ---- Databases
    await page.goto(BASE + '/databases');
    await page.click('button[data-bs-target="#db-users"]');
    await page.waitForSelector('#db-users.show.active');
    await page.goto(BASE + '/websites');
    await page.goto(BASE + '/databases#db-users');
    await page.waitForSelector('#db-users.show.active');
    await page.click('button[data-bs-target="#new-db-user"]');
    await page.waitForSelector('#new-db-user.show');
    step('databases: Users tab (also from #db-users) and the user form');

    // ---- Security and FTP
    for (const p of ['/security', '/security/firewall', '/security/ssh', '/security/fail2ban', '/security/policies', '/security/hulk', '/security/compilers', '/security/smtp', '/security/password-strength', '/security/forkbomb', '/security/traceroute', '/ftp']) {
      const res = await page.goto(BASE + p);
      if (res.status() !== 200) { throw new Error(p + ' ' + res.status()); }
      if (p.startsWith('/security') && !(await page.$('[data-wcp-section-menu="security"] a.active'))) { throw new Error(p + ': security menu'); }
    }
    await page.goto(BASE + '/security/firewall');
    if ((await page.textContent('[data-wcp-section-menu="security"] a.active')).trim() !== 'Firewall') { throw new Error('active menu item'); }
    await page.click('button[data-bs-target="#new-rule"]');
    await page.waitForSelector('#new-rule.show #r-port');
    step('security pages (sidebar menu) and FTP load; firewall rule form opens');
    await page.screenshot({ path: OUT + '/adminlists-firewall.png', fullPage: true });
    for (const [p, shot] of [['/security/policies', 'policies'], ['/security/password-strength', 'password-strength'], ['/security/traceroute', 'traceroute'], ['/security/compilers', 'compilers'], ['/security/hulk', 'hulk']]) {
      await page.goto(BASE + p);
      await page.screenshot({ path: OUT + '/adminlists-' + shot + '.png', fullPage: true });
    }
    const items = await page.$$eval('[data-wcp-section-menu="security"] a', (as) => as.map((a) => a.textContent.trim()));
    const want = ['Overview', 'Firewall', 'SSH', 'Brute-force protection', 'Malware scan', 'Shell access', 'ModSecurity', 'Configure Security Policies', 'Compiler Access', 'SMTP Restrictions', 'cPHulk Brute Force Protection', 'Password Strength Configuration', 'Shell Fork Bomb Protection', 'Traceroute Enable/Disable'];
    if (want.some((w, i) => items[i] !== w)) { throw new Error('security menu order: ' + items.join(', ')); }
    step('security menu in order: ' + items.join(', '));
    await page.goto(BASE + '/security/hulk#countries');
    await page.waitForSelector('#countries.show.active');
    await page.click('button[data-bs-target="#report"]');
    await page.waitForSelector('#report.show.active');
    await page.screenshot({ path: OUT + '/adminlists-hulk-report.png', fullPage: true });
    step('cPHulk: countries tab from #countries, report tab');

    // ---- Phones
    await page.setViewportSize({ width: 390, height: 844 });
    for (const p of ['/websites', '/databases', '/security', '/security/firewall', '/security/policies', '/security/password-strength', '/security/traceroute', '/ftp']) {
      await page.goto(BASE + p);
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      if (over > 0) { throw new Error(p + ' scrolls sideways on a phone by ' + over + 'px'); }
    }
    step('phones: no sideways scrolling');

    if (errors.length) { throw new Error('browser errors:\n' + errors.join('\n')); }
    console.log('PASS adminlists');
  } catch (e) {
    await page.screenshot({ path: OUT + '/adminlists-failure.png', fullPage: true }).catch(() => {});
    console.error('FAIL adminlists:', e.message);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
