import type {
  CounterAuditSummary,
  MeterRatioVersion,
  RatioApplicationMode,
  RatioReadingUnit,
  SiteCalendarDayType,
} from '../../api/accounting-config.ts';
import { isOneOf } from '../../lib/typed.ts';
import { siteMidnight } from '../../lib/site-time.ts';
import { instantFromSiteInput } from '../savings/savings-model.ts';

export const SITE_CALENDAR_DAY_TYPES = [
  'HOLIDAY',
  'SPECIAL_DAY',
  'WEEKDAY',
  'WEEKEND',
] as const satisfies readonly SiteCalendarDayType[];

export const SITE_CALENDAR_DAY_TYPE_LABEL: Readonly<Record<SiteCalendarDayType, string>> = {
  HOLIDAY: '节假日',
  SPECIAL_DAY: '特殊日',
  WEEKDAY: '工作日',
  WEEKEND: '周末',
};

export const SITE_CALENDAR_DAY_TYPE_DESCRIPTION: Readonly<Record<SiteCalendarDayType, string>> = {
  HOLIDAY: '节假日（按节假日分时电价计费）',
  SPECIAL_DAY: '特殊日（按特殊日分时电价计费）',
  WEEKDAY: '工作日（调休上班按工作日电价计费）',
  WEEKEND: '周末（按周末分时电价计费）',
};

export type BadgeToneVariant = 'default' | 'success' | 'warning' | 'destructive' | 'outline';

export const SITE_CALENDAR_DAY_TYPE_VARIANT: Readonly<
  Record<SiteCalendarDayType, { readonly marker: string; readonly variant: BadgeToneVariant }>
> = {
  HOLIDAY: { marker: '■', variant: 'warning' },
  SPECIAL_DAY: { marker: '●', variant: 'default' },
  WEEKDAY: { marker: '○', variant: 'outline' },
  WEEKEND: { marker: '●', variant: 'success' },
};

export const RATIO_APPLICATION_MODES = [
  'PLATFORM_APPLIED',
  'DEVICE_APPLIED',
  'EDGE_APPLIED',
] as const satisfies readonly RatioApplicationMode[];

export const RATIO_APPLICATION_MODE_LABEL: Readonly<Record<RatioApplicationMode, string>> = {
  PLATFORM_APPLIED: '平台乘变比（云端折算）',
  DEVICE_APPLIED: '表端已乘变比（云端不重复乘）',
  EDGE_APPLIED: '边缘网关已乘变比（云端不重复乘）',
};

export const RATIO_APPLICATION_MODE_SHORT_LABEL: Readonly<Record<RatioApplicationMode, string>> = {
  PLATFORM_APPLIED: '平台乘变比',
  DEVICE_APPLIED: '表端已乘变比',
  EDGE_APPLIED: '边缘已乘变比',
};

export const RATIO_READING_UNITS = [
  'kWh',
  'Wh',
  'MWh',
  'GJ',
  'MJ',
  'RTh',
] as const satisfies readonly RatioReadingUnit[];

export const RATIO_READING_UNIT_LABEL: Readonly<Record<RatioReadingUnit, string>> = {
  Wh: 'Wh（瓦时）',
  kWh: 'kWh（千瓦时）',
  MWh: 'MWh（兆瓦时）',
  GJ: 'GJ（吉焦）',
  MJ: 'MJ（兆焦）',
  RTh: 'RTh（冷吨时）',
};

export type RatioLifecycleKey = 'ACTIVE' | 'RETIRED' | 'SUPERSEDED' | 'DRAFT';

export interface RatioLifecyclePresentation {
  readonly key: RatioLifecycleKey;
  readonly label: string;
  readonly variant: BadgeToneVariant;
}

/**
 * Derives lifecycle display from both `status` and `effectiveTo` against `now`.
 * Backend keeps `status === 'RELEASED'` when retiring a version by setting `effectiveTo`.
 */
export function deriveRatioLifecycleStatus(
  version: Pick<MeterRatioVersion, 'status' | 'effectiveFrom' | 'effectiveTo'>,
  now: Date = new Date(),
): RatioLifecyclePresentation {
  if (version.status === 'DRAFT') {
    return { key: 'DRAFT', label: '◦ 草稿', variant: 'outline' };
  }
  if (version.status === 'SUPERSEDED') {
    return { key: 'SUPERSEDED', label: '— 已替代', variant: 'outline' };
  }
  if (version.effectiveTo) {
    const endMs = Date.parse(version.effectiveTo);
    if (Number.isFinite(endMs) && endMs <= now.getTime()) {
      return { key: 'RETIRED', label: '○ 已终止', variant: 'outline' };
    }
  }
  return { key: 'ACTIVE', label: '● 生效中', variant: 'success' };
}

