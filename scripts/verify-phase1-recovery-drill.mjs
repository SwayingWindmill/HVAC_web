import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import process from 'node:process';

import { buildDrillPassedAttainment, validateRecoveryAttainment } from './phase1-recovery-attainment.ts';

const root = resolve(import.meta.dirname, '..');
const targetPath = resolve(root, 'deploy/platform/phase1/recovery/recovery-targets.v1.json');
const currentAttainmentPath = resolve(root, 'deploy/platform/phase1/recovery/attainment.v1.json');
const fileArg = process.argv.find((arg) => arg.startsWith('--file='));
if (!fileArg) {
  throw new Error('usage: node scripts/verify-phase1-recovery-drill.mjs --file=/path/to/drill-record.json');
}

const recordPath = resolve(process.cwd(), fileArg.slice('--file='.length));
const attainmentOutputArg = process.argv.find((arg) => arg.startsWith('--attainment-output='));
const [targets, recordBytes, currentAttainment] = await Promise.all([
  JSON.parse(await readFile(targetPath, 'utf8')),
  readFile(recordPath),
  JSON.parse(await readFile(currentAttainmentPath, 'utf8')),
]);
const record = JSON.parse(recordBytes.toString('utf8'));

const failures = [];
const assert = (condition, message) => {
  if (!condition) failures.push(message);
};

const timestamp = (name, value) => {
  const parsed = Date.parse(value);
  assert(Number.isFinite(parsed), `${name} must be an RFC3339 timestamp`);
  return parsed;
};
const secondsBetween = (start, end) => Math.max(0, Math.round((end - start) / 1000));
const numericActual = (name) => {
  const value = record.actual?.[name];
  assert(Number.isFinite(value), `actual.${name} must be a measured number of seconds`);
  return value;
};
const compareMeasured = (name, calculated) => {
  const recorded = numericActual(name);
  if (Number.isFinite(recorded)) {
    assert(Math.abs(recorded - calculated) <= 1, `actual.${name}=${recorded} does not match timestamps (${calculated})`);
  }
};

assert(record.schemaVersion === 1, 'drill record schemaVersion must be 1');
assert(record.sourceOfTruth === targets.sourceOfTruth, `drill record sourceOfTruth must be ${targets.sourceOfTruth}`);
assert(['staging', 'production'].includes(record.environment), 'environment must be staging or production');
assert(['PROCESS_FAILURE', 'DATABASE_PROCESS_FAILURE', 'SERVER_OS_RECOVERY', 'WHOLE_SERVER_REPLACEMENT'].includes(record.scenario), 'scenario is not supported');
assert(typeof record.drillId === 'string' && record.drillId.length > 0 && !record.drillId.startsWith('['), 'drillId must be a real drill identifier');

const incident = timestamp('incidentConfirmedAt', record.incidentConfirmedAt);
const recoverable = timestamp('latestRecoverablePostgresAt', record.latestRecoverablePostgresAt);
const postgresRestored = timestamp('postgresBusinessRestoredAt', record.postgresBusinessRestoredAt);
const controlRestored = timestamp('controlBusinessRestoredAt', record.controlBusinessRestoredAt);
const telemetryRestored = timestamp('telemetryBusinessRestoredAt', record.telemetryBusinessRestoredAt);
const metricRestored = timestamp('metricBusinessRestoredAt', record.metricBusinessRestoredAt);
const businessValidated = timestamp('businessValidationCompletedAt', record.businessValidationCompletedAt);

