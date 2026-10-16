import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

// The recording's hours are only complete once the clock has passed them, so the page runs at the moment it was recorded.
const recordedAt = JSON.parse(readFileSync(new URL('./fixtures/energy-series.json', import.meta.url), 'utf8')).electricity.metadata.dataWatermark;

test('energy consumption compares the period with the same span before it, per Site-local hour', async ({ page, gateway }) => {
  await page.clock.setFixedTime(new Date(recordedAt));
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
  const details = page.getByRole('region', { name: '能耗明细' });
  await details.getByRole('tab', { name: '分时电费' }).click();
  await expect(page).toHaveURL(/view=tou/);
  await expect(details).toContainText('两部制分时电价');
  const cost = details.getByRole('region', { name: '分时电费' });
  const periods = cost.getByRole('table', { name: '分时段电费' }).getByRole('row');
  await expect(periods.filter({ hasText: '峰' }).first()).toContainText('425kWh');
  await expect(periods.filter({ hasText: '峰' }).first()).toContainText('¥510');
  await expect(periods.filter({ hasText: '平' }).first()).toContainText('494kWh');
  await expect(periods.filter({ hasText: '平' }).first()).toContainText('¥356');

  // Newest first; each hour's energy also lands in its tariff period's column.
  await details.getByRole('tab', { name: '分时段明细' }).click();
  const rows = details.getByRole('table', { name: '分时段明细' }).getByRole('row');
  await expect(rows).toHaveCount(5);
  await expect(rows.last().getByRole('cell')).toHaveText(['10:00', '177.5', '177.5', '0.0%', '178', '—', '—', '607.1', '3.42']);

  // Today and the same hours yesterday, each as electricity and cooling, and both hourly again to price them
  // (the machines' split is read on its own, see below).
  const queries = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series' && !write.body?.breakdown);
  expect(queries.map((write) => write.body?.energyType).sort()).toEqual(['cooling', 'cooling', 'electricity', 'electricity', 'electricity', 'electricity']);
  for (const write of queries) {
    expect(write.body).toMatchObject({ siteId: SITE_ID, granularity: 'hour', timezone: 'Asia/Shanghai', qualityPolicy: 'VALID_ONLY' });
    // Both periods start at Site-local midnight (UTC+8), whatever the browser timezone.
    expect(write.body?.from).toMatch(/T16:00:00\.000Z$/);
    expect(write.csrfToken).toBeTruthy();
  }

  await page.getByRole('tab', { name: '本月' }).click();
  await expect(page).toHaveURL(/period=month/);
  await expect.poll(() => gateway.writes.filter((write) => write.body?.granularity === 'day' && !write.body?.breakdown).length).toBe(4);
});

const COOLING_TOWER = '018f3e00-4000-7000-8000-000000000004';

test('the same page splits the plant\'s electricity by machine from each one\'s own sub-meter', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);

  const flow = page.getByRole('region', { name: '用电去向' });
  await expect(flow).toContainText('空调总用电 3,649 kWh · 设备电表合计 3,601 kWh');
  await expect(flow.getByRole('region', { name: '能流' })).toContainText('未单独计量');

  const machines = flow.getByRole('table', { name: '设备用电' }).getByRole('row');
  await expect(machines.nth(1)).toContainText('CHILLER-01冷水机组1,852');
  await expect(machines.nth(1)).toContainText('50.7%');
  await expect(machines.last()).toContainText('未单独计量总电表减各设备电表48');

  await expect(page.getByRole('region', { name: '分项趋势' }).getByRole('img', { name: /^冷水机组每时用电/ })).toBeVisible();

  // An hourly read of the total meter with each machine's sub-meter from the Site's midnight, and the same for yesterday; no power estimate.
  const reads = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series' && write.body?.breakdown === 'device');
  expect(reads).toHaveLength(2);
  for (const read of reads) {
    expect(read.body).toMatchObject({ energyType: 'electricity', granularity: 'hour', from: expect.stringMatching(/T16:00:00\.000Z$/) });
  }
  expect(gateway.writes.filter((write) => write.path === '/api/v1/telemetry/device-series:aggregate')).toHaveLength(0);

  // A year is split by month too.
  await page.getByRole('tab', { name: '本年' }).click();
  await expect.poll(() => gateway.writes.filter((write) => write.body?.breakdown === 'device' && write.body?.granularity === 'month').length).toBe(2);
});

test('a machine without a sub-meter is shown unmetered and its energy stays unallocated', async ({ page, gateway }) => {
  gateway.unmeteredDeviceIds = [COOLING_TOWER];
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);

  const machines = page.getByRole('table', { name: '设备用电' }).getByRole('row');
  await expect(machines.filter({ hasText: 'CT-01' })).toContainText('没有电表，用电计入未单独计量');
  await expect(machines.last()).toContainText('总电表减各设备电表224');
});

test('without a Tariff on its grid import, the Site energy is not priced', async ({ page, gateway }) => {
  gateway.tariff = null;
  await page.goto(`/energy-analysis/consumption?site=${SITE_ID}`);
  await page.getByRole('region', { name: '能耗明细' }).getByRole('tab', { name: '分时电费' }).click();
  await expect(page.getByRole('region', { name: '分时电费' })).toContainText('站点还没有配置电价');
});