export function canRetireRatioVersion(
  version: Pick<MeterRatioVersion, 'status' | 'effectiveTo'>,
): boolean {
  return version.status === 'RELEASED' && version.effectiveTo === null;
}

export function previewCloudMultiplier(input: {
  readonly ctRatio: number;
  readonly ptRatio: number;
  readonly meterMultiplier: number;
  readonly ratioApplicationMode: RatioApplicationMode;
}): number | null {
  if (
    !Number.isFinite(input.ctRatio) ||
    input.ctRatio <= 0 ||
    !Number.isFinite(input.ptRatio) ||
    input.ptRatio <= 0 ||
    !Number.isFinite(input.meterMultiplier) ||
    input.meterMultiplier <= 0
  ) {
    return null;
  }
  if (input.ratioApplicationMode !== 'PLATFORM_APPLIED') {
    return 1;
  }
  return Number((input.ctRatio * input.ptRatio * input.meterMultiplier).toPrecision(12));
}

const DATE_KEY_PATTERN = /^(\d{4})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/;
const DATETIME_LOCAL_PATTERN = /^(\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01]))T([01]\d|2[0-3]):([0-5]\d)$/;

export function isValidDateKey(value: string): boolean {
  const match = DATE_KEY_PATTERN.exec(value.trim());
  if (!match) return false;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const check = new Date(Date.UTC(year, month - 1, day));
  return (
    check.getUTCFullYear() === year &&
    check.getUTCMonth() + 1 === month &&
    check.getUTCDate() === day
  );
}

export function parseSiteMomentInput(raw: string, timeZone: string): Date | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;

  if (isValidDateKey(trimmed)) {
    const [year, month, day] = trimmed.split('-').map(Number);
    return siteMidnight(year, month, day, timeZone);
  }

  const localMatch = DATETIME_LOCAL_PATTERN.exec(trimmed);
  if (localMatch) {
    const datePart = localMatch[1];
    if (!datePart || !isValidDateKey(datePart)) return null;
    const instant = instantFromSiteInput(trimmed, timeZone);
    return Number.isFinite(instant.getTime()) ? instant : null;
  }

  const parsedMs = Date.parse(trimmed);
  return Number.isFinite(parsedMs) ? new Date(parsedMs) : null;
}

export interface RatioFormInput {
  readonly pointId: string;
  readonly ctRatio: string;
  readonly ptRatio: string;
  readonly meterMultiplier: string;
  readonly ratioApplicationMode: string;
  readonly readingUnit: string;
  readonly effectiveFrom: string;
  readonly effectiveTo?: string;
  readonly reason?: string;
}

export type RatioFormValidationField =
  | 'pointId'
  | 'ctRatio'
  | 'ptRatio'
  | 'meterMultiplier'
  | 'ratioApplicationMode'
  | 'readingUnit'
  | 'effectiveFrom'
  | 'effectiveTo'
  | 'reason';

export type RatioFormValidationResult =
  | {
      readonly ok: true;
      readonly value: {
        readonly pointId: string;
        readonly ctRatio: number;
        readonly ptRatio: number;
        readonly meterMultiplier: number;
        readonly cloudMultiplier: number;
        readonly ratioApplicationMode: RatioApplicationMode;
        readonly readingUnit: RatioReadingUnit;
        readonly effectiveFrom: string;
        readonly effectiveTo?: string;
        readonly reason?: string;
      };
    }
  | {
      readonly ok: false;
      readonly field: RatioFormValidationField;
      readonly message: string;
    };

function parsePositiveNumber(raw: string): number | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
}

