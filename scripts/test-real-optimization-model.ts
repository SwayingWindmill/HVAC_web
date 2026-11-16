import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  buildOptimizationWorkOrderDraft,
  buildReadinessSummary,
  findActiveRecommendationWorkOrder,
  isUnscheduledReady,
  parseRecommendation,
  parseShadowEvaluation,
  preparationReadinessReasons,
  type PublishedRecommendation,
  type PublishedShadowEvaluation,
} from '../apps/hvac-web/src/features/optimization/optimization-model.ts';
import {
  buildArmLeasePayload,
  formatAutoControlCapability,
  formatAutoControlTripReason,
  initialArmLeaseValues,
  validateArmLeaseForm,
} from '../apps/hvac-web/src/features/optimization/auto-control-model.ts';
import { stagingReason, stagingView } from '../apps/hvac-web/src/features/optimization/staging-model.ts';
import type { StagingEvaluation } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';

const publishedFixture = JSON.parse(
  readFileSync(new URL('../apps/hvac-web/e2e/fixtures/optimization-latest.json', import.meta.url), 'utf8'),
) as PublishedRecommendation;

const shadowFixture = JSON.parse(
  readFileSync(new URL('../apps/hvac-web/e2e/fixtures/optimization-shadow-latest.json', import.meta.url), 'utf8'),
) as PublishedShadowEvaluation;

test('buildReadinessSummary reports 6/6 ready with remote setpoint dispatch when writable setpoint is active', () => {
  const recommendation = parseRecommendation(publishedFixture);
  assert.equal(recommendation.id, '01a12bdc-97d7-7e50-bc97-f14cb86800ba');
  assert.equal(recommendation.restoreSupplyTempC, 7.06);

  const summary = buildReadinessSummary({
    recommendation,
    preparation: {
      trigger: 'SCHEDULE',
      status: 'PREPARED',
      reason: null,
      firedAt: '2026-10-11T16:20:00Z',
      completedAt: '2026-10-11T16:20:02Z',
      responseCoefficient: { status: 'CALIBRATED', calibratedAt: '2026-10-01' },
      tenantStrategy: { status: 'CONFIRMED' },
    },
    hasWritableSetpoint: true,
  });

  assert.equal(summary.readyCount, 6);
  assert.equal(summary.totalCount, 6);
  assert.equal(summary.headline, '前置与安全条件：6/6 就绪 · 可远程下发设定点');
  assert.equal(summary.items.length, 6);
  assert.deepEqual(
    summary.items.map((item) => [item.key, item.status]),
    [
      ['supply_temp', 'ready'],
      ['zone_comfort', 'ready'],
      ['maintenance_locks', 'ready'],
      ['live_revalidation', 'ready'],
      ['forecast_and_tariff', 'ready'],
      ['execution_channel', 'ready'],
    ],
  );
});

test('buildReadinessSummary reports 5/6 ready with manual work-order channel when site has no writable setpoint', () => {
  const recommendation = parseRecommendation(publishedFixture);
  const summary = buildReadinessSummary({
    recommendation,
    preparation: null,
    hasWritableSetpoint: false,
  });

  assert.equal(summary.readyCount, 5);
  assert.equal(summary.totalCount, 6);
  assert.equal(summary.headline, '前置与安全条件：5/6 就绪 · 仅人工调温工单');
  const channel = summary.items.find((item) => item.key === 'execution_channel');
  assert.equal(channel?.status, 'manual');
  assert.match(channel?.detail ?? '', /人工调温工单/);

  const pendingChannel = buildReadinessSummary({
    recommendation,
    preparation: null,
    hasWritableSetpoint: null,
  });
  assert.equal(pendingChannel.readyCount, 5);
  assert.equal(pendingChannel.headline, '前置与安全条件：5/6 就绪 · 执行通道待核验');
  assert.equal(pendingChannel.items.find((item) => item.key === 'execution_channel')?.status, 'unknown');
});

