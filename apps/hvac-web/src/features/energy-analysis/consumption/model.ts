import { PlatformApiError, type EnergySeriesQuery, type EnergySeriesResponse } from '../../../api/generated/platformGateway.gen.ts';

export const ENERGY_PERIODS = { today: '今日', '7d': '近 7 天', month: '本月', year: '本年' } as const;
export type EnergyPeriod = keyof typeof ENERGY_PERIODS;
export type Granularity = EnergySeriesQuery['granularity'];
/** A bucket's electricity as the Energy analytics owner priced it at the Site's Tariff. */
export type EnergyCost = NonNullable<EnergySeriesResponse['points'][number]['cost']>;

interface WallTime { year: number; month: number; day: number; hour: number; minute: number }

const wallTimeFormatters = new Map<string, Intl.DateTimeFormat>();

function wallTimeFormatter(timeZone: string): Intl.DateTimeFormat {
  let formatter = wallTimeFormatters.get(timeZone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat('en-US', {
      timeZone,
      year: 'numeric',
      month: 'numeric',
      day: 'numeric',
      hour: 'numeric',
      minute: 'numeric',
      hourCycle: 'h23',
    });
    wallTimeFormatters.set(timeZone, formatter);
  }
  return formatter;
}

export function wallTime(instant: Date, timeZone: string): WallTime {
  let year = 0;
  let month = 0;
  let day = 0;
  let hour = 0;
  let minute = 0;
  for (const part of wallTimeFormatter(timeZone).formatToParts(instant)) {
    if (part.type === 'year') year = Number(part.value);
    else if (part.type === 'month') month = Number(part.value);
    else if (part.type === 'day') day = Number(part.value);
    else if (part.type === 'hour') hour = Number(part.value);
    else if (part.type === 'minute') minute = Number(part.value);
  }
  return { year, month, day, hour, minute };
}

/** The instant at which the Site's wall clock shows the given local midnight. */
export function siteMidnight(year: number, month: number, day: number, timeZone: string): Date {
  const guess = Date.UTC(year, month - 1, day);
  const shown = wallTime(new Date(guess), timeZone);
  const offset = Date.UTC(shown.year, shown.month - 1, shown.day, shown.hour, shown.minute) - guess;
  return new Date(guess - offset);
}

/** Snap `now` to 30s boundaries while staying past Site midnight so `to > from` always holds. */
export function energyQueryNow(timeZone: string, nowMs = Date.now(), bucketMs = 30_000): Date {
  const snapped = Math.floor(nowMs / bucketMs) * bucketMs, { year, month, day } = wallTime(new Date(nowMs), timeZone), midnight = siteMidnight(year, month, day, timeZone).getTime();
  return new Date(snapped > midnight ? snapped : Math.max(nowMs, midnight + 1000));
}

/** Site-local window of a period, ending now; buckets are hours today, days within a month, months within a year. */
export function periodWindow(period: EnergyPeriod, now: Date, timeZone: string): { granularity: Granularity; from: Date; to: Date } {
  const today = wallTime(now, timeZone);
  switch (period) {
    case 'today':
      return { granularity: 'hour', from: siteMidnight(today.year, today.month, today.day, timeZone), to: now };
    case '7d':
      return { granularity: 'day', from: siteMidnight(today.year, today.month, today.day - 6, timeZone), to: now };
    case 'month':
      return { granularity: 'day', from: siteMidnight(today.year, today.month, 1, timeZone), to: now };
    case 'year':
      return { granularity: 'month', from: siteMidnight(today.year, 1, 1, timeZone), to: now };
  }
}

export interface EnergyRow {
  readonly periodStart: string;
  readonly label: string;
  readonly electricityKWh: number | null;
  readonly crossGapElectricityKWh: number;
  readonly coolingKWh: number | null;
  /** Cooling delivered per unit of HVAC electricity in the bucket; null without both energies. */
  readonly cop: number | null;
  /** The electricity's estimated energy charge; null without electricity. */
  readonly cost: EnergyCost | null;
  /** Synthesized-total hour missing one or more sub-meters. */
  readonly incomplete: boolean;
  readonly missingMeters: readonly string[];
}

