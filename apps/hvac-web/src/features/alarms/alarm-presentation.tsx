import { Badge } from '@/components/ui/badge';
import { SeverityMark } from '@/components/severity-mark';
import { SEVERITY_LEVEL_LABELS, type SeverityLevel } from '@/lib/operator-format';
import { formatEdgeEvaluationProvenance } from './alarm-operations';
import type {
  Alarm,
  AlarmEvaluationEffect,
  AlarmEvaluationStatus,
  AlarmOperation,
  AlarmSeverity,
  AlarmTimelineEntry,
} from './alarm-api';

export const SEVERITY_ORDER: readonly AlarmSeverity[] = ['CRITICAL', 'MAJOR', 'MINOR', 'WARNING', 'INFO'];

export const SEVERITY_LEVEL: Readonly<Record<AlarmSeverity, SeverityLevel>> = {
  CRITICAL: 'critical',
  MAJOR: 'major',
  MINOR: 'minor',
  WARNING: 'warning',
  INFO: 'info',
};

export const SEVERITY_LABELS: Readonly<Record<AlarmSeverity, string>> = {
  CRITICAL: SEVERITY_LEVEL_LABELS[SEVERITY_LEVEL.CRITICAL],
  MAJOR: SEVERITY_LEVEL_LABELS[SEVERITY_LEVEL.MAJOR],
  MINOR: SEVERITY_LEVEL_LABELS[SEVERITY_LEVEL.MINOR],
  WARNING: SEVERITY_LEVEL_LABELS[SEVERITY_LEVEL.WARNING],
  INFO: SEVERITY_LEVEL_LABELS[SEVERITY_LEVEL.INFO],
};

const OPERATION_LABELS: Readonly<Record<AlarmOperation, string>> = {
  PUBLISH: '触发',
  ACKNOWLEDGE: '确认',
  ASSIGN: '分配',
  UNASSIGN: '取消分配',
  SUPPRESS: '抑制',
  UNSUPPRESS: '解除抑制',
  CLEAR: '恢复',
};

export function operationLabel(operation: AlarmOperation): string {
  return OPERATION_LABELS[operation];
}

/** Why the system recovered an alarm, when the reason is one the operator should read. */
export function recoveryReason(entry: AlarmTimelineEntry): string | null {
  return entry.operation === 'CLEAR' && entry.reason === 'RULE_RETIRED' ? '规则已撤销' : null;
}

/** Condition, acknowledgement and active suppression shown together, as an operator reads them. */
export function alarmStatusLabel(alarm: Alarm): string {
  const condition = alarm.condition === 'ACTIVE' ? '活动' : '已恢复';
  const base = `${condition} · ${alarm.acknowledgement ? '已确认' : '未确认'}`;
  return alarm.condition === 'ACTIVE' && alarm.suppression ? `${base} · 已抑制` : base;
}

export const EVALUATION_STATUS_LABELS: Readonly<Record<AlarmEvaluationStatus, string>> = {
  MATCHED: '已命中',
  NOT_MATCHED: '未命中',
  INDETERMINATE: '待定',
};

export const EVALUATION_EFFECT_LABELS: Readonly<Record<AlarmEvaluationEffect, string>> = {
  PUBLISH: '触发告警',
  CLEAR: '自动恢复',
  NONE: '维持当前状态',
};

const QUALITY_BLOCKER_LABELS: Readonly<Record<string, string>> = {
  OUTSIDE_SCHEDULE: '不在运行时段内',
};

const QUALITY_BLOCKER_PREFIXES: ReadonlyArray<readonly [string, string]> = [
  ['MISSING_INPUT:', '缺少点位读数'],
  ['STALE_INPUT:', '点位读数已过期'],
  ['INVALID_INPUT:', '点位输入无效'],
  ['UNTRUSTED_QUALITY:', '读数质量不可信'],
];

const SNAPSHOT_KEY_LABELS: Readonly<Record<string, string>> = {
  supplyTemp: '供水温度',
  supply_water_temperature: '供水温度',
  returnTemp: '回水温度',
  return_water_temperature: '回水温度',
  leaving_evaporator_temperature: '蒸发器出水温度',
  leaving_condenser_temperature: '冷凝器出水温度',
  entering_condenser_temperature: '冷却水进水温度',
  outdoorTemp: '室外温度',
  outdoor_temperature: '室外温度',
  wet_bulb_temperature: '湿球温度',
  flowRate: '流量',
  flow_rate: '流量',
  chilled_water_flow: '冷冻水流量',
  cooling_water_flow: '冷却水流量',
  frequency: '频率',
  power: '功率',
  power_total: '功率',
  active_power: '有功功率',
  active_energy_total: '累计电量',
  deltaT: '供回水温差',
  delta_t: '供回水温差',
  loadKw: '冷负荷',
  load: '冷负荷',
  cooling_capacity: '瞬时制冷量',
  pressure: '压力',
  runState: '运行状态',
  run_state: '运行状态',
  probe_offset: '探头零点偏差',
  value: '测量值',
};

