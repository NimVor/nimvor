// End-to-end test of the database browser UI. The dev agent simulates
// system commands (no real MariaDB), so the browser's API is answered here
// with canned data; the agent side is covered by its real-host tests. This
// checks rendering (NULL, binary, shortened values), sorting, filtering,
// paging, editing, inserting, deleting and the SQL console.
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

const COLS = [{ name: 'id', type: 'int(11)', nullable: false, key: 'PRI', default: null, extra: 'auto_increment', comment: '' },
  { name: 'name', type: 'varchar(50)', nullable: false, key: '', default: null, extra: '', comment: '' },
  { name: 'note', type: 'text', nullable: true, key: '', default: null, extra: '', comment: '' },
  { name: 'data', type: 'blob', nullable: true, key: '', default: null, extra: '', comment: '' }];
const RCOLS = [{ name: 'id', numeric: true }, { name: 'name' }, { name: 'note' }, { name: 'data', binary: true }];
const ROWS = [['1', 'apple', null, { hex: '00ff', size: 2, truncated: false }], ['2', 'banana', { text: 'long…', size: 5000, truncated: true }, null], ['3', '<b>x</b>', 'NULL', null]];

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [], calls = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  const last = (op) => { for (let i = calls.length - 1; i >= 0; i--) { if (calls[i].op === op) { return calls[i]; } } return null; };
  try {
    await page.route('**/databases/browse/api**', async (route) => {
      const body = JSON.parse(route.request().postData() || '{}');
      calls.push(body);
      const data = {
        tables: { database: 'e2e_db', server: '10.11.14-MariaDB', tables: [{ name: 'orders', view: false, engine: 'InnoDB', rows: 120, bytes: 16384, comment: '' }, { name: 'v_orders', view: true, rows: 0, bytes: 0 }] },
        structure: { table: 'orders', columns: COLS, indexes: [{ name: 'PRIMARY', unique: true, columns: 'id', type: 'BTREE' }], key: ['id'], create: 'CREATE TABLE `orders` (\n  `id` int(11) NOT NULL AUTO_INCREMENT\n)' },
        rows: { columns: RCOLS, rows: ROWS, keys: [{ id: '1' }, { id: '2' }, { id: '3' }], key: ['id'], total: 120, offset: body.offset || 0, limit: body.limit || 50, truncated: false, warnings: 0 },
        row: { columns: RCOLS, rows: [ROWS[0]], truncated: false },
        save: { affected_rows: 1, insert_id: body.insert ? 121 : 0 },
        delete: { deleted: (body.keys || []).length },
        query: { results: [{ columns: [{ name: '1', numeric: true }], rows: [['1']], truncated: false, warnings: 0 }, { affected_rows: 2, insert_id: 0, info: 'Rows matched: 2  Changed: 2  Warnings: 0' }], ms: 4 },
      }[body.op];
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true, data }) });
    });

    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    await page.goto(BASE + '/databases/browse?name=e2e_db');
    await page.waitForSelector('[data-table="orders"]');
    step('tables: ' + await page.locator('[data-table]').count());

    // Browse: rendering of special values is text, never HTML.
    await page.click('[data-table="orders"]');
    await page.waitForSelector('.wcp-db-grid tbody tr');
    if (await page.locator('.wcp-db-grid tbody tr').count() !== 3) { throw new Error('expected 3 rows'); }
    if (await page.locator('.wcp-db-grid .wcp-db-null').count() !== 3) { throw new Error('NULL cells (and only real NULLs) must be marked'); }
    if (!(await page.textContent('.wcp-db-grid')).includes('0x00ff')) { throw new Error('binary not shown as hex'); }
    if (await page.locator('.wcp-db-grid b').count() !== 0) { throw new Error('HTML was rendered'); }
    if (!(await page.textContent('.wcp-db-bar')).includes('1–3 of 120')) { throw new Error('pager text'); }
    await page.screenshot({ path: OUT + '/db-browser-grid.png' });
    step('grid rendered');

    // Sorting, filtering, paging.
    await page.click('.wcp-db-grid th button:has-text("name")');
    await page.waitForFunction(() => document.querySelector('.wcp-db-grid th[aria-sort]'));
    if (last('rows').order !== 'name' || last('rows').desc) { throw new Error('sort asc: ' + JSON.stringify(last('rows'))); }
    await page.click('.wcp-db-grid th button:has-text("name")');
    await page.waitForSelector('.wcp-db-grid th[aria-sort="descending"]');
    if (!last('rows').desc) { throw new Error('sort desc'); }
    await page.selectOption('.wcp-db-filter select >> nth=0', 'name');
    await page.selectOption('.wcp-db-filter select >> nth=1', 'like');
    await page.fill('.wcp-db-filter input', 'ap%');
    await page.click('.wcp-db-filter button[type=submit]');
    await page.waitForFunction(() => document.querySelectorAll('.wcp-db-filter button').length === 2);
    const f = last('rows').filter;
    if (!f || f.column !== 'name' || f.op !== 'like' || f.value !== 'ap%') { throw new Error('filter: ' + JSON.stringify(f)); }
    await page.click('[aria-label="Next page"]');
    await page.waitForFunction(() => (document.querySelector('.wcp-db-bar') || { textContent: '' }).textContent.includes('51–53'));
    if (last('rows').offset !== 50) { throw new Error('paging'); }
    step('sort, filter and paging requests');

    // Edit: only changed columns are sent; NULL checkbox sends null.
    await page.dblclick('.wcp-db-grid tbody tr >> nth=0');
    await page.waitForSelector('.wcp-db-editor');
    if (JSON.stringify(last('row').key) !== '{"id":"1"}') { throw new Error('row key'); }
    const nameInput = page.locator('.wcp-db-editor .row').nth(1).locator('input.form-control, textarea');
    await nameInput.fill('apricot');
    await page.locator('.wcp-db-editor .row').nth(3).locator('.form-control').fill('cafe');
    await page.click('.wcp-db-editor button[type=submit]');
    await page.waitForSelector('.wcp-db-grid');
    const saved = last('save');
    if (JSON.stringify(saved.values) !== '{"name":"apricot","data":{"hex":"cafe"}}' || JSON.stringify(saved.key) !== '{"id":"1"}') { throw new Error('save: ' + JSON.stringify(saved)); }
    step('row edited: ' + JSON.stringify(saved.values));

    // Delete two selected rows after confirmation.
    await page.check('.wcp-db-grid tbody tr >> nth=0 >> input[type=checkbox]');
    await page.check('.wcp-db-grid tbody tr >> nth=2 >> input[type=checkbox]');
    await page.click('[data-db-delete]');
    await page.waitForSelector('#db-dialog.show');
    await page.click('[data-db-dialog-ok]');
    await page.waitForFunction(() => !document.querySelector('#db-dialog.show'));
    await page.waitForTimeout(300);
    if (JSON.stringify(last('delete').keys) !== '[{"id":"1"},{"id":"3"}]') { throw new Error('delete: ' + JSON.stringify(last('delete'))); }
    step('two rows deleted');

    // Insert: the auto-increment column is left to the database.
    await page.click('[data-db-tab="insert"]');
    await page.waitForSelector('.wcp-db-editor');
    await page.locator('.wcp-db-editor .row').nth(1).locator('.form-control').fill('cherry');
    await page.click('.wcp-db-editor button[type=submit]');
    await page.waitForSelector('.wcp-db-grid');
    const ins = last('save');
    if (!ins.insert || JSON.stringify(ins.values) !== '{"name":"cherry"}') { throw new Error('insert: ' + JSON.stringify(ins)); }
    step('row inserted');

    // Structure.
    await page.click('[data-db-tab="structure"]');
    await page.waitForSelector('.wcp-db-create');
    if (!(await page.textContent('.wcp-db-create')).includes('CREATE TABLE `orders`')) { throw new Error('create statement'); }
    step('structure shown');

    // SQL console: results and messages; destructive SQL asks first.
    await page.click('[data-db-tab="sql"]');
    await page.fill('.wcp-db-sqltext', 'SELECT 1; UPDATE orders SET name = name WHERE id < 3');
    await page.press('.wcp-db-sqltext', 'Control+Enter');
    await page.waitForSelector('.wcp-db-result');
    const out = await page.textContent('.wcp-db-sql');
    if (!out.includes('2 row(s) affected') || !out.includes('Statement 1: 1 row(s)')) { throw new Error('query output: ' + out); }
    const before = calls.length;
    await page.fill('.wcp-db-sqltext', 'DROP TABLE orders');
    await page.click('.wcp-db-sql button:has-text("Run")');
    await page.waitForSelector('#db-dialog.show');
    await page.click('#db-dialog [data-bs-dismiss="modal"] >> nth=1');
    await page.waitForFunction(() => !document.querySelector('#db-dialog.show'));
    if (calls.length !== before) { throw new Error('cancelled DROP was sent'); }
    step('SQL console');
    await page.screenshot({ path: OUT + '/db-browser.png' });
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('database browser: ok');
  } catch (e) {
    await page.screenshot({ path: OUT + '/dbbrowser-failure.png', fullPage: true });
    console.error(e);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
