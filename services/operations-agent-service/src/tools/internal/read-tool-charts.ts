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
