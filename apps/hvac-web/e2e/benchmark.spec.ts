import { expect, test } from './test';

test('authorized sites rank by plant COP over the period, read on each site\'s clock', async ({ page, gateway }) => {
  await page.goto('/benchmark');

  const facts = page.getByRole('region', { name: '对标概况' });
  await expect(facts).toContainText('整体综合能效3.48COP');
  await expect(facts).toContainText('至少两个站点有能效数据才可比较');

  const ranking = page.getByRole('region', { name: '站点排名' });
  await expect(ranking.getByRole('row', { name: /中央机房/ })).toContainText('3.48');
  await expect(page.getByRole('region', { name: '更多对标维度' })).toContainText('单位面积能耗');

  const reads = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series');
  expect(reads.map((read) => read.body?.energyType).sort()).toEqual(['cooling', 'electricity']);
  for (const read of reads) expect(read.body).toMatchObject({ granularity: 'day', timezone: 'Asia/Shanghai', from: expect.stringMatching(/T16:00:00\.000Z$/) });
});

test('a site the user may see but not analyze stays listed, outside the comparison', async ({ page, gateway }) => {
  gateway.analyticsDenied = true;
  await page.goto('/benchmark');
  await expect(page.getByRole('region', { name: '站点排名' }).getByRole('row', { name: /中央机房/ })).toContainText('没有该站点的能耗分析权限');
  await expect(page.getByRole('region', { name: '对标概况' })).toContainText('1 个无权限');
});
