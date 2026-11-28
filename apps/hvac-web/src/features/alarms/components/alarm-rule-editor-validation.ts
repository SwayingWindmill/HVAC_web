import type { SiteEquipmentIndex } from '@/features/assets/use-device-names';
import type { AlarmRuleAction, AlarmRuleActionKind, AlarmRuleActionTrigger, AlarmRuleDependencyGate } from '../alarm-operations';
import { SEVERITY_LABELS, SEVERITY_ORDER } from '../alarm-presentation';
import { newRuleUuidV7 } from '../alarm-rule-dag';
import type { AlarmRule, AlarmRuleCondition } from '../alarm-rules';
import {
  buildEquipmentOptions,
  createConditionForKind,
} from './alarm-rule-condition-options';

export const SEVERITY_OPTIONS = SEVERITY_ORDER.map((value) => ({ value, label: SEVERITY_LABELS[value] }));

export const RULE_KIND_OPTIONS: ReadonlyArray<{ readonly value: NonNullable<AlarmRule['ruleKind']>; readonly label: string }> = [
  { value: 'THRESHOLD', label: '阈值触发' },
  { value: 'SLOPE', label: '斜率变化' },
  { value: 'WINDOW', label: '流式窗口' },
  { value: 'EXPRESSION', label: '多点表达式' },
];

export const EXECUTION_TARGET_OPTIONS: ReadonlyArray<{ readonly value: NonNullable<AlarmRule['executionTarget']>; readonly label: string }> = [
  { value: 'CLOUD', label: '仅云端执行' },
  { value: 'EDGE', label: '仅边缘网关执行' },
  { value: 'CLOUD_AND_EDGE', label: '云边协同执行' },
];

export const TRIGGER_MODE_OPTIONS: ReadonlyArray<{ readonly value: AlarmRule['triggerMode']; readonly label: string }> = [
  { value: 'SIMPLE', label: '瞬时满足立即触发' },
  { value: 'DURATION', label: '持续时长达标后触发' },
  { value: 'REPEATING', label: '连续多次命中后触发' },
];

export const DEPENDENCY_GATE_OPTIONS: ReadonlyArray<{ readonly value: AlarmRuleDependencyGate; readonly label: string }> = [
  { value: 'NONE', label: '仅标注关系（无前置阻断）' },
  { value: 'ALL_ACTIVE', label: '需全部上游告警均处于活动态' },
  { value: 'ANY_ACTIVE', label: '任一上游告警处于活动态即可' },
];

export const ACTION_KIND_OPTIONS: ReadonlyArray<{ readonly value: AlarmRuleActionKind; readonly label: string }> = [
  { value: 'WORK_ORDER', label: '自动创建运维工单' },
  { value: 'PROTECTION_COMMAND', label: '下发设备保护控制指令' },
  { value: 'WEBHOOK', label: '回调外部 Webhook 接口' },
];

export const ACTION_TRIGGER_OPTIONS: ReadonlyArray<{ readonly value: AlarmRuleActionTrigger; readonly label: string }> = [
  { value: 'RAISE', label: '触发告警时' },
  { value: 'CLEAR', label: '自动恢复时' },
  { value: 'BOTH', label: '触发与恢复均执行' },
];

export const WORK_ORDER_PRIORITY_OPTIONS: ReadonlyArray<{
  readonly value: NonNullable<AlarmRuleAction['workOrderPriority']>;
  readonly label: string;
}> = [
  { value: 'LOW', label: '低' },
  { value: 'MEDIUM', label: '中' },
  { value: 'HIGH', label: '高' },
  { value: 'URGENT', label: '紧急' },
];

