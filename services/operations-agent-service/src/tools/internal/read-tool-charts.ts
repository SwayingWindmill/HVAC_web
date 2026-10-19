import type { FindingChart } from '../../agent/index.js';
import { semanticCatalog } from './semantic-catalog.gen.js';

// How each read Tool draws a finding card's chart from what it returned to the model (#640): the
// model names the read, these turn its rows into categories and series, so every number on the
// chart is one the model read.

// The words readEnergy puts in its query, one for each granularity.
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
    kind: granularity === '按小时' ? 'LINE' : 'BAR',
    title: `${rangeWords(startDate, endDate)}${EVERY[granularity]}${energy}`,
    unit: 'kWh',
    categories: read.points.map((point) => periodLabel(point.siteTime, granularity, startDate === endDate)),
    series: [{ name: energy, values: read.points.map((point) => point.energyKWh) }],
  };
};

interface EnergyComparisonRead {
  readonly baseline: { readonly query: { readonly energy: string; readonly startDate: string; readonly endDate: string }; readonly totalKWh: number | null };
  readonly current: { readonly query: { readonly startDate: string; readonly endDate: string }; readonly totalKWh: number | null };
}

/** energy_compare_periods: the two periods' totals side by side, the earlier first. */
export const energyComparisonChart = (result: unknown): FindingChart | null => {
  const { baseline, current } = result as EnergyComparisonRead;
  if (baseline.totalKWh === null || current.totalKWh === null) return null;
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
    categories: read.devices.map((device) => device.name.slice(0, 32)),
    series: [{ name: '用电', values: read.devices.map((device) => device.totalKWh) }],
  };
};

// Grouping columns by their Chinese titles; every other column but the period is a measure.
const DIMENSION_TITLES: ReadonlySet<string> = new Set(semanticCatalog.views.flatMap((view) => view.dimensions.map((dimension) => dimension.title)));
/** A measure's unit from its title, as the Cube model names them: 电费 in 元, 能效 without one, energy in kWh. */
const measureUnit = (title: string): string => (title.includes('电费') ? '元' : title.includes('能效') ? '' : 'kWh');
const numberOrNull = (value: unknown): number | null => (typeof value === 'number' ? value : null);

interface SemanticRead {
  readonly query: { readonly granularity: string; readonly startDate: string; readonly endDate: string };
  readonly columns: readonly string[];
  readonly rows: readonly Readonly<Record<string, unknown>>[];
}

/**
 * energy_semantic_query: periods as a line (hours) or bars, one series per measure sharing the first
 * one's unit; one period grouped by a dimension as one bar per value (devices ranked); several
 * periods grouped as one series per value. Two groupings, or more than four values, are not charted.
 */
export const semanticChart = (result: unknown): FindingChart | null => {
  const read = result as SemanticRead;
  const first = read.rows[0];
  if (first === undefined) return null;
  const { granularity, startDate, endDate } = read.query;
  const present = read.columns.filter((title) => title in first);
  // A grouping with one value groups nothing: 能源类型 用电 alone.
  const groupings = present.filter((title) => DIMENSION_TITLES.has(title) && new Set(read.rows.map((row) => row[title])).size > 1);
  const measures = present.filter((title) => !DIMENSION_TITLES.has(title));
  const measure = measures[0];
  if (measure === undefined || groupings.length > 1) return null;
  const unit = measureUnit(measure);
  const periods = [...new Set(read.rows.map((row) => String(row.siteTime)))];
  const label = (siteTime: string) => periodLabel(siteTime, granularity, startDate === endDate);
  const range = rangeWords(startDate, endDate);
  const grouping = groupings[0];
  if (grouping === undefined) {
    const sameUnit = measures.filter((title) => measureUnit(title) === unit).slice(0, 4);
    return {
      kind: granularity === '按小时' ? 'LINE' : 'BAR',
      title: `${range}${EVERY[granularity]}${sameUnit.join('、')}`.slice(0, 64),
      unit,
      categories: read.rows.map((row) => label(String(row.siteTime))),
      series: sameUnit.map((title) => ({ name: title, values: read.rows.map((row) => numberOrNull(row[title])) })),
    };
  }
  const values = [...new Set(read.rows.map((row) => String(row[grouping] ?? '')))];
  if (periods.length === 1) {
    const rows = grouping === '设备' ? [...read.rows].sort((a, b) => (numberOrNull(b[measure]) ?? 0) - (numberOrNull(a[measure]) ?? 0)) : read.rows;
    return {
      kind: grouping === '设备' ? 'HORIZONTAL_BAR' : 'BAR',
      title: `${range}各${grouping}的${measure}`.slice(0, 64),
      unit,
      categories: rows.map((row) => String(row[grouping] ?? '').slice(0, 32)),
      series: [{ name: measure, values: rows.map((row) => numberOrNull(row[measure])) }],
    };
  }
  if (values.length > 4) return null;
  return {
    kind: granularity === '按小时' ? 'LINE' : 'BAR',
    title: `${range}${EVERY[granularity]}各${grouping}的${measure}`.slice(0, 64),
    unit,
    categories: periods.map(label),
    series: values.map((value) => ({
      name: value.slice(0, 32),
      values: periods.map((period) => numberOrNull(read.rows.find((row) => String(row.siteTime) === period && String(row[grouping] ?? '') === value)?.[measure])),
    })),
  };
};

interface HistoryRead {
  readonly query: { readonly startDate: string; readonly endDate: string; readonly granularity: string };
  readonly series: readonly {
    readonly name?: string;
    readonly unit: string;
    readonly points: readonly { readonly siteTime: string; readonly average?: number; readonly increase?: number }[];
  }[];
}

/** telemetry_query_history: readings sharing the first one's unit as lines, averages for readings and increases for counters. */
export const historyChart = (result: unknown): FindingChart | null => {
  const read = result as HistoryRead;
  const numeric = read.series.filter((series) => series.points.some((point) => point.average !== undefined || point.increase !== undefined));
  const first = numeric[0];
  if (first === undefined) return null;
  const charted = numeric.filter((series) => series.unit === first.unit).slice(0, 4);
  const names = charted.map((series) => series.name ?? '读数');
  const { startDate, endDate, granularity } = read.query;
  const periods = [...new Set(charted.flatMap((series) => series.points.map((point) => point.siteTime)))].sort();
  return {
    kind: 'LINE',
    title: `${rangeWords(startDate, endDate)}${EVERY[granularity]}${names.join('、')}`.slice(0, 64),
    unit: first.unit,
    categories: periods.map((siteTime) => periodLabel(siteTime, granularity, startDate === endDate)),
    series: charted.map((series, index) => ({
      name: names[index]!.slice(0, 32),
      values: periods.map((siteTime) => {
        const point = series.points.find((candidate) => candidate.siteTime === siteTime);
        return point?.average ?? point?.increase ?? null;
      }),
    })),
  };
};
