import { sha256Hex } from './sha256.js';

export type NightEnergyGranularity = 'hour' | 'day' | 'month';
export type NightEnergyQualityPolicy = 'VALID_ONLY' | 'VALID_AND_SUSPECT';

export interface NightEnergySeriesPoint {
  readonly periodStart: string;
  readonly periodEnd: string;
  readonly energyKWh: number;
}

export interface NightEnergyQualitySummary {
  readonly valid: number;
  readonly suspect: number;
  readonly invalid: number;
}

export interface NightEnergySeriesMetadata {
  readonly requestedGranularity: NightEnergyGranularity;
  readonly actualGranularity: NightEnergyGranularity;
  readonly dataWatermark?: string;
  readonly aggregateWatermark?: string;
  readonly datasetRevision: string;
  readonly partial: boolean;
  readonly qualitySummary: NightEnergyQualitySummary;
}

export interface NightEnergySeries {
  readonly schemaVersion: 1;
  readonly points: readonly NightEnergySeriesPoint[];
  readonly metadata: NightEnergySeriesMetadata;
}

export interface SiteNightEnergyScope {
  readonly organizationId: string;
  readonly siteId: string;
  readonly timezone: string;
  readonly equipmentIds: readonly string[];
}

export interface SiteNightEnergyWindow {
  readonly startLocalTime: string;
  readonly endLocalTime: string;
}

export interface SiteNightEnergyAnalysisInput {
  readonly site: SiteNightEnergyScope;
  readonly window: SiteNightEnergyWindow;
  readonly targetLocalDate: string;
  readonly baselineLocalDate: string;
  readonly increaseThresholdPercent: number;
  readonly qualityPolicy: NightEnergyQualityPolicy;
  readonly targetSeries: NightEnergySeries;
  readonly baselineSeries: NightEnergySeries;
}

export type NightEnergyReadinessBlockerCode =
  | 'DATASET_REVISION_MISSING'
  | 'DATASET_REVISION_MISMATCH'
  | 'WATERMARK_STALE'
  | 'PARTIAL_DATASET'
  | 'GRANULARITY_MISMATCH'
  | 'QUALITY_POLICY_EXCEEDED'
  | 'QUALITY_SUMMARY_MISMATCH'
  | 'MISSING_BUCKETS'
  | 'NON_FINITE_ENERGY'
  | 'WINDOW_DURATION_MISMATCH'
  | 'ZERO_BASELINE';

export interface NightEnergyReadinessBlocker {
  readonly code: NightEnergyReadinessBlockerCode;
  readonly appliesTo: 'TARGET' | 'BASELINE' | 'COMPARISON';
  readonly detail: string;
}

export interface SiteNightEnergyPeriodReference {
  readonly localDate: string;
  readonly from: string;
  readonly to: string;
  readonly expectedBuckets: number;
}

export interface SiteNightEnergyAnalysisReference {
  readonly algorithmVersion: 'site-night-energy-comparison/v1';
  readonly digest: string;
  readonly timezone: string;
  readonly window: SiteNightEnergyWindow;
  readonly bucketRule: 'START_INCLUSIVE_END_EXCLUSIVE_CONTIGUOUS_HOURLY';
  readonly targetPeriod: SiteNightEnergyPeriodReference;
  readonly baselinePeriod: SiteNightEnergyPeriodReference;
  readonly datasetRevision: string | null;
  readonly watermarks: {
    readonly target: {
      readonly data: string | null;
      readonly aggregate: string | null;
    };
    readonly baseline: {
      readonly data: string | null;
      readonly aggregate: string | null;
    };
  };
  readonly qualitySummaries: {
    readonly target: NightEnergyQualitySummary;
    readonly baseline: NightEnergyQualitySummary;
  };
}

export interface SiteNightEnergyComparison {
  readonly targetEnergyKWh: number;
  readonly baselineEnergyKWh: number;
  readonly changeKWh: number;
  readonly changePercent: number;
  readonly increaseThresholdPercent: number;
}

export type NightEnergyEvidenceDraft =
  | {
    readonly kind: 'SITE_ENERGY_SERIES_READY';
    readonly classification: 'FACT';
    readonly statement: string;
    readonly analysisReferenceDigest: string;
  }
  | {
    readonly kind: 'SITE_ENERGY_PERIOD_COMPARISON';
    readonly classification: 'ALGORITHM_RESULT';
    readonly statement: string;
    readonly analysisReferenceDigest: string;
    readonly comparison: SiteNightEnergyComparison;
  };

