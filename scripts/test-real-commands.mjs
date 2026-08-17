import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CommandApiError,
  commandSchema,
  validateCommandScope,
} from '../apps/hvac-web/src/api/command-contract.ts';
import {
  isTerminalCommandStatus,
  projectRealCommand,
} from '../apps/hvac-web/src/real/real-commands-projection.ts';

const tenantId = '018f3e00-1000-7000-8000-000000000001';
const siteId = '018f3e00-2000-7000-8000-000000000001';
const otherSiteId = '018f3e00-2000-7000-8000-000000000002';
const deviceId = '018f3e00-3000-7000-8000-000000000001';
const commandId = '018f3e00-4000-7000-8000-000000000001';

function command(overrides = {}) {
  const status = overrides.status ?? 'QUEUED';
  const version = overrides.version ?? 3;
  return commandSchema.parse({
    schemaVersion: 1,
    commandId,
    tenantId,
    siteId,
    deviceId,
    capability: 'SET_TEMPERATURE_SETPOINT',
    capabilityRevision: 'capability:set-temperature-setpoint:v1',
    status,
    risk: overrides.risk ?? 'LOW',
    approvalPolicy: overrides.approvalPolicy ?? 'NONE',
    approvalCount: overrides.approvalCount ?? 0,
    requiredApprovalCount: overrides.requiredApprovalCount ?? 0,
    parameters: { setpointC: 24 },
    deviceCommandSequence: 1,
    snapshotRevision: 17,
    version,
    transitions: overrides.transitions ?? [
      { toStatus: 'SUBMITTED', reason: 'COMMAND_SUBMITTED', actorType: 'PRINCIPAL', occurredAt: '2026-07-31T09:00:00.000Z', version: 1 },
      { fromStatus: 'SUBMITTED', toStatus: 'VALIDATING', reason: 'COMMAND_VALIDATING', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:01.000Z', version: 2 },
      { fromStatus: 'VALIDATING', toStatus: status, reason: 'COMMAND_GOVERNANCE_EVALUATED', actorType: 'WORKLOAD', occurredAt: '2026-07-31T09:00:02.000Z', version },
    ],
    createdAt: '2026-07-31T09:00:00.000Z',
    updatedAt: '2026-07-31T09:00:02.000Z',
  });
}

test('Command projection requires authoritative Tenant and Site identity', () => {
  const parsed = command();
  assert.equal(parsed.tenantId, tenantId);
  assert.equal(parsed.siteId, siteId);
  assert.throws(
    () => commandSchema.parse({ ...parsed, siteId: undefined }),
    /Required/,
  );
});

test('Real Command scope validation fails closed for a cross-Site projection', () => {
  const parsed = command();
  assert.equal(validateCommandScope(parsed, {
    trustedTenantId: tenantId,
    trustedSiteId: siteId,
  }), parsed);

  assert.throws(
    () => validateCommandScope(parsed, {
      trustedTenantId: tenantId,
      trustedSiteId: otherSiteId,
    }),
    (error) => error instanceof CommandApiError
      && error.status === 404
      && error.code === 'RESOURCE_NOT_FOUND',
  );
});

test('Real Command projection distinguishes approval, terminal success, and unknown outcome', () => {
  const awaiting = command({
    status: 'AWAITING_APPROVAL',
    risk: 'MEDIUM',
    approvalPolicy: 'SINGLE_APPROVER',
    requiredApprovalCount: 1,
  });
  const approvalProjection = projectRealCommand(awaiting);
  assert.equal(approvalProjection.businessState, 'AWAITING_APPROVAL');
  assert.equal(approvalProjection.canApprove, true);
  assert.equal(approvalProjection.terminal, false);

  const succeeded = command({ status: 'SUCCEEDED' });
  assert.equal(projectRealCommand(succeeded).businessState, 'SUCCEEDED');
  assert.equal(isTerminalCommandStatus('SUCCEEDED'), true);

  const unknown = command({ status: 'OUTCOME_UNKNOWN' });
  const unknownProjection = projectRealCommand(unknown);
  assert.equal(unknownProjection.businessState, 'OUTCOME_UNKNOWN');
  assert.match(unknownProjection.outcomeWarning, /不会自动重发/);
});
