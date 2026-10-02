import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the plant view shows current plant figures from the Snapshot', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await expect(page.getByText('设备在线').locator('..')).toContainText('7 / 7');
  await expect(page.getByText('冷站 COP').locator('..')).toContainText(/\d\.\d{2}/);
  // Without a realtime grant the page says it is polling rather than pretending to stream.
  await expect(page.getByText('每 30 秒刷新')).toBeVisible();
});

test('a device in the plant snapshot opens its detail in the equipment ledger', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('link', { name: 'CHWP-01' }).click();
  await expect(page).toHaveURL(/\/operations\/systems-devices\?.*inspect=/);
  await expect(page.getByRole('dialog').getByRole('heading', { name: 'CHWP-01' })).toBeVisible();
});