export interface SiteNightEnergyFindingDraft {
  readonly kind: 'SITE_NIGHT_ENERGY_INCREASE' | 'SITE_NIGHT_ENERGY_WITHIN_THRESHOLD';
  readonly scope: 'SITE';
  readonly classification: 'INFERENCE';
  readonly statement: string;
  readonly supportEvidenceKinds: readonly ['FACT', 'ALGORITHM_RESULT'];
}

interface EquipmentAttributionRequiredNextBase {
  readonly status: 'REQUIRED_NEXT';
  readonly organizationId: string;
  readonly siteId: string;
  readonly equipmentIds: readonly string[];
  readonly targetPeriod: SiteNightEnergyPeriodReference;
  readonly baselinePeriod: SiteNightEnergyPeriodReference;
}

export type EquipmentAttributionRequiredNext =
  | EquipmentAttributionRequiredNextBase & {
    readonly kind: 'EQUIPMENT_ENERGY_BINDINGS';
    readonly owner: 'registry';
    readonly capability: 'registry.getEquipmentEnergyBindings';
    readonly requiredMetadata: readonly [
      'BUSINESS_REVISION',
      'QUALITY',
      'CAPTURED_AT',
      'PAYLOAD_DIGEST',
    ];
  }
  | EquipmentAttributionRequiredNextBase & {
    readonly kind: 'EQUIPMENT_ENERGY_PERIOD_COMPARISON';
    readonly owner: 'telemetry-query-service';
    readonly capability: 'analytics.energy.getEquipmentSeries';
    readonly requiredMetadata: readonly [
      'DATASET_REVISION',
      'WATERMARK',
      'PARTIAL',
      'QUALITY',
      'CAPTURED_AT',
      'PAYLOAD_DIGEST',
    ];
  };

export interface UnsupportedEquipmentAttribution {
  readonly status: 'UNABLE_TO_CONCLUDE';
  readonly statement: string;
  readonly requiredNext: readonly [
    EquipmentAttributionRequiredNext,
    EquipmentAttributionRequiredNext,
  ];
}

interface SiteNightEnergyAnalysisCommon {
  readonly analysisReference: SiteNightEnergyAnalysisReference;
  readonly equipmentAttribution: UnsupportedEquipmentAttribution;
}

export interface SupportedSiteNightEnergyAnalysis extends SiteNightEnergyAnalysisCommon {
  readonly status: 'SUPPORTED_SITE_FINDING';
  readonly comparison: SiteNightEnergyComparison;
  readonly evidence: readonly [NightEnergyEvidenceDraft, NightEnergyEvidenceDraft];
  readonly siteFinding: SiteNightEnergyFindingDraft;
}

export interface UnableSiteNightEnergyAnalysis extends SiteNightEnergyAnalysisCommon {
  readonly status: 'UNABLE_TO_CONCLUDE';
  readonly blockers: readonly NightEnergyReadinessBlocker[];
}

export type SiteNightEnergyAnalysisResult =
  | SupportedSiteNightEnergyAnalysis
  | UnableSiteNightEnergyAnalysis;

export type NightEnergyAnalysisErrorCode = 'INVALID_ANALYSIS_INPUT';

export class NightEnergyAnalysisError extends Error {
  readonly code: NightEnergyAnalysisErrorCode;

  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'NightEnergyAnalysisError';
    this.code = 'INVALID_ANALYSIS_INPUT';
  }
}

interface LocalDateParts {
  readonly year: number;
  readonly month: number;
  readonly day: number;
}

interface LocalTimeParts {
  readonly hour: number;
  readonly minute: number;
}

interface ExpectedPeriod {
  readonly localDate: string;
  readonly fromMs: number;
  readonly toMs: number;
  readonly expectedBuckets: number;
}

interface SeriesAssessment {
  readonly totalEnergyKWh: number;
  readonly datasetRevision: string;
  readonly dataWatermark: string | null;
  readonly aggregateWatermark: string | null;
  readonly qualitySummary: NightEnergyQualitySummary;
  readonly blockers: readonly NightEnergyReadinessBlocker[];
}

const algorithmVersion = 'site-night-energy-comparison/v1' as const;
const hourMs = 3_600_000;
const uuidV7Pattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/iu;
const localDatePattern = /^(\d{4})-(\d{2})-(\d{2})$/u;
const localTimePattern = /^(\d{2}):(\d{2})$/u;
const rfc3339Pattern = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/u;

