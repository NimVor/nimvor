// End-to-end test of security keys with Chromium's virtual authenticator:
// add a key on the profile page, sign out, sign in with password + key.
// WebAuthn does not accept IP addresses, so the panel is opened as localhost.
const { chromium } = require('playwright');
const BASE = (process.env.E2E_BASE || 'http://127.0.0.1:8095').replace('127.0.0.1', 'localhost');
const OUT = process.env.E2E_OUT || '/tmp';
(async () => {
  const browser = await chromium.launch();
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')\n' + (e.stack || '')));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = (s) => console.log('STEP', s);
  try {
    const cdp = await context.newCDPSession(page);
    await cdp.send('WebAuthn.enable');
    await cdp.send('WebAuthn.addVirtualAuthenticator', { options: { protocol: 'ctap2', transport: 'usb', hasResidentKey: false, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });

    const signIn = async () => {
      await page.goto(BASE + '/login');
      await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
      await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
    };
    await signIn();
    await page.goto(BASE + '/profile#security-keys');
    await page.fill('[data-nim-key-add] input[name=name]', 'Virtual key');
    await Promise.all([page.waitForNavigation(), page.click('[data-nim-key-add] button')]);
    await page.waitForSelector('#security-keys :text("Virtual key")');
    step('key added');

    await page.goto(BASE + '/profile');
    await page.click('form[action$="/logout"] button, a[href$="/logout"]').catch(async () => {
      await page.evaluate(() => { const f = document.querySelector('form[action$="/logout"]'); if (f) { f.submit(); } });
    });
    await page.waitForURL(/\/login/);
    await signIn();
    if (!page.url().includes('/login/two-factor')) { throw new Error('no second factor asked: ' + page.url()); }
    await Promise.all([page.waitForNavigation(), page.click('[data-nim-key-login]')]);
    if (new URL(page.url()).pathname !== '/') { throw new Error('not signed in: ' + page.url() + ' ' + await page.textContent('body')); }
    step('signed in with the key');

    // Clean up: remove the key again.
    await page.goto(BASE + '/profile#security-keys');
    await page.click('#security-keys details summary');
    await page.fill('#security-keys details input[name=current_password]', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('#security-keys details button')]);
    if (await page.locator('#security-keys :text("Virtual key")').count() !== 0) { throw new Error('key not removed'); }
    step('key removed');
    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('security key: ok');
  } catch (e) {
    await page.screenshot({ path: OUT + '/securitykey-failure.png', fullPage: true });
    console.error(e);
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
