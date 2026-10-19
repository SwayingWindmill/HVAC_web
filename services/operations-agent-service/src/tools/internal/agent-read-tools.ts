import { createHash } from 'node:crypto';

import {
  AgentToolError,
  type AgentRunContext,
  type AgentRunSite,
  type AgentTool,
  type AgentToolErrorCode,
  type AgentToolExecutionRequest,
} from '../../agent/index.js';
import {
  OwnerReadError,
  type AuthorizationDecision,
  type DeviceTelemetryReadRequest,
  type DeviceTelemetryReader,
  type EnergyAnalyticsReadRequest,
  type EnergyAnalyticsReader,
  type EnergyCost,
  type OwnerReadContext,
  type OwnerReadErrorCode,
  type OwnerReadResult,
  type RegistryReadRequest,
  type SemanticQueryReadRequest,
  type SemanticQueryReader,
  type SemanticQueryResult,
  type TelemetryHistoryAggregate,
  type TelemetryHistoryReadRequest,
  type TelemetryHistoryReader,
  type ToolAuthorizableReadRequest,
  type RegistryReader,
  type ToolAuthorizationReader,
  type AlarmListReadRequest,
  type AlarmListResult,
  type AlarmReader,
  type FddFindingsReadRequest,
  type FddFindingsResult,
  type FddReader,
  type WorkOrderListReadRequest,
  type WorkOrderListResult,
  type WorkOrderReader,
} from '../../application/index.js';
import type { DeviceObservationSnapshotDto } from './device-telemetry-owner-reader.js';
import type { EnergySeriesResponseDto } from './energy-analytics-owner-reader.js';
import type { RegistryOwnerPayload } from './registry-owner-reader.js';
import { energyComparisonChart, energySeriesChart, equipmentEnergyChart } from './read-tool-charts.js';
import { semanticCatalog } from './semantic-catalog.gen.js';

export const HVAC_READ_TOOL_NAMES = Object.freeze([
  'assets_list',
  'equipment_list',
  'telemetry_get_current',
  'telemetry_query_history',
  'energy_query_series',
  'energy_compare_periods',
  'energy_by_equipment',
  'energy_semantic_query',
  'alarm_list',
  'fdd_findings',
  'work_order_list',
] as const);

export type HvacReadToolName = typeof HVAC_READ_TOOL_NAMES[number];

export interface HvacReadToolLimits {
  readonly maxAssets: number;
  readonly maxEnergyPoints: number;
  readonly maxEnergyRangeMs: number;
  /** Rows one semantic query may return (the Run's maxToolResultRecords). */
  readonly maxResultRecords: number;
  readonly maxResultBytes: number;
  readonly timeoutMs: number;
}

export interface CreateHvacReadToolsInput {
  readonly capabilities: readonly string[];
  readonly authorization: AuthorizationDecision;
  readonly toolAuthorizationReader: ToolAuthorizationReader;
  readonly registryReader: RegistryReader;
  readonly deviceTelemetryReader: DeviceTelemetryReader;
  readonly telemetryHistoryReader: TelemetryHistoryReader;
  readonly energyAnalyticsReader: EnergyAnalyticsReader;
  readonly semanticQueryReader: SemanticQueryReader;
  readonly alarmReader: AlarmReader;
  readonly fddReader: FddReader;
  readonly workOrderReader: WorkOrderReader;
  readonly limits?: Partial<HvacReadToolLimits>;
}

const DEFAULT_LIMITS: HvacReadToolLimits = Object.freeze({
  maxAssets: 200,
  maxEnergyPoints: 1_000,
  maxEnergyRangeMs: 31 * 24 * 60 * 60 * 1_000,
  maxResultRecords: 2_000,
  maxResultBytes: 64 * 1_024,
  timeoutMs: 8_000,
});

const SITE_CAPABILITIES = Object.freeze(['site.read']);
const ASSET_CAPABILITIES = Object.freeze(['site.read', 'asset.list']);
const EQUIPMENT_CAPABILITIES = Object.freeze(['site.read', 'asset-model.read']);
const TELEMETRY_CAPABILITIES = Object.freeze(['site.read', 'telemetry.snapshot.read']);
const HISTORY_CAPABILITIES = Object.freeze(['site.read', 'telemetry.history.read']);
const ENERGY_CAPABILITIES = Object.freeze(['site.read', 'analytics.energy-series.read']);
const EQUIPMENT_ENERGY_CAPABILITIES = Object.freeze(['site.read', 'asset-model.read', 'analytics.energy-series.read']);
const ALARM_CAPABILITIES = Object.freeze(['site.read', 'alarm.read']);
const FDD_CAPABILITIES = Object.freeze(['site.read', 'fdd.read']);
const WORK_ORDER_CAPABILITIES = Object.freeze(['site.read', 'work-order.read']);
const FDD_LIST_LIMIT = 50;
const WORK_ORDER_LIST_LIMIT = 100;
const ALARM_LIST_LIMIT = 50;

const emptyInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({}),
  additionalProperties: false,
});

const alarmInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    status: Object.freeze({ type: 'string', enum: Object.freeze(['active', 'cleared']), description: 'active（默认）是正在发生的告警，cleared 是最近已恢复的。' }),
  }),
  additionalProperties: false,
});

const telemetryInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    deviceId: Object.freeze({
      type: 'string',
      pattern: '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
    }),
    pointKeys: Object.freeze({
      type: 'array',
      minItems: 1,
      maxItems: 16,
      uniqueItems: true,
      items: Object.freeze({ type: 'string', pattern: '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$' }),
    }),
  }),
  required: Object.freeze(['deviceId', 'pointKeys']),
  additionalProperties: false,
});

// Site-local calendar dates, both ends included: 「10月13日到16日」 is 2026-10-13 to 2026-10-16.
// The Tool turns them into the Site's whole days, so the model never converts to UTC.
const siteDateSchema = (description: string) => Object.freeze({ type: 'string', pattern: '^\\d{4}-\\d{2}-\\d{2}$', description });
const START_DATE = siteDateSchema('First day, Site-local, YYYY-MM-DD.');
const END_DATE = siteDateSchema('Last day, Site-local, YYYY-MM-DD, included.');

const historyInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    deviceId: Object.freeze({ type: 'string', minLength: 36, maxLength: 36 }),
    pointKeys: Object.freeze({ type: 'array', minItems: 1, maxItems: 8, uniqueItems: true, items: Object.freeze({ type: 'string', minLength: 1, maxLength: 128 }) }),
    startDate: START_DATE,
    endDate: END_DATE,
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['quarter_hour', 'hour', 'day']) }),
  }),
  required: Object.freeze(['deviceId', 'pointKeys', 'startDate', 'endDate']),
  additionalProperties: false,
});

const energyTypeSchema = Object.freeze({
  type: 'string',
  enum: Object.freeze(['electricity', 'cooling']),
  description: 'electricity（默认）是用电量，cooling 是供冷量，都以 kWh 计。能效 = 供冷量 ÷ 用电量。',
});

const energyWindowInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    startDate: START_DATE,
    endDate: END_DATE,
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    qualityPolicy: Object.freeze({ type: 'string', enum: Object.freeze(['VALID_ONLY', 'VALID_AND_SUSPECT']) }),
  }),
  required: Object.freeze(['startDate', 'endDate']),
  additionalProperties: false,
});

const energyQueryInputSchema = Object.freeze({
  ...energyWindowInputSchema,
  properties: Object.freeze({ ...energyWindowInputSchema.properties, energyType: energyTypeSchema }),
});

const energyCompareInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    baselineStartDate: START_DATE,
    baselineEndDate: END_DATE,
    currentStartDate: START_DATE,
    currentEndDate: END_DATE,
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    qualityPolicy: Object.freeze({ type: 'string', enum: Object.freeze(['VALID_ONLY', 'VALID_AND_SUSPECT']) }),
    energyType: energyTypeSchema,
  }),
  required: Object.freeze(['baselineStartDate', 'baselineEndDate', 'currentStartDate', 'currentEndDate']),
  additionalProperties: false,
});

// The query service's own ceilings (analyticsmodel), which it checks again: members per query and
// time buckets; the Run's budget may be lower.
const SEMANTIC_MEASURE_LIMIT = 4;
const SEMANTIC_DIMENSION_LIMIT = 2;
const SEMANTIC_BUCKET_LIMIT = 744;

// Every catalog member as `view.member`, with the view's and the member's Chinese meaning, so the
// model can only name what the semantic views publish (#573).
const semanticMemberSchema = (kind: 'measures' | 'dimensions') => Object.freeze({
  anyOf: Object.freeze(semanticCatalog.views.flatMap((view) => view[kind].map((member) => Object.freeze({
    const: `${view.name}.${member.name}`,
    description: `${view.title} · ${member.title}：${member.description}`,
  })))),
});

