import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

const { recordedAt } = JSON.parse(readFileSync(new URL('./fixtures/demand-quarter-hours.json', import.meta.url), 'utf8'));

test('load is the plant meter\'s quarter-hour average power: now, today\'s and the month\'s highest, and tomorrow\'s forecast on one curve', async ({ page, gateway }) => {
  await page.clock.setFixedTime(new Date(recordedAt));
  gateway.forecastOrigin = recordedAt;
  await page.goto(`/energy-analysis/load?site=${SITE_ID}`);

  const facts = page.getByRole('region', { name: '负荷概况' });
  // The quarter-hour still running is current load, not a peak.
  await expect(facts).toContainText('当前负荷247.2kW22:30 起的 15 分钟，进行中');
  await expect(facts).toContainText('今日最高247.2kW出现在 21:45 · 负荷率 97%');
  await expect(facts).toContainText('本月最高247.2kW出现在 10/11 21:45');
  await expect(facts).toContainText('明天最高（预测）');
  await expect(page.getByText('预测按近 7 天同时段')).toBeVisible();

  // Metered load and yesterday's under it, the forecast after now.
  const curve = page.getByRole('region', { name: '负荷曲线' });
  await expect(curve.locator('.recharts-line')).toHaveCount(2);
  await expect(curve.locator('.recharts-area')).toHaveCount(2);

  // The rest of today and tomorrow, hour by hour (the recorded forecast reaches 24 hours ahead, so tomorrow stops at 22:00);
  // the Tariff bills peak-period demand, so each hour says how far it stays under the month's highest.
  const details = page.getByRole('region', { name: '负荷明细' });
  await expect(details.getByRole('table', { name: '明天逐时预测' }).getByRole('row')).toHaveCount(24);
  await expect(details.getByRole('table', { name: '今天剩余 2 小时逐时预测' }).getByRole('row')).toHaveCount(3);
  await expect(details.getByRole('columnheader', { name: '距本月最高' })).toHaveCount(2);
  await expect(details).toContainText('预测覆盖到明天 22:30');

  await details.getByRole('tab', { name: '本月每日最高' }).click();
  await expect(page).toHaveURL(/view=days/);
  await expect(details).toContainText('本月最高 247.2 kW · 出现在 10/11 21:45');

  // The recording has no peak-period quarter-hour, the only period that carries a demand rate.
  await details.getByRole('tab', { name: '需量电费' }).click();
  await expect(details).toContainText('暂无数据');
  await expect(details).toContainText('峰时段本月还没有完整的 15 分钟');

  const reads = gateway.writes.filter((write) => write.path === '/api/v1/telemetry/device-series:aggregate');
  expect(reads.map((read) => [read.body!.from, read.body!.granularity])).toEqual([
    ['2026-09-30T16:00:00.000Z', 'QUARTER_HOUR'],
    ['2026-10-10T16:00:00.000Z', 'QUARTER_HOUR'],
  ]);
});
