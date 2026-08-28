import { existsSync, readFileSync } from 'node:fs';

function read(path) {
  return readFileSync(path, 'utf8');
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

const migration = read('infra/connectivity/postgres/init/001-s09-connectivity.sql');
const store = read('modules/iot/internal/connectivity/store.go');
const processor = read('modules/iot/internal/adapter/processor.go');
const connector = read('modules/command/pkg/mqttconnector/connector.go');
const config = read('modules/iot/internal/adapter/config.go');
const exampleConfig = read('modules/iot/configs/central-plant.local.example.json');
const simulator = read('scripts/phase1-central-plant-simulator.mjs');
const canonicalIoTCommandRuntimePath = 'cmd/iot-service/command_runtime.go';

for (const table of [
  'transport_profiles',
  'integration_instances',
  'credential_refs',
  'device_bindings',
  'gateway_child_bindings',
  'enrollments',
  'sessions',
  'connector_ownership_leases',
  'command_reply_correlations',
]) {
  assert(migration.includes(`CREATE TABLE connectivity.${table}`), `missing Connectivity owner table ${table}`);
  assert(migration.includes(`ALTER TABLE connectivity.${table} FORCE ROW LEVEL SECURITY`), `${table} must force Tenant RLS`);
}
assert(migration.includes("credential_kind text NOT NULL CHECK (credential_kind IN ('MTLS_CERTIFICATE','TOKEN_HASH'))"), 'CredentialRef kind contract is missing');
assert(migration.includes("status text NOT NULL CHECK (status IN ('ACTIVE','REVOKED','EXPIRED'))"), 'CredentialRef lifecycle contract is missing');
assert(!migration.includes('credential_value') && !migration.includes('private_key') && !migration.includes('plaintext'), 'Connectivity schema must not persist recoverable secret values');
assert(migration.includes("state text NOT NULL CHECK (state IN ('PREPARED','MAY_COMMIT','REPLIED','RESOLVED'))"), 'durable command correlation state machine is missing');
assert(migration.includes('connectivity_active_gateway_session_uidx'), 'one active Gateway session invariant is missing');

assert(store.includes("SET status = 'INVALIDATED'"), 'credential revocation must invalidate active Sessions');
assert(store.includes("close_reason = $4") && store.includes('CREDENTIAL_REVOKED:'), 'revocation reason must be retained as Session evidence');
assert(store.includes("c.status = 'ACTIVE'") && store.includes("s.status = 'ACTIVE'"), 'Gateway authorization must require active CredentialRef and Session');
assert(store.includes('ConsumeEnrollment') && store.includes('connectivity.gateway_child_bindings'), 'one-time enrollment must require a pre-registered binding');
assert(!store.includes('INSERT INTO core_registry.'), 'Connectivity enrollment must never auto-create Registry identity');
assert(store.includes('ClaimConnectorOwnership') && store.includes('lease_generation'), 'connector ownership must be durable and generation fenced');
assert(store.includes('l.lease_generation = c.owner_generation AND l.lease_until > $5'), 'physical publish commit-point must revalidate the active ownership lease atomically');

assert(processor.includes('AuthorizeGatewayChild'), 'MQTT ingress must authorize every child against GatewayChildBinding');
assert(processor.includes('not pre-registered in an active GatewayChildBinding'), 'unknown child must fail closed');
assert(!processor.includes('CreateDevice') && !processor.includes('GetOrCreate'), 'MQTT ingress must not create Device identity');

assert(connector.includes('ArmCommandCorrelation') && connector.indexOf('ArmCommandCorrelation') < connector.indexOf('manager.Publish'), 'command commit point must be durable before MQTT publish');
assert(connector.includes('CorrelationMayCommit') && connector.includes('resumeOutcomeUnknown'), 'post-commit restart must not republish physical control');
assert(connector.includes('RecoverCommandReplies'), 'late command replies must be restart-recoverable');
assert(migration.includes('capability text NOT NULL') && store.includes('&capability') && store.includes('Envelope.Capability = commandmodel.Capability(capability)'), 'restart recovery must persist and restore command Capability for cohort authorization');
for (const obsolete of ['DeviceExternalIDByDeviceID', 'maxFenceByDevice', 'results map[']) {
  assert(!connector.includes(obsolete), `obsolete in-memory/static command authority remains: ${obsolete}`);
}

assert(config.includes('ConfigSchemaVersion = 2'), 'MQTT adapter config must use S09 schema version 2');
assert(!exampleConfig.includes('gatewayScopes'), 'static Gateway scope authority must be removed from MQTT config');
assert(!exampleConfig.includes('brokerUrl') && config.includes('BrokerURL') && config.includes('json:"-"'), 'Broker origin must come only from the durable TransportProfile');
assert(simulator.includes('buildConnectivitySeed()'), 'local simulator must seed Connectivity owner state');
assert(simulator.includes('certificate.fingerprint256') && !simulator.includes('privateKey:'), 'local Connectivity seed must use certificate fingerprint/SecretRef, not private key material');
assert(existsSync(canonicalIoTCommandRuntimePath), 'canonical iot-service command runtime wiring is missing');

console.log('S09 Connectivity/session/credential architecture check passed.');
