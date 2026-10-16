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

/** The forecast points from one instant up to another. */
export function forecastBetween(forecast: LoadForecast, from: number, to: number): LoadForecastPoint[] {
  return forecast.points.filter((point) => Date.parse(point.at) >= from && Date.parse(point.at) < to);
}

export function forecastPeak(points: readonly LoadForecastPoint[]): { at: string; loadKW: number } | null {
  let peak: LoadForecastPoint | null = null;
  for (const point of points) if (!peak || point.loadKW > peak.loadKW) peak = point;
  return peak ? { at: peak.at, loadKW: peak.loadKW } : null;
}

export interface HourlyForecast {
  readonly label: string;
  /** The first forecast instant in the hour. */
  readonly start: string;
  readonly loadKW: number;
  /** Averages of the hour's bounds; null when any point in the hour has none. */
  readonly lowerKW: number | null;
  readonly upperKW: number | null;
}

/** Forecast points averaged over each Site-local hour, in time order. */
export function hourlyForecast(points: readonly LoadForecastPoint[], timeZone: string): HourlyForecast[] {
  const hourLabel = new Intl.DateTimeFormat('en-GB', { timeZone, hour: '2-digit', hourCycle: 'h23' });
  const groups: { label: string; points: LoadForecastPoint[] }[] = [];
  for (const point of points) {
    const label = `${hourLabel.format(new Date(point.at))}:00`;
    const last = groups.at(-1);
    if (last?.label === label) last.points.push(point);
    else groups.push({ label, points: [point] });
  }
  const average = (values: readonly number[]) => values.reduce((sum, value) => sum + value, 0) / values.length;
  return groups.map(({ label, points: hour }) => {
    const banded = hour.every((point) => point.lowerKW !== null && point.upperKW !== null);
    return {
      label,
      start: hour[0].at,
      loadKW: average(hour.map((point) => point.loadKW)),
      lowerKW: banded ? average(hour.map((point) => point.lowerKW!)) : null,
      upperKW: banded ? average(hour.map((point) => point.upperKW!)) : null,
    };
  });
}
