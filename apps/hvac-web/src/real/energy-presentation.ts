import type { EnergySeriesPoint } from '../api/energy-analytics';

export interface EnergyPointSummary {
  readonly total: number | null;
  readonly average: number | null;
  readonly peak: EnergySeriesPoint | null;
  readonly valley: EnergySeriesPoint | null;
  readonly measuredCount: number;
}

export interface EnergyCalendarCell {
  readonly date: string;
  readonly day: number;
  readonly weekday: number;
  readonly inPeriod: boolean;
  readonly point: EnergySeriesPoint | null;
  readonly previousPoint: EnergySeriesPoint | null;
}

export interface EnergyPeriodSlot {
  readonly key: string;
  readonly label: string;
  readonly point: EnergySeriesPoint | null;
  readonly previousPoint: EnergySeriesPoint | null;
}

export function sortEnergyPoints(points: readonly EnergySeriesPoint[]): EnergySeriesPoint[] {
  return [...points].sort((left, right) => Date.parse(left.periodStart) - Date.parse(right.periodStart));
}

export function summarizeEnergyPoints(points: readonly EnergySeriesPoint[]): EnergyPointSummary {
  if (points.length === 0) {
    return { total: null, average: null, peak: null, valley: null, measuredCount: 0 };
  }
  const sorted = sortEnergyPoints(points);
  const total = sorted.reduce((sum, point) => sum + point.energyKWh, 0);
  return {
    total,
    average: total / sorted.length,
    peak: sorted.reduce((peak, point) => point.energyKWh > peak.energyKWh ? point : peak, sorted[0]),
    valley: sorted.reduce((valley, point) => point.energyKWh < valley.energyKWh ? point : valley, sorted[0]),
    measuredCount: sorted.length,
  };
}

export function localDateKey(instant: string, timezone: string): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(new Date(instant));
  const value = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return `${value.year}-${value.month}-${value.day}`;
}

export function localMonthKey(instant: string, timezone: string): string {
  return localDateKey(instant, timezone).slice(0, 7);
}

function pointMapByDate(points: readonly EnergySeriesPoint[], timezone: string): Map<string, EnergySeriesPoint> {
  return new Map(sortEnergyPoints(points).map((point) => [localDateKey(point.periodStart, timezone), point]));
}

function pointMapByMonth(points: readonly EnergySeriesPoint[], timezone: string): Map<string, EnergySeriesPoint> {
  return new Map(sortEnergyPoints(points).map((point) => [localMonthKey(point.periodStart, timezone), point]));
}

function addCalendarDays(date: string, offset: number): string {
  const [year, month, day] = date.split('-').map(Number);
  const next = new Date(Date.UTC(year, month - 1, day + offset));
  return next.toISOString().slice(0, 10);
}

export function buildWeekSlots(
  anchor: string,
  points: readonly EnergySeriesPoint[],
  previousPoints: readonly EnergySeriesPoint[],
  timezone: string,
): EnergyPeriodSlot[] {
  const current = pointMapByDate(points, timezone);
  const previous = sortEnergyPoints(previousPoints);
  const labels = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];
  return labels.map((label, index) => {
    const key = addCalendarDays(anchor, index);
    return { key, label, point: current.get(key) ?? null, previousPoint: previous[index] ?? null };
  });
}

export function buildMonthCalendar(
  anchor: string,
  points: readonly EnergySeriesPoint[],
  previousPoints: readonly EnergySeriesPoint[],
  timezone: string,
): EnergyCalendarCell[] {
  const [year, month] = anchor.split('-').map(Number);
  const firstWeekday = (new Date(Date.UTC(year, month - 1, 1)).getUTCDay() + 6) % 7;
  const daysInMonth = new Date(Date.UTC(year, month, 0)).getUTCDate();
  const current = pointMapByDate(points, timezone);
  const previous = sortEnergyPoints(previousPoints);
  const cells: EnergyCalendarCell[] = [];
  for (let index = 0; index < 42; index += 1) {
    const day = index - firstWeekday + 1;
    const inPeriod = day >= 1 && day <= daysInMonth;
    const date = inPeriod
      ? `${String(year).padStart(4, '0')}-${String(month).padStart(2, '0')}-${String(day).padStart(2, '0')}`
      : addCalendarDays(anchor, day - 1);
    cells.push({
      date,
      day: Number(date.slice(8, 10)),
      weekday: index % 7,
      inPeriod,
      point: inPeriod ? current.get(date) ?? null : null,
      previousPoint: inPeriod ? previous[day - 1] ?? null : null,
    });
  }
  return cells;
}

export function buildYearSlots(
  anchor: string,
  points: readonly EnergySeriesPoint[],
  previousPoints: readonly EnergySeriesPoint[],
  timezone: string,
): EnergyPeriodSlot[] {
  const year = Number(anchor.slice(0, 4));
  const current = pointMapByMonth(points, timezone);
  const previous = sortEnergyPoints(previousPoints);
  return Array.from({ length: 12 }, (_, index) => {
    const key = `${year}-${String(index + 1).padStart(2, '0')}`;
    return {
      key,
      label: `${index + 1} 月`,
      point: current.get(key) ?? null,
      previousPoint: previous[index] ?? null,
    };
  });
}

export function buildCumulativeEnergy(points: readonly EnergySeriesPoint[]): Array<readonly [number, number]> {
  let cumulative = 0;
  return sortEnergyPoints(points).map((point) => {
    cumulative += point.energyKWh;
    return [Date.parse(point.periodStart), cumulative] as const;
  });
}

function csvCell(value: string | number): string {
  const text = String(value);
  return /[",\n]/.test(text) ? `"${text.split('"').join('""')}"` : text;
}

export function buildEnergyCsv(
  currentLabel: string,
  currentPoints: readonly EnergySeriesPoint[],
  previousLabel: string,
  previousPoints: readonly EnergySeriesPoint[],
): string {
  const rows: Array<Array<string | number>> = [['period', 'periodStart', 'periodEnd', 'energyKWh']];
  for (const point of sortEnergyPoints(currentPoints)) {
    rows.push([currentLabel, point.periodStart, point.periodEnd, point.energyKWh]);
  }
  for (const point of sortEnergyPoints(previousPoints)) {
    rows.push([previousLabel, point.periodStart, point.periodEnd, point.energyKWh]);
  }
  return rows.map((row) => row.map(csvCell).join(',')).join('\n');
}
