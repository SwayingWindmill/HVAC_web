import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { resolve } from 'node:path';
import ts from 'typescript';

const sourcePath = resolve('apps/hvac-web/src/domain/centralPlantTelemetry.ts');
const source = await readFile(sourcePath, 'utf8');
const { outputText, diagnostics = [] } = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ESNext,
    target: ts.ScriptTarget.ES2020,
  },
  fileName: sourcePath,
  reportDiagnostics: true,
});
const errors = diagnostics.filter((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error);
assert.equal(errors.length, 0, errors.map((diagnostic) => ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')).join('\n'));
const compiledModule = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`);
const {
  buildDeviceTelemetryHighlights,
  formatTelemetryUnit,
  getDeviceTelemetryProfile,
} = compiledModule;
const pointContract = JSON.parse(await readFile(
  resolve('contracts/registry/central-plant-device-points.v2.json'),
  'utf8',
));

test('chiller profile requests the exact central-plant energy keys', () => {
  const profile = getDeviceTelemetryProfile('CHILLER');
  assert.equal(profile.kind, 'CHILLER');
  assert.equal(profile.title, '冷水机组');
  assert.deepEqual(profile.keys, [
    'chiller.run_state',
    'chiller.power',
    'chiller.cop',
    'chiller.cooling_capacity',
    'chiller.compressor_load',
    'chiller.load_limit',
    'chiller.leaving_chilled_water_temperature',
    'chiller.entering_chilled_water_temperature',
    'chiller.chilled_water_temperature_setpoint',
    'chiller.entering_cooling_water_temperature',
    'chiller.business_revision',
    'chiller.fault_code',
  ]);
});

test('all central-plant profiles match the canonical V2 Device/Point contract', () => {
  const adapterKeys = pointContract.devices.flatMap((device) => device.points.map((point) => point.telemetryKey));
  const prefixes = {
    CHILLER: 'chiller.',
    CHILLED_WATER_PUMP: 'chwp.',
    COOLING_WATER_PUMP: 'cwp.',
    COOLING_TOWER: 'cooling_tower.',
    HVAC_POWER_METER: 'hvac_meter.',
    BTU_METER: 'btu_meter.',
  };
  for (const [deviceType, prefix] of Object.entries(prefixes)) {
    const profile = getDeviceTelemetryProfile(deviceType);
    const profileKeys = [...profile.keys];
    const contractKeys = adapterKeys.filter((key) => key.startsWith(prefix));
    assert.equal(new Set(profileKeys).size, profileKeys.length, `${deviceType} profile contains duplicate keys`);
    assert.equal(new Set(contractKeys).size, contractKeys.length, `${deviceType} adapter contract contains duplicate keys`);
    assert(profile.highlightKeys.every((key) => profileKeys.includes(key)), `${deviceType} highlights escape its exact-key profile`);
    assert.deepEqual(
      profileKeys.sort(),
      contractKeys.sort(),
      `${deviceType} profile drifted from the adapter point contract`,
    );
  }
});

test('unknown device types retain the existing generic exact keys', () => {
  const profile = getDeviceTelemetryProfile('HVAC_SENSOR');
  assert.equal(profile.kind, 'GENERIC');
  assert.deepEqual(profile.keys, ['temperature', 'humidity', 'setpoint', 'power']);
});

test('UCUM transport units are rendered with operator-friendly symbols', () => {
  assert.equal(formatTelemetryUnit('Cel'), '°C');
  assert.equal(formatTelemetryUnit('m3/h'), 'm³/h');
  assert.equal(formatTelemetryUnit('kW'), 'kW');
  assert.equal(formatTelemetryUnit(null), null);
});

test('device highlights preserve missing and quality state instead of inventing zero', () => {
  const highlights = buildDeviceTelemetryHighlights('CHILLER', {
    values: [
      {
        key: 'chiller.run_state', state: 'PRESENT', value: 'RUNNING', valueType: 'STRING', unit: null,
        sampledAt: '2026-07-28T10:00:00Z', receivedAt: '2026-07-28T10:00:01Z', freshness: 'FRESH',
        quality: 'GOOD', qualityReasons: [], policyRevision: 1,
      },
      {
        key: 'chiller.power', state: 'PRESENT', value: 212.5, valueType: 'NUMBER', unit: 'kW',
        sampledAt: '2026-07-28T10:00:00Z', receivedAt: '2026-07-28T10:00:01Z', freshness: 'FRESH',
        quality: 'GOOD', qualityReasons: [], policyRevision: 1,
      },
      {
        key: 'chiller.cop', state: 'MISSING', freshness: 'MISSING', missingReason: 'NEVER_OBSERVED', policyRevision: 1,
      },
      {
        key: 'chiller.cooling_capacity', state: 'PRESENT', value: 1080, valueType: 'NUMBER', unit: 'kW',
        sampledAt: '2026-07-28T09:58:00Z', receivedAt: '2026-07-28T09:58:01Z', freshness: 'STALE',
        quality: 'SUSPECT', qualityReasons: ['SOURCE_LAG_EXCEEDED'], policyRevision: 1,
      },
    ],
  });

  assert.deepEqual(highlights.map(({ key, label, displayValue, unit, state, freshness, quality }) => ({
    key, label, displayValue, unit, state, freshness, quality,
  })), [
    {
      key: 'chiller.run_state', label: '运行状态', displayValue: 'RUNNING', unit: null,
      state: 'PRESENT', freshness: 'FRESH', quality: 'GOOD',
    },
    {
      key: 'chiller.power', label: '主机功率', displayValue: '212.5', unit: 'kW',
      state: 'PRESENT', freshness: 'FRESH', quality: 'GOOD',
    },
    {
      key: 'chiller.cop', label: '主机 COP', displayValue: 'MISSING', unit: null,
      state: 'MISSING', freshness: 'MISSING', quality: null,
    },
    {
      key: 'chiller.cooling_capacity', label: '制冷量', displayValue: '1080', unit: 'kW',
      state: 'PRESENT', freshness: 'STALE', quality: 'SUSPECT',
    },
  ]);
});