export interface EnergySummary {
  readonly granularity: Granularity;
  readonly rows: readonly EnergyRow[];
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  readonly cop: number | null;
  readonly copUnavailableReason: CopUnavailableReason | null;
  readonly peak: EnergyRow | null;
  /** Latest instant both energies are known up to; null when either has no data. */
  readonly dataWatermark: string | null;
  /** Suspect or invalid meter intervals left out of every total. */
  readonly excludedIntervals: number;
  /** Counter intervals > 1h recovered in the window: counted in totals, excluded from hourly/daily curves. */
  readonly crossGapElectricityKWh: number;
  readonly crossGapCost: EnergyCost | null;
  /** Count of buckets in this window flagged incomplete due to missing sub-meter telemetry. */
  readonly incompleteBuckets: number;
  /** Distinct sub-meters missing across any incomplete hour in this window. */
  readonly missingMeters: readonly string[];
}

const HOUR_MS = 3_600_000;
const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

/** Physical bounds for plant COP (cooling / electricity), aligned with nameplate.COPPlausible [1, 12]. */
export const MIN_VALID_PLANT_COP = 1;
export const MAX_VALID_PLANT_COP = 12;
/** Minimum energy per bucket/period to filter out chiller standby leakage and meter drift. */
export const MIN_COP_ELECTRICITY_KWH = 1;
export const MIN_COP_COOLING_KWH = 1;

export type CopUnavailableReason = 'no-data' | 'standby' | 'out-of-range';

export interface CopEvaluation {
  readonly cop: number | null;
  readonly unavailableReason: CopUnavailableReason | null;
}

export const COP_UNAVAILABLE_COPY: Readonly<Record<CopUnavailableReason, string>> = {
  'no-data': '缺少用电或供冷数据',
  standby: '冷机待机或无有效冷负荷，暂不计算',
  'out-of-range': '能效超出合理范围，暂不计算',
};

export function copUnavailableText(
  reason: CopUnavailableReason | null,
  missingMeters: readonly string[] = [],
): string {
  const resolved = reason ?? 'no-data';
  if (resolved === 'out-of-range' && missingMeters.length > 0) {
    return `缺 ${missingMeters.join('、')}，能效超出合理范围，暂不计算`;
  }
  return COP_UNAVAILABLE_COPY[resolved];
}

/**
 * Evaluates energy-weighted plant COP (`coolingKWh / electricityKWh`), guarding against missing
 * inputs, non-finite values, chiller standby / negligible load, and out-of-bounds outliers.
 */
export function evaluatePlantCop(
  coolingKWh: number | null | undefined,
  electricityKWh: number | null | undefined,
): CopEvaluation {
  if (
    coolingKWh === null || coolingKWh === undefined
    || electricityKWh === null || electricityKWh === undefined
    || !Number.isFinite(coolingKWh)
    || !Number.isFinite(electricityKWh)
  ) {
    return { cop: null, unavailableReason: 'no-data' };
  }
  if (coolingKWh < 0 || electricityKWh < 0) {
    return { cop: null, unavailableReason: 'out-of-range' };
  }
  if (electricityKWh === 0) {
    return { cop: null, unavailableReason: coolingKWh > 0 ? 'out-of-range' : 'no-data' };
  }
  if (coolingKWh === 0 || coolingKWh < MIN_COP_COOLING_KWH) {
    return { cop: null, unavailableReason: 'standby' };
  }
  if (electricityKWh < MIN_COP_ELECTRICITY_KWH) {
    return { cop: null, unavailableReason: 'out-of-range' };
  }
  const value = coolingKWh / electricityKWh;
  if (!Number.isFinite(value)) {
    return { cop: null, unavailableReason: 'out-of-range' };
  }
  if (value < MIN_VALID_PLANT_COP) {
    return { cop: null, unavailableReason: 'standby' };
  }
  if (value > MAX_VALID_PLANT_COP) {
    return { cop: null, unavailableReason: 'out-of-range' };
  }
  return { cop: value, unavailableReason: null };
}

