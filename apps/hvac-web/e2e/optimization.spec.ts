import { readFileSync } from 'node:fs';
import { principal, SITE_ID } from './gateway';
import { expect, test } from './test';

const fixture = (name: string) => JSON.parse(readFileSync(new URL(`./fixtures/${name}`, import.meta.url), 'utf8'));

function withoutSupplySetpoint(model: Record<string, any>) {
  const removedIds = new Set(
    model.telemetryPoints
      .filter((point: { sourceMetadata?: { capability?: string } }) => point.sourceMetadata?.capability === 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT')
      .map((point: { id: string }) => point.id),
  );
  const telemetryPoints = model.telemetryPoints.filter((point: { id: string }) => !removedIds.has(point.id));
  const relationships = model.relationships.filter(
    (rel: { fromType: string; fromId: string; toType: string; toId: string }) =>
      !(rel.fromType === 'POINT' && removedIds.has(rel.fromId))
      && !(rel.toType === 'POINT' && removedIds.has(rel.toId)),
  );
  return {
    ...model,
    telemetryPoints,
    relationships,
    counts: { ...model.counts, points: telemetryPoints.length },
  };
}

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

  // L1 keeps engineering details collapsed behind the single-line readiness summary until expanded.
  const readinessToggle = page.getByRole('button', { name: /前置与安全条件：6\/6 就绪 · 可远程下发设定点/ });
  await expect(readinessToggle).toBeVisible();
  await expect(page.getByText('供水每升高 1 °C 日用电')).toBeHidden();

  await readinessToggle.click();
  const readinessPanel = page.getByRole('region', { name: '前置与安全条件' });
  await expect(readinessPanel).toContainText('检修与手动锁定');
  await expect(readinessPanel).toContainText('实时读数核验');
  await expect(readinessPanel).toContainText('负荷预测与分时电价');
  await expect(readinessPanel).toContainText('执行通道状态');
  // The policy's response coefficients are set by hand, and the expanded details say so alongside rollback rules.
  await expect(readinessPanel).toContainText('供水每升高 1 °C 日用电−60 kWh · 人工设定');
  await expect(readinessPanel).toContainText('供水每升高 1 °C 室内温度+0.3 °C · 人工设定');
  await expect(readinessPanel).toContainText('下发后观察 30 分钟');

  await proposal.getByRole('button', { name: '按建议下发' }).click();
  const confirm = page.getByRole('alertdialog');
  await expect(confirm).toContainText('向 CHILLER-01 下发指令？');
  await expect(confirm).toContainText('8.0 °C');
  await expect(confirm).toContainText('下发后观察 30 分钟');
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

test('view tabs persist in the URL and normalize unknown view values back to proposal', async ({ page, gateway }) => {
  gateway.optimization = fixture('optimization-latest.json');
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}&view=bogus`);
  await expect(page.getByRole('tab', { name: '当前建议', selected: true })).toBeVisible();

  await page.getByRole('tab', { name: '模型与负荷分析' }).click();
  await expect(page).toHaveURL(new RegExp(`view=analysis`));
  await expect(page.getByRole('tab', { name: '模型与负荷分析', selected: true })).toBeVisible();
});

test('a site without a writable supply setpoint offers a prefilled manual work order and deduplicates open ones', async ({ page, gateway }) => {
  gateway.optimization = fixture('optimization-latest.json');
  gateway.assetModel = withoutSupplySetpoint(gateway.assetModel);

  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  const proposal = page.getByRole('region', { name: '当前建议' });
  await expect(proposal.getByText('按建议下发')).toBeHidden();
  await expect(page.getByRole('button', { name: /前置与安全条件：5\/6 就绪 · 仅人工调温工单/ })).toBeVisible();

  await proposal.getByRole('button', { name: '创建人工调温工单' }).click();
  const dialog = page.getByRole('dialog', { name: '创建人工调温工单' });
  await expect(dialog.getByLabel('标题')).toHaveValue('人工调整冷冻水供水温度至 8.1 °C');
  await expect(dialog.getByLabel('说明')).toHaveValue(/估算电费 ¥51/);
  await dialog.getByRole('button', { name: '创建工单' }).click();

  const createdWrite = gateway.writes.find((write) => write.path === `/api/v1/sites/${SITE_ID}/work-orders`);
  expect(createdWrite?.body).toMatchObject({
    title: '人工调整冷冻水供水温度至 8.1 °C',
    priority: 'MEDIUM',
    sourceReferences: [
      {
        domain: 'EXTERNAL',
        resourceId: 'optimization-recommendation:01a12bdc-97d7-7e50-bc97-f14cb86800ba',
        relationship: 'ORIGIN',
      },
    ],
  });

  const orderLink = proposal.getByRole('link', { name: /已转为人工调温工单 · 待处理/ });
  await expect(orderLink).toBeVisible();
  await expect(proposal.getByRole('button', { name: '创建人工调温工单' })).toBeHidden();

  // Reloading keeps the unclosed work order linked instead of offering duplicate creation.
  await page.reload();
  await expect(page.getByRole('region', { name: '当前建议' }).getByRole('link', { name: /已转为人工调温工单 · 待处理/ })).toBeVisible();
  await page.getByRole('region', { name: '当前建议' }).getByRole('link', { name: /已转为人工调温工单 · 待处理/ }).click();
  await expect(page).toHaveURL(new RegExp(`/operations/work-center.*inspect=`));
  await expect(page.getByText('来自优化建议')).toBeVisible();
  await expect(page.getByRole('link', { name: '查看优化建议' })).toBeVisible();
});

test('a viewer without work-order.create sees a read-only explanation instead of a work-order trigger', async ({ page, gateway }) => {
  gateway.optimization = fixture('optimization-latest.json');
  gateway.assetModel = withoutSupplySetpoint(gateway.assetModel);
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter(
        (capability: string) => capability !== 'work-order.create',
      ),
    },
  };

  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  const proposal = page.getByRole('region', { name: '当前建议' });
  await expect(proposal.getByText('按建议下发')).toBeHidden();
  await expect(proposal.getByRole('button', { name: '创建人工调温工单' })).toBeHidden();
  await expect(proposal).toContainText('当前账号无创建工单权限');
});

test('a recommendation that keeps the current supply temperature offers neither remote dispatch nor a manual work order', async ({ page, gateway }) => {
  const unchanged = fixture('optimization-latest.json');
  unchanged.recommendation.candidate.supplyTempC = unchanged.recommendation.baseline.supplyTempC;
  gateway.optimization = unchanged;

  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  const proposal = page.getByRole('region', { name: '当前建议' });
  await expect(proposal).toContainText('保持 7.1°C');
  await expect(proposal.getByRole('button', { name: '按建议下发' })).toBeHidden();
  await expect(proposal.getByRole('button', { name: '创建人工调温工单' })).toBeHidden();
});

test('shadow evaluation is lazy-loaded only on the analysis tab and renders non-executable COP diagnostics and 24h load forecast', async ({ page, gateway }) => {
  gateway.optimization = fixture('optimization-latest.json');
  gateway.optimizationShadow = fixture('optimization-shadow-latest.json');

  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  await expect(page.getByRole('region', { name: '当前建议' })).toBeVisible();
  expect(gateway.shadowReads).toBe(0);

  await page.getByRole('tab', { name: '模型与负荷分析' }).click();
  await expect.poll(() => gateway.shadowReads).toBe(1);

  const analysisPanel = page.getByRole('tabpanel', { name: '模型与负荷分析' });
  await expect(analysisPanel.getByText('影子对照 · 只记录不发布，不作为控制指令下发')).toBeVisible();
  await expect(analysisPanel.getByRole('button', { name: '按建议下发' })).toBeHidden();
  await expect(analysisPanel.getByRole('button', { name: '创建人工调温工单' })).toBeHidden();

  await expect(analysisPanel).toContainText('−52.4 kWh');
  await expect(analysisPanel).toContainText('−60 kWh');
  await expect(analysisPanel).toContainText('站点未登记冷却水设定点，暂未开放冷却水温度重置');

  const candidateTable = analysisPanel.getByRole('table', { name: '影子候选供水温度步长' });
  await expect(candidateTable).toContainText('6.1 °C');
  await expect(candidateTable).toContainText('7.1 °C');
  await expect(candidateTable).toContainText('8.1 °C');
  await expect(candidateTable).toContainText('影子最优');

  const fitRegion = analysisPanel.getByRole('region', { name: '冷机 COP 拟合诊断' });
  await expect(fitRegion).toContainText('CHILLER-01');
  await expect(fitRegion).toContainText('已拟合');
  await expect(fitRegion).toContainText('874');
  await expect(fitRegion).toContainText('346 kW');
  await expect(fitRegion).toContainText('4.3%');
  await expect(fitRegion).toContainText('4.92');
  await expect(fitRegion).toContainText('单机运行 874');
  await expect(fitRegion.getByRole('button', { name: /回归系数（COP 公式）：常数 2\.84/ })).toBeVisible();

  const forecastRegion = analysisPanel.getByRole('region', { name: '未来 24 小时负荷预测' });
  await expect(forecastRegion).toBeVisible();
  await expect(forecastRegion.getByRole('link', { name: '查看用电负荷' })).toHaveAttribute('href', new RegExp('/energy-analysis/load'));
  await forecastRegion.getByRole('tab', { name: '表格' }).click();
  await expect(forecastRegion.getByRole('table', { name: '未来 24 小时负荷预测表' })).toBeVisible();
});

test('analysis tab distinguishes missing shadow records from NO_USABLE_MODEL per-chiller rejection diagnostics', async ({ page, gateway }) => {
  gateway.optimization = fixture('optimization-latest.json');
  gateway.optimizationShadow = null;

  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}&view=analysis`);
  await expect(page.getByText('当前建议尚无影子评估记录')).toBeVisible();

  const shadow = fixture('optimization-shadow-latest.json');
  gateway.optimizationShadow = {
    ...shadow,
    outcome: 'NO_USABLE_MODEL',
    reason: 'no Chiller has a usable COP fit',
    cvRmse: null,
    energyResponse: undefined,
    candidates: undefined,
    summary: undefined,
    fit: {
      ...shadow.fit,
      cvRmsePower: null,
      chillers: [
        {
          deviceId: '018f3e00-4000-7000-8000-000000000001',
          status: 'INSUFFICIENT_DATA',
          reason: 'fewer than 96 steady 15-minute samples',
          samples: 42,
        },
      ],
    },
  };

  await page.reload();
  const analysisPanel = page.getByRole('tabpanel', { name: '模型与负荷分析' });
  await expect(analysisPanel.getByText('暂无可用冷机拟合模型')).toBeVisible();
  await expect(analysisPanel).toContainText('CHILLER-01');
  await expect(analysisPanel).toContainText('稳态样本不足');
  await expect(analysisPanel).toContainText('少于 96 个');
});
