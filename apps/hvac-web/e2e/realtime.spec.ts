import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the plant view shows current plant figures from the Snapshot', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  const facts = page.getByRole('region', { name: '冷站实时概况' });
  await expect(facts).toContainText('设备在线7 / 7台数据均为最新');
  await expect(facts).toContainText('主要设备运行4 / 4台冷机、水泵与冷却塔');
  await expect(facts).toContainText(/冷站 COP\d\.\d{2}/);
  // Without a realtime grant the page says it is polling rather than pretending to stream.
  await expect(page.getByText('每 30 秒刷新')).toBeVisible();
});

test('a device in the plant snapshot opens its detail in the equipment ledger', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  await page.getByRole('figure', { name: '冷站系统图' }).getByRole('link', { name: /CHWP-01/ }).click();
  await expect(page).toHaveURL(/\/operations\/systems-devices\?.*inspect=/);
  await expect(page.getByRole('dialog').getByRole('heading', { name: 'CHWP-01' })).toBeVisible();
});

test('the plant diagram puts each measured temperature on its pipe once and shows flow while pumps run', async ({ page }) => {
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  const diagram = page.getByRole('figure', { name: '冷站系统图' });
  await expect(diagram).toContainText('出塔30.7°C');
  await expect(diagram).toContainText('34.1°C回塔');
  await expect(diagram).toContainText('进冷凝器30.7°C');
  await expect(diagram).toContainText('出水7°C');
  await expect(diagram).toContainText('供水7°C');
  await expect(diagram).toContainText('12.6°C回水');
  // A probe shown on its pipe is not repeated in the node.
  await expect(diagram).not.toContainText('出塔水温');
  await expect(diagram.getByText('流量', { exact: true })).toHaveCount(2);
  await expect(diagram.locator('[data-flowing]')).toHaveCount(4);
  await expect(diagram.locator('[data-flowing="true"]')).toHaveCount(4);
});

test('a stopped chilled-water pump stops the flow on the chilled-water loop only', async ({ page, gateway }) => {
  for (const item of gateway.snapshots.items) {
    for (const value of item.snapshot.values) {
      if (item.deviceId.endsWith('0002') && value.key === 'run_state') value.value = 'STOPPED';
    }
  }
  await page.goto(`/operations/realtime?site=${SITE_ID}`);
  const diagram = page.getByRole('figure', { name: '冷站系统图' });
  await expect(diagram.locator('[data-flowing="false"]')).toHaveCount(2);
  await expect(diagram.locator('[data-flowing="true"]')).toHaveCount(2);
});
