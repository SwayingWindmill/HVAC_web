import assert from 'node:assert/strict';
import test from 'node:test';
import type { Asset, AssetRelationship, TelemetryPoint } from '../apps/hvac-web/src/api/generated/platformGateway.gen.ts';
import { blockingInterlocks, controlTargets, plantUnits } from '../apps/hvac-web/src/features/control/control-model.ts';

const at = '2026-10-01T00:00:00.000Z';
const site = { tenantId: 't', siteId: 's' };
const asset = (id: string, displayName: string, assetType = 'CHILLER'): Asset => ({
  id, ...site, code: displayName, displayName, assetType, status: 'ACTIVE', revision: 1, createdAt: at, updatedAt: at,
});
const point = (
  id: string,
  deviceId: string,
  pointCode: string,
  pointType: TelemetryPoint['pointType'],
  sourceMetadata: Record<string, unknown> = {},
  extra: Partial<TelemetryPoint> = {},
): TelemetryPoint => ({
  id, ...site, reportingDeviceId: deviceId, sensorId: null, pointCode, sourceKey: `chiller.${pointCode}`, displayName: pointCode,
  pointType, valueType: 'NUMBER', unit: null, writable: pointType === 'COMMAND', sampleIntervalMs: 1000, publishIntervalMs: 1000,
  staleAfterMs: 3000, counterDecreaseMode: null, counterRolloverModulus: null, sourceMetadata, status: 'ACTIVE', revision: 1, createdAt: at, updatedAt: at, ...extra,
});
const controls = (id: string, pointId: string, assetId: string, validTo: string | null = null): AssetRelationship => ({
  id, ...site, fromType: 'POINT', fromId: pointId, toType: 'ASSET', toId: assetId, role: 'CONTROLS', status: 'ACTIVE',
  validFrom: at, validTo, revision: 1, createdAt: at, updatedAt: at,
});
const now = new Date('2026-10-11T00:00:00Z');

test('a plant asset offers the actions and settings its command points control, with the reading that confirms each', () => {
  const model = {
    assets: [asset('a-chiller', 'CHILLER-01'), asset('a-retired', 'CHILLER-OLD')],
    telemetryPoints: [
      point('p-stop', 'd-chiller', 'stop', 'COMMAND', { capability: 'STOP', feedbackPointKey: 'run_state' }, { displayName: '停止冷水机' }),
      point('p-start', 'd-chiller', 'start', 'COMMAND', { capability: 'START', feedbackPointKey: 'run_state' }, { displayName: '启动冷水机' }),
      point('p-setpoint', 'd-chiller', 'set_chilled_water_temperature_setpoint', 'COMMAND', {
        capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', parameterKey: 'setpointC', minimum: 5, maximum: 12, step: 0.5,
        feedbackPointKey: 'chilled_water_temperature_setpoint',
      }, { displayName: '冷冻水出水设定温度', unit: 'Cel' }),
      point('p-feedback', 'd-chiller', 'chilled_water_temperature_setpoint', 'TELEMETRY'),
      point('p-old', 'd-old', 'start', 'COMMAND', { capability: 'START' }),
    ],
    relationships: [
      controls('r1', 'p-stop', 'a-chiller'), controls('r2', 'p-start', 'a-chiller'), controls('r3', 'p-setpoint', 'a-chiller'),
      controls('r4', 'p-old', 'a-retired', '2026-10-02T00:00:00.000Z'),
    ],
  };

  const targets = controlTargets(model, now);
  assert.deepEqual(targets.map((target) => target.assetName), ['CHILLER-01']);
  const [chiller] = targets;
  assert.equal(chiller.deviceId, 'd-chiller');
  assert.deepEqual(chiller.actions.map((action) => [action.capability, action.label]), [['START', '启动冷水机'], ['STOP', '停止冷水机']]);
  assert.deepEqual(chiller.settings, [{
    assetId: 'a-chiller', pointId: 'p-setpoint', capability: 'SET_CHILLED_WATER_TEMPERATURE_SETPOINT', label: '冷冻水出水设定温度',
    parameterKey: 'setpointC', unit: '°C', minimum: 5, maximum: 12, step: 0.5, feedbackSourceKey: 'chiller.chilled_water_temperature_setpoint',
  }]);
});

