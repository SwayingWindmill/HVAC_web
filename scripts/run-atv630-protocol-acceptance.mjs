// Release acceptance for the deployed ATV630 protocol path (#347, #349). It needs the
// running local stack (`npm run local:up`) and is not a PR gate. The ATV630 Edge is its
// own Gateway on a dedicated acceptance Site; commands go through Command Governance and
// are checked against the Virtual ATV630's own drive state and public telemetry.
import { spawnSync } from 'node:child_process';
import { randomBytes, randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';

import { centralPlantIdentity, localUUID, sqlLiteral } from './central-plant-local-contract.mjs';
import { localAdministratorBrowser } from './lib/local-administrator-browser.mjs';
import { ensureServiceDataDirectory, localContainer, localEnvFile, localProject, repoRoot, runtimePath, writePrivate } from './lib/local-environment.mjs';

const launcher = path.join(repoRoot, 'scripts', 'phase1-wsl-compose.mjs');
const profileFlags = ['--integration', '--intelligence', '--atv630-protocol-acceptance'];
const reportPath = path.join(repoRoot, 'out', 'atv630-protocol-acceptance', 'report.json');
const origin = /^PUBLIC_ORIGIN=(.+)$/m.exec(readFileSync(localEnvFile, 'utf8'))?.[1]?.trim() || 'https://localhost:8443';
const tenantId = centralPlantIdentity.tenantId;
const id = (n) => localUUID(0xa63000000000 + n);
const ids = { site: id(1), gateway: id(2), pump: id(3), asset: id(4), deviceBinding: id(5), sourceKey: id(6) };
const approverCredentials = runtimePath('local-approver.credentials');
// The diagnostics listeners sit on internal Compose networks, which Docker does not publish to
// the host, so they are called from a throwaway container on the mqtt network.
const virtualDiagnostics = 'http://virtual-atv630:19096';
const edgeDiagnostics = 'http://atv630-edge:19097';

const plantConfig = JSON.parse(readFileSync(path.join(repoRoot, 'tools', 'eg8200-simulator', 'configs', 'central-plant.local.json'), 'utf8'));
const points = plantConfig.points
  .filter((point) => point.deviceId === 'CHWP-01' && ['run_state', 'frequency', 'fault_code', 'start', 'stop', 'reset_fault', 'set_frequency'].includes(point.pointCode))
  .map((point, index) => ({ ...point, registryId: id(10 + index), subjectBindingId: id(20 + index) }));
const telemetryPoints = points.filter((point) => point.pointType !== 'COMMAND');
const commandPoints = points.filter((point) => point.pointType === 'COMMAND');
const commandPoint = (capability) => commandPoints.find((point) => point.sourceMetadata.capability === capability);
const report = { schemaVersion: 1, ticket: 349, capability: 'atv630-deployed-protocol-path', startedAt: new Date().toISOString(), steps: [] };

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: repoRoot, encoding: 'utf8', stdio: ['pipe', 'pipe', 'inherit'], ...options });
  if (result.status !== 0) throw new Error(`${path.basename(args[0] ?? command)} ${args.slice(1).join(' ')} failed with ${result.status}`);
  return result.stdout;
}
const compose = (...args) => run('node', [launcher, ...profileFlags, ...args], { stdio: 'inherit' });
const psql = (database, sql) => run('docker', ['exec', '-i', localContainer('postgres'), 'psql', '-U', 'postgres', '-d', database, '-v', 'ON_ERROR_STOP=1', '-Atq'], { input: sql }).trim();
const milliseconds = (value) => Number(/^(\d+)s$/.exec(value)[1]) * 1000;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function waitFor(label, check, timeoutMs = 60_000) {
  const deadline = Date.now() + timeoutMs;
  let last;
  while (Date.now() < deadline) {
    last = await check();
    if (last?.ok) return last;
    await sleep(1000);
  }
  throw new Error(`${label} not observed within ${timeoutMs / 1000}s; last=${JSON.stringify(last)}`);
}

function record(step, evidence) {
  report.steps.push({ step, ...evidence });
  console.log(`PASS ${step}: ${JSON.stringify(evidence)}`);
}

