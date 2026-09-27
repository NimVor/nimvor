// End-to-end test of the job window (a component install, a backup, …):
// closed after the task ended, the page reloads so it shows the result (an
// installed component no longer offers "Install"); closed while the task
// runs, the task is followed quietly and a notice says to reload. The job
// endpoint is simulated, so the test does not depend on a real install.
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';
const JOB = 'abcdef0123456789';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  const step = (s) => console.log('STEP', s);
  // Bootstrap ignores Escape while the window is still fading in.
  const opened = async () => { await page.waitForSelector('#wcp-job.show'); await page.waitForTimeout(400); };
  let finished = false;
  await page.route('**/jobs/' + JOB + '*', route => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({ log: finished ? 'done\n' : '', offset: finished ? 5 : 0, done: finished,
      job: { status: finished ? 'succeeded' : 'running' } }),
  }));
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    await page.goto(BASE + '/packages');

    // Closed after the task ended: the page reloads.
    finished = true;
    await page.evaluate(id => { window.__before = 1; WhaCP.showJob(id); }, JOB);
    await page.waitForSelector('[data-wcp-job-status]:text("succeeded")');
    await opened();
    await Promise.all([page.waitForNavigation(), page.keyboard.press('Escape')]);
    if (await page.evaluate(() => window.__before === 1)) { throw new Error('the page was not reloaded'); }
    step('closed after the task ended: page reloaded');

    // Closed while running: no reload; a notice when it ends.
    finished = false;
    await page.evaluate(id => { window.__before = 1; WhaCP.showJob(id); }, JOB);
    await page.waitForSelector('[data-wcp-job-status]:text("running")');
    await opened();
    await page.keyboard.press('Escape');
    await page.waitForSelector('#wcp-job', { state: 'hidden' }).catch(() => {});
    finished = true;
    await page.waitForSelector('text=Reload the page to see the result', { timeout: 10000 });
    if (!(await page.evaluate(() => window.__before === 1))) { throw new Error('reloaded while the task was running'); }
    step('closed while running: followed quietly, notice at the end');

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK job window');
  } catch (e) {
    await page.screenshot({ path: OUT + '/jobmodal-failure.png', fullPage: true });
    console.error(e);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