const failInput = (message: string): never => {
  throw new NightEnergyAnalysisError(message);
};

const parseLocalDate = (value: string, label: string): LocalDateParts => {
  const match = localDatePattern.exec(value);
  if (match === null) {
    throw new NightEnergyAnalysisError(`${label} must use YYYY-MM-DD.`);
  }
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const normalized = new Date(Date.UTC(year, month - 1, day)).toISOString().slice(0, 10);
  if (normalized !== value) failInput(`${label} is not a valid calendar date.`);
  return { year, month, day };
};

const parseLocalTime = (value: string, label: string): LocalTimeParts => {
  const match = localTimePattern.exec(value);
  if (match === null) {
    throw new NightEnergyAnalysisError(`${label} must use HH:mm.`);
  }
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (hour > 23 || minute > 59) failInput(`${label} is not a valid local time.`);
  return { hour, minute };
};

const localMinutes = (value: LocalTimeParts): number => value.hour * 60 + value.minute;

const nextLocalDate = (value: string): string => {
  const parsed = parseLocalDate(value, 'local date');
  return new Date(Date.UTC(parsed.year, parsed.month - 1, parsed.day) + 86_400_000)
    .toISOString()
    .slice(0, 10);
};

const timezoneFormatter = (timezone: string): Intl.DateTimeFormat => {
  try {
    return new Intl.DateTimeFormat('en-US-u-ca-iso8601', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    });
  } catch (cause) {
    throw new NightEnergyAnalysisError('Site timezone must be a valid IANA timezone.', { cause });
  }
};

const formattedLocalParts = (
  formatter: Intl.DateTimeFormat,
  epochMs: number,
): Required<LocalDateParts & LocalTimeParts> & { readonly second: number } => {
  const parts = Object.fromEntries(
    formatter
      .formatToParts(epochMs)
      .filter(({ type }) => ['year', 'month', 'day', 'hour', 'minute', 'second'].includes(type))
      .map(({ type, value }) => [type, Number(value)]),
  );
  return parts as Required<LocalDateParts & LocalTimeParts> & { readonly second: number };
};

const zonedLocalToEpochMs = (
  date: LocalDateParts,
  time: LocalTimeParts,
  formatter: Intl.DateTimeFormat,
): number => {
  const desiredAsUtc = Date.UTC(date.year, date.month - 1, date.day, time.hour, time.minute, 0);
  let candidate = desiredAsUtc;
  for (let attempt = 0; attempt < 6; attempt += 1) {
    const represented = formattedLocalParts(formatter, candidate);
    const representedAsUtc = Date.UTC(
      represented.year,
      represented.month - 1,
      represented.day,
      represented.hour,
      represented.minute,
      represented.second,
    );
    const adjustment = desiredAsUtc - representedAsUtc;
    candidate += adjustment;
    if (adjustment === 0) break;
  }
  const verified = formattedLocalParts(formatter, candidate);
  if (verified.year !== date.year
    || verified.month !== date.month
    || verified.day !== date.day
    || verified.hour !== time.hour
    || verified.minute !== time.minute
    || verified.second !== 0) {
    failInput('Night-energy window resolves to a nonexistent or ambiguous local boundary.');
  }
  return candidate;
};

const createExpectedPeriod = (
  localDate: string,
  window: SiteNightEnergyWindow,
  formatter: Intl.DateTimeFormat,
): ExpectedPeriod => {
  const startDate = parseLocalDate(localDate, 'comparison local date');
  const startTime = parseLocalTime(window.startLocalTime, 'window.startLocalTime');
  const endTime = parseLocalTime(window.endLocalTime, 'window.endLocalTime');
  if (localMinutes(startTime) === localMinutes(endTime)) {
    failInput('Night-energy window cannot span exactly zero or twenty-four hours.');
  }
  const endLocalDate = localMinutes(endTime) <= localMinutes(startTime)
    ? nextLocalDate(localDate)
    : localDate;
  const endDate = parseLocalDate(endLocalDate, 'comparison end local date');
  const fromMs = zonedLocalToEpochMs(startDate, startTime, formatter);
  const toMs = zonedLocalToEpochMs(endDate, endTime, formatter);
  const duration = toMs - fromMs;
  if (duration <= 0 || duration % hourMs !== 0) {
    failInput('Night-energy window must resolve to a positive whole number of hours.');
  }
  return {
    localDate,
    fromMs,
    toMs,
    expectedBuckets: duration / hourMs,
  };
};