const semanticQueryInputSchema = Object.freeze({
  type: 'object',
  properties: Object.freeze({
    measures: Object.freeze({
      type: 'array', items: semanticMemberSchema('measures'), minItems: 1, maxItems: SEMANTIC_MEASURE_LIMIT,
      description: '一到四个指标，都来自同一个视图。',
    }),
    dimensions: Object.freeze({
      type: 'array', items: semanticMemberSchema('dimensions'), maxItems: SEMANTIC_DIMENSION_LIMIT,
      description: '最多两个分组，和指标来自同一个视图；每个分组按取值各占一行。',
    }),
    granularity: Object.freeze({ type: 'string', enum: Object.freeze(['hour', 'day', 'month']) }),
    startDate: START_DATE,
    endDate: END_DATE,
  }),
  required: Object.freeze(['measures', 'granularity', 'startDate', 'endDate']),
  additionalProperties: false,
});

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const hasOnlyKeys = (value: Record<string, unknown>, allowed: readonly string[]): boolean => (
  Object.keys(value).every((key) => allowed.includes(key))
);

// A refused argument or a result too large to hand over is the Tool's own wording, so the model is
// told what to change; every other failure reaches it as a bare code.
const fail = (code: AgentToolErrorCode, message: string = code): never => {
  throw new AgentToolError(code, message, code === 'TOOL_ARGUMENTS_INVALID' || code === 'TOOL_RESULT_TOO_LARGE' ? message : undefined);
};
const RESULT_TOO_LARGE = '结果太大：缩短日期范围或改用更粗的粒度。';

const requireRecord = (value: unknown, message: string): Record<string, unknown> => {
  if (!isRecord(value)) fail('TOOL_ARGUMENTS_INVALID', message);
  return value as Record<string, unknown>;
};

const requireString = (value: unknown, message: string): string => (
  typeof value === 'string' ? value : fail('TOOL_ARGUMENTS_INVALID', message)
);

const normalizeLimits = (input: Partial<HvacReadToolLimits> | undefined): HvacReadToolLimits => {
  const limits = { ...DEFAULT_LIMITS, ...input };
  for (const [name, value] of Object.entries(limits)) {
    if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`Invalid Tool runtime limit: ${name}.`);
  }
  return Object.freeze(limits);
};

const hasCapabilities = (available: readonly string[], required: readonly string[]): boolean => (
  required.every((capability) => available.includes(capability))
);

const assertExecutionCapability = (
  context: AgentRunContext,
  required: readonly string[],
): void => {
  if (!hasCapabilities(context.capabilities, required)) {
    fail('TOOL_UNAUTHORIZED', 'The current principal cannot execute this Tool.');
  }
};

const exactEmptyArguments = (value: unknown): void => {
  if (!isRecord(value) || Object.keys(value).length !== 0) {
    fail('TOOL_ARGUMENTS_INVALID', '这个工具不接受参数，站点由服务端确定。');
  }
};

/** Site-local days, both ends included, already checked against the Run's range budget. */
interface SiteDays {
  readonly startDate: string;
  readonly endDate: string;
  readonly days: number;
  /** The first day as a UTC calendar day, for counting forward. */
  readonly firstDay: number;
}

const DATE_REFUSAL = '日期参数有误：startDate 和 endDate 用站点本地日期 YYYY-MM-DD，首尾都包含。';

const DAY_MS = 86_400_000;

const calendarDay = (value: unknown): number => {
  const text = requireString(value, DATE_REFUSAL);
  const match = /^(\d{4})-(\d{2})-(\d{2})$/u.exec(text);
  const day = match === null ? Number.NaN : Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  if (!Number.isFinite(day) || new Date(day).toISOString().slice(0, 10) !== text) fail('TOOL_ARGUMENTS_INVALID', DATE_REFUSAL);
  return day;
};

const parseSiteDays = (start: unknown, end: unknown, limits: HvacReadToolLimits): SiteDays => {
  const firstDay = calendarDay(start);
  const days = (calendarDay(end) - firstDay) / DAY_MS + 1;
  if (days < 1) fail('TOOL_ARGUMENTS_INVALID', `${DATE_REFUSAL}最后一天早于第一天。`);
  if (days * DAY_MS > limits.maxEnergyRangeMs) {
    fail('TOOL_ARGUMENTS_INVALID', `${DATE_REFUSAL}每次最多读 ${limits.maxEnergyRangeMs / DAY_MS} 天。`);
  }
  return Object.freeze({ startDate: String(start), endDate: String(end), days, firstDay });
};

/** An instant's date and clock in the Site's zone. */
const siteParts = (instant: number, timezone: string): Record<string, string> => Object.fromEntries(new Intl.DateTimeFormat('en-CA', {
  timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
}).formatToParts(new Date(instant)).map((part) => [part.type, part.value]));

const MINUTE_MS = 60_000;

// The first minute of a Site-local day: midnight, or the first minute after it on a day the clocks
// skip midnight. Every zone's midnight lies within 15 hours of UTC midnight.
const startOfSiteDay = (day: number, timezone: string): number => {
  const date = new Date(day).toISOString().slice(0, 10);
  let before = day - 15 * 60 * MINUTE_MS;
  let atOrAfter = day + 15 * 60 * MINUTE_MS;
  while (atOrAfter - before > MINUTE_MS) {
    const middle = before + Math.floor((atOrAfter - before) / 2 / MINUTE_MS) * MINUTE_MS;
    const parts = siteParts(middle, timezone);
    if (`${parts.year}-${parts.month}-${parts.day}` >= date) atOrAfter = middle;
    else before = middle;
  }
  return atOrAfter;
};

/** The Site's whole days as the owner's UTC range: from the first day's start to the start of the day after the last. */
const siteRange = (days: SiteDays, timezone: string): { readonly from: string; readonly to: string } => ({
  from: new Date(startOfSiteDay(days.firstDay, timezone)).toISOString(),
  to: new Date(startOfSiteDay(days.firstDay + days.days * DAY_MS, timezone)).toISOString(),
});

interface TelemetryArguments {
  readonly deviceId: string;
  readonly pointKeys: readonly string[];
}

const validUuidV7 = (value: string): boolean => (
  value.length === 36
  && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/u.test(value)
);

const validTelemetryKey = (value: string): boolean => (
  value.length >= 1
  && value.length <= 128
  && /^[A-Za-z][A-Za-z0-9_.:-]*/u.exec(value)?.[0] === value
);

const DEVICE_REFUSAL = '设备 ID 或点位名有误：先用 equipment_list 查到设备 ID 和点位 key。';

const parseTelemetryArguments = (value: unknown): TelemetryArguments => {
  const record = requireRecord(value, DEVICE_REFUSAL);
  if (!hasOnlyKeys(record, ['deviceId', 'pointKeys'])) fail('TOOL_ARGUMENTS_INVALID', DEVICE_REFUSAL);
  const deviceId = requireString(record.deviceId, DEVICE_REFUSAL);
  if (!validUuidV7(deviceId)
    || !Array.isArray(record.pointKeys)
    || record.pointKeys.length < 1
    || record.pointKeys.length > 16
    || record.pointKeys.some((key) => typeof key !== 'string' || !validTelemetryKey(key))
    || new Set(record.pointKeys).size !== record.pointKeys.length) {
    fail('TOOL_ARGUMENTS_INVALID', DEVICE_REFUSAL);
  }
  return Object.freeze({ deviceId, pointKeys: Object.freeze((record.pointKeys as string[]).slice()) });
};

interface HistoryArguments extends SiteDays {
  readonly deviceId: string;
  readonly pointKeys: readonly string[];
  readonly granularity: TelemetryHistoryReadRequest['input']['granularity'];
}

const HISTORY_GRANULARITIES = Object.freeze({ quarter_hour: 'QUARTER_HOUR', hour: 'HOUR', day: 'DAY' } as const);
const HISTORY_BUCKETS_PER_DAY = Object.freeze({ QUARTER_HOUR: 96, HOUR: 24, DAY: 1 } as const);

const parseHistoryArguments = (value: unknown, limits: HvacReadToolLimits): HistoryArguments => {
  const record = requireRecord(value, '历史读数参数有误：需要 deviceId、pointKeys、startDate、endDate，粒度只能是 quarter_hour、hour 或 day。');
  if (!hasOnlyKeys(record, ['deviceId', 'pointKeys', 'startDate', 'endDate', 'granularity'])
    || (record.granularity !== undefined && !Object.hasOwn(HISTORY_GRANULARITIES, String(record.granularity)))) {
    fail('TOOL_ARGUMENTS_INVALID', '历史读数参数有误：需要 deviceId、pointKeys、startDate、endDate，粒度只能是 quarter_hour、hour 或 day。');
  }
  const { deviceId, pointKeys } = parseTelemetryArguments({ deviceId: record.deviceId, pointKeys: record.pointKeys });
  if (pointKeys.length > 8) fail('TOOL_ARGUMENTS_INVALID', '一次最多读 8 个点位。');
  const days = parseSiteDays(record.startDate, record.endDate, limits);
  const granularity = HISTORY_GRANULARITIES[(record.granularity ?? 'hour') as keyof typeof HISTORY_GRANULARITIES];
  // One bucket per point per period; past the result's bound the model should narrow the range or coarsen.
  if (days.days * HISTORY_BUCKETS_PER_DAY[granularity] * pointKeys.length > limits.maxEnergyPoints) {
    fail('TOOL_ARGUMENTS_INVALID', '时间段太多：缩短日期范围、少读几个点位，或改用更粗的粒度。');
  }
  return Object.freeze({ deviceId, pointKeys, ...days, granularity });
};

