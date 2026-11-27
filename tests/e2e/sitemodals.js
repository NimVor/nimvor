// End-to-end test of the windows that replace separate pages:
// - "Create website" (dashboard) and "Add site" (websites list) open the new-website wizard in a window;
//   a type card opens the form for that type; a refused submit marks the field in the window, a good one goes to the new website
// - the Nginx chip on the websites list opens Nginx in a window with its buttons at the top
// - the database server settings window has one header only
// No script or CSP errors.
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const page = await (await browser.newContext({ viewport: { width: 1300, height: 900 } })).newPage();
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')'));
  // The refused submit is answered with HTTP 422 on purpose; the browser logs that as a failed resource.
  page.on('console', m => { if (m.type() === 'error' && !/status of 422/.test(m.text())) errors.push('console: ' + m.text()); });
  const step = s => console.log('STEP', s);
  const stamp = Date.now().toString(36);
  const dlg = '#nim-site-modal .modal-content';
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    // --- Create website from the dashboard's quick action
    await page.goto(BASE + '/');
    await page.click('.nim-quick a:has-text("Create website")');
    await page.waitForSelector(dlg + ' form[data-nim-wizard]', { timeout: 10000 });
    if (!page.url().endsWith('/') && !page.url().includes('/?')) { throw new Error('the quick action left the dashboard: ' + page.url()); }
    step('Create website opens the wizard in a window, on the dashboard');
    // A type card opens the form; a wrong domain is refused in the window, no page change.
    if (await page.locator(dlg + ' [data-nim-wizard-submit]:visible').count() !== 0) { throw new Error('the Create button shows before a type is chosen'); }
    await page.click(dlg + ' label.nim-type-card >> nth=0');
    await page.waitForSelector(dlg + ' #f-domain', { state: 'visible', timeout: 5000 });
    if (await page.locator(dlg + ' [data-nim-wizard-submit]:visible').count() !== 1) { throw new Error('the Create button is missing on the form'); }
    step('a type card opens the form for that type, with Cancel / Create website');
    // The owner must be chosen (existing customer, new customer account or none): without a choice the window says so.
    await page.fill(dlg + ' #f-domain', 'noowner' + stamp + '.example.test');
    await page.click(dlg + ' button[type=submit]:visible');
    await page.waitForSelector(dlg + ' [data-nim-owner] .invalid-feedback', { timeout: 10000 });
    await page.click(dlg + ' label.nim-choice:has(input[name=owner_mode][value=new])');
    if (!await page.locator(dlg + ' #f-new-email').isVisible()) { throw new Error('the new customer fields did not show'); }
    step('the owner must be chosen; "New customer account" asks for an email');
    await page.click(dlg + ' label.nim-choice:has(input[name=owner_mode][value=none])');
    await page.fill(dlg + ' #f-domain', 'not a domain!');
    await page.click(dlg + ' button[type=submit]:visible');
    await page.waitForSelector(dlg + ' .is-invalid, ' + dlg + ' [data-nim-modal-error]:not(.d-none)', { timeout: 10000 });
    if (!await page.locator(dlg).isVisible()) { throw new Error('the window closed on a refused submit'); }
    step('a refused submit stays in the window and marks the field');
    // Advanced settings are folded; Change type goes back to the cards.
    if (await page.locator(dlg + ' details.nim-adv[open]').count() !== 0) { throw new Error('the advanced settings are open by default'); }
    await page.click(dlg + ' .nim-type-head:visible [data-nim-wizard-back]');
    await page.waitForSelector(dlg + ' label.nim-type-card', { state: 'visible', timeout: 5000 });
    await page.click(dlg + ' label.nim-type-card >> nth=0');
    await page.click(dlg + ' label.nim-choice:has(input[name=owner_mode][value=none])');
    await page.fill(dlg + ' #f-domain', 'win' + stamp + '.example.test');
    await Promise.all([page.waitForURL(/\/sites\/\d+/, { timeout: 60000 }), page.click(dlg + ' button[type=submit]:visible')]);
    step('a good submit goes to the new website: ' + page.url());

    // --- Add site on the websites list
    await page.goto(BASE + '/websites');
    await page.click('a:has-text("Add site")');
    await page.waitForSelector(dlg + ' form[data-nim-wizard]', { timeout: 10000 });
    if (!page.url().endsWith('/websites')) { throw new Error('Add site left the list: ' + page.url()); }
    step('Add site opens the same window on the websites list');
    await page.waitForTimeout(600); // the window finishes opening before Escape counts
    await page.keyboard.press('Escape');
    await page.waitForSelector(dlg, { state: 'hidden', timeout: 5000 });

    // --- Nginx chip
    const chip = page.locator('.nim-svc:has-text("Nginx")');
    if (await chip.count()) {
      await chip.first().hover();
      await page.waitForSelector('.nim-hovercard', { timeout: 15000 });
      step('hovering the Nginx chip shows its state');
      await chip.first().click();
      await page.waitForSelector(dlg + ' .modal-header', { timeout: 10000 });
      const head = await page.textContent(dlg + ' .modal-header');
      if (!/Nginx/.test(head)) { throw new Error('the window is not Nginx: ' + head); }
      if (!/(Stop|Start)/.test(head)) { throw new Error('no Start/Stop in the window header: ' + head); }
      if (!page.url().endsWith('/websites')) { throw new Error('the chip left the list'); }
      step('clicking the chip opens Nginx in a window with Start/Stop at the top');
      await page.waitForTimeout(600); // the window finishes opening before Escape counts
    await page.keyboard.press('Escape');
      await page.waitForSelector(dlg, { state: 'hidden', timeout: 5000 });
    } else {
      step('(no Nginx chip in this fixture; skipped)');
    }

    // --- Database server settings: one header
    await page.goto(BASE + '/databases');
    const dbchip = page.locator('a.nim-svc[data-nim-site-modal]');
    if (await dbchip.count()) {
      await dbchip.first().click();
      await page.waitForSelector(dlg + ' .modal-title', { timeout: 10000 });
      if (await page.locator(dlg + ' .card-header').count() !== 0) { throw new Error('the window has a second header'); }
      step('the database settings window has one header');
    }

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK site windows');
  } catch (e) {
    console.error(e.message || e);
    await page.screenshot({ path: OUT + '/sitemodals-failure.png' }).catch(() => {});
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
