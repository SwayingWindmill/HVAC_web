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
