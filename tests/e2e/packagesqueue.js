// End-to-end test of the Packages page: stats on top, Updates and
// Components tabs, and the installation queue. "Install" adds the component
// to a queue without reloading the page; the queue window has the queue on
// the left (each component's state, a list to add several more at once) and
// the output of the selected component on the right; rows turn into
// "Installed". (The dev instance simulates system commands: nothing is
// really installed.)
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')\n' + (e.stack || '')));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  const modal = '#wcp-pkg-queue';
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    await page.goto(BASE + '/packages');
    await page.evaluate(() => { window.__noReload = 1; });
    const tabs = await page.$$eval('.wcp-subtabs [role=tab]', b => b.map(x => x.textContent.replace(/\s*\d+\s*$/, '').trim()));
    if (JSON.stringify(tabs) !== JSON.stringify(['Updates', 'Components'])) { throw new Error('tabs: ' + JSON.stringify(tabs)); }
    const statText = (await page.textContent('.wcp-pkg-stats')).replace(/\s+/g, ' ');
    for (const w of ['Updates pending', 'Security updates', 'Package index', 'Components installed', 'Not installed', 'Failed / not usable', 'In the queue']) {
      if (!statText.includes(w)) { throw new Error('stat missing: ' + w + ' in ' + statText); }
    }
    const statNum = async k => parseInt(await page.textContent(`[data-wcp-pkg-stat="${k}"]`), 10);
    const installed0 = await statNum('installed');
    await page.click('[data-bs-target="#components"]');
    step('stats on top; Updates and Components tabs');

    const names = await page.$$eval('[data-wcp-pkg]', rows => rows.filter(r => r.querySelector('[data-wcp-pkg-install]')).map(r => r.getAttribute('data-wcp-pkg')));
    if (names.length < 2) { throw new Error('fewer than two components to install: ' + names); }
    const [a, b] = names;
    const row = n => `[data-wcp-pkg="${n}"]`;

    await page.click(`${row(a)} [data-wcp-pkg-install] button`);
    await page.waitForSelector(`${modal}.show [data-wcp-pkg-item="${a}"]`);
    step('first component queued; the queue window opened');
    await page.waitForTimeout(400);
    await page.keyboard.press('Escape');
    await page.waitForSelector(modal, { state: 'hidden' });

    await page.click(`${row(b)} [data-wcp-pkg-install] button`);
    await page.waitForSelector(`${modal}.show [data-wcp-pkg-item="${b}"]`);
    if (await page.locator(`${modal} [data-wcp-pkg-item]`).count() !== 2) { throw new Error('the queue does not list both components'); }
    step('second component queued; both listed');

    for (const n of [a, b]) {
      await page.waitForSelector(`${row(n)} [data-wcp-pkg-state] :text("Installed")`, { timeout: 30000 });
      await page.waitForSelector(`${modal} [data-wcp-pkg-item="${n}"] :text("Installed")`);
      if (await page.locator(`${row(n)} [data-wcp-pkg-install]`).count() !== 0) { throw new Error(n + ' still offers Install'); }
    }
    step('both rows show Installed; the queue shows both installed');
    if ((await page.textContent(`${modal} [data-wcp-pkg-log]`)).trim() === '') { throw new Error('no installation log shown'); }
    if (!(await page.evaluate(() => window.__noReload === 1))) { throw new Error('the page was reloaded'); }
    step('log shown; the page was never reloaded');

    await page.waitForTimeout(400);
    await page.keyboard.press('Escape');
    await page.waitForSelector(modal, { state: 'hidden' });
    await page.click('[data-wcp-pkg-show]');
    await page.waitForSelector(`${modal}.show [data-wcp-pkg-item="${a}"]`);
    step('the Queue button reopens the window');
    if ((await statNum('installed')) !== installed0 + 2) { throw new Error('installed stat not updated'); }

    // Two panes; pick an item to see its output.
    await page.click(`${modal} [data-wcp-pkg-item="${a}"]`);
    if (!(await page.textContent(`${modal} [data-wcp-pkg-logname]`)).includes(a)) { throw new Error('selected output not shown'); }
    // Add several components at once from inside the window.
    const more = await page.$$eval(`${modal} [data-wcp-pkg-add] input`, i => i.map(x => x.value));
    if (more.length >= 2) {
      await page.check(`${modal} [data-wcp-pkg-add] input[value="${more[0]}"]`);
      await page.check(`${modal} [data-wcp-pkg-add] input[value="${more[1]}"]`);
      await page.click(`${modal} [data-wcp-pkg-add-btn]`);
      for (const n of more.slice(0, 2)) {
        await page.waitForSelector(`${modal} [data-wcp-pkg-item="${n}"]`);
        await page.waitForSelector(`${modal} [data-wcp-pkg-item="${n}"] :text("Installed")`, { timeout: 30000 });
      }
      if (await page.$(`${modal} [data-wcp-pkg-add] input[value="${more[0]}"]`)) { throw new Error('an installed component is still offered'); }
      if (!(await page.isVisible(modal))) { throw new Error('the window closed'); }
      step('two more added at once from the open window and installed');
    } else {
      step('fewer than two components left to add (' + more.length + ')');
    }

    await page.waitForTimeout(400);
    await page.screenshot({ path: OUT + '/packages-queue.png', fullPage: false });
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK packages queue');
  } catch (e) {
    await page.screenshot({ path: OUT + '/packages-queue-failure.png', fullPage: true });
    console.error(e);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
