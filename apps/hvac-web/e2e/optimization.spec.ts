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
  await expect(page.getByText('已提交，正在下发')).toBeVisible();
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
  await expect(dialog).toBeHidden();

  const orderLink = proposal.getByRole('link', { name: /已转为人工调温工单 · 待处理/ });
  await expect(orderLink).toBeVisible();
  await expect(proposal.getByRole('button', { name: '创建人工调温工单' })).toBeHidden();

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

test('a stop recommendation for an idle tower is confirmed as a high-risk command; locked assets are listed as paused', async ({ page, gateway }) => {
  const model = gateway.assetModel;
  const asset = (code: string) => model.assets.find((candidate: { code: string }) => candidate.code === code);
  const tower = asset('ct-01');
  const pump = asset('cwp-01');
  const stopPoint = model.telemetryPoints.find((point: { reportingDeviceId: string; pointCode: string }) =>
    point.pointCode === 'stop' && model.devices.find((device: { id: string }) => device.id === point.reportingDeviceId)?.code === 'ct-01');
  gateway.staging = {
    tenantId: principal.context.tenantId, siteId: SITE_ID, evaluatedAt: new Date().toISOString(), confirmationMode: 'MANUAL_CONFIRM', siteControlMode: 'REMOTE_AUTO',
    recommendations: [{
      id: '01a19000-0000-7000-8000-000000000001', kind: 'IDLE_AUXILIARY_STOP', confirmationMode: 'MANUAL_CONFIRM', requiresSupervisorReview: true, requiresTotp: true,
      suggestedCapability: 'STOP', suggestedCommandPointId: stopPoint.id, targetAssetId: tower.id, targetAssetCode: 'ct-01', targetAssetName: 'CT-01', targetAssetType: 'COOLING_TOWER',
      predictedPowerSavingKW: 6.5, estimatedHourlySavingKWh: 6.5, estimatedShiftSavingKWh: 52, shiftHours: 8, continuousIdleSeconds: 3120, continuousIdleMinutes: 52,
      rationale: 'IDLE_AUXILIARY_STOP', preconditions: ['MANUAL_CONFIRM'],
    }],
    diagnostics: [
      { scope: 'CHILLER_STAGE_DOWN', assetId: asset('chiller-01').id, status: 'NO_OP', reason: 'SINGLE_CHILLER_RUNNING', detail: 'internal' },
      { scope: 'IDLE_AUXILIARY_STOP', assetId: pump.id, status: 'SUPPRESSED', reason: 'ASSET_CONTROL_LOCKED', detail: 'MANUAL_LOCK internal' },
    ],
  };
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);

  const staging = page.getByRole('region', { name: '减台与辅机停机' });
  await expect(staging).toContainText('停 CT-01');
  await expect(staging).toContainText('−6.5kW');
  await expect(staging).toContainText('本班约 52 kWh');
  await expect(staging).toContainText('对应主机已停52 分钟');
  await expect(staging.getByLabel('暂停的建议')).toContainText('CWP-01已锁定，解锁后才会给停机建议');
  // The service's internal sentences and codes never reach the page.
  await expect(staging).not.toContainText('MANUAL');
  await expect(staging).not.toContainText('internal');

  await staging.getByRole('button', { name: '确认停机…' }).click();
  const confirm = page.getByRole('alertdialog', { name: '停 CT-01？' });
  await expect(confirm).toContainText('6.5 kW · 本班约 52 kWh');
  await confirm.getByRole('textbox', { name: '动态口令' }).fill('246810');
  await confirm.getByRole('checkbox', { name: '我已和现场确认可以停' }).check();
  await confirm.getByRole('button', { name: '提交停机' }).click();

  await expect(staging).toContainText('已提交 · 待审批');
  await expect(staging.getByRole('link', { name: '在运行控制查看 ›' })).toBeVisible();
  expect(gateway.writes.find((write) => write.path === '/api/v1/commands')?.body).toEqual({
    assetId: tower.id, commandPointId: stopPoint.id, parameters: {}, verificationCode: '246810',
  });
});

test('under manual control the stop recommendations say they are paused and cannot be confirmed', async ({ page, gateway }) => {
  gateway.controlState = { ...gateway.controlState, controlMode: 'MANUAL_OVERRIDE', reason: '夜班巡检', revision: 2 };
  gateway.staging = {
    tenantId: principal.context.tenantId, siteId: SITE_ID, evaluatedAt: new Date().toISOString(), confirmationMode: 'MANUAL_CONFIRM', siteControlMode: 'MANUAL_OVERRIDE',
    recommendations: [],
    diagnostics: [{ scope: 'CHILLER_STAGE_DOWN', status: 'SUPPRESSED', reason: 'SITE_CONTROL_EXIT_ACTIVE', detail: 'MANUAL_OVERRIDE' }],
  };
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  const staging = page.getByRole('region', { name: '减台与辅机停机' });
  await expect(staging.getByRole('status')).toContainText('人工接管中，停机建议已暂停');
  await expect(staging.getByLabel('暂停的建议')).toContainText('减台站点不在远程自动控制，停机建议已暂停');
  await expect(page.getByRole('banner').getByRole('link', { name: '人工接管' })).toBeVisible();
});

