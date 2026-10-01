import type { Alarm, AlarmCondition, AlarmSeverity } from '@/api/alarm-contract';
import type { FDDFinding } from '@/api/intelligence';
import { createFrontendReviewAssetsRegistry } from './frontend-review-assets-data';

interface ReviewAlarmFilter {
  readonly condition?: AlarmCondition;
  readonly severity?: AlarmSeverity;
  readonly acknowledged?: boolean;
  readonly suppressed?: boolean;
  readonly limit?: number;
}

const REVIEW_NOW = Date.now();
const stateByScope = new Map<string, Alarm[]>();

function iso(minutesAgo: number): string {
  return new Date(REVIEW_NOW - minutesAgo * 60_000).toISOString();
}

function reviewUuid(namespace: number, index: number): string {
  return `01940000-${namespace.toString(16).padStart(4, '0')}-7${index.toString(16).padStart(3, '0')}-8000-${index.toString(16).padStart(12, '0')}`;
}

function fingerprint(character: string): string {
  return character.repeat(64).slice(0, 64);
}

function deviceAssetId(
  tenantId: string,
  siteId: string,
  matcher: (deviceType: string) => boolean,
): { deviceId: string; assetId: string; pointId?: string } {
  const model = createFrontendReviewAssetsRegistry(tenantId, siteId).assetModel;
  const device = model.devices.find((candidate) => matcher(candidate.deviceType)) ?? model.devices[0]!;
  const relation = model.relationships.find((candidate) => (
    candidate.fromType === 'DEVICE'
    && candidate.fromId === device.id
    && candidate.toType === 'ASSET'
  ));
  const point = model.telemetryPoints.find((candidate) => candidate.reportingDeviceId === device.id);
  return {
    deviceId: device.id,
    assetId: relation?.toId ?? model.assets[0]!.id,
    pointId: point?.id,
  };
}

function timelineEntry(
  version: number,
  operation: Alarm['timeline'][number]['operation'],
  condition: AlarmCondition,
  severity: AlarmSeverity,
  occurredAt: string,
  reason: string,
  extra: Partial<Alarm['timeline'][number]> = {},
): Alarm['timeline'][number] {
  return {
    operation,
    condition,
    reason,
    actorType: operation === 'PUBLISH' || operation === 'CLEAR' ? 'SYSTEM' : 'USER',
    actorId: operation === 'PUBLISH' || operation === 'CLEAR' ? 'alarm-engine' : 'frontend-review-operator',
    currentSeverity: severity,
    policyRevision: 'frontend-review-alarm-policy',
    correlationId: `frontend-review-${version}`,
    occurredAt,
    version,
    ...extra,
  };
}

