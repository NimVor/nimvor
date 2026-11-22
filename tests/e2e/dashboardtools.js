// End-to-end test of the dashboard's widgets and service widget:
// - the dashboard shows the Disk and Backups widgets and no longer has the tools grid
// - the Services widget has start/stop and restart buttons that work
//   without leaving the page (one row per service)
// No script or CSP errors.
const path = require('path');
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')\n' + (e.stack || '')));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    // A layout left over from an earlier run is put back to the default first.
    if (await page.locator('button:has-text("Restore default")').count()) {
      await page.click('button:has-text("Restore default")');
      await page.waitForSelector('#nim-confirm.show [data-nim-confirm-ok]', { timeout: 5000 });
      await Promise.all([page.waitForNavigation(), page.click('#nim-confirm [data-nim-confirm-ok]')]);
    }

    if (await page.locator('.nim-tools, .nim-tool-card').count() !== 0) { throw new Error('the tools grid is still on the dashboard'); }
    for (const w of ['disk', 'backups']) {
      if (await page.locator('[data-widget="' + w + '"]').count() === 0) { throw new Error('widget ' + w + ' missing'); }
    }
    step('widgets: disk and backups, no tools grid');

    // A measured widget is loaded after the page and ends in a real state, never stuck on "Loading".
    await page.waitForSelector('[data-widget="top_processes"]:not([data-state="loading"])', { timeout: 30000 });
    const tpState = await page.getAttribute('[data-widget="top_processes"]', 'data-state');
    if (!['ok', 'empty', 'unavailable', 'error'].includes(tpState)) { throw new Error('top processes widget state: ' + tpState); }
    step('top processes widget loaded (' + tpState + ')');

    // Performance history: the chart is drawn from what the monitor recorded; with no history it says so instead of drawing anything.
    // (A new server has no history yet: the plot is then empty and a note says so, which is also a valid state.)
    await page.waitForSelector('[data-widget="performance_history"][data-state="ok"] [data-nim-chart][data-mounted="1"]', { timeout: 15000 });
    await page.waitForFunction(() => {
        const box = document.querySelector('[data-widget="performance_history"] [data-nim-chart]');
        return box && (box.querySelector('svg path.line') || (box.querySelector('.nim-chart-note') && !box.querySelector('.nim-chart-note').hidden));
    }, null, { timeout: 15000 });
    for (const tab of ['mem', 'load', 'net', 'cpu']) {
        await page.click('[data-widget="performance_history"] [data-chart-tab="' + tab + '"]');
        const shown = await page.$eval('[data-widget="performance_history"] [data-chart-pane]:not([hidden])', n => n.getAttribute('data-chart-pane'));
        if (shown !== tab) { throw new Error('the performance tab ' + tab + ' shows the readings of ' + shown); }
    }
    await page.click('[data-widget="performance_history"] [data-range-button="7d"]');
    if (await page.$eval('[data-widget="performance_history"] [data-range-button="7d"]', b => b.getAttribute('aria-pressed')) !== 'true') { throw new Error('the 7 d range is not selected'); }
    await page.click('[data-widget="performance_history"] [data-range-button="24h"]');
    step('performance history: tabs and ranges work');

    // Customize: hide the Disk widget, then restore the default layout.
    await page.click('button:has-text("Customize")');
    await page.waitForSelector('#nim-customize.show', { timeout: 5000 });
    const firstId = await page.$eval('#nim-customize [data-customize-row] input[name="order[]"]', i => i.value);
    await page.click('#nim-customize [data-customize-row]:first-child [data-customize-move="1"]');
    const afterMove = await page.$eval('#nim-customize [data-customize-row] input[name="order[]"]', i => i.value);
    if (afterMove === firstId) { throw new Error('the row did not move'); }
    await page.uncheck('#cw-disk');
    await Promise.all([page.waitForNavigation(), page.click('#nim-customize button[type=submit]:has-text("Save")')]);
    if (await page.locator('[data-widget-host="disk"]').count() !== 0) { throw new Error('a hidden widget is still shown'); }
    step('customize: moved a widget and hid Disk');
    await page.click('button:has-text("Restore default")');
    await page.waitForSelector('#nim-confirm.show [data-nim-confirm-ok]', { timeout: 5000 });
    await Promise.all([page.waitForNavigation(), page.click('#nim-confirm [data-nim-confirm-ok]')]);
    await page.waitForSelector('[data-widget-host="disk"]', { timeout: 10000 });
    step('customize: default layout restored');

    // Arrange: drag the second widget in front of the first one, widen another by its edge, and both are kept after a reload.
    // Layout shifts from widgets still loading would move the drop target, so wait until all are drawn.
    await page.waitForFunction(() => !document.querySelector('[data-widget-host] [data-state="loading"]'), null, { timeout: 40000 });
    const savedAfter = () => page.waitForResponse(r => r.url().endsWith('/dashboard/layout/arrange') && r.status() === 200, { timeout: 15000 }).catch(() => null);
    const ids = () => page.$$eval('[data-widgets] [data-widget-host]', hs => hs.map(h => h.getAttribute('data-widget-host')));
    const before = await ids();
    await page.click('[data-arrange-toggle]:has-text("Arrange")');
    await page.waitForSelector('[data-widgets].is-arranging .nim-resize-handle', { timeout: 5000 });
    // The second widget is dropped on the top-left corner of the first: it takes the first one's place.
    const a = page.locator('[data-widget-host="' + before[1] + '"]'), b = page.locator('[data-widget-host="' + before[0] + '"]');
    const saved = savedAfter();
    await a.dragTo(b, { targetPosition: { x: 10, y: 10 } });
    if (!await saved) { throw new Error('arrange: dragging a widget did not save the layout (order now: ' + (await ids()).slice(0, 4).join(',') + ')'); }
    if ((await ids())[0] !== before[1]) { throw new Error('the dragged widget did not move'); }
    // Width: the S/M/L/Full buttons, and the edge dragged to the left (a widget's edge can only be dragged as far as the screen).
    const sized = page.locator('[data-widget-host="' + before[3] + '"]');
    await sized.scrollIntoViewIfNeeded();
    const saved2 = savedAfter();
    await sized.locator('[data-size-set="full"]').click();
    if (!await saved2) { throw new Error('arrange: the width button did not save the layout'); }
    if (await sized.getAttribute('data-size') !== 'full') { throw new Error('the width button did not change the width'); }
    // The toast from the save sits at the bottom right of the screen, right where a full-width widget's edge is.
    await page.evaluate(() => document.querySelectorAll('#nim-toasts .toast').forEach(t => t.remove()));
    const handle = sized.locator('[data-resize-handle]');
    await handle.scrollIntoViewIfNeeded();
    const hb = await handle.boundingBox();
    const grid = await page.locator('[data-widgets]').boundingBox();
    const y = Math.min(Math.max(hb.y + 30, 30), 850, hb.y + hb.height - 5); // a point of the handle that is on screen
    const x = hb.x + hb.width / 2;
    const hit = await page.evaluate(([px, py]) => { const e = document.elementFromPoint(px, py); return e ? (e.tagName + '.' + String(e.className)) : 'nothing'; }, [x, y]);
    const saved3 = savedAfter();
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x - grid.width * 0.5, y, { steps: 10 });
    await page.mouse.up();
    if (!await saved3) { throw new Error('arrange: dragging the edge did not save the layout (pointer at ' + Math.round(x) + ',' + Math.round(y) + ' hit ' + hit + '; size ' + await sized.getAttribute('data-size') + ')'); }
    if (await sized.getAttribute('data-size') === 'full') { throw new Error('dragging the edge did not change the width'); }
    const moved = await ids(), size2 = await sized.getAttribute('data-size');
    await page.reload();
    if ((await ids()).join() !== moved.join()) { throw new Error('the dragged order was not kept'); }
    if (await page.locator('[data-widget-host="' + before[3] + '"]').getAttribute('data-size') !== size2) { throw new Error('the width was not kept'); }
    step('arrange: drag to move and edge to resize, both kept after reload');
    await page.click('button:has-text("Restore default")');
    await page.waitForSelector('#nim-confirm.show [data-nim-confirm-ok]', { timeout: 5000 });
    await Promise.all([page.waitForNavigation(), page.click('#nim-confirm [data-nim-confirm-ok]')]);
    await page.waitForSelector('[data-widget-host="disk"]', { timeout: 10000 });

    await page.waitForSelector('[data-services] > li:not(:has(.placeholder))', { timeout: 20000 });
    if (await page.locator('[data-services] .nim-service-btn').count() === 0) {
      // The development instance simulates systemd: no services to list.
      step('services widget: no services here (simulated system)');
      if (errors.length) { throw new Error(errors.join('\n')); }
      console.log('OK dashboard tools');
      return;
    }
    const units = await page.$$eval('[data-services] > li', lis => lis.map(li => li.firstChild.textContent.trim()));
    if (new Set(units).size !== units.length) { throw new Error('a service is listed twice: ' + units.join(', ')); }
    const url = page.url();
    const btn = page.locator('[data-services] button[aria-label^="Restart"]').first();
    const label = await btn.getAttribute('aria-label');
    await btn.click();
    await page.waitForSelector('.toast, [role=alert], [role=status]', { timeout: 15000 });
    if (page.url() !== url) { throw new Error('the restart left the page'); }
    await page.screenshot({ path: path.join(OUT, 'dashboard-services.png') });
    step('services widget: ' + label + ' without leaving the page');

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK dashboard tools');
  } catch (e) {
    await page.screenshot({ path: path.join(OUT, 'dashboardtools-failure.png'), fullPage: true }).catch(() => {});
    console.error(e.message);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