function registrySeed() {
  const scope = `${sqlLiteral(tenantId)}`;
  const pointRows = points.map((point) => `(${sqlLiteral(point.registryId)}, ${scope}, ${sqlLiteral(ids.site)}, ${sqlLiteral(ids.pump)}, NULL,
    ${sqlLiteral(point.pointCode)}, ${sqlLiteral(point.telemetryKey)}, ${sqlLiteral(point.name)}, ${sqlLiteral(point.pointType)}, ${sqlLiteral(point.valueType)},
    ${point.unit ? sqlLiteral(point.unit) : 'NULL'}, ${point.writable}, ${milliseconds(point.sampleInterval)}, ${milliseconds(point.publishInterval)}, ${milliseconds(point.staleAfter)},
    ${sqlLiteral(JSON.stringify({ ...(point.sourceMetadata ?? {}), protocol: 'MODBUS_TCP', address: `ATV630:${point.sourceKey}` }))}::jsonb,
    'ACTIVE', 1, clock_timestamp(), clock_timestamp(), NULL, NULL, NULL, NULL)`).join(',\n');
  const subjectRows = points.map((point) => `(${sqlLiteral(point.subjectBindingId)}, ${scope}, ${sqlLiteral(ids.site)}, ${sqlLiteral(point.registryId)}, 'ASSET', NULL, ${sqlLiteral(ids.asset)},
    ${point.pointType === 'COMMAND' ? "'CONTROLS'" : "'DESCRIBES'"}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`).join(',\n');
  return `BEGIN;
INSERT INTO core_registry.sites (id, code, display_name, timezone, status, revision, created_at, updated_at, tenant_id)
VALUES (${sqlLiteral(ids.site)}, 'atv630-protocol-acceptance', 'ATV630 协议验收站点', 'Asia/Shanghai', 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${scope})
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.assets (id, site_id, code, display_name, asset_type, status, revision, created_at, updated_at, tenant_id)
VALUES (${sqlLiteral(ids.asset)}, ${sqlLiteral(ids.site)}, 'chwp-atv630', 'CHWP-ATV630', 'CHILLED_WATER_PUMP', 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${scope})
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.devices (id, site_id, code, display_name, device_type, status, revision, created_at, updated_at, tenant_id, product_id, template_version_id) VALUES
  (${sqlLiteral(ids.gateway)}, ${sqlLiteral(ids.site)}, 'atv630-edge', 'ATV630-EDGE', 'GATEWAY', 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${scope}, NULL, NULL),
  (${sqlLiteral(ids.pump)}, ${sqlLiteral(ids.site)}, 'chwp-atv630', 'CHWP-ATV630 变频器', 'CHILLED_WATER_PUMP', 'ACTIVE', 1, clock_timestamp(), clock_timestamp(), ${scope}, NULL, NULL)
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.gateway_device_source_keys (id, tenant_id, site_id, gateway_device_id, source_key, device_id, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(ids.sourceKey)}, ${scope}, ${sqlLiteral(ids.site)}, ${sqlLiteral(ids.gateway)}, 'CHWP-01', ${sqlLiteral(ids.pump)}, 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.device_bindings (id, site_id, device_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at, tenant_id)
VALUES (${sqlLiteral(ids.deviceBinding)}, ${sqlLiteral(ids.site)}, ${sqlLiteral(ids.pump)}, ${sqlLiteral(ids.asset)}, 'CONTROLLER', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp(), ${scope})
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.telemetry_points (id, tenant_id, site_id, reporting_device_id, sensor_id, point_code, source_key, display_name, point_type, value_type, unit, writable, sample_interval_ms, publish_interval_ms, stale_after_ms, source_metadata, status, revision, created_at, updated_at, point_template_id, template_version_id, counter_decrease_mode, counter_rollover_modulus) VALUES
${pointRows}
ON CONFLICT (id) DO NOTHING;
INSERT INTO core_registry.point_subject_bindings (id, tenant_id, site_id, point_id, subject_type, space_id, asset_id, binding_role, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${subjectRows}
ON CONFLICT (id) DO NOTHING;
COMMIT;`;
}