test('the emergency release that drops every output channel is never offered as a device stop', () => {
  const model = {
    assets: [asset('a-loop', '冷冻水总管', 'CHILLED_WATER_LOOP'), asset('a-pump', '1 号冷冻泵', 'CHILLED_WATER_PUMP')],
    telemetryPoints: [
      point('p-emergency', 'd-et1010', 'emergency_stop_all', 'COMMAND', { capability: 'STOP', actuationMode: 'EMERGENCY_DO_RELEASE', feedbackPointKey: 'run_state' }),
      point('p-pump', 'd-et1010', 'chwp_1_stop', 'COMMAND', { capability: 'STOP', actuationMode: 'LEVEL', feedbackPointKey: 'run_state' }),
    ],
    relationships: [controls('r1', 'p-emergency', 'a-loop'), controls('r2', 'p-pump', 'a-pump')],
  };
  assert.deepEqual(controlTargets(model, now).map((target) => [target.assetName, target.actions.map((action) => action.pointId)]), [['1 号冷冻泵', ['p-pump']]]);
});

test('plant assets are offered in the order water flows through the plant', () => {
  const ids = ['a-ct', 'a-cwp', 'a-chiller', 'a-chwp'];
  const model = {
    assets: [asset('a-ct', 'CT-01', 'COOLING_TOWER'), asset('a-cwp', 'CWP-01', 'COOLING_WATER_PUMP'), asset('a-chiller', 'CHILLER-01'), asset('a-chwp', 'CHWP-01', 'CHILLED_WATER_PUMP')],
    telemetryPoints: ids.map((id) => point(`p-${id}`, `d-${id}`, 'start', 'COMMAND', { capability: 'START' })),
    relationships: ids.map((id) => controls(`r-${id}`, `p-${id}`, id)),
  };
  assert.deepEqual(controlTargets(model, now).map((target) => target.assetName), ['CHILLER-01', 'CHWP-01', 'CWP-01', 'CT-01']);
});

test('a chiller and the pumps and tower its start interlocks name form one unit; unnamed assets stay apart', () => {
  const interlocks = [
    { device: 'ctrl-chwp-1', point: 'run_state', expected: 'RUNNING', reason: '1# 冻水泵需先处于运行状态，方可脉冲开启 1# 主机' },
    { device: 'ctrl-tower-1', point: 'run_state', expected: 'RUNNING', reason: '1# 冷却塔风机需先处于运行状态，方可脉冲开启 1# 主机' },
  ];
  const model = {
    assets: [asset('a-chiller', 'A 机组'), asset('a-chwp', '1 号冷冻泵', 'CHILLED_WATER_PUMP'), asset('a-tower', '1 号冷却塔', 'COOLING_TOWER'), asset('a-spare', '5 号冷却塔', 'COOLING_TOWER')],
    telemetryPoints: [
      point('p-start', 'd-chiller', 'start', 'COMMAND', { capability: 'START', interlocks }),
      point('p-seq', 'd-chiller', 'stop_unit_sequence', 'COMMAND', { capability: 'STOP_UNIT_SEQUENCE' }),
      point('p-chwp', 'd-chwp', 'stop', 'COMMAND', { capability: 'STOP', interlocks: [{ device: 'ctrl-chiller-a', expected: 'STOPPED', reason: '1# 主机尚未停机，禁止断开 1# 冻水泵' }] }),
      point('p-tower', 'd-tower', 'stop', 'COMMAND', { capability: 'STOP' }),
      point('p-spare', 'd-spare', 'stop', 'COMMAND', { capability: 'STOP' }),
    ],
    relationships: [controls('r1', 'p-start', 'a-chiller'), controls('r2', 'p-seq', 'a-chiller'), controls('r3', 'p-chwp', 'a-chwp'), controls('r4', 'p-tower', 'a-tower'), controls('r5', 'p-spare', 'a-spare')],
  };
  const devices = [{ id: 'd-chiller', code: 'ctrl-chiller-a' }, { id: 'd-chwp', code: 'ctrl-chwp-1' }, { id: 'd-tower', code: 'ctrl-tower-1' }, { id: 'd-spare', code: 'ctrl-tower-5' }];
  const { units, others } = plantUnits(controlTargets(model, now), devices);
  assert.deepEqual(units.map((unit) => [unit.chiller.assetName, unit.members.map((member) => member.assetName)]), [['A 机组', ['1 号冷冻泵', '1 号冷却塔']]]);
  assert.deepEqual(others.map((target) => target.assetName), ['5 号冷却塔']);
  assert.deepEqual(units[0].chiller.actions.map((action) => action.capability), ['START', 'STOP_UNIT_SEQUENCE']);

  const start = units[0].chiller.actions[0];
  const blocked = blockingInterlocks(start, new Map([['ctrl-chwp-1', 'RUNNING'], ['ctrl-tower-1', 'STOPPED']]));
  assert.deepEqual(blocked.map((interlock) => interlock.deviceCode), ['ctrl-tower-1']);
  // A device whose state is unreadable blocks too: the plant refuses what it cannot verify.
  assert.equal(blockingInterlocks(start, new Map()).length, 2);
});