function buildReviewAlarms(tenantId: string, siteId: string): Alarm[] {
  const chilledPump = deviceAssetId(tenantId, siteId, (type) => type.includes('CHILLED_WATER_PUMP'));
  const chiller = deviceAssetId(tenantId, siteId, (type) => type.includes('CHILLER'));
  const tower = deviceAssetId(tenantId, siteId, (type) => type.includes('COOLING_TOWER'));
  const meter = deviceAssetId(tenantId, siteId, (type) => type.includes('METER'));

  const firstId = reviewUuid(0x9100, 1);
  const secondId = reviewUuid(0x9100, 2);
  const thirdId = reviewUuid(0x9100, 3);
  const fourthId = reviewUuid(0x9100, 4);

  return [
    {
      schemaVersion: 2,
      alarmId: firstId,
      tenantId,
      siteId,
      deviceId: chilledPump.deviceId,
      pointId: chilledPump.pointId,
      alarmType: 'CHILLED_WATER_FLOW_LOW',
      fingerprint: fingerprint('a'),
      incidentCorrelationId: reviewUuid(0x9200, 1),
      sourceType: 'DEVICE_RULE',
      sourceReference: 'rule:chwp-flow-low',
      ruleRevision: 'chwp-flow-low:r12',
      title: '1# 冷冻水泵流量偏低',
      summary: '冷冻水泵保持运行，但当前流量低于本次规则评估窗口的预期范围。',
      condition: 'ACTIVE',
      currentSeverity: 'MAJOR',
      peakSeverity: 'MAJOR',
      occurrenceCount: 3,
      firstOccurredAt: iso(42),
      lastOccurredAt: iso(8),
      evidence: [
        { kind: 'TELEMETRY_WINDOW', reference: 'chwp.flow_rate:24m', capturedAt: iso(8) },
        { kind: 'TELEMETRY_SNAPSHOT', reference: 'chwp.frequency:current', capturedAt: iso(7) },
      ],
      links: [{ kind: 'DEVICE', targetId: chilledPump.deviceId }],
      timeline: [
        timelineEntry(1, 'PUBLISH', 'ACTIVE', 'MAJOR', iso(42), '流量异常条件持续满足，发布告警。'),
      ],
      version: 1,
      createdAt: iso(42),
      updatedAt: iso(8),
    },
    {
      schemaVersion: 2,
      alarmId: secondId,
      tenantId,
      siteId,
      deviceId: chiller.deviceId,
      pointId: chiller.pointId,
      alarmType: 'CHILLER_COP_LOW',
      fingerprint: fingerprint('b'),
      incidentCorrelationId: reviewUuid(0x9200, 2),
      sourceType: 'DEVICE_RULE',
      sourceReference: 'rule:chiller-cop-low',
      ruleRevision: 'chiller-cop-low:r7',
      title: '1# 冷水机组 COP 持续偏低',
      summary: '主机处于运行状态，评估窗口内 COP 持续低于当前工况基线。',
      condition: 'ACTIVE',
      currentSeverity: 'WARNING',
      peakSeverity: 'WARNING',
      acknowledgement: {
        acknowledgedAt: iso(31),
        acknowledgedBy: 'frontend-review-operator',
        comment: '已确认，继续核对冷冻水与冷却水侧工况。',
      },
      assigneeId: '值班工程师 A',
      occurrenceCount: 2,
      firstOccurredAt: iso(55),
      lastOccurredAt: iso(12),
      evidence: [
        { kind: 'TELEMETRY_WINDOW', reference: 'chiller.cop:30m', capturedAt: iso(12) },
        { kind: 'TELEMETRY_WINDOW', reference: 'chiller.power:30m', capturedAt: iso(12) },
      ],
      links: [{ kind: 'DEVICE', targetId: chiller.deviceId }],
      timeline: [
        timelineEntry(1, 'PUBLISH', 'ACTIVE', 'WARNING', iso(55), 'COP 低效条件持续满足，发布告警。'),
        timelineEntry(2, 'ACKNOWLEDGE', 'ACTIVE', 'WARNING', iso(31), '值班人员已确认告警。'),
        timelineEntry(3, 'ASSIGN', 'ACTIVE', 'WARNING', iso(27), '指派值班工程师跟进。', { assigneeId: '值班工程师 A' }),
      ],
      version: 3,
      createdAt: iso(55),
      updatedAt: iso(12),
    },
    {
      schemaVersion: 2,
      alarmId: thirdId,
      tenantId,
      siteId,
      deviceId: tower.deviceId,
      pointId: tower.pointId,
      alarmType: 'COOLING_TOWER_APPROACH_HIGH',
      fingerprint: fingerprint('c'),
      incidentCorrelationId: reviewUuid(0x9200, 3),
      sourceType: 'DEVICE_RULE',
      sourceReference: 'rule:tower-approach-high',
      ruleRevision: 'tower-approach-high:r4',
      title: '1# 冷却塔逼近温度偏高',
      summary: '冷却塔运行期间逼近温度偏高，相关温度测点存在间歇性质量问题。',
      condition: 'ACTIVE',
      currentSeverity: 'MINOR',
      peakSeverity: 'MINOR',
      acknowledgement: {
        acknowledgedAt: iso(18),
        acknowledgedBy: 'frontend-review-operator',
        comment: '已确认，先核实温度测点质量。',
      },
      occurrenceCount: 1,
      firstOccurredAt: iso(26),
      lastOccurredAt: iso(9),
      evidence: [
        { kind: 'TELEMETRY_WINDOW', reference: 'cooling_tower.approach_temperature:20m', capturedAt: iso(9) },
      ],
      links: [{ kind: 'DEVICE', targetId: tower.deviceId }],
      timeline: [
        timelineEntry(1, 'PUBLISH', 'ACTIVE', 'MINOR', iso(26), '逼近温度异常条件满足，发布告警。'),
        timelineEntry(2, 'ACKNOWLEDGE', 'ACTIVE', 'MINOR', iso(18), '值班人员已确认告警。'),
      ],
      version: 2,
      createdAt: iso(26),
      updatedAt: iso(9),
    },
    {
      schemaVersion: 2,
      alarmId: fourthId,
      tenantId,
      siteId,
      deviceId: meter.deviceId,
      pointId: meter.pointId,
      alarmType: 'METER_COMMUNICATION_LOSS',
      fingerprint: fingerprint('d'),
      incidentCorrelationId: reviewUuid(0x9200, 4),
      sourceType: 'DEVICE_RULE',
      sourceReference: 'rule:meter-communication-loss',
      ruleRevision: 'meter-communication-loss:r3',
      title: '1# 中央空调电表通信中断',
      summary: '电表通信曾中断，当前连接已恢复。',
      condition: 'CLEARED',
      currentSeverity: 'INFO',
      peakSeverity: 'WARNING',
      occurrenceCount: 1,
      firstOccurredAt: iso(180),
      lastOccurredAt: iso(180),
      clearedAt: iso(146),
      evidence: [
        { kind: 'CONNECTIVITY', reference: 'meter.connection', capturedAt: iso(146) },
      ],
      links: [{ kind: 'DEVICE', targetId: meter.deviceId }],
      timeline: [
        timelineEntry(1, 'PUBLISH', 'ACTIVE', 'WARNING', iso(180), '设备通信中断，发布告警。'),
        timelineEntry(2, 'CLEAR', 'CLEARED', 'INFO', iso(146), '设备通信恢复，清除告警。'),
      ],
      version: 2,
      createdAt: iso(180),
      updatedAt: iso(146),
    },
  ];
}