const isRecord = (value: unknown): value is Record<string, unknown> => (
  typeof value === 'object' && value !== null && !Array.isArray(value)
);

const hasExactKeys = (
  value: Record<string, unknown>,
  required: readonly string[],
  optional: readonly string[] = [],
): boolean => {
  const actual = Object.keys(value);
  return required.every((key) => key in value)
    && actual.every((key) => required.includes(key) || optional.includes(key));
};

const validateSeriesContract = (value: unknown, label: string): NightEnergySeries => {
  if (!isRecord(value)) {
    throw new NightEnergyAnalysisError(`${label} must be a versioned Energy Series.`);
  }
  const series = value;
  if (!hasExactKeys(series, ['schemaVersion', 'points', 'metadata'])
    || series.schemaVersion !== 1
    || !Array.isArray(series.points)
    || series.points.length > 10_000
    || !isRecord(series.metadata)) {
    throw new NightEnergyAnalysisError(`${label} must be a versioned Energy Series.`);
  }
  const points = series.points;
  const metadata = series.metadata;
  if (!hasExactKeys(
    metadata,
    [
      'requestedGranularity',
      'actualGranularity',
      'datasetRevision',
      'partial',
      'qualitySummary',
    ],
    ['dataWatermark', 'aggregateWatermark'],
  )
    || typeof metadata.requestedGranularity !== 'string'
    || typeof metadata.actualGranularity !== 'string'
    || typeof metadata.datasetRevision !== 'string'
    || typeof metadata.partial !== 'boolean'
    || (metadata.dataWatermark !== undefined && typeof metadata.dataWatermark !== 'string')
    || (metadata.aggregateWatermark !== undefined
      && typeof metadata.aggregateWatermark !== 'string')
    || !isRecord(metadata.qualitySummary)) {
    throw new NightEnergyAnalysisError(
      `${label} metadata is not a verified Energy Series contract.`,
    );
  }
  const qualitySummary = metadata.qualitySummary;
  if (!hasExactKeys(qualitySummary, ['valid', 'suspect', 'invalid'])
    || typeof qualitySummary.valid !== 'number'
    || typeof qualitySummary.suspect !== 'number'
    || typeof qualitySummary.invalid !== 'number') {
    throw new NightEnergyAnalysisError(
      `${label} metadata is not a verified Energy Series contract.`,
    );
  }
  for (const point of points) {
    if (!isRecord(point)
      || !hasExactKeys(point, ['periodStart', 'periodEnd', 'energyKWh'])
      || typeof point.periodStart !== 'string'
      || typeof point.periodEnd !== 'string'
      || typeof point.energyKWh !== 'number') {
      throw new NightEnergyAnalysisError(
        `${label} contains an unverified Energy Series point.`,
      );
    }
  }
  return series as unknown as NightEnergySeries;
};

const isSafeCount = (value: unknown): value is number => (
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
);

const asQualitySummary = (value: NightEnergyQualitySummary): NightEnergyQualitySummary => ({
  valid: isSafeCount(value?.valid) ? value.valid : 0,
  suspect: isSafeCount(value?.suspect) ? value.suspect : 0,
  invalid: isSafeCount(value?.invalid) ? value.invalid : 0,
});

const asInstantMs = (value: unknown): number | null => (
  typeof value === 'string' && rfc3339Pattern.test(value) && Number.isFinite(Date.parse(value))
    ? Date.parse(value)
    : null
);

const pushBlocker = (
  blockers: NightEnergyReadinessBlocker[],
  blocker: NightEnergyReadinessBlocker,
): void => {
  if (!blockers.some(({ code, appliesTo }) => (
    code === blocker.code && appliesTo === blocker.appliesTo
  ))) blockers.push(blocker);
};

