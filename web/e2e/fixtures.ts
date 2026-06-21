import {
  expect,
  test as base,
  type Browser,
  type BrowserContext,
  type Page,
} from '@playwright/test';

type BrowserName = 'chromium' | 'firefox' | 'webkit';
type CdpFixtures = {
  browser: Browser;
  context: BrowserContext;
  page: Page;
};

const cdpEndpoint = process.env.PLAYWRIGHT_CDP_ENDPOINT?.trim();

function assertBrowserName(browserName: string): asserts browserName is BrowserName {
  if (browserName !== 'chromium' && browserName !== 'firefox' && browserName !== 'webkit') {
    throw new Error(`Unexpected browserName "${browserName}"`);
  }
}

function envNumber(name: string, fallback: number) {
  const value = process.env[name]?.trim();
  if (!value) {
    return fallback;
  }
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 0) {
    throw new Error(`${name} must be a non-negative integer`);
  }
  return parsed;
}

function envFlag(name: string, fallback: boolean) {
  const value = process.env[name]?.trim().toLowerCase();
  if (!value) {
    return fallback;
  }
  if (['1', 'true', 'yes', 'on'].includes(value)) {
    return true;
  }
  if (['0', 'false', 'no', 'off'].includes(value)) {
    return false;
  }
  throw new Error(`${name} must be a boolean-like value`);
}

export const test = cdpEndpoint ? base.extend<CdpFixtures>({
  browser: [async ({ playwright, browserName }, use) => {
    assertBrowserName(browserName);
    if (browserName !== 'chromium') {
      throw new Error('PLAYWRIGHT_CDP_ENDPOINT requires the chromium project.');
    }

    const browser = await playwright.chromium.connectOverCDP(cdpEndpoint, {
      slowMo: envNumber('PLAYWRIGHT_CDP_SLOW_MO_MS', 0),
    });
    await use(browser);
    await browser.close({ reason: 'Test ended.' });
  }, { scope: 'worker', timeout: 0 }],

  context: async ({ browser }, use) => {
    const context = browser.contexts()[0];
    if (!context) {
      throw new Error('Chrome CDP did not expose a default browser context');
    }
    await context.clearCookies();
    await context.clearPermissions();
    await use(context);
  },

  page: async ({ context }, use) => {
    const page = await context.newPage();
    await page.addInitScript(() => {
      window.localStorage.removeItem('netsgo-auth');
      window.sessionStorage.clear();
    });
    await page.bringToFront();
    await use(page);

    const finishDelay = envNumber('PLAYWRIGHT_CDP_FINISH_DELAY_MS', 0);
    if (finishDelay > 0) {
      await page.bringToFront().catch(() => undefined);
      await page.waitForTimeout(finishDelay).catch(() => undefined);
    }
    if (!envFlag('PLAYWRIGHT_CDP_KEEP_TAB', false)) {
      await page.close({ reason: 'Test ended.' }).catch(() => undefined);
    }
  },
}) : base;

export { expect };