export interface EvaluationDisplayContext {
  readonly deviceNames?: ReadonlyMap<string, string>;
  readonly pointByReading?: ReadonlyMap<string, { readonly name: string; readonly unit?: string | null }>;
}

const UUID_LIKE_VALUE = /[0-9a-f]{8}-[0-9a-f]{4}-/i;
const RAW_ENUM_TOKEN = /^[A-Z][A-Z0-9_]{2,}$/;
const RAW_IDENTIFIER_TOKEN = /^[a-z0-9_./:-]+$/i;

function resolveReadingLabel(rawKey: string, context?: EvaluationDisplayContext): string | null {
  const fromCatalog = context?.pointByReading?.get(rawKey);
  if (fromCatalog?.name) {
    const slash = rawKey.indexOf('/');
    const deviceId = slash > 0 ? rawKey.slice(0, slash) : '';
    const deviceName = deviceId ? context?.deviceNames?.get(deviceId) : undefined;
    return deviceName && !fromCatalog.name.includes(deviceName) ? `${deviceName} ${fromCatalog.name}` : fromCatalog.name;
  }
  const slash = rawKey.indexOf('/');
  const pointKey = slash > 0 && UUID_LIKE_VALUE.test(rawKey.slice(0, slash)) ? rawKey.slice(slash + 1) : rawKey;
  if (SNAPSHOT_KEY_LABELS[pointKey]) {
    return SNAPSHOT_KEY_LABELS[pointKey];
  }
  if (!UUID_LIKE_VALUE.test(pointKey) && !RAW_ENUM_TOKEN.test(pointKey) && !RAW_IDENTIFIER_TOKEN.test(pointKey)) {
    return pointKey;
  }
  return null;
}

export function formatQualityBlocker(blocker: string | undefined, context?: EvaluationDisplayContext): string | null {
  if (!blocker) return null;
  if (QUALITY_BLOCKER_LABELS[blocker]) return QUALITY_BLOCKER_LABELS[blocker];
  if (blocker === 'INVALID_INPUT:DIV_BY_ZERO') return '表达式出现除零错误';
  for (const [prefix, message] of QUALITY_BLOCKER_PREFIXES) {
    if (blocker.startsWith(prefix)) {
      const targetKey = blocker.slice(prefix.length);
      const label = resolveReadingLabel(targetKey, context);
      return label ? `${label}${message.replace('点位', '')}` : message;
    }
  }
  return '条件暂不可判定';
}

const SNAPSHOT_METADATA_KEYS = new Set([
  'asOf',
  'siteId',
  'tenantId',
  'subjectId',
  'subjectType',
  'correlationId',
  'inputRevision',
  'inputs',
  'edgeProvenance',
]);

function readObjectField(obj: unknown, fieldName: string): unknown {
  if (typeof obj !== 'object' || obj === null || Array.isArray(obj)) {
    return undefined;
  }
  for (const [key, value] of Object.entries(obj)) {
    if (key === fieldName) {
      return value;
    }
  }
  return undefined;
}

function formatSnapshotScalar(label: string, rawValue: unknown): string | null {
  if (typeof rawValue === 'number' && Number.isFinite(rawValue)) {
    const formatted = Number.isInteger(rawValue) ? String(rawValue) : rawValue.toFixed(1);
    return `${label} ${formatted}`;
  }
  if (typeof rawValue === 'boolean') {
    return `${label} ${rawValue ? '是' : '否'}`;
  }
  if (
    typeof rawValue === 'string' &&
    rawValue.trim() !== '' &&
    !UUID_LIKE_VALUE.test(rawValue) &&
    !RAW_ENUM_TOKEN.test(rawValue)
  ) {
    return `${label} ${rawValue.trim()}`;
  }
  return null;
}

