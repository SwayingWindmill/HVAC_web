// The Site load forecast as the Forecast owner publishes it: quarter-hour points from the
// forecast origin onward, each with a band when a model produced it, none when it fell back
// to repeating the last observation.

export type ForecastQuality = 'VALID' | 'FALLBACK';

export interface LoadForecastPoint {
  readonly at: string;
  readonly loadKW: number;
  readonly lowerKW: number | null;
  readonly upperKW: number | null;
  readonly quality: ForecastQuality;
}

export interface LoadForecast {
  readonly origin: string;
  readonly quality: ForecastQuality;
  readonly points: readonly LoadForecastPoint[];
}

interface PublishedPoint {
  readonly forecast_for: string;
  readonly value: number;
  readonly lower_bound: number | null;
  readonly upper_bound: number | null;
  readonly quality: ForecastQuality;
}

export interface PublishedForecast {
  readonly snapshot: { readonly forecastOrigin: string; readonly quality: ForecastQuality };
  readonly points: readonly PublishedPoint[];
}

export function parseLoadForecast(published: PublishedForecast): LoadForecast {
  return {
    origin: published.snapshot.forecastOrigin,
    quality: published.snapshot.quality,
    points: [...published.points]
      .sort((left, right) => Date.parse(left.forecast_for) - Date.parse(right.forecast_for))
      .map((point) => ({
        at: new Date(point.forecast_for).toISOString(),
        loadKW: point.value,
        lowerKW: point.lower_bound,
        upperKW: point.upper_bound,
        quality: point.quality,
      })),
  };
}

const DAY_MS = 86_400_000;

/** The next 24 hours of a forecast; the model reaches further so Optimization always has a full day ahead. */
export function nextDay(forecast: LoadForecast): LoadForecast {
  const end = Date.parse(forecast.origin) + DAY_MS;
  return { ...forecast, points: forecast.points.filter((point) => Date.parse(point.at) <= end) };
}

const QUARTER_HOUR = 0.25;

export function forecastSummary(forecast: LoadForecast) {
  let peak = forecast.points[0];
  let trough = forecast.points[0];
  let energyKWh = 0;
  for (const point of forecast.points) {
    if (point.loadKW > peak.loadKW) peak = point;
    if (point.loadKW < trough.loadKW) trough = point;
    energyKWh += point.loadKW * QUARTER_HOUR;
  }
  return {
    peak: { at: peak.at, loadKW: peak.loadKW },
    trough: { at: trough.at, loadKW: trough.loadKW },
    energyKWh,
  };
}

export interface ForecastChartRow {
  readonly at: number;
  readonly actualKW: number | null;
  readonly forecastKW: number | null;
  /** Lower and upper bound; null where the point has no band. */
  readonly band: readonly [number, number] | null;
}

/**
 * Actual hours and forecast quarter-hours on one time axis. An hour's energy in kWh is its average
 * load in kW, so the actual line and the forecast compare directly.
 */
export function forecastChartRows(
  actualHours: readonly { readonly periodStart: string; readonly electricityKWh: number | null }[],
  forecast: LoadForecast,
): ForecastChartRow[] {
  const actual = actualHours.map((hour): ForecastChartRow => ({
    at: Date.parse(hour.periodStart), actualKW: hour.electricityKWh, forecastKW: null, band: null,
  }));
  const ahead = forecast.points.map((point): ForecastChartRow => ({
    at: Date.parse(point.at),
    actualKW: null,
    forecastKW: point.loadKW,
    band: point.lowerKW !== null && point.upperKW !== null ? [point.lowerKW, point.upperKW] : null,
  }));
  return [...actual, ...ahead].sort((left, right) => left.at - right.at);
}

export interface HourlyForecast {
  readonly label: string;
  /** The first forecast instant in the hour. */
  readonly start: string;
  readonly loadKW: number;
  /** Averages of the hour's bounds; null when any point in the hour has none. */
  readonly lowerKW: number | null;
  readonly upperKW: number | null;
  readonly peak: boolean;
}

/** The forecast averaged over each Site-local hour, in time order, with the highest hour marked. */
export function hourlyForecast(forecast: LoadForecast, timeZone: string): HourlyForecast[] {
  const hourLabel = new Intl.DateTimeFormat('en-GB', { timeZone, hour: '2-digit', hourCycle: 'h23' });
  const groups: { label: string; points: LoadForecastPoint[] }[] = [];
  for (const point of forecast.points) {
    const label = `${hourLabel.format(new Date(point.at))}:00`;
    const last = groups.at(-1);
    if (last?.label === label) last.points.push(point);
    else groups.push({ label, points: [point] });
  }
  const average = (values: readonly number[]) => values.reduce((sum, value) => sum + value, 0) / values.length;
  const hours = groups.map(({ label, points }) => {
    const banded = points.every((point) => point.lowerKW !== null && point.upperKW !== null);
    return {
      label,
      start: points[0].at,
      loadKW: average(points.map((point) => point.loadKW)),
      lowerKW: banded ? average(points.map((point) => point.lowerKW!)) : null,
      upperKW: banded ? average(points.map((point) => point.upperKW!)) : null,
    };
  });
  const peak = Math.max(...hours.map((hour) => hour.loadKW));
  return hours.map((hour) => ({ ...hour, peak: hour.loadKW === peak }));
}
