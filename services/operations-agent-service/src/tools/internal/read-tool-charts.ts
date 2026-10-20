import { MAX_CHART_SERIES, type FindingChart } from '../../agent/index.js';
import { semanticCatalog } from './semantic-catalog.gen.js';

// How each read Tool draws a finding card's chart from what it returned to the model (#640): the
// model names the read, these turn its rows into categories and series, so every number on the
// chart is one the model read. The runtime fits the text to the public contract.

// The words the read Tools put in their query, one for each granularity.
const EVERY: Readonly<Record<string, string>> = Object.freeze({ 按15分钟: '每15分钟', 按小时: '每小时', 按日: '每天', 按月: '每月' });

/** 2026-08-01 → 8月1日. */
const dayWords = (date: string): string => {
  const [, month, day] = date.split('-');
  return `${Number(month)}月${Number(day)}日`;
};

const rangeWords = (startDate: string, endDate: string): string => (
  startDate === endDate ? dayWords(startDate) : `${dayWords(startDate)}–${dayWords(endDate)}`
);

/** A Site time (2026-08-01 07:00) as an axis label: 07:00 within one day, 8/1 07:00 across days, 8/1 per day, 8月 per month. */
const periodLabel = (siteTime: string, granularity: string, oneDay: boolean): string => {
  const [date, clock] = siteTime.split(' ');
  const [, month, day] = date!.split('-');
  if (granularity === '按月') return `${Number(month)}月`;
  const dayLabel = `${Number(month)}/${Number(day)}`;
  if (granularity === '按日') return dayLabel;
  return oneDay ? clock! : `${dayLabel} ${clock}`;
};

const overTime = (granularity: string): FindingChart['kind'] => (granularity === '按小时' || granularity === '按15分钟' ? 'LINE' : 'BAR');

interface EnergySeriesRead {
  readonly query: { readonly energy: string; readonly startDate: string; readonly endDate: string; readonly granularity: string };
  readonly points: readonly { readonly siteTime: string; readonly energyKWh: number }[];
}

/** energy_query_series: hours as a line, days and months as bars. */
export const energySeriesChart = (result: unknown): FindingChart | null => {
  const read = result as EnergySeriesRead;
  if (read.points.length === 0) return null;
  const { energy, startDate, endDate, granularity } = read.query;
  return {
    kind: overTime(granularity),
    title: `${rangeWords(startDate, endDate)}${EVERY[granularity]}${energy}`,
    unit: 'kWh',
    categories: read.points.map((point) => periodLabel(point.siteTime, granularity, startDate === endDate)),
    series: [{ name: energy, values: read.points.map((point) => point.energyKWh) }],
  };
};

interface EnergyComparisonRead {
  readonly baseline: { readonly query: { readonly energy: string; readonly startDate: string; readonly endDate: string }; readonly totalKWh: number | null };
  readonly current: { readonly query: { readonly startDate: string; readonly endDate: string }; readonly totalKWh: number | null };
  readonly comparison: { readonly absoluteChangeKWh: number | null };
}

/** energy_compare_periods: the two periods' totals side by side, the earlier first; none when the Tool could not compare them. */
export const energyComparisonChart = (result: unknown): FindingChart | null => {
  const { baseline, current, comparison } = result as EnergyComparisonRead;
  if (comparison.absoluteChangeKWh === null || baseline.totalKWh === null || current.totalKWh === null) return null;
  return {
    kind: 'BAR',
    title: `两段时间的${baseline.query.energy}`,
    unit: 'kWh',
    categories: [rangeWords(baseline.query.startDate, baseline.query.endDate), rangeWords(current.query.startDate, current.query.endDate)],
    series: [{ name: baseline.query.energy, values: [baseline.totalKWh, current.totalKWh] }],
  };
};

interface EquipmentEnergyRead {
  readonly query: { readonly startDate: string; readonly endDate: string };
  readonly devices: readonly { readonly name: string; readonly totalKWh: number }[];
}

/** energy_by_equipment: each sub-metered device's total, most first. */
export const equipmentEnergyChart = (result: unknown): FindingChart | null => {
  const read = result as EquipmentEnergyRead;
  if (read.devices.length === 0) return null;
  return {
    kind: 'HORIZONTAL_BAR',
    title: `${rangeWords(read.query.startDate, read.query.endDate)}各设备用电`,
    unit: 'kWh',
    categories: read.devices.map((device) => device.name),
    series: [{ name: '用电', values: read.devices.map((device) => device.totalKWh) }],
  };
};

// Grouping columns by their Chinese titles; every other column but the period is a measure.
const DIMENSION_TITLES: ReadonlySet<string> = new Set(semanticCatalog.views.flatMap((view) => view.dimensions.map((dimension) => dimension.title)));
// Only energy is charted (#640): the Cube model titles its cost measures 电费 and the plant's COP 能效; the rest are kWh.
const isEnergy = (title: string): boolean => !title.includes('电费') && !title.includes('能效');
const numberOrNull = (value: unknown): number | null => (typeof value === 'number' ? value : null);