const assessSeries = (
  series: NightEnergySeries,
  period: ExpectedPeriod,
  appliesTo: 'TARGET' | 'BASELINE',
  qualityPolicy: NightEnergyQualityPolicy,
): SeriesAssessment => {
  const blockers: NightEnergyReadinessBlocker[] = [];
  const metadata = series?.metadata;
  const datasetRevision = typeof metadata?.datasetRevision === 'string'
    ? metadata.datasetRevision.trim()
    : '';
  if (datasetRevision.length === 0) {
    pushBlocker(blockers, {
      code: 'DATASET_REVISION_MISSING',
      appliesTo,
      detail: `${appliesTo} Energy Series is missing Dataset Revision.`,
    });
  }
  if (metadata?.partial !== false) {
    pushBlocker(blockers, {
      code: 'PARTIAL_DATASET',
      appliesTo,
      detail: `${appliesTo} Energy Series is partial.`,
    });
  }
  if (metadata?.requestedGranularity !== 'hour' || metadata.actualGranularity !== 'hour') {
    pushBlocker(blockers, {
      code: 'GRANULARITY_MISMATCH',
      appliesTo,
      detail: `${appliesTo} Energy Series must use requested and actual hourly granularity.`,
    });
  }

  const dataWatermarkMs = asInstantMs(metadata?.dataWatermark);
  const aggregateWatermarkMs = asInstantMs(metadata?.aggregateWatermark);
  if (dataWatermarkMs === null
    || aggregateWatermarkMs === null
    || dataWatermarkMs < period.toMs
    || aggregateWatermarkMs < period.toMs) {
    pushBlocker(blockers, {
      code: 'WATERMARK_STALE',
      appliesTo,
      detail: `${appliesTo} Energy Series watermarks do not cover the requested period end.`,
    });
  }

  const qualitySummary = asQualitySummary(metadata?.qualitySummary);
  const rawQuality = metadata?.qualitySummary;
  if (!isSafeCount(rawQuality?.valid)
    || !isSafeCount(rawQuality?.suspect)
    || !isSafeCount(rawQuality?.invalid)) {
    pushBlocker(blockers, {
      code: 'QUALITY_SUMMARY_MISMATCH',
      appliesTo,
      detail: `${appliesTo} Energy Series Quality Summary is invalid.`,
    });
  }
  if (qualitySummary.invalid > 0
    || (qualityPolicy === 'VALID_ONLY' && qualitySummary.suspect > 0)) {
    pushBlocker(blockers, {
      code: 'QUALITY_POLICY_EXCEEDED',
      appliesTo,
      detail: `${appliesTo} Energy Series Quality Summary exceeds ${qualityPolicy}.`,
    });
  }

  const points = Array.isArray(series?.points) ? series.points : [];
  if (qualitySummary.valid + qualitySummary.suspect + qualitySummary.invalid !== points.length) {
    pushBlocker(blockers, {
      code: 'QUALITY_SUMMARY_MISMATCH',
      appliesTo,
      detail: `${appliesTo} Energy Series Quality Summary does not match its bucket count.`,
    });
  }

  let cursor = period.fromMs;
  let totalEnergyKWh = 0;
  let bucketSequenceValid = points.length === period.expectedBuckets;
  for (const point of points) {
    const start = asInstantMs(point?.periodStart);
    const end = asInstantMs(point?.periodEnd);
    if (start === null
      || end === null
      || start !== cursor
      || end !== start + hourMs
      || end > period.toMs) {
      bucketSequenceValid = false;
    }
    if (start !== null && end !== null) cursor = end;
    if (typeof point?.energyKWh !== 'number'
      || !Number.isFinite(point.energyKWh)
      || point.energyKWh < 0) {
      pushBlocker(blockers, {
        code: 'NON_FINITE_ENERGY',
        appliesTo,
        detail: `${appliesTo} Energy Series contains a non-finite or negative energy value.`,
      });
    } else {
      totalEnergyKWh += point.energyKWh;
    }
  }
  if (!bucketSequenceValid || cursor !== period.toMs) {
    pushBlocker(blockers, {
      code: 'MISSING_BUCKETS',
      appliesTo,
      detail: `${appliesTo} Energy Series does not contain one contiguous bucket per expected hour.`,
    });
  }

  return {
    totalEnergyKWh,
    datasetRevision,
    dataWatermark: dataWatermarkMs === null ? null : metadata.dataWatermark ?? null,
    aggregateWatermark: aggregateWatermarkMs === null
      ? null
      : metadata.aggregateWatermark ?? null,
    qualitySummary,
    blockers,
  };
};

const rounded = (value: number): number => {
  const result = Math.round((value + Number.EPSILON) * 1_000_000) / 1_000_000;
  return Object.is(result, -0) ? 0 : result;
};

const formattedNumber = (value: number): string => {
  const fixed = rounded(value).toFixed(6);
  return fixed.replace(/\.0+$/u, '').replace(/(\.\d*?[1-9])0+$/u, '$1');
};