function scopeKey(tenantId: string, siteId: string): string {
  return `${tenantId}:${siteId}`;
}

function state(tenantId: string, siteId: string): Alarm[] {
  const key = scopeKey(tenantId, siteId);
  const existing = stateByScope.get(key);
  if (existing) return existing;
  const seeded = buildReviewAlarms(tenantId, siteId);
  stateByScope.set(key, seeded);
  return seeded;
}

export function listFrontendReviewAlarms(
  tenantId: string,
  siteId: string,
  filter: ReviewAlarmFilter,
): { schemaVersion: 2; items: Alarm[]; nextCursor: null; hasMore: false } {
  let items = [...state(tenantId, siteId)];
  if (filter.condition) items = items.filter((alarm) => alarm.condition === filter.condition);
  if (filter.severity) items = items.filter((alarm) => alarm.currentSeverity === filter.severity);
  if (filter.acknowledged !== undefined) items = items.filter((alarm) => Boolean(alarm.acknowledgement) === filter.acknowledged);
  if (filter.suppressed !== undefined) items = items.filter((alarm) => Boolean(alarm.suppression) === filter.suppressed);
  items.sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt));
  return {
    schemaVersion: 2,
    items: items.slice(0, filter.limit ?? 50),
    nextCursor: null,
    hasMore: false,
  };
}

export function getFrontendReviewAlarm(tenantId: string, siteId: string, alarmId: string): Alarm | undefined {
  return state(tenantId, siteId).find((alarm) => alarm.alarmId === alarmId);
}

