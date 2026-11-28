import type { KeyboardEvent } from 'react';
import type { SiteEquipmentIndex } from '@/features/assets/use-device-names';
import { WINDOW_AGG_LABELS, WINDOW_TYPE_LABELS } from '../alarm-rule-dag';
import type { AlarmRuleCondition, AlarmRuleReading } from '../alarm-rules';

export type ConditionKind = AlarmRuleCondition['kind'];
export type CompareOperator = NonNullable<AlarmRuleCondition['operator']>;
export type HysteresisDirection = NonNullable<AlarmRuleCondition['direction']>;
export type WindowAgg = NonNullable<AlarmRuleCondition['agg']>;
export type WindowType = NonNullable<AlarmRuleCondition['windowType']>;
export type DiagnosticReason = NonNullable<AlarmRuleCondition['reasons']>[number];

export const KIND_OPTIONS: ReadonlyArray<{ readonly value: ConditionKind; readonly label: string }> = [
  { value: 'COMPARE', label: '单一阈值比较' },
  { value: 'HYSTERESIS', label: '回差死区' },
  { value: 'RANGE', label: '安全区间' },
  { value: 'SLOPE', label: '斜率突变' },
  { value: 'WINDOW_AGG', label: '流式窗口聚合' },
  { value: 'EXPRESSION', label: '多点表达式' },
  { value: 'NO_DATA', label: '长期缺数' },
  { value: 'STALE', label: '读数过期' },
  { value: 'UNCHANGED', label: '数值停滞' },
  { value: 'CHANGED', label: '近期变化' },
  { value: 'REASONS', label: '诊断原因命中' },
  { value: 'AND', label: '全部满足（且）' },
  { value: 'OR', label: '任一满足（或）' },
];

export const WINDOW_AGG_OPTIONS: ReadonlyArray<{ readonly value: WindowAgg; readonly label: string }> = [
  { value: 'AVG', label: WINDOW_AGG_LABELS.AVG },
  { value: 'MIN', label: WINDOW_AGG_LABELS.MIN },
  { value: 'MAX', label: WINDOW_AGG_LABELS.MAX },
  { value: 'SUM', label: WINDOW_AGG_LABELS.SUM },
  { value: 'COUNT', label: WINDOW_AGG_LABELS.COUNT },
  { value: 'STDDEV', label: WINDOW_AGG_LABELS.STDDEV },
  { value: 'P95', label: WINDOW_AGG_LABELS.P95 },
  { value: 'P99', label: WINDOW_AGG_LABELS.P99 },
];

export const WINDOW_TYPE_OPTIONS: ReadonlyArray<{ readonly value: WindowType; readonly label: string }> = [
  { value: 'SLIDING', label: WINDOW_TYPE_LABELS.SLIDING },
  { value: 'TUMBLING', label: WINDOW_TYPE_LABELS.TUMBLING },
];

export const COMPARE_OP_OPTIONS: ReadonlyArray<{ readonly value: CompareOperator; readonly label: string }> = [
  { value: 'GT', label: '大于 (>)' },
  { value: 'GTE', label: '大于等于 (>=)' },
  { value: 'LT', label: '小于 (<)' },
  { value: 'LTE', label: '小于等于 (<=)' },
  { value: 'EQ', label: '等于 (=)' },
  { value: 'NE', label: '不等于 (!=)' },
];

export const SLOPE_OP_OPTIONS: ReadonlyArray<{ readonly value: CompareOperator; readonly label: string }> = [
  { value: 'GT', label: '上升超过 (>)' },
  { value: 'GTE', label: '上升不少于 (>=)' },
  { value: 'LT', label: '下降超过 (<)' },
  { value: 'LTE', label: '下降不少于 (<=)' },
];

export const HYSTERESIS_DIRECTIONS: ReadonlyArray<{ readonly value: HysteresisDirection; readonly label: string }> = [
  { value: 'ABOVE', label: '上限越限' },
  { value: 'BELOW', label: '下限越限' },
];

export const DIAGNOSTIC_REASON_OPTIONS: ReadonlyArray<{ readonly value: DiagnosticReason; readonly label: string }> = [
  { value: 'OUT_OF_RANGE', label: '超出量程' },
  { value: 'SPIKE', label: '跳变' },
  { value: 'SENTINEL', label: '通讯异常值' },
  { value: 'FLATLINE', label: '卡住' },
  { value: 'DERIVED_INPUT', label: '计算输入不可信' },
  { value: 'LOW_CONFIDENCE', label: '温差不确定度过大' },
  { value: 'SOURCE_UNTRUSTED', label: '采集源不可信' },
  { value: 'PROBE_OFFSET', label: '供回水零点偏差' },
  { value: 'PHYSICS_WET_BULB_DRY', label: '湿球高于干球' },
  { value: 'PHYSICS_WET_BULB_CONDENSER', label: '湿球不像湿球' },
  { value: 'PHYSICS_POWER', label: '功率超过铭牌' },
  { value: 'PHYSICS_COP', label: '能效超过卡诺上限' },
  { value: 'PHYSICS_CHW_DIRECTION', label: '冷冻水供回水接反' },
  { value: 'PHYSICS_CW_DIRECTION', label: '冷却水进出水接反' },
  { value: 'PHYSICS_HEAT_BALANCE', label: '热平衡对不上' },
];