const periodReference = (period: ExpectedPeriod): SiteNightEnergyPeriodReference => ({
  localDate: period.localDate,
  from: new Date(period.fromMs).toISOString(),
  to: new Date(period.toMs).toISOString(),
  expectedBuckets: period.expectedBuckets,
});

const canonicalInstant = (value: string | undefined): string | null => {
  if (value === undefined) return null;
  const instant = asInstantMs(value);
  return instant === null ? value : new Date(instant).toISOString();
};

const canonicalEnergy = (value: number): number | string => (
  Number.isFinite(value) ? value : `NON_FINITE:${String(value)}`
);

const canonicalSeries = (series: NightEnergySeries): object => ({
  schemaVersion: 1,
  points: series.points.map(({ periodStart, periodEnd, energyKWh }) => ({
    periodStart: canonicalInstant(periodStart),
    periodEnd: canonicalInstant(periodEnd),
    energyKWh: canonicalEnergy(energyKWh),
  })),
  metadata: {
    requestedGranularity: series.metadata.requestedGranularity,
    actualGranularity: series.metadata.actualGranularity,
    dataWatermark: canonicalInstant(series.metadata.dataWatermark),
    aggregateWatermark: canonicalInstant(series.metadata.aggregateWatermark),
    datasetRevision: series.metadata.datasetRevision.trim(),
    partial: series.metadata.partial,
    qualitySummary: {
      valid: series.metadata.qualitySummary.valid,
      suspect: series.metadata.qualitySummary.suspect,
      invalid: series.metadata.qualitySummary.invalid,
    },
  },
});

const createEquipmentAttribution = (
  input: SiteNightEnergyAnalysisInput,
  targetPeriod: SiteNightEnergyPeriodReference,
  baselinePeriod: SiteNightEnergyPeriodReference,
): UnsupportedEquipmentAttribution => ({
  status: 'UNABLE_TO_CONCLUDE',
  statement: 'No specific Equipment can be named until canonical Equipment energy bindings and comparable Equipment-level energy series are available.',
  requiredNext: [
    {
      status: 'REQUIRED_NEXT',
      kind: 'EQUIPMENT_ENERGY_BINDINGS',
      owner: 'registry',
      capability: 'registry.getEquipmentEnergyBindings',
      requiredMetadata: [
        'BUSINESS_REVISION',
        'QUALITY',
        'CAPTURED_AT',
        'PAYLOAD_DIGEST',
      ],
      organizationId: input.site.organizationId,
      siteId: input.site.siteId,
      equipmentIds: [...input.site.equipmentIds].sort(),
      targetPeriod,
      baselinePeriod,
    },
    {
      status: 'REQUIRED_NEXT',
      kind: 'EQUIPMENT_ENERGY_PERIOD_COMPARISON',
      owner: 'telemetry-query-service',
      capability: 'analytics.energy.getEquipmentSeries',
      requiredMetadata: [
        'DATASET_REVISION',
        'WATERMARK',
        'PARTIAL',
        'QUALITY',
        'CAPTURED_AT',
        'PAYLOAD_DIGEST',
      ],
      organizationId: input.site.organizationId,
      siteId: input.site.siteId,
      equipmentIds: [...input.site.equipmentIds].sort(),
      targetPeriod,
      baselinePeriod,
    },
  ],
});

