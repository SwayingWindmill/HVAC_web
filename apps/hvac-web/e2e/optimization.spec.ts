import { SITE_ID } from './gateway';
import { expect, test } from './test';

test('without a recommendation the page offers a run and says when its inputs are not ready', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  await expect(page.getByText('还没有优化建议')).toBeVisible();

  await page.getByRole('button', { name: '生成建议' }).first().click();
  await expect(page.getByRole('alert')).toContainText('暂时无法生成建议：昨日用电与电费、最新负荷预测或实时供水、室内温度尚未就绪');
  const runs = gateway.writes.filter((write) => write.path === '/api/v1/optimization/runs');
  expect(runs).toEqual([expect.objectContaining({ body: { siteId: SITE_ID }, csrfToken: expect.any(String) })]);
});
