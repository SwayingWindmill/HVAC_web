import { expect, test } from './test';

test('authorized sites rank by plant COP over the period, read on each site\'s clock', async ({ page, gateway }) => {
  await page.goto('/benchmark');

  const facts = page.getByRole('region', { name: '对标概况' });
  await expect(facts).toContainText('整体综合能效3.48COP');
  await expect(facts).toContainText('至少两个站点有能效数据才可比较');

  const ranking = page.getByRole('region', { name: '站点排名' });
  await expect(ranking.getByRole('row', { name: /中央机房/ })).toContainText('3.48');
  await expect(page.getByRole('region', { name: '更多对标维度' })).toContainText('1 / 1 个站点登记了建筑面积');

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

// The Site's Registry profile: its floor area gives electricity per square metre (GB/T 51161), its
// GB 50176 zone labels it; ranking by intensity puts the least electricity per square metre first.
test("sites also compare on electricity per square metre of their recorded floor area", async ({ page }) => {
  await page.goto('/benchmark');
  const ranking = page.getByRole('region', { name: '站点排名' });
  const row = ranking.getByRole('row', { name: /中央机房/ });
  await expect(row).toContainText('夏热冬冷地区');
  await expect(row).toContainText('建筑面积 32,000 m²');
  await expect(row).toContainText(/\d+\.\d{2} kWh\/m²/);

  await ranking.getByRole('tab', { name: '按单位面积用电' }).click();
  await expect(page).toHaveURL(/rank=intensity/);
  await expect(ranking).toContainText('没有登记建筑面积的站点不参与此排名');
  await expect(row.getByRole('cell').first()).toHaveText('1');
});
