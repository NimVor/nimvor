// End-to-end test of the backup storage form: choosing a type shows its
// fields and disables the others (so hidden required fields do not block
// submission), and a local folder outside the allowed roots is refused.
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
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    await page.goto(BASE + '/backups');
    await page.click('summary:has-text("Add storage")');
    const state = async () => page.evaluate(() => {
      const f = document.querySelector('#st-type').form;
      const vis = (n) => { const el = f.elements[n]; return !!el && !el.disabled && el.offsetParent !== null; };
      return ['endpoint', 'host', 'password', 'tls', 'private_key', 'client_id', 'path', 'prefix'].filter(vis).join(',');
    });
    const expect = async (type, want) => {
      await page.selectOption('#st-type', type);
      const got = await state();
      if (got !== want) { throw new Error(type + ': visible fields ' + got + ', want ' + want); }
      step(type + ': ' + got);
    };
    await expect('s3', 'endpoint,prefix');
    await expect('sftp', 'host,password,private_key,prefix');
    await expect('ftp', 'host,password,tls,prefix');
    await expect('gdrive', 'client_id,prefix');
    await expect('local', 'path,prefix');

    await page.fill('input[name=name]', 'E2E disk');
    await page.fill('input[name=path]', '/etc/whacp');
    await Promise.all([page.waitForNavigation(), page.click('button:has-text("Test and save")')]);
    await page.waitForSelector('.invalid-feedback');
    if (await page.inputValue('#st-type') !== 'local') { throw new Error('type not kept after validation error'); }
    if (!(await state()).startsWith('path')) { throw new Error('local fields not shown after validation error'); }
    step('local path outside allowed roots refused');
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('backup storage form: ok');
  } catch (e) {
    await page.screenshot({ path: OUT + '/backupstorage-failure.png', fullPage: true });
    console.error(e);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
