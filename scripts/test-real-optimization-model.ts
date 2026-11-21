import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import {
  buildOptimizationWorkOrderDraft,
  buildReadinessSummary,
  findActiveRecommendationWorkOrder,
  formatAuxiliaryCapabilityLabel,
  formatAuxiliaryLoopLabel,
  formatAuxiliaryTargetValue,
  formatCoolingLoadBin,
  formatCopUpliftPct,
  formatGuardrailAction,
  formatGuardrailStage,
  formatLoopKey,
  formatNegativeSampleKind,
  formatNegativeSampleReason,
  formatSavedKWh,
  formatSolverRef,
  formatSolverSource,
  formatWetBulbBin,
  isUnscheduledReady,
  parseOptimizationGovernanceSummary,
  parseRecommendation,
  parseShadowEvaluation,
  preparationReadinessReasons,
  serializeNegativeSamplesJsonl,
  type PublishedOptimizationGovernanceSummary,
  type PublishedRecommendation,
  type PublishedShadowEvaluation,
} from '../apps/hvac-web/src/features/optimization/optimization-model.ts';
import {
  buildArmLeasePayload,
  formatAutoControlCapability,
  formatAutoControlExecutionMode,
  formatAutoControlTripReason,
  formatGuardrailVerdict,
  formatMonitorDecisionTrace,
  formatMonitorExecutionMode,
  formatMonitorLoopLabel,
  formatMonitorNumericValue,
  initialArmLeaseValues,
  MONITOR_STATUS_PRESENTATION,
  resolveActiveLeaseBackoff,
  resolveAutoControlExecutionMode,
  resolveMonitorExecutionMode,
  resolveMonitorUnit,
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
  assert.equal(formatAutoControlTripReason('supply_temperature_below_min'), '供水温度低于防冻下限，已触发保护退回');
  assert.equal(
    formatAutoControlTripReason('supply_temperature_below_min (4.60C < 5.00C)'),
    '供水温度低于防冻下限，已触发保护退回（4.60 °C < 5.00 °C）',
  );
  assert.equal(formatAutoControlTripReason('stale_or_bad_telemetry'), '实时传感器数据过期或质量异常，已暂停自动调节');
  assert.equal(
    formatAutoControlTripReason('device_fault_or_local_manual'),
    '设备存在故障报警或处于现场就地手动，自动调节已锁定',
  );
  assert.equal(
    formatAutoControlTripReason('cabinet_local_manual'),
    '设备存在故障报警或处于现场就地手动，自动调节已锁定',
  );
  assert.equal(
    formatAutoControlTripReason('manual_override_backoff'),
    '检测到人工干预设定值，AI 自动调节已进入退避保护窗口',
  );
  assert.equal(
    formatAutoControlTripReason('cabinet_setpoint_manual_override'),
    '检测到人工干预设定值，AI 自动调节已进入退避保护窗口',
  );
  assert.equal(formatAutoControlTripReason('within_deadband'), '目标差值在调节死区内，跳过下发以保护设备动作寿命');
  assert.equal(formatAutoControlTripReason('setpoint_tracking_deviation'), '设定值超时未收敛，已自动回退并退出租约');
  assert.equal(formatAutoControlTripReason('夜间巡检，暂时撤回自动租约'), '夜间巡检，暂时撤回自动租约');
  assert.equal(formatAutoControlTripReason('some_unknown_internal_code'), '触发安全边界保护退出');
  assert.equal(formatAutoControlCapability('SET_CHILLED_WATER_TEMPERATURE_SETPOINT'), '调整冷冻水供水温度设定');
  assert.equal(formatGuardrailVerdict('CLAMPED'), '已按安全边界限幅');
  assert.equal(formatGuardrailVerdict('ALLOWED'), '护栏校验通过');
  assert.equal(formatGuardrailVerdict('within_deadband'), '处于调节死区内，已跳过下发');
  assert.equal(formatGuardrailVerdict(undefined), null);
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
    assert.equal(built.payload.deadbandC, 0.2);
    assert.equal(built.payload.supplyTempMinC, 6);
    assert.equal(built.payload.supplyTempMaxC, 12);
    assert.equal(built.payload.maxCommandsPerHour, 2);
  }

  const belowMin = validateArmLeaseForm({ ...valid, supplyTempMinC: '4.5' }, { includeSubmitOnly: true });
  assert.equal(belowMin.supplyTempMinC, '供水温度下限需在 5.0 至 20.0 °C 之间');

  const badDeadband = validateArmLeaseForm({ ...valid, deadbandC: '0.02' }, { includeSubmitOnly: true });
  assert.equal(badDeadband.deadbandC, '调节死区需在 0.05 至 2.0 °C 之间');

  const exactMin = buildArmLeasePayload({ ...valid, supplyTempMinC: '5.0' }, 0);
  assert.equal(exactMin.ok, true);
  if (exactMin.ok) {
    assert.equal(exactMin.payload.supplyTempMinC, 5);
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

test('resolveActiveLeaseBackoff detects active future backoff and suppresses expired backoff', () => {
  const nowMs = Date.parse('2026-11-18T02:00:00Z');
  assert.equal(resolveActiveLeaseBackoff({}, nowMs), null);
  assert.equal(
    resolveActiveLeaseBackoff({
      manualBackoffUntil: '2026-11-18T01:59:00Z',
      manualBackoffReason: 'manual_override_backoff',
    }, nowMs),
    null,
  );
  assert.deepEqual(
    resolveActiveLeaseBackoff({
      manualBackoffUntil: '2026-11-18T02:30:00Z',
      manualBackoffReason: 'manual_override_backoff',
    }, nowMs),
    {
      until: '2026-11-18T02:30:00Z',
      reasonText: '检测到人工干预设定值，AI 自动调节已进入退避保护窗口',
    },
  );
  assert.deepEqual(
    resolveActiveLeaseBackoff({
      manualBackoffUntil: null,
      manualBackoffReason: 'cabinet_setpoint_manual_override',
    }, nowMs),
    {
      until: null,
      reasonText: '检测到人工干预设定值，AI 自动调节已进入退避保护窗口',
    },
  );
});

test('executionMode defaults to CLOSED_LOOP, supports SHADOW payload & prefill, and formats SHADOW_RECORDED & decision trace cleanly', () => {
  const defaults = initialArmLeaseValues();
  assert.equal(defaults.executionMode, 'CLOSED_LOOP');
  assert.equal(resolveAutoControlExecutionMode(null), 'CLOSED_LOOP');
  assert.equal(formatAutoControlExecutionMode('CLOSED_LOOP'), '闭环下发');
  assert.equal(formatAutoControlExecutionMode('SHADOW'), '影子跟跑');

  const shadowSeed = initialArmLeaseValues({
    id: '01990000-8800-7000-8000-000000000010',
    tenantId: '018f3d00-0000-7000-8000-000000000001',
    siteId: '018f3e00-1000-7000-8000-000000000001',
    status: 'ACTIVE',
    executionMode: 'SHADOW',
    armedBy: '018f3e00-2000-7000-8000-000000000001',
    armedAt: '2026-11-18T00:00:00Z',
    expiresAt: '2026-11-18T08:00:00Z',
    allowedCapabilities: ['SET_CHILLED_WATER_TEMPERATURE_SETPOINT'],
    maxSetpointStepC: 1.0,
    supplyTempMinC: 6.0,
    supplyTempMaxC: 12.0,
    zoneTempMaxC: 26.0,
    minRunningChillers: 1,
    maxCommandsPerHour: 2,
    deadbandC: 0.2,
    revision: 1,
    createdAt: '2026-11-18T00:00:00Z',
    updatedAt: '2026-11-18T00:00:00Z',
  });
  assert.equal(shadowSeed.executionMode, 'SHADOW');

  const shadowBuilt = buildArmLeasePayload({
    ...defaults,
    executionMode: 'SHADOW',
    reason: '白天先跑影子对比，不下发设备',
    verificationCode: '123456',
  }, 0);
  assert.equal(shadowBuilt.ok, true);
  if (shadowBuilt.ok) {
    assert.equal(shadowBuilt.payload.executionMode, 'SHADOW');
  }

  assert.equal(MONITOR_STATUS_PRESENTATION.SHADOW_RECORDED.label, '影子跟跑记录');
  assert.equal(resolveMonitorExecutionMode({ status: 'SHADOW_RECORDED' }), 'SHADOW');
  assert.equal(resolveMonitorExecutionMode({ status: 'OBSERVING', executionMode: 'SHADOW' }), 'SHADOW');
  assert.equal(resolveMonitorExecutionMode({ status: 'VERIFIED', executionMode: 'CLOSED_LOOP' }), 'CLOSED_LOOP');
  assert.equal(formatMonitorExecutionMode('SHADOW'), '影子');
  assert.equal(formatMonitorExecutionMode('CLOSED_LOOP'), '闭环');

  assert.equal(formatMonitorDecisionTrace({}), null);
  assert.deepEqual(
    formatMonitorDecisionTrace({
      decisionId: '01990000-8800-7000-8000-000000000501',
      traceId: 'a1b2c3d4e5f60718293a4b5c6d7e8f90',
    }),
    {
      label: '已记录决策轨迹',
      title: '决策编号：01990000-8800-7000-8000-000000000501 · 链路追踪：a1b2c3d4e5f60718293a4b5c6d7e8f90',
    },
  );
});

test('governance summary helpers map solver routing, M&V COP bins, and flywheel labels without leaking internal tokens', () => {
  assert.equal(formatSolverRef('builtin://hvac-recommendation-v1'), '内置稳态优化策略');
  assert.equal(formatSolverRef('builtin://chiller-cop-model'), '冷机 COP 拟合策略');
  assert.equal(formatSolverRef('https://solver.internal.example/v2/recommend'), '外部协同优化服务');
  assert.equal(formatSolverRef('http://localhost:8090/solve'), '外部协同优化服务');
  assert.equal(formatSolverRef('http://ai-solver-service:19099/v1/solve'), '外部 Python 优化服务');
  assert.equal(formatSolverRef('builtin://experimental-v2'), '内置优化策略');

  assert.equal(formatSolverSource('DEFAULT'), '默认基线');
  assert.equal(formatSolverSource('DEPLOYMENT_BINDING'), '站点部署绑定');
  assert.equal(formatSolverSource('SITE_LOOP_CANARY'), '回路灰度策略');

  assert.equal(formatLoopKey('PLANT'), '全冷站主回路');
  assert.equal(formatLoopKey('chilled_water_supply_temp'), '冷冻水供水温度回路');
  assert.equal(formatLoopKey('unknown_internal_loop_key'), '指定控制回路');

  assert.equal(formatCopUpliftPct(8.3), '+8.3%');
  assert.equal(formatCopUpliftPct(-2.14), '−2.1%');
  assert.equal(formatCopUpliftPct(0), '0.0%');
  assert.equal(formatCopUpliftPct(null), '—');
  assert.equal(formatSavedKWh(14.8), '14.8 kWh');
  assert.equal(formatWetBulbBin(22, 24), '22.0 至 24.0 °C');
  assert.equal(formatCoolingLoadBin(400, 600), '400 至 600 kW');

  const populated: PublishedOptimizationGovernanceSummary = {
    tenantId: '018f3d00-0000-7000-8000-000000000001',
    siteId: '018f3e00-1000-7000-8000-000000000001',
    windowDays: 14,
    generatedAt: '2026-11-18T06:00:00Z',
    activeSolver: {
      servingRef: 'builtin://chiller-cop-model',
      source: 'SITE_LOOP_CANARY',
      loopKey: 'chilled_water_supply_temp',
      deploymentRevisionId: '01910000-0000-7000-8000-00000000000b',
    },
    tickCounts: {
      aiClosedLoop: 28,
      aiShadow: 12,
      ruleBaseline: 34,
      manualOverride: 3,
    },
    copComparison: {
      matchedBinCount: 2,
      matchedAiSamples: 20,
      matchedBaselineSamples: 26,
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

  const view = parseOptimizationGovernanceSummary(populated);
  assert.equal(view.activeSolver.solverLabel, '冷机 COP 拟合策略');
  assert.equal(view.activeSolver.sourceLabel, '回路灰度策略');
  assert.equal(view.activeSolver.loopLabel, '冷冻水供水温度回路');
  assert.equal(view.activeSolver.hasDeploymentBinding, true);
  assert.equal(view.tickCounts.totalTicks, 77);
  assert.equal(view.copComparison.weightedCopUpliftLabel, '+8.3%');
  assert.equal(view.copComparison.totalEstimatedSavedLabel, '14.8 kWh');
  assert.equal(view.copComparison.cells[0]?.wetBulbRangeLabel, '22.0 至 24.0 °C');
  assert.equal(view.copComparison.cells[0]?.coolingLoadRangeLabel, '400 至 600 kW');
  assert.equal(view.copComparison.cells[0]?.samplesPairLabel, '18 / 24');
  assert.deepEqual(
    view.flywheel.items.map((item) => [item.label, item.count]),
    [
      ['护栏限幅修正', 3],
      ['物理安全拦截', 2],
      ['人工接管覆写', 1],
      ['跟踪偏差回退', 2],
      ['监控越界回退', 1],
    ],
  );
  const serialized = JSON.stringify(view);
  assert.equal(serialized.includes('builtin://'), false);
  assert.equal(serialized.includes('01910000-0000-7000-8000-00000000000b'), false);

  const empty = parseOptimizationGovernanceSummary({
    ...populated,
    activeSolver: {
      servingRef: 'https://optimizer.example/run',
      source: 'DEFAULT',
      loopKey: 'default',
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
  });
  assert.equal(empty.activeSolver.solverLabel, '外部协同优化服务');
  assert.equal(empty.activeSolver.sourceLabel, '默认基线');
  assert.equal(empty.activeSolver.hasDeploymentBinding, false);
  assert.equal(empty.copComparison.weightedBaselineCOPLabel, '—');
  assert.equal(empty.copComparison.weightedAiCOPLabel, '—');
  assert.equal(empty.copComparison.weightedCopUpliftLabel, '—');
  assert.equal(empty.copComparison.totalEstimatedSavedLabel, '—');
  assert.equal(empty.copComparison.cells.length, 0);
});

test('negative sample inspector formatters and JSONL serializer produce plain Chinese labels without leaking raw internal codes', () => {
  assert.equal(formatLoopKey('CHW_PUMP'), '冷冻泵变频回路');
  assert.equal(formatLoopKey('CW_PUMP'), '冷却泵变频回路');
  assert.equal(formatLoopKey('CT_FAN'), '冷却塔风机回路');

  assert.equal(formatNegativeSampleKind('GUARDRAIL_CLAMPED'), '护栏限幅修正');
  assert.equal(formatNegativeSampleKind('TRACKING_DEVIATION'), '跟踪偏差回退');
  assert.equal(formatGuardrailStage('slew_rate'), '单步调幅限速');
  assert.equal(formatGuardrailStage('unknown_stage'), '安全护栏规则');
  assert.equal(formatGuardrailAction('vetoed'), '已拦截');
  assert.equal(formatNegativeSampleReason('step_rate_limit'), '单步变化超限');
  assert.equal(formatNegativeSampleReason('raw_unknown_code'), '安全护栏规则触发');

  assert.equal(serializeNegativeSamplesJsonl([]), '');
  const jsonl = serializeNegativeSamplesJsonl([{ id: '1', sampleKind: 'GUARDRAIL_CLAMPED' }, { id: '2', sampleKind: 'MANUAL_OVERRIDE' }]);
  const lines = jsonl.trim().split('\n');
  assert.equal(lines.length, 2);
  assert.equal(JSON.parse(lines[0]).id, '1');
  assert.equal(JSON.parse(lines[1]).sampleKind, 'MANUAL_OVERRIDE');
});

test('multi-loop VFD/fan lease bounds validate conditionally and monitor helpers format Hz and % cleanly', () => {
  const base = {
    ...initialArmLeaseValues(),
    reason: '开启冷冻泵与冷却塔多回路变频闭环',
    verificationCode: '123456',
  };

  // Without SET_FREQUENCY / SET_FAN_SPEED selected, VFD/fan bounds are omitted from payload.
  const chwOnly = buildArmLeasePayload(base, 0);
  assert.equal(chwOnly.ok, true);
  if (chwOnly.ok) {
    assert.equal(chwOnly.payload.pumpFreqMinHz, undefined);
    assert.equal(chwOnly.payload.fanSpeedMinPct, undefined);
  }

  // Invalid inverted pump frequency bounds fail when SET_FREQUENCY is selected, even while CHW fields are valid.
  const badFreq = validateArmLeaseForm({
    ...base,
    allowedCapabilities: ['SET_CHILLED_WATER_TEMPERATURE_SETPOINT', 'SET_FREQUENCY'],
    pumpFreqMinHz: '49.0',
    pumpFreqMaxHz: '45.0',
    maxFreqStepHz: '12.0',
  }, { includeSubmitOnly: true });
  assert.ok(badFreq.pumpFreqMaxHz);
  assert.ok(badFreq.maxFreqStepHz);

  // Valid multi-loop payload preserves all 8 VFD & fan bounds.
  const multiBuilt = buildArmLeasePayload({
    ...base,
    allowedCapabilities: ['SET_CHILLED_WATER_TEMPERATURE_SETPOINT', 'SET_FREQUENCY', 'SET_FAN_SPEED'],
    pumpFreqMinHz: '30.0',
    pumpFreqMaxHz: '48.0',
    maxFreqStepHz: '2.5',
    freqDeadbandHz: '0.5',
    fanSpeedMinPct: '35',
    fanSpeedMaxPct: '90',
    maxFanStepPct: '10',
    fanDeadbandPct: '2.0',
  }, 1);
  assert.equal(multiBuilt.ok, true);
  if (multiBuilt.ok) {
    assert.equal(multiBuilt.payload.pumpFreqMinHz, 30);
    assert.equal(multiBuilt.payload.pumpFreqMaxHz, 48);
    assert.equal(multiBuilt.payload.maxFreqStepHz, 2.5);
    assert.equal(multiBuilt.payload.freqDeadbandHz, 0.5);
    assert.equal(multiBuilt.payload.fanSpeedMinPct, 35);
    assert.equal(multiBuilt.payload.fanSpeedMaxPct, 90);
    assert.equal(multiBuilt.payload.maxFanStepPct, 10);
    assert.equal(multiBuilt.payload.fanDeadbandPct, 2);
  }

  assert.equal(resolveMonitorUnit({ targetCapability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT' }), 'C');
  assert.equal(resolveMonitorUnit({ targetCapability: 'SET_FREQUENCY' }), 'Hz');
  assert.equal(resolveMonitorUnit({ targetCapability: 'SET_FAN_SPEED', targetUnit: '%' }), '%');
  assert.equal(formatMonitorLoopLabel('CHW_PUMP', 'SET_FREQUENCY'), '冷冻泵变频');
  assert.equal(formatMonitorLoopLabel('CW_PUMP', 'SET_FREQUENCY'), '冷却泵变频');
  assert.equal(formatMonitorLoopLabel('CT_FAN', 'SET_FAN_SPEED'), '冷却塔风机');
  assert.equal(formatMonitorLoopLabel('PLANT', 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT'), '冷冻水供水');
  assert.equal(formatMonitorNumericValue(39.5, 'Hz'), '39.5 Hz');
  assert.equal(formatMonitorNumericValue(66.0, '%'), '66.0%');
  assert.equal(formatMonitorNumericValue(7.8, 'C'), '7.8 °C');
});

test('parseRecommendation preserves auxiliaryTargets and defaults cleanly when omitted', () => {
  const withoutAux = parseRecommendation(publishedFixture);
  assert.deepEqual(withoutAux.auxiliaryTargets, []);

  const withAux = parseRecommendation({
    ...publishedFixture,
    recommendation: {
      ...publishedFixture.recommendation,
      auxiliaryTargets: [
        {
          loopKey: 'chilled_water_pump_freq',
          capability: 'SET_FREQUENCY',
          targetValue: 38.5,
          unit: 'Hz',
          rationale: 'CHW pump coordinated',
        },
        {
          loopKey: 'cooling_tower_fan_speed',
          capability: 'SET_FAN_SPEED',
          targetValue: 67.0,
          unit: '%',
          rationale: 'CT fan coordinated',
        },
      ],
    },
  });
  assert.equal(withAux.auxiliaryTargets.length, 2);
  assert.equal(formatAuxiliaryLoopLabel(withAux.auxiliaryTargets[0].loopKey), '冷冻水泵频率');
  assert.equal(formatAuxiliaryCapabilityLabel(withAux.auxiliaryTargets[0].capability), '变频频率调节');
  assert.equal(formatAuxiliaryTargetValue(withAux.auxiliaryTargets[0].targetValue, withAux.auxiliaryTargets[0].unit), '38.5 Hz');
  assert.equal(formatAuxiliaryLoopLabel(withAux.auxiliaryTargets[1].loopKey), '冷却塔风机转速');
  assert.equal(formatAuxiliaryCapabilityLabel(withAux.auxiliaryTargets[1].capability), '风机转速调节');
  assert.equal(formatAuxiliaryTargetValue(withAux.auxiliaryTargets[1].targetValue, withAux.auxiliaryTargets[1].unit), '67.0%');
});
