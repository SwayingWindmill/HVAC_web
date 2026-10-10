import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('energy consumption shows Site electricity, cooling and plant efficiency per hour', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);

  const facts = page.getByRole('region', { name: '期间用能概况' });
  await expect(facts).toContainText('918.9kWh');
  await expect(facts).toContainText('3,198.5kWh');
  await expect(facts).toContainText('3.48COP');
  await expect(facts).toContainText('用电最高时段11:00');
  await expect(facts).toContainText('电费未接入');
  await expect(page.getByTestId('energy-freshness')).toHaveText('数据截至 10/10 15:06');

  const rows = page.getByRole('table', { name: '分时段能耗明细' }).getByRole('row');
  await expect(rows).toHaveCount(5);
  await expect(rows.nth(1).getByRole('cell')).toHaveText(['10:00', '177.5', '607.1', '3.42']);

  const queries = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series');
  expect(queries.map((write) => write.body?.energyType).sort()).toEqual(['cooling', 'electricity']);
  for (const write of queries) {
    expect(write.body).toMatchObject({ siteId: SITE_ID, granularity: 'hour', timezone: 'Asia/Shanghai', qualityPolicy: 'VALID_ONLY' });
    // Today starts at Site-local midnight (UTC+8), whatever the browser timezone.
    expect(write.body?.from).toMatch(/T16:00:00\.000Z$/);
    expect(write.csrfToken).toBeTruthy();
  }

  await page.getByRole('radio', { name: '本月' }).click();
  await expect(page).toHaveURL(/period=month/);
  await expect.poll(() => gateway.writes.filter((write) => write.body?.granularity === 'day').length).toBe(2);
});