test('#861 explains NOT_CALIBRATED and PENDING readiness reasons in supply optimization while staging works independently', async ({ page, gateway }) => {
  const model = gateway.assetModel;
  const tower = model.assets.find((candidate: { code: string }) => candidate.code === 'ct-01');
  const stopPoint = model.telemetryPoints.find((point: { reportingDeviceId: string; pointCode: string }) =>
    point.pointCode === 'stop' && model.devices.find((device: { id: string }) => device.id === point.reportingDeviceId)?.code === 'ct-01');

  gateway.optimizationPreparation = {
    trigger: null,
    status: 'NOT_SCHEDULED',
    reason: null,
    firedAt: null,
    completedAt: null,
    responseCoefficient: { status: 'NOT_CALIBRATED', calibratedAt: null },
    tenantStrategy: { status: 'PENDING' },
  };
  gateway.staging = {
    tenantId: principal.context.tenantId,
    siteId: SITE_ID,
    evaluatedAt: new Date().toISOString(),
    confirmationMode: 'MANUAL_CONFIRM',
    siteControlMode: 'REMOTE_AUTO',
    recommendations: [{
      id: '01a19000-0000-7000-8000-000000000009',
      kind: 'IDLE_AUXILIARY_STOP',
      confirmationMode: 'MANUAL_CONFIRM',
      requiresSupervisorReview: true,
      requiresTotp: true,
      suggestedCapability: 'STOP',
      suggestedCommandPointId: stopPoint.id,
      targetAssetId: tower.id,
      targetAssetCode: 'ct-01',
      targetAssetName: 'CT-01',
      targetAssetType: 'COOLING_TOWER',
      predictedPowerSavingKW: 6.5,
      estimatedHourlySavingKWh: 6.5,
      estimatedShiftSavingKWh: 52,
      shiftHours: 8,
      continuousIdleSeconds: 3120,
      continuousIdleMinutes: 52,
      rationale: 'IDLE_AUXILIARY_STOP',
      preconditions: ['MANUAL_CONFIRM'],
    }],
    diagnostics: [],
  };

  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);

  const staging = page.getByRole('region', { name: '减台与辅机停机' });
  await expect(staging).toContainText('停 CT-01');
  await expect(staging.getByRole('button', { name: '确认停机…' })).toBeEnabled();

  const proposal = page.getByRole('region', { name: '当前建议' });
  await expect(proposal).toContainText('响应系数还没标定');
  await expect(proposal).toContainText('共享租户策略还没确认');
  await expect(proposal).not.toContainText('站点还没有配置定时优化，建议只能手动生成。');

  const readinessToggle = page.getByRole('button', { name: /前置与安全条件：响应系数还没标定、共享租户策略还没确认/ });
  await readinessToggle.click();
  const readinessPanel = page.getByRole('region', { name: '前置与安全条件' });
  await expect(readinessPanel).toContainText('响应系数标定');
  await expect(readinessPanel).toContainText('还没标定');
  await expect(readinessPanel).toContainText('共享租户策略');
  await expect(readinessPanel).toContainText('还没确认');
});

