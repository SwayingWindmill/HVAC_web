import {
  chmodSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  applyThingsBoardSensorMonitoringSeed,
  loadThingsBoardSensorMonitoringConfig,
  validateThingsBoardSensorMonitoringConfig,
  writeThingsBoardRuntimeArtifacts,
} from './thingsboard-sensor-monitoring-seed.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const phase1Dir = path.join(repoRoot, 'deploy', 'platform', 'phase1');
const integrationCompose = path.join(phase1Dir, 'integrations', 'thingsboard.compose.yaml');
const defaultConfigPath = path.join(phase1Dir, 'config', 'thingsboard-sensor-monitoring.v1.json');
const defaultRuntimeDir = path.join(phase1Dir, 'runtime', 'config', 'thingsboard');
const defaultPhase1Env = path.join(phase1Dir, 'environments', 'development.runtime.env');
const defaultInternalPKI = path.join(phase1Dir, 'runtime', 'internal-pki');

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { encoding: 'utf8', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    const detail = String(result.stderr || result.stdout || '').trim();
    throw new Error(`${command} ${args.join(' ')} failed${detail ? `: ${detail}` : ''}`);
  }
  return String(result.stdout ?? '').trim();
}

function runInherited(command, args, options = {}) {
  const result = spawnSync(command, args, { stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} ${args.join(' ')} failed with exit code ${result.status}`);
}

function optionValue(args, name, fallback) {
  const index = args.indexOf(name);
  if (index === -1) return fallback;
  const value = args[index + 1];
  if (!value || value.startsWith('--')) throw new Error(`${name} requires a value`);
  return value;
}

function hasFlag(args, name) {
  return args.includes(name);
}

export function mergeTelemetrySourceBinding(envText, workloadSpiffe, integrationInstanceId) {
  const lines = envText.split(/\r?\n/);
  const prefix = 'TELEMETRY_SOURCE_BINDINGS_JSON=';
  const index = lines.findIndex((line) => line.startsWith(prefix));
  const existing = index === -1 ? {} : JSON.parse(lines[index].slice(prefix.length));
  const integrations = new Set(Array.isArray(existing[workloadSpiffe]) ? existing[workloadSpiffe] : []);
  integrations.add(integrationInstanceId);
  existing[workloadSpiffe] = [...integrations].sort();
  const replacement = `${prefix}${JSON.stringify(existing)}`;
  if (index === -1) {
    while (lines.length > 0 && lines.at(-1) === '') lines.pop();
    lines.push(replacement, '');
  } else {
    lines[index] = replacement;
  }
  const next = lines.join('\n');
  return { text: next, changed: next !== envText };
}

function updateTelemetrySourceBinding(envFile, config) {
  const current = readFileSync(envFile, 'utf8');
  const merged = mergeTelemetrySourceBinding(current, config.sourceWorkloadSpiffe, config.integrationInstanceId);
  if (!merged.changed) return false;
  const mode = statSync(envFile).mode & 0o777;
  const temporary = `${envFile}.thingsboard-${process.pid}.tmp`;
  writeFileSync(temporary, merged.text, { mode });
  chmodSync(temporary, mode);
  renameSync(temporary, envFile);
  return true;
}

function certificateHasSPIFFE(certPath, spiffe) {
  const result = spawnSync('sudo', ['openssl', 'x509', '-in', certPath, '-noout', '-text'], { encoding: 'utf8' });
  return result.status === 0 && String(result.stdout).includes(`URI:${spiffe}`);
}

function ensureSourceCertificate(internalPKIDir, spiffe) {
  const directory = path.join(internalPKIDir, 'thingsboard-reconciler');
  const certPath = path.join(directory, 'tls.crt');
  const keyPath = path.join(directory, 'tls.key');
  const caCertPath = path.join(internalPKIDir, 'ca.pem');
  const caKeyPath = path.join(internalPKIDir, 'ca.key');
  const caSerialPath = path.join(internalPKIDir, 'ca.srl');

  const keyExists = spawnSync('sudo', ['test', '-s', keyPath]).status === 0;
  if (keyExists && certificateHasSPIFFE(certPath, spiffe)) return { changed: false };
  if (spawnSync('sudo', ['test', '-s', caCertPath]).status !== 0 || spawnSync('sudo', ['test', '-s', caKeyPath]).status !== 0) {
    throw new Error(`Phase 1 internal CA is unavailable under ${internalPKIDir}`);
  }

  const temporary = mkdtempSync(path.join(tmpdir(), 'hvac-thingsboard-pki-'));
  try {
    const temporaryKey = path.join(temporary, 'tls.key');
    const temporaryCSR = path.join(temporary, 'tls.csr');
    const temporaryCert = path.join(temporary, 'tls.crt');
    const temporaryExt = path.join(temporary, 'tls.ext');
    run('openssl', ['genrsa', '-out', temporaryKey, '2048']);
    run('openssl', ['req', '-new', '-key', temporaryKey, '-out', temporaryCSR, '-subj', '/CN=thingsboard-reconciler']);
    writeFileSync(temporaryExt, `extendedKeyUsage=clientAuth\nsubjectAltName=URI:${spiffe}\n`, { mode: 0o600 });
    const serialArgs = spawnSync('sudo', ['test', '-f', caSerialPath]).status === 0
      ? ['-CAserial', caSerialPath]
      : ['-CAcreateserial'];
    runInherited('sudo', [
      'openssl', 'x509', '-req', '-in', temporaryCSR,
      '-CA', caCertPath, '-CAkey', caKeyPath,
      ...serialArgs,
      '-out', temporaryCert, '-days', '825', '-sha256', '-extfile', temporaryExt,
    ]);
    runInherited('sudo', ['install', '-d', '-m', '700', '-o', '65532', '-g', '65532', directory]);
    runInherited('sudo', ['install', '-m', '600', '-o', '65532', '-g', '65532', temporaryKey, keyPath]);
    runInherited('sudo', ['install', '-m', '644', '-o', '65532', '-g', '65532', temporaryCert, certPath]);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
  if (!certificateHasSPIFFE(certPath, spiffe)) throw new Error('ThingsBoard reconciler certificate is missing its SPIFFE URI');
  return { changed: true };
}

function ensureThingsBoardNetwork(container, network) {
  run('docker', ['network', 'inspect', network]);
  const networks = JSON.parse(run('docker', ['inspect', container, '--format', '{{json .NetworkSettings.Networks}}']));
  if (networks[network]) return false;
  runInherited('docker', ['network', 'connect', '--alias', 'thingsboard-ce', network, container]);
  return true;
}

function restartTelemetryWorker(phase1EnvFile) {
  runInherited(process.execPath, [
    path.join(repoRoot, 'scripts', 'phase1-wsl-compose.mjs'),
    'up', '-d', '--no-deps', '--force-recreate', 'telemetry-worker',
  ], {
    cwd: repoRoot,
    env: { ...process.env, PHASE1_ENV_FILE: phase1EnvFile },
  });
}

function composeEnvironment({ runtimeArtifacts, internalPKIDir, applicationNetwork }) {
  return {
    ...process.env,
    THINGSBOARD_RECONCILER_CONFIG_FILE: runtimeArtifacts.reconciler,
    INTERNAL_PKI_DIR: internalPKIDir,
    PHASE1_APPLICATION_NETWORK: applicationNetwork,
  };
}

function upReconciler({ runtimeArtifacts, internalPKIDir, thingsBoardEnvFile, applicationNetwork, noBuild }) {
  const project = process.env.THINGSBOARD_RECONCILER_PROJECT_NAME || 'hvac-thingsboard';
  const args = ['compose', '--project-name', project, '--env-file', thingsBoardEnvFile, '-f', integrationCompose];
  const env = composeEnvironment({ runtimeArtifacts, internalPKIDir, applicationNetwork });
  if (!noBuild) runInherited('docker', [...args, 'build', 'thingsboard-reconciler'], { cwd: repoRoot, env });
  runInherited('docker', [...args, 'up', '-d', '--no-build', 'thingsboard-reconciler'], { cwd: repoRoot, env });
}

function resolvePostgresContainer() {
  if (process.env.PHASE1_POSTGRES_CONTAINER?.trim()) return process.env.PHASE1_POSTGRES_CONTAINER.trim();
  const project = process.env.PHASE1_COMPOSE_PROJECT_NAME?.trim() || process.env.COMPOSE_PROJECT_NAME?.trim() || 'hvac-phase1-local';
  const id = run('docker', [
    'ps',
    '--filter', `label=com.docker.compose.project=${project}`,
    '--filter', 'label=com.docker.compose.service=postgres',
    '--format', '{{.ID}}',
  ]).split(/\r?\n/).find(Boolean);
  if (!id) throw new Error(`Postgres container for Compose project ${project} is not running`);
  return id;
}

function sqlQuote(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function psqlScalar(container, database, sql) {
  const user = process.env.POSTGRES_ADMIN_USER?.trim() || 'postgres';
  const value = run('docker', ['exec', container, 'psql', '-U', user, '-d', database, '-At', '-c', sql]);
  const number = Number(value.trim());
  if (!Number.isInteger(number)) throw new Error(`unexpected ${database} smoke result: ${value}`);
  return number;
}

function smoke(config) {
  const postgres = resolvePostgresContainer();
  const deviceIds = config.sources.map((source) => sqlQuote(source.deviceId)).join(',');
  const actual = {
    asset: psqlScalar(postgres, 'hvac_s1', `select count(*) from core_registry.assets where id=${sqlQuote(config.asset.id)}::uuid and status='ACTIVE';`),
    devices: psqlScalar(postgres, 'hvac_s1', `select count(*) from core_registry.devices where id in (${deviceIds}) and status='ACTIVE';`),
    sensors: psqlScalar(postgres, 'hvac_s1', `select count(distinct sensor_id) from core_registry.sensor_device_bindings where device_id in (${deviceIds}) and status='ACTIVE' and valid_to is null;`),
    points: psqlScalar(postgres, 'hvac_s1', `select count(*) from core_registry.telemetry_points where reporting_device_id in (${deviceIds}) and status='ACTIVE';`),
    keys: psqlScalar(postgres, 'hvac_s1', `select count(*) from iam.telemetry_key_bindings where principal_id=${sqlQuote(config.principalId)}::uuid and device_id in (${deviceIds}) and effect='ALLOW' and status='ACTIVE' and valid_to is null;`),
    projections: psqlScalar(postgres, 'hvac_s2', `select count(*) from telemetry_runtime.registry_point_bindings where device_id in (${deviceIds}) and binding_status='ACTIVE' and valid_to is null;`),
    latest: psqlScalar(postgres, 'hvac_s2', `select count(*) from telemetry_runtime.latest_accepted_telemetry where device_id in (${deviceIds});`),
  };
  const expected = { asset: 1, devices: 3, sensors: 22, points: 24, keys: 24, projections: 24, latest: 24 };
  for (const [name, expectedValue] of Object.entries(expected)) {
    if (actual[name] !== expectedValue) throw new Error(`ThingsBoard smoke ${name}=${actual[name]} expected=${expectedValue}`);
  }
  const project = process.env.THINGSBOARD_RECONCILER_PROJECT_NAME || 'hvac-thingsboard';
  const running = run('docker', [
    'ps', '--filter', `label=com.docker.compose.project=${project}`,
    '--filter', 'label=com.docker.compose.service=thingsboard-reconciler',
    '--format', '{{.ID}}',
  ]).split(/\r?\n/).filter(Boolean).length;
  if (running !== 1) throw new Error(`ThingsBoard reconciler running instances=${running} expected=1`);
  console.log(`thingsboard-integration smoke=OK asset=${actual.asset} devices=${actual.devices} sensors=${actual.sensors} points=${actual.points} keyGrants=${actual.keys} projections=${actual.projections} latest=${actual.latest}`);
}

function prepare({ config, phase1EnvFile, runtimeDir, internalPKIDir, thingsBoardContainer, applicationNetwork, restartWorker }) {
  const runtimeArtifacts = writeThingsBoardRuntimeArtifacts(config, runtimeDir);
  const sourceBindingChanged = updateTelemetrySourceBinding(phase1EnvFile, config);
  const certificate = ensureSourceCertificate(internalPKIDir, config.sourceWorkloadSpiffe);
  const networkChanged = ensureThingsBoardNetwork(thingsBoardContainer, applicationNetwork);
  if (sourceBindingChanged && restartWorker) restartTelemetryWorker(phase1EnvFile);
  console.log(`thingsboard-integration prepared sourceBindingChanged=${sourceBindingChanged} certificateChanged=${certificate.changed} networkChanged=${networkChanged}`);
  return { runtimeArtifacts };
}

function cli() {
  const args = process.argv.slice(2);
  const command = args[0] && !args[0].startsWith('--') ? args[0] : 'check';
  const configPath = path.resolve(optionValue(args, '--config', defaultConfigPath));
  const config = loadThingsBoardSensorMonitoringConfig(configPath);
  const summary = validateThingsBoardSensorMonitoringConfig(config);
  const phase1EnvFile = path.resolve(optionValue(args, '--phase1-env', process.env.PHASE1_ENV_FILE || defaultPhase1Env));
  const runtimeDir = path.resolve(optionValue(args, '--runtime-dir', process.env.THINGSBOARD_RUNTIME_DIR || defaultRuntimeDir));
  const internalPKIDir = path.resolve(optionValue(args, '--internal-pki-dir', process.env.INTERNAL_PKI_DIR || defaultInternalPKI));
  const thingsBoardEnvFile = path.resolve(optionValue(args, '--thingsboard-env', process.env.THINGSBOARD_ENV_FILE || '/opt/thingsboard/.env'));
  const thingsBoardContainer = optionValue(args, '--thingsboard-container', process.env.THINGSBOARD_CONTAINER || 'thingsboard-ce');
  const applicationNetwork = optionValue(args, '--application-network', process.env.PHASE1_APPLICATION_NETWORK || 'hvac-phase1-local_application');
  const noBuild = hasFlag(args, '--no-build');

  if (command === 'check') {
    console.log(`thingsboard-integration config=OK sources=${summary.sourceCount} sensors=${summary.sensorCount} points=${summary.pointCount}`);
    return;
  }
  if (command === 'prepare') {
    prepare({ config, phase1EnvFile, runtimeDir, internalPKIDir, thingsBoardContainer, applicationNetwork, restartWorker: !hasFlag(args, '--no-restart-worker') });
    return;
  }
  if (command === 'seed') {
    writeThingsBoardRuntimeArtifacts(config, runtimeDir);
    const postgres = applyThingsBoardSensorMonitoringSeed(config);
    console.log(`thingsboard-integration seed=OK postgres=${postgres}`);
    return;
  }
  if (command === 'up') {
    const runtimeArtifacts = writeThingsBoardRuntimeArtifacts(config, runtimeDir);
    ensureThingsBoardNetwork(thingsBoardContainer, applicationNetwork);
    upReconciler({ runtimeArtifacts, internalPKIDir, thingsBoardEnvFile, applicationNetwork, noBuild });
    return;
  }
  if (command === 'apply') {
    const prepared = prepare({ config, phase1EnvFile, runtimeDir, internalPKIDir, thingsBoardContainer, applicationNetwork, restartWorker: true });
    applyThingsBoardSensorMonitoringSeed(config);
    upReconciler({ runtimeArtifacts: prepared.runtimeArtifacts, internalPKIDir, thingsBoardEnvFile, applicationNetwork, noBuild });
    smoke(config);
    return;
  }
  if (command === 'smoke') {
    smoke(config);
    return;
  }
  throw new Error(`unsupported command ${command}; use check, prepare, seed, up, apply, or smoke`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    cli();
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}