if ([incident, recoverable, postgresRestored, controlRestored, telemetryRestored, metricRestored, businessValidated].every(Number.isFinite)) {
  assert(recoverable <= incident, 'latestRecoverablePostgresAt cannot be after incidentConfirmedAt');
  for (const [name, value] of [
    ['postgresBusinessRestoredAt', postgresRestored],
    ['controlBusinessRestoredAt', controlRestored],
    ['telemetryBusinessRestoredAt', telemetryRestored],
    ['metricBusinessRestoredAt', metricRestored],
    ['businessValidationCompletedAt', businessValidated],
  ]) {
    assert(value >= incident, `${name} cannot be before incidentConfirmedAt`);
  }

  const actual = {
    postgresRpoSeconds: secondsBetween(recoverable, incident),
    postgresRtoSeconds: secondsBetween(incident, postgresRestored),
    controlRtoSeconds: secondsBetween(incident, controlRestored),
    telemetryRtoSeconds: secondsBetween(incident, telemetryRestored),
    metricRtoSeconds: secondsBetween(incident, metricRestored),
    wholeScenarioRtoSeconds: secondsBetween(incident, businessValidated),
  };
  for (const [name, value] of Object.entries(actual)) compareMeasured(name, value);

  assert(actual.postgresRpoSeconds <= targets.targets.postgresqlBusinessSoT.rpoSeconds, `PostgreSQL RPO ${actual.postgresRpoSeconds}s exceeds ${targets.targets.postgresqlBusinessSoT.rpoSeconds}s`);
  assert(actual.postgresRtoSeconds <= targets.targets.postgresqlBusinessSoT.rtoSeconds, `PostgreSQL RTO ${actual.postgresRtoSeconds}s exceeds ${targets.targets.postgresqlBusinessSoT.rtoSeconds}s`);
  assert(actual.controlRtoSeconds <= targets.targets.controlCommandAudit.rtoSeconds, `Control RTO ${actual.controlRtoSeconds}s exceeds ${targets.targets.controlCommandAudit.rtoSeconds}s`);
  assert(actual.telemetryRtoSeconds <= targets.targets.telemetry.cloudServiceRtoSeconds, `Telemetry RTO ${actual.telemetryRtoSeconds}s exceeds ${targets.targets.telemetry.cloudServiceRtoSeconds}s`);
  assert(actual.metricRtoSeconds <= targets.targets.metricSeries.rtoSeconds, `Metric RTO ${actual.metricRtoSeconds}s exceeds ${targets.targets.metricSeries.rtoSeconds}s`);

  const scenarioKey = {
    PROCESS_FAILURE: 'processOrContainerFailure',
    DATABASE_PROCESS_FAILURE: 'databaseProcessFailure',
    SERVER_OS_RECOVERY: 'serverOsRecoverableFailure',
    WHOLE_SERVER_REPLACEMENT: 'wholeServerReplacement',
  }[record.scenario];
  const scenarioTarget = targets.failureScenarioRtoSeconds[scenarioKey];
  assert(actual.wholeScenarioRtoSeconds <= scenarioTarget, `${record.scenario} RTO ${actual.wholeScenarioRtoSeconds}s exceeds ${scenarioTarget}s`);
}

const prerequisites = record.prerequisites ?? {};
assert(prerequisites.externalBackupVerified === true, 'external backup must be verified');
assert(prerequisites.backupSurvivesProductionServerLoss === true, 'backup must survive production server loss');
assert(prerequisites.versionedConfigAvailable === true, 'versioned configuration must be available');
assert(prerequisites.protectedCredentialMaterialAvailable === true, 'protected credential material must be recoverable');
if (record.scenario === 'WHOLE_SERVER_REPLACEMENT') {
  assert(prerequisites.coldStandbyOrReplacementAvailable === true, 'whole-server 4h objective requires cold standby or replacement hardware');
}

const validation = record.validation ?? {};
for (const field of [
  'postgresqlIntegrity',
  'clickhouseWrite',
  'redisLatestRebuilding',
  'mqttConnectivity',
  'gatewayOnline',
  'telemetryFlowing',
  'metricCalculation',
  'alarmReevaluated',
  'commandReconciled',
  'schedulerBacklogProgressing',
  'outboxBacklogProgressing',
  'diskCapacitySafe',
  'clockSynchronized',
]) {
  assert(validation[field] === true, `validation.${field} must pass before RTO completion`);
}

assert(record.controlRecovery?.blindRetryUsed === false, 'Control recovery must not blindly retry incomplete Commands');
assert(Number.isInteger(record.controlRecovery?.incompleteCommandsReviewed) && record.controlRecovery.incompleteCommandsReviewed >= 0, 'controlRecovery.incompleteCommandsReviewed must be measured');

if (prerequisites.edgeStoreAndForwardAvailable === true) {
  assert(Number.isFinite(prerequisites.edgeRetentionHours) && prerequisites.edgeRetentionHours > 0, 'edgeRetentionHours must be measured when Edge replay is available');
  assert(record.telemetryReplay?.rateLimited === true, 'Edge replay must be rate limited during recovery');
}

for (const field of ['backupReference', 'walReference', 'recoveryHostReference', 'operator']) {
  const value = record.evidence?.[field];
  assert(typeof value === 'string' && value.length > 0 && !value.startsWith('['), `evidence.${field} must contain a non-secret evidence reference`);
}

if (failures.length > 0) {
  console.error('Phase 1 recovery drill verification failed:\n' + failures.map((failure) => `- ${failure}`).join('\n'));
  process.exit(1);
}

if (attainmentOutputArg) {
  const attainment = buildDrillPassedAttainment({
    current: currentAttainment,
    record,
    recordSha256: createHash('sha256').update(recordBytes).digest('hex'),
  });
  validateRecoveryAttainment(attainment, { now: new Date(record.businessValidationCompletedAt) });
  const outputPath = resolve(process.cwd(), attainmentOutputArg.slice('--attainment-output='.length));
  await writeFile(outputPath, `${JSON.stringify(attainment, null, 2)}\n`, { flag: 'wx' });
  console.log(`Phase 1 recovery attainment written: ${outputPath}`);
}

console.log(`Phase 1 recovery drill verified: drillId=${record.drillId}, environment=${record.environment}, scenario=${record.scenario}`);