export function validateRatioForm(
  input: RatioFormInput,
  timeZone: string,
): RatioFormValidationResult {
  const pointId = input.pointId.trim();
  if (!pointId) {
    return { ok: false, field: 'pointId', message: '请选择或填写测点 ID' };
  }

  const ctRatio = parsePositiveNumber(input.ctRatio);
  if (ctRatio === null) {
    return { ok: false, field: 'ctRatio', message: 'CT 变比须为大于 0 的数字' };
  }

  const ptRatio = parsePositiveNumber(input.ptRatio);
  if (ptRatio === null) {
    return { ok: false, field: 'ptRatio', message: 'PT 变比须为大于 0 的数字' };
  }

  const meterMultiplierRaw = input.meterMultiplier.trim();
  const meterMultiplier = meterMultiplierRaw === '' ? 1 : parsePositiveNumber(meterMultiplierRaw);
  if (meterMultiplier === null) {
    return { ok: false, field: 'meterMultiplier', message: '附加乘数须为大于 0 的数字' };
  }

  if (!isOneOf(RATIO_APPLICATION_MODES, input.ratioApplicationMode)) {
    return { ok: false, field: 'ratioApplicationMode', message: '请选择有效的应用层级' };
  }

  if (!isOneOf(RATIO_READING_UNITS, input.readingUnit)) {
    return { ok: false, field: 'readingUnit', message: '请选择有效的计量单位' };
  }

  const effectiveFromDate = parseSiteMomentInput(input.effectiveFrom, timeZone);
  if (!effectiveFromDate) {
    return { ok: false, field: 'effectiveFrom', message: '请输入有效的生效开始时间' };
  }

  const rawEffectiveTo = (input.effectiveTo ?? '').trim();
  let effectiveToIso: string | undefined;
  if (rawEffectiveTo) {
    const effectiveToDate = parseSiteMomentInput(rawEffectiveTo, timeZone);
    if (!effectiveToDate) {
      return { ok: false, field: 'effectiveTo', message: '请输入有效的生效结束时间' };
    }
    if (effectiveToDate.getTime() <= effectiveFromDate.getTime()) {
      return {
        ok: false,
        field: 'effectiveTo',
        message: '生效结束时间必须晚于生效开始时间',
      };
    }
    effectiveToIso = effectiveToDate.toISOString();
  }

  const reason = (input.reason ?? '').trim();
  if (reason.length > 500) {
    return { ok: false, field: 'reason', message: '变更说明不能超过 500 字' };
  }

  const cloudMultiplier = previewCloudMultiplier({
    ctRatio,
    ptRatio,
    meterMultiplier,
    ratioApplicationMode: input.ratioApplicationMode,
  });
  if (cloudMultiplier === null) {
    return { ok: false, field: 'ctRatio', message: '无法计算云端生效乘数，请核对变比参数' };
  }

  return {
    ok: true,
    value: {
      pointId,
      ctRatio,
      ptRatio,
      meterMultiplier,
      cloudMultiplier,
      ratioApplicationMode: input.ratioApplicationMode,
      readingUnit: input.readingUnit,
      effectiveFrom: effectiveFromDate.toISOString(),
      ...(effectiveToIso ? { effectiveTo: effectiveToIso } : {}),
      ...(reason ? { reason } : {}),
    },
  };
}

export type CounterTransitionFilterValue =
  | 'ALL'
  | 'INVALID_ZERO_DROP'
  | 'INVALID_SPIKE'
  | 'INVALID_DECREASE'
  | 'RESET'
  | 'ROLLOVER'
  | 'RECOVERY'
  | 'METER_REPLACEMENT'
  | 'INCREASE';

export interface CounterTransitionMeta {
  readonly label: string;
  readonly marker: string;
  readonly variant: BadgeToneVariant;
  readonly isAnomaly: boolean;
}

const COUNTER_TRANSITION_META_MAP: Readonly<Record<string, CounterTransitionMeta>> = {
  INVALID_ZERO_DROP: {
    label: '零值通信干扰',
    marker: '◆',
    variant: 'destructive',
    isAnomaly: true,
  },
  INVALID_SPIKE: {
    label: '单步飞值尖峰',
    marker: '◆',
    variant: 'destructive',
    isAnomaly: true,
  },
  INVALID_DECREASE: {
    label: '异常回退',
    marker: '▲',
    variant: 'destructive',
    isAnomaly: true,
  },
  INVALID: {
    label: '无效读数',
    marker: '◆',
    variant: 'destructive',
    isAnomaly: true,
  },
  RESET: {
    label: '换表归零',
    marker: '■',
    variant: 'warning',
    isAnomaly: true,
  },
  ROLLOVER: {
    label: '满量程翻转',
    marker: '■',
    variant: 'warning',
    isAnomaly: true,
  },
  RECOVERY: {
    label: '回退后恢复',
    marker: '●',
    variant: 'default',
    isAnomaly: true,
  },
  METER_REPLACEMENT: {
    label: '换表衔接',
    marker: '■',
    variant: 'warning',
    isAnomaly: true,
  },
  REVISION_BOUNDARY: {
    label: '点位修订分段',
    marker: '○',
    variant: 'outline',
    isAnomaly: false,
  },
  UNIT_BOUNDARY: {
    label: '计量单位分段',
    marker: '○',
    variant: 'outline',
    isAnomaly: false,
  },
  INITIAL: {
    label: '初始采样',
    marker: '○',
    variant: 'outline',
    isAnomaly: false,
  },
  UNCHANGED: {
    label: '示数持平',
    marker: '○',
    variant: 'outline',
    isAnomaly: false,
  },
  INCREASE: {
    label: '正常递进',
    marker: '●',
    variant: 'success',
    isAnomaly: false,
  },
};

export const COUNTER_TRANSITION_FILTER_OPTIONS: readonly {
  readonly value: CounterTransitionFilterValue;
  readonly label: string;
}[] = [
  { value: 'ALL', label: '全部类型' },
  { value: 'INVALID_ZERO_DROP', label: '零值通信干扰' },
  { value: 'INVALID_SPIKE', label: '单步飞值尖峰' },
  { value: 'INVALID_DECREASE', label: '异常回退' },
  { value: 'RESET', label: '换表归零' },
  { value: 'ROLLOVER', label: '满量程翻转' },
  { value: 'RECOVERY', label: '回退后恢复' },
  { value: 'METER_REPLACEMENT', label: '换表衔接' },
  { value: 'INCREASE', label: '正常递进' },
];

