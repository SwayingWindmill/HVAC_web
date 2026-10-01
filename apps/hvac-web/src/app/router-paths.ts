import type { Site } from '@/api/generated/platformGateway.gen';

export type EnergyRoutePeriod = 'year' | 'month' | 'week' | 'day';

export type AssetsDetailTarget =
  | { readonly kind: 'asset'; readonly id: string }
  | { readonly kind: 'device'; readonly id: string };

export type SiteRouteLeaf =
  | 'overview'
  | 'dashboard'
  | 'devices'
  | 'diagnostics'
  | 'issues'
  | 'energy'
  | 'forecast'
  | 'control'
  | 'optimize'
  | 'fdd'
  | 'alarms'
  | 'work-orders'
  | 'ai'
  | 'cost'
  | 'billing'
  | 'settlement'
  | 'monitor'
  | 'bigscreen'
  | 'operations'
  | 'trends'
  | 'efficiency'
  | 'demand'
  | 'opportunities'
  | 'verifications'
  | 'strategies'
  | 'executions'
  | 'mv'
  | 'carbon'
  | 'der'
  | 'data-quality'
  | 'energy-review'
  | 'action-plans'
  | 'reports'
  | 'benchmarking'
  | 'rules'
  | 'management-reviews'
  | 'model';

const UUID_V7_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export function isUUIDv7(value: string): boolean {
  return UUID_V7_PATTERN.test(value);
}

export function siteRoute(site: Pick<Site, 'id'>, leaf: SiteRouteLeaf): string {
  if (!isUUIDv7(site.id)) throw new Error('Site identity must be a Registry UUIDv7.');
  if (leaf === 'control' || leaf === 'trends') return `/sites/${site.id}/operations/${leaf}`;
  if (leaf === 'alarms' || leaf === 'issues' || leaf === 'diagnostics') return `/sites/${site.id}/issues`;
  return `/sites/${site.id}/${leaf}`;
}

export function siteEnergyRoute(site: Pick<Site, 'id'>, period: EnergyRoutePeriod = 'month'): string {
  return `${siteRoute(site, 'energy')}/${period}`;
}

export function siteDeviceRoute(site: Pick<Site, 'id'>, deviceId: string): string {
  if (!isUUIDv7(deviceId)) throw new Error('Device identity must be a Registry UUIDv7.');
  return `${siteRoute(site, 'devices')}/${deviceId}`;
}

export function siteIdFromPathname(pathname: string): string | undefined {
  const segments = pathname.split('/').filter(Boolean);
  if (segments[0] !== 'sites' || !segments[1] || !isUUIDv7(segments[1])) return undefined;
  return segments[1];
}

/**
 * Site-scoped routes carry the Site either in the `/sites/:siteId` path or, for the
 * global workspace entries, in the `site` search parameter.
 */
export function siteIdFromLocation(pathname: string, search: string): string | undefined {
  const fromPath = siteIdFromPathname(pathname);
  if (fromPath) return fromPath;
  const fromSearch = new URLSearchParams(search).get('site');
  return fromSearch && isUUIDv7(fromSearch) ? fromSearch : undefined;
}

/** The same workspace location re-targeted at another Site. Object selections are dropped. */
export function workspaceLocationForSite(pathname: string, siteId: string): string {
  if (!isUUIDv7(siteId)) throw new Error('Site identity must be a Registry UUIDv7.');
  const segments = pathname.split('/').filter(Boolean);
  if (segments[0] === 'sites') {
    return segments[2] ? `/sites/${siteId}/${segments[2]}` : `/sites/${siteId}`;
  }
  return `${pathname}?site=${siteId}`;
}