test('auto-control lease lifecycle validates bounds, handles MFA_NOT_CONFIGURED, renders execution monitors, and revokes', async ({ page, gateway }) => {
  gateway.optimization = fixture('optimization-latest.json');
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);

  const leasePanel = page.getByRole('region', { name: '自动控制租约' });
  await expect(leasePanel).toContainText('人工确认下发');
  await expect(page.getByRole('region', { name: '当前建议' })).toContainText('建议不会自动执行');

  await leasePanel.getByRole('button', { name: '开启自动控制租约…' }).click();
  const dialog = page.getByRole('dialog', { name: '开启限时自动控制租约' });
  await dialog.getByLabel('租约有效期').fill('25');
  await dialog.getByLabel('租约有效期').blur();
  await expect(dialog).toContainText('有效期需为 1 至 24 的整数小时');
  await dialog.getByLabel('租约有效期').fill('8');
  await dialog.getByLabel('原因').fill('白班冷负荷平稳，开启自动调温');
  await dialog.getByRole('textbox', { name: '动态口令' }).fill('123456');

  // Verify MFA_NOT_CONFIGURED keeps the dialog open with clickable enrollment link.
  gateway.stepUpFailureReason = 'MFA_NOT_CONFIGURED';
  await dialog.getByRole('button', { name: '确认开启租约' }).click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('link', { name: /去开通两步验证/ })).toHaveAttribute('href', /\/system\?tab=users/);

  // Clear synthetic failure and arm the lease.
  gateway.stepUpFailureReason = null;
  await dialog.getByRole('textbox', { name: '动态口令' }).fill('654321');
  await dialog.getByRole('button', { name: '确认开启租约' }).click();

  await expect(page.getByText('已开启限时自动控制租约')).toBeVisible();
  await expect(leasePanel).toContainText('限时自动控制租约生效中');
  await expect(leasePanel).toContainText('6.0 至 12.0 °C');
  await expect(leasePanel).toContainText('调整冷冻水供水温度设定');
  await expect(page.getByRole('region', { name: '当前建议' })).toContainText('自动控制租约生效中');

  // Seed execution monitors including a TRIPPED window with Chinese translated reason and rollback command link.
  const now = Date.now();
  gateway.optimizationExecutionMonitors = [
    {
      id: '01990000-8800-7000-8000-000000000101',
      tenantId: principal.context.tenantId,
      siteId: SITE_ID,
      leaseId: gateway.autoControlLeaseState.activeLease.id,
      recommendationId: '01a12bdc-97d7-7e50-bc97-f14cb86800ba',
      commandIntentId: '01990000-8800-7000-8000-000000000201',
      deviceId: '018f3e00-4000-7000-8000-000000000001',
      targetCapability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
      baselineSupplyTempC: 7.1,
      appliedSupplyTempC: 8.0,
      restoreSupplyTempC: 7.1,
      zoneTempMaxC: 26.0,
      supplyTempMaxC: 12.0,
      startedAt: new Date(now - 35 * 60_000).toISOString(),
      windowEndsAt: new Date(now - 5 * 60_000).toISOString(),
      status: 'VERIFIED',
      createdAt: new Date(now - 35 * 60_000).toISOString(),
      updatedAt: new Date(now - 5 * 60_000).toISOString(),
    },
    {
      id: '01990000-8800-7000-8000-000000000102',
      tenantId: principal.context.tenantId,
      siteId: SITE_ID,
      leaseId: gateway.autoControlLeaseState.activeLease.id,
      recommendationId: '01a12bdc-97d7-7e50-bc97-f14cb86800bb',
      commandIntentId: '01990000-8800-7000-8000-000000000202',
      deviceId: '018f3e00-4000-7000-8000-000000000001',
      targetCapability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
      baselineSupplyTempC: 8.0,
      appliedSupplyTempC: 9.0,
      restoreSupplyTempC: 8.0,
      zoneTempMaxC: 26.0,
      supplyTempMaxC: 12.0,
      startedAt: new Date(now - 15 * 60_000).toISOString(),
      windowEndsAt: new Date(now + 15 * 60_000).toISOString(),
      status: 'TRIPPED',
      tripReason: 'zone_temperature_exceeded_max banquet_room_temperature (26.60C > 26.00C)',
      rollbackCommandIntentId: '01990000-8800-7000-8000-000000000203',
      createdAt: new Date(now - 15 * 60_000).toISOString(),
      updatedAt: new Date(now - 2 * 60_000).toISOString(),
    },
  ];
  await page.reload();

  const monitorsTable = page.getByRole('table', { name: '30 分钟执行监护' });
  await expect(monitorsTable).toContainText('已核验通过');
  await expect(monitorsTable).toContainText('已触发保护退出');
  await expect(monitorsTable).toContainText('室内温度超出上限（26.60 °C > 26.00 °C）');
  await expect(monitorsTable).not.toContainText('zone_temperature_exceeded_max');
  await expect(monitorsTable.getByRole('link', { name: '查看下发指令 ›' }).first()).toHaveAttribute('href', /\/operations\/control\?.*inspect=01990000-8800-7000-8000-000000000201/);
  await expect(monitorsTable.getByRole('link', { name: '查看回退指令 ›' })).toHaveAttribute('href', /\/operations\/control\?.*inspect=01990000-8800-7000-8000-000000000203/);

  // Revoke lease returns to MANUAL_CONFIRM.
  await leasePanel.getByRole('button', { name: '撤回租约' }).click();
  const revokeDialog = page.getByRole('dialog', { name: '撤回自动控制租约？' });
  await revokeDialog.getByLabel('原因').fill('夜间人工巡检，暂停自动下发');
  await revokeDialog.getByRole('button', { name: '确认撤回租约' }).click();

  await expect(page.getByText('已撤回自动控制租约，恢复为人工确认下发')).toBeVisible();
  await expect(leasePanel).toContainText('人工确认下发');
  await expect(leasePanel).toContainText('上次租约：已人工撤回');
  await expect(leasePanel).toContainText('夜间人工巡检，暂停自动下发');

  // Without schedule.manage, arm/revoke controls are hidden.
  gateway.principalOverride = {
    ...principal,
    authorization: {
      ...principal.authorization,
      capabilities: principal.authorization.capabilities.filter((cap: string) => cap !== 'schedule.manage'),
    },
  };
  await page.reload();
  await expect(leasePanel.getByRole('button', { name: '开启自动控制租约…' })).toHaveCount(0);
  await expect(leasePanel).toContainText('只读，需运行排班管理权限才能授权');
});