export function acknowledgeFrontendReviewAlarm(
  tenantId: string,
  siteId: string,
  alarmId: string,
  comment?: string,
): Alarm | undefined {
  const alarms = state(tenantId, siteId);
  const index = alarms.findIndex((alarm) => alarm.alarmId === alarmId);
  if (index < 0) return undefined;
  const alarm = alarms[index]!;
  if (alarm.acknowledgement) return alarm;
  const version = alarm.version + 1;
  const updated: Alarm = {
    ...alarm,
    acknowledgement: {
      acknowledgedAt: new Date().toISOString(),
      acknowledgedBy: 'frontend-review-operator',
      ...(comment ? { comment } : {}),
    },
    timeline: [
      ...alarm.timeline,
      timelineEntry(version, 'ACKNOWLEDGE', alarm.condition, alarm.currentSeverity, new Date().toISOString(), comment || '值班人员已确认告警。'),
    ],
    version,
    updatedAt: new Date().toISOString(),
  };
  alarms[index] = updated;
  return updated;
}

export function assignFrontendReviewAlarm(
  tenantId: string,
  siteId: string,
  alarmId: string,
  assigneeId: string,
  reason: string,
): Alarm | undefined {
  const alarms = state(tenantId, siteId);
  const index = alarms.findIndex((alarm) => alarm.alarmId === alarmId);
  if (index < 0) return undefined;
  const alarm = alarms[index]!;
  const version = alarm.version + 1;
  const updated: Alarm = {
    ...alarm,
    assigneeId,
    timeline: [
      ...alarm.timeline,
      timelineEntry(version, 'ASSIGN', alarm.condition, alarm.currentSeverity, new Date().toISOString(), reason, { assigneeId }),
    ],
    version,
    updatedAt: new Date().toISOString(),
  };
  alarms[index] = updated;
  return updated;
}

export function createFrontendReviewFindings(tenantId: string, siteId: string): FDDFinding[] {
  const alarms = state(tenantId, siteId);
  const model = createFrontendReviewAssetsRegistry(tenantId, siteId).assetModel;
  const assetForDevice = new Map(
    model.relationships
      .filter((relation) => relation.fromType === 'DEVICE' && relation.toType === 'ASSET')
      .map((relation) => [relation.fromId, relation.toId]),
  );

  const definitions = [
    { type: '冷冻水流量异常偏低', confidence: 0.88, rule: 'fdd:chwp-low-flow:r8', blocker: undefined },
    { type: '主机 COP 持续偏低', confidence: 0.81, rule: undefined, blocker: undefined },
    { type: '冷却塔逼近温度异常', confidence: 0.76, rule: 'fdd:tower-approach:r5', blocker: '关联温度测点存在间歇性质量异常' },
    { type: '电表通信中断', confidence: 0.97, rule: 'fdd:meter-offline:r3', blocker: undefined },
  ] as const;

  return alarms.map((alarm, index) => {
    const definition = definitions[index]!;
    return {
      id: reviewUuid(0x9300, index + 1),
      tenantId,
      siteId,
      assetId: alarm.deviceId ? (assetForDevice.get(alarm.deviceId) ?? model.assets[0]!.id) : model.assets[0]!.id,
      findingType: definition.type,
      evaluationFrom: iso(index === 3 ? 185 : 35 + index * 10),
      evaluationTo: iso(index === 3 ? 145 : 6 + index * 2),
      evidenceIds: alarm.evidence.map((evidence, evidenceIndex) => `${alarm.alarmId}:evidence:${evidenceIndex + 1}:${evidence.kind}`),
      modelDeploymentRevisionId: definition.rule ? '' : reviewUuid(0x9400, index + 1),
      ruleRevisionId: definition.rule,
      confidence: definition.confidence,
      qualityBlocker: definition.blocker,
      alarmId: alarm.alarmId,
      workOrderId: '',
      createdAt: iso(index === 3 ? 144 : 5 + index),
    };
  });
}