interface EnergyArguments extends SiteDays {
  readonly energyType: 'electricity' | 'cooling';
  readonly granularity: 'hour' | 'day' | 'month';
  readonly qualityPolicy: 'VALID_ONLY' | 'VALID_AND_SUSPECT';
}

const ALL_ENERGY_TYPES: readonly EnergyArguments['energyType'][] = ['electricity', 'cooling'];

const parseEnergyWindow = (
  value: unknown,
  limits: HvacReadToolLimits,
  energyTypes: readonly EnergyArguments['energyType'][] = ['electricity'],
): EnergyArguments => {
  const record = requireRecord(value, '能耗参数有误：日期用 startDate 和 endDate，粒度只能是 hour、day 或 month。');
  if (!hasOnlyKeys(record, energyTypes.length > 1
    ? ['startDate', 'endDate', 'granularity', 'qualityPolicy', 'energyType']
    : ['startDate', 'endDate', 'granularity', 'qualityPolicy'])
    || (record.energyType !== undefined && !energyTypes.includes(record.energyType as EnergyArguments['energyType']))
    || (record.granularity !== undefined && !['hour', 'day', 'month'].includes(String(record.granularity)))
    || (record.qualityPolicy !== undefined
      && !['VALID_ONLY', 'VALID_AND_SUSPECT'].includes(String(record.qualityPolicy)))) {
    fail('TOOL_ARGUMENTS_INVALID', '能耗参数有误：日期用 startDate 和 endDate，粒度只能是 hour、day 或 month。');
  }
  const days = parseSiteDays(record.startDate, record.endDate, limits);
  return Object.freeze({
    energyType: (record.energyType ?? 'electricity') as EnergyArguments['energyType'],
    ...days,
    granularity: (record.granularity ?? 'hour') as EnergyArguments['granularity'],
    qualityPolicy: (record.qualityPolicy ?? 'VALID_ONLY') as EnergyArguments['qualityPolicy'],
  });
};

/** The part of a semantic query asked of one view. */
interface SemanticViewQuery {
  readonly view: typeof semanticCatalog.views[number];
  readonly measures: readonly string[];
  readonly dimensions: readonly string[];
}

interface SemanticQueryArguments extends SiteDays {
  /** One view, or the Site's energy and its estimated cost read side by side without grouping. */
  readonly parts: readonly SemanticViewQuery[];
  readonly granularity: 'hour' | 'day' | 'month';
}

// The two views a question about the Site often needs at once (「用电和电费」): same Site, same
// periods, so the Tool reads each and lines them up by period instead of refusing the model (#593).
const SIDE_BY_SIDE_VIEWS: readonly string[] = Object.freeze(['site_energy', 'site_energy_cost']);

const semanticBuckets = (days: number, granularity: SemanticQueryArguments['granularity']): number => (
  granularity === 'hour' ? days * 24 : granularity === 'day' ? days : Math.ceil(days / 28) + 1
);

// Refuses, before any read, members outside the catalog, views that cannot be read together and
// ranges beyond the Run's budget. The owner checks the catalog again and forces the Site.
const parseSemanticQuery = (value: unknown, limits: HvacReadToolLimits): SemanticQueryArguments => {
  const invalid = '语义查询参数有误：需要 measures、granularity、startDate、endDate，dimensions 可选。';
  const record = requireRecord(value, invalid);
  const named = (list: unknown, max: number): string[][] => {
    if (!Array.isArray(list) || list.length > max) fail('TOOL_ARGUMENTS_INVALID', invalid);
    return (list as unknown[]).map((member) => requireString(member, invalid).split('.'));
  };
  if (!hasOnlyKeys(record, ['measures', 'dimensions', 'granularity', 'startDate', 'endDate'])
    || !['hour', 'day', 'month'].includes(String(record.granularity))) {
    fail('TOOL_ARGUMENTS_INVALID', invalid);
  }
  const measureParts = named(record.measures, SEMANTIC_MEASURE_LIMIT);
  const dimensionParts = record.dimensions === undefined ? [] : named(record.dimensions, SEMANTIC_DIMENSION_LIMIT);
  const all = [...measureParts, ...dimensionParts];
  if (measureParts.length === 0 || all.some((parts) => parts.length !== 2)
    || new Set(all.map((parts) => parts.join('.'))).size !== all.length) {
    fail('TOOL_ARGUMENTS_INVALID', '指标和分组写成「视图.名字」，每个只写一次。');
  }
  const viewNames = [...new Set(measureParts.map((parts) => parts[0] ?? ''))];
  if (viewNames.length > 1 && (dimensionParts.length > 0 || !viewNames.every((name) => SIDE_BY_SIDE_VIEWS.includes(name)))) {
    fail('TOOL_ARGUMENTS_INVALID', '指标和分组要来自同一个视图；只有不分组时，站点能耗和估算电费的指标可以一起查。设备能耗或要分组时，分开各查一次。');
  }
  const parts = viewNames.map((viewName): SemanticViewQuery => {
    const view = semanticCatalog.views.find(({ name }) => name === viewName);
    const measures = measureParts.filter((parts) => parts[0] === viewName).map((parts) => parts[1] ?? '');
    const dimensions = dimensionParts.map((parts) => (
      parts[0] === viewName ? parts[1] ?? '' : fail('TOOL_ARGUMENTS_INVALID', '分组要和指标来自同一个视图。')
    ));
    if (view === undefined
      || !measures.every((name) => view.measures.some((member) => member.name === name))
      || !dimensions.every((name) => view.dimensions.some((member) => member.name === name))) {
      return fail('TOOL_ARGUMENTS_INVALID', '有指标或分组不在这个视图的清单里，只能用工具说明里列出的写法。');
    }
    return Object.freeze({ view, measures, dimensions });
  });
  const granularity = record.granularity as SemanticQueryArguments['granularity'];
  const days = parseSiteDays(record.startDate, record.endDate, limits);
  if (semanticBuckets(days.days, granularity) > Math.min(SEMANTIC_BUCKET_LIMIT, limits.maxEnergyPoints)) {
    fail('TOOL_ARGUMENTS_INVALID', '时间段太多：缩短日期范围或改用更粗的粒度。');
  }
  return Object.freeze({ parts, granularity, ...days });
};

interface EnergyComparisonArguments {
  readonly baseline: EnergyArguments;
  readonly current: EnergyArguments;
}

const parseEnergyComparison = (
  value: unknown,
  limits: HvacReadToolLimits,
): EnergyComparisonArguments => {
  const record = requireRecord(value, '对比参数有误：两段时间分别用 baselineStartDate、baselineEndDate 和 currentStartDate、currentEndDate。');
  if (!hasOnlyKeys(record, [
    'baselineStartDate',
    'baselineEndDate',
    'currentStartDate',
    'currentEndDate',
    'granularity',
    'qualityPolicy',
    'energyType',
  ])) {
    fail('TOOL_ARGUMENTS_INVALID', '对比参数有误：两段时间分别用 baselineStartDate、baselineEndDate 和 currentStartDate、currentEndDate。');
  }
  const shared = {
    ...(record.granularity === undefined ? {} : { granularity: record.granularity }),
    ...(record.qualityPolicy === undefined ? {} : { qualityPolicy: record.qualityPolicy }),
    ...(record.energyType === undefined ? {} : { energyType: record.energyType }),
  };
  return Object.freeze({
    baseline: parseEnergyWindow({ startDate: record.baselineStartDate, endDate: record.baselineEndDate, ...shared }, limits, ALL_ENERGY_TYPES),
    current: parseEnergyWindow({ startDate: record.currentStartDate, endDate: record.currentEndDate, ...shared }, limits, ALL_ENERGY_TYPES),
  });
};

const ownerErrorCode = (code: OwnerReadErrorCode): AgentToolErrorCode => {
  switch (code) {
    case 'OWNER_REQUEST_INVALID':
      return 'TOOL_OWNER_REQUEST_REJECTED';
    case 'OWNER_RESOURCE_NOT_FOUND':
      return 'TOOL_OWNER_RESOURCE_NOT_FOUND';
    case 'OWNER_READ_TIMEOUT':
      return 'TOOL_OWNER_TIMEOUT';
    case 'OWNER_READ_UNAVAILABLE':
      return 'TOOL_OWNER_UNAVAILABLE';
    case 'OWNER_RESPONSE_TOO_LARGE':
      return 'TOOL_RESULT_TOO_LARGE';
    case 'OWNER_RESPONSE_INVALID':
      return 'TOOL_OWNER_RESPONSE_INVALID';
  }
};

const normalizeToolFailure = (error: unknown): AgentToolError => {
  if (error instanceof AgentToolError) return error;
  if (error instanceof OwnerReadError) {
    const code = ownerErrorCode(error.code);
    return new AgentToolError(code, code);
  }
  return new AgentToolError('TOOL_EXECUTION_FAILED');
};

