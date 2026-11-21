import assert from 'node:assert/strict';
import test from 'node:test';

import {
  AlarmApiError,
  alarmListResponseSchema,
  alarmSchema,
  validateAlarmListScope,
  validateAlarmScope,
  type AlarmSuppression,
} from '../apps/hvac-web/src/api/alarm-contract.ts';
import {
  alarmRuleListSchema,
  alarmRuleSimulationEnvelopeSchema,
  alarmRuleDeadLetterListEnvelopeSchema,
  alarmRuleDiffEnvelopeSchema,
  alarmRulePublishEnvelopeSchema,
  alarmRuleRevisionListEnvelopeSchema,
  devicePriorityLockStateSchema,
  edgeRuleCompileEnvelopeSchema,
  siteAlarmRoutingEnvelopeSchema,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import {
  computeDraftDelta,
  computeRuleDagLayout,
  evaluateLocalEdgeEligibility,
  formatRuleValidationIssue,
  newRuleUuidV7,
  ruleDraftSignature,
  ruleToDraft,
} from '../apps/hvac-web/src/features/alarms/alarm-rule-dag.ts';
import type { AlarmRule } from '../apps/hvac-web/src/features/alarms/alarm-rules.ts';
import { summarizeEdgeRuleBundle } from '../apps/hvac-web/src/features/connectivity/gateway-config-shadow-model.ts';
import {
  describeDeadLetterError,
  describeDependencyGate,
  describeGroupBy,
  describePlannedActionSummary,
  describeReloadError,
  describeRoutingChannel,
  describeRoutingRouteName,
  describeRuleAction,
  routingDigestPrefix,
  summarizeRuleActions,
} from '../apps/hvac-web/src/features/alarms/alarm-operations.ts';

const tenantId = '018f3e00-1000-7000-8000-000000000001';
const siteId = '018f3e00-2000-7000-8000-000000000001';
const alarmId = '018f3e00-4000-7000-8000-000000000001';
const incidentCorrelationId = '018f3e00-4000-7000-8000-000000000002';

interface TimelineEntryOverrides {
  readonly operation?: string;
  readonly condition?: 'ACTIVE' | 'CLEARED';
  readonly occurredAt?: string;
  readonly version?: number;
  readonly reason?: string;
  readonly assigneeId?: string;
  readonly suppression?: AlarmSuppression;
  readonly currentSeverity?: 'INFO' | 'WARNING' | 'MINOR' | 'MAJOR' | 'CRITICAL';
}

function timelineEntry({
  operation = 'PUBLISH',
  condition = 'ACTIVE',
  occurredAt = '2026-07-31T09:00:00Z',
  version = 1,
  reason = `ALARM_${operation}`,
  assigneeId,
  suppression,
  currentSeverity = 'MAJOR',
}: TimelineEntryOverrides = {}) {
  return {
    operation,
    condition,
    reason,
    actorType: operation === 'PUBLISH' ? 'WORKLOAD' : 'PRINCIPAL',
    actorId: operation === 'PUBLISH' ? 'alarm-evaluator' : 'principal:operator-1',
    ...(assigneeId ? { assigneeId } : {}),
    ...(suppression ? { suppression } : {}),
    currentSeverity,
    policyRevision: 'alarm-policy-9',
    correlationId: `alarm-test-${version}`,
    occurredAt,
    version,
  };
}

function alarm(overrides: Record<string, unknown> = {}) {
  return {
    schemaVersion: 2,
    alarmId,
    tenantId,
    siteId,
    alarmType: 'SUPPLY_TEMPERATURE_DRIFT',
    fingerprint: 'a'.repeat(64),
    incidentCorrelationId,
    sourceType: 'SITE_RULE',
    sourceReference: 'rule:central-plant-temperature-drift:v3',
    ruleRevision: 'alarm-policy-9',
    title: 'Supply temperature drift',
    summary: 'Alarm Service published a durable operational exception.',
    condition: 'ACTIVE',
    currentSeverity: 'MAJOR',
    peakSeverity: 'MAJOR',
    occurrenceCount: 2,
    firstOccurredAt: '2026-07-31T09:00:00Z',
    lastOccurredAt: '2026-07-31T09:05:00Z',
    evidence: [{ kind: 'telemetry-snapshot', reference: 'snapshot:41', capturedAt: '2026-07-31T09:05:00Z' }],
    links: [],
    timeline: [timelineEntry()],
    version: 1,
    createdAt: '2026-07-31T09:00:00Z',
    updatedAt: '2026-07-31T09:05:00Z',
    ...overrides,
  };
}

function suppression(startsAt = '2026-07-31T09:07:00Z'): AlarmSuppression {
  return {
    startsAt,
    expiresAt: '2026-07-31T13:07:00Z',
    reason: 'maintenance window',
    actorId: 'principal:operator-1',
    policyRevision: 'alarm-policy-9',
  };
}

test('Alarm projection requires the S13 orthogonal aggregate facts', () => {
  const parsed = alarmSchema.parse(alarm());
  assert.equal(parsed.condition, 'ACTIVE');
  assert.equal(parsed.currentSeverity, 'MAJOR');
  assert.equal(parsed.peakSeverity, 'MAJOR');
  assert.equal(parsed.evidence.length, 1);
  assert.throws(() => alarmSchema.parse(alarm({ sourceReference: '', timeline: [] })));
  assert.throws(() => alarmSchema.parse(alarm({ peakSeverity: 'WARNING' })));
});

test('Real Alarm scope validation fails closed for a cross-Site projection', () => {
  const parsed = alarmSchema.parse(alarm());
  assert.throws(
    () => validateAlarmScope(parsed, { trustedTenantId: tenantId, trustedSiteId: '018f3e00-2000-7000-8000-000000000002' }),
    (error) => error instanceof AlarmApiError && error.status === 404 && error.code === 'RESOURCE_NOT_FOUND',
  );
});

test('Alarm list preserves empty collection semantics without fabrication', () => {
  const response = alarmListResponseSchema.parse({ schemaVersion: 2, items: [], nextCursor: null, hasMore: false });
  assert.deepEqual(validateAlarmListScope(response, { trustedTenantId: tenantId, trustedSiteId: siteId }).items, []);
});

test('current severity may recover while peak severity preserves the incident maximum', () => {
  const parsed = alarmSchema.parse(alarm({
    currentSeverity: 'MINOR',
    peakSeverity: 'CRITICAL',
    timeline: [
      timelineEntry({ currentSeverity: 'CRITICAL' }),
      timelineEntry({ operation: 'PUBLISH', occurredAt: '2026-07-31T09:06:00Z', version: 2, currentSeverity: 'MINOR' }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  }));
  assert.equal(parsed.currentSeverity, 'MINOR');
  assert.equal(parsed.peakSeverity, 'CRITICAL');
});

test('Work Order completion is not an Alarm condition transition', () => {
  assert.throws(() => alarmSchema.parse(alarm({
    condition: 'CLEARED',
    clearedAt: '2026-07-31T09:06:00Z',
    timeline: [
      timelineEntry(),
      timelineEntry({ operation: 'WORK_ORDER_COMPLETE', condition: 'CLEARED', occurredAt: '2026-07-31T09:06:00Z', version: 2 }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:06:00Z',
  })));
});

test('Alarm contract rejects timeline tampering and non-convergent condition/severity', () => {
  assert.throws(() => alarmSchema.parse(alarm({
    timeline: [timelineEntry({ version: 2 })],
  })));
  assert.throws(() => alarmSchema.parse(alarm({
    condition: 'CLEARED',
    clearedAt: '2026-07-31T09:06:00Z',
    timeline: [timelineEntry()],
    updatedAt: '2026-07-31T09:06:00Z',
  })));
  assert.throws(() => alarmSchema.parse(alarm({
    currentSeverity: 'MINOR',
    timeline: [timelineEntry({ currentSeverity: 'MAJOR' })],
  })));
});

test('Alarm suppression validation accepts valid ACTIVE suppression window and rejects cleared suppression', () => {
  const suppressed = alarmSchema.parse(alarm({
    suppression: suppression(),
    timeline: [
      timelineEntry(),
      timelineEntry({ operation: 'SUPPRESS', occurredAt: '2026-07-31T09:07:00Z', version: 2, suppression: suppression() }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:07:00Z',
  }));
  assert.equal(suppressed.suppression?.reason, 'maintenance window');

  assert.throws(() => alarmSchema.parse(alarm({
    condition: 'CLEARED',
    clearedAt: '2026-07-31T09:08:00Z',
    suppression: suppression(),
    timeline: [
      timelineEntry(),
      timelineEntry({ operation: 'CLEAR', condition: 'CLEARED', occurredAt: '2026-07-31T09:08:00Z', version: 2 }),
    ],
    version: 2,
    updatedAt: '2026-07-31T09:08:00Z',
  })));
});

test('AlarmRule and dry-run simulation contracts validate slope, expression, template and DAG metadata', () => {
  const deviceId = '018f3e00-4000-7000-8000-000000000006';
  const baseRuleId = '01910000-b100-7000-8000-00000000000a';
  const slopeRuleId = '01910000-b100-7000-8000-00000000000d';
  const parsedRules = alarmRuleListSchema.parse({
    data: [{
      ruleId: slopeRuleId,
      title: '1 号冷机供水温度骤升',
      severity: 'WARNING',
      ruleKind: 'SLOPE',
      templateKey: 'chiller-slope-rise',
      overriddenFields: ['raise.delta'],
      dependsOnRuleIds: [baseRuleId],
      hopDepth: 1,
      triggerMode: 'SIMPLE',
      raise: {
        kind: 'SLOPE',
        reading: { deviceId, pointCode: 'supply_water_temperature' },
        operator: 'GTE',
        delta: 2,
        windowSeconds: 300,
      },
      clear: {
        kind: 'EXPRESSION',
        expression: 'supply <= 8',
        inputs: { supply: { deviceId, pointCode: 'supply_water_temperature' } },
      },
    }],
    meta: { requestId: 'req-1' },
  });
  assert.equal(parsedRules.data[0].hopDepth, 1);

  const sim = alarmRuleSimulationEnvelopeSchema.parse({
    data: {
      ruleId: slopeRuleId,
      title: '1 号冷机供水温度骤升',
      hopDepth: 1,
      dependsOnRuleIds: [baseRuleId],
      finalActive: false,
      steps: [
        { step: 1, evaluatedAt: '2026-11-17T10:00:00Z', status: 'NOT_MATCHED', effect: 'NONE', active: false },
        { step: 2, evaluatedAt: '2026-11-17T10:01:00Z', status: 'MATCHED', effect: 'PUBLISH', active: true, matchedReasons: ['斜率变化达标'] },
        { step: 3, evaluatedAt: '2026-11-17T10:02:00Z', status: 'MATCHED', effect: 'CLEAR', active: false, matchedReasons: ['满足恢复条件'] },
      ],
    },
    meta: { requestId: 'req-2' },
  });
  assert.equal(sim.data.steps.length, 3);
  assert.throws(() => alarmRuleListSchema.parse({
    ...parsedRules,
    data: [{ ...parsedRules.data[0], hopDepth: 4 }],
  }));
});

test('Alarm operations formatters and Wave 4 routing/DLQ/priority-lock schemas validate', () => {
  assert.equal(describeDependencyGate('ALL_ACTIVE'), '需全部上游告警同时触发');
  assert.equal(describeDependencyGate('ANY_ACTIVE'), '任一上游告警触发即可');
  assert.equal(describeDependencyGate(undefined), '无前置条件');
  assert.equal(
    describeRuleAction({ kind: 'WORK_ORDER', triggerOn: 'RAISE', workOrderTitle: '排查冷机供水温度骤升异常' }),
    '触发时创建运维工单（排查冷机供水温度骤升异常）',
  );
  assert.equal(
    summarizeRuleActions([
      { kind: 'WORK_ORDER', triggerOn: 'RAISE', workOrderTitle: '排查冷机' },
      { kind: 'PROTECTION_COMMAND', triggerOn: 'RAISE', targetDeviceId: '018f3e00-4000-7000-8000-000000000001' },
    ]),
    '联动 2 项动作：创建运维工单、下发保护指令',
  );
  assert.equal(describeRoutingChannel('wecom_app'), '企业微信应用');
  assert.equal(describeRoutingChannel('wecom_bot'), '企业微信群机器人');
  assert.equal(describeGroupBy('severity'), '按严重级别合并');
  assert.match(describeGroupBy('alarmType'), /告警类型/);
  assert.doesNotMatch(describeGroupBy('alarmType'), /策略|alarmType/);
  assert.equal(describeRoutingRouteName('major'), '重要分级通道');
  assert.doesNotMatch(describeRoutingRouteName('warning'), /策略|warning/i);
  assert.equal(describeDeadLetterError('TRANSPORT'), '网络请求超时或传输中断');
  assert.equal(describeDeadLetterError('network_error'), '网络请求超时或传输中断');
  const ssrfText = describeDeadLetterError('ssrf_blocked');
  assert.ok(ssrfText.length > 0);
  assert.doesNotMatch(ssrfText, /策略|ssrf_blocked/i);
  assert.match(describeReloadError('INVALID_ROUTING') ?? '', /保留上一版有效配置/);
  const recipientsErr = describeReloadError('RECIPIENTS_INVALID') ?? '';
  assert.ok(recipientsErr.length > 0);
  assert.doesNotMatch(recipientsErr, /策略|RECIPIENTS_INVALID/);
  assert.equal(
    describePlannedActionSummary({ kind: 'PROTECTION_COMMAND', target: 'chiller-1', summary: 'protection command setpoint' }),
    '保护设定值与联锁指令',
  );
  assert.match(
    describePlannedActionSummary({ kind: 'WORK_ORDER', target: '排查冷机供水温度骤升异常', summary: 'work order [HIGH]: 排查冷机供水温度骤升异常' }),
    /排查冷机供水温度骤升异常/,
  );
  assert.equal(routingDigestPrefix('0123456789abcdef'), '0123456789ab');

  const dlq = alarmRuleDeadLetterListEnvelopeSchema.parse({
    data: [{
      outboxId: '01910000-e200-7000-8000-000000000001',
      siteId,
      alarmId,
      alarmVersion: 1,
      assignmentId: '01910000-b000-7000-8000-00000000000b',
      actionIndex: 0,
      triggerKind: 'RAISE',
      actionKind: 'WEBHOOK',
      targetRef: 'https://ops.example.invalid/hook',
      attempts: 3,
      lastErrorCode: 'TRANSPORT',
      deadLetteredAt: '2026-11-18T09:10:00Z',
      createdAt: '2026-11-18T09:00:00Z',
    }],
    meta: { requestId: 'req-dlq' },
  });
  assert.equal(dlq.data.length, 1);

  const routing = siteAlarmRoutingEnvelopeSchema.parse({
    data: {
      siteId,
      siteName: '中央机房',
      timezone: 'Asia/Shanghai',
      holdMinutes: 3,
      maxAgeHours: 12,
      rateLimitPerHour: 6,
      routes: [{
        name: 'major',
        minSeverity: 'MAJOR',
        groupWindowSeconds: 60,
        groupMaxItems: 10,
        groupBy: 'severity',
        notifyAck: true,
        notifyRecovery: true,
        steps: [{ afterMinutes: 0, channels: ['wecom_app', 'smtp'], recipientCount: 2 }],
      }],
      configDigest: '7a9e4c1b2f3d8a90',
      loadedAt: '2026-11-18T08:00:00Z',
      reloadCount: 0,
    },
    meta: { requestId: 'req-route' },
  });
  assert.equal(routing.data.routes[0].steps[0].recipientCount, 2);

  const lock = devicePriorityLockStateSchema.parse({
    deviceId: '018f3e00-4000-7000-8000-000000000001',
    lockedPriority: 'SAFETY_INTERLOCK',
    commandSource: 'OPERATOR',
    lockedBy: '018f3e00-2000-7000-8000-000000000001',
    lockedByName: '周敏',
    lockedUntil: '2026-11-18T12:00:00Z',
    reason: '传感器联动联锁锁机',
    updatedAt: '2026-11-18T11:00:00Z',
  });
  assert.equal(lock.lockedPriority, 'SAFETY_INTERLOCK');

  const scheduleLock = devicePriorityLockStateSchema.parse({
    deviceId: '018f3e00-4000-7000-8000-000000000002',
    lockedPriority: 'SCHEDULE',
    commandSource: 'SCHEDULE',
    lockedBy: 'schedule-engine',
    lockedUntil: '2026-11-18T13:00:00Z',
    reason: '定时启停锁定',
    updatedAt: '2026-11-18T11:00:00Z',
  });
  assert.equal(scheduleLock.lockedPriority, 'SCHEDULE');

  // P2-2 DAG layout, cycle/depth guards, draft delta, and rule bundle envelopes.
  const id1 = newRuleUuidV7(1_760_000_000_000);
  const id2 = newRuleUuidV7(1_760_000_000_001);
  const id3 = newRuleUuidV7(1_760_000_000_002);
  const id4 = newRuleUuidV7(1_760_000_000_003);
  const id5 = newRuleUuidV7(1_760_000_000_004);
  assert.match(id1, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);

  const baseCond = {
    kind: 'COMPARE' as const,
    reading: { deviceId: '018f3e00-4000-7000-8000-000000000001', pointCode: 'supply_water_temperature' },
    operator: 'GT' as const,
    value: 10,
  };
  const baseClear = { ...baseCond, operator: 'LT' as const, value: 8 };
  const r1: AlarmRule = { ruleId: id1, title: '主规则', severity: 'MAJOR', triggerMode: 'SIMPLE', raise: baseCond, clear: baseClear };
  const r2: AlarmRule = { ruleId: id2, title: '次级联动', severity: 'WARNING', triggerMode: 'SIMPLE', dependsOnRuleIds: [id1], dependencyGate: 'ALL_ACTIVE', raise: baseCond, clear: baseClear };
  const dag = computeRuleDagLayout([r1, r2]);
  assert.equal(dag.maxHopDepth, 1);
  assert.equal(dag.edges.length, 1);
  assert.equal(dag.cycleRuleIds.length, 0);

  const cycleLayout = computeRuleDagLayout([
    { ...r1, dependsOnRuleIds: [id2] },
    r2,
  ]);
  assert.equal(cycleLayout.cycleRuleIds.length, 2);

  const overDepthLayout = computeRuleDagLayout([
    r1,
    { ...r2 },
    { ...r1, ruleId: id3, dependsOnRuleIds: [id2] },
    { ...r1, ruleId: id4, dependsOnRuleIds: [id3] },
    { ...r1, ruleId: id5, dependsOnRuleIds: [id4] },
  ]);
  assert.equal(overDepthLayout.maxHopDepth, 4);
  assert.deepEqual(overDepthLayout.overDepthRuleIds, [id5]);

  const delta = computeDraftDelta([r1, r2], [{ ...r1, title: '主规则（已修改）' }, { ...r1, ruleId: id3, title: '新规则' }]);
  assert.equal(delta.dirty, true);
  assert.equal(delta.addedCount, 1);
  assert.equal(delta.changedCount, 1);
  assert.equal(delta.retiredCount, 1);
  assert.equal(ruleToDraft(r2).dependsOnRuleIds?.[0], id1);

  const diffEnv = alarmRuleDiffEnvelopeSchema.parse({
    data: {
      valid: true,
      currentRevision: 1,
      currentDigest: 'a'.repeat(64),
      nextDigest: 'b'.repeat(64),
      summary: { added: 1, changed: 0, unchanged: 1, retired: 0, addedRuleIds: [id2], changedRuleIds: [], unchangedRuleIds: [id1], retiredRuleIds: [] },
      changes: [{ ruleId: id2, title: '次级联动', change: 'ADDED', changedFields: [] }],
      graph: {
        nodes: [{ ruleId: id1, title: '主规则', severity: 'MAJOR', executionTarget: 'CLOUD', hopDepth: 0, dependsOnRuleIds: [], actionCount: 0 }],
        edges: [],
        topologicalOrder: [id1],
        maxHopDepth: 0,
      },
      issues: [],
    },
    meta: { requestId: 'req-diff' },
  });
  assert.equal(diffEnv.data.valid, true);

  const pubEnv = alarmRulePublishEnvelopeSchema.parse({
    data: {
      bundleRevision: 2,
      bundleDigest: 'b'.repeat(64),
      source: 'WEB_WORKBENCH',
      changed: true,
      publishedAt: '2026-11-21T10:00:00Z',
      publishedBy: 'principal:operator-1',
      summary: { added: 1, changed: 0, unchanged: 1, retired: 0, addedRuleIds: [id2], changedRuleIds: [], unchangedRuleIds: [id1], retiredRuleIds: [] },
      graph: diffEnv.data.graph,
      items: [r1],
    },
    meta: { requestId: 'req-pub' },
  });
  assert.equal(pubEnv.data.bundleRevision, 2);

  const revsEnv = alarmRuleRevisionListEnvelopeSchema.parse({
    data: {
      currentRevision: 2,
      items: [{
        bundleRevision: 2,
        bundleDigest: 'b'.repeat(64),
        source: 'WEB_WORKBENCH',
        summary: pubEnv.data.summary,
        ruleCount: 1,
        publishedAt: '2026-11-21T10:00:00Z',
        publishedBy: 'principal:operator-1',
      }],
    },
    meta: { requestId: 'req-revs' },
  });
  assert.equal(revsEnv.data.currentRevision, 2);

  // Unknown & self dependencies in computeRuleDagLayout + clean ruleToDraft + operator Chinese issue translation.
  const unknownLayout = computeRuleDagLayout([
    r1,
    { ...r2, dependsOnRuleIds: [id1, id4] },
    { ...r1, ruleId: id3, dependsOnRuleIds: [id3] },
  ]);
  assert.equal(unknownLayout.unknownDeps.length, 2);
  const cleanedDraft = ruleToDraft({ ...r2, dependsOnRuleIds: [], dependencyGate: 'ALL_ACTIVE' });
  assert.equal(cleanedDraft.dependsOnRuleIds, undefined);
  assert.equal(cleanedDraft.dependencyGate, undefined);
  assert.equal(ruleDraftSignature(r1), ruleDraftSignature({ ...r1, ruleKind: 'THRESHOLD' }));
  const translated = formatRuleValidationIssue(
    { ruleId: id2, code: 'RULE_CYCLE_DETECTED', message: `raw English message ${id2}` },
    new Map([[id2, '次级联动']]),
  );
  assert.ok(translated.includes('「次级联动」'));
  assert.ok(!/[0-9a-f]{8}-[0-9a-f]{4}-|raw English/.test(translated));
});

test('P2-3 streaming window aggregation schema, local edge eligibility and edge compile bundle validate', () => {
  const deviceId = '018f3e00-4000-7000-8000-000000000001';
  const upstreamId = newRuleUuidV7(1_760_000_001_000);
  const windowRuleId = newRuleUuidV7(1_760_000_001_001);
  const reading = { deviceId, pointCode: 'supply_water_temperature' };
  const clearCond = { kind: 'COMPARE' as const, reading, operator: 'LT' as const, value: 9.5 };

  const parsedWindow = alarmRuleListSchema.parse({
    data: [{
      ruleId: windowRuleId,
      title: '1 号冷机供水窗口均值偏高',
      severity: 'MAJOR',
      ruleKind: 'WINDOW',
      executionTarget: 'CLOUD_AND_EDGE',
      triggerMode: 'SIMPLE',
      raise: {
        kind: 'WINDOW_AGG',
        reading,
        agg: 'P95',
        windowType: 'SLIDING',
        windowSeconds: 300,
        minSamples: 5,
        operator: 'GTE',
        threshold: 12.5,
      },
      clear: clearCond,
      actions: [{ kind: 'WORK_ORDER', triggerOn: 'RAISE', workOrderPriority: 'HIGH', workOrderTitle: '排查冷机窗口异常' }],
    }],
    meta: { requestId: 'req-window-1', bundleRevision: 3, bundleDigest: 'c'.repeat(64) },
  });
  const winRule = parsedWindow.data[0]!;
  assert.equal(winRule.ruleKind, 'WINDOW');
  assert.equal(winRule.executionTarget, 'CLOUD_AND_EDGE');
  assert.equal(ruleToDraft(winRule).executionTarget, 'CLOUD_AND_EDGE');
  assert.equal(
    ruleDraftSignature({ ...winRule, executionTarget: undefined }),
    ruleDraftSignature({ ...winRule, executionTarget: 'CLOUD' }),
  );

  // Local edge eligibility rules match Go compiler constraints.
  assert.equal(evaluateLocalEdgeEligibility({ ...winRule, executionTarget: 'CLOUD' }, [winRule]).reasonCode, 'CLOUD_ONLY_TARGET');
  assert.equal(
    evaluateLocalEdgeEligibility(
      { ...winRule, raise: { kind: 'NO_DATA', reading, ageSeconds: 300 } },
      [winRule],
    ).reasonCode,
    'EDGE_UNSUPPORTED_CONDITION_KIND',
  );
  assert.equal(
    evaluateLocalEdgeEligibility({ ...winRule, executionTarget: 'EDGE' }, [winRule]).reasonCode,
    'EDGE_CLOUD_ONLY_ACTION',
  );
  assert.equal(evaluateLocalEdgeEligibility(winRule, [winRule]).eligible, true);

  const cloudUpstream: AlarmRule = {
    ruleId: upstreamId,
    title: '云端前置规则',
    severity: 'WARNING',
    executionTarget: 'CLOUD',
    triggerMode: 'SIMPLE',
    raise: { kind: 'COMPARE', reading, operator: 'GT', value: 8 },
    clear: clearCond,
  };
  const dependentEdge: AlarmRule = {
    ...winRule,
    dependsOnRuleIds: [upstreamId],
    dependencyGate: 'ALL_ACTIVE',
  };
  assert.equal(
    evaluateLocalEdgeEligibility(dependentEdge, [cloudUpstream, dependentEdge]).reasonCode,
    'EDGE_UPSTREAM_NOT_ON_EDGE',
  );
  assert.equal(
    evaluateLocalEdgeEligibility(dependentEdge, [{ ...cloudUpstream, executionTarget: 'EDGE' }, dependentEdge]).eligible,
    true,
  );

  const compileEnv = edgeRuleCompileEnvelopeSchema.parse({
    data: {
      siteId,
      bundleRevision: 3,
      bundleDigest: 'c'.repeat(64),
      edgeBundleDigest: 'd'.repeat(64),
      compiledAt: '2026-11-22T00:00:00Z',
      edgeRuleCount: 1,
      cloudOnlyCount: 0,
      eligibility: [{
        ruleId: windowRuleId,
        title: '1 号冷机供水窗口均值偏高',
        executionTarget: 'CLOUD_AND_EDGE',
        compiled: true,
      }],
      bundle: {
        schemaVersion: 1,
        siteId,
        bundleRevision: 3,
        bundleDigest: 'c'.repeat(64),
        edgeBundleDigest: 'd'.repeat(64),
        compiledAt: '2026-11-22T00:00:00Z',
        rules: [{
          ruleId: windowRuleId,
          title: '1 号冷机供水窗口均值偏高',
          severity: 'MAJOR',
          executionTarget: 'CLOUD_AND_EDGE',
          qualityPolicy: 'VALID_ONLY',
          freshnessSeconds: 900,
          triggerMode: 'SIMPLE',
          raise: {
            kind: 'WINDOW_AGG',
            input: `${deviceId}/supply_water_temperature`,
            agg: 'P95',
            windowType: 'SLIDING',
            windowSeconds: 300,
            minSamples: 5,
            operator: 'GTE',
            threshold: 12.5,
          },
          clear: {
            kind: 'COMPARE',
            input: `${deviceId}/supply_water_temperature`,
            operator: 'LT',
            value: { type: 'NUMBER', number: 9.5 },
          },
        }],
      },
    },
    meta: { requestId: 'req-compile-1' },
  });
  assert.equal(compileEnv.data.edgeRuleCount, 1);
  assert.deepEqual(summarizeEdgeRuleBundle(compileEnv.data.bundle), {
    configured: true,
    ruleCount: 1,
    bundleRevision: 3,
    shortDigest: 'd'.repeat(12),
    compiledAt: '2026-11-22T00:00:00Z',
  });
  assert.deepEqual(summarizeEdgeRuleBundle(null), {
    configured: false,
    ruleCount: 0,
    bundleRevision: 0,
    shortDigest: '',
    compiledAt: '',
  });
});
