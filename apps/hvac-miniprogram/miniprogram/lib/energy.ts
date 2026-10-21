// Energy from the Energy analytics owner: the Site's hourly and daily electricity and cooling, the
// owner cutting the Site's days so the mini program needs no time zone arithmetic.

import { gateway } from './platform';
import type { Site } from './site';
import { todayStart, type DayPoint, type HourPoint, type Window } from './today';

const DAY_MS = 86_400_000;

interface Series {
  readonly points: HourPoint[];
  /** With breakdown=device: each sub-metered device's energy, already part of the Site total. */
  readonly devices?: { readonly deviceId: string; readonly points: DayPoint[] }[];
}

const request = (site: Site, energyType: 'electricity' | 'cooling', granularity: 'hour' | 'day', window: Window, breakdown?: 'device') =>
  gateway.post<Series>('/api/v1/analytics/energy-series', {
    tenantId: site.tenantId, siteId: site.id, energyType, granularity, timezone: site.timezone,
    from: window.from, to: window.to, qualityPolicy: 'VALID_ONLY', ...(breakdown ? { breakdown } : {}),
  });

export const energySeries = (site: Site, energyType: 'electricity' | 'cooling', granularity: 'hour' | 'day', window: Window) =>
  request(site, energyType, granularity, window).then((response) => response.points);

/** Today's electricity by the hour, for the Site and for each device with its own meter. */
export const electricityByDevice = (site: Site, window: Window) => request(site, 'electricity', 'hour', window, 'device');

/** The Site's last two days of electricity, enough to find where today began. */
export const recentDays = (site: Site, now: Date) =>
  energySeries(site, 'electricity', 'day', { from: new Date(now.getTime() - 2 * DAY_MS).toISOString(), to: now.toISOString() });

/** Where today began at the Site; null when it has no electricity reading in the last two days. */
export const siteTodayStart = async (site: Site, now: Date) => todayStart(await recentDays(site, now), now);
