import { readFileSync } from 'node:fs';
import { SITE_ID } from './gateway';
import { expect, test } from './test';

const fixture = (name: string) => JSON.parse(readFileSync(new URL(`./fixtures/${name}`, import.meta.url), 'utf8'));

test('without a recommendation the page offers a run and says when its inputs are not ready', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  await expect(page.getByText('还没有优化建议')).toBeVisible();
  await expect(page.getByText('站点还没有配置定时优化，建议只能手动生成。')).toBeVisible();

  await page.getByRole('button', { name: '生成建议' }).first().click();
  await expect(page.getByRole('alert')).toContainText('暂时无法生成建议：最近一天的用电与电费、最新负荷预测或实时供水、室内温度尚未就绪');
  const runs = gateway.writes.filter((write) => write.path === '/api/v1/optimization/runs');
  expect(runs).toEqual([expect.objectContaining({ body: { siteId: SITE_ID }, csrfToken: expect.any(String) })]);
});

test('a requested run publishes a recommendation that is dispatched as a confirmed setpoint command', async ({ page, gateway }) => {
  gateway.nextRecommendation = fixture('optimization-latest.json');
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  await page.getByRole('button', { name: '生成建议' }).first().click();

  const proposal = page.getByRole('region', { name: '当前建议' });
  await expect(proposal).toContainText('7.18.1°C');
  await expect(proposal).toContainText('手动生成于');
  await expect(proposal).toContainText('低风险');
  await expect(proposal).toContainText('预计室内温度 24.4 °C，仍在 22–26 °C 舒适范围内');
  const impact = page.getByRole('region', { name: '预计效果' });
  await expect(impact).toContainText('预计日节电60kWh');
  await expect(impact).toContainText('预计日节省¥51');
  const basis = page.getByRole('region', { name: '计算依据' });
  await expect(basis).toContainText('最近一天冷站用电675 kWh');
  // The policy's response coefficients are set by hand, and the page says so.
  await expect(basis).toContainText('供水每升高 1 °C 日用电−60 kWh · 人工设定');
  await expect(basis).toContainText('供水每升高 1 °C 室内温度+0.3 °C · 人工设定');

  await proposal.getByRole('button', { name: '按建议下发' }).click();
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toContainText('向 CHILLER-01 下发指令？');
  await expect(confirm).toContainText('8.0 °C');
  await confirm.getByRole('button', { name: '确认下发' }).click();
  await expect(page.getByText('指令已提交，正在下发')).toBeVisible();
  // The proposal now follows the command it was sent as.
  await expect(proposal.getByRole('link', { name: /已提交为控制指令/ })).toBeVisible();
  const submitted = gateway.writes.find((write) => write.path === '/api/v1/commands');
  expect(submitted?.body).toEqual({ assetId: expect.any(String), commandPointId: expect.any(String), parameters: { setpointC: 8 } });
});

// The Site's hourly schedule keeps the recommendation current without anyone asking; the page and
// the overview say it was not asked for.
test('a recommendation the hourly schedule made says so', async ({ page, gateway }) => {
  gateway.optimization = { ...fixture('optimization-latest.json'), trigger: 'SCHEDULE' };
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  await expect(page.getByRole('region', { name: '当前建议' })).toContainText('定时生成于');
  await page.goto(`/overview?site=${SITE_ID}`);
  await expect(page.getByRole('region', { name: '节能成效' })).toContainText('定时生成');
});

// A firing that could not prepare a run names the input it found missing, rather than a bare
// "no recommendation".
test('without a recommendation the page says which input the last scheduled run was missing', async ({ page, gateway }) => {
  gateway.optimizationPreparation = { trigger: 'SCHEDULE', status: 'FAILED', reason: 'ZONE_TEMPERATURE_MISSING', firedAt: '2026-10-30T04:20:00Z', completedAt: '2026-10-30T04:20:02Z' };
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  await expect(page.getByText('还没有优化建议')).toBeVisible();
  await expect(page.getByText(/最近一次定时计算（.*）没有生成建议：读不到可用的实时室内温度。/)).toBeVisible();
});

test('a scheduled run that failed after the recommendation says the recommendation is the previous one', async ({ page, gateway }) => {
  gateway.optimization = { ...fixture('optimization-latest.json'), trigger: 'SCHEDULE' };
  gateway.optimizationPreparation = { trigger: 'SCHEDULE', status: 'FAILED', reason: 'BASELINE_OUTSIDE_LIMITS', firedAt: '2026-10-30T04:20:00Z', completedAt: '2026-10-30T04:20:02Z' };
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  const alert = page.getByRole('alert');
  await expect(alert).toContainText('最近一次定时计算没有生成新建议');
  await expect(alert).toContainText('当前供水温度或室内温度已在策略的安全或舒适范围之外。下面是上一条建议。');
  await expect(page.getByRole('region', { name: '当前建议' })).toContainText('定时生成于');
});
