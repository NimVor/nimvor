// End-to-end test of the WordPress toolkit page (the dev agent simulates
// WordPress, WP-CLI and the web server):
// - scan finds WordPress and opens its page: header, tool strip, tabs
// - File Manager opens at the installation's folder; Logs shows the debug log
// - switches: search engine indexing; Debugging window; password protection
//   window (the web server file is written); hotlink protection
// - Security tab: one-click hardening through the confirmation window
// - Staging tab and the Clone window; phones: no horizontal scrolling
// No script or CSP errors on any page.
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
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  const flash = async () => ((await page.$('.alert-success, .alert-danger')) ? (await page.textContent('.alert-success, .alert-danger')).trim() : '');
  const submit = async (selector) => Promise.all([page.waitForNavigation(), page.click(selector)]);
  const stamp = Date.now().toString(36);
  const domain = 'wp' + stamp + '.example.test';
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await submit('button[type=submit]');

    await page.goto(BASE + '/websites/new');
    await page.fill('#f-domain', domain);
    await submit('main form[action="/websites/new"] button[type=submit]');
    await page.goto(BASE + '/websites');
    const link = await page.$(`tr:has-text("${domain}") a[href^="/sites/"]`) || await page.$(`a[href^="/sites/"]:has-text("${domain}")`);
    const site = (await link.getAttribute('href')).replace(/\/$/, '');
    // WordPress files in the website (the simulated scan looks for them).
    const doc = path.join(DEV_ROOT, 'home/whacp', domain, 'public_html');
    fs.mkdirSync(path.join(doc, 'wp-includes'), { recursive: true });
    fs.writeFileSync(path.join(doc, 'wp-settings.php'), '<?php');
    fs.writeFileSync(path.join(doc, 'wp-includes/version.php'), "<?php $wp_version = '6.8.1';");
    step('website ' + domain + ' with WordPress files');

    await page.goto(BASE + site + '/wordpress');
    await submit('form[action$="/wordpress/scan"] button');
    await page.goto(BASE + site + '/wordpress');
    if (!/\/wordpress\/\d+$/.test(page.url())) { throw new Error('not opened: ' + page.url() + ' ' + (await flash())); }
    const wpUrl = page.url().replace(BASE, '');
    const main = await page.textContent('main');
    for (const s of ['Simulated WordPress', 'WordPress 6.8.1', 'Update to 6.8.3', 'File Manager', 'Logs', 'Clone', 'Restore points', 'Search engine indexing',
      'Password protection', 'Take over wp-cron.php', 'Hotlink protection', 'Vulnerability protection', 'Akismet Anti-spam']) {
      if (!main.includes(s)) { throw new Error('page misses ' + s); }
    }
    const fm = await page.getAttribute('.wcp-wp-tool:has-text("File Manager")', 'href');
    if (!fm.endsWith('/files?path=public_html')) { throw new Error('file manager link ' + fm); }
    await page.screenshot({ path: path.join(OUT, 'wordpress-dashboard.png'), fullPage: true });
    step('scan opened the installation: header, tools, dashboard');

    const fmPage = await ctx.newPage();
    fmPage.on('pageerror', e => errors.push('fm pageerror: ' + e.message));
    await fmPage.goto(BASE + fm);
    if ((await fmPage.getAttribute('[data-start]', 'data-start')) !== 'public_html') { throw new Error('file manager start folder'); }
    await fmPage.close();
    await page.goto(BASE + wpUrl + '/logs');
    if (!(await page.textContent('main')).includes('simulated notice')) { throw new Error('logs page'); }
    await page.goto(BASE + wpUrl);
    step('File Manager opens at the installation; Logs shows the debug log');

    // Search engine indexing off (with confirmation).
    await page.click('#wp-sw-indexing');
    await page.waitForSelector('#wcp-confirm.show');
    await submit('[data-wcp-confirm-ok]');
    if (!(await flash()).includes('done')) { throw new Error('indexing: ' + (await flash())); }
    // Debugging window.
    await page.click('a[href="#wp-debug"]');
    await page.waitForSelector('#wp-debug.show');
    await page.check('#dbg-WP_DEBUG'); await page.check('#dbg-WP_DEBUG_LOG');
    await submit('#wp-debug button.btn-primary');
    if (!(await flash()).includes('WP_DEBUG, WP_DEBUG_LOG')) { throw new Error('debug: ' + (await flash())); }
    // Password protection window: the web server file appears.
    await page.click('a[href="#wp-protect"]');
    await page.waitForSelector('#wp-protect.show');
    await page.fill('#pp-user', 'boss'); await page.fill('#pp-pass', 'Protect-pass-1');
    await submit('#wp-protect button.btn-primary');
    if (!(await flash()).includes('must sign in as boss')) { throw new Error('protect: ' + (await flash())); }
    const conf = fs.readFileSync(path.join(DEV_ROOT, 'etc/nginx/whacp-protect', domain + '.conf'), 'utf8');
    if (!conf.includes('auth_basic $whacp_auth;')) { throw new Error('protection file: ' + conf); }
    if (!(await page.textContent('main')).includes('Visitors sign in as boss')) { throw new Error('protection state not shown'); }
    // Hotlink protection on with the switch.
    await submit('#wp-sw-hotlink');
    if (!(await flash()).includes('Hotlink protection is on')) { throw new Error('hotlink: ' + (await flash())); }
    await page.screenshot({ path: path.join(OUT, 'wordpress-switches.png'), fullPage: true });
    step('switches: indexing, debugging, password protection, hotlink');

    // Security tab: one-click hardening.
    await page.click('button[data-bs-target="#security"]');
    await page.waitForSelector('#security.show');
    if (!(await page.textContent('#security')).includes('Block access to xmlrpc.php')) { throw new Error('measures not listed'); }
    await page.click('#security form button:has-text("Secure (recommended)")');
    await page.waitForSelector('#wcp-confirm.show');
    await submit('[data-wcp-confirm-ok]');
    if (!(await flash()).includes('Security measures applied')) { throw new Error('harden: ' + (await flash())); }
    const conf2 = fs.readFileSync(path.join(DEV_ROOT, 'etc/nginx/whacp-protect', domain + '.conf'), 'utf8');
    if (!conf2.includes('wp-content/uploads') || conf2.includes('xmlrpc.php {')) { throw new Error('recommended rules: ' + conf2); }
    await page.goto(BASE + wpUrl + '#security');
    await page.waitForSelector('#security.show');
    await page.screenshot({ path: path.join(OUT, 'wordpress-security.png'), fullPage: true });
    step('security: recommended measures applied (xmlrpc.php left open)');

    // Staging tab and the Clone window.
    await page.goto(BASE + wpUrl + '#staging');
    await page.waitForSelector('#staging.show');
    await page.click('#staging a[href="#wp-clone"]');
    await page.waitForSelector('#wp-clone.show');
    if (!(await page.$(`#cl-site option:has-text("${domain}")`))) { throw new Error('clone targets'); }
    await page.click('#wp-clone .btn-close');
    step('staging tab and clone window');

    // Phones.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(BASE + wpUrl);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
    if (overflow) { throw new Error('horizontal scroll on phones'); }
    await page.screenshot({ path: path.join(OUT, 'wordpress-phone.png'), fullPage: true });
    step('phones: no horizontal scroll');

    if (errors.length) { throw new Error('browser errors:\n' + errors.join('\n')); }
    console.log('OK wordpress toolkit');
  } catch (e) {
    await page.screenshot({ path: path.join(OUT, 'wordpress-failure.png'), fullPage: true }).catch(() => {});
    console.error('FAIL', e.message);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