function formatInputFact(fallbackKey: string, rawFact: unknown, context?: EvaluationDisplayContext): string | null {
  if (typeof rawFact !== 'object' || rawFact === null || Array.isArray(rawFact)) {
    const label = resolveReadingLabel(fallbackKey, context);
    return label ? formatSnapshotScalar(label, rawFact) : null;
  }
  const factKey = readObjectField(rawFact, 'key');
  const resolvedKey = typeof factKey === 'string' && factKey.trim() !== '' ? factKey.trim() : fallbackKey;
  const label = resolveReadingLabel(resolvedKey, context) ?? resolveReadingLabel(fallbackKey, context);
  if (!label) return null;

  if (readObjectField(rawFact, 'present') === false) {
    return `${label} 无读数`;
  }

  const typedValue = readObjectField(rawFact, 'value');
  if (typeof typedValue !== 'object' || typedValue === null || Array.isArray(typedValue)) {
    return formatSnapshotScalar(label, typedValue);
  }

  const valueType = readObjectField(typedValue, 'type');
  if (valueType === 'NUMBER') {
    const num = readObjectField(typedValue, 'number');
    return formatSnapshotScalar(label, typeof num === 'number' ? num : 0);
  }
  if (valueType === 'BOOLEAN') {
    const flag = readObjectField(typedValue, 'boolean');
    return formatSnapshotScalar(label, typeof flag === 'boolean' ? flag : false);
  }
  if (valueType === 'STRING') {
    return formatSnapshotScalar(label, readObjectField(typedValue, 'string'));
  }

  return (
    formatSnapshotScalar(label, readObjectField(typedValue, 'number')) ??
    formatSnapshotScalar(label, readObjectField(typedValue, 'boolean')) ??
    formatSnapshotScalar(label, readObjectField(typedValue, 'string')) ??
    formatSnapshotScalar(label, readObjectField(typedValue, 'text'))
  );
}

export function formatEvaluationSnapshot(snapshot: Readonly<Record<string, unknown>>, context?: EvaluationDisplayContext): string {
  const parts: string[] = [];
  const edgeProvenanceLabel = formatEdgeEvaluationProvenance(readObjectField(snapshot, 'edgeProvenance'));
  const rawInputs = readObjectField(snapshot, 'inputs');
  if (typeof rawInputs === 'object' && rawInputs !== null && !Array.isArray(rawInputs)) {
    for (const [key, fact] of Object.entries(rawInputs)) {
      const formatted = formatInputFact(key, fact, context);
      if (formatted) {
        parts.push(formatted);
      }
      if (parts.length >= 4) break;
    }
    if (edgeProvenanceLabel) {
      return parts.length > 0 ? `${edgeProvenanceLabel} · ${parts.join(' · ')}` : edgeProvenanceLabel;
    }
    return parts.length > 0 ? parts.join(' · ') : '已采集现场读数';
  }

  for (const [key, rawValue] of Object.entries(snapshot)) {
    if (rawValue === null || rawValue === undefined || SNAPSHOT_METADATA_KEYS.has(key)) continue;
    const label = resolveReadingLabel(key, context);
    if (!label) continue;
    const formatted = formatSnapshotScalar(label, rawValue);
    if (formatted) {
      parts.push(formatted);
    }
    if (parts.length >= 4) break;
  }
  if (edgeProvenanceLabel) {
    return parts.length > 0 ? `${edgeProvenanceLabel} · ${parts.join(' · ')}` : edgeProvenanceLabel;
  }
  return parts.length > 0 ? parts.join(' · ') : '已采集现场读数';
}

export function formatDuration(from: string, to: string | undefined, now: number): string {
  return formatSpan((to ? Date.parse(to) : now) - Date.parse(from));
}

/** A length of time in the units an operator reads: minutes, then hours and minutes, then days and hours. */
export function formatSpan(milliseconds: number): string {
  const minutes = Math.max(0, Math.round(milliseconds / 60_000));
  if (minutes < 60) return `${minutes} 分钟`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} 小时 ${minutes % 60} 分`;
  return `${Math.floor(hours / 24)} 天 ${hours % 24} 小时`;
}

export function severityRank(severity: AlarmSeverity): number {
  return SEVERITY_ORDER.indexOf(severity);
}

export const SEVERITY_COLORS: Readonly<Record<AlarmSeverity, string>> = {
  CRITICAL: 'var(--severity-critical)',
  MAJOR: 'var(--severity-major)',
  MINOR: 'var(--severity-minor)',
  WARNING: 'var(--severity-warning)',
  INFO: 'var(--severity-info)',
};

/** Severity as a soft-tinted label led by its shape, so it reads without colour. */
export function SeverityBadge({ severity }: { readonly severity: AlarmSeverity }) {
  const color = SEVERITY_COLORS[severity];
  return (
    <Badge
      variant="outline"
      className="gap-1 border-transparent"
      style={{ color, background: `color-mix(in oklab, ${color} 12%, transparent)` }}
    >
      <SeverityMark level={SEVERITY_LEVEL[severity]} label="" className="size-2" />
      {SEVERITY_LABELS[severity]}
    </Badge>
  );
}

/** Most severe first, then the most recent. */
export function sortAlarmsForOperators(alarms: readonly Alarm[]): Alarm[] {
  return [...alarms].sort((left, right) => severityRank(left.currentSeverity) - severityRank(right.currentSeverity)
    || Date.parse(right.lastOccurredAt) - Date.parse(left.lastOccurredAt));
}

