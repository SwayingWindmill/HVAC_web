import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const sourcePath = path.resolve('apps/hvac-web/src/real/dashboard-projection.ts');
const source = fs.readFileSync(sourcePath, 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2020,
    strict: true,
  },
  fileName: sourcePath,
}).outputText;

const module = { exports: {} };
vm.runInNewContext(compiled, { module, exports: module.exports }, { filename: sourcePath });
const projection = module.exports;

const devices = [
  ['d-online', 'Online AHU'],
  ['d-offline', 'Offline Pump'],
  ['d-stale', 'Stale Meter'],
  ['d-unknown', 'Unknown Sensor'],
  ['d-error', 'Unavailable Gateway'],
  ['d-na', 'Non-applicable Device'],
].map(([id, displayName]) => ({ id, displayName, deviceType: 'GENERIC', status: 'ACTIVE' }));

function snapshot(deviceId, displayState, currentState = null) {
  return {
    deviceId,
    evaluatedAt: '2026-07-31T04:00:00.000Z',
    evaluationAvailability: displayState === 'UNAVAILABLE' ? 'UNAVAILABLE' : 'AVAILABLE',
    displayState,
    presence: {
      applicability: displayState === null ? 'NOT_APPLICABLE' : 'APPLICABLE',
      currentState,
      lastSeenAt: currentState ? '2026-07-31T03:55:00.000Z' : null,
    },
  };
}

test('dashboard summary preserves device states and isolates unavailable observations', () => {
  const result = projection.projectDashboardDevices(devices, [
    { status: 'ok', deviceId: 'd-online', snapshot: snapshot('d-online', 'ONLINE', 'ONLINE') },
    { status: 'ok', deviceId: 'd-offline', snapshot: snapshot('d-offline', 'OFFLINE', 'OFFLINE') },
    { status: 'ok', deviceId: 'd-stale', snapshot: snapshot('d-stale', 'STALE', 'ONLINE') },
    { status: 'ok', deviceId: 'd-unknown', snapshot: snapshot('d-unknown', 'UNKNOWN', 'UNKNOWN') },
    { status: 'error', deviceId: 'd-error' },
    { status: 'ok', deviceId: 'd-na', snapshot: snapshot('d-na', null) },
  ]);

  assert.deepEqual({ ...result.counts }, {
    total: 6,
    online: 1,
    offline: 1,
    stale: 1,
    unknown: 1,
    unavailable: 1,
    notApplicable: 1,
    attention: 4,
  });
  assert.deepEqual(Array.from(result.attentionDevices, (item) => `${item.deviceId}:${item.state}`), [
    'd-offline:OFFLINE',
    'd-stale:STALE',
    'd-unknown:UNKNOWN',
    'd-error:UNAVAILABLE',
  ]);
});

test('energy projection keeps valid zero and reports missing, partial, stale, and suspect states honestly', () => {
  const base = {
    points: [
      { periodStart: '2026-07-31T00:00:00.000Z', periodEnd: '2026-07-31T01:00:00.000Z', energyKWh: 0 },
      { periodStart: '2026-07-31T01:00:00.000Z', periodEnd: '2026-07-31T02:00:00.000Z', energyKWh: 12.5 },
    ],
    metadata: {
      partial: false,
      aggregateWatermark: '2026-07-31T04:00:00.000Z',
      dataWatermark: undefined,
      actualGranularity: 'hour',
      qualitySummary: { valid: 2, suspect: 0, invalid: 0 },
    },
  };

  assert.deepEqual({ ...projection.projectDashboardEnergy(base, '2026-07-31T04:00:00.000Z') }, {
    state: 'READY',
    totalKWh: 12.5,
    pointCount: 2,
  });
  assert.equal(projection.projectDashboardEnergy({ ...base, points: [] }, '2026-07-31T04:00:00.000Z').state, 'EMPTY');
  assert.equal(projection.projectDashboardEnergy({ ...base, metadata: { ...base.metadata, partial: true } }, '2026-07-31T04:00:00.000Z').state, 'PARTIAL');
  assert.equal(projection.projectDashboardEnergy({ ...base, metadata: { ...base.metadata, aggregateWatermark: '2026-07-30T20:00:00.000Z' } }, '2026-07-31T04:00:00.000Z').state, 'STALE');
  assert.equal(projection.projectDashboardEnergy({ ...base, metadata: { ...base.metadata, qualitySummary: { valid: 1, suspect: 1, invalid: 0 } } }, '2026-07-31T04:00:00.000Z').state, 'SUSPECT');
});