/** Validates an already-computed (e.g. archived snapshot) COP against standby and physical bounds. */
export function sanitizeReportedCop(
  cop: number | null | undefined,
  coolingKWh?: number | null,
  electricityKWh?: number | null,
): CopEvaluation {
  if (cop === null || cop === undefined) {
    const inferredReason = coolingKWh !== undefined || electricityKWh !== undefined
      ? (evaluatePlantCop(coolingKWh, electricityKWh).unavailableReason ?? 'no-data')
      : 'no-data';
    return { cop: null, unavailableReason: inferredReason };
  }
  if (!Number.isFinite(cop)) {
    return { cop: null, unavailableReason: 'out-of-range' };
  }
  if (coolingKWh !== undefined && electricityKWh !== undefined) {
    const checked = evaluatePlantCop(coolingKWh, electricityKWh);
    if (checked.cop === null) {
      return { cop: null, unavailableReason: checked.unavailableReason };
    }
  }
  if (cop < 0 || cop > MAX_VALID_PLANT_COP) {
    return { cop: null, unavailableReason: 'out-of-range' };
  }
  if (cop < MIN_VALID_PLANT_COP) {
    return { cop: null, unavailableReason: 'standby' };
  }
  return { cop, unavailableReason: null };
}

export function bucketLabel(periodStart: string, granularity: Granularity, timeZone: string): string {
  const instant = new Date(periodStart);
  const local = wallTime(instant, timeZone);
  if (granularity === 'hour') return `${String(local.hour).padStart(2, '0')}:00`;
  if (granularity === 'month') return `${local.month} 月`;
  const weekday = WEEKDAYS[new Date(Date.UTC(local.year, local.month - 1, local.day)).getUTCDay()];
  return `${local.month}/${local.day} ${weekday}`;
}

const total = (series: EnergySeriesResponse, granularity: Granularity) => {
  const crossGap = granularity === 'month' ? 0 : (series.crossGap?.energyKWh ?? 0);
  if (series.points.length === 0 && crossGap <= 0) return null;
  return series.points.reduce((sum, point) => sum + point.energyKWh, 0) + crossGap;
};

const earlier = (left?: string, right?: string) =>
  !left || !right ? null : new Date(left) <= new Date(right) ? left : right;

/** Buckets passed to sumCosts: includes non-monthly cross-gap electricity so period cost totals match period kWh. */
export function summaryCostBuckets(
  summary: EnergySummary,
): readonly { readonly electricityKWh: number | null; readonly cost: EnergyCost | null }[] {
  if (summary.granularity === 'month' || summary.crossGapElectricityKWh <= 0) return summary.rows;
  return [...summary.rows, { electricityKWh: summary.crossGapElectricityKWh, cost: summary.crossGapCost }];
}

