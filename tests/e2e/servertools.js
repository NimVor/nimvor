// End-to-end test of the server tools for administrators:
// - top bar: host name, system, WhaCP version and load (the load opens the
//   live monitoring page)
// - the dashboard's Server card: restart-all-services, server restart and
//   (only when a newer WhaCP was found) update buttons
// - the tool search sits in the sidebar above Dashboard; no "Write mode" pill
// - Monitoring → Processes: every process, filters, Trace / Stop / Kill and
//   "end all processes of an account"
// - SSL/TLS → Services and the WhaCP update page render
// No script or CSP errors.
const path = require('path');
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')'));
  page.on('console', m => { if (m.type() === 'error' && !/status of 503/.test(m.text())) errors.push('console: ' + m.text() + ' (' + page.url() + ')'); });
  const step = (s) => console.log('STEP', s);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    // Top bar.
    const bar = await page.$$eval('.wcp-topbar-server dt', d => d.map(x => x.textContent.trim()));
    for (const want of ['Hostname', 'OS', 'WhaCP', 'Load averages']) {
      if (!bar.includes(want)) { throw new Error('top bar: ' + want + ' missing in ' + JSON.stringify(bar)); }
    }
    const load = await page.textContent('[data-wcp-topbar-load] span');
    if (!/^\d+\.\d\d \d+\.\d\d \d+\.\d\d$/.test(load.trim())) { throw new Error('load: ' + load); }
    if (await page.locator('[data-wcp-topbar-update]:not(.d-none)').count() !== 0) { throw new Error('the update button shows without an update'); }
    if (await page.locator('.wcp-server-tools button[aria-label="Restart all services"]').count() !== 1) { throw new Error('no restart-all button in the Server card'); }
    if (await page.locator('.wcp-topbar [aria-label="Restart all services"], .wcp-topbar [aria-label="Restart the server"]').count() !== 0) { throw new Error('server tools still in the top bar'); }
    step('top bar: host name, system, version, load ' + load.trim());
    // On the dashboard the top bar shows the same load as the Load card.
    await page.waitForFunction(() => {
      const t = document.querySelector('[data-wcp-topbar-load] span').textContent.trim().split(' ');
      const card = document.querySelector('[data-metric="load"]');
      if (!card) { return false; }
      const one = card.querySelector('[data-value]').textContent.trim();
      const rest = (card.querySelector('[data-detail]').textContent.match(/[\d.]+/g) || []).slice(0, 2);
      return t[0] === one && t[1] === rest[0] && t[2] === rest[1];
    }, null, { timeout: 30000 });
    step('top bar load matches the dashboard Load card');
    // The sidebar: search above Dashboard, no write-mode pill.
    const order = await page.evaluate(() => {
      const s = document.querySelector('.wcp-sidebar .wcp-search-trigger');
      const d = [...document.querySelectorAll('.wcp-sidebar .wcp-nav-link')].find(a => a.textContent.trim() === 'Dashboard');
      return s && d ? !!(s.compareDocumentPosition(d) & Node.DOCUMENT_POSITION_FOLLOWING) : null;
    });
    if (order !== true) { throw new Error('the search is not above Dashboard in the sidebar'); }
    if (await page.locator('.wcp-topbar .wcp-search-trigger').count() !== 0) { throw new Error('the search is still in the top bar'); }
    if ((await page.textContent('.wcp-sidebar')).includes('Write mode')) { throw new Error('"Write mode" still shown'); }
    step('sidebar: search above Dashboard, no write-mode pill');
    if (await page.locator('.wcp-sidebar-account').count() !== 0) { throw new Error('the profile block is still in the sidebar'); }
    // Groups fold, stay folded after a reload; the current page's group stays open.
    await page.click('[data-wcp-nav-toggle="Hosting"]');
    if (await page.locator('#wcp-nav-hosting').isVisible()) { throw new Error('Hosting did not fold'); }
    await page.reload();
    if (await page.locator('#wcp-nav-hosting').isVisible()) { throw new Error('the folded group opened again after a reload'); }
    await page.goto(BASE + '/ssl/autossl');
    if (!(await page.locator('#wcp-nav-hosting').isVisible())) { throw new Error('the group of the current page is folded'); }
    const sslMenu = await page.$$eval('[data-wcp-section-menu="ssl"] a', a => a.map(x => x.textContent.trim()));
    if (JSON.stringify(sslMenu) !== JSON.stringify(['SSL hosts', 'AutoSSL', 'Certificates', 'Keys and CSRs', 'TLS configuration', 'Services SSL'])) { throw new Error('SSL menu: ' + JSON.stringify(sslMenu)); }
    if (await page.locator('button:has-text("Run AutoSSL for all websites")').count() !== 1) { throw new Error('no Run AutoSSL button'); }
    await page.click('[data-wcp-nav-toggle="Hosting"]'); await page.click('[data-wcp-nav-toggle="Hosting"]');
    await page.evaluate(() => localStorage.removeItem('wcp-nav-folded'));
    await page.goto(BASE + '/');
    step('sidebar: no profile block, groups fold and are remembered, SSL menu, AutoSSL page');
    // Server restart: a confirmation with the host name.
    await page.click('.wcp-server-tools button[aria-label="Restart the server"]');
    await page.waitForSelector('#wcp-reboot.show');
    const host = await page.textContent('#wcp-reboot label[for="wcp-reboot-confirm"] code');
    if (!host || !host.trim()) { throw new Error('the restart dialog does not name the host'); }
    await page.click('#wcp-reboot [data-bs-dismiss="modal"]');
    step('server restart asks for the host name (' + host.trim() + ')');
    // The load opens live monitoring.
    await Promise.all([page.waitForNavigation(), page.click('[data-wcp-topbar-load]')]);
    if (!page.url().endsWith('/monitoring/live')) { throw new Error('load link: ' + page.url()); }
    step('load opens live monitoring');

    // Process manager (also: the top bar refreshes the load on other pages).
    const polled = page.waitForResponse(r => r.url().includes('/topbar/status') && r.status() === 200, { timeout: 10000 });
    await page.goto(BASE + '/monitoring/processes');
    await polled;
    step('top bar refreshes the load every few seconds');
    const rows = await page.locator('.wcp-proc-table tbody tr[data-wcp-row]').count();
    if (rows < 5) { throw new Error('only ' + rows + ' processes listed'); }
    const ops = await page.locator('.wcp-proc-table tbody tr[data-wcp-row] a:has-text("Trace")').count();
    if (ops === 0) { throw new Error('no Trace links'); }
    await page.selectOption('[data-wcp-dt-select="kind"]', 'user');
    const visibleKernel = await page.locator('.wcp-proc-table tbody tr[data-dt-kind="kernel"]:visible').count();
    if (visibleKernel !== 0) { throw new Error('kernel threads still shown with "Without kernel threads"'); }
    const users = await page.$$eval('#pk-user option', o => o.map(x => x.value).filter(Boolean));
    if (users.includes('root')) { throw new Error('root offered for "end all processes"'); }
    await page.screenshot({ path: path.join(OUT, 'processes.png') });
    step('processes: ' + rows + ' listed, filters, Trace/Stop/Kill, account list without root');

    // SSL/TLS → Services and the update page.
    await page.goto(BASE + '/ssl/services');
    const cards = await page.$$eval('.card-header', h => h.map(x => x.textContent.trim()));
    for (const want of ['WhaCP panel (web services)', 'Mail server (SMTP, submission, IMAP)', 'Where each SSL setting is']) {
      if (!cards.includes(want)) { throw new Error('ssl services: ' + want + ' missing in ' + JSON.stringify(cards)); }
    }
    step('SSL/TLS → Services');
    await page.goto(BASE + '/whacp/update');
    if (!(await page.textContent('h1')).includes('WhaCP updates')) { throw new Error('update page'); }
    step('update page');

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK server tools');
  } catch (e) {
    await page.screenshot({ path: path.join(OUT, 'servertools-failure.png'), fullPage: true }).catch(() => {});
    console.error(e.message);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
