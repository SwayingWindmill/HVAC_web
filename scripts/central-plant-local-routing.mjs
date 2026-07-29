const telemetryOwner = 'telemetry-runtime-service';

export function buildCentralPlantRouteOwnership(source) {
  if (!source || source.registryVersion !== 1 || !Array.isArray(source.routes)) {
    throw new Error('Route Ownership Registry is invalid');
  }
  let enabledTelemetryRoutes = 0;
  const routes = source.routes.map((route) => {
    if (route.owner !== telemetryOwner) return structuredClone(route);
    enabledTelemetryRoutes += 1;
    return {
      ...structuredClone(route),
      activationStatus: 'primary',
      rollout: { mode: 'all' },
      migrationPhase: 'R7-primary-100',
    };
  });
  if (enabledTelemetryRoutes !== 4) {
    throw new Error(`Expected four S2 Telemetry routes, found ${enabledTelemetryRoutes}`);
  }
  return { ...structuredClone(source), routes };
}
