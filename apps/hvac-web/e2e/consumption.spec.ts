import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('energy consumption compares the period with the same span before it, per Site-local hour', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);

  const energy = page.getByRole('region', { name: '期间用能' });
  await expect(energy.getByRole('tab', { name: /空调用电/ })).toContainText('918.9kWh');
  await expect(energy.getByRole('tab', { name: /供冷量/ })).toContainText('3,198.5kWh');
  await expect(energy.getByRole('tab', { name: /综合能效/ })).toContainText('3.48COP');
  await energy.getByRole('tab', { name: /综合能效/ }).click();
  await expect(page).toHaveURL(/metric=cop/);
  await expect(energy).toContainText('用电最高时段 11:00');
  await expect(page.getByTestId('energy-freshness')).toHaveText(' · 数据截至 10/10 15:06');

  // 10:00 and 13:00 fall in peak periods, 11:00 and 12:00 in a flat one.
  const estimate = energy.getByRole('tab', { name: /估算电费/ });
  await expect(estimate).toContainText('¥865');
  await expect(estimate).toContainText('均价 0.942 元/kWh · 用电 919 kWh');
  const cost = page.getByRole('region', { name: '分时电费' });
  await expect(cost).toContainText('两部制分时电价');
  const periods = cost.getByRole('table', { name: '分时段电费' }).getByRole('row');
  await expect(periods.filter({ hasText: '峰' }).first()).toContainText('425kWh');
  await expect(periods.filter({ hasText: '峰' }).first()).toContainText('¥510');
  await expect(periods.filter({ hasText: '平' }).first()).toContainText('494kWh');
  await expect(periods.filter({ hasText: '平' }).first()).toContainText('¥356');

  // Newest first; each hour's energy also lands in its tariff period's column.
  const rows = page.getByRole('table', { name: '分时段明细' }).getByRole('row');
  await expect(rows).toHaveCount(5);
  await expect(rows.last().getByRole('cell')).toHaveText(['10:00', '177.5', '177.5', '0.0%', '178', '—', '—', '607.1', '3.42']);

  // Today and the same hours yesterday, each as electricity and cooling, and both hourly again to price them.
  const queries = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series');
  expect(queries.map((write) => write.body?.energyType).sort()).toEqual(['cooling', 'cooling', 'electricity', 'electricity', 'electricity', 'electricity']);
  for (const write of queries) {
    expect(write.body).toMatchObject({ siteId: SITE_ID, granularity: 'hour', timezone: 'Asia/Shanghai', qualityPolicy: 'VALID_ONLY' });
    // Both periods start at Site-local midnight (UTC+8), whatever the browser timezone.
    expect(write.body?.from).toMatch(/T16:00:00\.000Z$/);
    expect(write.csrfToken).toBeTruthy();
  }

  await page.getByRole('tab', { name: '本月' }).click();
  await expect(page).toHaveURL(/period=month/);
  await expect.poll(() => gateway.writes.filter((write) => write.body?.granularity === 'day').length).toBe(4);
});

test('without a Tariff on its grid import, the Site energy is not priced', async ({ page, gateway }) => {
  gateway.tariff = null;
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);
  await expect(page.getByRole('region', { name: '分时电费' })).toContainText('站点还没有配置电价');
});
