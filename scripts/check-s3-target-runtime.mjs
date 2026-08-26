import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const read = (path) => readFileSync(resolve(root, path), 'utf8');
const readJSON = (path) => JSON.parse(read(path));
const failures = [];
const assert = (condition, message) => { if (!condition) failures.push(message); };
const requireFile = (path) => assert(existsSync(resolve(root, path)), `missing file: ${path}`);
const requireText = (path, needle) => assert(read(path).includes(needle), `${path} is missing required text: ${needle}`);
const forbidText = (path, needle) => assert(!read(path).includes(needle), `${path} contains forbidden text: ${needle}`);

const requiredFiles = [
  'contracts/architecture/edge-control-plane.v1.json',
  'contracts/ownership/s3-command-ownership.v1.json',
  'deploy/s3/implementation-plan.v1.json',
  'libs/edgecontrol/channel.go',
  'libs/edgecontrol/control.go',
  'libs/edgecontrol/cycle.go',
  'libs/edgecontrol/driver.go',
  'libs/edgecontrol/intent.go',
  'modules/command/pkg/commandservice/runtime_http.go',
  'modules/command/pkg/commanddispatcher/runtime_client.go',
  'modules/command/pkg/commanddispatcher/reported_state_client.go',
  'modules/telemetry/internal/telemetry/command_verifier_server.go',
  'tools/eg8200-simulator/internal/simulator/edge_runtime.go',
  'tools/eg8200-simulator/internal/simulator/edge_driver.go',
  'tools/eg8200-simulator/internal/simulator/mqtt_command.go',
  'deploy/platform/phase1/wsl.override.yaml',
  'deploy/platform/phase1/config/command-edge-bindings.local.json',
  'deploy/platform/phase1/config/command-runtime-cohorts.local.json',
];
for (const path of requiredFiles) requireFile(path);

if (failures.length === 0) {
  const architecture = readJSON('contracts/architecture/edge-control-plane.v1.json');
  const ownership = readJSON('contracts/ownership/s3-command-ownership.v1.json');
  const plan = readJSON('deploy/s3/implementation-plan.v1.json');

  assert(architecture.status === 'CURRENT TARGET', 'Edge Control Plane contract is not the current target');
  assert(architecture.authority?.referenceImplementation === 'OpenEMS/openems', 'OpenEMS reference implementation is not pinned');
  assert(architecture.authority?.referenceRelease === '2026.7.0' && architecture.authority?.referenceCommit === '2e2792d', 'OpenEMS reference release/commit changed without review');
  assert(architecture.authority?.implementationRule === 'official-source-first', 'Edge implementation must remain source-first');
  assert(architecture.targetEdgeRuntime?.edgeCloudTransport === 'MQTT over TLS', 'Edge/Cloud transport must remain MQTT over TLS');
  assert(architecture.targetEdgeRuntime?.hardRealtime === false, 'soft-real-time Edge runtime must not claim hard real-time ownership');

  assert(plan.productionTrafficPercent === 0, 'S3 production traffic must remain zero before S3-09 certification');
  assert(plan.firstTracerBullet?.publicRoutesEnabled === false, 'S3 public routes must remain disabled before certification');
  assert(plan.firstTracerBullet?.productionProviderEnabled === false, 'production provider must remain disabled before certification');
  assert(plan.firstTracerBullet?.connector === 'NATIVE_MQTT', 'S3 must use the reviewed Native MQTT connector');

  const dispatcher = ownership.restrictedWorkers?.find((worker) => worker.service === 'command-dispatcher');
  assert(dispatcher?.activationStatus === 'active-native-mqtt', 'Command Dispatcher must use Native MQTT');
  assert(dispatcher?.directDatabaseAccess === false, 'Command Dispatcher must not receive command_runtime credentials');
  assert(ownership.transport?.type === 'MQTT' && ownership.transport?.authority === 'transport-only', 'MQTT must remain transport-only authority');

  requireText('libs/edgecontrol/channel.go', 'type ProcessImage struct');
  requireText('libs/edgecontrol/channel.go', 'SwitchProcessImage');
  requireText('libs/edgecontrol/cycle.go', 'type Controller interface');
  requireText('libs/edgecontrol/cycle.go', 'type Scheduler struct');
  requireText('libs/edgecontrol/cycle.go', 'CyclePhaseBeforeProcessImage');
  requireText('libs/edgecontrol/cycle.go', 'CyclePhaseExecuteWrite');
  requireText('libs/edgecontrol/intent.go', 'ExpiresAt');
  requireText('libs/edgecontrol/intent.go', 'intent requires a positive lease interval');
  requireText('libs/edgecontrol/driver.go', 'type DeviceAdapter interface');
  requireText('libs/edgecontrol/driver.go', 'production-facing device contract shared by physical and simulated drivers');

  for (const token of ['SubmitCommand', 'STALE_FENCE', 'EXPIRED', 'COMMAND_MAPPING_INVALID', 'command/reply']) {
    requireText('tools/eg8200-simulator/internal/simulator/mqtt_command.go', token);
  }
  requireText('modules/command/pkg/commanddispatcher/runtime_client.go', 'ClaimDispatch');
  requireText('modules/command/pkg/commanddispatcher/reported_state_client.go', 'validS2EvidenceID');
  requireText('modules/telemetry/internal/telemetry/command_verifier_server.go', 'allowedCommandVerifierSPIFFE');

  requireText('deploy/platform/phase1/wsl.override.yaml', 'COMMAND_RUNTIME_COHORTS_FILE');
  requireText('deploy/platform/phase1/wsl.override.yaml', 'COMMAND_RUNTIME_BINDINGS_FILE');
  requireText('deploy/platform/phase1/wsl.override.yaml', 'TELEMETRY_COMMAND_VERIFIER_DEVICE_IDS');
  forbidText('modules/command/pkg/commandservice/runtime_http.go', 'OrganizationID');
  forbidText('tools/eg8200-simulator/internal/simulator/mqtt_command.go', 'thingsboard');
}

if (failures.length > 0) {
  console.error('S3 target runtime check failed:');
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}

console.log(`S3 target runtime check passed: files=${requiredFiles.length}; transport=native-mqtt; edge=openems-informed; productionTraffic=0`);
