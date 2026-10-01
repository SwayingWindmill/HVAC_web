import type { Capability, CurrentPrincipalResponse, Site } from '@/api/generated/platformGateway.gen';
import type { ShellRuntime } from './shell-runtime';
import { isUUIDv7 } from './router-paths';

export type RouteAccessFailureCode =
  | 'FORBIDDEN'
  | 'PLATFORM_UNAVAILABLE'
  | 'PLATFORM_DEGRADED'
  | 'SITE_DISCOVERY_UNAVAILABLE'
  | 'SITE_NOT_VISIBLE'
  | 'SITE_SCOPE_MISMATCH';

export class RouteAccessError extends Error {
  readonly code: RouteAccessFailureCode;

  constructor(code: RouteAccessFailureCode) {
    super(code);
    this.name = 'RouteAccessError';
    this.code = code;
  }
}

export function requireCapabilities(runtime: ShellRuntime, capabilities: readonly Capability[]): CurrentPrincipalResponse {
  const snapshot = runtime.current();
  const principal = snapshot.state === 'READY' ? snapshot.principal : undefined;
  if (!principal) throw new RouteAccessError('FORBIDDEN');
  const effective = new Set(principal.authorization.capabilities);
  if (!capabilities.every((capability) => effective.has(capability))) {
    throw new RouteAccessError('FORBIDDEN');
  }
  return principal;
}

export function requirePlatform(runtime: ShellRuntime): void {
  const platform = runtime.current().platform;
  if (!platform || platform.state === 'checking' || platform.state === 'unavailable') {
    throw new RouteAccessError('PLATFORM_UNAVAILABLE');
  }
  if (platform.state === 'degraded') throw new RouteAccessError('PLATFORM_DEGRADED');
}

export function requireSite(runtime: ShellRuntime, siteId: string): { principal: CurrentPrincipalResponse; site: Readonly<Site> } {
  const principal = requireCapabilities(runtime, ['site.read']);
  if (!isUUIDv7(siteId)) throw new RouteAccessError('SITE_NOT_VISIBLE');
  const sites = runtime.current().sites;
  if (!sites || sites.state === 'checking' || sites.state === 'unavailable') {
    throw new RouteAccessError('SITE_DISCOVERY_UNAVAILABLE');
  }
  if (sites.state === 'forbidden') throw new RouteAccessError('FORBIDDEN');
  const site = sites.items?.find((candidate) => candidate.id === siteId);
  if (!site) throw new RouteAccessError('SITE_NOT_VISIBLE');

  const activeSiteId = runtime.current().protectedScope?.siteId;
  if (activeSiteId && activeSiteId !== siteId) {
    throw new RouteAccessError('SITE_SCOPE_MISMATCH');
  }
  if (!activeSiteId) runtime.activateSiteScope(siteId);
  return { principal, site };
}