export function createFrontendReviewIssueQueue(siteId: string, scope: 'active' | 'all' | 'suppressed') {
  const tenantId = '01940000-0000-7000-8000-000000000001';
  const alarms = state(tenantId, siteId);
  const [flowAlarm, copAlarm, towerAlarm, meterAlarm] = alarms;

  const signal = (alarm: Alarm) => ({
    alarmId: alarm.alarmId,
    title: alarm.title,
    severity: alarm.currentSeverity,
    condition: alarm.condition,
    acknowledged: Boolean(alarm.acknowledgement),
    occurredAt: alarm.firstOccurredAt,
    updatedAt: alarm.updatedAt,
  });

  const activeItems = [
    {
      issueId: reviewUuid(0x9500, 1),
      title: '冷冻水输配能力异常',
      state: 'INVESTIGATING' as const,
      highestSeverity: flowAlarm!.currentSeverity,
      object: {
        type: 'DEVICE' as const,
        id: flowAlarm!.deviceId ?? null,
        label: '1# 冷冻水泵',
        locationLabel: '冷冻水系统',
      },
      alarmIds: [flowAlarm!.alarmId],
      signals: [signal(flowAlarm!)],
      activeAlarmCount: 1,
      unacknowledgedAlarmCount: flowAlarm!.acknowledgement ? 0 : 1,
      diagnosisState: 'PUBLISHED' as const,
      assignee: null,
      firstDetectedAt: flowAlarm!.firstOccurredAt,
      updatedAt: flowAlarm!.updatedAt,
      impact: {
        avoidableEnergyKwh: 486,
        avoidableCost: 392,
        currency: 'CNY',
        comfortImpactHours: 3.4,
        reliabilityRisk: 'HIGH' as const,
        estimatedFrom: flowAlarm!.firstOccurredAt,
        estimatedTo: flowAlarm!.updatedAt,
      },
      grouping: {
        method: 'RULE' as const,
        reason: '当前只有一个主要告警信号，但诊断、影响和后续验证已经形成独立问题上下文。',
        confidence: null,
      },
      nextAction: '核对关键支路阀位与设计流量，并交叉验证主流量测点。',
    },
    {
      issueId: reviewUuid(0x9500, 2),
      title: '冷却侧换热性能异常',
      state: 'ACTION_PENDING' as const,
      highestSeverity: copAlarm!.currentSeverity === 'WARNING' ? towerAlarm!.currentSeverity : copAlarm!.currentSeverity,
      object: {
        type: 'SYSTEM' as const,
        id: null,
        label: '冷却水系统',
        locationLabel: '中央冷站',
      },
      alarmIds: [copAlarm!.alarmId, towerAlarm!.alarmId],
      signals: [signal(copAlarm!), signal(towerAlarm!)],
      activeAlarmCount: 2,
      unacknowledgedAlarmCount: Number(!copAlarm!.acknowledgement) + Number(!towerAlarm!.acknowledgement),
      diagnosisState: 'EVIDENCE_LIMITED' as const,
      assignee: '值班工程师 A',
      firstDetectedAt: copAlarm!.firstOccurredAt,
      updatedAt: towerAlarm!.updatedAt,
      impact: {
        avoidableEnergyKwh: 936,
        avoidableCost: 756,
        currency: 'CNY',
        comfortImpactHours: 2.4,
        reliabilityRisk: 'MEDIUM' as const,
        estimatedFrom: copAlarm!.firstOccurredAt,
        estimatedTo: towerAlarm!.updatedAt,
      },
      grouping: {
        method: 'TOPOLOGY' as const,
        reason: '主机 COP 与冷却塔逼近温度异常位于同一冷却侧系统，并在重叠时间窗出现。该分组用于共同调查，不代表已证明因果。',
        confidence: null,
      },
      nextAction: '先核实冷却塔温度测点质量，再复核冷凝压力与冷却水换热表现。',
    },
  ];

  const resolvedItems = meterAlarm ? [{
    issueId: reviewUuid(0x9500, 3),
    title: '空调电表通信中断',
    state: 'RESOLVED' as const,
    highestSeverity: meterAlarm.peakSeverity,
    object: {
      type: 'DEVICE' as const,
      id: meterAlarm.deviceId ?? null,
      label: '1# 中央空调电表',
      locationLabel: '配电计量',
    },
    alarmIds: [meterAlarm.alarmId],
    signals: [signal(meterAlarm)],
    activeAlarmCount: 0,
    unacknowledgedAlarmCount: meterAlarm.acknowledgement ? 0 : 1,
    diagnosisState: 'PUBLISHED' as const,
    assignee: null,
    firstDetectedAt: meterAlarm.firstOccurredAt,
    updatedAt: meterAlarm.updatedAt,
    impact: {
      avoidableEnergyKwh: null,
      avoidableCost: null,
      currency: null,
      comfortImpactHours: null,
      reliabilityRisk: 'LOW' as const,
      estimatedFrom: meterAlarm.firstOccurredAt,
      estimatedTo: meterAlarm.clearedAt ?? meterAlarm.updatedAt,
    },
    grouping: {
      method: 'RULE' as const,
      reason: '通信中断与恢复属于同一设备、同一告警生命周期。',
      confidence: null,
    },
    nextAction: '观察通信稳定性；若重复出现，转数据质量与网关链路调查。',
  }] : [];

  const items = scope === 'suppressed'
    ? []
    : scope === 'all'
      ? [...activeItems, ...resolvedItems]
      : activeItems;

  return {
    schemaVersion: 1 as const,
    siteId,
    items,
    total: items.length,
    active: items.filter((item) => item.state !== 'RESOLVED').length,
    unassigned: items.filter((item) => item.state !== 'RESOLVED' && !item.assignee).length,
    highImpact: items.filter((item) => item.impact.reliabilityRisk === 'HIGH' || (item.impact.avoidableCost ?? 0) >= 500).length,
    groupedAlarmCount: items.reduce((total, item) => total + item.alarmIds.length, 0),
    rawAlarmCount: scope === 'suppressed'
      ? 0
      : alarms.filter((alarm) => scope === 'all' || alarm.condition === 'ACTIVE').length,
  };
}

