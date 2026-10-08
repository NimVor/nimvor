// Firewall: a rule added from the "Add rule" window is sent to the rule
// route. Regression: its field named "action" (Allow/Deny) shadowed
// form.action, so the form layer posted to "/security/[object
// HTMLSelectElement]" and the page answered 404 (the Allow buttons of
// "Ports WhaCP keeps open" too). (Dev instance: system commands simulated.)
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage();
  const posts = [];
  page.on('request', r => { if (r.method() === 'POST' && !r.url().endsWith('/login')) { posts.push(new URL(r.url()).pathname); } });
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    await page.goto(BASE + '/security/firewall');
    await page.click('button[data-bs-target="#new-rule"]');
    await page.waitForSelector('#new-rule.show');
    await page.fill('#r-port', '8082');
    await page.click('#new-rule button[type=submit]');
    await page.waitForFunction(() => /Applied|already exists|confirm/i.test(document.querySelector('main').textContent), null, { timeout: 15000 });
    if (posts[0] !== '/security/firewall/rules') { throw new Error('posted to ' + JSON.stringify(posts)); }
    if ((await page.textContent('main')).includes('404')) { throw new Error('404 after adding a rule'); }
    console.log('OK firewall');
  } catch (e) {
    console.log('FAIL', e.message);
    await page.screenshot({ path: OUT + '/firewall-failure.png', fullPage: true });
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
