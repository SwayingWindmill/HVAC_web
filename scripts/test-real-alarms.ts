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
  devicePriorityLockStateSchema,
  siteAlarmRoutingEnvelopeSchema,
} from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
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
});