function telemetrySeed() {
  const freshness = telemetryPoints.map((point) => `(${sqlLiteral(ids.pump)}, ${sqlLiteral(point.pointCode)}, 1, 30, true, ${milliseconds(point.sampleInterval) / 1000},
    ${sqlLiteral(point.valueType)}, ${point.unit ? sqlLiteral(point.unit) : 'NULL'}, NULL, NULL, clock_timestamp())`).join(',\n');
  return `BEGIN;
SET LOCAL ROLE s2_telemetry_migrator;
INSERT INTO telemetry_runtime.devices (device_id, tenant_id, site_id, presence_applicability, updated_at)
VALUES (${sqlLiteral(ids.pump)}, ${sqlLiteral(tenantId)}, ${sqlLiteral(ids.site)}, 'APPLICABLE', clock_timestamp()) ON CONFLICT (device_id) DO NOTHING;
INSERT INTO telemetry_runtime.presence_policies (device_id, policy_revision, online_within_seconds, offline_after_seconds, coverage_required, accepted_signal_types, max_future_clock_skew_seconds, max_source_lag_seconds, updated_at)
VALUES (${sqlLiteral(ids.pump)}, 1, 30, 120, true, ARRAY['SOURCE_ACTIVITY']::text[], 60, 120, clock_timestamp()) ON CONFLICT (device_id) DO NOTHING;
INSERT INTO telemetry_runtime.freshness_policies (device_id, telemetry_key, policy_revision, fresh_within_seconds, configured, expected_sample_interval_seconds, value_type, expected_unit, minimum_number, maximum_number, updated_at) VALUES
${freshness}
ON CONFLICT (device_id, telemetry_key) DO NOTHING;
INSERT INTO telemetry_runtime.observation_coverage (device_id, available, continuous_since, reason_code, source_revision, updated_at)
VALUES (${sqlLiteral(ids.pump)}, true, clock_timestamp(), NULL, 1, clock_timestamp()) ON CONFLICT (device_id) DO NOTHING;
RESET ROLE;
COMMIT;`;
}

// The administrator submits; a second local account approves, because Command
// Governance rejects self-approval.
function ensureApprover() {
  if (psql('hvac_identity', "SELECT count(*) FROM identity.users WHERE username = 'local-approver'") !== '0') {
    return readFileSync(approverCredentials, 'utf8').match(/^userId=(.+)$/m)[1];
  }
  const password = randomBytes(24).toString('base64url');
  const created = run('node', [launcher, 'run', '--rm', '-T', 'identity-admin'], {
    input: '',
    env: { ...process.env, IDENTITY_ADMIN_OPERATION: 'create', IDENTITY_ADMIN_USERNAME: 'local-approver', IDENTITY_ADMIN_DISPLAY_NAME: '本地审批人', IDENTITY_ADMIN_EMAIL: 'local-approver@hvac.local', IDENTITY_ADMIN_PASSWORD: password },
  });
  const user = JSON.parse(created.trim().split('\n').at(-1));
  writePrivate(approverCredentials, `username=local-approver\npassword=${password}\nuserId=${user.id}\n`);
  return user.id;
}

