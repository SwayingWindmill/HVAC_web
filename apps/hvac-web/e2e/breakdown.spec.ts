import { SITE_ID } from './gateway';
import { expect, test } from './test';

const COOLING_TOWER = '018f3e00-4000-7000-8000-000000000004';

test('the plant\'s electricity is split by machine from each one\'s own sub-meter', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/breakdown?site=${SITE_ID}`);

  const flow = page.getByRole('region', { name: '用电去向' });
  await expect(flow).toContainText('空调总用电 3,649 kWh · 设备电表合计 3,601 kWh');
  await expect(flow.getByRole('region', { name: '能流' })).toContainText('未单独计量');

  const machines = flow.getByRole('table', { name: '设备用电' }).getByRole('row');
  await expect(machines.nth(1)).toContainText('CHILLER-01冷水机组电表实测1,852');
  await expect(machines.nth(1)).toContainText('50.7%');
  await expect(machines.last()).toContainText('未单独计量总电表减各设备电表差值48');

  await expect(page.getByRole('region', { name: '分项用电趋势' }).getByRole('img', { name: /^冷水机组每时用电/ })).toBeVisible();

  // An hourly read of the total meter with each machine's sub-meter from the Site's midnight, and the same for yesterday; no power estimate.
  const reads = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series' && write.body?.breakdown === 'device');
  expect(reads).toHaveLength(2);
  for (const read of reads) {
    expect(read.body).toMatchObject({ energyType: 'electricity', granularity: 'hour', from: expect.stringMatching(/T16:00:00\.000Z$/) });
  }
  expect(gateway.writes.filter((write) => write.path === '/api/v1/telemetry/device-series:aggregate')).toHaveLength(0);
});

test('a machine without a sub-meter is shown unmetered and its energy stays unallocated', async ({ page, gateway }) => {
  gateway.unmeteredDeviceIds = [COOLING_TOWER];
  await page.goto(`/energy-analysis/breakdown?site=${SITE_ID}`);

  const machines = page.getByRole('table', { name: '设备用电' }).getByRole('row');
  await expect(machines.filter({ hasText: 'CT-01' })).toContainText('未计量 · 用电计入未单独计量');
  await expect(machines.last()).toContainText('差值224');
});