const runBounded = async <T>(
  signal: AbortSignal,
  timeoutMs: number,
  operation: () => Promise<T>,
): Promise<T> => {
  if (signal.aborted) fail('TOOL_CANCELLED');
  let timeoutHandle: ReturnType<typeof setTimeout> | undefined;
  let abortHandler: (() => void) | undefined;
  const timeout = new Promise<never>((_resolve, reject) => {
    timeoutHandle = setTimeout(() => reject(new AgentToolError('TOOL_TIMEOUT')), timeoutMs);
  });
  const aborted = new Promise<never>((_resolve, reject) => {
    abortHandler = () => reject(new AgentToolError('TOOL_CANCELLED'));
    signal.addEventListener('abort', abortHandler, { once: true });
  });
  try {
    return await Promise.race([operation(), timeout, aborted]);
  } catch (error) {
    throw normalizeToolFailure(error);
  } finally {
    if (timeoutHandle !== undefined) clearTimeout(timeoutHandle);
    if (abortHandler !== undefined) signal.removeEventListener('abort', abortHandler);
  }
};

const requestIdFor = (
  context: AgentRunContext,
  reader: HvacReadToolName | 'site',
  ownerRequest: Omit<ToolAuthorizableReadRequest, 'requestId'>,
): string => {
  const digest = createHash('sha256')
    .update(JSON.stringify([context.runId, reader, ownerRequest]))
    .digest('hex');
  return `agent-tool:${digest}`;
};

const baseOwnerContext = (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
): OwnerReadContext => {
  if (authorization.decision !== 'ALLOW'
    || authorization.delegationGrant === undefined
    || authorization.delegationGrant.trim().length === 0) {
    fail('TOOL_UNAUTHORIZED', 'The server authorization context is incomplete.');
  }
  return {
    sessionId: context.sessionId,
    runId: context.runId,
    scope: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      assetId: null,
      deviceId: null,
    },
    authorization,
    correlationId: context.correlationId,
  };
};

const authorizeOwnerRead = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  reader: ToolAuthorizationReader,
  request: ToolAuthorizableReadRequest,
): Promise<OwnerReadContext> => {
  const base = baseOwnerContext(context, authorization);
  const scoped = request.tool === 'telemetry.current.getDeviceObservationSnapshot' || request.tool === 'telemetry.history.aggregateDeviceHistory'
    ? {
      ...base,
      scope: { ...base.scope, deviceId: request.input.deviceId },
    }
    : base;
  const grant = await reader.authorize({ request, context: scoped });
  if (grant.delegationGrant.trim().length === 0) {
    fail('TOOL_UNAUTHORIZED', 'Owner authorization returned an empty grant.');
  }
  return {
    ...scoped,
    authorization: {
      ...authorization,
      delegationGrant: grant.delegationGrant,
      toolDelegationGrants: {
        ...authorization.toolDelegationGrants,
        [request.tool]: grant.delegationGrant,
      },
      ...(grant.policyRevision === undefined ? {} : { policyRevision: grant.policyRevision }),
    },
  };
};

const assertResultBytes = <T>(result: T, limits: HvacReadToolLimits): T => {
  if (Buffer.byteLength(JSON.stringify(result), 'utf8') > limits.maxResultBytes) {
    fail('TOOL_RESULT_TOO_LARGE', '结果太大：缩短日期范围、改用更粗的粒度或少读几项。');
  }
  return result;
};

const sitePayloadFrom = (result: OwnerReadResult): Extract<RegistryOwnerPayload, { kind: 'SITE' }> => {
  const payload = result.payload as RegistryOwnerPayload;
  if (payload.kind !== 'SITE') fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload as Extract<RegistryOwnerPayload, { kind: 'SITE' }>;
};

const assetsPayloadFrom = (
  result: OwnerReadResult,
): Extract<RegistryOwnerPayload, { kind: 'SITE_ASSETS' }> => {
  const payload = result.payload as RegistryOwnerPayload;
  if (payload.kind !== 'SITE_ASSETS') fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload as Extract<RegistryOwnerPayload, { kind: 'SITE_ASSETS' }>;
};

const assetModelPayloadFrom = (
  result: OwnerReadResult,
): Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }> => {
  const payload = result.payload as RegistryOwnerPayload;
  if (payload.kind !== 'SITE_ASSET_MODEL') fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload as Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }>;
};

const telemetryPayloadFrom = (result: OwnerReadResult): DeviceObservationSnapshotDto => {
  const payload = result.payload as DeviceObservationSnapshotDto;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.values)) {
    fail('TOOL_OWNER_RESPONSE_INVALID');
  }
  return payload;
};

const energyPayloadFrom = (result: OwnerReadResult): EnergySeriesResponseDto => {
  const payload = result.payload as EnergySeriesResponseDto;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.points) || !isRecord(payload.metadata)) {
    fail('TOOL_OWNER_RESPONSE_INVALID');
  }
  return payload;
};

/** An instant as the Site reads it, "2026-10-15 18:00", so the model never mistakes UTC hours for local ones. */
const siteTime = (instant: string, timezone: string): string => {
  const parts = siteParts(Date.parse(instant), timezone);
  return `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}`;
};

// What the model reads is what an operator would read: Chinese values, Site-local times, no owner
// names, revisions, quality counts or internal enums (#590).
// Grouping values as the pages show them: energy type, data quality and Tariff period in words.
const DIMENSION_WORDS = Object.freeze({
  energy_type: Object.freeze({ electricity: '用电', cooling: '供冷' }),
  quality: Object.freeze({ VALID: '有效', SUSPECT: '可疑', INVALID: '无效' }),
  tariff_period: Object.freeze({ SUPER_PEAK: '尖峰', PEAK: '峰', FLAT: '平', VALLEY: '谷' }),
}) as Readonly<Record<string, Readonly<Record<string, string>>>>;
const ENERGY_WORDS = Object.freeze({ electricity: '用电', cooling: '供冷' } as const) satisfies typeof DIMENSION_WORDS[string];
const ENERGY_GRANULARITY_WORDS = Object.freeze({ hour: '按小时', day: '按日', month: '按月' } as const);
const HISTORY_GRANULARITY_WORDS = Object.freeze({ QUARTER_HOUR: '按15分钟', HOUR: '按小时', DAY: '按日' } as const);
const QUALITY_POLICY_WORDS = Object.freeze({ VALID_ONLY: '只算有效读数', VALID_AND_SUSPECT: '含可疑读数' } as const);
const POINT_KIND_WORDS: Readonly<Record<string, string>> = Object.freeze({ TELEMETRY: '读数', COUNTER: '累计量', STATE: '状态' });
const pointKind = (pointType: string): string => POINT_KIND_WORDS[pointType] ?? '读数';
// Units as the device page writes them (formatTelemetryUnit in the Web): Registry stores UCUM codes.
const UNIT_WORDS: Readonly<Record<string, string>> = Object.freeze({ Cel: '°C', 'm3/h': 'm³/h' });
const unitWords = <T extends string | null | undefined>(unit: T): T | string => (unit ? UNIT_WORDS[unit] ?? unit : unit);

/** 「完整」, or that it is not and how far the data reaches. */
const dataStatus = (partial: boolean, watermark: string | null | undefined, timezone: string): string => {
  if (!partial) return '完整';
  return watermark == null ? '不完整' : `不完整：数据截至 ${siteTime(watermark, timezone)}`;
};

type SitePointInput = { readonly periodStart: string; readonly energyKWh: number; readonly cost?: EnergyCost };

/** Each point at Site-local time; electricity also carries the owner's estimated cost and the energy it could not price. */
const sitePoints = (points: readonly SitePointInput[], timezone: string) => (
  points.map((point) => ({
    siteTime: siteTime(point.periodStart, timezone),
    energyKWh: point.energyKWh,
    ...(point.cost ? { estimatedCostCNY: point.cost.amountCNY, unpricedKWh: point.cost.unpricedKWh } : {}),
  }))
);

/** The series' estimated energy charge and unpriced energy; cooling has neither. */
const seriesCost = (points: readonly SitePointInput[]) => {
  const costs = points.flatMap((point) => (point.cost ? [point.cost] : []));
  if (costs.length === 0) return {};
  return {
    estimatedCostCNY: Number(costs.reduce((sum, cost) => sum + cost.amountCNY, 0).toFixed(2)),
    unpricedKWh: Number(costs.reduce((sum, cost) => sum + cost.unpricedKWh, 0).toFixed(6)),
  };
};

const historyPayloadFrom = (result: OwnerReadResult): TelemetryHistoryAggregate => {
  const payload = result.payload as TelemetryHistoryAggregate;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.buckets)) fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload;
};

// Equipment states as the device page says them.
const EQUIPMENT_STATE_WORDS: Readonly<Record<string, string>> = Object.freeze({ RUNNING: '运行', STOPPED: '停机', STANDBY: '待机', FAULT: '故障' });
const equipmentStateWord = (value: unknown): unknown => (typeof value === 'string' ? EQUIPMENT_STATE_WORDS[value] ?? value : value);

