// Setup wizard: the checklist of what is done and left, a profile chosen,
// its steps run one by one from the browser (jobs followed in their logs),
// the result kept for next time, and the dashboard card. (Dev instance:
// system commands are simulated, so some steps fail on the fixture.)
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1300, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('console', m => { if (m.type() === 'error' && !/status of 4\d\d/.test(m.text())) { errors.push('console: ' + m.text()); } });
  const nav = click => Promise.all([page.waitForNavigation(), click()]);
  const step = s => console.log('STEP', s);
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await nav(() => page.click('button[type=submit]'));
    // Reached from the sidebar.
    await page.goto(BASE + '/components');
    await nav(() => page.click('a[href="/setup"]'));
    const checks = await page.$$eval('[data-check]', l => l.map(x => x.getAttribute('data-check')));
    for (const c of ['setup', 'components', 'two_factor', 'remote_backups', 'backup_key', 'account']) {
      if (!checks.includes(c)) { throw new Error('checklist lacks ' + c + ': ' + checks); }
    }
    step('checklist: ' + checks.join(', '));

    // Choosing a profile shows its steps.
    await page.check('#sp-minimal');
    const shown = await page.$$eval('[data-setup-steps]:not([hidden]) [data-step]', l => l.map(x => x.getAttribute('data-step')));
    if (shown.includes('mail') || !shown.includes('packages') || shown[shown.length - 1] !== 'profile') { throw new Error('minimal steps: ' + shown); }
    step('minimal profile: ' + shown.length + ' steps');

    // Without a PHP version nothing runs.
    for (const v of ['80', '81', '82', '83', '84']) { await page.uncheck('#sp-php-' + v); }
    await page.check('#sp-confirm');
    await page.click('[data-setup-run]');
    await page.waitForFunction(() => document.querySelector('[data-setup-summary]').textContent.includes('Choose at least one PHP version'));
    await page.check('#sp-php-83');

    // Run: every step ends done, already in place or failed.
    await page.click('[data-setup-run]');
    await page.waitForFunction(() => /The setup finished|Stopped/.test(document.querySelector('[data-setup-summary]').textContent), null, { timeout: 180000 });
    const summary = await page.textContent('[data-setup-summary]');
    if (summary.includes('Stopped')) { throw new Error(summary); }
    const rows = await page.$$eval('[data-setup-steps="minimal"] [data-step]', l => l.map(x => [x.getAttribute('data-step'), x.getAttribute('data-status'), x.querySelector('[data-step-detail]').textContent.trim()]));
    for (const [id, st] of rows) {
      if (!['ok', 'skip', 'fail'].includes(st)) { throw new Error('step ' + id + ' ended as "' + st + '"'); }
    }
    step('ran: ' + rows.map(r => r[0] + '=' + r[1]).join(' '));
    for (const r of rows.filter(x => x[1] === 'fail')) { console.log('  ', r[0], '-', r[2]); }
    const jobRows = await page.$$eval('[data-step] [data-step-job]:not([hidden])', l => l.length);
    if (jobRows === 0) { throw new Error('no step followed a job'); }
    step(jobRows + ' step(s) followed their job');
    const planRow = rows.find(r => r[0] === 'plans');
    if (!planRow || planRow[1] === 'fail') { throw new Error('plans: ' + planRow); }

    // Kept: the page shows the last setup and its results.
    await nav(() => page.click('[data-setup-reload]'));
    const main = (await page.textContent('main')).replace(/\s+/g, ' ');
    if (!main.includes('Last setup: Minimal')) { throw new Error('last setup not shown'); }
    if (!(await page.isChecked('#sp-minimal'))) { throw new Error('profile not remembered'); }
    const kept = await page.$$eval('[data-setup-steps="minimal"] [data-step]', l => l.filter(x => x.getAttribute('data-status') !== '').length);
    if (kept !== rows.length) { throw new Error('results not kept: ' + kept); }
    step('results kept');

    // Phone width.
    await page.setViewportSize({ width: 390, height: 844 });
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (over > 1) { throw new Error('horizontal scroll on a phone: ' + over + 'px'); }
    await page.setViewportSize({ width: 1300, height: 900 });
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK setup wizard');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/setup-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
