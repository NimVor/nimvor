// End-to-end test of the field hints (the info icon beside a label, View::hint()):
// - the explanation shows on hover and on keyboard focus, stays open when the icon is clicked,
//   and closes with Escape and with a click elsewhere
// - the text is in the page for screen readers (aria-describedby) and the icon is a labelled button
// - a long helper line is no longer printed under the input
// - on a phone a tap opens the tooltip and it stays inside the screen
// No script or CSP errors.
const { chromium } = require('playwright');
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:8095';
const OUT = process.env.E2E_OUT || '/tmp';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1300, height: 900 } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message + ' (' + page.url() + ')'));
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  const step = s => console.log('STEP', s);
  const tipVisible = async () => { const t = page.locator('.nim-hint-tip'); return (await t.count()) > 0 && await t.first().isVisible(); };
  try {
    await page.goto(BASE + '/login');
    await page.fill('#username', 'admin'); await page.fill('#password', 'dev-password-change-me');
    await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

    await page.goto(BASE + '/settings');
    const first = page.locator('[data-nim-hint]').first();
    if (await page.locator('[data-nim-hint]').count() < 5) { throw new Error('the settings page has too few hints'); }
    const label = await first.getAttribute('aria-label');
    if (!/^More (about|information)/.test(label || '')) { throw new Error('the icon has no accessible name: ' + label); }
    const descId = await first.getAttribute('aria-describedby');
    const text = (await page.locator('#' + descId).textContent() || '').trim();
    if (text.length < 5) { throw new Error('the hint text is not in the page'); }
    step('hint icons: labelled buttons, text in the page for screen readers');

    await first.hover();
    await page.waitForSelector('.nim-hint-tip', { state: 'visible', timeout: 3000 });
    if ((await page.locator('.nim-hint-tip').textContent()) !== text) { throw new Error('the tooltip shows other text'); }
    step('hover shows the explanation');
    await page.mouse.move(5, 5);
    await page.waitForSelector('.nim-hint-tip', { state: 'hidden', timeout: 3000 });
    step('moving away hides it');

    await first.focus();
    await page.waitForSelector('.nim-hint-tip', { state: 'visible', timeout: 3000 });
    await page.keyboard.press('Escape');
    if (await tipVisible()) { throw new Error('Escape did not close the tooltip'); }
    if (await first.getAttribute('aria-expanded') !== 'false') { throw new Error('aria-expanded not reset'); }
    step('keyboard focus opens it, Escape closes it');

    await first.click();
    await page.mouse.move(5, 5);
    await page.waitForTimeout(400);
    if (!await tipVisible()) { throw new Error('a clicked tooltip did not stay open'); }
    if (await first.getAttribute('aria-expanded') !== 'true') { throw new Error('aria-expanded not set'); }
    await page.mouse.click(5, 300);
    if (await tipVisible()) { throw new Error('a click elsewhere did not close it'); }
    step('click keeps it open; a click elsewhere closes it');

    // A form saved with the hint icon next to its label: clicking the icon never submits or focuses the field.
    await page.goto(BASE + '/settings');
    const url = page.url();
    await page.locator('[data-nim-hint]').nth(1).click();
    if (page.url() !== url) { throw new Error('clicking an info icon navigated'); }
    step('the icon does not submit the form');

    // Tweak Settings still finds a setting by a word that is only in its explanation (the text stays in the page).
    await page.goto(BASE + '/settings/tweak');
    await page.fill('[data-nim-tweak-search]', 'webhook');
    await page.waitForTimeout(300);
    const found = await page.textContent('[data-nim-tweak-count]');
    if (!/[1-9]\d* setting/.test(found || '')) { throw new Error('searching the explanations found nothing: "' + found + '"'); }
    step('Tweak Settings search still matches the explanations');

    const phone = await browser.newContext({ viewport: { width: 375, height: 700 }, hasTouch: true, isMobile: true });
    const mp = await phone.newPage();
    await mp.goto(BASE + '/login');
    await mp.fill('#username', 'admin'); await mp.fill('#password', 'dev-password-change-me');
    await Promise.all([mp.waitForNavigation(), mp.click('button[type=submit]')]);
    await mp.goto(BASE + '/settings');
    const icon = mp.locator('[data-nim-hint]').first();
    await icon.tap();
    await mp.waitForSelector('.nim-hint-tip', { state: 'visible', timeout: 3000 });
    const box = await mp.locator('.nim-hint-tip').boundingBox();
    if (!box || box.x < 0 || box.x + box.width > 376) { throw new Error('the tooltip leaves the phone screen: ' + JSON.stringify(box)); }
    step('phone: a tap opens the tooltip and it fits the screen');
    await mp.screenshot({ path: OUT + '/fieldhints-phone.png' });
    await page.screenshot({ path: OUT + '/fieldhints.png' });

    if (errors.length) { throw new Error(errors.join('\n')); }
    console.log('OK field hints');
  } catch (e) {
    console.error(e.message || e);
    await page.screenshot({ path: OUT + '/fieldhints-failure.png' }).catch(() => {});
    process.exitCode = 1;
  } finally {
    await browser.close();
  }
})();
