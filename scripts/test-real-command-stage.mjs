import test from 'node:test';
import assert from 'node:assert/strict';
import { projectCommandStage } from '../apps/hvac-web/src/shared/status/command-status.ts';

function command(status, reason = status) {
  return { status, transitions: [{ reason }] };
}

test('Real command projection keeps approval, dispatch and verification distinct', () => {
  assert.equal(projectCommandStage(command('SUBMITTED')), 'CREATED');
  assert.equal(projectCommandStage(command('AWAITING_APPROVAL')), 'APPROVAL_PENDING');
  assert.equal(projectCommandStage(command('QUEUED')), 'DISPATCHED');
  assert.equal(projectCommandStage(command('DISPATCHING')), 'SENT');
  assert.equal(projectCommandStage(command('SUCCEEDED')), 'VERIFIED');
});

test('Real command projection never treats unknown or timeout as success', () => {
  assert.equal(projectCommandStage(command('OUTCOME_UNKNOWN')), 'UNKNOWN');
  assert.equal(projectCommandStage(command('EXPIRED')), 'EXPIRED');
  assert.equal(projectCommandStage(command('FAILED', 'DISPATCH_TIMEOUT')), 'TIMEOUT');
  assert.equal(projectCommandStage(command('REJECTED')), 'REJECTED');
});
