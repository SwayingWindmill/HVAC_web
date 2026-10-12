import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

const { recordedAt } = JSON.parse(readFileSync(new URL('./fixtures/demand-quarter-hours.json', import.meta.url), 'utf8'));

test('demand is the plant meter\'s quarter-hour average power, with today\'s and the month\'s peak', async ({ page, gateway }) => {
  await page.clock.setFixedTime(new Date(recordedAt));
  await page.goto(`/energy-analysis/demand?site=${SITE_ID}`);

  const facts = page.getByRole('region', { name: '需量概况' });
  // The quarter-hour still running is current demand, not a peak.
  await expect(facts).toContainText('当前需量247.2kW22:30 起的 15 分钟，进行中');
  await expect(facts).toContainText('今日最大需量247.2kW出现在 21:45');
  await expect(facts).toContainText('本月最大需量247.2kW出现在 10/11 21:45');
  await expect(facts).toContainText('今日负荷率97%');
  // The recording has no peak-period quarter-hour, the only period that carries a demand rate.
  await expect(facts).toContainText('本月需量电费暂无数据');
  await expect(facts).toContainText('峰时段本月还没有完整的 15 分钟');
  await expect(page.getByText('需量电费按峰时段当月最大需量计收')).toBeVisible();
  await expect(page.getByRole('region', { name: '需量曲线' })).toContainText('本月最大 247 kW');

  const reads = gateway.writes.filter((write) => write.path === '/api/v1/telemetry/device-series:aggregate');
  expect(reads.map((read) => [read.body!.from, read.body!.granularity])).toEqual([
    ['2026-09-30T16:00:00.000Z', 'QUARTER_HOUR'],
    ['2026-10-10T16:00:00.000Z', 'QUARTER_HOUR'],
  ]);
});
