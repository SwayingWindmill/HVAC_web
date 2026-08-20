import { readFileSync } from 'node:fs';

function read(path) {
  return readFileSync(path, 'utf8');
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

const goWork = read('go.work');
const model = read('libs/edgefleet/model.go');
const replica = read('libs/edgefleet/replica.go');
const release = read('libs/edgefleet/release.go');
const offline = read('libs/edgefleet/offline.go');
const mqtt = read('libs/edgefleet/mqtt.go');
const store = read('services/mqtt-telemetry-adapter/internal/connectivity/edge_fleet.go');
const runtimeStore = read('services/mqtt-telemetry-adapter/internal/connectivity/edge_fleet_runtime.go');
const syncStore = read('services/mqtt-telemetry-adapter/internal/connectivity/edge_fleet_sync.go');
const fleetRuntime = read('services/mqtt-telemetry-adapter/internal/fleet/runtime.go');
const iotMain = read('services/mqtt-telemetry-adapter/cmd/mqtt-telemetry-adapter/main.go');
const edgeFleetRuntime = read('tools/eg8200-simulator/internal/simulator/edge_fleet.go');
const edgeEvidenceSpool = read('tools/eg8200-simulator/internal/simulator/mqtt_evidence_spool.go');
const edgeMQTTPublisher = read('tools/eg8200-simulator/internal/simulator/mqtt_publisher.go');
const edgeCommandRuntime = read('tools/eg8200-simulator/internal/simulator/mqtt_command.go');
const edgeMQTTConfig = read('tools/eg8200-simulator/internal/simulator/mqtt_config.go');
const otaStore = read('services/mqtt-telemetry-adapter/internal/connectivity/edge_fleet_ota.go');
const simulatorBootstrap = read('scripts/phase1-central-plant-simulator.mjs');
const migration = read('infra/connectivity/postgres/init/003-s12-edge-fleet.sql');
const mqttSchema = read('contracts/mqtt/edge-replication-envelope.v1.schema.json');
const thingsBoardReview = read('docs/architecture/thingsboard-source-review.md');
const openEMSReview = read('docs/architecture/openems-source-review.md');
const manifest = read('deploy/platform/phase1/migrations/manifest.v1.json');

assert(goWork.includes('./libs/edgefleet'), 'S12 edgefleet module must be part of go.work');
assert(model.includes('OwnerRegistry') && model.includes('AuthorityCloud'), 'S12 must define Cloud-owned downlink domains');
assert(model.includes('OwnerObservedManifest') && model.includes('AuthorityEdge'), 'S12 must define Edge-owned observed domains');
assert(replica.includes('Staging') && replica.includes('ActiveSnapshotRevision'), 'snapshot staging and active state must remain distinct');
assert(replica.includes('StagedRelease') && replica.includes('ActivateSnapshotRelease'), 'signed release and snapshot must share one activation boundary');
assert(!replica.includes('func (replica *Replica) CommitSnapshot('), 'unsigned snapshot commit path must not exist');
assert(replica.includes('json.Marshal(replica.state)') && replica.includes('replica.state = rollback'), 'failed activation must restore the complete prior replica state');
assert(replica.includes('ReconnectSnapshotResume') && replica.includes('ReconnectDelta'), 'reconnect must choose resume/full/delta explicitly');
assert(replica.includes('DeliveryQuarantined') && replica.includes('DisposeQuarantine'), 'bad delivery must have explicit quarantine/disposition state');
assert(replica.includes("ack.Status != DeliveryAcked && ack.Status != DeliveryDisposed"), 'committed cursor must stop at non-terminal delivery gaps');

assert(release.includes('ed25519.Verify'), 'EdgeRelease/OTA activation must verify Ed25519 signatures locally');
assert(!release.includes('func (replica *Replica) ActivateRelease('), 'release-only activation path must not bypass the signed snapshot boundary');
assert(release.includes('ActivateOTA') && release.includes('StagedOTAArtifactID') && release.includes('ActiveOTAArtifactID'), 'OTA must have signed stage/activate state');
assert(release.includes('startupHealthCheck') && release.includes('RolledBack: true'), 'OTA startup health failure must rollback');
assert(release.includes('ErrRuntimeIncompatible') && release.includes('ErrCapabilityMissing') && release.includes('ErrPreflightFailed'), 'signed activation must enforce runtime/capability/preflight gates');
assert(release.includes('OTACampaignPaused') && release.includes('AdvanceWave'), 'OTA must support governed waves and pause/resume');
assert(offline.includes('OpenOfflineBuffer') && offline.includes('offline-spool.json'), 'offline capacity must be a disk-backed spool, not memory-only state');
assert(offline.includes('EvidenceSafety') && offline.includes('EvidenceAudit') && offline.includes('EvidenceDiagnostic'), 'offline buffer must classify safety/audit/diagnostic evidence');
assert(offline.includes('isProtectedEvidence') && offline.includes('EvidenceDiagnostic'), 'offline pressure must preserve protected evidence ahead of diagnostics');
assert(offline.includes('func (buffer *OfflineBuffer) Pending()') && offline.includes('evidencePriority(items[left].Class) > evidencePriority(items[right].Class)'), 'offline flush order must be priority-first and stable within a class');
assert(edgeEvidenceSpool.includes('OpenOfflineBuffer') && edgeEvidenceSpool.includes('buffer.Pending()') && edgeEvidenceSpool.includes('buffer.Remove(item.ID)'), 'EG8200 outbound MQTT must use the durable priority spool through successful QoS1 delivery');
assert(edgeMQTTPublisher.includes('EvidenceTelemetryNormal') && edgeMQTTPublisher.includes('evidenceSpool.Flush') && !edgeMQTTPublisher.includes('PublishViaQueue') && !edgeMQTTPublisher.includes('filequeue.New'), 'telemetry must use the shared priority spool rather than a second file queue');
assert(edgeCommandRuntime.includes('EvidenceControl') && edgeCommandRuntime.includes('CapacityReadOnlySafety') && edgeCommandRuntime.includes('EDGE_CAPACITY_READ_ONLY'), 'command replies must be protected evidence and new physical writes must fail closed under offline capacity exhaustion');
assert(edgeFleetRuntime.includes('EvidenceConfigResult') && edgeFleetRuntime.includes('BacklogBytes') && edgeFleetRuntime.includes('CapacityState'), 'Fleet/config results and observed capacity must be backed by the shared Edge spool');
assert(mqtt.includes('ReplicationSchemaVersion = "1.0"') && mqtt.includes('DecodeReplicationEnvelope'), 'Fleet MQTT envelope must be strict and versioned');

for (const table of [
  'edge_nodes', 'edge_enrollments', 'edge_identity_bindings', 'edge_handshakes', 'edge_releases',
  'desired_edge_states', 'observed_edge_states', 'edge_snapshots', 'edge_snapshot_chunks', 'edge_sync_sessions',
  'edge_delivery_cursors', 'edge_delivery_items', 'edge_ota_artifacts', 'edge_ota_campaigns', 'edge_ota_assignments', 'edge_fleet_events',
]) {
  assert(migration.includes(`CREATE TABLE connectivity.${table}`), `S12 migration is missing connectivity.${table}`);
  assert(migration.includes(`ALTER TABLE connectivity.${table} FORCE ROW LEVEL SECURITY`), `connectivity.${table} must FORCE Tenant RLS`);
}
assert(!/\b(private_key|credential_value|plaintext_secret)\s+(text|bytea|jsonb)\b/i.test(migration), 'S12 business schema must not persist recoverable private keys or credential values');
assert(migration.includes("ARRAY['password','secret','token','privateKey','credentialValue']"), 'S12 fleet evidence must reject secret-bearing JSON keys');
assert(migration.includes('BEFORE UPDATE ON connectivity.edge_releases') && migration.includes('BEFORE UPDATE ON connectivity.edge_ota_artifacts'), 'signed release/artifact facts must be immutable');
assert(migration.includes('package_ref text NOT NULL') && migration.includes('rollback_artifact_id uuid NOT NULL'), 'OTA artifacts must persist a non-secret package reference and mandatory rollback target');
assert(migration.includes('WHERE closed_at IS NULL'), 'only one open Edge sync session may exist per EdgeNode');
assert(!migration.includes('GRANT DELETE'), 'Connectivity runtime must not receive DELETE authority for S12 tables');

assert(store.includes("SELECT $1::uuid, $2::uuid, i.site_id, i.id, $4, $5, 'SUSPENDED'"), 'EdgeNode must not become active before enrollment');
assert(store.includes("SET status = 'ACTIVE'"), 'successful one-time enrollment must activate the Edge identity');
assert(store.includes('s.credential_revision = $6'), 'Fleet handshake must bind the active credential revision');
assert(store.includes('edgeFleetPayloadDigest(input.Payload)'), 'Cloud delivery persistence must recompute payload digest');
assert(store.includes('EXCLUDED.desired_revision > connectivity.desired_edge_states.desired_revision') && store.includes('ErrEdgeFleetStale'), 'DesiredEdgeState must reject stale revisions');
assert(store.includes("state NOT IN ('ACKED','DISPOSED')"), 'Cloud committed cursor must stop at quarantined/pending gaps');
assert(runtimeStore.includes('TransitionOTACampaign') && runtimeStore.includes('OTACampaignPause') && runtimeStore.includes('OTACampaignAdvance'), 'durable OTA campaign must support pause and wave advancement');
assert(runtimeStore.includes('OpenEdgeSyncSession') && runtimeStore.includes('CloseEdgeSyncSession'), 'Fleet sync session lifecycle must be durable');
assert(syncStore.includes('LoadEdgeSyncBundle') && syncStore.includes('ReconnectSnapshotResume') && syncStore.includes('loadPendingEdgeDeliveries'), 'Cloud sync must materialize durable full/resume/delta bundles');
assert(syncStore.includes("state='DISPOSED'") && fleetRuntime.includes('ReplicationQuarantineDisposition'), 'quarantine disposition must be a Cloud durable fact sent down to Edge');
assert(fleetRuntime.includes('EnableManualAcknowledgment: true') && fleetRuntime.includes('received.Client.Ack'), 'Fleet uplink must ACK only after durable processing or permanent rejection');
assert(fleetRuntime.includes('/fleet/up') && fleetRuntime.includes('/fleet/down'), 'iot-service Fleet runtime must use dedicated MQTT sync topics');
assert(iotMain.includes('/health/fleet/ready') && iotMain.includes('EDGE_FLEET_MODULE_STOPPED'), 'Fleet must have an independent readiness/fault domain inside iot-service');
assert(edgeFleetRuntime.includes('ActivateSnapshotRelease') && edgeFleetRuntime.includes('ReplicationChangeAck'), 'EG8200 Fleet runtime must apply signed snapshots and ACK deltas through the Replica');
assert(edgeFleetRuntime.includes('ReplicationQuarantineDisposition') && edgeFleetRuntime.includes('DisposeQuarantine'), 'Edge may apply only Cloud-issued quarantine disposition');
assert(edgeFleetRuntime.includes('ActivateOTA') && edgeFleetRuntime.includes('ReplicationOTAResult') && edgeFleetRuntime.includes('resolveOTAPackageDigest'), 'EG8200 must fetch/hash the referenced OTA package, perform signed activation/rollback and report the result');
assert(otaStore.includes('LoadDispatchableOTA') && otaStore.includes("status IN ('PENDING','STAGING')") && otaStore.includes('RecordOTAResult'), 'OTA campaigns must durably dispatch current-wave assignments and converge reported outcomes');
assert(release.includes('PackageRef') && release.includes('Scheme != "artifact"'), 'signed OTA payloads must use canonical non-secret artifact references');
assert(edgeMQTTConfig.includes('MQTTGatewayConfigSchemaVersion = 2') && edgeMQTTConfig.includes('FleetReleasePublicKeyFile'), 'EG8200 MQTT config must be schema v2 with a trusted release public key');
assert(simulatorBootstrap.includes('generateKeyPairSync(\'ed25519\')') && simulatorBootstrap.includes('edge_identity_bindings') && simulatorBootstrap.includes('edge_delivery_cursors'), 'local simulator must bootstrap public-key trust and durable Edge identity facts');

assert(mqttSchema.includes('"schemaVersion": { "const": "1.0" }'), 'S12 MQTT replication contract must have one explicit schema version');
assert(mqttSchema.includes('"SNAPSHOT_BEGIN"') && mqttSchema.includes('"CHANGE_BATCH"') && mqttSchema.includes('"OBSERVED_STATE"'), 'MQTT replication contract must cover snapshot, delta and observed state');
assert(!/fullSync|routingSecret|secretKey|deprecated/i.test(mqttSchema), 'S12 MQTT contract must not retain ThingsBoard fullSync/static-secret compatibility fields');
assert(mqttSchema.includes('"ownerDomain": { "enum": ["REGISTRY", "PROFILE", "RULE", "SCHEDULE", "SAFETY_POLICY", "DRIVER_CONFIG"] }'), 'downlink contract must expose only Cloud-owned owner domains');

assert(thingsBoardReview.includes('## S12 — Edge Fleet, sync, Desired Config and signed OTA') && thingsBoardReview.includes('EdgeSyncCursor.java') && thingsBoardReview.includes('PostgresCloudEventUplinkRetriever.java'), 'ThingsBoard pinned source review must be recorded for S12');
assert(openEMSReview.includes('## Review 005 — S12 signed Desired Edge state and local activation') && openEMSReview.includes('EdgeConfigWorker.java'), 'OpenEMS pinned source review must be recorded for S12');
assert(manifest.includes('infra/connectivity/postgres/init/003-s12-edge-fleet.sql'), 'S12 connectivity migration must be in the production allowlist');

console.log('S12 Edge Fleet/sync/release/OTA architecture check passed.');
