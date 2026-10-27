// End-to-end test of the file manager against a running dev panel
// (tests/e2e/run.sh starts one).
const { chromium } = require('playwright');
const fs = require('fs');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';
(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')\n' + (e.stack || '')));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);

  await page.goto(BASE + '/login');
  await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
  await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
  step('logged in: ' + page.url());

  await page.goto(BASE + '/websites/new');
  const dom = 'fm' + Date.now() + '.test'; await page.fill('#f-domain', dom);
  await page.selectOption('#f-kind', 'static');
  await Promise.all([page.waitForNavigation(), page.click('form button[type=submit].btn-primary')]);
  step('site created: ' + page.url());
  const m = page.url().match(/sites\/(\d+)/);
  if (!m) { throw new Error('website not created: ' + (await page.locator('.alert, .invalid-feedback').allTextContents()).join(' | ')); }
  const siteId = m[1];

  await page.goto(`${BASE}/sites/${siteId}/files`);
  await page.waitForSelector('[data-fm-rows] tr[data-path]');
  step('manager loaded, rows=' + await page.locator('[data-fm-rows] tr[data-path]').count() + ' path=' + await page.inputValue('[data-fm-path]'));
  await page.screenshot({ path: OUT + '/fm-1.png' });

  // New folder
  await page.click('[data-fm-cmd="newfolder"]');
  await page.fill('#fm-dialog input', 'assets');
  await page.click('[data-fm-dialog-ok]');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/assets"]');
  step('folder created');
  // tree shows it
  await page.waitForSelector('[data-fm-tree] li[data-path="public_html/assets"]');
  step('tree updated');

  // New file -> editor page
  await page.click('[data-fm-cmd="newfile"]');
  await page.fill('#fm-dialog input', 'hello.txt');
  await Promise.all([page.waitForNavigation(), page.click('[data-fm-dialog-ok]')]);
  step('editor: ' + page.url());
  await page.waitForSelector('[data-fe-text]:not([disabled])');
  await page.fill('[data-fe-text]', 'line one\nline two\n');
  await page.keyboard.press('Control+s');
  await page.waitForSelector('[data-fe-state].text-success');
  step('saved; gutter=' + JSON.stringify(await page.textContent('[data-fe-gutter]')));
  await page.screenshot({ path: OUT + '/fe.png' });
  await Promise.all([page.waitForNavigation(), page.click('[data-fe-close]')]);
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/hello.txt"]');
  step('back in folder: ' + await page.inputValue('[data-fm-path]'));

  // Upload via input
  fs.writeFileSync(OUT + '/up.css', 'body{color:red}');
  await page.setInputFiles('[data-fm-upload-input]', OUT + '/up.css');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/up.css"]', { timeout: 15000 });
  step('uploaded');

  // Rename via F2
  await page.click('[data-fm-rows] tr[data-path="public_html/up.css"] td.nim-fm-name');
  await page.focus('[data-fm-drop]');
  await page.keyboard.press('F2');
  await page.fill('#fm-dialog input', 'style.css');
  await page.click('[data-fm-dialog-ok]');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/style.css"]');
  step('renamed');

  // Permissions
  await page.click('[data-fm-rows] tr[data-path="public_html/style.css"] td.nim-fm-name');
  await page.click('[data-fm-cmd="perms"]');
  await page.fill('#fm-dialog input.font-monospace', '600');
  await page.click('[data-fm-dialog-ok]');
  await page.waitForFunction(() => { const r = document.querySelector('[data-fm-rows] tr[data-path="public_html/style.css"]'); return r && r.textContent.includes('0600'); });
  step('chmod 600');

  // Multi-select with ctrl and compress
  await page.click('[data-fm-rows] tr[data-path="public_html/hello.txt"] td.nim-fm-name');
  await page.click('[data-fm-rows] tr[data-path="public_html/style.css"] td.nim-fm-name', { modifiers: ['Control'] });
  step('status: ' + await page.textContent('[data-fm-status]'));
  await page.click('[data-fm-cmd="compress"]');
  await page.fill('#fm-dialog input', 'pack.zip');
  await page.click('[data-fm-dialog-ok]');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/pack.zip"]', { timeout: 20000 });
  step('compressed');

  // Extract into assets via context menu
  await page.click('[data-fm-rows] tr[data-path="public_html/pack.zip"] td.nim-fm-name', { button: 'right' });
  await page.click('[data-fm-menu] [data-fm-cmd="extract"]');
  await page.fill('#fm-dialog input.font-monospace', 'public_html/assets');
  await page.click('[data-fm-dialog-ok]');
  await page.waitForTimeout(2500);
  await page.dblclick('[data-fm-rows] tr[data-path="public_html/assets"] td.nim-fm-name');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/assets/hello.txt"]', { timeout: 15000 });
  step('extracted into assets');
  await page.screenshot({ path: OUT + '/fm-2.png' });

  // Up, search
  await page.click('[data-fm-cmd="up"]');
  await page.fill('[data-fm-search]', 'hello');
  await page.press('[data-fm-search]', 'Enter');
  await page.waitForSelector('[data-fm-banner]:not(.d-none)');
  step('search rows=' + await page.locator('[data-fm-rows] tr[data-path]').count());
  await page.click('[data-fm-banner] [data-fm-cmd="reload"]');
  await page.waitForSelector('[data-fm-banner].d-none', { state: 'attached' });

  // Delete via keyboard
  await page.click('[data-fm-rows] tr[data-path="public_html/pack.zip"] td.nim-fm-name');
  await page.focus('[data-fm-drop]');
  await page.keyboard.press('Delete');
  await page.click('[data-fm-dialog-ok]');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/pack.zip"]', { state: 'detached' });
  step('deleted (moved to the trash)');

  // Trash: listed with where it was, restored to its place
  await page.click('[data-fm-cmd="trash"]');
  await page.waitForSelector('#fm-trash.show');
  const trow = page.locator('#fm-trash [data-fm-trash-rows] tr', { hasText: 'pack.zip' });
  await trow.waitFor();
  if (!(await trow.textContent()).includes('/public_html')) { throw new Error('trash row lacks the folder: ' + await trow.textContent()); }
  await trow.locator('input[type=checkbox]').check();
  await page.click('[data-fm-trash-restore]');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/pack.zip"]', { timeout: 15000 });
  await page.waitForSelector('#fm-trash.show [data-fm-trash-rows] td[colspan]');
  step('restored from the trash');
  await page.click('#fm-trash .btn-close');
  await page.waitForSelector('#fm-trash', { state: 'hidden' });

  // Permanent delete skips the trash
  await page.click('[data-fm-rows] tr[data-path="public_html/pack.zip"] td.nim-fm-name');
  await page.click('[data-fm-cmd="delete"]');
  await page.check('#fm-dialog .form-check-input');
  await page.click('[data-fm-dialog-ok]');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/pack.zip"]', { state: 'detached' });
  await page.click('[data-fm-cmd="trash"]');
  await page.waitForSelector('#fm-trash.show [data-fm-trash-rows] td[colspan]');
  step('deleted permanently; the trash stays empty');
  await page.click('#fm-trash .btn-close');
  await page.waitForSelector('#fm-trash', { state: 'hidden' });

  // Empty trash asks first, then the trash window comes back empty
  await page.click('[data-fm-rows] tr[data-path="public_html/assets"] td.nim-fm-name');
  await page.click('[data-fm-cmd="delete"]');
  await page.click('[data-fm-dialog-ok]');
  await page.waitForSelector('[data-fm-rows] tr[data-path="public_html/assets"]', { state: 'detached' });
  await page.click('[data-fm-cmd="trash"]');
  await page.locator('#fm-trash [data-fm-trash-rows] tr', { hasText: 'assets' }).waitFor();
  await page.click('[data-fm-trash-empty]');
  await page.waitForSelector('#fm-dialog.show');
  if (!(await page.textContent('#fm-dialog')).includes('cannot be undone')) { throw new Error('empty trash without a warning'); }
  await page.click('[data-fm-dialog-ok]');
  await page.waitForSelector('#fm-trash.show [data-fm-trash-rows] td[colspan]');
  step('trash emptied after confirmation');
  await page.screenshot({ path: OUT + '/fm-trash.png' });
  await page.keyboard.press('Escape');

  // Mobile width screenshot
  await page.setViewportSize({ width: 390, height: 800 });
  await page.screenshot({ path: OUT + '/fm-mobile.png' });
  await browser.close();
  if (errors.length) { console.error('JavaScript errors:', errors); process.exit(1); }
  console.log('file manager: ok');
})().catch(e => { console.error('FAILED', e.message); process.exit(1); });
