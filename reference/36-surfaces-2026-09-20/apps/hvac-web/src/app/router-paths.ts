import type { Site } from '@/api/generated/platformGateway.gen';

export type EnergyRoutePeriod = 'year' | 'month' | 'week' | 'day';

export type AssetsDetailTarget =
  | { readonly kind: 'asset'; readonly id: string }
  | { readonly kind: 'device'; readonly id: string };

export type SiteRouteLeaf =
  | 'overview'
  | 'dashboard'
  | 'assets'
  | 'diagnostics'
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
  return `/sites/${site.id}/${leaf}`;
}

export function siteEnergyRoute(site: Pick<Site, 'id'>, period: EnergyRoutePeriod = 'month'): string {
  return `${siteRoute(site, 'energy')}/${period}`;
}

export function siteAssetDeviceRoute(site: Pick<Site, 'id'>, deviceId: string): string {
  if (!isUUIDv7(deviceId)) throw new Error('Device identity must be a Registry UUIDv7.');
  return `${siteRoute(site, 'assets')}/${deviceId}`;
}

export function siteIdFromPathname(pathname: string): string | undefined {
  const segments = pathname.split('/').filter(Boolean);
  if (segments[0] !== 'sites' || !segments[1] || !isUUIDv7(segments[1])) return undefined;
  return segments[1];
}
