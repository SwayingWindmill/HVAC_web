import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('the plant\'s electricity is split by machine from each one\'s hourly power', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/breakdown?site=${SITE_ID}`);

  const facts = page.getByRole('region', { name: '分项概况' });
  await expect(facts).toContainText('冷水机组1,983kWh62.5%');
  await expect(facts).toContainText('冷却塔123kWh3.9%');

  const ranking = page.getByRole('region', { name: '设备用电排行' });
  await expect(ranking.getByRole('listitem').first()).toContainText('CHILLER-01冷水机组1,983 kWh62.5%');
  await expect(page.getByRole('region', { name: '能流' })).toContainText('输配系统');

  // One hourly power read per machine, from the Site's midnight.
  const reads = gateway.writes.filter((write) => write.path === '/api/v1/telemetry/device-series:aggregate' && write.body?.granularity === 'HOUR');
  expect(reads).toHaveLength(4);
  for (const read of reads) expect(read.body).toMatchObject({ keys: ['power'], timezone: 'Asia/Shanghai', from: expect.stringMatching(/T16:00:00\.000Z$/) });
});
