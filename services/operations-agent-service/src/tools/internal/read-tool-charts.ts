import type { FindingChart } from '../../agent/index.js';

// How each read Tool draws a finding card's chart from what it returned to the model (#640): the
// model names the read, these turn its rows into categories and series, so every number on the
// chart is one the model read.

// The words readEnergy puts in its query, one for each granularity.
const EVERY: Readonly<Record<string, string>> = Object.freeze({ 按小时: '每小时', 按日: '每天', 按月: '每月' });

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