test('buildReadinessSummary surfaces exact missing preparation input without fabricating green checks', () => {
  const summary = buildReadinessSummary({
    recommendation: null,
    preparation: {
      trigger: 'SCHEDULE',
      status: 'FAILED',
      reason: 'ZONE_TEMPERATURE_MISSING',
      firedAt: '2026-10-30T04:20:00Z',
      completedAt: '2026-10-30T04:20:02Z',
      responseCoefficient: { status: 'NOT_CALIBRATED', calibratedAt: null },
      tenantStrategy: { status: 'PENDING' },
    },
    hasWritableSetpoint: false,
  });

  assert.equal(summary.readyCount, 0);
  assert.equal(summary.headline, '前置与安全条件：读不到可用的实时室内温度 · 仅人工调温工单');
  const comfort = summary.items.find((item) => item.key === 'zone_comfort');
  const live = summary.items.find((item) => item.key === 'live_revalidation');
  assert.equal(comfort?.status, 'blocked');
  assert.equal(live?.status, 'blocked');
  const maintenance = summary.items.find((item) => item.key === 'maintenance_locks');
  assert.equal(maintenance?.status, 'unknown');
});

test('buildOptimizationWorkOrderDraft builds EXTERNAL origin work-order prefill and deduplicates unclosed work orders', () => {
  const recommendation = parseRecommendation(publishedFixture);
  const draft = buildOptimizationWorkOrderDraft(recommendation);

  assert.equal(draft.initial.title, '人工调整冷冻水供水温度至 8.1 °C');
  assert.equal(draft.initial.priority, 'MEDIUM');
  assert.match(draft.initial.description, /7\.1 °C.*8\.1 °C/);
  assert.match(draft.initial.description, /60 kWh/);
  assert.match(draft.initial.description, /估算电费 ¥51/);
  assert.match(draft.initial.description, /24\.4 °C/);
  assert.match(draft.initial.description, /观察 30 分钟.*恢复至 7\.1 °C/);
  assert.deepEqual(draft.sourceReferences, [
    {
      domain: 'EXTERNAL',
      resourceId: 'optimization-recommendation:01a12bdc-97d7-7e50-bc97-f14cb86800ba',
      relationship: 'ORIGIN',
    },
  ]);

  const cancelledOrder = {
    workOrderId: 'wo-cancelled',
    status: 'CANCELLED',
    createdAt: '2026-10-11T17:00:00Z',
    sourceReferences: draft.sourceReferences,
  };
  const openOrder = {
    workOrderId: 'wo-open',
    status: 'OPEN',
    createdAt: '2026-10-11T17:05:00Z',
    sourceReferences: draft.sourceReferences,
  };

  assert.equal(findActiveRecommendationWorkOrder([cancelledOrder], recommendation.id), null);
  assert.equal(findActiveRecommendationWorkOrder([cancelledOrder, openOrder], recommendation.id)?.workOrderId, 'wo-open');
});

test('parseShadowEvaluation resolves chiller asset names, marks selected step, and formats COP diagnostics', () => {
  const names = new Map([['018f3e00-4000-7000-8000-000000000001', 'CHILLER-01']]);
  const shadow = parseShadowEvaluation(shadowFixture, names);

  assert.equal(shadow.outcome, 'FEASIBLE');
  assert.equal(shadow.runId, '01a12bdc-97d7-7a23-a6df-9fb2cfcb06b8');
  assert.equal(shadow.condenserResetNote, '站点未登记冷却水设定点，暂未开放冷却水温度重置');
  assert.equal(shadow.energyResponse?.fittedKWhPerSupplyC, -52.4);
  assert.equal(shadow.energyResponse?.policyKWhPerSupplyC, -60);
  assert.equal(shadow.candidates.length, 3);
  assert.deepEqual(
    shadow.candidates.map((row) => [row.supplyTempC, row.selected, row.feasible]),
    [
      [6.06, false, true],
      [7.06, false, true],
      [8.06, true, true],
    ],
  );
  assert.equal(shadow.fit?.chillers.length, 1);
  assert.equal(shadow.fit?.chillers[0]?.displayName, 'CHILLER-01');
  assert.equal(shadow.fit?.chillers[0]?.statusLabel, '已拟合');
  assert.equal(shadow.fit?.chillers[0]?.samples, 874);
  assert.equal(shadow.fit?.chillers[0]?.capacityKW, 345.6);
  assert.equal(shadow.fit?.chillers[0]?.coefficients?.intercept, 2.84);
  assert.equal(shadow.fit?.setAsideTotal, 38);
  assert.deepEqual(
    shadow.fit?.loadAllocationBreakdown.filter((item) => item.count > 0),
    [{ key: 'single', label: '单机运行', count: 874 }],
  );
});

