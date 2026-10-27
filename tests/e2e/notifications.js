// End-to-end test of notifications: the top bar's unread count, the
// dashboard's failures card, the notifications page (filters, marking read,
// escaping) and its layout on a phone. Notifications are written straight
// into the dev instance's database; how they are produced is covered by the
// panel's tests.
const { chromium } = require('playwright');
const { execFileSync } = require('child_process');
const path = require('path');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';
const DB = path.resolve(__dirname, '../../.dev/nimvor.sqlite');

function seed() {
  execFileSync('php', ['-r', `
    $db = new PDO('sqlite:' . $argv[1]);
    $now = gmdate('c');
    // Other tests of the run leave notifications behind: start from none.
    $db->exec("DELETE FROM notification_reads");
    $db->exec("DELETE FROM notifications");
    $s = $db->prepare("INSERT INTO notifications (server_id, kind, severity, title, body, link, dedupe_key, created_at) VALUES (1, 'job', ?, ?, ?, ?, ?, ?)");
    $s->execute(['error', 'Backup shop.test failed <img src=x onerror=alert(1)>', 'upload refused', '/backups', 'e2e:1', $now]);
    $s->execute(['success', 'Deploy shop.test finished', '', '', 'e2e:2', $now]);
    $s->execute(['warning', 'Certificate shop.test expires in 5 day(s).', '', '/monitoring', 'e2e:3', $now]);
  `, DB]);
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')\n' + (e.stack || '')));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  page.on('dialog', d => { errors.push('dialog opened: ' + d.message()); d.dismiss(); });
  const step = (s) => console.log('STEP', s);
  const count = async () => (await page.locator('[data-nim-bell-count]').isVisible()) ? (await page.textContent('[data-nim-bell-count]')).trim() : '0';
  try {
    seed();
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    if (await count() !== '3') { throw new Error('bell count: ' + await count()); }
    if (!(await page.textContent('body')).includes('Unread failures')) { throw new Error('dashboard failures card missing'); }
    step('bell and dashboard card');

    // The bell opens a list loaded over AJAX; marking read stays on the page.
    await page.click('[data-nim-bell]');
    await page.waitForSelector('[data-nim-bell-list] .nim-bell-item');
    if (await page.locator('[data-nim-bell-list] .nim-bell-item.is-unread').count() !== 3) { throw new Error('dropdown: expected 3 unread'); }
    if (await page.locator('[data-nim-bell-list] img').count() !== 0) { throw new Error('HTML in a title was rendered in the dropdown'); }
    const url = page.url();
    await page.click('[data-nim-bell-list] .nim-bell-item:has-text("Deploy shop.test finished") button[aria-label^="Mark as read"]');
    await page.waitForFunction(() => document.querySelector('[data-nim-bell-count]').textContent.trim() === '2');
    if (page.url() !== url) { throw new Error('marking read in the dropdown left the page'); }
    if (await page.locator('[data-nim-bell-list] .nim-bell-item.is-unread').count() !== 2) { throw new Error('dropdown not updated after marking read'); }
    if (await page.locator('[data-nim-bell-list] .nim-bell-item').count() !== 2) { throw new Error('a read notification stays in the dropdown'); }
    await page.screenshot({ path: OUT + '/notifications-bell.png' });
    step('bell dropdown: list, escaped, mark one read');

    await page.click('[data-nim-bell-menu] a:has-text("View all notifications")');
    await page.waitForSelector('[data-nim-notifications]');
    if (await page.locator('[data-nim-notifications] > li').count() !== 2) { throw new Error('expected 2 unread'); }
    if (await page.locator('[data-nim-notifications] img').count() !== 0) { throw new Error('HTML in a title was rendered'); }
    await page.screenshot({ path: OUT + '/notifications.png' });
    step('page lists unread, escaped');

    await page.selectOption('select[name=severity]', 'error');
    await page.waitForURL(/severity=error/);
    if (await page.locator('[data-nim-notifications] > li').count() !== 1) { throw new Error('severity filter'); }
    await Promise.all([page.waitForNavigation(), page.click('[data-nim-notifications] button[aria-label="Mark read"]')]);
    if (!page.url().includes('severity=error')) { throw new Error('filter lost after marking read: ' + page.url()); }
    if (await count() !== '1') { throw new Error('count after one read: ' + await count()); }
    step('filter and mark one read');

    await page.goto(BASE + '/notifications');
    await Promise.all([page.waitForNavigation(), page.click('button:has-text("Mark all read") >> visible=true')]);
    if (await count() !== '0') { throw new Error('count after all read: ' + await count()); }
    if (!(await page.textContent('body')).includes('all caught up')) { throw new Error('empty state'); }
    await Promise.all([page.waitForNavigation(), page.click('.card-header a.nav-link:has-text("All")')]);
    if (await page.locator('[data-nim-notifications] > li').count() < 3) { throw new Error('all view'); }
    step('mark all read');

    // The dropdown lists unread notifications only: empty now.
    await page.click('[data-nim-bell]');
    await page.waitForFunction(() => /No unread notifications/.test(document.querySelector('[data-nim-bell-list]').textContent));
    if (await page.locator('[data-nim-bell-list] .nim-bell-item').count() !== 0) { throw new Error('the dropdown is not empty after everything was read'); }
    await page.click('[data-nim-bell]');
    step('dropdown empty once everything is read');

    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (overflow > 1) { throw new Error('horizontal scroll on a phone: ' + overflow + 'px'); }
    await page.screenshot({ path: OUT + '/notifications-phone.png', fullPage: true });
    step('phone layout');

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK notifications');
  } catch (e) {
    await page.screenshot({ path: OUT + '/notifications-failure.png', fullPage: true }).catch(() => {});
    console.error(e.message);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
