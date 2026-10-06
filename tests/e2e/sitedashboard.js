// End-to-end test of the website dashboard and its sections:
// - dashboard: summary, no server details, feature groups
// - "Search features…": keyboard to "Create database", which opens that form
// - quick actions ("Create backup" opens the backup form) and the website menu
// - databases: a refused name reopens the form with the error
// - DNS: BIND set up through the panel (installed state simulated in the
//   dev root), a zone created from the website, a record added with the
//   "CNAME www" shortcut
// - email set-up page inside the website; SSL headline and anchors
// - Advanced: disable and enable through the Danger Zone confirmation
// - phones: "Website menu" opens the menu, no horizontal scrolling
// - a customer sees their own website, not the administrator's, and no
//   server-only features
// No script or CSP errors on any page. (The dev instance simulates system commands.)
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';
const DEV_ROOT = path.resolve(__dirname, '../../.dev/root');

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } });
  const page = await ctx.newPage();
  const errors = [];
  const watch = (p) => {
    p.on('pageerror', e => errors.push('pageerror: ' + e.message));
    p.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  };
  watch(page);
  const step = (s) => console.log('STEP', s);
  const flash = async (p = page) => ((await p.$('.alert-success, .alert-danger')) ? (await p.textContent('.alert-success, .alert-danger')).trim() : '');
  const login = async (p, user, pass) => {
    await p.goto(BASE + '/login');
    await p.fill('#username', user); await p.fill('#password', pass);
    await Promise.all([p.waitForNavigation(), p.click('button[type=submit]')]);
  };
  // Forms and links may load the next page in place (AJAX layer) or
  // navigate: wait until the network is quiet either way.
  const submit = async (p, selector) => {
    await p.click(selector);
    await p.waitForTimeout(250);
    await p.waitForLoadState('networkidle');
  };
  const stamp = Date.now().toString(36);
  const domain = 'dash' + stamp + '.example.test';
  try {
    await login(page, 'admin', 'dev-password-change-me');

    // A customer to own the website (created through the panel).
    await page.goto(BASE + '/users/new');
    await page.fill('[name=username]', 'cust' + stamp);
    await page.fill('[name=email]', 'cust' + stamp + '@example.test');
    await page.fill('[name=password]', 'Customer-pass-' + stamp);
    await page.fill('[name=password_confirmation]', 'Customer-pass-' + stamp);
    const roleValue = await page.$eval('[name=role_id]', s => [...s.options].find(o => /Hosting customer/.test(o.textContent)).value);
    await page.selectOption('[name=role_id]', roleValue);
    await submit(page, 'main form[action="/users"] button[type=submit]');
    step('customer created');

    await page.goto(BASE + '/websites/new');
    await page.fill('#f-domain', domain);
    const ownerValue = await page.$eval('#f-owner', (s, u) => [...s.options].find(o => o.textContent.startsWith(u + ' ')).value, 'cust' + stamp);
    await page.selectOption('#f-owner', ownerValue);
    // The form is sent by the AJAX layer (the next page loads in place).
    await Promise.all([page.waitForResponse(r => r.url().endsWith('/websites/new') && r.request().method() === 'POST'), page.click('main form[action="/websites/new"] button[type=submit]')]);
    await page.waitForLoadState('networkidle');
    await page.goto(BASE + '/websites');
    const manage = await page.$(`a[href^="/sites/"]:has-text("${domain}")`) || await page.$(`tr:has-text("${domain}") a[href^="/sites/"]`);
    if (!manage) { throw new Error('website not listed'); }
    const site = (await manage.getAttribute('href')).replace(/\/$/, '');
    await page.goto(BASE + site);
    step('website created and opened: ' + site);

    // ---- Site page: details and tiles
    const main = await page.textContent('main');
    for (const s of ['Site user', 'Root directory', 'Handler', 'Site size', 'Database size', 'Site managing', 'Delete the website', 'Disable the website']) {
      if (!main.includes(s)) { throw new Error('site page misses ' + s); }
    }
    const tiles = await page.$$eval('[data-wcp-tile]', t => t.map(x => x.getAttribute('data-wcp-tile')));
    for (const t of ['ftp accounts', 'files', 'databases', 'domains (dns)', 'ssl certificates', 'scheduler', 'mail', 'backup copies', 'site access', 'settings', 'php settings', 'logs']) {
      if (!tiles.includes(t)) { throw new Error('missing tile ' + t + ': ' + tiles); }
    }
    if (await page.$('.wcp-tabs')) { throw new Error('old tab bar still shown'); }
    step('site page: details, tiles, delete and disable');

    // ---- Search features
    await page.press('body', '/');
    if (await page.evaluate(() => document.activeElement.id) !== 'wcp-fs') { throw new Error('"/" does not focus the search'); }
    await page.keyboard.type('create datab');
    const first = await page.$eval('[data-wcp-feature-results] a:not(.d-none)', a => a.textContent.trim());
    if (!first.startsWith('Create database')) { throw new Error('first result: ' + first); }
    await Promise.all([page.waitForURL(/\/databases#new-database$/), page.keyboard.press('Enter')]);
    await page.waitForSelector('#new-database.show');
    await page.waitForFunction(() => document.activeElement && document.activeElement.id === 'db-name');
    step('search → Enter opens "Create database" with the name field focused');

    // ---- Databases (the dev instance cannot create real databases; the
    // form's behaviour is what changed: a refused name reopens it with the error)
    await page.fill('#db-name', 'Bad Name!');
    await submit(page, '#new-database button[type=submit]');
    if (!page.url().includes(site + '/databases')) { throw new Error('stayed on the website: ' + page.url()); }
    await page.waitForSelector('#new-database.show');
    if (!(await page.$('#new-database .text-danger, #new-database .is-invalid'))) { throw new Error('field error not shown: ' + await flash()); }
    if (!(await page.textContent('#databases')).includes('No databases yet')) { throw new Error('empty state'); }
    // Fully shown (its first field focused) before it can be closed.
    await page.waitForFunction(() => document.activeElement && document.activeElement.id === 'db-name');
    await page.keyboard.press('Escape');
    await page.waitForSelector('#new-database', { state: 'hidden' });
    await page.click('button[data-bs-target="#new-db-user"]');
    await page.waitForSelector('#new-db-user.show');
    step('databases: refused name reopens the window with its error; empty state; "Create user" window');

    // ---- Quick action and website menu
    await page.goto(BASE + site);
    await page.fill('#wcp-fs', 'back up now');
    await Promise.all([page.waitForURL(/\/backups#backup-now$/), page.keyboard.press('Enter')]);
    await page.waitForSelector('#backup-now.show');
    await page.goto(BASE + site);
    await Promise.all([page.waitForNavigation(), page.click('[data-wcp-tile="backup copies"]')]);
    if (!page.url().endsWith(site + '/backups')) { throw new Error('tile → ' + page.url()); }
    step('search "back up now" opens the backup form; the tile opens backups');

    await Promise.all([page.waitForNavigation(), page.click('[data-wcp-site-menu] a:has-text("SSL / HTTPS")')]);
    if (!page.url().endsWith(site + '/ssl')) { throw new Error('menu → ' + page.url()); }
    if (await page.getAttribute('[data-wcp-site-menu] a[aria-current=page]', 'href') !== site + '/ssl') { throw new Error('menu active item'); }
    if (!(await page.textContent('#certificate')).includes('HTTPS is not enabled')) { throw new Error('SSL headline'); }
    for (const id of ['#issue', '#upload-certificate']) { if (!(await page.$(id))) { throw new Error('SSL anchor ' + id); } }
    if (!(await page.textContent('.wcp-crumbs')).includes(domain)) { throw new Error('breadcrumb'); }
    step('website menu navigates and marks the page; SSL headline and anchors');

    // ---- DNS: set up BIND (installed state simulated), zone from the website, a record by shortcut
    fs.mkdirSync(path.join(DEV_ROOT, 'usr/sbin'), { recursive: true });
    fs.writeFileSync(path.join(DEV_ROOT, 'usr/sbin/named'), '');
    fs.mkdirSync(path.join(DEV_ROOT, 'etc/bind'), { recursive: true });
    for (const f of ['named.conf', 'named.conf.local']) { if (!fs.existsSync(path.join(DEV_ROOT, 'etc/bind', f))) { fs.writeFileSync(path.join(DEV_ROOT, 'etc/bind', f), ''); } }
    await page.goto(BASE + '/settings');
    await page.click('#tab-dns');
    await page.waitForSelector('#pane-dns.show');
    await page.fill('[name=dns_nameservers]', 'ns1.example.test, ns2.example.test');
    await page.fill('[name=dns_default_ipv4]', '203.0.113.10');
    await submit(page, 'main form:has([name=dns_nameservers]) button[type=submit]');
    await page.goto(BASE + '/dns');
    if (await page.$('#dns-confirm')) {
      await page.check('#dns-confirm');
      await submit(page, 'main form[action="/dns/setup"] button');
      if (!(await flash()).includes('ready')) { throw new Error('DNS setup: ' + await flash()); }
    }
    await page.goto(BASE + site);
    await Promise.all([page.waitForNavigation(), page.click('[data-wcp-tile="domains (dns)"]')]);
    if (!(await page.textContent('#dns-setup')).includes('not hosted here') || !(await page.$('[data-wcp-site-menu]'))) { throw new Error('DNS set-up page'); }
    await submit(page, '#dns-setup button[type=submit]');
    if (!/\/dns\/zones\/\d+$/.test(page.url())) { throw new Error('zone not created: ' + page.url() + ' ' + await flash()); }
    if (!(await page.$('[data-wcp-site-menu] a[aria-current=page]'))) { throw new Error('zone page lost the website menu'); }
    const dnsHeads = await page.$$eval('#dns-table thead th', t => t.map(x => x.textContent.trim()));
    if (dnsHeads.join('|') !== 'Select|Type|Name|Value|TTL|Actions') { throw new Error('DNS columns: ' + dnsHeads); }
    await page.click('button[data-wcp-dns-new]');
    await page.waitForSelector('#dns-record.show');
    await page.selectOption('#rec-type', 'CNAME');
    await page.fill('#rec-name', 'shop');
    await page.fill('#rec-value', domain + '.');
    await submit(page, '#dns-record button:has-text("Save record")');
    const row = await page.$(`#dns-table tr:has(td:text-is("shop"))`);
    if (!row || !(await row.textContent()).includes('CNAME')) { throw new Error('record not added: ' + await flash()); }
    step('DNS: zone created inside the website, record added in the record window');

    // ---- Email set-up page stays inside the website
    await page.goto(BASE + site + '/mail');
    if (!(await page.$('#mail-setup')) || !(await page.$('[data-wcp-site-menu]'))) { throw new Error('mail set-up page'); }
    if (!(await page.$('#mail-setup a[href="/mail"]'))) { throw new Error('administrator link to set up the mail server'); }
    step('email set-up page inside the website');

    // ---- Advanced: Danger Zone
    await page.goto(BASE + site);
    await Promise.all([page.waitForNavigation(), page.click('[data-wcp-site-menu] a:has-text("Advanced")')]);
    for (const s of ['System user', 'Document root', 'Danger Zone', 'Delete website']) {
      if (!(await page.textContent('main')).includes(s)) { throw new Error('Advanced misses ' + s); }
    }
    await page.click('#danger-zone form[action$="/toggle"] button');
    await page.waitForSelector('#wcp-confirm.show');
    await submit(page, '[data-wcp-confirm-ok]');
    if (!page.url().endsWith(site + '/advanced') || !(await page.textContent('.wcp-subtitle')).includes('Disabled')) { throw new Error('disable: ' + page.url()); }
    await submit(page, '#danger-zone form[action$="/toggle"] button');
    if (!(await page.textContent('.wcp-subtitle')).includes('Active')) { throw new Error('enable'); }
    step('Advanced: disable (with confirmation) and enable return to Advanced');

    // ---- Phones
    const phone = await browser.newPage({ viewport: { width: 390, height: 844 } });
    watch(phone);
    await login(phone, 'admin', 'dev-password-change-me');
    await phone.goto(BASE + site);
    const menuRight = () => phone.$eval('#wcp-sidebar', el => el.getBoundingClientRect().right);
    if (await menuRight() > 0) { throw new Error('menu on screen before opening'); }
    const overflow = await phone.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    if (overflow > 0) { throw new Error('horizontal scrolling on phones: ' + overflow + 'px'); }
    await phone.click('[data-wcp-site-menu-button]');
    await phone.waitForSelector('#wcp-sidebar.is-open [data-wcp-site-menu] a:has-text("Databases")', { state: 'visible' });
    await phone.waitForFunction(() => document.getElementById('wcp-sidebar').getBoundingClientRect().left >= 0);
    await Promise.all([phone.waitForNavigation(), phone.click('[data-wcp-site-menu] a:has-text("Databases")')]);
    if (!phone.url().endsWith(site + '/databases')) { throw new Error('phone menu → ' + phone.url()); }
    await phone.screenshot({ path: OUT + '/sitedashboard-phone.png' });
    await phone.close();
    step('phones: "Website menu" opens the menu and navigates; no horizontal scroll');

    // ---- Customer visibility
    const custCtx = await browser.newContext({ viewport: { width: 1400, height: 900 } });
    const cust = await custCtx.newPage();
    watch(cust);
    await login(cust, 'cust' + stamp, 'Customer-pass-' + stamp);
    await cust.goto(BASE + site);
    const custMain = await cust.textContent('main');
    for (const s of ['Server security', 'Audit log', 'Memcached', 'Spam filter', 'Backup destinations']) {
      if (custMain.includes(s)) { throw new Error('customer sees ' + s); }
    }
    if (await cust.$('#wcp-sidebar')) { throw new Error('customers get no sidebar'); }
    if (!(await cust.$('[data-wcp-tiles]'))) { throw new Error('customer site page'); }
    await cust.goto(BASE + '/');
    if (!cust.url().endsWith('/websites') || !(await cust.$('.wcp-site-card'))) { throw new Error('customer home: ' + cust.url()); }
    const cards = await cust.$$eval('.wcp-site-card', c => c.map(x => x.textContent));
    if (cards.length !== 1 || !cards[0].includes(domain)) { throw new Error('customer cards: ' + cards.length); }
    await cust.fill('[data-wcp-cards-search]', 'no-such-site');
    if (await cust.isVisible('.wcp-site-card')) { throw new Error('card search'); }
    const others = await (await cust.request.get(BASE + '/sites/1', { maxRedirects: 0 })).status();
    if (site !== '/sites/1' && others !== 404) { throw new Error("customer opens another's website: HTTP " + others); }
    await custCtx.close();
    step("customer: no sidebar, home is their site cards (searchable), own website only");

    await page.screenshot({ path: OUT + '/sitedashboard.png', fullPage: true });
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK website dashboard');
  } catch (e) {
    await page.screenshot({ path: OUT + '/sitedashboard-failure.png', fullPage: true }).catch(() => {});
    console.error(e);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