test('auto-control P0 guardrails enforce 5.0 C floor, show manual backoff notice, and translate clamped monitor fields', async ({ page, gateway }) => {
  const now = Date.now();
  const activeLease = {
    id: '01990000-8800-7000-8000-000000000099',
    tenantId: principal.context.tenantId,
    siteId: SITE_ID,
    status: 'ACTIVE',
    armedBy: principal.principalId,
    armedAt: new Date(now - 600_000).toISOString(),
    expiresAt: new Date(now + 4 * 3_600_000).toISOString(),
    allowedCapabilities: ['SET_CHILLED_WATER_TEMPERATURE_SETPOINT'],
    maxSetpointStepC: 1.0,
    supplyTempMinC: 5.0,
    supplyTempMaxC: 12.0,
    zoneTempMaxC: 26.0,
    minRunningChillers: 1,
    maxCommandsPerHour: 2,
    deadbandC: 0.3,
    manualBackoffUntil: new Date(now + 30 * 60_000).toISOString(),
    manualBackoffReason: 'manual_override_backoff',
    revision: 1,
    createdAt: new Date(now - 600_000).toISOString(),
    updatedAt: new Date(now - 60_000).toISOString(),
  };
  gateway.autoControlLeaseState = {
    tenantId: principal.context.tenantId,
    siteId: SITE_ID,
    confirmationMode: 'AUTO_WITH_LEASE',
    activeLease,
    latestLease: activeLease,
  };
  gateway.optimizationExecutionMonitors = [
    {
      id: '01990000-8800-7000-8000-000000000301',
      tenantId: principal.context.tenantId,
      siteId: SITE_ID,
      leaseId: activeLease.id,
      recommendationId: '01a12bdc-97d7-7e50-bc97-f14cb86800bc',
      commandIntentId: '01990000-8800-7000-8000-000000000401',
      deviceId: '018f3e00-4000-7000-8000-000000000001',
      targetCapability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
      baselineSupplyTempC: 6.0,
      appliedSupplyTempC: 5.0,
      restoreSupplyTempC: 6.0,
      supplyTempMinC: 5.0,
      zoneTempMaxC: 26.0,
      supplyTempMaxC: 12.0,
      rawRecommendedValue: 4.5,
      clampedValue: 5.0,
      guardrailVerdict: 'CLAMPED',
      startedAt: new Date(now - 10 * 60_000).toISOString(),
      windowEndsAt: new Date(now + 20 * 60_000).toISOString(),
      status: 'OBSERVING',
      createdAt: new Date(now - 10 * 60_000).toISOString(),
      updatedAt: new Date(now - 60_000).toISOString(),
    },
    {
      id: '01990000-8800-7000-8000-000000000302',
      tenantId: principal.context.tenantId,
      siteId: SITE_ID,
      leaseId: activeLease.id,
      recommendationId: '01a12bdc-97d7-7e50-bc97-f14cb86800bd',
      commandIntentId: null,
      deviceId: '018f3e00-4000-7000-8000-000000000001',
      targetCapability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
      baselineSupplyTempC: 6.5,
      appliedSupplyTempC: 5.5,
      restoreSupplyTempC: 6.5,
      supplyTempMinC: 5.0,
      zoneTempMaxC: 26.0,
      supplyTempMaxC: 12.0,
      guardrailVerdict: 'VETOED',
      startedAt: new Date(now - 25 * 60_000).toISOString(),
      windowEndsAt: new Date(now + 5 * 60_000).toISOString(),
      status: 'MANUAL_OVERRIDE',
      tripReason: 'cabinet_setpoint_manual_override',
      createdAt: new Date(now - 25 * 60_000).toISOString(),
      updatedAt: new Date(now - 20 * 60_000).toISOString(),
    },
  ];

  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);

  const leasePanel = page.getByRole('region', { name: '自动控制租约' });
  const backoffStatus = leasePanel.getByRole('status').filter({ hasText: 'AI 自动调节退避保护中' });
  await expect(backoffStatus).toBeVisible();
  await expect(backoffStatus).toContainText('检测到人工干预设定值，AI 自动调节已进入退避保护窗口');
  await expect(backoffStatus).toContainText('退避至');
  await expect(backoffStatus).not.toContainText('manual_override_backoff');
  await expect(leasePanel).toContainText('调节死区 0.3 °C');

  const monitorsTable = page.getByRole('table', { name: '30 分钟执行监护' });
  await expect(monitorsTable).toContainText('供水 5.0 至 12.0 °C');
  await expect(monitorsTable).toContainText('原建议 4.5 °C，限幅为 5.0 °C');
  await expect(monitorsTable).toContainText('护栏：已按安全边界限幅');
  await expect(monitorsTable).toContainText('人工接管退出');
  await expect(monitorsTable).toContainText('无下行指令');
  await expect(monitorsTable).not.toContainText('CLAMPED');
  await expect(monitorsTable).not.toContainText('VETOED');

  await leasePanel.getByRole('button', { name: '调整租约…' }).click();
  const dialog = page.getByRole('dialog', { name: '调整自动控制租约边界' });
  const deadbandInput = dialog.getByLabel('调节死区');
  await expect(deadbandInput).toHaveValue('0.3');
  await deadbandInput.fill('0.35');
  const minInput = dialog.getByLabel('供水温度下限');
  await expect(minInput).toHaveAttribute('min', '5');
  await expect(minInput).toHaveAttribute('placeholder', '5.0');
  await minInput.fill('4.5');
  await minInput.blur();
  await expect(dialog).toContainText('供水温度下限需在 5.0 至 20.0 °C 之间');

  await minInput.fill('5.0');
  await dialog.getByLabel('原因').fill('验证 5.0 °C 防冻下限可保存');
  await dialog.getByRole('textbox', { name: '动态口令' }).fill('654321');
  await dialog.getByRole('button', { name: '确认更新租约' }).click();
  await expect(page.getByText('已更新限时自动控制租约')).toBeVisible();
  await expect(leasePanel).toContainText('5.0 至 12.0 °C');
  await expect(leasePanel).toContainText('调节死区 0.35 °C');
});

