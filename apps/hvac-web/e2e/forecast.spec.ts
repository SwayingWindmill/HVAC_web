import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the load forecast shows the coming day beside the last one, with its peak and hour-by-hour load', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/forecast?site=${SITE_ID}`);

  const facts = page.getByRole('region', { name: '预测概况' });
  await expect(facts).toContainText('预测峰值负荷256.0kW');
  await expect(facts).toContainText('预测谷值负荷97.7kW');
  await expect(facts).toContainText('未来 24 小时用电5,971kWh');
  await expect(page.getByText('按近 7 天同时段负荷预测')).toBeVisible();

  await expect(page.getByRole('region', { name: '实际与预测' }).locator('.recharts-line')).toHaveCount(2);
  // Two tables side by side: their headers and the 24 or 25 Site-local hours the 96 quarter-hours touch.
  const hours = page.getByRole('region', { name: '逐时预测' }).getByRole('row');
  await expect.poll(() => hours.count()).toBeGreaterThanOrEqual(26);
  await expect(hours.filter({ hasText: '峰值' })).toHaveCount(1);

  // The last day's load comes from metered electricity, hour by hour.
  const reads = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series');
  expect(reads.map((write) => [write.body?.energyType, write.body?.granularity])).toContainEqual(['electricity', 'hour']);
});