/** One series per point, in the order asked: averages for readings, increases for counters. */
const readHistory = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  history: TelemetryHistoryReader,
  timezone: string,
  query: HistoryArguments,
) => {
  const draft = {
    tool: 'telemetry.history.aggregateDeviceHistory' as const,
    input: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      deviceId: query.deviceId,
      keys: query.pointKeys,
      ...siteRange(query, timezone),
      granularity: query.granularity,
      timezone,
      qualityPolicy: 'VALID_ONLY' as const,
    },
  };
  const request: TelemetryHistoryReadRequest = { ...draft, requestId: requestIdFor(context, 'telemetry_query_history', draft) };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await history.read({ request, context: ownerContext });
  const payload = historyPayloadFrom(result);
  const series = query.pointKeys.flatMap((key) => {
    const buckets = payload.buckets.filter((bucket) => bucket.telemetryKey === key);
    const first = buckets[0];
    if (first === undefined) return [];
    return [{
      key,
      unit: unitWords(first.unit),
      kind: pointKind(first.pointType),
      points: buckets.map((bucket) => ({
        siteTime: siteTime(bucket.periodStart, timezone),
        ...(bucket.gauge === undefined ? {} : { average: bucket.gauge.average, minimum: bucket.gauge.minimum, maximum: bucket.gauge.maximum }),
        ...(bucket.counter === undefined ? {} : { increase: bucket.counter.deltaSum }),
        ...(bucket.state === undefined ? {} : { lastValue: equipmentStateWord(bucket.state.lastValue) }),
        coverage: bucket.completeness,
      })),
    }];
  });
  const missingKeys = query.pointKeys.filter((key) => !series.some((entry) => entry.key === key));
  // The history owner reader rates a result GOOD only when it is projected past the range and every bucket is whole.
  return {
    query: { startDate: query.startDate, endDate: query.endDate, granularity: HISTORY_GRANULARITY_WORDS[query.granularity] },
    series,
    missingKeys,
    // Without it the model asks again for the same readings (#596).
    ...(missingKeys.length === 0 ? {} : { note: 'missingKeys 里的读数在查的这段时间没有历史数据，同样的时段再查一次结果也一样。' }),
    dataStatus: dataStatus(result.quality !== 'GOOD', payload.projectionWatermark, timezone),
  };
};

const readSite = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  registry: RegistryReader,
): Promise<{ readonly result: OwnerReadResult; readonly payload: Extract<RegistryOwnerPayload, { kind: 'SITE' }> }> => {
  const draft = {
    tool: 'registry.getSite' as const,
    input: { siteId: context.siteId },
  };
  const request: RegistryReadRequest = {
    ...draft,
    requestId: requestIdFor(context, 'site', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await registry.read({ request, context: ownerContext });
  return { result, payload: sitePayloadFrom(result) };
};

const readAssets = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  registry: RegistryReader,
): Promise<{ readonly result: OwnerReadResult; readonly payload: Extract<RegistryOwnerPayload, { kind: 'SITE_ASSETS' }> }> => {
  const draft = {
    tool: 'registry.listSiteAssets' as const,
    input: { siteId: context.siteId },
  };
  const request: RegistryReadRequest = {
    ...draft,
    requestId: requestIdFor(context, 'assets_list', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await registry.read({ request, context: ownerContext });
  return { result, payload: assetsPayloadFrom(result) };
};

const readAssetModel = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  registry: RegistryReader,
): Promise<{ readonly result: OwnerReadResult; readonly model: Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }>['model'] }> => {
  const draft = {
    tool: 'registry.getSiteAssetModel' as const,
    input: { siteId: context.siteId },
  };
  const request: RegistryReadRequest = {
    ...draft,
    requestId: requestIdFor(context, 'equipment_list', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await registry.read({ request, context: ownerContext });
  return { result, model: assetModelPayloadFrom(result).model };
};

/** Each device with the machine it belongs to and its active points, the keys other Tools read by. */
const describeEquipment = (model: Extract<RegistryOwnerPayload, { kind: 'SITE_ASSET_MODEL' }>['model']) => {
  const assetsById = new Map(model.assets.map((asset) => [asset.id, asset]));
  return model.devices.map((device) => {
    const link = model.relationships.find((relationship) => relationship.status === 'ACTIVE'
      && relationship.fromType === 'DEVICE' && relationship.fromId === device.id && relationship.toType === 'ASSET');
    const asset = link === undefined ? undefined : assetsById.get(link.toId);
    return {
      deviceId: device.id,
      name: device.displayName,
      machine: asset === undefined ? null : asset.displayName,
      points: model.telemetryPoints
        .filter((point) => point.reportingDeviceId === device.id && point.status === 'ACTIVE')
        .map((point) => ({ key: point.pointCode, name: point.displayName, unit: unitWords(point.unit), kind: pointKind(point.pointType) })),
    };
  });
};

const DEVICE_STATE_WORDS: Readonly<Record<string, string>> = Object.freeze({
  ONLINE: '在线', OFFLINE: '离线', STALE: '读数过期', UNKNOWN: '状态不明', UNAVAILABLE: '暂时读不到',
});

const valueState = (value: DeviceObservationSnapshotDto['values'][number]): string => {
  if (value.state === 'MISSING') return '没有读数';
  if (value.freshness === 'STALE' || value.quality === 'STALE') return '读数过期';
  if (value.quality === 'INVALID') return '读数无效';
  return value.quality === 'GOOD' ? '正常' : '读数可疑';
};

const readTelemetry = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  telemetry: DeviceTelemetryReader,
  timezone: string,
  query: TelemetryArguments,
) => {
  const draft = {
    tool: 'telemetry.current.getDeviceObservationSnapshot' as const,
    input: {
      siteId: context.siteId,
      deviceId: query.deviceId,
      pointKeys: query.pointKeys,
    },
  };
  const request: DeviceTelemetryReadRequest = {
    ...draft,
    requestId: requestIdFor(context, 'telemetry_get_current', draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await telemetry.read({ request, context: ownerContext });
  const payload = telemetryPayloadFrom(result);
  const complete = payload.evaluationAvailability === 'AVAILABLE'
    && payload.telemetryReadiness === 'CURRENT'
    && payload.values.every((value) => value.state === 'PRESENT');
  return {
    readAt: siteTime(payload.evaluatedAt, timezone),
    deviceState: payload.displayState === null ? '状态不明' : DEVICE_STATE_WORDS[payload.displayState] ?? '状态不明',
    values: payload.values.map((value) => (value.state === 'MISSING'
      ? { key: value.key, state: valueState(value) }
      : {
        key: value.key,
        value: equipmentStateWord(value.value),
        unit: unitWords(value.unit),
        siteTime: siteTime(value.sampledAt, timezone),
        state: valueState(value),
      })),
    dataStatus: complete ? '完整' : '不完整：有读数过期或缺失',
  };
};

const readEnergy = async (
  semanticTool: Extract<HvacReadToolName, 'energy_query_series' | 'energy_compare_periods'>,
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  energy: EnergyAnalyticsReader,
  timezone: string,
  query: EnergyArguments,
  limits: HvacReadToolLimits,
) => {
  const draft = {
    tool: 'analytics.getEnergySeries' as const,
    input: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      energyType: query.energyType,
      granularity: query.granularity,
      timezone,
      ...siteRange(query, timezone),
      qualityPolicy: query.qualityPolicy,
    },
  };
  const request: EnergyAnalyticsReadRequest = {
    ...draft,
    requestId: requestIdFor(context, semanticTool, draft),
  };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const result = await energy.read({ request, context: ownerContext });
  const payload = energyPayloadFrom(result);
  if (payload.points.length > limits.maxEnergyPoints) fail('TOOL_RESULT_TOO_LARGE', RESULT_TOO_LARGE);
  const totalKWh = payload.points.length === 0
    ? null
    : Number(payload.points.reduce((sum, point) => sum + point.energyKWh, 0).toFixed(6));
  return {
    query: {
      energy: ENERGY_WORDS[query.energyType],
      startDate: query.startDate,
      endDate: query.endDate,
      granularity: ENERGY_GRANULARITY_WORDS[query.granularity],
      readings: QUALITY_POLICY_WORDS[query.qualityPolicy],
    },
    points: sitePoints(payload.points, timezone),
    pointCount: payload.points.length,
    totalKWh,
    ...seriesCost(payload.points),
    dataStatus: dataStatus(payload.metadata.partial, payload.metadata.dataWatermark, timezone),
    ...readingNote(payload.metadata.qualitySummary, query.qualityPolicy),
    // Why a comparison cannot use this period, if it cannot; summarizeEnergy leaves it out of what the model reads.
    blocked: payload.metadata.partial ? '数据不完整，不能比较' : result.quality === 'GOOD' ? null : '有读数可疑，不能比较',
  };
};

/** Says so when readings were left out for quality; counts of measuring intervals mean nothing to an operator. */
const readingNote = (summary: EnergySeriesResponseDto['metadata']['qualitySummary'], policy: EnergyArguments['qualityPolicy']) => (
  summary.suspect + summary.invalid === 0 ? {}
    : { readingNote: policy === 'VALID_ONLY' ? '部分读数可疑或无效，没有计入' : '包含部分可疑读数，无效读数没有计入' }
);

const semanticPayloadFrom = (result: OwnerReadResult): SemanticQueryResult => {
  const payload = result.payload as SemanticQueryResult;
  if (payload.schemaVersion !== 1 || !Array.isArray(payload.columns) || !Array.isArray(payload.rows) || !isRecord(payload.metadata)) {
    fail('TOOL_OWNER_RESPONSE_INVALID');
  }
  return payload;
};

// One semantic query; each row comes back keyed by the column's Chinese title, its period at Site time.
const readSemanticView = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  reader: SemanticQueryReader,
  timezone: string,
  query: SemanticQueryArguments,
  part: SemanticViewQuery,
  limits: HvacReadToolLimits,
) => {
  const draft = {
    tool: 'analytics.semanticQuery' as const,
    input: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      view: part.view.name,
      measures: part.measures,
      ...(part.dimensions.length === 0 ? {} : { dimensions: part.dimensions }),
      granularity: query.granularity,
      timezone,
      ...siteRange(query, timezone),
    },
  };
  // A day the clocks go back has 25 hours; the owner counts real buckets.
  if (query.granularity === 'hour' && (Date.parse(draft.input.to) - Date.parse(draft.input.from)) / 3_600_000 > SEMANTIC_BUCKET_LIMIT) {
    fail('TOOL_ARGUMENTS_INVALID', '时间段太多：缩短日期范围或改用更粗的粒度。');
  }
  const request: SemanticQueryReadRequest = { ...draft, requestId: requestIdFor(context, 'energy_semantic_query', draft) };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const payload = semanticPayloadFrom(await reader.read({ request, context: ownerContext }));
  if (payload.rows.length > limits.maxResultRecords) fail('TOOL_RESULT_TOO_LARGE', '结果行数太多：缩短日期范围、改用更粗的粒度或少分一组。');
  return payload;
};

const readSemantic = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  reader: SemanticQueryReader,
  timezone: string,
  query: SemanticQueryArguments,
  limits: HvacReadToolLimits,
  devices: ReadonlyMap<string, string>,
) => {
  const payloads = await Promise.all(query.parts.map((part) => (
    readSemanticView(context, authorization, authorizer, reader, timezone, query, part, limits)
  )));
  const asRows = (payload: typeof payloads[number]) => payload.rows.map((row) => Object.fromEntries(payload.columns.map((column, index) => (
    column.kind === 'time' ? ['siteTime', siteTime(String(row[index]), timezone)]
      : column.kind === 'dimension' ? [column.title, dimensionWords(column.name, row[index] ?? null, devices)]
        : [column.title, row[index]]
  ))));
  const columns = [...new Set(payloads.flatMap((payload) => payload.columns.map(({ title }) => title)))];
  // Views read side by side are not grouped, so each has one row per period: line them up, and a
  // period one view has no row for is empty (null) in that view's columns.
  const byPeriod = new Map<string, Record<string, unknown>>();
  for (const row of payloads.flatMap(asRows)) {
    const period = String(row.siteTime);
    byPeriod.set(period, { ...(byPeriod.get(period) ?? Object.fromEntries(columns.slice(1).map((title) => [title, null]))), ...row });
  }
  const rows = payloads.length === 1
    ? asRows(payloads[0]!)
    : [...byPeriod.values()].sort((a, b) => String(a.siteTime).localeCompare(String(b.siteTime)));
  // The answer reaches only as far as the view that stops first.
  const partial = payloads.filter((payload) => payload.metadata.partial);
  const watermarks = partial.map((payload) => payload.metadata.dataWatermark);
  const reach = watermarks.some((watermark) => watermark == null) ? null : watermarks.sort()[0];
  return {
    view: query.parts.map((part) => part.view.title).join(' + '),
    query: { granularity: ENERGY_GRANULARITY_WORDS[query.granularity], startDate: query.startDate, endDate: query.endDate },
    columns,
    rows,
    rowCount: rows.length,
    dataStatus: dataStatus(partial.length > 0, reach, timezone),
  };
};