test('auto-control shadow execution mode arms, persists across update, renders SHADOW_RECORDED and setpoint_tracking_deviation cleanly', async ({ page, gateway }) => {
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);

  const leasePanel = page.getByRole('region', { name: '自动控制租约' });
  await leasePanel.getByRole('button', { name: '开启自动控制租约…' }).click();

  const armDialog = page.getByRole('dialog', { name: '开启限时自动控制租约' });
  await expect(armDialog.getByRole('radio', { name: /闭环自动控制/ })).toBeChecked();
  await armDialog.getByRole('radio', { name: /影子跟跑（只记录不下发）/ }).check();
  await expect(armDialog.getByRole('radio', { name: /影子跟跑（只记录不下发）/ })).toBeChecked();

  await armDialog.getByLabel('原因').fill('白班开启影子跟跑对比护栏表现');
  await armDialog.getByRole('textbox', { name: '动态口令' }).fill('123456');
  await armDialog.getByRole('button', { name: '确认开启租约' }).click();

  await expect(page.getByText('已开启影子跟跑租约')).toBeVisible();
  await expect(leasePanel).toContainText('影子跟跑中');
  await expect(leasePanel).toContainText('影子跟跑');
  await expect(leasePanel).toContainText('当前租约处于影子跟跑模式：每周期执行完整物理护栏校验，仅记录推荐与限幅对照轨迹，不下发设备指令。');

  const armWrite = gateway.writes.find((write) => write.method === 'PUT' && write.path.endsWith('/optimization/auto-control-lease'));
  expect(armWrite?.body?.executionMode).toBe('SHADOW');

  // Reopen via 调整租约… and confirm SHADOW mode prefills and persists with updated deadband.
  await leasePanel.getByRole('button', { name: '调整租约…' }).click();
  const editDialog = page.getByRole('dialog', { name: '调整自动控制租约边界' });
  await expect(editDialog.getByRole('radio', { name: /影子跟跑（只记录不下发）/ })).toBeChecked();
  await editDialog.getByLabel('调节死区').fill('0.4');
  await editDialog.getByLabel('原因').fill('调整影子跟跑死区参数');
  await editDialog.getByRole('textbox', { name: '动态口令' }).fill('654321');
  await editDialog.getByRole('button', { name: '确认更新租约' }).click();

  await expect(page.getByText('已更新影子跟跑租约')).toBeVisible();
  await expect(leasePanel).toContainText('影子跟跑中');
  await expect(leasePanel).toContainText('调节死区 0.4 °C');

  // Seed SHADOW_RECORDED (with decisionId & traceId) and TRIPPED_ROLLED_BACK (with setpoint_tracking_deviation).
  const now = Date.now();
  const decisionId = '01990000-8800-7000-8000-000000000501';
  const traceId = 'a1b2c3d4e5f60718293a4b5c6d7e8f90';
  gateway.optimizationExecutionMonitors = [
    {
      id: '01990000-8800-7000-8000-000000000511',
      tenantId: principal.context.tenantId,
      siteId: SITE_ID,
      leaseId: gateway.autoControlLeaseState.activeLease.id,
      recommendationId: '01a12bdc-97d7-7e50-bc97-f14cb86800ba',
      commandIntentId: null,
      deviceId: '018f3e00-4000-7000-8000-000000000001',
      targetCapability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
      baselineSupplyTempC: 7.1,
      appliedSupplyTempC: 8.1,
      restoreSupplyTempC: 7.1,
      zoneTempMaxC: 26.0,
      supplyTempMinC: 6.0,
      supplyTempMaxC: 12.0,
      rawRecommendedValue: 8.6,
      clampedValue: 8.1,
      guardrailVerdict: 'CLAMPED',
      executionMode: 'SHADOW',
      decisionId,
      traceId,
      startedAt: new Date(now - 12 * 60_000).toISOString(),
      windowEndsAt: new Date(now + 18 * 60_000).toISOString(),
      status: 'SHADOW_RECORDED',
      createdAt: new Date(now - 12 * 60_000).toISOString(),
      updatedAt: new Date(now - 12 * 60_000).toISOString(),
    },
    {
      id: '01990000-8800-7000-8000-000000000512',
      tenantId: principal.context.tenantId,
      siteId: SITE_ID,
      leaseId: gateway.autoControlLeaseState.activeLease.id,
      recommendationId: '01a12bdc-97d7-7e50-bc97-f14cb86800be',
      commandIntentId: '01990000-8800-7000-8000-000000000521',
      deviceId: '018f3e00-4000-7000-8000-000000000001',
      targetCapability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT',
      baselineSupplyTempC: 7.5,
      appliedSupplyTempC: 8.5,
      restoreSupplyTempC: 7.5,
      zoneTempMaxC: 26.0,
      supplyTempMinC: 6.0,
      supplyTempMaxC: 12.0,
      executionMode: 'CLOSED_LOOP',
      startedAt: new Date(now - 45 * 60_000).toISOString(),
      windowEndsAt: new Date(now - 15 * 60_000).toISOString(),
      status: 'TRIPPED_ROLLED_BACK',
      tripReason: 'setpoint_tracking_deviation',
      rollbackCommandIntentId: '01990000-8800-7000-8000-000000000522',
      createdAt: new Date(now - 45 * 60_000).toISOString(),
      updatedAt: new Date(now - 20 * 60_000).toISOString(),
    },
  ];
  await page.reload();

  const monitorsTable = page.getByRole('table', { name: '30 分钟执行监护' });
  await expect(monitorsTable).toContainText('影子跟跑记录');
  await expect(monitorsTable).toContainText('影子');
  await expect(monitorsTable).toContainText('影子模式仅记录推荐轨迹，不下发设备');
  await expect(monitorsTable).toContainText('已记录决策轨迹');
  await expect(monitorsTable.getByText('已记录决策轨迹')).toHaveAttribute('title', `决策编号：${decisionId} · 链路追踪：${traceId}`);
  await expect(monitorsTable).not.toContainText(decisionId);
  await expect(monitorsTable).not.toContainText(traceId);
  await expect(monitorsTable).not.toContainText('SHADOW_RECORDED');

  await expect(monitorsTable).toContainText('已自动回退');
  await expect(monitorsTable).toContainText('闭环');
  await expect(monitorsTable).toContainText('设定值超时未收敛，已自动回退并退出租约');
  await expect(monitorsTable).not.toContainText('setpoint_tracking_deviation');

  // Switch back to CLOSED_LOOP via 调整租约… and confirm header returns to 限时自动控制租约生效中.
  await leasePanel.getByRole('button', { name: '调整租约…' }).click();
  const switchDialog = page.getByRole('dialog', { name: '调整自动控制租约边界' });
  await switchDialog.getByRole('radio', { name: /闭环自动控制/ }).check();
  await switchDialog.getByLabel('原因').fill('影子验证完成，切换回闭环自动控制');
  await switchDialog.getByRole('textbox', { name: '动态口令' }).fill('123456');
  await switchDialog.getByRole('button', { name: '确认更新租约' }).click();

  await expect(page.getByText('已更新限时自动控制租约')).toBeVisible();
  await expect(leasePanel).toContainText('限时自动控制租约生效中');
  await expect(leasePanel).toContainText('闭环下发');
});

