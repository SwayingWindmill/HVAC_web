import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { validateRealAssetsCertificationEvidence } from './real-assets-certification-lib.mjs';

const root = resolve(process.cwd());
const read = (path) => readFile(resolve(root, path), 'utf8');
const errors = [];
const [workspace, drawer, trends, runner, fixture, packageJSON, workflow] = await Promise.all([
  read('apps/hvac-web/src/real/assets/RealAssetsWorkspace.tsx'),
  read('apps/hvac-web/src/real/assets/DeviceDetailDrawer.tsx'),
  read('apps/hvac-web/src/real/assets/DeviceHistoryTrends.tsx'),
  read('scripts/run-real-assets-certification.mjs'),
  read('scripts/fixtures/real-assets-certification/main.tsx'),
  read('package.json'),
  read('.github/workflows/real-assets-certification.yml'),
]);

const requireText = (source, expected, label) => {
  if (!source.includes(expected)) errors.push(`${label} is missing ${expected}`);
};

requireText(workspace, 'data-total-device-count', 'Real Assets workspace');
requireText(workspace, 'data-filtered-device-count', 'Real Assets workspace');
requireText(workspace, 'data-testid="real-assets-table-wrap"', 'Real Assets workspace');
requireText(workspace, "useState<LedgerMode>('devices')", 'Real Assets Device-first default');
requireText(workspace, 'realAssetsDevicePath', 'Real Assets typed Device detail path');
requireText(drawer, "import('./DeviceHistoryTrends')", 'Device Drawer lazy history boundary');
requireText(trends, 'animation: false', 'history reduced-motion-safe chart');
requireText(trends, 'aria: { enabled: true', 'history chart accessibility');
requireText(runner, 'REAL_ASSETS_CERTIFICATION_FIXTURE_REVISION', 'certification runner fixture revision binding');
requireText(runner, "completesS2Ticket70: false", 'certification scope boundary');
requireText(runner, "completesS2Ticket71: false", 'certification scope boundary');
requireText(runner, "snapshotBatchSizes", 'certification request evidence');
requireText(runner, "oldScopeLeakDetected", 'certification scope evidence');
requireText(fixture, "createProtectedScopeCoordinator", 'certification protected scope fixture');
requireText(fixture, "createRealAssetsTelemetryRuntime", 'certification telemetry fixture');
requireText(fixture, "maximumActive", 'certification subscription budget');
requireText(packageJSON, '"real-assets:certify"', 'package certification command');
requireText(workflow, 'npm run real-assets:certify', 'certification workflow');
requireText(workflow, 'out/real-assets-certification', 'certification evidence artifact');

if (process.argv.includes('--evidence')) {
  try {
    const evidence = JSON.parse(await read('out/real-assets-certification/browser-evidence.json'));
    const validation = validateRealAssetsCertificationEvidence(evidence);
    if (!validation.passed) errors.push(...validation.errors.map((error) => `evidence: ${error}`));
  } catch (error) {
    errors.push(`evidence could not be read: ${error instanceof Error ? error.message : String(error)}`);
  }
}

if (errors.length > 0) {
  console.error('Real Assets certification gate failed:');
  for (const error of errors) console.error(`- ${error}`);
  process.exit(1);
}
console.log('Real Assets 200 Device certification static and evidence boundaries passed.');
