import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('a month\'s report sums energy, priced cost and operations from their owners', async ({ page, gateway }) => {
  await page.goto(`/reports?site=${SITE_ID}`);

  const report = page.getByRole('article', { name: /能源报告/ });
  await expect(report).toContainText('空调用电919kWh');
  await expect(report).toContainText('峰时段电度电费');
  await expect(report).toContainText('两部制分时电价');
  await expect(report.getByRole('heading', { name: '运行与维护' })).toBeVisible();
  await expect(report.getByRole('heading', { name: '节能成效' })).toBeVisible();
  await expect(report).toContainText('本月没有实施中的节能措施。');

  // The month runs from the Site's midnight on the 1st; energy is read by day for the summary and by hour for the price.
  const reads = gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series');
  expect(reads.map((read) => read.body?.granularity).sort()).toEqual(['day', 'day', 'day', 'day', 'hour']);
  expect(reads.find((read) => read.body?.granularity === 'hour')?.body?.from).toMatch(/-(0[1-9]|[12]\d|3[01])T16:00:00\.000Z$/);

  await page.getByRole('combobox', { name: '报告月份' }).click();
  await page.getByRole('option').nth(1).click();
  await expect(page).toHaveURL(/month=\d{4}-\d{2}/);
  await expect(page.getByText('已结月')).toBeVisible();
});

const september = JSON.parse(readFileSync(new URL('./fixtures/report-snapshot-2026-09.json', import.meta.url), 'utf8'));

test('a closed month with a snapshot reads from it, and says what was not archived', async ({ page, gateway }) => {
  gateway.reportSnapshots = [september];
  await page.goto(`/reports?site=${SITE_ID}&month=2026-09`);

  const report = page.getByRole('article', { name: /能源报告/ });
  await expect(report).toContainText('已存档');
  await expect(report).toContainText('覆盖 16 / 30 天');
  await expect(report).toContainText('空调用电72,697kWh上月未存档');
  await expect(report).toContainText('峰时段电度电费29,820 kWh1.20 元/kWh');
  await expect(report).toContainText('¥35,784');
  await expect(report).toContainText(/用电最多的一天[\d,]+kWh9\/16 周三/);
  await expect(report.getByText('未存档，按当前记录现算').first()).toBeVisible();

  // The archived month's energy is the snapshot's, not read again from the series.
  expect(gateway.writes.filter((write) => write.path === '/api/v1/analytics/energy-series')).toHaveLength(0);
});

test('the year lists each month from its snapshot or the live series', async ({ page, gateway }) => {
  gateway.reportSnapshots = [september];
  await page.goto(`/reports?site=${SITE_ID}&view=year&year=2026`);

  const report = page.getByRole('article', { name: '2026 年能源报告' });
  await expect(report).toContainText('1 个月已存档');
  const months = report.getByRole('table', { name: '逐月明细' });
  await expect(months.getByRole('row', { name: /^9 月/ })).toContainText('已存档72.70 MWh209.46 MWh2.8860,259 元16 / 30 天');
  await expect(months.getByRole('row', { name: /^1 月/ })).toContainText('无数据');
});
