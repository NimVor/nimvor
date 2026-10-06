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
const DB = path.resolve(__dirname, '../../.dev/whacp.sqlite');

function seed() {
  execFileSync('php', ['-r', `
    $db = new PDO('sqlite:' . $argv[1]);
    $now = gmdate('c');
    $db->exec("DELETE FROM notifications WHERE dedupe_key LIKE 'e2e:%'");
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
  const count = async () => (await page.locator('[data-wcp-bell-count]').isVisible()) ? (await page.textContent('[data-wcp-bell-count]')).trim() : '0';
  try {
    seed();
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    if (await count() !== '3') { throw new Error('bell count: ' + await count()); }
    if (!(await page.textContent('body')).includes('Unread failures')) { throw new Error('dashboard failures card missing'); }
    step('bell and dashboard card');

    await page.click('[data-wcp-bell]');
    await page.waitForSelector('[data-wcp-notifications]');
    if (await page.locator('[data-wcp-notifications] > li').count() !== 3) { throw new Error('expected 3 unread'); }
    if (await page.locator('[data-wcp-notifications] img').count() !== 0) { throw new Error('HTML in a title was rendered'); }
    await page.screenshot({ path: OUT + '/notifications.png' });
    step('page lists unread, escaped');

    await page.selectOption('select[name=severity]', 'error');
    await page.waitForURL(/severity=error/);
    if (await page.locator('[data-wcp-notifications] > li').count() !== 1) { throw new Error('severity filter'); }
    await Promise.all([page.waitForNavigation(), page.click('[data-wcp-notifications] button[aria-label="Mark read"]')]);
    if (!page.url().includes('severity=error')) { throw new Error('filter lost after marking read: ' + page.url()); }
    if (await count() !== '2') { throw new Error('count after one read: ' + await count()); }
    step('filter and mark one read');

    await page.goto(BASE + '/notifications');
    await Promise.all([page.waitForNavigation(), page.click('button:has-text("Mark all read")')]);
    if (await count() !== '0') { throw new Error('count after all read: ' + await count()); }
    if (!(await page.textContent('body')).includes('all caught up')) { throw new Error('empty state'); }
    await Promise.all([page.waitForNavigation(), page.click('.card-header a.nav-link:has-text("All")')]);
    if (await page.locator('[data-wcp-notifications] > li').count() < 3) { throw new Error('all view'); }
    step('mark all read');

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