function authorizationSeed(adminPrincipalId, approverSubject) {
  const telemetryActions = ['telemetry.batch.read', 'telemetry.history.read', 'telemetry.recovery.checkpoint', 'telemetry.recovery.use', 'telemetry.resubscribe', 'telemetry.snapshot.read', 'telemetry.subscribe'];
  const actions = `ARRAY[${telemetryActions.map(sqlLiteral).join(',')}]::text[]`;
  const permissions = (principal, purpose, offset) => commandPoints.map((point, index) => `(${sqlLiteral(id(offset + index))}, ${principal}, ${sqlLiteral(tenantId)}, ${sqlLiteral(ids.site)}, ${sqlLiteral(ids.pump)},
    ${sqlLiteral(point.sourceMetadata.capability)}, ${sqlLiteral(point.sourceMetadata.capabilityRevision)}, ${sqlLiteral(purpose)}, 'MEDIUM', 'ALLOW', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`);
  const approver = `(SELECT id FROM iam.principals WHERE external_subject = ${sqlLiteral(approverSubject)})`;
  return `BEGIN;
INSERT INTO iam.principals (id, external_issuer, external_subject, display_name, email, status, revision, created_at, updated_at)
SELECT ${sqlLiteral(id(70))}, external_issuer, ${sqlLiteral(approverSubject)}, '本地审批人', 'local-approver@hvac.local', 'ACTIVE', 1, clock_timestamp(), clock_timestamp()
FROM iam.principals WHERE id = ${sqlLiteral(adminPrincipalId)}
ON CONFLICT (external_issuer, external_subject) DO NOTHING;
INSERT INTO iam.tenant_memberships (id, tenant_id, principal_id, status, valid_from, valid_to, revision, created_at, updated_at)
SELECT ${sqlLiteral(id(71))}, ${sqlLiteral(tenantId)}, ${approver}, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
ON CONFLICT (tenant_id, principal_id) DO NOTHING;
INSERT INTO iam.role_templates (id, tenant_id, role_key, display_name, capabilities, status, revision, created_at, updated_at)
VALUES (${sqlLiteral(id(73))}, ${sqlLiteral(tenantId)}, 'command-approver', '命令审批人', ARRAY['device.read']::text[], 'ACTIVE', 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (tenant_id, role_key) DO NOTHING;
INSERT INTO iam.role_bindings (id, tenant_id, principal_id, role_template_id, status, valid_from, valid_to, revision, created_at, updated_at)
SELECT ${sqlLiteral(id(72))}, ${sqlLiteral(tenantId)}, ${approver}, template.id, 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp()
FROM iam.role_templates template WHERE template.tenant_id = ${sqlLiteral(tenantId)} AND template.role_key = 'command-approver'
ON CONFLICT (tenant_id, principal_id, role_template_id) DO NOTHING;
INSERT INTO iam.site_bindings (id, tenant_id, site_id, principal_id, actions, effect, valid_from, valid_to, revision, created_at, updated_at)
VALUES (${sqlLiteral(id(30))}, ${sqlLiteral(tenantId)}, ${sqlLiteral(ids.site)}, ${sqlLiteral(adminPrincipalId)}, ${actions}, 'ALLOW', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (tenant_id, site_id, principal_id) DO NOTHING;
INSERT INTO iam.telemetry_scope_bindings (id, tenant_id, principal_id, site_id, device_id, actions, effect, status, valid_from, valid_to, revision, created_at, updated_at)
VALUES (${sqlLiteral(id(31))}, ${sqlLiteral(tenantId)}, ${sqlLiteral(adminPrincipalId)}, ${sqlLiteral(ids.site)}, NULL, ${actions}, 'ALLOW', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())
ON CONFLICT (id) DO NOTHING;
INSERT INTO iam.telemetry_key_bindings (id, tenant_id, principal_id, device_id, telemetry_key, actions, effect, status, valid_from, valid_to, revision, created_at, updated_at) VALUES
${telemetryPoints.map((point, index) => `(${sqlLiteral(id(40 + index))}, ${sqlLiteral(tenantId)}, ${sqlLiteral(adminPrincipalId)}, ${sqlLiteral(ids.pump)}, ${sqlLiteral(point.pointCode)}, ${actions}, 'ALLOW', 'ACTIVE', clock_timestamp(), NULL, 1, clock_timestamp(), clock_timestamp())`).join(',\n')}
ON CONFLICT (id) DO NOTHING;
INSERT INTO iam.command_permissions (id,principal_id,tenant_id,site_id,device_id,capability,capability_revision,purpose,maximum_risk,effect,status,valid_from,valid_to,revision,created_at,updated_at) VALUES
${[...permissions(sqlLiteral(adminPrincipalId), 'COMMAND_SUBMIT', 50), ...permissions(approver, 'COMMAND_APPROVE', 60)].join(',\n')}
ON CONFLICT (id) DO NOTHING;
COMMIT;`;
}

function writeEdgeMQTTConfig() {
  const config = JSON.parse(readFileSync(runtimePath('config', 'eg8200-mqtt.json'), 'utf8'));
  writeFileSync(runtimePath('config', 'atv630-edge-mqtt.json'), `${JSON.stringify({ ...config, gatewayId: ids.gateway }, null, 2)}\n`);
}

function diagnostics(method, url, body) {
  const script = `const r = await fetch(${JSON.stringify(url)}, ${JSON.stringify({ method, headers: { 'Content-Type': 'application/json' }, body: body && JSON.stringify(body) })}); console.log(JSON.stringify({ status: r.status, text: await r.text() }));`;
  const output = run('docker', ['run', '--rm', '--network', `${localProject}_mqtt`, '--entrypoint', 'node', 'hvac/operations-agent-service:0.1.0-dev', '--input-type=module', '-e', script]);
  return JSON.parse(output.trim().split('\n').at(-1));
}

async function getJSON(url) {
  const { status, text } = diagnostics('GET', url);
  if (status !== 200) throw new Error(`${url} returned ${status}`);
  return JSON.parse(text);
}

