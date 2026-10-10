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
  await page.getByRole('figure', { name: '冷站系统图' }).getByRole('link', { name: /CHWP-01/ }).click();
  await expect(page).toHaveURL(/\/operations\/systems-devices\?.*inspect=/);
  await expect(page.getByRole('dialog').getByRole('heading', { name: 'CHWP-01' })).toBeVisible();
});

test('the plant diagram puts each measured temperature on its loop and shows flow while pumps run', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  const diagram = page.getByRole('figure', { name: '冷站系统图' });
  await expect(diagram).toContainText('出塔30.7°C');
  await expect(diagram).toContainText('34.1°C回塔');
  await expect(diagram).toContainText('供水7°C');
  await expect(diagram).toContainText('12.6°C回水');
  await expect(diagram).toContainText('建筑负荷');
  // Both pumps run in the Snapshot, so all four pipe runs carry a flow line on supply and return.
  await expect(diagram.locator('line[stroke-dasharray="4 8"]')).toHaveCount(8);
});