export interface EquipmentReadingOption {
  readonly deviceId: string;
  readonly deviceName: string;
  readonly points: ReadonlyArray<{ readonly pointCode: string; readonly name: string }>;
}

export function buildEquipmentOptions(equipment: SiteEquipmentIndex): EquipmentReadingOption[] {
  const pointsByDevice = new Map<string, Array<{ pointCode: string; name: string }>>();
  for (const [key, info] of equipment.pointByReading) {
    const slash = key.indexOf('/');
    if (slash <= 0) continue;
    const deviceId = key.slice(0, slash);
    const pointCode = key.slice(slash + 1);
    const list = pointsByDevice.get(deviceId) ?? [];
    list.push({ pointCode, name: info.name });
    pointsByDevice.set(deviceId, list);
  }
  const options: EquipmentReadingOption[] = [];
  for (const [deviceId, deviceName] of equipment.nameByDevice) {
    options.push({
      deviceId,
      deviceName,
      points: pointsByDevice.get(deviceId) ?? [{ pointCode: 'supply_water_temperature', name: '供水温度' }],
    });
  }
  options.sort((a, b) => a.deviceName.localeCompare(b.deviceName, 'zh-CN'));
  return options;
}

export function defaultReadingFrom(
  equipmentOptions: readonly EquipmentReadingOption[],
  fallback?: AlarmRuleReading,
): AlarmRuleReading {
  if (fallback) return { ...fallback };
  const firstDevice = equipmentOptions[0];
  return {
    deviceId: firstDevice?.deviceId ?? '018f3e00-3000-7000-8000-000000000001',
    pointCode: firstDevice?.points[0]?.pointCode ?? 'supply_water_temperature',
  };
}

export function createConditionForKind(
  kind: ConditionKind,
  equipmentOptions: readonly EquipmentReadingOption[],
  baseReading?: AlarmRuleReading,
): AlarmRuleCondition {
  const reading = defaultReadingFrom(equipmentOptions, baseReading);
  const matchingDevice = equipmentOptions.find((d) => d.deviceId === reading.deviceId) ?? equipmentOptions[0];
  const altPoint = matchingDevice?.points.find((p) => p.pointCode !== reading.pointCode)?.pointCode
    ?? equipmentOptions[1]?.points[0]?.pointCode
    ?? reading.pointCode;
  const secondReading: AlarmRuleReading = altPoint !== reading.pointCode
    ? { deviceId: reading.deviceId, pointCode: altPoint }
    : defaultReadingFrom(equipmentOptions.slice(1), reading);
  switch (kind) {
    case 'COMPARE':
      return { kind: 'COMPARE', reading, operator: 'GT', value: 12 };
    case 'RANGE':
      return { kind: 'RANGE', reading, minimum: 5, maximum: 15 };
    case 'HYSTERESIS':
      return { kind: 'HYSTERESIS', reading, direction: 'ABOVE', trigger: 12, reset: 10.5 };
    case 'SLOPE':
      return { kind: 'SLOPE', reading, operator: 'GT', delta: 2.5, windowSeconds: 300 };
    case 'WINDOW_AGG':
      return {
        kind: 'WINDOW_AGG',
        reading,
        agg: 'AVG',
        windowType: 'SLIDING',
        windowSeconds: 300,
        minSamples: 3,
        operator: 'GT',
        threshold: 12,
      };
    case 'EXPRESSION':
      return {
        kind: 'EXPRESSION',
        expression: 'a - b > 3',
        inputs: { a: reading, b: secondReading },
      };
    case 'NO_DATA':
    case 'STALE':
    case 'UNCHANGED':
    case 'CHANGED':
      return { kind, reading, ageSeconds: 300 };
    case 'AND':
    case 'OR':
      return {
        kind,
        children: [
          { kind: 'COMPARE', reading, operator: 'GT', value: 12 },
          { kind: 'COMPARE', reading: secondReading, operator: 'LTE', value: 20 },
        ],
      };
    case 'REASONS':
      return { kind: 'REASONS', reading, match: 'ANY', reasons: ['OUT_OF_RANGE'] };
  }
}

export function parseCompareInputValue(raw: string): number | string | boolean {
  const trimmed = raw.trim();
  if (trimmed === 'true') return true;
  if (trimmed === 'false') return false;
  if (trimmed !== '') {
    const num = Number(trimmed);
    if (Number.isFinite(num)) return num;
  }
  return raw;
}

export function preventImeEnterSubmit(event: KeyboardEvent<HTMLInputElement>) {
  if (event.key === 'Enter' && (event.nativeEvent.isComposing || event.keyCode === 229)) {
    event.preventDefault();
  }
}