// A grouping value as the pages show it; equipment by its device's name.
const dimensionWords = (name: string, value: string | number | null, devices: ReadonlyMap<string, string>): string | null => {
  if (name === 'tariff_period' && (value === null || value === '')) return '未计价';
  if (value === null) return null;
  const text = String(value);
  if (name === 'equipment_id') return devices.get(text) ?? '未登记的设备';
  return DIMENSION_WORDS[name]?.[text] ?? text;
};

const sumKWh = (points: readonly { readonly energyKWh: number }[]): number => (
  Number(points.reduce((sum, point) => sum + point.energyKWh, 0).toFixed(6))
);

// Diagnosis findings and work orders as the 诊断 and 工单 pages name them (#595).
const FINDING_RULES: Readonly<Record<string, { readonly rule: string; readonly measures: string }>> = Object.freeze({
  CHILLED_WATER_LOW_DELTA_T: { rule: '冷冻水低温差', measures: '冷冻水供回水温差' },
});
// How far a finding fell past its threshold, as the 诊断 page grades it.
const deviation = (observed: number, threshold: number): string => {
  const shortfall = Math.abs(threshold - observed) / threshold;
  return shortfall >= 0.3 ? '严重偏离' : shortfall >= 0.1 ? '明显偏离' : '轻微偏离';
};
const WORK_ORDER_PRIORITY_WORDS = Object.freeze({ URGENT: '紧急', HIGH: '高', MEDIUM: '中', LOW: '低' } as const);
const WORK_ORDER_STATUS_WORDS = Object.freeze({ DRAFT: '草稿', OPEN: '待处理', IN_PROGRESS: '处理中', BLOCKED: '受阻', COMPLETED: '已完成', CANCELLED: '已取消' } as const);
const UNFINISHED_WORK_ORDER: ReadonlySet<string> = new Set(['OPEN', 'IN_PROGRESS', 'BLOCKED']);

const readFddFindings = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  fdd: FddReader,
): Promise<FddFindingsResult> => {
  const draft = { tool: 'fdd.listFindings' as const, input: { siteId: context.siteId, limit: FDD_LIST_LIMIT } };
  const request: FddFindingsReadRequest = { ...draft, requestId: requestIdFor(context, 'fdd_findings', draft) };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const payload = (await fdd.read({ request, context: ownerContext })).payload as FddFindingsResult;
  if (!Array.isArray(payload.items)) fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload;
};

const readWorkOrders = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  workOrders: WorkOrderReader,
): Promise<WorkOrderListResult> => {
  const draft = { tool: 'workorder.listWorkOrders' as const, input: { siteId: context.siteId, limit: WORK_ORDER_LIST_LIMIT } };
  const request: WorkOrderListReadRequest = { ...draft, requestId: requestIdFor(context, 'work_order_list', draft) };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  const payload = (await workOrders.read({ request, context: ownerContext })).payload as WorkOrderListResult;
  if (!Array.isArray(payload.items)) fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload;
};

// Alarms as the 告警 page names them (#594).
const SEVERITY_WORDS = Object.freeze({ CRITICAL: '紧急', MAJOR: '重要', MINOR: '次要', WARNING: '预警', INFO: '提示' } as const);
const OPERATION_WORDS = Object.freeze({ PUBLISH: '触发', ACKNOWLEDGE: '确认', ASSIGN: '认领', UNASSIGN: '取消认领', SUPPRESS: '抑制', UNSUPPRESS: '解除抑制', CLEAR: '恢复' } as const);
const ALARM_STATUS = Object.freeze({ active: { condition: 'ACTIVE', words: '正在发生', none: '当前没有正在发生的告警' }, cleared: { condition: 'CLEARED', words: '已恢复', none: '最近没有已恢复的告警' } } as const);
const ALARM_REFUSAL = '告警参数有误：只接受 status，取值 active（正在发生，默认）或 cleared（最近已恢复）。';

const parseAlarmArguments = (value: unknown): keyof typeof ALARM_STATUS => {
  const record = requireRecord(value, ALARM_REFUSAL);
  if (!hasOnlyKeys(record, ['status']) || (record.status !== undefined && record.status !== 'active' && record.status !== 'cleared')) {
    fail('TOOL_ARGUMENTS_INVALID', ALARM_REFUSAL);
  }
  return (record.status ?? 'active') as keyof typeof ALARM_STATUS;
};

const alarmsPayloadFrom = (result: OwnerReadResult): AlarmListResult => {
  const payload = result.payload as AlarmListResult;
  if (!Array.isArray(payload.items)) fail('TOOL_OWNER_RESPONSE_INVALID');
  return payload;
};

const readAlarms = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  alarms: AlarmReader,
  condition: 'ACTIVE' | 'CLEARED',
): Promise<AlarmListResult> => {
  const draft = {
    tool: 'alarm.listAlarms' as const,
    input: { siteId: context.siteId, condition, limit: ALARM_LIST_LIMIT },
  };
  const request: AlarmListReadRequest = { ...draft, requestId: requestIdFor(context, 'alarm_list', draft) };
  const ownerContext = await authorizeOwnerRead(context, authorization, authorizer, request);
  return alarmsPayloadFrom(await alarms.read({ request, context: ownerContext }));
};

