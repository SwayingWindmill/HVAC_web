import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('a workspace opened without a Site carries the default Site in the URL', async ({ page }) => {
  await page.goto('/operations/systems-devices');
  await expect(page).toHaveURL(new RegExp(`[?&]site=${SITE_ID}`));
  await expect(page.getByRole('heading', { name: '系统与设备' })).toBeVisible();
});

test('the ledger lists registered equipment and counts only observed points', async ({ page }) => {
  await page.goto(`/operations/systems-devices?site=${SITE_ID}`);
  const chiller = page.getByRole('row', { name: /CHILLER-01/ });
  await expect(chiller).toContainText('运行');
  // Command points are written, never observed; they must not count as missing data.
  await expect(chiller).toContainText('11 / 11');
  await expect(page.getByRole('row', { name: /WEATHER-STATION-01/ })).toContainText('室外环境');
});

test('the device inspector shows current readings and the 24-hour trend', async ({ page }) => {
  await page.goto(`/operations/systems-devices?site=${SITE_ID}`);
  await page.getByRole('row', { name: /CHILLER-01/ }).click();
  await expect(page).toHaveURL(/inspect=018f3e00-4000-7000-8000-000000000001/);

  const inspector = page.getByRole('dialog');
  await expect(inspector.getByRole('heading', { name: 'CHILLER-01' })).toBeVisible();
  await expect(inspector).toContainText('运行状态');
  await expect(inspector).not.toContainText('RUNNING');
  await expect(inspector).not.toContainText('启动冷水机');
  await expect(inspector.locator('.recharts-surface')).toBeVisible();
});

test('a work order created from a device names the device as its origin', async ({ page, gateway }) => {
  await page.goto(`/operations/systems-devices?site=${SITE_ID}&inspect=018f3e00-4000-7000-8000-000000000001`);
  const inspector = page.getByRole('dialog');
  await inspector.getByRole('button', { name: '创建工单' }).click();
  await page.getByLabel('标题').fill('CHILLER-01：冷凝器清洗');
  await page.getByLabel('说明').fill('冷凝器逼近温度偏高，安排清洗');
  await page.getByRole('dialog', { name: /创建工单/ }).getByRole('button', { name: '创建工单' }).click();

  await expect(inspector).toContainText('CHILLER-01：冷凝器清洗');
  const create = gateway.writes.find((write) => write.method === 'POST' && write.path.endsWith('/work-orders'));
  expect(create?.csrfToken).toBe('e2e-csrf-token');
  expect(create?.body?.sourceReferences).toEqual([{ domain: 'ASSET', resourceId: expect.any(String), relationship: 'ORIGIN' }]);
});
