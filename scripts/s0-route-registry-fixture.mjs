const PLATFORM_STATUS_METHOD = 'GET';
const PLATFORM_STATUS_PATH = '/api/v1/platform/status';

function positiveSafeInteger(value, label) {
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed <= 0) throw new Error(`${label} must be a positive safe integer`);
  return parsed;
}

export function seedLegacyPlatformStatusRoute(registry) {
  if (!registry || typeof registry !== 'object' || !Array.isArray(registry.routes)) {
    throw new Error('ownership registry must contain a routes array');
  }

  const seeded = structuredClone(registry);
  const platformStatusRoute = seeded.routes.find((entry) => entry?.method === PLATFORM_STATUS_METHOD && entry?.path === PLATFORM_STATUS_PATH);
  if (!platformStatusRoute) throw new Error('platform status route is missing from ownership registry');

  seeded.registryRevision = positiveSafeInteger(seeded.registryRevision, 'registry revision') + 1;
  platformStatusRoute.revision = positiveSafeInteger(platformStatusRoute.revision, 'platform status route revision') + 1;
  platformStatusRoute.owner = 'legacy-hvac-backend';
  platformStatusRoute.rollout = { mode: 'all' };
  platformStatusRoute.compatibilityMode = 'legacy-read';
  return seeded;
}