/** The Site total split by sub-metered device, largest first, each named from the asset model. */
const readEquipmentEnergy = async (
  context: AgentRunContext,
  authorization: AuthorizationDecision,
  authorizer: ToolAuthorizationReader,
  energy: EnergyAnalyticsReader,
  registry: RegistryReader,
  timezone: string,
  query: EnergyArguments,
  limits: HvacReadToolLimits,
) => {
  const draft = {
    tool: 'analytics.getEnergySeries' as const,
    input: {
      tenantId: context.tenantId,
      siteId: context.siteId,
      energyType: 'electricity' as const,
      granularity: query.granularity,
      timezone,
      ...siteRange(query, timezone),
      qualityPolicy: query.qualityPolicy,
      breakdown: 'device' as const,
    },
  };
  const request: EnergyAnalyticsReadRequest = { ...draft, requestId: requestIdFor(context, 'energy_by_equipment', draft) };
  const [{ model }, result] = await Promise.all([
    readAssetModel(context, authorization, authorizer, registry),
    authorizeOwnerRead(context, authorization, authorizer, request).then((ownerContext) => energy.read({ request, context: ownerContext })),
  ]);
  const payload = energyPayloadFrom(result);
  const deviceSeries = payload.devices ?? [];
  if (deviceSeries.reduce((count, device) => count + device.points.length, payload.points.length) > limits.maxEnergyPoints) {
    fail('TOOL_RESULT_TOO_LARGE', RESULT_TOO_LARGE);
  }
  const equipment = new Map(describeEquipment(model).map((device) => [device.deviceId, device]));
  const siteTotalKWh = payload.points.length === 0 ? null : sumKWh(payload.points);
  const devices = deviceSeries
    .map((device) => {
      const totalKWh = sumKWh(device.points);
      const known = equipment.get(device.deviceId);
      return {
        name: known?.name ?? '未登记的设备',
        machine: known?.machine ?? null,
        totalKWh,
        shareOfSite: siteTotalKWh === null || siteTotalKWh === 0 ? null : Number((totalKWh / siteTotalKWh).toFixed(3)),
        points: sitePoints(device.points, timezone),
      };
    })
    .sort((left, right) => right.totalKWh - left.totalKWh);
  return {
    query: { startDate: query.startDate, endDate: query.endDate, granularity: ENERGY_GRANULARITY_WORDS[query.granularity], readings: QUALITY_POLICY_WORDS[query.qualityPolicy] },
    siteTotalKWh,
    devices,
    dataStatus: dataStatus(payload.metadata.partial, payload.metadata.dataWatermark, timezone),
    ...readingNote(payload.metadata.qualitySummary, query.qualityPolicy),
  };
};

const summarizeEnergy = ({ points: _points, blocked: _blocked, ...visible }: Awaited<ReturnType<typeof readEnergy>>) => visible;

const compareEnergy = (
  baseline: Awaited<ReturnType<typeof readEnergy>>,
  current: Awaited<ReturnType<typeof readEnergy>>,
) => {
  const before = baseline.totalKWh;
  const after = current.totalKWh;
  if (before === null || after === null) return { status: '没有数据，不能比较', absoluteChangeKWh: null, percentChange: null };
  const blocked = baseline.blocked ?? current.blocked;
  if (blocked !== null) return { status: blocked, absoluteChangeKWh: null, percentChange: null };
  const absoluteChangeKWh = Number((after - before).toFixed(6));
  if (before === 0) {
    return { status: '对比期为 0，算不出百分比', absoluteChangeKWh, percentChange: null };
  }
  return {
    status: '可以比较',
    absoluteChangeKWh,
    percentChange: Number(((absoluteChangeKWh / before) * 100).toFixed(6)),
  };
};

const toolDefinition = (
  name: HvacReadToolName,
  description: string,
  inputSchema: Readonly<Record<string, unknown>>,
  requiredCapabilities: readonly string[],
) => Object.freeze({
  name,
  description,
  inputSchema,
  executionMode: 'parallel' as const,
  replayPolicy: 'safe' as const,
  requiredCapabilities,
});

export interface CreateSiteDescriberInput {
  readonly toolAuthorizationReader: ToolAuthorizationReader;
  readonly registryReader: RegistryReader;
}

