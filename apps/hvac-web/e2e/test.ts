import { expect, test as base } from '@playwright/test';
import { Gateway } from './gateway';

// Every test runs against its own recorded Gateway and fails on any call it does not serve.
export const test = base.extend<{ gateway: Gateway }>({
  gateway: [async ({ page }, use) => {
    const gateway = new Gateway();
    await gateway.install(page);
    await use(gateway);
    expect(gateway.unexpected).toEqual([]);
  }, { auto: true }],
});

export { expect };