export const PROTECTION_CAPABILITY_OPTIONS: ReadonlyArray<{
  readonly value: string;
  readonly label: string;
  readonly paramKey?: string;
  readonly paramLabel?: string;
  readonly defaultVal?: number;
  readonly min?: number;
  readonly max?: number;
  readonly step?: number;
}> = [
  { value: 'STOP', label: '停机保护' },
  { value: 'START', label: '联动启动' },
  { value: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', label: '设定冷冻水温度目标', paramKey: 'setpointC', paramLabel: '设定温度 (°C)', defaultVal: 8, min: 5, max: 12, step: 0.5 },
  { value: 'SET_FREQUENCY', label: '调整频率', paramKey: 'frequencyHz', paramLabel: '目标频率 (Hz)', defaultVal: 40, min: 20, max: 50, step: 0.5 },
  { value: 'SET_OPENING', label: '调整阀位开度', paramKey: 'openingPct', paramLabel: '目标开度 (%)', defaultVal: 50, min: 0, max: 100, step: 1 },
  { value: 'SET_TEMPERATURE_SETPOINT', label: '设定温度目标', paramKey: 'setpointC', paramLabel: '设定温度 (°C)', defaultVal: 24, min: 16, max: 30, step: 0.5 },
];

export function validateConditionTree(prefix: string, cond: AlarmRuleCondition): string | null {
  switch (cond.kind) {
    case 'AND':
    case 'OR': {
      const children = cond.children ?? [];
      if (children.length < 2) return `${prefix}的组合逻辑至少需要包含 2 条子条件`;
      for (let i = 0; i < children.length; i += 1) {
        const err = validateConditionTree(`${prefix}第 ${i + 1} 项`, children[i]);
        if (err) return err;
      }
      return null;
    }
    case 'COMPARE': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      const numericOp = cond.operator === 'GT' || cond.operator === 'GTE' || cond.operator === 'LT' || cond.operator === 'LTE';
      if (numericOp && (typeof cond.value !== 'number' || !Number.isFinite(cond.value))) {
        return `${prefix}选择大小比较符号时，目标阈值必须为有效数值`;
      }
      if (cond.value === undefined || cond.value === '') return `${prefix}缺少比较阈值`;
      return null;
    }
    case 'RANGE': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (cond.minimum === undefined || cond.maximum === undefined || !Number.isFinite(cond.minimum) || !Number.isFinite(cond.maximum)) {
        return `${prefix}需要填写有效的区间上下限`;
      }
      if (cond.minimum > cond.maximum) return `${prefix}的安全区间下限不能大于上限`;
      return null;
    }
    case 'HYSTERESIS': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (cond.trigger === undefined || cond.reset === undefined || !Number.isFinite(cond.trigger) || !Number.isFinite(cond.reset)) {
        return `${prefix}需要填写有效的触发阈值与回差恢复值`;
      }
      if ((cond.direction ?? 'ABOVE') === 'ABOVE' && cond.reset >= cond.trigger) {
        return `${prefix}为上限越限迟滞时，回差恢复值必须严格小于触发阈值`;
      }
      if (cond.direction === 'BELOW' && cond.reset <= cond.trigger) {
        return `${prefix}为下限越限迟滞时，回差恢复值必须严格大于触发阈值`;
      }
      return null;
    }
    case 'SLOPE': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (cond.delta === undefined || !Number.isFinite(cond.delta)) return `${prefix}需要填写有效的斜率变化幅度`;
      if (!cond.windowSeconds || cond.windowSeconds < 1 || cond.windowSeconds > 86400) {
        return `${prefix}的斜率时间窗必须在 1 至 86400 秒之间`;
      }
      return null;
    }
    case 'WINDOW_AGG': {
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (!cond.agg || !cond.windowType || !cond.operator) return `${prefix}的窗口聚合参数不完整`;
      if (cond.threshold === undefined || !Number.isFinite(cond.threshold)) return `${prefix}需要填写有效的聚合阈值`;
      if (!cond.windowSeconds || !Number.isInteger(cond.windowSeconds) || cond.windowSeconds < 1 || cond.windowSeconds > 86400) {
        return `${prefix}的窗口时长必须在 1 至 86400 秒之间`;
      }
      const minAllowed = cond.agg === 'STDDEV' ? 2 : 1;
      if (!cond.minSamples || !Number.isInteger(cond.minSamples) || cond.minSamples < minAllowed || cond.minSamples > 128) {
        return `${prefix}的最小样本数必须在 ${minAllowed} 至 128 之间`;
      }
      return null;
    }
    case 'EXPRESSION': {
      if (!(cond.expression ?? '').trim()) return `${prefix}的多点表达式不能为空`;
      const inputs = Object.values(cond.inputs ?? {});
      if (inputs.length === 0 || inputs.some((r) => !r.deviceId || !r.pointCode.trim())) {
        return `${prefix}的多点表达式至少需要绑定 1 个完整输入变量`;
      }
      return null;
    }
    case 'NO_DATA':
    case 'STALE':
    case 'UNCHANGED':
    case 'CHANGED':
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (!cond.ageSeconds || cond.ageSeconds < 1 || cond.ageSeconds > 31_536_000) {
        return `${prefix}的阈值时长必须在 1 至 31536000 秒之间`;
      }
      return null;
    case 'REASONS':
      if (!cond.reading?.deviceId || !cond.reading.pointCode.trim()) return `${prefix}缺少监测设备或点位`;
      if (!cond.reasons || cond.reasons.length === 0) return `${prefix}至少需要配置 1 项诊断原因`;
      return null;
  }
}

export function createEmptyRule(equipment: SiteEquipmentIndex): AlarmRule {
  const equipmentOptions = buildEquipmentOptions(equipment);
  const reading = {
    deviceId: equipmentOptions[0]?.deviceId ?? '018f3e00-3000-7000-8000-000000000001',
    pointCode: equipmentOptions[0]?.points[0]?.pointCode ?? 'supply_water_temperature',
  };
  return {
    ruleId: newRuleUuidV7(),
    title: '新增供水温度监测规则',
    severity: 'MAJOR',
    ruleKind: 'THRESHOLD',
    triggerMode: 'DURATION',
    durationSeconds: 300,
    dependsOnRuleIds: [],
    dependencyGate: 'NONE',
    actions: [],
    raise: createConditionForKind('COMPARE', equipmentOptions, reading),
    clear: { kind: 'COMPARE', reading, operator: 'LT', value: 10 },
  };
}