/** The Site's name and time zone for the prompt, read from Registry under the Run's own authorization. */
export const createSiteDescriber = ({ toolAuthorizationReader, registryReader }: CreateSiteDescriberInput) => (
  async (authorization: AuthorizationDecision, context: AgentRunContext): Promise<AgentRunSite> => {
    assertExecutionCapability(context, SITE_CAPABILITIES);
    const { payload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
    return Object.freeze({ name: payload.site.displayName, timezone: payload.site.timezone });
  }
);

export const createHvacReadTools = ({
  capabilities,
  authorization,
  toolAuthorizationReader,
  registryReader,
  deviceTelemetryReader,
  telemetryHistoryReader,
  energyAnalyticsReader,
  semanticQueryReader,
  alarmReader,
  fddReader,
  workOrderReader,
  limits: inputLimits,
}: CreateHvacReadToolsInput): readonly AgentTool[] => {
  const limits = normalizeLimits(inputLimits);

  const assetsTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'assets_list',
      '列出当前站点登记的资产。站点由服务端确定。',
      emptyInputSchema,
      ASSET_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      exactEmptyArguments(argumentsValue);
      assertExecutionCapability(context, ASSET_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload } = await readAssets(context, authorization, toolAuthorizationReader, registryReader);
        const boundedAssets = payload.assets.slice(0, limits.maxAssets).map((asset) => ({ name: asset.displayName, code: asset.code }));
        return assertResultBytes({
          assets: boundedAssets,
          ...(payload.assets.length > boundedAssets.length ? { note: `只列出前 ${boundedAssets.length} 个，共 ${payload.assets.length} 个` } : {}),
        }, limits);
      });
    },
  });

  const equipmentTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'equipment_list',
      '列出当前站点的设备：名称、所属机组和点位 key。读设备读数时用这里的设备 ID 和点位 key。',
      emptyInputSchema,
      EQUIPMENT_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      exactEmptyArguments(argumentsValue);
      assertExecutionCapability(context, EQUIPMENT_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { model } = await readAssetModel(context, authorization, toolAuthorizationReader, registryReader);
        const devices = describeEquipment(model);
        const bounded = devices.slice(0, limits.maxAssets);
        return assertResultBytes({
          devices: bounded,
          ...(devices.length > bounded.length ? { note: `只列出前 ${bounded.length} 个，共 ${devices.length} 个` } : {}),
        }, limits);
      });
    },
  });

  const telemetryTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'telemetry_get_current',
      '读当前站点一台设备几个点位现在的读数。设备 ID 和点位 key 来自 equipment_list。',
      telemetryInputSchema,
      TELEMETRY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseTelemetryArguments(argumentsValue);
      assertExecutionCapability(context, TELEMETRY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        return assertResultBytes(
          await readTelemetry(context, authorization, toolAuthorizationReader, deviceTelemetryReader, sitePayload.site.timezone, query),
          limits,
        );
      });
    },
  });

  const historyTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'telemetry_query_history',
      '读一台设备最多 8 个点位在一段日期里的变化，按 15 分钟、小时（默认）或天分段。读数给出每段的平均、最低和最高值，累计量给出每段的增量。设备 ID 和点位 key 来自 equipment_list。',
      historyInputSchema,
      HISTORY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseHistoryArguments(argumentsValue, limits);
      assertExecutionCapability(context, HISTORY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        return assertResultBytes(await readHistory(
          context,
          authorization,
          toolAuthorizationReader,
          telemetryHistoryReader,
          sitePayload.site.timezone,
          query,
        ), limits);
      });
    },
  });

  const energyQueryTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy_query_series',
      '读当前站点一段日期的用电量或供冷量，按小时、天或月列出，带合计；用电还带估算电费。',
      energyQueryInputSchema,
      ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseEnergyWindow(argumentsValue, limits, ALL_ENERGY_TYPES);
      assertExecutionCapability(context, ENERGY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        const result = await readEnergy(
          'energy_query_series',
          context,
          authorization,
          toolAuthorizationReader,
          energyAnalyticsReader,
          sitePayload.site.timezone,
          query,
          limits,
        );
        return assertResultBytes(result, limits);
      });
    },
    chart: energySeriesChart,
  });

  const energyCompareTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy_compare_periods',
      '比较当前站点两段日期的用电量或供冷量，给出两段的合计、差值和百分比。',
      energyCompareInputSchema,
      ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseEnergyComparison(argumentsValue, limits);
      assertExecutionCapability(context, ENERGY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        const [baselineSeries, currentSeries] = await Promise.all([
          readEnergy(
            'energy_compare_periods',
            context,
            authorization,
            toolAuthorizationReader,
            energyAnalyticsReader,
            sitePayload.site.timezone,
            query.baseline,
            limits,
          ),
          readEnergy(
            'energy_compare_periods',
            context,
            authorization,
            toolAuthorizationReader,
            energyAnalyticsReader,
            sitePayload.site.timezone,
            query.current,
            limits,
          ),
        ]);
        return assertResultBytes({
          baseline: summarizeEnergy(baselineSeries),
          current: summarizeEnergy(currentSeries),
          comparison: compareEnergy(baselineSeries, currentSeries),
        }, limits);
      });
    },
    chart: energyComparisonChart,
  });

  const equipmentEnergyTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy_by_equipment',
      '把当前站点一段日期的用电量按有分表的设备拆开，从多到少列出，每台设备带名称和按所选粒度的明细，不用先读设备清单。',
      energyWindowInputSchema,
      EQUIPMENT_ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseEnergyWindow(argumentsValue, limits);
      assertExecutionCapability(context, EQUIPMENT_ENERGY_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        return assertResultBytes(await readEquipmentEnergy(
          context,
          authorization,
          toolAuthorizationReader,
          energyAnalyticsReader,
          registryReader,
          sitePayload.site.timezone,
          query,
          limits,
        ), limits);
      });
    },
    chart: equipmentEnergyChart,
  });

  const alarmTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'alarm_list',
      '列出当前站点的告警：默认是正在发生的告警，status 用 cleared 看最近已恢复的。每条有标题、说明、严重程度、设备、开始和最近发生的时间、发生次数、是否已确认，以及经过（触发、确认、恢复的时间），最多 50 条。',
      alarmInputSchema,
      ALARM_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const status = ALARM_STATUS[parseAlarmArguments(argumentsValue)];
      assertExecutionCapability(context, ALARM_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        const timezone = sitePayload.site.timezone;
        const list = await readAlarms(context, authorization, toolAuthorizationReader, alarmReader, status.condition);
        // Each machine by the name the pages use, when the operator may read the device list.
        const devices = new Map<string, string>(
          list.items.some((alarm) => alarm.deviceId !== undefined) && hasCapabilities(context.capabilities, EQUIPMENT_CAPABILITIES)
            ? describeEquipment((await readAssetModel(context, authorization, toolAuthorizationReader, registryReader)).model)
              .map((device) => [device.deviceId, device.name])
            : [],
        );
        const note = list.items.length === 0 ? status.none : list.hasMore ? `只列出最近 ${ALARM_LIST_LIMIT} 条，还有更多` : undefined;
        return assertResultBytes({
          status: status.words,
          alarms: list.items.map((alarm) => ({
            title: alarm.title,
            summary: alarm.summary,
            severity: SEVERITY_WORDS[alarm.currentSeverity],
            ...(alarm.deviceId === undefined ? {} : { device: devices.get(alarm.deviceId) ?? '未登记的设备' }),
            since: siteTime(alarm.firstOccurredAt, timezone),
            latest: siteTime(alarm.lastOccurredAt, timezone),
            ...(alarm.clearedAt === undefined ? {} : { clearedAt: siteTime(alarm.clearedAt, timezone) }),
            times: alarm.occurrenceCount,
            handled: alarm.acknowledged ? '已确认' : '未确认',
            ...(alarm.assigned ? { claimed: '有人认领' } : {}),
            ...(alarm.suppressed ? { suppressed: '已抑制' } : {}),
            history: alarm.timeline.map((entry) => `${siteTime(entry.occurredAt, timezone)} ${OPERATION_WORDS[entry.operation]}`),
          })),
          ...(note === undefined ? {} : { note }),
        }, limits);
      });
    },
  });

  const fddTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'fdd_findings',
      '列出当前站点最近的故障诊断结果（最多 50 条）：哪条诊断规则、哪台设备、在什么时间段、测到多少、阈值多少、偏离程度，以及是否已转成告警或工单。',
      emptyInputSchema,
      FDD_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      exactEmptyArguments(argumentsValue);
      assertExecutionCapability(context, FDD_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        const timezone = sitePayload.site.timezone;
        const findings = await readFddFindings(context, authorization, toolAuthorizationReader, fddReader);
        // Each finding names the equipment it diagnoses, when the operator may read the asset model.
        const assets = new Map<string, string>(
          findings.items.length > 0 && hasCapabilities(context.capabilities, EQUIPMENT_CAPABILITIES)
            ? (await readAssetModel(context, authorization, toolAuthorizationReader, registryReader)).model.assets.map((asset) => [asset.id, asset.displayName])
            : [],
        );
        return assertResultBytes({
          findings: findings.items.map((finding) => {
            const rule = FINDING_RULES[finding.findingType];
            return {
              rule: rule?.rule ?? '其他诊断',
              equipment: assets.get(finding.assetId) ?? '未登记的设备',
              period: `${siteTime(finding.evaluationFrom, timezone)} 至 ${siteTime(finding.evaluationTo, timezone)}`,
              measured: `${rule?.measures ?? '测量值'} ${finding.observedValue} ${finding.valueUnit}，阈值 ${finding.thresholdValue} ${finding.valueUnit}`,
              deviation: deviation(finding.observedValue, finding.thresholdValue),
              reportedAt: siteTime(finding.createdAt, timezone),
              ...(finding.linkedToAlarm ? { alarm: '已转成告警' } : {}),
              ...(finding.linkedToWorkOrder ? { workOrder: '已建工单' } : {}),
            };
          }),
          ...(findings.items.length === 0 ? { note: '最近没有故障诊断结果' } : {}),
        }, limits);
      });
    },
  });

  const workOrderTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'work_order_list',
      '列出当前站点还没处理完的工单（待处理、处理中、受阻）：每条有标题、说明、优先级、状态、是否已指派、截止时间、来源（告警、哪台设备或人工创建）和创建时间。',
      emptyInputSchema,
      WORK_ORDER_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      exactEmptyArguments(argumentsValue);
      assertExecutionCapability(context, WORK_ORDER_CAPABILITIES);
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        const timezone = sitePayload.site.timezone;
        const list = await readWorkOrders(context, authorization, toolAuthorizationReader, workOrderReader);
        const unfinished = list.items.filter((workOrder) => UNFINISHED_WORK_ORDER.has(workOrder.status));
        const note = !list.hasMore
          ? (unfinished.length === 0 ? '现在没有还没处理完的工单' : undefined)
          : unfinished.length === 0
            ? `最近更新的 ${WORK_ORDER_LIST_LIMIT} 张工单都已处理完，更早的没有看`
            : `只看了最近更新的 ${WORK_ORDER_LIST_LIMIT} 张工单，可能还有更早的`;
        // A work order raised from a device names it, when the operator may read the asset model.
        const assets = new Map<string, string>(
          unfinished.some((workOrder) => workOrder.assetId !== undefined) && hasCapabilities(context.capabilities, EQUIPMENT_CAPABILITIES)
            ? (await readAssetModel(context, authorization, toolAuthorizationReader, registryReader)).model.assets.map((asset) => [asset.id, asset.displayName])
            : [],
        );
        return assertResultBytes({
          workOrders: unfinished.map((workOrder) => ({
            title: workOrder.title,
            description: workOrder.description,
            priority: WORK_ORDER_PRIORITY_WORDS[workOrder.priority],
            status: WORK_ORDER_STATUS_WORDS[workOrder.status],
            assigned: workOrder.assigned ? '已指派' : '未指派',
            ...(workOrder.dueAt === undefined ? {} : { dueAt: siteTime(workOrder.dueAt, timezone) }),
            from: workOrder.fromAlarm ? '来自告警' : workOrder.assetId !== undefined ? `来自设备 ${assets.get(workOrder.assetId) ?? '未登记的设备'}` : '人工创建',
            createdAt: siteTime(workOrder.createdAt, timezone),
            updatedAt: siteTime(workOrder.updatedAt, timezone),
          })),
          ...(note === undefined ? {} : { note }),
        }, limits);
      });
    },
  });

  const semanticQueryTool: AgentTool = Object.freeze({
    definition: toolDefinition(
      'energy_semantic_query',
      '一次查当前站点的几个指标，按小时、天或月列出，可以再按最多两个维度分组。一个问题要同时看几个数时用它。'
        + '指标和分组来自同一个视图；不分组时，站点能耗和估算电费的指标可以一起查（例如用电量和电费）。'
        + '能耗不按能源类型分组时会把用电和供冷加在一起；站点由服务端确定。',
      semanticQueryInputSchema,
      ENERGY_CAPABILITIES,
    ),
    async execute({ context, arguments: argumentsValue, signal }: AgentToolExecutionRequest) {
      const query = parseSemanticQuery(argumentsValue, limits);
      assertExecutionCapability(context, ENERGY_CAPABILITIES);
      const byDevice = query.parts.some((part) => part.dimensions.includes('equipment_id'));
      if (byDevice && !hasCapabilities(context.capabilities, EQUIPMENT_CAPABILITIES)) {
        fail('TOOL_ARGUMENTS_INVALID', '按设备分组需要查看设备清单的权限，这次只能不按设备分组。');
      }
      return runBounded(signal, limits.timeoutMs, async () => {
        const { payload: sitePayload } = await readSite(context, authorization, toolAuthorizationReader, registryReader);
        // Equipment is grouped by device id; the model reads each device's name instead.
        const devices = new Map<string, string>(byDevice
          ? describeEquipment((await readAssetModel(context, authorization, toolAuthorizationReader, registryReader)).model)
            .map((device) => [device.deviceId, device.name])
          : []);
        return assertResultBytes(await readSemantic(
          context,
          authorization,
          toolAuthorizationReader,
          semanticQueryReader,
          sitePayload.site.timezone,
          query,
          limits,
          devices,
        ), limits);
      });
    },
  });

  return Object.freeze([
    assetsTool,
    equipmentTool,
    telemetryTool,
    historyTool,
    energyQueryTool,
    energyCompareTool,
    equipmentEnergyTool,
    semanticQueryTool,
    alarmTool,
    fddTool,
    workOrderTool,
  ].filter((tool) => hasCapabilities(capabilities, tool.definition.requiredCapabilities)));
};
