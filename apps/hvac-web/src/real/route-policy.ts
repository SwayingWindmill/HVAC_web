import type { Capability } from '@/api/generated/platformGateway.gen';

export type FeatureDelivery = 'implemented' | 'not-integrated' | 'hidden';
export type FeatureAvailability = 'none' | 'platform';
export type PlatformAvailabilityState = 'checking' | 'available' | 'degraded' | 'unavailable';

export interface RealFeatureDefinition {
  id: string;
  label: string;
  path: string;
  delivery: FeatureDelivery;
  availability: FeatureAvailability;
  requiredCapabilities: readonly Capability[];
}

export interface RealNavigationItem {
  id: string;
  label: string;
  path: string;
  kind: 'link' | 'not-integrated';
  degraded: boolean;
  primary?: boolean;
}

export type RouteDecision =
  | { state: 'READY'; feature: RealFeatureDefinition }
  | { state: 'FORBIDDEN' }
  | { state: 'NOT_INTEGRATED'; feature: RealFeatureDefinition }
  | { state: 'UNAVAILABLE'; feature: RealFeatureDefinition }
  | { state: 'DEGRADED'; feature: RealFeatureDefinition }
  | { state: 'NOT_FOUND' };

function normalizedPath(pathname: string): string {
  if (!pathname.startsWith('/')) return '/';
  if (pathname.length > 1 && pathname.endsWith('/')) return pathname.slice(0, -1);
  return pathname;
}

function hasRequiredCapabilities(
  feature: RealFeatureDefinition,
  effectiveCapabilities: readonly Capability[],
): boolean {
  const effective = new Set(effectiveCapabilities);
  return feature.requiredCapabilities.every((capability) => effective.has(capability));
}

function resolveKnownFeature(
  manifest: readonly RealFeatureDefinition[],
  pathname: string,
): RealFeatureDefinition | undefined {
  const requested = normalizedPath(pathname);
  return manifest.find((feature) => normalizedPath(feature.path) === requested);
}

function availabilityState(
  feature: RealFeatureDefinition,
  platformAvailability: PlatformAvailabilityState,
): 'available' | 'degraded' | 'unavailable' {
  if (feature.availability === 'none') return 'available';
  if (platformAvailability === 'checking' || platformAvailability === 'unavailable') return 'unavailable';
  return platformAvailability;
}

export function resolveRoute(
  manifest: readonly RealFeatureDefinition[],
  pathname: string,
  effectiveCapabilities: readonly Capability[],
  platformAvailability: PlatformAvailabilityState,
): RouteDecision {
  const feature = resolveKnownFeature(manifest, pathname);
  if (!feature || feature.delivery === 'hidden') return { state: 'NOT_FOUND' };
  if (!hasRequiredCapabilities(feature, effectiveCapabilities)) return { state: 'FORBIDDEN' };
  if (feature.delivery === 'not-integrated') return { state: 'NOT_INTEGRATED', feature };

  const availability = availabilityState(feature, platformAvailability);
  if (availability === 'unavailable') return { state: 'UNAVAILABLE', feature };
  if (availability === 'degraded') return { state: 'DEGRADED', feature };
  return { state: 'READY', feature };
}

export function resolveNavigation(
  manifest: readonly RealFeatureDefinition[],
  effectiveCapabilities: readonly Capability[],
  platformAvailability: PlatformAvailabilityState,
): RealNavigationItem[] {
  return manifest.flatMap((feature) => {
    if (feature.delivery === 'hidden') return [];
    if (!hasRequiredCapabilities(feature, effectiveCapabilities)) return [];

    const availability = availabilityState(feature, platformAvailability);
    if (feature.delivery === 'implemented' && availability === 'unavailable') return [];

    return [{
      id: feature.id,
      label: feature.label,
      path: feature.path,
      kind: feature.delivery === 'not-integrated' ? 'not-integrated' : 'link',
      degraded: feature.delivery === 'implemented' && availability === 'degraded',
    }];
  });
}
