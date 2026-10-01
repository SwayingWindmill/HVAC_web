import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

export const defaultAcceptanceRigPath = 'deploy/acceptance/phase1-simulator-rig.v1.json';

const durationPattern = /^\d+(ms|s|m|h)$/;

function invariant(condition, message) {
  if (!condition) throw new Error(`Invalid acceptance rig profile: ${message}`);
}

function requireDuration(value, field) {
  invariant(typeof value === 'string' && durationPattern.test(value), `${field} must be a duration such as 30s`);
  return value;
}

export function durationMilliseconds(value) {
  const match = /^(\d+)(ms|s|m|h)$/.exec(value);
  if (!match) throw new Error(`unsupported duration ${value}`);
  const amount = Number(match[1]);
  switch (match[2]) {
    case 'ms':
      return amount;
    case 's':
      return amount * 1000;
    case 'm':
      return amount * 60 * 1000;
    default:
      return amount * 60 * 60 * 1000;
  }
}

/**
 * The live acceptance rig is a single reviewed profile because the plant cadence, the
 * Registry staleness contract, the runtime freshness policy and the MQTT session
 * lifetime only work as a consistent set. Loading validates that consistency so a
 * partially edited profile fails loudly instead of producing a rig that looks live but
 * reports STALE inputs.
 */
export async function loadAcceptanceRig(root, profilePath = defaultAcceptanceRigPath) {
  const profile = JSON.parse(await readFile(resolve(root, profilePath), 'utf8'));
  invariant(profile.schemaVersion === 1, 'schemaVersion must be 1');
  invariant(typeof profile.profile === 'string' && profile.profile.length > 0, 'profile name is required');
  invariant(typeof profile.reason === 'string' && profile.reason.length > 0, 'profile reason is required');
  invariant(Array.isArray(profile.requiredDevices) && profile.requiredDevices.length > 0, 'requiredDevices must not be empty');

  const plant = profile.plant ?? {};
  requireDuration(plant.publishInterval, 'plant.publishInterval');
  for (const kind of ['required', 'background']) {
    const cadence = plant[kind] ?? {};
    requireDuration(cadence.sampleInterval, `plant.${kind}.sampleInterval`);
    requireDuration(cadence.publishInterval, `plant.${kind}.publishInterval`);
    requireDuration(cadence.staleAfter, `plant.${kind}.staleAfter`);
    invariant(
      durationMilliseconds(cadence.staleAfter) > durationMilliseconds(cadence.publishInterval),
      `plant.${kind}.staleAfter must exceed its publish interval, otherwise the rig publishes points that are already stale`,
    );
    invariant(
      durationMilliseconds(cadence.publishInterval) >= durationMilliseconds(plant.publishInterval),
      `plant.${kind}.publishInterval must not be shorter than the publisher tick`,
    );
  }

  const registry = profile.registry ?? {};
  invariant(Number.isInteger(registry.publishIntervalMs) && registry.publishIntervalMs >= 1000, 'registry.publishIntervalMs must be at least 1000');
  invariant(Number.isInteger(registry.staleAfterMs) && registry.staleAfterMs > registry.publishIntervalMs, 'registry.staleAfterMs must exceed registry.publishIntervalMs');

  const freshness = profile.runtimeFreshness ?? {};
  invariant(
    Number.isInteger(freshness.freshWithinSeconds) && freshness.freshWithinSeconds > 0,
    'runtimeFreshness.freshWithinSeconds must be a positive integer',
  );
  invariant(
    Number.isInteger(freshness.expectedSampleIntervalSeconds) && freshness.expectedSampleIntervalSeconds > 0,
    'runtimeFreshness.expectedSampleIntervalSeconds must be a positive integer',
  );
  invariant(
    freshness.freshWithinSeconds >= freshness.expectedSampleIntervalSeconds,
    'runtimeFreshness.freshWithinSeconds must be at least the expected sample interval',
  );
  invariant(
    durationMilliseconds(plant.required.sampleInterval) === freshness.expectedSampleIntervalSeconds * 1000,
    'runtimeFreshness.expectedSampleIntervalSeconds must match the required-device sample interval',
  );
  invariant(
    registry.publishIntervalMs === durationMilliseconds(plant.required.publishInterval),
    'registry.publishIntervalMs must match the required-device publish interval',
  );

  const connectivity = profile.connectivity ?? {};
  invariant(
    Number.isInteger(connectivity.sessionLifetimeHours) && connectivity.sessionLifetimeHours > 0,
    'connectivity.sessionLifetimeHours must be a positive integer',
  );
  invariant(
    connectivity.sessionLifetimeHours >= 72,
    'connectivity.sessionLifetimeHours must outlast a multi-day acceptance window; the previous 24h session expired mid-run',
  );

  return profile;
}

export function requiredDevices(profile) {
  return new Set(profile.requiredDevices);
}

export function cadenceFor(profile, deviceName, point = {}) {
  // Every point of a Device moves to the device cadence, including COMMAND points:
  // the publisher tick is the minimum sample interval across all points, so leaving a
  // single 1s control point behind would pin the tick back to 1s and defeat the pacing.
  void point;
  const required = requiredDevices(profile).has(deviceName);
  const cadence = required ? profile.plant.required : profile.plant.background;
  return {
    sampleInterval: cadence.sampleInterval,
    publishInterval: cadence.publishInterval,
    staleAfter: cadence.staleAfter,
  };
}

export function applyRigToPoints(profile, points) {
  return points.map((point) => ({ ...point, ...cadenceFor(profile, point.deviceId, point) }));
}

export function millisecondsToDuration(milliseconds) {
  if (milliseconds % (60 * 60 * 1000) === 0) return `${milliseconds / (60 * 60 * 1000)}h`;
  if (milliseconds % (60 * 1000) === 0) return `${milliseconds / (60 * 1000)}m`;
  if (milliseconds % 1000 === 0) return `${milliseconds / 1000}s`;
  return `${milliseconds}ms`;
}

/**
 * The Registry freshness contract is a platform-side statement about how often a point
 * must arrive, which is not the same as the simulator's own edge staleness window. The
 * Registry seed therefore takes its cadence from the rig's Registry section while the
 * simulator configuration takes its cadence from the plant section.
 */
export function applyRegistryRigToPoints(profile, points) {
  return points.map((point) => {
    const cadence = registryCadenceFor(profile, point.deviceId);
    return {
      ...point,
      sampleInterval: millisecondsToDuration(cadence.publishIntervalMs),
      publishInterval: millisecondsToDuration(cadence.publishIntervalMs),
      staleAfter: millisecondsToDuration(cadence.staleAfterMs),
    };
  });
}

export function registryCadenceFor(profile, deviceName) {
  if (!requiredDevices(profile).has(deviceName)) {
    const background = profile.plant.background;
    return {
      publishIntervalMs: durationMilliseconds(background.publishInterval),
      staleAfterMs: durationMilliseconds(background.staleAfter),
    };
  }
  return {
    publishIntervalMs: profile.registry.publishIntervalMs,
    staleAfterMs: profile.registry.staleAfterMs,
  };
}
