import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildRollbackAssignment,
  canReleaseRuleDraft,
  createEmptyRuleDraft,
  deriveRulePermissions,
  makeRuleNode,
  ruleDraftFingerprint,
} from '../apps/hvac-web/src/features/system/rule-management/model.ts';

const tenantId = '01900000-0100-7000-8000-000000000001';
const siteId = '01900000-0101-7000-8000-000000000001';
const bindingId = '01900000-0102-7000-8000-000000000001';
const revisionId = '01900000-0103-7000-8000-000000000001';
const ruleId = '01900000-0104-7000-8000-000000000001';

const catalog = {
  version: 'core.v1',
  definitions: [
    { id: 'event_type_filter', version: 1, inputs: { in: 'EVENT' }, outputs: { match: 'EVENT', no_match: 'EVENT' }, stateSchemaVersion: 0, deterministic: true, resourceCost: 1, configFields: [{ name: 'schemas', kind: 'STRING_LIST', required: true }] },
    { id: 'owner_snapshot_read', version: 1, inputs: {}, outputs: { snapshot: 'SNAPSHOT' }, requiredPermission: 'owner.snapshot.read', stateSchemaVersion: 0, deterministic: true, resourceCost: 2, configFields: [{ name: 'revision', kind: 'POSITIVE_INTEGER', required: true }] },
    { id: 'alarm_intent', version: 1, inputs: { in: 'SNAPSHOT' }, outputs: { intent: 'INTENT' }, requiredPermission: 'alarm.intent.publish', effectOwner: 'ALARM', stateSchemaVersion: 0, deterministic: true, resourceCost: 2, configFields: [{ name: 'intentType', kind: 'ENUM', required: true, options: ['ALARM_PUBLICATION'] }] },
  ],
};

test('Rule draft permissions are derived only from the approved typed catalog', () => {
  const snapshotNode = makeRuleNode(catalog.definitions[1], []);
  const alarmNode = makeRuleNode(catalog.definitions[2], [snapshotNode]);
  assert.deepEqual(deriveRulePermissions(catalog, [snapshotNode, alarmNode]), ['alarm.intent.publish', 'owner.snapshot.read']);
  assert.equal(JSON.stringify(catalog).includes('className'), false);
  assert.equal(JSON.stringify(catalog).includes('script'), false);
});

test('release requires validation of the exact current draft fingerprint', () => {
  const draft = { ...createEmptyRuleDraft(), entryNodeId: 'filter', nodes: [{ id: 'filter', definitionId: 'event_type_filter', config: { schemas: ['telemetry.point.observed.v1'] } }] };
  const fingerprint = ruleDraftFingerprint(draft);
  assert.equal(canReleaseRuleDraft({ valid: true, digest: 'a'.repeat(64) }, fingerprint, draft), true);
  assert.equal(canReleaseRuleDraft({ valid: true, digest: 'a'.repeat(64) }, fingerprint, { ...draft, maxDepth: draft.maxDepth + 1 }), false);
  assert.equal(canReleaseRuleDraft({ valid: false }, fingerprint, draft), false);
});

test('rollback is a new assignment to a prior immutable revision', () => {
  const binding = { id: bindingId, tenantId, siteId, revision: 4, ruleRevisionId: '01900000-0105-7000-8000-000000000001', priority: 7, active: true, createdAt: '2026-08-20T00:00:00.000Z' };
  const revision = { ...createEmptyRuleDraft(), id: revisionId, ruleId, tenantId, revision: 2, state: 'RELEASED', digest: 'b'.repeat(64) };
  assert.deepEqual(buildRollbackAssignment(binding, revision), { bindingId, siteId, ruleRevisionId: revisionId, priority: 7 });
  assert.equal(binding.revision, 4);
  assert.equal(revision.state, 'RELEASED');
});