interface SemanticRead {
  readonly query: { readonly granularity: string; readonly startDate: string; readonly endDate: string };
  readonly columns: readonly string[];
  readonly rows: readonly Readonly<Record<string, unknown>>[];
}

/**
 * energy_semantic_query, energy measures only: periods as a line (hours) or bars, one series per
 * measure; one period grouped by a dimension as one bar per value (devices ranked); several periods
 * grouped as one series per value. Two groupings, or more values than series, are not charted.
 */
export const semanticChart = (result: unknown): FindingChart | null => {
  const read = result as SemanticRead;
  const first = read.rows[0];
  if (first === undefined) return null;
  const { granularity, startDate, endDate } = read.query;
  const present = read.columns.filter((title) => title in first);
  // A grouping with one value groups nothing: 能源类型 用电 alone.
  const groupings = present.filter((title) => DIMENSION_TITLES.has(title) && new Set(read.rows.map((row) => row[title])).size > 1);
  const measures = present.filter((title) => !DIMENSION_TITLES.has(title) && isEnergy(title)).slice(0, MAX_CHART_SERIES);
  const measure = measures[0];
  if (measure === undefined || groupings.length > 1) return null;
  const label = (siteTime: string) => periodLabel(siteTime, granularity, startDate === endDate);
  const range = rangeWords(startDate, endDate);
  const grouping = groupings[0];
  if (grouping === undefined) {
    return {
      kind: overTime(granularity),
      title: `${range}${EVERY[granularity]}${measures.join('、')}`,
      unit: 'kWh',
      categories: read.rows.map((row) => label(String(row.siteTime))),
      series: measures.map((title) => ({ name: title, values: read.rows.map((row) => numberOrNull(row[title])) })),
    };
  }
  const periods = [...new Set(read.rows.map((row) => String(row.siteTime)))];
  const valueOf = (row: Readonly<Record<string, unknown>>) => String(row[grouping] ?? '');
  if (periods.length === 1) {
    const rows = grouping === '设备' ? [...read.rows].sort((a, b) => (numberOrNull(b[measure]) ?? 0) - (numberOrNull(a[measure]) ?? 0)) : read.rows;
    return {
      kind: grouping === '设备' ? 'HORIZONTAL_BAR' : 'BAR',
      title: `${range}各${grouping}的${measure}`,
      unit: 'kWh',
      categories: rows.map(valueOf),
      series: [{ name: measure, values: rows.map((row) => numberOrNull(row[measure])) }],
    };
  }
  const values = [...new Set(read.rows.map(valueOf))];
  if (values.length > MAX_CHART_SERIES) return null;
  return {
    kind: overTime(granularity),
    title: `${range}${EVERY[granularity]}各${grouping}的${measure}`,
    unit: 'kWh',
    categories: periods.map(label),
    series: values.map((value) => ({
      name: value,
      values: periods.map((period) => numberOrNull(read.rows.find((row) => String(row.siteTime) === period && valueOf(row) === value)?.[measure])),
    })),
  };
};

interface HistoryRead {
  readonly query: { readonly startDate: string; readonly endDate: string; readonly granularity: string };
  readonly series: readonly {
    readonly name?: string;
    readonly unit: string | null;
    readonly points: readonly { readonly siteTime: string; readonly average?: number; readonly increase?: number }[];
  }[];
}

/** telemetry_query_history: readings sharing the first one's unit as lines, averages for readings and increases for counters. */
export const historyChart = (result: unknown): FindingChart | null => {
  const read = result as HistoryRead;
  const numeric = read.series.filter((series) => series.points.some((point) => point.average !== undefined || point.increase !== undefined));
  const first = numeric[0];
  if (first === undefined) return null;
  const charted = numeric.filter((series) => series.unit === first.unit).slice(0, MAX_CHART_SERIES);
  const names = charted.map((series) => series.name ?? '读数');
  const { startDate, endDate, granularity } = read.query;
  const periods = [...new Set(charted.flatMap((series) => series.points.map((point) => point.siteTime)))].sort();
  return {
    kind: 'LINE',
    title: `${rangeWords(startDate, endDate)}${EVERY[granularity]}${names.join('、')}`,
    unit: first.unit ?? '',
    categories: periods.map((siteTime) => periodLabel(siteTime, granularity, startDate === endDate)),
    series: charted.map((series, index) => ({
      name: names[index]!,
      values: periods.map((siteTime) => {
        const point = series.points.find((candidate) => candidate.siteTime === siteTime);
        return point?.average ?? point?.increase ?? null;
      }),
    })),
  };
};
