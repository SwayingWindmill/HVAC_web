// Energy from the Energy analytics owner: the Site's hourly and daily electricity and cooling, the
// owner cutting the Site's days so the mini program needs no time zone arithmetic.

import { gateway } from './platform';
import type { Site } from './site';
import {
  fetchRecentDays,
  fetchRecentHours,
  queryEnergySeries,
  recentHourlyWindow,
  todayStart,
  type Window,
} from './today';

export { recentHourlyWindow };

export const energySeries = (
  site: Site,
  energyType: 'electricity' | 'cooling',
  granularity: 'hour' | 'day',
  window: Window,
) => queryEnergySeries(gateway, site, energyType, granularity, window).then((response) => response.points);

/** Today's electricity by the hour, for the Site and for each device with its own meter. */
export const electricityByDevice = (site: Site, window: Window) =>
  queryEnergySeries(gateway, site, 'electricity', 'hour', window, 'device');

/** The Site's last 72 hours of hourly electricity or cooling readings. */
export const recentHours = (site: Site, energyType: 'electricity' | 'cooling', now: Date) =>
  fetchRecentHours(gateway, site, energyType, now);

/** The Site's last two days of electricity, enough to find where today began. */
export const recentDays = (site: Site, now: Date) => fetchRecentDays(gateway, site, now);

/** Where today began at the Site; null when it has no electricity reading in the last two days. */
export const siteTodayStart = async (site: Site, now: Date) => todayStart(await recentDays(site, now), now);