test('parseShadowEvaluation preserves NO_USABLE_MODEL per-chiller rejection reasons in plain Chinese', () => {
  const degraded: PublishedShadowEvaluation = {
    ...shadowFixture,
    outcome: 'NO_USABLE_MODEL',
    reason: 'no Chiller has a usable COP fit',
    cvRmse: null,
    energyResponse: undefined,
    candidates: undefined,
    summary: undefined,
    fit: {
      history: shadowFixture.fit!.history,
      cvRmsePower: null,
      chillers: [
        {
          deviceId: '018f3e00-4000-7000-8000-000000000001',
          status: 'INSUFFICIENT_DATA',
          reason: 'fewer than 96 steady 15-minute samples',
          samples: 42,
        },
        {
          deviceId: '018f3e00-4000-7000-8000-000000000099',
          status: 'REJECTED',
          reason: 'the fit says a warmer chilled-water supply lowers COP',
          samples: 120,
        },
      ],
    },
  };
  const shadow = parseShadowEvaluation(degraded, new Map([['018f3e00-4000-7000-8000-000000000001', 'CHILLER-01']]));
  assert.equal(shadow.outcome, 'NO_USABLE_MODEL');
  assert.equal(shadow.summary, null);
  assert.equal(shadow.fit?.chillers[0]?.displayName, 'CHILLER-01');
  assert.equal(shadow.fit?.chillers[0]?.statusLabel, '稳态样本不足');
  assert.match(shadow.fit?.chillers[0]?.reasonLabel ?? '', /96/);
  assert.equal(shadow.fit?.chillers[1]?.displayName, '未命名冷机');
  assert.match(shadow.fit?.chillers[1]?.reasonLabel ?? '', /物理规律/);
});

test('stagingView turns the service evaluation into cards, paused blockers and plain reasons without its internal sentences', () => {
  const evaluation: StagingEvaluation = {
    tenantId: '018f3d00-0000-7000-8000-000000000001',
    siteId: '018f3e00-1000-7000-8000-000000000001',
    evaluatedAt: '2026-11-15T02:05:00Z',
    confirmationMode: 'MANUAL_CONFIRM',
    siteControlMode: 'REMOTE_AUTO',
    recommendations: [
      {
        id: 'r-aux', kind: 'IDLE_AUXILIARY_STOP', confirmationMode: 'MANUAL_CONFIRM', requiresSupervisorReview: true, requiresTotp: true,
        suggestedCapability: 'STOP', suggestedCommandPointId: 'p-tower-4-stop', targetAssetId: 'a-tower-4', targetAssetType: 'COOLING_TOWER',
        predictedPowerSavingKW: 6.5, estimatedHourlySavingKWh: 6.5, estimatedShiftSavingKWh: 52, shiftHours: 8, continuousIdleMinutes: 52,
        rationale: 'IDLE STOP', preconditions: ['MANUAL_CONFIRM'],
      },
      {
        id: 'r-chiller', kind: 'CHILLER_STAGE_DOWN', confirmationMode: 'MANUAL_CONFIRM', requiresSupervisorReview: true, requiresTotp: true,
        suggestedCapability: 'STOP_UNIT_SEQUENCE', targetAssetId: 'a-chiller-c', targetAssetName: 'chiller-c', targetAssetType: 'CHILLER',
        predictedPowerSavingKW: 42, estimatedHourlySavingKWh: 42, estimatedShiftSavingKWh: 336, shiftHours: 8, currentTotalPowerKW: 507.5,
        currentCoolingLoadKW: 1380, remainingCapacityKW: 1760, headroomMarginFraction: 0.216, copBefore: 4.1, projectedCopAfter: 4.6,
        rationale: 'STOP_UNIT_SEQUENCE', preconditions: [],
      },
    ],
    diagnostics: [
      { scope: 'IDLE_AUXILIARY_STOP', assetId: 'a-cwp-4', status: 'SUPPRESSED', reason: 'ASSET_CONTROL_LOCKED', detail: 'MANUAL_LOCK' },
      { scope: 'IDLE_AUXILIARY_STOP', assetId: 'a-cwp-1', status: 'SUPPRESSED', reason: 'PAIRED_CHILLER_RUNNING', detail: 'RUNNING' },
      { scope: 'CHILLER_STAGE_DOWN', assetId: 'a-chiller-a', status: 'SUPPRESSED', reason: 'CHILLER_METER_STALE', detail: 'btu-calc' },
    ],
  };
  const names = new Map([['a-tower-4', '4 号冷却塔'], ['a-chiller-c', 'C 机组'], ['a-cwp-4', '4 号冷却泵'], ['a-chiller-a', 'A 机组']]);
  const view = stagingView(evaluation, (assetId) => names.get(assetId));

  // Stage-down first; the registry's name wins over the service's code.
  assert.deepEqual(view.cards.map((card) => [card.kind, card.assetName, card.pointId]), [['CHILLER_STAGE_DOWN', 'C 机组', null], ['IDLE_AUXILIARY_STOP', '4 号冷却塔', 'p-tower-4-stop']]);
  assert.deepEqual(view.cards[0].capacity, { loadKW: 1380, remainingKW: 1760, headroomPercent: 22 });
  assert.deepEqual(view.totals, { savingKW: 48.5, shiftKWh: 388, shiftHours: 8, powerBeforeKW: 507.5 });
  // A chiller still running for its pump is the plant's state, not a blocker.
  assert.deepEqual(view.paused.map((item) => [item.name, item.why, item.controlFix]), [
    ['4 号冷却泵', '已锁定，解锁后才会给停机建议', true],
    ['A 机组', '冷机电表读数不可用，不给减台建议', false],
  ]);
  assert.equal(stagingReason('SOMETHING_NEW'), '这次没有给出建议');
  assert.equal(stagingReason('CHILLER_THERMAL_DELTA_ACTIVE'), '对应主机进出水还有温差，还在换热，不能停');
  assert.equal(stagingReason('COOLING_LOAD_DWELL_STALE'), '冷站制冷量读数不可信，暂停减台评估');
  assert.equal(JSON.stringify(view).includes('MANUAL'), false);
});