// The Virtual ATV630's own drive state, observed independently of the Edge and the platform.
const virtualDriveState = () => getJSON(`${virtualDiagnostics}/acceptance/chwp`);
const disturb = async (route, body) => {
  const { status } = diagnostics('PUT', `${virtualDiagnostics}/acceptance/chwp/${route}`, body);
  if (status !== 204) throw new Error(`virtual ATV630 ${route} returned ${status}`);
};

async function telemetry(admin) {
  const response = await admin.context.request.get(`${origin}/api/v1/devices/${ids.pump}/observation-snapshot?keys=run_state,frequency,fault_code`);
  if (!response.ok()) return { ok: false, status: response.status() };
  const snapshot = await response.json();
  return Object.fromEntries(snapshot.values.map((value) => [value.key, value]));
}

// sample runs on every status poll so a lease-bound effect (ADR 0012: an expired cloud
// intent returns the drive to local control) is observed while the command is live.
async function command(admin, approver, capability, parameters, expectApproval, sample) {
  const samples = [];
  const point = commandPoint(capability);
  const headers = { 'X-CSRF-Token': admin.principal.session.csrfToken, Origin: origin, 'Idempotency-Key': randomUUID() };
  const created = await admin.context.request.post(`${origin}/api/v1/commands`, { headers, data: { assetId: ids.asset, commandPointId: point.registryId, parameters } });
  if (!created.ok()) throw new Error(`${capability} create returned ${created.status()}: ${await created.text()}`);
  const accepted = await created.json();
  if (expectApproval) {
    if (accepted.status !== 'AWAITING_APPROVAL') throw new Error(`${capability} expected AWAITING_APPROVAL, got ${accepted.status}`);
    const approved = await approver.context.request.post(`${origin}/api/v1/commands/${accepted.commandId}/approve`, {
      headers: { 'X-CSRF-Token': approver.principal.session.csrfToken, Origin: origin }, data: {},
    });
    if (!approved.ok()) throw new Error(`${capability} approve returned ${approved.status()}: ${await approved.text()}`);
  }
  const terminal = await waitFor(`${capability} terminal status`, async () => {
    const response = await admin.context.request.get(`${origin}/api/v1/commands/${accepted.commandId}`);
    if (sample) samples.push(await sample());
    if (!response.ok()) return { ok: false, httpStatus: response.status() };
    const current = await response.json();
    return { ok: ['SUCCEEDED', 'FAILED', 'REJECTED', 'CANCELLED', 'EXPIRED', 'OUTCOME_UNKNOWN'].includes(current.status), status: current.status };
  }, 180_000);
  if (terminal.status !== 'SUCCEEDED') throw new Error(`${capability} ended ${terminal.status}`);
  return { commandId: accepted.commandId, risk: accepted.risk, approvalPolicy: accepted.approvalPolicy, status: terminal.status, ...(sample ? { samples } : {}) };
}