export function createFrontendReviewIssueInvestigation(siteId: string, alarmId: string) {
  const tenantId = '01940000-0000-7000-8000-000000000001';
  const alarms = state(tenantId, siteId);
  const alarm = alarms.find((candidate) => candidate.alarmId === alarmId);
  if (!alarm) return null;

  const firstAlarmId = alarms[0]?.alarmId ?? alarmId;
  const secondAlarmId = alarms[1]?.alarmId ?? alarmId;
  const thirdAlarmId = alarms[2]?.alarmId ?? alarmId;
  const isFlowIssue = alarm.alarmId === firstAlarmId;
  const isCopIssue = alarm.alarmId === secondAlarmId;

  return {
    schemaVersion: 1 as const,
    alarmId: alarm.alarmId,
    status: isCopIssue ? 'ROOT_CAUSE_CONFIRMED' as const : 'INVESTIGATING' as const,
    impact: {
      avoidableEnergyKwh: isFlowIssue ? 486 : isCopIssue ? 820 : 116,
      avoidableCost: isFlowIssue ? 392 : isCopIssue ? 662 : 94,
      currency: 'CNY',
      comfortImpactHours: isFlowIssue ? 3.4 : isCopIssue ? 0.8 : 1.6,
      reliabilityRisk: isFlowIssue ? 'HIGH' as const : isCopIssue ? 'MEDIUM' as const : 'LOW' as const,
      estimatedFrom: alarm.firstOccurredAt,
      estimatedTo: alarm.updatedAt,
    },
    hypotheses: isFlowIssue ? [
      {
        id: 'hypothesis-flow-valve',
        title: '末端调节阀开度异常导致系统阻力增大',
        status: 'SUPPORTED' as const,
        rationale: '流量下降与泵频率升高同时出现，且供回水压差偏高，符合系统阻力增加的表现。',
        supportingEvidenceCount: 3,
        contradictingEvidenceCount: 0,
        nextVerification: '对比关键支路阀位反馈与设计流量，确认是否存在异常关小或卡涩。',
      },
      {
        id: 'hypothesis-flow-sensor',
        title: '流量测点偏差或漂移',
        status: 'CANDIDATE' as const,
        rationale: '当前只有一个主流量测点直接描述异常，需要用泵曲线和压差交叉验证。',
        supportingEvidenceCount: 1,
        contradictingEvidenceCount: 1,
        nextVerification: '用泵频率、压差与设计曲线估算流量，并与测点读数交叉核对。',
      },
      {
        id: 'hypothesis-flow-pump',
        title: '水泵实际输出能力下降',
        status: 'WEAKENED' as const,
        rationale: '泵频率与运行状态正常，现有证据更倾向于系统阻力而不是泵本体能力不足。',
        supportingEvidenceCount: 1,
        contradictingEvidenceCount: 2,
        nextVerification: '检查泵进出口压差与振动趋势，排除叶轮或过滤器问题。',
      },
    ] : [
      {
        id: 'hypothesis-primary',
        title: isCopIssue ? '冷凝侧换热效率下降' : '关联工况异常',
        status: isCopIssue ? 'CONFIRMED' as const : 'SUPPORTED' as const,
        rationale: isCopIssue
          ? '冷却水供回水温差、冷凝压力与主机功率变化在同一时段一致恶化，且清洗后指标恢复。'
          : '现有运行证据与该工况假设一致，但仍需补充验证。',
        supportingEvidenceCount: isCopIssue ? 5 : 2,
        contradictingEvidenceCount: 0,
        nextVerification: isCopIssue ? null : '核对关联设备状态与同时间窗趋势。',
      },
    ],
    rootCause: isCopIssue ? {
      status: 'CONFIRMED' as const,
      title: '冷却水侧换热能力下降',
      rationale: '现场清洗与恢复后的冷凝压力、COP 改善形成闭环证据。',
      evidenceIds: alarm.evidence.map((evidence, index) => `${alarm.alarmId}:evidence:${index + 1}:${evidence.kind}`),
      confirmedAt: iso(4),
      confirmedBy: '值班工程师 A',
    } : {
      status: 'UNCONFIRMED' as const,
      title: null,
      rationale: null,
      evidenceIds: [],
      confirmedAt: null,
      confirmedBy: null,
    },
    verificationSteps: isFlowIssue ? [
      {
        id: 'verify-flow-1',
        title: '核对关键支路阀位与流量',
        method: '对比阀位反馈、主干流量、供回水压差，并与同负荷历史窗口比较。',
        status: 'IN_PROGRESS' as const,
        owner: '值班工程师 A',
        dueAt: iso(-45),
      },
      {
        id: 'verify-flow-2',
        title: '交叉验证主流量测点',
        method: '使用泵频率和压差估算流量，判断传感器偏差是否足以解释当前异常。',
        status: 'PENDING' as const,
        owner: null,
        dueAt: iso(-120),
      },
    ] : [
      {
        id: 'verify-primary',
        title: isCopIssue ? '复核清洗后的 24 小时 COP' : '复核恢复后的稳定性',
        method: isCopIssue ? '在相近负荷与湿球温度条件下与历史基线比较。' : '观察关键运行量与告警是否再次出现。',
        status: isCopIssue ? 'PASSED' as const : 'PENDING' as const,
        owner: isCopIssue ? '值班工程师 A' : null,
        dueAt: null,
      },
    ],
    relatedIssues: [
      ...(alarm.alarmId !== secondAlarmId ? [{
        alarmId: secondAlarmId,
        title: alarms[1]?.title ?? '主机 COP 持续偏低',
        relationship: 'SAME_INCIDENT' as const,
        occurredAt: alarms[1]?.firstOccurredAt ?? alarm.firstOccurredAt,
      }] : []),
      ...(alarm.alarmId !== thirdAlarmId ? [{
        alarmId: thirdAlarmId,
        title: alarms[2]?.title ?? '冷却塔逼近温度偏高',
        relationship: 'UPSTREAM' as const,
        occurredAt: alarms[2]?.firstOccurredAt ?? alarm.firstOccurredAt,
      }] : []),
    ],
    updatedAt: iso(3),
  };
}

