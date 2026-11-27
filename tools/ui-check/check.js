const { chromium } = require('playwright-core');

(async () => {
  const base = process.env.BASE_URL;
  const browser = await chromium.launch({
    executablePath: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    headless: true,
    args: ['--disable-gpu', '--no-first-run'],
  });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1 });
  const errors = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push('console: ' + m.text()); });
  page.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
  await page.goto(base, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.case-card', { timeout: 20000 });
  await page.waitForTimeout(800);
  console.log('cards=' + await page.locator('.case-card').count());
  console.log('countBadge=' + await page.locator('#caseCountBadge').innerText());
  console.log('quota=' + await page.locator('#quotaText').innerText());
  await page.screenshot({ path: 'gallery.png', fullPage: false });

  await page.locator('.case-card').first().click();
  await page.waitForSelector('#caseModal:not([hidden])');
  await page.waitForTimeout(500);
  console.log('modalTitle=' + await page.locator('#modalTitle').innerText());
  await page.screenshot({ path: 'modal.png', fullPage: false });
  await page.locator('#modalClose').click();

  await page.locator('.nav-item[data-view=create]').click();
  await page.waitForTimeout(400);
  await page.screenshot({ path: 'create.png', fullPage: false });

  await page.locator('.nav-item[data-view=faceswap]').click();
  await page.waitForTimeout(400);
  await page.screenshot({ path: 'faceswap.png', fullPage: false });

  await page.locator('.nav-item[data-view=history]').click();
  await page.waitForTimeout(400);
  await page.screenshot({ path: 'history.png', fullPage: false });

  await page.locator('.nav-item[data-view=settings]').click();
  await page.waitForTimeout(400);
  await page.screenshot({ path: 'settings.png', fullPage: false });

  console.log('errors=' + JSON.stringify(errors));
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