const validateInput = (value: unknown): SiteNightEnergyAnalysisInput => {
  if (!isRecord(value)
    || !hasExactKeys(value, [
      'site',
      'window',
      'targetLocalDate',
      'baselineLocalDate',
      'increaseThresholdPercent',
      'qualityPolicy',
      'targetSeries',
      'baselineSeries',
    ])) {
    throw new NightEnergyAnalysisError(
      'Night-energy analysis input does not match the project contract.',
    );
  }
  const input = value;
  if (!isRecord(input.site)
    || !hasExactKeys(input.site, [
      'organizationId',
      'siteId',
      'timezone',
      'equipmentIds',
    ])
    || typeof input.site.organizationId !== 'string'
    || typeof input.site.siteId !== 'string'
    || typeof input.site.timezone !== 'string'
    || !Array.isArray(input.site.equipmentIds)
    || input.site.equipmentIds.some((identity) => typeof identity !== 'string')) {
    throw new NightEnergyAnalysisError(
      'Night-energy analysis Site Scope does not match the project contract.',
    );
  }
  const site = input.site;
  const equipmentIds = site.equipmentIds as string[];
  if (!isRecord(input.window)
    || !hasExactKeys(input.window, ['startLocalTime', 'endLocalTime'])
    || typeof input.window.startLocalTime !== 'string'
    || typeof input.window.endLocalTime !== 'string'
    || typeof input.targetLocalDate !== 'string'
    || typeof input.baselineLocalDate !== 'string'
    || typeof input.increaseThresholdPercent !== 'number'
    || typeof input.qualityPolicy !== 'string') {
    throw new NightEnergyAnalysisError(
      'Night-energy analysis comparison parameters do not match the project contract.',
    );
  }
  const targetSeries = validateSeriesContract(input.targetSeries, 'targetSeries');
  const baselineSeries = validateSeriesContract(input.baselineSeries, 'baselineSeries');
  const validated = {
    site: {
      organizationId: site.organizationId,
      siteId: site.siteId,
      timezone: site.timezone,
      equipmentIds: [...equipmentIds],
    },
    window: {
      startLocalTime: input.window.startLocalTime,
      endLocalTime: input.window.endLocalTime,
    },
    targetLocalDate: input.targetLocalDate,
    baselineLocalDate: input.baselineLocalDate,
    increaseThresholdPercent: input.increaseThresholdPercent,
    qualityPolicy: input.qualityPolicy,
    targetSeries,
    baselineSeries,
  } as SiteNightEnergyAnalysisInput;
  if (!uuidV7Pattern.test(validated.site.organizationId)
    || !uuidV7Pattern.test(validated.site.siteId)
    || validated.site.equipmentIds.some((identity) => !uuidV7Pattern.test(identity))) {
    failInput('Night-energy analysis Scope identities must be UUIDv7 values.');
  }
  if (new Set(validated.site.equipmentIds).size !== validated.site.equipmentIds.length) {
    failInput('Night-energy analysis Equipment identities must be unique.');
  }
  if (validated.targetLocalDate === validated.baselineLocalDate) {
    failInput('Target and baseline local dates must be different.');
  }
  if (!Number.isFinite(validated.increaseThresholdPercent)
    || validated.increaseThresholdPercent < 0
    || validated.increaseThresholdPercent > 10_000) {
    failInput('Increase threshold must be a finite percentage between 0 and 10000.');
  }
  if (validated.qualityPolicy !== 'VALID_ONLY'
    && validated.qualityPolicy !== 'VALID_AND_SUSPECT') {
    failInput('Night-energy Quality Policy is unsupported.');
  }
  return validated;
};