test('off-hours whole-chiller stops come first, and the schedule windows read as plain reasons', () => {
  const draft = (id: string, kind: StagingEvaluation['recommendations'][number]['kind'], targetAssetId: string, kw: number): StagingEvaluation['recommendations'][number] => ({
    id, kind, confirmationMode: 'MANUAL_CONFIRM', requiresSupervisorReview: true, requiresTotp: true, suggestedCapability: 'STOP_UNIT_SEQUENCE',
    targetAssetId, targetAssetType: 'CHILLER', predictedPowerSavingKW: kw, estimatedHourlySavingKWh: kw, estimatedShiftSavingKWh: kw * 8, shiftHours: 8,
    rationale: kind, preconditions: [],
  });
  const view = stagingView({
    tenantId: '018f3d00-0000-7000-8000-000000000001', siteId: '018f3e00-1000-7000-8000-000000000001', evaluatedAt: '2026-11-15T14:05:00Z',
    confirmationMode: 'MANUAL_CONFIRM', siteControlMode: 'REMOTE_AUTO',
    recommendations: [draft('r-aux', 'IDLE_AUXILIARY_STOP', 'a-tower-1', 6), draft('r-off-b', 'OFF_HOURS_STOP', 'a-chiller-b', 80), draft('r-off-a', 'OFF_HOURS_STOP', 'a-chiller-a', 95)],
    diagnostics: [
      { scope: 'CHILLER_STAGE_DOWN', status: 'NO_OP', reason: 'OFF_HOURS_WINDOW_ACTIVE', detail: '当前处于非供冷运行时间窗口' },
      { scope: 'OFF_HOURS_STOP', assetId: 'a-chiller-c', status: 'NO_OP', reason: 'CHILLER_ALREADY_STOPPED', detail: '' },
      { scope: 'IDLE_AUXILIARY_STOP', status: 'SUPPRESSED', reason: 'MAINTENANCE_WINDOW_ACTIVE', detail: '' },
    ],
  }, () => undefined);
  assert.deepEqual(view.cards.map((card) => card.id), ['r-off-a', 'r-off-b', 'r-aux']);
  assert.equal(view.stageDownIdle, '现在是运行计划里的停机时段，改为给整台停机建议');
  assert.deepEqual(view.paused.map((item) => [item.name, item.why]), [['辅机停机', '今天在运行计划的检修窗口内，停机建议已暂停']]);
});