export function summarizeEnergy(
  electricity: EnergySeriesResponse,
  cooling: EnergySeriesResponse,
  granularity: Granularity,
  timeZone: string,
): EnergySummary {
  const byStart = new Map<string, {
    electricity: number | null;
    crossGapElectricity: number;
    cooling: number | null;
    cost: EnergyCost | null;
    incomplete: boolean;
    missingMeters: Set<string>;
  }>();
  const at = (periodStart: string) => {
    const key = new Date(periodStart).toISOString();
    const entry = byStart.get(key) ?? {
      electricity: null,
      crossGapElectricity: 0,
      cooling: null,
      cost: null,
      incomplete: false,
      missingMeters: new Set<string>(),
    };
    byStart.set(key, entry);
    return entry;
  };
  const mergeCompleteness = (
    entry: ReturnType<typeof at>,
    point: EnergySeriesResponse['points'][number],
  ) => {
    const pointMissing = point.missingMeters ?? [];
    if (point.incomplete || pointMissing.length > 0) {
      entry.incomplete = true;
    }
    for (const meter of pointMissing) {
      const trimmed = meter.trim();
      if (trimmed) entry.missingMeters.add(trimmed);
    }
  };
  for (const point of electricity.points) {
    const entry = at(point.periodStart);
    entry.electricity = point.energyKWh;
    entry.crossGapElectricity = point.crossGapKWh ?? 0;
    entry.cost = point.cost ?? null;
    mergeCompleteness(entry, point);
  }
  for (const point of cooling.points) {
    const entry = at(point.periodStart);
    entry.cooling = point.energyKWh;
    mergeCompleteness(entry, point);
  }
  const rows = [...byStart.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([periodStart, energy]): EnergyRow => ({
      periodStart,
      label: bucketLabel(periodStart, granularity, timeZone),
      electricityKWh: energy.electricity,
      crossGapElectricityKWh: energy.crossGapElectricity,
      coolingKWh: energy.cooling,
      cop: evaluatePlantCop(energy.cooling, energy.electricity).cop,
      cost: energy.cost,
      incomplete: energy.incomplete,
      missingMeters: [...energy.missingMeters].sort((left, right) => left.localeCompare(right, 'zh-CN')),
    }));
  // Only buckets with both readings count so a cooling meter gap never understates efficiency;
  // standby electricity in paired buckets stays in the denominator for energy-weighted period COP.
  const paired = rows.filter(
    (row): row is EnergyRow & { readonly electricityKWh: number; readonly coolingKWh: number } =>
      row.electricityKWh !== null
      && row.coolingKWh !== null
      && Number.isFinite(row.electricityKWh)
      && Number.isFinite(row.coolingKWh),
  );
  const sumValues = (values: readonly number[]) => values.reduce((acc, value) => acc + value, 0);
  const copEval: CopEvaluation = paired.length === 0
    ? { cop: null, unavailableReason: 'no-data' }
    : evaluatePlantCop(
        sumValues(paired.map((row) => row.coolingKWh)),
        sumValues(paired.map((row) => row.electricityKWh)),
      );
  let peak: EnergyRow | null = null;
  for (const row of rows) if (row.electricityKWh !== null && (peak === null || row.electricityKWh > peak.electricityKWh!)) peak = row;
  const excluded = (series: EnergySeriesResponse) => series.metadata.qualitySummary.suspect + series.metadata.qualitySummary.invalid;
  const missingSet = new Set<string>();
  let incompleteBuckets = 0;
  for (const row of rows) {
    if (row.incomplete) incompleteBuckets += 1;
    for (const meter of row.missingMeters) missingSet.add(meter);
  }
  const missingMeters = [...missingSet].sort((left, right) => left.localeCompare(right, 'zh-CN'));
  return {
    granularity,
    rows,
    electricityKWh: total(electricity, granularity),
    coolingKWh: total(cooling, granularity),
    cop: copEval.cop,
    copUnavailableReason: copEval.unavailableReason,
    peak,
    dataWatermark: earlier(electricity.metadata.dataWatermark, cooling.metadata.dataWatermark),
    excludedIntervals: excluded(electricity) + excluded(cooling),
    crossGapElectricityKWh: electricity.crossGap?.energyKWh ?? 0,
    crossGapCost: electricity.crossGap?.cost ?? null,
    incompleteBuckets,
    missingMeters,
  };
}

/**
 * The same span of the period before, ending where the current period starts: yesterday up to
 * this time, the seven days before, last month or last year to the same point.
 */