export const analyzeSiteNightEnergy = (
  value: SiteNightEnergyAnalysisInput,
): SiteNightEnergyAnalysisResult => {
  const input = validateInput(value);
  const formatter = timezoneFormatter(input.site.timezone);
  const target = createExpectedPeriod(input.targetLocalDate, input.window, formatter);
  const baseline = createExpectedPeriod(input.baselineLocalDate, input.window, formatter);
  const targetReference = periodReference(target);
  const baselineReference = periodReference(baseline);
  const targetAssessment = assessSeries(
    input.targetSeries,
    target,
    'TARGET',
    input.qualityPolicy,
  );
  const baselineAssessment = assessSeries(
    input.baselineSeries,
    baseline,
    'BASELINE',
    input.qualityPolicy,
  );
  const blockers = [
    ...targetAssessment.blockers,
    ...baselineAssessment.blockers,
  ];

  if (target.expectedBuckets !== baseline.expectedBuckets) {
    pushBlocker(blockers, {
      code: 'WINDOW_DURATION_MISMATCH',
      appliesTo: 'COMPARISON',
      detail: 'Target and baseline local windows resolve to different elapsed durations.',
    });
  }
  if (targetAssessment.datasetRevision.length > 0
    && baselineAssessment.datasetRevision.length > 0
    && targetAssessment.datasetRevision !== baselineAssessment.datasetRevision) {
    pushBlocker(blockers, {
      code: 'DATASET_REVISION_MISMATCH',
      appliesTo: 'COMPARISON',
      detail: 'Target and baseline Energy Series use different Dataset Revisions.',
    });
  }
  if (baselineAssessment.totalEnergyKWh === 0) {
    pushBlocker(blockers, {
      code: 'ZERO_BASELINE',
      appliesTo: 'COMPARISON',
      detail: 'Baseline night energy is zero, so percentage change is undefined.',
    });
  }

  const sharedDatasetRevision = targetAssessment.datasetRevision.length > 0
    && targetAssessment.datasetRevision === baselineAssessment.datasetRevision
    ? targetAssessment.datasetRevision
    : null;
  const digestSource = {
    algorithmVersion,
    site: {
      organizationId: input.site.organizationId,
      siteId: input.site.siteId,
      timezone: input.site.timezone,
      equipmentIds: [...input.site.equipmentIds].sort(),
    },
    window: input.window,
    targetPeriod: targetReference,
    baselinePeriod: baselineReference,
    increaseThresholdPercent: input.increaseThresholdPercent,
    qualityPolicy: input.qualityPolicy,
    targetSeries: canonicalSeries(input.targetSeries),
    baselineSeries: canonicalSeries(input.baselineSeries),
  };
  const digest = `sha256:${sha256Hex(JSON.stringify(digestSource))}`;
  const analysisReference: SiteNightEnergyAnalysisReference = {
    algorithmVersion,
    digest,
    timezone: input.site.timezone,
    window: { ...input.window },
    bucketRule: 'START_INCLUSIVE_END_EXCLUSIVE_CONTIGUOUS_HOURLY',
    targetPeriod: targetReference,
    baselinePeriod: baselineReference,
    datasetRevision: sharedDatasetRevision,
    watermarks: {
      target: {
        data: targetAssessment.dataWatermark,
        aggregate: targetAssessment.aggregateWatermark,
      },
      baseline: {
        data: baselineAssessment.dataWatermark,
        aggregate: baselineAssessment.aggregateWatermark,
      },
    },
    qualitySummaries: {
      target: targetAssessment.qualitySummary,
      baseline: baselineAssessment.qualitySummary,
    },
  };
  const equipmentAttribution = createEquipmentAttribution(
    input,
    targetReference,
    baselineReference,
  );

  if (blockers.length > 0) {
    return {
      status: 'UNABLE_TO_CONCLUDE',
      blockers,
      analysisReference,
      equipmentAttribution,
    };
  }

  const targetEnergyKWh = rounded(targetAssessment.totalEnergyKWh);
  const baselineEnergyKWh = rounded(baselineAssessment.totalEnergyKWh);
  const changeKWh = rounded(targetEnergyKWh - baselineEnergyKWh);
  const changePercent = rounded((changeKWh / baselineEnergyKWh) * 100);
  const comparison: SiteNightEnergyComparison = {
    targetEnergyKWh,
    baselineEnergyKWh,
    changeKWh,
    changePercent,
    increaseThresholdPercent: input.increaseThresholdPercent,
  };
  const changeDirection = changePercent >= 0 ? 'increase' : 'decrease';
  const comparisonStatement = `Target night energy was ${formattedNumber(targetEnergyKWh)} kWh versus a ${formattedNumber(baselineEnergyKWh)} kWh baseline, a ${formattedNumber(Math.abs(changePercent))}% ${changeDirection}.`;
  const isIncrease = changePercent > 0
    && changePercent >= input.increaseThresholdPercent;
  const siteFinding: SiteNightEnergyFindingDraft = {
    kind: isIncrease ? 'SITE_NIGHT_ENERGY_INCREASE' : 'SITE_NIGHT_ENERGY_WITHIN_THRESHOLD',
    scope: 'SITE',
    classification: 'INFERENCE',
    statement: isIncrease
      ? comparisonStatement
      : `${comparisonStatement} This does not exceed the ${formattedNumber(input.increaseThresholdPercent)}% increase threshold.`,
    supportEvidenceKinds: ['FACT', 'ALGORITHM_RESULT'],
  };
  const evidence: readonly [NightEnergyEvidenceDraft, NightEnergyEvidenceDraft] = [
    {
      kind: 'SITE_ENERGY_SERIES_READY',
      classification: 'FACT',
      statement: 'Authoritative target and baseline Site energy series are complete, comparable and within the accepted Quality Policy.',
      analysisReferenceDigest: digest,
    },
    {
      kind: 'SITE_ENERGY_PERIOD_COMPARISON',
      classification: 'ALGORITHM_RESULT',
      statement: comparisonStatement,
      analysisReferenceDigest: digest,
      comparison,
    },
  ];
  return {
    status: 'SUPPORTED_SITE_FINDING',
    comparison,
    evidence,
    siteFinding,
    analysisReference,
    equipmentAttribution,
  };
};
