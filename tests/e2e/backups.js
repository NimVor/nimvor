// Backups page: tabs (Website backups, Server configuration, Remote
// storage, Account backups) and windows. A server backup creates the backup
// key, which is downloaded protected by a passphrase (after the panel
// password). A local remote storage is added, tested, turned off and on and
// edited. A hosting account is backed up to one file, downloaded, then
// terminated and restored from that file: the account and its website come
// back. (Dev instance: system commands are simulated.)
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';
const DEV_ROOT = process.env.E2E_DEV_ROOT || path.join(__dirname, '../../.dev/root');

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1300, height: 900 }, acceptDownloads: true });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')'));
  page.on('console', m => { if (m.type() === 'error' && !/status of 4\d\d/.test(m.text())) errors.push('console: ' + m.text() + ' (' + page.url() + ')'); });
  const step = s => console.log('STEP', s);
  const main = async () => (await page.textContent('main')).replace(/\s+/g, ' ');
  const nav = click => Promise.all([page.waitForNavigation(), click()]);
  const tab = async id => { await page.click(`[data-bs-target="#${id}"]`); await page.waitForSelector(`#${id}.active`); };
  const until = async (fn, what, ms = 40000) => {
    const end = Date.now() + ms;
    while (Date.now() < end) {
      if (await fn()) { return; }
      await page.waitForTimeout(1000);
    }
    throw new Error('timed out: ' + what);
  };
  const stamp = Date.now().toString(36).slice(-5);
  // The job log window opens after a job starts: close it.
  const closeJob = async () => {
    if (await page.waitForSelector('#wcp-job.show', { timeout: 2000 }).catch(() => null)) {
      await page.click('#wcp-job .btn-close');
      await page.waitForSelector('#wcp-job', { state: 'hidden' });
    }
  };
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await nav(() => page.click('button[type=submit]'));

    // A hosting account with a website.
    const user = 'ab' + stamp;
    const domain = 'acct' + stamp + '.example.test';
    await page.goto(BASE + '/accounts/new');
    await page.fill('#a-domain', domain);
    await page.fill('#a-username', user);
    await page.click('[data-wcp-genpass="#a-password"]');
    await page.fill('#a-email', user + '@example.test');
    await nav(() => page.click('main form button[type=submit]:has-text("Create account")'));
    if (!/\/users\/\d+\/edit$/.test(page.url())) { throw new Error('account not created: ' + (await main()).slice(0, 300)); }
    step('account ' + user + ' with ' + domain);

    await page.goto(BASE + '/backups');
    const tabs = await page.$$eval('.wcp-subtabs [role=tab]', b => b.map(x => x.textContent.replace(/\s+[\d/]+\s*$/, '').trim()));
    if (JSON.stringify(tabs) !== JSON.stringify(['Website backups', 'Server configuration', 'Remote storage', 'Account backups'])) { throw new Error('tabs: ' + JSON.stringify(tabs)); }
    step('tabs in order');

    // Server configuration: back up in a window; the key appears.
    await tab('server');
    await page.click('button[data-bs-target="#server-backup"]');
    await page.waitForSelector('#server-backup.show');
    await Promise.all([page.waitForLoadState('networkidle'), page.click('#server-backup button.btn-primary')]);
    await until(async () => { await page.goto(BASE + '/backups#server'); return !!(await page.$('button[data-bs-target="#key-download"]')); }, 'backup key created');
    await closeJob();
    await page.click('button[data-bs-target="#server-schedule"]');
    await page.waitForSelector('#server-schedule.show');
    await page.click('#server-schedule .btn-close');
    await page.waitForSelector('#server-schedule', { state: 'hidden' });
    step('server backup and schedule in windows; key created');

    // Key download: wrong panel password refused; then a passphrase-protected file.
    await page.click('button[data-bs-target="#key-download"]');
    await page.waitForSelector('#key-download.show');
    await page.fill('#kd-pass', 'a long passphrase for e2e');
    await page.fill('#kd-pass2', 'a long passphrase for e2e');
    await page.fill('#kd-cur', 'wrong-password');
    await nav(() => page.click('#key-download button.btn-primary'));
    if (!(await main()).includes('panel password is not correct')) { throw new Error('wrong password not refused: ' + (await main()).slice(0, 300)); }
    await page.goto(BASE + '/backups#key-download');
    await page.waitForSelector('#key-download.show');
    await page.fill('#kd-pass', 'a long passphrase for e2e');
    await page.fill('#kd-pass2', 'a long passphrase for e2e');
    await page.fill('#kd-cur', 'dev-password-change-me');
    const [dl] = await Promise.all([page.waitForEvent('download'), page.click('#key-download button.btn-primary')]);
    const keyFile = JSON.parse(fs.readFileSync(await dl.path(), 'utf8'));
    if (keyFile.format !== 'whacp-backup-key' || !keyFile.ciphertext || keyFile.kdf !== 'pbkdf2-sha256' || !/^whacp-backup-key-[0-9a-f]+\.json$/.test(dl.suggestedFilename())) { throw new Error('key file: ' + JSON.stringify(keyFile)); }
    step('backup key downloaded, encrypted with the passphrase');

    // Remote storage: a local folder, tested, off/on, edited.
    fs.mkdirSync(path.join(DEV_ROOT, 'mnt/whacp-e2e-' + stamp), { recursive: true });
    await page.goto(BASE + '/backups');
    await tab('storages');
    await page.click('button[data-bs-target="#storage-add"]');
    await page.waitForSelector('#storage-add.show');
    await page.selectOption('#sa-type', 'local');
    const sname = 'Disk ' + stamp;
    await page.fill('#storage-add input[name=name]', sname);
    await page.fill('#storage-add input[name=path]', '/mnt/whacp-e2e-' + stamp);
    await nav(() => page.click('#storage-add button:has-text("Test and add")'));
    await tab('storages');
    const row = `tr[data-storage="${sname}"]`;
    if (!(await page.textContent(row)).includes('Passed') || !(await page.textContent(row)).includes('On')) { throw new Error('storage not on after adding: ' + await page.textContent(row)); }
    await nav(() => page.click(`${row} button:has-text("Test")`));
    await tab('storages');
    await nav(() => page.click(`${row} button:has-text("Turn off")`));
    await tab('storages');
    if (!(await page.textContent(row)).includes('Off')) { throw new Error('not turned off'); }
    if (await page.$(`#server-backup option:has-text("${sname}")`)) { throw new Error('a storage turned off is offered for new backups'); }
    await nav(() => page.click(`${row} button:has-text("Turn on")`));
    await tab('storages');
    await page.click(`${row} button:has-text("Edit")`);
    const editId = await (await page.waitForSelector('.modal.show[id^="storage-edit-"]')).evaluate(m => m.id);
    await page.fill(`#${editId} input[name=name]`, sname + ' (edited)');
    await nav(() => page.click(`#${editId} button:has-text("Test and save")`));
    await tab('storages');
    if (!(await page.$(`tr[data-storage="${sname} (edited)"]`))) { throw new Error('storage edit not saved: ' + (await main()).slice(0, 300)); }
    step('local storage added, tested, turned off and on, edited');

    // Account backup: in a window; the file is listed and downloads.
    await tab('accounts');
    await page.click('button[data-bs-target="#account-backup"]');
    await page.waitForSelector('#account-backup.show');
    const optVal = await page.$eval('#ab-user', (sel, u) => [...sel.options].find(o => o.textContent.startsWith(u + ' ')).value, user);
    await page.selectOption('#ab-user', optVal);
    await page.fill('#ab-label', 'e2e ' + stamp);
    await Promise.all([page.waitForLoadState('networkidle'), page.click('#account-backup button.btn-primary')]);
    const fileRow = `tr[data-account-file="${user}"]`;
    await until(async () => { await page.goto(BASE + '/backups#accounts'); return !!(await page.$(fileRow)); }, 'account backup file listed');
    await closeJob();
    if (!(await page.textContent(fileRow)).includes(domain)) { throw new Error('the file does not list the website: ' + await page.textContent(fileRow)); }
    const [adl] = await Promise.all([page.waitForEvent('download'), page.click(`${fileRow} a[title="Download"]`)]);
    if (!adl.suggestedFilename().endsWith('.whacp-account') || fs.statSync(await adl.path()).size < 1024) { throw new Error('account file download'); }
    step('account backed up to one file; downloaded');

    // Terminate the account.
    await page.goto(BASE + '/users');
    await page.check(`input[name="ids[]"][aria-label="Select ${user}"]`);
    await page.click('button:has-text("Terminate selected")');
    await page.waitForSelector('#t-confirm');
    await page.fill('#t-confirm', 'TERMINATE');
    await page.click('#terminate-confirm button.btn-danger');
    await page.waitForFunction(() => !document.querySelector('#t-confirm'), null, { timeout: 60000 });
    await page.waitForLoadState('networkidle');
    await page.goto(BASE + '/users');
    if ((await main()).includes(user)) { throw new Error('account not terminated'); }
    step('account terminated');

    // Restore it from the file.
    await page.goto(BASE + '/backups#accounts');
    await nav(() => page.click(`${fileRow} a:has-text("Restore")`));
    if (!(await main()).includes('will be created')) { throw new Error('preview: ' + (await main()).slice(0, 400)); }
    await page.fill('#ar-confirm', user);
    await nav(() => page.click('main button:has-text("Check and import")'));
    await page.waitForSelector('main button:has-text("Recreate the account")', { timeout: 60000 });
    await closeJob();
    page.once('dialog', d => d.accept());
    await page.click('main button:has-text("Recreate the account")');
    const ok = await page.waitForSelector('#wcp-confirm.show [data-wcp-confirm-ok]', { timeout: 3000 }).catch(() => null);
    if (ok) { await nav(() => ok.click()); }
    await page.waitForSelector('.wcp-restore-report', { timeout: 60000 });
    await closeJob();
    const report = await main();
    if (!report.includes('Account ' + user + ' created') || !report.includes('Website ' + domain + ' created') || !report.includes('Restoring the files')) { throw new Error('report: ' + report.slice(0, 900)); }
    if (!(await page.$('[data-wcp-secret]'))) { throw new Error('the new password is not shown'); }
    await page.reload();
    if (await page.$('[data-wcp-secret]')) { throw new Error('the new password is shown twice'); }
    await page.goto(BASE + '/websites');
    if (!(await main()).includes(domain)) { throw new Error('website not back'); }
    step('account restored from the file after termination: account, website, files');

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(BASE + '/backups');
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (over > 1) { throw new Error('horizontal scroll on a phone: ' + over + 'px'); }
    step('fits a phone');
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK backups');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/backups-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
