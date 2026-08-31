import type { Capability, Site } from '@/api/generated/platformGateway.gen';

export type EnergyRoutePeriod = 'year' | 'month' | 'week' | 'day';

export type AssetsDetailTarget =
  | { readonly kind: 'asset'; readonly id: string }
  | { readonly kind: 'device'; readonly id: string };

export type SiteRouteLeaf =
  | 'dashboard'
  | 'assets'
  | 'energy'
  | 'forecast'
  | 'control'
  | 'optimize'
  | 'fdd'
  | 'alarms'
  | 'work-orders'
  | 'ai'
  | 'cost'
  | 'settlement'
  | 'bigscreen'
  | 'operations';

export interface SiteContext {
  readonly site: Readonly<Site>;
}

export type SiteEntryDecision =
  | { state: 'NO_AUTHORIZED_SITE' }
  | { state: 'REDIRECT'; target: string }
  | { state: 'CHOOSE_SITE'; sites: readonly Readonly<Site>[] };

export type SiteRoutingDecision =
  | { state: 'PLATFORM_ROUTE' }
  | SiteEntryDecision
  | {
    state: 'READY';
    route: SiteRouteLeaf;
    context: SiteContext;
    assetsDetail?: AssetsDetailTarget;
    energyPeriod?: EnergyRoutePeriod;
  }
  | { state: 'FORBIDDEN' }
  | { state: 'SITE_NOT_VISIBLE' }
  | { state: 'SITE_ROUTE_NOT_FOUND'; context: SiteContext };

const UUID_V7_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const SITE_ROUTE_LEAVES = new Set<SiteRouteLeaf>([
  'dashboard',
  'assets',
  'energy',
  'forecast',
  'control',
  'optimize',
  'fdd',
  'alarms',
  'work-orders',
  'ai',
  'cost',
  'settlement',
  'bigscreen',
  'operations',
]);
const ENERGY_PERIODS = new Set<EnergyRoutePeriod>(['year', 'month', 'week', 'day']);

export function isUUIDv7(value: string): boolean {
  return UUID_V7_PATTERN.test(value);
}

function readonlySite(site: Site): Readonly<Site> {
  return Object.freeze({ ...site });
}

function siteContext(site: Site): SiteContext {
  const validatedSite = readonlySite(site);
  return Object.freeze({
    site: validatedSite,
  });
}

export function siteRoute(site: Pick<Site, 'id'>, leaf: SiteRouteLeaf): string {
  if (!isUUIDv7(site.id)) throw new Error('Site identity must be a Registry UUIDv7.');
  return `/sites/${site.id}/${leaf}`;
}

export function siteEnergyRoute(site: Pick<Site, 'id'>, period: EnergyRoutePeriod = 'month'): string {
  return `${siteRoute(site, 'energy')}/${period}`;
}

export function resolveSiteEntry(
  authorizedSites: readonly Site[],
  _pathname = '/sites',
): SiteEntryDecision {
  if (authorizedSites.length === 0) return { state: 'NO_AUTHORIZED_SITE' };
  if (authorizedSites.length === 1) {
    return { state: 'REDIRECT', target: siteRoute(authorizedSites[0], 'dashboard') };
  }
  return {
    state: 'CHOOSE_SITE',
    sites: authorizedSites.map(readonlySite),
  };
}

export function resolveSiteRouting(
  pathname: string,
  authorizedSites: readonly Site[],
  effectiveCapabilities: readonly Capability[] = [],
): SiteRoutingDecision {
  const normalized = pathname.length > 1 && pathname.endsWith('/') ? pathname.slice(0, -1) : pathname;
  if (normalized === '/' || normalized === '/sites') return resolveSiteEntry(authorizedSites, normalized);
  if (!normalized.startsWith('/sites/')) return { state: 'PLATFORM_ROUTE' };

  const segments = normalized.split('/').filter(Boolean);
  const requestedSiteId = segments[1] ?? '';
  if (!isUUIDv7(requestedSiteId)) return { state: 'SITE_NOT_VISIBLE' };

  const site = authorizedSites.find((candidate) => candidate.id === requestedSiteId);
  if (!site) return { state: 'SITE_NOT_VISIBLE' };

  if (segments.length === 2) {
    return { state: 'REDIRECT', target: siteRoute(site, 'dashboard') };
  }

  if (!effectiveCapabilities.includes('site.read')) return { state: 'FORBIDDEN' };
  const context = siteContext(site);
  const leaf = segments[2] as SiteRouteLeaf | undefined;
  if (!leaf || !SITE_ROUTE_LEAVES.has(leaf)) {
    return { state: 'SITE_ROUTE_NOT_FOUND', context };
  }

  if (leaf === 'assets') {
    if (segments.length === 3) return { state: 'READY', route: leaf, context };
    const kind = segments[3];
    const id = segments[4];
    if (segments.length === 5 && id && (kind === 'asset' || kind === 'device')) {
      return { state: 'READY', route: leaf, context, assetsDetail: { kind, id } };
    }
    return { state: 'SITE_ROUTE_NOT_FOUND', context };
  }

  if (leaf === 'energy') {
    if (segments.length === 3) {
      return { state: 'READY', route: leaf, context, energyPeriod: 'month' };
    }
    const period = segments[3] as EnergyRoutePeriod | undefined;
    if (segments.length === 4 && period && ENERGY_PERIODS.has(period)) {
      return { state: 'READY', route: leaf, context, energyPeriod: period };
    }
    return { state: 'SITE_ROUTE_NOT_FOUND', context };
  }

  if (segments.length !== 3) return { state: 'SITE_ROUTE_NOT_FOUND', context };
  return { state: 'READY', route: leaf, context };
}