export function previousWindow(period: EnergyPeriod, now: Date, timeZone: string): { granularity: Granularity; from: Date; to: Date } {
  const current = periodWindow(period, now, timeZone);
  const start = wallTime(current.from, timeZone);
  const from = period === 'today' ? siteMidnight(start.year, start.month, start.day - 1, timeZone)
    : period === '7d' ? siteMidnight(start.year, start.month, start.day - 7, timeZone)
    : period === 'month' ? siteMidnight(start.year, start.month - 1, 1, timeZone)
    : siteMidnight(start.year - 1, 1, 1, timeZone);
  const to = new Date(Math.min(from.getTime() + (now.getTime() - current.from.getTime()), current.from.getTime()));
  return { granularity: current.granularity, from, to };
}

/** A bucket's place in its period: hour of the day, day of the window or month of the year. */
function bucketOrdinal(periodStart: string, from: Date, granularity: Granularity, timeZone: string): number {
  const at = wallTime(new Date(periodStart), timeZone);
  const start = wallTime(from, timeZone);
  if (granularity === 'month') return (at.year - start.year) * 12 + at.month - start.month;
  const days = (Date.UTC(at.year, at.month - 1, at.day) - Date.UTC(start.year, start.month - 1, start.day)) / 86_400_000;
  return granularity === 'hour' ? days * 24 + at.hour : days;
}

/** The start of the bucket at an ordinal of the period starting at `from`. */
function bucketAt(from: Date, granularity: Granularity, ordinal: number, timeZone: string): Date {
  const start = wallTime(from, timeZone);
  if (granularity === 'month') return siteMidnight(start.year, start.month + ordinal, 1, timeZone);
  if (granularity === 'day') return siteMidnight(start.year, start.month, start.day + ordinal, timeZone);
  return new Date(siteMidnight(start.year, start.month, start.day, timeZone).getTime() + ordinal * HOUR_MS);
}

export interface BucketComparison {
  readonly label: string;
  /** Where the bucket and the one it is compared with start. */
  readonly start: string;
  readonly previousStart: string;
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  readonly cop: number | null;
  readonly previousElectricityKWh: number | null;
  readonly previousCoolingKWh: number | null;
  readonly previousCop: number | null;
  readonly cost: EnergyCost | null;
  readonly previousCost: EnergyCost | null;
  /** The current bucket has ended; the one still running holds partial energy. */
  readonly complete: boolean;
  readonly incomplete: boolean;
  readonly missingMeters: readonly string[];
}

/**
 * The current period's buckets against the previous period's, lined up by their place in the
 * period and labelled as the current period's buckets.
 */
export function comparePeriods(
  current: EnergySummary,
  previous: EnergySummary,
  { from, previousFrom, now, timeZone }: { from: Date; previousFrom: Date; now: Date; timeZone: string },
): BucketComparison[] {
  const { granularity } = current;
  const byOrdinal = (summary: EnergySummary, start: Date) =>
    new Map(summary.rows.map((row) => [bucketOrdinal(row.periodStart, start, granularity, timeZone), row]));
  const currentRows = byOrdinal(current, from);
  const previousRows = byOrdinal(previous, previousFrom);
  return [...new Set([...currentRows.keys(), ...previousRows.keys()])].sort((left, right) => left - right).map((ordinal) => {
    const row = currentRows.get(ordinal);
    const before = previousRows.get(ordinal);
    const start = bucketAt(from, granularity, ordinal, timeZone).toISOString();
    return {
      label: bucketLabel(start, granularity, timeZone),
      start,
      previousStart: bucketAt(previousFrom, granularity, ordinal, timeZone).toISOString(),
      electricityKWh: row?.electricityKWh ?? null,
      coolingKWh: row?.coolingKWh ?? null,
      cop: row?.cop ?? null,
      previousElectricityKWh: before?.electricityKWh ?? null,
      previousCoolingKWh: before?.coolingKWh ?? null,
      previousCop: before?.cop ?? null,
      cost: row?.cost ?? null,
      previousCost: before?.cost ?? null,
      complete: bucketAt(from, granularity, ordinal + 1, timeZone) <= now,
      incomplete: row?.incomplete ?? false,
      missingMeters: row?.missingMeters ?? [],
    };
  });
}

