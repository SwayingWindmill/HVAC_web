import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { applyRigToPoints, cadenceFor, loadAcceptanceRig } from './lib/acceptance-rig.mjs';

// The acceptance stack runs the simulator from a runtime copy of the central plant
// configuration. Generating it from the base configuration plus the acceptance rig
// profile keeps the published cadence identical to the Registry contract the rig also
// applies; hand-edited copies used to drift apart.
const root = process.cwd();
const check = process.argv.includes('--check');
const basePath = resolve(root, 'tools/eg8200-simulator/configs/central-plant.local.json');
const outputPath = resolve(root, 'deploy/platform/phase1/runtime/config/acceptance-plant.json');

const profile = await loadAcceptanceRig(root);
const base = JSON.parse(await readFile(basePath, 'utf8'));

const config = structuredClone(base);
config.publishInterval = profile.plant.publishInterval;
config.points = applyRigToPoints(profile, base.points);

// Overrides are partial: merge objects recursively so overriding one field of a nested
// component keeps the component's remaining configuration.
function mergeOverride(target, override) {
  const merged = { ...(target ?? {}) };
  for (const [key, value] of Object.entries(override)) {
    const isObject = value !== null && typeof value === 'object' && !Array.isArray(value);
    merged[key] = isObject ? mergeOverride(merged[key], value) : value;
  }
  return merged;
}

Object.assign(config, mergeOverride(config, profile.plant.overrides ?? {}));

const serialized = `${JSON.stringify(config, null, 2)}\n`;

if (check) {
  const actual = await readFile(outputPath, 'utf8').catch(() => '');
  if (actual !== serialized) {
    throw new Error('acceptance plant config is stale; run npm run acceptance:rig:config');
  }
  const cadences = new Set(config.points.map((point) => `${point.sampleInterval}/${point.publishInterval}/${point.staleAfter}`));
  console.log(`Acceptance plant config matches the rig profile (${config.points.length} points, cadences: ${[...cadences].sort().join(' ')}).`);
} else {
  await writeFile(outputPath, serialized, 'utf8');
  const perDevice = new Map();
  for (const point of config.points) {
    const cadence = cadenceFor(profile, point.deviceId, point);
    perDevice.set(point.deviceId, `${cadence.sampleInterval}/${cadence.publishInterval}/${cadence.staleAfter}`);
  }
  console.log(`Generated ${outputPath}`);
  for (const [device, cadence] of [...perDevice].sort()) {
    console.log(`  ${device}: ${cadence}`);
  }
}
