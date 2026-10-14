import { queryOptions, useQuery } from '@tanstack/react-query';
import { intelligencePaths } from '@/api/generated/platformGateway.gen';
import { useEnergyReader } from '@/features/energy-analysis/consumption/query';
import { parseLoadForecast, type LoadForecast, type PublishedForecast } from './forecast-model';

export class ForecastRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

/** The latest published Site load forecast; null until the Forecast owner has published one. */
export function siteLoadForecastQuery(siteId: string) {
  return queryOptions({
    queryKey: ['forecast', siteId, 'site-load'],
    queryFn: async ({ signal }): Promise<LoadForecast | null> => {
      const response = await fetch(intelligencePaths.siteLoadForecast.replace('{siteId}', siteId), {
        credentials: 'same-origin', signal, headers: { Accept: 'application/json, application/problem+json' },
      });
      if (response.status === 404) return null;
      const payload = await response.json().catch(() => null);
      if (!response.ok) throw new ForecastRequestError(response.status, payload?.detail ?? '预测服务暂时不可用');
      return parseLoadForecast(payload as PublishedForecast);
    },
    // A new forecast is published every hour; five minutes keeps the page close to it.
    refetchInterval: 5 * 60_000,
    retry: false,
  });
}

/** The Site's HVAC load over the last 24 hours, hour by hour, from metered electricity. */
export function useRecentLoad() {
  const { site, read } = useEnergyReader();
  return useQuery({
    queryKey: ['energy-series', site.id, 'recent-load'],
    queryFn: async ({ signal }) => {
      const now = new Date();
      const series = await read('electricity', 'hour', new Date(now.getTime() - 24 * 3_600_000), now, signal);
      const byHour = new Map(series.points.map((point) => [Date.parse(point.periodStart), point.energyKWh]));
      // Every complete hour of the last day, unmetered ones as null so the chart shows the gap;
      // the hour still running holds partial energy, which would read as a fall in load.
      const lastComplete = Math.floor(now.getTime() / 3_600_000) * 3_600_000 - 3_600_000;
      return Array.from({ length: 24 }, (_, index) => {
        const start = lastComplete - (23 - index) * 3_600_000;
        return { periodStart: new Date(start).toISOString(), electricityKWh: byHour.get(start) ?? null };
      });
    },
    refetchInterval: 5 * 60_000,
    retry: false,
  });
}