test('governance summary lazy-loads on analysis tab without recommendation, shows canary badge, COP bins, and flywheel breakdown', async ({ page, gateway }) => {
  gateway.optimization = null;
  gateway.optimizationGovernanceSummary = {
    tenantId: principal.context.tenantId,
    siteId: SITE_ID,
    windowDays: 14,
    generatedAt: '2026-11-18T06:00:00Z',
    activeSolver: {
      servingRef: 'builtin://chiller-cop-model',
      source: 'SITE_LOOP_CANARY',
      loopKey: 'chilled_water_supply_temp',
      deploymentRevisionId: '01910000-0000-7000-8000-00000000000b',
    },
    tickCounts: {
      aiClosedLoop: 32,
      aiShadow: 18,
      ruleBaseline: 40,
      manualOverride: 4,
    },
    copComparison: {
      matchedBinCount: 2,
      matchedAiSamples: 22,
      matchedBaselineSamples: 29,
      weightedBaselineCOP: 4.42,
      weightedAiCOP: 4.79,
      weightedCopUpliftPct: 8.3,
      totalEstimatedSavedKWh: 14.8,
      cells: [
        {
          wetBulbMinC: 22.0,
          wetBulbMaxC: 24.0,
          coolingLoadMinKW: 400,
          coolingLoadMaxKW: 600,
          baselineSamples: 24,
          aiSamples: 18,
          baselineCOP: 4.42,
          aiCOP: 4.79,
          copUpliftPct: 8.3,
          estimatedSavedKWh: 14.8,
        },
        {
          wetBulbMinC: 24.0,
          wetBulbMaxC: 26.0,
          coolingLoadMinKW: 600,
          coolingLoadMaxKW: 800,
          baselineSamples: 5,
          aiSamples: 4,
          baselineCOP: 4.15,
          aiCOP: 4.41,
          copUpliftPct: 6.3,
          estimatedSavedKWh: 5.4,
        },
      ],
    },
    flywheel: {
      totalSamples: 9,
      guardrailClamped: 3,
      guardrailVetoed: 2,
      manualOverride: 1,
      monitorTripped: 1,
      trackingDeviation: 2,
    },
  };

  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  await expect(page.getByText('还没有优化建议')).toBeVisible();
  expect(gateway.governanceReads).toBe(0);

  await page.getByRole('tab', { name: '模型与负荷分析' }).click();
  await expect.poll(() => gateway.governanceReads).toBe(1);

  const analysisPanel = page.getByRole('tabpanel', { name: '模型与负荷分析' });
  const govPanel = analysisPanel.getByRole('region', { name: '策略路由与运行治理' });
  await expect(govPanel).toBeVisible();
  await expect(govPanel).toContainText('回路灰度策略');
  await expect(govPanel).toContainText('冷机 COP 拟合策略');
  await expect(govPanel).toContainText('冷冻水供水温度回路');
  await expect(govPanel).toContainText('+8.3%');
  await expect(govPanel).toContainText('14.8 kWh');
  await expect(govPanel).not.toContainText('SITE_LOOP_CANARY');
  await expect(govPanel).not.toContainText('builtin://chiller-cop-model');
  await expect(govPanel).not.toContainText('01910000-0000-7000-8000-00000000000b');

  const tickRegion = govPanel.getByRole('region', { name: '近 14 天运行周期分布' });
  await expect(tickRegion).toContainText('AI 闭环周期');
  await expect(tickRegion).toContainText('32');
  await expect(tickRegion).toContainText('AI 影子跟跑');
  await expect(tickRegion).toContainText('18');
  await expect(tickRegion).toContainText('规则基准周期');
  await expect(tickRegion).toContainText('40');
  await expect(tickRegion).toContainText('人工接管周期');
  await expect(tickRegion).toContainText('4');

  const flywheelRegion = govPanel.getByRole('region', { name: '安全护栏负样本飞轮' });
  await expect(flywheelRegion).toContainText('负样本 9');
  await expect(flywheelRegion).toContainText('护栏限幅修正');
  await expect(flywheelRegion).toContainText('物理安全拦截');
  await expect(flywheelRegion).toContainText('人工接管覆写');
  await expect(flywheelRegion).toContainText('跟踪偏差回退');
  await expect(flywheelRegion).toContainText('监控越界回退');

  const copTable = govPanel.getByRole('table', { name: '同湿球与冷负荷分箱 COP 对照' });
  await expect(copTable).toContainText('22.0 至 24.0 °C');
  await expect(copTable).toContainText('400 至 600 kW');
  await expect(copTable).toContainText('4.42');
  await expect(copTable).toContainText('4.79');
  await expect(copTable).toContainText('18 / 24');
  await expect(copTable).toContainText('24.0 至 26.0 °C');

  // Empty matched bins render calm empty state while keeping solver routing visible.
  gateway.optimizationGovernanceSummary = {
    ...gateway.optimizationGovernanceSummary,
    activeSolver: {
      servingRef: 'https://external-optimizer.example/solve',
      source: 'DEPLOYMENT_BINDING',
      loopKey: 'chilled_water_supply_temp',
      deploymentRevisionId: null,
    },
    copComparison: {
      matchedBinCount: 0,
      matchedAiSamples: 0,
      matchedBaselineSamples: 0,
      weightedBaselineCOP: null,
      weightedAiCOP: null,
      weightedCopUpliftPct: null,
      totalEstimatedSavedKWh: 0,
      cells: [],
    },
  };
  await page.reload();
  const reloadedGov = page.getByRole('region', { name: '策略路由与运行治理' });
  await expect(reloadedGov).toContainText('站点部署绑定');
  await expect(reloadedGov).toContainText('外部协同优化服务');
  await expect(reloadedGov).not.toContainText('https://external-optimizer.example/solve');
  await expect(reloadedGov).toContainText('尚无满足同湿球与同冷负荷分箱条件的对照周期，积累闭环与基准周期后将自动展示。');
});