const KWH = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 });

/** How a bucket is named in a question to the assistant: its full Site-local date, and its hour for hourly buckets. */
function bucketPhrase(start: string, granularity: Granularity, timeZone: string, withYear: boolean): string {
  const local = wallTime(new Date(start), timeZone);
  const year = withYear ? `${local.year}年` : '';
  if (granularity === 'month') return `${year}${local.month}月`;
  const date = `${year}${local.month}月${local.day}日`;
  if (granularity === 'hour') {
    const hour = String(local.hour).padStart(2, '0');
    return `${date} ${hour}:00–${String((local.hour + 1) % 24).padStart(2, '0')}:00`;
  }
  if (!withYear) return date;
  return `${date}（${WEEKDAYS[new Date(Date.UTC(local.year, local.month - 1, local.day)).getUTCDay()]}）`;
}

/** The question 解释 hands the assistant for one bucket: its date, its energy and what it is compared with. */
export function explainQuestion(bucket: BucketComparison, granularity: Granularity, timeZone: string): string {
  const phrase = bucketPhrase(bucket.start, granularity, timeZone, true);
  // A space only after a trailing time, as in 18:00–19:00 的.
  const subject = /\d$/u.test(phrase) ? `${phrase} ` : phrase;
  const own = granularity === 'hour' ? '这一小时' : granularity === 'day' ? '当天' : '当月';
  if (bucket.electricityKWh === null) return `${subject}的空调用电情况怎么样？`;
  if (bucket.previousElectricityKWh === null || bucket.previousElectricityKWh === bucket.electricityKWh) {
    return `${subject}的空调用电 ${KWH.format(bucket.electricityKWh)} kWh，用电正常吗？`;
  }
  const sameYear = wallTime(new Date(bucket.previousStart), timeZone).year === wallTime(new Date(bucket.start), timeZone).year;
  const before = granularity === 'hour'
    ? `${bucketPhrase(bucket.previousStart, 'day', timeZone, false)}同一时段`
    : bucketPhrase(bucket.previousStart, granularity, timeZone, !sameYear);
  const direction = bucket.electricityKWh > bucket.previousElectricityKWh ? '多' : '少';
  return `为什么 ${subject}的空调用电比 ${before} ${direction}？${own} ${KWH.format(bucket.electricityKWh)} kWh，${before} ${KWH.format(bucket.previousElectricityKWh)} kWh。`;
}

/** Buckets for a chart: a bucket still running holds partial energy that would read as a drop, so its current values are left out. */
export function completeOnly(buckets: readonly BucketComparison[]): BucketComparison[] {
  return buckets.map((bucket) => (bucket.complete ? bucket : { ...bucket, electricityKWh: null, coolingKWh: null, cop: null }));
}

export interface CompletedPeriodTotals {
  readonly electricityKWh: number | null;
  readonly coolingKWh: number | null;
  readonly cop: number | null;
  readonly costBuckets: readonly { readonly electricityKWh: number | null; readonly cost: EnergyCost | null }[];
}

