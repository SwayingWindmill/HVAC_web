const telemetryOwner = 'telemetry-runtime-service';

export function buildCentralPlantRouteOwnership(source) {
  if (!source || source.registryVersion !== 1 || !Array.isArray(source.routes)) {
    throw new Error('Route Ownership Registry is invalid');
  }
  const telemetryRoutes = source.routes.filter((route) => route.owner === telemetryOwner);
  if (telemetryRoutes.length !== 4) {
    throw new Error(`Expected four S2 Telemetry routes, found ${telemetryRoutes.length}`);
  }
  if (telemetryRoutes.some((route) => route.rollout?.mode !== 'all')) {
    throw new Error('Central plant requires the final all-traffic Telemetry route registry');
  }
  return structuredClone(source);
}