test('#861 readiness summary handles NOT_SCHEDULED with NOT_CALIBRATED and PENDING reasons as well as CALIBRATED with null firedAt', () => {
  const unready = {
    trigger: null,
    status: 'NOT_SCHEDULED' as const,
    reason: null,
    firedAt: null,
    completedAt: null,
    responseCoefficient: { status: 'NOT_CALIBRATED' as const, calibratedAt: null },
    tenantStrategy: { status: 'PENDING' as const },
  };
  assert.deepEqual(preparationReadinessReasons(unready), ['响应系数还没标定', '共享租户策略还没确认']);
  assert.equal(isUnscheduledReady(unready), false);

  const unreadySummary = buildReadinessSummary({
    recommendation: null,
    preparation: unready,
    hasWritableSetpoint: false,
  });
  assert.equal(unreadySummary.headline, '前置与安全条件：响应系数还没标定、共享租户策略还没确认 · 仅人工调温工单');
  assert.equal(unreadySummary.items.find((item) => item.key === 'forecast_and_tariff')?.status, 'blocked');
  assert.equal(unreadySummary.responseCoefficient?.status, 'NOT_CALIBRATED');
  assert.equal(unreadySummary.tenantStrategy?.status, 'PENDING');

  const readyUnscheduled = {
    trigger: null,
    status: 'NOT_SCHEDULED' as const,
    reason: null,
    firedAt: null,
    completedAt: null,
    responseCoefficient: { status: 'CALIBRATED' as const, calibratedAt: '2026-10-29T08:30:00Z' },
    tenantStrategy: { status: 'CONFIRMED' as const },
  };
  assert.deepEqual(preparationReadinessReasons(readyUnscheduled), []);
  assert.equal(isUnscheduledReady(readyUnscheduled), true);

  const readySummary = buildReadinessSummary({
    recommendation: parseRecommendation(publishedFixture),
    preparation: readyUnscheduled,
    hasWritableSetpoint: true,
    timeZone: 'Asia/Shanghai',
  });
  assert.equal(readySummary.responseCoefficient?.detail, '已标定（标定日期 2026-10-29）');
  assert.equal(readySummary.tenantStrategy?.label, '已确认');
});

test('formatAutoControlTripReason translates backend trip reasons cleanly without snake_case leaks', () => {
  assert.equal(formatAutoControlTripReason('telemetry_unhealthy_over_5m'), '关键遥测连续异常超过 5 分钟，已触发保护退出');
  assert.equal(
    formatAutoControlTripReason('supply_temperature_exceeded_max (12.40C > 12.00C)'),
    '冷冻水供水温度超出上限（12.40 °C > 12.00 °C）',
  );
  assert.equal(
    formatAutoControlTripReason('zone_temperature_exceeded_max banquet_room_temperature (26.80C > 26.00C)'),
    '室内温度超出上限（26.80 °C > 26.00 °C）',
  );
  assert.equal(
    formatAutoControlTripReason('chiller_power_exceeded_90pct chiller-a (385.0kW > 0.90*400.0kW)'),
    '冷机运行功率超过额定 90%（385.0 kW > 0.90 × 400.0 kW）',
  );
  assert.equal(formatAutoControlTripReason('superseded by new operator lease'), '已被操作员重新开通的新租约替换');
  assert.equal(formatAutoControlTripReason('夜间巡检，暂时撤回自动租约'), '夜间巡检，暂时撤回自动租约');
  assert.equal(formatAutoControlTripReason('some_unknown_internal_code'), '触发安全边界保护退出');
  assert.equal(formatAutoControlCapability('SET_CHILLED_WATER_TEMPERATURE_SETPOINT'), '调整冷冻水供水温度设定');
});

test('arm lease form validation matches auto_control_lease.go bounds', () => {
  const valid = {
    ...initialArmLeaseValues(),
    reason: '白班负荷平稳，开启自动调温',
    verificationCode: '123456',
  };
  const built = buildArmLeasePayload(valid, 0);
  assert.equal(built.ok, true);
  if (built.ok) {
    assert.equal(built.payload.expiresInHours, 8);
    assert.equal(built.payload.supplyTempMinC, 6);
    assert.equal(built.payload.supplyTempMaxC, 12);
    assert.equal(built.payload.maxCommandsPerHour, 2);
  }

  const invalid = validateArmLeaseForm({
    ...valid,
    expiresInHours: '25',
    supplyTempMinC: '13',
    supplyTempMaxC: '12',
    maxSetpointStepC: '2.5',
    zoneTempMaxC: '33',
    minRunningChillers: '-1',
    maxCommandsPerHour: '7',
    allowedCapabilities: [],
    reason: '   ',
    verificationCode: '12ab',
  }, { includeSubmitOnly: true });
  assert.ok(invalid.expiresInHours);
  assert.ok(invalid.supplyTempMaxC);
  assert.ok(invalid.maxSetpointStepC);
  assert.ok(invalid.zoneTempMaxC);
  assert.ok(invalid.minRunningChillers);
  assert.ok(invalid.maxCommandsPerHour);
  assert.ok(invalid.allowedCapabilities);
  assert.ok(invalid.reason);
  assert.ok(invalid.verificationCode);
});
