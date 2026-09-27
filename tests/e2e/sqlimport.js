// End-to-end test of importing a .sql file into a website's database:
// the file is uploaded in chunks into the website's tmp/ folder, the
// import job starts (the dev instance's simulated mysql client fails it,
// which is fine here) and the uploaded file is removed afterwards. A
// wrong file type is refused in the browser. (The database record is
// inserted directly: the dev instance cannot create real databases.)
const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';
const DEV = path.resolve(__dirname, '../../.dev');

(async () => {
  const browser = await browserLaunch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error' && !/status of 4\d\d/.test(m.text())) { errors.push('console: ' + m.text()); } });
  const step = (s) => console.log('STEP', s);
  const stamp = Date.now().toString(36);
  const domain = 'sql' + stamp + '.example.test';
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    await page.goto(BASE + '/websites/new');
    await page.fill('#f-domain', domain);
    await Promise.all([page.waitForNavigation(), page.click('main form[action="/websites/new"] button[type=submit]')]);
    const siteId = execFileSync('sqlite3', [path.join(DEV, 'whacp.sqlite'), `SELECT id FROM websites WHERE domain = '${domain}'`]).toString().trim();
    const dbName = 'imp_' + stamp.slice(-6);
    execFileSync('sqlite3', [path.join(DEV, 'whacp.sqlite'), `INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ${siteId}, NULL, '${dbName}', '2026-01-01')`]);
    step('website ' + domain + ' with database ' + dbName);

    await page.goto(BASE + '/sites/' + siteId + '/databases');
    const row = `#db-table tr:has-text("${dbName}")`;
    await page.click(`${row} [data-wcp-import] summary`);
    // A wrong file type is refused before anything is uploaded.
    const bad = path.join(OUT, 'notes.txt');
    fs.writeFileSync(bad, 'hello');
    await page.setInputFiles(`${row} [data-wcp-import-file]`, bad);
    await page.click(`${row} [data-wcp-import] button[type=submit]`);
    await page.waitForSelector(`${row} [data-wcp-import-error]:not(.d-none)`);
    step('a .txt file is refused in the browser');

    // A 2.5 MB dump: three chunks.
    const dump = path.join(OUT, 'shop-dump.sql');
    fs.writeFileSync(dump, 'CREATE TABLE t (a INT);\n' + ('INSERT INTO t VALUES (1);\n'.repeat(100000)));
    await page.setInputFiles(`${row} [data-wcp-import-file]`, dump);
    await Promise.all([page.waitForNavigation({ timeout: 60000 }), page.click(`${row} [data-wcp-import] button[type=submit]`)]);
    const flash = (await page.textContent('.alert-success, .alert-danger') || '').trim();
    if (!flash.includes('Import into ' + dbName + ' started')) { throw new Error('flash: ' + flash); }
    step('uploaded in chunks; the import job started');

    // The job ends (the simulated client fails it) and the upload is removed.
    const tmp = path.join(DEV, 'root/home/whacp', domain, 'tmp');
    const deadline = Date.now() + 20000;
    let left = null;
    while (Date.now() < deadline) {
      left = fs.readdirSync(tmp).filter(f => f.startsWith('whacp-import-'));
      if (left.length === 0) { break; }
      await page.waitForTimeout(500);
    }
    if (left.length !== 0) { throw new Error('upload not removed: ' + left); }
    step('the uploaded file was removed after the import');

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK sql import');
  } catch (e) {
    await page.screenshot({ path: OUT + '/sqlimport-failure.png', fullPage: true }).catch(() => {});
    console.error(e);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();

function browserLaunch() { return chromium.launch(); }
