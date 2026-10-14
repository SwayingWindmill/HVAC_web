import { useQueries } from '@tanstack/react-query';
import { getRouteApi } from '@tanstack/react-router';
import { deviceRecentHistoryQuery, type HourlyPoint } from '../systems-devices/device-history';
import type { PlantView } from './plant-model';

const siteRoute = getRouteApi('/_app/_site');

export interface PlantHistory {
  /** Quarter-hour points per source key, oldest first; absent when nothing was recorded. */
  readonly series: ReadonlyMap<string, readonly HourlyPoint[]>;
  readonly isPending: boolean;
  readonly isError: boolean;
}

/**
 * The last `hours` of the plant readings named by source key, read from Device History one
 * device at a time (each reading belongs to the one device whose point carries that key).
 */
export function usePlantHistory(plant: PlantView, sourceKeys: readonly string[], hours: number): PlantHistory {
  const { site, principal } = siteRoute.useRouteContext();
  const allowed = principal.authorization.capabilities.includes('telemetry.history.read');

  // Which device reports each key, and the point code its history is stored under.
  const byDevice = new Map<string, { readonly deviceId: string; readonly points: Map<string, string> }>();
  for (const sourceKey of sourceKeys) {
    for (const device of plant.devices) {
      const point = device.row.telemetryPoints.find((candidate) => candidate.sourceKey === sourceKey && candidate.status === 'ACTIVE');
      if (!point) continue;
      const entry = byDevice.get(device.deviceId) ?? { deviceId: device.deviceId, points: new Map<string, string>() };
      entry.points.set(point.pointCode, sourceKey);
      byDevice.set(device.deviceId, entry);
      break;
    }
  }
  const devices = [...byDevice.values()];

  const results = useQueries({
    queries: devices.map((entry) => ({
      ...deviceRecentHistoryQuery({
        siteId: site.id,
        deviceId: entry.deviceId,
        keys: [...entry.points.keys()],
        hours,
        timezone: site.timezone,
        csrfToken: principal.session.csrfToken,
      }),
      enabled: allowed,
    })),
  });

  const series = new Map<string, readonly HourlyPoint[]>();
  results.forEach((result, index) => {
    for (const [pointCode, sourceKey] of devices[index].points) {
      const points = result.data?.get(pointCode);
      if (points) series.set(sourceKey, points);
    }
  });
  return {
    series,
    isPending: allowed && results.some((result) => result.isPending),
    isError: results.some((result) => result.isError),
  };
}