test('canary bindings dialog manages loop routing and negative samples drawer inspects & exports flywheel', async ({ page, gateway }) => {
  void gateway;
  await page.goto(`/energy-analysis/optimization?site=${SITE_ID}`);
  await page.getByRole('tab', { name: '模型与负荷分析' }).click();

  const govPanel = page.getByRole('region', { name: '策略路由与运行治理' });
  await expect(govPanel).toBeVisible();

  // 1. Open CanaryBindingsDialog and test allowlist validation + CRUD.
  await govPanel.getByRole('button', { name: '管理灰度路由' }).click();
  const canaryDialog = page.getByRole('dialog');
  await expect(canaryDialog).toContainText('站点控制回路灰度路由管理');
  await expect(canaryDialog).toContainText('冷冻泵变频回路');
  await expect(canaryDialog).toContainText('ai-solver-service:8090');

  // Switch solver mode to external Python service and submit an unallowlisted host.
  await canaryDialog.getByRole('combobox', { name: '目标优化策略' }).click();
  await page.getByRole('option', { name: '外部 Python 优化服务' }).click();
  await canaryDialog.getByLabel('外部服务地址').fill('http://evil.example.com:8090');
  await canaryDialog.getByRole('button', { name: '保存灰度绑定' }).click();
  await expect(canaryDialog).toContainText('外部优化服务地址不在允许名单内');

  // Click the allowlisted host badge to fill http://ai-solver-service:8090 and save.
  await canaryDialog.getByRole('button', { name: 'ai-solver-service:8090' }).click();
  await canaryDialog.getByRole('button', { name: '保存灰度绑定' }).click();
  await expect(canaryDialog).toContainText('全冷站主回路');
  await expect(canaryDialog).toContainText('外部 Python 优化服务');

  // Revert PLANT back to default.
  const plantRow = canaryDialog.getByRole('row', { name: /全冷站主回路/ });
  await plantRow.getByRole('button', { name: '切回默认' }).click();
  await expect(canaryDialog.getByRole('row', { name: /全冷站主回路/ })).toHaveCount(0);

  await canaryDialog.getByRole('button', { name: '关闭' }).click();
  await expect(canaryDialog).toBeHidden();

  // 2. Open NegativeSamplesDrawer, filter by kind, expand trace details, and export JSONL.
  await govPanel.getByRole('button', { name: '查看负样本明细' }).click();
  const drawer = page.getByRole('dialog');
  await expect(drawer).toContainText('安全护栏负样本明细与复盘导出');
  await expect(drawer).toContainText('共 2 条');
  await expect(drawer).toContainText('单步变化超限');

  await drawer.getByRole('combobox', { name: '按负样本类别筛选' }).click();
  await page.getByRole('option', { name: '护栏限幅修正' }).click();
  await expect(drawer).toContainText('共 1 条');

  await drawer.getByRole('button', { name: '展开护栏判定细节' }).first().click();
  await expect(drawer).toContainText('安全护栏判定轨迹');
  await expect(drawer).toContainText('单步调幅限速');
  await expect(drawer).toContainText('10.50 → 8.50');
  await expect(drawer).toContainText('发生时刻采集快照');
  await expect(drawer).toContainText('520 kW');

  const downloadPromise = page.waitForEvent('download');
  await drawer.getByRole('button', { name: '导出 JSONL' }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe('negative-samples-14d.jsonl');
});