async function main() {
  const adminPrincipalId = JSON.parse(readFileSync(runtimePath('identity-reconcile.json'), 'utf8')).seed.principalId;
  psql('hvac_s1', registrySeed());
  psql('hvac_s2', telemetrySeed());
  const approverSubject = ensureApprover();
  psql('hvac_s1', authorizationSeed(adminPrincipalId, approverSubject));
  writeEdgeMQTTConfig();
  const edgeData = ensureServiceDataDirectory('data', 'atv630-edge');

  const admin = await localAdministratorBrowser(origin);
  const approver = await localAdministratorBrowser(origin, approverCredentials);
  try {
    if (!existsSync(path.join(edgeData, 'gateway-identity.pem'))) {
      const response = await admin.context.request.post(`${origin}/api/v1/gateways/${ids.gateway}/enrollment-code`, {
        headers: { 'X-CSRF-Token': admin.principal.session.csrfToken, Origin: origin },
      });
      if (!response.ok()) throw new Error(`ATV630 Edge enrollment code returned ${response.status()}`);
      process.env.ATV630_EDGE_ENROLLMENT_CODE = (await response.json()).enrollmentCode;
    }
    compose('build', '--build-arg', `GO_PROXY=${process.env.PHASE1_GO_PROXY || 'https://proxy.golang.org,direct'}`, 'virtual-atv630', 'atv630-template-release', 'atv630-edge');
    compose('up', '-d', 'virtual-atv630', 'atv630-edge');

    const edge = await waitFor('Edge Modbus and MQTT ready', async () => {
      const state = await getJSON(`${edgeDiagnostics}/acceptance/state`).catch(() => null);
      return { ok: state?.modbusReady && state?.mqttReady, templateRevisionId: state?.templateRevisionId };
    }, 120_000);
    record('edge-ready', { templateRevisionId: edge.templateRevisionId, credential: existsSync(path.join(edgeData, 'gateway-identity.pem')) });

    const baseline = await waitFor('fresh public telemetry', async () => {
      const values = await telemetry(admin);
      return { ok: ['run_state', 'frequency', 'fault_code'].every((key) => values[key]?.freshness === 'FRESH'), values };
    }, 120_000);
    record('telemetry-fresh', { run_state: baseline.values.run_state.value, frequency: baseline.values.frequency.value });

    const start = Number(baseline.values.frequency.value);
    for (const [delta, approval] of [[-2, false], [-5, true]]) {
      const target = Math.round(start + delta);
      const { samples, ...outcome } = await command(admin, approver, 'SET_FREQUENCY', { frequencyHz: target }, approval, async () => (await virtualDriveState()).frequencyHz);
      const reached = samples.find((frequencyHz) => Math.abs(frequencyHz - target) <= 0.5);
      if (reached === undefined) throw new Error(`drive state never reached ${target} Hz: ${samples.join(',')}`);
      record(`set-frequency-${target}`, { ...outcome, driveFrequencyHz: reached });
    }

    const stop = await command(admin, approver, 'STOP', {}, true);
    await waitFor('telemetry STOPPED', async () => { const values = await telemetry(admin); return { ok: values.run_state?.value === 'STOPPED', run_state: values.run_state?.value }; });
    record('stop', stop);

    const startCommand = await command(admin, approver, 'START', {}, true);
    await waitFor('telemetry RUNNING', async () => { const values = await telemetry(admin); return { ok: values.run_state?.value === 'RUNNING', run_state: values.run_state?.value }; });
    record('start', startCommand);

    // Stuck-high acts on a running pump (#334): the drive leaves its governed reference for the
    // nominal 50 Hz. No command is sent here, because an unprovable command would block the drive.
    const governed = await waitFor('drive settled at its governed reference', async () => {
      const first = (await virtualDriveState()).frequencyHz;
      const second = (await virtualDriveState()).frequencyHz;
      return { ok: first === second && first >= 20 && first < 49, frequencyHz: second };
    });
    await disturb('stuck-high', { active: true });
    const stuck = await waitFor('stuck-high drive above its governed reference', async () => { const chwp = await virtualDriveState(); return { ok: chwp.frequencyHz >= 49.5, frequencyHz: chwp.frequencyHz }; });
    const stuckTelemetry = await waitFor('stuck-high frequency in telemetry', async () => { const values = await telemetry(admin); return { ok: Number(values.frequency?.value) >= 49.5, frequency: values.frequency?.value }; });
    await disturb('stuck-high', { active: false });
    record('stuck-high', { governedFrequencyHz: governed.frequencyHz, driveFrequencyHz: stuck.frequencyHz, telemetryFrequency: stuckTelemetry.frequency });

    await disturb('fault', { code: '16' });
    await waitFor('telemetry FAULT 16', async () => { const values = await telemetry(admin); return { ok: values.run_state?.value === 'FAULT' && values.fault_code?.value === '16', run_state: values.run_state?.value, fault_code: values.fault_code?.value }; });
    const reset = await command(admin, approver, 'RESET_FAULT', {}, false);
    await waitFor('telemetry fault cleared', async () => { const values = await telemetry(admin); return { ok: values.fault_code?.value === '', fault_code: values.fault_code?.value }; });
    record('fault-reset', reset);

    run('docker', ['stop', localContainer('virtual-atv630')]);
    await waitFor('Edge reports Modbus down', async () => { const state = await getJSON(`${edgeDiagnostics}/acceptance/state`); return { ok: !state.modbusReady }; });
    run('docker', ['start', localContainer('virtual-atv630')]);
    await waitFor('Edge reports Modbus restored', async () => { const state = await getJSON(`${edgeDiagnostics}/acceptance/state`); return { ok: state.modbusReady }; });
    record('modbus-outage-visible', { recovered: true });

    report.status = 'passed';
  } finally {
    report.finishedAt = new Date().toISOString();
    report.status ??= 'failed';
    mkdirSync(path.dirname(reportPath), { recursive: true });
    writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
    await admin.browser.close();
    await approver.browser.close();
  }
  console.log(`ATV630 protocol acceptance passed: ${reportPath}`);
}

await main();
