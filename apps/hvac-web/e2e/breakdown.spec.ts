import { SITE_ID } from './gateway';
import { expect, test } from './test';

const COOLING_TOWER = '018f3e00-4000-7000-8000-000000000004';

test('the plant\'s electricity is split by machine from each one\'s own sub-meter', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/breakdown?site=${SITE_ID}`);

  const facts = page.getByRole('region', { name: '分项概况' });
  await expect(facts).toContainText('空调总用电3,649kWh分表合计 3,601 kWh未分项 48 kWh');
  await expect(facts).toContainText('冷水机组1,852kWh50.7%');
  await expect(facts).toContainText('水泵1,573kWh43.1%');
  await expect(facts).toContainText('冷却塔176kWh4.8%');

  const ranking = page.getByRole('region', { name: '设备用电排行' });
  await expect(ranking.getByRole('listitem').first()).toContainText('CHILLER-01冷水机组1,852 kWh50.7%');
  await expect(page.getByRole('region', { name: '能流' })).toContainText('未分项');

  // One hourly read of the total meter with each machine's sub-meter, from the Site's midnight; no power estimate.
  const reads = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series' && write.body?.breakdown === 'device');
  expect(reads).toHaveLength(1);
  expect(reads[0].body).toMatchObject({ energyType: 'electricity', granularity: 'hour', from: expect.stringMatching(/T16:00:00\.000Z$/) });
  expect(gateway.writes.filter((write) => write.path === '/api/v1/telemetry/device-series:aggregate')).toHaveLength(0);
});

test('a machine without a sub-meter is shown unmetered and its energy stays unallocated', async ({ page, gateway }) => {
  gateway.unmeteredDeviceIds = [COOLING_TOWER];
  await page.goto(`/energy-analysis/breakdown?site=${SITE_ID}`);

  const facts = page.getByRole('region', { name: '分项概况' });
  await expect(facts).toContainText('未分项 224 kWh');
  await expect(facts).toContainText('冷却塔暂无数据未计量1 台未计量，用电计入未分项');

  await expect(page.getByRole('region', { name: '设备用电排行' }).getByRole('listitem').last()).toContainText('CT-01冷却塔未计量');
});