export function counterTransitionMeta(transitionType: string): CounterTransitionMeta {
  return (
    COUNTER_TRANSITION_META_MAP[transitionType] ?? {
      label: '其他清洗事件',
      marker: '○',
      variant: 'outline',
      isAnomaly: false,
    }
  );
}

const COUNTER_QUALITY_META_MAP: Readonly<
  Record<string, { readonly label: string; readonly variant: BadgeToneVariant }>
> = {
  GOOD: { label: '有效', variant: 'success' },
  SUSPECT: { label: '可疑', variant: 'warning' },
  INVALID: { label: '无效', variant: 'destructive' },
  UNKNOWN: { label: '未知', variant: 'outline' },
};

export function counterQualityMeta(quality: string): {
  readonly label: string;
  readonly variant: BadgeToneVariant;
} {
  return (
    COUNTER_QUALITY_META_MAP[quality.toUpperCase()] ?? {
      label: '其他状态',
      variant: 'outline',
    }
  );
}

const COUNTER_QUALITY_REASON_LABEL: Readonly<Record<string, string>> = {
  OUT_OF_RANGE: '超出物理量程范围',
  SPIKE: '单步增量超出功率上限',
  SENTINEL: '命中故障哨兵值',
  FLATLINE: '读数长时间未变化',
  DERIVED_INPUT: '上游计算输入异常',
  LOW_CONFIDENCE: '测量不确定度偏高',
  SOURCE_UNTRUSTED: '采集来源不可信',
  TYPE_MISMATCH: '读数数据类型不匹配',
  UNIT_MISMATCH: '读数单位不一致',
  CLOCK_AHEAD: '设备时钟超前',
  CLOCK_BEHIND: '设备时钟滞后',
  SOURCE_LAG_EXCEEDED: '上报延迟超限',
  DUPLICATE: '重复样本',
  OUT_OF_ORDER: '乱序到达样本',
  REPLAYED: '断网续传补报',
  ZERO_DROP: '累计示数突降为零',
  COUNTER_ZERO_DROP: '累计示数突降为零',
  COUNTER_SPIKE: '单步累计增量飞值',
  COUNTER_DECREASE: '累计示数异常倒退',
  COUNTER_RESET: '设备归零重启',
  COUNTER_ROLLOVER: '表盘满量程翻转',
  METER_REPLACEMENT: '更换新表计衔接',
  RECOVERY: '异常跌落后恢复递进',
};

export function formatCounterQualityReasons(reasons: readonly string[]): string {
  if (reasons.length === 0) return '—';
  const labels = reasons.map((code) => COUNTER_QUALITY_REASON_LABEL[code] ?? '读数存在质量告警');
  return [...new Set(labels)].join('、');
}

export function formatWeekdayShort(dateKey: string): string {
  if (!isValidDateKey(dateKey)) return '';
  const [year, month, day] = dateKey.split('-').map(Number);
  const weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'] as const;
  return weekdays[new Date(Date.UTC(year, month - 1, day)).getUTCDay()] ?? '';
}

export interface CounterAuditKpiMetric {
  readonly key: keyof CounterAuditSummary;
  readonly label: string;
  readonly value: number;
  readonly tone: 'neutral' | 'warning' | 'destructive';
}

export function buildCounterAuditKpiMetrics(
  summary: CounterAuditSummary,
): readonly CounterAuditKpiMetric[] {
  return [
    { key: 'totalSamples', label: '总采样数', value: summary.totalSamples, tone: 'neutral' },
    {
      key: 'anomalyCount',
      label: '异常与事件',
      value: summary.anomalyCount,
      tone: summary.anomalyCount > 0 ? 'warning' : 'neutral',
    },
    {
      key: 'zeroDropCount',
      label: '零值干扰',
      value: summary.zeroDropCount,
      tone: summary.zeroDropCount > 0 ? 'destructive' : 'neutral',
    },
    {
      key: 'spikeCount',
      label: '单步飞值',
      value: summary.spikeCount,
      tone: summary.spikeCount > 0 ? 'destructive' : 'neutral',
    },
    {
      key: 'resetCount',
      label: '换表归零',
      value: summary.resetCount,
      tone: summary.resetCount > 0 ? 'warning' : 'neutral',
    },
    {
      key: 'rolloverCount',
      label: '满量程翻转',
      value: summary.rolloverCount,
      tone: summary.rolloverCount > 0 ? 'warning' : 'neutral',
    },
  ];
}