export function createFrontendReviewIssuePerformance(siteId: string, period: '7d' | '30d' | '90d') {
  const multiplier = period === '7d' ? 1 : period === '30d' ? 4 : 11;
  const hours = ['00', '02', '04', '06', '08', '10', '12', '14', '16', '18', '20', '22'];
  const baseRows = [
    [0, 0, 0, 1, 3, 4, 2, 2, 4, 5, 2, 1],
    [0, 0, 1, 1, 2, 3, 2, 3, 5, 4, 2, 1],
    [0, 0, 0, 1, 2, 4, 3, 3, 4, 6, 3, 1],
    [0, 0, 0, 1, 3, 5, 4, 2, 5, 5, 2, 1],
    [0, 0, 1, 2, 4, 4, 3, 3, 5, 7, 4, 2],
    [0, 0, 0, 1, 2, 2, 1, 2, 3, 3, 1, 0],
    [0, 0, 0, 1, 1, 2, 1, 1, 2, 3, 1, 0],
  ];
  const weekdayLabels = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];

  return {
    schemaVersion: 1 as const,
    siteId,
    period,
    from: new Date(REVIEW_NOW - (period === '7d' ? 7 : period === '30d' ? 30 : 90) * 86_400_000).toISOString(),
    to: new Date(REVIEW_NOW).toISOString(),
    totalTriggered: 28 * multiplier,
    activeAtEnd: 3,
    acknowledgementRate: 0.89,
    diagnosisCoverage: 0.82,
    averageAcknowledgeSeconds: 8 * 60 + 24,
    averageResolutionSeconds: 3 * 3600 + 42 * 60,
    repeatIssueRate: 0.31,
    floodWindows: Math.max(1, multiplier - 1),
    avoidableEnergyKwh: 3_840 * multiplier,
    avoidableCost: 3_110 * multiplier,
    currency: 'CNY',
    heatmap: {
      columns: hours.map((hour) => `${hour}:00`),
      rows: weekdayLabels.map((label, index) => ({
        label,
        values: baseRows[index]!.map((value) => value * Math.max(1, Math.round(multiplier / 2))),
      })),
    },
    topContributors: [
      { key: 'chiller-1', label: '1# 冷水机组', triggeredCount: 16 * multiplier, averageResolutionSeconds: 4.3 * 3600 },
      { key: 'tower-1', label: '1# 冷却塔', triggeredCount: 11 * multiplier, averageResolutionSeconds: 2.6 * 3600 },
      { key: 'chwp-1', label: '1# 冷冻水泵', triggeredCount: 9 * multiplier, averageResolutionSeconds: 3.1 * 3600 },
      { key: 'ahu-south', label: '南区空调箱', triggeredCount: 7 * multiplier, averageResolutionSeconds: 1.8 * 3600 },
    ],
    recurringIssues: [
      { key: 'cop-low', label: '主机 COP 持续偏低', occurrences: 8 * multiplier, avoidableEnergyKwh: 1_480 * multiplier },
      { key: 'tower-approach', label: '冷却塔逼近温度偏高', occurrences: 6 * multiplier, avoidableEnergyKwh: 920 * multiplier },
      { key: 'flow-low', label: '冷冻水流量偏低', occurrences: 5 * multiplier, avoidableEnergyKwh: 760 * multiplier },
      { key: 'sensor-drift', label: '温度测点漂移', occurrences: 4 * multiplier, avoidableEnergyKwh: 210 * multiplier },
    ],
  };
}
