// End-to-end test of PHP → version → Settings: the three tabs render
// without script or style errors (strict CSP), a php.ini value saved
// through the form shows up in the text editor, the editor refuses a
// setting that is not allowed, and a server-wide PHP-FPM setting is kept.
// (The dev instance simulates system commands.)
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  const flash = async () => (await page.textContent('.alert-success, .alert-danger') || '').trim();
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    await page.goto(BASE + '/php');
    await Promise.all([page.waitForNavigation(), page.click('a[href="/php/8.3"]')]);
    for (const tab of ['extensions', 'ini', 'fpm']) {
      const res = await page.goto(BASE + '/php/8.3?tab=' + tab);
      if (res.status() !== 200) { throw new Error(tab + ': HTTP ' + res.status()); }
    }
    step('PHP page links to the settings; all three tabs load');

    await page.goto(BASE + '/php/8.3?tab=ini');
    await page.fill('input[name="ini[max_input_vars]"]', '5000');
    await page.selectOption('select[name="ini[expose_php]"]', 'Off');
    await Promise.all([page.waitForNavigation(), page.click('form:has(input[name=mode][value=form]) button')]);
    if (!(await flash()).includes('saved')) { throw new Error('form save: ' + await flash()); }
    const text = await page.inputValue('textarea[name=content]');
    if (!text.includes('max_input_vars = 5000') || !text.includes('expose_php = Off')) { throw new Error('editor: ' + JSON.stringify(text)); }
    step('php.ini form saved; the editor shows the settings');

    await page.fill('textarea[name=content]', text + 'extension = /tmp/evil.so\n');
    await Promise.all([page.waitForNavigation(), page.click('form:has(input[name=mode][value=text]) button')]);
    if (!(await flash()).includes('extension cannot be set here')) { throw new Error('editor refusal: ' + await flash()); }
    if ((await page.inputValue('textarea[name=content]')).includes('evil')) { throw new Error('refused setting kept'); }
    step('the editor refuses extension=');

    await page.goto(BASE + '/php/8.3?tab=fpm');
    await page.fill('#fpm-process_max', '128');
    await page.selectOption('#fpm-log_level', 'warning');
    await Promise.all([page.waitForNavigation(), page.click('form[action$="/fpm"] button')]);
    if (!(await flash()).includes('saved') || await page.inputValue('#fpm-process_max') !== '128') { throw new Error('fpm save: ' + await flash()); }
    step('server-wide PHP-FPM setting saved');

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK php settings');
  } catch (e) {
    await page.screenshot({ path: OUT + '/phpsettings-failure.png', fullPage: true });
    console.error(e);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