function summarizeCompletedSide(
  rows: readonly BucketComparison[],
  pick: (row: BucketComparison) => {
    readonly electricityKWh: number | null;
    readonly coolingKWh: number | null;
    readonly cost: EnergyCost | null;
  },
): CompletedPeriodTotals {
  let electricitySum = 0;
  let hasElectricity = false;
  let coolingSum = 0;
  let hasCooling = false;
  let pairedElecSum = 0;
  let pairedCoolSum = 0;
  let hasPaired = false;
  const costBuckets: { readonly electricityKWh: number | null; readonly cost: EnergyCost | null }[] = [];

  for (const row of rows) {
    const value = pick(row);
    if (value.electricityKWh !== null && Number.isFinite(value.electricityKWh)) {
      electricitySum += value.electricityKWh;
      hasElectricity = true;
    }
    if (value.coolingKWh !== null && Number.isFinite(value.coolingKWh)) {
      coolingSum += value.coolingKWh;
      hasCooling = true;
    }
    if (
      value.electricityKWh !== null &&
      value.coolingKWh !== null &&
      Number.isFinite(value.electricityKWh) &&
      Number.isFinite(value.coolingKWh)
    ) {
      pairedElecSum += value.electricityKWh;
      pairedCoolSum += value.coolingKWh;
      hasPaired = true;
    }
    costBuckets.push({ electricityKWh: value.electricityKWh, cost: value.cost });
  }

  return {
    electricityKWh: hasElectricity ? electricitySum : null,
    coolingKWh: hasCooling ? coolingSum : null,
    cop: hasPaired ? evaluatePlantCop(pairedCoolSum, pairedElecSum).cop : null,
    costBuckets,
  };
}

/** Totals across completed buckets only, so today's KPI deltas compare identical full hours with yesterday. */
export function completedComparisonTotals(hours: readonly BucketComparison[]): {
  readonly current: CompletedPeriodTotals;
  readonly previous: CompletedPeriodTotals;
} {
  const completed = hours.filter((row) => row.complete);
  return {
    current: summarizeCompletedSide(completed, (row) => ({
      electricityKWh: row.electricityKWh,
      coolingKWh: row.coolingKWh,
      cost: row.cost,
    })),
    previous: summarizeCompletedSide(completed, (row) => ({
      electricityKWh: row.previousElectricityKWh,
      coolingKWh: row.previousCoolingKWh,
      cost: row.previousCost,
    })),
  };
}

/** The fractional change from `previous` to `current`; null when either is unknown or the base is zero. */
export function relativeChange(current: number | null, previous: number | null): number | null {
  if (current === null || previous === null || previous === 0) return null;
  return (current - previous) / previous;
}

export interface HeatmapDay {
  readonly label: string;
  /** Energy per Site-local hour 0–23; null where the hour has no reading yet. */
  readonly values: readonly (number | null)[];
}

/** Hourly energy laid out as Site-local days by hour of day, oldest day first. */
export function hourlyHeatmap(series: EnergySeriesResponse, timeZone: string, days: readonly string[]): HeatmapDay[] {
  const byDay = new Map(days.map((day) => [day, Array.from({ length: 24 }, (): number | null => null)]));
  for (const point of series.points) {
    const local = wallTime(new Date(point.periodStart), timeZone);
    const key = `${local.year}-${String(local.month).padStart(2, '0')}-${String(local.day).padStart(2, '0')}`;
    const hours = byDay.get(key);
    if (hours) hours[local.hour] = point.energyKWh;
  }
  return days.map((day) => {
    const [year, month, date] = day.split('-').map(Number);
    const weekday = WEEKDAYS[new Date(Date.UTC(year, month - 1, date)).getUTCDay()];
    return { label: `${month}/${date} ${weekday}`, values: byDay.get(day)! };
  });
}

/** The last `count` Site-local days as YYYY-MM-DD, oldest first, today last. */
/** The Site-local calendar day an instant falls on, as YYYY-MM-DD. */
export function siteDayKey(instant: Date, timeZone: string): string {
  const local = wallTime(instant, timeZone);
  return `${local.year}-${String(local.month).padStart(2, '0')}-${String(local.day).padStart(2, '0')}`;
}

export function siteDays(now: Date, timeZone: string, count: number): string[] {
  return Array.from({ length: count }, (_, index) => siteDayKey(new Date(now.getTime() - (count - 1 - index) * 86_400_000), timeZone));
}

export function retryEnergyQuery(failureCount: number, error: Error): boolean {
  if (failureCount >= 2) return false;
  if (error instanceof PlatformApiError) return error.problem.retryable;
  return true;
}

export function energyRetryDelay(attempt: number): number {
  return Math.min(1000 * 2 ** attempt, 4000);
}
